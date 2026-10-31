#!/usr/bin/env python3
"""Qualification construction only. Never produces Portico release custody."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import runpy
import shutil
import struct
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
TARGETS = {'macos-arm64': ('darwin', 'arm64'),
           'windows-x64': ('windows', 'amd64'), 'windows-arm64': ('windows', 'arm64')}


CONTEXT = runpy.run_path(str(ROOT / 'scripts/qualification-context.py'))
ENGINE = runpy.run_path(str(ROOT / 'scripts/fetch-engine.py'))
guard = CONTEXT['guard']


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def inventory(path):
    rows = {}
    for p in sorted(path.rglob('*')):
        if p.is_symlink():
            raise ValueError('symlinks forbidden in payload: ' + str(p))
        if p.is_file():
            if p.name in ('manifest.sig', 'manifest.json') and p.parent == path:
                raise ValueError('release custody forbidden')
            rows[p.relative_to(path).as_posix()] = digest(p)
    return rows


def architecture(path, target):
    with path.open('rb') as stream:
        data = stream.read(4096)
    osname, arch = TARGETS[target]
    if osname == 'windows':
        if data[:2] != b'MZ' or len(data) < 64:
            raise ValueError('not PE')
        offset = struct.unpack_from('<I', data, 60)[0]
        if data[offset:offset+4] != b'PE\0\0':
            raise ValueError('not PE signature')
        actual = struct.unpack_from('<H', data, offset+4)[0]
        expected = 0x8664 if arch == 'amd64' else 0xaa64
    else:
        if data[:4] != b'\xcf\xfa\xed\xfe':
            raise ValueError('not thin Mach-O')
        actual = struct.unpack_from('<I', data, 4)[0]
        expected = 0x1000007 if arch == 'amd64' else 0x100000c
    if actual != expected:
        raise ValueError('wrong executable architecture')


def run(command, cwd=ROOT):
    subprocess.run([str(x) for x in command], cwd=cwd, check=True, timeout=3600)


def construct(target, engine_input, pin, work, evidence, context):
    custody = CONTEXT['receipt'](os.environ, context)
    osname, arch = TARGETS[target]
    native = platform.machine().lower()
    if platform.system() != ('Darwin' if osname == 'darwin' else 'Windows') or native not in (
            ('x86_64', 'amd64') if arch == 'amd64' else ('arm64', 'aarch64')):
        raise ValueError('matching native host required')
    work.mkdir(parents=True, exist_ok=False)
    evidence.mkdir(parents=True, exist_ok=True)
    if shutil.disk_usage(work).free < 8 * 1024**3:
        raise ValueError('less than 8GiB free before payload construction')
    engine = ENGINE['verify_bundle'](engine_input, pin)
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    native_checks = {}
    for script, args in (
        ('verify-ffmpeg-features.py', []),
        ('verify-disc-bundle.py', ['--fixtures', ROOT / lock['discHelper']['fixtureManifest'],
                                  '--fixture-sha256', lock['discHelper']['fixtureManifestSHA256']]),
        ('verify-ffmpeg-recipes.py', ['--scratch', work, '--font', ROOT / 'packages/reader-core/assets/Literata[opsz,wght].ttf'])):
        report = evidence / (script + '.json')
        run([sys.executable, ROOT / 'scripts' / script, engine, *args, '--report', report])
        native_checks[script] = digest(report)
    ffmpeg = engine / 'bin' / ('ffmpeg.exe' if osname == 'windows' else 'ffmpeg')
    configuration = subprocess.check_output([str(ffmpeg), '-buildconf'], stderr=subprocess.STDOUT, text=True)
    flags = configuration.split()
    if (not {'--enable-gpl', '--enable-version3'} <= set(flags) or
            any(flag.startswith(('--enable-nonfree', '--enable-libfdk')) for flag in flags)):
        raise ValueError('engine must be GPLv3 with no nonfree or fdk')
    payload = work / 'payload'
    payload.mkdir()
    binary = payload / ('portico-server.exe' if osname == 'windows' else 'portico-server')
    tags = runpy.run_path(str(ROOT / 'scripts/release-build-tags.py'))['value'](False)
    os.environ.update({'CGO_ENABLED': '0', 'GOTOOLCHAIN': 'local', 'GOMAXPROCS': '2', 'GOFLAGS': '-p=2'})
    run(['go', 'build', '-mod=readonly', '-p=2', '-trimpath', '-tags=' + tags, '-o', binary, './cmd/server'], ROOT / 'server')
    architecture(binary, target)
    run(['npm.cmd' if osname == 'windows' else 'npm', 'ci', '--no-audit', '--no-fund'])
    run(['npm.cmd' if osname == 'windows' else 'npm', 'run', 'build', '--workspace', 'portico-web'])
    shutil.copytree(ROOT / 'web/dist', payload / 'web')
    if not (payload / 'web/index.html').is_file():
        raise ValueError('web build missing index')
    shutil.copytree(engine, payload / 'third_party/ffmpeg')
    for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
        architecture(payload / 'third_party/ffmpeg/bin' / (name + ('.exe' if osname == 'windows' else '')), target)
    rclone = runpy.run_path(str(ROOT / 'scripts/rclone-bundle.py'))
    rclone['prepare'](payload / 'third_party/rclone', osname + '-' + arch, work / 'rclone-cache')
    rclone['verify'](payload / 'third_party/rclone', osname + '-' + arch, True)
    for name in ('LICENSE', 'THIRD-PARTY-NOTICES.md'):
        shutil.copyfile(ROOT / name, payload / name)
    licences = payload / 'licenses'
    licences.mkdir()
    for source in ('server/internal/imagework/SCALE_LICENSE', 'server/internal/thirdparty/sqlite/LICENSE',
                   'server/internal/mediaexec/fonts/LICENSE', 'third_party/hls.js/LICENSE',
                   'packages/reader-core/assets/OFL.txt', 'web/src/kit/fonts/OFL.txt', 'packages/design/LICENSE-LUCIDE'):
        dest = licences / source.replace('/', '-')
        shutil.copyfile(ROOT / source, dest)
    shutil.copytree(ROOT / 'third_party/reader', licences / 'reader', ignore=shutil.ignore_patterns('*.js', '*.tgz'))
    build_number = os.environ['GITHUB_RUN_NUMBER'] if context == 'github-actions' else '0'
    (payload / 'web/portico-build.json').write_text(json.dumps({'version': '0.0.0', 'build': build_number}))
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    receipt = {'schema': 1, 'kind': 'unsigned-construction-qualification', 'release': False,
               'qualifiedRelease': False, **custody, 'sourceSha': sha, 'target': target, 'capabilityTags': tags,
               'enginePin': pin, 'nativeEngineReports': native_checks,
               'fullCompilerAcceptance': False,
               'notExercised': ['Portico integrity seal', 'sealed startup', 'OS signing', 'installation'],
               'files': inventory(payload),
               'runnerImage': {k: os.environ.get(k, '') for k in ('ImageOS', 'ImageVersion', 'RUNNER_ARCH')},
               'inputLocks': {p: digest(ROOT / p) for p in ('third_party/ffmpeg/sources.lock.json', 'third_party/rclone/bundle.lock.json', 'package-lock.json', 'server/go.mod', '.node-version')}}
    (payload / 'qualification-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
    output = work / 'packages'
    output.mkdir()
    if osname == 'darwin':
        run(['pkgbuild', '--version'])
        run(['bash', ROOT / 'scripts/package-macos.sh', '0.0.0', payload, output, build_number])
        run(['hdiutil', 'verify', output / ('Portico-Media-Server-macOS-' + target.split('-')[1] + '.dmg')])
        app = output / 'pkg-root/Applications/Portico Media Server.app/Contents'
        for name in ('Info.plist', 'Resources/PorticoServer.icns', 'MacOS/Portico Media Server'):
            if not (app / name).stat().st_size:
                raise ValueError('missing app component: ' + name)
        run(['plutil', '-lint', app / 'Info.plist'])
        pkg = output / ('Portico-Media-Server-macOS-' + target.split('-')[1] + '.pkg')
        with pkg.open('rb') as stream:
            header = stream.read(4)
        if header != b'xar!':
            raise ValueError('not a pkg xar archive')
    else:
        portable = output / ('Portico-Media-Server-Windows-' + target.split('-')[1] + '-Portable-NOT-FOR-USE.zip')
        windows_portable(payload, portable)
        installer = output / ('Portico-Media-Server-Windows-' + target.split('-')[1] + '-Setup-NOT-FOR-USE.exe')
        nsis = shutil.which('makensis') or str(pathlib.Path(os.environ.get('ProgramFiles(x86)', 'C:/Program Files (x86)')) / 'NSIS/makensis.exe')
        run([nsis, '-VERSION'])
        run([nsis, '-V2', '-DOUTPUT_FILE=' + str(installer), '-DSTAGE_DIR=' + str(payload),
             '-DPRODUCT_VERSION=0.0.0', ROOT / 'packaging/windows/installer.nsi'])
        raw = installer.read_bytes()
        if raw[:2] != b'MZ' or b'NullsoftInst' not in raw:
            raise ValueError('not an NSIS executable')
    receipt['packageFiles'] = {p.name: digest(p) for p in output.iterdir() if p.is_file()}
    receipt['constructed'] = True
    (evidence / 'unsigned-construction.json').write_text(json.dumps(receipt, indent=2) + '\n')


def windows_portable(payload, output):
    import zipfile
    # The portable launch script is deliberately absent from the service payload.
    with zipfile.ZipFile(output, 'x', compression=zipfile.ZIP_DEFLATED) as archive:
        for path in sorted(payload.rglob('*')):
            if path.is_symlink():
                raise ValueError('symlink in portable payload')
            if path.is_file():
                archive.write(path, path.relative_to(payload).as_posix())
        for name in ('README.txt', 'start-portico.cmd'):
            archive.write(ROOT / 'packaging/portable' / name, name)
        archive.writestr('NOT-FOR-USE.txt', ENGINE['README'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=TARGETS)
    parser.add_argument('--context', choices=['local', 'github-actions'], required=True)
    parser.add_argument('--lock', type=pathlib.Path, default=ROOT / 'third_party/ffmpeg/artifacts.lock.json')
    for option in ('engine', 'work', 'evidence'):
        parser.add_argument('--' + option, required=True, type=pathlib.Path)
    args = parser.parse_args()
    pin = ENGINE['select'](json.loads(args.lock.read_text()), args.target)
    construct(args.target, args.engine.resolve(), pin, args.work.resolve(), args.evidence.resolve(), args.context)
