#!/usr/bin/env python3
"""Authenticate final packages, web assets inside them, notices and update feed."""
import argparse
import hashlib
import json
import pathlib
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey

EXCLUDED = {'release-manifest.json', 'release-manifest.sig'}

def inventory(directory):
    files = []
    for path in sorted(directory.iterdir()):
        if path.name in EXCLUDED:
            continue
        if path.is_symlink() or not path.is_file():
            raise ValueError('Final release inventory must contain only regular files')
        digest = hashlib.sha256()
        with path.open('rb') as source:
            for block in iter(lambda: source.read(128 << 10), b''):
                digest.update(block)
        files.append({'path': path.name, 'bytes': path.stat().st_size, 'sha256': digest.hexdigest()})
    if not files or len(files) > 100:
        raise ValueError('Unexpected release inventory size')
    return files

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=pathlib.Path)
    parser.add_argument('--public-key', type=pathlib.Path, required=True)
    parser.add_argument('--key', type=pathlib.Path)
    parser.add_argument('--source', default='')
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    public = Ed25519PublicKey.from_public_bytes(args.public_key.read_bytes())
    manifest = args.directory / 'release-manifest.json'
    signature = args.directory / 'release-manifest.sig'
    if not args.verify:
        if args.key is None or args.key.is_symlink() or args.key.stat().st_mode & 0o077:
            raise ValueError('A protected, approved release signing key is required')
        key = Ed25519PrivateKey.from_private_bytes(args.key.read_bytes())
        if key.public_key().public_bytes_raw() != args.public_key.read_bytes():
            raise ValueError('Release signer differs from pinned public key')
        manifest.write_bytes((json.dumps({'schema': 1, 'sourceCommit': args.source, 'files': inventory(args.directory)}, sort_keys=True, indent=2)+'\n').encode())
        signature.write_bytes(key.sign(manifest.read_bytes()))
    public.verify(signature.read_bytes(), manifest.read_bytes())
    document = json.loads(manifest.read_bytes())
    if document['files'] != inventory(args.directory):
        raise ValueError('Final release files differ from authenticated inventory')
    if args.source and document['sourceCommit'] != args.source:
        raise ValueError('Release source differs from expected revision')

if __name__ == '__main__':
    main()
