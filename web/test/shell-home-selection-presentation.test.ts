import test from 'node:test';
import assert from 'node:assert/strict';
import type {BulkReceipt} from '@core/metadata-bulk.ts';
import type {BulkMetadataPreview, HomeSelectionMember, HomeSelectionPicker, HomeSelectionSnapshot} from '@core/home-selection/index.ts';
import {editorCoreDraft, editorResultView, editorUiDraft, editorValues, pickerResultView, selectionEditorAudience, selectionMemberDisplay} from '../src/shell/selection-presentation.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';
import {retryPickerError, retryPickerOperation, retryPickerWindow, runSelectionCommand} from '../src/shell/selection-actions.ts';
import {pickerResultSucceeded} from '../src/kit/selection/presentation.ts';
import {shellSelectionScope} from '../src/shell/selection-scope.ts';

test('selection scope retains the authenticated source and rejects unsupported authority without guessing', () => {
  const viewer = {serverId: 'server', accountId: 'account', profileId: 'profile', role: 'owner', authority: 'local'};
  for (const authority of ['local', 'hosted']) {
    assert.deepEqual(shellSelectionScope({...viewer, authority}, '/?row=recent'),
      {serverId: 'server', accountId: 'account', profileId: 'profile', authority, viewId: '/?row=recent'});
  }
  for (const authority of ['', 'account', 'other']) assert.throws(() => shellSelectionScope({...viewer, authority}, '/'), /unsupported-home-authority/);
});

test('a menu command consumes its current canonical target before the overlay close callback', async () => {
  let menuOpen = true, called = 0;
  const errors: unknown[] = [];
  runSelectionCommand(() => { assert.equal(menuOpen, true); called++; return Promise.resolve(); }, error => errors.push(error));
  menuOpen = false;
  assert.equal(called, 1);
  await Promise.resolve();
  assert.deepEqual(errors, []);
});
test('command feedback catches synchronous authority failures and asynchronous transport rejection', async () => {
  const sync = new Error('retired'), asyncError = new Error('busy'), errors: unknown[] = [];
  runSelectionCommand(() => { throw sync; }, error => errors.push(error));
  runSelectionCommand(() => Promise.reject(asyncError), error => errors.push(error));
  await Promise.resolve();
  assert.deepEqual(errors, [sync, asyncError]);
});

