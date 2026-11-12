import assert from 'node:assert/strict';
import {test} from 'node:test';
import {authorPredicate, loadFacetPage, type FacetRequest} from '../../src/screens/read/facets.ts';

// A Read library's Authors and Series lists on the rebuilt server: its people by the author role,
// and its series, each a choice whose value is what the item list's filter takes.
const person = (id: string, name: string, titleCount: number) => ({id, name, images: {}, titleCount});
const series = (id: string, title: string, children: number) => ({id, kind: 'series', title, libraryId: 'lib-reading', addedAt: '2026-11-12T00:00:00Z', images: {},
  state: {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}, counts: {children, leaves: children, unwatchedLeaves: children}});
const asked = (calls: string[]) => calls.map(path => new URL(path, 'http://server.test'));

test('the Authors list is the library\'s people by the author role, each with its titles and its id as the filter', async () => {
  const calls: string[] = [], label = 'A'.repeat(4096);
  const values = await loadFacetPage(async path => {calls.push(path); return {items: [person('per_1', label, 12), person('per_2', 'Frank Herbert', 3)], total: 2};},
    {libraryId: 'lib-reading', field: 'author', pivot: 'books', limit: 50}, new AbortController().signal);
  const [url] = asked(calls);
  assert.equal(url!.pathname, '/v1/people');
  assert.deepEqual([url!.searchParams.get('library'), url!.searchParams.get('role'), url!.searchParams.get('total')], ['lib-reading', 'author', 'true']);
  assert.deepEqual(values.values.map(value => [value.value, value.count]), [['per_1', 12], ['per_2', 3]]);
  assert.equal(values.values[0]!.label, label); assert.equal(values.total, 2);
  assert.equal('seriesId' in values.values[0]!, false);
});

test('the Series list is the library\'s series by title, each opening its own volumes', async () => {
  const calls: string[] = [];
  const values = await loadFacetPage(async path => {calls.push(path); return {items: [series('itm_dune', 'Dune', 6)], total: 1};},
    {libraryId: 'lib-reading', field: 'series', pivot: 'comics'}, new AbortController().signal);
  const [url] = asked(calls);
  assert.equal(url!.pathname, '/v1/items'); assert.equal(url!.searchParams.get('kind'), 'series'); assert.equal(url!.searchParams.get('sort'), 'title');
  assert.deepEqual(values.values, [{value: 'itm_dune', label: 'Dune', count: 6, seriesId: 'itm_dune'}]);
});

test('a later page of a list goes on from the server\'s cursor to where the page starts', async () => {
  const calls: string[] = [];
  const values = await loadFacetPage(async path => {
    calls.push(path);
    const cursor = new URL(path, 'http://server.test').searchParams.get('cursor');
    return cursor ? {items: Array.from({length: 200}, (_, n) => person(`per_b${n}`, `B ${n}`, 1))} : {items: Array.from({length: 200}, (_, n) => person(`per_a${n}`, `A ${n}`, 1)), nextCursor: 'more', total: 400};
  }, {libraryId: 'lib', field: 'author', pivot: 'books', limit: 100, offset: 150}, new AbortController().signal);
  assert.equal(calls.length, 2); assert.equal(asked(calls)[1]!.searchParams.get('cursor'), 'more');
  assert.deepEqual([values.values[0]!.value, values.values.at(-1)!.value, values.total], ['per_a150', 'per_b49', 400]);
});

test('a search in a very large library reads a bounded number of names and says its count is only a lower bound', async () => {
  let calls = 0;
  const values = await loadFacetPage(async () => {calls++; return {items: Array.from({length: 200}, (_, n) => person(`per_${calls}_${n}`, n === 0 ? 'Le Guin' : 'Someone', 1)), nextCursor: 'more'};},
    {libraryId: 'lib', field: 'author', pivot: 'books', q: 'guin', limit: 50}, new AbortController().signal);
  assert.equal(calls, 10, 'at most 2,000 names for one search');
  assert.equal(values.values.length, 10); assert.equal(values.totalIsLowerBound, true);
});

test('the page refuses a bad request before it reaches the server', async () => {
  let called = false;
  const request = async () => { called = true; return {items: []}; };
  await assert.rejects(loadFacetPage(request, {libraryId: 'lib', field: 'author', pivot: 'books', limit: 0}, new AbortController().signal), /invalid_request/);
  await assert.rejects(loadFacetPage(request, {libraryId: 'lib', field: 'author', pivot: 'books', offset: -1}, new AbortController().signal), /invalid_request/);
  assert.equal(called, false);
});

test('an author value filters browse by the author\'s person id', () => {
  assert.deepEqual(authorPredicate('per_1'), {variant: 'predicate', field: 'author', operator: 'equals', value: 'per_1'});
  assert.throws(() => authorPredicate(''), /invalid_request/);
});

test('a facet reply that arrives after the captured route or profile changed is not published', async () => {
  let current = true, finish!: (value: unknown) => void;
  const request: FacetRequest = Object.assign(async () => new Promise(resolve => {finish = resolve;}), {isCurrent: () => current, requestRaw: async () => {throw new Error('raw path used');}});
  const controller = new AbortController();
  const work = loadFacetPage(request, {libraryId: 'lib', field: 'author', pivot: 'books'}, controller.signal);
  const rejected = assert.rejects(work, {name: 'AbortError'});
  await Promise.resolve(); current = false; finish({items: [person('per_old', 'Old profile', 1)]});
  await rejected; assert.equal(controller.signal.aborted, false);
  await assert.rejects(loadFacetPage(request, {libraryId: 'lib', field: 'author', pivot: 'books'}, new AbortController().signal), {name: 'AbortError'});
});
