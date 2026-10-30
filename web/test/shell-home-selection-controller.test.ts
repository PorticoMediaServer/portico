import test from 'node:test';
import assert from 'node:assert/strict';
import {HomeSelectionController, homeSelectionContext, type HomeSelectionOptions} from '@core/home-selection/index.ts';
import type {HomeActions, HomeDocument, HomeEntry, HomeRow} from '@core/home.ts';
import type {HomeService} from '@core/home-service.ts';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {retryEditorRead} from '../src/shell/selection-actions.ts';
import {editorCoreDraft, editorResultView} from '../src/shell/selection-presentation.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';

const fence = 'a'.repeat(64), revision = 'b'.repeat(64);
type Target = {kind: 'item'; contentKind: 'movie'; id: string; libraryId: string};
type EditBody = {operationId: string; targets: Target[]; fields: unknown};
function preview(targets: readonly Target[]) {
  const year = {field: 'year', label: 'Year', group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: true};
  const title = {field: 'title', label: 'Title', group: 'general', type: 'text', bulk: false};
  return {serverId: 'server', viewerFence: fence, contentKind: 'movie', kind: 'item', total: targets.length,
    schema: [year], singleItemFields: [year, title], excludedFields: [title],
    fields: {year: {state: 'shared', value: '2020', distinctCount: 1, values: [{value: '2020', count: targets.length}], entries: [], locked: 0, truncated: false}},
    targets: targets.map(target => ({kind: target.kind, id: target.id, libraryId: target.libraryId, expectedRevision: revision}))};
}
function editorController(request: HomeSelectionOptions['api']['request']) {
  const offers: HomeActions = {play: false, queue: false, playlist: false, collection: false, myList: false, favorite: false, watched: false, played: false,
    rating: false, editMetadata: true, refreshMetadata: false, delete: false, download: false, details: true,
    selection: {play: false, queue: false, playlist: false, collection: false, myList: false, favorite: false, watched: false, download: false, edit: true, refresh: false, delete: false},
    state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 0, containerRevision: 0}};
  const entries: HomeEntry[] = ['one', 'two', 'three'].map(id => ({id, libraryId: 'library', kind: 'movie', title: f.movie, homeActions: offers}));
  const row: HomeRow = {id: 'recent', viewerFence: fence, title: f.libraryPrefix, kind: 'media', artworkShape: 'poster', endpoint: '', privacySensitivity: '', policyState: '', priority: 0,
    required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true, entries, total: 3, start: 0, limit: 3, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1}};
  const document: HomeDocument = {serverId: 'server', viewerFence: fence, rows: [row], spotlight: [], layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T00:00:00Z'};
  let sequence = 0;
  const unexpected = async (): Promise<never> => { throw new Error('Unexpected playback'); };
  const controller = new HomeSelectionController({api: {request}, home: {getSnapshot: () => ({phase: 'ready', document, paging: new Set()}), subscribe: () => () => {}} as unknown as HomeService,
    context: homeSelectionContext({serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'profile', viewId: 'home'}, document), events: {on: () => () => {}},
    environment: {platform: 'desktop-web', isOwner: true, canEditMetadata: true}, isCurrent: () => true, requestId: async () => `00000000-0000-4000-8000-${String(++sequence).padStart(12, '0')}`,
    playback: {play: unexpected, enqueue: unexpected, playChannel: unexpected}, routes: {openEntry: unexpected, openCollection: unexpected, openDestination: unexpected}, reportError: error => { throw error; }});
  controller.registerWindow({id: 'home:recent', row, sectionOrder: 0}); controller.selectLoaded();
  return controller;
}
test('shell retry after a failed one-title preview preserves the exact subset, draft, successful outcomes and receipt history', async () => {
  const reads: string[][] = [], writes: EditBody[] = [];
  const controller = editorController(async (path, _method, raw) => {
    const body = raw as EditBody & {targets: Target[]};
    if (path === routePath('post_metadata_bulk_preview')) {
      reads.push(body.targets.map(target => target.id));
      if (reads.length === 2) throw new Error('preview unavailable');
      return preview(body.targets) as never;
    }
    assert.equal(path, routePath('post_metadata_bulk')); writes.push(body);
    return {operationId: body.operationId, updated: 1, failed: writes.length === 1 ? 2 : 0,
      results: body.targets.map(target => writes.length === 1 && target.id !== 'one'
        ? {kind: 'item', id: target.id, ok: false, code: 'metadata_conflict', message: ''}
        : {kind: 'item', id: target.id, ok: true, revision})} as never;
  });
  await controller.openEditor();
  const draft = editorCoreDraft({fields: {year: '2026'}, lists: {}, lockEdited: true});
  controller.setEditorDraft(draft); await controller.saveEditor();
  await controller.retryEditor(draft, ['two']);
  assert.equal(controller.getSnapshot().editor!.phase, 'error');
  const retained = controller.getSnapshot().editor!.receipts[0];
  await retryEditorRead(controller, controller.getSnapshot().editor!.phase);
  const editor = controller.getSnapshot().editor!;
  assert.deepEqual(reads, [['one', 'two', 'three'], ['two'], ['two']]);
  assert.deepEqual(writes.map(body => body.targets.map(target => target.id)), [['one', 'two', 'three'], ['two']]);
  assert.equal(editor.receipts[0], retained); assert.deepEqual(editor.draft, draft);
  assert.equal(editor.receipts.length, 2);
  assert.deepEqual(editorResultView(editor.receipts)?.results.map(row => [row.id, row.ok]), [['one', true], ['two', true], ['three', false]]);
  controller.dispose();
});
test('shell uncertain retry repeats the genuine original operation body and never reloads or re-sends confirmed successes', async () => {
  const bodies: unknown[] = []; let previews = 0;
  const controller = editorController(async (path, _method, raw) => {
    const body = raw as EditBody;
    if (path === routePath('post_metadata_bulk_preview')) { previews++; return preview(body.targets) as never; }
    assert.equal(path, routePath('post_metadata_bulk')); bodies.push(raw);
    if (bodies.length === 1) throw new Error('reply lost');
    return {operationId: body.operationId, updated: body.targets.length, failed: 0, results: body.targets.map(target => ({kind: target.kind, id: target.id, ok: true, revision}))} as never;
  });
  await controller.openEditor(); controller.setEditorDraft(editorCoreDraft({fields: {year: '2026'}, lists: {}, lockEdited: true})); await controller.saveEditor();
  assert.equal(controller.getSnapshot().editor!.phase, 'uncertain');
  await retryEditorRead(controller, controller.getSnapshot().editor!.phase);
  assert.equal(previews, 1); assert.equal(bodies[0], bodies[1]); assert.equal(controller.getSnapshot().editor!.receipts.length, 1);
  controller.dispose();
});
