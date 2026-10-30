import test from 'node:test';
import assert from 'node:assert/strict';
import {currentShellProfile, readShellIdentity, ShellBootstrap, sameShellAuthority, shellArtworkAuthorityKey, watchHomeViewerFence, type ShellAssembler} from '../src/shell/bootstrap.ts';
import {type HttpLocalApi, type LocalSession} from '@core/index.ts';
import {HomeService} from '@core/home-service.ts';
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

function homeDocument(fence: string) {
  return {serverId: 'server-a', viewerFence: fence, generatedAt: '2026-10-23T12:00:00.000Z', rows: [], spotlight: [], revision: {catalog: 1, viewer: 1}, layout: {revision: 1, rowOrder: [], hiddenRowIds: []}};
}
test('pending and failed initial Home reads have no fabricated fence; the first genuine retry establishes the baseline', async () => {
  const gate = deferred<unknown>(); let response: Promise<unknown> | unknown = gate.promise, replacements = 0;
  const home = new HomeService({request: async <T>() => await response as T});
  const boundary = watchHomeViewerFence(home, () => true, () => {replacements++;});
  const pending = home.load(); assert.equal(home.getSnapshot().phase, 'loading'); assert.equal(boundary.viewerFence, undefined);
  gate.resolve({invalid: true}); await pending;
  assert.equal(home.getSnapshot().phase, 'error'); assert.equal(home.getSnapshot().document, undefined); assert.equal(boundary.viewerFence, undefined);
  response = homeDocument('actual-first-fence'); await home.retry();
  assert.equal(boundary.viewerFence, 'actual-first-fence'); assert.equal(replacements, 0);
  await home.load(); assert.equal(replacements, 0); boundary.dispose(); home.dispose();
});
test('a later genuine Home fence replacement retires only this boundary and never adopts its successor', async () => {
  let response = homeDocument('first'), replacements = 0;
  const home = new HomeService({request: async <T>() => response as T});
  const boundary = watchHomeViewerFence(home, () => true, () => {replacements++;});
  await home.load(); response = homeDocument('different'); await home.load();
  assert.equal(replacements, 1); assert.equal(boundary.viewerFence, undefined);
  response = homeDocument('third'); await home.load(); assert.equal(replacements, 1); home.dispose();
});
// H-11: a library added or removed changes the viewer fence only; Settings and the player
// host stay mounted, and Home-data checks follow the current document's viewer fence.
test('a viewer-fence-only change keeps the boundary and follows the new fence; a privacy change retires it', async () => {
  let response: unknown = {...homeDocument('first'), privacyFence: 'privacy'}, replacements = 0;
  const home = new HomeService({request: async <T>() => response as T});
  const boundary = watchHomeViewerFence(home, () => true, () => {replacements++;});
  await home.load(); assert.equal(boundary.viewerFence, 'first');
  response = {...homeDocument('more-libraries'), privacyFence: 'privacy'}; await home.load();
  assert.equal(replacements, 0); assert.equal(boundary.viewerFence, 'more-libraries');
  response = {...homeDocument('more-libraries'), privacyFence: 'another-viewer'}; await home.load();
  assert.equal(replacements, 1); assert.equal(boundary.viewerFence, undefined); home.dispose();
});
test('explicitly clearing an established document fences before its next request finishes', async () => {
  const gate = deferred<unknown>(); let response: Promise<unknown> | unknown = homeDocument('first'), replacements = 0;
  const home = new HomeService({request: async <T>() => await response as T});
  const boundary = watchHomeViewerFence(home, () => true, () => {replacements++;});
  await home.load(); response = gate.promise; const pending = home.load({clear: true});
  assert.equal(replacements, 1); assert.equal(boundary.viewerFence, undefined);
  gate.resolve(homeDocument('different')); await pending; assert.equal(replacements, 1); home.dispose();
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
    const api = {request: async (...args: unknown[]) => { calls.push(args); return directRecord('local-user', name); }} as unknown as HttpLocalApi;
    const restored = await currentShellProfile(api, directSession, controller.signal);
    assert.equal(restored.username, 'local-user'); assert.equal(restored.profileName, name);
    assert.equal(restored.primaryProfile, true); assert.equal(restored.profileArt, 'mint'); assert.equal(restored.canManageProfiles, true);
    assert.deepEqual(restored.accountFacts, []);
    assert.deepEqual(restored.profiles, [{id: 'profile-a', name, initial: Array.from(name)[0], art: 'mint', pinRequired: false, primary: true}]);
    assert.deepEqual(calls, [['/v1/direct', 'GET', undefined, controller.signal]]);
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
  const result=await currentShellProfile({request:async(path:string)=>{calls.push(path);return raw;}} as HttpLocalApi,directSession,new AbortController().signal);
  assert.equal(result.profileName,'Evening profile');assert.deepEqual(calls,['/v1/direct']);
});
test('a secondary profile has no management powers, even on an owner account', async () => {
 const raw=directRecord();raw.profiles.push({...raw.profiles[0],id:'kids',name:'Kids',primary:false});
 const child={...directSession,viewer:{...directSession.viewer,profileId:'kids',role:'member'}};
 const api={request:async()=>raw} as HttpLocalApi;
 await assert.rejects(currentShellProfile(api,child,new AbortController().signal),/main profile/);
 Object.assign(raw,{canManage:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false}});
 const facts=await currentShellProfile(api,child,new AbortController().signal);
 assert.equal(facts.canManageProfiles,false);assert.equal(facts.primaryProfile,false);assert.deepEqual(Object.values(facts.capabilities!),[false,false,false,false]);
 await assert.rejects(currentShellProfile(api,{...child,viewer:{...child.viewer,authority:'hosted'}},new AbortController().signal),/this server/);
});


test('shell identity loads during a building Reading generation and retains Settings authority', async () => {
  const summary = {counts: [{kind: 'ebook', count: 37, atLeast: true}], coverUrls: [], latestArtwork: '', firstScan: true, empty: false, emptyKnown: false};
  const calls: string[] = [];
  const api = {request: async (path: string) => {
    calls.push(path);
    if (path === '/v1/libraries') return {items: [{id: 'reading-a', name: 'Still importing', kind: 'book', summary}]};
    if (path === '/v1/direct') return directRecord();
    if (path === '/v1/system') return {id: 'server-a', name: 'Test server', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
    if (path.startsWith('/v1/guide/sources?')) throw Object.assign(new Error('not permitted'), {status: 403});
    throw new Error(`Unexpected shell read ${path}`);
  }} as unknown as HttpLocalApi;
  const read = await readShellIdentity(api, directSession, new AbortController().signal);
  assert.equal(read.libraries.length, 1);
  assert.deepEqual(read.libraries, [{id: 'reading-a', name: 'Still importing', kind: 'book', summary}]);
  assert.equal(read.profile.capabilities?.manageServer, true);
  assert.equal(read.profile.profileName, 'Evening profile');
  assert.equal(read.system.name, 'Test server');
  assert.ok(calls.includes('/v1/libraries') && calls.includes('/v1/direct') && calls.includes('/v1/system'));
});

test('a library directory that times out never stops sign-in: the libraries are unavailable, not empty, and authority answers still stop', async () => {
  const api = (libraries: () => unknown) => ({request: async (path: string) => {
    if (path === '/v1/libraries') return libraries();
    if (path === '/v1/direct') return directRecord();
    if (path === '/v1/system') return {id: 'server-a', name: 'Test server', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
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
