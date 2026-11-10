import test from 'node:test';
import {accountAnswer, accountPath, systemAnswer} from './next-wire.ts';
import assert from 'node:assert/strict';
import {currentShellProfile, readShellIdentity, ShellBootstrap, sameShellAuthority, shellArtworkAuthorityKey, type ShellAssembler} from '../src/shell/bootstrap.ts';
import {type HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {SelectedViewerRecord} from '@core/viewer-selection.ts';

const unavailableAssembly: ShellAssembler = async () => { throw new Error('Unexpected assembly.'); };
const selected = {session: {sessionFamilyId: 'family-a'}} as SelectedViewerRecord;
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return {promise, resolve}; }

test('an absent protected selection reaches the registered signed-out flow without reading a server', async () => {
  let reads = 0;
  const bootstrap = new ShellBootstrap(unavailableAssembly, {selected: {read: async () => null}, restore: async () => { reads++; return undefined; }});
  await bootstrap.start();
  assert.equal(bootstrap.getSnapshot().phase, 'signed-out');
  assert.equal(reads, 0);
  bootstrap.dispose();
});

test('retiring while the vault is opening prevents a later selection from restoring authority', async () => {
  const vault = deferred<SelectedViewerRecord | null>(); let reads = 0;
  const bootstrap = new ShellBootstrap(unavailableAssembly, {selected: {read: () => vault.promise}, restore: async () => { reads++; return undefined; }});
  const work = bootstrap.start(); bootstrap.dispose(); vault.resolve(selected); await work;
  assert.equal(bootstrap.getSnapshot().phase, 'signed-out'); assert.equal(reads, 0);
});

test('a route restored after retirement is disposed and never assembled', async () => {
  const route = deferred<{api: HttpLocalApi; session: LocalSession} | undefined>(); let disposed = 0, assembling = 0;
  const bootstrap = new ShellBootstrap(async context => { assembling++; return unavailableAssembly(context); }, {
    selected: {read: async () => selected}, restore: () => route.promise,
  });
  const work = bootstrap.start(); await Promise.resolve(); bootstrap.dispose();
  route.resolve({api: {getRouteConnection: () => ({dispose: () => { disposed++; }})} as unknown as HttpLocalApi, session: selected.session}); await work;
  assert.equal(disposed, 1); assert.equal(assembling, 0); assert.equal(bootstrap.getSnapshot().phase, 'signed-out');
});
test('terminal disposal prevents a late server switch from restarting protected restoration', async () => {
  const switchGate = deferred<void>();let reads = 0, switches = 0;
  const bootstrap = new ShellBootstrap(unavailableAssembly, {
    selected: {read: async () => {reads++;return null;}}, restore: async () => {assert.fail('no route restoration after close');},
    actions: () => ({switchServer: async () => {switches++;await switchGate.promise;}} as never),
  });
  const switching = bootstrap.selectServer('actual-selected-server');
  bootstrap.dispose();bootstrap.dispose();switchGate.resolve();await switching;
  await bootstrap.start();await bootstrap.resumeAfterAccountEntry();await bootstrap.selectServer('later-server');
  assert.equal(reads, 0);assert.equal(switches, 1);assert.equal(bootstrap.getSnapshot().phase, 'signed-out');
});

test('a failed protected read exposes retry state and a later explicit retry can restore signed-out state', async () => {
  let attempt = 0, observed = 0;
  const bootstrap = new ShellBootstrap(unavailableAssembly, {selected: {read: async () => { if (attempt++ === 0) throw new Error('vault_busy'); return null; }}, restore: async () => undefined});
  const off = bootstrap.subscribe(() => { observed++; });
  await bootstrap.start(); assert.equal(bootstrap.getSnapshot().phase, 'error');
  await bootstrap.start(); assert.equal(bootstrap.getSnapshot().phase, 'signed-out'); assert.ok(observed >= 4);
  off(); const before = observed; bootstrap.dispose(); assert.equal(observed, before);
});

