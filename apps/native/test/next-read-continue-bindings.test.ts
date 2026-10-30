import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type LocalSession} from '../../../packages/client-core/src';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {sameAuthority, type ShellAuthority} from '../src-next/shell/model';
import {nativeReadRouteAllowed} from '../src-next/screens/read/platform';
import {NativeReadingContinueBindings, type NativeReadingContinuePage, type NativeReadingContinueRow} from './fixtures/superseded/reading-continue-bindings';
import type {NativeReadNavigationContext} from '../src-next/screens/read/eligibility';

const revision = 'a'.repeat(64);
type Call = Readonly<{url: URL; method: string; init: RequestInit}>;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
const row = (id: string, sourceId = 'source', inventoryAvailable = true): NativeReadingContinueRow => ({
  item: {id, libraryId: 'library', title: 'Stored title', format: 'pdf', sourceId, inventoryAvailable},
  progress: {location: {format: 'pdf', pageIndex: 3, progression: 0.25}, observedAt: 1_700_000_000_000, revision: 2,
    sourceId, sourceRevision: revision, stale: false},
});
const pageOf = (items: readonly NativeReadingContinueRow[], nextCursor?: string): NativeReadingContinuePage =>
  ({items: [...items], ...(nextCursor === undefined ? {} : {nextCursor})});
const versions = (inventoryAvailable = true) => ({chosenSourceId: 'source', versions: [{sourceId: 'source', inventoryAvailable, format: 'pdf', size: 1200}]});
const isCancelled = (error: unknown) => (error as {code?: string}).code === 'cancelled';

function fixture(reply: (call: Call) => Promise<Response> | Response, options: Readonly<{tv?: boolean}> = {}) {
  let authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'f'.repeat(64)};
  let session: LocalSession | undefined = {accessToken: 'throwaway-continue-test', expiresAt: '2100-01-01T00:00:00Z', sessionFamilyId: 'family', tokenGeneration: '1',
    authorizationHorizon: '2100-01-01T00:00:00Z', viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}};
  let current = true;
  const calls: Call[] = [];
  const navigation = new NativeShellNavigation(authority, () => authority,
    (route, origin) => nativeReadRouteAllowed(route, [{id: 'library', kind: 'book', name: 'Books'}], options.tv === true, origin));
  const context: NativeReadNavigationContext = {navigation, authority: () => authority, current: () => current, session: () => session,
    libraries: () => [{id: 'library', name: 'Books', kind: 'book'}],
    api: new HttpLocalApi('http://127.0.0.1:39135', 'throwaway-continue-test', async (url, init = {}) => {
      const call = {url: new URL(String(url)), method: init.method ?? 'GET', init}; calls.push(call); return reply(call);
    })};
  const resident = new Set<NativeReadingContinueRow>();
  const state = {residentChecks: 0};
  const bindings = new NativeReadingContinueBindings(context, {isTV: options.tv === true,
    resident: value => {state.residentChecks++; return resident.has(value);}});
  return {context, calls, bindings, navigation, state,
    admit: (...rows: NativeReadingContinueRow[]) => {resident.clear(); for (const value of rows) resident.add(value);},
    replaceViewer() {session = session && {...session, sessionFamilyId: 'other-family', viewer: {...session.viewer, profileId: 'other-profile'}};},
    replaceAuthority() {authority = {...authority, epoch: 'other-epoch', viewerFence: 'e'.repeat(64)};},
    retire() {current = false;}};
}
async function until(check: () => boolean) {
  for (let n = 0; n < 30 && !check(); n++) await new Promise<void>(resolve => setImmediate(resolve));
  assert.ok(check());
}

test('Continue is a canonical lazy passthrough with the exact optional query and cursor', async t => {
  const f = fixture(call => {
    assert.equal(call.url.pathname, '/v1/reading/continue');
    return call.url.searchParams.get('cursor') === 'abc' ? json(pageOf([row('one')], 'next_1')) : json(pageOf([]));
  });
  t.after(() => f.bindings.dispose());
  assert.equal(f.calls.length, 0, 'constructor performs no request');
  const first: NativeReadingContinuePage = await f.bindings.continue({libraryId: 'library', cursor: 'abc', limit: 2});
  assert.deepEqual([...f.calls[0]!.url.searchParams], [['libraryId', 'library'], ['cursor', 'abc'], ['limit', '2']]);
  assert.deepEqual(Object.keys(first).sort(), ['items', 'nextCursor']);
  assert.equal(first.nextCursor, 'next_1');
  assert.equal(first.items[0]!.item.id, 'one');
  assert.equal(first.items[0]!.progress.sourceRevision, revision);
  assert.ok(Object.isFrozen(first) && Object.isFrozen(first.items[0]) && Object.isFrozen(first.items[0]!.item));
  const second = await f.bindings.continue();
  assert.equal(f.calls.length, 2, 'a second page is fetched, never replayed from a local cache');
  assert.equal(f.calls[1]!.url.search, '', 'omitted options send no invented limit');
  assert.equal('nextCursor' in second, false);
  assert.ok(f.calls.every(call => call.method === 'GET'));
});

