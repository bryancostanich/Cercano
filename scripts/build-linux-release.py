#!/usr/bin/env python3
"""Build and package the experimental, UNSIGNED Linux x86_64 artifact.

Produces, in the output directory:

  cercano-<version>-linux-x64.tar.gz
  cercano-<version>-linux-x64.tar.gz.sha256

Layout inside the tarball:

  cercano-<version>-linux-x64/bin/cercano
  cercano-<version>-linux-x64/bin/cercano-cli
  cercano-<version>-linux-x64/LICENSE
  cercano-<version>-linux-x64/README.txt

This artifact is EXPLICITLY UNSIGNED. There is no Linux signing
infrastructure yet; this is an experimental convenience artifact, not a
supported Linux release. See docs/linux-artifact.md for the status of
Linux support and the remaining checklist for a supported release.

Both binaries are built with CGO disabled (CGO_ENABLED=0): the SQLite driver
is modernc.org/sqlite (pure Go), the credential keyring talks to the Linux
Secret Service through 99designs/keyring backed by godbus (pure Go), and no
reachable dependency requires cgo, so no cross toolchain is needed. This was
confirmed by cross-compiling both binaries for linux/amd64 with
CGO_ENABLED=0 (ELF 64-bit LSB, x86-64, statically linked) before this script
existed.

The script fails closed, and a refused or failed build leaves no archive
behind:

- Unstable or untagged versions (CERCANO_ALLOW_UNTAGGED=1 for local dry runs).
- A v<version> tag that does not match HEAD.
- A `go build` failure, or a produced binary that is not an ELF64
  little-endian x86-64 executable with regular executable permissions.
- An archive or checksum that already exists (no silent replacement).

It never signs, notarizes, pushes, publishes or contacts GitHub.
"""

import gzip
import hashlib
import os
import re
import shutil
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

STABLE_VERSION = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
# Fixed member metadata so archives of the same inputs are byte-for-byte
# reproducible: epoch mtime, root ownership, no gzip timestamp.
TAR_EPOCH = 0
GZIP_EPOCH = 0

