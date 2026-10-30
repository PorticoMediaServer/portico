#!/usr/bin/env python3
"""Declare the three ingest libraries on an already bootstrapped private fixture."""
import argparse
import json
import os
from pathlib import Path
from orchestrate import private_file, private_dir, sign_in


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--base', required=True)
    p.add_argument('--credentials', required=True)
    p.add_argument('--root', required=True)
    p.add_argument('--out', required=True)
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    if not a.execute:
        print(json.dumps({'mode': 'plan', 'root': a.root, 'libraries': ['movie', 'tv', 'music']}))
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        raise ValueError('operator only')
    root = private_dir(a.root, create=True)
    creds = json.loads(private_file(a.credentials).read_text())
    client = sign_in(a.base, creds['username'], creds['password'], os.urandom(18).hex())
    ids = {}
    for kind, folder in [('movie', 'movies'), ('tv', 'tv'), ('music', 'music')]:
        directory = root / folder
        directory.mkdir(mode=0o700)
        response = client.call('POST', '/v1/libraries', {'name': 'P2-65 ingest '+kind, 'kind': kind, 'path': str(directory), 'metadataAgent': 'local'}, ok=(200,201))
        ids[kind] = response.get('library', response)['id']
    fd = os.open(a.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump({'base': a.base, 'root': str(root), 'libraries': ids}, output, indent=2)
        output.flush()
        os.fsync(output.fileno())
    print(json.dumps({'declared': len(ids), 'out': str(Path(a.out))}))


if __name__ == '__main__':
    main()