test('open reads the exact resident row and leaves the canonical source facts untouched', async t => {
  const f = fixture(call => {
    if (call.url.pathname === '/v1/reading/continue') return json(pageOf([row('item')]));
    if (call.url.pathname.endsWith('/reading/versions')) return json(versions());
    throw new Error('unexpected ' + call.url.pathname);
  });
  t.after(() => f.bindings.dispose());
  const loaded = await f.bindings.continue();
  const actual = loaded.items[0]!, before = JSON.stringify(actual);
  f.admit(actual);
  assert.equal(await f.bindings.open(actual), true);
  assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'read', itemId: 'item', sourceId: 'source'});
  assert.equal(f.calls.length, 2);
  assert.ok(f.calls[1]!.url.pathname.endsWith('/items/item/reading/versions'));
  assert.ok(f.calls.every(call => call.method === 'GET'), 'no session prepare, progress write or replacement request');
  assert.equal(JSON.stringify(actual), before);
  assert.ok(Object.isFrozen(actual) && Object.isFrozen(actual.progress) && Object.isFrozen(actual.progress.location));
});

test('forged, evicted and unavailable rows are refused before the Read door', async t => {
  const f = fixture(call => {
    if (call.url.pathname === '/v1/reading/continue') return json(pageOf([row('item')]));
    if (call.url.pathname.endsWith('/reading/versions')) return json(versions());
    throw new Error('unexpected ' + call.url.pathname);
  });
  t.after(() => f.bindings.dispose());
  const old = (await f.bindings.continue()).items[0]!;
  const fresh = (await f.bindings.continue()).items[0]!;
  assert.deepEqual(old, fresh, 'the previous row is equal-looking but no longer the resident object');
  f.admit(fresh);
  let calls = f.calls.length;
  assert.equal(await f.bindings.open(old), false, 'an evicted page row is never opened by id');
  assert.equal(await f.bindings.open(structuredClone(fresh)), false, 'an equal-looking copy is not the resident object');
  assert.equal(await f.bindings.open(structuredClone(row('foreign', 'other'))), false);
  assert.equal(f.calls.length, calls, 'membership refusals perform no Read IO');
  const gone = row('gone', 'source', false);
  f.admit(gone);
  assert.equal(await f.bindings.open(gone), false, 'an unavailable publication is rejected from its own fact');
  assert.equal(f.calls.length, calls);
  f.admit(fresh);
  assert.equal(await f.bindings.open(fresh), true);
  assert.equal(f.calls.length, calls + 1);
});

test('a source that became unavailable is refused by the existing Read door without navigation', async t => {
  const f = fixture(call => {
    if (call.url.pathname === '/v1/reading/continue') return json(pageOf([row('item')]));
    if (call.url.pathname.endsWith('/reading/versions')) return json(versions(false));
    throw new Error('unexpected ' + call.url.pathname);
  });
  t.after(() => f.bindings.dispose());
  const actual = (await f.bindings.continue()).items[0]!;
  f.admit(actual);
  assert.equal(await f.bindings.open(actual), false);
  assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'shell', destination: {kind: 'home'}});
  assert.equal(f.calls.length, 2, 'only the eligibility read, never a session prepare');
});

test('retired context, replaced viewer and changed authority refuse before IO', async t => {
  for (const retire of ['context', 'viewer', 'authority'] as const) {
    const f = fixture(call => {
      if (call.url.pathname === '/v1/reading/continue') return json(pageOf([row('item')]));
      if (call.url.pathname.endsWith('/reading/versions')) return json(versions());
      throw new Error('unexpected ' + call.url.pathname);
    });
    t.after(() => f.bindings.dispose());
    const actual = (await f.bindings.continue()).items[0]!;
    f.admit(actual);
    if (retire === 'context') f.retire(); else if (retire === 'viewer') f.replaceViewer(); else f.replaceAuthority();
    const calls = f.calls.length;
    await assert.rejects(f.bindings.continue(), isCancelled);
    assert.equal(await f.bindings.open(actual), false);
    assert.equal(f.calls.length, calls, retire + ' never reaches the network');
    assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'shell', destination: {kind: 'home'}});
  }
});

