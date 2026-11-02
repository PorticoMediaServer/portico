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

test('frame 6: a field says where its value came from the way an owner would', async () => {
  const {metadataFieldSource} = await import('../src/screens/metadata/editor-presentation.ts');
  assert.deepEqual(metadataFieldSource('provider:tmdb'), {kind: 'provider', provider: 'tmdb'});
  assert.deepEqual(metadataFieldSource('tvdb'), {kind: 'provider', provider: 'tvdb'});
  assert.deepEqual(metadataFieldSource('manual'), {kind: 'edited'});
  assert.deepEqual(metadataFieldSource('scanner'), {kind: 'library'});
  assert.equal(metadataFieldSource('automatic'), undefined, 'an empty value has no origin to name');
  assert.equal(metadataFieldSource(''), undefined);
});

test('frame 6: only the description counts characters, numbers give their range', async () => {
  const {metadataFieldMeter} = await import('../src/screens/metadata/editor-presentation.ts');
  assert.equal(metadataFieldMeter({type: 'text', maxLength: 300}), undefined);
  assert.equal(metadataFieldMeter({type: 'date', maxLength: 10}), undefined);
  assert.equal(metadataFieldMeter({type: 'multiline', maxLength: 20000}), 'count');
  assert.equal(metadataFieldMeter({type: 'integer', min: 0, max: 9999}), 'range');
  assert.equal(metadataFieldMeter({type: 'integer'}), undefined);
});

test('frame 6: General draws names, then dates and ratings, then companies, with long text last', async () => {
  const {generalMetadataFields} = await import('../src/screens/metadata/editor-presentation.ts');
  const spec = (field: string, type = 'text', group = 'general') => ({field, type, group}) as never;
  const server = [spec('title'), spec('sortTitle'), spec('metadataLanguage'), spec('originalTitle'), spec('edition'), spec('tagline', 'text', 'text'), spec('description', 'multiline', 'text'),
    spec('year', 'integer', 'numbers'), spec('releaseDate', 'date'), spec('contentRating'), spec('studio'), spec('country'), spec('tags', 'list'), spec('provider', 'text', 'identity'), spec('episodeNumber', 'integer', 'numbers')];
  assert.deepEqual(generalMetadataFields(server).map((row: {field: string}) => row.field), ['title', 'sortTitle', 'originalTitle', 'metadataLanguage', 'year', 'releaseDate', 'contentRating', 'edition', 'studio', 'country', 'episodeNumber', 'tagline', 'description']);
});
