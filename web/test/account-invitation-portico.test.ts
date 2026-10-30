import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {routeBase64} from '@core/route-identity.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import type {HostedRequestApi} from '@core/portico-servers.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

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
  let current = true, grantAccount = accountId, invalidProof = false;
  let identityGate: Promise<void> | undefined, grantGate: Promise<void> | undefined, releaseIdentity: (() => void) | undefined, releaseGrant: (() => void) | undefined;
  const grant = () => ({authority: 'local', serverId, canManage: true,
    capabilities: {manageAccount: true, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'local-member', hostedAccountId: grantAccount, username: 'actual_person', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Actual person', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}],
    session: {accessToken: 'issued-access', refreshToken: 'issued-refresh-credential', sessionFamilyId: 'issued-family', tokenGeneration: '1', installationId: 'i'.repeat(43), deviceId: 'device',
      expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId, accountId: 'local-member', profileId: 'main', role: 'member'}}});
  const connection = {pin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const api = new HttpLocalApi(origin, ''); api.getRouteConnection = () => connection as never; api.setAccessToken = token => {tokens.push(token);};
  api.request = (async (path, _method, body) => {
    calls.push(path);
    if (path === '/v1/system') return system;
    if (path === '/v1/access/invitations/preview') return {allLibraries: false, expiresAt: '2099-01-01T00:00:00Z', inviterUsername: 'owner', libraries: [{name: 'Actual collection'}], libraryCount: 1, role: 'member', serverName: system.name, signInMethod: 'portico'};
    if (path === '/v1/direct/portico-challenge') {
      const challenge = 'q'.repeat(43), payload = new TextEncoder().encode(JSON.stringify({kind: 'portico.challenge-proof', version: '1', serverId,
        publicKey: pin.publicKey, fingerprint: pin.fingerprint, challenge, nonce: (body as {nonce: string}).nonce}));
      const signature = new Uint8Array(await crypto.subtle.sign('Ed25519', keys.privateKey, payload)); if (invalidProof) signature[0] ^= 1;
      return {challenge, proof: {payload: routeBase64(payload), signature: routeBase64(signature)}};
    }
    if (path === '/v1/access/invitations/accept') {assert.deepEqual(Object.keys(body as object).sort(), short?['porticoIdentity','shortCode']:['code','porticoIdentity']); if(short)assert.equal((body as {shortCode:string}).shortCode,'0123456789ABCDEF');else assert.equal((body as {code: string}).code, code); if (grantGate) await grantGate; return grant();}
    throw new Error('Unexpected request: ' + path);
  }) as HttpLocalApi['request'];
  const account = {origin: hostedOrigin, accountId, isCurrent: () => current, request: (async (path: string, method: string, body: unknown) => {
    calls.push('Hosted:' + path); assert.equal(path, '/v1/servers/' + serverId + '/identity'); assert.equal(method, 'POST'); assert.deepEqual(body, {challenge: 'q'.repeat(43)});
    if (identityGate) await identityGate;
    return {payload: 'actual-controlled-envelope', signature: 'controlled-signature', keyId: 'controlled-key'};
  }) as HostedRequestApi['request']};
  const storage = {read: async () => undefined, change: async () => {}};
  const entry = new BrowserAccountEntry({store: {selected: async () => selected, read: async () => ({version: 1, revision: 9, servers: []}), connectionStorage: (_id: string, value: {expectedRevision: number}) => {assert.equal(value.expectedRevision, 9); return storage;}} as never,
    actions: {} as never, environment: () => ({storage}), restore: async () => undefined, inspect: async () => pin, connect: (async () => ({api, connection})) as never,
    connectPortico: (async () => ({api, connection, hosted: {origin: hostedOrigin, accountId, key: {}}})) as never,
    remember: async (_api, session, _environment, _hosted, guard) => {assert.equal(guard?.(), true); admitted.push(session.sessionFamilyId);selected = {signIn: 'portico', connection: {pin, session}};},
    retire: async (_api, session) => {retired.push(session.sessionFamilyId);}, invitationDestination: destination,
  }, {inspect: async () => system, getSnapshot: () => ({origin}), cancel: () => {}, dispose: () => {}} as unknown as ServerSignInProbe);
  if(short)await entry.inspectShortInvitation(origin,'0123-4567-89ab-cdef');else await entry.inspectInvitation({server: origin, id: serverId, code});
  return {entry, account, calls, retired, admitted, tokens, origin, changeAccount: () => {current = false;}, wrongGrantAccount: () => {grantAccount = 'different-account';}, invalidProof: () => {invalidProof = true;},
    holdIdentity: () => {identityGate = new Promise(resolve => {releaseIdentity = resolve;});}, releaseIdentity: () => releaseIdentity?.(),
    holdGrant: () => {grantGate = new Promise(resolve => {releaseGrant = resolve;});}, releaseGrant: () => releaseGrant?.()};
}
async function until(check: () => boolean) {const deadline = Date.now() + 2000; while (!check() && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 1)); assert.equal(check(), true);}
test('Portico invitation uses the proved server challenge and captured account before durable admission', async () => {
  const f = await fixture(); await f.entry.joinPortico(f.account);
  assert.deepEqual(f.calls.map(path => path.startsWith('Hosted:') ? 'Hosted identity' : path), ['/v1/system', '/v1/access/invitations/preview', '/v1/direct/portico-challenge', 'Hosted identity', '/v1/access/invitations/accept']);
  assert.deepEqual(f.admitted, ['issued-family']); assert.deepEqual(f.retired, []); f.entry.dispose();
});
test('an invalid challenge proof asks Hosted for no assertion', async () => {
  const f = await fixture(); f.invalidProof(); await f.entry.joinPortico(f.account);
  assert.equal(f.calls.some(path => path.startsWith('Hosted:')), false); assert.deepEqual(f.admitted, []); assert.deepEqual(f.retired, []); f.entry.dispose();
});
test('changing the Hosted family during assertion issuance sends no identity to the server', async () => {
  const f = await fixture(); f.holdIdentity(); const pending = f.entry.joinPortico(f.account);
  await until(() => f.calls.some(path => path.startsWith('Hosted:'))); f.changeAccount(); f.releaseIdentity(); await pending;
  assert.equal(f.calls.includes('/v1/access/invitations/accept'), false); assert.deepEqual(f.admitted, []);
  assert.equal(f.entry.getSnapshot().busy, false, 'the still-current invitation can continue with the newly signed-in account');
  assert.equal(f.entry.getSnapshot().error, undefined); f.entry.dispose();
});
test('a grant arriving after the Hosted family changes is retired without admission', async () => {
  const f = await fixture(); f.holdGrant(); const pending = f.entry.joinPortico(f.account);
  await until(() => f.calls.includes('/v1/access/invitations/accept')); f.changeAccount(); f.releaseGrant(); await pending;
  assert.deepEqual(f.retired, ['issued-family']); assert.deepEqual(f.admitted, []);
  assert.equal(f.entry.getSnapshot().busy, false); assert.equal(f.entry.getSnapshot().phase, 'portico'); f.entry.dispose();
});
test('a different Hosted account in the returned grant is retired', async () => {
  const f = await fixture(); f.wrongGrantAccount(); await f.entry.joinPortico(f.account);
  assert.deepEqual(f.retired, ['issued-family']); assert.deepEqual(f.admitted, []); f.entry.dispose();
});
test('Services gets only the verified Portico destination after retiring its transient server family', async () => {
  const f = await fixture('server-origin'); await f.entry.joinPortico(f.account);
  assert.deepEqual(f.entry.getSnapshot().joined, {origin: f.origin, signIn: 'portico'}); assert.deepEqual(f.admitted, []); assert.deepEqual(f.retired, ['issued-family']); assert.deepEqual(f.tokens, []); f.entry.dispose();
});


test('a short Portico invitation uses the exact proved destination and captured Hosted family',async()=>{
 const f=await fixture('adopt',true);await f.entry.joinPortico(f.account);
 assert.deepEqual(f.admitted,['issued-family']);assert.deepEqual(f.retired,[]);f.entry.dispose();
});
