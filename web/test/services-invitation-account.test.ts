import assert from 'node:assert/strict';
import {test} from 'node:test';
import {servicesInvitationAccount} from '../src/services/invitation-account.ts';
import type {ServicesAccount} from '../src/services/session.ts';

const first = {account: {id: 'actual-account', username: 'harbourview', displayName: 'Alex'}, familyId: 'first-family', accessToken: 'first-access', expiresAt: '2030-01-01T00:00:00Z'};
function fixture() {
  let session = first;
  const calls: unknown[][] = [];
  let send = async () => ({accepted: true});
  const account = {
    api: {origin: 'https://accounts.example', send: async (...args: unknown[]) => { calls.push(args); return send(); }},
    service: {synchronize: async () => {}, accessSession: async () => session, getSnapshot: () => ({session})},
  } as unknown as ServicesAccount;
  return {account, calls, replace: () => { session = {...first, familyId: 'replacement-family', accessToken: 'replacement-access'}; }, send: (work: typeof send) => { send = work; }};
}
test('invitation reads use the current canonical bearer while retaining their captured account and family', async () => {
  const f = fixture(), value = servicesInvitationAccount(f.account, first);
  assert.equal(value.origin, 'https://accounts.example'); assert.equal(value.username, 'harbourview'); assert.equal(value.isCurrent(), true);
  const controller = new AbortController();
  assert.deepEqual(await value.request('/v1/servers', 'GET', undefined, controller.signal), {accepted: true});
  assert.deepEqual(f.calls[0], ['/v1/servers', 'GET', undefined, controller.signal, 'first-access']);
  f.replace(); assert.equal(value.isCurrent(), false);
  await assert.rejects(value.request('/v1/servers'), {code: 'account_changed'});
  assert.equal(f.calls.length, 1, 'a replaced family must not issue the invitation request');
});
test('a family replacement while an invitation request is in flight discards its result', async () => {
  const f = fixture(), value = servicesInvitationAccount(f.account, first);
  f.send(async () => { f.replace(); return {accepted: true}; });
  await assert.rejects(value.request('/v1/servers'), {code: 'account_changed'});
  assert.equal(value.isCurrent(), false);
});
