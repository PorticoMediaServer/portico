import test from 'node:test';
import assert from 'node:assert/strict';
import {routePaths} from '../../contracts/src/route-paths.ts';
import {parseBrowsePreparing, readBrowsePage, readBrowseSelection, readReadingFacet} from '../src/browse-preparation.ts';
import {BrowseSession, type BrowseSessionOptions} from '../src/browse-session.ts';
import {parseBrowseCapabilities} from '../src/browse.ts';
import type {LibraryContentApi, ContentEntry} from '../src/library-content.ts';
import type {PreparationResponse, PreparationRuntime} from '../src/read-preparation.ts';

test('official generated preparation operation IDs are installed', () => {
  for (const id of ['get_library_reading_facet_query', 'delete_library_reading_facet_query',
    'get_library_reading_browse_query', 'delete_library_reading_browse_query',
    'get_library_browse_selection_query', 'delete_library_browse_selection_query'] as const) {
    assert.equal(typeof routePaths[id], 'string', id);
    assert.match(routePaths[id], /^\/v1\/libraries\/\{id\}\//);
  }
});
const signal = () => new AbortController().signal;
const tick = async () => {for (let n = 0; n < 20; n++) await Promise.resolve();};
const ready = (body: unknown, headers: Record<string, string> = {}): PreparationResponse => ({status: 200, headers, body});
const pending = (kind: string, id = 'query-1', retryAfterMs = 0): PreparationResponse => ({status: 202, headers: {}, body: {variant: kind === 'reading-facet' ? 'reading-preparing' : 'preparing', kind: `${kind}-preparing`, state: 'preparing', queryId: id, retryAfterMs}});
function clock() {
  let now = 0;
  const timers = new Map<number, {at: number; callback: () => void}>(); let sequence = 0;
  const runtime: PreparationRuntime = {now: () => now, random: () => 0, setTimeout: (callback, delay) => {const id = ++sequence; timers.set(id, {at: now + delay, callback}); return id;}, clearTimeout: id => {timers.delete(id as number);}};
  return {runtime, timers, async advance(ms: number) {now += ms; for (const [id, timer] of timers) if (timer.at <= now) {timers.delete(id); timer.callback();} await tick();}};
}
const facet = {variant: 'page', field: 'author', total: 1, values: [{value: 'Author', label: 'Author', count: 3}]};
const path = '/v1/libraries/lib/facets?field=author&pivot=books&limit=100';
const pivot = {id: 'books', labelKey: 'library.books', entityKinds: ['book'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid'], browsable: true, paging: 'offset'};
const caps = parseBrowseCapabilities({library: {id: 'lib', name: 'Books', kind: 'book', defaultView: 'discover'}, pivots: [pivot], resolvedPivot: pivot,
  fields: [], sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc'], defaultDirection: 'asc', applicableKinds: ['book']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}});
const query = {libraryId: 'lib', pivot: 'books', capabilities: caps, nodes: [], sorts: [{field: 'title' as const, direction: 'asc' as const}]};
function page(start = 0, extras: Record<string, unknown> = {}) {return {pivot: 'books', applied: {query: null, sort: query.sorts, seek: null},
  entries: [{id: `book-${start}`, kind: 'book', title: 'Book', libraryId: 'lib', navigation: {view: 'item'}}],
  pageInfo: {start, total: 4, revision: 'r1', hasMore: true, nextCursor: ''}, ...extras};}
const capabilityCounts = {play: 0, queue: 0, playlist: 0, myList: 4, watched: 4, collection: 4, favorite: 4, download: 0, edit: 4, refresh: 4, delete: 4};
function whole(extras: Record<string, unknown> = {}) {return {selector: {query: {libraryId: 'lib', pivot: 'books', filter: null, sort: query.sorts, selectionBasis: 'a'.repeat(64)}}, catalogRevision: 'r1', total: 4,
  types: [{kind: 'book', count: 4, capabilityCounts, libraries: [{libraryId: 'lib', count: 4}]}], ...extras};}
function session(answer: LibraryContentApi['requestRaw'], options: Partial<BrowseSessionOptions<ContentEntry>> = {}) {
  return new BrowseSession<ContentEntry>({query, request: async () => {throw new Error('body-only transport used');}, requestRaw: answer,
    current: () => true, item: entry => entry, ...options});
}

test('strict domain pending validation refuses cross-domain, malformed and unsafe handles', () => {
  for (const body of [null, {}, {...pending('reading-facet').body as object, kind: 'browse-selection-preparing'},
    {...pending('reading-facet').body as object, state: 'ready'}, {...pending('reading-facet').body as object, queryId: '../bad'},
    {...pending('reading-facet').body as object, retryAfterMs: -1}, {...pending('reading-facet').body as object, retryAfterMs: 1.5}]) {
    assert.throws(() => parseBrowsePreparing(body, 'reading-facet'), {code: 'invalid_preparing'});
  }
});
test('legacy successful ports remain usable and a body-only pending envelope fails closed', async () => {
  assert.equal((await readReadingFacet({request: async <T>() => facet as T}, 'lib', path, 'author', {signal: signal(), isCurrent: () => true})).total, 1);
  await assert.rejects(readReadingFacet({request: async <T>() => ({...facet, state: 'preparing'}) as T}, 'lib', path, 'author', {signal: signal(), isCurrent: () => true}));
  await assert.rejects(readReadingFacet({request: async <T>() => pending('reading-facet').body as T}, 'lib', path, 'author', {signal: signal(), isCurrent: () => true}));
  await assert.rejects(readBrowsePage({request: async <T>() => pending('reading-browse').body as T}, 'lib', {}, {signal: signal(), isCurrent: () => true}));
});
test('only 200 reaches a ready parser; terminal errors preserve status, code and all headers', async () => {
  const response = {status: 503, headers: {'Retry-After': '60', 'X-Proof': 'capacity'}, body: {error: {code: 'preparation_capacity', retry: 'same_request'}}};
  await assert.rejects(readReadingFacet({request: async () => {throw 0;}, requestRaw: async () => response}, 'lib', path, 'author', {isCurrent: () => true}),
    (error: any) => error.status === 503 && error.code === 'preparation_capacity' && error.retryable && error.response === response);
});
test('one 409 re-anchors the captured POST without its stale revision, then surfaces another refusal', async () => {
  const bodies: any[] = [], original = {pivot: 'books', sort: query.sorts, range: {start: 1, revision: 'old'}};
  const response = {status: 409, headers: {'X-Proof': 'stale'}, body: {error: {code: 'reading_revision_mismatch', retry: 'after_refresh'}}};
  const api: LibraryContentApi = {request: async () => {throw 0;}, requestRaw: async (_path, method, options) => {assert.equal(method, 'POST'); bodies.push(options?.body); return response;}};
  await assert.rejects(readBrowsePage(api, 'lib', original, {isCurrent: () => true}), (e: any) => e.status === 409 && e.response === response);
  assert.deepEqual(bodies, [original, {...original, range: {start: 1}}]); assert.ok(Object.isFrozen(bodies[0].sort));
});
test('Reading facets honor body and full Retry-After floors before GET, release after ready', async () => {
  const time = clock(), calls: string[] = []; let count = 0;
  const response = {...pending('reading-facet', 'query-1', 1800), headers: {'Retry-After': '2', Location: '/v1/libraries/lib/reading-facet-queries/query-1', 'X-Proof': 'preserved'}};
  const api: LibraryContentApi = {request: async () => {throw 0;}, requestRaw: async (target, method) => {calls.push(`${method} ${target}`); return count++ ? ready(facet) : response;}};
  const work = readReadingFacet(api, 'lib', path, 'author', {isCurrent: () => true, runtime: time.runtime});
  await tick(); await time.advance(2000); assert.equal(calls.length, 1);
  await time.advance(1); assert.equal((await work).total, 1);
  assert.deepEqual(calls.slice(1), ['GET /v1/libraries/lib/reading-facet-queries/query-1', 'DELETE /v1/libraries/lib/reading-facet-queries/query-1']);
});
test('initial Reading browse 202 polls GET and never publishes an empty successful page', async () => {
  const time = clock(), calls: string[] = []; let published = false;
  const s = session(async (target, method) => {calls.push(`${method} ${target}`); return method === 'POST' ? pending('reading-browse') : ready(page());}, {preparationRuntime: time.runtime, onPage: () => {published = true;}});
  const work = s.fetchPage(0, 1, signal()); await tick(); assert.equal(published, false);
  await time.advance(251); assert.equal((await work).items[0]?.id, 'book-0'); assert.equal(published, true);
  assert.match(calls[1]!, /^GET .*reading-browse-queries/); s.retire();
});
test('ordinary page resolves immediately; selection has its own lifetime and notifies late onWhole', async () => {
  const time = clock(), events: string[] = [], calls: string[] = [], controller = new AbortController();
  const s = session(async (target, method) => {calls.push(`${method} ${target}`); return method === 'POST' ? ready(page(0, {wholeSelectionPreparing: pending('browse-selection').body})) : ready(whole());},
    {preparationRuntime: time.runtime, onPage: () => events.push('page'), onWhole: () => events.push('whole')});
  const first = await s.fetchPage(0, 1, controller.signal); assert.equal(first.items.length, 1); assert.deepEqual(events, ['page']);
  controller.abort(); await tick(); assert.equal(calls.length, 1, 'completed fetch cancellation cannot cancel the aggregate');
  await time.advance(251); assert.deepEqual(events, ['page', 'whole']); assert.equal(s.wholeSelection()?.total, 4); s.retire();
});
test('retiring/query switching cancels pending selection and rejects a late ready response', async () => {
  const time = clock(), calls: string[] = [], events: unknown[] = []; let finish!: (value: PreparationResponse) => void;
  const s = session(async (target, method) => {calls.push(`${method} ${target}`); if (method === 'POST') return ready(page(0, {wholeSelectionPreparing: pending('browse-selection').body}));
    if (method === 'DELETE') return ready(undefined); return new Promise(resolve => {finish = resolve;});}, {preparationRuntime: time.runtime, onWhole: value => events.push(value)});
  await s.fetchPage(0, 1, signal()); await tick(); await time.advance(251); s.retire(); finish(ready(whole())); await tick();
  assert.equal(s.wholeSelection(), undefined); assert.deepEqual(events, []); assert.equal(calls.filter(call => call.startsWith('DELETE')).length, 1);
});
test('later pages never install a whole descriptor while the original aggregate is preparing', async () => {
  const time = clock(); let post = 0;
  const s = session(async (_target, method) => method === 'POST' ? ready(post++ ? page(1, {wholeSelection: whole({total: 0, types: []})}) : page(0, {wholeSelectionPreparing: pending('browse-selection').body})) : ready(whole()), {preparationRuntime: time.runtime});
  await s.fetchPage(0, 1, signal()); await s.fetchPage(1, 1, signal()); assert.equal(s.wholeSelection(), undefined); await tick();
  await time.advance(251); assert.equal(s.wholeSelection()?.total, 4); s.retire();
});
for (const changed of [{catalogRevision: 'other'}, {selector: {query: {libraryId: 'other', pivot: 'books', sort: query.sorts}}},
  {selector: {query: {libraryId: 'lib', pivot: 'movies', sort: query.sorts}}}, {selector: {query: {libraryId: 'lib', pivot: 'books', sort: [{field: 'title', direction: 'desc'}]}}},
  {selector: {query: {libraryId: 'lib', pivot: 'books', filter: {variant:'predicate',field: 'title', operator: 'equals', value: 'other'}, sort: query.sorts}}}]) {
  test(`prepared selection refuses changed original page basis: ${JSON.stringify(changed)}`, async () => {
    const time = clock(); let stale = false;
    const s = session(async (_target, method) => method === 'POST' ? ready(page(0, {wholeSelectionPreparing: pending('browse-selection').body})) : ready(whole(changed)), {preparationRuntime: time.runtime, onStale: () => {stale = true;}});
    await s.fetchPage(0, 1, signal()); await tick(); await time.advance(251); assert.equal(s.wholeSelection(), undefined); assert.equal(stale, true); s.retire();
  });
}
test('revocation during preparation remains an error and cancels known caller demand', async () => {
  const time = clock(), calls: string[] = []; let number = 0;
  const api: LibraryContentApi = {request: async () => {throw 0;}, requestRaw: async (target, method) => {calls.push(`${method} ${target}`); return number++ ? {status: 403, headers: {'X-Proof': 'revoked'}, body: {error: {code: 'forbidden'}}} : pending('reading-facet');}};
  const work = readReadingFacet(api, 'lib', path, 'author', {isCurrent: () => true, runtime: time.runtime}); const rejected = assert.rejects(work, (e: any) => e.status === 403 && e.code === 'forbidden');
  await tick(); await time.advance(251); await rejected; assert.match(calls.at(-1)!, /^DELETE /);
});
test('late initial Reading 202 after cancellation is released without publishing', async () => {
  const time = clock(), abort = new AbortController(), calls: string[] = []; let finish!: (value: PreparationResponse) => void;
  const api: LibraryContentApi = {request: async () => {throw 0;}, requestRaw: async (target, method) => {calls.push(`${method} ${target}`); if (method === 'DELETE') return ready(undefined); return new Promise(resolve => {finish = resolve;});}};
  const work = readReadingFacet(api, 'lib', path, 'author', {signal: abort.signal, isCurrent: () => true, runtime: time.runtime});
  const rejected = assert.rejects(work, {name: 'AbortError'}); abort.abort(); await rejected; finish(pending('reading-facet')); await tick(); assert.match(calls.at(-1)!, /^DELETE /);
});

test('409 cursor recovery re-anchors without a stale cursor and preserves query, sort and limit', async () => {
  const bodies: any[] = [], original = {pivot: 'books', sort: query.sorts, cursor: 'expired', limit: 40};
  const api: LibraryContentApi = {request: async () => {throw 0;}, requestRaw: async (_path, method, options) => {
    assert.equal(method, 'POST'); bodies.push(options?.body);
    return bodies.length === 1 ? {status: 409, headers: {}, body: {error: {code: 'reading_revision_mismatch'}}} : ready(page());
  }};
  await readBrowsePage(api, 'lib', original, {isCurrent: () => true});
  assert.deepEqual(bodies, [original, {pivot: 'books', sort: query.sorts, limit: 40}]);
  assert.equal(original.cursor, 'expired', 'the caller descriptor stays untouched');
});

test('BrowseSession works without AbortSignal.any and releases composed listeners on success and error', async () => {
  const originalAny = Object.getOwnPropertyDescriptor(AbortSignal, 'any')!;
  const add = AbortSignal.prototype.addEventListener, remove = AbortSignal.prototype.removeEventListener;
  const listeners = new Map<AbortSignal, Set<unknown>>();
  Object.defineProperty(AbortSignal, 'any', {...originalAny, value: undefined});
  AbortSignal.prototype.addEventListener = function(type: string, listener: EventListenerOrEventListenerObject | null, options?: AddEventListenerOptions | boolean) {
    if (type === 'abort') {const held = listeners.get(this) ?? new Set(); held.add(listener); listeners.set(this, held);}
    if (listener !== null) return add.call(this, type, listener, options);
  };
  AbortSignal.prototype.removeEventListener = function(type: string, listener: EventListenerOrEventListenerObject | null, options?: EventListenerOptions | boolean) {
    if (type === 'abort') listeners.get(this)?.delete(listener);
    if (listener !== null) return remove.call(this, type, listener, options);
  };
  try {
    for (const fail of [false, true]) for (const raw of [false, true]) {
      const input = new AbortController();
      const s = session(raw ? async () => {if (fail) throw new Error('offline'); return ready(page());} : undefined, {request: async () => {if (fail) throw new Error('offline'); return page();}});
      if (fail) await assert.rejects(s.fetchPage(0, 1, input.signal), /offline/);
      else assert.equal((await s.fetchPage(0, 1, input.signal)).items.length, 1);
      assert.equal(listeners.get(input.signal)?.size, 0, 'the completed page detaches its caller listener');
      assert.ok([...listeners.values()].every(value => value.size === 0), 'session and composed listeners are disposed');
      s.retire(); input.abort();
    }
  } finally {
    AbortSignal.prototype.addEventListener = add; AbortSignal.prototype.removeEventListener = remove;
    Object.defineProperty(AbortSignal, 'any', originalAny);
  }
});

test('a known optional selection is canceled even when retirement precedes its first shared-reader seam', async () => {
  const controller = new AbortController(); controller.abort(); const calls: string[] = [];
  const response = ready(page(0, {wholeSelectionPreparing: pending('browse-selection').body}));
  const api: LibraryContentApi = {request: async () => {throw 0;}, requestRaw: async (path, method) => {calls.push(`${method} ${path}`); return ready(undefined);}};
  await assert.rejects(readBrowseSelection(api, 'lib', {revision: 'r1', pivot: 'books', query: null, sort: query.sorts}, response,
    {signal: controller.signal, isCurrent: () => false}, {pivot: 'books', sort: query.sorts}), {name: 'AbortError'});
  assert.deepEqual(calls, ['DELETE /v1/libraries/lib/browse-selection-queries/query-1']);
});

test('retirement inside a page publication callback cannot lend later callbacks to the next query', async () => {
  const events: string[] = [];
  let s!: BrowseSession<ContentEntry>;
  s = session(undefined, {request: async () => page(), onRevision: () => {events.push('revision'); s.retire();}, onPage: () => events.push('page')});
  await assert.rejects(s.fetchPage(0, 1, signal()), {name: 'AbortError'}); assert.deepEqual(events, ['revision']);
});
