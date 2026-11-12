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
