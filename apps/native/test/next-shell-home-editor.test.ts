import assert from 'node:assert/strict';
import test from 'node:test';
import {nativeBulkChanges, nativeBulkDraft, nativeBulkValues} from '../src-next/shell/selection-editor';
import type {BulkMetadataPreview} from '../../../packages/client-core/src/home-selection/metadata-preview';

test('native Home editor preserves exact Mixed and the saturated Many lower bound', () => {
  const preview: BulkMetadataPreview = {serverId: 's', viewerFence: 'v', contentKind: 'movie', kind: 'item', total: 100, targets: [], singleItemFields: [], excludedFields: [],
    schema: [{field: 'year', label: 'year', group: 'numbers', type: 'integer', bulk: true}],
    fields: {year: {state: 'mixed', distinctCount: 2, values: [{value: '2000', count: 40}, {value: '2001', count: 60}], entries: [], locked: 0, truncated: false}}};
  assert.deepEqual(nativeBulkValues(preview).year, preview.fields.year);
  const many: BulkMetadataPreview = {...preview, fields: {year: {state: 'many', atLeast: 65, values: [], entries: [], locked: 23, truncated: true}}};
  assert.deepEqual(nativeBulkValues(many).year, many.fields.year);
  assert.equal(Object.hasOwn(nativeBulkValues(many).year!, 'distinctCount'), false);
  assert.deepEqual(nativeBulkDraft({}, true), {lockEdited: true});
});

test('bulk adaptation preserves list add/remove and explicit blank scalar without replacing Mixed values', () => {
  const changes = {studio: {value: ''}, tags: {add: ['tag-a'], remove: ['tag-b']}, genres: {add: ['genre-a'], remove: []}};
  const draft = nativeBulkDraft(changes, true);
  assert.deepEqual(draft, {fields: {studio: {value: ''}}, lists: {tags: {add: ['tag-a'], remove: ['tag-b']}}, genres: {add: ['genre-a'], remove: []}, lockEdited: true});
  assert.deepEqual(nativeBulkChanges(draft), changes);
});

test('a list without a lawful add/remove contract cannot become an aggregate replacement', () => {
  assert.throws(() => nativeBulkDraft({unknown: {add: ['a'], remove: []}}, true), /unsupported_bulk_list_change/);
});
