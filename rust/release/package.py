#!/usr/bin/env python3
"""Package the two native R-TAM programs into a self-contained release archive."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import struct
import tarfile
import tempfile
import tomllib
import zipfile


ROOT = Path(__file__).resolve().parents[2]
ASSETS = Path(__file__).resolve().with_name("assets")
PLATFORMS = {
    "windows": {
        "target": "x86_64-pc-windows-msvc",
        "binary_suffix": ".exe",
        "launcher_suffix": ".cmd",
        "archive_suffix": ".zip",
        "requirements": "Windows 10 or 11 on an x86_64 (Intel/AMD 64-bit) computer.",
        "server_command": ".\\start-server.cmd",
        "client_command": ".\\start-client.cmd",
        "default_root": "`%LOCALAPPDATA%\\R-TAM\\server` and `%LOCALAPPDATA%\\R-TAM\\client`",
        "checksum_command": "Get-FileHash .\\r-tam-@VERSION@-windows-x86_64.zip -Algorithm SHA256",
        "override": 'In a Command Prompt, use `set "TAM_DATA_DIR=D:\\R-TAM\\server"` before\n'
                    '`start-server.cmd`. Use a different folder for the client. The launchers\n'
                    "also work by double-clicking them in File Explorer.",
    },
    "linux": {
        "target": "x86_64-unknown-linux-gnu",
        "binary_suffix": "",
        "launcher_suffix": ".sh",
        "archive_suffix": ".tar.gz",
        "requirements": "Linux x86_64 with glibc 2.39 or later (for example, Ubuntu 24.04 or later).\n"
                        "This package requires the standard libgcc runtime. It does not target musl,\n"
                        "ARM or macOS. In WSL, store the extracted package and data on the Linux filesystem.",
        "server_command": "./start-server.sh",
        "client_command": "./start-client.sh",
        "default_root": "`${XDG_DATA_HOME:-$HOME/.local/share}/r-tam/server` and the separate `client` folder",
        "checksum_command": "sha256sum --check --ignore-missing SHA256SUMS",
        "override": '`TAM_DATA_DIR="$HOME/r-tam-custom-server" ./start-server.sh` chooses a custom\n'
                    "server folder. Use a different folder for the client. The Linux launchers\n"
                    "disable the tray and automatic browser opening, so they also work on a\n"
                    "server without a desktop; open the client address in a browser manually.",
    },
}


def read_binary(path, platform):
    data = path.read_bytes()
    if platform == "windows":
        valid = len(data) >= 64 and data[:2] == b"MZ"
        offset = struct.unpack_from("<I", data, 60)[0] if valid else 0
        valid = valid and offset + 6 <= len(data) and data[offset:offset + 4] == b"PE\0\0"
        valid = valid and struct.unpack_from("<H", data, offset + 4)[0] == 0x8664
    else:
        valid = (len(data) >= 64 and data[:6] == b"\x7fELF\x02\x01"
                 and struct.unpack_from("<H", data, 18)[0] == 62)
    if not valid:
        raise ValueError(f"{path.name} is not a {platform} x86_64 executable")
    return data


def distribution_files(platform, version, source, binaries):
    configuration = PLATFORMS[platform]
    files = {}
    manifest = {"version": version, "source": source,
                "platform": platform, "target": configuration["target"], "binaries": {}}
    for role in ("client", "server"):
        name = "r-tam-" + role + configuration["binary_suffix"]
        binary = read_binary(binaries / name, platform)
        files[name] = (binary, 0o755)
        manifest["binaries"][name] = {"sha256": hashlib.sha256(binary).hexdigest(), "bytes": len(binary)}
        name = "start-" + role + configuration["launcher_suffix"]
        launcher = (ASSETS / name).read_text(encoding="utf-8")
        newline = "\r\n" if platform == "windows" else "\n"
        files[name] = (launcher.replace("\r\n", "\n").replace("\n", newline).encode("utf-8"), 0o755)
    readme = (ASSETS / "README.md").read_text(encoding="utf-8")
    substitutions = dict(configuration, version=version, platform=platform, source=source)
    for key, value in substitutions.items():
        readme = readme.replace("@" + key.upper() + "@", value)
    # Commands inserted above can themselves contain the version placeholder.
    readme = readme.replace("@VERSION@", version)
    files["README.md"] = (readme.encode("utf-8"), 0o644)
    files["LICENSE.md"] = ((ROOT / "LICENSE.md").read_bytes(), 0o644)
    files["VERSION.json"] = ((json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode("utf-8"), 0o644)
    return files


def write_archive(destination, prefix, files, platform):
    if platform == "windows":
        with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            for name, (contents, mode) in sorted(files.items()):
                entry = zipfile.ZipInfo(prefix + "/" + name, date_time=(1980, 1, 1, 0, 0, 0))
                entry.create_system = 3
                entry.external_attr = (0o100000 | mode) << 16
                entry.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(entry, contents)
    else:
        with destination.open("wb") as output:
            with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0, compresslevel=9) as compressed:
                with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                    for name, (contents, mode) in sorted(files.items()):
                        entry = tarfile.TarInfo(prefix + "/" + name)
                        entry.size = len(contents)
                        entry.mode = mode
                        archive.addfile(entry, io.BytesIO(contents))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", required=True, choices=PLATFORMS)
    parser.add_argument("--version", required=True, help="workspace release version, for example 0.1.0")
    parser.add_argument("--source", required=True, help="full 40-character Git source commit")
    parser.add_argument("--binaries", required=True, type=Path, help="folder containing both native binaries")
    parser.add_argument("--output", required=True, type=Path, help="release archive and checksum destination")
    arguments = parser.parse_args()
    try:
        if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", arguments.version):
            raise ValueError("version must be a canonical major.minor.patch release version")
        with (ROOT / "rust" / "Cargo.toml").open("rb") as cargo_file:
            workspace_version = tomllib.load(cargo_file)["workspace"]["package"]["version"]
        if arguments.version != workspace_version:
            raise ValueError("version must match rust/Cargo.toml workspace.package.version")
        if not re.fullmatch(r"[0-9a-fA-F]{40}", arguments.source):
            raise ValueError("source must be a full 40-character Git commit")
        files = distribution_files(arguments.platform, arguments.version, arguments.source.lower(), arguments.binaries)
        prefix = f"r-tam-{arguments.version}-{arguments.platform}-x86_64"
        archive_name = prefix + PLATFORMS[arguments.platform]["archive_suffix"]
        checksum_name = f"SHA256SUMS-{arguments.platform}-x86_64.txt"
        arguments.output.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix=".r-tam-package-", dir=arguments.output) as staging:
            staged_archive = Path(staging) / archive_name
            write_archive(staged_archive, prefix, files, arguments.platform)
            digest = hashlib.sha256(staged_archive.read_bytes()).hexdigest()
            staged_checksum = Path(staging) / checksum_name
            staged_checksum.write_text(f"{digest}  {archive_name}\n", encoding="ascii", newline="\n")
            os.replace(staged_archive, arguments.output / archive_name)
            os.replace(staged_checksum, arguments.output / checksum_name)
        print(archive_name)
        print(checksum_name)
    except (OSError, ValueError, KeyError) as error:
        parser.exit(1, f"package error: {error}\n")


if __name__ == "__main__":
    main()
