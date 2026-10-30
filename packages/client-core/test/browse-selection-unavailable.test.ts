import test from 'node:test';
import assert from 'node:assert/strict';
import {parseBrowseCapabilities, parseBrowseResult} from '../src/browse.ts';
import {BrowseSession} from '../src/browse-session.ts';
import type {ContentEntry} from '../src/library-content.ts';
const unavailable = {reason: 'search_not_bounded'} as const;
const sort = [{field: 'title', direction: 'asc' as const}];
const pivot = {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: sort, supportedViews: ['grid'], browsable: true, paging: 'offset'};
const caps = parseBrowseCapabilities({library: {id: 'lib', name: 'Movies', kind: 'movie', defaultView: 'discover'}, pivots: [pivot], resolvedPivot: pivot, fields: [],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc'], defaultDirection: 'asc', applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}});
const query = {libraryId: 'lib', pivot: 'movies', capabilities: caps, nodes: [], sorts: sort};
const page = (start = 0, extra: Record<string, unknown> = {}) => ({pivot: 'movies', applied: {query: null, sort, seek: null},
  entries: [{id: `movie-${start}`, kind: 'movie', title: 'Movie', libraryId: 'lib', navigation: {view: 'item'}}],
  pageInfo: {start, total: 3, revision: 'r1', hasMore: true, nextCursor: ''}, ...extra});
const whole = {selector: {query: {libraryId: 'lib', pivot: 'movies', sort, selectionBasis: 'a'.repeat(64)}}, catalogRevision: 'r1', total: 3,
  types: [{kind: 'movie', count: 3, capabilityCounts: {play: 3, queue: 3, playlist: 3, myList: 3, watched: 3, collection: 3, favorite: 3, download: 0, edit: 3, refresh: 3, delete: 3}, libraries: [{libraryId: 'lib', count: 3}]}]};
const signal = () => new AbortController().signal;
test('an explicit unqualified-search refusal retains ordinary rows and carries no whole-view total', () => {
  const result = parseBrowseResult(page(0, {wholeSelectionUnavailable: unavailable}));
  assert.deepEqual(result.wholeSelectionUnavailable, unavailable); assert.equal(result.wholeSelection, undefined);
  assert.equal('total' in result.wholeSelectionUnavailable!, false); assert.equal(result.entries[0]?.id, 'movie-0'); assert.equal(result.pageInfo.total, 3);
  assert.ok(Object.isFrozen(result.wholeSelectionUnavailable));
});
test('unknown reasons, invented totals and overlapping ready/preparing/unavailable states fail closed', () => {
  for (const value of [null, {}, {reason: 'other'}, {reason: 'search_not_bounded', total: 123}]) assert.throws(() => parseBrowseResult(page(0, {wholeSelectionUnavailable: value})));
  const pending = {kind: 'browse-selection-preparing', state: 'preparing', queryId: 'query', retryAfterMs: 250};
  for (const extra of [{wholeSelection: whole, wholeSelectionUnavailable: unavailable}, {wholeSelectionPreparing: pending, wholeSelectionUnavailable: unavailable}, {wholeSelection: whole, wholeSelectionPreparing: pending}]) assert.throws(() => parseBrowseResult(page(0, extra)));
  assert.ok(parseBrowseResult(page(0, {wholeSelection: whole})).wholeSelection);
  assert.equal(parseBrowseResult(page(0, {wholeSelectionPreparing: pending})).wholeSelectionUnavailable, undefined);
});
test('the session retains only its first-page refusal while page selection and later pages remain usable', async () => {
  const events: unknown[] = []; let current = true;
  const session = new BrowseSession<ContentEntry>({query, current: () => current, item: entry => entry,
    request: async (_path, _method, body) => page((body as any).range.start, (body as any).range.start === 0 ? {wholeSelectionUnavailable: unavailable} : {wholeSelection: whole}),
    onWholeUnavailable: value => events.push(value)});
  const first = await session.fetchPage(0, 1, signal()); assert.equal(session.owns(first.items[0]!), true); assert.deepEqual(session.wholeSelectionUnavailable(), unavailable);
  await session.fetchPage(1, 1, signal()); assert.deepEqual(session.wholeSelectionUnavailable(), unavailable); assert.equal(session.wholeSelection(), undefined); assert.equal(events.length, 1);
  current = false; assert.equal(session.wholeSelectionUnavailable(), undefined); session.retire();
});
test('later-page refusal cannot replace a ready descriptor; query replacement and retirement own no stale refusal', async () => {
  const ready = new BrowseSession<ContentEntry>({query, current: () => true, item: entry => entry,
    request: async (_path, _method, body) => page((body as any).range.start, (body as any).range.start === 0 ? {wholeSelection: whole} : {wholeSelectionUnavailable: unavailable})});
  await ready.fetchPage(0, 1, signal()); await ready.fetchPage(1, 1, signal()); assert.equal(ready.wholeSelection()?.total, 3); assert.equal(ready.wholeSelectionUnavailable(), undefined); ready.retire();
  let release!: (value: unknown) => void;
  const retired = new BrowseSession<ContentEntry>({query, current: () => true, item: entry => entry, request: async () => new Promise(resolve => {release = resolve;})});
  const work = retired.fetchPage(0, 1, signal()); retired.retire(); release(page(0, {wholeSelectionUnavailable: unavailable})); await assert.rejects(work, {name: 'AbortError'}); assert.equal(retired.wholeSelectionUnavailable(), undefined);
  const replacement = new BrowseSession<ContentEntry>({query, current: () => true, item: entry => entry, request: async () => page()});
  await replacement.fetchPage(0, 1, signal()); assert.equal(replacement.wholeSelectionUnavailable(), undefined); replacement.retire();
});
test('a fresh first page can replace unavailable with ready without retaining both states', async () => {
  let read = 0;
  const changes: unknown[] = [], session = new BrowseSession<ContentEntry>({query, current: () => true, item: entry => entry,
    request: async () => page(0, read++ === 0 ? {wholeSelectionUnavailable: unavailable} : {wholeSelection: whole}), onWholeUnavailable: value => changes.push(value)});
  await session.fetchPage(0, 1, signal()); await session.fetchPage(0, 1, signal()); assert.equal(session.wholeSelectionUnavailable(), undefined); assert.equal(session.wholeSelection()?.total, 3); assert.deepEqual(changes, [unavailable, undefined]); session.retire();
});

test('a refusal for a different applied sort cannot disable this view', async () => {
  let callbacks = 0;
  const session = new BrowseSession<ContentEntry>({query, current: () => true, item: entry => entry,
    request: async () => page(0, {wholeSelectionUnavailable: unavailable, applied: {query: null, sort: [{field: 'title', direction: 'desc'}], seek: null}}),
    onWholeUnavailable: () => {callbacks++;}});
  await assert.rejects(session.fetchPage(0, 1, signal()), {code: 'stale_continuation'});
  assert.equal(callbacks, 0); assert.equal(session.wholeSelectionUnavailable(), undefined); session.retire();
});

test('retirement during refusal publication prevents page publication and ownership', async () => {
  let pages = 0;
  const session: BrowseSession<ContentEntry> = new BrowseSession<ContentEntry>({query, current: () => true, item: entry => entry,
    request: async () => page(0, {wholeSelectionUnavailable: unavailable}),
    onWholeUnavailable: () => {session.retire();}, onPage: () => {pages++;}});
  await assert.rejects(session.fetchPage(0, 1, signal()), {name: 'AbortError'});
  assert.equal(pages, 0); assert.equal(session.wholeSelectionUnavailable(), undefined);
});
