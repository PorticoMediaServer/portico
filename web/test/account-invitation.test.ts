import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';

const origin = 'https://inviting.example', serverId = 'srv_' + 's'.repeat(43), code = 'i'.repeat(43);
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
const system = {id: serverId, name: 'Actual inviting server', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
const preview = {allLibraries: false, expiresAt: '2099-01-01T00:00:00Z', inviterUsername: 'owner', libraries: [{name: 'Actual library'}], libraryCount: 1, role: 'member', serverName: system.name, signInMethod: 'local'};
function response() {
  return {authority: 'local', serverId, canManage: true, capabilities: {manageAccount: true, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'created-account', username: 'new_user', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'New person', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}],
    session: {accessToken: 'issued-access', refreshToken: 'issued-refresh-credential', sessionFamilyId: 'issued-family', tokenGeneration: '1', installationId: 'i'.repeat(43), deviceId: 'device',
      expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId, accountId: 'created-account', profileId: 'main', role: 'member'}}};
}
function fixture(destination: 'adopt' | 'server-origin' = 'adopt') {
  const calls: {path: string; body?: unknown}[] = [], retired: string[] = [], admitted: string[] = [], tokens: string[] = [];
  let selected: unknown, proofRefused=false;
  let currentOrigin = origin, previewAnswer: unknown = preview, failSignIn = false;
  let family = 'issued-family', token = 'issued-access';
  let retirementGate: Promise<void> | undefined, releaseRetirement: (() => void) | undefined;
  let signInGate: Promise<void> | undefined, releaseSignIn: (() => void) | undefined;
  let probeGate: Promise<void> | undefined, releaseProbe: (() => void) | undefined;
  const connection = {pin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const api = new HttpLocalApi(origin, '');
  api.getRouteConnection = () => connection as never;
  api.setAccessToken = value => {tokens.push(value);};
  api.request = (async (path, _method, body) => {
    calls.push({path, body});
    if (path === '/v1/system') return system;
    if (path === '/v1/access/invitations/preview') return previewAnswer;
    if (path === '/v1/access/invitations/accept') return {accountId: 'created-account', profileId: 'main', username: 'new_user', role: 'member', allowedLibraries: []};
    if (path === '/v1/direct/sign-in') {if (signInGate) await signInGate; if (failSignIn) throw new Error('offline after membership creation'); const result = response();result.session.sessionFamilyId = family;result.session.accessToken = token;return result;}
    throw new Error('Unexpected endpoint: ' + path);
  }) as HttpLocalApi['request'];
  const storage = {read: async () => undefined, change: async () => {}};
  const probe = {inspect: async (value: string) => {currentOrigin = value; if (probeGate) {const wait = probeGate; probeGate = undefined; await wait;} return system;},
    getSnapshot: () => ({origin: currentOrigin}), cancel: () => {}, dispose: () => {}} as unknown as ServerSignInProbe;
  const entry = new BrowserAccountEntry({
    store: {selected: async () => selected, read: async () => ({version: 1, revision: 7, servers: []}), connectionStorage: (_id: string, admission: {expectedRevision: number}) => {assert.equal(admission.expectedRevision, 7); return storage;}} as never,
    actions: {} as never, environment: () => ({storage}), restore: async () => undefined,
    inspect: async () => {if(proofRefused)throw Object.assign(new Error('unproven'),{code:'identity_mismatch'});return pin;}, connect: (async () => ({api, connection})) as never,
    remember: async (_api, session, _env, _hosted, current) => {assert.equal(current?.(), true); admitted.push(session.sessionFamilyId);selected = {signIn: 'local', connection: {pin, session}};},
    retire: async (_api, session) => {retired.push(session.sessionFamilyId);if (retirementGate) await retirementGate;}, invitationDestination: destination,
  }, probe);
  return {entry, calls, retired, admitted, tokens, refuseProof:()=>{proofRefused=true;}, successorGrant: () => {family = 'successor-family';token = 'successor-access';},
    holdRetirement: () => {retirementGate = new Promise(resolve => {releaseRetirement = resolve;});}, releaseRetirement: () => {releaseRetirement?.();}, preview: (value: unknown) => {previewAnswer = value;}, failSignIn: (value: boolean) => {failSignIn = value;},
    holdSignIn: () => {signInGate = new Promise(resolve => {releaseSignIn = resolve;});}, releaseSignIn: () => {releaseSignIn?.(); signInGate = undefined;},
    holdProbe: () => {probeGate = new Promise(resolve => {releaseProbe = resolve;});}, releaseProbe: () => {releaseProbe?.();}};
}
const link = {server: origin, id: serverId, code};
test('invitation code reaches only the verified server after kind/proof/link ownership guards', async () => {
  const f = fixture(); await f.entry.inspectInvitation(link);
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/system', '/v1/access/invitations/preview']);
  assert.equal(f.entry.getSnapshot().invitation?.libraries[0].name, 'Actual library');
  assert.equal(f.entry.getSnapshot().phase, 'password'); assert.deepEqual(f.calls[1].body, {code}); f.entry.dispose();
});
test('a link naming another identity is refused before the invitation secret is sent', async () => {
  const f = fixture(); await f.entry.inspectInvitation({...link, id: 'srv_' + 'x'.repeat(43)});
  assert.equal(f.calls.some(value => value.path.includes('invitations')), false);
  assert.equal((f.entry.getSnapshot().error as {code: string}).code, 'identity_mismatch'); f.entry.dispose();
});
test('a cancelled invitation probe cannot post its old code through a successor entry', async () => {
  const f = fixture(); f.holdProbe(); const old = f.entry.inspectInvitation(link);
  await new Promise(resolve => setImmediate(resolve)); await f.entry.inspect('https://successor.example');
  f.releaseProbe(); await old;
  assert.equal(f.calls.some(value => value.path.includes('invitations')), false); f.entry.dispose();
});
test('a preview cannot switch the verified local kind into a Portico credential path', async () => {
  const f = fixture(); f.preview({...preview, signInMethod: 'portico'}); await f.entry.inspectInvitation(link);
  assert.equal(f.entry.getSnapshot().invitation, undefined); assert.equal((f.entry.getSnapshot().error as {code: string}).code, 'invalid_response');
  await f.entry.joinLocal('new_user', 'long enough password'); assert.equal(f.admitted.length, 0); f.entry.dispose();
});
test('local join creates once, signs in, and admits the actual created account using the captured revision', async () => {
  const f = fixture(); await f.entry.inspectInvitation(link); await f.entry.joinLocal('new_user', 'long enough password', 'New person');
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/system', '/v1/access/invitations/preview', '/v1/access/invitations/accept', '/v1/direct/sign-in']);
  assert.deepEqual(f.admitted, ['issued-family']); assert.equal(f.entry.getSnapshot().phase, 'ready'); f.entry.dispose();
});
test('an acknowledged membership is not created again when its initial sign-in needs retry', async () => {
  const f = fixture(); await f.entry.inspectInvitation(link); f.failSignIn(true); await f.entry.joinLocal('new_user', 'long enough password');
  f.failSignIn(false); await f.entry.joinLocal('new_user', 'long enough password');
  assert.equal(f.calls.filter(value => value.path === '/v1/access/invitations/accept').length, 1);
  assert.deepEqual(f.admitted, ['issued-family']); f.entry.dispose();
});
test('Services completion retires the temporary server family and exposes only verified origin/kind', async () => {
  const f = fixture('server-origin'); await f.entry.inspectInvitation(link); await f.entry.joinLocal('new_user', 'long enough password');
  assert.deepEqual(f.admitted, []); assert.deepEqual(f.retired, ['issued-family']);
  assert.deepEqual(f.entry.getSnapshot().joined, {origin, signIn: 'local'});
  assert.equal(f.entry.getSnapshot().snapshot, undefined); assert.deepEqual(f.tokens, []); f.entry.dispose();
});
test('a Local join grant returned after leaving entry is retired without durable admission', async () => {
  const f = fixture(); await f.entry.inspectInvitation(link); f.holdSignIn(); const joining = f.entry.joinLocal('new_user', 'long enough password');
  for (let n = 0; n < 100 && !f.calls.some(value => value.path === '/v1/direct/sign-in'); n++) await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.calls.some(value => value.path === '/v1/direct/sign-in'), true);
  f.entry.cancel(); f.releaseSignIn(); await joining;
  assert.deepEqual(f.retired, ['issued-family']); assert.deepEqual(f.admitted, []); assert.equal(f.entry.getSnapshot().phase, 'welcome');
  assert.deepEqual(f.tokens, [], 'an abandoned pre-admission result cannot publish even a token clear'); f.entry.dispose();
});


