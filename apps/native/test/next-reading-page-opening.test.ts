import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type Library, type LocalSession} from '../../../packages/client-core/src';
import type {NativeReadNavigationContext} from '../src-next/screens/read/eligibility';
import {nativeReadingPageOpening, nativeReadingDownloadOffer, nativeReadingRowLifetime, NativeReadingDownloads} from '../src-next/screens/read/reading-page-opening';
import {createWindowedCollection} from '../../../packages/client-core/src/collections/windowed';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import {NativeLibraryDestination, type NativeLibrarySnapshot} from '../src-next/screens/library/model';
import type {NativeDownloadRequest} from '../src-next/screens/downloads/request';
import {NativeShellNavigation} from '../src-next/shell/routes';
import type {ShellAuthority} from '../src-next/shell/model';

// Authored regression vectors only. No build, test or device run is a receipt.
const capability = (id = 'books', kind = 'book') => ({
  library: {id, kind, name: 'Books', defaultView: 'grid', pinned: false}, pivots: [], resolvedPivot: null, fields: [], sorts: [], quickFilters: [],
  queryLimits: {maximumDepth: 8, maximumClauses: 100, maximumBytes: 4096, maximumSorts: 3, defaultLimit: 20, maximumLimit: 100, cursorTtlMs: 1000},
});
const json = (value: unknown) => new Response(JSON.stringify(value), {headers: {'Content-Type': 'application/json'}});
type Call = Readonly<{url: URL; signal: RequestInit['signal']}>;
function fixture(reply: (call: Call) => Response | Promise<Response> = () => json(capability())) {
  let authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'f'.repeat(64)};
  let session: LocalSession | undefined = {accessToken: 'throwaway-page-test', expiresAt: '2100-01-01T00:00:00Z', sessionFamilyId: 'family', tokenGeneration: '1',
    authorizationHorizon: '2100-01-01T00:00:00Z', viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}};
  let active = true, libraries: readonly Library[] = [{id: 'books', kind: 'book', name: 'Books'}];
  const calls: Call[] = [], navigation = new NativeShellNavigation(authority, () => authority);
  navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: 'books'}}, authority);
  const api = new HttpLocalApi('http://127.0.0.1:39135', 'throwaway-page-test', async (url, init = {}) => {
    const call = {url: new URL(String(url)), signal: init.signal}; calls.push(call); return reply(call);
  });
  const context: NativeReadNavigationContext = {api, navigation, authority: () => authority, session: () => session,
    current: () => active, libraries: () => libraries};
  const open = () => nativeReadingPageOpening(context, {libraryId: 'books', kind: 'book'}, navigation.getSnapshot());
  return {context, navigation, calls, open,
    home: () => navigation.open({kind: 'shell', destination: {kind: 'home'}}, authority),
    back: () => navigation.back(authority),
    family: () => {session = session && {...session, sessionFamilyId: 'successor'};},
    scope: () => {authority = {...authority, epoch: 'successor'};},
    kind: () => {libraries = [{id: 'books', kind: 'comic', name: 'Comics'}];},
    retire: () => {active = false;}};
}
const cancelled = (error: unknown) => (error as {code?: string}).code === 'cancelled';

function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {resolve, promise};}

test('opening reads the actual capability route through its original API and signal', async () => {
  const f = fixture(), opening = f.open()!;
  assert.ok(opening.isCurrent()); assert.equal(f.calls.length, 0);
  const result = await opening.capabilities();
  assert.equal(result.library.id, 'books'); assert.equal(result.library.kind, 'book');
  assert.equal(f.calls.length, 1);
  assert.equal(f.calls[0]!.url.pathname, '/v1/libraries/books/browse-capabilities');
  assert.equal(f.calls[0]!.url.searchParams.get('pivot'), 'books');
  assert.ok(f.calls[0]!.signal, 'canonical HTTP retains a bounded transport signal');
  opening.dispose();
  await assert.rejects(opening.capabilities(), cancelled);
  assert.equal(f.calls.length, 1, 'retired opening cannot dispatch again');
});

test('a capability for another id or kind never becomes an admitted page', async () => {
  for (const response of [capability('other'), capability('books', 'comic')]) {
    const f = fixture(() => json(response)), opening = f.open()!;
    await assert.rejects(opening.capabilities(), /native_reading_capability_scope/);
    opening.dispose();
  }
});

test('late capability is refused after Back even when the same route object returns', async () => {
  const pending = deferred<Response>(), f = fixture(() => pending.promise), opening = f.open()!;
  const old = opening.capabilities(); f.home(); f.back();
  const successor = f.open()!;
  assert.notEqual(successor.origin.revision, opening.origin.revision);
  pending.resolve(json(capability()));
  await assert.rejects(old, cancelled);
  assert.equal(opening.isCurrent(), false); assert.ok(successor.isCurrent());
  opening.dispose(); assert.equal(successor.signal.aborted, false);
  successor.dispose();
});

