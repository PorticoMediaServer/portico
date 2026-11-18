import test from 'node:test';
import assert from 'node:assert/strict';
import {readBrowseCapabilitiesResponse, readBrowseCountsResponse, readBrowseFacetResponse} from '../src/query/browse-reads.ts';
import {acrossLibraries} from '../src/browse.ts';
import {genreTitlesPivot} from '../src/library-browse.ts';

/** Across libraries there is one list, a genre's titles; its reads are the rebuilt server's, never the old /v1/browse routes. */
test('a genre\'s titles across libraries counts with the rebuilt reads', async () => {
  const signal = new AbortController().signal, paths: string[] = [];
  const query = {variant: 'predicate' as const, field: 'genre', operator: 'equals' as const, value: 'Drama'};
  const api = {async request(path: string) {paths.push(path); return path.startsWith('/v1/facets') ? {values: [{value: 'Drama', label: 'Drama', count: 4}]} : {items: [], total: 4};}};
  const counts = await readBrowseCountsResponse(api, acrossLibraries, genreTitlesPivot, [{id: 'draft', query}], signal) as {counts: {id: string; total: number}[]};
  assert.deepEqual(counts.counts.map(one => [one.id, one.total]), [['draft', 4]]);
  assert.ok(paths.length === 1 && paths[0]!.startsWith('/v1/items?'), paths.join(' '));
});

test('any other list across libraries is refused before a request is sent', async () => {
  let calls = 0;
  const api = {async request() {calls++; return {};}};
  await assert.rejects(readBrowseCapabilitiesResponse(api, acrossLibraries, 'movies'), {name: 'BrowseQueryError'});
  await assert.rejects(readBrowseCountsResponse(api, acrossLibraries, 'movies', [{id: 'x', query: null}]), {name: 'BrowseQueryError'});
  await assert.rejects(readBrowseFacetResponse(api, acrossLibraries, 'genre', 'movies', undefined, '', 10), {name: 'BrowseQueryError'});
  assert.equal(calls, 0);
  assert.ok(await readBrowseCapabilitiesResponse(api, acrossLibraries, genreTitlesPivot));
});
