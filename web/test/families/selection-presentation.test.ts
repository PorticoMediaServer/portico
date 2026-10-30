import test from 'node:test';
import assert from 'node:assert/strict';
import type {DeletePreview} from '../../../packages/client-core/src/administration.ts';
import type {RepairFieldSpec} from '../../../packages/client-core/src/metadata-repair.ts';
import {selectionKinds} from '../../../packages/client-core/src/selection-model/capabilities.ts';
import {addListValue, bulkDraftValid, bulkEditorSchema, bulkFieldDisplayValue, bulkSummary, bulkReceiptView, pickerResultSucceeded, deletePermission, missingPickerIds, selectionCountText, toggleListRemoval} from '../../src/kit/selection/presentation.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import type {BulkEditorDraft, BulkFieldValues, SelectionMediaKind} from '../../src/kit/selection/types.ts';

test('every canonical selection kind has an exact singular and plural count label', () => {
  const labels: Readonly<Record<SelectionMediaKind, readonly [string, string]>> = {
    movie: ['1 movie', '2 movies'], show: ['1 show', '2 shows'], season: ['1 season', '2 seasons'], episode: ['1 episode', '2 episodes'],
    artist: ['1 artist', '2 artists'], album: ['1 album', '2 albums'], song: ['1 song', '2 songs'], book: ['1 book', '2 books'],
    audiobook_file: ['1 audiobook part', '2 audiobook parts'], chapter: ['1 chapter', '2 chapters'], collection: ['1 collection', '2 collections'],
    playlist: ['1 playlist', '2 playlists'],
    ebook: ['1 book', '2 books'], comic: ['1 title', '2 titles'],
    category: ['1 category', '2 categories'], author: ['1 author', '2 authors'], book_series: ['1 series', '2 series'], disc: ['1 disc', '2 discs'], extra: ['1 extra', '2 extras'],
    channel: ['1 channel', '2 channels'], person: ['1 person', '2 people'],
  };
  for (const kind of selectionKinds) {
    assert.equal(selectionCountText({kind, count: 1}), labels[kind][0], kind);
    assert.equal(selectionCountText({kind, count: 2}), labels[kind][1], kind);
  }
  assert.equal(selectionCountText({count: 1}), '1 title');
  assert.equal(selectionCountText({count: 2}), '2 titles');
});

const schema: readonly RepairFieldSpec[] = [
  {field: 'year', label: 'Year', group: 'numbers', type: 'integer', min: 1800, max: 2200, bulk: true},
  {field: 'title', label: 'Title', group: 'general', type: 'text', bulk: false},
  {field: 'tags', label: 'Tags', group: 'general', type: 'list', bulk: true},
];
const values: Readonly<Record<string, BulkFieldValues>> = {year: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: '2019', count: 1}, {value: '2020', count: 3}], entries: []}, tags: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: 'shared', count: 3}, {value: '', count: 1}], entries: [{value: 'shared', count: 3}]}};
const empty: BulkEditorDraft = {fields: {}, lists: {}, lockEdited: true};

test('language display and summary use the canonical language formatter without changing write values', () => {
  for (const value of ['en', 'fr', 'en-US', 'zh-Hant', '']) assert.equal(bulkFieldDisplayValue('metadataLanguage', value), value ? defaultI18n.languageName(value) : '');
  assert.equal(bulkFieldDisplayValue('studio', 'en'), 'en');
  const registry = [{field: 'metadataLanguage', label: 'Metadata language', group: 'general' as const, type: 'text' as const, bulk: true}];
  const actual = {metadataLanguage: {state: 'mixed' as const, distinctCount: 2, truncated: false, values: [{value: 'en', count: 2}, {value: 'fr', count: 1}], entries: []}};
  const draft = {...empty, fields: {metadataLanguage: 'en'}};
  const summary = bulkSummary(registry, actual, draft, 3);
  assert.equal(summary[0].text, 'Metadata language becomes English on all 3');
  assert.equal(summary[0].changedCount, 1);
  assert.equal(draft.fields.metadataLanguage, 'en');
});

