import assert from 'node:assert/strict';
import test from 'node:test';
import {parseBrowseNode, parseBrowseCapabilities, completeBrowseNode, parseBrowseResult} from '../src/browse.ts';
import {parseSimpleFacetPage, parseSimpleFacetPreparing} from '../src/facet-windows.ts';
import {readSearchGenres, parseSearchGenres} from '../src/search-genres.ts';
import {readReadingFacet} from '../src/browse-preparation.ts';
import {refuseUnknownVariant} from '../src/tolerant.ts';

// R-09b: every response union carries a required `variant`. A variant this app has no name for is
// handled where it appears: an item is skipped, a whole document says the app needs updating, and
// nothing throws a parser error.

const unsupported = {code: 'unsupported_version'};

test('browse expression: a tagged node parses; a node variant from a newer server is refused by name, not guessed', () => {
  const leaf = {variant: 'predicate', field: 'genre', operator: 'equals', value: 'Drama'};
  assert.deepEqual(parseBrowseNode({variant: 'group', all: [leaf, {variant: 'relative', field: 'dateAdded', operator: 'within-last-days', value: 7}]}),
    {variant: 'group', all: [leaf, {variant: 'relative', field: 'dateAdded', operator: 'within-last-days', value: 7}]});
  assert.throws(() => parseBrowseNode({field: 'genre', operator: 'equals', value: 'Drama'}), {code: 'invalid_browse'}, 'an untagged node is not read');
  assert.throws(() => parseBrowseNode({variant: 'predicate', field: 'dateAdded', operator: 'within-last-days', value: 7}), {code: 'invalid_browse'}, 'the relative operator has its own variant');
  assert.throws(() => parseBrowseNode({variant: 'range', from: 1, to: 2}), unsupported);
  assert.throws(() => parseBrowseNode({variant: 'group', all: [leaf, {variant: 'range'}]}), unsupported, 'nested too');
  assert.deepEqual(completeBrowseNode({field: 'genre', operator: 'equals', value: 'x'}), {variant: 'predicate', field: 'genre', operator: 'equals', value: 'x'});
});

test('browse expression: a quick filter whose node is unknown is skipped alone', () => {
  const payload = {library: {id: 'lib', name: 'Lib', kind: 'movie', defaultView: 'browse', pinned: false}, pivots: [], resolvedPivot: null, fields: [], sorts: [],
    quickFilters: [
      {id: 'known', labelKey: 'filter.known', query: {variant: 'predicate', field: 'genre', operator: 'equals', value: 'Drama'}},
      {id: 'future', labelKey: 'filter.future', query: {variant: 'fuzzy', text: 'x'}},
    ], queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 8192, maximumSorts: 3, defaultLimit: 50, maximumLimit: 200, cursorTtlMs: 60000}};
  assert.deepEqual(parseBrowseCapabilities(payload).quickFilters.map(filter => filter.id), ['known']);
});

test('browse expression: an applied query of an unknown variant says the app needs updating', () => {
  const page = (query: unknown) => ({variant: 'ready', pivot: 'movies', applied: {query, sort: [{field: 'title', direction: 'asc'}]}, entries: [],
    pageInfo: {start: 0, total: 0, totalIsLowerBound: false, revision: 'r', hasMore: false, nextCursor: '', durationMs: 0, durationIsLowerBound: false}, positionIndex: []});
  assert.equal(parseBrowseResult(page({variant: 'predicate', field: 'genre', operator: 'equals', value: 'Drama'})).applied.query?.variant, 'predicate');
  assert.throws(() => parseBrowseResult(page({variant: 'range'})), unsupported);
});

const genreReady = {variant: 'ready', queryId: 'genre_1', revision: 'r', genres: [{value: 'Drama', label: 'Drama', count: 2, covers: []}]};

test('genre query: ready parses; an unknown variant is the update state, not a broken response', async () => {
  assert.equal(parseSearchGenres(genreReady).genres.length, 1);
  assert.throws(() => parseSearchGenres({...genreReady, variant: undefined}), {code: 'invalid_genre_response'}, 'an untagged page is not read');
  const api = {request: async <T>() => ({variant: 'archived', queryId: 'g'}) as T};
  await assert.rejects(readSearchGenres(api as never, {limit: 4}), unsupported);
});

test('library facets: the window parses; an unknown variant is the update state', async () => {
  const page = {variant: 'window', kind: 'simple-keyset', field: 'genre', revision: 'b1', total: 1, totalIsLowerBound: false,
    values: [{value: 'Drama', label: 'Drama', count: 1, countIsLowerBound: false}], window: {queryId: 'q1', basis: 'b1', limit: 2, returned: 1, reload: 'r1'}};
  assert.equal(parseSimpleFacetPage(page).window.queryId, 'q1');
  assert.throws(() => parseSimpleFacetPage({...page, variant: undefined}), {code: 'invalid_facet_window'}, 'an untagged window is not read');
  assert.equal(parseSimpleFacetPreparing({variant: 'preparing', kind: 'facet-preparing', state: 'preparing', queryId: 'q', retryAfterMs: 5}).variant, 'preparing');
  const api = {request: async <T>() => ({variant: 'sharded', field: 'genre'}) as T};
  await assert.rejects(readReadingFacet(api as never, 'lib', '/v1/libraries/lib/facets?field=author', 'author', {isCurrent: () => true}), unsupported);
});

test('reading facets: the page parses and an untagged page is not read', async () => {
  const page = {variant: 'page', field: 'author', total: 1, values: [{value: 'A', label: 'A', count: 1}]};
  const api = {request: async <T>() => page as T};
  assert.equal((await readReadingFacet(api as never, 'lib', '/p', 'author', {isCurrent: () => true})).field, 'author');
  await assert.rejects(readReadingFacet({request: async <T>() => ({...page, variant: undefined}) as T} as never, 'lib', '/p', 'author', {isCurrent: () => true}));
});

test('refuseUnknownVariant names only the unknown, and leaves an untagged body to its parser', () => {
  assert.doesNotThrow(() => refuseUnknownVariant({variant: 'ready'}, ['ready']));
  assert.doesNotThrow(() => refuseUnknownVariant({}, ['ready']));
  assert.doesNotThrow(() => refuseUnknownVariant(null, ['ready']));
  assert.throws(() => refuseUnknownVariant({variant: 'later'}, ['ready']), unsupported);
});