test('token rotation retains a scope but every authority or family replacement retires it', () => {
  const session = {sessionFamilyId: 'family-a', accessToken: 'test-token-a', viewer: {serverId: 'server-a', accountId: 'account-a', profileId: 'profile-a', authority: 'local', role: 'owner'}} as LocalSession;
  assert.equal(sameShellAuthority(session, {...session, accessToken: 'test-token-b'}), true);
  assert.equal(sameShellAuthority(session, {...session, sessionFamilyId: 'family-b'}), false);
  for (const key of ['serverId', 'accountId', 'profileId', 'authority', 'role'] as const) {
    assert.equal(sameShellAuthority(session, {...session, viewer: {...session.viewer, [key]: 'replacement'}} as LocalSession), false, key);
  }
});

test('artwork cache identity derives from captured family/viewer/role/epoch without a Home document', () => {
  const session = {sessionFamilyId: 'family-a', accessToken: 'token-a', viewer: {serverId: 'server-a', authority: 'local', accountId: 'account-a', profileId: 'profile-a', role: 'member'}} as LocalSession;
  const key = shellArtworkAuthorityKey(session, 4);
  assert.equal(shellArtworkAuthorityKey({...session, accessToken: 'rotated-token'}, 4), key);
  assert.notEqual(shellArtworkAuthorityKey(session, 5), key);
  assert.notEqual(shellArtworkAuthorityKey({...session, sessionFamilyId: 'family-b'}, 4), key);
  for (const field of ['serverId', 'accountId', 'profileId', 'role'] as const) assert.notEqual(shellArtworkAuthorityKey({...session, viewer: {...session.viewer, [field]: 'changed'}} as LocalSession, 4), key);
  assert.doesNotMatch(key, /token-a|viewerFence/);
});

function directRecord(username: string | undefined = 'local-user', profileName = 'Evening profile') {
  return {authority: 'local', serverId: 'server-a', canManage: true, capabilities: {manageAccount:true,managePeople:true,manageServer:true,manageSignIn:true},
    account: {id: 'account-a', username, primaryProfileId: 'profile-a', role: 'owner', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'profile-a', name: profileName, primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false}]};
}
const directSession = {sessionFamilyId: 'family-a', viewer: {serverId: 'server-a', accountId: 'account-a', profileId: 'profile-a', authority: 'local', role: 'owner'}} as LocalSession;
test('local account facts supply genuine username separately from differing or matching profile names', async () => {
  for (const name of ['Evening profile', 'local-user']) {
    const calls: unknown[][] = [], controller = new AbortController();
    const api = {request: async (...args: unknown[]) => { calls.push(args); return accountAnswer(args[0] as string, directRecord('local-user', name)); }} as unknown as HttpLocalApi;
    const restored = await currentShellProfile(api, directSession, controller.signal);
    assert.equal(restored.username, 'local-user'); assert.equal(restored.profileName, name);
    assert.equal(restored.primaryProfile, true); assert.equal(restored.profileArt, 'mint'); assert.equal(restored.canManageProfiles, true);
    assert.deepEqual(restored.accountFacts, []);
    assert.deepEqual(restored.profiles, [{id: 'profile-a', name, initial: Array.from(name)[0], art: 'mint', pinRequired: false, primary: true}]);
    assert.deepEqual(calls, [['/v1/me', 'GET', undefined, controller.signal], ['/v1/profiles', 'GET', undefined, controller.signal]]);
  }
});
test('local missing username or mismatched account/server/profile fails without promoting another display field', async () => {
  const raw = directRecord();
  for (const response of [{...raw, account: {...raw.account, username: undefined}}, {...raw, account: {...raw.account, id: 'other-account'}}, {...raw, serverId: 'other-server'}, {...raw, profiles: [{...raw.profiles[0], id: 'other-profile'}], account: {...raw.account, primaryProfileId: 'other-profile'}}]) {
    const api = {request: async () => response} as unknown as HttpLocalApi;
    await assert.rejects(currentShellProfile(api, directSession, new AbortController().signal));
  }
});
test('Portico members read profiles on this server and never from the Hosted account', async () => {
  const raw=directRecord();Object.assign(raw.account,{hostedAccountId:'central-account'});
  const calls:string[]=[];
  const result=await currentShellProfile({request:async(path:string)=>{calls.push(path);return accountAnswer(path,raw);}} as HttpLocalApi,directSession,new AbortController().signal);
  assert.equal(result.profileName,'Evening profile');assert.deepEqual(calls,['/v1/me','/v1/profiles']);
});
test('a secondary profile has no management powers, even on an owner account', async () => {
 const raw=directRecord();raw.profiles.push({...raw.profiles[0],id:'kids',name:'Kids',primary:false});
 const child={...directSession,viewer:{...directSession.viewer,profileId:'kids',role:'member'}};
 // The server says a profile other than the main one is a member that manages nothing.
 const api={request:async(path:string)=>accountAnswer(path,raw,{profileId:'kids'})} as HttpLocalApi;
 const shown=await currentShellProfile(api,child,new AbortController().signal);
 assert.equal(shown.canManageProfiles,false);assert.equal(shown.primaryProfile,false);assert.deepEqual(Object.values(shown.capabilities),[false,false,false,false]);
 // An answer that claimed management for it is refused.
 const claims={request:async(path:string)=>path==='/v1/me'?{...(accountAnswer(path,raw,{profileId:'kids'}) as object),canManageAccount:true}:accountAnswer(path,raw)} as HttpLocalApi;
 await assert.rejects(currentShellProfile(claims,child,new AbortController().signal),/main profile/);
});


