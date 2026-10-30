import test from 'node:test';
import assert from 'node:assert/strict';
import {parseBrowseCapabilities, type BrowseCapabilities, type BrowseFieldCapability, type BrowseNode, type BrowseSortSelection} from '../src/browse.ts';
import {formatMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {browseToolbarModel, appliedFilterParts, type KitTranslate} from '../src/presentation/kit-model/index.ts';

const t: KitTranslate = (id, ...args) => formatMessage(shared[id], args[0]);
const env = {t, locale: 'en-US'};
const titleSort: BrowseSortSelection = {field: 'title', direction: 'asc'};

// Reduced real vocabulary from design/ui-v1/raw/caps_movies.json and caps_music.json,
// reconciled with server/internal/catalog/browse_contract.go (fields and quick presets).
// These are capability inputs only: no fabricated result data, count, endpoint or ownerOnly field.
const enumOps = ['equals', 'not-equals', 'in', 'not-in'] as const;
const setOps = ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'] as const;
const numberOps = ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'] as const;
const fields: readonly BrowseFieldCapability[] = [
  {id: 'playState', labelKey: 'browse.field.playState', type: 'enum', operators: enumOps, controlHint: 'select', complexity: 'quick', cost: 'indexed-join', applicableKinds: ['movie', 'song'], allowedValues: ['unplayed', 'in-progress', 'played']},
  ...['favorite', 'watchlisted'].map(id => ({id, labelKey: `browse.field.${id}`, type: 'boolean', operators: ['equals', 'not-equals'], controlHint: 'toggle', complexity: 'quick', cost: 'indexed-join', applicableKinds: ['movie', 'song']} as const)),
  {id: 'availability', labelKey: 'browse.field.availability', type: 'enum', operators: enumOps, controlHint: 'select', complexity: 'standard', cost: 'indexed', applicableKinds: ['movie', 'song'], allowedValues: ['available', 'unavailable']},
  {id: 'genre', labelKey: 'browse.field.genre', type: 'identity-set', operators: setOps, controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed-join', applicableKinds: ['movie', 'song']},
  {id: 'year', labelKey: 'browse.field.year', type: 'number', operators: numberOps, controlHint: 'number-range', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'song']},
  {id: 'decade', labelKey: 'browse.field.decade', type: 'number', operators: ['equals', 'not-equals', 'in', 'not-in', 'between'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'song']},
  {id: 'actor', labelKey: 'browse.field.actor', type: 'identity-set', operators: setOps, controlHint: 'text', complexity: 'advanced', cost: 'indexed-join', applicableKinds: ['movie']},
  {id: 'audioLanguage', labelKey: 'browse.field.audioLanguage', type: 'identity-set', operators: setOps, controlHint: 'facet-multi-select', complexity: 'standard', cost: 'indexed-join', applicableKinds: ['movie', 'song']},
  {id: 'subtitleLanguage', labelKey: 'browse.field.subtitleLanguage', type: 'identity-set', operators: setOps, controlHint: 'facet-multi-select', complexity: 'standard', cost: 'indexed-join', applicableKinds: ['movie']},
  {id: 'versions', labelKey: 'browse.field.versions', type: 'number', operators: numberOps, controlHint: 'number-range', complexity: 'standard', cost: 'indexed-join', applicableKinds: ['movie', 'song']},
  {id: 'match', labelKey: 'browse.field.match', type: 'enum', operators: enumOps, controlHint: 'select', complexity: 'standard', cost: 'indexed-join', applicableKinds: ['movie', 'album'], allowedValues: ['matched', 'unmatched']},
];
function caps(kind = 'movie'): BrowseCapabilities {
  const sortIds = kind === 'music' ? ['title', 'added', 'year', 'duration', 'communityRating', 'personalRating', 'lastPlayed', 'album', 'artist', 'trackNumber', 'fileSize', 'random', 'groupOrder', 'playCount'] : ['title', 'added', 'year', 'duration', 'communityRating', 'personalRating', 'forYou', 'lastPlayed', 'releaseDate', 'criticRating', 'fileSize', 'resolution', 'random', 'groupOrder', 'playCount'];
  const sorts = sortIds.map(id => ({id, labelKey: `sort.${id}`, directions: id === 'forYou' ? ['desc'] : ['asc', 'desc'], defaultDirection: ['title', 'album', 'artist', 'trackNumber', 'random', 'groupOrder'].includes(id) ? 'asc' : 'desc', expensive: id === 'forYou', seeded: id === 'random', requiresGroup: id === 'groupOrder', applicableKinds: [kind === 'music' ? 'song' : 'movie']}));
  const pivot = {id: kind === 'music' ? 'songs' : 'movies', labelKey: kind === 'music' ? 'library.songs' : 'library.movies', entityKinds: [kind === 'music' ? 'song' : 'movie'], defaultSort: [titleSort], supportedViews: kind === 'music' ? ['list'] : ['grid', 'list'], browsable: true, sorts: sorts.map(s => s.id), filters: fields.map(f => f.id)};
  return parseBrowseCapabilities({library: {id: 'library', name: kind === 'music' ? 'Music' : 'Movies', kind, defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot, fields, sorts,
    quickFilters: [
      {id: 'in-progress', labelKey: 'filter.inProgress', query: {variant:'predicate',field: 'playState', operator: 'equals', value: 'in-progress'}},
      {id: 'unwatched', labelKey: 'filter.unwatched', query: {variant:'predicate',field: 'playState', operator: 'equals', value: 'unplayed'}},
      {id: 'watched', labelKey: 'filter.watched', query: {variant:'predicate',field: 'playState', operator: 'equals', value: 'played'}},
      {id: 'favorites', labelKey: 'filter.favorites', query: {variant:'predicate',field: 'favorite', operator: 'equals', value: true}},
      {id: 'watchlist', labelKey: 'filter.watchlist', query: {variant:'predicate',field: 'watchlisted', operator: 'equals', value: true}},
      {id: 'available', labelKey: 'filter.available', query: {variant:'predicate',field: 'availability', operator: 'equals', value: 'available'}},
      {id: 'missing', labelKey: 'filter.missing', query: {variant:'predicate',field: 'availability', operator: 'equals', value: 'unavailable'}},
    ], queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 1800000}});
}
const ids = (rows: readonly Readonly<{id: string}>[]) => rows.map(x => x.id);
const filterIds = (model: ReturnType<typeof browseToolbarModel>) => model.groups.flatMap(g => ids(g.fields));

// desktop-browse#1/#3 (:342–345, :396–399); D/notes/OPEN-QUESTIONS.md D27.
test('movie quick toggles follow the drawn order and preserve the server queries and My List', () => {
  const capabilities = caps();
  const model = browseToolbarModel(capabilities, 'movie', [], titleSort, env);
  assert.deepEqual(ids(model.quick), ['unwatched', 'in-progress', 'favorites', 'watchlist']);
  assert.deepEqual(model.quick.map(q => q.label?.text), ['Unwatched', 'In progress', 'Favorites', 'My List']);
  assert.deepEqual(ids(model.filterQuick), ['watched', 'available', 'missing']);
  for (const quick of [...model.quick, ...model.filterQuick]) {
    const source = capabilities.quickFilters.find(q => q.id === quick.id)!;
    assert.equal(quick.query, source.query);
    assert.equal(quick.labelKey, source.labelKey);
    assert.equal(quick.active, false);
  }
  assert.deepEqual(browseToolbarModel({...capabilities, quickFilters: [...capabilities.quickFilters].reverse()}, 'movie', [], titleSort, env).quick, model.quick);
});

// Answered Q1c + desktop-listen#4 :599. notes/listen.md keeps My List in Filters.
test('Songs offers distinct Unplayed and Played quick toggles and keeps other real presets in Filters', () => {
  const capabilities = caps('music');
  const played = capabilities.quickFilters.find(q => q.id === 'watched')!.query;
  const watchlist = capabilities.quickFilters.find(q => q.id === 'watchlist')!.query;
  const model = browseToolbarModel(capabilities, 'music', [played, watchlist], titleSort, env);
  assert.deepEqual(ids(model.quick), ['unwatched', 'watched', 'favorites']);
  assert.deepEqual(model.quick.map(q => [q.label?.text, q.active]), [['Unplayed', false], ['Played', true], ['Favorites', false]]);
  assert.deepEqual(model.quick[0]!.query, {variant:'predicate',field: 'playState', operator: 'equals', value: 'unplayed'});
  assert.deepEqual(model.quick[1]!.query, {variant:'predicate',field: 'playState', operator: 'equals', value: 'played'});
  assert.deepEqual(ids(model.filterQuick), ['in-progress', 'watchlist', 'available', 'missing']);
  assert.deepEqual(model.filterQuick.find(q => q.id === 'watchlist')?.label, {id: 'label', text: 'My List', role: 'context'});
  assert.deepEqual(model.applied, [watchlist]);
});

// desktop-browse#3 :377 caption, :402 exact field order, notes/browse.md Sort menu.
test('sorts use the designed order, retain expensive flags, and expose File size only to a real owner', () => {
  const capabilities = caps();
  const model = browseToolbarModel(capabilities, 'movie', [], {field: 'year', direction: 'desc'}, env);
  const expected = ['forYou', 'title', 'added', 'year', 'releaseDate', 'duration', 'communityRating', 'criticRating', 'personalRating', 'lastPlayed', 'playCount', 'resolution', 'random'];
  assert.deepEqual(ids(model.sorts), expected);
  assert.equal(model.sort.flippable, true);
  assert.equal(model.sorts[0]!.expensive, true);
  for (const sort of model.sorts) assert.equal(sort, capabilities.sorts.find(s => s.id === sort.id));
  const owner = browseToolbarModel(capabilities, 'movie', [], titleSort, {...env, owner: true});
  assert.deepEqual(ids(owner.sorts), [...expected.slice(0, -1), 'fileSize', 'random']);
  const memberFileSort = browseToolbarModel(capabilities, 'movie', [], {field: 'fileSize', direction: 'desc'}, env);
  assert.deepEqual(memberFileSort.sort, {field: 'fileSize', direction: 'desc', flippable: false, reshufflable: false});
  assert.equal(ids(memberFileSort.sorts).includes('fileSize'), false);
});

test('registry reordering cannot reorder designed sorts; real unknown sorts retain their relative order', () => {
  const capabilities = caps();
  const unknown = ['recentActivity', 'count'].map(id => ({...capabilities.sorts[0]!, id, labelKey: `sort.${id}`}));
  const source = {...capabilities, resolvedPivot: null, sorts: [unknown[0]!, ...capabilities.sorts].reverse().concat(unknown[1]!)};
  const model = browseToolbarModel(source, 'movie', [], titleSort, env);
  assert.deepEqual(ids(model.sorts), ['forYou', 'title', 'added', 'year', 'releaseDate', 'duration', 'communityRating', 'criticRating', 'personalRating', 'lastPlayed', 'playCount', 'resolution', 'random', 'recentActivity', 'count']);
  assert.equal(model.sorts.at(-2), unknown[0]);
  assert.equal(model.sorts.at(-1), unknown[1]);
});

test('single-direction and random sorts hide Order without inventing a sort or random seed', () => {
  const capabilities = caps();
  assert.deepEqual(browseToolbarModel(capabilities, 'movie', [], {field: 'forYou', direction: 'desc'}, env).sort, {field: 'forYou', direction: 'desc', flippable: false, reshufflable: false});
  assert.deepEqual(browseToolbarModel(capabilities, 'movie', [], {field: 'random', direction: 'desc', seed: 23}, env).sort, {field: 'random', direction: 'desc', seed: 23, flippable: false, reshufflable: true});
  assert.deepEqual(browseToolbarModel(capabilities, 'movie', [], {field: 'unknown', direction: 'asc'}, env).sort, {field: 'unknown', direction: 'asc', flippable: false, reshufflable: false});
  assert.deepEqual(browseToolbarModel(capabilities, 'movie', [], {field: 'forYou', direction: 'asc'}, env).sort, {field: 'forYou', direction: 'asc', flippable: false, reshufflable: false});
  assert.equal(browseToolbarModel({...capabilities, sorts: []}, 'movie', [], titleSort, env).sorts.length, 0);
});

test('group order is only offered on an explicit group surface when the server offers it', () => {
  const capabilities = caps('music');
  const group = browseToolbarModel(capabilities, 'music', [], {field: 'groupOrder', direction: 'asc'}, {...env, scope: 'group'});
  assert.equal(group.sorts[0]!.id, 'groupOrder');
  assert.equal(group.sort.flippable, true);
  assert.equal(ids(browseToolbarModel(capabilities, 'music', [], titleSort, env).sorts).includes('groupOrder'), false);
  assert.equal(ids(browseToolbarModel({...capabilities, sorts: capabilities.sorts.filter(s => s.id !== 'groupOrder')}, 'music', [], titleSort, {...env, scope: 'group'}).sorts).includes('groupOrder'), false);
});

// desktop-browse#4 :438–459 + notes/browse.md Filter builder: no old promoted menus.
test('filters live in the complete ordered panel groups; unknown readable fields remain available', () => {
  const capabilities = caps();
  const futureField: BrowseFieldCapability = {...fields[5]!, id: 'providerTag', labelKey: 'browse.field.providerTag'};
  const model = browseToolbarModel({...capabilities, resolvedPivot: null, fields: [...capabilities.fields].reverse().concat(futureField)}, 'movie', [], titleSort, env);
  assert.equal('promoted' in model, false);
  assert.deepEqual(model.groups.map(g => [g.id, ids(g.fields)]), [
    ['title', ['match', 'genre', 'year', 'actor', 'providerTag']],
    ['activity', ['playState', 'favorite', 'watchlisted']],
    ['file', ['audioLanguage', 'availability', 'versions', 'subtitleLanguage']],
  ]);
  assert.equal(model.groups[0]!.fields.at(-1), futureField);
  assert.equal(filterIds(model).includes('match'), true);
  assert.equal(filterIds(browseToolbarModel(capabilities, 'movie', [], titleSort, {...env, owner: true})).includes('match'), true);
  const localOnly = browseToolbarModel({...capabilities, fields: capabilities.fields.filter(f => f.id !== 'match')}, 'movie', [], titleSort, env);
  assert.equal(filterIds(localOnly).includes('match'), false);
  const decadeOnly = browseToolbarModel({...capabilities, resolvedPivot: null, fields: capabilities.fields.filter(f => f.id === 'decade')}, 'movie', [], titleSort, env);
  assert.deepEqual(filterIds(decadeOnly), ['decade']);
});

test('TV omits typed-only people fields, retains real facet controls and respects resolved pivot vocabulary', () => {
  const capabilities = caps();
  assert.equal(filterIds(browseToolbarModel(capabilities, 'movie', [], titleSort, env)).includes('actor'), true);
  assert.equal(filterIds(browseToolbarModel(capabilities, 'movie', [], titleSort, {...env, textEntry: false})).includes('actor'), false);
  const television = browseToolbarModel(capabilities, 'movie', [], titleSort, {...env, textEntry: false});
  assert.ok(filterIds(television).includes('genre'));
  assert.ok(filterIds(television).includes('audioLanguage'));
  assert.ok(filterIds(television).includes('year'));
  const pivot = {...capabilities.resolvedPivot!, sorts: ['title'], filters: ['genre']};
  const model = browseToolbarModel({...capabilities, resolvedPivot: pivot}, 'movie', [], titleSort, {...env, owner: true});
  assert.deepEqual(ids(model.sorts), ['title']);
  assert.deepEqual(filterIds(model), ['genre']);
  assert.deepEqual(model.quick, []);
  assert.deepEqual(model.filterQuick, []);
  const empty = browseToolbarModel({...capabilities, resolvedPivot: {...pivot, sorts: [], filters: []}}, 'movie', [], titleSort, env);
  assert.deepEqual(empty.sorts, []);
  assert.deepEqual(empty.groups, []);
});

test('missing quick capabilities, operators and allowed values never produce invented toggles', () => {
  const capabilities = caps('music');
  const limited = {...capabilities, fields: capabilities.fields.map(f => f.id === 'playState' ? {...f, allowedValues: ['unplayed']} : f)};
  assert.deepEqual(ids(browseToolbarModel(limited, 'music', [], titleSort, env).quick), ['unwatched', 'favorites']);
  assert.deepEqual(browseToolbarModel({...capabilities, quickFilters: []}, 'music', [], titleSort, env).quick, []);
  const noEquals = {...capabilities, fields: capabilities.fields.map(f => f.id === 'playState' ? {...f, operators: ['in'] as const} : f)};
  assert.deepEqual(ids(browseToolbarModel(noEquals, 'music', [], titleSort, env).quick), ['favorites']);
  const noWatchlist = {...capabilities, quickFilters: capabilities.quickFilters.filter(q => q.id !== 'watchlist')};
  assert.equal(ids(browseToolbarModel(noWatchlist, 'movie', [], titleSort, env).quick).includes('watchlist'), false);
});

test('exact positive quick predicates alone leave the applied line; Any/All/Not and raw values survive', () => {
  const capabilities = caps();
  const unplayed = capabilities.quickFilters.find(q => q.id === 'unwatched')!.query;
  const all: BrowseNode = {variant:'predicate',field: 'genre', operator: 'contains-all', value: ['R&B · Soul', 'Drama']};
  const any: BrowseNode = {variant:'predicate',field: 'genre', operator: 'contains-any', value: ['R&B · Soul', 'Drama']};
  const none: BrowseNode = {variant:'group',not: unplayed};
  const either: BrowseNode = {variant:'group',any: [unplayed, all]};
  const both: BrowseNode = {variant:'group',all: [unplayed, all]};
  const inverse: BrowseNode = {variant:'predicate',field: 'watchlisted', operator: 'equals', value: false};
  const predicates = [unplayed, all, any, none, either, both, inverse];
  const model = browseToolbarModel(capabilities, 'movie', predicates, titleSort, env);
  assert.equal(model.quick[0]!.active, true);
  assert.deepEqual(model.applied, [all, any, inverse, none, either, both]);
  assert.equal(model.applied[0], all);
  assert.equal(model.applied[3], none);
  const allChip = appliedFilterParts(all, String, env);
  const anyChip = appliedFilterParts(any, String, env);
  assert.equal(allChip.rawValue, all.value);
  assert.equal(allChip.operatorPart.text, 'contains all of');
  assert.equal(anyChip.operatorPart.text, 'contains any of');
  assert.equal(browseToolbarModel(capabilities, 'movie', [none, either, both], titleSort, env).quick[0]!.active, false);
});

test('unknown and compound quick presets retain query identity, exact matching and published capabilities', () => {
  const capabilities = caps();
  const quick = {id: 'provider-preset', labelKey: 'provider.preset', query: {variant:'group',all: [{variant:'predicate',field: 'genre', operator: 'contains-all', value: ['Drama', 'Mystery']}, {variant:'group',not: {variant:'predicate',field: 'watchlisted', operator: 'equals', value: false}}]}} as const;
  const publicMatch = {id: 'unmatched', labelKey: 'filter.unmatched', query: {variant:'predicate',field: 'match', operator: 'equals', value: 'unmatched'}} as const;
  const prototypeId = {...quick, id: '__proto__'};
  const source = {...capabilities, quickFilters: [...capabilities.quickFilters, quick, publicMatch, prototypeId]};
  const model = browseToolbarModel(source, 'movie', [quick.query], titleSort, env);
  const output = model.filterQuick.find(q => q.id === quick.id)!;
  assert.equal(output.query, quick.query);
  assert.equal(output.active, true);
  assert.equal(output.label, undefined);
  assert.equal(output.labelKey, 'provider.preset');
  assert.equal(model.filterQuick.find(q => q.id === '__proto__')?.label, undefined);
  assert.equal(ids(model.filterQuick).includes('unmatched'), true);
  assert.equal(ids(browseToolbarModel(source, 'movie', [], titleSort, {...env, owner: true}).filterQuick).includes('unmatched'), true);
});

test('presentation never mutates frozen capability registries, predicates or current sort', () => {
  const capabilities = caps();
  const predicates = Object.freeze([{variant:'predicate',field: 'genre', operator: 'contains', value: 'R&B · Soul'}] as const);
  const sort = Object.freeze({field: 'random', direction: 'desc', seed: 0} as const);
  const before = JSON.stringify({capabilities, predicates, sort});
  const model = browseToolbarModel(capabilities, 'movie', predicates, sort, {...env, owner: true});
  assert.equal(JSON.stringify({capabilities, predicates, sort}), before);
  assert.ok(Object.isFrozen(model));
  for (const value of [model.quick, model.filterQuick, model.groups, model.sorts, model.sort, model.applied, ...model.groups.map(g => g.fields)]) assert.ok(Object.isFrozen(value));
});
