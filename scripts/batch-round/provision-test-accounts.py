#!/usr/bin/env python3
"""Complete fixture accounts using real identity APIs and fresh library-create receipts."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import secrets
import urllib.parse


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fixture-source', type=Path, required=True)
    parser.add_argument('--credentials', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    if not args.execute:
        print('PLAN: verify fresh server/library receipts; create members and owner kids/guest profiles, restrictions and avatars through real APIs')
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        parser.error('Live fixture account setup is prohibited in CI')
    spec = importlib.util.spec_from_file_location('fixture_seed', args.fixture_source / 'fixtures/test-library/seed_state.py')
    seed = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(seed)
    cfg = seed.private_json(args.credentials)
    saved = cfg.get('fixtureStateV3', {})
    libraries = saved.get('libraries', {})
    if not libraries or not saved.get('serverId'):
        parser.error('Need current fresh-server API library-create receipts, not an older DB cache')
    client = seed.Client('http://127.0.0.1:49300')
    signed, client.token = seed.sign_in(client, cfg['owner']['username'], cfg['owner']['password'])
    primary = next(p for p in signed['profiles'] if p.get('primary'))
    client.token = seed.select_profile(client, primary['id'])
    system = client.read('/v1/system')
    if system.get('id') != saved['serverId']:
        parser.error('Fixture receipts belong to another server; never transplant old identity state')
    report = {'created': {'members': [], 'profiles': [], 'avatars': []}, 'warnings': [], 'serverId': system['id']}
    ids = [lib['id'] for lib in libraries.values()]
    for member in cfg.get('members', []):
        seed.ensure_member(client, member['username'], member['password'], member.get('name', member['username']), ids, report)
    kids = seed.ensure_profile(client, 'Synthetic Kids', 'mint', report)
    guest = seed.ensure_profile(client, 'Synthetic Guest', 'gold', report)
    profiles = saved.setdefault('profiles', {})
    pin = profiles.get(kids['id'], {}).get('pin') or str(1000 + secrets.randbelow(9000))
    edit = {'expectedRevision': kids['revision'], 'policy': {'allowedLibraries': ids}}
    if not kids.get('pinRequired'):
        edit['pin'] = pin
    path = '/v1/direct/profiles/' + urllib.parse.quote(kids['id'], safe='')
    client.call('PATCH', path, edit, ok=(200,))
    profiles[kids['id']] = {'name': 'Synthetic Kids', 'pin': pin}
    seed.write_private(args.credentials, cfg)
    restriction = client.read(path + '/restrictions')
    client.call('PUT', path + '/restrictions', {'expectedRevision': restriction['revision'], 'ratingSystem': 'mpaa',
        'maximumAgeRating': 'PG', 'allowUnrated': True, 'blockedLabels': [], 'allowDownloads': False,
        'allowLiveTv': False, 'allowDvr': False, 'allowWatchTogether': False}, ok=(200,))
    for profile in [primary, kids, guest]:
        seed.upload_avatar(client, profile['id'], 'Profile ' + profile.get('name', 'Synthetic'), report)
    snapshot = client.read('/v1/direct')
    actual = snapshot.get('profiles') or []
    report['ownerProfiles'] = len(actual)
    report['kidsPinRequired'] = any(p.get('id') == kids['id'] and p.get('pinRequired') for p in actual)
    report['memberAccounts'] = len(seed.items(client.read('/v1/direct/members')))
    report['ok'] = report['kidsPinRequired'] and report['memberAccounts'] >= len(cfg.get('members', [])) and not report['warnings']
    args.report.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.report.with_suffix('.tmp')
    fd = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as output:
        output.write(json.dumps(report, indent=2) + '\n')
    temporary.replace(args.report)
    print(json.dumps({k: report[k] for k in ['ok', 'ownerProfiles', 'kidsPinRequired', 'memberAccounts']}))
    if not report['ok']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
