import test from 'node:test';
import assert from 'node:assert/strict';
import {createSelectionEditor, editorPreviewRequest, editorTargetLawful, SelectionEditorError} from '../src/screens/browse/selection-editor.ts';

const capabilities = {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: false, edit: true, refresh: true, delete: true};
const item = {kind: 'movie', id: 'm1', libraryId: 'a', capabilities};
const explicit = {mode: 'explicit' as const, context: {}, items: [{key: 'k', item}], displayItems: [{key: 'k', item}]};
const query = {mode: 'query' as const, context: {}, queryId: 'q', selector: {query: {libraryId: 'a', pivot: 'movies', filter: {variant:'predicate',field: 'year', operator: 'at-least', value: 2000}, sort: [{field: 'title', direction: 'asc'}]}},
  total: 48000, sourceTotal: 48000, refinement: {kind: null, excludedItems: []}, summary: {}, expected: {catalogRevision: 'revision-1'}};
const spec = {field: 'contentRating', label: 'Content rating', type: 'text', bulk: true, group: 'general', maxLength: 300};
const preview = {variant: 'ready', serverId: 'server', viewerFence: 'a'.repeat(64), contentKind: 'movie', kind: 'item', total: 1, schema: [spec], singleItemFields: [spec], excludedFields: [],
  fields: {contentRating: {state: 'shared', distinctCount: 1, value: 'G', values: [{value: 'G', count: 1}], entries: [], locked: 0, truncated: false}},
  targets: [{kind: 'item', id: 'm1', libraryId: 'a', expectedRevision: 'b'.repeat(64)}]};
const queryPreview = {variant: 'ready', serverId: 'server', viewerFence: 'a'.repeat(64), contentKind: 'movie', kind: 'item', total: 48000, selectionRevision: 'revision-1', schema: [], singleItemFields: [], excludedFields: [], fields: {}, targets: []};

test('explicit picks preview their exact identities and a whole view is refused', () => {
  assert.deepEqual(editorPreviewRequest(explicit as never), {targets: [{kind: 'item', contentKind: 'movie', id: 'm1', libraryId: 'a'}]});
  assert.equal(editorTargetLawful(explicit as never), true);
  assert.equal(editorTargetLawful(null), false);
  // This direct explicit producer refuses a query; the separate query editor
  // keeps the whole-view selector instead of approximating it with loaded ids.
  assert.throws(() => editorPreviewRequest(query as never), (error: unknown) => error instanceof SelectionEditorError && error.code === 'whole-view-editor-unavailable');
  assert.equal(editorTargetLawful(query as never), false);
});

test('the preview reads through the real endpoint and the editor refuses when not current', async () => {
  const paths: string[] = [], bodies: unknown[] = [];
  const api = {request: async (path: string, _method?: string, body?: unknown) => {paths.push(path); bodies.push(body); return (body as {targets?: unknown}).targets ? preview : queryPreview; }};
  const editor = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => 'op', isCurrent: () => true, serverId: 'server'});
  const loaded = await editor.preview({target: explicit} as never);
  assert.equal(loaded.total, 1);
  assert.match(paths[0] ?? '', /metadata\/bulk\/preview/);
  const retired = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => 'op', isCurrent: () => false, serverId: 'server'});
  await assert.rejects(retired.preview({target: explicit} as never), (error: unknown) => error instanceof SelectionEditorError && error.code === 'cancelled');
});

test('a save sends the direct bulk edit for the previewed targets and returns per-title receipts', async () => {
  const bodies: unknown[] = [];
  const api = {request: async (path: string, _method?: string, body?: unknown) => {
    if (path.includes('preview')) return preview;
    bodies.push(body);
    const edit = body as {operationId: string; targets: {kind: string; id: string}[]};
    return {operationId: edit.operationId, results: edit.targets.map(target => ({kind: target.kind, id: target.id, ok: true, revision: 'c'.repeat(64)})),
      updated: edit.targets.length, failed: 0};
  }};
  const editor = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, serverId: 'server'});
  const operation = {target: explicit} as never;
  const loaded = await editor.preview(operation);
  const receipts = await editor.save(operation, loaded, {fields: {contentRating: 'G'}, lists: {}, lockEdited: true});
  assert.equal(receipts[0]?.updated, 1);
  assert.equal(receipts[0]?.results[0]?.ok, true);
  const body = bodies[0] as {operationId: string; targets: {kind: string; id: string; expectedRevision: string}[]; fields: Record<string, {value: string}>; lists: Record<string, unknown>; lockEdited: boolean};
  assert.equal(body.operationId, 'operation-1');
  assert.deepEqual(body.targets, [{kind: 'item', id: 'm1', expectedRevision: 'b'.repeat(64)}]);
  assert.deepEqual(body.fields, {contentRating: {value: 'G'}});
  assert.deepEqual(body.lists, {});
  assert.equal(body.lockEdited, true);
});

// Step 7: the editor's Try again could never succeed after the viewer's access was decided again. The first preview's
// fence was remembered and every later preview under another one was refused (metadata-authority-changed) for as long as
// the editor lived. A preview read under a moved fence is the newer answer. Saving stays gated on the latest preview's
// fence: a save from an older preview is refused before anything is sent.
test('step 7: Try again after an access change previews under the moved fence, and only the newest preview can save', async () => {
  let fence = 'a'.repeat(64); const sent: unknown[] = [];
  const api = {request: async (path: string, _method?: string, body?: unknown) => {
    if (path.includes('preview')) return {...preview, viewerFence: fence};
    sent.push(body);
    const edit = body as {operationId: string; targets: {kind: string; id: string}[]};
    return {operationId: edit.operationId, results: edit.targets.map(target => ({kind: target.kind, id: target.id, ok: true, revision: 'c'.repeat(64)})), updated: edit.targets.length, failed: 0};
  }};
  const editor = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, serverId: 'server'});
  const operation = {target: explicit} as never;
  const older = await editor.preview(operation);
  fence = 'e'.repeat(64);
  const newer = await editor.preview(operation);
  assert.equal(newer.viewerFence, 'e'.repeat(64), 'the preview under the moved fence is shown');
  // The older preview's grant was issued under the fence that moved: its save is refused, and nothing is sent.
  await assert.rejects(editor.save(operation, older, {fields: {contentRating: 'G'}, lists: {}, lockEdited: true}),
    (error: unknown) => error instanceof SelectionEditorError && error.code === 'metadata-preview-not-current');
  assert.equal(sent.length, 0);
  const receipts = await editor.save(operation, newer, {fields: {contentRating: 'G'}, lists: {}, lockEdited: true});
  assert.equal(receipts[0]?.updated, 1); assert.equal(sent.length, 1);
});
