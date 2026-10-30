import {test} from 'node:test';
import assert from 'node:assert/strict';
import {AccountNotificationsClient, parseAccountExportJob, parseAccountNotification, parseAccountNotificationPage, parseNotificationPreferences, runAccountExport} from '../src/account-notifications.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

// The exact shapes BE-hosted sends (be/hosted da0a9db: postgres.Notification / NotificationFeed /
// NotificationPreferences / ExportJob; detail per kind as documented in notifications.go).
const feed = {
  items: [
    {id: 'signin_fam1', kind: 'new_device', occurredAt: '2026-09-23T10:00:00Z', read: false, detail: {deviceId: 'device_fam1', name: 'Living room', platform: 'tvos', app: 'Portico'}},
    {id: 'sec_1', kind: 'security_event', occurredAt: '2026-09-23T09:00:00Z', read: false, detail: {event: 'two_step_enabled'}},
    {id: 'off_1', kind: 'server_offline', occurredAt: '2026-09-22T09:00:00Z', read: true, server: {id: 'srv', name: 'Den'}, detail: {}},
    {id: 'export_x', kind: 'account_export_ready', occurredAt: '2026-09-22T08:00:00Z', read: true, detail: {exportId: 'export_x'}},
    {id: 'keyexp_1', kind: 'signing_key_expiring', occurredAt: '2026-09-21T08:00:00Z', read: true, detail: {keyId: 'k1', expiresAt: '2026-10-01T00:00:00Z'}},
    {id: 'future_1', kind: 'something_new', occurredAt: '2026-09-20T08:00:00Z', read: true, detail: null},
  ],
  unread: 2,
  nextCursor: 'cur_2',
};

test('the feed parses every documented kind and keeps an unknown kind as "other"', () => {
  const page = parseAccountNotificationPage(feed);
  assert.equal(page.unread, 2);
  assert.equal(page.nextCursor, 'cur_2');
  assert.deepEqual(page.items.map(i => i.kind), ['new_device', 'security_event', 'server_offline', 'account_export_ready', 'signing_key_expiring', 'other']);
  assert.equal(page.items[0]!.detail.name, 'Living room');
  assert.equal(page.items[2]!.server?.name, 'Den');
  assert.equal(page.items[5]!.rawKind, 'something_new');
  assert.equal(parseAccountNotificationPage({items: [], unread: 0}).nextCursor, undefined);
  assert.throws(() => parseAccountNotificationPage({items: [], unread: -1}));
});

test('one item that cannot be read is left out and the rest of the page is kept', () => {
  resetSkippedParts();
  const page = parseAccountNotificationPage({...feed, futureField: 1, items: [
    {...feed.items[0], read: 'no'},
    {...feed.items[1], futureField: {nested: true}},
    {...feed.items[2], id: ''},
    'not an item',
    {...feed.items[3], occurredAt: 'yesterday'},
    feed.items[5],
  ]});
  assert.deepEqual(page.items.map(i => i.id), ['sec_1', 'future_1']);
  assert.equal('futureField' in page.items[0]!, false);
  assert.equal(page.unread, 2);
  assert.equal(page.nextCursor, 'cur_2');
  assert.deepEqual(skippedParts(), [{document: 'account-notifications', part: 'items', count: 4}]);
  // A single item read on its own has nothing else to keep.
  assert.throws(() => parseAccountNotification({...feed.items[0], read: 'no'}));
});

test('a server reference or detail that cannot be read is left off the item', () => {
  resetSkippedParts();
  const offline = parseAccountNotification({...feed.items[2], server: {id: 'srv', name: 'x'.repeat(201)}, detail: 'none'});
  assert.equal(offline.kind, 'server_offline');
  assert.equal(offline.server, undefined);
  assert.deepEqual(offline.detail, {});
  assert.deepEqual(skippedParts().map(p => p.part), ['server', 'detail']);
});