test('late cleanup cannot clear a successor admitted through the same captured API object', async () => {
  const f = fixture();await f.entry.inspectInvitation(link);f.holdSignIn();f.holdRetirement();
  const abandoned = f.entry.joinLocal('new_user', 'long enough password');
  for (let n = 0; n < 100 && !f.calls.some(value => value.path === '/v1/direct/sign-in'); n++) await new Promise(resolve => setImmediate(resolve));
  assert.ok(f.calls.some(value => value.path === '/v1/direct/sign-in'));
  f.entry.cancel();f.releaseSignIn();
  for (let n = 0; n < 100 && f.retired.length === 0; n++) await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(f.retired, ['issued-family']);
  f.successorGrant();await f.entry.inspectInvitation(link);await f.entry.joinLocal('new_user', 'long enough password');
  assert.deepEqual(f.admitted, ['successor-family']);assert.deepEqual(f.tokens, ['successor-access']);
  f.releaseRetirement();await abandoned;
  assert.deepEqual(f.tokens, ['successor-access']);assert.equal(f.entry.getSnapshot().phase, 'ready');f.entry.dispose();
});


test('human short code uses the verified entry and existing one-use local join without a long secret',async()=>{
 const f=fixture();await f.entry.inspectShortInvitation(origin,'0123-4567-89ab-cdef');
 assert.deepEqual(f.calls.map(v=>v.path),['/v1/system','/v1/access/invitations/preview']);
 assert.deepEqual(f.calls[1].body,{shortCode:'0123456789ABCDEF'});
 assert.equal(JSON.stringify(f.entry.getSnapshot()).includes('0123456789ABCDEF'),false);
 await f.entry.joinLocal('new_user','long enough password','New person');
 assert.deepEqual(f.calls.find(v=>v.path==='/v1/access/invitations/accept')?.body,{shortCode:'0123456789ABCDEF',username:'new_user',password:'long enough password',name:'New person'});
 assert.deepEqual(f.admitted,['issued-family']);f.entry.dispose();
});

test('short code is never sent after failed proof, invalid syntax or abandoned address inspection',async()=>{
 const f=fixture();f.refuseProof();await f.entry.inspectShortInvitation(origin,'0123456789ABCDEF');
 assert.equal(f.calls.some(v=>v.path.includes('invitations')),false);f.entry.dispose();
 const invalid=fixture();await invalid.entry.inspectShortInvitation(origin,'x'.repeat(43));assert.equal(invalid.calls.length,0);invalid.entry.dispose();
 const cancelled=fixture();cancelled.holdProbe();const old=cancelled.entry.inspectShortInvitation(origin,'0123456789ABCDEF');
 await new Promise(resolve=>setImmediate(resolve));await cancelled.entry.inspect('https://successor.example');cancelled.releaseProbe();await old;
 assert.equal(cancelled.calls.some(v=>v.path.includes('invitations')),false);cancelled.entry.dispose();
});