test('television refuses Continue and open before membership or any IO', async t => {
  const f = fixture(() => {
    throw new Error('no television Read request');
  }, {tv: true});
  t.after(() => f.bindings.dispose());
  await assert.rejects(f.bindings.continue({limit: 1}), isCancelled);
  assert.equal(await f.bindings.open(row('item')), false);
  assert.equal(f.state.residentChecks, 0);
  assert.equal(f.calls.length, 0);
});

test('dispose aborts a pending page and a late reply never becomes a replacement', async t => {
  let release!: (value: Response) => void, signal: AbortSignal | undefined;
  const pending = new Promise<Response>(resolve => {release = resolve;});
  const f = fixture(call => {signal = call.init.signal ?? undefined; return pending;});
  t.after(() => f.bindings.dispose());
  const load = f.bindings.continue();
  await until(() => signal !== undefined);
  f.bindings.dispose();
  assert.equal(signal?.aborted, true);
  await assert.rejects(load, isCancelled);
  release(json(pageOf([row('late')])));
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(f.calls.length, 1, 'cleanup issues no replacement request');
  assert.equal(await f.bindings.open(row('late')), false);
  await assert.rejects(f.bindings.continue(), isCancelled);
  assert.equal(f.calls.length, 1);
});

test('a caller cancel aborts the page and ignores its late reply', async t => {
  let release!: (value: Response) => void, signal: AbortSignal | undefined;
  const pending = new Promise<Response>(resolve => {release = resolve;});
  const f = fixture(call => {signal = call.init.signal ?? undefined; return pending;});
  t.after(() => f.bindings.dispose());
  const caller = new AbortController(), load = f.bindings.continue({}, caller.signal);
  await until(() => signal !== undefined);
  caller.abort();
  await assert.rejects(load, isCancelled);
  assert.equal(signal?.aborted, true);
  release(json(pageOf([row('late')])));
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(f.calls.length, 1);
});

test('a late source failure after cancellation cannot replace the cancelled page', async t => {
  let release!: (value: Response) => void, signal: AbortSignal | undefined;
  const pending = new Promise<Response>(resolve => {release = resolve;});
  const f = fixture(call => {signal = call.init.signal ?? undefined; return pending;});
  t.after(() => f.bindings.dispose());
  const load = f.bindings.continue(), cancelled = assert.rejects(load, isCancelled);
  await until(() => signal !== undefined);
  f.bindings.dispose();
  await cancelled;
  assert.equal(signal?.aborted, true);
  release(json({error: {code: 'internal'}}, 500));
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(f.calls.length, 1, 'a failed late reply issues no replacement request');
  assert.equal(await f.bindings.open(row('late')), false);
});

test('a canonical Continue source failure is surfaced unchanged, never as an empty page', async t => {
  const f = fixture(() => json({error: {code: 'internal'}}, 500));
  t.after(() => f.bindings.dispose());
  await assert.rejects(f.bindings.continue({limit: 1}),
    error => error instanceof ReadingError && error.code === 'internal' && error.status === 500);
  assert.equal(f.calls.length, 1);
});

test('an already-cancelled caller signal refuses before any Continue request', async t => {
  const f = fixture(() => {
    throw new Error('no request after cancellation');
  });
  t.after(() => f.bindings.dispose());
  const caller = new AbortController();
  caller.abort();
  await assert.rejects(f.bindings.continue({limit: 1}, caller.signal), isCancelled);
  assert.equal(f.calls.length, 0);
});

test('disposing during an open aborts eligibility and never navigates late', async t => {
  let release!: (value: Response) => void, signal: AbortSignal | undefined;
  const pending = new Promise<Response>(resolve => {release = resolve;});
  const f = fixture(call => {
    if (call.url.pathname === '/v1/reading/continue') return json(pageOf([row('item')]));
    signal = call.init.signal ?? undefined;
    return pending;
  });
  t.after(() => f.bindings.dispose());
  const actual = (await f.bindings.continue()).items[0]!;
  f.admit(actual);
  const opening = f.bindings.open(actual);
  await until(() => signal !== undefined);
  f.bindings.dispose();
  assert.equal(signal?.aborted, true);
  assert.equal(await opening, false);
  release(json(versions()));
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'shell', destination: {kind: 'home'}});
  assert.equal(f.calls.some(call => call.method !== 'GET'), false);
});

