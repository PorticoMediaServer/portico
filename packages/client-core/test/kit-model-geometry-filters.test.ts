import type {BrowseOperator, BrowseNode} from '../src/browse.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {formatMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {appliedFilterParts, cardGeometry, hasUniformGeometry, type FilterOperator, type KitTranslate, type TitleKind} from '../src/presentation/kit-model/index.ts';
const t: KitTranslate = (id, ...args) => formatMessage(shared[id], args[0]);
const env = {t, locale: 'en-US'};

for (const [kind, shape, ratio] of [
  ['movie', 'poster', 2 / 3], ['show', 'poster', 2 / 3], ['season', 'poster', 2 / 3], ['episode', 'poster', 2 / 3], ['extra', 'poster', 2 / 3],
  ['artist', 'circle', 1], ['author', 'circle', 1], ['album', 'square', 1], ['song', 'square', 1], ['disc', 'square', 1],
  ['book', 'square', 1], ['audiobook_file', 'square', 1], ['chapter', 'square', 1], ['book_series', 'square', 1],
] as const) test(`ordinary ${kind} geometry is ${shape} with width/height direction`, () => {
  assert.deepEqual(cardGeometry({domain: 'catalog', kind}, 'ordinary'), {shape, widthOverHeight: ratio});
});
test('continuation/search/detail/context geometry is explicit and never invents a banner ratio', () => {
  assert.deepEqual(cardGeometry({domain: 'catalog', kind: 'movie'}, 'continuation'), {shape: 'wide', widthOverHeight: 16 / 9});
  for (const context of ['continuation', 'search', 'in-show'] as const) assert.deepEqual(cardGeometry({domain: 'catalog', kind: 'episode'}, context), {shape: 'wide', widthOverHeight: 16 / 9});
  assert.equal(cardGeometry({domain: 'catalog', kind: 'extra'}, 'detail-extra').shape, 'wide');
  assert.equal(cardGeometry({domain: 'catalog', kind: 'album'}, 'continuation').shape, 'square');
  assert.deepEqual(cardGeometry({domain: 'catalog', kind: 'collection'}, 'collection-banner'), {shape: 'banner'});
  assert.equal(cardGeometry({domain: 'catalog', kind: 'collection', family: 'music'}, 'ordinary').shape, 'square');
  assert.equal(cardGeometry({domain: 'catalog', kind: 'category', family: 'audiobook'}, 'ordinary').shape, 'square');
  assert.deepEqual(cardGeometry({domain: 'channel'}, 'ordinary'), {shape: 'wide', widthOverHeight: 16 / 9});
  assert.deepEqual(cardGeometry({domain: 'person'}, 'ordinary'), {shape: 'circle', widthOverHeight: 1});
});
test('local Read vocabulary separates portrait books/comics and 3:4 periodicals from audiobook book', () => {
  for (const kind of ['book', 'comic'] as const) assert.deepEqual(cardGeometry({domain: 'read', kind}, 'ordinary'), {shape: 'poster', widthOverHeight: 2 / 3});
  for (const kind of ['magazine', 'newspaper'] as const) assert.deepEqual(cardGeometry({domain: 'read', kind}, 'ordinary'), {shape: 'periodical', widthOverHeight: 3 / 4});
  assert.equal(cardGeometry({domain: 'catalog', kind: 'book'}, 'ordinary').shape, 'square');
});
test('ordinary row callers must keep shapes uniform; no coercion or mixed-row assumption', () => {
  const geometries = (kinds: readonly TitleKind[]) => kinds.map(kind => cardGeometry({domain: 'catalog', kind}, 'ordinary'));
  assert.equal(hasUniformGeometry(geometries(['movie', 'show', 'episode'])), true);
  assert.equal(hasUniformGeometry(geometries(['album', 'book'])), true);
  assert.equal(hasUniformGeometry(geometries(['movie', 'album'])), false);
  assert.equal(hasUniformGeometry(geometries(['artist', 'album'])), false);
  assert.equal(hasUniformGeometry([]), true);
});

