#!/usr/bin/env python3
"""Build and package the experimental, UNSIGNED Windows x64 artifact.

Produces, in the output directory:

  cercano-<version>-windows-x64.zip
  cercano-<version>-windows-x64.zip.sha256

Layout inside the zip:

  cercano-<version>-windows-x64/bin/cercano.exe
  cercano-<version>-windows-x64/bin/cercano-cli.exe
  cercano-<version>-windows-x64/LICENSE
  cercano-<version>-windows-x64/README.txt

This artifact is EXPLICITLY UNSIGNED. There is no Windows code-signing
infrastructure yet; this is an experimental convenience artifact, not a
supported Windows release. See docs/windows-artifact.md for the status of
Windows support and the remaining checklist for a supported release.

Both binaries are built with CGO disabled (CGO_ENABLED=0): the SQLite driver
is modernc.org/sqlite (pure Go), the credential keyring uses wincred through
99designs/keyring (pure Go), and no reachable dependency requires cgo, so no
cross toolchain is needed. This was confirmed by cross-compiling both
binaries for windows/amd64 with CGO_ENABLED=0 before this script existed.

The script fails closed, and a refused or failed build leaves no archive
behind:

- Unstable or untagged versions (CERCANO_ALLOW_UNTAGGED=1 for local dry runs).
- A v<version> tag that does not match HEAD.
- A `go build` failure, or a produced binary that is not a Windows PE image.
- An archive or checksum that already exists (no silent replacement).

It never signs, notarizes, pushes, publishes or contacts GitHub.
"""

import hashlib
import os
import re
import shutil
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

STABLE_VERSION = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
# Fixed member timestamp (the zip format's epoch) so archives of the same
# inputs are byte-for-byte reproducible.
ZIP_TIMESTAMP = (1980, 1, 1, 0, 0, 0)

README_TEMPLATE = """Cercano {version} for Windows x64 (EXPERIMENTAL, UNSIGNED)

This is an experimental, unsigned Windows build of Cercano. It is NOT a
supported Windows release. See docs/windows-artifact.md for the status of
Windows support and the checklist still owed for a supported release.

Contents:
  bin/cercano.exe      agent entrypoint
  bin/cercano-cli.exe  terminal client

This archive is NOT code signed. Windows will show security warnings
(SmartScreen / unknown publisher). Verify the published SHA-256 checksum
before use, and treat unsigned binaries from untrusted sources as
untrusted.

The release pipeline verifies that both binaries report exactly this
version via --version on a Windows host, with isolated user directories,
and runs the Windows-native helper unit tests. It does NOT verify more
than that.

Not included or verified:
  - Code signing of any kind
  - Full Windows test coverage, an installer, or a documented OS support
    floor
  - Models, third-party runtimes, provider credentials, or downloads

Homepage: https://github.com/bryancostanich/Cercano
"""


def fail(message):
    print(f"Error: {message}", file=sys.stderr)
    sys.exit(1)


def run_git(repo_root, *args):
    result = subprocess.run(["git", *args], cwd=str(repo_root),
                            capture_output=True, text=True)
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def build(module_dir, package, output, version):
    env = dict(os.environ)
    # Explicit platform flags: the artifact is windows/amd64 only, and the
    # pure-Go dependency set means cgo is never needed for it.
    env["GOOS"] = "windows"
    env["GOARCH"] = "amd64"
    env["CGO_ENABLED"] = "0"
    result = subprocess.run(
        ["go", "build", "-trimpath", "-buildvcs=false",
         "-ldflags", f"-X main.version={version}",
         "-o", str(output), package],
        cwd=str(module_dir), env=env, capture_output=True, text=True)
    if result.returncode != 0:
        print(result.stderr, file=sys.stderr, end="")
        print(result.stdout, file=sys.stderr, end="")
        fail(f"Build failed: {output}")
    print(f"Built {package} -> {output}")


def verify_pe(binary: Path) -> None:
    """Require an executable AMD64 PE32+ header, not just a DOS MZ stub."""
    import struct
    with open(binary, "rb") as f:
        dos = f.read(64)
        if len(dos) != 64 or dos[:2] != b"MZ":
            fail(f"{binary.name}: not a Windows PE executable")
        offset = struct.unpack_from("<I", dos, 60)[0]
        if offset < 64 or offset > binary.stat().st_size - 26:
            fail(f"{binary.name}: invalid PE header offset")
        f.seek(offset)
        header = f.read(26)
    machine = struct.unpack_from("<H", header, 4)[0]
    flags = struct.unpack_from("<H", header, 22)[0]
    magic = struct.unpack_from("<H", header, 24)[0]
    if header[:4] != b"PE\0\0" or machine != 0x8664 or magic != 0x20b or not flags & 2 or flags & 0x2000:
        fail(f"{binary.name}: expected Windows x64 PE32+ executable")


