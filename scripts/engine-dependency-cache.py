#!/usr/bin/env python3
"""Plan hosted dependency cache transport without granting engine acceptance."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ('windows-x64', 'linux-x64', 'linux-arm64', 'macos-arm64', 'macos-x64')


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def compiler(target):
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    result = dict(target=target, architecture=platform.machine())
    if target.startswith('macos-'):
        result.update(xcode=subprocess.check_output(['xcodebuild', '-version'], text=True).strip(),
            sdk=subprocess.check_output(['/usr/bin/xcrun', '--show-sdk-version'], text=True).strip(),
            clang=subprocess.check_output(['/usr/bin/xcrun', 'clang', '--version'], text=True).strip(),
            image=os.environ.get('ImageVersion'))
        result['toolSHA256'] = {}
        for name in ('clang', 'clang++', 'ld', 'libtool', 'ar', 'install_name_tool'):
            path = Path(subprocess.check_output(['/usr/bin/xcrun', '--find', name], text=True).strip()).resolve(strict=True)
            result['toolSHA256'][name] = digest(path)
        sdk_root = Path(subprocess.check_output(['/usr/bin/xcrun', '--show-sdk-path'], text=True).strip())
        result['SDKSettingsSHA256'] = {p.name: digest(p) for p in (sdk_root / 'SDKSettings.json', sdk_root / 'SDKSettings.plist') if p.is_file()}
        result['hostCompilerSHA256'] = {name: digest(Path(shutil.which(name)).resolve(strict=True))
            for name in ('cc', 'c++', 'gcc', 'g++', 'clang', 'clang++') if shutil.which(name)}
    elif target == 'linux-x64': result['image'] = lock['linuxSourceBuild']['compilerImage']
    else:
        # Compiler stage keys additionally bind actual resolved base/target
        # image digests. This is a transport namespace, never an image pin.
        result.update(recipe=lock['btbn']['commit'], image=os.environ.get('ImageVersion'))
    return result


def plan(target, directory, output):
    if os.environ.get('PORTICO_ENGINE_BUILD_CONTEXT') != 'development':
        raise ValueError('dependency cache transport requires an explicit development producer')
    if target not in TARGETS:
        raise ValueError('unsupported dependency cache target')
    if (os.environ.get('GITHUB_ACTIONS') != 'true' or
            os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted' or
            os.environ.get('GITHUB_REPOSITORY') != 'PorticoMediaServer/portico' or
            os.environ.get('GITHUB_EVENT_NAME') != 'workflow_dispatch'):
        raise ValueError('dedicated hosted dependency cache context required')
    directory.mkdir(parents=True, exist_ok=True)
    identity = compiler(target)
    scope = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()
    prefix = 'engine-dependencies-v1-' + target + '-' + scope + '-'
    value = dict(schemaVersion=1, target=target, compiler=identity, namespace=prefix,
                 qualified=False, release=False)
    (directory / 'transport-context.json').write_text(json.dumps(value, indent=2) + '\n')
    with output.open('a') as stream:
        stream.write('prefix=' + prefix + '\nkey=' + prefix + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'] + '\n')
        stream.write('compiler=' + json.dumps(identity, separators=(',', ':')) + '\n')


def linux_dovi(action, work):
    spec = importlib.util.spec_from_file_location('prefix_cache', Path(__file__).with_name('engine-prefix-cache.py'))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    inputs = dict(target='linux-x64', stage='libdovi', source=lock['libdovi'],
        nativeRust=lock.get('nativeRustTools'), compiler=lock['linuxSourceBuild']['compilerImage'],
        scripts={name: digest(ROOT / 'scripts' / name) for name in ('build-linux-libdovi.py', 'ffmpeg-rust-tools.py')
                 if (ROOT / 'scripts' / name).is_file()}, sourceDateEpoch=lock['sourceDateEpoch'])
    roots = dict(prefix=work / 'linux-deps/prefix', libdovi=work / 'libdovi',
        rust=work / 'native-rust-tools', sources=Path(os.environ['PORTICO_LANE_SCRATCH']) / 'libdovi/sources',
        prefixseal=work / 'linux-deps/prefix-files.json', inputs=work / 'linux-deps/dependency-inputs.json')
    cache = module.PrefixCache(Path(os.environ['PORTICO_ENGINE_STAGE_CACHE']) / 'linux-dovi', work / '.dovi-cache-state')
    result = cache.begin(inputs, roots) if action == 'begin' else cache.finish(inputs, roots)
    evidence = work / 'dependency-cache-receipts'; evidence.mkdir(exist_ok=True)
    if action == 'finish' or result['hit']:
        (evidence / 'linux-libdovi.json').write_text(json.dumps(dict(cacheHit=bool(result.get('hit')), **result.get('receipt', result)), sort_keys=True) + '\n')
    return ('HIT' if result['hit'] else 'MISS') if action == 'begin' else 'SAVED'


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=('plan', 'has-output', 'linux-dovi-begin', 'linux-dovi-finish'))
    p.add_argument('--target', choices=TARGETS)
    p.add_argument('--directory', type=Path, required=True)
    p.add_argument('--output', type=Path)
    a = p.parse_args()
    if a.action.startswith('linux-dovi-'):
        print(linux_dovi(a.action.removeprefix('linux-dovi-'), a.directory.resolve()))
    elif a.action == 'plan': plan(a.target, a.directory.resolve(), a.output)
    else:
        present = any(a.directory.glob('*/stages/*/index.json')) or any(a.directory.glob('sources/*/receipt.json'))
        with a.output.open('a') as out: out.write('present=' + str(present).lower() + '\n')
