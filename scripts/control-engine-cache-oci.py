#!/usr/bin/env python3
"""Real hosted ORAS registry round-trip; tiny inert fixtures, never an engine."""
import argparse
import json
import lzma
import os
from pathlib import Path
import shutil

import importlib.util
spec = importlib.util.spec_from_file_location('cache_oci', Path(__file__).with_name('engine-cache-oci.py'))
OCI = importlib.util.module_from_spec(spec); spec.loader.exec_module(OCI)


def control(target, work):
    OCI.guard(fixture=True); work.mkdir(parents=True, exist_ok=False)
    directory = work / 'cache'; directory.mkdir()
    plan = OCI.module('engine-dependency-cache')
    plan.plan(target, directory, work / 'plan.out')
    prefix = work / 'prefix'; prefix.mkdir()
    stage = 'macos' if target == 'macos-arm64' else 'linux-native'
    api = OCI.module('engine-prefix-cache'); cache = api.PrefixCache(directory / stage, work / 'state')
    inputs = dict(fixture='OCI transport only, NOT-FOR-USE')
    assert not cache.begin(inputs, {'prefix': prefix})['hit']
    executable = prefix / 'inert-tool'; executable.write_bytes(b'not an executable\n'); executable.chmod(0o755)
    (prefix / 'LICENSE').write_text('inert fixture notice\n')
    cache.finish(inputs, {'prefix': prefix})
    scope = OCI.identity(target, 'stages', directory)
    transport = OCI.Transport(target, fixture=True)
    saved = transport.save(directory, scope, work)
    restored = work / 'restored'
    reused = transport.restore(restored, scope, work)
    assert reused['hit'] and reused['snapshot'] == saved['snapshot']
    assert OCI.inventory(restored, 'stages') == OCI.inventory(directory, 'stages')
    shutil.rmtree(prefix); prefix.mkdir()
    second = api.PrefixCache(restored / stage, work / 'state2')
    assert second.begin(inputs, {'prefix': prefix})['hit']
    assert executable.read_bytes() == b'not an executable\n' and executable.stat().st_mode & 0o777 == 0o755
    unchanged = transport.save(restored, scope, work, before=saved['snapshot'])
    assert unchanged['saved'] is False
    # A different compiler scope must not accept this exact artifact.
    try: transport.pull(saved['reference'], work / 'wrong-scope', dict(scope, namespace=scope['namespace'] + 'changed'), work)
    except ValueError: pass
    else: raise ValueError('cross-compiler OCI artifact accepted')
    report = dict(target=target, stages=saved, stageScope=scope, actualPrefixCacheHit=True, unchangedSaveSkipped=True,
                  qualified=False, release=False, readyForPublication=False)
    if target in ('linux-x64', 'linux-arm64'):
        source = work / 'source-cache'; source.mkdir()
        name = '50-fixture_' + 'a' * 64 + '.tar.xz'; (source / name).write_bytes(lzma.compress(b'inert exact source fixture'))
        (source / 'receipt.json').write_text(json.dumps({name: OCI.digest(source / name)}))
        source_target = 'windows-x64' if target == 'linux-x64' else 'linux-arm64'
        source_scope = OCI.identity(source_target, 'sources', source)
        source_transport = OCI.Transport(source_target, fixture=True)
        report['sources'] = source_transport.save(source, source_scope, work)
        report['sourceScope'] = source_scope
        source_transport.restore(work / 'sources-restored', source_scope, work)
        assert (work / 'sources-restored' / name).read_bytes() == (source / name).read_bytes()
    (work / 'oci-transport-control.json').write_text(json.dumps(report, indent=2) + '\n')
    return report


def read_control(reports, work):
    OCI.guard(fixture=True); work.mkdir(parents=True, exist_ok=False)
    seen, results = set(), []
    for path in sorted(reports.rglob('oci-transport-control.json')):
        report = json.loads(path.read_text()); target = report['target']
        if target in seen or target not in ('linux-x64', 'linux-arm64', 'macos-arm64'):
            raise ValueError('unexpected or duplicate fixture report')
        seen.add(target)
        for key, field in (('stages', 'stageScope'), ('sources', 'sourceScope')):
            if key not in report: continue
            scope = report[field]; destination = work / (target + '-' + key)
            transport = OCI.Transport(scope['target'], fixture=True)
            restored = transport.pull(report[key]['reference'], destination, scope, work, exact_input=True)
            if restored['snapshot'] != report[key]['snapshot']: raise ValueError('read-only fixture snapshot changed')
            results.append(dict(target=target, kind=key, snapshot=restored['snapshot']))
    if seen != {'linux-x64', 'linux-arm64', 'macos-arm64'}: raise ValueError('all three fixture packages required')
    result = dict(readOnlyTokenPulls=results, qualified=False, release=False)
    (work / 'read-only-control.json').write_text(json.dumps(result, indent=2) + '\n')
    return result


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__); p.add_argument('phase', nargs='?', default='write', choices=('write', 'read'))
    p.add_argument('--target', choices=('linux-x64', 'linux-arm64', 'macos-arm64')); p.add_argument('--reports', type=Path)
    p.add_argument('--work', type=Path, required=True); a = p.parse_args()
    if a.phase == 'write':
        if not a.target: p.error('one fixture target required')
        print(json.dumps(control(a.target, a.work)))
    else:
        if not a.reports: p.error('downloaded fixture reports required')
        print(json.dumps(read_control(a.reports, a.work)))