def write_zip(archive, entries):
    """Write entries [(arcname, source_path, mode)] deterministically."""
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as zf:
        for arcname, source, mode in entries:
            info = zipfile.ZipInfo(arcname, date_time=ZIP_TIMESTAMP)
            # create_system=3 (unix) so stored modes are honored by consumers.
            info.create_system = 3
            info.external_attr = (0o100000 | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            zf.writestr(info, source.read_bytes())


def verify_zip(archive, entries):
    """Re-open the archive and check layout, integrity and staged bytes.

    A zip that does not contain exactly the expected members — or whose
    members do not match the staged binaries byte for byte — must never be
    released as a false success.
    """
    expected = sorted(arcname for arcname, _, _ in entries)
    staged = {arcname: source for arcname, source, _ in entries}
    with zipfile.ZipFile(archive) as zf:
        names = sorted(zf.namelist())
        if names != expected:
            fail(f"Archive layout mismatch: {names} != {expected}")
        if zf.testzip() is not None:
            fail(f"Archive is corrupt: {archive}")
        for arcname in expected:
            if zf.read(arcname) != staged[arcname].read_bytes():
                fail(f"Archived bytes differ from staged file: {arcname}")


def main():
    if len(sys.argv) != 3:
        fail("Usage: build-windows-release.py <VERSION> <OUTPUT_DIR>")
    version, output_dir_arg = sys.argv[1], sys.argv[2]

    # Stable versions only, mirroring the macOS release script: the archive
    # name, the published checksum and the source tag must all agree.
    if not STABLE_VERSION.match(version):
        fail(f"Version must be stable X.Y.Z without a leading v or leading "
             f"zeros: {version}")

    repo_root = Path(__file__).resolve().parents[1]

    # The version must come from a real tag so the artifact and the source
    # revision agree. CERCANO_ALLOW_UNTAGGED=1 is for local dry runs only.
    if os.environ.get("CERCANO_ALLOW_UNTAGGED") != "1":
        tag_commit = run_git(repo_root, "rev-parse", "--verify",
                             f"refs/tags/v{version}^{{commit}}")
        if tag_commit is None:
            fail(f"No tag v{version} in this repository. Tag the release, or "
                 f"set CERCANO_ALLOW_UNTAGGED=1 for a local dry run.")
        head_commit = run_git(repo_root, "rev-parse", "--verify", "HEAD^{commit}")
        if tag_commit != head_commit:
            fail(f"HEAD does not match v{version}; refusing to label "
                 f"different source as this release.")

    output_dir = Path(output_dir_arg)
    output_dir.mkdir(parents=True, exist_ok=True)
    output_dir = output_dir.resolve()
    name = f"cercano-{version}-windows-x64"
    final_archive = output_dir / f"{name}.zip"
    checksum_file = output_dir / f"{name}.zip.sha256"
    # Never silently replace an artifact someone may already have published.
    for existing in (final_archive, checksum_file):
        if existing.exists():
            fail(f"Refusing to overwrite existing artifact: {existing}")

    temp_dir = Path(tempfile.mkdtemp())
    try:
        stage_root = temp_dir / name
        (stage_root / "bin").mkdir(parents=True)

        print("Building agent...")
        build(repo_root / "source" / "server", "./cmd/cercano",
              stage_root / "bin" / "cercano.exe", version)
        print("Building terminal client...")
        build(repo_root / "source" / "clients" / "cli", ".",
              stage_root / "bin" / "cercano-cli.exe", version)

        # Verify both binaries before packaging either: a non-Windows binary
        # or an empty output must fail here, not on a user's machine.
        for exe in ("cercano.exe", "cercano-cli.exe"):
            verify_pe(stage_root / "bin" / exe)

        shutil.copy2(repo_root / "LICENSE", stage_root / "LICENSE")
        (stage_root / "README.txt").write_text(README_TEMPLATE.format(version=version))

        entries = [
            (f"{name}/LICENSE", stage_root / "LICENSE", 0o644),
            (f"{name}/README.txt", stage_root / "README.txt", 0o644),
            (f"{name}/bin/cercano.exe", stage_root / "bin" / "cercano.exe", 0o755),
            (f"{name}/bin/cercano-cli.exe", stage_root / "bin" / "cercano-cli.exe", 0o755),
        ]
        temp_archive = temp_dir / f"{name}.zip"
        write_zip(temp_archive, entries)
        verify_zip(temp_archive, entries)

        shutil.move(str(temp_archive), str(final_archive))
        digest = hashlib.sha256(final_archive.read_bytes()).hexdigest()
        # shasum-compatible format so `sha256sum -c` / `shasum -c` both work.
        checksum_file.write_text(f"{digest}  {final_archive.name}\n")
    finally:
        shutil.rmtree(temp_dir, ignore_errors=True)

    print(f"Archive:  {final_archive}")
    print(f"Checksum: {checksum_file}")
    print("UNSIGNED, EXPERIMENTAL Windows artifact — not a supported release.")


if __name__ == "__main__":
    main()
