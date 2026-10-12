#!/usr/bin/env python3
"""Retain WSL server lifecycle and Go/Rust bcrypt evidence in a fresh output folder."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import ssl
import subprocess
import sys
import time
import traceback
import urllib.error
import urllib.parse
import urllib.request


ENV_PASSWORD = "r-tam-lifecycle-environment-only"
RUST_PASSWORD = "r-tam-lifecycle-rust-hash-only"
GO_PASSWORD = "r-tam-lifecycle-go-hash-only"
REPO = Path(__file__).resolve().parents[3]


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def require(condition, detail):
    if not condition:
        raise AssertionError(detail)


def request(base, path, *, form=None, headers=None, expected=200):
    fields = dict(headers or {})
    payload = None
    if form is not None:
        payload = urllib.parse.urlencode(form).encode()
        fields["Content-Type"] = "application/x-www-form-urlencoded"
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({}), NoRedirect(),
        urllib.request.HTTPSHandler(context=ssl._create_unverified_context()),
    )
    req = urllib.request.Request(base + path, data=payload, headers=fields)
    try:
        response = opener.open(req, timeout=5)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        received = {key.lower(): value for key, value in response.headers.items()}
        require(response.status == expected,
                f"{path}: expected {expected}, got {response.status}: {raw[:300]!r}")
    return raw, received


def token(html):
    match = re.search(rb'name="csrf" value="([^"]+)"', html)
    require(match is not None, "missing CSRF input in admin form")
    return match[1].decode()


def login(base, password, tls=False):
    html, headers = request(base, "/admin/")
    visitor = headers["set-cookie"].split(";", 1)[0]
    _, headers = request(base, "/admin/login", form={"csrf": token(html), "password": password},
                         headers={"Cookie": visitor}, expected=303)
    require(headers["location"] == "/admin/status", "login did not redirect to status")
    cookie = headers["set-cookie"]
    for flag in ("HttpOnly", "SameSite=Strict", "Path=/admin"):
        require(flag in cookie, f"missing cookie flag {flag}")
    require(("; Secure" in cookie) == tls, "cookie Secure flag disagrees with TLS")
    session = cookie.split(";", 1)[0]
    request(base, "/admin/status", headers={"Cookie": session, "Accept": "application/json"})
    return session, cookie


class Server:
    def __init__(self, binary, output, name, data, tls=False):
        self.binary, self.output, self.name = binary, output, name
        self.data, self.tls = data, tls
        self.data.mkdir(parents=True, exist_ok=True)
        self.stdout_path = output / f"{name}.stdout.log"
        self.stderr_path = output / f"{name}.stderr.log"
        self.process = None
        self.logs = []
        self.base = None
        self.exit_code = None

    def start(self, address="127.0.0.1:0", wait=True):
        args = [str(self.binary), "-addr", address, "-tray=false", "-announce=false"]
        if self.tls:
            args.append("-tls")
        self.logs = [self.stdout_path.open("wb", buffering=0), self.stderr_path.open("wb", buffering=0)]
        self.process = subprocess.Popen(args, cwd=self.data,
            env={**os.environ, "TAM_DATA_DIR": str(self.data), "TAM_PWD": ENV_PASSWORD},
            stdout=self.logs[0], stderr=self.logs[1])
        if not wait:
            return
        deadline = time.monotonic() + 20
        last = None
        while time.monotonic() < deadline:
            require(self.process.poll() is None, f"{self.name} exited during startup: {self.stderr_path}")
            text = self.stdout_path.read_text(errors="replace")
            urls = re.findall(r"^https?://127\.0\.0\.1:\d+/$", text, re.MULTILINE)
            if urls:
                self.base = urls[0].rstrip("/")
                parsed = urllib.parse.urlparse(self.base)
                require(parsed.port > 0, "port-zero URL did not report the actual port")
                require(parsed.scheme == ("https" if self.tls else "http"), "startup URL scheme mismatch")
                try:
                    raw, _ = request(self.base, "/api")
                    root = json.loads(raw)
                    require(root["whoami"] == "TAM Server" and root["healthy"] is True, root)
                    (self.output / f"{self.name}.root.json").write_bytes(raw)
                    return
                except (OSError, ValueError) as error:
                    last = error
            time.sleep(0.05)
        raise AssertionError(f"{self.name} readiness timeout: {last}; see {self.stdout_path}")

    def stop(self):
        try:
            if self.process is not None:
                if self.process.poll() is None:
                    self.process.send_signal(signal.SIGTERM)
                    try:
                        self.process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        self.process.kill()
                        self.process.wait(timeout=5)
                        raise AssertionError(f"{self.name} did not drain after SIGTERM")
                self.exit_code = self.process.returncode
        finally:
            for log in self.logs:
                log.close()
            self.logs = []

    def stopped_cleanly(self):
        self.stop()
        require(self.exit_code == 0, f"{self.name} SIGTERM exit {self.exit_code}")
        log = (self.data / "tam-server.log").read_bytes()
        require(b"R-TAM Server stopped" in log, f"{self.name}: missing stopped daemon log")
        return log


BCRYPT_HELPER = r'''package main
import (
    "encoding/json"
    "fmt"
    "os"
    "strings"
    "golang.org/x/crypto/bcrypt"
)
func main() {
    fail := func(err error) { if err != nil { panic(err) } }
    raw, err := os.ReadFile(os.Args[1]); fail(err)
    var doc map[string]any
    fail(json.Unmarshal(raw, &doc))
    hash, ok := doc["password_hash"].(string)
    if !ok || !strings.HasPrefix(hash, "$2a$10$") { panic("Rust hash is not 2a cost 10") }
    fail(bcrypt.CompareHashAndPassword([]byte(hash), []byte(os.Args[2])))
    cost, err := bcrypt.Cost([]byte(hash)); fail(err)
    if cost != 10 { panic("incorrect bcrypt cost") }
    next, err := bcrypt.GenerateFromPassword([]byte(os.Args[3]), 10); fail(err)
    doc["password_hash"] = string(next)
    raw, err = json.MarshalIndent(doc, "", "  "); fail(err)
    fail(os.WriteFile(os.Args[1], raw, 0600))
    fmt.Println(`{"go_verified_rust_hash":true,"rust_hash_version":"2a","cost":10,"go_hash_written":true}`)
}
'''


def exercise(binary, output, results, servers):
    http_data, tls_data = output / "data/http", output / "data/tls"
    first = Server(binary, output, "http-first", http_data)
    servers.append(first)
    first.start()
    results["http_actual_url"] = first.base

    duplicate = Server(binary, output, "duplicate-lock", http_data)
    servers.append(duplicate)
    duplicate.start(urllib.parse.urlparse(first.base).netloc, wait=False)
    duplicate.process.wait(timeout=10)
    duplicate.stop()
    require(duplicate.exit_code == 1, f"duplicate folder exit {duplicate.exit_code}")
    require("owns data folder" in duplicate.stderr_path.read_text(), "folder lock was not checked before occupied bind")
    require(not re.search(r"https?://", duplicate.stdout_path.read_text()), "duplicate process printed readiness")
    results["duplicate_lock_before_bind"] = True

    session, _ = login(first.base, ENV_PASSWORD)
    html, _ = request(first.base, "/admin/password", headers={"Cookie": session})
    _, headers = request(first.base, "/admin/password", form={"csrf": token(html),
        "current": ENV_PASSWORD, "password": RUST_PASSWORD, "confirm": RUST_PASSWORD},
        headers={"Cookie": session}, expected=303)
    require(headers["location"] == "/admin/password", "password change did not redirect")
    request(first.base, "/api/auth", headers={"TAM-PW": RUST_PASSWORD})
    request(first.base, "/api/auth", headers={"TAM-PW": ENV_PASSWORD}, expected=401)
    results["admin_login_password_change"] = True
    first_log = first.stopped_cleanly()
    shutil.copyfile(http_data / "server.json", output / "server-rust.json")

    helper = output / "bcrypt_check.go"
    helper.write_text(BCRYPT_HELPER)
    command = ["go", "run", str(helper), str(http_data / "server.json"), RUST_PASSWORD, GO_PASSWORD]
    result = subprocess.run(command, cwd=REPO, capture_output=True, text=True, timeout=120)
    (output / "bcrypt.stdout.log").write_text(result.stdout)
    (output / "bcrypt.stderr.log").write_text(result.stderr)
    results["bcrypt_command"] = command
    results["bcrypt_exit_code"] = result.returncode
    require(result.returncode == 0, f"Go bcrypt helper failed: {result.stderr}")
    results["bcrypt"] = json.loads(result.stdout)
    shutil.copyfile(http_data / "server.json", output / "server-go.json")

    second = Server(binary, output, "http-go-hash", http_data)
    servers.append(second)
    second.start()
    results["http_restart_actual_url"] = second.base
    request(second.base, "/api/auth", headers={"TAM-PW": GO_PASSWORD})
    request(second.base, "/api/auth", headers={"TAM-PW": ENV_PASSWORD}, expected=401)
    second_log = second.stopped_cleanly()
    require(second_log.startswith(first_log) and len(second_log) > len(first_log), "daemon log was not appended")
    results["rust_reads_go_hash_over_environment"] = True
    results["append_log"] = True

    tls_first = Server(binary, output, "tls-first", tls_data, tls=True)
    servers.append(tls_first)
    tls_first.start()
    results["tls_actual_url"] = tls_first.base
    _, cookie = login(tls_first.base, ENV_PASSWORD, tls=True)
    (output / "tls-cookie.txt").write_text(cookie)
    results["tls_secure_cookie"] = True
    pair = {name: (tls_data / name).read_bytes() for name in ("server.crt", "server.key")}
    tls_first.stopped_cleanly()

    tls_second = Server(binary, output, "tls-restart", tls_data, tls=True)
    servers.append(tls_second)
    tls_second.start()
    results["tls_restart_actual_url"] = tls_second.base
    for name, original in pair.items():
        require((tls_data / name).read_bytes() == original, f"retained {name} changed on restart")
    request(tls_second.base, "/api/auth", headers={"TAM-PW": ENV_PASSWORD})
    tls_second.stopped_cleanly()
    results["retained_tls_pair_sha256"] = {name: hashlib.sha256(raw).hexdigest() for name, raw in pair.items()}
    results["graceful_sigterm"] = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server", required=True, type=Path, help="Linux Rust server executable")
    parser.add_argument("--output", required=True, type=Path, help="new persistent evidence directory (never /tmp)")
    args = parser.parse_args()
    if sys.platform != "linux" or "microsoft" not in Path("/proc/sys/kernel/osrelease").read_text().lower():
        parser.error("run this listener probe only in WSL Linux; native Windows listeners are prohibited")
    binary, output = args.server.resolve(), args.output.resolve()
    if not binary.is_file() or not os.access(binary, os.X_OK):
        parser.error(f"missing executable: {binary}")
    if not shutil.which("go") or not (REPO / "go.mod").is_file():
        parser.error("Go must be on PATH and the repository go.mod must be present")
    if output == Path("/") or any(output == root or root in output.parents for root in map(Path, ("/tmp", "/var/tmp", "/dev/shm"))):
        parser.error("--output must be a persistent home-cache or repository path, never temporary storage")
    if output.exists():
        parser.error("--output must be new; retained evidence and event data are never overwritten")
    output.mkdir(parents=True)
    results = {"status": "running", "output": str(output), "server": str(binary),
        "server_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "probe_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        "command": [sys.executable, *sys.argv], "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    servers, exit_code = [], 1
    try:
        git = "git"
        git_pointer = REPO / ".git"
        if git_pointer.is_file() and re.match(r"gitdir:\s*[A-Za-z]:", git_pointer.read_text()):
            git = shutil.which("git.exe") or "/mnt/c/Program Files/Git/cmd/git.exe"
        results["git_command"] = git
        results["git_head_at_run"] = subprocess.check_output([git, "rev-parse", "HEAD"], cwd=REPO, text=True).strip()
        results["server_source_commit"] = subprocess.check_output([git, "log", "-1", "--format=%H", "--", "rust/crates/server"], cwd=REPO, text=True).strip()
        results["source_worktree_changes"] = subprocess.check_output([git, "status", "--porcelain", "--", "rust/crates/core", "rust/crates/server"], cwd=REPO, text=True).splitlines()
        results["go_version"] = subprocess.check_output(["go", "version"], text=True).strip()
        exercise(binary, output, results, servers)
        results["status"], exit_code = "passed", 0
    except (Exception, KeyboardInterrupt):
        results["status"] = "failed"
        results["error"] = traceback.format_exc()
    finally:
        cleanup_errors = []
        for server in reversed(servers):
            try:
                server.stop()
            except Exception as error:
                cleanup_errors.append(f"{server.name}: {error}")
        results["processes"] = [{"name": server.name, "pid": server.process.pid if server.process else None,
            "exit_code": server.exit_code, "stopped": server.process is None or server.process.poll() is not None}
            for server in servers]
        if cleanup_errors:
            results["cleanup_errors"] = cleanup_errors
            results["status"], exit_code = "failed", 1
        results["finished_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        (output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        print(json.dumps(results, indent=2))
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
