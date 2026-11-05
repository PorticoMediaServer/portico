import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {readBrowseCapabilitiesResponse, readImmediateBrowsePageResponse, readBrowseCountsResponse, readBrowseFacetResponse, readReadingBrowseFacet} from '../src/query/browse-reads.ts';
import {browseCapabilitiesPath, browsePath, browseCountsPath, browseCountsRequest, browseContextFacetsPath} from '../src/browse.ts';

const fixture = (name: string) => JSON.parse(readFileSync(new URL(`./compat-corpus/main/${name}.json`, import.meta.url), 'utf8')).body;
test('browse operations preserve both scopes, exact bodies, cursors, signals and raw responses', async () => {
  const signal = new AbortController().signal;
  for (const libraryId of ['*', 'lib/a b']) {
    const capabilities = fixture('browse-capabilities'), page = fixture('browse-songs');
    const body = {pivot: 'songs', cursor: 'opaque/+==', limit: 37, range: {start: 60, revision: 'opaque-revision'}};
    const replies = [capabilities, page];
    const calls: unknown[][] = [];
    const api = {async request(...args: unknown[]) {calls.push(args); return replies.shift();}};
    assert.deepEqual(await readBrowseCapabilitiesResponse(api, libraryId, 'songs', signal), capabilities);
    assert.deepEqual(await readImmediateBrowsePageResponse(api, libraryId, body, signal), page);
    assert.deepEqual(calls, [[browseCapabilitiesPath(libraryId, 'songs'), 'GET', undefined, signal], [browsePath(libraryId), 'POST', body, signal]]);
    assert.equal(calls[1][2], body, 'the captured request body is passed through without rewriting');
  }
});
test('facets and batched counts keep contextual filters and exact batch membership', async () => {
  const signal = new AbortController().signal;
  const query = {variant: 'predicate' as const, field: 'genre', operator: 'equals' as const, value: 'Drama'};
  const queries = [{id: 'draft', query}];
  const counts = {counts: [{id: 'draft', total: 4, totalIsLowerBound: false, revision: 'rev'}]};
  const calls: unknown[][] = [];
  const api = {async request(...args: unknown[]) {calls.push(args);return counts;}};
  assert.deepEqual(await readBrowseCountsResponse(api, '*', 'movies', queries, signal), counts);
  assert.deepEqual(calls, [[browseCountsPath('*'), 'POST', browseCountsRequest('movies', queries), signal]]);
  const facetApi = {async request(...args: unknown[]) {calls.push(args);return {field: 'genre', total: 1, values: [{value: 'Drama', label: 'Drama', count: 4}]};}};
  const facet = await readBrowseFacetResponse(facetApi, '*', 'genre', 'movies', query, 'Dr', 200, signal);
  assert.equal((facet as {values: {value: string}[]}).values[0].value, 'Drama');
  assert.deepEqual(calls[1], [browseContextFacetsPath('*', 'genre', 'movies', query, 'Dr', 200), 'GET', undefined, signal]);
});
test('transport failures and cancellation are not retried or cached by an operation', async () => {
  for (const error of [Object.assign(new Error('stale'), {code: 'stale_continuation'}), new DOMException('retired', 'AbortError')]) {
    let calls = 0;
    const api = {async request() {calls++;throw error;}};
    await assert.rejects(readBrowseCapabilitiesResponse(api, '*', ''), value => value === error);
    assert.equal(calls, 1);
  }
});
test('reading facets still refuse retired lifetimes before starting a preparation', async () => {
  let calls = 0;
  const api = {async request<T>() {calls++;return {} as T;}};
  await assert.rejects(readReadingBrowseFacet(api, 'lib', 'genre', 'books', undefined, '', 200,
    {signal: new AbortController().signal, isCurrent: () => false}), {name: 'AbortError'});
  assert.equal(calls, 0);
});

test('browse response operations preserve transport promise identity for original guard and parse ordering', () => {
  const pending = new Promise<unknown>(() => {}), api = {request: () => pending};
  assert.equal(readBrowseCapabilitiesResponse(api, 'lib', 'books'), pending);
  assert.equal(readImmediateBrowsePageResponse(api, 'lib', {pivot: 'books'}), pending);
  assert.equal(readBrowseCountsResponse(api, 'lib', 'books', [{id: 'draft', query: null}]), pending);
  assert.equal(readBrowseFacetResponse(api, 'lib', 'genre', 'books', undefined, '', 200), pending);
});
