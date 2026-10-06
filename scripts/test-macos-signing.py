#!/usr/bin/env python3
"""Execute the signer using isolated tool shims; never access real signing keys."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().with_name('sign-macos-release.sh')
SHA = 'A' * 40
NAME = 'Developer ID Application: Test Corp (TEAM123)'
SHIM = r'''
import json, os, sys
from pathlib import Path
name = Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['LOG'], 'a') as f:
    f.write(json.dumps([name] + args) + '\n')
if name == 'uname':
    print('arm64' if args else os.environ.get('HOST', 'Darwin'))
elif name == 'lipo':
    print(os.environ.get('ARCH', 'arm64'))
elif name == 'security':
    print(os.environ['IDENTITIES'])
    sys.exit(int(os.environ.get('SECURITY_EXIT', '0')))
elif name == 'codesign':
    mode = 'SIGN' if '--force' in args else 'VERIFY' if '--verify' in args else 'DETAILS'
    if mode == 'DETAILS':
        print(os.environ['DETAILS'], file=sys.stderr)
    sys.exit(int(os.environ.get(mode + '_EXIT', '0')))
else:
    sys.exit(99)
'''

class SigningTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='signing tests ')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / 'stage with spaces' / 'bin'
        self.bin.mkdir(parents=True)
        for name in ('cercano', 'cercano-cli'):
            p = self.bin / name
            p.write_bytes(b'mock binary')
            p.chmod(0o755)
        tools = self.root / 'tools'
        tools.mkdir()
        for name in ('uname', 'lipo', 'security', 'codesign'):
            p = tools / name
            p.write_text('#!' + sys.executable + '\n' + SHIM)
            p.chmod(0o755)
        for name in ('dirname', 'mkdir'):
            (tools / name).symlink_to('/usr/bin/' + name if name == 'dirname' else '/bin/' + name)
        self.log = self.root / 'calls.jsonl'
        self.env = dict(os.environ)
        for key in list(self.env):
            if key.startswith('CERCANO_CODESIGN_'):
                del self.env[key]
        self.env.update(PATH=str(tools), LOG=str(self.log),
                        CERCANO_CODESIGN_ID=NAME,
                        IDENTITIES=f'  1) {SHA} "{NAME}"\n  1 valid identities found',
                        DETAILS=f'Authority={NAME}\nCodeDirectory v=20500 flags=0x10000(runtime)\nTimestamp=Sep 22, 2026\n')

    def run_signer(self, ok=True):
        result = subprocess.run(['/bin/bash', str(SCRIPT), str(self.bin)],
                                env=self.env, text=True, capture_output=True, timeout=20)
        self.assertEqual(result.returncode == 0, ok, result.stdout + result.stderr)
        return [json.loads(x) for x in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_success_and_keychain_spaces(self):
        self.env['CERCANO_CODESIGN_KEYCHAIN'] = '/tmp/signing keys.keychain-db'
        calls = self.run_signer()
        security = next(c for c in calls if c[0] == 'security')
        self.assertEqual(security[-1], self.env['CERCANO_CODESIGN_KEYCHAIN'])
        signs = [c for c in calls if '--force' in c]
        self.assertEqual(len(signs), 2)
        for c in signs:
            self.assertIn('--timestamp', c)
            self.assertEqual(c[c.index('--sign') + 1], SHA)
            self.assertEqual(c[c.index('--options') + 1], 'runtime')
            self.assertEqual(c[c.index('--keychain') + 1], self.env['CERCANO_CODESIGN_KEYCHAIN'])
            self.assertTrue(Path(c[-1]).is_file())
        self.assertEqual(len([c for c in calls if '--verify' in c]), 2)

    def test_fingerprint(self):
        self.env['CERCANO_CODESIGN_ID'] = SHA.lower()
        self.run_signer()

    def test_preflight_failures_never_sign(self):
        for updates in ({'CERCANO_CODESIGN_ID': ''}, {'CERCANO_CODESIGN_ID': 'none'},
                        {'CERCANO_CODESIGN_ID': 'Test Corp'}, {'IDENTITIES': ''},
                        {'IDENTITIES': f'1) {SHA} "Apple Development: Test"', 'CERCANO_CODESIGN_ID': SHA},
                        {'IDENTITIES': f'1) {SHA} "{NAME}"\n2) {"B" * 40} "{NAME}"'},
                        {'ARCH': 'x86_64 arm64'}, {'HOST': 'Linux'}, {'SECURITY_EXIT': '1'}):
            with self.subTest(updates=updates):
                old = self.env.copy()
                self.env.update(updates)
                self.log.unlink(missing_ok=True)
                calls = self.run_signer(False)
                self.assertFalse(any('--force' in c for c in calls))
                self.env = old

    def test_missing_second_binary(self):
        (self.bin / 'cercano-cli').unlink()
        self.assertFalse(any('--force' in c for c in self.run_signer(False)))

    def test_symlink(self):
        (self.bin / 'cercano-cli').unlink()
        (self.bin / 'cercano-cli').symlink_to(self.bin / 'cercano')
        self.run_signer(False)

    def test_command_failures(self):
        for key in ('SIGN_EXIT', 'VERIFY_EXIT', 'DETAILS_EXIT'):
            with self.subTest(key=key):
                self.env[key] = '1'
                self.run_signer(False)
                del self.env[key]

    def test_missing_metadata(self):
        good = self.env['DETAILS']
        for line in good.splitlines():
            with self.subTest(line=line):
                self.env['DETAILS'] = good.replace(line, '')
                self.run_signer(False)
        self.env['DETAILS'] = good.replace('Timestamp=Sep 22, 2026', 'Timestamp=')
        self.run_signer(False)

if __name__ == '__main__':
    unittest.main()
