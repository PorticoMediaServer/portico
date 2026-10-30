import test from 'node:test';
import assert from 'node:assert/strict';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {PeoplePagesController, type PeoplePages} from '../src-next/screens/settings-live/server/people-pages';

const authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'fence'};
const account = {canManage: true, account: {role: 'admin', primaryProfileId: 'primary', disabled: false}} as DirectSnapshot;
const device = (id: string) => ({id, accountId: 'account', profileId: 'primary', username: 'a', name: id, platform: 'tv', app: '', appVersion: '', lastNetwork: '', liveState: 'idle', firstSeenAt: '2026-01-01T00:00:00Z', lastSeenAt: '2026-01-01T00:00:00Z', revision: 1});

test('People continuation coalesces, retains rows on failure, retries the offered cursor and fences retirement', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let state!: PeoplePages, calls = 0, resolve!: (value: unknown) => void, reject!: (error: Error) => void;
  const api = {request: <T,>(path: string) => {
    assert.match(path, /\/devices\?limit=50/); calls++;
    return new Promise<unknown>((ok, fail) => {resolve = ok; reject = fail;}) as Promise<T>;
  }};
  const pages = new PeoplePagesController({account, api}, lifetime, next => {state = next;});
  const first = pages.load('devices');
  resolve({items: [device('one')], limit: 50, nextCursor: 'next'}); await first;
  const second = pages.load('devices'); await pages.load('devices'); assert.equal(calls, 2);
  reject(new Error('offline')); await second;
  assert.deepEqual(state.devices.items.map(row => row.id), ['one']); assert.equal(state.devices.cursor, 'next');
  const retry = pages.load('devices'); resolve({items: [device('one'), device('two')], limit: 50, nextCursor: 'next'}); await retry;
  assert.deepEqual(state.devices.items.map(row => row.id), ['one', 'two']); assert.equal(state.devices.cursor, '');
  await pages.load('devices'); assert.equal(calls, 3);
  pages.dispose(); lifetime.dispose();
});

test('retired People continuation cannot publish a late page', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let resolve!: (value: unknown) => void, updates = 0;
  const pages = new PeoplePagesController({account, api: {request: <T,>() => new Promise<unknown>(ok => {resolve = ok;}) as Promise<T>}}, lifetime, () => {updates++;});
  const pending = pages.load('devices'); lifetime.dispose(); resolve({items: [device('late')], limit: 50, nextCursor: ''}); await pending;
  assert.equal(updates, 1); pages.dispose();
});

const member = (id: string) => ({accountId: id, username: id, profileId: 'primary', role: 'member', disabled: false, revision: 1, allowedLibraries: [], limitsRevision: 1, serverMaxStreams: null, effectiveMaxStreams: null, limits: {accessTokens: false, maxProfiles: 1, allowRemote: false, conversion: 'none', maxStreams: 0, maxStreamsMode: 'inherit'}});
const invitation = (id: string) => ({id, revision: 1, role: 'member', state: 'pending', createdAt: '2026-01-01T00:00:00Z', expiresAt: '2027-01-01T00:00:00Z', allowedLibraries: [], access: {maxStreamsMode: 'inherit', maxStreams: 0, allowRemote: true, accessTokens: true, maxContentRating: ''}});

test('owner and administrator follow each real People section cursor and request pending invitations', async () => {
  for (const role of ['owner', 'admin'] as const) {
    const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
    let state!: PeoplePages;
    const calls: string[] = [];
    const pages = new PeoplePagesController({account: {...account, account: {...account.account, role}}, api: {request: async <T,>(path: string) => {
      calls.push(path);
      const [base, query] = path.split('?'), params = new URLSearchParams(query);
      const cursor = params.get('cursor');
      assert.equal(params.get('limit'), '50');
      const ordinal = cursor === 'second' ? 2 : cursor === 'third' ? 3 : 1;
      const nextCursor = ordinal === 1 ? 'second' : ordinal === 2 ? 'third' : '';
      if (base.endsWith('/members')) return {items: [member(`m${ordinal}`)], limit: 50, nextCursor} as T;
      if (base.endsWith('/devices')) return {items: [device(`d${ordinal}`)], limit: 50, nextCursor} as T;
      assert.equal(params.get('state'), 'pending');
      // The only pending record is outside the server's first unfiltered page;
      // filtering at the producer yields it in this first pending page.
      return {items: ordinal === 1 ? [invitation('pending')] : [], limit: 50, nextCursor, totalCount: 60, matchedCount: 1} as T;
    }}}, lifetime, next => {state = next;});
    for (const section of ['members', 'invitations', 'devices'] as const) {
      await pages.load(section); await pages.load(section); await pages.load(section); await pages.load(section);
    }
    assert.deepEqual(state.members.items.map(row => row.accountId), ['m1', 'm2', 'm3']);
    assert.deepEqual(state.devices.items.map(row => row.id), ['d1', 'd2', 'd3']);
    assert.deepEqual(state.invitations.items.map(row => row.id), ['pending']);
    assert.equal(calls.length, 9);
    pages.dispose(); lifetime.dispose();
  }
});


