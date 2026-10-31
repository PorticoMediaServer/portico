#!/usr/bin/env python3
"""Explicit construction context; never grants Portico release custody."""
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = 'PorticoMediaServer/portico/.github/workflows/packaging-qualification.yml'


def guard(env, context):
    for key in env:
        if any(word in key.upper() for word in ('SIGNING', 'NOTAR', 'APPLE_ID', 'CSC_', 'PRODUCTKEY', 'SIGNINGCONFIG')):
            raise ValueError('signing/product configuration forbidden: ' + key)
    if context == 'local':
        if any(key in env for key in ('GITHUB_ACTIONS', 'GITHUB_WORKFLOW_REF', 'GITHUB_RUN_ID')):
            raise ValueError('local qualification must not impersonate GitHub Actions')
        return {'context': 'local', 'release': False}
    if (context != 'github-actions' or env.get('GITHUB_ACTIONS') != 'true' or
            env.get('GITHUB_WORKFLOW_REF', '').split('@')[0] != WORKFLOW or
            env.get('GITHUB_REPOSITORY') != 'PorticoMediaServer/portico' or
            env.get('GITHUB_EVENT_NAME') not in ('pull_request', 'workflow_dispatch') or
            env.get('GITHUB_REF', '').startswith('refs/tags/')):
        raise ValueError('only the dedicated qualification workflow is allowed')
    return {'context': context, 'runId': env['GITHUB_RUN_ID'],
            'runAttempt': env['GITHUB_RUN_ATTEMPT'], 'release': False}


def receipt(env, context):
    result = guard(env, context)
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    if context == 'github-actions':
        expected = env.get('GITHUB_SHA')
        if env['GITHUB_EVENT_NAME'] == 'pull_request':
            request = json.loads(Path(env['GITHUB_EVENT_PATH']).read_text())['pull_request']
            if request['head']['repo']['full_name'] != 'PorticoMediaServer/portico':
                raise ValueError('same-repository qualification only')
            expected = request['head']['sha']
        if sha != expected:
            raise ValueError('qualification checkout differs from event head')
    return dict(result, installerSourceSHA=sha)
