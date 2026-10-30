import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type LocalSession} from '../../../packages/client-core/src';
import {NativeShellNavigation, type NativeShellRoute} from '../src-next/shell/routes';
import {nativeReadRouteAllowed} from '../src-next/screens/read/platform';
import {openNativeRead} from '../src-next/screens/read/eligibility';
import {NativeReadTitleState, type NativeReadTitleContext, type NativeReadTitleRoute} from './fixtures/superseded/read-title-state';

const revision = 'a'.repeat(64);
const version = (sourceId: string, inventoryAvailable = true) => ({sourceId, inventoryAvailable, format: 'pdf', size: 1200});
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
type Call = Readonly<{url: URL; method: string; init: RequestInit}>;
function fixture(reply: (call: Call) => Promise<Response> | Response, tv = false) {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'f'.repeat(64)};
  let session: LocalSession | undefined = {accessToken: 'throwaway-read-test', expiresAt: '2100-01-01T00:00:00Z', sessionFamilyId: 'family', tokenGeneration: '1',
    authorizationHorizon: '2100-01-01T00:00:00Z', viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}};
  const calls: Call[] = [], navigation = new NativeShellNavigation(authority, () => authority,
    (route, origin) => nativeReadRouteAllowed(route, [{id: 'library', kind: 'book', name: 'Books'}], tv, origin));
  const library: NativeShellRoute = {kind: 'shell', destination: {kind: 'library', libraryId: 'library'}};
  const route: NativeReadTitleRoute = {kind: 'title', entry: {id: 'item', title: 'Stored title', kind: 'book', libraryId: 'library'}};
  if (!tv) {navigation.open(library, authority); navigation.open(route, authority);}
  const context: NativeReadTitleContext = {navigation, authority: () => authority, current: () => true, session: () => session,
    libraries: () => [{id: 'library', name: 'Books', kind: 'book'}],
    api: new HttpLocalApi('http://127.0.0.1:39131', 'throwaway-read-test', async (url, init = {}) => {
      const call = {url: new URL(String(url)), method: init.method ?? 'GET', init}; calls.push(call); return reply(call);
    })};
  const parent = new AbortController(), state = new NativeReadTitleState(context, route, parent.signal);
  return {context, calls, navigation, route, library, state, parent,
    replaceViewer() {session = session && {...session, sessionFamilyId: 'other-family', viewer: {...session.viewer, profileId: 'other-profile'}};}};
}
function controls(versions = [version('source')], onProgress?: (call: Call) => Promise<Response> | Response) {
  return (call: Call) => {
    if (call.url.pathname.endsWith('/reading/versions')) return json({versions, chosenSourceId: (versions.find(v => v.inventoryAvailable) ?? versions[0])?.sourceId});
    if (call.method === 'POST') {
      const {itemId, sourceId} = JSON.parse(String(call.init.body));
      return json({sessionId: 'session_' + sourceId, itemId, sourceId, revision, format: 'pdf', size: 1200, metadata: {title: 'Catalogue title'}});
    }
    if (call.url.pathname.endsWith('/progress')) return onProgress?.(call) ?? json({progress: null});
    if (call.method === 'DELETE') return new Response(null, {status: 204});
    throw new Error('unexpected read detail request');
  };
}
async function until(check: () => boolean) {for (let n = 0; n < 30 && !check(); n++) await new Promise<void>(resolve => setImmediate(resolve)); assert.ok(check());}

test('Read title is lazy, uses genuine sole publication facts, closes metadata session and opens the exact source with library Back', async t => {
  const f = fixture(controls()); t.after(() => f.state.dispose());
  assert.equal(f.calls.length, 0); await f.state.load();
  const snapshot = f.state.getSnapshot();
  assert.equal(snapshot.phase, 'reading'); assert.equal(snapshot.sourceId, 'source');
  assert.equal(snapshot.manifest?.metadata.title, 'Catalogue title'); assert.equal(snapshot.manifest?.metadata.author, undefined);
  assert.equal(snapshot.progress?.progress, null); assert.equal(snapshot.busy, false);
  assert.equal(f.calls.filter(c => c.method === 'DELETE').length, 1);
  assert.equal(await f.state.read(), true);
  assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'read', itemId: 'item', sourceId: 'source'});
  assert.equal(f.navigation.back(f.context.authority()), true); assert.equal(f.navigation.getSnapshot().route, f.route);
  assert.equal(f.navigation.back(f.context.authority()), true); assert.equal(f.navigation.getSnapshot().route, f.library);
  assert.equal(f.calls.filter(c => c.method === 'POST').length, 1, 'Read navigation does not prepare another metadata session');
  assert.ok(f.calls.every(c => c.method !== 'PATCH' && c.method !== 'PUT'), 'detail never writes Settings or progress');
});

