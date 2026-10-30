import test from 'node:test';
import assert from 'node:assert/strict';
import {TabProfileTrust} from '../src/platform/tab-profile-trust.ts';
import type {TrustedProfile} from '@core/profile-management.ts';
const proof = (change: Partial<TrustedProfile> = {}): TrustedProfile => ({token: 'private-pin-proof', authority: 'local', accountId: 'account-a', profileId: 'profile-a', serverId: 'server-a', installationId: 'installation-a', pinRevision: 1, profileRevision: 1, membershipRevision: 1, expiresAt: new Date(Date.now() + 3600000).toISOString(), ...change});
test('remembered PIN proof stays in this tab and exact server/account/profile/installation scope', () => {
  const first = new TabProfileTrust(), second = new TabProfileTrust(), value = proof(); first.save(value);
  assert.equal(first.read(value)?.token, value.token); assert.equal(second.read(value), undefined);
  for (const change of [{serverId: 'server-b'}, {accountId: 'account-b'}, {profileId: 'profile-b'}, {installationId: 'installation-b'}]) assert.equal(first.read({...value, ...change}), undefined);
});
test('expiry retires a PIN proof; malformed proof never becomes remembered authority', () => {
  const store = new TabProfileTrust(), value = proof(); store.save(value);
  assert.equal(store.read(value, Date.parse(value.expiresAt)), undefined);
  assert.throws(() => store.save(proof({membershipRevision: 0})));
  assert.equal(store.read(value), undefined);
});
test('account sign-out clears captured account proofs without touching another account or server', () => {
  const store = new TabProfileTrust(), a = proof(), b = proof({serverId: 'server-b'}), c = proof({accountId: 'account-b'});
  for (const value of [a, b, c]) store.save(value);
  store.forgetAccount(a.serverId, a.accountId);
  assert.equal(store.read(a), undefined); assert.ok(store.read(b)); assert.ok(store.read(c));
});
