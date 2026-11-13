import assert from 'node:assert/strict';
import test from 'node:test';
import {AppSessionStore, type AppServerSession, type AppSessionRecord} from '../../../packages/client-core/src/app-sessions.ts';
import {CredentialSessionService, type CredentialRecord} from '../../../packages/client-core/src/credentials.ts';
import {screenModule} from './helpers/screen-harness.mjs';

test('account sign-out finishes durable local cleanup before an unreachable server answers', async () => {
  const hosted = {origin: 'https://accounts.test', accountId: 'account', key: {keyId: 'key', publicKey: 'k'.repeat(43)}};
  const entry = (id: string, portico = false): AppServerSession => ({signIn: portico ? 'portico' : 'local', connection: {
    version: '1', selectionId: id.padEnd(32, '_'), logicalOrigin: `https://${id}.test`, pin: {serverId: id, publicKey: 'p', fingerprint: 'f'},
    routes: {paired: [`https://${id}.test`]}, ...(portico ? {hosted} : {}),
    session: {accessToken: 'synthetic-' + id, expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
      sessionFamilyId: 'family-' + id, tokenGeneration: '1', viewer: {serverId: id, authority: 'local', accountId: 'membership-' + id, profileId: 'main', role: 'member'}},
  }});
  let record: AppSessionRecord | undefined = {version: 1, revision: 1, servers: [entry('remote', true), entry('local')], selectedServerId: 'remote'};
  const sessions = new AppSessionStore({read: async () => structuredClone(record), change: async update => {record = structuredClone(update(structuredClone(record)));}});
  let credentialsRecord: CredentialRecord | undefined;
  const revoked: string[] = [];
  const credentials = new CredentialSessionService({authorityFromSession: session => session.refreshToken!,
    refresh: async () => {throw new Error('unexpected refresh');}, revoke: async authority => {revoked.push(authority);}},
    {load: async () => structuredClone(credentialsRecord), save: async value => {credentialsRecord = structuredClone(value);}}, async () => crypto.randomUUID());
  await credentials.adopt({account: {id: 'account', username: 'fixture', displayName: 'Fixture'}, accessToken: 'synthetic-access', refreshToken: 'synthetic-refresh',
    familyId: 'account-family', expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString()});
  let entered!: () => void, rejectServer!: (error: Error) => void;
  const attempting = new Promise<void>(resolve => {entered = resolve;});
  const server = new Promise<never>((_, reject) => {rejectServer = reject;});
  const modules: Record<string, unknown> = {
    'react-native': {NativeModules: {}, Platform: {OS: 'android'}},
    '../../../../packages/client-core/src/sign-in': {ServerSignInProbe: class {cancel() {} dispose() {}}},
    './app-session-storage': {nativeAppSessions: sessions}, './credentials': {createCredentials: () => credentials},
    './account-authority': {hostedUrl: hosted.origin}, './server-connections': {nativeServerEnvironment: () => ({})},
    '../../../../packages/client-core/src/server-connections': {restoreNativeSession: async () => {entered(); return server;}},
    '../../../../packages/client-core/src/route-connection': {forgetSessionRoute() {}},
  };
  const {NativeAccounts} = await screenModule(new URL('../src-next/platform/accounts.ts', import.meta.url), new Proxy(modules, {get: (all, key: string) => all[key] ?? {}}));
  const owner = new NativeAccounts();
  let settled = false, failure: unknown;
  const ending = owner.signOutPorticoAccount({accountId: 'account', familyId: 'account-family'}).then(() => {settled = true;}, (error: unknown) => {failure = error;});
  try {
    await attempting;
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(settled, true, 'server revocation must not hold sign-out open');
    assert.equal(failure, undefined);
    assert.equal(credentialsRecord?.active, undefined, 'Services credentials were durably forgotten');
    assert.equal(credentials.getSnapshot().session, undefined);
    assert.deepEqual(record?.servers.map(value => value.connection.pin.serverId), ['local'], 'only this Portico account’s server credentials are forgotten');
    assert.equal(record?.selectedServerId, undefined);
    assert.deepEqual(revoked, ['synthetic-refresh'], 'Services is told through the existing credential owner');
  } finally {rejectServer(new Error('server unreachable')); await ending; owner.dispose();}
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(failure, undefined, 'a late server failure cannot report a failed, half-completed sign-out');
});
