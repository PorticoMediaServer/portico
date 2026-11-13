import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeDestinationPins} from '../src-next/screens/destinations/pins.ts';
import type {PinsApi} from '../../../packages/client-core/src/library-pins.ts';

const resource = {kind: 'collection', id: 'actual', name: 'Actual collection', pinRevision: 0};
const preferences = (libraries = ['a'], hasResource = true) => ({effective: {pins: [...libraries.map(id => 'library:' + id), ...(hasResource ? ['collection:actual'] : [])]}});
const list = {id: resource.id, kind: resource.kind, name: resource.name};
const options = {serverId: 'server', current: () => true, libraryIds: () => ['a', 'b', 'c'], requestId: async () => 'actual-operation-id'};

test('pinning preserves saved resources and publishes the confirmed profile preference', async () => {
  let stored = preferences();
  const writes: unknown[] = [];
  const api: PinsApi = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path === '/v1/lists/actual') return list as T;
    assert.ok(path.startsWith('/v1/preferences'));
    if (method === 'PATCH') {
      writes.push(body);
      assert.deepEqual(body, {scope: 'profile-server', deviceClass: 'web', values: {pins: ['library:c', 'library:a', 'collection:actual', 'library:b']}});
      stored = {effective: {pins: (body as {values: {pins: string[]}}).values.pins}};
    }
    return stored as T;
  }};
  const controller = new NativeDestinationPins({...options, api});
  try {
    const initial = controller.load(); assert.equal(initial, controller.load()); await initial;
    stored = preferences(['c', 'a']); // Another device changed the pins before this tap.
    let duplicate: Promise<void> | undefined;
    const off = controller.subscribe(() => {if (controller.getSnapshot().busy) duplicate = controller.pinLibrary('b', true);});
    const saving = controller.pinLibrary('b', true);
    assert.deepEqual(controller.getSnapshot().libraries?.pinnedLibraryIds, ['a'], 'no optimistic arrangement');
    assert.equal(duplicate, saving, 'subscriber reentry borrows the in-flight operation');
    await saving; off();
    assert.equal(writes.length, 1);
    assert.deepEqual(controller.getSnapshot().libraries, {revision: 0, pinnedLibraryIds: ['c', 'a', 'b']});
    assert.equal(controller.getSnapshot().busy, false);
  } finally {controller.dispose();}
});

test('a removed pin is not written again, and a delayed native request ID cannot write for a retired viewer', async () => {
  let current = true, hasResource = true, issued = 0, release!: (id: string) => void;
  let entered!: () => void; const requestStarted = new Promise<void>(resolve => {entered = resolve;});
  const writes: unknown[] = [];
  const api: PinsApi = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (method === 'PATCH') writes.push(body);
    return (path === '/v1/lists/actual' ? list : preferences(['a'], hasResource)) as T;
  }};
  const controller = new NativeDestinationPins({...options, api, current: () => current, requestId: () => {
    issued++; return new Promise<string>(resolve => {release = resolve; entered();});
  }});
  await controller.load(); const entry = controller.getSnapshot().resources!.order[0];
  hasResource = false; await controller.unpinResource(entry);
  assert.equal(issued, 0); assert.deepEqual(writes, [], 'absent current pin never falls back to the old row');
  hasResource = true; await controller.load();
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
  const api: PinsApi = {request: <T>(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> => new Promise(resolve => {
    pending.push({signal, finish: () => resolve(preferences() as T)});
  })};
  const controller = new NativeDestinationPins({...options, api, current: () => current});
  const loading = controller.load(); await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(pending.length, 2); current = false; const retired = controller.getSnapshot();
  assert.ok(pending.every(request => request.signal?.aborted));
  pending.forEach(request => request.finish()); await loading;
  assert.equal(controller.getSnapshot(), retired); assert.equal(retired.libraries, undefined);
});

test('a drag drop places the real library before its captured target identity using the latest preference', async () => {
  let stored = preferences(['a', 'b', 'c']);
  const writes: unknown[] = [];
  const api: PinsApi = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path === '/v1/lists/actual') return list as T;
    if (method === 'PATCH') {
      writes.push(body);
      assert.deepEqual(body, {scope: 'profile-server', deviceClass: 'web', values: {pins: ['library:a', 'library:c', 'library:b', 'collection:actual']}});
      stored = preferences(['a', 'c', 'b']);
    }
    return stored as T;
  }};
  const controller = new NativeDestinationPins({...options, api});
  try {
    await controller.load();
    stored = preferences(['c', 'a', 'b']);
    await controller.placeLibrary('a', 'c');
    assert.equal(writes.length, 1);
    assert.deepEqual(controller.getSnapshot().libraries, {revision: 0, pinnedLibraryIds: ['a', 'c', 'b']});
    await controller.placeLibrary('a', 'c'); assert.equal(writes.length, 1, 'an unchanged position does not make a second write');
  } finally {controller.dispose();}
});
