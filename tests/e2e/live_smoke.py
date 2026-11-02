"""Verify the disposable Live TV channel is visible to the test owner."""
from datetime import datetime, timedelta, timezone
import importlib.util
import json
import os
from pathlib import Path
import urllib.parse

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('fixture', ROOT / 'scripts/e2e-server.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)

api = fixture.API(os.environ['PORTICO_E2E_SERVER_URL'])
username = api.get('/v1/dev/e2e')['username']
api.token = api.post('/v1/dev/e2e/sign-in', {'username': username})['session']['accessToken']
now = datetime.now(timezone.utc)
params = urllib.parse.urlencode({
    'kind': 'live-source', 'start': (now - timedelta(minutes=30)).isoformat(),
    'end': (now + timedelta(minutes=90)).isoformat(), 'timezone': 'UTC', 'limit': '30',
})
guide = json.dumps(api.get('/v1/guide?' + params))
if 'Synthetic TV' not in guide or 'Synthetic programme' not in guide:
    raise RuntimeError('synthetic Live TV channel or schedule is missing from the guide')
print('PASS: synthetic Live TV channel and guide are visible')
