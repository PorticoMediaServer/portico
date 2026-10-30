import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {device, NativeModules} from './react-native-stub.mjs';
import {OfflineDownloads, offlineOwnerKey} from '../platform/offline-downloads.ts';

const viewer = {authority: 'local', accountId: 'account', profileId: 'profile', serverId: 'server'};
const owner = offlineOwnerKey(viewer), native = NativeModules.PorticoDownloads, originalWrite = native.writeState;
const stores: OfflineDownloads[] = [];
afterEach(() => {for (const store of stores.splice(0)) store.dispose(); native.writeState = originalWrite; device.reset();});
const preparation = (id: string) => ({id, itemId: `item-${id}`, libraryId: 'library', profileId: 'profile', quality: 'original', origin: 'manual', state: 'queued',
  artifact: {kind: '', sha256: '', bytes: 0, estimated: true}, title: {kind: 'movie', title: id, onServer: true},
  actions: ['cancel'], revision: 1, createdAt: '2026-10-28T00:00:00Z', updatedAt: '2026-10-28T00:00:00Z', readyAt: '', expiresAt: ''});

async function fixture() {
  device.reset();
  const store = new OfflineDownloads(); stores.push(store); store.adopt(viewer); await store.open();
  const internal = store as any, calls: string[] = [], items: any[] = [];
  let response: any = {items: [], nextCursor: '', closed: true};
  const service = {getSnapshot: () => ({items, requests: [], nextCursor: ''})};
  internal.service = service;
  internal.api = {request: async (path: string) => {
    calls.push(path); assert.ok(path.startsWith('/v1/downloads/preparations?'), 'a persisted terminal latch survives missing command history');
    const query = new URL(path, 'https://fixture.invalid').searchParams;
    assert.equal(query.get('operationId'), 'operation'); assert.equal(query.get('deviceId'), 'device');
    assert.equal(query.get('limit'), '20'); return typeof response === 'function' ? response() : response;
  }};
  const restart = async () => {
    const api = internal.api;
    store.dispose();
    const next = new OfflineDownloads(); stores.push(next); next.adopt(viewer); await next.open();
    const current = next as any; current.service = service; current.api = api;
    return {store: next, current};
  };
  return {store, internal, calls, items, service, restart, response: (value: any) => {response = value;},
    seedTerminal: async () => {
      await store.followSelectionAction('operation', 'device');
      const state = JSON.parse(device.state); state.selectionActions[owner].operation.terminal = true;
      await originalWrite(JSON.stringify(state)); return restart();
    }};
}

test('actual native queue durably records the original op/device before transport and refuses unwritable confirmation', async () => {
  const f = await fixture();
  await f.store.followSelectionAction('operation', 'device');
  const marker = JSON.parse(device.state).selectionActions[owner].operation;
  assert.deepEqual(marker, {operationId: 'operation', deviceId: 'device', cursor: ''}); assert.equal(f.calls.length, 0);
  await f.store.followSelectionAction('operation', 'device');
  assert.equal(Object.keys(JSON.parse(device.state).selectionActions[owner]).length, 1);
  await assert.rejects(f.store.followSelectionAction('operation', 'other-device'));
  native.writeState = async () => {throw new Error('disk full');};
  await assert.rejects(f.store.followSelectionAction('unwritten-operation', 'device'));
  assert.equal(JSON.parse(device.state).selectionActions[owner]['unwritten-operation'], undefined);
  assert.equal(f.calls.length, 0);
});

test('actual native queue restart preserves terminal proof and commits exact wants and sparse-page cursor in one native write', async () => {
  const f = await fixture(), {store, current} = await f.seedTerminal();
  const writes: any[] = [];
  native.writeState = async (json: string) => {writes.push(JSON.parse(json)); await originalWrite(json);};
  f.response({items: [preparation('prep')], nextCursor: 'cursor-2', closed: true});
  await current.enrollSelectionActions(owner, f.service, current.generation);
  assert.equal(writes.length, 1);
  assert.equal(writes[0].wanted[owner].prep.itemId, 'item-prep');
  assert.equal(writes[0].selectionActions[owner].operation.cursor, 'cursor-2');
  assert.equal(writes[0].selectionActions[owner].operation.terminal, true);
  f.response({items: [], nextCursor: 'cursor-3', closed: true});
  await current.enrollSelectionActions(owner, f.service, current.generation);
  assert.equal(JSON.parse(device.state).selectionActions[owner].operation.cursor, 'cursor-3');
  f.response({items: [], nextCursor: '', closed: true});
  await current.enrollSelectionActions(owner, f.service, current.generation);
  assert.equal(JSON.parse(device.state).selectionActions[owner].operation, undefined);
  assert.equal(JSON.parse(device.state).wanted[owner].prep.itemId, 'item-prep');
  assert.equal(f.calls.length, 3); assert.equal(device.started.length, 0, 'server admission is not device transfer completion');
  store.dispose();
});

