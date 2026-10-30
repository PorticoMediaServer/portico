import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowseSession, browseSessionBuilder, withLibraryScope, type BrowseSessionQuery} from '../src/browse-session.ts';
import {parseBrowseCapabilities, type BrowseResult} from '../src/browse.ts';
import {isStaleAnswer} from '../src/freshness-actions.ts';
import type {ContentEntry} from '../src/library-content.ts';

const pivot = {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true, paging: 'offset'};
const capabilities = parseBrowseCapabilities({library: {id: 'lib', name: 'Movies', kind: 'movie', defaultView: 'discover'}, pivots: [pivot], resolvedPivot: pivot,
  fields: [{id: 'watched', labelKey: 'watched', type: 'boolean', operators: ['equals'], controlHint: 'toggle', applicableKinds: ['movie']}],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}});
const query: BrowseSessionQuery = {libraryId: 'lib', pivot: 'movies', capabilities, nodes: [{field: 'watched', operator: 'equals', value: false}],
  sorts: [{field: 'title', direction: 'asc'}]};
const page = (start: number, o: {revision?: string; count?: number; pivot?: string; whole?: unknown; total?: number} = {}) => ({
  pivot: o.pivot ?? 'movies', applied: {query: null, sort: [{field: 'title', direction: 'asc'}], seek: null},
  entries: Array.from({length: o.count ?? 2}, (_, n) => ({id: `movie-${start + n}`, kind: 'movie', title: `Movie ${start + n}`, ...(n === 0 ? {} : {libraryId: 'lib'}), navigation: {view: 'item'}})),
  pageInfo: {start, total: o.total ?? 10, totalIsLowerBound: false, revision: o.revision ?? 'r1', hasMore: true, nextCursor: ''}, positionIndex: [{key: 'M', index: 4}],
  ...(o.whole ? {wholeSelection: o.whole} : {})});
const capabilityCounts = {play: 10, queue: 10, playlist: 10, myList: 10, watched: 10, collection: 10, favorite: 10, download: 0, edit: 10, refresh: 10, delete: 10};
const whole = (libraryId = 'lib') => ({selector: {query: {libraryId, pivot: 'movies', filter: null, sort: [{field: 'title', direction: 'asc'}]}}, catalogRevision: 'r1', total: 10,
  types: [{kind: 'movie', count: 10, capabilityCounts, libraries: [{libraryId, count: 10}]}]});
function harness(answer: (body: any) => unknown | Promise<unknown>) {
  const bodies: any[] = [], events: string[] = [];
  let current = true;
  const session = new BrowseSession<ContentEntry>({query, current: () => current, item: entry => entry,
    request: async (path, method, body) => {assert.equal(path, '/v1/libraries/lib/browse'); assert.equal(method, 'POST'); bodies.push(body); return answer(body);},
    onRevision: revision => events.push(`revision:${revision}`), onWhole: value => events.push(`whole:${value.libraryId}`), onStale: () => events.push('stale')});
  return {session, bodies, events, close: () => {current = false;}};
}
const signal = () => new AbortController().signal;

test('the first page pins the revision; later pages send it and keep the query, sort and limit', async () => {
  const h = harness(body => page(body.range.start));
  const first = await h.session.fetchPage(0, 2, signal());
  assert.equal(first.total, 10); assert.deepEqual(first.positionIndex, {M: 4});
  assert.equal(first.items[0]!.libraryId, 'lib', 'an entry the server left unscoped takes the page library');
  await h.session.fetchPage(2, 2, signal());
  assert.deepEqual(h.bodies.map(body => body.range), [{start: 0}, {start: 2, revision: 'r1'}]);
  assert.deepEqual(h.bodies[1].query, browseSessionBuilder(query).build().query);
  assert.equal(h.bodies[1].limit, 2);
  assert.deepEqual(h.events, ['revision:r1'], 'the revision is announced once');
  assert.equal(h.session.revision, 'r1');
  assert.ok(h.session.owns(first.items[1]!));
});

