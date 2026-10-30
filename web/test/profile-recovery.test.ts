import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowserProfileRecovery} from '../src/platform/profile-recovery.ts';
import {parseDirectSnapshot} from '@core/profile-management.ts';
import type {HttpLocalApi} from '@core/index.ts';

function snapshot(canManage = false, accountId = 'account') {
  return {authority: 'local', serverId: 'server', canManage,
    capabilities: {manageAccount: canManage, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: accountId, username: 'member', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Main', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: true},
      {id: 'alex', name: 'Alex', primary: false, position: 1, art: 'violet', revision: 1, pinRevision: 1, pinRequired: true}]};
}
function grant(accountId = 'account', family = 'temporary-family') {return {...snapshot(true, accountId), session: {accessToken: 'temporary-access', refreshToken: 'temporary-refresh', deviceId: 'device', installationId: 'installation', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', sessionFamilyId: family, tokenGeneration: '1', viewer: {authority: 'local', serverId: 'server', accountId, profileId: 'main', role: 'account'}}};}
function fixture(canManage = false) {
  const calls: {kind: string; path: string; body: unknown}[] = [];
  let answer: unknown = grant(), current = true, release: (() => void) | undefined;
  const source = {baseUrl: 'https://server.test', request: async (path: string, _method: string, body: unknown) => {calls.push({kind: 'selected', path, body}); if (release) await new Promise<void>(done => {release = done;}); return answer;}} as unknown as HttpLocalApi;
  const temporary = {request: async (path: string, _method: string, body: unknown) => {calls.push({kind: 'temporary', path, body});}} as unknown as HttpLocalApi;
  const flow = new BrowserProfileRecovery(source, parseDirectSnapshot(snapshot(canManage)), 'alex', 'local', 'selected-family', () => current, () => temporary);
  return {flow, calls, answer: (value: unknown) => {answer = value;}, cancelAuthority: () => {current = false;}, hold: () => {release = () => {};}, release: () => release?.()};
}
test('fresh password proves one profile reset, retires its temporary family and never installs it', async () => {
  const f = fixture(); await f.flow.password('private-password'); assert.equal(f.flow.getSnapshot().phase, 'pin');
  await f.flow.reset('2468'); assert.equal(f.flow.getSnapshot().phase, 'done');
  assert.deepEqual(f.calls.map(({kind, path}) => [kind, path]), [['selected', '/v1/direct/sign-in'], ['temporary', '/v1/direct/profiles/alex/pin-reset'], ['temporary', '/v1/sessions/current']]);
  assert.deepEqual(f.calls[1].body, {pin: '2468'}); f.flow.dispose();
});
test('an actual current management capability uses its captured account sign-in without minting a temporary family', async () => {
  const f = fixture(true); assert.equal(f.flow.getSnapshot().phase, 'pin'); await f.flow.reset('2468');
  assert.equal(f.calls.length, 1); assert.equal(f.calls[0].path, '/v1/direct/profiles/alex/pin-reset'); f.flow.dispose();
});
test('a sign-in for another account is retired and cannot reset this profile', async () => {
  const f = fixture(); f.answer(grant('other-account')); await f.flow.password('private-password');
  assert.equal(f.flow.getSnapshot().phase, 'password'); assert.ok(f.flow.getSnapshot().error);
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in', '/v1/sessions/current']); f.flow.dispose();
});
test('a sign-in completed after cancellation is retired without publishing a reset grant', async () => {
  const f = fixture(); f.hold(); const pending = f.flow.password('private-password');
  await Promise.resolve(); f.flow.dispose(); f.release(); await pending;
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in', '/v1/sessions/current']);
  assert.notEqual(f.flow.getSnapshot().phase, 'pin');
});
test('configured two-step verification completes before a new PIN can be set', async () => {
  const f = fixture(); f.answer({challenge: {token: 'challenge', expiresAt: '2099-01-01T00:00:00Z'}});
  await f.flow.password('private-password'); assert.equal(f.flow.getSnapshot().phase, 'challenge');
  await f.flow.reset('2468'); assert.equal(f.calls.length, 1);
  f.answer(grant()); await f.flow.code('123456'); assert.equal(f.flow.getSnapshot().phase, 'pin');
  assert.equal(f.calls[1].path, '/v1/auth/two-factor/challenge'); f.flow.dispose();
});
test('a proof naming the selected app family is refused and never retired', async () => {
  const f = fixture(); f.answer(grant('account', 'selected-family')); await f.flow.password('private-password');
  assert.equal(f.flow.getSnapshot().phase, 'password'); assert.ok(f.flow.getSnapshot().error);
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in']); f.flow.dispose();
});
test('a PIN step without a proof falls back to the proof step, never to the selected session', async () => {
  const f = fixture(); f.answer({challenge: {token: 'challenge', expiresAt: '2099-01-01T00:00:00Z'}});
  await f.flow.password('private-password'); f.answer(grant()); await f.flow.code('123456');
  assert.equal(f.flow.getSnapshot().phase, 'pin');
  f.cancelAuthority(); await f.flow.reset('2468');
  assert.equal(f.calls.filter(value => value.path.endsWith('/pin-reset')).length, 0, 'a stale authority resets nothing');
  f.flow.dispose();
});
