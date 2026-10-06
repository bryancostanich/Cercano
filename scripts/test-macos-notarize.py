#!/usr/bin/env python3
"""Subprocess tests with an isolated PATH: no real Apple tools can run."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zipfile

SCRIPT = Path(__file__).with_name('notarize-macos-local.py').resolve()
SID = '12345678-1234-1234-1234-123456789abc'
DETAILS = 'Authority=Developer ID Application: Test\nCodeDirectory flags=0x10000(runtime)\nTimestamp=Today\n'
SHIM = '''
import json, os, sys, time
from pathlib import Path
name = Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['CALLS'], 'a') as f: f.write(json.dumps([name] + args) + '\\n')
if name == 'uname': print('arm64' if args else 'Darwin')
elif name == 'lipo': print('arm64')
elif name == 'codesign':
    if '-d' in args: print(os.environ['DETAILS'], file=sys.stderr)
    sys.exit(int(os.environ.get('VERIFY_EXIT', '0')))
elif name == 'xcrun':
    action = args[1].upper()
    print(os.environ[action], flush=True)
    print(os.environ.get(action + '_STDERR', ''), file=sys.stderr, flush=True)
    time.sleep(float(os.environ.get(action + '_SLEEP', '0')))
    sys.exit(int(os.environ.get(action + '_EXIT', '0')))
else: sys.exit(99)
'''

class NotaryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='notary tests ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.stage = self.root / 'stage bin'
        self.stage.mkdir()
        for name in ('cercano', 'cercano-cli'):
            p = self.stage / name
            p.write_bytes(b'mock executable')
            p.chmod(0o755)
        tools = self.root / 'tools'
        tools.mkdir()
        for name in ('uname', 'lipo', 'codesign', 'xcrun'):
            p = tools / name
            p.write_text('#!' + sys.executable + '\n' + SHIM)
            p.chmod(0o755)
        self.out = self.root / 'new output'
        self.calls = self.root / 'calls'
        self.env = dict(os.environ, PATH=str(tools), CALLS=str(self.calls), DETAILS=DETAILS,
                        SUBMIT=json.dumps({'id': SID, 'status': 'Accepted'}),
                        LOG=json.dumps({'jobId': SID, 'status': 'Accepted'}))

    def execute(self, success=True, timeout='3'):
        result = subprocess.run([sys.executable, str(SCRIPT), str(self.stage), str(self.out),
                                 '--keychain-profile', 'test profile', '--keychain', '/tmp/keys with spaces',
                                 '--timeout', timeout], env=self.env, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertEqual((self.out / 'accepted.json').exists(), success)
        return result

    def uploads(self):
        return [json.loads(line) for line in self.calls.read_text().splitlines()
                if json.loads(line)[:3] == ['xcrun', 'notarytool', 'submit']]

    def test_success(self):
        self.execute()
        archive = self.out / 'cercano-notarization-submission.zip'
        with zipfile.ZipFile(archive) as z:
            self.assertEqual(z.namelist(), ['bin/cercano', 'bin/cercano-cli'])
            self.assertEqual(z.read('bin/cercano'), b'mock executable')
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        self.assertTrue(Path(str(archive) + '.sha256').read_text().startswith(digest))
        call, = self.uploads()
        self.assertEqual(call[call.index('--keychain-profile') + 1], 'test profile')
        self.assertEqual(call[call.index('--keychain') + 1], '/tmp/keys with spaces')
        self.assertEqual((self.out / 'submission-id.txt').read_text().strip(), SID)

    def test_rejected(self):
        self.env['SUBMIT'] = json.dumps({'id': SID, 'status': 'Invalid'})
        self.env['LOG'] = json.dumps({'jobId': SID, 'status': 'Invalid'})
        self.execute(False)
        self.assertEqual(json.loads((self.out / 'log-stdout.txt').read_text())['status'], 'Invalid')

    def test_rejected_summary_is_printed(self):
        """A generic rejection must explain itself, not just name the save dir.

        run36922503915 failed with only 'Submission did not return a
        successful Accepted result' and the runner-only files were lost, so
        the well-known notarytool fields have to reach stdout too.
        """
        self.env['SUBMIT'] = json.dumps(
            {'id': SID, 'status': 'Invalid', 'message': 'The binary contains an invalid signature.'})
        result = self.execute(False)
        self.assertIn('status=Invalid', result.stdout)
        self.assertIn('id=' + SID, result.stdout)
        self.assertIn('returncode=0', result.stdout)
        self.assertIn('invalid signature', result.stdout)

    def test_summary_report_written_on_success(self):
        """The sanitized report exists for CI on both success and failure."""
        self.execute()
        report = json.loads((self.out / 'diagnostic-report.json').read_text())
        self.assertEqual(report['submit']['id'], SID)
        self.assertEqual(report['submit']['status'], 'Accepted')
        self.assertEqual(report['submit']['returncode'], 0)
        self.assertFalse(report['submit']['timed_out'])
        self.assertEqual(report['log']['status'], 'Accepted')
        self.assertEqual(report['log']['returncode'], 0)

    def test_summary_redacts_credential_like_values(self):
        """Even a hostile message must not leak credential-shaped values.

        Raw submit/log captures stay on the runner; only the sanitized
        summary and report reach CI stdout and the failure artifact.
        """
        self.env['SUBMIT'] = json.dumps({
            'id': SID, 'status': 'Invalid',
            'message': 'password=hunter2 token=zzz-secret api_key=abc123 '
                       'passwd: qwerty123 leaked abcd-efgh-ijkl-mnop end'})
        result = self.execute(False)
        report_text = (self.out / 'diagnostic-report.json').read_text()
        for marker in ('hunter2', 'zzz-secret', 'abc123', 'qwerty123',
                       'abcd-efgh-ijkl-mnop'):
            self.assertNotIn(marker, report_text)
            self.assertNotIn(marker, result.stdout)
            self.assertNotIn(marker, result.stderr)
        self.assertIn('[REDACTED]', report_text)
        self.assertIn('status=Invalid', result.stdout)

    def test_nonzero_accepted(self):
        self.env['SUBMIT_EXIT'] = '1'
        self.execute(False)

    def test_malformed(self):
        self.env['SUBMIT'] = 'bad json'
        self.execute(False)

    def test_timeout(self):
        self.env['SUBMIT_SLEEP'] = '2'
        result = self.execute(False, '.3')
        self.assertIn('history', result.stderr)
        self.assertEqual((self.out / 'submission-id.txt').read_text().strip(), SID)
        self.assertEqual(len(self.uploads()), 1)
        self.assertTrue(json.loads((self.out / 'submit-result.json').read_text())['timed_out'])

    def test_preflight(self):
        self.env['VERIFY_EXIT'] = '1'
        self.execute(False)
        self.assertEqual(self.uploads(), [])

    def test_metadata(self):
        for i, removed in enumerate(DETAILS.splitlines()):
            with self.subTest(removed=removed):
                self.out = self.root / f'output{i}'
                self.env['DETAILS'] = DETAILS.replace(removed, '')
                self.execute(False)
                self.assertEqual(self.uploads(), [])

    def test_existing_output(self):
        self.out.mkdir()
        marker = self.out / 'marker'
        marker.write_text('unchanged')
        self.execute(False)
        self.assertEqual(list(self.out.iterdir()), [marker])
        self.assertEqual(marker.read_text(), 'unchanged')

    def test_bad_logs(self):
        for i, log in enumerate(('bad json', '{}', json.dumps({'jobId': SID, 'status': 'Invalid'}),
                                 json.dumps({'jobId': 'wrong', 'status': 'Accepted'}))):
            with self.subTest(log=log):
                self.out = self.root / f'output{i}'
                self.env['LOG'] = log
                self.execute(False)

    def test_missing_and_symlink(self):
        binary = self.stage / 'cercano-cli'
        binary.unlink()
        self.execute(False)
        self.assertEqual(self.uploads(), [])
        self.out = self.root / 'symlink output'
        binary.symlink_to(self.stage / 'cercano')
        self.execute(False)
        self.assertEqual(self.uploads(), [])

    def test_submit_stderr_fallback(self):
        """Test that stderr fallback is captured and sanitized when submit fails with plain stderr."""
        self.env['SUBMIT'] = 'bad json'
        self.env['SUBMIT_EXIT'] = '1'
        self.env['SUBMIT_STDERR'] = 'HTTP 403: Forbidden - Invalid credentials\nAdditional error details'
        result = self.execute(False)
        # Check that stderr fallback is captured in the report
        report = json.loads((self.out / 'diagnostic-report.json').read_text())
        self.assertIn('submit', report)
        self.assertEqual(report['submit']['returncode'], 1)
        self.assertFalse(report['submit']['timed_out'])  # Not a timeout, explicit failure
        # Check that useful error information is surfaced
        self.assertIn('HTTP 403', result.stdout)
        self.assertIn('Forbidden', result.stdout)

    def test_network_unavailable_fallback(self):
        """Test that network errors are properly surfaced in summary/report."""
        self.env['SUBMIT'] = ''
        self.env['SUBMIT_EXIT'] = '1'
        self.env['SUBMIT_STDERR'] = 'Network unavailable: Connection refused\nUnable to reach Apple servers'
        result = self.execute(False)
        # Check that network error is surfaced
        self.assertIn('network unavailable', result.stdout.lower())
        self.assertIn('connection refused', result.stdout.lower())
        # Check that the report contains the fallback information
        report = json.loads((self.out / 'diagnostic-report.json').read_text())
        self.assertIn('submit', report)
        self.assertEqual(report['submit']['returncode'], 1)
        self.assertFalse(report['submit']['timed_out'])  # Not a timeout, explicit failure

    def test_exact_secret_values_are_redacted_without_labels(self):
        self.env['SUBMIT'] = ''
        self.env['SUBMIT_EXIT'] = '1'
        self.env['APPLE_APP_SPECIFIC_PASSWORD'] = 'arbitrary private value with spaces'
        self.env['CERTIFICATE_P12'] = 'base64-fixture-value'
        self.env['SUBMIT_STDERR'] = ('Network unavailable: arbitrary private value with spaces '
                                     'base64-fixture-value')
        result = self.execute(False)
        report = (self.out / 'diagnostic-report.json').read_text()
        for secret in (self.env['APPLE_APP_SPECIFIC_PASSWORD'], self.env['CERTIFICATE_P12']):
            self.assertNotIn(secret, result.stdout)
            self.assertNotIn(secret, report)
        self.assertIn('Network unavailable', report)
        self.assertIn('[REDACTED]', report)

    def test_explicit_credential_redaction(self):
        """Test explicit redaction of known credential env values."""
        self.env['SUBMIT'] = 'bad json'
        self.env['SUBMIT_STDERR'] = f'APPLE_APP_SPECIFIC_PASSWORD=abc123 MACOS_CERTIFICATE_PASSWORD=secret123 CERTIFICATE_PASSWORD=pwd123 MACOS_CERTIFICATE_P12=p12file.pem CERTIFICATE_P12=pem-content\n-----BEGIN PRIVATE KEY-----\nsecretkeydata\n-----END PRIVATE KEY-----'
        self.env['SUBMIT_EXIT'] = '1'
        result = self.execute(False)
        # Check that known credential values are redacted
        report_text = (self.out / 'diagnostic-report.json').read_text()
        for credential in ('abc123', 'secret123', 'pwd123', 'p12file.pem', 'pem-content', 'secretkeydata'):
            self.assertNotIn(credential, report_text)
        self.assertIn('[REDACTED]', report_text)
        # Check that the error message is still useful
        self.assertNotIn('status=Invalid', result.stdout)
        self.assertIn('APPLE_APP_SPECIFIC_PASSWORD=[REDACTED]', result.stdout)

if __name__ == '__main__':
    unittest.main()
