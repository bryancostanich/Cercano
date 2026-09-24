#!/usr/bin/env python3
"""Render the Cercano Homebrew formula from a verified local release archive.

Trust boundary: this tool checks archive *layout* and that the archive's bytes
hash to the expected SHA-256. It does NOT verify code signatures, notarization,
Mach-O architecture, or the version the binaries actually report. Those belong
to the signing, notarization and artifact-verification steps. Rendering a
formula here does not make an archive releasable, and this tool never downloads,
uploads, installs, executes archive contents, or publishes to the tap.
"""

import argparse
import hashlib
import os
import posixpath
import re
import sys
import tarfile
from pathlib import Path

PLACEHOLDERS = ("@RELEASE_URL@", "@VERSION@", "@SHA256@")
# Must match scripts/build-macos-unsigned.sh's staging layout.
EXECUTABLES = ("bin/cercano", "bin/cercano-cli")
METADATA = ("LICENSE", "README.txt")
URL_TEMPLATE = "https://github.com/bryancostanich/Cercano/releases/download/v{version}/{name}"
VERSION_PATTERN = re.compile(r"\A(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
SHA256_PATTERN = re.compile(r"\A[0-9a-f]{64}\Z")


class ValidationError(Exception):
    """A release input failed validation; no formula is written."""


def parse_version(value):
    if not VERSION_PATTERN.match(value):
        raise ValidationError(
            f"version must be stable X.Y.Z without a leading v or leading zeros: {value!r}")
    return value


def parse_digest(value):
    if not SHA256_PATTERN.match(value):
        raise ValidationError("sha256 must be 64 lowercase hexadecimal characters")
    return value


def hash_archive(path):
    """Hash the bytes before opening them as a tar, so an unexpected archive is
    rejected before any parsing of its structure."""
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_archive(path, version):
    """Validate layout without extracting or executing anything."""
    expected_name = f"cercano-{version}-darwin-arm64.tar.gz"
    if path.name != expected_name:
        raise ValidationError(f"archive must be named {expected_name}, got {path.name}")
    top = f"cercano-{version}-darwin-arm64"
    expected_files = {f"{top}/{name}" for name in EXECUTABLES}
    allowed_files = expected_files | {f"{top}/{name}" for name in METADATA}
    allowed_dirs = {top, f"{top}/bin"}

    seen = set()
    found_files = set()
    with tarfile.open(path, "r:gz") as archive:
        for member in archive:
            name = member.name
            if name.startswith("/") or name.startswith("./") or "\\" in name:
                raise ValidationError(f"unsupported member path: {name!r}")
            normalized = posixpath.normpath(name)
            if normalized != name or normalized in (".", "..") or normalized.startswith("../"):
                raise ValidationError(f"unsupported member path: {name!r}")
            if normalized in seen:
                raise ValidationError(f"duplicate member: {normalized}")
            seen.add(normalized)
            if member.isdir():
                if normalized not in allowed_dirs:
                    raise ValidationError(f"unexpected directory: {normalized}")
                continue
            if not member.isfile():
                raise ValidationError(f"unsupported member type: {normalized}")
            if normalized not in allowed_files:
                raise ValidationError(f"unexpected file: {normalized}")
            executable = bool(member.mode & 0o111)
            if normalized in expected_files and not executable:
                raise ValidationError(f"{normalized} is not executable")
            if normalized not in expected_files and executable:
                raise ValidationError(f"unexpected executable payload: {normalized}")
            found_files.add(normalized)

    missing = expected_files - found_files
    if missing:
        raise ValidationError(f"archive is missing: {', '.join(sorted(missing))}")


def render(template, version, archive_name, digest):
    for placeholder in PLACEHOLDERS:
        if template.count(placeholder) != 1:
            raise ValidationError(
                f"template must contain exactly one {placeholder}; refusing to render")
    values = {
        "@RELEASE_URL@": URL_TEMPLATE.format(version=version, name=archive_name),
        "@VERSION@": version,
        "@SHA256@": digest,
    }
    rendered = template
    for placeholder, value in values.items():
        rendered = rendered.replace(placeholder, value)
    leftover = [text for text in re.findall(r"@[A-Z0-9_]+@", rendered)]
    if leftover:
        raise ValidationError(f"unresolved placeholders remain: {', '.join(sorted(set(leftover)))}")
    return rendered


def write_new_file(path, content):
    """Create the output exclusively; never overwrite an existing formula."""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
    with os.fdopen(fd, "w") as handle:
        handle.write(content)


def build(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--version", required=True)
    parser.add_argument("--archive", required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--template", default=str(Path(__file__).with_name("cercano.rb.in")))
    args = parser.parse_args(argv)

    version = parse_version(args.version)
    expected_digest = parse_digest(args.sha256)
    archive = Path(args.archive)
    if not archive.is_file():
        raise ValidationError(f"archive is not a regular file: {archive}")
    output = Path(args.output)
    if output.exists():
        raise ValidationError(f"output already exists: {output}")

    actual_digest = hash_archive(archive)
    if actual_digest != expected_digest:
        raise ValidationError(
            f"archive sha256 mismatch: expected {expected_digest}, got {actual_digest}")
    validate_archive(archive, version)

    template_path = Path(args.template)
    if not template_path.is_file():
        raise ValidationError(f"template not found: {template_path}")
    rendered = render(template_path.read_text(), version, archive.name, expected_digest)
    write_new_file(output, rendered)
    return output


def main(argv=None):
    try:
        output = build(argv)
    except ValidationError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    print(f"Rendered {output}")
    print("Signature, notarization, architecture and reported version are NOT checked here.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
