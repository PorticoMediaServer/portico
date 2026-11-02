#!/usr/bin/env python3
"""Consume an exact, separately built qualification engine. Never build one."""
import argparse
from datetime import datetime, timezone, timedelta
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.parse
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ('windows-x64', 'windows-arm64', 'macos-arm64')
MAX_ARCHIVE = 3 * 1024**3
MAX_EXPANDED = 8 * 1024**3
MAX_FILES = 200000
README = 'Qualification engine intermediate; NOT-FOR-USE; not a Portico release.\n'
WINDOWS_CONFIGURE_SCRIPTS = {
    'provenance/scripts/build-engine.py', 'provenance/scripts/engine_inputs.py',
    'provenance/scripts/engine-recipe-adapter.py', 'provenance/scripts/configure-ffmpeg-recipe.py', 'provenance/scripts/configure-ffmpeg-bzip2.py', 'provenance/scripts/ffmpeg-configure-checks.py',
    'provenance/scripts/configure-windows-libdovi.py',
    'provenance/scripts/engine-registry-cache.py', 'provenance/scripts/engine-download-cache.py',
    'provenance/scripts/patch-btbn-winarm64-system-error.sh',
    'provenance/scripts/prepare-ffmpeg-source.py', 'provenance/scripts/archive-ffmpeg-source.py',
    'provenance/scripts/ffmpeg-build-tools.py', 'provenance/scripts/requirements-ffmpeg-meson.txt',
}


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def json_digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def timestamp(value):
    parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    if parsed.utcoffset() != timedelta(0):
        raise ValueError('UTC timestamp required')
    return parsed


def select(lock, target, now=None):
    if lock.get('schemaVersion') != 1 or target not in TARGETS:
        raise ValueError('unknown engine lock schema/target')
    pin = lock['engines'].get(target)
    if pin is None:
        raise ValueError('no accepted pinned engine for ' + target + '; engine-dependent qualification blocked')
    # D1 rejected every investigated stand-in. No substitute may enter the
    # own-engine path or acquire a fabricated toolchain/native receipt.
    if pin['target'] != target or pin['kind'] != 'portico-engine' or pin['purpose'] != 'qualification':
        raise ValueError('only pinned Portico qualification engines are accepted')
    for field in ('archiveSHA256', 'bundleManifestSHA256', 'sourceArchiveSHA256', 'licensesSHA256'):
        if not re.fullmatch('[a-f0-9]{64}', pin[field]):
            raise ValueError('invalid engine digest: ' + field)
    if not re.fullmatch('[a-f0-9]{40}', pin['engineSourceSHA']):
        raise ValueError('invalid engine source SHA')
    expected_name = f"ci-intermediate-ffmpeg-{target}-{pin['engineSourceSHA']}-NOT-FOR-USE"
    if pin['name'] != expected_name:
        raise ValueError('engine intermediate name mismatch')
    created, expires = timestamp(pin['createdAt']), timestamp(pin['expiresAt'])
    if not created <= (now or datetime.now(timezone.utc)) < expires or not timedelta(0) < expires - created <= timedelta(days=14):
        raise ValueError('expired, future or over-14-day engine artifact')
    origin = pin['origin']
    if origin['kind'] != 'github-actions' or origin['repository'] != 'PorticoMediaServer/portico':
        raise ValueError('unapproved engine origin')
    if any(type(origin[k]) is not int or origin[k] <= 0 for k in ('runId', 'runAttempt', 'artifactId')):
        raise ValueError('invalid artifact/run identity')
    return pin


class Redirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urllib.parse.urlsplit(newurl).scheme != 'https':
            raise ValueError('non-HTTPS artifact redirect')
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if urllib.parse.urlsplit(req.full_url).netloc != urllib.parse.urlsplit(newurl).netloc:
            redirected.remove_header('Authorization')
        return redirected


