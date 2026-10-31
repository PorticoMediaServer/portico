import test from 'node:test';
import assert from 'node:assert/strict';
import type {LocalSession} from '../../../packages/client-core/src/index.ts';
import {paintAuthorityHolds} from '../src-next/platform/playback/paint-authority.ts';

const held: LocalSession = {accessToken: 'token-1', tokenGeneration: '1', sessionFamilyId: 'family', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-06-01T00:00:00Z', deviceId: 'device', installationId: 'installation',
  viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'owner'}};

test('passive start and exit pixels survive a routine refresh of the same sign-in', () => {
  assert.equal(paintAuthorityHolds(held, held), true);
  assert.equal(paintAuthorityHolds(held, {...held, accessToken: 'token-2', tokenGeneration: '2', expiresAt: '2099-02-01T00:00:00Z'}), true);
});

test('another sign-in, an older token, a role change or another device ends them', () => {
  const live = (change: Partial<LocalSession>, viewer: Partial<LocalSession['viewer']> = {}): LocalSession => ({...held, accessToken: 'token-2', tokenGeneration: '2', ...change, viewer: {...held.viewer, ...viewer}});
  for (const [name, session] of [
    ['other family', live({sessionFamilyId: 'family-2'})],
    ['other profile', live({}, {profileId: 'kids'})],
    ['other account', live({}, {accountId: 'other'})],
    ['other server', live({}, {serverId: 'server-2'})],
    ['role change', live({}, {role: 'member'})],
    ['other device', live({deviceId: 'device-2'})],
    ['other installation', live({installationId: 'installation-2'})],
    ['older token', {...held, accessToken: 'token-0', tokenGeneration: '0'}],
    ['same generation, other token', {...held, accessToken: 'token-x'}],
  ] as const) assert.equal(paintAuthorityHolds(held, session), false, name);
  assert.equal(paintAuthorityHolds(held, undefined), false);
  assert.equal(paintAuthorityHolds(undefined, held), false);
});
