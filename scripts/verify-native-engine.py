#!/usr/bin/env python3
"""Run existing engine gates natively; never grant release/compiler acceptance."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
TARGETS = {'windows-x64': ('Windows', {'amd64', 'x86_64'}),
           'linux-x64': ('Linux', {'amd64', 'x86_64'}),
           'linux-arm64': ('Linux', {'arm64', 'aarch64'}),
           'windows-arm64': ('Windows', {'arm64', 'aarch64'}),
           'macos-arm64': ('Darwin', {'arm64', 'aarch64'}),
           'macos-x64': ('Darwin', {'amd64', 'x86_64'})}


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def native(target, context, env):
    expected_os, machines = TARGETS[target]
    if platform.system() != expected_os or platform.machine().lower() not in machines:
        raise ValueError('matching native OS/architecture required; no emulated qualification')
    if target == 'windows-arm64' and env.get('PROCESSOR_ARCHITECTURE', '').lower() != 'arm64':
        raise ValueError('native ARM64 Python required')
    for key in env:
        if any(word in key.upper() for word in ('SIGNING', 'NOTAR', 'APPLE_ID', 'CSC_', 'PRODUCTKEY', 'SIGNINGCONFIG')):
            raise ValueError('signing configuration forbidden in engine qualification')
    if context == 'local':
        if any(key in env for key in ('GITHUB_ACTIONS', 'GITHUB_RUN_ID', 'GITHUB_WORKFLOW_REF')):
            raise ValueError('local engine qualification must not impersonate GitHub')
    elif (context != 'github-actions' or env.get('GITHUB_ACTIONS') != 'true' or
          env.get('GITHUB_REPOSITORY') != 'PorticoMediaServer/portico' or
          env.get('RUNNER_ENVIRONMENT') != 'github-hosted' or
          env.get('GITHUB_EVENT_NAME') != 'workflow_dispatch' or
          env.get('GITHUB_WORKFLOW_REF', '').split('@')[0] !=
          'PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml' or
          not env.get('GITHUB_REF', '').startswith('refs/heads/')):
        raise ValueError('dedicated manual engine qualification workflow required')


def verify(target, bundle, evidence, context):
    native(target, context, os.environ)
    evidence.mkdir(parents=True, exist_ok=True)
    receipt = {'schemaVersion': 1, 'target': target, 'context': context, 'release': False,
        'nativeEngineChecks': False, 'fullCompilerAcceptance': False, 'qualifierScripts': {},
        'unexercised': ['physical hardware codec availability', 'full platform/compiler matrix',
                        'installer construction and installation', 'Portico integrity seal', 'OS signing']}
    manifest_path = bundle / 'toolchain-manifest.json'
    manifest_sha = digest(manifest_path)
    manifest = json.loads(manifest_path.read_text())
    if manifest['target'] != target:
        raise ValueError('native bundle target mismatch')
    if digest(bundle / 'requirements.v1.json') != digest(ROOT / 'third_party/ffmpeg/requirements.v1.json'):
        raise ValueError('native bundle feature contract mismatch')
    lock = json.loads((bundle / 'sources.lock.json').read_text())
    receipt.update(toolchainManifestSHA256=manifest_sha, engineBuildId=manifest['buildId'], reports={})
    commands = [('verify-ffmpeg-manifest.py', []), ('verify-ffmpeg-features.py', []),
        ('verify-disc-bundle.py', ['--fixtures', ROOT / lock['discHelper']['fixtureManifest'],
                                 '--fixture-sha256', lock['discHelper']['fixtureManifestSHA256']]),
        ('verify-ffmpeg-recipes.py', ['--scratch', evidence,
                                    '--font', ROOT / 'packages/reader-core/assets/Literata[opsz,wght].ttf'])]
    if target.startswith('macos-'):
        commands.insert(1, ('macos-engine-runtime.py', ['verify', '--target', target]))
    if target.startswith('linux-'):
        commands.insert(1, ('verify-ffmpeg-abi.py', []))
    try:
        for name, args in commands:
            receipt['qualifierScripts'][name] = digest(ROOT / 'scripts' / name)
            report = evidence / (name + '.json')
            command = [sys.executable, ROOT / 'scripts' / name, bundle, *args]
            if name == 'macos-engine-runtime.py':
                command = [sys.executable, ROOT / 'scripts' / name, *args, '--bundle', bundle]
            if name != 'verify-ffmpeg-manifest.py':
                command += ['--report', report]
            with (evidence / (name + '.log')).open('w') as output:
                subprocess.run([str(x) for x in command], stdout=output, stderr=subprocess.STDOUT, check=True, timeout=3600)
            if report.exists():
                receipt['reports'][name] = digest(report)
        if digest(manifest_path) != manifest_sha:
            raise ValueError('engine manifest changed during native checks')
        subprocess.run([sys.executable, ROOT / 'scripts/verify-ffmpeg-manifest.py', bundle], check=True)
        receipt['nativeEngineChecks'] = True
    except Exception as error:
        receipt['failure'] = str(error)
        raise
    finally:
        temporary = evidence / 'native-engine.new'
        temporary.write_text(json.dumps(receipt, indent=2) + '\n')
        temporary.replace(evidence / 'native-engine.json')
    return receipt


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=TARGETS)
    parser.add_argument('--context', choices=['local', 'github-actions'], required=True)
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    verify(args.target, args.bundle.resolve(), args.evidence.resolve(), args.context)
