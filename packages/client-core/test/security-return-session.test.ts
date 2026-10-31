import test from 'node:test';
import assert from 'node:assert/strict';
import type {LocalSession} from '../src/index.ts';
import {securityReturnDeadline, securityReturnSessionHolds} from '../src/security-return.ts';

// A Security return is bound to the sign-in it was opened under, never to one token: a routine
// refresh carries it on, anything that ends or replaces the sign-in ends it (docs/ENGINEERING-RULES.md).

const horizon = '2030-01-02T03:04:05Z';
function session(change: Partial<LocalSession> = {}, viewer: Partial<LocalSession['viewer']> = {}): LocalSession {
  return {accessToken: 'token_one', tokenGeneration: '1', sessionFamilyId: 'family_one', expiresAt: '2030-01-01T00:15:00Z', authorizationHorizon: horizon,
    deviceId: 'device_one', installationId: 'installation_one',
    viewer: {serverId: 'srv_one', authority: 'local', accountId: 'owner', profileId: 'profile', role: 'owner', ...viewer}, ...change};
}

test('a refresh in the same family carries the Security return on, with the refreshed token', () => {
  const held = session();
  assert.equal(securityReturnSessionHolds(held, session()), true);
  assert.equal(securityReturnSessionHolds(held, session({accessToken: 'token_two', tokenGeneration: '2', expiresAt: '2030-01-01T00:30:00Z'})), true);
  assert.equal(securityReturnSessionHolds(held, session({accessToken: 'token_three', tokenGeneration: '3', authorizationHorizon: '2030-01-02T03:04:05.000Z'})), true, 'the same horizon written another way');
});

test('another family, a re-sign-in after revocation, or an older token ends it', () => {
  const held = session();
  assert.equal(securityReturnSessionHolds(held, session({accessToken: 'token_two', tokenGeneration: '2', sessionFamilyId: 'family_two'})), false, 'another family');
  assert.equal(securityReturnSessionHolds(held, session({accessToken: 'token_new', tokenGeneration: '1', sessionFamilyId: 'family_after_revocation'})), false, 'a revoked family is replaced by a new one');
  assert.equal(securityReturnSessionHolds(session({accessToken: 'token_two', tokenGeneration: '2'}), held), false, 'an older token');
  assert.equal(securityReturnSessionHolds(held, session({accessToken: 'token_two', tokenGeneration: '1'})), false, 'the same generation with another token');
});

test('another server, account, profile, role, device or installation ends it', () => {
  const held = session(), refreshed = {accessToken: 'token_two', tokenGeneration: '2'};
  for (const [name, live] of Object.entries({
    server: session(refreshed, {serverId: 'srv_two'}), account: session(refreshed, {accountId: 'other'}), profile: session(refreshed, {profileId: 'kids'}),
    role: session(refreshed, {role: 'member'}), device: session({...refreshed, deviceId: 'device_two'}), installation: session({...refreshed, installationId: 'installation_two'}),
    horizon: session({...refreshed, authorizationHorizon: '2030-02-01T00:00:00Z'}),
  })) assert.equal(securityReturnSessionHolds(held, live), false, name);
});

test('the deadline is five minutes, the Portico Account session or the family horizon, never the server token', () => {
  const now = Date.parse('2030-01-01T00:00:00Z');
  assert.equal(securityReturnDeadline(now, '2030-01-01T01:00:00Z', horizon), now + 300_000);
  assert.equal(securityReturnDeadline(now, '2030-01-01T00:02:00Z', horizon), Date.parse('2030-01-01T00:02:00Z'), 'the account session ends first');
  assert.equal(securityReturnDeadline(now, '2030-01-01T01:00:00Z', '2030-01-01T00:03:00Z'), Date.parse('2030-01-01T00:03:00Z'), 'the family horizon ends first');
  assert.equal(securityReturnDeadline(now, '2030-01-01T01:00:00Z'), now + 300_000, 'a Portico Account return has no server family');
});