test('family, authority, library kind and context retirement each refuse the original opening', async () => {
  for (const change of ['family', 'scope', 'kind', 'retire'] as const) {
    const f = fixture(), opening = f.open()!; f[change]();
    assert.equal(opening.isCurrent(), false, change);
    await assert.rejects(opening.capabilities(), cancelled);
    assert.equal(f.calls.length, 0); opening.dispose();
  }
});

test('minting refuses a render from a different route or an unadmitted library kind', () => {
  const f = fixture(), rendered = f.navigation.getSnapshot(); f.home();
  assert.equal(nativeReadingPageOpening(f.context, {libraryId: 'books', kind: 'book'}, rendered), undefined);
  assert.equal(nativeReadingPageOpening(f.context, {libraryId: 'books', kind: 'movie'}, f.navigation.getSnapshot()), undefined);
});

test('Download keeps exact resident entry and original row signal, never an equal-id replacement', () => {
  const f = fixture(), opening = f.open()!, row = {id: 'one', title: 'Original'}, rowLife = new AbortController();
  let resident = row;
  const offer = nativeReadingDownloadOffer(opening, row, () => resident === row, {signal: rowLife.signal, isCurrent: () => !rowLife.signal.aborted})!;
  assert.equal(offer.lifetime.signal, rowLife.signal); assert.ok(offer.lifetime.isCurrent());
  row.title = 'Later display';
  assert.equal(offer.item.title, 'Original'); assert.ok(Object.isFrozen(offer.item));
  resident = {...row};
  assert.equal(offer.lifetime.isCurrent(), false);
  assert.equal(nativeReadingDownloadOffer(opening, row, () => resident === row), undefined);
  opening.dispose();
});

test('page and row retirement each disable the original sheet lifetime', () => {
  for (const retire of ['page', 'row'] as const) {
    const f = fixture(), opening = f.open()!, life = new AbortController();
    const offer = nativeReadingDownloadOffer(opening, {id: 'one', title: 'One'}, () => true, {signal: life.signal, isCurrent: () => true})!;
    if (retire === 'page') opening.dispose(); else life.abort();
    assert.equal(offer.lifetime.isCurrent(), false); opening.dispose();
  }
});

test('a forged opening cannot issue a Download offer', () => {
  const f = fixture(), opening = f.open()!;
  assert.equal(nativeReadingDownloadOffer({...opening}, {id: 'one', title: 'One'}, () => true), undefined);
  opening.dispose();
});

function downloads() {
  const f = fixture(), opening = f.open()!, requests: NativeDownloadRequest[] = [];
  let available = true;
  const menu = new NativeReadingDownloads(opening, () => available, request => {requests.push(request);});
  const row = {id: 'one', title: 'One'}, offer = nativeReadingDownloadOffer(opening, row, () => true)!;
  return {...f, opening, menu, requests, offer, unavailable: () => {available = false;}};
}

test('select dispatches one exact reading request and preserves lifetime after menu consumption', () => {
  const f = downloads(); f.menu.offer(() => f.offer); f.menu.select(f.offer); f.menu.select(f.offer);
  assert.equal(f.menu.getSnapshot(), undefined); assert.equal(f.requests.length, 1);
  assert.deepEqual(f.requests[0], {title: 'One', itemId: 'one', reading: true, lifetime: f.offer.lifetime});
  assert.ok(f.requests[0]!.lifetime!.isCurrent());
  f.opening.dispose(); assert.equal(f.requests[0]!.lifetime!.isCurrent(), false); f.menu.dispose();
});

test('stale close/select cannot consume a successor menu and forged offers are refused', () => {
  const f = downloads(), next = nativeReadingDownloadOffer(f.opening, {id: 'two', title: 'Two'}, () => true)!;
  f.menu.offer(() => ({...f.offer})); assert.equal(f.menu.getSnapshot(), undefined);
  f.menu.offer(() => f.offer); f.menu.offer(() => next);
  f.menu.dismiss(f.offer); f.menu.select(f.offer);
  assert.equal(f.menu.getSnapshot(), next); assert.equal(f.requests.length, 0);
  f.menu.select(next); assert.equal(f.requests[0]!.itemId, 'two'); f.menu.dispose(); f.opening.dispose();
});

test('claim precedes capture, availability and publication reentrancy', () => {
  const f = fixture(), opening = f.open()!, offer = nativeReadingDownloadOffer(opening, {id: 'one', title: 'One'}, () => true)!;
  const requests: NativeDownloadRequest[] = []; let captureCount = 0;
  const nested = () => {captureCount++; return offer;};
  const menu: NativeReadingDownloads = new NativeReadingDownloads(opening, () => {menu.offer(nested); menu.select(offer); return true;}, value => {requests.push(value);});
  menu.subscribe(() => {menu.offer(nested); menu.select(offer);});
  menu.offer(() => {menu.offer(nested); return offer;});
  assert.equal(captureCount, 0); assert.equal(requests.length, 0);
  menu.select(offer); assert.equal(requests.length, 1); assert.equal(captureCount, 0);
  menu.dispose(); opening.dispose();
});

