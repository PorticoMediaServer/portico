import {test} from 'node:test';
import assert from 'node:assert/strict';
import {holdFraction, selectionCircleGeometry, selectionIntent} from '../src-next/kit/selection/selection-geometry.ts';

test('phone and TV circle geometry follows their own selection frames', () => {
  assert.deepEqual(selectionCircleGeometry(false), {diameter: 24, border: 2, check: 16});
  assert.equal(selectionCircleGeometry(true).diameter, 44);
  assert.equal(selectionCircleGeometry(false, 26).diameter, 26);
});
test('semantic intent distinguishes entering, toggling and extending a range', () => {
  assert.deepEqual(selectionIntent(false, false), {intent: 'enter'});
  assert.deepEqual(selectionIntent(false, true), {intent: 'toggle'});
  assert.deepEqual(selectionIntent(true, false), {intent: 'toggle'});
  assert.deepEqual(selectionIntent(false, true, true), {intent: 'range', shiftKey: true});
});
test('hold feedback never fabricates progress when measurement is absent', () => {
  assert.equal(holdFraction(null), null);
  assert.equal(holdFraction(NaN), null);
  assert.equal(holdFraction(.53), .53);
  assert.equal(holdFraction(-1), 0);
  assert.equal(holdFraction(2), 1);
});

import {bulkReceiptView, pickerResultSucceeded, changeList, changeScalar, deleteSelectionGate, failedSelectionIds, removeFieldChange, setListChange} from '../src-next/kit/selection/presentation.ts';
import type {DeletePreview} from '../../../packages/client-core/src/administration.ts';

const preview: DeletePreview = {
  revision: 7, confirmation: '4', confirmationKind: 'count', totalBytes: 0, totalFiles: 4, totalBytesText: '0 B', blocked: [],
  targets: ['a', 'b', 'c', 'd'].map(itemId => ({itemId, title: itemId, kind: 'movie', libraryId: 'one', libraryName: 'one', allowMediaDeletion: true, titles: 1, bytes: 0, files: [], dependents: []})),
};
const valid = {owner: true, selectionCount: 4, preview, confirmation: '4'};
test('deletion requires an exact preview and confirmation, with one library and no safety blocks', () => {
  assert.deepEqual(deleteSelectionGate(valid), {allowed: true});
  assert.equal(deleteSelectionGate({...valid, owner: false}).reason, 'owner');
  assert.equal(deleteSelectionGate({...valid, preview: null}).reason, 'preview');
  assert.equal(deleteSelectionGate({...valid, selectionCount: 5}).reason, 'preview');
  assert.equal(deleteSelectionGate({...valid, confirmation: ' 4'}).reason, 'confirmation');
  assert.equal(deleteSelectionGate({...valid, confirmation: ''}).reason, 'confirmation');
  assert.equal(deleteSelectionGate({...valid, pending: true}).reason, 'pending');
  assert.equal(deleteSelectionGate({...valid, selectionCount: 240}).reason, 'limit');
  assert.equal(deleteSelectionGate({...valid, preview: {...preview, blocked: ['one']}}).reason, 'blocked');
  assert.equal(deleteSelectionGate({...valid, preview: {...preview, targets: preview.targets.map((target, index) => index ? target : {...target, libraryId: 'two'})}}).reason, 'libraries');
  assert.equal(deleteSelectionGate({...valid, preview: {...preview, targets: preview.targets.map((target, index) => index ? target : {...target, allowMediaDeletion: false})}}).reason, 'blocked');
});
test('partial retries retain only failed ids, without repeating prior successes', () => {
  assert.deepEqual(failedSelectionIds([{id: 'a', ok: true}, {id: 'b', ok: false}, {id: 'c', ok: true}, {id: 'b', ok: false}]), ['b']);
  assert.deepEqual(failedSelectionIds([{id: 'a', ok: true}]), []);
});
test('editing a scalar never writes untouched Mixed values and reset restores omission', () => {
  const original = {tags: {add: ['night'], remove: []}};
  const changed = changeScalar(original, 'contentRating', 'G');
  assert.deepEqual(changed, {tags: {add: ['night'], remove: []}, contentRating: {value: 'G'}});
  assert.deepEqual(original, {tags: {add: ['night'], remove: []}});
  assert.deepEqual(removeFieldChange(changed, 'contentRating'), original);
});
test('list changes stay incremental and cannot add and remove one value together', () => {
  const added = changeList({}, 'tags', 'night', 'add');
  assert.deepEqual(changeList(added, 'tags', 'night', 'add'), {tags: {add: ['night'], remove: []}});
  assert.deepEqual(changeList(added, 'tags', 'night', 'remove'), {tags: {add: [], remove: ['night']}});
  assert.deepEqual(added, {tags: {add: ['night'], remove: []}});
});

test('undoing the last list change leaves the field entirely untouched', () => {
  assert.deepEqual(setListChange({tags: {add: ['night'], remove: []}}, 'tags', [], []), {});
});

test('a refreshed preview changes the confirmation instead of reusing a prior title', () => {
  const single = {...preview, confirmationKind: 'title' as const, confirmation: 'Current title', targets: preview.targets.slice(0, 1)};
  assert.equal(deleteSelectionGate({owner: true, selectionCount: 1, preview: single, confirmation: 'Old title'}).reason, 'confirmation');
  assert.equal(deleteSelectionGate({owner: true, selectionCount: 1, preview: single, confirmation: 'Current title'}).allowed, true);
  assert.equal(deleteSelectionGate({owner: true, selectionCount: 1, preview: single, confirmation: 'Current title', loading: true}).reason, 'loading');
});

test('bulk results project latest real target outcomes without an aggregate operation id', () => {
  const first = {operationId: 'first', updated: 1, failed: 2, results: [{kind: 'item', id: 'a', ok: true}, {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict'}, {kind: 'item', id: 'c', ok: false, code: 'not_found'}]};
  const retry = {operationId: 'retry-b', updated: 1, failed: 0, results: [{kind: 'item', id: 'b', ok: true}]};
  const view = bulkReceiptView([first, retry, retry]);
  assert.equal(view.updated, 2); assert.equal(view.failed, 1);
  assert.deepEqual(failedSelectionIds(view.results), ['c']);
  assert.deepEqual(view.results.map(row => [row.id, row.ok]), [['a', true], ['b', true], ['c', false]]);
  assert.equal('operationId' in view, false);
  assert.equal(first.results[1]!.ok, false);
});

test('picker success requires supplied insertion and failure facts rather than processed counts', () => {
  const result = {destination: {id: 'real-destination', title: 'Actual destination'}, complete: true, processed: 200, total: 200, failures: []};
  assert.equal(pickerResultSucceeded(result), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20}), false);
  const withExisting = {...result, inserted: 20, failed: 0, alreadyIn: 180};
  assert.equal(pickerResultSucceeded(withExisting), true);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 1}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, unknown: 1}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, pending: true}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, complete: false}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, failures: [{id: 'failure', title: 'Actual title', reason: 'Actual reason'}]}), false);
});
