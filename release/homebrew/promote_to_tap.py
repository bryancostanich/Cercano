#!/usr/bin/env python3
"""Stage a rendered formula into a local checkout of the Homebrew tap.

Promotion is the step that makes a release reachable by users, so it refuses
to run until the referenced artifact is actually downloadable and matches the
checksum the formula claims. A formula pointing at a missing or altered asset
would break `brew install` for everyone.

This writes to a local tap checkout only. It never commits, pushes, opens a
pull request, or touches GitHub. Review the result and publish deliberately.
"""

import argparse
import hashlib
import ipaddress
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import render_formula  # noqa: E402

DOWNLOAD_TIMEOUT = 300
MAX_ARCHIVE_BYTES = 512 * 1024 * 1024


class PromotionError(Exception):
    """Promotion was refused; the tap checkout is left unchanged."""


def read_formula_fields(text):
    fields = {}
    for key in ("url", "version", "sha256"):
        match = re.search(rf'^\s*{key}\s+"([^"]+)"\s*$', text, re.M)
        if not match:
            raise PromotionError(f"formula has no {key} field")
        fields[key] = match.group(1)
    leftover = re.findall(r"@[A-Z0-9_]+@", text)
    if leftover:
        raise PromotionError(
            f"formula still contains placeholders: {', '.join(sorted(set(leftover)))}")
    return fields


def is_loopback(url):
    host = urllib.parse.urlparse(url).hostname or ""
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return host == "localhost"


def verify_published_artifact(url, expected_sha256):
    """Download the artifact from its published URL and check its digest.

    Plain HTTP is permitted only for loopback, so tests can exercise the real
    download path; a published release URL must always be HTTPS.
    """
    if not url.startswith("https://") and not (url.startswith("http://") and is_loopback(url)):
        raise PromotionError(f"refusing a non-HTTPS release URL: {url}")
    digest = hashlib.sha256()
    total = 0
    try:
        with urllib.request.urlopen(url, timeout=DOWNLOAD_TIMEOUT) as response:
            if getattr(response, "status", 200) != 200:
                raise PromotionError(f"release artifact is not available: HTTP {response.status}")
            while chunk := response.read(1024 * 1024):
                total += len(chunk)
                if total > MAX_ARCHIVE_BYTES:
                    raise PromotionError("release artifact is implausibly large; refusing")
                digest.update(chunk)
    except urllib.error.HTTPError as error:
        raise PromotionError(
            f"release artifact is not available at {url}: HTTP {error.code}") from error
    except urllib.error.URLError as error:
        raise PromotionError(f"cannot reach the release artifact: {error.reason}") from error
    if total == 0:
        raise PromotionError("release artifact is empty")
    actual = digest.hexdigest()
    if actual != expected_sha256:
        raise PromotionError(
            f"published artifact does not match the formula: formula claims {expected_sha256}, "
            f"download is {actual}")
    return total


def check_ruby_syntax(path):
    if shutil.which("ruby") is None:
        return "ruby not installed; formula syntax was NOT checked"
    result = subprocess.run(["ruby", "-c", str(path)], capture_output=True, text=True)
    if result.returncode != 0:
        raise PromotionError(f"formula is not valid Ruby:\n{result.stderr.strip()}")
    return None


def promote(formula_path, tap_path, expected_version, skip_download=False):
    formula_path = Path(formula_path)
    tap_path = Path(tap_path)
    if not formula_path.is_file():
        raise PromotionError(f"rendered formula not found: {formula_path}")
    if not (tap_path / ".git").is_dir():
        raise PromotionError(f"not a git checkout of the tap: {tap_path}")

    text = formula_path.read_text()
    fields = read_formula_fields(text)
    version = render_formula.parse_version(fields["version"])
    digest = render_formula.parse_digest(fields["sha256"])
    if expected_version and version != expected_version:
        raise PromotionError(
            f"formula is version {version}, expected {expected_version}")

    expected_name = f"cercano-{version}-darwin-arm64.tar.gz"
    if not fields["url"].endswith(f"/v{version}/{expected_name}"):
        raise PromotionError(
            f"release URL does not point at v{version}/{expected_name}: {fields['url']}")

    notes = []
    syntax_note = check_ruby_syntax(formula_path)
    if syntax_note:
        notes.append(syntax_note)

    if skip_download:
        notes.append("published artifact was NOT verified (--skip-download)")
    else:
        size = verify_published_artifact(fields["url"], digest)
        notes.append(f"verified published artifact: {size} bytes, sha256 matches")

    # Formula/ is the conventional tap layout; create it only if the tap uses it.
    destination_dir = tap_path / "Formula" if (tap_path / "Formula").is_dir() else tap_path
    destination = destination_dir / "cercano.rb"
    previous = destination.read_text() if destination.is_file() else None
    if previous == text:
        raise PromotionError("tap already contains this exact formula; nothing to promote")
    destination.write_text(text)
    return destination, previous is not None, notes


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("formula", help="rendered cercano.rb from render_formula.py")
    parser.add_argument("tap", help="local checkout of cercano-ai/homebrew-cercano")
    parser.add_argument("--version", help="expected version, as a cross-check")
    parser.add_argument("--skip-download", action="store_true",
                        help="offline; records the artifact as UNVERIFIED instead of checking it")
    args = parser.parse_args(argv)

    try:
        destination, replaced, notes = promote(
            args.formula, args.tap, args.version, args.skip_download)
    except (PromotionError, render_formula.ValidationError, OSError) as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        print("The tap checkout was not modified.", file=sys.stderr)
        return 1

    for note in notes:
        print(note)
    print(f"{'Updated' if replaced else 'Created'} {destination}")
    print("Nothing was committed or pushed. Review the diff, then publish deliberately.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
