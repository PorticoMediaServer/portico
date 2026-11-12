import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {readBrowseCapabilitiesResponse, readImmediateBrowsePageResponse, readBrowseCountsResponse, readBrowseFacetResponse, readReadingBrowseFacet,
  readSelectedChannelGuideResponse} from '../src/query/browse-reads.ts';
import {browseCapabilitiesPath, browsePath, browseCountsPath, browseCountsRequest, browseContextFacetsPath} from '../src/browse.ts';

const fixture = (name: string) => JSON.parse(readFileSync(new URL(`./compat-corpus/main/${name}.json`, import.meta.url), 'utf8')).body;
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

test('single-channel guide retains exact query order, hidden policy and signal', async () => {
  const signal = new AbortController().signal, calls: unknown[][] = [], pending = new Promise<unknown>(() => {});
  const api = {request(...args: unknown[]) {calls.push(args);return pending;}};
  for (const include of [false, true]) {
    const route = {kind: 'live-source' as const, sourceId: 'source /?', ...(include ? {includeServerHidden: true} : {}), timezone: 'UTC', search: '', start: 'start', end: 'end'};
    const expected = '/v1/guide?' + new URLSearchParams({...route, includeServerHidden: String(include), channels: 'channel /?', limit: '1'});
    assert.equal(readSelectedChannelGuideResponse(api, route, 'channel /?', include, signal), pending);
    assert.deepEqual(calls.at(-1), [expected, 'GET', undefined, signal]);
  }
});
