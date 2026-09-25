#!/usr/bin/env python3
"""Tests for scripts/verify-macos-release.py.

Every check is proven to fail when its property is violated, so a passing
verification cannot come from a check that never runs. Stub `lipo`, `otool`,
`codesign` executables stand in for the real toolchain; no real
signature, Apple service or network is involved. These tests therefore prove
the gate's logic, not that a real signed archive passes.
"""

import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
SCRIPT = REPO / "scripts" / "verify-macos-release.py"
VERSION = "1.2.3"
TOP = f"cercano-{VERSION}-darwin-arm64"


class VerifyReleaseTest(unittest.TestCase):
    def setUp(self):
        if sys.platform != "darwin":
            self.skipTest("release verification targets macOS")
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.stubs = self.dir / "stubs"
        self.stubs.mkdir()
        self._write_stubs()

    def stub(self, name, body):
        path = self.stubs / name
        path.write_text("#!/bin/bash\n" + body)
        path.chmod(0o755)

    def _write_stubs(self):
        self.stub("lipo", 'echo "${FAKE_ARCH:-arm64}"')
        self.stub("otool", 'echo "      cmd LC_BUILD_VERSION"; echo "     minos ${FAKE_MINOS:-12.0}"')
        # cdhash identifies the exact bytes Apple notarized.
        self.stub("codesign", """
[[ "${FAKE_CODESIGN_EXIT:-0}" == 0 ]] || exit "${FAKE_CODESIGN_EXIT}"
if [[ "$1" == "-d" ]]; then
  echo "Authority=${FAKE_AUTHORITY:-Developer ID Application: Example (TEAMID)}"
  echo "CodeDirectory v=20500 flags=0x${FAKE_FLAGS:-10000}(${FAKE_FLAG_TEXT:-runtime})"
  echo "Timestamp=${FAKE_TIMESTAMP:-1 Jan 2026 at 00:00:00}"
  if [[ "${FAKE_OMIT_CDHASH:-0}" != 1 ]]; then
    case "${@: -1}" in
      *cercano-cli) echo "CDHash=${FAKE_CDHASH_CLI:-bbbb}" ;;
      *) echo "CDHash=${FAKE_CDHASH:-aaaa}" ;;
    esac
  fi
fi
exit 0
""")

    def build_archive(self, name=None, version=VERSION, members=None, binary_body=None):
        top = f"cercano-{version}-darwin-arm64"
        path = self.dir / (name or f"{top}.tar.gz")
        body = binary_body or f'#!/bin/bash\necho "cercano v{version}"\n'
        entries = members if members is not None else [
            (f"{top}/bin/cercano", 0o755, body.encode()),
            (f"{top}/bin/cercano-cli", 0o755, body.encode()),
            (f"{top}/LICENSE", 0o644, b"license"),
            (f"{top}/README.txt", 0o644, b"readme"),
        ]
        with tarfile.open(path, "w:gz") as tar:
            for directory in (top, f"{top}/bin"):
                info = tarfile.TarInfo(directory)
                info.type = tarfile.DIRTYPE
                info.mode = 0o755
                tar.addfile(info)
            for member_name, mode, payload in entries:
                info = tarfile.TarInfo(member_name)
                info.mode = mode
                info.size = len(payload)
                tar.addfile(info, io.BytesIO(payload))
        return path

    def run_verify(self, archive, *args, env=None):
        environ = dict(os.environ)
        environ["PATH"] = f"{self.stubs}:{os.environ['PATH']}"
        environ.update(env or {})
        # Notarization evidence is mandatory unless the caller opts out, so
        # supply default evidence for tests targeting other checks.
        if not any(a.startswith("--notarization-evidence") or a == "--skip-gatekeeper"
                   for a in args):
            args = (*args, "--notarization-evidence", str(self.evidence_dir("auto-evidence")))
        return subprocess.run(
            [sys.executable, str(SCRIPT), str(archive), "--version", VERSION, *args],
            capture_output=True, text=True, env=environ)

    def assert_failed(self, result, fragment):
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn(fragment, result.stderr)
        self.assertIn("Do not publish", result.stderr)

    def test_accepts_valid_archive(self):
        result = self.run_verify(self.build_archive())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Verified layout", result.stdout)
        self.assertNotIn("WARNING", result.stdout)

    def test_checksum_mismatch(self):
        self.assert_failed(self.run_verify(self.build_archive(), "--sha256", "0" * 64),
                           "sha256 mismatch")

    def test_checksum_match_is_reported(self):
        archive = self.build_archive()
        import hashlib
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        result = self.run_verify(archive, "--sha256", digest)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(digest, result.stdout)

    def test_rejects_wrong_architecture(self):
        self.assert_failed(self.run_verify(self.build_archive(), env={"FAKE_ARCH": "x86_64 arm64"}),
                           "expected arm64 only")

    def test_rejects_raised_deployment_target(self):
        self.assert_failed(self.run_verify(self.build_archive(), env={"FAKE_MINOS": "26.0"}),
                           "expected macOS 12.0 build target")

    def test_rejects_invalid_signature(self):
        self.assert_failed(self.run_verify(self.build_archive(), env={"FAKE_CODESIGN_EXIT": "1"}),
                           "signature verification failed")

    def test_rejects_non_developer_id_authority(self):
        self.assert_failed(self.run_verify(self.build_archive(), env={"FAKE_AUTHORITY": "Apple Development: Someone (X)"}),
                           "not signed with a Developer ID Application identity")

    def test_rejects_missing_hardened_runtime(self):
        self.assert_failed(self.run_verify(self.build_archive(), env={"FAKE_FLAG_TEXT": "adhoc"}),
                           "hardened runtime is not enabled")

    def test_rejects_missing_timestamp(self):
        self.assert_failed(self.run_verify(self.build_archive(), env={"FAKE_TIMESTAMP": "none"}),
                           "missing secure timestamp")

    def evidence_dir(self, name="evidence", status="Accepted", job="1234-abcd",
                     hashes=("aaaa", "bbbb"), log_status="Accepted", log_job=None,
                     omit_log=False):
        directory = self.dir / name
        directory.mkdir()
        (directory / "accepted.json").write_text(
            json.dumps({"status": status, "id": job}))
        if not omit_log:
            (directory / "log-stdout.txt").write_text(json.dumps({
                "jobId": log_job or job, "status": log_status,
                "ticketContents": [{"path": f"x/{i}", "cdhash": h}
                                   for i, h in enumerate(hashes)],
            }))
        return directory

    def test_accepts_binaries_present_in_apples_ticket(self):
        result = self.run_verify(self.build_archive(), "--notarization-evidence",
                                 str(self.evidence_dir()))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("1234-abcd", result.stdout)
        self.assertNotIn("WARNING", result.stdout)

    def test_rejects_binary_absent_from_apples_ticket(self):
        # Signed identically, but these bytes were never notarized.
        self.assert_failed(
            self.run_verify(self.build_archive(), "--notarization-evidence",
                            str(self.evidence_dir(hashes=("aaaa",)))),
            "is not in Apple's Accepted notarization ticket")

    def test_requires_evidence_to_claim_notarization(self):
        environ = dict(os.environ)
        environ["PATH"] = f"{self.stubs}:{os.environ['PATH']}"
        result = subprocess.run(
            [sys.executable, str(SCRIPT), str(self.build_archive()), "--version", VERSION],
            capture_output=True, text=True, env=environ)
        self.assert_failed(result, "cannot be verified without --notarization-evidence")

    def test_rejects_log_for_a_different_submission(self):
        self.assert_failed(
            self.run_verify(self.build_archive(), "--notarization-evidence",
                            str(self.evidence_dir(log_job="other-job"))),
            "different submission")

    def test_rejects_unaccepted_apple_log(self):
        self.assert_failed(
            self.run_verify(self.build_archive(), "--notarization-evidence",
                            str(self.evidence_dir(log_status="Invalid"))),
            "log does not record an Accepted status")

    def test_rejects_missing_apple_log(self):
        self.assert_failed(
            self.run_verify(self.build_archive(), "--notarization-evidence",
                            str(self.evidence_dir(omit_log=True))),
            "no Apple notarization log")

    def test_rejects_ticket_without_binaries(self):
        self.assert_failed(
            self.run_verify(self.build_archive(), "--notarization-evidence",
                            str(self.evidence_dir(hashes=()))),
            "lists no notarized binaries")

    def test_rejects_missing_cdhash_in_signature(self):
        self.assert_failed(
            self.run_verify(self.build_archive(), "--notarization-evidence",
                            str(self.evidence_dir()), env={"FAKE_OMIT_CDHASH": "1"}),
            "no code directory hash")

    def test_skip_warns_instead_of_claiming_notarization(self):
        result = self.run_verify(self.build_archive(), "--skip-gatekeeper")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("notarization UNVERIFIED", result.stdout)
        self.assertIn("NOT verified", result.stdout)

    def test_rejects_version_mismatch(self):
        archive = self.build_archive(binary_body='#!/bin/bash\necho "cercano v9.9.9"\n')
        self.assert_failed(self.run_verify(archive), "does not contain 1.2.3")

    def test_rejects_incomplete_archive(self):
        members = [
            (f"{TOP}/bin/cercano", 0o755, b"#!/bin/bash\necho cercano v1.2.3\n"),
            (f"{TOP}/LICENSE", 0o644, b"license"),
        ]
        self.assert_failed(self.run_verify(self.build_archive(members=members)), "missing")

    def test_rejects_non_executable_binary(self):
        body = f'#!/bin/bash\necho "cercano v{VERSION}"\n'.encode()
        members = [
            (f"{TOP}/bin/cercano", 0o644, body),
            (f"{TOP}/bin/cercano-cli", 0o755, body),
            (f"{TOP}/LICENSE", 0o644, b"license"),
            (f"{TOP}/README.txt", 0o644, b"readme"),
        ]
        self.assert_failed(self.run_verify(self.build_archive(members=members)), "not executable")

    def test_rejects_missing_archive(self):
        self.assert_failed(self.run_verify(self.dir / "absent.tar.gz"), "not a regular file")

    def test_rejects_unaccepted_submission_record(self):
        self.assert_failed(
            self.run_verify(self.build_archive(), "--notarization-evidence",
                            str(self.evidence_dir(status="Invalid"))),
            "does not record an Accepted status")


if __name__ == "__main__":
    unittest.main()
