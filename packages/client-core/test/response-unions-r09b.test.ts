import assert from 'node:assert/strict';
import test from 'node:test';
import {parseBrowseNode, parseBrowseCapabilities, completeBrowseNode, parseBrowseResult, browseCondition, browseNot} from '../src/browse.ts';
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

test('browse expression: the condition builders tag exactly as completeBrowseNode does', () => {
  const predicate = browseCondition('genre', 'equals', 'Drama');
  const list = browseCondition('genre', 'contains-any', ['Drama', 'Comedy']);
  const relative = browseCondition('dateAdded', 'within-last-days', 7);
  assert.deepEqual(predicate, completeBrowseNode({field: 'genre', operator: 'equals', value: 'Drama'}));
  assert.deepEqual(list, completeBrowseNode({field: 'genre', operator: 'contains-any', value: ['Drama', 'Comedy']}));
  assert.deepEqual(relative, completeBrowseNode({field: 'dateAdded', operator: 'within-last-days', value: 7}));
  assert.equal(predicate.variant, 'predicate');
  assert.equal(relative.variant, 'relative');
  assert.deepEqual(browseNot(predicate), completeBrowseNode({not: {field: 'genre', operator: 'equals', value: 'Drama'}}));
  assert.deepEqual(browseNot(relative), completeBrowseNode({not: {field: 'dateAdded', operator: 'within-last-days', value: 7}}));
  for (const node of [predicate, list, relative, browseNot(predicate), browseNot(relative)]) assert.deepEqual(parseBrowseNode(node), node);
});

test('refuseUnknownVariant names only the unknown, and leaves an untagged body to its parser', () => {
  assert.doesNotThrow(() => refuseUnknownVariant({variant: 'ready'}, ['ready']));
  assert.doesNotThrow(() => refuseUnknownVariant({}, ['ready']));
  assert.doesNotThrow(() => refuseUnknownVariant(null, ['ready']));
  assert.throws(() => refuseUnknownVariant({variant: 'later'}, ['ready']), unsupported);
});
