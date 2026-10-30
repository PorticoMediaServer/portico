import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLibraryDestination} from '../src-next/screens/library/model.ts';
import {NativeLibraryFilterDraft} from '../src-next/screens/library/filters.ts';
import type {BrowseCapabilities, BrowseRequestBody} from '../../../packages/client-core/src/browse.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import {readingTabModel} from '../../../packages/client-core/src/presentation/library-tabs.ts';
import type {PreparationResponse} from '../../../packages/client-core/src/read-preparation.ts';

const tick = async () => {for (let n = 0; n < 40; n++) await Promise.resolve();};
const scope = {serverId: 'server', viewerId: 'viewer'};
const library = {id: 'lib', name: 'Library', kind: 'movie'};
const pivot = {id: 'movies', labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid'], browsable: true, sorts: ['title'], filters: []} as const;
const caps: BrowseCapabilities = {library: {...library, defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot, fields: [],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 600000}};
const counts = {play: 2, queue: 2, playlist: 2, myList: 2, watched: 2, collection: 2, favorite: 2, download: 0, edit: 2, refresh: 2, delete: 2};
const whole = {selector: {query: {libraryId: 'lib', pivot: 'movies', filter: null, sort: pivot.defaultSort, selectionBasis: 'a'.repeat(64)}}, catalogRevision: 'r1', total: 2,
  types: [{kind: 'movie', count: 2, capabilityCounts: counts, libraries: [{libraryId: 'lib', count: 2}]}]};
const pending = (domain: string, queryId: string) => ({variant: domain === 'reading-facet' ? 'reading-preparing' : 'preparing', kind: `${domain}-preparing`, state: 'preparing', queryId, retryAfterMs: 0});
const ready = (body: unknown): PreparationResponse => ({status: 200, headers: {}, body});
function page(body: BrowseRequestBody, extra: Record<string, unknown> = {}) {const start = body.range?.start ?? 0; return {pivot: 'movies', applied: {query: body.query ?? null, sort: body.sort ?? [], seek: null},
  entries: [{id: `movie-${start}`, kind: 'movie', title: 'Movie', libraryId: 'lib', navigation: {view: 'item'}}],
  pageInfo: {start, total: 2, revision: 'r1', hasMore: start === 0, nextCursor: ''}, ...extra};}

test('native Library publishes the ordinary page first and republishes when its own aggregate becomes ready', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  let publications = 0; const calls: string[] = [];
  const api: LibraryContentApi = {request: async <T>() => caps as T, requestRaw: async (path, method, options) => {
    calls.push(`${method} ${path}`); return method === 'POST' ? ready(page(options?.body as BrowseRequestBody, {wholeSelectionPreparing: pending('browse-selection', 'selection')})) : ready(whole);
  }};
  const destination = new NativeLibraryDestination({api, scope, library, current: () => true}); destination.subscribe(() => {publications++;});
  try {
    await destination.load(); await tick();
    const initial = destination.getSnapshot(); assert.equal(initial.collection?.itemAt(0)?.id, 'movie-0'); assert.equal(initial.wholeSelection, undefined);
    const before = publications; t.mock.timers.tick(400); await tick();
    assert.equal(destination.getSnapshot().wholeSelection?.total, 2); assert.ok(publications > before);
    assert.ok(calls.some(call => call === 'GET /v1/libraries/lib/browse-selection-queries/selection'));
  } finally {destination.dispose();}
});

test('native query switch cancels its original selection demand and rejects a late aggregate', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  let finish!: (response: PreparationResponse) => void; let posts = 0; const calls: string[] = [];
  const api: LibraryContentApi = {request: async <T>() => caps as T, requestRaw: async (path, method, options) => {
    calls.push(`${method} ${path}`);
    if (method === 'POST') return ready(page(options?.body as BrowseRequestBody, posts++ === 0 ? {wholeSelectionPreparing: pending('browse-selection', 'old')} : {}));
    if (method === 'DELETE') return ready(undefined);
    return new Promise(resolve => {finish = resolve;});
  }};
  const destination = new NativeLibraryDestination({api, scope, library, current: () => true});
  try {
    await destination.load(); await tick(); t.mock.timers.tick(400); await tick();
    destination.setQuery([], {field: 'title', direction: 'desc'}); await tick(); finish(ready(whole)); await tick();
    assert.equal(destination.getSnapshot().wholeSelection, undefined); assert.equal(destination.getSnapshot().sort?.direction, 'desc');
    assert.equal(calls.filter(call => call === 'DELETE /v1/libraries/lib/browse-selection-queries/old').length, 1);
  } finally {destination.dispose();}
});

