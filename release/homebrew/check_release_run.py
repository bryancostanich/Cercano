#!/usr/bin/env python3
"""Read-only provenance gate for tap-only recovery from an existing CI run."""
import argparse
import os
import re
from update_tap import api, Failure, require
from render_formula import parse_version, ValidationError

BASE = '/repos/cercano-ai/Cercano/'


def check(version, run_id):
    parse_version(version)
    require(bool(re.fullmatch(r'[1-9][0-9]*', run_id)), 'Invalid Actions run ID')
    token = os.environ.get('GITHUB_TOKEN', '')
    release = api('GET', BASE + 'releases/tags/v' + version, token)
    require(release.get('tag_name') == 'v' + version and release.get('draft') is False,
            'Requested release is not published')
    commit = api('GET', BASE + 'commits/v' + version, token)
    run = api('GET', BASE + 'actions/runs/' + run_id, token)
    require(run.get('head_sha') == commit.get('sha') and bool(commit.get('sha')),
            'CI run does not match the published tag commit')
    require(run.get('path') == '.github/workflows/release-macos.yml'
            and run.get('event') == 'workflow_dispatch'
            and run.get('status') == 'completed'
            and run.get('head_repository', {}).get('full_name') == 'cercano-ai/Cercano',
            'Not a completed trusted release workflow run')
    data = api('GET', BASE + 'actions/runs/' + run_id + '/jobs?filter=latest&per_page=100', token)
    require(data.get('total_count', 0) <= 100, 'Unexpected job count')
    jobs = data.get('jobs', [])
    for name in ('Build, sign, notarize and verify', 'Publish to GitHub Releases'):
        matches = [j for j in jobs if j.get('name') == name]
        require(len(matches) == 1 and matches[0].get('conclusion') == 'success',
                'Successful signing and publication jobs are required')
    return commit['sha']


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--version', required=True)
    p.add_argument('--run-id', required=True)
    a = p.parse_args()
    try:
        print('Verified release source:', check(a.version, a.run_id))
    except (Failure, ValidationError, ValueError, KeyError):
        raise SystemExit('Release provenance validation failed; no tap update attempted')
