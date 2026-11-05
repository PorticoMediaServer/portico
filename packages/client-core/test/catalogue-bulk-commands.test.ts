import assert from 'node:assert/strict';
import test from 'node:test';
import {catalogueBulkBase, catalogueBulkEdit, catalogueDeletionCommand} from '../src/catalogue-bulk-commands.ts';
import {bulkTargetLimit, validBulkEdit} from '../src/metadata-bulk.ts';
import type {RepairFieldSpec} from '../src/metadata-repair.ts';

const schema: readonly RepairFieldSpec[] = [
  {field: 'studio', label: 'Studio', group: 'general', type: 'text', bulk: true},
  {field: 'tags', label: 'Tags', group: 'general', type: 'list', bulk: true},
  {field: 'genres', label: 'Genres', group: 'general', type: 'list', bulk: true},
];

test('metadata commands preserve scalar/list/genre values, locks, target order and the 200 boundary', () => {
  const changes = {studio: {value: 'New Studio'}, tags: {add: [' Noir '], remove: ['Old']}, genres: {add: ['Drama'], remove: []}};
  const base = catalogueBulkBase(schema, changes, false)!;
  const targets = Array.from({length: bulkTargetLimit + 1}, (_, n) => ({kind: 'item' as const, id: `item-${n}`, expectedRevision: 'b'.repeat(64), libraryId: 'not-on-wire'}));
  const first = catalogueBulkEdit(base, targets, 0, 'operation-1'), tail = catalogueBulkEdit(base, targets, bulkTargetLimit, 'operation-2');
  assert.equal(first.targets.length, 200); assert.equal(tail.targets.length, 1);
  assert.equal(first.targets[199].id, 'item-199'); assert.equal(tail.targets[0].id, 'item-200');
  assert.equal('libraryId' in first.targets[0], false);
  assert.equal(validBulkEdit(first, schema), true); assert.equal(validBulkEdit(tail, schema), true);
  assert.equal(JSON.stringify(first), JSON.stringify({fields: {studio: {value: 'New Studio'}}, lists: {tags: {add: [' Noir '], remove: ['Old']}},
    genres: {add: ['Drama'], remove: []}, lockEdited: false, operationId: 'operation-1', targets: targets.slice(0, 200).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision}))}));
  assert.equal(first.lists!.tags!.add, changes.tags.add, 'the existing captured list is not normalized or replaced');
  assert.equal(catalogueBulkBase(schema, {}, true), undefined);
  assert.equal(catalogueBulkBase(schema, {unknown: {value: 'ignored'}}, true), undefined);
});

test('deletion command preserves the preview revision, raw confirmation, file choice and stable identity', () => {
  const command = catalogueDeletionCommand({revision: 7, targets: [{itemId: 'second'}, {itemId: 'first'}]} as never,
    {confirmation: ' 2 ', removeFiles: false}, 'delete-1');
  assert.equal(JSON.stringify(command), JSON.stringify({itemIds: ['second', 'first'], expectedRevision: 7, confirmation: ' 2 ', deleteFiles: false, operationId: 'delete-1'}));
  assert.equal(Object.isFrozen(command), true);
});
