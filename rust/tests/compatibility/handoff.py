#!/usr/bin/env python3
"""Prove real SQLite/settings handoff and repeated-save compatibility."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile

from run import PASSWORD, Program, eventually, request, verify_db


def database_rows(path, table):
    with sqlite3.connect(path) as database:
        return database.execute(f'SELECT * FROM "{table}" ORDER BY 1').fetchall()


def copy_directory(source, destination):
    # Call only after the process has stopped; the complete WAL family is kept.
    assert not list(destination.iterdir()), f"copy destination must be empty: {destination}"
    shutil.copytree(source, destination, dirs_exist_ok=True)


def server_handoff(source_binary, destination_binary, directory):
    source = Program(source_binary, "server", directory / "source")
    destination = Program(destination_binary, "server", directory / "destination")
    checks = []
    path = "/api/tickets?z=last&a=two%20words&a=one&b=~%21%2A%28%29&empty="
    ticket = {"prefix": "A &é", "t_id": 4, "first_name": "<Jo>&\u2028\u2029😀",
              "last_name": "Handoff", "phone_number": "444", "pref": "TEXT", "rev": 0}
    try:
        source.start()
        key, _ = request(source.base, "/api/auth", "POST", {"description": "handoff"}, {"TAM-PW": PASSWORD})
        auth = {"TAM-KEY": key["auth_key"]}
        ordered = {**auth, "X-TAM-Client-Name": "handoff-same-client", "X-TAM-Save": "1"}
        request(source.base, "/api/prefixes", "POST", [{"prefix": "A &é", "color": "blue", "weight": 0}], ordered)
        ordered["X-TAM-Save"] = "2"
        accepted, _ = request(source.base, path, "POST", [ticket], ordered)
        exported, _ = request(source.base, "/api/backuprestore", headers=auth)
        source.stop()
        source_db = source.directory / "tam-remote.db"
        with sqlite3.connect(source_db) as database:
            database.execute("CREATE TABLE handoff_unknown (value TEXT)")
            database.execute("INSERT INTO handoff_unknown VALUES ('preserve me')")
        before = database_rows(source_db, "client_saves")
        copy_directory(source.directory, destination.directory)
        destination.start()
        assert request(destination.base, "/api/backuprestore", headers=auth)[0] == exported
        repeated, headers = request(destination.base, path, "POST", [ticket], ordered)
        assert headers.get("x-tam-stale") == "1" and repeated == accepted
        assert database_rows(destination.directory / "tam-remote.db", "client_saves") == before
        assert database_rows(destination.directory / "tam-remote.db", "handoff_unknown") == [("preserve me",)]
        checks.append("copied event, rows, revisions, key, digest and unknown table preserved")
        changed = {**ticket, "first_name": "different content under old number"}
        conflict, headers = request(destination.base, path, "POST", [changed], ordered, 409)
        assert headers.get("x-tam-last-save") == "2" and conflict["last_save"] == 2
        assert request(destination.base, "/api/backuprestore", headers=auth)[0] == exported
        checks.append("same number with different digest rejected without mutation")
        ordered["X-TAM-Save"] = "3"
        request(destination.base, path, "POST", [changed], ordered)
        destination.stop()
        verify_db(source_db)
        verify_db(destination.directory / "tam-remote.db")
        checks.append("new ordered save accepted and copied SQLite integrity clean")
        return checks
    finally:
        source.stop()
        destination.stop()


def client_handoff(server_binary, source_binary, destination_binary, directory):
    server = Program(server_binary, "server", directory / "server")
    source = Program(source_binary, "client", directory / "source")
    destination = Program(destination_binary, "client", directory / "destination")
    checks = []
    ticket = {"prefix": "A", "t_id": 8, "first_name": "Durable",
              "last_name": "Handoff", "phone_number": "888", "pref": "CALL"}
    try:
        server.start()
        source.start()
        request(source.base, "/api/pair", "POST", {
            "host": "127.0.0.1", "port": str(server.port), "tls": False, "password": PASSWORD,
        })
        eventually(lambda: request(source.base, "/api/status")[0].get("state") == "connected")
        settings, _ = request(source.base, "/api/settings")
        auth = {"TAM-KEY": settings["remote_key"]}
        request(source.base, "/api/prefixes", "POST", [{"prefix": "A", "color": "green", "weight": 0}])
        eventually(lambda: request(source.base, "/api/status")[0].get("pending", 1) == 0)
        server.stop(crash=True)
        request(source.base, "/api/tickets", "POST", [ticket])
        assert request(source.base, "/api/status")[0]["pending"] >= 1
        source.stop()
        source_db = source.directory / "tam-local.db"
        queue_before = database_rows(source_db, "outbox")
        order_before = database_rows(source_db, "save_order")
        mirror_before = database_rows(source_db, "mirror")
        assert queue_before and order_before and mirror_before
        copy_directory(source.directory, destination.directory)
        destination.start()
        assert request(destination.base, "/api/settings")[0] == settings
        assert database_rows(destination.directory / "tam-local.db", "outbox") == queue_before
        assert database_rows(destination.directory / "tam-local.db", "save_order") == order_before
        assert database_rows(destination.directory / "tam-local.db", "mirror") == mirror_before
        local, _ = request(destination.base, "/api/tickets/A/8")
        assert local["first_name"] == ticket["first_name"]
        checks.append("copied settings, mirror, client identity, counter, row and FIFO queue preserved")
        server.start()
        eventually(lambda: request(destination.base, "/api/status")[0].get("pending", 1) == 0)
        replayed, _ = request(server.base, "/api/tickets/A/8", headers=auth)
        assert replayed[0]["first_name"] == ticket["first_name"]
        assert not database_rows(destination.directory / "tam-local.db", "outbox")
        assert database_rows(destination.directory / "tam-local.db", "outbox_failed") == []
        checks.append("original numbered save replayed from copied queue without failed residue")
        destination.stop()
        server.stop()
        verify_db(source_db)
        verify_db(destination.directory / "tam-local.db")
        verify_db(server.directory / "tam-remote.db")
        checks.append("all copied client/server databases retain SQLite integrity")
        return checks
    finally:
        source.stop()
        destination.stop()
        server.stop()


def main():
    parser = argparse.ArgumentParser()
    for name in ("go-server", "go-client", "rust-server", "rust-client"):
        parser.add_argument(f"--{name}", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--baseline-only", action="store_true")
    selection.add_argument("--server-only", action="store_true")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="handoff-", dir=args.output.resolve()))
    if args.baseline_only:
        scenarios = [
            ("go-go-server-baseline", server_handoff, args.go_server, args.go_server),
            ("go-go-client-baseline", client_handoff, args.go_server, args.go_client, args.go_client),
        ]
    else:
        scenarios = [
            ("go-to-rust-server", server_handoff, args.go_server, args.rust_server),
            ("rust-to-go-server", server_handoff, args.rust_server, args.go_server),
            ("go-to-rust-client", client_handoff, args.go_server, args.go_client, args.rust_client),
            ("rust-to-go-client", client_handoff, args.rust_server, args.rust_client, args.go_client),
        ]
    if args.server_only:
        scenarios = [scenario for scenario in scenarios if scenario[1] is server_handoff]
    results = {"directory": str(directory), "scenarios": []}
    try:
        for label, exercise, *binaries in scenarios:
            print(f"START {label}", flush=True)
            checks = exercise(*binaries, directory / label)
            results["scenarios"].append({"scenario": label, "checks": checks, "passed": len(checks)})
            print(f"PASS {label}: {len(checks)} checks", flush=True)
    except BaseException as error:
        results["failure"] = f"{type(error).__name__}: {error}"
        raise
    finally:
        (directory / "results.json").write_text(json.dumps(results, indent=2), encoding="utf-8")
        print(f"Evidence retained: {directory}", flush=True)


if __name__ == "__main__":
    main()
