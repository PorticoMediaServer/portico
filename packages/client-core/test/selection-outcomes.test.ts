import test from 'node:test';
import assert from 'node:assert/strict';
import {bulkReceiptView, deleteSelectionGate, pickerResultSucceeded, DELETE_SELECTION_LIMIT} from '../src/selection-outcomes.ts';
import type {DeletePreview} from '../src/administration.ts';
import type {BulkReceipt} from '../src/metadata-bulk.ts';

const receipt = (operationId: string, results: BulkReceipt['results']): BulkReceipt => ({operationId, results, updated: results.filter(row => row.ok).length, failed: results.filter(row => !row.ok).length});
const target = (itemId: string, libraryId = 'lib', allowMediaDeletion = true) => ({itemId, libraryId, libraryName: 'Films', title: `Title ${itemId}`, kind: 'movie', titles: 1, files: [], bytes: 0, allowMediaDeletion, dependents: []});
const preview = (targets: readonly ReturnType<typeof target>[], overrides: Partial<DeletePreview> = {}): DeletePreview => Object.freeze({
  revision: 3, confirmation: targets.length === 1 ? targets[0]!.title : String(targets.length), confirmationKind: targets.length === 1 ? 'title' as const : 'count' as const,
  totalBytes: 0, totalFiles: 0, totalBytesText: '0 B', blocked: [], targets: Object.freeze(targets), ...overrides,
});
const gate = (input: Partial<Parameters<typeof deleteSelectionGate>[0]>) => deleteSelectionGate({owner: true, selectionCount: 1, preview: preview([target('a')]), confirmation: 'Title a', ...input});

test('the latest receipt per kind and id wins, in either direction', () => {
  const receipts = [
    receipt('op1', [{kind: 'item', id: 'a', ok: false, code: 'failed'}, {kind: 'item', id: 'b', ok: true}]),
    receipt('op2', [{kind: 'item', id: 'a', ok: true}, {kind: 'item', id: 'b', ok: false, code: 'failed'}]),
  ];
  const view = bulkReceiptView(receipts);
  assert.deepEqual(view.results.map(row => [row.id, row.ok]), [['a', true], ['b', false]]);
  assert.equal(view.updated, 1);
  assert.equal(view.failed, 1);
});

test('success replaces an earlier failure for the same target and failures replace success', () => {
  assert.deepEqual(bulkReceiptView([receipt('op1', [{kind: 'item', id: 'a', ok: false}]), receipt('op2', [{kind: 'item', id: 'a', ok: true}])]).results.map(row => row.ok), [true]);
  assert.deepEqual(bulkReceiptView([receipt('op1', [{kind: 'item', id: 'a', ok: true}]), receipt('op2', [{kind: 'item', id: 'a', ok: false}])]).results.map(row => row.ok), [false]);
});

test('the same id in another kind is another target', () => {
  const view = bulkReceiptView([receipt('op1', [{kind: 'item', id: 'a', ok: true}, {kind: 'show', id: 'a', ok: false}])]);
  assert.deepEqual(view.results.map(row => [row.kind, row.id]), [['item', 'a'], ['show', 'a']]);
  assert.equal(view.updated, 1);
  assert.equal(view.failed, 1);
});

test('an empty receipt list is an empty view, not a success', () => {
  assert.deepEqual(bulkReceiptView([]), {results: [], updated: 0, failed: 0});
});

const picker = (overrides: Partial<Parameters<typeof pickerResultSucceeded>[0]> = {}) => ({complete: true, inserted: 2, failed: 0, unknown: 0, pending: false, failures: [], ...overrides});

test('a picker succeeds only on a complete observation with present inserted count and no failures', () => {
  assert.equal(pickerResultSucceeded(picker()), true);
  assert.equal(pickerResultSucceeded(picker({unknown: undefined})), true);
  assert.equal(pickerResultSucceeded(picker({complete: false})), false);
  assert.equal(pickerResultSucceeded(picker({inserted: undefined})), false);
  assert.equal(pickerResultSucceeded(picker({failed: 1})), false);
  assert.equal(pickerResultSucceeded(picker({unknown: 1})), false);
  assert.equal(pickerResultSucceeded(picker({pending: true})), false);
  assert.equal(pickerResultSucceeded(picker({failures: [{id: 'a', title: 'A', reason: 'no'}]})), false);
  assert.equal(pickerResultSucceeded(picker({failed: undefined})), false);
  // Zero is a real count and a real success.
  assert.equal(pickerResultSucceeded(picker({inserted: 0, failed: 0})), true);
});