test('only genuine 404 or empty versions chooses ordinary detail; access and service errors remain Read errors', async t => {
  for (const status of [200, 404, 403, 503]) {
    const f = fixture(() => json(status === 200 ? {versions: []} : {error: {code: 'not_found'}}, status)); t.after(() => f.state.dispose());
    await f.state.load(); assert.equal(f.state.getSnapshot().phase, status === 200 || status === 404 ? 'ordinary' : 'error');
    assert.equal(await f.state.read(), false); assert.equal(f.calls.length, 1);
    if (status === 403 || status === 503) assert.equal((f.state.getSnapshot().error as ReadingError).status, status);
  }
});

test('multiple real versions require a selected available source and never infer the first or an unavailable source', async t => {
  const f = fixture(controls([version('one'), version('two'), version('gone', false)])); t.after(() => f.state.dispose());
  await f.state.load(); assert.equal(f.state.getSnapshot().sourceId, undefined); assert.equal(f.calls.length, 1);
  await f.state.choose('unknown'); await f.state.choose('gone'); assert.equal(f.calls.length, 1);
  assert.equal(await f.state.read(), false); await f.state.choose('two');
  assert.equal(f.state.getSnapshot().manifest?.sourceId, 'two'); assert.equal(f.calls.filter(c => c.method === 'POST').length, 1);
});

test('a changed source selection aborts old progress, closes both captured sessions once and ignores late old facts', async t => {
  let release!: (r: Response) => void;
  const late = new Promise<Response>(resolve => {release = resolve;}); let oldSignal: AbortSignal | null | undefined;
  const f = fixture(controls([version('one'), version('two')], call => {
    if (call.url.pathname.includes('session_one')) {oldSignal = call.init.signal; return late;}
    return json({progress: null});
  })); t.after(() => f.state.dispose());
  await f.state.load(); const first = f.state.choose('one'); await until(() => !!oldSignal);
  await f.state.choose('two'); await first; assert.equal(oldSignal?.aborted, true);
  const snapshot = f.state.getSnapshot(); release(json({progress: null})); await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.state.getSnapshot(), snapshot); assert.equal(snapshot.manifest?.sourceId, 'two');
  assert.equal(f.calls.filter(c => c.method === 'DELETE' && c.url.pathname.endsWith('session_one')).length, 1);
  assert.equal(f.calls.filter(c => c.method === 'DELETE' && c.url.pathname.endsWith('session_two')).length, 1);
});

test('navigation or parent retirement aborts progress and closes the captured session without publishing late facts', async t => {
  for (const retirement of ['navigation', 'parent'] as const) {
    let release!: (r: Response) => void, signal: AbortSignal | null | undefined;
    const pending = new Promise<Response>(resolve => {release = resolve;});
    const f = fixture(controls(undefined, call => {signal = call.init.signal; return pending;})); t.after(() => f.state.dispose());
    const load = f.state.load(); await until(() => !!signal); const before = f.state.getSnapshot();
    if (retirement === 'parent') f.parent.abort(); else f.navigation.open({kind: 'account'}, f.context.authority());
    await f.state.dispose(); await load; assert.equal(signal?.aborted, true);
    release(json({progress: null})); await new Promise(resolve => setImmediate(resolve));
    assert.equal(f.state.getSnapshot(), before); assert.equal(f.calls.filter(c => c.method === 'DELETE').length, 1);
    assert.equal(await f.state.read(), false);
  }
});

test('replacement viewer cannot receive old title facts, retarget a retry or send old-session cleanup', async t => {
  let release!: (r: Response) => void;
  const pending = new Promise<Response>(resolve => {release = resolve;});
  const f = fixture(controls(undefined, () => pending)); t.after(() => f.state.dispose());
  const load = f.state.load(); await until(() => f.calls.some(c => c.url.pathname.endsWith('/progress')));
  const before = f.state.getSnapshot(); f.replaceViewer(); release(json({progress: null})); await load;
  await f.state.load(); await f.state.dispose();
  assert.equal(f.state.getSnapshot(), before); assert.equal(f.calls.filter(c => c.method === 'DELETE').length, 0);
  assert.equal(f.calls.filter(c => c.method === 'POST').length, 1); assert.equal(await f.state.read(), false);
});

test('retired parent, inactive title and TV admission perform no Read network requests', async t => {
  const f = fixture(controls()); t.after(() => f.state.dispose()); f.parent.abort(); await f.state.load(); assert.equal(f.calls.length, 0);
  const inactive = fixture(controls()); t.after(() => inactive.state.dispose()); inactive.navigation.open({kind: 'account'}, inactive.context.authority());
  await inactive.state.load(); assert.equal(inactive.calls.length, 0);
  const tv = fixture(controls(), true); t.after(() => tv.state.dispose());
  assert.equal(await openNativeRead(tv.context, {itemId: 'item', sourceId: 'source'}), false); assert.equal(tv.calls.length, 0);
});
