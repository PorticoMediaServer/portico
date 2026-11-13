import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {HttpLocalApi} from '../../../packages/client-core/src/http.ts';
import {InboxService} from '../../../packages/client-core/src/inbox.ts';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime.ts';
import * as freshness from '../src-next/shell/freshness.ts';
import * as eventState from '../src-next/platform/event-resync.ts';
import {screenModule} from './helpers/screen-harness.mjs';

test('the native inbox reads plain profile pages and empty write answers, and retires a late page with its viewer', async () => {
  const calls: {path: string; method: string; body?: unknown}[] = [];
  let marked = false, hold = false, finish!: (value: Response) => void;
  const notice = (id: string) => ({id, kind: 'message', body: 'A message', createdAt: '2026-11-12T10:00:00Z', read: id === 'n1' && marked, archived: false});
  const api = new HttpLocalApi('https://server.example', 'fixture-token', async (url, init) => {
    const route = new URL(String(url));
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer fixture-token');
    calls.push({path: route.pathname + route.search, method: init?.method ?? 'GET', ...(init?.body ? {body: JSON.parse(String(init.body))} : {})});
    if (init?.method === 'PATCH') {marked = true; return new Response(null, {status: 204});}
    assert.equal(route.pathname, '/v1/notifications');
    if (hold && route.searchParams.has('cursor')) return new Promise<Response>(resolve => {finish = resolve;});
    const cursor = route.searchParams.has('cursor');
    return new Response(JSON.stringify({items: [notice(cursor ? 'n2' : 'n1')], unreadCount: marked ? 1 : 2, ...(!cursor ? {nextCursor: '2'} : {})}));
  });
  const {createNativeNotificationsRenderer} = await screenModule(new URL('../src-next/screens/notifications/register.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}},
    '../../../../../packages/client-core/src/inbox': {InboxService},
    '../../platform/event-resync': eventState, '../../shell/freshness': freshness,
    '../../platform/server-connections': {nativeRouteCrypto: {random: (size: number) => new Uint8Array(size).fill(1)}},
    '../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}},
    '../settings-live/lifetime': {NativeScreenLifetime}, './NotificationsScreen': {NativeNotificationsScreen: 'Inbox'},
  });
  const listeners = new Set<() => void>();
  let authority = {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', epoch: 'epoch', viewerFence: 'fence'};
  const route = {kind: 'notifications'};
  const context = {api, events: {}, current: () => true, authority: () => authority,
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);},
    navigation: {getSnapshot: () => ({revision: 1, route})}};
  const renderer = createNativeNotificationsRenderer(context);
  try {
    const screen = renderer.render(route, {}), service = screen.props.profileService as InboxService;
    await service.load('profile', 'all'); await service.more();
    assert.deepEqual(service.getSnapshot().items.map(item => item.id), ['n1', 'n2']);
    await service.apply('read', ['n1']);
    assert.equal(service.getSnapshot().error, undefined); assert.equal(service.getSnapshot().items[0]?.read, true);
    assert.ok(calls.some(call => call.method === 'PATCH' && call.path === '/v1/notifications/n1'));
    assert.ok(calls.every(call => !call.path.includes('/inbox') && !call.path.includes('unread-count')));
    await service.load('profile', 'all'); hold = true;
    const pending = service.more();
    for (let i = 0; i < 12 && !finish; i++) await Promise.resolve();
    assert.equal(typeof finish, 'function'); authority = {...authority, profileId: 'another-profile'}; for (const listener of listeners) listener();
    finish(new Response(JSON.stringify({items: [notice('late')], unreadCount: 1}))); await pending;
    assert.ok(!service.getSnapshot().items.some(item => item.id === 'late'));
    assert.throws(() => renderer.render(route, {}), {name: 'AbortError'});
  } finally {renderer.dispose(); freshness.viewerQueriesFor(api, {serverId: 'server'}).dispose();}
});
