import test from 'node:test';
import assert from 'node:assert/strict';
import {createPeopleDevicesApi} from '../src/screens/settings/people-devices-api.ts';

const device = {id: 'device-a', name: 'Phone', createdAt: '2026-11-01T00:00:00Z', lastSeenAt: '2026-11-10T00:00:00Z', signedIn: true, current: false};

test('a person\'s devices are one read of their member devices list, with no second page', async () => {
  const calls: string[] = [];
  const api = createPeopleDevicesApi({request: async <T>(path: string): Promise<T> => {calls.push(path); return {items: [device]} as T;}}, () => true);
  const signal = new AbortController().signal;
  const page = await api.list('member a', '', signal);
  assert.deepEqual(page.items.map(row => [row.id, row.accountId, row.name]), [['device-a', 'member a', 'Phone']]);
  assert.equal(page.nextCursor, '');
  assert.equal((await api.list('member a', 'more', signal)).items.length, 0);
  assert.deepEqual(calls, ['/v1/admin/members/member%20a/devices']);
});

test('signing out one device is one DELETE; signing out all is one DELETE per device on the list', async () => {
  const calls: {path: string; method?: string}[] = [];
  const api = createPeopleDevicesApi({request: async <T>(path: string, method?: string): Promise<T> => {calls.push({path, method}); return (method === 'GET' ? {items: [device, {...device, id: 'device-b'}]} : undefined) as T;}}, () => true);
  const signal = new AbortController().signal;
  await api.signOut('member-a', 'device-a', signal); await api.signOut('member-a', undefined, signal);
  assert.deepEqual(calls, [
    {path: '/v1/admin/members/member-a/devices/device-a', method: 'DELETE'},
    {path: '/v1/admin/members/member-a/devices', method: 'GET'},
    {path: '/v1/admin/members/member-a/devices/device-a', method: 'DELETE'},
    {path: '/v1/admin/members/member-a/devices/device-b', method: 'DELETE'},
  ]);
});

test('retired authority and aborted openings cannot send a command', async () => {
  let calls = 0, current = true;
  const api = createPeopleDevicesApi({request: async <T>(): Promise<T> => {calls++; return undefined as T;}}, () => current);
  const request = new AbortController(); request.abort();
  await assert.rejects(api.signOut('member-a', undefined, request.signal), {name: 'AbortError'});
  current = false;
  await assert.rejects(api.signOut('member-a', undefined, new AbortController().signal), {name: 'AbortError'});
  assert.equal(calls, 0);
});
