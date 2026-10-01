#!/usr/bin/env python3
"""Failure-path and layout tests for scripts/build-windows-release.py.

These tests never build Go code, never sign anything, and never contact
GitHub. The script under test is driven with a stub `go` on PATH, so
refusal behavior, archive layout, permissions, determinism and the "no
false success" properties are exercised deterministically on any host.

What this CANNOT prove: that a real Go toolchain produces a working
Windows binary, that the binaries run on Windows, or that a real Actions
run succeeds. Those need the real Windows build and the smoke tests in the
release workflow.
"""

import hashlib
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
SCRIPT = REPO / "scripts" / "build-windows-release.py"
VERSION = "1.2.3"
NAME = f"cercano-{VERSION}-windows-x64"


class WindowsReleaseBuildTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.out = self.dir / "dist"
        self.bin = self.dir / "stubs"
        self.bin.mkdir()
        self.log = self.dir / "calls.log"
        self._write_stubs()

    def _write_stubs(self):
        # Route only go-build subprocesses through an inert Python fixture.
        # Native Windows cannot execute a POSIX shebang stub via CreateProcess.
        self.fixture = self.bin / "fake_go.py"
        self.fixture.write_text(f"""import os, sys
from pathlib import Path
with open({str(self.log)!r}, 'a', encoding='utf-8') as f:
    f.write('go env GOOS=' + os.environ.get('GOOS', '') + ' GOARCH=' + os.environ.get('GOARCH', '') + ' CGO_ENABLED=' + os.environ.get('CGO_ENABLED', '') + ' ' + ' '.join(sys.argv[1:]) + '\\n')
if os.environ.get('GO_BUILD_FAIL'): sys.exit(7)
out = Path(sys.argv[sys.argv.index('-o') + 1])
out.parent.mkdir(parents=True, exist_ok=True)
import struct
header = bytearray(154)
header[:2] = b'MZ'
struct.pack_into('<I', header, 60, 128)
header[128:132] = bytes([80, 69, 0, 0])
struct.pack_into('<H', header, 132, int(os.environ.get('FAKE_MACHINE', '34404')))
struct.pack_into('<H', header, 150, 2)
struct.pack_into('<H', header, 152, 0x20b)
out.write_bytes(b'not-a-pe-image' if os.environ.get('FAKE_NON_PE') else header + os.environ.get('FAKE_VERSION_PAYLOAD', {VERSION!r}).encode())
""")
        self.launcher = self.bin / "launch.py"
        self.launcher.write_text(f"""import runpy, subprocess, sys
original = subprocess.run
def run(args, *a, **kw):
    if args[0] == 'go': args = [sys.executable, {str(self.fixture)!r}, *args[1:]]
    return original(args, *a, **kw)
subprocess.run = run
sys.argv = sys.argv[1:]
runpy.run_path(sys.argv[0], run_name='__main__')
""")

    def _sandbox(self, tagged=False):
        """Copy the script into a sandbox repo; optionally tag v1.2.3."""
        sandbox = self.dir / "sandbox"
        (sandbox / "scripts").mkdir(parents=True, exist_ok=True)
        shutil.copy2(SCRIPT, sandbox / "scripts" / SCRIPT.name)
        (sandbox / "LICENSE").write_text("license text\n")
        for relative in ("source/server/cmd/cercano", "source/clients/cli"):
            (sandbox / relative).mkdir(parents=True, exist_ok=True)
        if tagged:
            def git(*args):
                return subprocess.run(["git", *args], cwd=str(sandbox),
                                       check=True, capture_output=True, text=True)
            git("init")
            git("config", "user.name", "Release Test")
            git("config", "user.email", "test@example.invalid")
            git("add", "-A")
            git("commit", "--allow-empty", "-m", "tagged source")
            git("tag", "-a", f"v{VERSION}", "-m", "release fixture")
        return sandbox

    def run_build(self, version=VERSION, env=None, output=None, tagged=False,
                  allow_untagged=True, sandbox=None):
        sandbox = sandbox or self._sandbox(tagged=tagged)
        environ = dict(os.environ)
        environ.pop("CERCANO_ALLOW_UNTAGGED", None)
        environ["HOME"] = str(self.dir)
        if allow_untagged:
            environ["CERCANO_ALLOW_UNTAGGED"] = "1"
        environ.update(env or {})
        return subprocess.run(
            [sys.executable, str(self.launcher), str(sandbox / "scripts" / SCRIPT.name),
             version, str(output or self.out)],
            capture_output=True, text=True, env=environ, cwd=str(sandbox))

    def assert_refused(self, result, fragment):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(fragment, result.stderr)
        archives = list(self.out.glob("*.zip")) if self.out.exists() else []
        self.assertEqual(archives, [], "a refused build must not leave an archive")

    def test_builds_unsigned_archive_with_expected_layout(self):
        result = self.run_build()
        self.assertEqual(result.returncode, 0, result.stderr)
        archive = self.out / f"{NAME}.zip"
        self.assertTrue(archive.is_file())
        with zipfile.ZipFile(archive) as zf:
            members = {info.filename: info for info in zf.infolist()}
        self.assertEqual(sorted(members), [
            f"{NAME}/LICENSE", f"{NAME}/README.txt",
            f"{NAME}/bin/cercano-cli.exe", f"{NAME}/bin/cercano.exe",
        ])
        for exe in (f"{NAME}/bin/cercano.exe", f"{NAME}/bin/cercano-cli.exe"):
            mode = members[exe].external_attr >> 16 & 0o777
            self.assertEqual(mode, 0o755, f"{exe} must stay executable")
        for doc in (f"{NAME}/LICENSE", f"{NAME}/README.txt"):
            mode = members[doc].external_attr >> 16 & 0o777
            self.assertEqual(mode, 0o644, f"{doc} must not be executable")
        # The archive is explicitly unsigned: nothing but the two binaries,
        # LICENSE and README.txt is packaged, and the README says so.
        readme = zipfile.ZipFile(archive).read(f"{NAME}/README.txt").decode()
        self.assertIn("UNSIGNED", readme)
        self.assertIn("supported Windows release", readme)
        self.assertIn("NOT a", readme)
        self.assertIn(VERSION, readme)
        # The build must target windows/amd64 with cgo disabled.
        calls = self.log.read_text()
        self.assertIn("GOOS=windows", calls)
        self.assertIn("GOARCH=amd64", calls)
        self.assertIn("CGO_ENABLED=0", calls)
        self.assertIn(f"-X main.version={VERSION}", calls)
        # Both entrypoints are built from the same validated version.
        self.assertEqual(calls.count("go env"), 2)
        self.assertIn("./cmd/cercano", calls)
        self.assertIn(VERSION.encode(), zipfile.ZipFile(archive)
                      .read(f"{NAME}/bin/cercano.exe"))
        # Checksum matches the archived bytes, in shasum-compatible format.
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        self.assertEqual((self.out / f"{NAME}.zip.sha256").read_text(),
                         f"{digest}  {NAME}.zip\n")

    def test_archives_are_deterministic(self):
        # Two builds of the same inputs must be byte-identical, so a
        # re-run cannot quietly change published bytes.
        self.assertEqual(self.run_build().returncode, 0)
        first = (self.out / f"{NAME}.zip").read_bytes()
        self.out.mkdir(parents=True, exist_ok=True)
        second_out = self.dir / "dist2"
        self.assertEqual(self.run_build(output=second_out).returncode, 0)
        self.assertEqual((second_out / f"{NAME}.zip").read_bytes(), first)

    def test_refuses_bad_version(self):
        for version in ("v1.2.3", "1.2", "1.2.3-rc1", "01.2.3"):
            with self.subTest(version=version):
                self.assert_refused(self.run_build(version=version),
                                     "Version must be stable")

    def test_refuses_untagged_version_by_default(self):
        self.assert_refused(self.run_build(allow_untagged=False, tagged=False),
                            "No tag v")

    def test_tagged_source_builds_without_the_untagged_escape_hatch(self):
        result = self.run_build(allow_untagged=False, tagged=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.out / f"{NAME}.zip").is_file())

    def test_refuses_a_tag_on_another_commit(self):
        sandbox = self._sandbox(tagged=True)
        result = self.run_build(allow_untagged=False, tagged=True,
                                sandbox=sandbox)
        self.assertEqual(result.returncode, 0, result.stderr)
        subprocess.run(["git", "-C", str(sandbox), "commit", "--allow-empty",
                       "-m", "different source"], check=True)
        result = self.run_build(allow_untagged=False, tagged=True,
                                sandbox=sandbox)
        self.assertNotEqual(result.returncode, 0,
                            "a tag on another commit must not label this checkout")
        self.assertIn("does not match", result.stderr)

    def test_build_failure_produces_no_archive(self):
        self.assert_refused(self.run_build(env={"GO_BUILD_FAIL": "1"}),
                            "Build failed")

    def test_refuses_wrong_machine(self):
        self.assert_refused(self.run_build(env={"FAKE_MACHINE": "332"}),
                            "expected Windows x64 PE32+ executable")

    def test_refuses_non_pe_binary(self):
        self.assert_refused(self.run_build(env={"FAKE_NON_PE": "1"}),
                            "not a Windows PE executable")

    def test_refuses_to_overwrite_existing_artifacts(self):
        self.assertEqual(self.run_build().returncode, 0)
        before = (self.out / f"{NAME}.zip").read_bytes()
        result = self.run_build()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Refusing to overwrite", result.stderr)
        self.assertEqual((self.out / f"{NAME}.zip").read_bytes(), before)

    def test_regression_temp_path_with_spaces(self):
        """Regression test for paths with spaces in temp directory."""
        # Create a temporary directory with spaces
        spaced_tmp = self.dir / "temp with spaces"
        spaced_tmp.mkdir()
        spaced_out = spaced_tmp / "dist"
        
        # Run build with spaced path
        result = self.run_build(output=spaced_out)
        self.assertEqual(result.returncode, 0, result.stderr)
        
        # Verify the archive was created
        archive = spaced_out / f"{NAME}.zip"
        self.assertTrue(archive.is_file())
        
        # Verify the log was written correctly
        calls = self.log.read_text()
        self.assertIn("go env GOOS=windows", calls)
        self.assertIn("GOARCH=amd64", calls)
        self.assertIn("CGO_ENABLED=0", calls)


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
