import test from 'node:test';
import assert from 'node:assert/strict';
import {decodeBrowseLibraryResponse} from '../../contracts/src/decoders.ts';
import {schemas} from '../../contracts/src/schemas.ts';
import type {BrowseLibraryResponse} from '../../contracts/src/types.ts';
import {parseBrowsePreparing} from '../src/browse-preparation.ts';
import {defaultI18n} from '../../i18n/src/index.ts';
import {BrowseQueryBuilder, BrowseQueryError, browseContextFacetsPath, browseCountsRequest, parseBrowseCapabilities, parseBrowseNode, parseBrowseResult, validateBrowseQuery, type BrowseCapabilities, type BrowseFieldCapability, type BrowseNode} from '../src/browse.ts';
import {validateHomeSeeAll} from '../src/home.ts';
import {parseContentSeeAll} from '../src/library-content.ts';
import {PersonalSavedService} from '../src/personal-saved.ts';
import {savedViewSummary} from '../src/presentation/saved-view-summary.ts';

/**
 * Relative recency (`within-last-days`) meets four layers that do not all say the same
 * thing:
 *
 *  1. the generated contract's top-level relative branch restricts the operator to the
 *     three published date fields, for 1–36500 whole days. Its group children remain
 *     permissive; the nested cases below record that separate limitation;
 *  2. `parseBrowseNode` checks structure, not vocabulary: it refuses an unknown operator
 *     or a bad day count but knows no field list;
 *  3. `parseBrowseResult` echoes `applied.query` through that structural parser;
 *  4. `validateBrowseQuery`/`BrowseQueryBuilder` are capability checks: they refuse an
 *     operator the published field capability does not offer.
 *
 * These tests record what each layer does today. A top-level window on a non-date field
 * is refused by (1) and (4) and admitted by (2) and (3). Nesting it bypasses (1), not (4).
 *
 * (2) does not only read server responses. `PersonalSavedService.definition` uses it for
 * the definition a create or update intent sends, and `browseContextFacetsPath` uses it to
 * serialize an outgoing facet link. A capability-free parser therefore does not establish
 * that every send is validated: (4) is the validation, and it is not on those two paths.
 *
 * The same window crosses the other readers verbatim — the published vocabulary and its
 * quick filters, a See all destination, a Home row's See all, a counts request, a facet
 * link and a saved view's stored definition — and none of them turns it into a date
 * computed from the clock. BrowseQueryBuilder and the saved-view summary perform
 * capability checks; the transport paths above do not establish that a query is valid.
 */

const OPERATOR = 'within-last-days';
const DATE_FIELDS = ['dateAdded', 'releaseDate', 'lastPlayedAt'] as const;

// Relative predicates belong to a ready page, not the separate preparing reply.
const readySchemas = schemas.BrowseLibraryResponse.anyOf.filter((shape): shape is Extract<typeof shape, {properties: {applied: unknown}}> => 'applied' in shape.properties);
assert.equal(readySchemas.length, 1, 'the contract has exactly one ready page branch');
const nodeSchema = readySchemas[0].properties.applied.properties.query;
const relative = nodeSchema.oneOf.find((shape): shape is Extract<typeof shape, {properties: {field: {enum: unknown}}}> => 'enum' in shape.properties.field);
const general = nodeSchema.oneOf.find((shape): shape is Exclude<typeof shape, {properties: {field: {enum: unknown}}}> => !('enum' in shape.properties.field));
assert.ok(relative, 'the ready query has a relative date predicate');
assert.ok(general, 'the ready query has a general predicate/group branch');

function dateField(id: string): BrowseFieldCapability {
  return {id, type: 'date', labelKey: `browse.field.${id}`, controlHint: 'date-range', complexity: 'standard', cost: 'indexed', operators: ['within-last-days', 'equals', 'at-most', 'at-least', 'between', 'is-present', 'is-missing'], applicableKinds: ['movie']};
}

const yearField: BrowseFieldCapability = {id: 'year', type: 'number', labelKey: 'browse.field.year', controlHint: 'number-range', complexity: 'quick', cost: 'indexed', operators: ['equals', 'at-most', 'at-least', 'between'], applicableKinds: ['movie']};