def remote(pin, destination):
    origin = pin['origin']
    base = 'https://api.github.com/repos/' + origin['repository']
    headers = {'Accept': 'application/vnd.github+json'}
    if os.environ.get('GH_TOKEN'):
        headers['Authorization'] = 'Bearer ' + os.environ['GH_TOKEN']
    opener = urllib.request.build_opener(Redirect())

    def request(url):
        return opener.open(urllib.request.Request(url, headers=headers), timeout=60)

    artifact = base + '/actions/artifacts/' + str(origin['artifactId'])
    with request(artifact) as response:
        metadata = json.load(response)
    verify_metadata(pin, metadata)
    with request(base + f"/actions/runs/{origin['runId']}/attempts/{origin['runAttempt']}") as response:
        attempt = json.load(response)
    if (attempt['id'] != origin['runId'] or attempt['run_attempt'] != origin['runAttempt'] or
            attempt['head_sha'] != pin['engineSourceSHA']):
        raise ValueError('engine run attempt/source mismatch')
    with request(artifact + '/zip') as response, destination.open('wb') as output:
        total = 0
        while chunk := response.read(1024 * 1024):
            total += len(chunk)
            if total > MAX_ARCHIVE:
                raise ValueError('artifact download exceeds bound')
            output.write(chunk)


def verify_metadata(pin, metadata):
    origin = pin['origin']
    if (metadata['id'] != origin['artifactId'] or metadata['name'] != pin['name'] or
            metadata['workflow_run']['id'] != origin['runId'] or
            metadata['workflow_run']['head_sha'] != pin['engineSourceSHA'] or metadata['expired'] or
            not 0 < metadata['size_in_bytes'] <= MAX_ARCHIVE or
            metadata['digest'] != 'sha256:' + pin['archiveSHA256'] or
            timestamp(metadata['created_at']) != timestamp(pin['createdAt']) or
            timestamp(metadata['expires_at']) != timestamp(pin['expiresAt'])):
        raise ValueError('artifact identity/source/digest/retention mismatch')


def safe_name(name):
    parts = name.rstrip('/').split('/')
    reserved = {'con', 'prn', 'aux', 'nul', *(f'com{i}' for i in range(1, 10)), *(f'lpt{i}' for i in range(1, 10))}
    if (not name or PurePosixPath(name).is_absolute() or '\\' in name or ':' in name or
            any(p in ('', '.', '..') or p.endswith((' ', '.')) or p.split('.')[0].lower() in reserved or
                any(ord(c) < 32 or c in '<>"|?*' for c in p) for p in parts)):
        raise ValueError('unsafe artifact member: ' + name)
    return '/'.join(parts)


def extract(archive, destination):
    """Validate every entry before writing; never materialize links or devices."""
    zipped = zipfile.is_zipfile(archive)
    with (zipfile.ZipFile(archive) if zipped else tarfile.open(archive)) as source:
        members = source.infolist() if zipped else []
        if not zipped:
            for member in source:
                members.append(member)
                if len(members) > MAX_FILES:
                    raise ValueError('too many artifact members')
        total, seen, files = 0, set(), set()
        if len(members) > MAX_FILES:
            raise ValueError('too many artifact members')
        entries = []
        for member in members:
            # Windows ZipInfo normalizes backslashes (and truncates NULs) in
            # filename. Validate the original central-directory name first.
            name = safe_name(member.orig_filename if zipped else member.name)
            directory = member.is_dir() if zipped else member.isdir()
            size = member.file_size if zipped else member.size
            mode = (member.external_attr >> 16) & 0o170000 if zipped else 0
            if (name.casefold() in seen or (zipped and mode not in (0, 0o100000, 0o040000)) or
                    (not zipped and (not (member.isfile() or directory) or member.sparse)) or size < 0):
                raise ValueError('duplicate/link/special artifact member')
            total += size
            if total > MAX_EXPANDED:
                raise ValueError('artifact expansion exceeds bound')
            seen.add(name.casefold())
            if not directory:
                files.add(name.casefold())
            entries.append((member, name, directory))
        for _, name, _ in entries:
            if any(parent.as_posix().casefold() in files for parent in PurePosixPath(name).parents if parent.as_posix() != '.'):
                raise ValueError('artifact file/directory collision')
        destination.mkdir()
        for member, name, directory in entries:
            path = destination / name
            if directory:
                path.mkdir(parents=True, exist_ok=True)
                continue
            path.parent.mkdir(parents=True, exist_ok=True)
            with (source.open(member) if zipped else source.extractfile(member)) as inp, path.open('xb') as out:
                shutil.copyfileobj(inp, out, 1024 * 1024)
            path.chmod(0o755 if name.startswith('bundle/bin/') else 0o644)


