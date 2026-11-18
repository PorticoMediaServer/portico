import test from 'node:test';
import assert from 'node:assert/strict';
import {acrossCondition, acrossOwnOrder, acrossPivot, episodeGroupCondition, episodeGroupQuery, groupMemoryId, groupMemoryScope, groupSort} from '../src/presentation/browse-groups.ts';
import {acrossLibraries, libraryTabSorts, parseBrowseCapabilities, parseBrowseResult} from '../src/browse.ts';
import {bucketIndexFor, titleBucketValue} from '../src/browse-buckets.ts';

test('a group keeps its own order under a filter when the server offers it', () => {
  const fallback = {field: 'title', direction: 'asc'} as const;
  assert.deepEqual(groupSort(undefined, true, [{id: 'title'}, {id: 'groupOrder'}], fallback), {field: 'groupOrder', direction: 'asc'});
  assert.deepEqual(groupSort(undefined, true, [{id: 'title'}], fallback), fallback);
  assert.deepEqual(groupSort(undefined, false, [{id: 'groupOrder'}], fallback), fallback);
  assert.deepEqual(groupSort({field: 'year', direction: 'desc'}, true, [{id: 'groupOrder'}], fallback), {field: 'year', direction: 'desc'});
});

test('groups across libraries name their condition, pivot and own order', () => {
  assert.deepEqual(acrossCondition('watchlist'), {variant:'predicate',field: 'watchlisted', operator: 'equals', value: true});
  assert.deepEqual(acrossCondition('history'), {variant:'predicate',field: 'lastPlayedAt', operator: 'is-present', value: null});
  assert.deepEqual(acrossCondition('cast', 'p1'), {variant:'predicate',field: 'cast', operator: 'equals', value: 'p1'});
  assert.equal(acrossPivot('list'), 'items');
  assert.equal(acrossPivot('person'), 'titles');
  assert.equal(acrossOwnOrder('savedCollection'), true, 'a personal collection has its own order');
  assert.equal(acrossOwnOrder('watchlist'), false);
  assert.deepEqual(episodeGroupCondition('s1', 'range:100'), {variant:'predicate',field: 'episodeGroup', operator: 'equals', value: 's1/range:100'});
});

test('a vocabulary across libraries is read, with offset paging and the group order marked', () => {
  const pivot = {id: 'titles', labelKey: 'library.titles', entityKinds: ['movie', 'show'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid'], browsable: true, sorts: ['title', 'groupOrder'], filters: ['year'], paging: 'offset'};
  const caps = parseBrowseCapabilities({
    library: {id: '', name: '', kind: '', defaultView: ''},
    pivots: [pivot], resolvedPivot: pivot,
    fields: [{id: 'year', labelKey: 'browse.field.year', type: 'number', operators: ['at-least'], controlHint: 'number-range', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie'], facetSource: {endpoint: '/v1/browse/facets', field: 'year'}}],
    sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}, {id: 'groupOrder', labelKey: 'sort.groupOrder', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, requiresGroup: true, applicableKinds: ['movie']}],
    quickFilters: [],
    surfaces: [{id: 'watchlist', endpoint: '/v1/browse', membershipField: 'watchlisted', sorts: ['title'], filters: ['year'], paging: 'offset'}],
    queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 1800000},
  });
  assert.equal(caps.library.id, acrossLibraries);
  assert.equal(caps.resolvedPivot?.paging, 'offset');
  assert.equal(caps.surfaces?.[0]?.paging, 'offset');
  assert.deepEqual(libraryTabSorts(caps.sorts).map(sort => sort.id), ['title']);
});

test('B-07.10: the server letter of an entry finds its rail anchor, articles and all', () => {
  const page = parseBrowseResult({pivot: 'movies', applied: {query: null, sort: [{field: 'title', direction: 'asc'}]}, entries: [{id: 'm1', kind: 'movie', title: 'The Wire', sortLetter: 'W'}], pageInfo: {start: 0, total: 30, revision: 'r', hasMore: true, nextCursor: ''}, positionIndex: [{key: 'T', index: 10}, {key: 'W', index: 20}]});
  const entry = page.entries[0]!;
  assert.equal(entry.sortLetter, 'W');
  assert.equal(bucketIndexFor(page.positionIndex, 'title', titleBucketValue(entry)), 20);
  // Without the server's letter the display title's first letter is the guess.
  assert.equal(bucketIndexFor(page.positionIndex, 'title', titleBucketValue({title: 'The Wire'})), 10);
});

test('a group is remembered under a short stable name, apart from libraries', () => {
  const a = groupMemoryId('lib', 'movies', [{variant:'predicate',field: 'collection', operator: 'equals', value: 'c1'}]);
  assert.equal(a, groupMemoryId('lib', 'movies', [{variant:'predicate',field: 'collection', operator: 'equals', value: 'c1'}]));
  assert.notEqual(a, groupMemoryId('lib', 'movies', [{variant:'predicate',field: 'collection', operator: 'equals', value: 'c2'}]));
  assert.ok(a.length < 40);
  assert.deepEqual(groupMemoryScope({serverId: 's', viewerId: 'v'}), {serverId: 's', viewerId: 'v#groups'});
});

test('an episode group plays whole through the server in its own order', () => {
  assert.deepEqual(episodeGroupQuery('tv', 'show1', 'range:100'), {libraryId: 'tv', pivot: 'episodes', query: {variant:'group',all: [{variant:'predicate',field: 'episodeGroup', operator: 'equals', value: 'show1/range:100'}]}, sort: [{field: 'groupOrder', direction: 'asc'}]});
});
