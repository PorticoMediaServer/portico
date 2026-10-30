#!/usr/bin/env python3
"""Build the pinned patched dependency in a new isolated directory; never publish."""
import argparse, hashlib, json, os, pathlib, shutil, subprocess
BASE = pathlib.Path(__file__).resolve().parent
VERSION = '1.7.2-portico.1'
ARCHIVE_SHA = '04bd1c1e08f62183f028845eefbe5067bb2a891982258680ea1af4bad6ae06f9'
LOCK_SHA = '85eb71253ccb37f3cd8ae15e2fdb88437bf0917684c46ad4c942a3514546f3a4'
SOURCE_SHA = '70f4aec2999f1f0950c8cac9782fe67b6e2f5a5f6e4202b017ba7097bc8ac445'
def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()
def run(args, cwd, env=None): subprocess.run(args, cwd=cwd, env=env, check=True)
def pack(source, output):
    stage = output / 'package-staging'
    stage.mkdir()
    # Ship only complete engine variants, worker, declarations, maps and source.
    shutil.copytree(source / 'src', stage / 'src')
    (stage / 'dist').mkdir()
    for path in (source / 'dist').iterdir():
        if path.is_file() and path.name.startswith('hls') and not path.name.startswith('hls-demo'):
            shutil.copy2(path, stage / 'dist' / path.name)
    shutil.copy2(source / 'LICENSE', stage / 'LICENSE')
    pkg = json.loads((source / 'package.json').read_text())
    keep = ['name', 'version', 'license', 'description', 'homepage', 'authors', 'repository', 'bugs', 'main', 'module', 'types', 'exports', 'files']
    pkg = {key: pkg[key] for key in keep if key in pkg}
    pkg['private'] = True
    assert pkg['version'] == VERSION
    (stage / 'package.json').write_text(json.dumps(pkg, indent=2) + '\n')
    run(['npm', 'pack', '--ignore-scripts', '--pack-destination', str(output)], stage)
    return output / ('hls.js-' + VERSION + '.tgz')
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=pathlib.Path, help='New empty build directory; output is not installed automatically')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    archive = output / 'upstream.tar.gz'
    run(['curl', '-fL', '--proto', '=https', '--tlsv1.2', 'https://codeload.github.com/video-dev/hls.js/tar.gz/refs/tags/v1.7.2', '-o', str(archive)], output)
    assert sha(archive) == ARCHIVE_SHA, 'Upstream archive changed'
    run(['tar', '-xzf', str(archive)], output)
    source = output / 'hls.js-1.7.2'
    assert sha(source / 'package-lock.json') == LOCK_SHA
    assert sha(source / 'src/utils/level-helper.ts') == SOURCE_SHA
    run(['patch', '-p1', '-i', str(BASE / 'anchored-fragment-boundary.patch')], source)
    for name in ['package.json', 'package-lock.json']:
        path = source / name
        data = json.loads(path.read_text())
        data['version'] = VERSION
        if name == 'package-lock.json': data['packages']['']['version'] = VERSION
        path.write_text(json.dumps(data, indent=2) + '\n')
    (output / 'tmp').mkdir()
    env = dict(os.environ, TMPDIR=str(output / 'tmp'))
    run(['npm', 'ci', '--ignore-scripts', '--no-audit', '--no-fund', '--cache', str(output / 'cache')], source, env)
    run(['npm', 'run', 'build'], source, env)
    artifact = pack(source, output)
    print(json.dumps({'artifact': str(artifact), 'sha256': sha(artifact)}))
if __name__ == '__main__': main()