def verify_windows_sources(files, source_scripts, dockerfile):
    selected = 0
    for line in dockerfile.splitlines():
        if not line.startswith('RUN '):
            continue
        scripts = re.findall(r'--mount=src=(scripts\.d/[^,\s]+),dst=/stage\.sh(?:\s|$)', line)
        sources = re.findall(r'--mount=src=(\.cache/downloads/[^,\s]+),dst=/cache\.tar\.xz(?:\s|$)', line)
        if ('--mount=src=scripts.d/' in line and len(scripts) != 1) or (sources and not scripts):
            raise ValueError('unsupported Windows dependency source mount')
        for script in scripts:
            script = safe_name('recipe/' + script)
            if script not in files:
                raise ValueError('missing selected dependency recipe: ' + script)
            if script not in source_scripts and not sources:
                continue  # A selected setup-only stage does not download source.
            if len(sources) != 1:
                raise ValueError('missing selected dependency source mount: ' + script)
            source = safe_name('recipe/' + sources[0])
            expected = r'recipe/\.cache/downloads/' + re.escape(PurePosixPath(script).stem) + r'_[a-f0-9]{64}\.tar\.xz'
            if not re.fullmatch(expected, source) or source not in files:
                raise ValueError('missing exact dependency source snapshot: ' + source)
            selected += 1
    if not selected:
        raise ValueError('missing Windows dependency source inventory')


def verify_native_linux_sources(hashes, dockerfile, provenance):
    """Check native Linux's existing manifest-bound inputs, not a Windows receipt."""
    required = {'linux-build-inputs.json', 'dependency-inputs.json'}
    if not isinstance(provenance, dict) or set(provenance) != required:
        raise ValueError('missing manifest-sealed native Linux source inputs')
    for name, data in provenance.items():
        archived = ('provenance/' if name == 'linux-build-inputs.json' else
                    'provenance/linux-dependencies/') + name
        if hashes.get(archived) != hashlib.sha256(data).hexdigest():
            raise ValueError('native Linux archived inputs differ: ' + name)
    inputs = json.loads(provenance['linux-build-inputs.json'])
    ledger = json.loads(provenance['dependency-inputs.json'])
    if (not isinstance(ledger, list) or not ledger or
            not re.fullmatch(r'[a-f0-9]{64}', str(inputs.get('files', {}).get('Dockerfile'))) or
            not re.fullmatch(r'[a-f0-9]{64}', str(inputs.get('sourceArchiveSHA256'))) or
            hashes.get('recipe/Dockerfile') != inputs.get('files', {}).get('Dockerfile') or
            not any(name.startswith('provenance/') and name.count('/') == 1 and
                    sha == inputs.get('sourceArchiveSHA256') for name, sha in hashes.items())):
        raise ValueError('native Linux source/graph identity mismatch')
    # The graph is bound by linux-build-inputs.json in the externally pinned
    # manifest. Never execute recipes from an artifact to validate it.
    records = {}
    for record in ledger:
        script = safe_name('recipe/' + record['script'])
        if script in records or not script.startswith('recipe/scripts.d/') or not script.endswith('.sh'):
            raise ValueError('invalid native Linux dependency recipe')
        records[script] = record
    selected = set()
    for line in dockerfile.splitlines():
        if not line.startswith('RUN '):
            continue
        scripts = re.findall(r'--mount=src=(scripts\.d/[^,\s]+),dst=/stage\.sh(?:\s|$)', line)
        sources = re.findall(r'--mount=src=(\.cache/downloads/[^,\s]+),dst=/cache\.tar\.xz(?:\s|$)', line)
        if ('--mount=src=scripts.d/' in line and len(scripts) != 1) or (sources and not scripts):
            raise ValueError('unsupported native Linux dependency source mount')
        for script in scripts:
            name = safe_name('recipe/' + script)
            if name not in hashes:
                raise ValueError('missing native Linux dependency recipe: ' + name)
            if not sources and name not in records:
                continue  # The native builder does not compile setup-only stages.
            record = records.get(name)
            if not record or len(sources) != 1 or name in selected:
                raise ValueError('missing/duplicate native Linux dependency source record: ' + name)
            archive = safe_name(record['sourceArchive'])
            expected = PurePosixPath(script).stem + '_' + record['downloadRecipeSHA256'] + '.tar.xz'
            if (archive != expected or sources[0] != '.cache/downloads/' + archive or
                    not re.fullmatch(r'.+_[a-f0-9]{64}\.tar\.xz', archive) or
                    hashes.get(name) != record.get('scriptSHA256') or
                    hashes.get('provenance/linux-dependencies/sources/' + archive) != record.get('sourceArchiveSHA256') or
                    record.get('compilerImage') != inputs.get('compilerImage') or
                    hashes.get('provenance/scripts/build-linux-ffmpeg-deps.py') != record.get('runnerSHA256') or
                    not record.get('licenses')):
                raise ValueError('native Linux dependency source identity mismatch: ' + name)
            selected.add(name)
    if not selected or selected != set(records):
        raise ValueError('native Linux dependency ledger differs from selected graph')


