import base64
import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

import update_tap as u


class TapUpdateTest(unittest.TestCase):
    def test_canonical_organization_endpoints(self):
        self.assertEqual(u.TAP_PATH, '/repos/cercano-ai/homebrew-tap/contents/Formula/cercano.rb')
        self.assertEqual(u.RELEASE_PATH, '/repos/cercano-ai/Cercano/releases/tags/')

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.version = '1.2.3'
        self.name = 'cercano-1.2.3-darwin-arm64.tar.gz'
        self.archive = self.root / self.name
        with tarfile.open(self.archive, 'w:gz') as tar:
            for name in ['bin/cercano', 'bin/cercano-cli', 'LICENSE', 'README.txt']:
                info = tarfile.TarInfo('cercano-1.2.3-darwin-arm64/' + name)
                info.mode = 0o755 if name.startswith('bin/') else 0o644
                info.size = 4
                tar.addfile(info, io.BytesIO(b'test'))
        self.digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        self.text = u.renderer.render(Path(u.__file__).with_name('cercano.rb.in').read_text(),
                                      self.version, self.name, self.digest)
        self.formula = self.root / 'cercano.rb'
        self.formula.write_text(self.text)
        self.release = {'tag_name': 'v1.2.3', 'draft': False,
                        'assets': [{'name': self.name}, {'name': self.name + '.sha256'}]}
        self.old = self.text.replace('1.2.3', '1.2.2')
        self.writes = []
        self.calls = []
        self.conflicts = 0
        self.winner = None
        self.api_mock = patch.object(u, 'api', side_effect=self.api).start()
        self.download_mock = patch.object(u, 'download', side_effect=self.download).start()
        self.addCleanup(patch.stopall)

    def download(self, url, checksum=False):
        self.assertTrue(url.startswith('https://github.com/cercano-ai/Cercano/releases/download/v1.2.3/'))
        return self.digest + '  ' + self.name + '\n' if checksum else self.digest

    def api(self, method, path, token, payload=None):
        self.calls.append((method, path))
        if path.startswith(u.RELEASE_PATH):
            return self.release
        self.assertEqual(token, 'test-tap-secret')
        if method == 'GET':
            self.assertEqual(path, u.TAP_PATH + '?ref=main')
            if self.old is None:
                raise u.Failure('not found', 404)
            return {'type': 'file', 'encoding': 'base64', 'sha': 'current-blob',
                    'content': base64.b64encode(self.old.encode()).decode()}
        self.assertEqual(path, u.TAP_PATH)
        self.assertEqual(method, 'PUT')
        self.assertEqual(payload['branch'], 'main')
        self.assertEqual(base64.b64decode(payload['content']).decode(), self.text)
        self.writes.append(payload)
        if self.conflicts:
            self.conflicts -= 1
            if self.winner is not None:
                self.old = self.winner
            raise u.Failure('conflict', 409)
        return {'commit': {'sha': 'new'}}

    def run_update(self, token='test-tap-secret'):
        return u.update(self.version, self.digest, self.archive, self.formula, token)

    def test_updates_only_fixed_path_with_expected_blob(self):
        self.assertEqual(self.run_update(), 'updated')
        self.assertEqual(self.writes[0]['sha'], 'current-blob')
        self.assertNotIn('test-tap-secret', json.dumps(self.writes))

    def test_creates_missing_formula(self):
        self.old = None
        self.assertEqual(self.run_update(), 'updated')
        self.assertNotIn('sha', self.writes[0])

    def test_idempotent(self):
        self.old = self.text
        self.assertEqual(self.run_update(), 'unchanged')
        self.assertEqual(self.writes, [])

    def test_missing_token_does_not_make_requests(self):
        with self.assertRaisesRegex(u.Failure, 'HOMEBREW_TAP_TOKEN'):
            self.run_update('')
        self.assertEqual(self.calls, [])

    def test_downgrade_refused(self):
        self.old = self.text.replace('1.2.3', '1.10.0')
        with self.assertRaisesRegex(u.Failure, 'downgrade'):
            self.run_update()
        self.assertEqual(self.writes, [])

    def test_same_version_changed_digest_refused(self):
        self.old = self.text.replace(self.digest, '0' * 64)
        with self.assertRaisesRegex(u.Failure, 'different checksum'):
            self.run_update()
        self.assertEqual(self.writes, [])

    def test_same_version_changed_formula_refused(self):
        self.old = self.text + '\n# manual change\n'
        with self.assertRaisesRegex(u.Failure, 'different formula'):
            self.run_update()
        self.assertEqual(self.writes, [])

    def test_exact_pretransfer_formula_urls_are_recognized(self):
        legacy = self.text.replace('https://github.com/cercano-ai/Cercano',
                                   'https://github.com/bryancostanich/Cercano')
        self.assertTrue(u.trusted_formula_matches(legacy, self.text, '0.20.3'))
        self.assertFalse(u.trusted_formula_matches(legacy, self.text, '0.20.4'))
        self.assertFalse(u.trusted_formula_matches(legacy + '\nsystem("unexpected")\n',
                                                 self.text, '0.20.3'))
        self.assertFalse(u.trusted_formula_matches(legacy.replace(self.digest, '0' * 64),
                                                 self.text, '0.20.3'))
        self.assertFalse(u.trusted_formula_matches(legacy.replace('bryancostanich', 'untrusted'),
                                                 self.text, '0.20.3'))
        self.assertTrue(u.trusted_formula_matches(self.text, self.text, self.version))

    def test_formula_tampering_refused_before_network(self):
        self.formula.write_text(self.text + '\nsystem("unexpected")\n')
        with self.assertRaisesRegex(u.Failure, 'trusted release template'):
            self.run_update()
        self.assertEqual(self.calls, [])

    def test_archive_tampering_refused_before_network(self):
        self.archive.write_bytes(b'changed')
        with self.assertRaisesRegex(u.Failure, 'CI archive checksum'):
            self.run_update()
        self.assertEqual(self.calls, [])

    def test_public_archive_tampering_refused(self):
        self.download_mock.side_effect = lambda *a, **k: 'bad'
        with self.assertRaisesRegex(u.Failure, 'Published archive checksum'):
            self.run_update()
        self.assertEqual(self.writes, [])

    def test_sidecar_tampering_refused(self):
        self.download_mock.side_effect = lambda *a, **k: 'bad' if k.get('checksum') else self.digest
        with self.assertRaisesRegex(u.Failure, 'sidecar'):
            self.run_update()
        self.assertEqual(self.writes, [])

    def test_draft_refused(self):
        self.release['draft'] = True
        with self.assertRaisesRegex(u.Failure, 'not published'):
            self.run_update()
        self.assertEqual(self.writes, [])

    def test_missing_asset_refused(self):
        self.release['assets'].pop()
        with self.assertRaisesRegex(u.Failure, 'incomplete'):
            self.run_update()
        self.assertEqual(self.writes, [])

    def test_conflict_refetches_and_rechecks(self):
        self.conflicts = 1
        self.assertEqual(self.run_update(), 'updated')
        self.assertEqual(len(self.writes), 2)
        self.assertEqual(self.calls.count(('GET', u.TAP_PATH + '?ref=main')), 2)

    def test_concurrent_newer_winner_is_not_overwritten(self):
        self.conflicts = 1
        self.winner = self.text.replace('1.2.3', '2.0.0')
        with self.assertRaisesRegex(u.Failure, 'downgrade'):
            self.run_update()
        self.assertEqual(len(self.writes), 1)

    def test_concurrent_identical_winner_is_noop(self):
        self.conflicts = 1
        self.winner = self.text
        self.assertEqual(self.run_update(), 'unchanged')
        self.assertEqual(len(self.writes), 1)

    def test_conflicts_are_bounded(self):
        self.conflicts = 10
        with self.assertRaisesRegex(u.Failure, 'retry only the tap job'):
            self.run_update()
        self.assertEqual(len(self.writes), 3)

    def test_rejects_unsafe_redirects(self):
        for url in ['http://github.com/x', 'file:///secret', 'https://evilgithub.com/x',
                    'https://github.com.evil.test/x', 'https://user:pass@github.com/x']:
            with self.subTest(url=url), self.assertRaises(u.Failure):
                u.public_url(url)
        u.public_url('https://release-assets.githubusercontent.com/x')

    def test_authenticated_redirects_refused(self):
        with self.assertRaisesRegex(u.Failure, 'authenticated API redirect'):
            u.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://github.com')


