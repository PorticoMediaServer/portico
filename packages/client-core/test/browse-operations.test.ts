import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {readBrowseCapabilities, readImmediateBrowsePage, readBrowseCounts, readBrowseFacet, readContextualBrowseFacet} from '../src/query/browse-reads.ts';
import {parseBrowseCapabilities, parseBrowseResult, browseCapabilitiesPath, browsePath, browseCountsPath, browseCountsRequest, browseContextFacetsPath} from '../src/browse.ts';

const fixture = (name: string) => JSON.parse(readFileSync(new URL(`./compat-corpus/main/${name}.json`, import.meta.url), 'utf8')).body;
test('browse operations preserve both scopes, exact bodies, cursors, signals and existing DTO parsing', async () => {
  const signal = new AbortController().signal;
  for (const libraryId of ['*', 'lib/a b']) {
    const capabilities = fixture('browse-capabilities'), page = fixture('browse-songs');
    const body = {pivot: 'songs', cursor: 'opaque/+==', limit: 37, range: {start: 60, revision: 'opaque-revision'}};
    const replies = [capabilities, page];
    const calls: unknown[][] = [];
    const api = {async request(...args: unknown[]) {calls.push(args); return replies.shift();}};
    assert.deepEqual(await readBrowseCapabilities(api, libraryId, 'songs', signal), parseBrowseCapabilities(capabilities));
    assert.deepEqual(await readImmediateBrowsePage(api, libraryId, body, signal), parseBrowseResult(page));
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
  assert.deepEqual(await readBrowseCounts(api, '*', 'movies', queries, signal), counts.counts);
  assert.deepEqual(calls, [[browseCountsPath('*'), 'POST', browseCountsRequest('movies', queries), signal]]);
  const facetApi = {async request(...args: unknown[]) {calls.push(args);return {field: 'genre', total: 1, values: [{value: 'Drama', label: 'Drama', count: 4}]};}};
  const facet = await readBrowseFacet(facetApi, '*', 'genre', 'movies', query, 'Dr', 200, signal);
  assert.equal(facet.values[0].value, 'Drama');
  assert.deepEqual(calls[1], [browseContextFacetsPath('*', 'genre', 'movies', query, 'Dr', 200), 'GET', undefined, signal]);
});
test('transport failures and cancellation are not retried or cached by an operation', async () => {
  for (const error of [Object.assign(new Error('stale'), {code: 'stale_continuation'}), new DOMException('retired', 'AbortError')]) {
    let calls = 0;
    const api = {async request() {calls++;throw error;}};
    await assert.rejects(readBrowseCapabilities(api, '*', ''), value => value === error);
    assert.equal(calls, 1);
  }
});
test('reading facets still refuse retired lifetimes before starting a preparation', async () => {
  let calls = 0;
  const api = {async request<T>() {calls++;return {} as T;}};
  await assert.rejects(readContextualBrowseFacet(api, {id: 'lib', kind: 'book'}, 'genre', 'books', undefined, '',
    {signal: new AbortController().signal, isCurrent: () => false}), {name: 'AbortError'});
  assert.equal(calls, 0);
});

test('post-response guards precede parsing while original transport failures bypass them', async () => {
  const cancelled = Object.assign(new Error('original-cancellation'), {name: 'AbortError', code: 'cancelled'});
  await assert.rejects(readBrowseCapabilities({async request() {return null;}}, 'lib', 'books', undefined, () => {throw cancelled;}), value => value === cancelled);
  await assert.rejects(readBrowseCounts({async request() {return null;}}, 'lib', 'books', [{id: 'draft', query: null}], undefined, () => {throw cancelled;}), value => value === cancelled);
  assert.equal(await readBrowseCounts({async request() {return null;}}, 'lib', 'books', [{id: 'draft', query: null}], undefined, () => false), undefined);
  const transport = new TypeError('transport-failed');let guards = 0;
  await assert.rejects(readBrowseCapabilities({async request() {throw transport;}}, 'lib', 'books', undefined, () => {guards++;throw cancelled;}), value => value === transport);
  assert.equal(guards, 0);
});