const fields = (id: string) => ({year: 'Year', genre: 'Genre', rating: 'Critic rating', collection: 'Collection'}[id] ?? id);
for (const [operator, label, negated, presence] of [
  ['equals', ':', false, undefined], ['not-equals', 'is not', true, undefined], ['in', 'is one of', false, undefined], ['not-in', 'is not one of', true, undefined],
  ['is-present', 'Has', false, 'present'], ['is-missing', 'No', true, 'missing'], ['at-least', '≥', false, undefined], ['greater-than', '>', false, undefined],
  ['contains', 'contains', false, undefined], ['contains-any', 'contains any of', false, undefined], ['contains-all', 'contains all of', false, undefined],
  ['at-most', '≤', false, undefined], ['less-than', '<', false, undefined], ['between', 'to', false, undefined], ['starts-with', 'starts with', false, undefined],
] as const) test(`applied ${operator} retains exact operator, negation and presence`, () => {
  const result = appliedFilterParts({field: 'rating', operator, value: operator === 'between' ? [2, 8] : 8}, fields, env);
  assert.equal(result.fieldId, 'rating'); assert.deepEqual(result.field, {id: 'field', text: 'Critic rating', role: 'context'});
  assert.equal(result.operator, operator); assert.deepEqual(result.operatorPart, {id: 'operator', text: label, role: 'context'});
  assert.equal(result.negated, negated); assert.equal(result.presence, presence);
  assert.deepEqual(result.values.map(x => x.text), presence ? [] : operator === 'between' ? ['2', '8'] : ['8']);
});
test('filters retain multi-values, boolean false/zero, prefixes and provider punctuation', () => {
  const decade = appliedFilterParts({field: 'decade', operator: 'not-in', value: [1990, 2000]}, fields, env);
  assert.equal(decade.field.text, 'Year'); assert.equal(decade.fieldId, 'year'); assert.equal(decade.negated, true);
  assert.deepEqual(decade.values.map(x => x.text), ['1990s', '2000s']);
  assert.deepEqual(appliedFilterParts({field: 'x', operator: 'in', value: [false, true, 0, null, NaN]}, fields, env).values.map(x => x.text), ['No', 'Yes', '0']);
  assert.equal(appliedFilterParts({field: 'genre', operator: 'starts-with', value: 'R&B · Soul'}, fields, env).values[0]!.text, 'R&B · Soul');
  assert.deepEqual(appliedFilterParts({field: 'genre', operator: 'is-missing', value: ['ignored']}, fields, env).values, []);
});

// This record must fail compilation when the real browse domain adds an uncovered operator.
const actualOperators = {
  equals: ':', 'not-equals': 'is not', contains: 'contains', 'starts-with': 'starts with', in: 'is one of', 'not-in': 'is not one of',
  'less-than': '<', 'at-most': '≤', 'greater-than': '>', 'at-least': '≥', between: 'to', 'is-present': 'Has', 'is-missing': 'No',
  'contains-any': 'contains any of', 'contains-all': 'contains all of',
} as const satisfies Record<BrowseOperator, string>;
function exactOperatorDomain(value: FilterOperator): BrowseOperator {return value;}
function exactFilterDomain(value: BrowseOperator): FilterOperator {return value;}
void exactOperatorDomain; void exactFilterDomain;
test('actual browse predicates retain all 15 operators and exact raw values at the model boundary', () => {
  assert.equal(Object.keys(actualOperators).length, 15);
  for (const operator of Object.keys(actualOperators) as BrowseOperator[]) {
    const value = operator === 'is-present' || operator === 'is-missing' ? null : ['in', 'not-in', 'between', 'contains-any', 'contains-all'].includes(operator) ? ['R&B · Soul', 'raw_ID'] : 'raw_ID';
    const node: Extract<BrowseNode, {field: string}> = {field: 'genre', operator, value};
    const model = appliedFilterParts(node, fields, env);
    assert.equal(model.operator, operator);
    assert.equal(model.operatorPart.text, actualOperators[operator]);
    assert.equal(model.rawValue, value);
    assert.deepEqual(model.values.map(p => p.text), value === null ? [] : Array.isArray(value) ? value : [value]);
  }
  const any = appliedFilterParts({field: 'genre', operator: 'contains-any', value: ['Drama', 'Comedy']}, fields, env);
  const all = appliedFilterParts({field: 'genre', operator: 'contains-all', value: ['Drama', 'Comedy']}, fields, env);
  assert.notEqual(any.operatorPart.text, all.operatorPart.text);
  assert.deepEqual(any.values, all.values);
  const raw = [false, 0, null, undefined, NaN, 'raw_ID'] as const;
  assert.equal(appliedFilterParts({field: 'x', operator: 'in', value: raw}, fields, env).rawValue, raw);
});
