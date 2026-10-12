#!/usr/bin/env python3
"""Inspect durable Rust intent state at real HTTP acceptance/ack boundaries."""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import http.server
import json
from pathlib import Path
import sqlite3
import tempfile
import threading
import urllib.error
import urllib.request

from run import PASSWORD, Program, eventually, request, verify_db


class Relay:
    def __init__(self, upstream):
        self.upstream = upstream
        self.mode = "normal"
        self.accepted = threading.Event()
        self.release_ack = threading.Event()
        self.error = None
        relay = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                self.forward()

            def do_POST(self):
                self.forward()

            def do_DELETE(self):
                self.forward()

            def respond(self, status, body, headers=None):
                self.send_response(status)
                for name, value in (headers or {}).items():
                    if name.lower() not in ("connection", "content-length", "transfer-encoding"):
                        self.send_header(name, value)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                try:
                    self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def forward(self):
                payload = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                selected = self.command == "POST" and self.path.split("?")[0] == "/api/tickets"
                mode = relay.mode if selected else "normal"
                if mode == "reject":
                    self.respond(400, b'{"detail":"definitive test rejection"}', {"Content-Type": "application/json"})
                    return
                headers = {name: value for name, value in self.headers.items()
                           if name.lower() not in ("host", "connection", "content-length")}
                outgoing = urllib.request.Request(relay.upstream + self.path, method=self.command,
                                                  data=payload if self.command != "GET" else None,
                                                  headers=headers)
                try:
                    try:
                        response = urllib.request.urlopen(outgoing, timeout=8)
                    except urllib.error.HTTPError as error:
                        response = error
                    with response:
                        body = response.read()
                        status = response.status
                        fields = dict(response.headers.items())
                    if mode == "hold_ack" and status == 200:
                        relay.accepted.set()
                        if not relay.release_ack.wait(15):
                            relay.error = "ack hold was not released by the test"
                            return
                    if mode == "html" and status == 200:
                        body = b"<!doctype html><title>Wi-Fi login</title>"
                        fields["Content-Type"] = "text/html"
                    elif mode == "wrong_identity" and status == 200:
                        rows = json.loads(body)
                        rows[0]["t_id"] += 1
                        body = json.dumps(rows).encode()
                    self.respond(status, body, fields)
                except Exception as error:
                    relay.error = f"{type(error).__name__}: {error}"
                    self.respond(502, b'{"detail":"test relay upstream failure"}', {"Content-Type": "application/json"})

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.port = self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def start(self):
        self.thread.start()
        return self

    def stop(self):
        self.release_ack.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
        assert not self.thread.is_alive()


def rows(path, sql, parameters=()):
    with sqlite3.connect(path) as database:
        return database.execute(sql, parameters).fetchall()


