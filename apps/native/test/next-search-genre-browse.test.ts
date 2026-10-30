import test from 'node:test';
import assert from 'node:assert/strict';
import type {BrowseCapabilities, BrowseRequestBody} from '../../../packages/client-core/src/browse.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import {NativeGenreBrowseDestination} from '../src-next/screens/search/genre-browse.ts';

const pivot = {id: 'video-titles', labelKey: 'browse.pivot.titles', entityKinds: ['movie', 'show'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], browsable: true, sorts: ['title'], filters: ['genre', 'favorite']} as const;
const capabilities: BrowseCapabilities = {
  library: {id: '*', name: 'Authorized across scope', kind: 'mixed', defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot,
  fields: [{id: 'genre', labelKey: 'filter.field.genre', type: 'identity-set', operators: ['equals', 'contains-any'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'show'], facetSource: {field: 'genre', endpoint: '/v1/browse/facets'}},
    {id: 'favorite', labelKey: 'filter.favorite', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'show']}],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie', 'show']}], quickFilters: [],
  surfaces: [{id: 'genre-titles', endpoint: '/v1/browse', paging: 'keyset', sorts: ['title'], filters: ['genre', 'favorite']}],
  queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000},
};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function page(body: BrowseRequestBody, revision = 'actual-opaque-token') {
  const start = body.range?.start ?? 0, count = body.limit ?? 40, total = 5_000_000;
  return {pivot: 'video-titles', applied: {query: body.query, sort: body.sort, seek: null},
    entries: Array.from({length: count}, (_, index) => ({id: `actual-${start + index}`, libraryId: index % 2 ? 'tv-library' : 'movie-library', kind: index % 2 ? 'show' : 'movie', title: `Actual ${start + index}`})),
    pageInfo: {start, total, revision, totalIsLowerBound: false, hasMore: start + count < total, nextCursor: ''}, positionIndex: []};
}

test('genre browse reads the canonical across pivot and bounded sparse windows with its actual opaque revision', async () => {
  const calls: {path: string; body?: BrowseRequestBody}[] = [];
  const api: LibraryContentApi = {request: async <T>(path: string, _method?: string, body?: unknown): Promise<T> => {
    calls.push({path, body: body as BrowseRequestBody | undefined}); return (path.includes('capabilities') ? capabilities : page(body as BrowseRequestBody)) as T;
  }};
  const source = new NativeGenreBrowseDestination({api, value: 'actual-genre-value', current: () => true});
  try {
    const loading = source.load(); assert.equal(source.load(), loading); await loading; await tick();
    assert.equal(calls[0].path, '/v1/browse-capabilities?pivot=video-titles');
    const initial = calls.find(call => call.body)!;
    assert.equal(initial.path, '/v1/browse'); assert.equal(initial.body?.pivot, 'video-titles');
    assert.deepEqual(initial.body?.query, {field: 'genre', operator: 'equals', value: 'actual-genre-value'});
    assert.equal(source.getSnapshot().revision, 'actual-opaque-token');
    const collection = source.getSnapshot().collection!; collection.ensureRange(100_000, 100_001); await tick();
    assert.ok(collection.residentItems().length <= 240); assert.equal(collection.getSnapshot().total, 5_000_000);
    assert.ok(calls.slice(2).filter(call => call.body).every(call => call.body!.range?.revision === 'actual-opaque-token'));
    const entry = collection.itemAt(100_000)!; assert.equal(source.contains(entry), true);
    source.remember(100_000, 18342.25); assert.equal(source.getMemory().offset, 18342.25);
    assert.throws(() => source.setQuery([]), /anchor/);
    const draft = source.createFilters(); draft.clear(); assert.deepEqual(draft.getSnapshot().predicates, [initial.body!.query]); draft.dispose();
  } finally {source.dispose();}
});

test('a source-revision change never mixes fresh entries into old resident pages', async () => {
  let changed = false;
  const api: LibraryContentApi = {request: async <T>(path: string, _method?: string, body?: unknown): Promise<T> =>
    (path.includes('capabilities') ? capabilities : page(body as BrowseRequestBody, changed ? 'different-token' : 'first-token')) as T};
  const source = new NativeGenreBrowseDestination({api, value: 'genre', current: () => true});
  await source.load(); await tick(); const before = source.getSnapshot().collection!.itemAt(0)!;
  changed = true; source.getSnapshot().collection!.ensureRange(100_000, 100_001); await tick();
  assert.equal(source.getSnapshot().phase, 'error'); assert.equal(source.getSnapshot().revision, 'first-token');
  assert.equal(source.contains(before), false, 'old callbacks are no longer current members of a usable source');
  await source.load(); await tick();
  assert.equal(source.getSnapshot().revision, 'different-token'); assert.equal(source.getSnapshot().phase, 'ready'); source.dispose();
});

test('retiring the principal aborts real capability I/O and refuses its late reply', async () => {
  let current = true, signal: AbortSignal | undefined, finish!: (value: unknown) => void;
  const api: LibraryContentApi = {request: <T>(_path: string, _method?: string, _body?: unknown, actualSignal?: AbortSignal): Promise<T> => {
    signal = actualSignal; return new Promise(resolve => {finish = value => resolve(value as T);});
  }};
  const source = new NativeGenreBrowseDestination({api, value: 'genre', current: () => current}), pending = source.load();
  await tick(); current = false; const retired = source.getSnapshot(); assert.ok(signal?.aborted);
  finish(capabilities); await pending; assert.equal(source.getSnapshot(), retired); assert.equal(retired.collection, undefined);
});