test('the page size, cursor and unread count are still exact', () => {
  assert.throws(() => parseAccountNotificationPage({items: Array.from({length: 201}, () => feed.items[0]), unread: 0}));
  assert.throws(() => parseAccountNotificationPage({items: {}, unread: 0}));
  assert.throws(() => parseAccountNotificationPage({items: [], unread: 0, nextCursor: 7}));
  assert.throws(() => parseAccountNotificationPage({items: [], unread: 0, nextCursor: 'x'.repeat(2049)}));
  assert.throws(() => parseAccountNotificationPage({items: [], unread: 1.5}));
  assert.throws(() => parseAccountNotificationPage(null));
  // An export in a state this app does not know is neither pending nor ready, so it is not guessed at.
  assert.throws(() => parseAccountExportJob({id: 'export_x', state: 'failed', expiresAt: '2026-09-24T00:00:00Z'}));
  assert.equal(parseAccountExportJob({id: 'export_x', state: 'ready', expiresAt: '2026-09-24T00:00:00Z', futureField: 1}).state, 'ready');
});

test('requests go to the documented routes with the documented bodies', async () => {
  const calls: [string, string | undefined, unknown][] = [];
  const replies: Record<string, unknown> = {
    'GET /v1/account/notifications?cursor=cur_2': {items: [], unread: 0},
    'GET /v1/account/notification-preferences': {serverOffline: true},
    'PUT /v1/account/notification-preferences': {serverOffline: false},
  };
  const client = new AccountNotificationsClient(async <T,>(path: string, method?: string, body?: unknown) => { calls.push([path, method, body]); return replies[`${method} ${path}`] as T; });
  await client.page('cur_2');
  await client.markRead(['a', 'b']);
  await client.markRead('all');
  await client.markRead([]);
  assert.deepEqual((await client.preferences()).serverOffline, true);
  assert.deepEqual((await client.savePreferences({serverOffline: false})).serverOffline, false);
  assert.deepEqual(calls.slice(1, 3), [['/v1/account/notifications/read', 'POST', {ids: ['a', 'b']}], ['/v1/account/notifications/read', 'POST', {all: true}]]);
  assert.equal(calls.length, 5);
  assert.throws(() => parseNotificationPreferences({serverOffline: true,storageNearlyFull:true}));
});

test('the data export: request, wait until ready, download (no email, no token link)', async () => {
  const calls: [string, string | undefined, unknown][] = [];
  let polls = 0;
  const client = new AccountNotificationsClient(async <T,>(path: string, method?: string, body?: unknown) => {
    calls.push([path, method, body]);
    if (path === '/v1/account/export') return {id: 'export_x', state: 'pending', expiresAt: '2026-09-24T00:00:00Z'} as T;
    if (path === '/v1/account/exports/export_x') return {id: 'export_x', state: ++polls >= 2 ? 'ready' : 'pending', expiresAt: '2026-09-24T00:00:00Z'} as T;
    if (path === '/v1/account/exports/export_x/download') return {account: {id: 'acct'}} as T;
    throw new Error('unexpected ' + path);
  });
  const op = 'op-0123456789abcdef';
  const out = await runAccountExport(client, op, {sleep: async () => {}});
  assert.equal(out.state, 'ready');
  assert.deepEqual(out.state === 'ready' && out.data, {account: {id: 'acct'}});
  assert.deepEqual(calls[0], ['/v1/account/export', 'POST', {operationId: op}]);
  assert.deepEqual(calls.at(-1), ['/v1/account/exports/export_x/download', 'POST', {}]);
  const slow = new AccountNotificationsClient(async <T,>(path: string) => ({id: 'export_y', state: 'pending', expiresAt: '2026-09-24T00:00:00Z'} as T));
  assert.equal((await runAccountExport(slow, 'op-fedcba9876543210', {sleep: async () => {}, maxWaitMs: 5000})).state, 'pending');
  await assert.rejects(runAccountExport(slow, 'short', {sleep: async () => {}}));
});