test('a changed revision, pivot, start or oversized page ends the session for every later page', async () => {
  for (const wrong of [{revision: 'r2'}, {pivot: 'shows'}, {start: 3}, {count: 3}]) {
    const h = harness(body => body.range.start === 0 ? page(0) : page(wrong.start ?? body.range.start, wrong));
    const first = await h.session.fetchPage(0, 2, signal());
    await assert.rejects(h.session.fetchPage(2, 2, signal()), (error: unknown) => isStaleAnswer(error));
    assert.equal(h.session.stale, true);
    assert.equal(h.session.revision, undefined);
    assert.equal(h.session.owns(first.items[0]!), false, 'earlier members are no longer this view');
    const reads = h.bodies.length;
    await assert.rejects(h.session.fetchPage(4, 2, signal()), (error: unknown) => isStaleAnswer(error));
    assert.equal(h.bodies.length, reads, 'a stale session never sends an unversioned read');
    assert.deepEqual(h.events, ['revision:r1', 'stale']);
  }
});

test('stale and ended answers end the session; transient failures do not', async () => {
  for (const [failure, ends] of [[{code: 'stale_continuation'}, true], [{status: 404}, true], [{status: 403}, true], [{status: 503}, false], [new Error('offline'), false]] as const) {
    const h = harness(() => {throw failure;});
    await assert.rejects(h.session.fetchPage(0, 2, signal()));
    assert.equal(h.session.stale, ends, JSON.stringify(failure));
  }
});

test('the whole-view descriptor comes only from this library’s first page and leaves with the session', async () => {
  const other = harness(body => page(body.range.start, {whole: whole('other')}));
  await other.session.fetchPage(0, 2, signal());
  assert.equal(other.session.wholeSelection(), undefined);
  const later = harness(body => page(body.range.start, body.range.start ? {whole: whole()} : {}));
  await later.session.fetchPage(0, 2, signal()); await later.session.fetchPage(2, 2, signal());
  assert.equal(later.session.wholeSelection(), undefined, 'a later page never installs it');
  const h = harness(body => page(body.range.start, {whole: whole()}));
  await h.session.fetchPage(0, 2, signal());
  assert.equal(h.session.wholeSelection()?.libraryId, 'lib');
  h.session.forgetWholeSelection();
  assert.equal(h.session.wholeSelection(), undefined);
  await h.session.fetchPage(0, 2, signal());
  assert.equal(h.session.wholeSelection()?.libraryId, 'lib', 'a fresh first page reinstalls it');
  h.session.retire();
  assert.equal(h.session.wholeSelection(), undefined);
});

test('a retired or closed session refuses late answers and owns nothing', async () => {
  let release!: (value: unknown) => void;
  const h = harness(() => new Promise(resolve => {release = resolve;}));
  const pending = h.session.fetchPage(0, 2, signal());
  h.session.retire(); release(page(0));
  await assert.rejects(pending, {name: 'AbortError'});
  assert.deepEqual(h.events, [], 'a retired session announces nothing');
  await assert.rejects(h.session.fetchPage(0, 2, signal()), {name: 'AbortError'});
  const closed = harness(body => page(body.range.start));
  const first = await closed.session.fetchPage(0, 2, signal());
  closed.close();
  assert.equal(closed.session.owns(first.items[0]!), false);
  assert.equal(closed.session.revision, undefined);
});

test('library scoping fills only absent libraries and keeps an unchanged page', () => {
  const raw = page(0) as unknown as BrowseResult;
  const scoped = withLibraryScope(raw, 'lib');
  assert.notEqual(scoped, raw); assert.equal(scoped.entries[0]!.libraryId, 'lib'); assert.equal(scoped.entries[1], raw.entries[1]);
  assert.equal(withLibraryScope(scoped, 'lib'), scoped);
  assert.equal(withLibraryScope(raw, ''), raw);
});