test('actual native queue fences read-ahead and transfers after an atomic state write failure, then resumes the same paired state', async () => {
  const f = await fixture(), {current} = await f.seedTerminal();
  f.response({items: [preparation('prep')], nextCursor: '', closed: true}); f.items.push(preparation('prep'));
  native.writeState = async () => {throw new Error('disk full');};
  await current.enrollSelectionActions(owner, f.service, current.generation);
  assert.equal(current.selectionActionDirty, true); assert.equal(f.calls.length, 1);
  assert.ok(JSON.parse(device.state).selectionActions[owner].operation);
  assert.equal(JSON.parse(device.state).wanted[owner], undefined);
  await current.collectWanted(current.generation);
  assert.equal(f.calls.length, 1); assert.equal(device.started.length, 0);
  native.writeState = originalWrite;
  await current.collectWanted(current.generation);
  assert.equal(current.selectionActionDirty, false);
  assert.equal(JSON.parse(device.state).selectionActions[owner].operation, undefined);
  assert.equal(JSON.parse(device.state).wanted[owner].prep.itemId, 'item-prep');
  assert.equal(f.calls.length, 1);
});

test('actual native queue cannot enroll a late page into a successor profile', async () => {
  const f = await fixture(), {store, current} = await f.seedTerminal();
  let finish!: (value: unknown) => void;
  f.response(() => new Promise(resolve => {finish = resolve;}));
  const reading = current.enrollSelectionActions(owner, f.service, current.generation);
  await new Promise<void>(resolve => setImmediate(resolve));
  store.adopt({...viewer, profileId: 'successor'});
  finish({items: [preparation('prep')], nextCursor: '', closed: true}); await reading;
  assert.equal(JSON.parse(device.state).wanted[owner], undefined);
  assert.equal(current.stored.wanted[offlineOwnerKey({...viewer, profileId: 'successor'})], undefined);
  assert.ok(JSON.parse(device.state).selectionActions[owner].operation);
});

test('actual native queue recovers without a command summary and ignores all inventory until the scoped header is closed', async () => {
  const f = await fixture(); await f.store.followSelectionAction('operation', 'device');
  f.response({items: [preparation('prep')], nextCursor: '', closed: false});
  await f.internal.enrollSelectionActions(owner, f.service, f.internal.generation);
  assert.equal(JSON.parse(device.state).wanted[owner], undefined);
  assert.equal(JSON.parse(device.state).selectionActions[owner].operation.terminal, undefined);
  const writes: any[] = [];
  native.writeState = async (json: string) => {writes.push(JSON.parse(json)); await originalWrite(json);};
  f.response({items: [preparation('prep')], nextCursor: '', closed: true});
  await f.internal.enrollSelectionActions(owner, f.service, f.internal.generation);
  assert.equal(writes.length, 2, 'closure proof then atomic wants/EOF are both durable');
  assert.equal(writes[0].selectionActions[owner].operation.terminal, true);
  assert.equal(writes[1].selectionActions[owner].operation, undefined);
  assert.equal(writes[1].wanted[owner].prep.itemId, 'item-prep');
  assert.equal(f.calls.length, 2, 'only scoped inventory reads, never the expired aggregate status');
});

test('actual native queue preserves an unresolved command when the scoped transfer header is missing', async () => {
  const f = await fixture(); await f.store.followSelectionAction('operation', 'device');
  f.response(() => Promise.reject(Object.assign(new Error('header absent'), {status: 404, code: 'not_found'})));
  await f.internal.enrollSelectionActions(owner, f.service, f.internal.generation);
  assert.ok(JSON.parse(device.state).selectionActions[owner].operation);
  assert.equal(JSON.parse(device.state).wanted[owner], undefined);
  assert.ok(f.store.getSnapshot().pendingDownloads?.some(row => row.id === 'selection-action:operation'));
});