test('only actual bulk fields can enter a save; untouched Mixed fields never become empty writes', () => {
  assert.equal(bulkDraftValid(schema, values, empty), false);
  assert.equal(bulkDraftValid(schema, values, {...empty, fields: {year: '2020'}}), true);
  assert.equal(bulkDraftValid(schema, values, {...empty, fields: {title: 'changed'}}), false);
  assert.equal(bulkDraftValid(schema, values, {...empty, fields: {unsupported: 'value'}}), false);
  assert.equal(bulkDraftValid(schema, {}, {...empty, fields: {year: '2020'}}), false);
  assert.deepEqual(bulkSummary(schema, values, empty, 4), []);
});

test('schema numeric limits reject invalid values before an operation is offered', () => {
  for (const year of ['not-a-number', 'Infinity', '2020.5', '1799', '2201']) assert.equal(bulkDraftValid(schema, values, {...empty, fields: {year}}), false, year);
  assert.equal(bulkDraftValid(schema, values, {...empty, fields: {year: ''}}), true);
});

test('the summary counts the actual aggregate difference instead of claiming all titles change', () => {
  const summary = bulkSummary(schema, values, {...empty, fields: {year: '2020'}}, 4);
  assert.equal(summary.length, 1);
  assert.equal(summary[0]!.changedCount, 1);
  assert.equal(summary[0]!.text, 'Year becomes 2020 on all 4');
});

test('Genres is retained only when present in the actual bulk registry', () => {
  const actual = [...schema, {field: 'genres', label: 'Genres', group: 'general' as const, type: 'list' as const, bulk: true}];
  assert.deepEqual(bulkEditorSchema(actual, 'movie').map(field => field.field), ['year', 'genres', 'tags']);
  assert.deepEqual(bulkEditorSchema(schema, 'movie').map(field => field.field), ['year', 'tags']);

});

test('list changes preserve shared and partial values and never replace a list', () => {
  const removed = toggleListRemoval(undefined, 'shared');
  assert.deepEqual(removed, {add: [], remove: ['shared']});
  assert.deepEqual(toggleListRemoval(removed, 'shared'), {add: [], remove: []});
  const added = addListValue(removed, '  shared  ');
  assert.deepEqual(added, {add: ['shared'], remove: []});
  assert.deepEqual(addListValue(added!, 'shared'), added);
  assert.deepEqual(toggleListRemoval(added!, 'shared'), {add: [], remove: []});
  assert.equal(addListValue(undefined, '  '), null);
  assert.equal(addListValue(undefined, 'bad\u0000value'), null);
  assert.equal(bulkDraftValid(schema, values, {...empty, lists: {tags: {add: ['shared'], remove: ['shared']}}}), false);
});

test('picker retry sends only unique missing ids in their real result order', () => {
  assert.deepEqual(missingPickerIds([{id: 'failed-2', title: 'second', reason: 'changed'}, {id: 'failed-1', title: 'first', reason: 'changed'}, {id: 'failed-2', title: 'second', reason: 'changed'}]), ['failed-2', 'failed-1']);
  assert.deepEqual(missingPickerIds([]), []);
});

const target = {itemId: 'title-1', title: 'title', kind: 'movie', libraryId: 'library-1', libraryName: 'library', allowMediaDeletion: true, titles: 1, bytes: 10, files: [], dependents: []};
const preview: DeletePreview = {revision: 9, confirmation: 'title', confirmationKind: 'title', totalBytes: 10, totalFiles: 1, totalBytesText: '10 bytes', blocked: [], targets: [target]};

