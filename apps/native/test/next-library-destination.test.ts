import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLibraryDestination, nativeLibraryEntryKey, nativeLibraryCanSave} from '../src-next/screens/library/model.ts';
import type {BrowseCapabilities, BrowseNode, BrowseRequestBody} from '../../../packages/client-core/src/browse.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import {NativeLibraryFilterDraft} from '../src-next/screens/library/filters.ts';
import {libraryFilterChange, libraryFilterField} from '../src-next/screens/library/filter-controls.ts';
import {libraryAppliedFilters} from '../src-next/screens/library/applied-filters.ts';
import {rangePresets, activeRangePreset} from '../../../packages/client-core/src/presentation/browse-toolbar.ts';

const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
const library = {id: 'actual-movies', name: 'Movies', kind: 'movie'};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const pivot = {id: 'movies', labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], browsable: true, sorts: ['title'], filters: ['favorite'] } as const;
const capabilities: BrowseCapabilities = {
  library: {...library, defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot,
  fields: [{id: 'favorite', labelKey: 'filter.favorite', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']}],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}],
  quickFilters: [{id: 'favorites', labelKey: 'saved.tab.favorites', query: {variant: 'predicate', field: 'favorite', operator: 'equals', value: true}}],
  surfaces: [{id: 'library', endpoint: '/v1/libraries/{id}/browse', paging: 'keyset', sorts: ['title'], filters: ['favorite']}],
  queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000},
};

const filterCapabilities: BrowseCapabilities = {...capabilities,
  pivots: [{...pivot, filters: ['favorite', 'genre', 'year', 'decade']}], resolvedPivot: {...pivot, filters: ['favorite', 'genre', 'year', 'decade']},
  fields: [...capabilities.fields,
    {id: 'genre', labelKey: 'filter.field.genre', type: 'identity-set', operators: ['contains-any', 'contains-all'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie'], facetSource: {field: 'genre', endpoint: '/v1/libraries/actual-movies/facets'}},
    {id: 'year', labelKey: 'filter.field.year', type: 'number', operators: ['in'], controlHint: 'number-range', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']},
    {id: 'decade', labelKey: 'filter.field.year', type: 'number', operators: ['in'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']}],
};
function page(body: BrowseRequestBody, total = 50000) {
  const start = body.range?.start ?? 0, count = Math.min(body.limit ?? 40, total - start);
  return {pivot: body.pivot, applied: {query: body.query ?? null, sort: body.sort, seek: null},
    entries: Array.from({length: count}, (_, offset) => ({id: `actual-${start + offset}`, libraryId: library.id, kind: 'movie', title: `Title ${start + offset}`})),
    pageInfo: {start, total, revision: 'catalogue-1', totalIsLowerBound: false, hasMore: start + count < total, nextCursor: ''}, positionIndex: [{key: 'M', index: 48000}]};
}

test('Library refuses another library vocabulary and never falls back by name or list index', async () => {
  let requests = 0;
  const api: LibraryContentApi = {request: async <T>() => {requests++; return {...capabilities, library: {...capabilities.library, id: 'other-library'}} as T;}};
  const destination = new NativeLibraryDestination({api, scope, library, current: () => true});
  try {await destination.load(); assert.equal(destination.getSnapshot().phase, 'error'); assert.equal(destination.getSnapshot().collection, undefined); assert.equal(requests, 1);}
  finally {destination.dispose();}
});

test('Library filter draft aborts superseded searches and hides late retired viewer data', async () => {
  const pending: {path: string; signal?: AbortSignal; resolve: (value: unknown) => void}[] = [];
  let current = true;
  const api: LibraryContentApi = {request: async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) =>
    await new Promise<T>(resolve => pending.push({path, signal, resolve: value => resolve(value as T)}))};
  const draft = new NativeLibraryFilterDraft({api, capabilities: filterCapabilities, pivot: 'movies', sort: {field: 'title', direction: 'asc'}, predicates: [], fixed: [], current: () => current,
    apply() {assert.fail('a retired viewer must not apply');}, onDispose() {}});
  draft.refresh(); draft.setOpenFields(['genre']); draft.search('genre', 'Dr');
  assert.equal(pending[1].signal?.aborted, true);
  pending[1].resolve({field: 'genre', total: 1, values: [{value: 'old', label: 'Old', count: 1}]}); await tick();
  assert.equal(draft.getSnapshot().facets.genre?.value, undefined);
  current = false; assert.equal(draft.getSnapshot().open, false); assert.ok(pending.every(value => value.signal?.aborted));
  pending[0].resolve({counts: [{id: 'draft', total: 9, totalIsLowerBound: false, revision: 'old'}]});
  pending[2].resolve({field: 'genre', total: 1, values: [{value: 'private', label: 'Private', count: 1}]}); await tick();
  assert.deepEqual(draft.getSnapshot().facets, {}); assert.equal(draft.apply(), false);
});

test('Library filter controls preserve negative-only values, typed decades, fixed predicates and cross-field alternatives', () => {
  const genre = filterCapabilities.fields.find(field => field.id === 'genre')!, year = filterCapabilities.fields.find(field => field.id === 'year')!;
  const negative: BrowseNode = {variant: 'group', not: {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Drama']}};
  const state = libraryFilterField(genre, [negative], undefined, new Date('2026-10-23T00:00:00Z'));
  assert.equal(state.kind, 'values'); if (state.kind !== 'values') assert.fail();
  assert.deepEqual(state.excluded, ['Drama']); assert.deepEqual(state.included, []);
  assert.deepEqual(libraryFilterChange(filterCapabilities, genre, [negative], state, [], new Date()), [negative]);
  const fixed: BrowseNode = {variant: 'predicate', field: 'favorite', operator: 'equals', value: true};
  const alternative: BrowseNode = {variant: 'group', any: [{variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Comedy']}, fixed]};
  const changed = libraryFilterChange(filterCapabilities, year, [alternative, fixed], {kind: 'values', included: ['1980'], excluded: [], mode: 'any'}, [fixed], new Date());
  assert.deepEqual(changed, [alternative, fixed, {variant: 'predicate', field: 'decade', operator: 'in', value: [1980]}]);
  assert.equal(libraryFilterField(year, changed, undefined, new Date()).summary, '1980s');
});

test('Applied Library chips retain semantic predicate IDs and never flatten a cross-field OR', () => {
  const decade: BrowseNode = {variant: 'predicate', field: 'decade', operator: 'in', value: [1980]};
  const negative: BrowseNode = {variant: 'group', not: {variant: 'predicate', field: 'genre', operator: 'contains-any', value: ['Horror']}};
  const alternative: BrowseNode = {variant: 'group', any: [decade, negative]};
  const before = JSON.stringify(alternative);
  const labels = {field: (field: {id: string}) => field.id === 'year' ? 'Year' : 'Genre', sort: () => '', quick: () => ''};
  const chips = libraryAppliedFilters([decade, negative, decade, alternative], filterCapabilities, labels);
  assert.equal(chips.length, 2, 'duplicate conditions have one semantic focus/control identity');
  assert.equal(chips[0].id, JSON.stringify(decade)); assert.equal(chips[0].label, 'Year: 1980s');
  assert.equal(chips[1].id, JSON.stringify(negative));
  assert.deepEqual(libraryAppliedFilters([negative, decade], filterCapabilities, labels).map(chip => chip.id), [chips[1].id, chips[0].id]);
  assert.equal(JSON.stringify(alternative), before, 'a complex condition remains whole for the actual query/editor');
});

// P2-61 HOME-RESILIENCE: a FIRST page refused as stale (412 while a library imports) is read once
// more; a second refusal is that library's error, never a loop. An authority answer is never retried.
async function firstPageRefusals(refusals: number, refusal: {status: number; code?: string} = {status: 412, code: 'revision_mismatch'}) {
  let browse = 0, left = refusals;
  const api: LibraryContentApi = {request: async <T>(path: string, _method?: string, body?: unknown) => {
    if (path.includes('capabilities')) return capabilities as T;
    browse++;
    if (left > 0) {left--; throw Object.assign(new Error('Reading projection changed'), refusal);}
    return page(body as BrowseRequestBody, 3) as T;
  }};
  const destination = new NativeLibraryDestination({api, scope, library, current: () => true});
  await destination.load();
  for (let i = 0; i < 20; i++) await tick();
  return {destination, browse: () => browse};
}

test('an authority answer on the first page is never re-read', async () => {
  const {destination, browse} = await firstPageRefusals(5, {status: 401, code: 'unauthorized'});
  try {
    assert.equal(browse(), 1, 'a 401 still stops');
    assert.equal(destination.getSnapshot().phase, 'error');
  } finally {destination.dispose();}
});
