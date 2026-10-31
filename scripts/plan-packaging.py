#!/usr/bin/env python3
"""Report available pinned engines; a missing engine is an explicit test block."""
import argparse
import json
import os
from pathlib import Path
import runpy

ROOT = Path(__file__).resolve().parents[1]
ENGINE = runpy.run_path(str(ROOT / 'scripts/fetch-engine.py'))
CONTEXT = runpy.run_path(str(ROOT / 'scripts/qualification-context.py'))
RUNNERS = {'windows-x64': 'windows-2025', 'windows-arm64': 'windows-11-arm', 'macos-arm64': 'macos-15'}


def plan(lock):
    if lock.get('schemaVersion') != 1 or lock.get('maximumRetentionDays') != 14 or set(lock['engines']) - RUNNERS.keys():
        raise ValueError('invalid installer engine lock')
    included, blocked = [], {}
    for target, runner in RUNNERS.items():
        if target not in lock['engines']:
            blocked[target] = 'No accepted engine pin; installer construction and runtime checks blocked until an engine is available.'
        else:
            ENGINE['select'](lock, target)
            included.append({'target': target, 'runner': runner})
    return {'matrix': {'include': included}, 'blocked': blocked, 'packagingQualified': False}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', choices=['local', 'github-actions'], required=True)
    parser.add_argument('--lock', type=Path, default=ROOT / 'third_party/ffmpeg/artifacts.lock.json')
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    report = dict(plan(json.loads(args.lock.read_text())), **CONTEXT['receipt'](os.environ, args.context))
    args.evidence.mkdir(parents=True, exist_ok=True)
    (args.evidence / 'installer-plan.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))
    if args.context == 'github-actions':
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write('available=' + str(bool(report['matrix']['include'])).lower() + '\n')
            output.write('matrix=' + json.dumps(report['matrix']) + '\n')
        with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as summary:
            summary.write('## Installer qualification\n\n')
            for target, reason in report['blocked'].items():
                summary.write(f'- **BLOCKED {target}:** {reason}\n')
            summary.write('\nPlanner/control success is not engine or installer acceptance. No engine is compiled in this workflow.\n')
