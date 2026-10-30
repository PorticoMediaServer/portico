import test from 'node:test';
import {nextToolbarSort} from '../src/presentation/browse-toolbar.ts';
import assert from 'node:assert/strict';
import {parseBrowseCapabilities,parseBrowseResult,parseBrowseFacets,validateBrowseQuery,formatBrowseAnchor,BrowseQueryBuilder,BrowseQueryError,browseWithin,facetGroupOf,type BrowseCapabilities} from '../src/browse.ts';
import {parsePinOrder,parseLibraryNavigationPins,parsePinnedLibraries,pinOrderRequest,libraryNavigationRequest} from '../src/library-pins.ts';

function capabilitiesPayload(changes: Record<string, unknown> = {}): any {
  return {
    library: {id: 'lib', name: 'Movies', kind: 'movie', defaultView: 'discover', pinned: false},
    pivots: [
      {id: 'discover', labelKey: 'library.discover', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['rail'], browsable: false},
      {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true},
    ],
    resolvedPivot: {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true},
    fields: [
      {id: 'title', labelKey: 'browse.field.title', type: 'string', operators: ['equals', 'starts-with', 'contains'], controlHint: 'text', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']},
      {id: 'year', labelKey: 'browse.field.year', type: 'number', operators: ['equals', 'between', 'at-least'], controlHint: 'number-range', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie'], facetSource: {endpoint: '/v1/libraries/{id}/facets', field: 'year'}},
      {id: 'playState', labelKey: 'browse.field.playState', type: 'enum', operators: ['equals'], controlHint: 'select', complexity: 'quick', cost: 'indexed-join', applicableKinds: ['movie'], allowedValues: ['unplayed', 'in-progress', 'played']},
      {id: 'favorite', labelKey: 'browse.field.favorite', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick', cost: 'indexed-join', applicableKinds: ['movie']},
      {id: 'genre', labelKey: 'browse.field.genre', type: 'identity-set', operators: ['contains', 'contains-any'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed-join', applicableKinds: ['movie'], facetSource: {endpoint: '/v1/libraries/{id}/facets', field: 'genre'}},
    ],
    sorts: [
      {id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']},
      {id: 'year', labelKey: 'sort.year', directions: ['asc', 'desc'], defaultDirection: 'desc', expensive: false, applicableKinds: ['movie']},
    ],
    quickFilters: [{id: 'favorites', labelKey: 'filter.favorites', query: {variant:'predicate',field: 'favorite', operator: 'equals', value: true}}],
    queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 1800000},
    ...changes,
  };
}
const capabilities: BrowseCapabilities = parseBrowseCapabilities(capabilitiesPayload());

function resultPayload(changes: Record<string, unknown> = {}): any {
  return {
    pivot: 'movies',
    applied: {query: {variant:'predicate',field: 'genre', operator: 'contains', value: 'Action'}, sort: [{field: 'title', direction: 'asc'}], seek: null},
    entries: [{id: 'm1', kind: 'movie', title: 'Alpha', navigation: {view: 'item', entityId: 'm1'}}],
    pageInfo: {start: 0, total: 3, revision: 'browse_abc', hasMore: true, nextCursor: 'cursor'},
    positionIndex: [{key: '#', index: 0}, {key: 'A', index: 1}],
    ...changes,
  };
}

test('capabilities parse only what the server can execute', () => {
  assert.equal(capabilities.resolvedPivot?.id, 'movies');
  assert.equal(capabilities.queryLimits.maximumDepth, 5);
  assert.equal(capabilities.fields.find(f => f.id === 'year')?.facetSource?.field, 'year');
  // The library, the query limits and the bounds of the lists stay exact.
  assert.throws(() => parseBrowseCapabilities(capabilitiesPayload({library: {id: '', name: 'Movies', kind: 'movie', defaultView: 'discover'}})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseCapabilities(capabilitiesPayload({fields: Array.from({length: 129}, (_, i) => ({id: `f${i}`, labelKey: 'k', type: 'string', operators: ['equals'], controlHint: 'text', complexity: 'quick', cost: 'indexed', applicableKinds: []}))})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseCapabilities(capabilitiesPayload({pivots: 'nope'})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseCapabilities(capabilitiesPayload({resolvedPivot: {id: 'ghost', labelKey: 'k', entityKinds: [], defaultSort: [], supportedViews: [], browsable: true}})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseCapabilities(capabilitiesPayload({queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 1, maximumSorts: 3, defaultLimit: 40, maximumLimit: 10, cursorTtlMs: 1000}})), {code:'invalid_browse'});
});

test('a filter, sort, pivot or quick filter this app cannot read is not offered and the rest are kept', () => {
  const field = {id: 'title', labelKey: 'k', type: 'string', operators: ['equals'], controlHint: 'text', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']};
  const parsed = parseBrowseCapabilities(capabilitiesPayload({
    fields: [
      field, {...field, labelKey: 'again'},
      {...field, id: 'unknownOperators', operators: ['nonsense']},
      {...field, id: 'someOperators', operators: ['equals', 'resembles']},
      {...field, id: 'unknownControl', controlHint: 'dial'},
      {...field, id: 'unknownType', type: 'colour'},
      {...field, id: 'hints', complexity: 'expert', cost: 'full-scan', future: {a: 1}},
      {...field, id: 'brokenFacet', facetSource: {endpoint: 7}},
      'nope',
    ],
    sorts: [
      {id: 'title', labelKey: 'sort.title', directions: ['asc', 'sideways'], defaultDirection: 'sideways', applicableKinds: ['movie']},
      {id: 'unknownDirections', labelKey: 'k', directions: ['sideways'], defaultDirection: 'sideways', expensive: false, applicableKinds: []},
      {id: 'year', labelKey: 'sort.year', directions: ['asc', 'desc'], defaultDirection: 'desc', expensive: true, applicableKinds: ['movie']},
    ],
    pivots: [
      {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie', 7], defaultSort: [{field: 'title', direction: 'asc'}, {field: 'year', direction: 'sideways'}], supportedViews: ['grid'], browsable: true},
      {id: 'unsure', labelKey: 'library.unsure'},
      {labelKey: 'no id'},
    ],
    quickFilters: [{id: 'favorites', labelKey: 'filter.favorites', query: {variant:'predicate',field: 'favorite', operator: 'equals', value: true}}, {id: 'future', labelKey: 'k', query: {variant:'predicate',field: 'mood', operator: 'resembles', value: 'calm'}}],
  }));
  assert.deepEqual(parsed.fields.map(f => f.id), ['title', 'someOperators', 'hints']);
  assert.equal(parsed.fields[0].labelKey, 'k', 'a repeated id keeps its first appearance');
  assert.deepEqual(parsed.fields[1].operators, ['equals']);
  assert.deepEqual([parsed.fields[2].complexity, parsed.fields[2].cost, 'future' in parsed.fields[2]], ['advanced', 'indexed-join', false]);
  assert.deepEqual(parsed.sorts.map(v => [v.id, v.directions, v.defaultDirection, v.expensive]), [['title', ['asc'], 'asc', false], ['year', ['asc', 'desc'], 'desc', true]]);
  assert.deepEqual(parsed.pivots, [
    {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid'], sorts: [], filters: [], browsable: true},
    {id: 'unsure', labelKey: 'library.unsure', entityKinds: [], defaultSort: [], supportedViews: [], sorts: [], filters: [], browsable: false},
  ]);
  assert.deepEqual(parsed.quickFilters.map(f => f.id), ['favorites']);
});

test('browse pages parse position index and page info; identity, what was applied and bounds stay exact', () => {
  const page = parseBrowseResult(resultPayload());
  assert.equal(page.pageInfo.total, 3);
  assert.deepEqual([...page.positionIndex], [{key: '#', index: 0}, {key: 'A', index: 1}]);
  assert.equal(page.applied.sort[0].field, 'title');
  assert.throws(() => parseBrowseResult(resultPayload({applied: {query: {variant:'predicate',field: 'genre', operator: 'nonsense', value: 'x'}, sort: [], seek: null}})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseResult(resultPayload({pivot: ''})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseResult(resultPayload({pageInfo: {start: 0, total: 3, revision: '', hasMore: true, nextCursor: 'cursor'}})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseResult(resultPayload({pageInfo: {start: 0, total: 3, revision: 'r', hasMore: true, nextCursor: 7}})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseResult(resultPayload({entries: 'nope'})), {code:'invalid_browse'});
  assert.throws(() => parseBrowseResult(resultPayload({entries: Array.from({length: 201}, (_, i) => ({id: `m${i}`, kind: 'movie', title: 'A'})), pageInfo: {start: 0, total: 500, revision: 'r', hasMore: true, nextCursor: ''}})), {code:'invalid_browse'});
  // A page none of whose entries can be read is refused rather than shown as empty.
  assert.throws(() => parseBrowseResult(resultPayload({entries: [{id: 'm1', kind: 'hologram', title: 'A'}]})), {code:'invalid_browse'});
});

test('one browse entry that cannot be read is left out, and the total and hasMore follow the rows the server sent', () => {
  const movie = (id: string) => ({id, kind: 'movie', title: id});
  const page = parseBrowseResult(resultPayload({entries: [movie('m1'), movie('m1'), {id: 'x', kind: 'hologram', title: 'Future'}, {id: 'broken'}, {...movie('m2'), year: 0, future: true}], pageInfo: {start: 0, total: 9, revision: 'r', hasMore: true, nextCursor: 'cursor'}}));
  assert.deepEqual(page.entries.map(e => e.id), ['m1', 'm2']);
  assert.deepEqual(page.entries[1], movie('m2'));
  assert.deepEqual([page.pageInfo.total, page.pageInfo.hasMore], [9, true]);
  // The total is at least where the page ends; hasMore says whether rows remain after it.
  const short = parseBrowseResult(resultPayload({entries: [movie('m1'), movie('m2'), movie('m3')], pageInfo: {start: 5, total: 1, revision: 'r', hasMore: true}}));
  assert.deepEqual([short.pageInfo.total, short.pageInfo.hasMore, short.pageInfo.nextCursor], [8, false, '']);
  // Anchors out of order or past the total are left out of the rail.
  assert.deepEqual([...parseBrowseResult(resultPayload({positionIndex: [{key: 'B', index: 2}, {key: 'A', index: 1}, {key: 'Z', index: 3}]})).positionIndex], [{key: 'B', index: 2}]);
});

test('M20: anchors parse on every sort, with empty keys and unknown shapes dropped', () => {
  const entry = (id: string) => ({id, kind: 'movie', title: `Title ${id}`});
  const ids = ['m1', 'm2', 'm3', 'm4'];
  // A year-sorted page: years plus an empty bucket for rows without a value.
  const year = parseBrowseResult(resultPayload({
    applied: {query: null, sort: [{field: 'year', direction: 'desc'}], seek: null},
    entries: ids.map(entry),
    pageInfo: {start: 0, total: 4, revision: 'r', hasMore: false, nextCursor: ''},
    positionIndex: [{key: '1995', index: 0}, {key: '1988', index: 2}, {key: '', index: 3}],
  }));
  assert.deepEqual([...year.positionIndex], [{key: '1995', index: 0}, {key: '1988', index: 2}, {key: '', index: 3}]);
  // The index is optional: continuation pages and older servers carry none.
  for (const missing of [undefined, null]) {
    const page = parseBrowseResult(resultPayload({
      entries: ids.map(entry),
      pageInfo: {start: 0, total: 4, revision: 'r', hasMore: false, nextCursor: ''},
      positionIndex: missing,
    }));
    assert.deepEqual([...page.positionIndex], []);
  }
  // Entries with an unknown shape are dropped; the page still parses.
  const dropped = parseBrowseResult(resultPayload({
    entries: ids.slice(0, 3).map(entry),
    pageInfo: {start: 0, total: 3, revision: 'r', hasMore: false, nextCursor: ''},
    positionIndex: [{key: '1995', index: 0}, {key: 99, index: 1}, null, {index: 2}, {key: '', index: 2}],
  }));
  assert.deepEqual([...dropped.positionIndex], [{key: '1995', index: 0}, {key: '', index: 2}]);
  // An anchor that is out of order is left out of the rail; the page is kept.
  assert.deepEqual([...parseBrowseResult(resultPayload({
    entries: ids.map(entry),
    pageInfo: {start: 0, total: 4, revision: 'r', hasMore: false, nextCursor: ''},
    positionIndex: [{key: '1995', index: 2}, {key: '1988', index: 1}],
  })).positionIndex], [{key: '1995', index: 2}]);
});

test('M20: anchor keys read as rail labels for every sort', () => {
  assert.equal(formatBrowseAnchor('1995', 'year', 'No value'), '1995');
  assert.equal(formatBrowseAnchor('1990s', 'year', 'No value'), '1990s');
  assert.equal(formatBrowseAnchor('D', 'title', 'No value'), 'D');
  assert.equal(formatBrowseAnchor('2026-09', 'added', 'No value'), 'Sep 2026');
  assert.equal(formatBrowseAnchor('2026-01', 'lastPlayed', 'No value'), 'Jan 2026');
  assert.equal(formatBrowseAnchor('2026', 'added', 'No value'), '2026');
  assert.equal(formatBrowseAnchor('8', 'communityRating', 'No value'), '8★');
  assert.equal(formatBrowseAnchor('0', 'personalRating', 'No value'), '0★');
  assert.equal(formatBrowseAnchor('90', 'duration', 'No value'), '1h 30m');
  assert.equal(formatBrowseAnchor('30', 'duration', 'No value'), '30m');
  assert.equal(formatBrowseAnchor('60', 'duration', 'No value'), '1h');
  assert.equal(formatBrowseAnchor('0', 'duration', 'No value'), '0m');
  assert.equal(formatBrowseAnchor('', 'year', 'No value'), 'No value');
  assert.equal(formatBrowseAnchor('', 'added', 'No value'), 'No value');
  // Unknown sorts and unparseable keys pass through untouched, never fatal.
  assert.equal(formatBrowseAnchor('1995', 'nonsense', 'No value'), '1995');
  assert.equal(formatBrowseAnchor('soon', 'duration', 'No value'), 'soon');
  assert.equal(formatBrowseAnchor('2026-13', 'added', 'No value'), '2026-13');
});

test('facet pages keep the values they can read; the field asked for and the bound stay exact', () => {
  const page = parseBrowseFacets({total: 6, field: 'genre', values: [{value: 'Action', label: 'Action', count: 2}]}, 'genre');
  assert.equal(page.values[0].count, 2);
  assert.throws(() => parseBrowseFacets({total: 6, field: 'genre', values: []}, 'year'), {code:'invalid_browse'});
  assert.throws(() => parseBrowseFacets({total: 6, field: 'genre', values: 'nope'}), {code:'invalid_browse'});
  assert.throws(() => parseBrowseFacets({total: 6, field: 'genre', values: Array.from({length: 201}, (_, i) => ({value: `v${i}`, label: 'x', count: 1}))}), {code:'invalid_browse'});
  const loose = parseBrowseFacets({total: 6, field: 'genre', future: 1, values: [{value: 'a', label: 'A', count: 1}, {value: 'a', label: 'Again', count: 2}, {label: 'no value', count: 1}, {value: 'b'}, {value: 'c', label: 'C', count: -1, future: true}, 7]});
  assert.deepEqual(loose.values, [{value: 'a', label: 'A', count: 1}, {value: 'b', label: 'b', count: 0}, {value: 'c', label: 'C', count: 0}]);
});

test('queries are validated against the published capabilities before they are sent', () => {
  validateBrowseQuery(capabilities, {variant:'predicate',field: 'year', operator: 'between', value: [1990, 1999]});
  for (const [node, field] of [
    [{variant:'predicate',field: 'studio', operator: 'equals', value: 'x'}, 'query.field'],
    [{variant:'predicate',field: 'title', operator: 'at-least', value: 'x'}, 'query.operator'],
    [{variant:'predicate',field: 'year', operator: 'equals', value: 'x'}, 'query.value'],
    [{variant:'predicate',field: 'playState', operator: 'equals', value: 'halfway'}, 'query.value'],
    [{variant:'predicate',field: 'favorite', operator: 'equals', value: 'yes'}, 'query.value'],
    [{variant:'predicate',field: 'year', operator: 'between', value: [1]}, 'query.value'],
    [{variant:'predicate',field: 'year', operator: 'between', value: 1990}, 'query.value'],
  ] as const) {
    assert.throws(() => validateBrowseQuery(capabilities, node as any), (e: unknown) => e instanceof BrowseQueryError && e.field === field);
  }
  let deep: any = {variant:'predicate',field: 'title', operator: 'equals', value: 'x'};
  for (let level = 0; level < 6; level += 1) deep = {variant:'group',not: deep};
  assert.throws(() => validateBrowseQuery(capabilities, deep), (e: unknown) => e instanceof BrowseQueryError && e.field.startsWith('query.not'));
});

test('the builder produces a request the server contract accepts', () => {
  const body = new BrowseQueryBuilder(capabilities, 'movies')
    .where('genre', 'contains', 'Action')
    .quickFilter('favorites')
    .sort('title', 'asc')
    .limit(25)
    .seek('d')
    .build();
  assert.deepEqual(body, {pivot: 'movies', query: {variant:'group',all: [{variant:'predicate',field: 'genre', operator: 'contains', value: 'Action'}, {variant:'predicate',field: 'favorite', operator: 'equals', value: true}]}, sort: [{field: 'title', direction: 'asc'}], limit: 25, seek: {prefix: 'd'}});
  assert.throws(() => new BrowseQueryBuilder(capabilities, 'discover'), (e: unknown) => e instanceof BrowseQueryError);
  assert.throws(() => new BrowseQueryBuilder(capabilities, 'songs'), (e: unknown) => e instanceof BrowseQueryError);
  assert.throws(() => new BrowseQueryBuilder(capabilities).limit(500), (e: unknown) => e instanceof BrowseQueryError && e.field === 'limit');
  assert.throws(() => new BrowseQueryBuilder(capabilities).sort('title').sort('title'), (e: unknown) => e instanceof BrowseQueryError);
  assert.throws(() => new BrowseQueryBuilder(capabilities).sort('nonsense'), (e: unknown) => e instanceof BrowseQueryError);
  assert.throws(() => new BrowseQueryBuilder(capabilities).sort('year', 'desc').seek('a'), (e: unknown) => e instanceof BrowseQueryError && e.field === 'seek');
  assert.throws(() => new BrowseQueryBuilder(capabilities).cursor('c').range({start: 0}), (e: unknown) => e instanceof BrowseQueryError);
  const ranged = new BrowseQueryBuilder(capabilities).range({start: 40, revision: 'browse_abc', anchorId: 'm4'}).build();
  assert.deepEqual(ranged.range, {start: 40, revision: 'browse_abc', anchorId: 'm4'});
});

test('pin arrangements are read and written as server state', () => {
  const order = parsePinOrder({serverId: 'server', viewerFence: 'fence', revision: 2, order: [{kind: 'view', id: 'a'}, {kind: 'collection', id: 'b'}]}, 'server');
  assert.equal(order.order[0].id, 'a');
  assert.throws(() => parsePinOrder({serverId: 'other', viewerFence: 'f', revision: 1, order: []}, 'server'), {code:'invalid_pins'});
  assert.throws(() => parsePinOrder({serverId: 'server', viewerFence: 'f', revision: 1, order: [{kind: 'view', id: 'a'}, {kind: 'view', id: 'a'}]}, 'server'), {code:'invalid_pins'});
  assert.throws(() => parsePinOrder({serverId: 'server', viewerFence: 'f', revision: 1, order: [{kind: 'library', id: 'a'}]}, 'server'), {code:'invalid_pins'});

  const pins = parseLibraryNavigationPins({pinnedLibraryIds: ['a', 'b'], revision: 3});
  assert.deepEqual([...pins.pinnedLibraryIds], ['a', 'b']);
  assert.throws(() => parseLibraryNavigationPins({pinnedLibraryIds: ['a', 'a'], revision: 1}), {code:'invalid_pins'});

  const libraries = parsePinnedLibraries({items: [{id: 'b', name: 'B', kind: 'tv', defaultView: 'browse', pinned: true}, {id: 'a', name: 'A', kind: 'movie', defaultView: 'discover', pinned: false}]});
  assert.equal(libraries[0].id, 'b');
  assert.throws(() => parsePinnedLibraries({items: [{id: 'a', name: 'A', kind: 'movie', defaultView: 'discover', pinned: false}, {id: 'b', name: 'B', kind: 'tv', defaultView: 'browse', pinned: true}]}), {code:'invalid_pins'});

  assert.deepEqual(pinOrderRequest(2, [{kind: 'view', id: 'a'}]), {expectedRevision: 2, order: [{kind: 'view', id: 'a'}]});
  assert.throws(() => pinOrderRequest(-1, []), /cannot be saved/i);
  assert.deepEqual(libraryNavigationRequest(0, ['a']), {expectedRevision: 0, pinnedLibraryIds: ['a']});
  assert.throws(() => libraryNavigationRequest(0, ['a', 'a']), /cannot be saved/i);
});

test('surface options and a stable random seed survive parsing and request building', () => {
  const raw=capabilitiesPayload();
  raw.pivots[1].sorts=['title','random'];raw.pivots[1].filters=['title'];raw.pivots[1].paging='keyset';
  raw.resolvedPivot={...raw.pivots[1]};raw.queryLimits.maximumSorts=1;
  raw.sorts.push({id:'random',labelKey:'sort.random',directions:['asc','desc'],defaultDirection:'asc',expensive:false,seeded:true,applicableKinds:['movie']});
  raw.surfaces=[{id:'library',endpoint:'/v1/libraries/{id}/browse',sorts:['title','random'],filters:['title'],paging:'keyset'}];
  const parsed=parseBrowseCapabilities(raw);
  assert.deepEqual(parsed.resolvedPivot?.sorts,['title','random']);
  assert.equal(parsed.surfaces?.[0].paging,'keyset');
  assert.deepEqual(new BrowseQueryBuilder(parsed).sort('random','asc',123).build().sort,[{field:'random',direction:'asc',seed:123}]);
  assert.deepEqual(nextToolbarSort(parsed,{field:'random',direction:'asc',seed:123},'random'),{field:'random',direction:'desc',seed:123});
  assert.throws(()=>new BrowseQueryBuilder(parsed).sort('year'),BrowseQueryError);
  assert.throws(()=>new BrowseQueryBuilder(parsed).sort('title','asc',1),BrowseQueryError);
  assert.throws(()=>new BrowseQueryBuilder(parsed).sort('random','asc',-1),BrowseQueryError);
  assert.throws(()=>new BrowseQueryBuilder(parsed).sort('random','asc',4294967296),BrowseQueryError);
  const page=parseBrowseResult(resultPayload({applied:{query:null,sort:[{field:'random',direction:'asc',seed:123}]}}));
  assert.equal(page.applied.sort[0].seed,123);
});


test('a container scope keeps the full sort set and adds its membership to filters',()=>{
 const raw=capabilitiesPayload();
 raw.fields.push({id:'memberOf',labelKey:'browse.field.memberOf',type:'identity-set',operators:['equals'],controlHint:'text',complexity:'standard',cost:'indexed-join',applicableKinds:['movie']});
 raw.pivots[1].filters=['memberOf','year'];raw.pivots[1].sorts=['title','year'];raw.resolvedPivot={...raw.pivots[1]};
 raw.surfaces=[{id:'container-titles',endpoint:'/v1/libraries/{id}/browse',membershipField:'memberOf',sorts:['title','year'],filters:['memberOf','year'],paging:'keyset'}];
 const parsed=parseBrowseCapabilities(raw);
 const body=browseWithin(parsed,'container-titles','container').where('year','at-least',2000).sort('year','desc').build();
 assert.deepEqual(body.query,{variant:'group',all:[{variant:'predicate',field:'memberOf',operator:'equals',value:'container'},{variant:'predicate',field:'year',operator:'at-least',value:2000}]});
 assert.deepEqual(body.sort,[{field:'year',direction:'desc'}]);
 assert.throws(()=>browseWithin(parsed,'person-titles','person'),BrowseQueryError);
});

test('a total counted only to the threshold is open-ended until the last page', () => {
  const entries = (start: number, n: number) => Array.from({length: n}, (_, i) => ({id: `m${start + i}`, kind: 'movie', title: `Film ${start + i}`, navigation: {view: 'item', entityId: `m${start + i}`}}));
  const first = parseBrowseResult(resultPayload({entries: entries(0, 2), pageInfo: {start: 0, total: 10000, totalIsLowerBound: true, revision: 'browse_abc', hasMore: true, nextCursor: 'c'}, positionIndex: []}));
  assert.equal(first.pageInfo.total, 10000);
  assert.equal(first.pageInfo.totalIsLowerBound, true);
  assert.equal(first.pageInfo.hasMore, true);
  // A range read past the threshold: more follows, so the total reaches a page beyond it.
  const deep = parseBrowseResult(resultPayload({entries: entries(10040, 2), pageInfo: {start: 10040, total: 10000, totalIsLowerBound: true, revision: 'browse_abc', hasMore: true, nextCursor: ''}, positionIndex: []}));
  assert.equal(deep.pageInfo.total, 10044);
  assert.equal(deep.pageInfo.hasMore, true);
  // The last page knows the end and the exact count.
  const last = parseBrowseResult(resultPayload({entries: entries(12000, 2), pageInfo: {start: 12000, total: 10000, totalIsLowerBound: true, revision: 'browse_abc', hasMore: false, nextCursor: ''}, positionIndex: []}));
  assert.equal(last.pageInfo.total, 12002);
  assert.equal(last.pageInfo.totalIsLowerBound, false);
  assert.equal(last.pageInfo.hasMore, false);
  // An exact total is unchanged.
  const exact = parseBrowseResult(resultPayload());
  assert.equal(exact.pageInfo.totalIsLowerBound, false);
  assert.equal(exact.pageInfo.total, 3);
});

test('a list across libraries names its group for its filter counts', () => {
  assert.deepEqual(facetGroupOf([{variant:'predicate',field: 'list', operator: 'equals', value: 'pl1'}, {variant:'predicate',field: 'genre', operator: 'equals', value: 'Drama'}]), {group: 'list', value: 'pl1'});
  assert.deepEqual(facetGroupOf([{variant:'predicate',field: 'favorite', operator: 'equals', value: true}]), {group: 'favorite', value: ''});
  assert.equal(facetGroupOf([{variant:'predicate',field: 'genre', operator: 'equals', value: 'Drama'}]), undefined);
});

test('CB-1/2/3/5/6: contextual facets, batch counts, owner sort and remembered totals stay honest', async () => {
 const {browseContextFacetsPath,browseCountsRequest,parseBrowseCounts,browseCountsPath,BrowseTotalsMemory}=await import('../src/browse.ts');
 const query={variant:'group',all:[{variant:'predicate',field:'year',operator:'at-least' as const,value:2000},{variant:'predicate',field:'genre',operator:'contains' as const,value:'Drama'}]};
 const url=new URL(browseContextFacetsPath('lib','genre','movies',query,'com',10),'https://private.invalid');
 assert.equal(url.pathname,'/v1/libraries/lib/facets');
 assert.deepEqual(JSON.parse(url.searchParams.get('query')!),query);
 assert.equal(url.searchParams.get('pivot'),'movies');
 assert.equal(url.searchParams.get('q'),'com');
 const facets=parseBrowseFacets({field:'actor',total:500,totalIsLowerBound:true,revision:'revision',values:[{value:'a',label:'An actor',count:10000,countIsLowerBound:true}]},'actor');
 assert.equal(facets.totalIsLowerBound,true);assert.equal(facets.values[0].countIsLowerBound,true);
 assert.deepEqual(browseCountsRequest('movies',[{id:'relax',query}]),{pivot:'movies',queries:[{id:'relax',query}]});
 assert.equal(browseCountsPath('lib'),'/v1/libraries/lib/browse/counts');assert.equal(browseCountsPath('*'),'/v1/browse/counts');
 assert.throws(()=>browseCountsRequest('movies',Array.from({length:9},(_,i)=>({id:String(i),query:null}))));
 assert.throws(()=>parseBrowseCounts({counts:[{id:'wrong',total:10,totalIsLowerBound:true,revision:'r'}]},['wanted']));
 const counts=parseBrowseCounts({counts:[{id:'relax',total:10000,totalIsLowerBound:true,revision:'r'}]},['relax']);assert.equal(counts[0].totalIsLowerBound,true);
 const owner=parseBrowseCapabilities(capabilitiesPayload({sorts:[{id:'fileSize',labelKey:'sort.fileSize',directions:['asc','desc'],defaultDirection:'desc',ownerOnly:true,applicableKinds:['movie']}]}));
 assert.equal(owner.sorts[0].ownerOnly,true);
 const memory=new BrowseTotalsMemory();const base=parseBrowseResult(resultPayload({applied:{query:null,sort:[]},pageInfo:{start:0,total:80,totalIsLowerBound:false,revision:'a',hasMore:true,nextCursor:'c'}}));
 assert.deepEqual(memory.observe('viewer:library',base,true),{total:80,totalIsLowerBound:false});
 const filtered=parseBrowseResult(resultPayload({pageInfo:{start:0,total:10,revision:'b',hasMore:true,nextCursor:'c',durationMs:9120000,durationIsLowerBound:true}}));
 assert.deepEqual(memory.observe('viewer:library',filtered,false),{total:80,totalIsLowerBound:false});assert.equal(memory.observe('other-viewer:library',filtered,false),undefined);
 assert.equal(filtered.pageInfo.durationMs,9120000);assert.equal(filtered.pageInfo.durationIsLowerBound,true);
 memory.clear();assert.equal(memory.observe('viewer:library',filtered,false),undefined);
});

test('Home display pages keep exact membership order and actual entry identity',async()=>{
 const {browseMembershipId,browseMembershipAnchor}=await import('../src/browse.ts');
 const page=parseBrowseResult(resultPayload({entries:[{id:'ep',kind:'episode',title:'Actual episode'},{id:'season',kind:'season',title:'Actual season'}],displayTargets:[{membershipId:'show1',entryId:'ep'},{membershipId:'show2',entryId:'season'}],pageInfo:{start:0,total:2,revision:'r',hasMore:false,nextCursor:''}}));
 assert.equal(page.entries[0].id,'ep');assert.equal(browseMembershipId(page,0),'show1');assert.equal(browseMembershipId(page,1),'show2');assert.equal(browseMembershipAnchor(page,'ep'),'show1');
 assert.equal(browseMembershipAnchor(page,'missing'),undefined);
 assert.throws(()=>parseBrowseResult(resultPayload({displayTargets:[{membershipId:'show',entryId:'different'}]})));
 assert.throws(()=>parseBrowseResult(resultPayload({displayTargets:[]})));
 assert.throws(()=>parseBrowseResult(resultPayload({entries:[{id:'ep',kind:'episode',title:'Actual episode'},{id:'broken',kind:'not-a-known-kind',title:''}],displayTargets:[{membershipId:'show1',entryId:'ep'},{membershipId:'show2',entryId:'broken'}]})));
});

test('capped totals never hide Unicode rail keys beyond the count or invent an exact empty end',()=>{
 const index=Array.from({length:300},(_,i)=>({key:String.fromCodePoint(0x4e00+i),index:10000+i}));
 const page=parseBrowseResult(resultPayload({pageInfo:{start:0,total:10000,totalIsLowerBound:true,revision:'r',hasMore:true,nextCursor:'c'},positionIndex:index}));
 assert.deepEqual(page.positionIndex,index);assert.equal(page.pageInfo.total,10300);assert.equal(page.pageInfo.totalIsLowerBound,true);
 const empty=parseBrowseResult(resultPayload({entries:[],pageInfo:{start:99999,total:10000,totalIsLowerBound:true,revision:'r',hasMore:false,nextCursor:''},positionIndex:[]}));
 assert.equal(empty.pageInfo.totalIsLowerBound,true);assert.equal(empty.pageInfo.total,10000);
});

test('GP-CB-1 and CB-6: member/TV toolbar consumes match and ownerOnly capabilities',async()=>{
 const {toolbarModel}=await import('../src/presentation/browse-toolbar.ts');
 const caps=parseBrowseCapabilities(capabilitiesPayload({fields:[{id:'match',labelKey:'browse.field.match',type:'enum',operators:['equals'],controlHint:'select',complexity:'standard',cost:'indexed-join',applicableKinds:['movie'],allowedValues:['matched','unmatched']},{id:'actor',labelKey:'browse.field.actor',type:'identity-set',operators:['equals'],controlHint:'facet-multi-select',complexity:'advanced',cost:'indexed-join',applicableKinds:['movie'],facetSource:{endpoint:'/v1/libraries/{id}/facets',field:'actor'}}],sorts:[{id:'fileSize',labelKey:'sort.fileSize',directions:['desc'],defaultDirection:'desc',ownerOnly:true,applicableKinds:['movie']},{id:'title',labelKey:'sort.title',directions:['asc'],defaultDirection:'asc',applicableKinds:['movie']}]}));
 const member=toolbarModel(caps,'movie',[],{field:'title',direction:'asc'},{owner:false,textEntry:false});
 assert.deepEqual(member.groups.flatMap(g=>g.fields).map(f=>f.id).sort(),['actor','match']);assert.deepEqual(member.sorts.map(s=>s.id),['title']);
 const owner=toolbarModel(caps,'movie',[],{field:'title',direction:'asc'},{owner:true});assert.equal(owner.sorts.some(s=>s.id==='fileSize'),true);
});

test('public music Unmatched navigation follows actual capabilities for member, owner and TV',async()=>{
 const {libraryTabs,canBrowseMusicUnmatched}=await import('../src/presentation/library-tabs.ts');
 const {browsePath}=await import('../src/browse.ts');
 const caps=parseBrowseCapabilities(capabilitiesPayload({library:{id:'music',name:'Music',kind:'music',defaultView:'discover'},pivots:[{id:'albums',labelKey:'library.albums',entityKinds:['album'],defaultSort:[{field:'title',direction:'asc'}],supportedViews:['grid'],browsable:true,filters:['match']}],resolvedPivot:{id:'albums',labelKey:'library.albums',entityKinds:['album'],defaultSort:[{field:'title',direction:'asc'}],supportedViews:['grid'],browsable:true,filters:['match']},fields:[{id:'match',labelKey:'browse.field.match',type:'enum',operators:['equals'],controlHint:'select',complexity:'standard',cost:'indexed-join',applicableKinds:['album'],allowedValues:['matched','unmatched']}]}));
 assert.equal(canBrowseMusicUnmatched(caps),true);
 for(const caller of ['member','owner','tv']){
  const tabs=libraryTabs('music',{owner:caller==='owner',unmatched:canBrowseMusicUnmatched(caps)});const tab=tabs.find(t=>t.id==='unmatched')!;
  assert.equal(tab.pivot,'albums');assert.deepEqual(tab.fixedFilter,[{variant:'predicate',field:'match',operator:'equals',value:'unmatched'}]);assert.equal(browsePath(caps.library.id),'/v1/libraries/music/browse');
 }
 const local={...caps,fields:caps.fields.filter(f=>f.id!=='match'),pivots:caps.pivots.map(p=>({...p,filters:[]}))};
 for(const unavailable of [undefined,local])for(const owner of [false,true])assert.equal(libraryTabs('music',{owner,unmatched:canBrowseMusicUnmatched(unavailable)}).some(t=>t.id==='unmatched'),false);
 assert.equal(canBrowseMusicUnmatched({...caps,library:{...caps.library,kind:'audiobook'}}),false);
 assert.equal(canBrowseMusicUnmatched({...caps,pivots:caps.pivots.map(p=>({...p,filters:undefined}))}),false);
 assert.equal(canBrowseMusicUnmatched({...caps,pivots:caps.pivots.map(p=>({...p,filters:[]}))}),false);
});

test('Reading facet windows use the admitted single-library offset',async()=>{
 const {browseContextFacetsPath,acrossLibraries}=await import('../src/browse.ts');
 const url=new URL(browseContextFacetsPath('reading','author','authors',undefined,'',1,1),'https://private.invalid');
 assert.equal(url.searchParams.get('offset'),'1');
 assert.equal(url.searchParams.get('limit'),'1');
 assert.throws(()=>browseContextFacetsPath('reading','author','authors',undefined,'',1,-1));
 assert.throws(()=>browseContextFacetsPath(acrossLibraries,'author','authors',undefined,'',1,1));
});

test('Reading facets preserve full accepted values and only server-issued series navigation IDs',()=>{
 const value='Accepted '+ 'a'.repeat(1000), seriesId='s'.repeat(43);
 const page=parseBrowseFacets({field:'series',total:1,values:[{value,label:value,count:2,seriesId}]},'series');
 assert.equal(page.values[0].value,value);assert.equal(page.values[0].label,value);assert.equal(page.values[0].seriesId,seriesId);
 const authors=parseBrowseFacets({field:'author',total:1,values:[{value,label:value,count:2,seriesId}]},'author');
 assert.equal(authors.values[0].seriesId,undefined);
 const malformed=parseBrowseFacets({field:'series',total:1,values:[{value,label:value,count:2,seriesId:'invented'}]},'series');
 assert.equal(malformed.values[0].seriesId,undefined);assert.equal(malformed.values[0].value,value);
});
