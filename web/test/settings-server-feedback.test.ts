import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsFeedbackSource} from '../src/screens/settings/server/troubleshooting/feedback-source.ts';

const serverId = 'captured-server';
/** A report as GET /v1/admin/reports answers it. */
const report = (id: string, status = 'open', message = 'Something went wrong.', extra: Record<string, unknown> = {}) =>
  ({id, reporter: 'alex', kind: 'problem', status, message, hasLog: false, createdAt: '2026-10-22T18:20:00Z', updatedAt: '2026-10-22T18:20:00Z',
    context: {about: 'playback/buffering', item: {id: 'itm_1', title: 'Heat'}, platform: 'android', appVersion: '1.4.2', online: true}, deviceName: 'Kitchen TV', ...extra});

function fixture() {
  const calls: {path: string; method: string; body: unknown}[] = [];
  let lost = false, listed = [report('one'), report('two')];
  const transport = {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    calls.push({path, method, body});
    if (method === 'PATCH' && path.startsWith('/v1/admin/reports/')) {
      if (lost) {lost = false;throw new TypeError('network');}
      const sent = body as {status: string; reply?: string};
      const id = path.split('/').pop()!;
      const updated = report(id, sent.status, 'Something went wrong.', sent.reply ? {reply: sent.reply} : {});
      listed = listed.map(row => row.id === id ? updated : row);
      return updated as T;
    }
    if (path.startsWith('/v1/admin/reports/')) {
      const id = path.split('/').pop()!;
      const row = listed.find(item => item.id === id);
      if (!row) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
      return row as T;
    }
    if (path.startsWith('/v1/admin/reports')) return (path.includes('cursor=9') ? {items: [report('three', 'in_progress')]} : {items: listed, nextCursor: '9'}) as T;
    if (path.includes('/catalogue/failures')) return {failures: [], more: false} as T;
    return {items: [], nextCursor: ''} as T;
  }};
  return {source: new SettingsFeedbackSource(transport, serverId, 'captured-viewer', () => true), calls, lose: () => {lost = true;}, listed: () => listed};
}

test('one owner read lists the reports from the rebuilt server and the server names the next page', async () => {
  const f = fixture();
  try {
    await f.source.load();
    assert.equal(f.source.getSnapshot().phase, 'ready');
    assert.deepEqual(f.source.getSnapshot().reports.map(row => row.id), ['one', 'two']);
    assert.equal(f.source.getSnapshot().next, '9');
    assert.equal(f.calls[0].path, '/v1/admin/reports');
    const one = f.source.getSnapshot().reports[0]!;
    assert.deepEqual({kind: one.kind, category: one.category, itemId: one.itemId, itemTitle: one.itemTitle, deviceName: one.deviceName, client: one.client, reporter: one.reporter},
      {kind: 'playback', category: 'buffering', itemId: 'itm_1', itemTitle: 'Heat', deviceName: 'Kitchen TV', client: {platform: 'android', appVersion: '1.4.2', online: true}, reporter: 'alex'});
    await f.source.more();
    assert.equal(f.calls.at(-1)!.path, '/v1/admin/reports?cursor=9');
    assert.deepEqual(f.source.getSnapshot().reports.map(row => row.id), ['one', 'two', 'three']);
    assert.equal(f.source.getSnapshot().reports[2]!.status, 'in-progress');
    assert.equal(f.source.getSnapshot().next, undefined);
  } finally {f.source.dispose();}
});

test('the dialog reads its own report and saves status and reply on that row; the reply reaches the server', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const fresh = await f.source.read('one', new AbortController().signal);
    const saved = await f.source.triage(fresh, 'in-progress', '  We are looking into it. ', new AbortController().signal);
    assert.deepEqual(f.calls.at(-1), {path: '/v1/admin/reports/one', method: 'PATCH', body: {status: 'in_progress', reply: 'We are looking into it.'}});
    assert.equal(saved.status, 'in-progress'); assert.equal(saved.reply, 'We are looking into it.');
    assert.equal(f.source.getSnapshot().reports.find(row => row.id === 'one')?.status, 'in-progress');
    await f.source.triage(saved, 'closed', '', new AbortController().signal);
    assert.deepEqual(f.calls.at(-1)!.body, {status: 'closed'}, 'no reply is sent when the owner wrote none');
    await assert.rejects(f.source.read('not-listed', new AbortController().signal));
  } finally {f.source.dispose();}
});

test('a crash report and a report without context read plainly', async () => {
  const {parseOwnerReport} = await import('../../packages/client-core/src/server-admin/feedback-source.ts');
  const crash = parseOwnerReport({id: 'c', reporter: 'alex', kind: 'crash', status: 'open', hasLog: true, createdAt: '2026-10-22T18:20:00Z', updatedAt: 'x'});
  assert.deepEqual({kind: crash.kind, category: crash.category, crash: crash.crash, hasLog: crash.hasLog, message: crash.message}, {kind: 'apps', category: 'crash', crash: true, hasLog: true, message: ''});
  const bare = parseOwnerReport({id: 'b', reporter: 'alex', kind: 'problem', status: 'someday', message: 'Hi', createdAt: '2026-10-22T18:20:00Z', updatedAt: 'x'});
  assert.deepEqual({kind: bare.kind, category: bare.category, status: bare.status, client: bare.client, deviceName: bare.deviceName}, {kind: 'other', category: 'other', status: 'open', client: undefined, deviceName: undefined});
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

test('a status change whose answer was lost can be sent again: the same reply sends the reporter no second notice', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const fresh = await f.source.read('one', new AbortController().signal);
    f.lose();
    await assert.rejects(f.source.triage(fresh, 'resolved', 'Fixed.', new AbortController().signal));
    await f.source.triage(fresh, 'resolved', 'Fixed.', new AbortController().signal);
    const sent = f.calls.filter(call => call.method === 'PATCH').map(call => call.body);
    assert.deepEqual(sent, [{status: 'resolved', reply: 'Fixed.'}, {status: 'resolved', reply: 'Fixed.'}]);
    assert.equal(f.calls.some(call => call.path.includes('/feedback')), false, 'the old feedback routes are never used');
  } finally {f.source.dispose();}
});