test('editing sends only changed scalar fields and keeps genre and tag add/remove deltas distinct', () => {
  const draft = {fields: {year: '2026'}, lists: {genres: {add: [f.genre], remove: []}, tags: {add: [], remove: [f.genre]}}, lockEdited: true};
  const body = editorCoreDraft(draft);
  assert.deepEqual(body, {fields: {year: {value: '2026'}}, lists: {tags: {add: [], remove: [f.genre]}}, genres: {add: [f.genre], remove: []}, lockEdited: true});
  assert.deepEqual(editorUiDraft(body), draft);
  assert.ok(!Object.hasOwn(body.fields!, 'title'));
});
test('metadata preview preserves many as a lower bound and retains exact bounded entries', () => {
  const entries = Array.from({length: 64}, (_, index) => ({value: String(index), count: 1}));
  const preview = {schema: [{field: 'tags', type: 'list', bulk: true}], fields: {tags: {state: 'many', atLeast: 65, truncated: true, values: [], entries, locked: 23}}} as unknown as BulkMetadataPreview;
  const shown = editorValues(preview).tags;
  assert.equal(shown.state, 'many'); assert.equal(shown.atLeast, 65); assert.equal(shown.distinctCount, undefined); assert.equal(shown.truncated, true);
  assert.equal(shown.entries, entries); assert.deepEqual(shown.values, []);
  assert.equal(editorValues(preview), preview.fields);
});
test('an actual shared empty string survives the editor display and unchanged draft omits it', () => {
  const preview = {schema: [{field: 'studio', type: 'text', bulk: true}], fields: {studio: {state: 'shared', distinctCount: 1, truncated: false, value: '', values: [{value: '', count: 4}], entries: [], locked: 0}}} as unknown as BulkMetadataPreview;
  const shown = editorValues(preview).studio;
  assert.equal(shown.state, 'shared');
  assert.deepEqual(shown.values, [{value: '', count: 4}]);
  assert.deepEqual(editorCoreDraft(editorUiDraft({lockEdited: true})).fields, {});
});
test('dialog display keeps real container and source-local channel names without a renderer identity cache', () => {
  const collection = {item: {kind: 'collection', id: 'collection', libraryId: 'movies'}, collection: {collection: {id: 'collection', name: f.movie}, members: []}} as unknown as HomeSelectionMember;
  assert.deepEqual(selectionMemberDisplay(collection), {id: 'collection', title: f.movie, posterUrl: undefined});
  const channel = {item: {kind: 'channel', sourceId: 'source', channelId: 'channel'}, channel: {channel: {sourceId: 'source', id: 'channel', name: f.channel}}} as unknown as HomeSelectionMember;
  assert.equal(selectionMemberDisplay(channel).id, JSON.stringify(['source', 'channel']));
  assert.equal(selectionMemberDisplay(channel).title, f.channel);
  assert.throws(() => selectionMemberDisplay({item: {kind: 'movie', id: 'private-id'}} as HomeSelectionMember), /display-required/);
});
test('actual retry receipts replace the prior failed status without counting a successful title twice', () => {
  const receipts: BulkReceipt[] = [
    {operationId: 'first', updated: 1, failed: 1, results: [{kind: 'item', id: 'one', ok: true}, {kind: 'item', id: 'two', ok: false, code: 'revision_mismatch'}]},
    {operationId: 'retry', updated: 1, failed: 0, results: [{kind: 'item', id: 'two', ok: true}]},
  ];
  const result = editorResultView(receipts)!;
  assert.equal(result.updated, 2); assert.equal(result.failed, 0); assert.equal(result.results.length, 2);
  assert.equal(result.results[1], receipts[1].results[0]);
  assert.equal('operationId' in result, false);
  assert.equal(receipts[0].results[1].ok, false);
  assert.equal(editorResultView([]), undefined);
});