class TransportTest(unittest.TestCase):
    def test_api_auth_is_header_only_and_body_never_logged(self):
        error = urllib.error.HTTPError('https://api.github.com/path', 409,
                                      'sensitive-response-value', {}, None)
        with patch.object(u.urllib.request, 'build_opener') as opener:
            opener.return_value.open.side_effect = error
            with self.assertRaises(u.Failure) as caught:
                u.api('PUT', u.TAP_PATH, 'secret-fixture', {'branch': 'main'})
            self.assertEqual(caught.exception.status, 409)
            self.assertNotIn('secret-fixture', str(caught.exception))
            self.assertNotIn('sensitive-response-value', str(caught.exception))
            request = opener.return_value.open.call_args.args[0]
            self.assertEqual(request.full_url, 'https://api.github.com' + u.TAP_PATH)
            self.assertEqual(request.get_header('Authorization'), 'Bearer secret-fixture')
            self.assertNotIn(b'secret-fixture', request.data)
            self.assertEqual(request.get_header('Content-type'), 'application/json')

    def test_public_download_never_carries_credentials(self):
        body = b'public-archive'
        response = io.BytesIO(body)
        response.status = 200
        response.geturl = lambda: 'https://release-assets.githubusercontent.com/x'
        with patch.dict(u.os.environ, {'GITHUB_TOKEN': 'read-secret',
                                       'HOMEBREW_TAP_TOKEN': 'tap-secret'}):
            with patch.object(u.urllib.request, 'build_opener') as opener:
                opener.return_value.open.return_value = response
                self.assertEqual(u.download('https://github.com/x'), hashlib.sha256(body).hexdigest())
                self.assertEqual(opener.return_value.open.call_args.args, ('https://github.com/x',))

    def test_download_checks_final_redirect_destination(self):
        response = io.BytesIO(b'untrusted')
        response.status = 200
        response.geturl = lambda: 'https://evil.test/x'
        with patch.object(u.urllib.request, 'build_opener') as opener:
            opener.return_value.open.return_value = response
            with self.assertRaises(u.Failure):
                u.download('https://github.com/x')


if __name__ == '__main__':
    unittest.main()
