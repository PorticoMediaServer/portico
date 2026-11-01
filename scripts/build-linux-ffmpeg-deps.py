#!/usr/bin/env python3
"""Build the pinned Linux dependency closure serially, without OCI layer copies.

The compiler image is an immutable build input, never an FFmpeg distribution.
All linked media dependencies are built from the pinned source recipes. Sources,
licences, exact input hashes and the installed prefix remain in the job directory.
"""
import argparse
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import time

CURRENT_STAGE = None


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def atomic_json(path, value):
    temporary = path.with_suffix(path.suffix + '.new')
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')
    temporary.replace(path)


def run(*argv, **kwargs):
    started = time.monotonic()
    try:
        return subprocess.run(argv, check=True, **kwargs)
    finally:
        if CURRENT_STAGE is not None and os.environ.get('PORTICO_STAGE_LOG'):
            with Path(os.environ['PORTICO_STAGE_LOG']).open('a') as log:
                log.write(json.dumps({'stage': CURRENT_STAGE, 'seconds': round(time.monotonic() - started, 3)}) + '\n')


def prepare(recipe, config, jobs):
    # The final stage names the complete required dependency closure. Native
    # FFmpeg codecs remain enabled; unrelated optional third-party encoders are
    # not dependencies of this bundle.
    final = recipe / 'scripts.d/zz-final.sh'
    text = final.read_text()
    text = re.sub(r'ffbuild_depends\(\) \{.*?\n\}',
                  'ffbuild_depends() {\n' + ''.join('    echo ' + x + '\n' for x in config['roots']) + '}',
                  text, count=1, flags=re.S)
    final.write_text(text)
    for path in recipe.rglob('*.sh'):
        if '.git' in path.parts:
            continue
        text = path.read_text()
        text = text.replace('$(nproc)', str(jobs)).replace('xz -T0', 'xz -T1')
        path.write_text(text)
    run('bash', 'generate.sh', 'linux64', 'gpl', '8.1', cwd=recipe)
    stages = []
    for source in re.findall(r'RUN --mount=src=(scripts\.d/[^,]+),dst=/stage.sh', (recipe / 'Dockerfile').read_text()):
        if source not in stages:
            stages.append(source)
    if not stages:
        raise ValueError('Pinned recipe generated no dependency stages')
    return stages


