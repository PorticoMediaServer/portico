import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';

// R-12 / E-mobile-01 ruling: once a pick is published, ending the replaced family never blocks or
// fails the pick. It is queued durably and tried once, bounded, in the background.

async function pickWith(outcome: 'hang' | 'reject' | 'ok') {
  const queued: {record: any; live: unknown}[] = [];
  let logouts = 0, disposed = 0;
  const route = {pin: {serverId: 'srv'}, dispose: () => {disposed++;}};
  class FakeApi {
    baseUrl = 'https://srv.test'; constructor(_o?: string, _t?: string) {}
    useRoutes() {} setAccessToken() {} getRouteConnection() {return route;}
    async request() {return {};}
    async logout() {logouts++; if (outcome === 'hang') return new Promise(() => {}); if (outcome === 'reject') throw new Error('offline');}
  }
  const old = {sessionFamilyId: 'old', accessToken: 'tok-old', viewer: {accountId: 'a', profileId: 'p0', role: 'member'}};
  const record = {pin: {serverId: 'srv'}, session: old, logicalOrigin: 'https://srv.test'};
  const issued = {sessionFamilyId: 'new', accessToken: 'tok-new', viewer: {accountId: 'a', profileId: 'p1', role: 'member'}};
  const installation = {installationId: 'i'};
  const module = await screenModule(new URL('../src-next/platform/profile-selection.ts', import.meta.url), {
    react: {default: {}},
    '../../../../packages/client-core/src': {HttpLocalApi: FakeApi},
    '../../../../packages/client-core/src/installation': {installationIdentity: () => installation},
    '../../../../packages/client-core/src/profile-management': {parseDirectSnapshot: () => ({account: {}, profiles: [{id: 'p1'}]}), selectDirectProfile: async () => ({session: issued})},
    '../../../../packages/contracts/src/routes': {routePath: (name: string) => name},
    '../../../../packages/client-core/src/server-connections': {restoreNativeSession: async () => ({api: new FakeApi(), session: old})},
    './app-session-storage': {nativeAppSessions: {selected: async () => ({connection: record, signIn: 'local'})}},
    './accounts': {nativeAccounts: {onCancel: () => () => {}, admission: async () => ({}), remember: async () => {}}},
    './server-connections': {nativeNetworkFetch: {}, nativeServerEnvironment: () => ({})},
    './session-revocation': {revokeInBackground: (rec: unknown, live: unknown) => {queued.push({record: rec, live}); return Promise.resolve();}},
    '../shell/account-binding': {createNativeShellAccountBinding: () => ({assertCurrent: async () => {}, assertAccount: () => {}, assertSameAccount: () => {}}), profileSessionRole: () => 'member'},
    './pick-failure': {logPickFailure() {}, provenIdentityMismatch: () => false},
  }) as typeof import('../src-next/platform/profile-selection.ts');
  const selection = module.nativeProfileSelection('srv', 'old');
  const result = await Promise.race([selection.select('p1').then(() => 'resolved', () => 'rejected'), new Promise(r => setTimeout(() => r('blocked'), 300))]);
  return {result, queued, logouts, disposed};
}

for (const outcome of ['hang', 'reject', 'ok'] as const) {
  test(`a published pick resolves even when the previous session's logout would ${outcome}`, async () => {
    const {result, queued, logouts} = await pickWith(outcome);
    assert.equal(result, 'resolved');
    assert.equal(logouts, 0, 'no unbounded direct logout on the path to Home');
    assert.equal(queued.length, 1, 'the replaced family goes through the durable revocation queue');
    assert.equal(queued[0]!.record.session.sessionFamilyId, 'old');
  });
}
