#!/usr/bin/env python3
"""Exercise real Go/Rust mDNS announcement, metadata and withdrawal in Linux/WSL.

Random nonstandard server ports exclude subnet-sweep discovery. Mixed runs use
both real clients and both real servers concurrently, with opposite TLS modes.
Rust's supported process-local hostname environment gives its announcement a
distinct instance without changing the machine hostname. All evidence is kept.
"""
from __future__ import annotations

import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import tempfile
import time
import traceback

from run import Program, eventually, request


def binary_evidence(path):
    path = path.resolve()
    assert path.is_file(), f"missing real binary: {path}"
    return {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def exercise(args, directory, results):
    programs = []
    servers = {}
    clients = {}

    def begin(name):
        results["active_check"] = name
        print(f"START {name}", flush=True)

    def passed():
        name = results.pop("active_check")
        results["checks"].append({"name": name, "passed": True})
        print(f"PASS {name}", flush=True)

    def observe(label, client, expected, absent, identities):
        rows, _ = request(client.base, "/api/servers")
        results["observations"].append({"phase": results["active_check"], "client": label,
                                        "elapsed": round(time.monotonic() - started, 3),
                                        "servers": rows})
        assert isinstance(rows, list), (label, rows)
        ports = {row["port"] for row in rows}
        assert not (ports & {str(servers[name].port) for name in absent}), (label, rows)
        for name in expected:
            server = servers[name]
            found = [row for row in rows if row["port"] == str(server.port)]
            assert len(found) == 1, (label, name, rows)
            row = found[0]
            assert set(row) == {"name", "host", "port", "tls", "version"}, row
            assert row["name"] == identities[name]["name"], (name, row, identities[name])
            assert row["version"] == identities[name]["version"], (name, row, identities[name])
            assert isinstance(row["port"], str) and row["port"] == str(server.port), row
            assert isinstance(row["tls"], bool) and row["tls"] == server.tls, row
            address = ipaddress.ip_address(row["host"])
            assert address.version == 4 and not address.is_loopback and not address.is_unspecified, row
            # Metadata must identify the actual program at the advertised address.
            base = f"{'https' if row['tls'] else 'http'}://{row['host']}:{row['port']}"
            info, _ = request(base, "/api")
            assert info["whoami"] == "TAM Server", (row, info)
            assert all(info[field] == identities[name][field]
                       for field in ("name", "version", "event")), (row, info, identities[name])
        return True

    def check_clients(expected, absent, identities):
        for label, client in clients.items():
            eventually(lambda: observe(label, client, expected, absent, identities), 45)

    started = time.monotonic()
    try:
        specifications = [("go", args.go_server, args.tls)]
        if not args.baseline_only:
            specifications.append(("rust", args.rust_server, not args.tls))
        for name, binary, tls in specifications:
            server = Program(binary, "server", directory / f"{name}-server", tls,
                             listen_host="0.0.0.0", announce=False)
            assert server.port not in (8000, 8443), "random port must exclude subnet fallback ports"
            if name == "rust":
                hostname = f"r-tam-discovery-{server.port}"
                server.environment = {"HOSTNAME": hostname, "COMPUTERNAME": hostname}
            servers[name] = server
            programs.append(server)
        assert len({server.port for server in servers.values()}) == len(servers), "server ports collided"
        clients["go"] = Program(args.go_client, "client", directory / "go-client")
        if not args.baseline_only:
            clients["rust"] = Program(args.rust_client, "client", directory / "rust-client")
        programs.extend(clients.values())
        results["programs"] = {name: {"port": p.port, "tls": p.tls,
                                      "directory": str(p.directory)}
                               for name, p in [(f"{k}-server", v) for k, v in servers.items()]
                               + [(f"{k}-client", v) for k, v in clients.items()]}

        begin("non-announcing real servers are absent at random ports")
        for program in programs:
            program.start()
        identities = {name: request(server.base, "/api")[0] for name, server in servers.items()}
        results["identities"] = identities
        assert len({info["event"] for info in identities.values()}) == len(identities), identities
        check_clients([], list(servers), identities)
        passed()

        begin("every real client discovers every announcing server with reachable exact metadata")
        for server in servers.values():
            server.stop()
            server.announce = True
            server.start()
        check_clients(list(servers), [], identities)
        passed()

        absent = []
        for name, server in servers.items():
            begin(f"{name} announcer withdrawal disappears and surviving announcers remain discoverable")
            server.stop()
            absent.append(name)
            check_clients([other for other in servers if other not in absent], absent, identities)
            passed()

        begin("announcer restart is rediscovered by every real client")
        for server in servers.values():
            server.start()
        check_clients(list(servers), [], identities)
        passed()
    finally:
        failures = []
        for program in reversed(programs):
            try:
                program.stop()
            except BaseException as error:
                failures.append(f"{program.role} {program.directory}: {type(error).__name__}: {error}")
        results["cleanup"] = {"all_processes_stopped": all(
            p.process is None or p.process.poll() is not None for p in programs)}
        if failures:
            results["cleanup"]["failures"] = failures
            raise AssertionError(failures)
    begin("all discovery programs drained")
    assert results["cleanup"]["all_processes_stopped"], results["cleanup"]
    passed()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("go-server", "go-client", "rust-server", "rust-client"):
        parser.add_argument(f"--{name}", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--baseline-only", action="store_true", help="Use only actual Go server/client.")
    parser.add_argument("--tls", action="store_true", help="Use Go TLS and Rust plain; default is the reverse.")
    args = parser.parse_args()
    assert os.name == "posix", "network probes run only in Linux/WSL"
    args.output.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="discovery-", dir=args.output.resolve()))
    results = {"directory": str(directory), "baseline_only": args.baseline_only,
               "go_tls": args.tls, "checks": [], "observations": [], "binaries": {}}
    try:
        names = ["go_server", "go_client"]
        if not args.baseline_only:
            names += ["rust_server", "rust_client"]
        for name in names:
            results["binaries"][name] = binary_evidence(getattr(args, name))
        exercise(args, directory, results)
        results["passed"] = True
    except BaseException as error:
        results["passed"] = False
        results["failure"] = f"{type(error).__name__}: {error}"
        results["checks"].append({"name": results.pop("active_check", "validate supplied binaries"),
                                  "passed": False, "failure": results["failure"]})
        (directory / "failure.txt").write_text(traceback.format_exc(), encoding="utf-8")
        raise
    finally:
        (directory / "results.json").write_text(json.dumps(results, indent=2), encoding="utf-8")
        print(f"Evidence retained: {directory}", flush=True)


if __name__ == "__main__":
    main()
