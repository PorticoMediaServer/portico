import test from 'node:test';
import assert from 'node:assert/strict';
import {createPeopleDevicesApi} from '../src/screens/settings/people-devices-api.ts';

test('the canonical device list is account-filtered on first and later pages', async () => {
  const calls: string[] = [];
  const api = createPeopleDevicesApi({request: async <T>(path: string): Promise<T> => {calls.push(path); return {items: [], limit: 100, nextCursor: ''} as T;}}, () => true);
  const signal = new AbortController().signal;
  await api.list('member-a', '', signal); await api.list('member-a', 'cursor&next', signal);
  assert.equal(calls.length, 2);
  for (const path of calls) {const url = new URL(path, 'https://server.example'); assert.equal(url.pathname, '/v1/admin/access/devices'); assert.equal(url.searchParams.get('account'), 'member-a'); assert.equal(url.searchParams.get('limit'), '100');}
  assert.equal(new URL(calls[1], 'https://server.example').searchParams.get('cursor'), 'cursor&next');
});

test('single and all-device sign-outs use the existing direct-member commands', async () => {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const api = createPeopleDevicesApi({request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {calls.push({path, method, body}); return undefined as T;}}, () => true);
  const signal = new AbortController().signal;
  await api.signOut('member-a', 'device-a', signal); await api.signOut('member-a', undefined, signal);
  assert.deepEqual(calls, [
    {path: '/v1/direct/members/member-a/devices/device-a/sign-out', method: 'POST', body: undefined},
    {path: '/v1/direct/members/member-a/devices/sign-out', method: 'POST', body: undefined},
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
