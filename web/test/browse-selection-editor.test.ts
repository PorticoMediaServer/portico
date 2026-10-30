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
  const editor = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => 'op', isCurrent: () => true, serverId: 'server', viewerFence: () => preview.viewerFence});
  const loaded = await editor.preview({target: explicit} as never);
  assert.equal(loaded.total, 1);
  assert.match(paths[0] ?? '', /metadata\/bulk\/preview/);
  const retired = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => 'op', isCurrent: () => false, serverId: 'server', viewerFence: () => preview.viewerFence});
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
  const editor = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, serverId: 'server', viewerFence: () => preview.viewerFence});
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