def exercise(server_binary, client_binary, directory):
    server = Program(server_binary, "server", directory / "server")
    client = Program(client_binary, "client", directory / "client")
    relay = None
    checks = []
    db = client.directory / "tam-local.db"
    ticket = {"prefix": "A", "t_id": 30, "first_name": "Accepted before ack",
              "last_name": "Survives kill", "phone_number": "30", "pref": "TEXT"}
    try:
        server.start()
        relay = Relay(server.base).start()
        client.start()
        request(client.base, "/api/pair", "POST", {
            "host": "127.0.0.1", "port": str(relay.port), "tls": False, "password": PASSWORD,
        })
        eventually(lambda: request(client.base, "/api/status")[0].get("state") == "connected")
        settings, _ = request(client.base, "/api/settings")
        auth = {"TAM-KEY": settings["remote_key"]}
        request(client.base, "/api/prefixes", "POST", [{"prefix": "A", "color": "blue", "weight": 0}])
        eventually(lambda: request(client.base, "/api/status")[0].get("pending", 1) == 0)

        relay.mode = "reject"
        rejected = {**ticket, "t_id": 29, "first_name": "Never accepted"}
        request(client.base, "/api/tickets", "POST", [rejected], expected=400)
        assert rows(db, "SELECT * FROM tickets WHERE prefix='A' AND t_id=29") == []
        assert rows(db, "SELECT COUNT(*) FROM outbox") == [(0,)]
        assert request(server.base, "/api/tickets/A/29", headers=auth)[0] == []
        checks.append("definitive online refusal leaves no accepted row or replayable intent")

        relay.mode = "hold_ack"
        with ThreadPoolExecutor(max_workers=1) as executor:
            pending_call = executor.submit(request, client.base, "/api/tickets", "POST", [ticket])
            assert relay.accepted.wait(5), "server acceptance not reached"
            accepted, _ = request(server.base, "/api/tickets/A/30", headers=auth)
            assert accepted[0]["first_name"] == ticket["first_name"]
            server_revision = accepted[0]["rev"]
            intents = rows(db, "SELECT method,path,body,client,save_number FROM outbox ORDER BY id")
            boundary = {
                "server_accepted": accepted,
                "client_intents": [list(item[:2]) + [json.loads(item[2])] + list(item[3:]) for item in intents],
                "client_rows": rows(db, "SELECT prefix,t_id,first_name FROM tickets WHERE prefix='A' AND t_id=30"),
            }
            (directory / "ack-boundary.json").write_text(json.dumps(boundary, indent=2), encoding="utf-8")
            assert len(intents) == 1 and intents[0][0:2] == ("POST", "/api/tickets"), boundary
            assert intents[0][3] and intents[0][4] > 0
            assert json.loads(intents[0][2])[0]["first_name"] == ticket["first_name"]
            assert rows(db, "SELECT * FROM tickets WHERE prefix='A' AND t_id=30") == []
            client.stop(crash=True)
            relay.mode = "normal"
            relay.release_ack.set()
            try:
                pending_call.result(timeout=10)
            except (OSError, urllib.error.URLError):
                pass
            else:
                raise AssertionError("killed client unexpectedly acknowledged the held request")
        verify_db(db)
        client.start()
        eventually(lambda: request(client.base, "/api/status")[0].get("pending", 1) == 0)
        replayed, _ = request(server.base, "/api/tickets/A/30", headers=auth)
        assert replayed[0]["first_name"] == ticket["first_name"] and replayed[0]["rev"] == server_revision
        assert rows(db, "SELECT first_name FROM tickets WHERE prefix='A' AND t_id=30") == [(ticket["first_name"],)]
        assert rows(db, "SELECT COUNT(*) FROM outbox_failed") == [(0,)]
        checks.append("intent is durable before acknowledgment; kill/restart repeats accepted save without changing revision")

        for mode, identity in (("html", 31), ("wrong_identity", 32)):
            relay.mode = mode
            offered = {**ticket, "t_id": identity, "first_name": mode}
            # Unverifiable success is uncertain; it must retain a pending save.
            try:
                request(client.base, "/api/tickets", "POST", [offered])
            except AssertionError:
                # Implementations can report uncertainty as a non-200 response.
                pass
            assert rows(db, "SELECT COUNT(*) FROM outbox")[0][0] >= 1, mode
            assert request(server.base, f"/api/tickets/A/{identity}", headers=auth)[0][0]["first_name"] == mode
            relay.mode = "normal"
            eventually(lambda: request(client.base, "/api/status")[0].get("pending", 1) == 0)
            assert rows(db, "SELECT first_name FROM tickets WHERE prefix='A' AND t_id=?", (identity,)) == [(mode,)]
            checks.append(f"{mode} acknowledgment never discards the durable save; valid repeat reconciles it")

        assert relay.error is None, relay.error
        client.stop()
        server.stop()
        verify_db(db)
        verify_db(server.directory / "tam-remote.db")
        checks.append("fault-boundary databases pass integrity checks after clean drain")
        return checks
    finally:
        client.stop()
        if relay is not None:
            relay.stop()
        server.stop()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--server", required=True, type=Path)
    parser.add_argument("--rust-client", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="faults-", dir=args.output.resolve()))
    results = {"directory": str(directory), "server": str(args.server), "client": str(args.rust_client)}
    try:
        checks = exercise(args.server, args.rust_client, directory)
        results.update(checks=checks, passed=len(checks))
        print(f"PASS durable intent boundaries: {len(checks)} checks", flush=True)
    except BaseException as error:
        results["failure"] = f"{type(error).__name__}: {error}"
        raise
    finally:
        (directory / "results.json").write_text(json.dumps(results, indent=2), encoding="utf-8")
        print(f"Evidence retained: {directory}", flush=True)


if __name__ == "__main__":
    main()