/** The vocabulary the server publishes today, narrowed to what these cases read. */
function capabilities(maximumDepth = 5): BrowseCapabilities {
  const pivot = {id: 'movies', labelKey: 'browse.pivot.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc' as const}], supportedViews: ['grid'], browsable: true};
  return {
    library: {id: 'library', name: 'Library', kind: 'movie', defaultView: 'browse', pinned: false},
    pivots: [pivot], resolvedPivot: pivot,
    fields: [dateField('dateAdded'), dateField('releaseDate'), dateField('lastPlayedAt'), yearField],
    sorts: [{id: 'title', labelKey: 'browse.sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}],
    quickFilters: [],
    queryLimits: {maximumDepth, maximumClauses: 40, maximumBytes: 8192, maximumSorts: 3, defaultLimit: 50, maximumLimit: 200, cursorTtlMs: 60000},
  };
}

/** A page the generated contract accepts, carrying the applied query under test. */
type ReadyBrowsePage = Extract<BrowseLibraryResponse, {applied: unknown}>;
function page(query: BrowseNode | null): ReadyBrowsePage {
  return {pivot: 'movies', applied: {query: query as ReadyBrowsePage['applied']['query'], sort: [{field: 'title', direction: 'asc'}]}, entries: [], pageInfo: {start: 0, total: 0, totalIsLowerBound: false, revision: 'revision-1', hasMore: false, nextCursor: '', durationMs: 0, durationIsLowerBound: false}, positionIndex: []};
}

const queryError = (path: string) => (error: unknown) => error instanceof BrowseQueryError && error.code === 'invalid_query' && error.field === path;

/** A server vocabulary carrying the relative window, one operator it does not know and
 * one quick filter this app cannot read. */
const vocabulary = {
  library: {id: 'library', name: 'Library', kind: 'movie', defaultView: 'browse'},
  pivots: [{id: 'movies', labelKey: 'browse.pivot.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid'], browsable: true, filters: ['dateAdded', 'year']}],
  fields: [
    {id: 'dateAdded', labelKey: 'browse.field.dateAdded', type: 'date', controlHint: 'date-range', operators: [OPERATOR, 'within-last-weeks', 'at-least'], applicableKinds: ['movie']},
    {id: 'year', labelKey: 'browse.field.year', type: 'number', controlHint: 'number-range', operators: ['at-least', 'between'], applicableKinds: ['movie']},
  ],
  sorts: [{id: 'added', labelKey: 'sort.added', directions: ['asc', 'desc'], defaultDirection: 'desc', applicableKinds: ['movie']}],
  quickFilters: [
    {id: 'recent', labelKey: 'filter.quick.recent', query: {field: 'dateAdded', operator: OPERATOR, value: 30}},
    {id: 'invented', labelKey: 'filter.quick.invented', query: {field: 'dateAdded', operator: 'within-last-weeks', value: 4}},
  ],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 10000},
};

test('ready pages carry applied queries and preparing replies stay in their typed protocol branch', () => {
  const ready = decodeBrowseLibraryResponse(page({field: 'dateAdded', operator: OPERATOR, value: 30}));
  assert.ok('applied' in ready, 'a ready response exposes the applied query');
  assert.deepEqual(parseBrowseResult(ready).applied.query, {field: 'dateAdded', operator: OPERATOR, value: 30});
  const pending = {state: 'preparing', kind: 'reading-browse-preparing', queryId: 'query-1', retryAfterMs: 25} satisfies Exclude<BrowseLibraryResponse, ReadyBrowsePage>;
  const preparing = decodeBrowseLibraryResponse(pending);
  assert.ok('state' in preparing, 'a preparing response exposes its poll state');
  assert.deepEqual({...preparing}, pending);
  assert.equal('applied' in preparing, false);
  assert.deepEqual(parseBrowsePreparing(preparing, 'reading-browse'), {queryId: 'query-1', retryAfterMs: 25});
  assert.throws(() => parseBrowseResult(preparing), {code: 'invalid_browse'});
  for (const malformed of [{...pending, kind: 'reading-facet-preparing'}, {...pending, queryId: ''}, {...pending, retryAfterMs: 0}]) {
    assert.throws(() => decodeBrowseLibraryResponse(malformed), /Invalid contract value/);
  }
});

