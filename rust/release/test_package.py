"""Behavioral checks for release contents and isolated launcher data folders."""

import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tarfile
import tempfile
import tomllib
import unittest
import zipfile


SCRIPT = Path(__file__).with_name("package.py")
SOURCE = "0123456789abcdef0123456789abcdef01234567"
with (Path(__file__).resolve().parents[1] / "Cargo.toml").open("rb") as cargo_file:
    VERSION = tomllib.load(cargo_file)["workspace"]["package"]["version"]
MISMATCH_VERSION = f"{int(VERSION.split('.')[0]) + 1}.0.0"


class PackageTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="r-tam-package-test-")
        self.root = Path(self.temporary.name)
        self.binaries = self.root / "binaries"
        self.binaries.mkdir()
        self.output = self.root / "output"

    def tearDown(self):
        self.temporary.cleanup()

    def fixtures(self, platform):
        if platform == "windows":
            header = bytearray(128)
            header[:2] = b"MZ"
            struct.pack_into("<I", header, 60, 64)
            header[64:68] = b"PE\0\0"
            struct.pack_into("<H", header, 68, 0x8664)
            suffix = ".exe"
        else:
            header = bytearray(64)
            header[:6] = b"\x7fELF\x02\x01"
            struct.pack_into("<H", header, 18, 62)
            suffix = ""
        for role in ("server", "client"):
            (self.binaries / f"r-tam-{role}{suffix}").write_bytes(header)

    def invoke(self, platform="windows", version=VERSION, source=SOURCE):
        return subprocess.run(
            [sys.executable, "-B", str(SCRIPT), "--platform", platform,
             "--version", version, "--source", source,
             "--binaries", str(self.binaries), "--output", str(self.output)],
            capture_output=True, text=True, check=False,
        )

    def test_windows_archive_has_only_distribution_files_and_matching_checksum(self):
        # Omitting a binary/license or including a data/log file breaks this contract.
        self.fixtures("windows")
        (self.binaries / "private.db").write_bytes(b"must not ship")
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        archive = self.output / f"r-tam-{VERSION}-windows-x86_64.zip"
        prefix = f"r-tam-{VERSION}-windows-x86_64/"
        with zipfile.ZipFile(archive) as package:
            self.assertEqual(set(package.namelist()), {
                prefix + name for name in (
                    "r-tam-server.exe", "r-tam-client.exe", "start-server.cmd",
                    "start-client.cmd", "README.md", "LICENSE.md", "VERSION.json",
                )
            })
            metadata = json.loads(package.read(prefix + "VERSION.json"))
            self.assertEqual(metadata["version"], VERSION)
            self.assertEqual(metadata["source"], SOURCE)
            self.assertEqual(metadata["target"], "x86_64-pc-windows-msvc")
            self.assertIn(b"Permission is hereby granted", package.read(prefix + "LICENSE.md"))
        checksum = (self.output / "SHA256SUMS-windows-x86_64.txt").read_text()
        self.assertEqual(checksum, hashlib.sha256(archive.read_bytes()).hexdigest()
                         + f"  r-tam-{VERSION}-windows-x86_64.zip\n")
        original = archive.read_bytes()
        self.assertEqual(self.invoke().returncode, 0)
        self.assertEqual(archive.read_bytes(), original)

    def test_linux_archive_preserves_executable_modes_and_is_repeatable(self):
        # Wrong tar modes make extracted launchers unusable; variable metadata changes hashes.
        self.fixtures("linux")
        result = self.invoke("linux")
        self.assertEqual(result.returncode, 0, result.stderr)
        archive = self.output / f"r-tam-{VERSION}-linux-x86_64.tar.gz"
        original = archive.read_bytes()
        with tarfile.open(archive, "r:gz") as package:
            self.assertEqual(len(package.getmembers()), 7)
            for member in package.getmembers():
                self.assertTrue(member.isfile())
                self.assertEqual(len(Path(member.name).parts), 2)
                executable = Path(member.name).name in {
                    "r-tam-server", "r-tam-client", "start-server.sh", "start-client.sh",
                }
                self.assertEqual(member.mode, 0o755 if executable else 0o644)
                self.assertEqual(member.uid, 0)
                self.assertEqual(member.gid, 0)
            metadata = json.load(package.extractfile(
                f"r-tam-{VERSION}-linux-x86_64/VERSION.json"))
            self.assertEqual(metadata["target"], "x86_64-unknown-linux-gnu")
        self.assertEqual(self.invoke("linux").returncode, 0)
        self.assertEqual(archive.read_bytes(), original)

    def test_invalid_inputs_do_not_create_distribution_output(self):
        # Rejecting invalid provenance or incomplete/wrong binaries before writes avoids bad releases.
        self.fixtures("windows")
        for version, source in (("../bad", SOURCE), (MISMATCH_VERSION, SOURCE), (VERSION, "short")):
            with self.subTest(version=version, source=source):
                result = self.invoke(version=version, source=source)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.output.exists())
        wrong_target = self.invoke("linux")
        self.assertNotEqual(wrong_target.returncode, 0)
        self.assertFalse(self.output.exists())
        (self.binaries / "r-tam-client.exe").unlink()
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())

    @unittest.skipUnless(os.name == "posix", "requires native POSIX launcher execution")
    def test_linux_launchers_keep_role_folders_and_pass_explicit_settings_and_arguments(self):
        # Sharing role folders, overriding TAM_DATA_DIR or losing quoted args risks user data.
        self.fixtures("linux")
        result = self.invoke("linux")
        self.assertEqual(result.returncode, 0, result.stderr)
        extraction = self.root / "package with spaces"
        with tarfile.open(self.output / f"r-tam-{VERSION}-linux-x86_64.tar.gz") as package:
            package.extractall(extraction, filter="data")
        extracted = extraction / f"r-tam-{VERSION}-linux-x86_64"
        environment = os.environ.copy()
        environment.pop("TAM_DATA_DIR", None)
        environment.pop("XDG_DATA_HOME", None)
        environment["HOME"] = str(self.root / "fresh-home")
        for role in ("server", "client"):
            probe = extracted / f"r-tam-{role}"
            probe.write_text('#!/bin/sh\nprintf \'%s\\n\' "$TAM_DATA_DIR" "$@"\n')
            probe.chmod(0o755)
            launcher = extracted / f"start-{role}.sh"
            invocation = subprocess.run(
                [str(launcher), "-addr", "localhost:4091", "two words"],
                cwd=self.root, env=environment, capture_output=True, text=True, check=False,
            )
            self.assertEqual(invocation.returncode, 0, invocation.stderr)
            expected = [str(self.root / "fresh-home/.local/share/r-tam" / role), "-tray=false"]
            if role == "client":
                expected.append("-open=false")
            self.assertEqual(invocation.stdout.splitlines(), expected + [
                "-addr", "localhost:4091", "two words",
            ])
            environment["TAM_DATA_DIR"] = str(self.root / "explicit-folder")
            invocation = subprocess.run([str(launcher)], env=environment,
                                        capture_output=True, text=True, check=False)
            self.assertEqual(invocation.stdout.splitlines()[0], str(self.root / "explicit-folder"))
            environment.pop("TAM_DATA_DIR")
            environment["XDG_DATA_HOME"] = str(self.root / "xdg-folder")
            invocation = subprocess.run([str(launcher)], env=environment,
                                        capture_output=True, text=True, check=False)
            self.assertEqual(invocation.stdout.splitlines()[0], str(self.root / "xdg-folder/r-tam" / role))
            environment.pop("XDG_DATA_HOME")


if __name__ == "__main__":
    unittest.main()
