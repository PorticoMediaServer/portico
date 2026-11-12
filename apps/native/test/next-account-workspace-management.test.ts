import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

async function fixture() {
  let active = true;
  let session = {account: {id: 'account'}, familyId: 'family', accessToken: 'fixture-token'};
  const listeners = new Set<() => void>(), calls: unknown[][] = [];
  const ownerRow = {id: 'owned', name: 'Owned server', ownedByMe: true, role: 'owner'};
  const memberRow = {id: 'joined', name: 'Joined server', ownedByMe: false, role: 'member', presence: {online: false, lastSeenAt: '2026-01-01T00:00:00Z'}};
  // Portico Services' sign-ins of the account, as `porticoAccountSessions` gives them.
  const row = (id: string, current = false) => ({id, current, mode: 'native', status: 'active', createdAt: '2026-01-01T00:00:00Z', lastUsedAt: null, deviceName: null, platform: null, app: null});
  let listed: any[] = [row('this-sign-in', true), row('other-sign-in')], refusal: unknown, lost = false;
  const credentials = {getSnapshot: () => ({session}), subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}};
  const guard = async (_: unknown, work: (value: typeof session) => Promise<unknown>, opening: any) => {
    if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired');
    const result = await work(session); if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired'); return result;
  };
  const {NativeAccountWorkspace} = await screenModule(new URL('../src-next/platform/account-workspace.ts', import.meta.url), {
    react: {default: {}},
    '../../../../packages/client-core/src': {parseServerPresence: (value: unknown) => value,
      HttpLocalApi: class {async request(path: string, method: string) {calls.push(['http', path, method]); if (path === 'servers_list') return {items: [ownerRow, memberRow]}; return {};}}},
    '../../../../packages/client-core/src/portico-services-native': {porticoAccountSessions: (token: string, origin: string) => ({
      list: async () => {calls.push(['list', token, origin]); return listed;},
      end: async (id: string) => {calls.push(['end', id]); if (!refusal || lost) listed = listed.filter(value => value.id !== id); if (refusal) throw refusal;},
      endOthers: async () => {calls.push(['end-others']); if (!refusal || lost) listed = listed.filter(value => value.current); if (refusal) throw refusal;},
    })},
    '../../../../packages/client-core/src/account-notifications': {AccountNotificationsClient: class {}},
    '../../../../packages/client-core/src/hosted-security': {parseSecurityState: (value: unknown) => value},
    '../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({body: error.message})},
    '../../../../packages/contracts/src/hosted/routes': {routePath: (id: string) => id},
    '../../../../packages/client-core/src/portico-servers': {leavePorticoServer: async (api: any, origin: string, id: string, signal: AbortSignal) => {
      calls.push(['leave', origin, id]); await api.request(`server/${id}/leave`, 'POST', {}, signal);
    }},
    './account-authority': {hostedUrl: 'https://accounts.example.invalid'}, './server-connections': {nativeNetworkFetch: () => {throw new Error('unexpected transport');}},
    './account-work': {accountExpectation: (value: typeof session) => ({accountId: value.account.id, familyId: value.familyId}),
      nativeAccountRead: (...args: Parameters<typeof guard>) => {calls.push(['read']); return guard(...args);},
      nativeAccountWork: (...args: Parameters<typeof guard>) => {calls.push(['command']); return guard(...args);}},
    './account-security': {NativeAccountSecurity: class {dispose() {}}},
  });
  const service = new NativeAccountWorkspace({accountCredentials: () => credentials, signOutPorticoAccount: async () => {calls.push(['sign-out']);}}, {accountId: 'account', familyId: 'family'}, () => active);
  return {service, calls, refuse: (value: unknown, answerLost = false) => {refusal = value; lost = answerLost;},
    replace: () => {session = {...session, familyId: 'replacement'}; listeners.forEach(listener => listener());}, retire: () => {active = false;}};
}

