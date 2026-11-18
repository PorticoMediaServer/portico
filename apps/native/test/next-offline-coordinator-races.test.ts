import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineFixture, preparation, viewer, owner, deferred, settle} from './helpers/offline-fixture.mjs';

test('cancelled pending work cannot appear when an already queued native start publishes later', async () => {
  const f = await offlineFixture(), entered = deferred(), release = deferred();
  let cancelling: Promise<void> | undefined;
  try {
    await f.offline.open(); f.offline.connect(f.api, f.service, viewer); await settle();
    const copy = preparation('queued-native-start');
    f.snapshot.items = [copy];
    const start = f.native.start;
    f.native.start = async request => {entered.resolve(); await release.promise; return start(request);};
    await f.offline.want([copy]); await entered.promise;
    assert.equal(f.entries.has(copy.id), false, 'the queued native start has not published its row yet');
    cancelling = f.offline.cancelPending(copy.id);
    release.resolve(); await cancelling; await settle();
    assert.equal(f.started.length, 1, 'the already issued native start actually completed');
    assert.equal(f.entries.has(copy.id), false, 'cancellation removes the exact late native result');
    assert.deepEqual(f.offline.getSnapshot().entries, []);
    assert.deepEqual(f.offline.getSnapshot().pendingDownloads, []);
  } finally {
    release.resolve();
    try {await cancelling;} finally {f.offline.dispose();}
  }
});

test('parallel collection waits until container membership and its exact preparation are durable', async () => {
  const f = await offlineFixture(), entered = deferred(), release = deferred();
  let first: Promise<void> | undefined, second: Promise<void> | undefined;
  try {
    await f.offline.open(); f.offline.connect(f.api, f.service, viewer); await settle();
    const copy = preparation('durable-container-copy');
    f.snapshot.items = [copy];
    f.snapshot.requests = [{title: 'Show', request: {requestId: 'request', total: 1, totalKnown: true, state: 'complete',
      ready: 1, preparing: 0, queued: 0, failed: 0, paused: 0, nextCursor: '', items: [{itemId: copy.itemId, preparation: copy}]}}];
    const write = f.native.writeState;
    let held = false;
    f.native.writeState = async json => {
      if (!held && JSON.parse(json).enrolledMembers?.[owner]?.request) {held = true; entered.resolve(); await release.promise;}
      await write(json);
    };
    first = f.offline.collect(); await entered.promise;
    second = f.offline.collect(); await settle();
    assert.equal(JSON.parse(f.disk.state || '{}').enrolledMembers?.[owner]?.request, undefined);
    assert.equal(f.started.length, 0, 'an overlapping pass cannot use undurable in-memory member markers');
    release.resolve(); await Promise.all([first, second]); await settle();
    assert.equal(f.started.length, 1);
    assert.equal(JSON.parse(f.disk.state).enrolledMembers[owner].request[copy.itemId], true);
  } finally {
    release.resolve();
    try {await Promise.all([first, second]);} finally {f.offline.dispose();}
  }
});

test('native completion during the opening list read is replayed after its older snapshot', async () => {
  const f = await offlineFixture(), entered = deferred(), release = deferred();
  let opening: Promise<void> | undefined;
  try {
    const copy = f.seed('completed-during-open', 'item', {state: 'downloading', bytesDone: 0});
    const listed = {...copy}, removed = f.seed('removed-during-open');
    f.native.list = async () => {entered.resolve(); await release.promise; return [listed, removed];};
    opening = f.offline.open(); await entered.promise;
    Object.assign(copy, {state: 'ready', bytesDone: 100}); f.emit(copy);
    f.emit({id: removed.id, state: 'removed'});
    release.resolve(); await opening; f.offline.adopt(viewer);
    assert.equal(f.offline.getSnapshot().ready, true);
    assert.deepEqual(f.offline.getSnapshot().entries.map((entry: {id: string}) => entry.id), [copy.id]);
    assert.equal(f.offline.getSnapshot().entries[0].state, 'ready');
    assert.equal(f.offline.getSnapshot().entries[0].bytesDone, 100);
  } finally {
    release.resolve();
    try {await opening;} finally {f.offline.dispose();}
  }
});

test('failed list attempts retire their listeners before a successful retry', async t => {
  const f = await offlineFixture(); t.after(() => f.offline.dispose());
  const list = f.native.list;
  let attempts = 0;
  f.native.list = async () => {if (++attempts <= 3) throw new Error('unreadable store'); return list();};
  for (let i = 0; i < 3; i++) {
    await assert.rejects(f.offline.open(), /unreadable store/);
    assert.equal(f.listeners.size, 0);
  }
  await f.offline.open();
  assert.equal(f.listeners.size, 1);
});

test('disposal while a list is pending retires the listener and keeps the store closed', async () => {
  const f = await offlineFixture(), entered = deferred(), release = deferred();
  f.native.list = async () => {entered.resolve(); await release.promise; return [f.seed('copy')];};
  const opening = f.offline.open();
  await entered.promise;
  assert.equal(f.listeners.size, 1);
  f.offline.dispose();
  assert.equal(f.listeners.size, 0);
  release.resolve(); await opening;
  assert.equal(f.offline.getSnapshot().ready, false);
});
