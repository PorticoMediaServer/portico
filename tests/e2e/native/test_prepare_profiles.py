import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('prepare_profiles', Path(__file__).with_name('prepare-profiles.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class API:
    def __init__(self):
        self.calls = []
        self.token = None

    def get(self, path):
        if path == '/v1/dev/e2e':
            return {'username': 'owner'}
        self.calls.append(('GET', path))
        return {'revision': 7}

    def post(self, path, body, expected=None):
        if path.endswith('sign-in'):
            return {'session': {'accessToken': 'private'}}
        self.calls.append(('POST', path, body, expected))
        name = body['name']
        return {'profiles': [{'name': name, 'id': name, 'revision': 2}]}

    def call(self, method, path, body):
        self.calls.append((method, path, body))


class ProfileFixtureTest(unittest.TestCase):
    def test_search_query_echo_is_not_a_title(self):
        self.assertFalse(module.has_title({'query': 'Moon', 'items': []}, 'Moon'))
        self.assertTrue(module.has_title({'groups': [{'items': [{'title': 'Moon'}]}]}, 'Moon'))

    def test_separate_restricted_and_locked_profiles(self):
        api = API()
        module.prepare(api)
        restricted = next(call for call in api.calls if call[0] == 'PUT')
        self.assertEqual(restricted[1], '/v1/direct/profiles/Restricted E2E/restrictions')
        self.assertEqual(restricted[2]['expectedRevision'], 7)
        self.assertEqual(restricted[2]['maximumAgeRating'], 'PG-13')
        locked = next(call for call in api.calls if call[0] == 'PATCH')
        self.assertEqual(locked[1], '/v1/direct/profiles/Locked E2E')
        self.assertEqual(locked[2]['expectedRevision'], 2)
        self.assertRegex(locked[2]['pin'], r'^\d{4}$')
        self.assertNotIn('pin', restricted[2])


if __name__ == '__main__':
    unittest.main()
