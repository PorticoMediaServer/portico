#!/usr/bin/env python3
"""Two hosted jobs prove native dependency reuse; never builds an engine."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('prefix_cache', ROOT / 'scripts/engine-prefix-cache.py')
cache_module = importlib.util.module_from_spec(spec); spec.loader.exec_module(cache_module)


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def control(phase, directory, cache):
    if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted':
        raise ValueError('actual hosted control context required')
    directory.mkdir(parents=True, exist_ok=False)
    roots = dict(prefix=directory / 'prefix', sources=directory / 'sources')
    for root in roots.values(): root.mkdir()
    cc = Path(shutil.which('cc')).resolve(strict=True)
    compiler = dict(path=str(cc), sha256=digest(cc), version=subprocess.check_output([cc, '--version'], text=True).splitlines()[0])
    library = b'int portico_cache_fixture(void) { return 7; }\n'
    inputs = dict(stage='native-fixture-library', compiler=compiler,
                  sourceSHA256=hashlib.sha256(library).hexdigest(), flags=['-O2', '-c', 'ar-rcs'])
    store = cache_module.PrefixCache(cache, directory / 'state')
    result = store.begin(inputs, roots)
    if phase == 'seed' and result['hit']: raise ValueError('seed must execute its real compiler')
    if phase == 'reuse' and not result['hit']: raise ValueError('warm job must restore the exact dependency instead of recompiling')
    compiles = 0
    if not result['hit']:
        source = roots['sources'] / 'fixture.c'; source.write_bytes(library)
        obj = directory / 'fixture.o'
        subprocess.run([cc, '-O2', '-c', source, '-o', obj], check=True)
        subprocess.run(['ar', 'rcs', roots['prefix'] / 'libfixture.a', obj], check=True,
                       env=dict(os.environ, ZERO_AR_DATE='1'))
        (roots['sources'] / 'stage-receipt.json').write_text(json.dumps(dict(inputs=inputs, qualified=False, release=False)))
        compiles += 1; store.finish(inputs, roots)
    if (roots['sources'] / 'fixture.c').read_bytes() != library:
        raise ValueError('original source did not accompany dependency cache hit')
    receipts = json.loads((roots['sources'] / 'stage-receipt.json').read_text())
    if receipts['inputs'] != inputs: raise ValueError('dependency stage receipt changed')
    # Terminal stages never use the prefix cache. Both real native compiles
    # link and execute, including after only terminal configure changes.
    program = directory / 'main.c'
    program.write_text('int portico_cache_fixture(void); int main(void) { return portico_cache_fixture() == 7 ? 0 : 1; }\n')
    outputs = []
    for index in (1, 2):
        output = directory / ('terminal-' + str(index))
        subprocess.run([cc, '-O2', '-DPORTICO_UNUSED_CONFIG=' + str(index), program,
                        roots['prefix'] / 'libfixture.a', '-o', output], check=True)
        subprocess.run([output], check=True); outputs.append(dict(round=index, sha256=digest(output), ran=True))
    for root in roots.values(): shutil.rmtree(root); root.mkdir()
    changed = dict(inputs, sourceSHA256=hashlib.sha256(library.replace(b'return 7', b'return 8')).hexdigest())
    if store.begin(changed, roots)['hit']: raise ValueError('changed dependency source must invalidate cache')
    report = dict(schemaVersion=1, phase=phase, cacheHit=result['hit'], dependencyCompileInvocations=compiles,
        terminalCompileInvocations=2, originalSourceAndReceipt=True, changedSourceRefused=True,
        terminals=outputs, compiler=compiler, qualified=False, release=False, engineBuild=False)
    path = directory.parent / ('prefix-cache-' + phase + '.json')
    path.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase', choices=('seed', 'reuse'))
    parser.add_argument('--work', type=Path, required=True); parser.add_argument('--cache', type=Path, required=True)
    args = parser.parse_args(); control(args.phase, args.work.resolve(), args.cache.resolve())
