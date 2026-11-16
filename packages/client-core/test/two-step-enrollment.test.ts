import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLocalSession} from '../src/local-session.ts';
import {parseDirectSignIn, openOnlyProfile, directSignInContinuation} from '../src/profile-management.ts';

const answer = (required = true) => ({
  session: {accessToken: 'token', expiresAt: '2099-01-01T00:00:00Z', sessionId: 'sid', sessionExpiresAt: '2099-04-01T00:00:00Z', tokenGeneration: '1',
    viewer: {accountId: 'a', profileId: '', serverId: 's', role: 'account'}, twoStepSetupRequired: required},
  account: {id: 'a', username: 'person', role: 'member'}, profiles: [{id: 'p', name: 'Person', isPrimary: true, color: 'blue', pinRequired: false}],
});
test('required two-step enrollment survives session storage and precedes automatic profile selection', async () => {
  const raw = answer(), calls: string[] = [];
  const parsed = parseDirectSignIn(raw);
  assert.equal(parseLocalSession(raw.session).twoStepSetupRequired, true);
  assert.equal(parseLocalSession(JSON.parse(JSON.stringify(parseLocalSession(raw.session))), {}, {stored: true}).twoStepSetupRequired, true);
  assert.equal(parsed.kind, 'signed-in');
  if (parsed.kind !== 'signed-in') throw new Error('expected sign-in');
  assert.equal(directSignInContinuation(parsed), 'two-step-setup');
  const scoped = {request: async <T>(path: string) => {calls.push(path); throw new Error('must not select a profile');}};
  assert.equal(await openOnlyProfile({...scoped, withAccessToken: () => scoped}, parsed), parsed);
  assert.deepEqual(calls, []);
  const ordinary = parseDirectSignIn(answer(false));
  assert.notEqual(directSignInContinuation(ordinary), 'two-step-setup');
});
