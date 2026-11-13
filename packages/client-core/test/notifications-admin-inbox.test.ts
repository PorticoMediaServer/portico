import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi} from '../src/http.ts';
import {readNotices, readUnreadNotices} from '../src/notifications-next.ts';

// 12-console rule 78: the server delivers server notices to the owner's ordinary inbox,
// and administrators' ordinary inboxes when the owner chooses. There is no second feed.
test('server notices use the profile inbox and its unread count in one read', async () => {
  const calls: string[] = [];
  const api = new HttpLocalApi('https://server.example', 'fixture-token', async url => {
    calls.push(String(url));
    return new Response(JSON.stringify({items: [{id: 'backup', kind: 'backup_failed', createdAt: '2026-11-13T00:00:00Z', read: false, archived: false}], unreadCount: 1}));
  });
  assert.equal(await readUnreadNotices(api), 1);
  assert.deepEqual(calls, ['https://server.example/v1/notifications?view=unread']);
  const page = await readNotices(api, 'all');
  assert.deepEqual(page.audiences, ['profile']);
  assert.equal(page.items[0]?.audience, 'profile');
  assert.equal(page.items[0]?.category, 'backup_failed');
});

test('native Notifications has one profile feed for every role and the badge uses the modern unread reader', () => {
  const source = readFileSync(new URL('../../../apps/native/src-next/screens/notifications/NotificationsScreen.tsx', import.meta.url), 'utf8');
  const registration = readFileSync(new URL('../../../apps/native/src-next/screens/notifications/register.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(source + registration, /readsAdminInbox|account-admin|adminService|notifications\.audience\.admin/);
  const services = readFileSync(new URL('../../../apps/native/src-next/shell/bootstrap-services.ts', import.meta.url), 'utf8');
  assert.match(services, /await readUnreadNotices\(this\.inbox,/);
  assert.doesNotMatch(services, /this\.inbox\.(load|refreshUnread)/);
});
