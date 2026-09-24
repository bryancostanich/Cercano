#!/usr/bin/env python3
"""Verify a finished macOS release archive before it is published.

This inspects the archive as a consumer receives it, rather than trusting the
machine that produced it: layout, checksum, architecture, deployment target,
Developer ID signature with hardened runtime and secure timestamp, executable
permissions, and the version each binary reports.

Notarization honesty: a bare Mach-O executable cannot carry a stapled ticket
(`xcrun stapler` supports bundles, disk images and installer packages). So
notarization is confirmed the way Gatekeeper does it, by assessing the binary
against Apple's online records with `spctl`. That requires network access. Use
--skip-gatekeeper offline, which reports notarization as UNVERIFIED rather than
claiming success. Accepted-submission evidence from notarize-macos-local.py can
be supplied with --notarization-evidence; it is corroborating provenance, not
proof that these bytes are notarized.

Nothing here signs, notarizes, uploads, publishes or mutates the archive.
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "release" / "homebrew"))
import render_formula  # noqa: E402  (path set above so release tooling agrees on layout)

EXECUTABLES = ("bin/cercano", "bin/cercano-cli")
EXPECTED_MINOS = "12.0"


class Failure(Exception):
    """A release check failed; the archive must not be published."""


def run(argv, timeout=60):
    return subprocess.run(argv, capture_output=True, text=True, timeout=timeout)


def check(condition, message):
    if not condition:
        raise Failure(message)


def verify_checksum(archive, expected):
    digest = hashlib.sha256()
    with open(archive, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    actual = digest.hexdigest()
    if expected is not None:
        check(actual == expected, f"sha256 mismatch: expected {expected}, got {actual}")
    return actual


def extract(archive, version, destination):
    """Extract only after layout validation, and only regular files."""
    render_formula.validate_archive(archive, version)
    top = f"cercano-{version}-darwin-arm64"
    with tarfile.open(archive, "r:gz") as tar:
        for member in tar.getmembers():
            if not member.isfile():
                continue
            target = destination / member.name
            check(target.resolve().is_relative_to(destination.resolve()),
                  f"member escapes extraction directory: {member.name}")
            target.parent.mkdir(parents=True, exist_ok=True)
            source = tar.extractfile(member)
            check(source is not None, f"unreadable member: {member.name}")
            target.write_bytes(source.read())
            target.chmod(0o755 if member.mode & 0o111 else 0o644)
    return destination / top


def verify_architecture(binary):
    result = run(["lipo", "-archs", str(binary)])
    check(result.returncode == 0, f"cannot inspect architecture: {binary.name}")
    archs = result.stdout.split()
    check(archs == ["arm64"], f"{binary.name}: expected arm64 only, got {' '.join(archs) or 'nothing'}")


def verify_deployment_target(binary):
    result = run(["otool", "-l", str(binary)])
    check(result.returncode == 0, f"cannot read load commands: {binary.name}")
    found = None
    in_build_version = False
    for line in result.stdout.splitlines():
        fields = line.split()
        if fields[-1:] == ["LC_BUILD_VERSION"]:
            in_build_version = True
        elif in_build_version and fields[:1] == ["minos"]:
            found = fields[1]
            break
    check(found == EXPECTED_MINOS,
          f"{binary.name}: expected macOS {EXPECTED_MINOS} build target, got {found or 'none'}")


def verify_signature(binary):
    result = run(["codesign", "--verify", "--strict", str(binary)])
    check(result.returncode == 0,
          f"signature verification failed: {binary.name}\n{result.stderr.strip()}")
    result = run(["codesign", "-d", "--verbose=4", str(binary)])
    details = result.stdout + "\n" + result.stderr
    check(result.returncode == 0, f"cannot read signature details: {binary.name}")
    check(bool(re.search(r"^Authority=Developer ID Application: .+", details, re.M)),
          f"{binary.name}: not signed with a Developer ID Application identity")
    check(bool(re.search(r"^CodeDirectory[^\n]*flags=0x[0-9a-fA-F]+\([^\n)]*\bruntime\b", details, re.M)),
          f"{binary.name}: hardened runtime is not enabled")
    timestamp = re.search(r"^Timestamp=(.+)$", details, re.M)
    check(bool(timestamp) and timestamp[1].strip().lower() not in ("", "none"),
          f"{binary.name}: missing secure timestamp")


def verify_notarization(binary):
    """Gatekeeper assessment; consults Apple's online notarization records."""
    result = run(["spctl", "--assess", "--type", "exec", "--verbose=4", str(binary)], timeout=120)
    details = result.stdout + "\n" + result.stderr
    check(result.returncode == 0, f"{binary.name}: Gatekeeper rejected the binary\n{details.strip()}")
    check("source=Notarized Developer ID" in details,
          f"{binary.name}: accepted but not as a notarized Developer ID binary\n{details.strip()}")


