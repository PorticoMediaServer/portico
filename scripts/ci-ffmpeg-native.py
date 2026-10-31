#!/usr/bin/env python3
"""CI-only source/native adapters. No release seal or installer publication."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import struct
import subprocess
import sys
import tarfile

ROOT = Path(__file__).resolve().parent.parent
TARGETS = ('windows-x64', 'windows-arm64', 'macos-x64', 'macos-arm64')
MAX_BYTES = 30 * 1024**3
MAX_TRANSFER_BYTES = 3 * 1024**3
MAX_FILES = 200000
README = 'Unsigned qualification intermediate; NOT-FOR-USE; not a Portico release; expires in 1 day.\n'


def run(*args, env=None):
    subprocess.run([str(x) for x in args], check=True, cwd=ROOT, env=env)


def digest(p):
    with p.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


def safe_name(name):
    p = PurePosixPath(name)
    if not name or p.is_absolute() or any(x in ('', '.', '..') for x in name.split('/')) or '\\' in name or ':' in name:
        raise ValueError('unsafe transfer path: ' + name)
    return p


def inventory(root):
    if root.is_symlink():
        raise ValueError('outer symlink')
    result, total = {}, 0
    for p in sorted(root.rglob('*')):
        if p.is_symlink() or not (p.is_file() or p.is_dir()):
            raise ValueError('outer symlink/special file: ' + str(p))
        if p.is_file() and p.relative_to(root).as_posix() != 'transfer-manifest.json':
            name = p.relative_to(root).as_posix()
            safe_name(name)
            total += p.stat().st_size
            if total > MAX_TRANSFER_BYTES or len(result) >= MAX_FILES:
                raise ValueError('transfer exceeds bounds')
            result[name] = {'size': p.stat().st_size, 'sha256': digest(p)}
    return result


def corresponding_source(path):
    """Inspect without extracting: bound expansion and reject escaping links."""
    names, total = set(), 0
    source_stages = set()
    with tarfile.open(path) as archive:
        for member in archive:
            name = member.name.rstrip('/')
            safe_name(name)
            if name in names:
                raise ValueError('duplicate source member')
            names.add(name)
            if member.isfile() and name.startswith('recipe/scripts.d/') and name.endswith('.sh'):
                stream = archive.extractfile(member)
                script = stream.read().decode('utf-8') if stream else ''
                if re.search(r'^SCRIPT_REPO=', script, re.MULTILINE):
                    source_stages.add(PurePosixPath(name).stem)
            total += member.size
            if len(names) > MAX_FILES or total > MAX_BYTES:
                raise ValueError('source archive exceeds bounds')
            if member.issym() or member.islnk():
                link = PurePosixPath(member.linkname)
                if link.is_absolute() or '..' in link.parts or '\\' in member.linkname or ':' in member.linkname:
                    raise ValueError('unsafe source link')
            elif not (member.isfile() or member.isdir()):
                raise ValueError('special source member')
    required = {'recipe/build.sh', 'recipe/makeimage.sh', 'provenance/contract/sources.lock.json',
                'provenance/contract/build-source-authority.json', 'provenance/scripts/build-ffmpeg.sh', 'provenance/scripts/ci-ffmpeg-native.py',
                'provenance/portico-disc/LICENSE', 'provenance/portico-disc/portico-disc.c'}
    if not required <= names or not any('/patches/' in n for n in names):
        raise ValueError('incomplete corresponding source/scripts/locks/licenses')
    # The pinned recipe's download.sh materializes EVERY dependency stage in
    # .cache/downloads before any compiler layer runs. Keep those actual source
    # snapshots, not just the recipe URLs or the FFmpeg top-level tarball.
    if not source_stages:
        raise ValueError('dependency source stage inventory missing')
    for stage in source_stages:
        if not any(n.startswith('recipe/.cache/downloads/' + stage + '_') and n.endswith('.tar.xz') for n in names):
            raise ValueError('dependency source snapshot missing: ' + stage)


def redistributable(config):
    tokens = config.split()
    if any(x.startswith(('--enable-nonfree', '--enable-libfdk')) for x in tokens):
        raise ValueError('nonredistributable enabled configuration')
    if not {'--enable-gpl', '--enable-version3'} <= set(tokens):
        raise ValueError('GPL/version3 required')


def binary_configuration(path):
    # FFmpeg embeds the exact configure string in its executable. A cross
    # producer cannot execute that PE, so inspect the recorded configuration;
    # the native consumer separately checks actual -buildconf output.
    configs = [x.decode('ascii') for x in re.findall(rb'[\x20-\x7e]{40,}', path.read_bytes())
               if b'--enable-gpl' in x and b'--enable-version3' in x]
    if not configs:
        raise ValueError('compiled configure receipt missing')
    for config in configs:
        redistributable(config)
    return configs


def architecture(path, target):
    with path.open('rb') as f:
        if target.startswith('windows'):
            if f.read(2) != b'MZ':
                raise ValueError('missing PE header')
            f.seek(60)
            offset = struct.unpack('<I', f.read(4))[0]
            if offset > path.stat().st_size - 6:
                raise ValueError('invalid PE offset')
            f.seek(offset)
            if f.read(4) != b'PE\0\0' or struct.unpack('<H', f.read(2))[0] != (0x8664 if target.endswith('x64') else 0xaa64):
                raise ValueError('wrong PE architecture')
        else:
            if f.read(4) != b'\xcf\xfa\xed\xfe' or struct.unpack('<I', f.read(4))[0] != (0x1000007 if target.endswith('x64') else 0x100000c):
                raise ValueError('wrong native Mach-O architecture')


def tools(raw, target):
    for tool in ('ffmpeg', 'ffprobe', 'portico-disc'):
        matches = list(raw.rglob(tool + ('.exe' if target.startswith('windows') else '')))
        if len(matches) != 1:
            raise ValueError('expected exactly one ' + tool)
        architecture(matches[0], target)


def native(target):
    expected_os = 'Windows' if target.startswith('windows') else 'Darwin'
    expected_arch = ('amd64', 'x86_64') if target.endswith('x64') else ('arm64', 'aarch64')
    if platform.system() != expected_os or platform.machine().lower() not in expected_arch:
        raise ValueError('matching native OS/architecture required; no Wine/emulation qualification')
    # On ARM Windows also reject an x64 Python process under emulation.
    if target == 'windows-arm64' and os.environ.get('PROCESSOR_ARCHITECTURE', '').lower() != 'arm64':
        raise ValueError('native ARM64 Python required')


def fresh(p):
    if p.exists() or p.is_symlink():
        raise ValueError('output must not exist: ' + str(p))
    p.mkdir(parents=True)


def head():
    return subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()


def context():
    sha = head()
    expected = os.environ.get('GITHUB_SHA')
    event = os.environ.get('GITHUB_EVENT_NAME')
    if event == 'pull_request':
        data = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
        request = data['pull_request']
        if request['head']['repo']['full_name'] != 'PorticoMediaServer/portico':
            raise ValueError('same-repository source required')
        expected = request['head']['sha']
    elif event != 'workflow_dispatch':
        raise ValueError('qualification dispatch or same-repository PR required')
    if os.environ.get('GITHUB_ACTIONS') != 'true' or not os.environ.get('GITHUB_RUN_ID') or expected != sha:
        raise ValueError('exact-SHA GitHub Actions checkout required')
    return {'sourceSHA': sha, 'runId': os.environ['GITHUB_RUN_ID'], 'runAttempt': os.environ.get('GITHUB_RUN_ATTEMPT', '1')}


def build_env():
    env = dict(os.environ, PORTICO_BUILD_JOBS='2', MAKEFLAGS='-j2', CMAKE_BUILD_PARALLEL_LEVEL='2', CARGO_BUILD_JOBS='2', RAYON_NUM_THREADS='2', NUMJOBS='2')
    env.pop('PORTICO_LANE_SCRATCH', None)
    env.pop('PORTICO_FFMPEG_PHASE', None)
    for name in ('PORTICO_FFMPEG_SOURCE_ONLY', 'PORTICO_FFMPEG_DEPS_ONLY'):
        if env.get(name, '0') != '0':
            raise ValueError('partial source/dependency builds forbidden')
    if platform.system() == 'Darwin':
        # NUMJOBS bypasses the pinned recipe's automatic job-count branch,
        # which also initializes this Apple tool. Never select GNU libtool
        # from the dependency prefix when merging the x265 static archives.
        tool = Path(subprocess.check_output(['/usr/bin/xcrun', '--find', 'libtool'], text=True).strip())
        if not tool.is_absolute() or not tool.is_file() or not os.access(tool, os.X_OK):
            raise ValueError('xcrun did not resolve an absolute executable Apple libtool')
        env['MACOS_LIBTOOL'] = str(tool)
    return env


def libva_diagnostics(text):
    marker = '    meson "${myconf[@]}" ..\n'
    if text.count(marker) != 1:
        raise ValueError('pinned libva Meson diagnostic boundary changed')
    # The file lives inside a disposable compiler container, with no CI token
    # in its environment. Preserve Meson's original failure and all checks.
    return text.replace(marker, '''    if meson "${myconf[@]}" ..; then
        :
    else
        local meson_status=$?
        printf '%s\\n' 'BEGIN libva meson-log.txt (at most 1048576 bytes)'
        if [[ -f meson-logs/meson-log.txt && ! -L meson-logs/meson-log.txt ]]; then
            head -c 1048576 -- meson-logs/meson-log.txt || :
        else
            printf '%s\\n' 'Meson log unavailable or not a regular non-symlink file'
        fi
        printf '\\n%s\\n' 'END libva meson-log.txt'
        return "$meson_status"
    fi
''')


def prepare_recipe(recipe):
    if platform.system() != 'Linux' or os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted':
        raise ValueError('producer requires fresh isolated GitHub-hosted Linux')
    # Task-local generated recipe only. Hard limits include dependency pools,
    # not just FFmpeg's final make invocation. No personal Docker state touched.
    for p in recipe.rglob('*'):
        if '.git' in p.parts or not p.is_file() or p.suffix not in ('.sh', ''):
            continue
        try:
            text = p.read_text()
        except UnicodeError:
            continue
        text = text.replace('$(nproc)', '2').replace('$(getconf _NPROCESSORS_ONLN)', '2')
        text = re.sub(r'(?<![\w/"-])ninja(?=\s|$)', 'ninja -j2', text) if p.suffix == '.sh' else text
        text = re.sub(r'-j\s*[0-9]+', '-j2', text)
        if p.name == 'Dockerfile':
            # Plain make includes libiconv's order-sensitive autogen cleanup
            # and macro generation. Keep that serial; explicit compile -j2
            # above still overrides this default within the same job ceiling.
            # Host TMPDIR is not inherited by Docker build stages. Shaderc's
            # checked staging recipe requires a writable container-local path.
            # download.sh uses the runner UID, not root: use normal sticky
            # temporary-directory permissions inside the disposable image.
            text = re.sub(r'(?m)^(FROM .+)$', r'\1\nENV MAKEFLAGS=-j1 CARGO_BUILD_JOBS=2 CMAKE_BUILD_PARALLEL_LEVEL=2 RAYON_NUM_THREADS=2 TMPDIR=/var/tmp/portico-ci\nRUN mkdir -p "$TMPDIR" && chmod 1777 "$TMPDIR"', text)
        if p.name == 'download.sh':
            text = text.replace('docker run ', 'docker run --cpus 2 --memory 4g --memory-swap 4g ')
        p.write_text(text)
    p = recipe / 'makeimage.sh'
    text = p.read_text()
    if 'max-parallelism = 4' not in text or 'ffbuilder' not in text or '--driver-opt network=host' not in text:
        raise ValueError('pinned builder resource hooks changed')
    text = text.replace('max-parallelism = 4', 'max-parallelism = 1')
    text = text.replace('ffbuilder', 'portico-ci-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ.get('GITHUB_RUN_ATTEMPT', '1'))
    text = text.replace('--driver-opt network=host', '--driver-opt memory=4g --driver-opt cpu-quota=200000 --driver-opt cpu-period=100000')
    p.write_text(text)
    p = recipe / 'build.sh'
    text = p.read_text()
    if 'docker run --rm' not in text:
        raise ValueError('pinned runtime resource hook changed')
    p.write_text(text.replace('docker run --rm', 'docker run --cpus 2 --memory 4g --memory-swap 4g --rm'))
    p = recipe / 'scripts.d/50-vaapi/50-libva.sh'
    p.write_text(libva_diagnostics(p.read_text()))


def verify_native(bundle, target, evidence):
    tools(bundle / 'bin', target)
    # Invoke unchanged Python gates using this interpreter. Git Bash's python3
    # alias is not assumed to be the native Windows Python installation.
    run(sys.executable, ROOT / 'scripts/verify-ffmpeg-manifest.py', bundle)
    for script, args in (
        ('verify-ffmpeg-features.py', []),
        ('verify-disc-bundle.py', ['--fixtures', os.environ['PORTICO_DISC_FIXTURES'], '--fixture-sha256', os.environ['PORTICO_DISC_FIXTURES_SHA256']]),
        ('verify-ffmpeg-recipes.py', ['--scratch', os.environ['TMPDIR'], '--font', ROOT / 'packages/reader-core/assets/Literata[opsz,wght].ttf'])):
        run(sys.executable, ROOT / 'scripts' / script, bundle, *args, '--report', evidence / (script + '.json'))
    ffmpeg = bundle / 'bin' / ('ffmpeg.exe' if target.startswith('windows') else 'ffmpeg')
    redistributable(subprocess.check_output([str(ffmpeg), '-buildconf'], stderr=subprocess.STDOUT, text=True))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest='command', required=True)
    internal = sub.add_parser('prepare-recipe')
    internal.add_argument('recipe', type=Path)
    for command, options in [('produce', ('output',)), ('consume', ('input', 'bundle-out')), ('macos', ('work', 'bundle-out'))]:
        parser = sub.add_parser(command)
        parser.add_argument('target', choices=TARGETS)
        for option in (*options, 'evidence'):
            parser.add_argument('--' + option, required=True, type=Path)
    a = p.parse_args()
    if a.command == 'prepare-recipe':
        prepare_recipe(a.recipe)
        return
    if a.command == 'consume':
        for path in (a.input, *a.input.parents):
            if path.is_symlink():
                raise ValueError('symlink in transfer root path')
    custody = context()
    a.evidence.mkdir(parents=True, exist_ok=True)
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    os.environ.update(PORTICO_DISC_SOURCE_COMMIT=lock['discHelper']['sourceCommit'],
        PORTICO_DISC_FIXTURES=str(ROOT / lock['discHelper']['fixtureManifest']),
        PORTICO_DISC_FIXTURES_SHA256=lock['discHelper']['fixtureManifestSHA256'])
    if shutil.disk_usage(ROOT).free < 20 * 1024**3:
        raise ValueError('less than 20 GiB free; refusing build/transfer')
    if a.command == 'produce':
        if not a.target.startswith('windows'):
            p.error('produce requires Windows target')
        if platform.system() != 'Linux' or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted':
            raise ValueError('fresh isolated hosted Linux producer required')
        fresh(a.output)
        work = ROOT / 'runtime' / ('ci-native-' + a.target)
        if work.exists():
            raise ValueError('fresh compiler work required')
        try:
            env = build_env()
            env['PORTICO_FFMPEG_PHASE'] = 'windows-producer'
            run('bash', ROOT / 'scripts/build-ffmpeg.sh', a.target, work, env=env)
            receipt = json.loads((work / 'producer.json').read_text())
            if receipt['qualified'] is not False or receipt['input'] != 'extracted':
                raise ValueError('invalid producer receipt')
            raw = work / 'extracted'
            inventory(raw)
            tools(raw, a.target)
            corresponding_source(work / 'corresponding-source.tar.xz')
            binary_configuration(next(raw.rglob('ffmpeg.exe')))
            shutil.copytree(raw, a.output / 'input')
            shutil.copy2(work / 'corresponding-source.tar.xz', a.output)
            (a.output / 'README.txt').write_text(README)
            manifest = dict(custody, schemaVersion=1, target=a.target, qualified=False, files=inventory(a.output))
            (a.output / 'transfer-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        finally:
            # Only this adapter's fresh scratch; failure to clean is fatal.
            if work.exists():
                builder = 'portico-ci-' + custody['runId'] + '-' + custody['runAttempt']
                # Fresh hosted job only; never prune or touch personal builders.
                check = subprocess.run(['docker', 'buildx', 'inspect', builder], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                if check.returncode == 0:
                    run('docker', 'buildx', 'rm', '-f', builder)
                shutil.rmtree(work)
    else:
        native(a.target)
        final_bundle = a.bundle_out.resolve()
        if final_bundle.exists():
            raise ValueError('bundle output exists')
        a.bundle_out = final_bundle.with_name(final_bundle.name + '.native-checking')
        if a.bundle_out.exists():
            raise ValueError('native staging output exists')
        if a.command == 'consume':
            manifest = json.loads((a.input / 'transfer-manifest.json').read_text())
            if any(manifest.get(k) != v for k, v in dict(custody, schemaVersion=1, target=a.target, qualified=False).items()):
                raise ValueError('transfer custody mismatch')
            if inventory(a.input) != manifest['files'] or (a.input / 'README.txt').read_text() != README:
                raise ValueError('transfer inventory mismatch')
            corresponding_source(a.input / 'corresponding-source.tar.xz')
            if a.bundle_out.exists():
                raise ValueError('bundle output exists')
            try:
                run(sys.executable, ROOT / 'scripts/normalize-ffmpeg-bundle.py', a.input / 'input', a.target, a.bundle_out, a.input / 'corresponding-source.tar.xz')
            except BaseException:
                if a.bundle_out.exists():
                    shutil.rmtree(a.bundle_out)
                raise
        else:
            if not a.target.startswith('macos'):
                p.error('macos requires macOS target')
            run('bash', ROOT / 'scripts/build-ffmpeg.sh', a.target, a.work.resolve(), env=build_env())
            if a.bundle_out.exists():
                raise ValueError('bundle output exists')
            shutil.copytree(a.work / 'bundle', a.bundle_out)
        try:
            verify_native(a.bundle_out.resolve(), a.target, a.evidence.resolve())
        except BaseException:
            if a.bundle_out.exists():
                shutil.rmtree(a.bundle_out)
            raise
        a.bundle_out.rename(final_bundle)
        (a.evidence / 'native-engine.json').write_text(json.dumps(dict(custody, target=a.target, nativeEngineChecks=True,
            toolchainManifestSHA256=digest(final_bundle / 'toolchain-manifest.json'),
            fullCompilerAcceptance=False, limits=['Linux ELF ABI verifier does not support this target', 'NSIS/app construction is a separate gate', 'No OS signing or Portico release integrity seal exercised'])) + '\n')


if __name__ == '__main__':
    main()
