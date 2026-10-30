import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLibrarySaveView} from '../src-next/screens/library/save-view.ts';
import type {SavedApi} from '../../../packages/client-core/src/saved.ts';

const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
const definition = {libraryId: 'actual-movies', pivot: 'movies', query: {field: 'favorite', operator: 'equals', value: true}, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'} as const;
const directory = () => ({serverId: 'server', viewerFence: 'actual-viewer', revision: 1, resources: [], nextCursor: ''});
const operationId = '12345678-1234-4123-8123-123456789abc';

test('Save view trims the real name, preserves the captured definition and closes only after the canonical receipt', async () => {
  const bodies: any[] = [], saved: string[] = [];
  const api: SavedApi = {request: async <T>(_path: string, method?: string, body?: unknown) => {
    if (method !== 'POST') return directory() as T;
    bodies.push(body); return {serverId: 'server', viewerFence: 'actual-viewer', receipt: {operationId, revision: 2, resourceId: 'created-view', deleted: false}} as T;
  }};
  const controller = new NativeLibrarySaveView({api, scope, definition, current: () => true, requestId: async () => operationId, saved: id => {saved.push(id);}});
  try {
    await controller.save(); assert.equal(bodies.length, 0);
    controller.setName('  Actual saved name  ');
    await Promise.all([controller.save(), controller.save()]);
    assert.equal(bodies.length, 1); assert.equal(bodies[0].name, 'Actual saved name'); assert.deepEqual(bodies[0].definition, definition);
    assert.deepEqual(saved, ['created-view']); assert.equal(controller.getSnapshot().open, false); assert.equal(controller.getSnapshot().name, '');
  } finally {controller.dispose();}
});

test('an uncertain Save retry retains the name and exact operation body, and viewer retirement refuses late success', {timeout: 10000}, async () => {
  const bodies: any[] = []; let fail = true, current = true, finish: (() => void) | undefined;
  const api: SavedApi = {request: async <T>(_path: string, method?: string, body?: unknown) => {
    if (method !== 'POST') return directory() as T;
    bodies.push(body);
    if (fail) throw Object.assign(new Error('offline'), {code: 'network_error', retryable: true});
    await new Promise<void>(resolve => {finish = resolve;});
    return {serverId: 'server', viewerFence: 'actual-viewer', receipt: {operationId, revision: 2, resourceId: 'created-view', deleted: false}} as T;
  }};
  let saved = 0;
  const controller = new NativeLibrarySaveView({api, scope, definition, current: () => current, requestId: async () => operationId, saved: () => {saved++;}});
  controller.setName('Keep this name'); await controller.save();
  assert.equal(controller.getSnapshot().open, true); assert.equal(controller.getSnapshot().name, 'Keep this name');
  assert.ok(controller.getSnapshot().error); assert.equal(controller.getSnapshot().nameLocked, true);
  controller.setName('Do not change an unresolved operation'); assert.equal(controller.getSnapshot().name, 'Keep this name');
  fail = false; const retry = controller.save();
  while (!finish) await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(bodies[1], bodies[0]);
  current = false; assert.equal(controller.getSnapshot().open, false); assert.equal(controller.getSnapshot().name, '');
  finish(); await retry; assert.equal(saved, 0);
});

test('a failed initial directory read retries, while a failed post-receipt refresh cannot create a duplicate view', async () => {
  let reads = 0, posts = 0, saved = 0;
  const api: SavedApi = {request: async <T>(_path: string, method?: string) => {
    if (method === 'POST') {posts++; return {serverId: 'server', viewerFence: 'actual-viewer', receipt: {operationId, revision: 2, resourceId: 'created-view', deleted: false}} as T;}
    reads++;
    if (reads === 1 || reads > 2) throw Object.assign(new Error('offline'), {code: 'network_error'});
    return directory() as T;
  }};
  const controller = new NativeLibrarySaveView({api, scope, definition, current: () => true, requestId: async () => operationId, saved: () => {saved++;}});
  try {
    controller.setName('Preserved name'); await controller.save();
    assert.equal(controller.getSnapshot().open, true); assert.equal(controller.getSnapshot().name, 'Preserved name'); assert.equal(posts, 0);
    await controller.save(); assert.equal(posts, 1); assert.equal(saved, 1); assert.equal(controller.getSnapshot().open, false);
    await controller.save(); assert.equal(posts, 1);
  } finally {controller.dispose();}
});
