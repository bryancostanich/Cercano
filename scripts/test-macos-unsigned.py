#!/usr/bin/env python3
"""Verify a locally built unsigned rehearsal archive; never start an agent."""
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile


def run(args, **kwargs):
    return subprocess.check_output(args, text=True, timeout=30, **kwargs).strip()


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: python3 scripts/test-macos-unsigned.py ARCHIVE VERSION")
    archive = Path(sys.argv[1]).resolve()
    version = sys.argv[2]
    root_name = f"cercano-{version}-darwin-arm64-unsigned"
    if archive.name != root_name + ".tar.gz":
        raise SystemExit("archive name/version mismatch")
    checksum = Path(str(archive) + ".sha256").read_text().split()
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    if checksum != [digest, archive.name]:
        raise SystemExit("checksum or checksum filename mismatch")
    print(f"SHA-256 verified: {digest}")
    required = {"bin/cercano", "bin/cercano-cli", "LICENSE", "README.txt"}
    with tempfile.TemporaryDirectory(prefix="cercano-archive-test-") as tmp:
        base = Path(tmp)
        # Extract only the exact expected regular files; reject links and extras.
        with tarfile.open(archive, "r:gz") as tar:
            found = set()
            for member in tar.getmembers():
                if member.isdir() and member.name.rstrip("/") in {root_name, root_name + "/bin"}:
                    continue
                relative = member.name.removeprefix(root_name + "/")
                if not member.isfile() or relative not in required or relative in found:
                    raise SystemExit(f"unexpected archive member: {member.name}")
                if member.name != root_name + "/" + relative:
                    raise SystemExit("invalid archive root")
                found.add(relative)
                target = base / root_name / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                source = tar.extractfile(member)
                if source is None:
                    raise SystemExit("missing archive data")
                with source:
                    target.write_bytes(source.read())
                target.chmod(member.mode & 0o777)
            if found != required:
                raise SystemExit(f"missing archive members: {required - found}")
        home = base / "home"
        home.mkdir()
        outside = base / "outside"
        outside.mkdir()
        env = {"PATH": "/usr/bin:/bin", "HOME": str(home),
               "XDG_CONFIG_HOME": str(home / "config"), "XDG_CACHE_HOME": str(home / "cache"),
               "XDG_DATA_HOME": str(home / "data"), "TMPDIR": str(base)}
        for name in ("cercano", "cercano-cli"):
            binary = base / root_name / "bin" / name
            if not os.access(binary, os.X_OK):
                raise SystemExit(f"not executable: {name}")
            arch = run(["/usr/bin/lipo", "-archs", str(binary)])
            if arch != "arm64":
                raise SystemExit(f"unexpected architecture for {name}: {arch}")
            flags = ["--version", "version"] if name == "cercano" else ["--version"]
            for flag in flags:
                output = run([str(binary), flag], cwd=outside, env=env)
                if output != f"{name} v{version}":
                    raise SystemExit(f"unexpected version output: {output!r}")
                print(f"PASS {name} {flag}: {output} (arm64)")
            print(run(["/usr/bin/otool", "-L", str(binary)]))
        if list(home.iterdir()):
            raise SystemExit("version commands unexpectedly wrote to isolated HOME")
    print("PASS archive contents, checksum, architectures, versions, and isolated HOME")
    print("Not verified: full startup, minimum supported macOS, licensing completeness, signing, notarization, or Gatekeeper.")


if __name__ == "__main__":
    main()
