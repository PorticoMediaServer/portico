import test from 'node:test';
import assert from 'node:assert/strict';
import {parseBrowseResult, parseWholeSelection} from '../src/browse.ts';

const filter = {variant:'predicate',field: 'year', operator: 'at-least', value: 2000};
const sort = [{field: 'title', direction: 'asc'}];
const capabilityCounts = {play: 48000, queue: 48000, playlist: 48000, myList: 48000, watched: 48000, collection: 48000, favorite: 48000, download: 0, edit: 48000, refresh: 48000, delete: 48000};
const descriptor = {
  selector: {query: {libraryId: 'movies', pivot: 'movies', filter, sort}},
  catalogRevision: 'r1',
  total: 48000,
  types: [{kind: 'movie', count: 48000, capabilityCounts, libraries: [{libraryId: 'movies', count: 48000}]}],
};
const page = (whole: unknown, over: Record<string, unknown> = {}) => ({
  pivot: 'movies',
  applied: {query: filter, sort, seek: null},
  entries: [],
  pageInfo: {start: 0, total: 48000, totalIsLowerBound: false, revision: 'r1', hasMore: false, nextCursor: ''},
  positionIndex: [],
  wholeSelection: whole,
  ...over,
});

test('the exact whole-view aggregate parses and rides on the page it describes', () => {
  const whole = parseWholeSelection(descriptor, {revision: 'r1', pivot: 'movies', query: {variant:'predicate',field: 'year', operator: 'at-least', value: 2000}, sort: [{field: 'title', direction: 'asc'}]});
  assert.equal(whole?.libraryId, 'movies');
  assert.equal(whole?.catalogRevision, 'r1');
  assert.equal(whole?.total, 48000);
  assert.equal(whole?.filter?.field, 'year');
  assert.equal(whole?.types[0]?.capabilities.play, 48000);
  assert.equal(whole?.types[0]?.libraries[0]?.count, 48000);
  assert.equal(parseBrowseResult(page(descriptor)).wholeSelection?.total, 48000);
});

test('an absent aggregate is simply absent, and a stale page still parses', () => {
  assert.equal(parseWholeSelection(undefined), undefined);
  assert.equal(parseBrowseResult(page(undefined)).wholeSelection, undefined);
});

test('a descriptor for another revision, pivot, predicate or ordering is refused', () => {
  for (const over of [
    {pageInfo: {start: 0, total: 48000, totalIsLowerBound: false, revision: 'r2', hasMore: false, nextCursor: ''}},
    {pivot: 'episodes'},
    {applied: {query: {variant:'predicate',field: 'year', operator: 'at-least', value: 2010}, sort, seek: null}},
    {applied: {query: filter, sort: [{field: 'year', direction: 'desc'}], seek: null}},
    {applied: {query: null, sort, seek: null}},
  ]) {
    assert.equal(parseBrowseResult(page(descriptor, over as Record<string, unknown>)).wholeSelection, undefined, JSON.stringify(over));
  }
});

test('the wire key is capabilityCounts and the selector is query-only', () => {
  const misspelled = {...descriptor, types: [{...descriptor.types[0], capabilityCounts: undefined, capabilities: capabilityCounts}]};
  assert.equal(parseWholeSelection(misspelled), undefined);
  const container = {...descriptor, selector: {query: descriptor.selector.query, container: {kind: 'library', id: 'movies'}}};
  assert.equal(parseWholeSelection(container), undefined);
  const unknownKey = {...descriptor, selector: {query: {...descriptor.selector.query, extra: 1}}};
  assert.equal(parseWholeSelection(unknownKey), undefined);
  const unknownCapability = {...descriptor, types: [{...descriptor.types[0], capabilityCounts: {...capabilityCounts, shuffle: 0}}]};
  assert.equal(parseWholeSelection(unknownCapability), undefined);
});

test('counts must partition one closed kind and one library', () => {
  for (const types of [
    [{...descriptor.types[0], capabilityCounts: {...capabilityCounts, play: 48001}}],
    [{...descriptor.types[0], libraries: [{libraryId: 'movies', count: 47999}]}],
    [{...descriptor.types[0], libraries: [{libraryId: 'tv', count: 48000}]}],
    [{...descriptor.types[0], libraries: [{libraryId: 'movies', count: 24000}, {libraryId: 'movies', count: 24000}]}],
    [{...descriptor.types[0], kind: 'podcast'}],
    [descriptor.types[0], descriptor.types[0]],
    [{...descriptor.types[0], count: -1}],
  ]) assert.equal(parseWholeSelection({...descriptor, types}), undefined);
  assert.equal(parseWholeSelection({...descriptor, catalogRevision: ''}), undefined);
  assert.equal(parseWholeSelection({...descriptor, total: 47999}), undefined);
});

test('a malformed filter or sort drops only the descriptor, never the page', () => {
  const badFilter = {...descriptor, selector: {query: {...descriptor.selector.query, filter: {variant:'predicate',field: 'year', operator: 'sideways', value: 1}}}};
  assert.equal(parseWholeSelection(badFilter), undefined);
  const badSort = {...descriptor, selector: {query: {...descriptor.selector.query, sort: [{field: 'title', direction: 'sideways'}]}}};
  assert.equal(parseWholeSelection(badSort), undefined);
  const parsed = parseBrowseResult(page(badFilter));
  assert.equal(parsed.wholeSelection, undefined);
  assert.equal(parsed.pageInfo.revision, 'r1');
});