README_TEMPLATE = """Cercano {version} for Linux x86_64 (EXPERIMENTAL, UNSIGNED)

This is an experimental, unsigned Linux build of Cercano. It is NOT a
supported Linux release. See docs/linux-artifact.md for the status of
Linux support and the checklist still owed for a supported release.

Contents:
  bin/cercano      agent entrypoint
  bin/cercano-cli  terminal client

This archive is NOT signed. There is no Linux code-signing
infrastructure yet. Verify the published SHA-256 checksum before use,
and treat unsigned binaries from untrusted sources as untrusted.

The release pipeline verifies that both binaries report exactly this
version via --version on a Linux host, with isolated user directories,
and runs hermetic Linux-native unit tests. It does NOT verify more
than that.

Not included or verified:
  - Code signing of any kind
  - Full Linux test coverage, a package (deb/rpm/flatpak) story, or a
    documented distribution support floor
  - Models, third-party runtimes, provider credentials, or downloads

Homepage: https://github.com/cercano-ai/Cercano
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
    # Explicit platform flags: the artifact is linux/amd64 only, and the
    # pure-Go dependency set means cgo is never needed for it.
    env["GOOS"] = "linux"
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


def verify_elf(binary: Path) -> None:
    """Require an ELF64, little-endian, x86-64 executable header.

    Go with CGO_ENABLED=0 produces a static ET_EXEC (or ET_DYN PIE) image;
    a truncated file, a script, or another architecture must fail here.
    """
    with open(binary, "rb") as f:
        header = f.read(20)
    if len(header) < 20 or header[:4] != b"\x7fELF":
        fail(f"{binary.name}: not a Linux ELF executable")
    ei_class, ei_data = header[4], header[5]
    if ei_class != 2:
        fail(f"{binary.name}: expected 64-bit ELF (ELFCLASS64), got class {ei_class}")
    if ei_data != 1:
        fail(f"{binary.name}: expected little-endian ELF, got EI_DATA {ei_data}")
    e_type, e_machine = struct.unpack_from("<HH", header, 16)
    if e_machine != 0x3E:
        fail(f"{binary.name}: expected x86-64 ELF (e_machine 62), got {e_machine}")
    if e_type not in (2, 3):  # ET_EXEC, ET_DYN
        fail(f"{binary.name}: expected an ELF executable type, got e_type {e_type}")


def verify_permissions(binary: Path, expected_mode: int) -> None:
    """Require a regular file with exactly the expected permission mode."""
    info = binary.stat()
    if not stat.S_ISREG(info.st_mode):
        fail(f"{binary.name}: not a regular file")
    if stat.S_IMODE(info.st_mode) != expected_mode:
        fail(f"{binary.name}: expected mode {oct(expected_mode)}, "
             f"got {oct(stat.S_IMODE(info.st_mode))}")


def make_tarinfo(name, mode, is_dir):
    info = tarfile.TarInfo(name)
    info.mode = mode
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    info.mtime = TAR_EPOCH
    info.type = tarfile.DIRTYPE if is_dir else tarfile.REGTYPE
    info.size = 0
    return info


def write_tar(archive, entries):
    """Write entries [(arcname, source_path_or_None, mode)] deterministically."""
    with open(archive, "wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw,
                           mtime=GZIP_EPOCH) as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.GNU_FORMAT) as tf:
                for arcname, source, mode in entries:
                    if source is None:  # directory entry
                        tf.addfile(make_tarinfo(arcname.rstrip("/") + "/", mode, True))
                        continue
                    info = make_tarinfo(arcname, mode, False)
                    info.size = source.stat().st_size
                    with open(source, "rb") as f:
                        tf.addfile(info, f)


def verify_tar(archive, entries):
    """Re-open the archive and check layout, integrity and staged bytes.

    A tarball that does not contain exactly the expected members — or whose
    members do not match the staged binaries byte for byte — must never be
    released as a false success.
    """
    expected = sorted(arcname for arcname, _, _ in entries)
    staged = {arcname: source for arcname, source, _ in entries}
    modes = {arcname: mode for arcname, _, mode in entries}
    with tarfile.open(archive, "r:gz") as tf:
        members = {m.name.rstrip("/"): m for m in tf.getmembers()}
        if sorted(members) != expected:
            fail(f"Archive layout mismatch: {sorted(members)} != {expected}")
        for arcname in expected:
            member = members[arcname]
            if stat.S_IMODE(member.mode) != modes[arcname]:
                fail(f"Archive member {arcname} has mode {oct(member.mode)}, "
                     f"expected {oct(modes[arcname])}")
            if staged[arcname] is None:
                if not member.isdir():
                    fail(f"Archive member {arcname} is not a directory")
                continue
            if not member.isfile():
                fail(f"Archive member {arcname} is not a regular file")
            if tf.extractfile(member).read() != staged[arcname].read_bytes():
                fail(f"Archived bytes differ from staged file: {arcname}")


def main():
    if len(sys.argv) != 3:
        fail("Usage: build-linux-release.py <VERSION> <OUTPUT_DIR>")
    version, output_dir_arg = sys.argv[1], sys.argv[2]

    # Stable versions only, mirroring the macOS and Windows release scripts:
    # the archive name, the published checksum and the source tag must all agree.
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
    name = f"cercano-{version}-linux-x64"
    final_archive = output_dir / f"{name}.tar.gz"
    checksum_file = output_dir / f"{name}.tar.gz.sha256"
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
              stage_root / "bin" / "cercano", version)
        print("Building terminal client...")
        build(repo_root / "source" / "clients" / "cli", ".",
              stage_root / "bin" / "cercano-cli", version)

        # Verify both binaries before packaging either: a non-Linux binary,
        # another architecture or an empty output must fail here, not on a
        # user's machine.
        for exe in ("cercano", "cercano-cli"):
            verify_elf(stage_root / "bin" / exe)

        shutil.copy2(repo_root / "LICENSE", stage_root / "LICENSE")
        (stage_root / "README.txt").write_text(README_TEMPLATE.format(version=version))

        # Regular files, binaries executable, documents readable only.
        os.chmod(stage_root / "bin" / "cercano", 0o755)
        os.chmod(stage_root / "bin" / "cercano-cli", 0o755)
        os.chmod(stage_root / "LICENSE", 0o644)
        os.chmod(stage_root / "README.txt", 0o644)
        for exe in ("cercano", "cercano-cli"):
            verify_permissions(stage_root / "bin" / exe, 0o755)
        for doc in ("LICENSE", "README.txt"):
            verify_permissions(stage_root / doc, 0o644)

        entries = [
            (f"{name}", None, 0o755),
            (f"{name}/bin", None, 0o755),
            (f"{name}/LICENSE", stage_root / "LICENSE", 0o644),
            (f"{name}/README.txt", stage_root / "README.txt", 0o644),
            (f"{name}/bin/cercano", stage_root / "bin" / "cercano", 0o755),
            (f"{name}/bin/cercano-cli", stage_root / "bin" / "cercano-cli", 0o755),
        ]
        temp_archive = temp_dir / f"{name}.tar.gz"
        write_tar(temp_archive, entries)
        verify_tar(temp_archive, entries)

        shutil.move(str(temp_archive), str(final_archive))
        digest = hashlib.sha256(final_archive.read_bytes()).hexdigest()
        # shasum-compatible format so `sha256sum -c` / `shasum -c` both work.
        checksum_file.write_text(f"{digest}  {final_archive.name}\n")
    finally:
        shutil.rmtree(temp_dir, ignore_errors=True)

    print(f"Archive:  {final_archive}")
    print(f"Checksum: {checksum_file}")
    print("UNSIGNED, EXPERIMENTAL Linux artifact — not a supported release.")


if __name__ == "__main__":
    main()
