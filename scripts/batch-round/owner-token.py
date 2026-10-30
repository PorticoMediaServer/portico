#!/usr/bin/env python3
"""Admit the isolated fixture owner and write a private operator token. Never echo it."""
import argparse
import importlib.util
import os
from pathlib import Path
import secrets


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--target', required=True)
    p.add_argument('--credentials', required=True)
    p.add_argument('--out', required=True)
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    spec = importlib.util.spec_from_file_location('factory', Path(__file__).with_name('provision-scale.py'))
    factory = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(factory)
    factory.refuse_ci(p)
    if not a.execute:
        print('PLAN: ordinary owner admission -> private operator token; no request')
        return
    if not a.target.startswith(('http://127.0.0.1:', 'http://100.120.50.24:')):
        p.error('isolated fixture target required')
    credentials = factory.read_private_json(a.credentials)
    client, _, profiles = factory.sign_in(a.target, credentials['username'], credentials['password'], secrets.token_urlsafe(24))
    primary = next((entry for entry in profiles if entry.get('primary')), profiles[0])
    session = factory.select_profile(client, primary['id'])
    factory.current_admission(client, session, 'owner')
    fd = os.open(a.out, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as output:
        output.write(session['accessToken'] + '\n')
        output.flush()
        os.fsync(output.fileno())
    factory.write_private_json(str(a.out) + '.expiry.json', {'expiresAt': session['expiresAt']})
    print('Owner admission checked; private token written. Reissue before each operator window.')


if __name__ == '__main__':
    main()