test('the published relative window is exactly the three date fields, in whole days from 1 to 36500', () => {
  assert.deepEqual([...relative.properties.field.enum].sort(), [...DATE_FIELDS].sort());
  assert.deepEqual(relative.properties.value, {type: 'integer', minimum: 1, maximum: 36500});
  // The general predicate shape does not carry the relative operator: it is only a date
  // window on one of those fields, never a scalar value.
  const generalOperators: readonly string[] = general.properties.operator.enum;
  assert.equal(generalOperators.includes(OPERATOR), false);
  for (const field of DATE_FIELDS) for (const value of [1, 7, 30, 90, 365, 36500]) {
    const node = {field, operator: OPERATOR, value};
    assert.deepEqual(parseBrowseNode(node), node, `${field} ${value} is read exactly`);
    assert.doesNotThrow(() => validateBrowseQuery(capabilities(), node as BrowseNode), `${field} ${value} is offered`);
    assert.doesNotThrow(() => decodeBrowseLibraryResponse(page(node as BrowseNode)), `${field} ${value} is published`);
    assert.deepEqual(parseBrowseResult(page(node as BrowseNode)).applied.query, node);
  }
});

test('a day count outside 1–36500 is refused by the contract, the structural parser and the send guard', () => {
  for (const value of [0, -1, 36501, 1.5, '30', null, true, [], {}, NaN, Infinity]) {
    const node = {field: 'dateAdded', operator: OPERATOR, value};
    assert.throws(() => parseBrowseNode(node), {code: 'invalid_browse'}, `parse ${String(value)}`);
    assert.throws(() => decodeBrowseLibraryResponse(page(node as never)), /Invalid contract value/, `decode ${String(value)}`);
    assert.throws(() => validateBrowseQuery(capabilities(), node as never), queryError('query.value'), `validate ${String(value)}`);
  }
});

test('a relative window on a non-date field is refused by the contract and the capability check, and admitted structurally', () => {
  const node: BrowseNode = {field: 'year', operator: OPERATOR, value: 30};
  // 1. The generated contract cannot express it at all: the relative shape enumerates
  //    date fields and the general shape's operator list leaves the relative one out.
  assert.throws(() => decodeBrowseLibraryResponse(page(node as never)), /Invalid contract value/);
  // 2. The structural parser knows no fields, so the node is not a parse failure: the app
  //    can hold one the published contract cannot express. This is the observed difference
  //    from the schema, not a claim about who may send it.
  assert.deepEqual(parseBrowseNode(node), node);
  // 3. A page's applied query is echoed through that same parser, unchanged.
  assert.deepEqual(parseBrowseResult(page(node)).applied.query, node);
  // 4. Capability validation refuses it at the operator, naming the same path the
  //    server would: the published year filter does not offer the relative operator.
  assert.throws(() => validateBrowseQuery(capabilities(), node), queryError('query.operator'));
  assert.throws(() => validateBrowseQuery(capabilities(), node), /within-last-days is not available for year\./);
  // The executed send path refuses before a request exists.
  assert.throws(() => new BrowseQueryBuilder(capabilities(), 'movies').where('year', OPERATOR, 30), queryError('query.all[0].operator'));
});

test('nested groups keep relative windows exact, refuse a non-date field at its own path, and stay within the published depth', () => {
  const node: BrowseNode = {all: [
    {field: 'dateAdded', operator: OPERATOR, value: 30},
    {any: [{field: 'releaseDate', operator: OPERATOR, value: 7}, {not: {field: 'lastPlayedAt', operator: OPERATOR, value: 90}}]},
  ]};
  assert.deepEqual(parseBrowseNode(node), node);
  assert.deepEqual(parseBrowseResult(page(node)).applied.query, node);
  assert.doesNotThrow(() => validateBrowseQuery(capabilities(), node));
  assert.doesNotThrow(() => decodeBrowseLibraryResponse(page(node)));
  // A non-date window is refused wherever it sits, with that node's path.
  assert.throws(() => validateBrowseQuery(capabilities(), {all: [{field: 'dateAdded', operator: OPERATOR, value: 30}, {field: 'year', operator: OPERATOR, value: 30}]}), queryError('query.all[1].operator'));
  // Six levels parse structurally but exceed the published depth of five: the
  // structural bound and the executable bound are separate numbers.
  let deep: BrowseNode = {field: 'dateAdded', operator: OPERATOR, value: 30};
  for (let level = 0; level < 6; level++) deep = {all: [deep]};
  assert.doesNotThrow(() => parseBrowseNode(deep));
  assert.throws(() => validateBrowseQuery(capabilities(), deep), /Nest at most 5 levels\./);
});