test('the exact preview and its typed confirmation are required', () => {
  assert.deepEqual(gate({}), {allowed: true});
  assert.deepEqual(gate({confirmation: 'Title b'}), {allowed: false, reason: 'confirmation'});
  assert.deepEqual(gate({confirmation: ''}), {allowed: false, reason: 'confirmation'});
  assert.deepEqual(gate({confirmation: 'title a'}), {allowed: false, reason: 'confirmation'});
  assert.deepEqual(gate({preview: null}), {allowed: false, reason: 'preview'});
  assert.deepEqual(gate({preview: preview([])}), {allowed: false, reason: 'preview'});
  // A preview whose confirmation is empty proves nothing to type.
  assert.deepEqual(gate({preview: preview([target('a')], {confirmation: ''}), confirmation: ''}), {allowed: false, reason: 'preview'});
});

test('counts are positive safe integers within the mock-up limit', () => {
  for (const count of [0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, DELETE_SELECTION_LIMIT + 1]) {
    assert.deepEqual(gate({selectionCount: count}), {allowed: false, reason: 'limit'}, String(count));
  }
  assert.equal(DELETE_SELECTION_LIMIT, 200);
  const many = Array.from({length: 200}, (_, index) => target(`i${index}`));
  assert.deepEqual(deleteSelectionGate({owner: true, selectionCount: 200, preview: preview(many), confirmation: '200'}), {allowed: true});
  assert.deepEqual(deleteSelectionGate({owner: true, selectionCount: 200, preview: preview(many.slice(1)), confirmation: '199'}), {allowed: false, reason: 'preview'});
});

test('the preview must hold exactly one unique target per selected item', () => {
  assert.deepEqual(gate({selectionCount: 2}), {allowed: false, reason: 'preview'});
  assert.deepEqual(gate({selectionCount: 2, preview: preview([target('a'), target('a')]), confirmation: '2'}), {allowed: false, reason: 'preview'});
  assert.deepEqual(gate({selectionCount: 2, preview: preview([target('a'), target('b')]), confirmation: '2'}), {allowed: true});
  // A substituted target set still counts as two unique items, so membership is the controller's.
  assert.deepEqual(gate({selectionCount: 2, preview: preview([target('x'), target('y')]), confirmation: '2'}), {allowed: true});
});

test('one library, deletion allowed everywhere, never a blocked or disallowed preview', () => {
  assert.deepEqual(gate({selectionCount: 2, preview: preview([target('a'), target('b', 'other')]), confirmation: '2'}), {allowed: false, reason: 'libraries'});
  assert.deepEqual(gate({preview: preview([target('a', 'lib', false)], {confirmation: 'Title a'})}), {allowed: false, reason: 'blocked'});
  assert.deepEqual(gate({preview: preview([target('a')], {confirmation: 'Title a', blocked: ['lib']})}), {allowed: false, reason: 'blocked'});
  assert.deepEqual(gate({selectionCount: 2, preview: preview([target('a'), target('b', 'lib', false)]), confirmation: '2'}), {allowed: false, reason: 'blocked'});
});

test('ownership, loading and busy states close the prompt', () => {
  assert.deepEqual(gate({owner: false}), {allowed: false, reason: 'owner'});
  assert.deepEqual(gate({loading: true}), {allowed: false, reason: 'loading'});
  assert.deepEqual(gate({pending: true}), {allowed: false, reason: 'pending'});
  assert.deepEqual(gate({loading: true, pending: true, confirmation: 'wrong'}), {allowed: false, reason: 'loading'});
});

test('every refusal is a frozen fact, and the allowed gate carries no reason', () => {
  const refused = gate({owner: false});
  assert.equal(Object.isFrozen(refused), true);
  assert.equal('reason' in gate({}), false);
  assert.equal(Object.isFrozen(gate({})), true);
});
