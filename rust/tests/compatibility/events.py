#!/usr/bin/env python3
"""Real-program event transitions, preservation failures and queue safety.

Run in Linux WSL with an output directory in Linux home/cache: permission
denial must be real, not an ignored chmod on drvfs. Every run retains evidence.
--rust-safety additionally checks Rust's intentional pending-restore safeguard.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import stat
import tempfile
import time
import traceback
import urllib.parse

from run import PASSWORD, Program, eventually, request, verify_db


QUEUE_COLUMNS = "id, created_at, method, path, body, client, save_number"


def json_value(value):
    if isinstance(value, bytes):
        return {"bytes_hex": value.hex()}
    return value


def snapshot(client):
    """One read transaction; retry diagnostics are excluded, intent is not."""
    with sqlite3.connect(client.directory / "tam-local.db") as database:
        database.execute("BEGIN")
        result = {}
        for table in ("prefixes", "tickets", "baskets", "mirror", "save_order"):
            result[table] = [[json_value(v) for v in row] for row in
                             database.execute(f"SELECT * FROM {table} ORDER BY 1, 2")]
        for table in ("outbox", "outbox_failed"):
            result[table] = [[json_value(v) for v in row] for row in database.execute(
                f"SELECT {QUEUE_COLUMNS} FROM {table} ORDER BY id")]
        return result


def backup(client):
    return request(client.base, "/api/backuprestore/local")[0]


def canonical(document):
    return {**document, **{name: sorted(document[name], key=lambda row: (
        row["prefix"], row.get("t_id", row.get("b_id", 0))))
        for name in ("prefixes", "tickets", "baskets")}}


def pair(client, server, expected=200):
    return request(client.base, "/api/pair", "POST", {
        "host": "127.0.0.1", "port": str(server.port),
        "tls": server.tls, "password": PASSWORD,
    }, expected=expected)[0]


def settings(client):
    return request(client.base, "/api/settings")[0]


def auth(client):
    return {"TAM-KEY": settings(client)["remote_key"]}


def ticket(number, name, prefix="A"):
    return {"prefix": prefix, "t_id": number, "first_name": name,
            "last_name": "Retained", "phone_number": str(number), "pref": "TEXT"}


def local_copy(client, event, expected):
    actual = backup(client)
    assert actual.get("event", "") == event, actual
    assert canonical(actual) == canonical(expected), (actual, expected)
    return True


def server_backup(server, headers):
    return request(server.base, "/api/backuprestore", headers=headers)[0]


def queued(client, number, name, prefix="A"):
    request(client.base, "/api/tickets", "POST", [ticket(number, name, prefix)])
    data = snapshot(client)
    assert len(data["outbox"]) >= 1, data
    assert any(row[1] == number and row[2] == name for row in data["tickets"]), data
    return data


def archive_files(client):
    return set(client.directory.glob("before-*.json"))


def check_archive(client, before, expected):
    paths = archive_files(client) - before
    assert len(paths) == 1, [str(path) for path in paths]
    path = paths.pop()
    assert canonical(json.loads(path.read_text(encoding="utf-8"))) == canonical(expected), path
    assert stat.S_IMODE(path.stat().st_mode) & 0o077 == 0, (path, oct(path.stat().st_mode))
    return path


@contextmanager
def unwritable_directory(directory):
    """Deny archive creation only; existing DB/WAL files remain writable."""
    assert os.name == "posix" and os.geteuid() != 0, "run as an unprivileged Linux user"
    previous = stat.S_IMODE(directory.stat().st_mode)
    probe = directory / ".archive-permission-probe"
    try:
        directory.chmod(0o555)
        try:
            with probe.open("xb"):
                pass
        except PermissionError:
            pass
        else:
            probe.unlink()
            raise AssertionError("chmod did not deny new files; use a Linux home/cache output directory")
        yield
    finally:
        directory.chmod(previous)


def unchanged(client, rows, document, configuration, settings_bytes):
    assert snapshot(client) == rows, (snapshot(client), rows)
    assert canonical(backup(client)) == canonical(document)
    assert settings(client) == configuration
    assert (client.directory / "settings.json").read_bytes() == settings_bytes


def exercise(server_binary, client_binary, directory, results, rust_safety=False, tls=False):
    first = Program(server_binary, "server", directory / "server-a", tls)
    second = Program(server_binary, "server", directory / "server-b", tls)
    client = Program(client_binary, "client", directory / "client")
    programs = (first, second, client)

    def begin(name):
        results["active_check"] = name

    def passed(name, **evidence):
        results["checks"].append({"name": name, "passed": True, **evidence})
        results.pop("active_check", None)
        print(f"PASS {name}", flush=True)

    try:
        begin("independent real servers have distinct events")
        first.start()
        second.start()
        client.start()
        event_a = request(first.base, "/api")[0]["event"]
        event_b = request(second.base, "/api")[0]["event"]
        assert event_a and event_b and event_a != event_b
        passed("independent real servers have distinct events", event_a=event_a, event_b=event_b)

        begin("same-event repair retains and replays numbered queued save")
        pair(client, first)
        eventually(lambda: backup(client).get("event") == event_a)
        request(client.base, "/api/prefixes", "POST", [{"prefix": "A", "color": "blue", "weight": 1}])
        request(client.base, "/api/tickets", "POST", [ticket(1, "Original A")])
        request(client.base, "/api/baskets", "POST", [{"prefix": "A", "b_id": 1,
                "description": "Original basket", "donors": "Original donors", "winning_ticket": 0}])
        request(client.base, "/api/drawing", "POST", [{"prefix": "A", "b_id": 1, "winning_ticket": 1}])
        expected_a = server_backup(first, auth(client))
        eventually(lambda: local_copy(client, event_a, expected_a))

        # A revoked key leaves saves offline even while the server is available.
        old_key = settings(client)["remote_key"]
        request(first.base, "/api/auth?key_to_del=" + urllib.parse.quote(old_key), "DELETE",
                headers={"TAM-PW": PASSWORD})
        repair_queue = queued(client, 7, "Repair retained")
        archives = archive_files(client)
        eventually(lambda: request(client.base, "/api/status")[0].get("state") == "unauthenticated")
        pair(client, first)
        eventually(lambda: not snapshot(client)["outbox"])
        assert snapshot(client)["outbox_failed"] == []
        assert archive_files(client) == archives
        assert settings(client)["remote_key"] != old_key
        assert request(first.base, "/api/tickets/A/7", headers=auth(client))[0][0]["first_name"] == "Repair retained"
        expected_a = server_backup(first, auth(client))
        eventually(lambda: local_copy(client, event_a, expected_a))
        passed("same-event repair retains and replays numbered queued save", queue=repair_queue["outbox"])

        if tls:
            begin("certificate rotation retains pin and queued save until same-event re-pair")
            first.stop()
            tls_queue = queued(client, 8, "Pinned queued")
            original_settings = settings(client)
            retained = directory / "certificate-before-rotation"
            retained.mkdir()
            for name in ("server.crt", "server.key"):
                source = first.directory / name
                saved = retained / name
                saved.write_bytes(source.read_bytes())
                saved.chmod(0o600)
                source.unlink()
            first.start()
            assert request(first.base, "/api")[0]["event"] == event_a
            eventually(lambda: request(client.base, "/api/status")[0].get("state") == "certificate")
            assert snapshot(client) == tls_queue
            assert settings(client) == original_settings
            assert request(first.base, "/api/tickets/A/8", headers=auth(client))[0] == []
            pair(client, first)
            assert settings(client)["remote_fingerprint"] != original_settings["remote_fingerprint"]
            eventually(lambda: not snapshot(client)["outbox"])
            assert snapshot(client)["outbox_failed"] == []
            assert archive_files(client) == archives
            assert request(first.base, "/api/tickets/A/8", headers=auth(client))[0][0]["first_name"] == "Pinned queued"
            expected_a = server_backup(first, auth(client))
            eventually(lambda: local_copy(client, event_a, expected_a))
            passed("certificate rotation retains pin and queued save until same-event re-pair")

        begin("failed pairing archive preserves rows event pending identities settings and remote keys")
        first.stop()
        pair_queue = queued(client, 9, "Pair old event only")
        old_copy = backup(client)
        config_before = settings(client)
        settings_bytes = (client.directory / "settings.json").read_bytes()
        archives = archive_files(client)
        keys_before = request(second.base, "/api/auth", headers={"TAM-PW": PASSWORD})[0]
        with unwritable_directory(client.directory):
            failure = pair(client, second, 500)
            assert "detail" in failure
            unchanged(client, pair_queue, old_copy, config_before, settings_bytes)
            assert archive_files(client) == archives
            assert request(second.base, "/api/auth", headers={"TAM-PW": PASSWORD})[0] == keys_before
        passed("failed pairing archive preserves rows event pending identities settings and remote keys", response=failure)

        # Rust deliberately refuses either restore while intent is still pending.
        begin("pending saves block selective push without mutation")
        for target in ("prefixes", "tickets", "baskets"):
            request(client.base, f"/api/backuprestore/push/{target}", "POST", {}, expected=409)
        unchanged(client, pair_queue, old_copy, config_before, settings_bytes)
        passed("pending saves block selective push without mutation")
        if rust_safety:
            begin("Rust pending saves block both explicit restores without mutation")
            for location in ("local", "remote"):
                request(client.base, f"/api/backuprestore/{location}", "POST", expected_a, expected=409)
                unchanged(client, pair_queue, old_copy, config_before, settings_bytes)
            passed("Rust pending saves block both explicit restores without mutation")
        else:
            results["checks"].append({"name": "Rust pending saves block both explicit restores without mutation",
                                      "passed": None, "skipped": "Go intentionally allows restore with pending saves"})

        # Populate B before switching so a missing clear is visible in SQLite.
        begin("new-event pair archives old copy before clear and quarantines rather than sends old saves")
        key_b = request(second.base, "/api/auth", "POST", {"description": "probe"}, {"TAM-PW": PASSWORD})[0]["auth_key"]
        headers_b = {"TAM-KEY": key_b}
        seed_headers = {**headers_b, "X-TAM-Client-Name": "event-probe-seed", "X-TAM-Save": "1"}
        request(second.base, "/api/prefixes", "POST", [{"prefix": "B", "color": "red", "weight": 2}], seed_headers)
        request(second.base, "/api/tickets", "POST", [ticket(1, "Original B", "B")],
                {**seed_headers, "X-TAM-Save": "2"})
        expected_b = server_backup(second, headers_b)
        pair(client, second)
        eventually(lambda: local_copy(client, event_b, expected_b))
        archived_a = check_archive(client, archives, old_copy)
        digest_a = hashlib.sha256(archived_a.read_bytes()).hexdigest()
        switched = snapshot(client)
        assert switched["outbox"] == [] and switched["outbox_failed"] == pair_queue["outbox"]
        assert server_backup(second, headers_b) == expected_b
        passed("new-event pair archives old copy before clear and quarantines rather than sends old saves",
               archive=str(archived_a), sha256=digest_a, failed=switched["outbox_failed"])

        # Restore the OTHER event on the same server while no client can replay.
        begin("failed heartbeat archive preserves old event rows pending identities and settings")
        second.stop()
        heartbeat_queue = queued(client, 10, "Heartbeat old event only", "B")
        heartbeat_copy = backup(client)
        client.stop()
        second.start()
        request(second.base, "/api/backuprestore", "POST", expected_a, headers_b)
        restored = server_backup(second, headers_b)
        assert restored["event"] == event_a
        second.stop()
        client.start()
        heartbeat_queue = snapshot(client)
        heartbeat_settings = settings(client)
        heartbeat_bytes = (client.directory / "settings.json").read_bytes()
        archives = archive_files(client)
        log_offset = (client.directory / "console.log").stat().st_size
        with unwritable_directory(client.directory):
            second.start()
            # Witness an actual heartbeat preservation failure before assertions.
            eventually(lambda: "permission denied" in (client.directory / "console.log").read_bytes()[log_offset:].decode(errors="replace").lower(), 25)
            unchanged(client, heartbeat_queue, heartbeat_copy, heartbeat_settings, heartbeat_bytes)
            assert archive_files(client) == archives
            assert server_backup(second, headers_b) == restored
        passed("failed heartbeat archive preserves old event rows pending identities and settings")
        begin("ordinary remote restore triggers heartbeat archive quarantine and fresh event pull")
        eventually(lambda: local_copy(client, event_a, restored))
        archived_b = check_archive(client, archives, heartbeat_copy)
        transitioned = snapshot(client)
        assert transitioned["outbox"] == []
        assert transitioned["outbox_failed"] == heartbeat_queue["outbox_failed"] + heartbeat_queue["outbox"]
        assert server_backup(second, headers_b) == restored
        assert hashlib.sha256(archived_a.read_bytes()).hexdigest() == digest_a
        passed("ordinary remote restore triggers heartbeat archive quarantine and fresh event pull",
               archive=str(archived_b), earlier_archive_unchanged=True)

        # Unpair without a password retains its key; with a password revokes it.
        begin("unpair without password retains local rows failed saves and remote key")
        rows_before = snapshot(client)
        document_before = backup(client)
        retained_key = settings(client)["remote_key"]
        request(client.base, "/api/unpair", "POST", {})
        assert request(client.base, "/api/status")[0]["mode"] == "standalone"
        assert canonical(backup(client)) == canonical(document_before)
        assert snapshot(client) == rows_before
        assert not settings(client)["remote_server"] and not settings(client)["remote_key"]
        request(second.base, "/api/tickets", headers={"TAM-KEY": retained_key})
        passed("unpair without password retains local rows failed saves and remote key")

        # Pairing again to the same event from standalone may preserve an archive;
        # Go's second-based exclusive filenames require a distinct wall second.
        begin("unpair quarantines pending save without losing accepted local rows")
        archives = archive_files(client)
        if not rust_safety:
            stamp = time.strftime("%Y%m%d-%H%M%S")
            eventually(lambda: time.strftime("%Y%m%d-%H%M%S") != stamp, 2)
        pair(client, second)
        eventually(lambda: local_copy(client, event_a, restored))
        check_archive(client, archives, document_before)
        assert hashlib.sha256(archived_a.read_bytes()).hexdigest() == digest_a
        second.stop()
        unpair_queue = queued(client, 11, "Unpair retained", "B")
        unpair_copy = backup(client)
        revoke_key = settings(client)["remote_key"]
        client.stop()
        second.start()
        # Refuse replay on restart so unpair handles the still-pending intent.
        request(second.base, "/api/auth?key_to_del=" + urllib.parse.quote(revoke_key), "DELETE", headers={"TAM-PW": PASSWORD})
        client.start()
        request(client.base, "/api/unpair", "POST", {})
        assert canonical(backup(client)) == canonical(unpair_copy)
        state = snapshot(client)
        assert not state["outbox"]
        assert state["outbox_failed"] == unpair_queue["outbox_failed"] + unpair_queue["outbox"]
        passed("unpair quarantines pending save without losing accepted local rows")
        begin("unpair with password revokes remote key and retains local copy")
        archives = archive_files(client)
        existing_archives = {path: hashlib.sha256(path.read_bytes()).hexdigest() for path in archives}
        if not rust_safety:
            stamp = time.strftime("%Y%m%d-%H%M%S")
            eventually(lambda: time.strftime("%Y%m%d-%H%M%S") != stamp, 2)
        pair(client, second)
        eventually(lambda: local_copy(client, event_a, restored))
        check_archive(client, archives, unpair_copy)
        assert all(hashlib.sha256(path.read_bytes()).hexdigest() == digest
                   for path, digest in existing_archives.items())
        revoke_key = settings(client)["remote_key"]
        retained_rows = snapshot(client)
        request(client.base, "/api/unpair", "POST", {"password": PASSWORD})
        assert snapshot(client) == retained_rows
        assert canonical(backup(client)) == canonical(restored)
        request(second.base, "/api/tickets", headers={"TAM-KEY": revoke_key}, expected=401)
        passed("unpair with password revokes remote key and retains local copy")
    finally:
        failures = []
        for program in reversed(programs):
            try:
                program.stop()
            except BaseException as error:
                failures.append(f"{program.role}: {error}")
        for program in programs:
            path = program.directory / ("tam-local.db" if program.role == "client" else "tam-remote.db")
            if path.exists():
                try:
                    verify_db(path)
                except BaseException as error:
                    failures.append(f"integrity {path}: {error}")
        if failures:
            results["cleanup_failure"] = failures
            raise AssertionError(failures)
    begin("all processes drained and all three SQLite databases intact")
    passed("all processes drained and all three SQLite databases intact")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server", required=True, type=Path)
    parser.add_argument("--client", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--rust-safety", action="store_true")
    parser.add_argument("--tls", action="store_true")
    args = parser.parse_args()
    assert os.name == "posix", "network probes run only in Linux/WSL"
    args.output.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="events-", dir=args.output.resolve()))
    results = {"server": str(args.server.resolve()), "client": str(args.client.resolve()),
               "directory": str(directory), "rust_safety": args.rust_safety, "tls": args.tls, "checks": []}
    try:
        exercise(args.server, args.client, directory, results, args.rust_safety, args.tls)
        results["passed"] = True
    except BaseException as error:
        results["passed"] = False
        results["failure"] = f"{type(error).__name__}: {error}"
        results["checks"].append({"name": results.pop("active_check", "program cleanup and database integrity"),
                                  "passed": False, "failure": results["failure"]})
        (directory / "failure.txt").write_text(traceback.format_exc(), encoding="utf-8")
        raise
    finally:
        (directory / "results.json").write_text(json.dumps(results, indent=2), encoding="utf-8")
        print(f"Evidence retained: {directory}", flush=True)


if __name__ == "__main__":
    main()
