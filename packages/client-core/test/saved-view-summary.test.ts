import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {parseBrowseCapabilities, type BrowseNode, type BrowseSortSelection} from '../src/browse.ts';
import type {SavedDefinition} from '../src/personal-saved.ts';
import {savedViewSummary} from '../src/presentation/saved-view-summary.ts';
import {orderToolbarPredicates, toolbarModel} from '../src/presentation/browse-toolbar.ts';
import {browseToolbarModel} from '../src/presentation/kit-model/browse.ts';

const field = (id: string, type: string, operators: readonly string[]) => ({id, type, operators, labelKey: `browse.field.${id}`, controlHint: type === 'identity-set' ? 'facet-multi-select' : type === 'enum' ? 'select' : type === 'date' ? 'date-range' : 'number-range', ...(type === 'enum' ? {allowedValues: ['unplayed', 'in-progress', 'played']} : {}), applicableKinds: ['movie']});
const caps = parseBrowseCapabilities({library: {id: 'actual-library', name: 'Movies', kind: 'movie', defaultView: 'browse'},
  pivots: [{id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true}],
  fields: [field('genre', 'identity-set', ['contains-any', 'in']), field('year', 'number', ['between', 'at-least']), field('decade', 'number', ['equals', 'in']), field('playState', 'enum', ['equals']), field('durationMs', 'number', ['at-most', 'at-least', 'between']), field('communityRating', 'number', ['at-least']), field('dateAdded', 'date', ['at-least', 'within-last-days']), field('releaseDate', 'date', ['within-last-days']), field('lastPlayedAt', 'date', ['within-last-days'])],
  quickFilters: [{id: 'unwatched', labelKey: 'filter.unwatched', query: {variant:'predicate',field: 'playState', operator: 'equals', value: 'unplayed'}}, {id: 'in-progress', labelKey: 'filter.inProgress', query: {variant:'predicate',field: 'playState', operator: 'equals', value: 'in-progress'}}],
  sorts: ['title', 'added', 'duration', 'communityRating', 'lastPlayed'].map(id => ({id, labelKey: `sort.${id}`, directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']})),
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 10000}});
const node = (field: string, operator: 'equals' | 'contains-any' | 'in' | 'at-most' | 'at-least' | 'between' | 'within-last-days', value: string | number | readonly (string | number)[]): BrowseNode => ({field, operator, value});
const definition = (query: BrowseNode | null, field = 'title', direction: 'asc' | 'desc' = 'asc'): SavedDefinition => ({libraryId: 'actual-library', pivot: 'movies', query, sort: [{field, direction}], presentation: 'grid'});

test('§38 six drawn Saved tile summaries and conditions-only TV names are exact', t => {
  t.mock.timers.enable({apis: ['Date'], now: new Date('2026-10-23T12:00:00Z')});
  const vectors = [
    [definition({variant:'group',all: [node('playState', 'equals', 'unplayed'), node('year', 'between', [1990, 1999]), node('genre', 'contains-any', ['Action'])]}, 'added', 'desc'), 'Genre Action, Year 1990s, Unwatched. Newest first.', 'Genre Action, Year 1990s, Unwatched'],
    [definition(node('durationMs', 'at-most', 5400000), 'duration'), 'Length under 90 minutes. Shortest first.', 'Length under 90 minutes'],
    [definition(node('playState', 'equals', 'unplayed')), 'Unwatched. Title A to Z.', 'Unwatched'],
    [definition(node('communityRating', 'at-least', 8), 'communityRating', 'desc'), 'Rating 8 and up. Highest first.', 'Rating 8 and up'],
    [definition(node('dateAdded', 'at-least', '2026-09-23'), 'added', 'desc'), 'Added in the last 30 days. Newest first.', 'Added in the last 30 days'],
    [definition(node('playState', 'equals', 'in-progress'), 'lastPlayed', 'desc'), 'In progress. Last played first.', 'In progress'],
  ] as const;
  for (const [value, line, conditions] of vectors) {
    const before = structuredClone(value), result = savedViewSummary(value, caps, defaultI18n);
    assert.deepEqual(result, {line, conditions, suggestedName: conditions});
    assert.deepEqual(value, before, 'rendering never rewrites a captured definition');
    assert(Object.isFrozen(result));
  }
});

test('out-of-toolbar grammar, absent fields/operators and wrong library/pivot never expose wire text', () => {
  const leaf = node('genre', 'contains-any', ['Action']);
  const bad: SavedDefinition[] = [
    definition({variant:'group',any: [leaf, node('year', 'at-least', 2000)]}), definition({variant:'group',not: leaf}), definition({variant:'group',all: [{variant:'group',all: [leaf]}]}), definition({variant:'group',all: []}),
    definition(node('unknown-wire-field', 'equals', 'secret-wire-value')), definition(node('genre', 'between', ['A', 'Z'])),
    {...definition(leaf), libraryId: 'other-library'}, {...definition(leaf), pivot: 'other-pivot'},
    {...definition(leaf), sort: [{field: 'unknown-sort', direction: 'asc'}]},
    {...definition(leaf), sort: [{field: 'title', direction: 'asc'}, {field: 'added', direction: 'desc'}]},
  ];
  for (const value of bad) assert.equal(savedViewSummary(value, caps, defaultI18n), undefined);
  const unknownLabel = {...caps, fields: [...caps.fields, {...caps.fields[0], id: 'untranslated-field', labelKey: 'untranslated.wire.key'}]};
  assert.equal(savedViewSummary(definition(node('untranslated-field', 'contains-any', ['Value'])), unknownLabel, defaultI18n), undefined);
});

test('disjunction and metadata spelling are shared; several decades use the same list grammar', () => {
  assert.equal(savedViewSummary(definition(node('genre', 'in', ['Sci-Fi & Fantasy', 'Comedy'])), caps, defaultI18n)?.line, 'Genre Sci-Fi & Fantasy or Comedy. Title A to Z.');
  assert.equal(savedViewSummary(definition(node('decade', 'in', [1990, 2000])), caps, defaultI18n)?.line, 'Year 1990s or 2000s. Title A to Z.');
  assert.equal(defaultI18n.t('filter.field.audienceRating'), 'Audience rating', 'the summary-only Rating template never changes the toolbar');
});

test('no conditions and names over 200 Unicode scalars yield an empty suggestion while line remains', () => {
  assert.deepEqual(savedViewSummary(definition(null), caps, defaultI18n), {line: 'Title A to Z.', conditions: '', suggestedName: ''});
  const exactly = savedViewSummary(definition(node('genre', 'contains-any', ['🎬'.repeat(194)])), caps, defaultI18n)!;
  assert.equal([...exactly.suggestedName].length, 200);
  const longer = savedViewSummary(definition(node('genre', 'contains-any', ['🎬'.repeat(195)])), caps, defaultI18n)!;
  assert.equal(longer.suggestedName, '');assert.equal([...longer.conditions].length, 201);assert(longer.line.length > 0);
});

test('only current date presets claim last-N-days; old saved absolute boundaries stay absolute', t => {
  t.mock.timers.enable({apis: ['Date'], now: new Date('2026-10-23T12:00:00Z')});
  for (const count of [7, 30, 90, 365]) {
    const date = new Date(Date.now() - count * 86400000).toISOString().slice(0, 10);
    assert.equal(savedViewSummary(definition(node('dateAdded', 'at-least', date)), caps, defaultI18n)?.conditions, `Added in the last ${count} days`);
  }
  const old = savedViewSummary(definition(node('dateAdded', 'at-least', '2020-01-01')), caps, defaultI18n);
  assert(old);assert.doesNotMatch(old.line, /last .* days/);assert.match(old.line, /2020/);
});

test('length ranges and unknown preset operators retain the shared chip grammar', () => {
  assert.equal(savedViewSummary(definition(node('durationMs', 'between', [3600000, 5400000])), caps, defaultI18n)?.conditions, 'Length 60–90 minutes');
  assert.equal(savedViewSummary(definition({variant:'group',all: [node('durationMs', 'at-least', 3600000), node('durationMs', 'at-most', 5400000)]}), caps, defaultI18n)?.conditions, 'Length 60–90 minutes');
  assert.equal(savedViewSummary(definition(node('durationMs', 'at-least', 7200000)), caps, defaultI18n)?.conditions, 'Length over 120 minutes');
  assert.equal(savedViewSummary(definition(node('year', 'at-least', 2000)), caps, defaultI18n)?.conditions, 'Year 2000 and up');
});

test('one shared stable field ordering feeds both toolbar models, retaining whole compounds', () => {
  const predicates = [node('playState', 'equals', 'unplayed'), node('year', 'at-least', 1990), node('genre', 'contains-any', ['Action']), node('year', 'at-least', 2000)];
  assert.deepEqual(orderToolbarPredicates(predicates).map(value => 'field' in value ? value.field : ''), ['genre', 'year', 'year', 'playState']);
  const sort: BrowseSortSelection = {field: 'title', direction: 'asc'};
  for (const model of [toolbarModel(caps, 'movie', predicates, sort), browseToolbarModel(caps, 'movie', predicates, sort, {t: defaultI18n.t, locale: 'en-US'})]) {
    assert.deepEqual(model.applied.map(value => 'field' in value ? value.field : ''), ['genre', 'year', 'year']);
  }
  const unranked = [{variant:'predicate',field: 'unranked-one', operator: 'equals', value: 1}, {variant:'group',all: [node('genre', 'contains-any', ['Action'])]}, {variant:'predicate',field: 'unranked-two', operator: 'equals', value: 2}];
  assert.deepEqual(orderToolbarPredicates(unranked), unranked);
});


test('relative date summaries retain their meaning on later days and use the §38 vector', t => {
  t.mock.timers.enable({apis: ['Date'], now: new Date('2026-10-23T12:00:00Z')});
  for (const count of [1, 7, 30, 90, 365, 36500]) {
    const d = definition(node('dateAdded', 'within-last-days', count), 'added', 'desc');
    const expected = `Added in the last ${new Intl.NumberFormat(defaultI18n.locale).format(count)} days. Newest first.`;
    assert.equal(savedViewSummary(d, caps, defaultI18n)?.line, expected);
    t.mock.timers.tick(86400000);
    assert.equal(savedViewSummary(d, caps, defaultI18n)?.line, expected);
  }
  for (const [field, label] of [['releaseDate', 'Release date'], ['lastPlayedAt', 'Last played']]) {
    assert.equal(savedViewSummary(definition(node(field, 'within-last-days', 30)), caps, defaultI18n)?.conditions, `${label} in the last 30 days`);
  }
});


test('a Hermes build without Intl.ListFormat still renders the summary and suggested name', () => {
  const intl = Intl as unknown as {ListFormat?: unknown}, saved = intl.ListFormat;
  try {
    intl.ListFormat = undefined;
    const value = definition(node('genre', 'in', ['Comedy', 'Drama']));
    const result = savedViewSummary(value, caps, defaultI18n);
    assert.deepEqual(result, {line: 'Genre Comedy, Drama. Title A to Z.', conditions: 'Genre Comedy, Drama', suggestedName: 'Genre Comedy, Drama'});
    const narrowed = {t: defaultI18n.t, has: defaultI18n.has, locale: defaultI18n.locale};
    assert.equal(savedViewSummary(value, caps, narrowed)?.conditions, 'Genre Comedy, Drama', 'a caller without the optional list hook keeps the same fallback');
    assert.equal(savedViewSummary(definition(null), caps, defaultI18n)?.line, 'Title A to Z.', 'the no-ListFormat path never swallows the whole summary');
  } finally {
    intl.ListFormat = saved;
  }
});
