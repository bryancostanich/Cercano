#!/usr/bin/env python3
"""Failure-path tests for scripts/build-macos-release.sh.

These tests never build Go code, never touch a real signing identity, and never
contact Apple. The script under test is driven with a stub `go`, `codesign`,
`lipo`, `otool` and signing script on PATH, so we can assert refusal behavior
and archive layout deterministically.

What this CANNOT prove: that a real toolchain produces a correct binary, that a
real Developer ID signature is valid, or that the archive is notarized. Those
require the real build and the clean-Mac rehearsal.
"""

import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
SCRIPT = REPO / "scripts" / "build-macos-release.sh"
VERSION = "1.2.3"
NAME = f"cercano-{VERSION}-darwin-arm64"


class ReleaseBuildTest(unittest.TestCase):
    def setUp(self):
        if sys.platform != "darwin":
            self.skipTest("release build script targets macOS")
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.out = self.dir / "dist"
        self.bin = self.dir / "stubs"
        self.bin.mkdir()
        self.log = self.dir / "calls.log"
        self._write_stubs()

    def stub(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/bash\n" + body)
        path.chmod(0o755)
        return path

    def _write_stubs(self):
        # `go build -o OUT PKG` writes a fake binary that reports the version
        # passed via -ldflags, so version-mismatch handling is exercised.
        self.stub("go", f"""
echo "go $*" >> {self.log}
out=""
prev=""
version=""
for arg in "$@"; do
  [[ "$prev" == "-o" ]] && out="$arg"
  [[ "$prev" == "-ldflags" ]] && version="${{arg##*=}}"
  prev="$arg"
done
[[ -n "$out" ]] || exit 0
mkdir -p "$(dirname "$out")"
printf '#!/bin/bash\\necho "cercano v%s"\\n' "${{FAKE_VERSION:-$version}}" > "$out"
chmod +x "$out"
""")
        self.stub("lipo", f'echo "lipo $*" >> {self.log}; echo "${{FAKE_ARCH:-arm64}}"')
        self.stub("otool", f'echo "otool $*" >> {self.log}; echo "      cmd LC_BUILD_VERSION"; echo " minos ${{FAKE_MINOS:-12.0}}"')
        self.stub("codesign", f'echo "codesign $*" >> {self.log}; exit ${{FAKE_CODESIGN_EXIT:-0}}')
        self.stub("sign-macos-release.sh", f"""
echo "sign $1" >> {self.log}
[[ "${{FAKE_SIGN_EXIT:-0}}" == 0 ]] || exit "${{FAKE_SIGN_EXIT}}"
for name in cercano cercano-cli; do
  [[ -x "$1/$name" ]] || {{ echo "missing $name" >&2; exit 1; }}
done
""")

    def run_build(self, version=VERSION, env=None, output=None):
        # A stub signing script is injected by copying the release script into a
        # sandbox scripts/ directory alongside the stub signer.
        sandbox = self.dir / "sandbox"
        for relative in ("scripts", "source/server/cmd/cercano", "source/clients/cli"):
            (sandbox / relative).mkdir(parents=True, exist_ok=True)
        shutil.copy2(SCRIPT, sandbox / "scripts" / SCRIPT.name)
        shutil.copy2(self.bin / "sign-macos-release.sh", sandbox / "scripts" / "sign-macos-release.sh")
        (sandbox / "LICENSE").write_text("license text\n")
        environ = {
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "HOME": str(self.dir),
            "CERCANO_CODESIGN_ID": "Developer ID Application: Example (TEAMID)",
            "CERCANO_ALLOW_UNTAGGED": "1",
        }
        environ.update(env or {})
        return subprocess.run(
            ["bash", str(sandbox / "scripts" / SCRIPT.name), version, str(output or self.out)],
            capture_output=True, text=True, env=environ, cwd=str(sandbox))

    def assert_refused(self, result, fragment):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(fragment, result.stderr)
        archives = list(self.out.glob("*.tar.gz")) if self.out.exists() else []
        self.assertEqual(archives, [], "a refused build must not leave an archive")

    def test_builds_signed_archive_with_expected_layout(self):
        result = self.run_build()
        self.assertEqual(result.returncode, 0, result.stderr)
        archive = self.out / f"{NAME}.tar.gz"
        self.assertTrue(archive.is_file())
        with tarfile.open(archive) as tar:
            members = {m.name: m for m in tar.getmembers()}
        self.assertEqual(sorted(members), [
            f"{NAME}/LICENSE", f"{NAME}/README.txt",
            f"{NAME}/bin/cercano", f"{NAME}/bin/cercano-cli",
        ])
        for name in (f"{NAME}/bin/cercano", f"{NAME}/bin/cercano-cli"):
            self.assertTrue(members[name].mode & 0o111, f"{name} must stay executable")
            self.assertTrue(members[name].isfile())
        self.assertFalse(members[f"{NAME}/LICENSE"].mode & 0o111)
        checksum = (self.out / f"{NAME}.tar.gz.sha256").read_text()
        self.assertIn(f"{NAME}.tar.gz", checksum)
        calls = self.log.read_text()
        self.assertIn("sign ", calls, "release archives must be signed")
        # Signing must happen before the archive is built, not after.
        self.assertLess(calls.index("sign "), len(calls))

    def test_rendered_by_formula_renderer(self):
        # The archive this script emits must satisfy the formula renderer's
        # layout rules; otherwise release tooling disagrees with itself.
        self.assertEqual(self.run_build().returncode, 0)
        sys.path.insert(0, str(REPO / "release" / "homebrew"))
        try:
            import render_formula
            archive = self.out / f"{NAME}.tar.gz"
            render_formula.validate_archive(archive, VERSION)
        finally:
            sys.path.pop(0)

    def test_refuses_unsigned_release(self):
        for value in ("", "none", "-"):
            with self.subTest(identity=value):
                self.assert_refused(self.run_build(env={"CERCANO_CODESIGN_ID": value}),
                                    "never unsigned")

    def test_refuses_bad_version(self):
        for version in ("v1.2.3", "1.2", "1.2.3-rc1", "01.2.3"):
            with self.subTest(version=version):
                self.assert_refused(self.run_build(version=version), "Version must be stable")

    def test_refuses_untagged_version_by_default(self):
        self.assert_refused(self.run_build(env={"CERCANO_ALLOW_UNTAGGED": "0"}), "No tag v")

    def test_refuses_wrong_architecture_before_signing(self):
        self.assert_refused(self.run_build(env={"FAKE_ARCH": "x86_64 arm64"}), "expected arm64 only")
        self.assertNotIn("sign ", self.log.read_text())

    def test_refuses_raised_deployment_floor(self):
        self.assert_refused(self.run_build(env={"FAKE_MINOS": "26.0"}), "macOS 12.0 build target")

    def test_refuses_version_mismatch(self):
        self.assert_refused(self.run_build(env={"FAKE_VERSION": "9.9.9"}), "does not contain 1.2.3")

    def test_signing_failure_produces_no_archive(self):
        self.assert_refused(self.run_build(env={"FAKE_SIGN_EXIT": "1"}), "Signing failed")

    def test_post_staging_verification_failure_produces_no_archive(self):
        # codesign --verify runs after staging; a failure there must abort.
        self.assert_refused(self.run_build(env={"FAKE_CODESIGN_EXIT": "1"}),
                            "Signature verification failed after staging")

    def test_refuses_to_overwrite_existing_artifacts(self):
        self.assertEqual(self.run_build().returncode, 0)
        before = (self.out / f"{NAME}.tar.gz").read_bytes()
        result = self.run_build()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Refusing to overwrite", result.stderr)
        self.assertEqual((self.out / f"{NAME}.tar.gz").read_bytes(), before)


if __name__ == "__main__":
    unittest.main()
