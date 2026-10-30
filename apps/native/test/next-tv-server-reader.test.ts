import test from 'node:test';
import assert from 'node:assert/strict';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {routePath} from '../../../packages/contracts/src/routes';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {createTVServerReader, nativeServerAdmission} from '../src-next/screens/settings-live/server/tv-server-reader';
const authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'fence'};
const account = {canManage: true, account: {role: 'owner', primaryProfileId: 'primary', disabled: false}} as DirectSnapshot;
function scope() {return new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});}

test('members, secondary profiles and disabled accounts never read owner TV settings', async () => {
  const variants = [
    {...account, account: {...account.account, role: 'member' as const}},
    {...account, account: {...account.account, primaryProfileId: 'other'}},
    {...account, account: {...account.account, disabled: true}},
    {...account, canManage: false},
  ];
  for (const value of variants) {
    let reads = 0;
    const lifetime = scope();
    const reader = createTVServerReader({account: value, api: {request: async <T,>() => {reads++; return {} as T;}}}, lifetime);
    await assert.rejects(reader.request('/v1/admin/libraries'));
    await assert.rejects(reader.peoplePage('members'));
    assert.equal(reads, 0);
    lifetime.dispose();
  }
});

test('an administrator reads only the shared People pages, never the owner log, storage, catalogue or remote access', async () => {
  const value = {...account, account: {...account.account, role: 'admin' as const}};
  let reads = 0;
  const lifetime = scope();
  const reader = createTVServerReader({account: value, api: {request: async <T,>() => {reads++; return {} as T;}}}, lifetime);
  // The shared People page is the administrator's real audience.
  for (const path of [routePath('get_admin_access_members'), routePath('get_admin_access_invitations'), routePath('get_admin_access_devices')]) {
    await reader.request(path);
  }
  assert.equal(reads, 3);
  // The owner's pages are not part of that audience, even though the tier would pass.
  for (const path of [
    routePath('get_admin_logs'), routePath('get_admin_storage_usage'), routePath('get_data_storage_usage'),
    routePath('get_networking_certificate'), routePath('get_networking_remote'), routePath('get_connectivity_status'),
    '/v1/admin/libraries', '/v1/titles',
  ]) {
    await assert.rejects(reader.request(path), /native_tv_server_read_denied/);
  }
  assert.equal(reads, 3);
  lifetime.dispose();
});

test('owner reader allows GET only and rejects a response after authority retirement', async () => {
  const lifetime = scope();
  let reads = 0, finish!: (value: unknown) => void;
  const reader = createTVServerReader({account, api: {request: <T,>() => {reads++; return new Promise<unknown>(resolve => {finish = resolve;}) as Promise<T>;}}}, lifetime);
  assert.equal(nativeServerAdmission(account, lifetime), 'owner');
  await assert.rejects(reader.request('/v1/admin/libraries', 'POST', {}));
  assert.equal(reads, 0);
  const pending = reader.request('/v1/admin/libraries');
  assert.equal(reads, 1);
  lifetime.dispose(); finish({items: []});
  await assert.rejects(pending, {name: 'AbortError'});
  await assert.rejects(reader.request('/v1/admin/libraries'));
  assert.equal(reads, 1);
});

test('typed People pages serialize only their bounded GET query and retain admission', async () => {
  for (const role of ['owner', 'admin'] as const) {
    const lifetime = scope();
    const calls: string[] = [];
    const reader = createTVServerReader({account: {...account, account: {...account.account, role}}, api: {request: async <T,>(path: string, method?: string) => {assert.equal(method, 'GET'); calls.push(path); return {} as T;}}}, lifetime);
    for (const section of ['members', 'invitations', 'devices'] as const) await reader.peoplePage(section, 'a&cursor=evil');
    assert.deepEqual(calls.map(path => {const [base, query] = path.split('?'); return [base, Object.fromEntries(new URLSearchParams(query))];}), [
      [routePath('get_admin_access_members'), {limit: '50', cursor: 'a&cursor=evil'}],
      [routePath('get_admin_access_invitations'), {limit: '50', state: 'pending', cursor: 'a&cursor=evil'}],
      [routePath('get_admin_access_devices'), {limit: '50', cursor: 'a&cursor=evil'}],
    ]);
    await assert.rejects(reader.peoplePage('members', 'x'.repeat(4097)));
    await assert.rejects(reader.peoplePage('bogus' as 'members'));
    if (role === 'admin') await assert.rejects(reader.request(routePath('get_admin_access_members') + '?cursor=arbitrary'));
    lifetime.dispose();
    await assert.rejects(reader.peoplePage('devices'));
    assert.equal(calls.length, 3);
  }
});

test('typed People reader refuses forged selectors and control cursors before dispatch', async () => {
  let calls = 0;
  const lifetime = scope();
  const admin = {...account, account: {...account.account, role: 'admin' as const}};
  const reader = createTVServerReader({account: admin, api: {request: async <T,>() => {calls++; return {} as T;}}}, lifetime);
  for (const forged of ['https://example.invalid/v1/admin/access/members', '/v1/admin/access/members?role=owner', 'members?role=owner', '__proto__']) {
    await assert.rejects(reader.peoplePage(forged as 'members'));
  }
  await assert.rejects(reader.peoplePage('members', 'cursor\nlimit=1000'));
  await assert.rejects(reader.peoplePage('invitations', 'cursor\u2060'));
  await assert.rejects(reader.request(routePath('get_admin_access_members') + '?cursor=x'));
  await assert.rejects(reader.request(routePath('get_admin_access_members'), 'POST'));
  await assert.rejects(reader.request('https://example.invalid/v1/admin/access/members'));
  assert.equal(calls, 0);
  lifetime.dispose();
});


test('G35 keeps server logs and coded troubleshooting events off TV for every role', async () => {
  for (const role of ['owner', 'admin'] as const) {
    const lifetime = scope(); let reads = 0;
    const reader = createTVServerReader({account: {...account, account: {...account.account, role}}, api: {request: async <T,>() => {reads++; return {} as T;}}}, lifetime);
    for (const path of [routePath('get_admin_logs'), routePath('get_admin_logs') + '?limit=8', '/v1/admin/access/events']) await assert.rejects(reader.request(path), /native_tv_server_read_denied/);
    assert.equal(reads, 0);
    lifetime.dispose();
  }
});
