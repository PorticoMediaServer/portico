import {test} from 'node:test';
import assert from 'node:assert/strict';
import {InboxService} from '../src/inbox.ts';

// Instants travel as RFC 3339 text; the parsers hand the app epoch milliseconds.
const at=(ms:number)=>new Date(ms).toISOString();

const notification = (over: Record<string, unknown> = {}) => ({
  id: 'n-1', audience: 'profile', severity: 'info', source: 'downloads', category: 'download.finished',
  title: 'Download ready', body: 'A title finished downloading.', arguments: {}, actions: [],
  dedupeKey: 'download:dl-1', revision: 7, createdAt: at(1), updatedAt: at(1), expiresAt: at(99),
  readAt: null, archivedAt: null, read: false, archived: false, cursor: '3', ...over,
});
const counts = (unread: number) => ({unread, read: 0, archived: 0, total: unread});

function server() {
  const calls: {path: string; method: string; body?: unknown}[] = [];
  let fail: Error | undefined;
  const api = {
    request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
      calls.push({path, method, body});
      if (fail) throw fail;
      if (path.startsWith('/v1/notifications/unread-count')) return {data: {revision: 7, audience: 'profile', counts: counts(2), observedAt: at(2)}} as T;
      if (path.startsWith('/v1/notifications/inbox/actions')) {
        return {data: {revision: 8, audience: 'profile', applied: 1, receipts: [], counts: counts(0)}} as T;
      }
      if (path.includes('cursor=c2')) {
        return {data: {revision: 7, audience: 'profile', audiences: ['profile'], state: 'unread', counts: counts(2), items: [notification({id: 'n-2', cursor: '4'})], nextCursor: '', observedAt: at(2), retentionDays: 180}} as T;
      }
      if (path.startsWith('/v1/notifications/inbox')) {
        return {data: {revision: 7, audience: 'profile', audiences: ['profile'], state: 'unread', counts: counts(1), items: [notification()], nextCursor: 'c2', observedAt: at(2), retentionDays: 180}} as T;
      }
      throw new Error('unexpected ' + path);
    },
  };
  return {api, calls, failWith: (e?: Error) => { fail = e; }};
}

test('more appends the next page deduplicated; without a cursor it is a no-op', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load();
  assert.equal(service.getSnapshot().items.length, 1);
  assert.equal(service.getSnapshot().more, true);
  await service.more();
  assert.equal(service.getSnapshot().items.length, 2);
  assert.equal(service.getSnapshot().more, false);
  const before = s.calls.length;
  await service.more();
  assert.equal(s.calls.length, before, 'no cursor left: no request');
  service.dispose();
});

test('a failed more keeps the list and says so from the catalogue', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load();
  s.failWith(new TypeError('Failed to fetch'));
  await service.more();
  assert.equal(service.getSnapshot().items.length, 1);
  assert.match(service.getSnapshot().error ?? '', /can’t reach your server/);
  service.dispose();
});

test('read-all marks every row at once and is put back if refused', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op-2');
  await service.load();
  await service.more();
  assert.equal(service.getSnapshot().items.length, 2);
  await service.readAll();
  const sent = s.calls.find((c) => c.path === '/v1/notifications/inbox/actions')!;
  assert.deepEqual(sent.body, {operationId: 'op-2', expectedRevision: 7, audience: 'profile', operations: [{action: 'read-all'}]});
  s.failWith(new Error('The server is busy.'));
  // Reload to get rows back, then refuse a read-all to see the rollback.
  s.failWith(undefined);
  await service.load();
  await service.more();
  s.failWith(new Error('The server is busy.'));
  await service.readAll();
  assert.equal(service.getSnapshot().items.length, 2, 'the rows come back');
  assert.equal(service.getSnapshot().error, 'That change couldn’t be saved.');
  service.dispose();
});

test('empty pages and unknown audiences stay readable', async () => {
  const api = {
    request: async <T,>(path: string): Promise<T> => {
      if (path.startsWith('/v1/notifications/unread-count')) return {data: {revision: 1, audience: 'profile', counts: counts(0), observedAt: at(1)}} as T;
      return {data: {revision: 1, audience: 'profile', audiences: ['profile'], state: 'unread', counts: counts(0), items: [], nextCursor: '', observedAt: at(1), retentionDays: 180}} as T;
    },
  };
  const service = new InboxService(api, () => 'op');
  await service.load();
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.equal(service.getSnapshot().items.length, 0);
  await service.more();
  assert.equal(service.getSnapshot().items.length, 0);
  service.dispose();
});
