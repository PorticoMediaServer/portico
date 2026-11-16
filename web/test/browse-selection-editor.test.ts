import test from 'node:test';
import assert from 'node:assert/strict';
import {createSelectionEditor, editorPreviewRequest, editorTargetLawful, SelectionEditorError} from '../src/screens/browse/selection-editor.ts';
import {rebuiltMetadata} from '../../packages/client-core/test/fixtures/rebuilt-metadata.ts';

const capabilities = {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: false, edit: true, refresh: true, delete: true};
const item = {kind: 'movie', id: 'm1', libraryId: 'a', capabilities};
const explicit = {mode: 'explicit' as const, context: {}, items: [{key: 'k', item}], displayItems: [{key: 'k', item}]};
const query = {mode: 'query' as const, context: {}, queryId: 'q', selector: {query: {libraryId: 'a', pivot: 'movies', filter: {variant:'predicate',field: 'year', operator: 'at-least', value: 2000}, sort: [{field: 'title', direction: 'asc'}]}},
  total: 48000, sourceTotal: 48000, refinement: {kind: null, excludedItems: []}, summary: {}, expected: {catalogRevision: 'revision-1'}};
const fake = () => rebuiltMetadata({m1: {title: 'Film', fields: {contentRating: 'G'}, revision: '7'}});
const apiOf = (server: ReturnType<typeof rebuiltMetadata>) => ({request: async (path: string, method?: string, body?: unknown) => {
  const value = server.answer(path, method, body); if (value === undefined) throw new Error('unexpected ' + path); return value;}});

test('explicit picks preview their exact identities and a whole view is refused', () => {
  assert.deepEqual(editorPreviewRequest(explicit as never), {targets: [{kind: 'item', contentKind: 'movie', id: 'm1', libraryId: 'a'}]});
  assert.equal(editorTargetLawful(explicit as never), true);
  assert.equal(editorTargetLawful(null), false);
  // This direct explicit producer refuses a query; the separate query editor
  // keeps the whole-view selector instead of approximating it with loaded ids.
  assert.throws(() => editorPreviewRequest(query as never), (error: unknown) => error instanceof SelectionEditorError && error.code === 'whole-view-editor-unavailable');
  assert.equal(editorTargetLawful(query as never), false);
});

test('the preview reads the title\'s own document on the rebuilt server, and the editor refuses when not current', async () => {
  const server = fake();
  const editor = createSelectionEditor({api: apiOf(server) as never, events: {on: () => () => {}}, requestId: async () => 'op', isCurrent: () => true, serverId: 'server'});
  const loaded = await editor.preview({target: explicit} as never);
  assert.equal(loaded.total, 1); assert.deepEqual(server.reads, ['m1']);
  assert.deepEqual([loaded.fields.contentRating?.state, loaded.fields.contentRating?.value], ['shared', 'G']);
  const retired = createSelectionEditor({api: apiOf(server) as never, events: {on: () => () => {}}, requestId: async () => 'op', isCurrent: () => false, serverId: 'server'});
  await assert.rejects(retired.preview({target: explicit} as never), (error: unknown) => error instanceof SelectionEditorError && error.code === 'cancelled');
});

test('a save is one edit_items job for the previewed titles and returns per-title receipts', async () => {
  const server = fake();
  const editor = createSelectionEditor({api: apiOf(server) as never, events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, serverId: 'server'});
  const operation = {target: explicit} as never;
  const loaded = await editor.preview(operation);
  const receipts = await editor.save(operation, loaded, {fields: {contentRating: 'PG'}, lists: {}, lockEdited: true});
  assert.equal(receipts[0]?.updated, 1); assert.equal(receipts[0]?.results[0]?.ok, true);
  assert.deepEqual(server.jobs, [{kind: 'edit_items', target: {items: ['m1']}, params: {contentRating: 'PG', lock: true}}]);
});