def container(recipe, work, config, stages, jobs):
    import shutil
    image = config['compilerImage']
    source_network = os.environ.get('PORTICO_BUILD_SOURCE_NETWORK', 'bridge')
    if source_network not in ('bridge', 'host'):
        raise ValueError('Unsupported public source-intake container network')
    if not re.fullmatch(r'ghcr\.io/porticomediaserver/ffmpeg-base-linux64@sha256:[0-9a-f]{64}', image):
        raise ValueError('An immutable Linux compiler image is required')
    for directory in ['sources', 'build', 'prefix', 'dest', 'tmp', 'licenses', 'receipts', 'extra']:
        (work / directory).mkdir(parents=True, exist_ok=True)
    run('docker', 'pull', image)
    runner = work / 'dependency-runner.py'
    shutil.copyfile(Path(__file__), runner)
    requirements = Path(__file__).with_name('requirements-ffmpeg-meson.txt')
    tool_receipt = Path(__file__).with_name('ffmpeg-build-tools.py')
    script = work / 'dependencies.sh'
    script.write_text('''#!/bin/bash
set -euo pipefail
portico_output_owner="$(stat -c '%u:%g' /portico)"
portico_restore_owner() {
    portico_stage_status=$?
    trap - EXIT
    chown -hRP "$portico_output_owner" /portico
    exit "$portico_stage_status"
}
trap portico_restore_owner EXIT
cd /recipe
source util/vars.sh linux64 gpl 8.1
export SOURCE_DATE_EPOCH="''' + str(config['sourceDateEpoch']) + '''"
export TMPDIR=/portico/tmp
export CARGO_BUILD_JOBS=''' + str(jobs) + ''' CMAKE_BUILD_PARALLEL_LEVEL=''' + str(jobs) + '''
export ACLOCAL_PATH=/opt/ffbuild/share/aclocal
export ZERO_AR_DATE=1 LC_ALL=C.UTF-8 TZ=UTC
export CFLAGS="$CFLAGS -ffile-prefix-map=/portico/build=/usr/src/portico"
export CXXFLAGS="$CXXFLAGS -ffile-prefix-map=/portico/build=/usr/src/portico"
python3 -m venv /portico/build-tools
/portico/build-tools/bin/python -m pip install --disable-pip-version-check --require-hashes --only-binary=:all: --no-deps -r /portico-meson-requirements.txt
python3 /portico-meson-proof.py receipt --python /portico/build-tools/bin/python --requirements /portico-meson-requirements.txt --output /portico/meson-tool.json
export PATH="/portico/build-tools/bin:$PATH"
export PORTICO_STAGE_LOG=/portico/stage-times.jsonl
gcc --version > /portico/compiler.txt
"$CC" --version >> /portico/compiler.txt
"$CXX" --version >> /portico/compiler.txt
ld --version >> /portico/compiler.txt
meson --version >> /portico/compiler.txt
python3 /build-linux-ffmpeg-deps.py --inside
''')
    atomic_json(work / 'stage-plan.json', {
        'stages': stages, 'compilerImage': image, 'jobs': jobs, 'sourceFetchNetwork': source_network,
        'recipeSHA256': {str(p.relative_to(recipe)): digest(p) for p in sorted(recipe.rglob('*.sh')) if '.git' not in p.parts},
    })
    # Writable paths are ordinary on-disk bind mounts. Docker's /tmp is never
    # used for source/build scratch. A single container holds small native build
    # tools (e.g. glslc) between dependency stages; exported before it exits.
    cache_arguments = []
    if os.environ.get('PORTICO_ENGINE_STAGE_CACHE'):
        cache_root = Path(os.environ['PORTICO_ENGINE_STAGE_CACHE']).resolve(strict=True)
        cache_arguments = ['-v', str(cache_root) + ':/portico-stage-cache',
            '-v', str(Path(__file__).with_name('engine-prefix-cache.py')) + ':/engine-prefix-cache.py:ro',
            '-e', 'PORTICO_ENGINE_BUILD_CONTEXT', '-e', 'PORTICO_ENGINE_STAGE_CACHE=/portico-stage-cache']
    run('docker', 'run', '--rm', '--name', 'portico-hardware-r29-linux-deps',
        '--network', source_network,
        '--cpus', str(jobs), '--memory', '4g', '--memory-swap', '4g',
        '--label', 'portico.lane=hardware-r29',
        '-v', str(recipe) + ':/recipe:ro', '-v', str(work) + ':/portico',
        '-v', str(work / 'prefix') + ':/opt/ffbuild',
        '-v', str(work / 'dest') + ':/opt/ffdest',
        '-v', str(runner) + ':/build-linux-ffmpeg-deps.py:ro',
        '-v', str(requirements) + ':/portico-meson-requirements.txt:ro',
        '-v', str(tool_receipt) + ':/portico-meson-proof.py:ro',
        '-e', 'TMPDIR=/portico/tmp', *cache_arguments, image, 'bash', '/portico/dependencies.sh')


