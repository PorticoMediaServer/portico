#!/usr/bin/env python3
"""Prepare and verify Portico's pinned rclone payload; never used by the wizard."""
import argparse
import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile
import zipfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
CONTRACT = ROOT / 'third_party/rclone'


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def load_target(target):
    lock = json.loads((CONTRACT / 'bundle.lock.json').read_text())
    if target not in lock['targets']:
        raise ValueError('No bundled rclone for target ' + target)
    return lock, lock['targets'][target]


def authenticate(lock):
    if digest(CONTRACT / 'SHA256SUMS') != lock['checksumsSha256']:
        raise ValueError('rclone signed checksums changed')
    # Use a disposable keyring, never the packager's personal trust database.
    with tempfile.TemporaryDirectory(prefix='portico-rclone-gpg-') as home:
        command = ['gpg', '--homedir', home, '--batch', '--no-autostart']
        subprocess.run(command + ['--import', str(CONTRACT / 'KEYS')], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        result = subprocess.run(command + ['--status-fd', '2', '--decrypt', str(CONTRACT / 'SHA256SUMS')],
                                check=True, capture_output=True, text=True)
        if '[GNUPG:] VALIDSIG ' + lock['signingFingerprint'] + ' ' not in result.stderr:
            raise ValueError('rclone release signature has the wrong signer')
        signed = {line.split()[1]: line.split()[0] for line in result.stdout.splitlines() if len(line.split()) == 2}
        for item in [*lock['targets'].values(), lock['source'], lock['vendor']]:
            if signed.get(item['url'].rsplit('/', 1)[1]) != item['sha256']:
                raise ValueError('rclone lock disagrees with signed release checksums')


def contract_files():
    return ['LICENSE', 'DEPENDENCY-NOTICES.txt', 'bundle.lock.json', 'SHA256SUMS', 'KEYS']


def prepare(output, target, cache):
    lock, selected = load_target(target)
    authenticate(lock)
    if output.exists():
        raise ValueError('rclone output already exists: ' + str(output))
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / selected['url'].rsplit('/', 1)[1]
    if not archive.exists():
        # HTTPS plus the pinned, signed digest. Partial downloads never become cached inputs.
        with tempfile.NamedTemporaryFile(dir=cache, delete=False) as temp:
            temporary = pathlib.Path(temp.name)
        try:
            subprocess.run(['curl', '--fail', '--location', '--silent', '--show-error', '--retry', '3', '--connect-timeout', '15', '--max-time', '180', selected['url'], '-o', str(temporary)], check=True)
            if digest(temporary) != selected['sha256']:
                raise ValueError('Downloaded rclone archive checksum mismatch')
            temporary.replace(archive)
        finally:
            temporary.unlink(missing_ok=True)
    if digest(archive) != selected['sha256']:
        raise ValueError('Cached rclone archive checksum mismatch')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=output.parent, prefix='.rclone-') as stage:
        stage = pathlib.Path(stage)
        with zipfile.ZipFile(archive) as zipped:
            for name, expected in selected['files'].items():
                # Only the pinned inventory, never extract arbitrary archive paths.
                if pathlib.PurePosixPath(name).name != name:
                    raise ValueError('Invalid rclone inventory path')
                raw = zipped.read(selected['archiveRoot'] + '/' + name)
                if hashlib.sha256(raw).hexdigest() != expected:
                    raise ValueError('rclone archive payload changed: ' + name)
                (stage / name).write_bytes(raw)
        (stage / selected['binary']).chmod(0o755)
        for name in contract_files():
            shutil.copyfile(CONTRACT / name, stage / name)
        files = {p.name: digest(p) for p in stage.iterdir()}
        manifest = {'schema': 1, 'target': target, 'version': lock['version'], 'revision': lock['revision'],
                    'archiveSha256': selected['sha256'], 'files': files}
        (stage / 'bundle-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        verify(stage, target)
        shutil.copytree(stage, output)


def verify(bundle, target, execute=False, codesigned=False):
    lock, selected = load_target(target)
    manifest = json.loads((bundle / 'bundle-manifest.json').read_text())
    expected = dict(selected['files'])
    expected.update({name: digest(CONTRACT / name) for name in contract_files()})
    original_sha = selected['files'][selected['binary']]
    if codesigned and target.startswith('darwin-'):
        if manifest.get('upstreamBinarySha256') != original_sha:
            raise ValueError('rclone codesign provenance changed')
        expected[selected['binary']] = manifest['files'][selected['binary']]
    identity = {'schema': 1, 'target': target, 'version': lock['version'], 'revision': lock['revision'],
                    'archiveSha256': selected['sha256'], 'files': expected}
    if codesigned and target.startswith('darwin-'):
        identity['upstreamBinarySha256'] = original_sha
    if manifest != identity:
        raise ValueError('rclone bundle manifest/target does not match the pinned release')
    actual = {p.name for p in bundle.iterdir()}
    if actual != set(expected) | {'bundle-manifest.json'}:
        raise ValueError('rclone bundle inventory changed')
    for name, sha in expected.items():
        path = bundle / name
        if path.is_symlink() or not path.is_file() or digest(path) != sha:
            raise ValueError('Missing or modified rclone payload: ' + name)
    if execute:
        result = subprocess.run([str((bundle / selected['binary']).resolve()), 'version'],
                                check=True, capture_output=True, text=True, timeout=5)
        if result.stdout.splitlines()[0] != 'rclone v' + lock['version']:
            raise ValueError('Packaged rclone reports the wrong version')
    return manifest


def verify_release(directory, target, public_key_file):
    # Packaging consumes the builder's authenticated artifact, never a supplied
    # manifest or a replacement binary with merely plausible metadata.
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
    from cryptography.exceptions import InvalidSignature
    raw = (directory / 'manifest.json').read_bytes()
    public_key = Ed25519PublicKey.from_public_bytes(public_key_file.read_bytes())
    try:
        public_key.verify((directory / 'manifest.sig').read_bytes(), raw)
    except InvalidSignature as error:
        raise ValueError('Release manifest signature does not match the pinned key') from error
    manifest = json.loads(raw)
    return check_payload(directory, target, manifest)


def verify_unsealed(directory, target):
    # An internal NOT-FOR-USE build: the same payload checks, with no signature to check. It is
    # accepted only when its manifest says it is unsealed and carries no signature file.
    manifest = json.loads((directory / 'manifest.json').read_bytes())
    if manifest.get('sealed') is not False or (directory / 'manifest.sig').exists():
        raise ValueError('Not an unsealed build')
    return check_payload(directory, target, manifest)


def check_payload(directory, target, manifest):
    name = 'portico-server.exe' if target.startswith('windows-') else 'portico-server'
    if manifest.get('component') != 'server' or manifest.get('target') != target or manifest['artifact']['path'] != name:
        raise ValueError('Release target or server artifact path changed')
    artifact = directory / name
    if artifact.is_symlink() or digest(artifact) != manifest['artifact']['sha256']:
        raise ValueError('Release server artifact changed')
    rclone = directory / 'third_party/rclone'
    if digest(rclone / 'bundle-manifest.json') != manifest['rcloneManifestSha256']:
        raise ValueError('Release rclone manifest changed')
    return verify(rclone, target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['prepare', 'verify', 'verify-release', 'verify-unsealed'])
    parser.add_argument('directory', type=pathlib.Path)
    parser.add_argument('--target', required=True)
    parser.add_argument('--cache', type=pathlib.Path, default=ROOT / '.cache/rclone')
    parser.add_argument('--execute-version', action='store_true')
    parser.add_argument('--public-key', type=pathlib.Path)
    args = parser.parse_args()
    try:
        if args.operation == 'verify-release':
            if args.public_key is None:
                raise ValueError('A pinned release public key is required')
            verify_release(args.directory, args.target, args.public_key)
            print('rclone release payload verified: ' + args.target)
            return
        if args.operation == 'verify-unsealed':
            verify_unsealed(args.directory, args.target)
            print('rclone payload of an UNSEALED, NOT FOR USE build verified: ' + args.target)
            return
        if args.operation == 'prepare':
            prepare(args.directory, args.target, args.cache)
        verify(args.directory, args.target, args.execute_version)
    except (ValueError, OSError, KeyError, subprocess.SubprocessError, zipfile.BadZipFile) as error:
        parser.exit(1, 'rclone bundle: ' + str(error) + '\n')
    print('rclone bundle verified: ' + args.target)


if __name__ == '__main__':
    main()
