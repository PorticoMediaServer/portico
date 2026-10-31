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
TARGETS = {'macos-x64': ('darwin', 'amd64'), 'macos-arm64': ('darwin', 'arm64'),
           'windows-x64': ('windows', 'amd64'), 'windows-arm64': ('windows', 'arm64')}


def guard(env):
    workflow = env.get('GITHUB_WORKFLOW_REF', '').split('@')[0]
    if (env.get('GITHUB_ACTIONS') != 'true' or
        workflow != 'PorticoMediaServer/portico/.github/workflows/packaging-qualification.yml' or
        env.get('GITHUB_EVENT_NAME') not in ('pull_request', 'workflow_dispatch') or
        env.get('GITHUB_REF', '').startswith('refs/tags/')):
        raise ValueError('only the dedicated qualification workflow may construct unsigned payloads')
    for key in env:
        if any(word in key.upper() for word in ('SIGNING', 'NOTAR', 'APPLE_ID', 'CSC_', 'PRODUCTKEY', 'SIGNINGCONFIG')):
            raise ValueError('signing/product configuration forbidden: ' + key)


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


def extract_transfer(archive, destination):
    import zipfile
    with zipfile.ZipFile(archive) as zipped:
        total = 0
        seen = set()
        for entry in zipped.infolist():
            path = pathlib.PurePosixPath(entry.filename)
            total += entry.file_size
            if (not entry.filename or any(x in ('', '.', '..') for x in entry.filename.rstrip('/').split('/')) or path.is_absolute() or '..' in path.parts or '\\' in entry.filename or ':' in entry.filename
                    or entry.filename in seen or (entry.external_attr >> 16) & 0o170000 == 0o120000
                    or total > 3 * 1024**3):
                raise ValueError('unsafe artifact entry')
            seen.add(entry.filename)
        zipped.extractall(destination)


def download(artifact_id, expected_digest, target, destination):
    """Authenticate raw Actions ZIP before bounded, traversal-safe extraction."""
    import urllib.request
    import urllib.parse
    guard(os.environ)
    class Redirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
            if urllib.parse.urlsplit(req.full_url).netloc != urllib.parse.urlsplit(newurl).netloc:
                redirected.remove_header('Authorization')
            if urllib.parse.urlsplit(newurl).scheme != 'https':
                raise ValueError('non-HTTPS artifact redirect')
            return redirected
    opener = urllib.request.build_opener(Redirect())
    if not artifact_id.isdecimal() or len(expected_digest.removeprefix('sha256:')) != 64:
        raise ValueError('invalid artifact identity')
    base = 'https://api.github.com/repos/PorticoMediaServer/portico/actions/artifacts/' + artifact_id
    headers = {'Authorization': 'Bearer ' + os.environ['GH_TOKEN'], 'Accept': 'application/vnd.github+json'}
    with opener.open(urllib.request.Request(base, headers=headers), timeout=60) as response:
        metadata = json.load(response)
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    expected_name = 'ci-intermediate-ffmpeg-' + target + '-' + sha + '-NOT-FOR-USE'
    if (metadata['name'] != expected_name or metadata['workflow_run']['id'] != int(os.environ['GITHUB_RUN_ID'])
            or metadata['workflow_run']['head_sha'] != sha or metadata['expired']
            or metadata['size_in_bytes'] > 3 * 1024**3
            or metadata['digest'].removeprefix('sha256:') != expected_digest.removeprefix('sha256:')):
        raise ValueError('artifact run/source/target/digest/size mismatch')
    evidence = destination.parent / 'evidence'
    evidence.mkdir(parents=True, exist_ok=True)
    (evidence / 'action-artifact.json').write_text(json.dumps({'id': artifact_id, 'digest': expected_digest, 'run': metadata['workflow_run']['id'], 'sourceSha': sha, 'target': target}, indent=2) + '\n')
    destination.mkdir(parents=True, exist_ok=False)
    archive = destination.parent / (destination.name + '.zip')
    try:
        with opener.open(urllib.request.Request(base + '/zip', headers=headers), timeout=60) as response, archive.open('wb') as out:
            total = 0
            while chunk := response.read(1024 * 1024):
                total += len(chunk)
                if total > 3 * 1024**3:
                    raise ValueError('artifact too large')
                out.write(chunk)
        if digest(archive) != expected_digest.removeprefix('sha256:'):
            raise ValueError('raw action artifact digest mismatch')
        extract_transfer(archive, destination)
    finally:
        archive.unlink(missing_ok=True)


def construct(target, engine, work, evidence):
    guard(os.environ)
    osname, arch = TARGETS[target]
    native = platform.machine().lower()
    if platform.system() != ('Darwin' if osname == 'darwin' else 'Windows') or native not in (
            ('x86_64', 'amd64') if arch == 'amd64' else ('arm64', 'aarch64')):
        raise ValueError('matching native host required')
    work.mkdir(parents=True, exist_ok=False)
    evidence.mkdir(parents=True, exist_ok=True)
    if shutil.disk_usage(work).free < 8 * 1024**3:
        raise ValueError('less than 8GiB free before payload construction')
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    proof = json.loads((evidence / 'native-engine.json').read_text())
    if (proof.get('sourceSHA') != sha or proof.get('target') != target or
            proof.get('nativeEngineChecks') is not True or
            proof.get('toolchainManifestSHA256') != digest(engine / 'toolchain-manifest.json')):
        raise ValueError('missing matching native engine qualification')
    run([sys.executable, ROOT / 'scripts/verify-ffmpeg-manifest.py', engine])
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
    (payload / 'web/portico-build.json').write_text(json.dumps({'version': '0.0.0', 'build': os.environ['GITHUB_RUN_NUMBER']}))
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    receipt = {'schema': 1, 'kind': 'unsigned-construction-qualification', 'release': False,
               'qualifiedRelease': False, 'sourceSha': sha, 'target': target, 'capabilityTags': tags,
               'notExercised': ['Portico integrity seal', 'sealed startup', 'OS signing', 'installation'],
               'files': inventory(payload),
               'runnerImage': {k: os.environ.get(k, '') for k in ('ImageOS', 'ImageVersion', 'RUNNER_ARCH')},
               'inputLocks': {p: digest(ROOT / p) for p in ('third_party/ffmpeg/sources.lock.json', 'third_party/rclone/bundle.lock.json', 'package-lock.json', 'server/go.mod', '.node-version')}}
    (payload / 'qualification-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
    output = work / 'packages'
    output.mkdir()
    if osname == 'darwin':
        run(['pkgbuild', '--version'])
        run(['bash', ROOT / 'scripts/package-macos.sh', '0.0.0', payload, output,
             os.environ['GITHUB_RUN_NUMBER'], 'x64' if arch == 'amd64' else 'arm64'])
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
        installer = output / ('Portico-Media-Server-Windows-' + target.split('-')[1] + '-Setup.exe')
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


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=TARGETS)
    for option in ('engine', 'work', 'evidence'):
        parser.add_argument('--' + option, required=True, type=pathlib.Path)
    args = parser.parse_args()
    construct(args.target, args.engine.resolve(), args.work.resolve(), args.evidence.resolve())