const readingPivot = {...pivot, id: 'books', entityKinds: ['book'], filters: ['author']} as const;
const reading: BrowseCapabilities = {...caps, library: {...caps.library, kind: 'book'}, pivots: [readingPivot], resolvedPivot: readingPivot,
  fields: [{id: 'author', labelKey: 'filter.author', type: 'string', operators: ['equals', 'contains-any'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['book']}],
  sorts: [{...caps.sorts[0]!, applicableKinds: ['book']}]};
const facet = {variant: 'page', field: 'author', total: 1, values: [{value: 'Author', label: 'Author', count: 2}]};
test('native Reading facet preparation retains prior values, then reports revocation without an empty success', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']}); let reads = 0; const calls: string[] = [];
  const api: LibraryContentApi = {request: async () => {throw new Error('Reading raw transport was dropped');}, requestRaw: async (path, method) => {
    calls.push(`${method} ${path}`); if (method === 'DELETE') return ready(undefined);
    if (reads++ === 0) return ready(facet);
    if (reads === 2) return {status: 202, headers: {'Retry-After': '1'}, body: pending('reading-facet', 'facet')};
    return {status: 403, headers: {}, body: {error: {code: 'forbidden'}}};
  }};
  const draft = new NativeLibraryFilterDraft({api, capabilities: reading, pivot: 'books', sort: {field: 'title', direction: 'asc'}, predicates: [], fixed: [], current: () => true, apply() {}, onDispose() {}});
  try {
    draft.setOpenFields(['author']); await tick(); assert.equal(draft.getSnapshot().facets.author?.value?.values[0]?.value, 'Author');
    draft.search('author', 'a'); await tick(); assert.equal(draft.getSnapshot().facets.author?.phase, 'loading'); assert.equal(draft.getSnapshot().facets.author?.value?.total, 1);
    t.mock.timers.tick(1000); await tick(); assert.equal(calls.length, 2, 'full server floor is respected');
    t.mock.timers.tick(100); await tick(); const state = draft.getSnapshot().facets.author;
    assert.equal(state?.phase, 'error'); assert.equal(state?.value?.total, 1); assert.equal((state?.error as {status: number}).status, 403);
    assert.equal(calls.at(-1), 'DELETE /v1/libraries/lib/reading-facet-queries/facet');
  } finally {draft.dispose();}
});

test('native Reading initial browse 202 keeps its collection pending until the real page and releases its handle', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const calls: string[] = []; let captured!: BrowseRequestBody;
  const api: LibraryContentApi = {request: async <T>() => reading as T, requestRaw: async (path, method, options) => {
    calls.push(`${method} ${path}`);
    if (method === 'DELETE') return ready(undefined);
    if (method === 'POST') {captured = options?.body as BrowseRequestBody; return {status: 202, headers: {'Retry-After': '1'}, body: pending('reading-browse', 'reading-page')};}
    return ready({...page(captured), pivot: 'books', entries: [{id: 'book', kind: 'book', title: 'Book', libraryId: 'lib', navigation: {view: 'item'}}]});
  }};
  const destination = new NativeLibraryDestination({api, scope, library: {...library, kind: 'book'}, tab: readingTabModel(reading)!.find(tab => tab.pivot === 'books')!, current: () => true});
  try {
    await destination.load(); await tick();
    assert.equal(destination.getSnapshot().collection?.itemAt(0), undefined);
    assert.equal(destination.getSnapshot().collection?.getSnapshot().status, 'loading');
    t.mock.timers.tick(1000); await tick(); assert.equal(calls.length, 1, 'server Retry-After is a strict floor');
    t.mock.timers.tick(101); await tick();
    assert.equal(destination.getSnapshot().collection?.itemAt(0)?.id, 'book');
    assert.equal(destination.getSnapshot().collection?.getSnapshot().status, 'ready');
    assert.deepEqual(calls.slice(1), ['GET /v1/libraries/lib/reading-browse-queries/reading-page', 'DELETE /v1/libraries/lib/reading-browse-queries/reading-page']);
  } finally {destination.dispose();}
});
