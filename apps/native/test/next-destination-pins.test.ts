import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeDestinationPins} from '../src-next/screens/destinations/pins.ts';
import type {PinsApi} from '../../../packages/client-core/src/library-pins.ts';

const navigationPath = '/v1/me/library-navigation', pinsPath = '/v1/saved-pins/order';
const resource = {kind: 'collection' as const, id: 'actual', name: 'Actual collection', pinRevision: 4};
const pins = (order = [resource], revision = 1) => ({serverId: 'server', viewerFence: 'actual-viewer', revision, order});
const options = {serverId: 'server', current: () => true, libraryIds: () => ['a', 'b', 'c'], requestId: async () => 'actual-operation-id'};

test('pinning retries the real revision conflict and publishes only the server arrangement', async () => {
  let navigation = {revision: 1, pinnedLibraryIds: ['a']}, conflict = true;
  const writes: unknown[] = [];
  const api: PinsApi = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path === pinsPath) return pins() as T;
    assert.equal(path, navigationPath);
    if (method === 'PUT') {
      writes.push(body);
      if (conflict) {conflict = false; navigation = {revision: 2, pinnedLibraryIds: ['c', 'a']}; throw Object.assign(new Error('changed'), {code: 'revision_conflict'});}
      assert.deepEqual(body, {expectedRevision: 2, pinnedLibraryIds: ['c', 'a', 'b']});
      navigation = {revision: 3, pinnedLibraryIds: ['c', 'a', 'b']};
    }
    return navigation as T;
  }};
  const controller = new NativeDestinationPins({...options, api});
  try {
    const initial = controller.load(); assert.equal(initial, controller.load()); await initial;
    let duplicate: Promise<void> | undefined;
    const off = controller.subscribe(() => {if (controller.getSnapshot().busy) duplicate = controller.pinLibrary('b', true);});
    const saving = controller.pinLibrary('b', true);
    assert.deepEqual(controller.getSnapshot().libraries?.pinnedLibraryIds, ['a'], 'no optimistic arrangement');
    assert.equal(duplicate, saving, 'subscriber reentry borrows the in-flight operation');
    await saving; off();
    assert.equal(writes.length, 2);
    assert.deepEqual(controller.getSnapshot().libraries, navigation);
    assert.equal(controller.getSnapshot().busy, false);
  } finally {controller.dispose();}
});

test('a removed pin cannot reuse an old revision, and a delayed native request ID cannot write for a retired viewer', async () => {
  let current = true, order = [resource], issued = 0, release!: (id: string) => void;
  let entered!: () => void; const requestStarted = new Promise<void>(resolve => {entered = resolve;});
  const writes: unknown[] = [];
  const api: PinsApi = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (method === 'PUT') {writes.push(body); return {pinned: false, pinRevision: 5} as T;}
    return (path === navigationPath ? {revision: 1, pinnedLibraryIds: ['a']} : pins(order)) as T;
  }};
  const controller = new NativeDestinationPins({...options, api, current: () => current, requestId: () => {
    issued++; return new Promise<string>(resolve => {release = resolve; entered();});
  }});
  await controller.load(); const entry = controller.getSnapshot().resources!.order[0];
  order = []; await controller.unpinResource(entry);
  assert.equal(issued, 0); assert.deepEqual(writes, [], 'absent current pin never falls back to the old row');
  order = [resource]; await controller.load();
  const next = controller.unpinResource(controller.getSnapshot().resources!.order[0]);
  await requestStarted;
  current = false; const retired = controller.getSnapshot(); release('actual-operation-id'); await next;
  assert.deepEqual(writes, []); assert.equal(controller.getSnapshot(), retired);
  assert.equal(retired.resources, undefined);
  controller.dispose();
});

test('retiring an in-flight pin read aborts its actual request and never publishes a late response', async () => {
  let current = true;
  const pending: {signal?: AbortSignal; finish: () => void}[] = [];
  const api: PinsApi = {request: <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> => new Promise(resolve => {
    pending.push({signal, finish: () => resolve((path === navigationPath ? {revision: 1, pinnedLibraryIds: ['a']} : pins()) as T)});
  })};
  const controller = new NativeDestinationPins({...options, api, current: () => current});
  const loading = controller.load(); await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(pending.length, 2); current = false; const retired = controller.getSnapshot();
  assert.ok(pending.every(request => request.signal?.aborted));
  pending.forEach(request => request.finish()); await loading;
  assert.equal(controller.getSnapshot(), retired); assert.equal(retired.libraries, undefined);
});

test('a drag drop places the real library before its captured target identity under the latest CAS revision', async () => {
  let navigation = {revision: 1, pinnedLibraryIds: ['a', 'b', 'c']};
  const writes: unknown[] = [];
  const api: PinsApi = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path === pinsPath) return pins() as T;
    if (method === 'PUT') {
      writes.push(body); assert.deepEqual(body, {expectedRevision: 2, pinnedLibraryIds: ['a', 'c', 'b']});
      navigation = {revision: 3, pinnedLibraryIds: ['a', 'c', 'b']};
    }
    return navigation as T;
  }};
  const controller = new NativeDestinationPins({...options, api});
  try {
    await controller.load();
    navigation = {revision: 2, pinnedLibraryIds: ['c', 'a', 'b']};
    await controller.placeLibrary('a', 'c');
    assert.equal(writes.length, 1);
    assert.deepEqual(controller.getSnapshot().libraries, navigation);
    await controller.placeLibrary('a', 'c'); assert.equal(writes.length, 1, 'an unchanged position does not make a second write');
  } finally {controller.dispose();}
});
