import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsFeedbackSource} from '../src/screens/settings/server/troubleshooting/feedback-source.ts';

const serverId = 'captured-server', fence = 'a'.repeat(64);
const envelope = (data: unknown) => ({scope: {serverId, viewerFence: fence}, data});
const report = (id: string, revision: number, status = 'open', message = 'Something went wrong.') =>
  ({id, cursor: '1', createdAt: '2026-10-22T18:20:00Z', updatedAt: '2026-10-22T18:20:00Z', expiresAt: '2027-04-20T18:20:00Z', revision, status,
    kind: 'playback', category: 'buffering', message, reporter: {name: 'Alex', authority: 'local', role: 'member', self: false},
    diagnostics: {decision: 'not-requested'}, client: {platform: 'android', appVersion: '1.4.2', online: true}, duplicates: 0, thread: []});
const list = (items: unknown[], nextCursor: string) => ({items, nextCursor, statusCounts: {}, total: items.length, filter: {}, observedAt: '2026-10-22T18:20:00Z'});

function fixture() {
  const calls: {path: string; method: string; body: unknown}[] = [];
  let conflict = false, lost = false, serverRevision = 1, listed = [report('one', 1), report('two', 2)];
  const transport = {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    calls.push({path, method, body});
    if (method === 'POST' && path.endsWith('/status')) {
      if (conflict) {conflict = false;throw Object.assign(new Error('moved'), {status: 409, code: 'console_conflict', currentRevision: 3});}
      if (lost) {lost = false;throw new TypeError('network');}
      const sent = body as {expectedRevision: number; status: string; reply: string};
      serverRevision = sent.expectedRevision + 1;
      const updated = {...report('one', serverRevision, sent.status), message: 'Something went wrong.'};
      listed = listed.map(row => row.id === 'one' ? updated : row);
      return envelope(updated) as T;
    }
    if (path.includes('/admin/feedback/reports/')) {
      const id = path.split('/').pop()!;
      // A conflict re-read sees the newer revision the server actually holds.
      const row = listed.find(item => item.id === id) ?? report(id, 1);
      return envelope({...row, revision: id === 'one' ? serverRevision : row.revision}) as T;
    }
    if (path.includes('/admin/feedback/reports')) return envelope(path.includes('cursor=9') ? list([report('three', 1)], '') : list(listed, '9')) as T;
    if (path.includes('/catalogue/failures')) return {failures: [], more: false} as T;
    if (path.includes('/console/diagnostics/')) return envelope({items: [], nextCursor: ''}) as T;
    return envelope({items: [], limit: 25, nextCursor: ''}) as T;
  }};
  return {source: new SettingsFeedbackSource(transport, serverId, 'captured-viewer', () => true), calls, conflict: () => {conflict = true;serverRevision = 3;}, lose: () => {lost = true;}, listed: () => listed};
}

test('one owner read lists the inbox and the server names the next page', async () => {
  const f = fixture();
  try {
    await f.source.load();
    assert.equal(f.source.getSnapshot().phase, 'ready');
    assert.deepEqual(f.source.getSnapshot().reports.map(row => row.id), ['one', 'two']);
    assert.equal(f.source.getSnapshot().next, '9');
    assert.equal(f.calls[0].path.includes('/admin/feedback'), true);
    await f.source.more();
    assert.deepEqual(f.source.getSnapshot().reports.map(row => row.id), ['one', 'two', 'three']);
    assert.equal(f.source.getSnapshot().next, undefined);
  } finally {f.source.dispose();}
});

test('the dialog reads its own report and saves status and reply on that captured row', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const fresh = await f.source.read('one', new AbortController().signal);
    assert.equal(fresh.revision, 1);
    const saved = await f.source.triage(fresh, 'in-progress', 'We are looking into it.', new AbortController().signal);
    assert.equal(saved.status, 'in-progress');
    assert.equal(saved.revision, 2);
    assert.equal(f.source.getSnapshot().reports.find(row => row.id === 'one')?.status, 'in-progress');
    await assert.rejects(f.source.read('not-listed', new AbortController().signal));
  } finally {f.source.dispose();}
});

test('a report that moved while the dialog was open carries the edit over once, silently', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const fresh = await f.source.read('one', new AbortController().signal);
    f.conflict();
    const saved = await f.source.triage(fresh, 'resolved', 'Fixed in 1.4.3.', new AbortController().signal);
    // The second write used the revision the reread proved (3), not the stale one (1).
    const patches = f.calls.filter(call => call.method === 'POST');
    assert.equal(patches.length, 2);
    assert.deepEqual([0, 1].map(index => (patches[index].body as {expectedRevision: number}).expectedRevision), [1, 3]);
    assert.equal(saved.status, 'resolved');
    assert.equal(saved.revision, 4);
  } finally {f.source.dispose();}
});

test('a retired scope stops reading and writing at once', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const fresh = await f.source.read('one', new AbortController().signal);
    f.source.dispose();
    const count = f.calls.length;
    await assert.rejects(f.source.triage(fresh, 'closed', '', new AbortController().signal));
    assert.equal(f.calls.length, count);
  } finally {f.source.dispose();}
});

test('a status change whose answer was lost is retried as the same operation', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const fresh = await f.source.read('one', new AbortController().signal);
    f.lose();
    await assert.rejects(f.source.triage(fresh, 'resolved', 'Fixed.', new AbortController().signal));
    await f.source.triage(fresh, 'resolved', 'Fixed.', new AbortController().signal);
    const sent = f.calls.filter(call => call.method === 'POST').map(call => call.body as {operationId: string; expectedRevision: number});
    assert.equal(sent.length, 2);
    assert.equal(sent[0].operationId, sent[1].operationId);
    assert.ok(sent[0].operationId);
    assert.equal(f.calls.filter(call => call.method === 'PATCH').length, 0, 'the retired PATCH route is never used');
  } finally {f.source.dispose();}
});