def verify_source(path, target, build_receipt=None, linux_provenance=None):
    # Inventory only: corresponding source may contain ordinary internal
    # symlinks, but no escaping links, devices or unbounded expansion.
    names, files, source_scripts, total = set(), set(), set(), 0
    dockerfile = ''
    recipe_tree, configure_scripts, dockerfile_sha = {}, {}, None
    native_hashes = {}
    with tarfile.open(path) as archive:
        for member in archive:
            name = safe_name(member.name)
            if name in names or len(names) >= MAX_FILES or member.sparse:
                raise ValueError('duplicate/sparse/oversized source inventory')
            names.add(name)
            total += member.size
            if total > 30 * 1024**3:
                raise ValueError('source expansion exceeds bound')
            if member.issym() or member.islnk():
                safe_name(member.linkname)
            elif not (member.isfile() or member.isdir()):
                raise ValueError('special corresponding-source member')
            if member.isfile():
                files.add(name)
            recipe_script = name.startswith('recipe/scripts.d/') and name.endswith('.sh')
            selected_text = member.isfile() and (name == 'recipe/Dockerfile' or recipe_script)
            data = None
            if selected_text:
                if member.size > (2 if name == 'recipe/Dockerfile' else 1) * 1024**2:
                    raise ValueError('oversized generated graph/dependency recipe')
                data = archive.extractfile(member).read()
            if target.startswith('windows') and (name.startswith('recipe/') or name in WINDOWS_CONFIGURE_SCRIPTS):
                if member.isfile():
                    sha = hashlib.sha256(data).hexdigest() if data is not None else hashlib.file_digest(
                        archive.extractfile(member), 'sha256').hexdigest()
                    if name.startswith('recipe/'):
                        recipe_tree[name.removeprefix('recipe/')] = {'kind': 'file', 'sha256': sha, 'mode': member.mode & 0o777}
                    if name in WINDOWS_CONFIGURE_SCRIPTS:
                        configure_scripts[name] = sha
                    if name == 'recipe/Dockerfile':
                        dockerfile_sha = sha
                elif member.issym():
                    recipe_tree[name.removeprefix('recipe/')] = {'kind': 'symlink', 'target': member.linkname, 'mode': member.mode & 0o777}
                elif member.islnk():
                    raise ValueError('hard-linked recipe inputs are not supported')
            if target == 'linux-x64' and member.isfile() and (
                    name.startswith(('recipe/scripts.d/', 'provenance/linux-dependencies/sources/')) or
                    name in {'recipe/Dockerfile', 'provenance/linux-build-inputs.json',
                             'provenance/linux-dependencies/dependency-inputs.json',
                             'provenance/scripts/build-linux-ffmpeg-deps.py'} or
                    (name.startswith('provenance/') and name.count('/') == 1)):
                native_hashes[name] = (hashlib.sha256(data).hexdigest() if data is not None else
                                      hashlib.file_digest(archive.extractfile(member), 'sha256').hexdigest())
            if member.isfile() and name == 'recipe/Dockerfile':
                dockerfile = data.decode('utf-8')
            if member.isfile() and recipe_script:
                if re.search(rb'^SCRIPT_REPO=', data, re.M):
                    source_scripts.add(name)
    required = {'provenance/contract/sources.lock.json', 'provenance/contract/build-source-authority.json',
                'provenance/scripts/build-ffmpeg.sh', 'provenance/portico-disc/LICENSE',
                'provenance/portico-disc/portico-disc.c'}
    if not required <= names or not any('/patches/' in name for name in names):
        raise ValueError('incomplete corresponding source provenance')
    if target.startswith('windows'):
        if not {'recipe/build.sh', 'recipe/makeimage.sh', 'recipe/Dockerfile'} <= files:
            raise ValueError('missing Windows dependency source inventory')
        if (not isinstance(build_receipt, dict) or build_receipt.get('schemaVersion') != 1 or
                build_receipt.get('release') is not False or build_receipt.get('target') != target or
                not re.fullmatch('[a-f0-9]{40}', str(build_receipt.get('engineSourceSHA'))) or
                set(configure_scripts) != WINDOWS_CONFIGURE_SCRIPTS or
                build_receipt.get('configureScripts') != configure_scripts or
                build_receipt.get('recipeTreeSHA256') != json_digest(recipe_tree) or
                build_receipt.get('dockerfileSHA256') != dockerfile_sha or
                build_receipt.get('sourceArchiveSHA256') != digest(path)):
            raise ValueError('sealed source build receipt does not match recipe, graph or configure scripts')
        # Keep the complete upstream recipe, including disabled target scripts.
        # Corresponding source must cover every actual generated build mount,
        # with the exact command-hash snapshot, rather than a filename prefix.
        verify_windows_sources(files, source_scripts, dockerfile)
    elif target == 'linux-x64':
        verify_native_linux_sources(native_hashes, dockerfile, linux_provenance)
    elif target == 'linux-arm64':
        # BtbN ARM keeps the same exact command-hash snapshots as Windows,
        # without the Windows-only assembly sidecar. The entire source archive
        # is bound by the externally pinned product's manifest and transfer.
        verify_windows_sources(files, source_scripts, dockerfile)
    elif target.startswith('macos-') and ('recipe/build-ffmpeg' not in names or
          not any(name.startswith('recipe/packages/') and name.endswith(('.tar.gz', '.tar.xz', '.tar.bz2')) for name in names)):
        raise ValueError('missing Mac recipe/dependency sources')
    elif not target.startswith('macos-'):
        raise ValueError('unsupported corresponding-source target')