test('the applied query is echoed exactly, and no relative operator beyond the published one exists', () => {
  const absolute: BrowseNode = {field: 'dateAdded', operator: 'at-least', value: '2026-09-23'};
  const relative30: BrowseNode = {field: 'dateAdded', operator: OPERATOR, value: 30};
  const result = parseBrowseResult(page({all: [relative30, absolute]}));
  assert.deepEqual(result.applied.query, {all: [relative30, absolute]});
  assert.equal(Object.isFrozen(result.applied.query), true);
  assert.equal(parseBrowseResult(page(null)).applied.query, null);
  for (const operator of ['within-last-weeks', 'within-last-months', 'last-days', 'in-the-last-days']) {
    assert.throws(() => parseBrowseNode({field: 'dateAdded', operator, value: 4}), {code: 'invalid_browse'});
  }
});

test('absolute date predicates are unchanged by the relative window work', () => {
  const nodes = [
    {field: 'dateAdded', operator: 'at-least', value: '2026-09-23'},
    {field: 'releaseDate', operator: 'between', value: ['2020-01-01', '2020-12-31']},
    {field: 'lastPlayedAt', operator: 'is-missing', value: null},
  ];
  for (const node of nodes) {
    assert.deepEqual(parseBrowseNode(node), node);
    assert.doesNotThrow(() => validateBrowseQuery(capabilities(), node as BrowseNode));
    assert.deepEqual(parseBrowseResult(page(node as BrowseNode)).applied.query, node);
    // An old absolute predicate and a new relative one live in one group unchanged.
    assert.doesNotThrow(() => decodeBrowseLibraryResponse(page({all: [node as BrowseNode, {field: 'dateAdded', operator: OPERATOR, value: 30}]})));
  }
});

test('every operator the published predicate shape can express is structurally readable', () => {
  const list = new Set(['in', 'not-in', 'between', 'contains-any', 'contains-all']);
  const presence = new Set(['is-present', 'is-missing']);
  for (const operator of general.properties.operator.enum) {
    const value = presence.has(operator) ? null : list.has(operator) ? (operator === 'between' ? ['a', 'b'] : ['a']) : 'a';
    assert.doesNotThrow(() => parseBrowseNode({field: 'title', operator, value}), `${operator} is not read`);
  }
});

test('the published vocabulary keeps the relative operator and drops only what the grammar cannot read', () => {
  const caps = parseBrowseCapabilities(vocabulary);
  // An operator this app does not know leaves the field's own list; the relative one stays.
  assert.deepEqual(caps.fields.find(field => field.id === 'dateAdded')?.operators, [OPERATOR, 'at-least']);
  // A quick filter carrying a relative window is offered with its query intact; one the
  // grammar cannot read is left out alone and the rest of the vocabulary stays.
  assert.deepEqual(caps.quickFilters.map(filter => filter.id), ['recent']);
  assert.deepEqual(caps.quickFilters[0].query, {field: 'dateAdded', operator: OPERATOR, value: 30});
  // Choosing that filter sends exactly what was published.
  assert.deepEqual(new BrowseQueryBuilder(caps, 'movies').quickFilter('recent').build(), {pivot: 'movies', query: {field: 'dateAdded', operator: OPERATOR, value: 30}});
});

test('a See all destination keeps the relative window, and one that cannot be read is left out alone', () => {
  const sort = [{field: 'added', direction: 'desc'}];
  const relative = parseContentSeeAll({pivot: 'movies', sort, query: {field: 'releaseDate', operator: OPERATOR, value: 90}});
  assert.deepEqual(relative?.query, {field: 'releaseDate', operator: OPERATOR, value: 90});
  assert.deepEqual(relative?.sort, sort);
  // An operator this app cannot read takes the destination only, never the section or row.
  assert.equal(parseContentSeeAll({pivot: 'movies', sort, query: {field: 'releaseDate', operator: 'within-last-weeks', value: 4}}), undefined);
  // A Home row's See all is the same canonical destination, read by the same rules.
  const home = validateHomeSeeAll({type: 'library', libraryId: 'library', pivot: 'movies', sort, query: {field: 'dateAdded', operator: OPERATOR, value: 7}}) as {query?: unknown};
  assert.deepEqual(home.query, {field: 'dateAdded', operator: OPERATOR, value: 7});
  assert.throws(() => validateHomeSeeAll({type: 'library', libraryId: 'library', pivot: 'movies', sort, query: {field: 'dateAdded', operator: 'within-last-weeks', value: 4}}));
});

