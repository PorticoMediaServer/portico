import test from 'node:test';
import assert from 'node:assert/strict';
import {parseConnectivityStatus} from '../src/server-administration.ts';
import {schemas} from '../../contracts/src/schemas.ts';

const status = {
  observedAt: '2026-10-24T00:00:00Z',
  policy: {revision: 1, remoteSignInPolicy: 'owner-only', remoteBitrateLimitKbps: 0,
    connectionLimit: 0, advertiseUrl: '', lanNetworks: [], accessUrls: [], lanDiscoveryEnabled: false},
};

test('connectivity retains only the identity and listener facts actually reported', () => {
  const absent = parseConnectivityStatus(status);
  assert.equal('identityCode' in absent, false);
  assert.equal('localUrl' in absent, false);
  const facts = parseConnectivityStatus({...status, identityCode: 'actual-code', localUrl: 'http://127.0.0.1:32500'});
  assert.equal(facts.identityCode, 'actual-code');
  assert.equal(facts.localUrl, 'http://127.0.0.1:32500');
  assert.equal(Object.isFrozen(facts), true);
  for (const field of ['identityCode', 'localUrl']) {
    for (const value of [null, 7, false, '', 'bad\nvalue']) {
      assert.throws(() => parseConnectivityStatus({...status, [field]: value}));
    }
  }
});

test('canonical connectivity facts are optional nonnull strings', () => {
  const shape = schemas.GetConnectivityStatusResponse;
  for (const field of ['identityCode', 'localUrl'] as const) {
    assert.equal(shape.properties[field].type, 'string');
    assert.equal(new Set<string>(shape.required).has(field), false);
    assert.equal('nullable' in shape.properties[field], false);
  }
});
