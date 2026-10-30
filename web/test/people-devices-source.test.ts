import test from 'node:test';
import assert from 'node:assert/strict';
import {PersonDevicesSource, type PeopleDevicesApi} from '../src/screens/settings/people-devices-source.ts';
import type {AccessDevice, AdministrationPage} from '@core/server-administration.ts';

const device = (id: string, accountId = 'person'): AccessDevice => ({id, accountId, username: 'Member', profileId: 'profile', name: id, platform: 'web', app: 'Portico', appVersion: '1', lastNetwork: 'home', liveState: 'idle', firstSeenAt: '2026-10-01T00:00:00Z', lastSeenAt: '2026-10-27T00:00:00Z', revision: 1});
const page = (items: readonly AccessDevice[], nextCursor = ''): AdministrationPage<AccessDevice> => ({items, nextCursor, limit: 100});
function fixture() {
  let current = true, allowed = true;
  const reads: {accountId: string; cursor: string; signal: AbortSignal}[] = [], writes: {accountId: string; deviceId?: string}[] = [];
  let result = page([device('one')]);
  let held: Promise<AdministrationPage<AccessDevice>> | undefined;
  const api: PeopleDevicesApi = {
    list: async (accountId, cursor, signal) => {reads.push({accountId, cursor, signal}); return held ?? result;},
    signOut: async (accountId, deviceId) => {writes.push({accountId, deviceId});},
  };
  const source = new PersonDevicesSource('person', api, () => current, () => allowed);
  return {source, reads, writes, result: (value: AdministrationPage<AccessDevice>) => {result = value;}, hold: (value: Promise<AdministrationPage<AccessDevice>>) => {held = value;}, retire: () => {current = false;}, forbid: () => {allowed = false;}};
}

test('each page retains the captured account filter and exact continuation cursor', async () => {
  const f = fixture(); f.result(page([device('one')], 'next&cursor')); await f.source.load();
  assert.equal(f.reads.length, 1, 'only one bounded page is read without Load more');
  f.result(page([device('two')])); await f.source.load(true);
  assert.deepEqual(f.reads.map(({accountId, cursor}) => ({accountId, cursor})), [{accountId: 'person', cursor: ''}, {accountId: 'person', cursor: 'next&cursor'}]);
  assert.deepEqual(f.source.getSnapshot().items.map(row => row.id), ['one', 'two']);
});

test('a wrong-account response is refused on initial and later pages', async () => {
  for (const later of [false, true]) {
    const f = fixture();
    if (later) {f.result(page([device('one')], 'more')); await f.source.load();}
    f.result(page([device('other-device', 'other-person')]));
    await assert.rejects(f.source.load(later), /invalid_person_devices/);
    assert.equal(f.source.getSnapshot().items.some(row => row.accountId !== 'person'), false);
  }
});

test('single and all-device sign-outs use the captured member and reload their own page', async () => {
  const f = fixture(); await f.source.load();
  await f.source.signOut(f.source.getSnapshot().items[0]);
  await f.source.signOut();
  assert.deepEqual(f.writes, [{accountId: 'person', deviceId: 'one'}, {accountId: 'person', deviceId: undefined}]);
  assert.deepEqual(f.reads.map(read => read.cursor), ['', '', '']);
});

test('stale rows, disabled management and retired authority cannot sign out devices', async () => {
  const f = fixture(); await f.source.load();
  await assert.rejects(f.source.signOut(device('one')), /person_device_changed/);
  f.forbid(); await assert.rejects(f.source.signOut(), /person_device_changed/);
  f.retire(); await assert.rejects(f.source.signOut(), /person_device_changed/);
  assert.deepEqual(f.writes, []);
});

test('closing a tab aborts its request and refuses a late response', async () => {
  const f = fixture(); let resolve!: (value: AdministrationPage<AccessDevice>) => void;
  f.hold(new Promise(done => {resolve = done;}));
  const pending = f.source.load(); f.source.dispose();
  assert.equal(f.reads[0].signal.aborted, true);
  resolve(page([device('late')]));
  await assert.rejects(pending, {name: 'AbortError'});
  assert.deepEqual(f.source.getSnapshot().items, []);
});

test('a repeated cursor or oversized page fails rather than looping or widening the read', async () => {
  const f = fixture(); f.result(page([device('one')], 'same')); await f.source.load();
  f.result(page([device('two')], 'same')); await assert.rejects(f.source.load(true), /invalid_person_devices/);
  f.result({...page([]), limit: 200}); await assert.rejects(f.source.load(), /invalid_person_devices/);
});

test('a sign-out response cannot refresh or publish after its person tab closes', async () => {
  let resolve!: () => void, reads = 0, writes = 0;
  const gate = new Promise<void>(done => {resolve = done;});
  const source = new PersonDevicesSource('person', {
    list: async () => {reads++; return page([device('one')]);},
    signOut: async () => {writes++; await gate;},
  }, () => true, () => true);
  await source.load();
  const pending = source.signOut(source.getSnapshot().items[0]); source.dispose(); resolve();
  await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(writes, 1); assert.equal(reads, 1, 'late confirmation must not start another request');
  assert.deepEqual(source.getSnapshot().items, []);
});

test('an uncertain sign-out failure is visible and never automatically replayed', async () => {
  let writes = 0;
  const failure = new Error('connection ended');
  const source = new PersonDevicesSource('person', {list: async () => page([device('one')]), signOut: async () => {writes++; throw failure;}}, () => true, () => true);
  await source.load(); await assert.rejects(source.signOut(), failure);
  assert.equal(writes, 1); assert.equal(source.getSnapshot().busy, false); assert.equal(source.getSnapshot().error, failure);
});
