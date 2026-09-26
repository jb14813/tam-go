#!/usr/bin/env python3
"""Drive the original tam client and the Go client against one Go server.

Both clients are already running (scripts/compat/run.sh starts them). The
original client needs its TAM-CLIENT-ID header on every /api call; the Go
client needs nothing. Every step saves through one client and reads through
the other, so a mismatch in the wire format shows up as a failed check.
"""

import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.request


def call(base, method, path, body=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw) if raw else None
        except ValueError:
            return e.code, raw.decode(errors="replace")


def wait_for(base, headers, want):
    for _ in range(120):
        try:
            code, doc = call(base, "GET", "/api", headers=headers)
            if code == 200 and isinstance(doc, dict) and doc.get("whoami") == want:
                return
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            pass
        time.sleep(0.5)
    sys.exit(f"{base} did not come up as {want}")


def detect_client_id(base):
    """The original client makes up its TAM-CLIENT-ID when it starts and
    hands it to its pages, so read it from the rendered main menu."""
    for _ in range(120):
        try:
            with urllib.request.urlopen(base + "/", timeout=10) as r:
                html = r.read().decode(errors="replace")
            m = re.search(r'tamClientID\s*:\s*"([0-9a-fA-F-]{36})"', html)
            if m:
                return m.group(1)
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            pass
        time.sleep(0.5)
    sys.exit(f"could not read the client id from {base}/")


checks = 0


def expect(cond, what):
    global checks
    checks += 1
    if not cond:
        sys.exit(f"FAIL: {what}")
    print(f"ok  {what}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--original", required=True, help="base URL of the original client")
    ap.add_argument("--client-id", default="", help="its TAM-CLIENT-ID; read from its main menu when omitted")
    ap.add_argument("--go-client", required=True, help="base URL of tam-client")
    ap.add_argument("--server-host", required=True)
    ap.add_argument("--server-port", required=True)
    ap.add_argument("--password", required=True, help="the Go server's TAM_PWD")
    a = ap.parse_args()

    orig = a.original
    client_id = a.client_id or detect_client_id(orig)
    oh = {"TAM-CLIENT-ID": client_id}
    goc = a.go_client
    p = f"X{int(time.time()) % 100000}"

    wait_for(orig, oh, "TAM Client")
    wait_for(goc, {}, "TAM Client")

    # The original client is pointed at the Go server the way its Settings
    # page does it: settings, then a key made with the server password.
    code, doc = call(orig, "POST", "/api/settings", {"remote_server": a.server_host, "remote_port": a.server_port, "remote_tls": False}, oh)
    expect(code == 200, "original client: settings saved")
    code, doc = call(orig, "POST", "/api/auth", {"description": "original compat"}, {**oh, "TAM-PWD": a.password})
    expect(code == 200 and doc.get("auth_key"), "original client: key created on the Go server")
    code, doc = call(orig, "POST", "/api/settings", {"remote_key": doc["auth_key"]}, oh)
    expect(code == 200, "original client: key stored")
    code, doc = call(orig, "GET", "/api", headers=oh)
    expect(code == 200 and doc == {"whoami": "TAM Server", "authenticated": True, "healthy": True}, f"original client sees the Go server: {doc}")

    # The Go client pairs in one step.
    code, doc = call(goc, "POST", "/api/pair", {"host": a.server_host, "port": a.server_port, "tls": False, "password": a.password})
    expect(code == 200, f"go client: paired ({doc})")
    for _ in range(20):
        code, doc = call(goc, "GET", "/api/status")
        if doc.get("state") == "connected":
            break
        time.sleep(0.25)
    expect(doc.get("state") == "connected", f"go client: connected ({doc})")

    # Prefix and tickets saved by the original, read by the Go client.
    code, _ = call(orig, "POST", "/api/prefixes", [{"prefix": p, "color": "blue", "weight": 5}], oh)
    expect(code == 200, "original: prefix saved")
    tickets = [
        {"prefix": p, "t_id": 1, "first_name": "Ann", "last_name": "Both", "phone_number": "555-1", "pref": "CALL"},
        {"prefix": p, "t_id": 2, "first_name": "Ben", "last_name": "Both", "phone_number": "555-2", "pref": "TEXT"},
    ]
    code, _ = call(orig, "POST", "/api/tickets", tickets, oh)
    expect(code == 200, "original: tickets saved")
    code, doc = call(goc, "GET", f"/api/tickets/{p}")
    expect(code == 200 and [t["first_name"] for t in doc] == ["Ann", "Ben"], f"go client reads the original's tickets: {doc}")
    code, doc = call(goc, "GET", "/api/prefixes")
    expect(any(x["prefix"] == p and x["color"] == "blue" for x in doc), "go client lists the original's prefix")

    # Baskets saved by the Go client, read by the original.
    code, _ = call(goc, "POST", "/api/baskets", [{"prefix": p, "b_id": 1, "description": "Wine", "donors": "Smiths", "winning_ticket": 0}])
    expect(code == 200, "go client: basket saved")
    code, doc = call(orig, "GET", f"/api/baskets/{p}/1/1", headers=oh)
    expect(code == 200 and doc[0]["description"] == "Wine", f"original reads the go client's basket: {doc}")

    # Drawing by the original, reports by the Go client.
    code, _ = call(orig, "POST", "/api/drawing", [{"prefix": p, "b_id": 1, "description": "Wine", "donors": "Smiths", "winning_ticket": 2}], oh)
    expect(code == 200, "original: winner saved")
    code, doc = call(goc, "GET", f"/api/reports/bybasket/{p}")
    expect(code == 200 and doc[0]["winning_ticket"] == 2 and doc[0]["first_name"] == "Ben", f"go client report shows the winner: {doc}")
    code, doc = call(orig, "GET", f"/api/reports/byname/{p}", headers=oh)
    expect(code == 200 and doc[0]["last_name"] == "Both", f"original report: {doc}")
    code, doc = call(goc, "GET", "/api/reports/counts")
    expect(any(x["prefix"] == p and x["total_buys"] == 2 for x in doc), "go client counts")
    code, doc = call(orig, "GET", "/api/search/tickets?first_name=&last_name=Both&phone_number=", headers=oh)
    expect(code == 200 and len([t for t in doc if t["prefix"] == p]) == 2, "original search")

    # Backups through both.
    code, doc = call(orig, "GET", "/api/backuprestore/remote", headers=oh)
    expect(code == 200 and any(t["prefix"] == p for t in doc["tickets"]), "original: server backup download")
    code, doc = call(goc, "GET", "/api/backuprestore/remote")
    expect(code == 200 and any(b["prefix"] == p for b in doc["baskets"]), "go client: server backup download")
    code, doc = call(orig, "POST", f"/api/backuprestore/push/tickets", {}, oh)
    expect(code == 200, "original: push tickets")
    code, doc = call(goc, "POST", "/api/backuprestore/push/baskets", {})
    expect(code == 200, "go client: push baskets")

    # The original deletes the prefix; the Go client no longer lists it.
    code, doc = call(orig, "DELETE", f"/api/prefixes?p={p}", headers=oh)
    expect(code == 200, f"original: prefix deleted ({doc})")
    code, doc = call(goc, "GET", "/api/prefixes")
    expect(all(x["prefix"] != p for x in doc), "go client: prefix gone")

    print(f"all {checks} checks passed")


if __name__ == "__main__":
    main()
