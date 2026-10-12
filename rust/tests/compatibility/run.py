#!/usr/bin/env python3
"""Exercise actual Go/Rust TAM pairs using disposable local event directories."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import socket
import sqlite3
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


PASSWORD = "r-tam-local-compatibility-only"


def request(base, path, method="GET", body=None, headers=None, expected=200):
    payload = None if body is None else json.dumps(body, ensure_ascii=False).encode()
    fields = {"Accept": "application/json", **(headers or {})}
    if payload is not None:
        fields["Content-Type"] = "application/json"
    req = urllib.request.Request(base + path, data=payload, headers=fields, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=8, context=ssl._create_unverified_context())
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        status = response.status
        answer = json.loads(raw)
        received = {name.lower(): value for name, value in response.headers.items()}
    assert status == expected, (method, path, status, expected, answer)
    return answer, received


def eventually(check, seconds=45):
    deadline = time.monotonic() + seconds
    last = None
    while time.monotonic() < deadline:
        try:
            value = check()
            if value:
                return value
        except (OSError, ValueError, AssertionError) as error:
            last = error
        time.sleep(0.1)
    raise AssertionError(f"condition not met within {seconds}s: {last}")


class Program:
    def __init__(self, binary, role, directory, tls=False,
                 listen_host="127.0.0.1", announce=False, environment=None):
        self.binary = Path(binary).resolve()
        assert self.binary.is_file(), f"missing {role} binary: {self.binary}"
        self.role = role
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            self.port = reservation.getsockname()[1]
        self.tls = tls
        self.listen_host = listen_host
        self.announce = announce
        self.environment = environment or {}
        scheme = "https" if tls else "http"
        self.base = f"{scheme}://127.0.0.1:{self.port}"
        self.process = None
        self.log = None

    def start(self):
        args = [str(self.binary), "-addr", f"{self.listen_host}:{self.port}", "-tray=false"]
        args += [f"-announce={str(self.announce).lower()}"] if self.role == "server" else ["-open=false"]
        if self.tls:
            args.append("-tls")
        self.log = open(self.directory / "console.log", "ab", buffering=0)
        env = {**os.environ, **self.environment,
               "TAM_DATA_DIR": str(self.directory), "TAM_PWD": PASSWORD}
        self.process = subprocess.Popen(args, cwd=self.directory, env=env,
                                        stdout=self.log, stderr=subprocess.STDOUT)

        def ready():
            assert self.process.poll() is None, (self.role, "startup exit", self.directory)
            root, _ = request(self.base, "/api")
            if self.role == "server":
                assert root["whoami"] == "TAM Server", root
            else:
                # A paired Go client proxies the server identity at this route.
                assert root["whoami"] in ("TAM Client", "TAM Server"), root
            return root

        eventually(ready, 20)
        return self

    def stop(self, crash=False):
        if self.process is not None and self.process.poll() is None:
            self.process.kill() if crash else self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
                raise AssertionError(f"{self.role} failed to drain: {self.directory}")
        if self.log is not None:
            self.log.close()
            self.log = None


def verify_db(path):
    with sqlite3.connect(path) as database:
        assert database.execute("PRAGMA integrity_check").fetchall() == [("ok",)], path
        assert database.execute("PRAGMA foreign_key_check").fetchall() == [], path


def exercise_pair(server_binary, client_binary, directory, tls=False):
    prefix = "A &é"
    escaped = urllib.parse.quote(prefix, safe="")
    server = Program(server_binary, "server", directory / "server", tls)
    client = Program(client_binary, "client", directory / "client")
    checks = []
    try:
        server.start()
        client.start()
        settings, _ = request(client.base, "/api/settings")
        assert settings["remote_port"] == "8000" and settings["default_pref"] == "CALL"
        request(client.base, "/api/pair", "POST", {
            "host": "127.0.0.1", "port": str(server.port), "tls": tls, "password": PASSWORD,
        })
        settings, _ = request(client.base, "/api/settings")
        key = settings["remote_key"]
        assert key
        if tls:
            assert len(settings["remote_fingerprint"]) == 64
        auth = {"TAM-KEY": key}
        eventually(lambda: request(client.base, "/api/status")[0].get("state") == "connected")
        checks.append("pair and Go protocol identities")

        request(client.base, "/api/prefixes", "POST", [
            {"prefix": prefix, "color": "blue", "weight": 0},
            {"prefix": "Total", "color": "white", "weight": 1},
        ])
        tickets = [
            {"prefix": prefix, "t_id": 0, "first_name": "Zero", "last_name": "Buyer", "phone_number": "0", "pref": "CALL"},
            {"prefix": prefix, "t_id": 1, "first_name": "Jo", "last_name": "Ann", "phone_number": "1", "pref": "CALL"},
            {"prefix": prefix, "t_id": 2, "first_name": "Joa", "last_name": "Nn", "phone_number": "1", "pref": "TEXT"},
            {"prefix": "Total", "t_id": 1, "first_name": "Other", "last_name": "Buyer", "phone_number": "9", "pref": "CALL"},
        ]
        request(client.base, "/api/tickets", "POST", tickets)
        request(client.base, "/api/baskets", "POST", [
            {"prefix": prefix, "b_id": 1, "description": "Basket <one>", "donors": "A & B", "winning_ticket": 0},
            {"prefix": prefix, "b_id": 2, "description": "Undrawn", "donors": "C", "winning_ticket": 0},
        ])
        request(client.base, "/api/drawing", "POST", [{"prefix": prefix, "b_id": 1, "winning_ticket": 1}])
        direct, _ = request(server.base, f"/api/tickets/{escaped}/1", headers=auth)
        assert isinstance(direct, list) and len(direct) == 1 and direct[0]["first_name"] == "Jo"
        local, _ = request(client.base, f"/api/tickets/{escaped}/1")
        assert isinstance(local, dict) and local["first_name"] == "Jo"
        rows, _ = request(client.base, f"/api/tickets/{escaped}/3/1")
        assert [row["t_id"] for row in rows] == [1, 2, 3] and rows[-1]["pref"] == "CALL"
        checks.append("forms, direct arrays and client placeholders")

        by_basket, _ = request(client.base, f"/api/reports/bybasket/{escaped}")
        assert len(by_basket) == 2 and by_basket[0]["first_name"] == "Jo"
        assert by_basket[1]["winning_ticket"] == 0 and by_basket[1]["first_name"] == ""
        by_name, _ = request(client.base, f"/api/reports/byname/{escaped}")
        assert sorted(row["b_id"] for row in by_name) == [1, 2]
        counts, _ = request(client.base, "/api/reports/counts")
        total = [row for row in counts if row["is_total"]]
        named_total = [row for row in counts if row["prefix"] == "Total" and not row["is_total"]]
        assert len(total) == len(named_total) == 1 and total[0]["unique_buyers"] == 4
        found, _ = request(client.base, "/api/search/tickets?first_name=jo")
        assert {row["t_id"] for row in found} == {1, 2}
        checks.append("reports, ticket-zero exclusion, tuple buyers and search")

        original, _ = request(client.base, f"/api/tickets/{escaped}/1")
        base = {field: original[field] for field in ("first_name", "last_name", "phone_number", "pref")}
        order_headers = {**auth, "X-TAM-Client-Name": "compat-other-client", "X-TAM-Save": "1"}
        request(server.base, "/api/tickets", "POST", [{**original, "phone_number": "222", "base": base}], order_headers)
        accepted, _ = request(client.base, "/api/tickets", "POST", [
            {**original, "first_name": "Corrected", "phone_number": "333", "base": base},
        ])
        assert accepted[0]["first_name"] == "Corrected" and accepted[0]["phone_number"] == "222"
        checks.append("stale field edit keeps another client's newer value")

        backup, _ = request(client.base, "/api/backuprestore/remote")
        assert backup["event"] and len(backup["tickets"]) == 4
        local_backup, _ = request(client.base, "/api/backuprestore/local")
        assert all(name in local_backup for name in ("prefixes", "baskets", "tickets"))
        (directory / "server-backup.json").write_text(json.dumps(backup, ensure_ascii=False), encoding="utf-8")
        rejected, _ = request(server.base, "/api/backuprestore", "POST",
                              {"prefixes": [], "baskets": [], "tickets": [{"prefix": prefix, "t_id": -1}]}, auth, 400)
        assert "detail" in rejected
        after, _ = request(server.base, "/api/backuprestore", headers=auth)
        assert after == backup
        request(client.base, "/api/backuprestore/remote", "POST", backup)
        request(client.base, "/api/backuprestore/push/tickets", "POST", {})
        checks.append("backups, explicit restore, newer-only push and invalid restore rollback")

        eventually(lambda: request(client.base, "/api/status")[0].get("pending", 0) == 0)
        server.stop(crash=True)
        offline_ticket = {"prefix": prefix, "t_id": 7, "first_name": "Offline", "last_name": "Survivor", "phone_number": "7", "pref": "TEXT"}
        request(client.base, "/api/tickets", "POST", [offline_ticket])
        status, _ = request(client.base, "/api/status")
        assert status["pending"] >= 1
        offline_report, headers = request(client.base, f"/api/reports/bybasket/{escaped}")
        assert headers.get("x-tam-copy") == "1" and len(offline_report) == 2
        client.stop(crash=True)
        verify_db(client.directory / "tam-local.db")
        with sqlite3.connect(client.directory / "tam-local.db") as database:
            assert database.execute("SELECT COUNT(*) FROM outbox").fetchone()[0] >= 1
            assert database.execute("SELECT first_name FROM tickets WHERE prefix=? AND t_id=7", (prefix,)).fetchone() == ("Offline",)
        server.start()
        client.start()
        eventually(lambda: request(client.base, "/api/status")[0].get("pending", 1) == 0)
        replayed, _ = request(server.base, f"/api/tickets/{escaped}/7", headers=auth)
        assert replayed[0]["first_name"] == "Offline"
        checks.append("offline queue, marked copy report, process crash and durable replay")

        # Definitive validation failures must neither mutate rows nor create a replay.
        pending_before = request(client.base, "/api/status")[0]["pending"]
        request(client.base, "/api/tickets", "POST", [{"prefix": prefix, "t_id": -1}], expected=400)
        assert request(client.base, "/api/status")[0]["pending"] == pending_before
        checks.append("definitive invalid write stays unaccepted")

        client.stop()
        server.stop()
        verify_db(client.directory / "tam-local.db")
        verify_db(server.directory / "tam-remote.db")
        checks.append("clean shutdown and SQLite integrity")
        return checks
    finally:
        client.stop()
        server.stop()


def main():
    parser = argparse.ArgumentParser()
    for name in ("go-server", "go-client", "rust-server", "rust-client"):
        parser.add_argument(f"--{name}", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--tls", action="store_true")
    parser.add_argument("--pair", action="append", choices=[
        "go-go-baseline", "rust-rust", "rust-server-go-client", "go-server-rust-client",
    ], help="Run selected pairs; defaults to all four.")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="mixed-", dir=args.output.resolve()))
    pairs = [
        ("go-go-baseline", args.go_server, args.go_client),
        ("rust-rust", args.rust_server, args.rust_client),
        ("rust-server-go-client", args.rust_server, args.go_client),
        ("go-server-rust-client", args.go_server, args.rust_client),
    ]
    if args.pair:
        pairs = [pair for pair in pairs if pair[0] in args.pair]
    for _, server, client in pairs:
        for binary in (server, client):
            assert binary.is_file(), f"missing real binary: {binary}"
    results = {"tls": args.tls, "directory": str(directory), "pairs": []}
    try:
        for label, server, client in pairs:
            print(f"START {label}", flush=True)
            checks = exercise_pair(server, client, directory / label, args.tls)
            results["pairs"].append({"pair": label, "checks": checks, "passed": len(checks)})
            print(f"PASS {label}: {len(checks)} checks", flush=True)
    except BaseException as error:
        results["failure"] = f"{type(error).__name__}: {error}"
        raise
    finally:
        (directory / "results.json").write_text(json.dumps(results, indent=2), encoding="utf-8")
        print(f"Evidence retained: {directory}", flush=True)


if __name__ == "__main__":
    main()
