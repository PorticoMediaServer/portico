#!/usr/bin/env python3
"""Provision isolated owner profiles without placing credentials on the device."""
import importlib.util
import os
from pathlib import Path
import secrets


def prepare(api):
    username = api.get('/v1/dev/e2e')['username']
    signed = api.post('/v1/dev/e2e/sign-in', {'username': username})
    api.token = signed['session']['accessToken']
    snapshot = api.post('/v1/direct/profiles', {'name': 'Restricted E2E', 'art': ''}, (201,))
    profile = next(p for p in snapshot['profiles'] if p['name'] == 'Restricted E2E')
    path = '/v1/direct/profiles/' + profile['id']
    restriction = api.get(path + '/restrictions')
    api.call('PUT', path + '/restrictions', {
        'expectedRevision': restriction['revision'], 'ratingSystem': 'mpaa',
        'maximumAgeRating': 'PG-13', 'allowUnrated': True, 'blockedLabels': [],
        'allowDownloads': False, 'allowLiveTv': False, 'allowDvr': False,
        'allowWatchTogether': False,
    })
    snapshot = api.post('/v1/direct/profiles', {'name': 'Locked E2E', 'art': ''}, (201,))
    profile = next(p for p in snapshot['profiles'] if p['name'] == 'Locked E2E')
    api.call('PATCH', '/v1/direct/profiles/' + profile['id'],
             {'expectedRevision': profile['revision'], 'pin': f'{secrets.randbelow(10000):04d}'})
    return path


def has_title(value, title):
    if isinstance(value, dict):
        return value.get('title') == title or any(has_title(item, title) for item in value.values())
    if isinstance(value, list):
        return any(has_title(item, title) for item in value)
    return False


def main():
    root = Path(__file__).resolve().parents[3]
    spec = importlib.util.spec_from_file_location('fixture', root / 'scripts/e2e-server.py')
    fixture = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(fixture)
    api = fixture.API(os.environ['PORTICO_E2E_SERVER_URL'])
    path = prepare(api)
    # A missing search index or unrated/mis-scanned Moon must not look like a
    # successful restriction. Check the same server before and after selection.
    fixture.wait(lambda: has_title(api.get('/v1/search?q=Moon'), 'Moon'), 120)
    selected = api.post(path + '/select', {'pin': '', 'installationId': api.installation,
                                          'trustToken': '', 'trust': False})
    restricted = fixture.API(api.base, selected['session']['accessToken'], api.installation)
    if has_title(restricted.get('/v1/search?q=Moon'), 'Moon'):
        raise RuntimeError('Restriction preflight failed: Moon remained visible to the restricted profile')
    fixture.wait(lambda: has_title(restricted.get('/v1/search?q=Arrival'), 'Arrival'), 120)
    print('Created an age-limited profile and a separate PIN-protected profile')


if __name__ == '__main__':
    main()
