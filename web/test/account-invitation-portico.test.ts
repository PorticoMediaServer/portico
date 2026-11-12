import test from 'node:test';
import {systemAnswer} from '../../packages/client-core/test/next-wire.ts';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {routeBase64} from '@core/route-identity.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import {setInstallationIdentity} from '@core/installation.ts';

test.beforeEach(() => setInstallationIdentity({installationId: 'i'.repeat(43), name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}));
test.after(() => setInstallationIdentity(undefined));

/** Browser lifecycle proof with real challenge signatures. Hosted admission is a
 * controlled response here, not a cross-repository Hosted/Server acceptance test. */
async function fixture(destination: 'adopt' | 'server-origin' = 'adopt',short=false) {
  const keys = await crypto.subtle.generateKey('Ed25519', true, ['sign', 'verify']) as CryptoKeyPair;
  const publicBytes = new Uint8Array(await crypto.subtle.exportKey('raw', keys.publicKey));
  const context = new TextEncoder().encode('portico.server.identity.v1\0'), input = new Uint8Array(context.length + publicBytes.length);
  input.set(context); input.set(publicBytes, context.length);
  const serverId = 'srv_' + routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', input)));
  const pin = {serverId, publicKey: routeBase64(publicBytes), fingerprint: routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', publicBytes)))};
  const origin = 'https://inviting.example', hostedOrigin = 'https://account.example', accountId = 'current-hosted-account', code = 'c'.repeat(43);
  const system = {id: serverId, name: 'Proven inviting server', version: '1', signIn: 'portico', move: null, setupRequired: false, hostedConfigured: true, hostedAttached: true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  let selected: unknown;
  const calls: string[] = [], retired: string[] = [], admitted: string[] = [], tokens: string[] = [];
  let current = true, grantAccount = accountId, otherKey = false;
  let identityGate: Promise<void> | undefined, grantGate: Promise<void> | undefined, releaseIdentity: (() => void) | undefined, releaseGrant: (() => void) | undefined;
  const grant = () => ({authority: 'local', serverId, canManage: true,
    capabilities: {manageAccount: true, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'local-member', hostedAccountId: grantAccount, username: 'actual_person', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Actual person', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}],
    session: {accessToken: 'issued-access', refreshToken: 'issued-refresh-credential', sessionFamilyId: 'issued-family', tokenGeneration: '1', installationId: 'i'.repeat(43), deviceId: 'device',
      expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId, accountId: 'local-member', profileId: 'main', role: 'member'}}});
  const connection = {pin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const api = new HttpLocalApi(origin, ''); api.getRouteConnection = () => connection as never; api.setAccessToken = token => {tokens.push(token);};
  const challenge = 'challenge-from-the-inviting-server-0001';
  api.request = (async (path, _method, body) => {
    calls.push(path);
    if (path === '/v1/system') return systemAnswer(system);
    if (path === '/v1/access/invitations/preview') return {allLibraries: false, expiresAt: '2099-01-01T00:00:00Z', inviterUsername: 'owner', libraries: [{name: 'Actual collection'}], libraryCount: 1, role: 'member', serverName: system.name, signInMethod: 'portico', forPorticoAccount: true};
    if (path === '/v1/auth/portico/challenge') return {challenge, serverId, expiresAt: '2099-01-01T00:00:00Z'};
    if (path === '/v1/auth/portico/exchange') {
      const sent = body as {challenge: string; identity: unknown; invitation?: string};
      assert.equal(sent.challenge, challenge); assert.equal(sent.invitation, short ? '0123456789ABCDEF' : code);
      if (grantGate) await grantGate;
      return {session: grant().session, account: {id: 'local-member', username: 'actual_person', role: 'member'}, profiles: [{id: 'main', name: 'Actual person', color: 'blue', isPrimary: true, pinRequired: false}]};
    }
    throw new Error('Unexpected request: ' + path);
  }) as HttpLocalApi['request'];
  // Portico Services' note for the server's challenge, naming this page's account.
  const note = () => ({identity: {payload: btoa(JSON.stringify({kind: 'portico.identity.v1', accountId: grantAccount, serverId, challenge})).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, ''), signature: 'sig', keyId: 'k'}});
  const account = {origin: hostedOrigin, accountId, username: 'actual_person', isCurrent: () => current, observe: () => () => {},
    identity: async (forServer: string, forChallenge: string) => {
      calls.push('Services:identity'); assert.equal(forServer, serverId); assert.equal(forChallenge, challenge);
      if (identityGate) await identityGate;
      return note();
    }};
  const storage = {read: async () => undefined, change: async () => {}};
  const entry = new BrowserAccountEntry({store: {selected: async () => selected, read: async () => ({version: 1, revision: 9, servers: []}), connectionStorage: (_id: string, value: {expectedRevision: number}) => {assert.equal(value.expectedRevision, 9); return storage;}} as never,
    actions: {} as never, environment: () => ({storage}), restore: async () => undefined, inspect: async () => (otherKey ? {...pin, publicKey: 'another-key'} : pin), connect: (async () => ({api, connection})) as never,
    remember: async (_api, session, _environment, _hosted, guard) => {assert.equal(guard?.(), true); admitted.push(session.sessionFamilyId);selected = {signIn: 'portico', connection: {pin, session}};},
    retire: async (_api, session) => {retired.push(session.sessionFamilyId);}, invitationDestination: destination,
  }, {inspect: async () => system, getSnapshot: () => ({origin}), cancel: () => {}, dispose: () => {}} as unknown as ServerSignInProbe);
  if(short)await entry.inspectShortInvitation(origin,'0123-4567-89ab-cdef');else await entry.inspectInvitation({server: origin, id: serverId, code});
  return {entry, account, calls, retired, admitted, tokens, origin, changeAccount: () => {current = false;}, wrongGrantAccount: () => {grantAccount = 'different-account';}, otherKey: () => {otherKey = true;},
    holdIdentity: () => {identityGate = new Promise(resolve => {releaseIdentity = resolve;});}, releaseIdentity: () => releaseIdentity?.(),
    holdGrant: () => {grantGate = new Promise(resolve => {releaseGrant = resolve;});}, releaseGrant: () => releaseGrant?.()};
}
async function until(check: () => boolean) {const deadline = Date.now() + 2000; while (!check() && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 1)); assert.equal(check(), true);}
test('a server at the invitation\'s address with another key is asked for nothing and gets no note', async () => {
  const f = await fixture(); f.otherKey(); await f.entry.joinPortico(f.account);
  assert.equal(f.calls.includes('Services:identity'), false); assert.equal(f.calls.includes('/v1/auth/portico/challenge'), false);
  assert.deepEqual(f.admitted, []); assert.equal((f.entry.getSnapshot().error as {code?: string})?.code, 'challenge_not_proven'); f.entry.dispose();
});
test('changing the Portico Account while its note is issued sends nothing to the server', async () => {
  const f = await fixture(); f.holdIdentity(); const pending = f.entry.joinPortico(f.account);
  await until(() => f.calls.includes('Services:identity')); f.changeAccount(); f.releaseIdentity(); await pending;
  assert.equal(f.calls.includes('/v1/auth/portico/exchange'), false); assert.deepEqual(f.admitted, []);
  assert.equal(f.entry.getSnapshot().busy, false, 'the still-current invitation can continue with the newly signed-in account');
  assert.equal(f.entry.getSnapshot().error, undefined); f.entry.dispose();
});
for (const short of [false, true]) test(`joining with a Portico Account sends the server\'s challenge, Services\' note and the invitation\'s code${short ? ' (short code)' : ''}`, async () => {
  const f = await fixture('adopt', short); await f.entry.joinPortico(f.account);
  assert.deepEqual(f.calls.filter(path => path.startsWith('/v1/auth/') || path.startsWith('Services:')), ['/v1/auth/portico/challenge', 'Services:identity', '/v1/auth/portico/exchange']);
  assert.deepEqual(f.admitted, ['issued-family']); f.entry.dispose();
});
test('a note naming another Portico Account is never sent to the server', async () => {
  const f = await fixture(); f.wrongGrantAccount(); await f.entry.joinPortico(f.account);
  assert.equal(f.calls.includes('/v1/auth/portico/exchange'), false); assert.deepEqual(f.admitted, []); f.entry.dispose();
});
