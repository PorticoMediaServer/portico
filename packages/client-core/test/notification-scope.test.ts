import assert from 'node:assert/strict';
import test from 'node:test';
import {createNotificationScope} from '../src/notification-scope.ts';
import {readNotices, changeNotices, readAllNotices} from '../src/notifications-next.ts';
const lifetime = (patch = {}) => ({serverId: 's', signal: new AbortController().signal, isCurrent: () => true, ...patch});
test('current inbox, badge and mutations reach the captured API', async () => {
 const calls: unknown[] = [];
 const scope = createNotificationScope({request: async <T>(path: string, method: string, body: unknown) => {calls.push([path, method, body]);return {items: [], unreadCount: 2} as T;}}, lifetime());
 assert.equal((await readNotices(scope, 'unread')).counts.unread, 2);
 await changeNotices(scope, 'archive', ['ntf_one']);await readAllNotices(scope);
 assert.deepEqual(calls, [['/v1/notifications?view=unread', 'GET', undefined], ['/v1/notifications/ntf_one', 'PATCH', {archived: true}], ['/v1/notifications/read-all', 'POST', undefined]]);
 scope.dispose();
});
test('a late notification response cannot cross the captured viewer lifetime', async () => {
 let settle!: (v: unknown) => void, current = true;
 const scope = createNotificationScope({request: <T>() => new Promise(resolve => {settle = resolve;}) as Promise<T>}, lifetime({isCurrent: () => current}));
 const read = scope.request('/v1/notifications?view=all');current = false;settle({items: [], unreadCount: 0});
 await assert.rejects(read, {name: 'AbortError'});scope.dispose();
});
test('the scope admits only current notification routes', async () => {
 const scope = createNotificationScope({request: async <T>(_p: string, _m: string, _b: unknown, signal?: AbortSignal) => {return {items: []} as T;}}, lifetime());
 for (const path of ['/v1/admin/settings/general', '/v1/notifications?view=all&view=unread', '/v1/notifications#other']) await assert.rejects(scope.request(path), {code: 'invalid_document'});
 await scope.request('/v1/notifications');scope.dispose();
 await assert.rejects(scope.request('/v1/notifications'), {name: 'AbortError'});
});

test('disposing cancels an outstanding read', async () => {
 let signal: AbortSignal | undefined;
 const scope = createNotificationScope({request: <T>(_p: string, _m: string, _b: unknown, captured?: AbortSignal) => {
  signal = captured;return new Promise<T>((_resolve, reject) => captured!.addEventListener('abort', () => reject(new DOMException('Cancelled', 'AbortError')), {once: true}));
 }}, lifetime());
 const read = scope.request('/v1/notifications');scope.dispose();
 assert.equal(signal?.aborted, true);await assert.rejects(read, {name: 'AbortError'});
});
