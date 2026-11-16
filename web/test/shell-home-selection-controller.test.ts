import test from 'node:test';
import assert from 'node:assert/strict';
import {HomeSelectionController, homeSelectionContext, type HomeSelectionOptions} from '@core/home-selection/index.ts';
import type {HomeActions, HomeDocument, HomeEntry, HomeRow} from '@core/home.ts';
import type {HomeService} from '@core/home-service.ts';
import {rebuiltMetadata} from '../../packages/client-core/test/fixtures/rebuilt-metadata.ts';
import {retryEditorRead} from '../src/shell/selection-actions.ts';
import {editorCoreDraft, editorResultView} from '../src/shell/selection-presentation.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

const fence = 'a'.repeat(64), revision = 'b'.repeat(64);
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
  const controller = new HomeSelectionController(withQueries({api: {request}, home: {getSnapshot: () => ({phase: 'ready', document, paging: new Set()}), subscribe: () => () => {}} as unknown as HomeService,
    context: homeSelectionContext({serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'profile', viewId: 'home'}, document), events: {on: () => () => {}},
    environment: {platform: 'desktop-web', isOwner: true, canEditMetadata: true}, isCurrent: () => true, requestId: async () => `00000000-0000-4000-8000-${String(++sequence).padStart(12, '0')}`,
    playback: {play: unexpected, enqueue: unexpected, playChannel: unexpected}, routes: {openEntry: unexpected, openCollection: unexpected, openDestination: unexpected}, reportError: error => { throw error; }}));
  controller.registerWindow({id: 'home:recent', row, sectionOrder: 0}); controller.selectLoaded();
  return controller;
}
test('shell retry after a failed one-title preview preserves the exact subset, draft, successful outcomes and receipt history', async () => {
  const reads: string[] = [], failed = ['two', 'three']; let readTwo = 0;
  const fake = rebuiltMetadata({one: {}, two: {}, three: {}}, {fail: failed});
  const controller = editorController(async (path, method, raw) => {
    const match = /^\/v1\/admin\/items\/([^/]+)\/metadata$/.exec(path);
    if (match) {
      reads.push(match[1]!);
      if (match[1] === 'two' && ++readTwo === 3) throw new Error('preview unavailable');
    }
    if (path === '/v1/jobs' && fake.jobs.length) failed.splice(failed.indexOf('two'), 1);
    const result = fake.answer(path, method, raw);
    if (result === undefined) throw new Error('unexpected ' + path);
    return result as never;
  });
  try {
    await controller.openEditor();
    const draft = editorCoreDraft({fields: {year: '2026'}, lists: {}, lockEdited: true});
    controller.setEditorDraft(draft); await controller.saveEditor();
    await controller.retryEditor(draft, ['two']);
    assert.equal(controller.getSnapshot().editor!.phase, 'error');
    const retained = controller.getSnapshot().editor!.receipts[0];
    await retryEditorRead(controller, controller.getSnapshot().editor!.phase);
    const editor = controller.getSnapshot().editor!;
    assert.deepEqual(reads, ['one', 'two', 'three', 'one', 'two', 'three', 'two', 'two']);
    assert.deepEqual(fake.jobs.map(body => body.target.items), [['one', 'two', 'three'], ['two']]);
    assert.equal(editor.receipts[0], retained); assert.deepEqual(editor.draft, draft);
    assert.equal(editor.receipts.length, 2);
    assert.deepEqual(editorResultView(editor.receipts)?.results.map(row => [row.id, row.ok]), [['one', true], ['two', true], ['three', false]]);
  } finally {controller.dispose();}
});
test('shell uncertain retry repeats the original edit without reloading the preview', async () => {
  const fake = rebuiltMetadata({one: {}, two: {}, three: {}}, {lose: 1});
  const controller = editorController(async (path, method, body) => {
    const result = fake.answer(path, method, body); if (result === undefined) throw new Error('unexpected ' + path); return result as never;
  });
  try {
    await controller.openEditor(); controller.setEditorDraft(editorCoreDraft({fields: {year: '2026'}, lists: {}, lockEdited: true})); await controller.saveEditor();
    assert.equal(controller.getSnapshot().editor!.phase, 'uncertain');
    await retryEditorRead(controller, controller.getSnapshot().editor!.phase);
    assert.deepEqual(fake.reads, ['one', 'two', 'three']);
    assert.deepEqual(fake.jobs[0], fake.jobs[1]); assert.equal(controller.getSnapshot().editor!.receipts.length, 1);
  } finally {controller.dispose();}
});