test('a stale People row cannot start a successor cursor', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let state!: PeoplePages, calls = 0;
  const pages = new PeoplePagesController({account, api: {request: async <T,>() => {
    calls++;
    return {items: [device(String(calls))], limit: 50, nextCursor: calls === 1 ? 'first' : calls === 2 ? 'second' : ''} as T;
  }}}, lifetime, next => {state = next;});
  await pages.load('devices');
  const oldCursor = state.devices.cursor;
  await pages.load('devices', oldCursor);
  await pages.load('devices', oldCursor);
  assert.equal(calls, 2);
  assert.equal(state.devices.cursor, 'second');
  pages.dispose(); lifetime.dispose();
});

test('authority replacement while a page waits never publishes into the successor', async () => {
  let current = authority;
  let notify = () => {};
  const owner = {authority: () => current, current: () => true, subscribeAuthority: (listener: () => void) => {notify = listener; return () => {};}};
  const oldLifetime = new NativeScreenLifetime(owner);
  let finish!: (value: unknown) => void, oldUpdates = 0, successor!: PeoplePages;
  const old = new PeoplePagesController({account, api: {request: <T,>() => new Promise<unknown>(resolve => {finish = resolve;}) as Promise<T>}}, oldLifetime, () => {oldUpdates++;});
  const pending = old.load('devices');
  current = {...authority, epoch: 'successor'}; notify();
  const nextLifetime = new NativeScreenLifetime(owner);
  const next = new PeoplePagesController({account, api: {request: async <T,>() => ({items: [device('successor')], limit: 50, nextCursor: ''}) as T}}, nextLifetime, state => {successor = state;});
  await next.load('devices'); finish({items: [device('stale')], limit: 50, nextCursor: ''}); await pending;
  assert.equal(oldUpdates, 1); assert.deepEqual(successor.devices.items.map(row => row.id), ['successor']);
  old.dispose(); next.dispose(); nextLifetime.dispose();
});

test('explicit People refresh retires old flights, resets all sections and seen cursors, and disposal is terminal', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let state!: PeoplePages, devices = 0, finishOld!: (value: unknown) => void;
  const requests: string[] = [];
  const pages = new PeoplePagesController({account, api: {request: async <T,>(path: string) => {
    requests.push(path);
    if (path.includes('/members')) return {items: [], limit: 50, nextCursor: ''} as T;
    if (path.includes('/invitations')) return {items: [], limit: 50, nextCursor: '', totalCount: 0, matchedCount: 0} as T;
    devices++;
    if (devices === 3) return new Promise<unknown>(resolve => {finishOld = resolve;}) as Promise<T>;
    return {items: [device(`d${devices}`)], limit: 50, nextCursor: devices === 1 || devices === 4 ? 'x' : devices === 2 ? 'y' : ''} as T;
  }}}, lifetime, next => {state = next;});
  await pages.load('devices'); await pages.load('devices');
  const old = pages.load('devices');
  const oldGeneration = pages.currentGeneration();
  pages.refresh();
  assert.equal(state.devices.items.length, 0); assert.equal(state.devices.pending, true);
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(state.devices.items.map(row => row.id), ['d4']); assert.equal(state.devices.cursor, 'x');
  finishOld({items: [device('stale')], limit: 50, nextCursor: ''}); await old;
  assert.deepEqual(state.devices.items.map(row => row.id), ['d4']);
  await pages.load('devices', 'x', oldGeneration); assert.equal(devices, 4);
  await pages.load('devices'); assert.deepEqual(state.devices.items.map(row => row.id), ['d4', 'd5']);
  const count = requests.length; pages.dispose(); pages.refresh(); pages.start(); await pages.load('devices');
  assert.equal(requests.length, count); lifetime.dispose();
});

test('a prior generation rejection cannot replace refreshed People state', async () => {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  let state!: PeoplePages, rejectOld!: (reason: Error) => void, calls = 0;
  const pages = new PeoplePagesController({account, api: {request: <T,>(path: string) => {
    if (path.includes('/members')) return Promise.resolve({items: [], limit: 50, nextCursor: ''} as T);
    if (path.includes('/invitations')) return Promise.resolve({items: [], limit: 50, nextCursor: '', totalCount: 0, matchedCount: 0} as T);
    calls++;
    return calls === 1 ? new Promise<T>((_, reject) => {rejectOld = reject;}) : Promise.resolve({items: [device('new')], limit: 50, nextCursor: ''} as T);
  }}}, lifetime, next => {state = next;});
  const stale = pages.load('devices'); pages.refresh();
  await new Promise<void>(resolve => setImmediate(resolve));
  rejectOld(new Error('old failure')); await stale;
  assert.deepEqual(state.devices.items.map(row => row.id), ['new']); assert.equal(state.devices.error, undefined);
  pages.dispose(); lifetime.dispose();
});
