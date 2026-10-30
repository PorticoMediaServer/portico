import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeBulkMedia} from '../src-next/screens/destinations/bulk-media.ts';
import {bulkFieldHint, bulkFieldStateLabel} from '../src-next/kit/selection/presentation.ts';
import type {CatalogueSelectionOptions} from '../../../packages/client-core/src/catalogue-selection.ts';
import type {PreparationResponse} from '../../../packages/client-core/src/read-preparation.ts';

const tick = async () => {for (let i = 0; i < 30; i++) await Promise.resolve();};
const path = '/v1/metadata/bulk/preview', poll = `${path}-queries/private-1`;
const entries = Array.from({length: 70}, (_, index) => ({id: String(index), libraryId: 'library', kind: 'movie' as const, title: `Title ${index}`}));
const many = {state: 'many' as const, atLeast: 65, values: [], entries: [{value: 'Preserved', count: 17}], locked: 23, truncated: true};
const field = {field: 'studio', label: 'Studio', group: 'general', type: 'text', bulk: true};
const ready = {variant: 'ready', serverId: 'server', viewerFence: 'a'.repeat(64), kind: 'item', contentKind: 'movie', total: 70,
  schema: [field], singleItemFields: [field], excludedFields: [], fields: {studio: many},
  targets: entries.map(entry => ({kind: 'item', id: entry.id, libraryId: entry.libraryId, expectedRevision: 'b'.repeat(64)}))};
const preparing = {variant: 'preparing', kind: 'metadata-preview-preparing', state: 'preparing', queryId: 'private-1', retryAfterMs: 1000};
function fixture(raw: (path: string, method: string, options?: any) => Promise<PreparationResponse>) {
  let current = true; const life = new AbortController();
  const api = {request: async () => {throw Error('body-only transport used');}, requestRaw: raw};
  const bulk = new NativeBulkMedia({api, scope: {serverId: 'server', viewerId: 'owner'}, requestId: async () => 'request-id', signal: life.signal, isCurrent: () => current} as unknown as CatalogueSelectionOptions, entries);
  return {bulk, retire() {current = false; life.abort();}, life};
}

test('native many presentation preserves the lower bound without formatting it as an exact count', () => {
  assert.equal(bulkFieldStateLabel(many), 'Many values');
  assert.equal(bulkFieldHint(many, 70, false), ''); assert.equal(bulkFieldHint(many, 70, true), 'was Mixed');
  assert.equal(bulkFieldHint({state: 'mixed', distinctCount: 2, values: [], entries: [], truncated: false}, 70, false), '2 values');
  assert.equal(bulkFieldHint({state: 'shared', distinctCount: 1, value: '', values: [], entries: [], truncated: false}, 70, false), 'All 70');
});
test('NativeBulkMedia forwards actual raw status and preserves many, entries and exact locks', async () => {
  const calls: string[] = [];
  const f = fixture(async (target, method) => {calls.push(`${method} ${target}`); return {status: 200, headers: {}, body: ready};});
  try {
    await f.bulk.openEditor();
    const snapshot = f.bulk.getSnapshot(); assert.equal(snapshot.phase, 'ready');
    assert.deepEqual(snapshot.values.studio, many); assert.equal(snapshot.preview!.fields.studio!.locked, 23);
    assert.deepEqual(snapshot.changes, {}); assert.equal(snapshot.lockEdited, true);
    assert.deepEqual(calls, [`POST ${path}`]);
  } finally {f.bulk.dispose();}
});
test('native preparing never publishes partial fields; closing sends private DELETE and no write', async () => {
  const calls: string[] = [], snapshots: string[] = [];
  const f = fixture(async (target, method, options): Promise<PreparationResponse> => {calls.push(`${method} ${target}`); if (method === 'POST') return {status: 202, headers: {Location: poll, 'Retry-After': '1'}, body: preparing};
    assert.equal(method, 'DELETE'); assert.equal(options.signal, undefined); assert.equal(options.singleAttempt, true); return {status: 204, headers: {}};});
  const unsubscribe = f.bulk.subscribe(() => snapshots.push(f.bulk.getSnapshot().phase));
  try {
    const work = f.bulk.openEditor(); await tick();
    assert.equal(f.bulk.getSnapshot().phase, 'loading'); assert.equal(f.bulk.getSnapshot().preview, undefined); assert.deepEqual(f.bulk.getSnapshot().values, {});
    f.bulk.close(); await work; await tick();
    assert.equal(f.bulk.getSnapshot().phase, 'idle'); assert.ok(!snapshots.includes('ready')); assert.deepEqual(calls, [`POST ${path}`, `DELETE ${poll}`]);
  } finally {unsubscribe(); f.bulk.dispose();}
});
test('native close releases a late pending handle and cannot revive the editor', async () => {
  let finish!: (response: PreparationResponse) => void; const calls: string[] = [];
  const f = fixture(async (target, method) => {calls.push(`${method} ${target}`); return method === 'POST' ? new Promise(resolve => {finish = resolve;}) : {status: 204, headers: {}};});
  try {
    const work = f.bulk.openEditor(); await tick(); f.bulk.close(); await work;
    finish({status: 202, headers: {Location: poll}, body: preparing}); await tick();
    assert.equal(f.bulk.getSnapshot().phase, 'idle'); assert.deepEqual(calls, [`POST ${path}`, `DELETE ${poll}`]);
  } finally {f.bulk.dispose();}
});
test('native raw 202 with a ready-shaped body is refused rather than displaying partial success', async () => {
  const f = fixture(async () => ({status: 202, headers: {}, body: ready}));
  try {await f.bulk.openEditor(); assert.equal(f.bulk.getSnapshot().phase, 'error'); assert.equal(f.bulk.getSnapshot().preview, undefined);}
  finally {f.bulk.dispose();}
});

test('native authority retirement cancels preparing immediately rather than awaiting the next poll', async () => {
  const calls: string[] = [];
  const f = fixture(async (target, method) => {calls.push(`${method} ${target}`); return method === 'POST' ? {status: 202, headers: {}, body: preparing} : {status: 204, headers: {}};});
  try {
    const work = f.bulk.openEditor(); await tick(); f.retire(); await work; await tick();
    assert.equal(f.bulk.getSnapshot().preview, undefined); assert.deepEqual(calls, [`POST ${path}`, `DELETE ${poll}`]);
  } finally {f.bulk.dispose();}
});
