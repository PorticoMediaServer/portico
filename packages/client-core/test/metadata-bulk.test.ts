import test from 'node:test';
import assert from 'node:assert/strict';
import {validBulkEdit, validateBulkReceipt, bulkConflicts, bulkTargetLimit} from '../src/metadata-bulk.ts';
import type {BulkEdit} from '../src/metadata-bulk.ts';
import type {RepairFieldSpec} from '../src/metadata-repair.ts';

const schema: readonly RepairFieldSpec[] = [
  {field: 'title', label: 'Title', group: 'general', type: 'text', maxLength: 300, bulk: false},
  {field: 'contentRating', label: 'Content rating', group: 'general', type: 'text', maxLength: 300, bulk: true},
  {field: 'tags', label: 'Tags', group: 'general', type: 'list', maxLength: 128, max: 64, bulk: true},
];
const fence = (c: string) => c.repeat(64);
const targets = [
  {kind: 'item' as const, id: 'a', expectedRevision: fence('1')},
  {kind: 'item' as const, id: 'b', expectedRevision: fence('2')},
];
const edit: BulkEdit = {operationId: 'batch-one', targets, fields: {contentRating: {value: 'PG-13'}}};

test('only fields the registry marks bulk-editable may be sent', () => {
  assert.equal(validBulkEdit(edit, schema), true);
  assert.equal(validBulkEdit({...edit, fields: {title: {value: 'One title'}}}, schema), false);
  assert.equal(validBulkEdit({...edit, fields: {unknown: {value: 'x'}}}, schema), false);
  assert.equal(validBulkEdit({...edit, fields: {}}, schema), false);
  // A list edit is add/remove, never a replacement, and tags is the only list here.
  assert.equal(validBulkEdit({operationId: 'b', targets, lists: {tags: {add: ['Noir']}}}, schema), true);
  assert.equal(validBulkEdit({operationId: 'b', targets, lists: {tags: {}}}, schema), false);
  assert.equal(validBulkEdit({operationId: 'b', targets, lists: {labels: {add: ['x']}}}, schema), false);
  assert.equal(validBulkEdit({operationId: 'b', targets, lists: {tags: {add: ['  ']}}}, schema), false);
  assert.equal(validBulkEdit({operationId: 'b', targets, lists: {tags: {add: ['x'.repeat(129)]}}}, schema), false);
  assert.equal(validBulkEdit({operationId: 'b', targets, fields: {tags: {values: ['Noir']}}}, schema), true);
  assert.equal(validBulkEdit({operationId: 'b', targets, fields: {contentRating: {values: ['Noir']}}}, schema), false);
  assert.equal(validBulkEdit({operationId: 'b', targets, fields: {contentRating: {value: 'PG', useAutomatic: true}}}, schema), false);
  assert.equal(validBulkEdit({operationId: 'b', targets, genres: {add: ['Drama']}}, schema), true);
  assert.equal(validBulkEdit({operationId: 'b', targets: [{kind: 'show', id: 'a', expectedRevision: fence('1')}], genres: {add: ['Drama']}}, schema), false);
});

test('the batch is bounded, single-kind, deduplicated and fenced per target', () => {
  assert.equal(validBulkEdit({...edit, targets: []}, schema), false);
  assert.equal(validBulkEdit({...edit, targets: Array.from({length: bulkTargetLimit + 1}, (_, n) => ({kind: 'item' as const, id: 'i' + n, expectedRevision: fence('1')}))}, schema), false);
  assert.equal(validBulkEdit({...edit, targets: [targets[0], targets[0]]}, schema), false);
  assert.equal(validBulkEdit({...edit, targets: [targets[0], {kind: 'show' as const, id: 'b', expectedRevision: fence('2')}]}, schema), false);
  assert.equal(validBulkEdit({...edit, targets: [{kind: 'item' as const, id: 'a', expectedRevision: 'not-a-fence'}]}, schema), false);
  assert.equal(validBulkEdit({...edit, operationId: 'has spaces'}, schema), false);
});

test('a partial receipt is a normal result that names exactly the targets sent', () => {
  const raw = {
    operationId: 'batch-one', updated: 1, failed: 1,
    results: [
      {kind: 'item', id: 'a', ok: true, revision: fence('c')},
      {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict', message: 'Metadata changed.'},
    ],
  };
  const receipt = validateBulkReceipt(raw, edit);
  assert.equal(receipt.updated, 1);
  assert.deepEqual([...bulkConflicts(receipt)], ['b']);
  // The receipt must answer the request: same order, same ids, counts that add up.
  assert.throws(() => validateBulkReceipt({...raw, results: [raw.results[1], raw.results[0]]}, edit));
  assert.throws(() => validateBulkReceipt({...raw, results: [raw.results[0]]}, edit));
  assert.throws(() => validateBulkReceipt({...raw, updated: 2}, edit));
  assert.throws(() => validateBulkReceipt({...raw, operationId: 'other'}, edit));
  assert.throws(() => validateBulkReceipt({...raw, results: [{kind: 'item', id: 'a', ok: true}, raw.results[1]]}, edit));
  // A success that carries a field this app does not use is still a success.
  assert.equal(validateBulkReceipt({...raw, results: [{...raw.results[0], code: 'edit_failed', note: 'x'}, raw.results[1]]}, edit).results[0]!.ok, true);
});
