#!/usr/bin/env python3
"""Upload signed staging binaries to Apple; never publish or auto-resubmit."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import uuid
import zipfile


class Failure(Exception):
    pass


def run(argv, timeout=60):
    return subprocess.run(argv, capture_output=True, text=True, timeout=timeout)


def verify(binary):
    if binary.is_symlink() or not binary.is_file() or not os.access(binary, os.X_OK):
        raise Failure(f'Expected regular non-symlink executable: {binary}')
    if run(['lipo', '-archs', str(binary)]).stdout.strip() != 'arm64':
        raise Failure(f'Expected arm64-only binary: {binary}')
    result = run(['codesign', '--verify', '--strict', str(binary)])
    if result.returncode:
        raise Failure(f'Signature verification failed: {binary}\n{result.stderr}')
    result = run(['codesign', '-d', '--verbose=4', str(binary)])
    details = result.stdout + '\n' + result.stderr
    if result.returncode or not re.search(r'^Authority=Developer ID Application: .+', details, re.M):
        raise Failure(f'Missing Developer ID signature: {binary}')
    if not re.search(r'^CodeDirectory[^\n]*flags=0x[0-9a-fA-F]+\([^\n)]*\bruntime\b', details, re.M):
        raise Failure(f'Missing hardened runtime: {binary}')
    timestamp = re.search(r'^Timestamp=(.+)$', details, re.M)
    if not timestamp or timestamp[1].strip().lower() in ('', 'none'):
        raise Failure(f'Missing secure timestamp: {binary}')


def text(value):
    return value.decode('utf-8', errors='replace') if isinstance(value, bytes) else value or ''


def recorded(argv, timeout, output, label):
    timed_out = False
    try:
        result = run(argv, timeout)
        stdout, stderr, code = result.stdout, result.stderr, result.returncode
    except subprocess.TimeoutExpired as exc:
        stdout, stderr, code = text(exc.stdout), text(exc.stderr), -1
        timed_out = True
    (output / f'{label}-stdout.txt').write_text(stdout)
    (output / f'{label}-stderr.txt').write_text(stderr)
    (output / f'{label}-result.json').write_text(json.dumps({'returncode': code, 'timed_out': timed_out}) + '\n')
    try:
        payload = json.loads(stdout)
    except ValueError:
        payload = None
    return code, timed_out, payload


def submission_id(payload):
    value = payload.get('id') if isinstance(payload, dict) else None
    try:
        return str(uuid.UUID(value)) if isinstance(value, str) else None
    except ValueError:
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('staging_bin_dir', type=Path)
    parser.add_argument('output_dir', type=Path, help='Must not already exist; preserves diagnostics on failure')
    parser.add_argument('--keychain-profile', required=True)
    parser.add_argument('--keychain')
    parser.add_argument('--timeout', type=float, default=1800, help='Seconds per Apple request, maximum 1800')
    args = parser.parse_args()
    if not 0 < args.timeout <= 1800 or not args.keychain_profile.strip():
        parser.error('Provide a nonempty profile and timeout in (0, 1800]')
    auth = ['--keychain-profile', args.keychain_profile]
    if args.keychain:
        auth += ['--keychain', args.keychain]
    output = args.output_dir.absolute()
    sid = None
    created = False
    try:
        if run(['uname']).stdout.strip() != 'Darwin' or run(['uname', '-m']).stdout.strip() != 'arm64':
            raise Failure('Darwin arm64 host required')
        # Exclusive mkdir also rejects dangling symlinks and concurrent reuse.
        output.mkdir(mode=0o700, parents=False, exist_ok=False)
        created = True
        sources = [args.staging_bin_dir.absolute() / name for name in ('cercano', 'cercano-cli')]
        for binary in sources:
            verify(binary)
        archive = output / 'cercano-notarization-submission.zip'
        with tempfile.TemporaryDirectory(prefix='cercano-notary-') as tmp:
            copies = []
            for binary in sources:
                copy = Path(tmp) / binary.name
                shutil.copy2(binary, copy)
                verify(copy)
                copies.append(copy)
            with zipfile.ZipFile(archive, 'x', zipfile.ZIP_DEFLATED) as z:
                for copy in copies:
                    z.write(copy, 'bin/' + copy.name)
        with archive.open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest() if hasattr(hashlib, 'file_digest') else hashlib.sha256(stream.read()).hexdigest()
        (output / (archive.name + '.sha256')).write_text(f'{digest}  {archive.name}\n')
        print('Uploading both signed binaries to Apple for notarization.', flush=True)
        code, timed_out, payload = recorded(
            ['xcrun', 'notarytool', 'submit', str(archive), *auth, '--wait', '--output-format', 'json'],
            args.timeout, output, 'submit')
        sid = submission_id(payload)
        if sid:
            (output / 'submission-id.txt').write_text(sid + '\n')
        # Preserve an available log even for a rejected submission.
        log_ok = False
        if sid and not timed_out:
            log_code, log_timeout, log_payload = recorded(
                ['xcrun', 'notarytool', 'log', sid, *auth], args.timeout, output, 'log')
            log_ok = (log_code == 0 and not log_timeout and isinstance(log_payload, dict)
                      and log_payload.get('jobId') == sid and log_payload.get('status') == 'Accepted')
        if timed_out:
            raise Failure('Submission wait timed out; Apple may still be processing it. Do not resubmit blindly.')
        if code or not isinstance(payload, dict) or payload.get('status') != 'Accepted' or not sid:
            raise Failure('Submission did not return a successful Accepted result. Inspect saved diagnostics.')
        if not log_ok:
            raise Failure('Could not verify the matching Accepted notarization log. Inspect saved diagnostics.')
        (output / 'accepted.json').write_text(json.dumps({'id': sid, 'status': 'Accepted', 'sha256': digest}, indent=2) + '\n')
        print(f'Accepted: {sid}\nEvidence: {output}\nSubmission ZIP is not final release packaging. Nothing published or stapled.')
        return 0
    except (Failure, OSError, subprocess.SubprocessError) as exc:
        print(f'Error: {exc}', file=sys.stderr)
        if created:
            print(f'Diagnostics retained: {output}', file=sys.stderr)
            print('Inspect before any manual retry; this script never resubmits:', file=sys.stderr)
            for action in ('history', 'info', 'log'):
                command = ['xcrun', 'notarytool', action]
                if action != 'history':
                    command += [sid or 'SUBMISSION_ID_FROM_HISTORY']
                print('  ' + shlex.join(command + auth), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
