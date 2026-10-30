import test from 'node:test';
import assert from 'node:assert/strict';
import type {RepairData} from '../../packages/client-core/src/metadata-repair.ts';
import {compactMetadataField, metadataTabCount} from '../src/screens/metadata/editor-presentation.ts';

const data = {
  schema: [{field: 'title', type: 'text'}, {field: 'genres', type: 'list'}, {field: 'tags', type: 'list'}, {field: 'labels', type: 'list'}],
  snapshot: {fields: {genres: {values: ['Drama', 'Adventure']}, tags: {values: ['Desert']}, labels: {values: []}},
    relationships: [{kind: 'credit'}, {kind: 'genre'}, {kind: 'credit'}]},
  artwork: {candidates: [{id: 'one'}, {id: 'two'}]}, candidates: [{id: 'match'}], history: [{id: 1}, {id: 2}, {id: 3}],
} as unknown as RepairData;

test('metadata tab counts come from the available records and keep unrelated relationships out', () => {
  assert.equal(metadataTabCount('artwork', data, {}), 2);
  assert.equal(metadataTabCount('people', data, {}), 2);
  assert.equal(metadataTabCount('matching', data, {}), 1);
  assert.equal(metadataTabCount('history', data, {}), 3);
  assert.equal(metadataTabCount('general', data, {}), undefined);
  assert.equal(metadataTabCount('lyrics', data, {}), undefined);
  assert.equal(metadataTabCount('tags', undefined, {}), undefined);
});

test('tag counts track visible draft values, including clearing the complete list', () => {
  assert.equal(metadataTabCount('tags', data, {}), 3);
  assert.equal(metadataTabCount('tags', data, {genres: {value: '', values: [], locked: true}}), 1);
  assert.equal(metadataTabCount('tags', data, {tags: {value: 'A, B', values: ['A, B', 'C'], locked: true}}), 4);
  assert.equal(metadataTabCount('tags', data, {genres: {value: '', locked: true}}), 3);
});

test('compact fields pair numbers and dates but keep titles, language and prose full width', () => {
  for (const [field, type] of [['year', 'integer'], ['seasonNumber', 'integer'], ['airDate', 'date'], ['contentRating', 'text'], ['edition', 'text'], ['series', 'text']] as const) {
    assert.equal(compactMetadataField({field, type}), true, field);
  }
  for (const [field, type] of [['title', 'text'], ['sortTitle', 'text'], ['metadataLanguage', 'enum'], ['description', 'multiline'], ['tagline', 'text']] as const) {
    assert.equal(compactMetadataField({field, type}), false, field);
  }
});