def verify_bundle_source(bundle, target, engine_source_sha=None):
    """Dispatch by producer layout after checking its manifest-bound records."""
    manifest = json.loads((bundle / 'toolchain-manifest.json').read_text())
    if manifest.get('target') != target:
        raise ValueError('source manifest target mismatch')

    def sealed(name):
        path = bundle / name
        if (path.is_symlink() or not path.is_file() or path.stat().st_size > 16 * 1024**2 or
                digest(path) != manifest['files'].get(name)):
            raise ValueError('missing or changed manifest-sealed source input: ' + name)
        return path.read_bytes()

    source = bundle / 'corresponding-source.tar.xz'
    if source.is_symlink() or digest(source) != manifest['files'].get(source.name):
        raise ValueError('corresponding source manifest digest mismatch')
    build_receipt, linux_provenance = None, None
    if target.startswith('windows'):
        build_receipt = json.loads(sealed('source-build-receipt.json'))
        if engine_source_sha is not None and build_receipt.get('engineSourceSHA') != engine_source_sha:
            raise ValueError('source build receipt differs from pinned engine source')
    elif target == 'linux-x64':
        linux_provenance = {name: sealed(name) for name in ('linux-build-inputs.json', 'dependency-inputs.json')}
    verify_source(source, target, build_receipt, linux_provenance)


