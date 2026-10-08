#!/usr/bin/env python3
"""Publish the verified release formula to one fixed tap path, with optimistic locking.

Only this command writes remotely. Tokens are environment-only. Public artifact
requests never carry credentials. Re-running after success is a no-op.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import sys
import urllib.error
import urllib.parse
import urllib.request

import render_formula as renderer
from promote_to_tap import read_formula_fields, PromotionError

TAP_PATH = '/repos/cercano-ai/homebrew-tap/contents/Formula/cercano.rb'
RELEASE_PATH = '/repos/cercano-ai/Cercano/releases/tags/'
MAX_ARCHIVE = 512 * 1024 * 1024


class Failure(Exception):
    def __init__(self, message, status=None):
        super().__init__(message)
        self.status = status


def require(ok, message):
    if not ok:
        raise Failure(message)


def public_url(url):
    p = urllib.parse.urlsplit(url)
    host = p.hostname or ''
    require(p.scheme == 'https' and not p.username and not p.password
            and p.port in (None, 443)
            and any(host == d or host.endswith('.' + d)
                    for d in ('github.com', 'githubusercontent.com')),
            'Refusing non-HTTPS or non-GitHub artifact redirect')


class PublicRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        public_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise Failure('Refusing authenticated API redirect')


def api(method, path, token, payload=None):
    # No caller-supplied host; authentication cannot leave api.github.com.
    headers = {'Accept': 'application/vnd.github+json',
               'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'cercano-tap-release'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    data = json.dumps(payload).encode() if payload is not None else None
    if data is not None:
        headers['Content-Type'] = 'application/json'
    req = urllib.request.Request('https://api.github.com' + path, data=data,
                                 headers=headers, method=method)
    try:
        with urllib.request.build_opener(NoRedirect()).open(req, timeout=60) as r:
            body = r.read(8 * 1024 * 1024 + 1)
            require(len(body) <= 8 * 1024 * 1024, 'Oversized API response')
            return json.loads(body)
    except urllib.error.HTTPError as e:
        # Never print response bodies, URLs, headers or credentials.
        raise Failure(f'GitHub {method} failed: HTTP {e.code}', e.code) from None
    except urllib.error.URLError:
        raise Failure('GitHub API connection failed') from None


def download(url, checksum=False):
    public_url(url)
    limit = 4096 if checksum else MAX_ARCHIVE
    size, digest, chunks = 0, hashlib.sha256(), []
    try:
        with urllib.request.build_opener(PublicRedirect()).open(url, timeout=120) as r:
            public_url(r.geturl())
            require(r.status == 200, 'Public release download did not return HTTP 200')
            while chunk := r.read(1024 * 1024):
                size += len(chunk)
                require(size <= limit, 'Public release download exceeds size limit')
                digest.update(chunk)
                if checksum:
                    chunks.append(chunk)
    except (urllib.error.URLError, OSError):
        raise Failure('Public release download failed; retry the tap job after checking assets') from None
    require(size > 0, 'Empty release download')
    return b''.join(chunks).decode('utf-8') if checksum else digest.hexdigest()


def published(version, name, digest):
    release = api('GET', RELEASE_PATH + 'v' + version, os.environ.get('GITHUB_TOKEN', ''))
    require(release.get('tag_name') == 'v' + version and release.get('draft') is False,
            'Release is not published for the requested version')
    names = {a['name'] for a in release.get('assets', [])}
    require({name, name + '.sha256'} <= names, 'Published macOS assets are incomplete')
    url = renderer.URL_TEMPLATE.format(version=version, name=name)
    require(download(url) == digest, 'Published archive checksum differs from trusted build')
    require(download(url + '.sha256', checksum=True).split() == [digest, name],
            'Published checksum sidecar differs from trusted build')


def prepare(version, digest, archive, formula):
    renderer.parse_version(version)
    renderer.parse_digest(digest)
    require(renderer.hash_archive(archive) == digest, 'CI archive checksum mismatch')
    renderer.validate_archive(archive, version)
    expected = renderer.render(Path(__file__).with_name('cercano.rb.in').read_text(),
                               version, archive.name, digest)
    require(formula.read_text() == expected, 'CI formula differs from trusted release template')
    return expected


def decision(existing, desired, version, digest):
    if existing is None:
        return 'update'
    fields = read_formula_fields(existing)
    old_version = renderer.parse_version(fields['version'])
    old = tuple(map(int, old_version.split('.')))
    new = tuple(map(int, version.split('.')))
    require(old <= new, 'Tap already has a newer version; refusing downgrade')
    if old == new:
        require(fields['sha256'] == digest, 'Same version has different checksum; refusing replacement')
        require(existing == desired, 'Same version has different formula; refusing replacement')
        return 'unchanged'
    return 'update'


def update(version, digest, archive, formula, token):
    require(bool(token), 'Configure HOMEBREW_TAP_TOKEN in the release environment, then retry only Update Homebrew tap')
    desired = prepare(version, digest, archive, formula)
    published(version, archive.name, digest)
    for attempt in range(3):
        sha, existing = None, None
        try:
            current = api('GET', TAP_PATH + '?ref=main', token)
            require(current.get('type') == 'file' and current.get('encoding') == 'base64',
                    'Unexpected tap formula object')
            sha = current['sha']
            existing = base64.b64decode(current['content']).decode('utf-8')
        except Failure as e:
            if e.status != 404:
                raise
        if decision(existing, desired, version, digest) == 'unchanged':
            return 'unchanged'
        payload = {'branch': 'main', 'message': f'chore(cercano): update to {version}',
                   'content': base64.b64encode(desired.encode()).decode()}
        if sha:
            payload['sha'] = sha
        try:
            api('PUT', TAP_PATH, token, payload)
            return 'updated'
        except Failure as e:
            if e.status != 409:
                raise
    raise Failure('Tap changed repeatedly; retry only the tap job. No force update performed.')


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument('--version', required=True)
    p.add_argument('--sha256', required=True)
    p.add_argument('--archive', type=Path, required=True)
    p.add_argument('--formula', type=Path, required=True)
    a = p.parse_args()
    try:
        result = update(a.version, a.sha256, a.archive, a.formula,
                        os.environ.get('HOMEBREW_TAP_TOKEN', ''))
    except (Failure, renderer.ValidationError, PromotionError) as e:
        print(f'Tap update refused: {e}', file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError):
        print('Tap update failed: unreadable input or malformed response; no force update attempted', file=sys.stderr)
        return 1
    print(f'Homebrew tap {result}: Cercano {a.version}')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
