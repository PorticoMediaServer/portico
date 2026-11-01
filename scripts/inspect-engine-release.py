#!/usr/bin/env python3
"""Read-only release evidence preflight. Never creates a qualified engine or release."""
import argparse
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = ('windows-x64', 'linux-x64', 'linux-arm64', 'macos-arm64')
OPTIONAL = ('macos-x64',)
TOOLS = ('ffmpeg', 'ffprobe', 'portico-disc')
COMMON_GATES = ('verify-ffmpeg-manifest.py', 'verify-ffmpeg-features.py',
                'verify-disc-bundle.py', 'verify-ffmpeg-recipes.py')
SHA256 = re.compile(r'^[a-f0-9]{64}$')


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def unique(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate JSON field: ' + key)
        result[key] = value
    return result


def read(path, directory):
    if (path.is_symlink() or not path.resolve(strict=True).is_relative_to(directory.resolve(strict=True)) or
            not path.is_file() or path.stat().st_size > 4 * 1024**2):
        raise ValueError('missing, linked, external or oversized evidence: ' + str(path))
    return json.loads(path.read_text(), object_pairs_hook=unique)


def gate_names(target):
    extra = ('verify-ffmpeg-abi.py',) if target.startswith('linux-') else (
        ('macos-engine-runtime.py',) if target.startswith('macos-') else ())
    return (*COMMON_GATES, *extra)


def inspect_target(target, directory, identity, root=ROOT):
    issues = []
    result = {'target': target, 'receiptChecksPassed': False, 'holds': issues}
    def require(condition, message):
        if not condition:
            issues.append(message)
    if directory.is_symlink() or not directory.is_dir():
        issues.append('target evidence directory missing or linked')
        return result
    def load(relative):
        try:
            return read(directory / relative, directory)
        except (OSError, ValueError) as error:
            issues.append(relative + ': ' + str(error))
            return None
    sender = load('product/transfer.json')
    native = load('native/cold-native.json')
    gate = load('native/gates/native-engine.json')
    manifest = load('native/toolchain-manifest.json')
    repeat = load('native/repeat-build.json')
    for name, receipt in [('construction', sender), ('native', native)]:
        if receipt is None:
            continue
        require(isinstance(receipt, dict), name + ' receipt must be an object')
        if not isinstance(receipt, dict):
            continue
        require(all(receipt.get(key) == value and type(receipt.get(key)) is type(value)
                    for key, value in identity.items()), name + ' source/run/attempt/context mismatch')
        require(receipt.get('target') == target, name + ' target mismatch')
        require(receipt.get('qualified') is False and receipt.get('release') is False,
                name + ' intermediate was unexpectedly labelled qualified/release')
    if isinstance(sender, dict):
        require(sender.get('constructionPassed') is True, 'construction did not pass')
        require(all(isinstance(sender.get(key), str) and SHA256.fullmatch(sender[key]) for key in
                    ('bundleArchiveSHA256', 'correspondingSourceSHA256')), 'construction archive digests malformed')
    if isinstance(native, dict):
        require(native.get('nativeEngineChecks') is True, 'native engine checks did not pass')
    names = gate_names(target)
    if not isinstance(gate, dict):
        require(False, 'native gate receipt missing or malformed')
    else:
        require(gate.get('target') == target and gate.get('context') == 'github-actions' and gate.get('release') is False,
                'native gate identity mismatch')
        require(gate.get('nativeEngineChecks') is True, 'native gate failed')
        require(gate.get('fullCompilerAcceptance') is True, 'full compiler/runtime-source acceptance remains blocked')
        expected_scripts = {name: digest(root / 'scripts' / name) for name in names}
        require(gate.get('qualifierScripts') == expected_scripts, 'qualifier scripts differ from checked publisher source')
        reports = gate.get('reports', {})
        require(isinstance(reports, dict) and set(reports) == set(names) - {'verify-ffmpeg-manifest.py'},
                'native report set is incomplete or unexpected')
        if isinstance(reports, dict):
            for name, expected in reports.items():
                if name not in names:
                    continue
                path = directory / 'native/gates' / (name + '.json')
                try:
                    read(path, directory)
                    require(isinstance(expected, str) and SHA256.fullmatch(expected) and digest(path) == expected,
                            'native report bytes changed: ' + name)
                except (OSError, ValueError) as error:
                    issues.append('native report unavailable: ' + name + ': ' + str(error))
    if isinstance(manifest, dict):
        manifest_sha = digest(directory / 'native/toolchain-manifest.json')
        require(isinstance(sender, dict) and sender.get('toolchainManifestSHA256') == manifest_sha,
                'manifest differs from exact construction transfer')
        require(isinstance(gate, dict) and gate.get('toolchainManifestSHA256') == manifest_sha,
                'manifest differs from native proof')
        lock = read(root / 'third_party/ffmpeg/sources.lock.json', root)
        require(manifest.get('target') == target and manifest.get('licenseMode') == 'gpl3' and
                manifest.get('buildId') == lock['releaseTag'] and manifest.get('ffmpegVersion') == lock['ffmpeg']['version'],
                'manifest target/version/GPLv3/release tag mismatch')
        files = manifest.get('files', {})
        require(isinstance(files, dict), 'manifest inventory malformed')
        if isinstance(files, dict):
            require(all(isinstance(sha, str) and SHA256.fullmatch(sha) for sha in files.values()),
                    'manifest contains malformed SHA-256')
            for name in ('sources.lock.json', 'requirements.v1.json'):
                require(files.get(name) == digest(root / 'third_party/ffmpeg' / name), 'checked contract changed: ' + name)
            require(any(name.startswith('LICENSES/') for name in files), 'licence inventory missing')
            require(isinstance(sender, dict) and files.get('corresponding-source.tar.xz') == sender.get('correspondingSourceSHA256')
                    and bool(files.get('corresponding-source.tar.xz')), 'source archive does not match transfer')
            if repeat is not None:
                require(files.get('repeat-build.json') == digest(directory / 'native/repeat-build.json'),
                        'repeat evidence differs from manifest')
            rounds_ok = isinstance(repeat, list) and len(repeat) == 2 and all(isinstance(row, dict) for row in repeat)
            require(rounds_ok, 'two actual compile rounds are required')
            if rounds_ok:
                require([row.get('round') for row in repeat] == [1, 2] and all(type(row.get('round')) is int for row in repeat), 'repeat round identities differ')
                source_sha = repeat[0].get('sourceArchiveSHA256')
                require(isinstance(source_sha, str) and SHA256.fullmatch(source_sha) and
                        repeat[1].get('sourceArchiveSHA256') == source_sha, 'repeat source archives differ')
                binaries = {name: files.get('bin/' + name + ('.exe' if target.startswith('windows-') else '')) for name in TOOLS}
                require(all(isinstance(value, str) and SHA256.fullmatch(value) for value in binaries.values()),
                        'repeat executable inventory missing')
                require(all(row.get('binaries') == binaries for row in repeat), 'repeat executable hashes differ')
        result['toolchainManifestSHA256'] = manifest_sha
    else:
        require(False, 'toolchain manifest missing or malformed')
    result['receiptChecksPassed'] = not issues
    return result


def inspect_set(inputs, source_sha, run_id, attempt, root=ROOT):
    if not re.fullmatch(r'[a-f0-9]{40}', source_sha) or any(type(n) is not int or n <= 0 for n in (run_id, attempt)):
        raise ValueError('exact source SHA and positive run/attempt required')
    identity = {'engineSourceSHA': source_sha, 'runId': run_id, 'runAttempt': attempt, 'context': 'github-actions'}
    unknown = [p.name for p in inputs.iterdir() if p.is_dir() and p.name not in (*REQUIRED, *OPTIONAL)]
    if unknown:
        raise ValueError('unexpected target directories: ' + ', '.join(sorted(unknown)))
    results = {target: inspect_target(target, inputs / target, identity, root)
               for target in (*REQUIRED, *OPTIONAL) if target in REQUIRED or (inputs / target).exists()}
    return {'schemaVersion': 1, 'kind': 'engine-release-receipt-preflight', 'context': 'read-only-inspection',
            'expectedProducer': identity, 'qualified': False, 'release': False, 'readyForPublication': False,
            'preflightPassed': all(results[target]['receiptChecksPassed'] for target in REQUIRED),
            'targets': results, 'remainingBoundary':
            'Receipt inspection only. Exact Actions locator/digest intake, complete artifact/source/licence validation and the main-only publisher remain separate.'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', type=Path, required=True)
    parser.add_argument('--source-sha', required=True)
    parser.add_argument('--run-id', type=int, required=True)
    parser.add_argument('--attempt', type=int, required=True)
    args = parser.parse_args()
    report = inspect_set(args.inputs.resolve(strict=True), args.source_sha, args.run_id, args.attempt)
    print(json.dumps(report, indent=2))
    raise SystemExit(0 if report['preflightPassed'] else 1)
