import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

async function fixture() {
  let active = true;
  let session = {account: {id: 'account'}, familyId: 'family', accessToken: 'fixture-token'};
  const listeners = new Set<() => void>(), calls: unknown[][] = [];
  const ownerRow = {id: 'owned', name: 'Owned server', ownedByMe: true, role: 'owner'};
  const memberRow = {id: 'joined', name: 'Joined server', ownedByMe: false, role: 'member', presence: {online: false, lastSeenAt: '2026-01-01T00:00:00Z'}};
  const sessionRow = {id: 'other-family', status: 'active', current: false, mode: 'native', createdAt: '2026-01-01T00:00:00Z', expiresAt: '2027-01-01T00:00:00Z'};
  let sessions: any = {phase: 'ready', items: [sessionRow], pending: null, retryId: null, nextCursor: '', hasPrevious: false};
  let revokeState: any, othersState: any, receipt: unknown = {};
  const credentials = {getSnapshot: () => ({session}), subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}};
  const guard = async (_: unknown, work: (value: typeof session) => Promise<unknown>, opening: any) => {
    if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired');
    const result = await work(session); if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired'); return result;
  };
  const {NativeAccountWorkspace} = await screenModule(new URL('../src-next/platform/account-workspace.ts', import.meta.url), {
    react: {default: {}},
    '../../../../packages/client-core/src': {parseServerPresence: (value: unknown) => value,
      HttpLocalApi: class {async request(path: string, method: string) {calls.push(['http', path, method]); if (path === 'servers_list') return {items: [ownerRow, memberRow]}; return method === 'DELETE' ? receipt : {};}}},
    '../../../../packages/client-core/src/account-sessions': {AccountSessionReviewService: class {
      api: any; constructor(input: any) {this.api = input.api;}
      async refresh() {await this.api.request('sessions', 'GET');}
      getSnapshot = () => sessions;
      async revoke(id: string) {calls.push(['revoke', id]); await this.api.request(`session/${id}`, 'DELETE'); sessions = revokeState ?? {...sessions, items: [], retryId: null};}
      async retryRevoke() {
        if (sessions.retryOthers) {calls.push(['retry-others']); await this.api.request('sessions/revoke-others', 'POST', {operationId: 'kept-operation'}); sessions = othersState ?? {...sessions, retryOthers: false}; return;}
        calls.push(['retry', sessions.retryId]); await this.api.request(`session/${sessions.retryId}`, 'DELETE'); sessions = {...sessions, items: [], retryId: null};}
      async revokeOthers() {calls.push(['others']); await this.api.request('sessions/revoke-others', 'POST', {operationId: 'new-operation'}); sessions = othersState ?? {...sessions, notice: 'Signed out of 1 other device.'};}
      dispose() {}
    }},
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
  return {service, calls, sessions: (value: any) => {sessions = value;},
    revoked: (state: any, value: unknown = {}) => {revokeState = state; receipt = value;}, othersEnded: (state: any) => {othersState = state;},
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

test('workspace revokes a captured session through command work and refuses stale or replaced accounts', async () => {
  const f = await fixture(); await f.service.open('devices');
  const row = f.service.getSnapshot().sessions[0];
  await assert.rejects(f.service.revokeSession({...row}), /changed/);
  await f.service.revokeSession(row);
  assert.deepEqual(f.calls.find(value => value[0] === 'revoke'), ['revoke', 'other-family']);
  assert.ok(f.calls.some(value => value[0] === 'http' && value[1] === 'session/other-family' && value[2] === 'DELETE'));
  assert.equal(f.calls.filter(value => value[0] === 'command').length, 1);
  f.replace(); assert.equal(f.service.getSnapshot().phase, 'ended');
  await assert.rejects(f.service.revokeSession(row), /changed/);
  f.service.dispose();
});

test('an ambiguous revocation retries its original family even if that row left the current page', async () => {
  const f = await fixture(); f.sessions({phase: 'ready', items: [], pending: null, retryId: 'original-family', nextCursor: '', hasPrevious: false});
  await f.service.open('devices');
  await assert.rejects(f.service.revokeSession('another-family'), /changed/);
  await f.service.revokeSession('original-family');
  assert.deepEqual(f.calls.find(value => value[0] === 'retry'), ['retry', 'original-family']);
  f.service.dispose();
});

test('an ended session-review result without a confirmed current-family receipt does not log out a renewed family', async () => {
  const f = await fixture(); await f.service.open('devices');
  f.revoked({phase: 'ended', items: [], pending: null, retryId: null});
  await f.service.revokeSession(f.service.getSnapshot().sessions[0]);
  assert.equal(f.calls.filter(value => value[0] === 'sign-out').length, 0);
  assert.equal(f.service.current(), true);
  assert.match(f.service.getSnapshot().mutationFailure, /outcome_unconfirmed/);
  assert.equal(f.calls.filter(value => value[0] === 'revoke').length, 1, 'the refused command is not replayed');
  f.service.dispose();
});

test('a confirmed current-family revocation retires exactly that signed-in account', async () => {
  const f = await fixture();
  f.sessions({phase: 'ready', items: [{id: 'family', current: true, status: 'active'}], pending: null, retryId: null});
  await f.service.open('devices');
  f.revoked({phase: 'ended', items: [], pending: null, retryId: null}, {id: 'family', revoked: true, current: true});
  await f.service.revokeSession(f.service.getSnapshot().sessions[0]);
  assert.equal(f.calls.filter(value => value[0] === 'sign-out').length, 1);
  f.service.dispose();
});

test('Sign out all other devices is one command that keeps this device signed in', async () => {
  const f = await fixture(); await f.service.open('devices');
  await f.service.revokeOtherSessions();
  assert.deepEqual(f.calls.filter(value => value[0] === 'others'), [['others']]);
  assert.ok(f.calls.some(value => value[0] === 'http' && value[1] === 'sessions/revoke-others' && value[2] === 'POST'));
  assert.equal(f.calls.filter(value => value[0] === 'command').length, 1);
  assert.equal(f.calls.filter(value => value[0] === 'revoke' || value[0] === 'sign-out').length, 0, 'never one device at a time, never this device');
  assert.equal(f.service.getSnapshot().notice, 'Signed out of 1 other device.');
  assert.ok(!f.service.getSnapshot().endingOthers);
  f.service.dispose();
});

test('an unconfirmed Sign out all other devices is only ever retried as that same operation', async () => {
  const f = await fixture(); f.sessions({phase: 'ready', items: [], pending: null, retryId: null, retryOthers: true, nextCursor: '', hasPrevious: false});
  await f.service.open('devices');
  assert.equal(f.service.getSnapshot().retryOthers, true);
  await f.service.revokeOtherSessions();
  assert.deepEqual(f.calls.filter(value => value[0] === 'others' || value[0] === 'retry-others'), [['retry-others']]);
  f.service.dispose();
});

test('an ended result from Sign out all other devices never signs this device out', async () => {
  const f = await fixture(); await f.service.open('devices');
  f.othersEnded({phase: 'ended', items: [], pending: null, retryId: null});
  await f.service.revokeOtherSessions();
  assert.equal(f.calls.filter(value => value[0] === 'sign-out').length, 0);
  assert.equal(f.service.current(), true);
  assert.match(f.service.getSnapshot().mutationFailure, /outcome_unconfirmed/);
  assert.equal(f.calls.filter(value => value[0] === 'others').length, 1, 'the command is not replayed');
  f.service.dispose();
});

test('Sign out all other devices is refused for a replaced account or outside the Devices page', async () => {
  const f = await fixture(); await f.service.open('servers');
  await assert.rejects(f.service.revokeOtherSessions(), /changed/);
  await f.service.open('devices'); f.replace();
  await assert.rejects(f.service.revokeOtherSessions(), /changed/);
  assert.equal(f.calls.filter(value => value[0] === 'others').length, 0);
  f.service.dispose();
});
