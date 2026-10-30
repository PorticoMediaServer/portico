import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n, type MessageId} from '../../../packages/i18n/src';
import {nativeRowFilterChange, nativeRowFilterField, nativeRowFilterValues} from '../src-next/screens/home/row-filters';
import {homeBrowseLabel} from '../src-next/screens/home/browse-label';
import type {BrowseFieldCapability, BrowseNode} from '../../../packages/client-core/src/browse';
const field: BrowseFieldCapability = {id: 'genre', labelKey: 'browse.field.genre', type: 'enum', operators: ['contains-any', 'contains-all'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']};
const now = new Date('2026-10-23T00:00:00Z');
test('changing a row facet preserves other-field compounds and makes exact inclusion/exclusion predicates', () => {
  const compound: BrowseNode = {any: [{field: 'year', operator: 'equals', value: 2026}, {field: 'genre', operator: 'contains-any', value: ['Drama']}]};
  const filters: BrowseNode[] = [compound, {field: 'genre', operator: 'contains-any', value: ['Comedy']}];
  assert.equal(nativeRowFilterField(compound), undefined);
  assert.deepEqual(nativeRowFilterChange(field, {kind: 'values', included: ['Action'], excluded: ['Horror'], mode: 'all'}, filters, now),
    [compound, {field: 'genre', operator: 'contains-all', value: ['Action']}, {not: {field: 'genre', operator: 'contains-any', value: ['Horror']}}]);
  assert.deepEqual(filters[1], {field: 'genre', operator: 'contains-any', value: ['Comedy']});
  assert.throws(() => nativeRowFilterChange({...field, operators: ['contains-any']}, {kind: 'values', included: ['Action'], excluded: [], mode: 'all'}, [], now), /not_offered/);
});
test('clearing a row control removes only its own predicates, including its negative group', () => {
  const other: BrowseNode = {field: 'homeRow', operator: 'equals', value: 'actual-row'};
  assert.deepEqual(nativeRowFilterChange(field, {kind: 'values', included: [], excluded: [], mode: 'any'}, [other, {not: {field: 'genre', operator: 'contains-any', value: ['Horror']}}], now), [other]);
  assert.throws(() => nativeRowFilterChange({...field, id: 'durationMs', type: 'number'}, {kind: 'range', value: 'invented'}, [], now), /not_offered/);
});


test('editing a negative-only genre keeps every selected genre excluded', () => {
  const filters: BrowseNode[] = [{not: {field: 'genre', operator: 'contains-any', value: ['Horror']}}];
  const control = nativeRowFilterValues(field, filters);
  assert.deepEqual(control, {kind: 'values', included: ['Horror'], excluded: [], mode: 'none'});
  const next = nativeRowFilterChange(field, {...control, included: [...control.included, 'Thriller']}, filters, now);
  assert.deepEqual(next, [{not: {field: 'genre', operator: 'contains-any', value: ['Horror', 'Thriller']}}]);
  assert.deepEqual(filters, [{not: {field: 'genre', operator: 'contains-any', value: ['Horror']}}]);
});

test('Home row vocabulary uses the translator the screen is showing', () => {
  const words: string[] = [];
  const i18n = {has: (id: string): id is MessageId => id === 'filter.field.genre', t: (id: MessageId) => {words.push(id); return `GB/${id}`;}};
  assert.equal(homeBrowseLabel({id: 'genre'}, i18n), 'GB/filter.field.genre');
  assert.deepEqual(words, ['filter.field.genre'], 'the supplied translator names the row unless the catalogue lacks the word');
  assert.equal(homeBrowseLabel({id: 'genre'}), defaultI18n.t('filter.field.genre'));
});

test('a capability the catalogue has no word for is shown as its own id in words', () => {
  const words: string[] = [];
  const i18n = {has: (id: string): id is MessageId => id === 'filter.field.genre', t: (id: MessageId) => {words.push(id); return `GB/${id}`;}};
  const value = {id: 'bestMatchSort', labelKey: 'arbitrary.untranslated.key'};
  assert.equal(homeBrowseLabel(value, i18n), 'Best match sort', 'the fallback splits camelCase words');
  assert.deepEqual(words, [], 'no translation is asked for a word the catalogue lacks, and an arbitrary labelKey is never rendered');
  assert.equal(value.id, 'bestMatchSort', 'the admitted capability id is not rewritten by presentation');
  assert.equal(homeBrowseLabel({id: 'episode_count'}, i18n), 'Episode count', 'underscored ids read as words');
  assert.equal(homeBrowseLabel({id: 'invented-field'}, i18n), 'Invented field', 'hyphenated ids read as words');
});