def inside(work=Path('/portico'), recipe=Path('/recipe')):
    global CURRENT_STAGE
    import shutil
    import tarfile
    os.environ['ACLOCAL_PATH'] = '/opt/ffbuild/share/aclocal'
    plan = json.loads((work / 'stage-plan.json').read_text())
    cache = None
    cache_roots = dict(prefix=Path(os.environ.get('FFBUILD_PREFIX', '/opt/ffbuild')), sources=work / 'sources',
                       licenses=work / 'licenses', receipts=work / 'receipts', extra=work / 'extra')
    if os.environ.get('PORTICO_ENGINE_STAGE_CACHE'):
        spec = importlib.util.spec_from_file_location('prefix_cache', Path(__file__).with_name('engine-prefix-cache.py'))
        module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        cache = module.PrefixCache(Path(os.environ['PORTICO_ENGINE_STAGE_CACHE']) / 'linux-native', work / '.cache-state')
    inputs = []
    for source in plan['stages']:
        name = Path(source).stem
        CURRENT_STAGE = name
        raw = subprocess.check_output(['bash', '-c',
            'source util/vars.sh linux64 gpl 8.1; source util/dl_functions.sh; '
            'source "$1"; ffbuild_dockerdl', 'source-contract', source], cwd=recipe, text=True).rstrip('\n')
        if not raw:
            continue
        contract = hashlib.sha256((raw + '\n').encode()).hexdigest()
        archive = work / 'sources' / (name + '_' + contract + '.tar.xz')
        receipt = work / 'receipts' / (name + '.json')
        stage = work / 'build' / name
        expected = {'script': source, 'scriptSHA256': digest(recipe / source),
                    'downloadRecipeSHA256': contract, 'compilerImage': plan['compilerImage'],
                    'sourceFetchNetwork': plan['sourceFetchNetwork'],
                    'runnerSHA256': digest(Path(__file__))}
        cache_inputs = dict(**expected, sourceDateEpoch=os.environ['SOURCE_DATE_EPOCH'],
            recipeHelpers={str(p.relative_to(recipe)): digest(p) for directory in ('util', 'images/base')
                           for p in sorted((recipe / directory).rglob('*')) if p.is_file()},
            flags={key: os.environ.get(key, '') for key in ('CFLAGS', 'CXXFLAGS', 'LDFLAGS',
                'CARGO_BUILD_JOBS', 'CMAKE_BUILD_PARALLEL_LEVEL', 'ZERO_AR_DATE')})
        restored = cache.begin(cache_inputs, cache_roots) if cache and not receipt.exists() else None
        if restored and restored['hit']:
            cache_evidence = work / 'dependency-cache-receipts'; cache_evidence.mkdir(exist_ok=True)
            atomic_json(cache_evidence / (name + '.json'), dict(cacheHit=True, **restored['receipt']))
            if (work / 'extra/glslc').is_file():
                for destination in ('/opt/glslc', '/usr/bin/glslc'):
                    shutil.copyfile(work / 'extra/glslc', destination); Path(destination).chmod(0o755)
        if receipt.exists():
            saved = json.loads(receipt.read_text())
            if any(saved.get(k) != v for k, v in expected.items()) or not archive.is_file() or digest(archive) != saved['sourceArchiveSHA256']:
                raise ValueError('Resume input changed for ' + name)
            inputs.append(saved)
            print('REUSE ' + name, flush=True)
            continue
        if stage.exists():
            shutil.rmtree(stage)
        stage.mkdir()
        print('SOURCE ' + name, flush=True)
        if not archive.exists():
            # Download commands originate only in the exact public source
            # recipe. Any submodule/dependency commits are retained in source.
            download_root = work / 'tmp' / (name + '.download-root')
            run('bash', '-e', '-o', 'pipefail', '-c', raw + '\npwd -P > ' + shlex.quote(str(download_root)), cwd=stage)
            archive_root = Path(download_root.read_text().strip()).resolve(strict=True)
            if not archive_root.is_relative_to(stage):
                raise ValueError('Download recipe escaped its source directory: ' + name)
            # Upstream archives the final shell directory. libiconv, for
            # example, downloads into iconv/ and changes there to add gnulib.
            run('tar', '--sort=name', '--mtime=@' + os.environ['SOURCE_DATE_EPOCH'],
                '--owner=0', '--group=0', '--numeric-owner', '-I', 'xz -T1',
                '-cf', str(archive) + '.new', '.', cwd=archive_root)
            Path(str(archive) + '.new').replace(archive)
            # Build from the archived layout too; the first build and a resumed
            # build must see precisely the same root and original source bytes.
            shutil.rmtree(stage)
            stage.mkdir()
            run('tar', '-xf', str(archive), '-C', str(stage))
        else:
            run('tar', '-xf', str(archive), '-C', str(stage))
        # Collect actual licence files before build recipes edit the source.
        licenses = {}
        for path in sorted(stage.rglob('*')):
            if not path.is_file() or path.is_symlink() or '.git' in path.parts:
                continue
            if (path.name.lower().startswith(('license', 'licence', 'copying', 'copyright', 'notice')) or
                    any(part.lower() in ('license', 'licenses', 'licence', 'licences') for part in path.relative_to(stage).parts[:-1])):
                relative = path.relative_to(stage)
                destination = work / 'licenses' / name / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(path, destination)
                licenses[str(relative)] = digest(path)
        if name == '50-ffnvcodec':
            # NVIDIA's pinned headers carry separate MIT/BSD notices in their
            # opening comments, without a root LICENSE file. Preserve every
            # actual header notice, including its source path and byte hash.
            for header in sorted(stage.glob('ffnvcodec*/include/ffnvcodec/*.h')):
                raw = header.read_bytes()
                match = re.match(rb'/\*.*?\*/', raw, flags=re.S)
                if not match or b'copyright' not in match[0].lower():
                    raise ValueError('Missing NVIDIA header copyright: ' + str(header))
                notice = match[0] + b'\n'
                if not any(word in notice.lower() for word in (b'permission', b'redistribution', b'license')):
                    raise ValueError('Missing NVIDIA header licence: ' + str(header))
                relative = header.relative_to(stage)
                destination = work / 'licenses' / name / relative.parent / ('LICENSE-' + header.name + '.txt')
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(notice)
                licenses[str(relative) + '#opening-comment'] = hashlib.sha256(notice).hexdigest()
        if not licenses:
            raise ValueError('No actual dependency licence found for ' + name)
        print('BUILD ' + name, flush=True)
        run('bash', '-e', '-o', 'pipefail', '-c', '''
stage_source="$PWD"
cd "$2"
source util/vars.sh linux64 gpl 8.1
source "$2/$1"
cd "$stage_source"
case "$1" in
  *libbluray.sh|*libdvdread.sh|*libdvdnav.sh|*libudfread.sh)
    export GIT_DIR="$stage_source/.git" GIT_WORK_TREE="$stage_source";;
esac
export RAW_CFLAGS="$CFLAGS" RAW_CXXFLAGS="$CXXFLAGS" RAW_LDFLAGS="$LDFLAGS"
export CFLAGS="$CFLAGS ${STAGE_CFLAGS:-}" CXXFLAGS="$CXXFLAGS ${STAGE_CXXFLAGS:-}"
export LDFLAGS="$LDFLAGS ${STAGE_LDFLAGS:-}"
ffbuild_dockerbuild
''', 'dependency-build', source, str(recipe), cwd=stage)
        # Each stage installs into the same static prefix. No OCI stage/cache
        # duplication, and its compiler intermediates are removed immediately.
        destination = Path(os.environ['FFBUILD_DESTPREFIX'])
        if destination.exists():
            run('cp', '-a', str(destination) + '/.', os.environ['FFBUILD_PREFIX'])
            shutil.rmtree(destination)
        if Path('/opt/glslc').is_file():
            shutil.copyfile('/opt/glslc', '/usr/bin/glslc')
            Path('/usr/bin/glslc').chmod(0o755)
        record = {**expected, 'sourceArchive': archive.name, 'sourceArchiveSHA256': digest(archive),
                  'licenses': licenses}
        atomic_json(receipt, record)
        inputs.append(record)
        atomic_json(work / 'dependency-inputs.json', inputs)
        shutil.rmtree(stage)
        if cache:
            if Path('/opt/glslc').is_file():
                shutil.copyfile('/opt/glslc', work / 'extra/glslc'); (work / 'extra/glslc').chmod(0o755)
            cached = cache.finish(cache_inputs, cache_roots)
            cache_evidence = work / 'dependency-cache-receipts'; cache_evidence.mkdir(exist_ok=True)
            atomic_json(cache_evidence / (name + '.json'), dict(cacheHit=False, **cached))
        print('DONE ' + name, flush=True)
    CURRENT_STAGE = None
    glslc = Path('/opt/glslc')
    if glslc.is_file():
        shutil.copyfile(glslc, work / 'extra/glslc')
        (work / 'extra/glslc').chmod(0o755)
    atomic_json(work / 'prefix-files.json', {
        str(path.relative_to(work / 'prefix')): digest(path)
        for path in sorted((work / 'prefix').rglob('*')) if path.is_file()
    })
    atomic_json(work / 'dependency-inputs.json', inputs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--recipe', type=Path)
    parser.add_argument('--work', type=Path)
    args = parser.parse_args()
    if args.inside:
        inside()
        return
    root = Path(__file__).resolve().parent.parent
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    import runpy
    compiler_pins = runpy.run_path(str(root / 'scripts/preserve-ffmpeg-compilers.py'))['lock']()
    if lock['linuxSourceBuild']['compilerImage'] != compiler_pins['images']['linux64']['mirror']:
        raise ValueError('Linux compiler image differs from its reviewed preservation pin')
    config = {**lock['linuxSourceBuild'], 'sourceDateEpoch': lock['sourceDateEpoch']}
    jobs = int(os.environ.get('PORTICO_BUILD_JOBS', '2'))
    if jobs not in (1, 2):
        raise ValueError('Use one or two compiler workers for this bounded recipe')
    stages = prepare(args.recipe.resolve(), config, jobs)
    container(args.recipe.resolve(), args.work.resolve(), config, stages, jobs)


if __name__ == '__main__':
    main()