test('workspace Servers stays in the workspace and admits Leave only for a current non-owner row', async () => {
  const f = await fixture(); await f.service.open('servers');
  const state = f.service.getSnapshot(); assert.equal(state.page, 'servers'); assert.equal(state.phase, 'ready');
  assert.equal(state.servers[1].presence.lastSeenAt, '2026-01-01T00:00:00Z');
  await assert.rejects(f.service.leaveServer(state.servers[0]), /changed/);
  await assert.rejects(f.service.leaveServer({...state.servers[1]}), /changed/, 'a copied/stale row is not an opening witness');
  assert.equal(f.calls.filter(value => value[0] === 'leave').length, 0);
  await f.service.leaveServer(state.servers[1]);
  assert.deepEqual(f.calls.find(value => value[0] === 'leave'), ['leave', 'https://accounts.example.invalid', 'joined']);
  assert.deepEqual(f.service.getSnapshot().servers.map((row: any) => row.id), ['owned']);
  assert.equal(f.calls.filter(value => value[0] === 'command').length, 1);
  await assert.rejects(f.service.leaveServer(state.servers[1]), /changed/);
  f.service.dispose();
});

test('Devices lists the account\'s sign-ins from Portico Services, and signs one other device out through command work', async () => {
  const f = await fixture(); await f.service.open('devices');
  assert.deepEqual(f.calls.slice(0, 2), [['read'], ['list', 'fixture-token', 'https://accounts.example.invalid']]);
  const [here, other] = f.service.getSnapshot().sessions;
  await assert.rejects(f.service.revokeSession({...other}), /changed/, 'a copied row is not one this page drew');
  await assert.rejects(f.service.revokeSession(here), /changed/, 'this device signs out from the menu, not from its row');
  await f.service.revokeSession(other);
  assert.deepEqual(f.calls.filter(value => value[0] === 'end'), [['end', 'other-sign-in']]);
  assert.equal(f.calls.filter(value => value[0] === 'command').length, 1);
  assert.deepEqual(f.service.getSnapshot().sessions.map((value: any) => value.id), ['this-sign-in'], 'the list is read again');
  assert.ok(!f.service.getSnapshot().pending);
  f.replace(); assert.equal(f.service.getSnapshot().phase, 'ended');
  await assert.rejects(f.service.revokeSession(here), /changed/);
  f.service.dispose();
});

test('a sign-out whose answer was lost is settled by the list: no failure when the device is gone, the failure when it is still there', async () => {
  const lost = await fixture(); await lost.service.open('devices');
  lost.refuse(new Error('network failed'), true);
  await lost.service.revokeSession(lost.service.getSnapshot().sessions[1]);
  assert.equal(lost.service.getSnapshot().mutationFailure, undefined);
  assert.equal(lost.calls.filter(value => value[0] === 'end').length, 1, 'never sent twice');
  lost.service.dispose();
  const refused = await fixture(); await refused.service.open('devices');
  refused.refuse(new Error('Services is busy'));
  await refused.service.revokeSession(refused.service.getSnapshot().sessions[1]);
  assert.equal(refused.service.getSnapshot().mutationFailure, 'Services is busy');
  assert.equal(refused.service.getSnapshot().sessions.length, 2);
  assert.equal(refused.calls.filter(value => value[0] === 'sign-out').length, 0, 'this device stays signed in');
  refused.service.dispose();
});

test('Sign out all other devices is one command that keeps this device signed in', async () => {
  const f = await fixture(); await f.service.open('devices');
  await f.service.revokeOtherSessions();
  assert.deepEqual(f.calls.filter(value => value[0] === 'end-others'), [['end-others']]);
  assert.equal(f.calls.filter(value => value[0] === 'command').length, 1);
  assert.equal(f.calls.filter(value => value[0] === 'end' || value[0] === 'sign-out').length, 0, 'never one device at a time, never this device');
  assert.deepEqual(f.service.getSnapshot().sessions.map((value: any) => value.id), ['this-sign-in']);
  assert.ok(!f.service.getSnapshot().endingOthers);
  f.refuse(new Error('Services is busy')); await f.service.revokeOtherSessions();
  assert.equal(f.service.getSnapshot().mutationFailure, undefined, 'nothing is left to sign out, so nothing failed');
  f.service.dispose();
});

test('Sign out all other devices is refused for a replaced account or outside the Devices page', async () => {
  const f = await fixture(); await f.service.open('servers');
  await assert.rejects(f.service.revokeOtherSessions(), /changed/);
  await f.service.open('devices'); f.replace();
  await assert.rejects(f.service.revokeOtherSessions(), /changed/);
  assert.equal(f.calls.filter(value => value[0] === 'end-others').length, 0);
  f.service.dispose();
});