test('retirement during publication prevents dispatch without clearing a different page store', () => {
  const f = downloads(), successor = downloads();
  successor.menu.offer(() => successor.offer);
  f.menu.offer(() => f.offer);
  f.menu.subscribe(() => f.menu.dispose());
  f.menu.select(f.offer);
  assert.equal(f.requests.length, 0); assert.equal(successor.menu.getSnapshot(), successor.offer);
  f.menu.dispose(); f.opening.dispose(); successor.menu.dispose(); successor.opening.dispose();
});

test('unavailable and retired menus cannot dispatch or reacquire a fresh row', () => {
  const f = downloads(); f.unavailable(); f.menu.offer(() => f.offer);
  assert.equal(f.menu.getSnapshot(), undefined);
  f.menu.dispose(); let captures = 0;
  f.menu.offer(() => {captures++; return f.offer;}); f.menu.select(f.offer);
  assert.equal(captures, 0); assert.equal(f.requests.length, 0); f.opening.dispose();
});


async function residentRow() {
  const f = fixture(), opening = f.open()!, life = new AbortController();
  const entry: ContentEntry = Object.freeze({id: 'one', libraryId: 'books', kind: 'ebook', title: 'One'});
  const collection = createWindowedCollection<ContentEntry>({fetchPage: async () => ({items: [entry], total: 1}), pageSize: 1, maxResidentPages: 2, keyOf: value => value.id});
  const ready = new Promise<void>(resolve => {
    const unsubscribe = collection.subscribe(() => {if (collection.itemAt(0) === entry) {unsubscribe(); resolve();}});
  });
  collection.ensureRange(0, 0); await ready;
  const destination = new NativeLibraryDestination({api: f.context.api, library: f.context.libraries()[0]!,
    scope: {serverId: 'server', viewerId: 'viewer'}, current: opening.isCurrent, tab: {id: 'browse', label: 'library.tab.books', pivot: 'books', shows: 'titles'}});
  const drawn: NativeLibrarySnapshot = Object.freeze({...destination.getSnapshot(), phase: 'ready', collection,
    sort: Object.freeze({field: 'title', direction: 'asc'}), predicates: Object.freeze([])});
  let live = drawn, held = true;
  const offered = nativeReadingRowLifetime(opening, entry, drawn, () => live, {signal: life.signal, isCurrent: () => held});
  return {opening, collection, entry, drawn, offered, life,
    set: (next: NativeLibrarySnapshot) => {live = next;}, release: () => {held = false;},
    dispose: () => {life.abort(); destination.dispose(); collection.dispose(); opening.dispose();}};
}

test('retained sparse rows cannot act under a newer query while the old generation is still visible', async () => {
  const f = await residentRow();
  try {
    assert.ok(f.offered.isCurrent());
    const pending = deferred<{items: ContentEntry[]; total: number}>();
    f.collection.invalidate(() => pending.promise);
    assert.ok(f.collection.residentItems().includes(f.entry), 'old row intentionally remains on screen');
    f.set({...f.drawn, queryRevision: f.drawn.queryRevision + 1});
    assert.equal(f.offered.isCurrent(), false);
    pending.resolve({items: [], total: 0});
  } finally {f.dispose();}
});

test('row witness binds exact sort, filter, tab, collection and entry identity', async () => {
  const f = await residentRow();
  try {
    for (const next of [{...f.drawn, sort: {...f.drawn.sort!}}, {...f.drawn, predicates: []}, {...f.drawn, tab: {...f.drawn.tab}},
      {...f.drawn, collection: undefined}, {...f.drawn, phase: 'loading' as const}]) {
      f.set(next); assert.equal(f.offered.isCurrent(), false);
    }
    f.set(f.drawn); assert.ok(f.offered.isCurrent());
    const clone = nativeReadingRowLifetime(f.opening, {...f.entry}, f.drawn, () => f.drawn, {signal: f.life.signal, isCurrent: () => true});
    assert.equal(clone.isCurrent(), false);
    f.release(); assert.equal(f.offered.isCurrent(), false);
  } finally {f.dispose();}
});


test('retiring a page aborts its bounded transport without borrowing a successor opening', async () => {
  const pending = deferred<Response>();
  // This vector exercises transport cancellation: Fetch rejects when its original
  // signal aborts. Other vectors deliberately keep a late response pending.
  const f = fixture(({signal}) => new Promise<Response>((resolve, reject) => {
    const abort = () => reject(signal?.reason);
    signal?.addEventListener('abort', abort, {once: true});
    if (signal?.aborted) abort();
    void pending.promise.then(resolve, reject).finally(() => signal?.removeEventListener('abort', abort));
  })), opening = f.open()!;
  const reading = opening.capabilities();
  assert.equal(f.calls.length, 1); const original = f.calls[0]!.signal!;
  opening.dispose();
  assert.equal(original.aborted, true);
  await assert.rejects(reading, error => error === original.reason, 'the original transport rejects with its own abort reason');
  const successor = f.open()!; assert.ok(successor.isCurrent());
  pending.resolve(json(capability()));
  assert.equal(opening.isCurrent(), false); assert.equal(successor.signal.aborted, false);
  successor.dispose();
});