def verify_bundle(envelope, pin):
    if {p.name for p in envelope.iterdir()} != {'README.txt', 'engine-receipt.json', 'bundle'}:
        raise ValueError('engine envelope inventory mismatch')
    if (envelope / 'README.txt').read_text() != README:
        raise ValueError('missing qualification-only notice')
    proof = json.loads((envelope / 'engine-receipt.json').read_text())
    if (proof.get('schemaVersion') != 1 or proof.get('name') != pin['name'] or
            proof.get('target') != pin['target'] or proof.get('engineSourceSHA') != pin['engineSourceSHA'] or
            proof.get('release') is not False):
        raise ValueError('engine receipt identity mismatch')
    bundle = envelope / 'bundle'
    if digest(bundle / 'toolchain-manifest.json') != pin['bundleManifestSHA256']:
        raise ValueError('engine manifest digest mismatch')
    manifest = json.loads((bundle / 'toolchain-manifest.json').read_text())
    if manifest['target'] != pin['target']:
        raise ValueError('engine manifest target mismatch')
    subprocess.run([sys.executable, str(ROOT / 'scripts/verify-ffmpeg-manifest.py'), str(bundle)], check=True)
    if digest(bundle / 'corresponding-source.tar.xz') != pin['sourceArchiveSHA256']:
        raise ValueError('corresponding source digest mismatch')
    verify_bundle_source(bundle, pin['target'], pin['engineSourceSHA'])
    licenses = {p.relative_to(bundle).as_posix(): digest(p) for p in sorted((bundle / 'LICENSES').rglob('*')) if p.is_file()}
    if len(licenses) < 2 or json_digest(licenses) != pin['licensesSHA256']:
        raise ValueError('licence inventory digest mismatch')
    if digest(bundle / 'requirements.v1.json') != digest(ROOT / 'third_party/ffmpeg/requirements.v1.json'):
        raise ValueError('engine feature contract differs from installer contract')
    return bundle


def fetch(lock, target, output, archive=None):
    pin = select(lock, target)
    if output.exists() or any(p.is_symlink() for p in (output, *output.parents)):
        raise ValueError('engine output must be fresh and have no symlink parents')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='engine-intake-', dir=output.parent) as temporary:
        work = Path(temporary)
        raw = work / 'artifact.zip'
        if archive is None:
            remote(pin, raw)
        else:
            if archive.is_symlink() or not archive.is_file() or archive.stat().st_size > MAX_ARCHIVE:
                raise ValueError('invalid local artifact')
            shutil.copyfile(archive, raw)
        if not raw.is_file() or raw.is_symlink() or not 0 < raw.stat().st_size <= MAX_ARCHIVE or digest(raw) != pin['archiveSHA256']:
            raise ValueError('raw engine artifact SHA-256/size mismatch')
        extract(raw, work / 'input')
        verify_bundle(work / 'input', pin)
        (work / 'input').rename(output)
    return output / 'bundle'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--lock', type=Path, default=ROOT / 'third_party/ffmpeg/artifacts.lock.json')
    parser.add_argument('--target', choices=TARGETS, required=True)
    parser.add_argument('--purpose', choices=['qualification'], required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--archive', type=Path, help='local copy of the exact locked artifact bytes')
    args = parser.parse_args()
    print(fetch(json.loads(args.lock.read_text()), args.target, args.output.absolute(), args.archive))