test('a replaced owner cannot navigate an in-flight open and resolves false', async t => {
  let release!: (value: Response) => void, signal: AbortSignal | undefined;
  const pending = new Promise<Response>(resolve => {release = resolve;});
  const f = fixture(call => {
    if (call.url.pathname === '/v1/reading/continue') return json(pageOf([row('item')]));
    signal = call.init.signal ?? undefined;
    return pending;
  });
  t.after(() => f.bindings.dispose());
  const actual = (await f.bindings.continue()).items[0]!;
  f.admit(actual);
  const opening = f.bindings.open(actual);
  await until(() => signal !== undefined);
  f.replaceViewer();
  release(json(versions()));
  assert.equal(await opening, false, 'a replaced viewer cancels the old owner instead of throwing');
  assert.equal(signal?.aborted, true);
  assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'shell', destination: {kind: 'home'}});
});

test('one owner disposal never aborts another owner canonical page', async t => {
  const pending: {resolve: (value: Response) => void; signal?: AbortSignal}[] = [];
  const f = fixture(call => {
    const entry = {resolve: (_value: Response) => {}, signal: call.init.signal ?? undefined};
    pending.push(entry);
    return new Promise<Response>(resolve => {entry.resolve = resolve;});
  });
  t.after(() => f.bindings.dispose());
  const other = new NativeReadingContinueBindings(f.context, {isTV: false, resident: () => true});
  t.after(() => other.dispose());
  const first = f.bindings.continue(), second = other.continue();
  await until(() => pending.length === 2 && pending.every(entry => entry.signal !== undefined));
  f.bindings.dispose();
  await assert.rejects(first, isCancelled);
  assert.equal(pending[1]!.signal?.aborted, false, 'sibling cleanup is not this owner retirement');
  pending[1]!.resolve(json(pageOf([row('other')])));
  const page = await second;
  assert.equal(page.items[0]!.item.id, 'other');
  assert.equal(f.calls.length, 2);
});

test('sparse options and opaque cursor/library/source facts pass through unrewritten', async t => {
  const cursor = 'opaque_' + 'a'.repeat(121), next = 'next_' + 'B'.repeat(120) + '-_', libraryId = 'lib_A-b9';
  const envelope = {
    items: [{
      item: {id: 'book_1', libraryId: 'library_other', title: 'Stored title', format: 'epub', sourceId: 'source_A-b9', inventoryAvailable: true},
      progress: {location: {cfi: 'epubcfi(/6/4[chap01]!/4/2/2:0)', format: 'epub', progression: 0.42, section: 'OEBPS/chapter1.xhtml'},
        observedAt: 1_700_000_000_123, revision: 9, sourceId: 'source_A-b9', sourceRevision: 'b'.repeat(64), stale: false},
    }],
    nextCursor: next,
  };
  const f = fixture(call => {
    assert.equal(call.url.pathname, '/v1/reading/continue');
    return call.url.searchParams.get('cursor') === cursor ? json(envelope) : json({items: []});
  });
  t.after(() => f.bindings.dispose());
  const page = await f.bindings.continue({libraryId, cursor, limit: 7});
  assert.deepEqual([...f.calls[0]!.url.searchParams], [['libraryId', libraryId], ['cursor', cursor], ['limit', '7']]);
  assert.deepEqual(JSON.parse(JSON.stringify(page)), envelope, 'the canonical decoded envelope is returned exactly');
  assert.equal(page.nextCursor, next);
  const sparse = await f.bindings.continue({libraryId: undefined, cursor: undefined, limit: undefined});
  assert.equal(f.calls[1]!.url.search, '', 'explicit undefined options invent no query value');
  assert.deepEqual(JSON.parse(JSON.stringify(sparse)), {items: []});
  assert.ok(Object.isFrozen(page) && Object.isFrozen(page.items[0]!.progress.location));
});

test('the captured original authority is what a later Continue still belongs to', async t => {
  const f = fixture(call => call.url.pathname === '/v1/reading/continue' ? json(pageOf([row('one')])) : json(versions()));
  t.after(() => f.bindings.dispose());
  const first = await f.bindings.continue({limit: 1});
  assert.equal(first.items[0]!.item.id, 'one');
  assert.ok(sameAuthority(f.context.authority(), f.navigation.getSnapshot().authority));
  f.admit(first.items[0]!);
  assert.equal(await f.bindings.open(first.items[0]!), true);
  assert.equal(f.calls.length, 2);
  assert.ok(f.calls.every(call => call.method === 'GET' && call.init.credentials === 'omit' && call.init.redirect === 'error'));
});
