#!/usr/bin/env python3
"""Tests for promote_to_tap.py.

The artifact download is served by a local HTTP server on loopback; no real
release, tap or network is involved. Every refusal is asserted to leave the tap
checkout unmodified, since a half-applied promotion is worse than none.
"""

import hashlib
import http.server
import tempfile
import threading
import unittest
from pathlib import Path

import promote_to_tap

VERSION = "1.2.3"
ARCHIVE = f"cercano-{VERSION}-darwin-arm64.tar.gz"
PAYLOAD = b"pretend release archive"
DIGEST = hashlib.sha256(PAYLOAD).hexdigest()


class ArtifactServer(http.server.BaseHTTPRequestHandler):
    payload = PAYLOAD
    status = 200

    def do_GET(self):
        if self.status != 200:
            self.send_error(self.status)
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(self.payload)))
        self.end_headers()
        self.wfile.write(self.payload)

    def log_message(self, *args):
        pass


class PromoteTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = http.server.HTTPServer(("127.0.0.1", 0), ArtifactServer)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def setUp(self):
        ArtifactServer.payload = PAYLOAD
        ArtifactServer.status = 200
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.tap = self.dir / "tap"
        (self.tap / ".git").mkdir(parents=True)
        (self.tap / "Formula").mkdir()

    def write_formula(self, url=None, version=VERSION, digest=DIGEST, extra=""):
        path = self.dir / "cercano.rb"
        url = url or f"https://github.com/cercano-ai/Cercano/releases/download/v{version}/cercano-{version}-darwin-arm64.tar.gz"
        path.write_text(
            "class Cercano < Formula\n"
            f'  url "{url}"\n'
            f'  version "{version}"\n'
            f'  sha256 "{digest}"\n'
            f"{extra}"
            "end\n")
        return path

    def promoted(self):
        return (self.tap / "Formula" / "cercano.rb")

    def assert_refused(self, fragment, *args, **kwargs):
        with self.assertRaises((promote_to_tap.PromotionError,
                                promote_to_tap.render_formula.ValidationError)) as caught:
            promote_to_tap.promote(*args, **kwargs)
        self.assertIn(fragment, str(caught.exception))
        self.assertFalse(self.promoted().exists(),
                         "a refused promotion must not modify the tap")

    def test_promotes_with_verified_artifact(self):
        # Point the formula at the loopback server so the real download and
        # digest check run, rather than being skipped.
        url = f"{self.base}/releases/download/v{VERSION}/{ARCHIVE}"
        destination, replaced, notes = promote_to_tap.promote(
            self.write_formula(url=url), self.tap, VERSION)
        self.assertTrue(destination.is_file())
        self.assertFalse(replaced)
        self.assertTrue(any("sha256 matches" in note for note in notes), notes)

    def test_refuses_when_published_artifact_digest_differs(self):
        # The formula claims one digest; the published bytes hash to another.
        ArtifactServer.payload = b"tampered release archive"
        url = f"{self.base}/releases/download/v{VERSION}/{ARCHIVE}"
        self.assert_refused("does not match the formula",
                            self.write_formula(url=url), self.tap, VERSION)

    def test_refuses_plain_http_for_non_loopback(self):
        url = f"http://example.com/releases/download/v{VERSION}/{ARCHIVE}"
        self.assert_refused("non-HTTPS", self.write_formula(url=url), self.tap, VERSION)

    def test_promotes_offline_with_explicit_skip(self):
        destination, replaced, notes = promote_to_tap.promote(
            self.write_formula(), self.tap, VERSION, skip_download=True)
        self.assertTrue(destination.is_file())
        self.assertFalse(replaced)
        self.assertTrue(any("NOT verified" in note for note in notes))

    def test_records_update_versus_creation(self):
        promote_to_tap.promote(self.write_formula(), self.tap, VERSION, skip_download=True)
        second = self.write_formula(digest="b" * 64)
        _, replaced, _ = promote_to_tap.promote(second, self.tap, VERSION, skip_download=True)
        self.assertTrue(replaced)

    def test_refuses_identical_formula(self):
        formula = self.write_formula()
        promote_to_tap.promote(formula, self.tap, VERSION, skip_download=True)
        with self.assertRaises(promote_to_tap.PromotionError) as caught:
            promote_to_tap.promote(formula, self.tap, VERSION, skip_download=True)
        self.assertIn("nothing to promote", str(caught.exception))

    def test_refuses_unrendered_placeholders(self):
        path = self.dir / "template.rb"
        path.write_text('class Cercano < Formula\n  url "@RELEASE_URL@"\n'
                        '  version "@VERSION@"\n  sha256 "@SHA256@"\nend\n')
        self.assert_refused("still contains placeholders", path, self.tap, VERSION,
                            skip_download=True)

    def test_refuses_version_mismatch(self):
        self.assert_refused("expected 9.9.9", self.write_formula(), self.tap, "9.9.9",
                            skip_download=True)

    def test_refuses_url_not_matching_version(self):
        url = "https://github.com/cercano-ai/Cercano/releases/download/v9.9.9/cercano-9.9.9-darwin-arm64.tar.gz"
        self.assert_refused("does not point at", self.write_formula(url=url), self.tap,
                            VERSION, skip_download=True)

    def test_refuses_non_git_tap(self):
        plain = self.dir / "not-a-tap"
        plain.mkdir()
        with self.assertRaises(promote_to_tap.PromotionError) as caught:
            promote_to_tap.promote(self.write_formula(), plain, VERSION, skip_download=True)
        self.assertIn("not a git checkout", str(caught.exception))

    def test_refuses_missing_formula(self):
        self.assert_refused("not found", self.dir / "absent.rb", self.tap, VERSION,
                            skip_download=True)

    def test_refuses_invalid_ruby(self):
        path = self.dir / "broken.rb"
        path.write_text('class Cercano < Formula\n  url "https://example.com/v1.2.3/'
                        f'cercano-{VERSION}-darwin-arm64.tar.gz"\n'
                        f'  version "{VERSION}"\n  sha256 "{DIGEST}"\n')  # never closed
        with self.assertRaises(promote_to_tap.PromotionError) as caught:
            promote_to_tap.promote(path, self.tap, VERSION, skip_download=True)
        self.assertIn("not valid Ruby", str(caught.exception))
        self.assertFalse(self.promoted().exists())

    def test_verifies_digest_of_downloaded_artifact(self):
        url = f"{self.base}/releases/download/v{VERSION}/{ARCHIVE}"
        self.assertEqual(promote_to_tap.verify_published_artifact(url, DIGEST), len(PAYLOAD))
        with self.assertRaises(promote_to_tap.PromotionError) as caught:
            promote_to_tap.verify_published_artifact(url, "0" * 64)
        self.assertIn("does not match the formula", str(caught.exception))

    def test_refuses_missing_published_artifact(self):
        ArtifactServer.status = 404
        with self.assertRaises(promote_to_tap.PromotionError) as caught:
            promote_to_tap.verify_published_artifact(
                f"{self.base}/releases/download/v{VERSION}/{ARCHIVE}", DIGEST)
        self.assertIn("not available", str(caught.exception))

    def test_refuses_empty_artifact(self):
        ArtifactServer.payload = b""
        with self.assertRaises(promote_to_tap.PromotionError) as caught:
            promote_to_tap.verify_published_artifact(
                f"{self.base}/releases/download/v{VERSION}/{ARCHIVE}",
                hashlib.sha256(b"").hexdigest())
        self.assertIn("empty", str(caught.exception))

    def test_promotes_to_tap_root_when_no_formula_directory(self):
        flat = self.dir / "flat-tap"
        (flat / ".git").mkdir(parents=True)
        destination, _, _ = promote_to_tap.promote(
            self.write_formula(), flat, VERSION, skip_download=True)
        self.assertEqual(destination, flat / "cercano.rb")


if __name__ == "__main__":
    unittest.main()
