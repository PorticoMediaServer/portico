import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsReportsSource} from '../src/screens/settings/server/troubleshooting/reports-source.ts';

const serverId = 'captured-server';
const envelope = (result: unknown) => ({protocolVersion: '1.0', serverId, result});
const report = (id: string, body?: string) => ({id, accountId: 'account', profileId: 'profile', deviceId: 'device', platform: 'ios', deviceClass: 'phone',
  appVersion: '2.0', receivedAt: '2026-10-23T12:00:00Z', bytes: 10, ...(body === undefined ? {} : {body})});
const setAside = {failures: [{domain: 1, reason: 'No match found', attempts: 2, failedAt: '2026-10-23T12:00:00Z', title: 'A title'}], more: false};
const fence = 'a'.repeat(64);
const record = (sequence: number, lane: string) => ({sequence, at: '2026-10-23T12:00:00Z', lane, severity: 'info', component: 'backups', code: 'backup_finished', fields: {kept: 7}});
const page = (items: unknown[], nextCursor = '') => ({items, nextCursor});
const consoleEnvelope = (data: unknown) => ({scope: {serverId, viewerFence: fence}, data});

function fixture() {
  const calls: {path: string; method: string}[] = [];
  let failing = false, deleted = false;
  const transport = {request: async <T>(path: string, method = 'GET') => {
    calls.push({path, method});
    if (failing) throw new Error('down');
    if (path.includes('/console/diagnostics/')) {
      const lane = path.split('/').pop()!;
      const second = path.includes('cursor=9');
      return consoleEnvelope(page([record(second ? 2 : 1, lane)], second ? '' : '9')) as T;
    }
    if (path.includes('/catalogue/failures')) return setAside as T;
    if (method === 'DELETE') {deleted = true;return undefined as T;}
    if (path.includes('client-logs/')) return envelope(report('one', 'a log that names a title')) as T;
    if (path.includes('cursor=next-page')) return envelope({items: [report('two')], limit: 25}) as T;
    return envelope({items: deleted ? [] : [report('one')], limit: 25, ...(deleted ? {} : {nextCursor: 'next-page'})}) as T;
  }};
  return {source: new SettingsReportsSource(transport, serverId, 'captured-viewer', () => true), calls, fail: () => {failing = true;}, recover: () => {failing = false;}};
}

test('one read serves the first page and the titles a rebuild set aside, and the server names the next page', async () => {
  const f = fixture();
  try {
    assert.deepEqual(f.source.getSnapshot(), {phase: 'idle', reports: []});
    await f.source.load();
    const state = f.source.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.deepEqual(state.reports.map(item => item.id), ['one']);
    assert.equal(state.next, 'next-page');
    assert.equal(state.setAside?.failures[0].title, 'A title');
    assert.ok(Object.isFrozen(state) && Object.isFrozen(state.reports));
  } finally {f.source.dispose();}
});

test('the next page appends in the server order, and no page is guessed when the server offers none', async () => {
  const f = fixture();
  try {
    await f.source.load();
    await f.source.more();
    assert.deepEqual(f.source.getSnapshot().reports.map(item => item.id), ['one', 'two']);
    assert.equal(f.source.getSnapshot().next, undefined);
    const count = f.calls.length;
    await f.source.more();
    assert.equal(f.calls.length, count);
  } finally {f.source.dispose();}
});

test('one report is read fresh for its open dialog and its log never lands in the shared snapshot', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const value = await f.source.read('one', new AbortController().signal);
    assert.equal(value.body, 'a log that names a title');
    assert.doesNotMatch(JSON.stringify(f.source.getSnapshot()), /a log that names a title/);
    await assert.rejects(f.source.read('not-listed', new AbortController().signal));
  } finally {f.source.dispose();}
});

test('delete removes only a listed report and then rereads the list the server answers', async () => {
  const f = fixture();
  try {
    await f.source.load();
    assert.equal(await f.source.remove('not-listed'), false);
    assert.equal(f.calls.some(call => call.method === 'DELETE'), false);
    assert.equal(await f.source.remove('one'), true);
    assert.equal(f.calls.filter(call => call.method === 'DELETE').length, 1);
    assert.deepEqual(f.source.getSnapshot().reports, []);
    assert.equal(f.source.getSnapshot().phase, 'ready');
  } finally {f.source.dispose();}
});

test('a failed read is an error the owner can retry, and a retired scope reads nothing more', async () => {
  const f = fixture();
  try {
    f.fail();
    await f.source.load();
    assert.equal(f.source.getSnapshot().phase, 'error');
    const count = f.calls.length;
    f.recover();
    await f.source.retry();
    assert.equal(f.source.getSnapshot().phase, 'ready');
    assert.ok(f.calls.length > count);
    f.source.dispose();
    const after = f.calls.length;
    await f.source.load();
    assert.deepEqual(f.source.getSnapshot(), {phase: 'idle', reports: []});
    assert.equal(f.calls.length, after);
  } finally {f.source.dispose();}
});

test('records come by lane from their own console read, and only the lane the owner chose is kept', async () => {
  const f = fixture();
  try {
    await f.source.load();
    assert.deepEqual(f.source.getSnapshot().records?.lane, 'runtime');
    assert.equal(f.source.getSnapshot().records?.items.length, 1);
    await f.source.selectLane('audit');
    assert.deepEqual(f.source.getSnapshot().records?.lane, 'audit');
    assert.equal(f.source.getSnapshot().records?.items[0].severity, 'info');
    // The server named another page, so Older appends it; Latest returns to the first page.
    await f.source.moreRecords();
    assert.deepEqual(f.source.getSnapshot().records?.items.map(item => item.sequence), [1, 2]);
    await f.source.latestRecords();
    assert.deepEqual(f.source.getSnapshot().records?.items.map(item => item.sequence), [1]);
    // A page the server named is the only reason to read again.
    await f.source.moreRecords();
    assert.deepEqual(f.source.getSnapshot().records?.items.map(item => item.sequence), [1, 2]);
    const count = f.calls.filter(call => call.path.includes('/console/diagnostics/')).length;
    await f.source.moreRecords();
    assert.equal(f.calls.filter(call => call.path.includes('/console/diagnostics/')).length, count);
  } finally {f.source.dispose();}
});