test('deletion requires the exact server confirmation, a real preview, an owner and a matching count', () => {
  assert.equal(deletePermission(true, 1, preview, 'title'), true);
  for (const text of ['', 'TITLE', ' title', 'title ']) assert.equal(deletePermission(true, 1, preview, text), false);
  assert.equal(deletePermission(false, 1, preview, 'title'), false);
  assert.equal(deletePermission(true, 1, null, 'title'), false);
  assert.equal(deletePermission(true, 2, preview, 'title'), false);
  assert.equal(deletePermission(true, 1, preview, 'title', true), false);
  assert.equal(deletePermission(true, 1, {...preview, confirmation: ''}, ''), false);
  assert.equal(deletePermission(true, 2, {...preview, confirmation: '2', confirmationKind: 'count', targets: [target, target]}, '2'), false);
});

test('delete restrictions cannot be bypassed through a typed count or a whole-view selection', () => {
  assert.equal(deletePermission(true, 201, {...preview, confirmation: '201', confirmationKind: 'count'}, '201'), false);
  assert.equal(deletePermission(true, 1, {...preview, blocked: ['library-1']}, 'title'), false);
  assert.equal(deletePermission(true, 1, {...preview, targets: [{...target, allowMediaDeletion: false}]}, 'title'), false);
  const mixed: DeletePreview = {...preview, confirmation: '2', confirmationKind: 'count', targets: [target, {...target, itemId: 'title-2', libraryId: 'library-2'}]};
  assert.equal(deletePermission(true, 2, mixed, '2'), false);
});

test('a truncated distribution cannot invent how many targets already have an omitted value', () => {
  const limited: BulkFieldValues = {state: 'mixed', distinctCount: 80, truncated: true, values: Array.from({length: 64}, (_, i) => ({value: String(1900 + i), count: 1})), entries: []};
  const omitted = bulkSummary(schema, {year: limited}, {...empty, fields: {year: '2000'}}, 80);
  assert.equal(omitted[0]!.changedCount, undefined);
  assert.equal(omitted[0]!.changedTitles, undefined);
  assert.equal(bulkSummary(schema, {year: limited}, {...empty, fields: {year: '1900'}}, 80)[0]!.changedCount, 79);
  assert.equal(limited.distinctCount, 80);
  assert.equal(limited.values.length, 64);
});

test('latest receipt outcomes retain unrelated failures and count retried successes once', () => {
  const first = {operationId: 'first', updated: 1, failed: 2, results: [{kind: 'item', id: 'a', ok: true}, {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict'}, {kind: 'item', id: 'c', ok: false, code: 'not_found'}]};
  const retry = {operationId: 'retry-b', updated: 1, failed: 0, results: [{kind: 'item', id: 'b', ok: true}]};
  const view = bulkReceiptView([first, retry, retry]);
  assert.equal(view.updated, 2); assert.equal(view.failed, 1);
  assert.deepEqual(view.results.map(row => [row.id, row.ok]), [['a', true], ['b', true], ['c', false]]);
  assert.equal('operationId' in view, false);
  assert.equal(first.results[1]!.ok, false);
  assert.deepEqual(bulkReceiptView([]), {results: [], updated: 0, failed: 0});
});

test('picker success requires supplied insertion and failure facts rather than processed counts', () => {
  const result = {destination: {id: 'real-destination', name: 'Actual destination'}, complete: true, processed: 200, total: 200, failures: []};
  assert.equal(pickerResultSucceeded(result), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, alreadyIn: 180}), true);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 1}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, unknown: 1}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, pending: true}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, complete: false}), false);
  assert.equal(pickerResultSucceeded({...result, inserted: 20, failed: 0, failures: [{id: 'failure', title: 'Actual title', reason: 'Actual reason'}]}), false);
});

test('a shared preview with an absent value cannot claim a known prior difference count', () => {
  const unknown: BulkFieldValues = {state: 'shared', distinctCount: 1, truncated: false, values: [], entries: []};
  assert.equal(bulkSummary(schema, {year: unknown}, {...empty, fields: {year: '2020'}}, 4)[0]!.changedCount, undefined);
});