def verify_version(binary, version):
    """Run only after the Developer ID signature has been verified."""
    result = run([str(binary), "--version"], timeout=30)
    check(result.returncode == 0, f"{binary.name} does not report a version")
    reported = (result.stdout + result.stderr).strip()
    check(version in reported, f"{binary.name} reports {reported!r}, which does not contain {version}")


def load_evidence(path, version):
    accepted = Path(path) / "accepted.json"
    check(accepted.is_file(), f"no accepted.json in notarization evidence: {path}")
    payload = json.loads(accepted.read_text())
    check(payload.get("status") == "Accepted", "notarization evidence does not record an Accepted status")
    check(bool(payload.get("id")), "notarization evidence has no submission id")
    return payload["id"]


def verify(archive, version, expected_sha256=None, skip_gatekeeper=False, evidence=None):
    check(archive.is_file(), f"archive is not a regular file: {archive}")
    notes = []
    digest = verify_checksum(archive, expected_sha256)
    submission = load_evidence(evidence, version) if evidence else None

    with tempfile.TemporaryDirectory(prefix="cercano-verify-") as tmp:
        root = extract(archive, version, Path(tmp))
        for relative in EXECUTABLES:
            binary = root / relative
            check(binary.is_file() and not binary.is_symlink(), f"missing executable: {relative}")
            check(os.access(binary, os.X_OK), f"not executable: {relative}")
            verify_architecture(binary)
            verify_deployment_target(binary)
            verify_signature(binary)
            if skip_gatekeeper:
                notes.append(f"{binary.name}: notarization UNVERIFIED (Gatekeeper check skipped)")
            else:
                verify_notarization(binary)
            verify_version(binary, version)
        for name in ("LICENSE", "README.txt"):
            check((root / name).is_file(), f"missing required file: {name}")

    return {"sha256": digest, "submission": submission, "notes": notes}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("archive", type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--sha256", help="expected archive digest; recommended for downloaded artifacts")
    parser.add_argument("--skip-gatekeeper", action="store_true",
                        help="offline mode; reports notarization as UNVERIFIED instead of checking it")
    parser.add_argument("--notarization-evidence", help="output directory from notarize-macos-local.py")
    args = parser.parse_args(argv)

    try:
        version = render_formula.parse_version(args.version)
        expected = render_formula.parse_digest(args.sha256) if args.sha256 else None
        result = verify(args.archive, version, expected, args.skip_gatekeeper, args.notarization_evidence)
    except (Failure, render_formula.ValidationError, OSError, ValueError,
            subprocess.SubprocessError) as error:
        print(f"FAILED: {error}", file=sys.stderr)
        print("Do not publish this archive.", file=sys.stderr)
        return 1

    print(f"Archive:  {args.archive}")
    print(f"sha256:   {result['sha256']}")
    if result["submission"]:
        print(f"Notarization submission on record: {result['submission']}")
    for note in result["notes"]:
        print(f"WARNING: {note}")
    print("Verified layout, architecture, deployment target, Developer ID signature,")
    print("hardened runtime, secure timestamp, permissions and reported version.")
    if result["notes"]:
        print("Notarization was NOT verified in this run; re-run online before publishing.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
