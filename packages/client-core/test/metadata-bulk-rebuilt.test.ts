import test from 'node:test';
import assert from 'node:assert/strict';
import {readBulkMetadataPreview, saveBulkMetadata} from '../src/metadata-bulk-rebuilt.ts';
import {validateBulkReceipt} from '../src/metadata-bulk.ts';
import {rebuiltMetadata} from './fixtures/rebuilt-metadata.ts';

// Definition 04 rules 27 to 30: titles of one kind edited together; what they share and what is Mixed; a list gains
// and loses entries; each title saved or not, with why, and Try again for those.
const targets = ['a', 'b'].map(id => ({kind: 'item' as const, contentKind: 'movie', id, libraryId: 'lib'}));
const api = (fake: ReturnType<typeof rebuiltMetadata>) => ({async request<T>(path: string, method?: string, body?: unknown): Promise<T> {
  const value = fake.answer(path, method, body); if (value === undefined) throw new Error('unexpected ' + path); return value as T;}});

test('the preview reads each title and says what they share, what is Mixed and how many are locked', async () => {
  const fake = rebuiltMetadata({a: {title: 'A', fields: {contentRating: 'PG', year: 1999}, lists: {genres: ['Drama'], labels: ['Noir']}, revision: '4', locked: ['year']},
    b: {title: 'B', fields: {contentRating: 'PG', year: 2001}, lists: {genres: ['Drama', 'Crime']}}});
  const preview = await readBulkMetadataPreview(api(fake), targets, 'server');
  assert.deepEqual(fake.reads, ['a', 'b']);
  assert.deepEqual(preview.schema.map(spec => spec.field), ['contentRating', 'year', 'edition', 'genres', 'studios', 'countries', 'tags']);
  assert.deepEqual([preview.fields.contentRating!.state, preview.fields.contentRating!.value], ['shared', 'PG']);
  assert.deepEqual([preview.fields.year!.state, preview.fields.year!.locked, preview.fields.year!.values.map(row => row.titles)], ['mixed', 1, [['A'], ['B']]]);
  assert.deepEqual(preview.fields.genres!.entries.map(row => [row.value, row.count]), [['Drama', 2], ['Crime', 1]]);
  assert.deepEqual(preview.excludedFields.map(spec => spec.label), ['Title']);
  assert.deepEqual(preview.targets.map(target => target.expectedRevision), ['4', '1']);
  await assert.rejects(readBulkMetadataPreview(api(rebuiltMetadata({a: {kind: 'movie'}, b: {kind: 'show'}})), targets, 'server'), {code: 'metadata-one-kind-required'});
});

test('a save is one edit_items job; a list gains and loses entries; fields are locked unless asked not to', async () => {
  const fake = rebuiltMetadata({a: {lists: {genres: ['Drama']}}, b: {}});
  const preview = await readBulkMetadataPreview(api(fake), targets, 'server');
  const edit = {operationId: 'op1', targets: preview.targets, fields: {contentRating: {value: 'R'}, year: {value: '1984'}}, lists: {tags: {add: ['Noir']}, studios: {add: ['New studio'], remove: ['Old studio']}, countries: {add: ['Canada']}}, genres: {add: ['Crime'], remove: ['Drama']}, lockEdited: true};
  const receipt = validateBulkReceipt(await saveBulkMetadata(api(fake), edit), edit);
  assert.deepEqual(fake.jobs, [{kind: 'edit_items', target: {items: ['a', 'b']}, params: {contentRating: 'R', year: 1984, add: {labels: ['Noir'], studios: ['New studio'], countries: ['Canada'], genres: ['Crime']}, remove: {studios: ['Old studio'], genres: ['Drama']}, lock: true}}]);
  assert.deepEqual([receipt.updated, receipt.failed], [2, 0]);
});

test('when the job could not save every title, each is read again: the result says title by title, and sending the same edit again saves the rest', async () => {
  const fake = rebuiltMetadata({a: {}, b: {}}, {fail: ['b']});
  const preview = await readBulkMetadataPreview(api(fake), targets, 'server');
  const edit = {operationId: 'op1', targets: preview.targets, fields: {contentRating: {value: 'R'}}, lockEdited: true};
  const first = validateBulkReceipt(await saveBulkMetadata(api(fake), edit), edit);
  assert.deepEqual(first.results.map(row => [row.id, row.ok, row.message]), [['a', true, undefined], ['b', false, 'Saved 1 of 2. Not saved: b.']]);
  const retry = {...edit, operationId: 'op2', targets: edit.targets.filter(target => target.id === 'b')};
  const fixed = rebuiltMetadata(fake.docs);
  assert.equal(validateBulkReceipt(await saveBulkMetadata(api(fixed), retry), retry).updated, 1);
});


test('matching values on an unsaved title do not falsely claim its requested lock was saved', async () => {
  const fake = rebuiltMetadata({a: {fields: {year: 1984}}}, {fail: ['a']});
  const preview = await readBulkMetadataPreview(api(fake), targets.slice(0, 1), 'server');
  const edit = {operationId: 'op1', targets: preview.targets, fields: {year: {value: '1984'}}, lockEdited: true};
  const result = validateBulkReceipt(await saveBulkMetadata(api(fake), edit), edit);
  assert.equal(result.failed, 1); assert.equal(result.updated, 0);
});
