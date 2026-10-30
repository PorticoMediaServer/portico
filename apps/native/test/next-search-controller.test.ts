import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {HttpLocalApi} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import {NativeSearchDestination} from '../src-next/screens/search/model';
import {nativeSearchRememberHistory} from '../src-next/screens/search/navigation';
import {screenModule} from './helpers/screen-harness.mjs';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function preference(value: boolean) {
  return parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'registry', registry: {revision: 'registry', fields: [
    {key: 'search.rememberHistory', type: 'boolean', default: false, group: 'privacy', labelKey: 'preferences.search.rememberHistory', scopes: ['profile-server']},
  ]}, documents: [
    {scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {'search.rememberHistory': value}},
    {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}},
  ], effective: {'search.rememberHistory': value}, effectiveSource: {'search.rememberHistory': 'profile-server'}, clampedFields: []});
}
async function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'Home-only'};
  const navigation = new NativeShellNavigation(authority, () => authority), authorityListeners = new Set<() => void>(), preferenceListeners = new Set<() => void>();
  const paths: string[] = [], signals: AbortSignal[] = [], errors: unknown[] = [];
  let active = true, preferences = preference(true), historyReply = async (): Promise<unknown> => ({serverId: 'server', viewerFence: 'History-own-fence', maxEntries: 8,
    entries: [{q: 'arrived recent', updatedAt: '2026-10-23T00:00:00Z', uses: 1}]});
  const context = {navigation, current: () => active, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local'}}),
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
    preferences: () => preferences, subscribePreferences: (listener: () => void) => {preferenceListeners.add(listener); return () => {preferenceListeners.delete(listener);};},
    api: new HttpLocalApi('https://fixture.invalid', '', async (input, init) => {
      const url = new URL(String(input)); paths.push(url.pathname + url.search); if (init?.signal) signals.push(init.signal);
      const data = url.pathname.includes('/history') ? await historyReply() : {
        scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'Search-own-fence'}, revision: {catalog: 3, viewer: 7},
        query: {q: url.searchParams.get('q'), group: url.searchParams.get('group') || '', groups: ['movies'], limit: 40, sort: 'relevance', direction: 'desc', libraryIds: [], recorded: false},
        capabilities: {groups: [{id: 'movies', title: '', available: true, reason: '', sorts: ['relevance']}], sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
        groups: [{id: 'movies', entityKind: 'movie', title: '', status: 'success', errorCode: '', totalCount: 1, totalCountExact: true, nextCursor: '',
          items: [{id: 'actual-film', kind: 'movie', libraryId: 'movies', title: 'Stored title', navigation: {view: 'item', entityId: 'actual-film'}}]}],
      };
      return new Response(JSON.stringify(data), {status: 200, headers: {'Content-Type': 'application/json'}});
    }),
  };
  const {createNativeSearchController} = await screenModule(new URL('../src-next/screens/search/controller.ts', import.meta.url), {
    react: {default: React}, '../destinations/lifetime': {nativeDestinationLifetime}, './navigation': {nativeSearchRememberHistory}, './model': {NativeSearchDestination},
    '../native-screen-services': {nativeScreenServices: () => ({reportError: (error: unknown) => errors.push(error)})},
  });
  navigation.open({kind: 'shell', destination: {kind: 'search'}}, authority);
  const target = navigation.getSnapshot().route, controller = createNativeSearchController(context, target);
  return {controller, context, target, navigation, authority, paths, signals, errors, authorityListeners, preferenceListeners, createNativeSearchController,
    historyReply: (read: typeof historyReply) => {historyReply = read;},
    privacyOff: () => {preferences = preference(false); for (const listener of [...preferenceListeners]) listener();},
    retire: () => {active = false; for (const listener of [...authorityListeners]) listener();},
  };
}

test('Search owns the actual root object and retains only an arrived query/page through Title and Back', async () => {
  const f = await fixture();
  try {
    assert.deepEqual(f.paths, []); assert.equal(f.controller.activation.getSnapshot(), f.controller.activation.getSnapshot());
    assert.throws(() => f.createNativeSearchController(f.context, {...f.target}), /route_required/);
    f.controller.source.setText('matrix'); f.controller.source.selectGroup('movies'); await tick();
    const data = f.controller.source.getSnapshot().search.data; assert.ok(data);
    f.navigation.open({kind: 'title', entry: {kind: 'movie', id: 'actual-film', libraryId: 'movies', title: 'Stored title'}}, f.authority);
    assert.equal(f.controller.activation.getSnapshot().visible, false); const calls = f.paths.length;
    f.controller.source.setText('old hidden callback'); await f.controller.source.commit(); assert.equal(f.paths.length, calls);
    f.navigation.back(f.authority); await tick();
    assert.equal(f.navigation.getSnapshot().route, f.target); assert.equal(f.controller.activation.getSnapshot().intent, 'restore');
    assert.equal(f.controller.source.getSnapshot().search.data, data); assert.equal(f.paths.length, calls);
    f.navigation.back(f.authority); assert.equal(f.controller.source.getSnapshot().text, ''); assert.deepEqual(f.controller.source.getSnapshot().groups, []);
    assert.equal(f.authorityListeners.size, 0); assert.equal(f.preferenceListeners.size, 0);
    f.navigation.open(f.target, f.authority); assert.equal(f.controller.activation.getSnapshot().visible, false);
    assert.deepEqual(f.errors, []);
  } finally {f.controller.dispose(); f.navigation.dispose();}
});

test('hidden Search aborts history; confirmed privacy-off and authority retirement defeat late private facts', async () => {
  const f = await fixture(); let finish!: (value: unknown) => void;
  try {
    await f.controller.load(); assert.equal(f.controller.source.getSnapshot().recent.length, 1);
    f.historyReply(() => new Promise(resolve => {finish = resolve;})); const pending = f.controller.load(); await tick();
    const signal = f.signals.at(-1)!;
    f.navigation.open({kind: 'title', entry: {kind: 'movie', id: 'actual-film', libraryId: 'movies', title: 'Stored title'}}, f.authority);
    assert.equal(signal.aborted, true); f.privacyOff(); assert.deepEqual(f.controller.source.getSnapshot().recent, []);
    finish({serverId: 'server', viewerFence: 'old-fence', maxEntries: 8, entries: [{q: 'late private', updatedAt: '2026-10-23T00:00:00Z', uses: 1}]}); await pending;
    f.navigation.back(f.authority); await f.controller.load(); assert.deepEqual(f.controller.source.getSnapshot().recent, []);
    f.retire(); const count = f.paths.length; f.controller.source.setText('retired'); await f.controller.source.commit(); await f.controller.load();
    assert.equal(f.paths.length, count); assert.equal(f.controller.activation.getSnapshot().visible, false); assert.deepEqual(f.errors, []);
  } finally {f.controller.dispose(); f.navigation.dispose();}
});