function destinationPicker(): HomeSelectionPicker {
  const jobs: HomeSelectionPicker['jobs'] = [{jobId: 'original-job', command: 'playlist-add', state: 'partial', done: 20, failed: 4, total: 24, totalKnown: true}];
  return {kind: 'playlist', phase: 'error', rows: [], query: '', next: false, previous: false, error: null, results: [], jobs,
    destination: {kind: 'playlist', id: 'new-resource-outside-page', name: f.collection, artwork: [], count: 26, revision: 4, canAdd: true},
    receipt: {kind: 'playlist', destinationId: 'new-resource-outside-page', jobIds: ['original-job'], jobs, added: 2, alreadyPresent: 18, failed: 3, unknown: 1,
      complete: false, pending: false, unresolvedJobIds: [], failures: ['actual-leaf', 'second-leaf', 'third-leaf'].map(itemId => ({itemId, code: 'not_found'})),
      failureEntries: ['actual-leaf', 'second-leaf', 'third-leaf'].map(id => ({id, title: f.episode, kind: 'episode' as const, posterUrl: 'selected-art'})), nextCursor: ''}};
}
test('picker uses stable distinct outcomes and retained destination display, never processed jobs or selected parent counts', () => {
  const picker = destinationPicker(), result = pickerResultView(picker, code => `localized:${code}`)!;
  assert.equal(result.destination.id, picker.destination!.id); assert.equal(result.destination.name, f.collection);
  assert.equal(result.destination.size, 26);
  assert.equal(picker.rows.length, 0);
  assert.equal(result.inserted, 2); assert.equal(result.alreadyIn, 18); assert.equal(result.failed, 3); assert.equal(result.unknown, 1);
  assert.equal(result.complete, false); assert.equal(result.pending, false);
  assert.equal(Object.hasOwn(result, 'processed'), false); assert.equal(Object.hasOwn(result, 'total'), false);
  assert.deepEqual(result.failures[0], {id: 'actual-leaf', title: f.episode, reason: 'localized:not_found'});
  assert.equal(pickerResultSucceeded(result), false);
});
test('unknown or absent outcomes cannot become a success toast and failure identities must match actual leaves', () => {
  const picker = destinationPicker(), result = pickerResultView(picker, code => code)!;
  assert.equal(pickerResultView({...picker, receipt: null}, code => code), undefined);
  assert.equal(pickerResultView({...picker, destination: null}, code => code), undefined);
  assert.equal(pickerResultSucceeded({...result, complete: true, failed: 0, failures: []}), false);
  assert.equal(pickerResultSucceeded({...result, complete: true, failed: undefined, unknown: 0, failures: []}), false);
  assert.equal(pickerResultSucceeded({...result, complete: true, failed: 0, unknown: 0, failures: []}), true);
  assert.throws(() => pickerResultView({...picker, receipt: {...picker.receipt!, failureEntries: picker.receipt!.failureEntries.map((entry, index) => index === 0 ? {...entry, id: 'parent', title: f.show} : entry)}}, code => code), /failure-display/);
});
test('a bounded failure window never replaces the durable distinct failed count', () => {
  const picker = destinationPicker(), ids = Array.from({length: 100}, (_, index) => `leaf-${index}`);
  const receipt = {...picker.receipt!, failed: 103, failures: ids.map(itemId => ({itemId, code: 'not_found'})),
    failureEntries: ids.map(id => ({id, title: f.episode, kind: 'episode' as const})), nextCursor: 'actual-next-window'};
  const result = pickerResultView({...picker, receipt}, code => code)!;
  assert.equal(result.failed, 103); assert.equal(result.failures.length, 100);
  assert.deepEqual(result.failures.map(row => row.id), ids);
});
test('failed-only picker retry consumes the current bounded leaf window and refuses stale, successful or parent IDs', async () => {
  const initial = destinationPicker();
  let picker: HomeSelectionPicker = {...initial, phase: 'partial', receipt: {...initial.receipt!, unknown: 0, complete: true}}, retries = 0;
  const controller = {getSnapshot: () => ({picker}) as HomeSelectionSnapshot, retryPicker: async () => { retries++; }};
  const ids = picker.receipt!.failures.map(row => row.itemId);
  await retryPickerWindow(controller, ids); assert.equal(retries, 1);
  for (const ids of [[], ['parent'], ['actual-leaf', 'actual-leaf']]) assert.throws(() => retryPickerWindow(controller, ids), /invalid-picker-retry-window/);
  picker = {...picker, phase: 'uncertain'};
  assert.throws(() => retryPickerWindow(controller, ids), /invalid-picker-retry-window/);
  await retryPickerOperation(controller); assert.equal(retries, 2);
  picker = {...picker, phase: 'ready'};
  assert.throws(() => retryPickerOperation(controller), /not-uncertain/);
  assert.throws(() => retryPickerError(controller), /not-in-error/);
  assert.equal(retries, 2);
  picker = {...picker, phase: 'error'};
  await retryPickerError(controller); assert.equal(retries, 3);
});
test('an admitted editor audience reflects the actual viewer role without granting commands', () => {
  assert.deepEqual(selectionEditorAudience('owner'), {authority: 'owner'});
  assert.equal(selectionEditorAudience('admin').authority, 'administrator');
  assert.equal(selectionEditorAudience('administrator').authority, 'administrator');
  assert.equal(selectionEditorAudience('member').authority, 'editor');
  assert.notEqual(selectionEditorAudience('member').label, selectionEditorAudience('admin').label);
});