test('shell identity loads during a building Reading generation and retains Settings authority', async () => {
  const summary = {counts: [{kind: 'ebook', count: 37, atLeast: true}], coverUrls: [], latestArtwork: '', firstScan: true, empty: false, emptyKnown: false};
  const calls: string[] = [];
  const api = {request: async (path: string) => {
    calls.push(path);
    if (path === '/v1/libraries') return {items: [{id: 'reading-a', name: 'Still importing', kind: 'book', summary}], viewerFence: 'viewer-a', privacyFence: 'privacy-a'};
    if (accountPath(path)) return accountAnswer(path, directRecord());
    if (path === '/v1/system') return systemAnswer({id: 'server-a', name: 'Test server', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []});
    if (path.startsWith('/v1/guide/sources?')) throw Object.assign(new Error('not permitted'), {status: 403});
    throw new Error(`Unexpected shell read ${path}`);
  }} as unknown as HttpLocalApi;
  const read = await readShellIdentity(api, directSession, new AbortController().signal);
  assert.equal(read.libraries.length, 1);
  assert.deepEqual(read.libraries, [{id: 'reading-a', name: 'Still importing', kind: 'book', summary}]);
  assert.equal(read.profile.capabilities?.manageServer, true);
  assert.equal(read.profile.profileName, 'Evening profile');
  assert.equal(read.system.name, 'Test server');
  // Client data step 5: the shell learns who the viewer is from the directory, not from Home.
  assert.deepEqual('fences' in read && read.fences, {viewerFence: 'viewer-a', privacyFence: 'privacy-a'});
  assert.equal(calls.includes('/v1/home'), false);
  assert.ok(calls.includes('/v1/libraries') && calls.includes('/v1/me') && calls.includes('/v1/system'));
});

test('a library directory that times out never stops sign-in: the libraries are unavailable, not empty, and authority answers still stop', async () => {
  const api = (libraries: () => unknown) => ({request: async (path: string) => {
    if (path === '/v1/libraries') return libraries();
    if (accountPath(path)) return accountAnswer(path, directRecord());
    if (path === '/v1/system') return systemAnswer({id: 'server-a', name: 'Test server', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []});
    if (path.startsWith('/v1/guide/sources?')) throw Object.assign(new Error('not permitted'), {status: 403});
    throw new Error(`Unexpected shell read ${path}`);
  }}) as unknown as HttpLocalApi;
  const busy = await readShellIdentity(api(() => {throw Object.assign(new Error('timeout'), {status: 503, code: 'metadata_unavailable'});}), directSession, new AbortController().signal);
  assert.deepEqual(busy.libraries, []);
  assert.equal('librariesUnavailable' in busy && busy.librariesUnavailable, true, 'unknown, never "no libraries"');
  assert.equal(busy.profile.profileName, 'Evening profile', 'the rest of the shell still loads');
  const settled = await readShellIdentity(api(() => ({items: []})), directSession, new AbortController().signal);
  assert.equal('librariesUnavailable' in settled, false, 'a real empty directory is just empty');
  await assert.rejects(readShellIdentity(api(() => {throw Object.assign(new Error('signed out'), {status: 401});}), directSession, new AbortController().signal), /signed out/);
});
