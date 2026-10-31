import test from 'node:test';
import assert from 'node:assert/strict';
import {ShellBootstrap, followsStoredSignIn, type ShellAssembler} from '../src/shell/bootstrap.ts';
import type {SelectedViewerRecord} from '@core/viewer-selection.ts';

class TabStorage {values = new Map<string, string>(); getItem(k: string) {return this.values.get(k) ?? null;} setItem(k: string, v: string) {this.values.set(k, v);} removeItem(k: string) {this.values.delete(k);}}
const tab = new TabStorage();
(globalThis as {sessionStorage?: unknown}).sessionStorage = tab;
const gate = await import('../src/platform/tab-session-gate.ts');

const session = {accessToken: 'access', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', sessionFamilyId: 'family-a', tokenGeneration: '1',
  viewer: {serverId: 'server-a', accountId: 'account-a', profileId: 'alex', authority: 'local', role: 'member'}};
const saved = {version: 1, serverUrl: 'https://demo.example:32500', session} as unknown as SelectedViewerRecord;
const profile = (id: string, pinRequired: boolean, primary = false) => ({id, name: id, primary, position: primary ? 0 : 1, art: 'blue', revision: 1, pinRevision: 1, pinRequired, allowedLibraries: []});
const direct = (profiles: Array<{id: string; primary: boolean}>) => ({authority: 'local', serverId: 'server-a', rememberProfiles: true, canManage: false,
  capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
  account: {id: 'account-a', username: 'alex', primaryProfileId: profiles.find(value => value.primary)!.id, role: 'member', revision: 1, disabled: false, allowedLibraries: []}, profiles});
const assembler: ShellAssembler = async () => {throw new Error('assembled');};

function launch(answer: () => unknown, held = session) {
  const requests: string[] = [];
  let disposed = 0;
  const api = {baseUrl: 'https://demo.example:32500', getRouteConnection: () => ({dispose: () => {disposed++;}}),
    request: async (path: string) => {requests.push(path); const value = answer(); if (value instanceof Error) throw value; return value;}};
  const bootstrap = new ShellBootstrap(assembler, {selected: {read: async () => ({...saved, session: held})}, restore: async () => ({api, session: held}) as never});
  return {bootstrap, requests, disposed: () => disposed};
}

test('not remembered in this tab, with several profiles: Who\'s watching first, and the route is released', async () => {
  gate.resetTabSessionGate(); tab.values.clear();
  const l = launch(() => direct([profile('main', false, true), profile('alex', true)]));
  await l.bootstrap.start();
  assert.equal(l.bootstrap.getSnapshot().phase, 'choose-profile');
  assert.deepEqual(l.requests, ['/v1/direct']);
  assert.equal(l.disposed(), 1);
  l.bootstrap.dispose();
});

test('the account\'s only profile with no PIN opens without asking', async () => {
  gate.resetTabSessionGate(); tab.values.clear();
  const l = launch(() => direct([profile('alex', false, true)]));
  await l.bootstrap.start();
  assert.notEqual(l.bootstrap.getSnapshot().phase, 'choose-profile');
  assert.equal(gate.tabAdmits({serverId: 'server-a', familyId: 'family-z', profileId: 'alex'}), true, 'the page load now follows this server');
  l.bootstrap.dispose();
});

test('remembered in this tab: opens with no account read for the gate', async () => {
  gate.resetTabSessionGate(); tab.values.clear();
  gate.recordTabPick('server-a', {serverId: 'server-a', familyId: 'family-a', profileId: 'alex', expiresAt: '2099-01-01T00:00:00Z'});
  gate.resetTabSessionGate();
  const l = launch(() => direct([profile('main', false, true), profile('alex', true)]));
  await l.bootstrap.start();
  assert.notEqual(l.bootstrap.getSnapshot().phase, 'choose-profile');
  assert.equal(l.requests.filter(path => path === '/v1/direct').length <= 1, true);
  l.bootstrap.dispose();
});

test('a PIN changed elsewhere (profile_pin_reentry) asks for it again and stops remembering in this tab', async () => {
  gate.resetTabSessionGate(); tab.values.clear();
  // Remembered for an older sign-in only, so the gate reads the account, and that read is refused.
  gate.recordTabPick('server-a', {serverId: 'server-a', familyId: 'family-old', profileId: 'alex', expiresAt: '2099-01-01T00:00:00Z'});
  gate.resetTabSessionGate();
  const l = launch(() => Object.assign(new Error('gated'), {code: 'profile_pin_reentry', status: 403}));
  await l.bootstrap.start();
  assert.equal(l.bootstrap.getSnapshot().phase, 'choose-profile');
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits({serverId: 'server-a', familyId: 'family-old', profileId: 'alex'}), false, 'the old remembering is gone');
  l.bootstrap.dispose();
});

test('another tab choosing a profile never signs this tab out: it restarts onto the new sign-in without asking', async () => {
  gate.resetTabSessionGate(); tab.values.clear();
  assert.equal(followsStoredSignIn({code: 'session_replaced'}), true, 'a different saved sign-in restarts this tab');
  assert.equal(followsStoredSignIn({code: 'refresh_revoked'}), false, 'a refusal still signs out');
  assert.equal(followsStoredSignIn({code: 'saved_sign_in_damaged'}), false);
  const several = () => direct([profile('main', false, true), profile('alex', true)]);
  // This page load opened server-a (its only profile then), so it passed the gate.
  const first = launch(() => direct([profile('alex', false, true)]));
  await first.bootstrap.start();
  first.bootstrap.dispose();
  // Tab 2 then picked "main" (a new family); this tab's restart reads it from the shared store.
  const next = launch(several, {...session, sessionFamilyId: 'family-b', viewer: {...session.viewer, profileId: 'main'}});
  await next.bootstrap.start();
  assert.notEqual(next.bootstrap.getSnapshot().phase, 'choose-profile');
  assert.notEqual(next.bootstrap.getSnapshot().phase, 'signed-out');
  assert.deepEqual(next.requests.filter(path => path === '/v1/direct'), [], 'no gate read: the page load is already admitted');
  next.bootstrap.dispose();
  // On its next reload it is no longer admitted, and nothing was remembered here: Who's watching.
  gate.resetTabSessionGate();
  const reload = launch(several, {...session, sessionFamilyId: 'family-b', viewer: {...session.viewer, profileId: 'main'}});
  await reload.bootstrap.start();
  assert.equal(reload.bootstrap.getSnapshot().phase, 'choose-profile');
  reload.bootstrap.dispose();
});