test('a counts or facets request carries the relative window unchanged, never a date computed from the clock', t => {
  const node: BrowseNode = {field: 'dateAdded', operator: OPERATOR, value: 30};
  assert.deepEqual(browseCountsRequest('movies', [{id: 'recent', query: node}]).queries[0]?.query, node);
  const facets = browseContextFacetsPath('library', 'genre', 'movies', node);
  assert.deepEqual(JSON.parse(new URLSearchParams(facets.slice(facets.indexOf('?') + 1)).get('query') as string), node);

  // The executed body is the same at any instant: a relative window is never frozen
  // into the calendar date it happens to mean today.
  t.mock.timers.enable({apis: ['Date'], now: new Date('2026-10-24T06:00:00Z')});
  const first = new BrowseQueryBuilder(capabilities(), 'movies').where('dateAdded', OPERATOR, 30).sort('title').build();
  t.mock.timers.tick(90 * 86400000);
  const later = new BrowseQueryBuilder(capabilities(), 'movies').where('dateAdded', OPERATOR, 30).sort('title').build();
  assert.deepEqual(later, first);
  assert.equal(JSON.stringify(later).includes(OPERATOR), true);
  assert.equal(/20\d\d-\d\d-\d\d/.test(JSON.stringify(later)), false);
});

test('a saved view is transport for the query it stores, and rendering withholds what it cannot build', async () => {
  const forbidden = {field: 'year', operator: OPERATOR, value: 30};
  const definition = {libraryId: 'library', pivot: 'movies', query: forbidden, sort: [{field: 'added', direction: 'desc'}], presentation: 'grid'};
  const writes: any[] = [];
  const directory = () => ({serverId: 'server', viewerFence: 'fence', revision: 1, nextCursor: '', resources: [{serverId: 'server', viewerFence: 'fence', id: 'year-window', kind: 'view', name: 'Year', summary: '', visibility: 'private', revision: 1, role: 'owner', entryCount: 0, actions: ['pin', 'update', 'delete'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 0, shares: [], definition}]});
  const service = new PersonalSavedService(
    {request: async <T>(_path: string, method?: string, body?: any): Promise<T> => {
      if (method === 'GET') return directory() as unknown as T;
      writes.push(body);
      return {serverId: 'server', viewerFence: 'fence', receipt: {operationId: body.operationId, resourceId: 'year-window', revision: 2, deleted: false}} as unknown as T;
    }},
    {serverId: 'server', viewerId: 'viewer'},
    async () => '00000000-0000-4000-8000-000000000001',
  );
  await service.select({view: 'views'});
  // This fixture supplies an unexpressible definition. The client keeps and serializes it;
  // the mocked mutation success below makes no claim that the real server would accept it.
  assert.deepEqual(service.getSnapshot().resources[0]?.definition, definition);
  assert.equal(await service.mutate({action: 'create', kind: 'view', name: 'Year', definition: definition as never}), true);
  assert.deepEqual(writes[0].definition, definition);
  service.dispose();
  // Rendering is the guarded side: the summary is withheld rather than mislabelled as recency.
  assert.equal(savedViewSummary(definition as never, parseBrowseCapabilities(vocabulary), defaultI18n), undefined);
});

test('a nested applied query is echoed without a capability cross-check, and the capability check stops at the first offending node with its path', () => {
  const nested = {all: [
    {field: 'dateAdded', operator: OPERATOR, value: 30},
    {any: [{field: 'year', operator: OPERATOR, value: 7}]},
  ]};
  // Reading a page is structural: the applied query is kept exactly, including a window on
  // a field the capabilities do not offer. The consumer that holds both the page and the
  // vocabulary does not compare them here.
  assert.deepEqual(parseBrowseResult(page(nested as BrowseNode)).applied.query, nested);
  assert.doesNotThrow(() => decodeBrowseLibraryResponse(page(nested as BrowseNode)));
  // The capability check names the nested node, at its own path, and stops there.
  assert.throws(() => validateBrowseQuery(capabilities(), nested as BrowseNode), queryError('query.all[1].any[0].operator'));
  // A field the vocabulary does not publish at all is refused at its own path in its own tree.
  const unpublished = {all: [{any: [{not: {field: 'mood', operator: 'equals', value: 'calm'}}]}]};
  assert.throws(() => validateBrowseQuery(capabilities(), unpublished as BrowseNode), queryError('query.all[0].any[0].not.field'));
  // The consistent reading of the same shape passes, so the paths above name the node only.
  const consistent = {all: [{field: 'dateAdded', operator: OPERATOR, value: 30}, {any: [{field: 'releaseDate', operator: OPERATOR, value: 90}]}]};
  assert.doesNotThrow(() => validateBrowseQuery(capabilities(), consistent as BrowseNode));
});
