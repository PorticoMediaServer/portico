#!/usr/bin/env python3
"""Plan hosted dependency cache transport without granting engine acceptance."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ('windows-x64', 'linux-x64', 'linux-arm64', 'macos-arm64')


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def compiler(target):
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    result = dict(target=target, architecture=platform.machine())
    if target.startswith('macos-'):
        result.update(xcode=subprocess.check_output(['xcodebuild', '-version'], text=True).strip(),
            sdk=subprocess.check_output(['/usr/bin/xcrun', '--show-sdk-version'], text=True).strip(),
            clang=subprocess.check_output(['/usr/bin/xcrun', 'clang', '--version'], text=True).strip())
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
        pin = lock['compilerImages']
        path = ROOT / pin['path']
        if digest(path) != pin['sha256']: raise ValueError('compiler image lock changed')
        images = json.loads(path.read_text())['images']
        result.update(recipe=lock['btbn']['commit'], preservedCompilerImage=images[{'windows-x64': 'win64', 'linux-arm64': 'linuxarm64'}[target]]['digest'])
    return result


def dependency_inputs(target, root=ROOT):
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    values = dict(sourceDateEpoch=lock['sourceDateEpoch'], libdovi=lock['libdovi'],
                  nativeRust=lock['nativeRustTools'])
    scripts = ['build-linux-libdovi.py', 'ffmpeg-rust-tools.py', 'requirements-ffmpeg-meson.txt']
    if target.startswith('macos-'):
        values['recipe'] = lock['macosRecipe']
        scripts += ['build-macos-ffmpeg-extras.sh', 'build-macos-libdovi.py', 'build-macos-zvbi.sh',
                    'configure-macos-shaderc.py', 'macos-dependency-cache.py']
    else:
        values['recipe'] = lock['btbn']
        values['linuxDependencies'] = lock['linuxSourceBuild']
        scripts += ['build-linux-ffmpeg-deps.py']
    values['scripts'] = {name: digest(root / 'scripts' / name) for name in scripts}
    return values


def checkpoint(directory):
    if os.environ.get('PORTICO_ENGINE_BUILD_CONTEXT') != 'development':
        raise ValueError('dependency cache transport requires an explicit development producer')
    if directory.is_symlink(): raise ValueError('linked dependency cache root')
    roots = [p for p in directory.glob('*/stages/*')
             if re.fullmatch('[a-f0-9]{64}', p.name) and (p / 'index.json').is_file()]
    roots += [p for p in directory.glob('sources/*') if (p / 'receipt.json').is_file()]
    files = {}
    for root in sorted(roots):
        if root.is_symlink(): raise ValueError('linked dependency checkpoint')
        for path in sorted(root.rglob('*')):
            if path.is_symlink(): raise ValueError('linked dependency checkpoint file')
            if path.is_file(): files[path.relative_to(directory).as_posix()] = digest(path)
    sha = hashlib.sha256(json.dumps(files, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    return dict(present=bool(roots), snapshot=sha if roots else 'empty')


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
    prefix = 'engine-dependencies-v2-' + target + '-' + scope + '-'
    pins = hashlib.sha256(json.dumps(dependency_inputs(target), sort_keys=True).encode()).hexdigest()
    key = prefix + pins
    value = dict(schemaVersion=2, target=target, compiler=identity, namespace=prefix, inputKey=key,
                 qualified=False, release=False)
    (directory / 'transport-context.json').write_text(json.dumps(value, indent=2) + '\n')
    with output.open('a') as stream:
        stream.write('prefix=' + prefix + '\nkey=' + key + '\n')
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
    p.add_argument('action', choices=('plan', 'has-output', 'checkpoint', 'linux-dovi-begin', 'linux-dovi-finish'))
    p.add_argument('--target', choices=TARGETS)
    p.add_argument('--directory', type=Path, required=True)
    p.add_argument('--output', type=Path)
    a = p.parse_args()
    if a.action.startswith('linux-dovi-'):
        print(linux_dovi(a.action.removeprefix('linux-dovi-'), a.directory.resolve()))
    elif a.action == 'plan': plan(a.target, a.directory.resolve(), a.output)
    else:
        value = checkpoint(a.directory)
        with a.output.open('a') as out:
            out.write('present=' + str(value['present']).lower() + '\nsnapshot=' + value['snapshot'] + '\n')
