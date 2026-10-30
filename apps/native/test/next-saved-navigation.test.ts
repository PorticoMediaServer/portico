import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {routePath} from '../../../packages/contracts/src/routes';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {createNativeSavedHubController, createNativeSavedViewController, openNativeSavedView} from '../src-next/screens/saved/navigation';
import * as savedNavigation from '../src-next/screens/saved/navigation';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import * as customizeIntent from '../src-next/screens/home/customize-intent';
import * as librarySavedViewIntent from '../src-next/screens/library/saved-view-intent';
import * as savedSummaryOwner from '../src-next/screens/saved/view-summary-owner';
import * as savedRename from '../src-next/screens/saved/rename-view';
import {screenModule} from './helpers/screen-harness.mjs';

const title: ContentEntry = {kind: 'movie', id: 'actual-film', libraryId: 'movies', title: 'Stored title'};
function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'Home-only'};
  let active = true;
  const navigation = new NativeShellNavigation(authority, () => authority), listeners = new Set<() => void>();
  const session: LocalSession = {viewer: {...authority, authority: 'local', role: 'member'}, accessToken: '', expiresAt: '2026-10-25T00:00:00Z',
    authorizationHorizon: '2026-10-26T00:00:00Z', sessionFamilyId: 'fixture-family', tokenGeneration: '1'};
  const paths: string[] = [], signals: AbortSignal[] = [];
  let reply = async (url: URL): Promise<unknown> => {
    if (url.pathname.endsWith('/saved/summary')) return {serverId: 'server', viewerFence: 'Saved-summary-own-fence', revision: {catalog: 4, viewer: 8},
      myList: {posters: 12, squares: 3}, favorites: {posters: 5, squares: 2}, playlists: 4, collections: 7, savedViews: 6};
    if (url.pathname === routePath('get_saved_pins_order')) return {serverId: 'server', viewerFence: 'pins-own-fence', revision: 3,
      order: [{kind: 'collection', id: 'stored-resource', name: 'Stored collection', pinRevision: 2}]};
    if (url.pathname.includes('/saved-resources')) return {serverId: 'server', viewerFence: 'resource-own-fence', revision: 7, resources: [], nextCursor: ''};
    const view = url.searchParams.get('view')!;
    return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view, entityId: '', viewerFence: 'Saved-own-fence'},
      revision: {catalog: 9, viewer: 13}, heading: {key: 'saved.title', fallback: ''}, navigation: [{id: view, labelKey: 'saved.title', view}],
      query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [],
      sections: [{id: 'actual-section', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [title], totalCount: 12, nextCursor: ''}]};
  };
  const unavailable = (): never => {throw new Error('unexpected_saved_port');};
  const context: NativeShellBootstrapContext = {
    current: () => active, session: () => session, authority: () => authority, navigation,
    api: new HttpLocalApi('https://fixture.invalid', '', async (input, init) => {
      const url = new URL(String(input)); paths.push(url.pathname + url.search); if (init?.signal) signals.push(init.signal);
      return new Response(JSON.stringify(await reply(url)), {status: 200, headers: {'Content-Type': 'application/json'}});
    }),
    subscribeAuthority: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    get home() {return unavailable();}, get events() {return unavailable();}, preferences: unavailable, subscribePreferences: unavailable,
    get appVersion() {return unavailable();}, get serverName() {return unavailable();}, get playback() {return unavailable();},
    get watch() {return unavailable();}, get account() {return unavailable();}, libraries: unavailable,
  };
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  return {context, navigation, authority, paths, signals, listeners, setReply: (next: typeof reply) => {reply = next;},
    retire: () => {active = false; for (const listener of [...listeners]) listener();}};
}

test('Saved hub uses genuine pins and current membership; route capture keeps sort/filter without eager I/O', async () => {
  const f = fixture(), hub = createNativeSavedHubController(f.context, async () => 'request-id');
  try {
    assert.deepEqual(f.paths, []);
    assert.equal(hub.activation.getSnapshot(), hub.activation.getSnapshot());
    await hub.source.load(); assert.equal(hub.source.getSnapshot().phase, 'ready', JSON.stringify(hub.source.getSnapshot().error));
    const entry = hub.source.getSnapshot().myList.projection!.sections[0].entries[0];
    assert.equal(entry.kind, 'movie');
    if (entry.kind !== 'movie') throw new Error('expected actual movie');
    assert.equal(hub.source.contains(entry), true); assert.equal(hub.source.contains({...entry}), false);
    const pin = hub.source.getSnapshot().pins[0];
    assert.equal(hub.openPin({...pin}), false, 'matching IDs do not grant a copied pin membership');
    assert.equal(hub.openPin(pin), true);
    assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'saved-view', view: {view: 'resource', resourceId: pin.id}});
    assert.equal(hub.editPins(), false, 'a hidden Saved hub cannot navigate');
    f.navigation.back(f.authority);
    const query = {view: 'favorites', sort: 'title', direction: 'desc', filter: 'unwatched', shape: 'posters'} as const;
    assert.equal(hub.openView(query), true);
    const route = f.navigation.getSnapshot().route;
    assert.equal(route.kind, 'saved-view');
    if (route.kind !== 'saved-view') throw new Error('expected Saved route');
    assert.deepEqual(route.view, query); assert.notEqual(route.view, query); assert.equal(Object.isFrozen(route.view), true);
    f.navigation.back(f.authority); assert.equal(hub.editPins(), true); assert.equal(f.navigation.getSnapshot().route.kind, 'edit-pins');
    f.navigation.back(f.authority); assert.equal(hub.openDownloads(), true); assert.equal(f.navigation.getSnapshot().route.kind, 'downloads');
    f.retire(); assert.equal(hub.activation.getSnapshot().visible, false); assert.equal(hub.source.getSnapshot().summary, undefined);
    assert.equal(hub.openView({view: 'watchlist'}), false);
  } finally {hub.dispose(); f.navigation.dispose();}
  assert.equal(f.listeners.size, 0);
});

test('Saved subview preserves its actual source through Title/Back and permanently retires when popped', async () => {
  const f = fixture();
  openNativeSavedView(f.context, {view: 'watchlist', shape: 'posters'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const controller = createNativeSavedViewController(f.context, target, async () => 'request-id');
  try {
    assert.deepEqual(f.paths, []); assert.equal(controller.activation.getSnapshot().visible, true);
    await controller.load(); assert.equal(controller.source.getSnapshot().phase, 'ready');
    const snapshot = controller.source.getSnapshot(), member = snapshot.saved!.projection!.sections[0].entries[0];
    if (member.kind !== 'movie') throw new Error('expected actual movie');
    assert.equal(controller.source.contains(member), true); assert.equal(controller.source.contains({...member}), false);
    f.navigation.open({kind: 'title', entry: member}, f.authority);
    assert.equal(controller.source.getSnapshot(), snapshot); assert.equal(controller.activation.getSnapshot().visible, false);
    f.navigation.back(f.authority); assert.equal(f.navigation.getSnapshot().route, target);
    assert.equal(controller.activation.getSnapshot().intent, 'restore'); assert.equal(controller.source.getSnapshot(), snapshot);
    f.navigation.back(f.authority); assert.equal(controller.source.getSnapshot().phase, 'idle');
    assert.equal(controller.source.contains(member), false); assert.equal(f.listeners.size, 0);
    f.navigation.open(target, f.authority); await controller.load();
    assert.equal(controller.activation.getSnapshot().visible, false, 'a popped owner cannot be revived');
  } finally {controller.dispose(); f.navigation.dispose();}
});

test('retirement aborts a pending Saved request and late private pages never republish', async () => {
  const f = fixture(), finishes: ((value: unknown) => void)[] = [];
  f.setReply(() => new Promise(resolve => {finishes.push(resolve);}));
  openNativeSavedView(f.context, {view: 'collections'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const controller = createNativeSavedViewController(f.context, target, async () => 'request-id');
  const loading = controller.load(); await new Promise<void>(resolve => setImmediate(resolve));
  let changes = 0; controller.activation.subscribe(() => {changes++;});
  f.retire(); const closed = controller.source.getSnapshot();
  assert.equal(changes, 1); assert.deepEqual(closed, {phase: 'idle'}); assert.ok(f.signals.every(signal => signal.aborted));
  assert.equal(finishes.length, 2);
  for (const finish of finishes) finish({serverId: 'server', viewerFence: 'old-fence', revision: 7, resources: [], nextCursor: ''});
  await loading; assert.equal(controller.source.getSnapshot(), closed); assert.equal(controller.activation.getSnapshot().visible, false);
  assert.throws(() => createNativeSavedViewController(f.context, target, async () => 'unused'), /authority_required/);
  controller.dispose(); f.navigation.dispose();
});

test('headless Saved binding borrows one device lease and actual Watch menu with exact entry and route lifetimes', async () => {
  const f = fixture(), errors: unknown[] = [], opened: {entry: ContentEntry; current: () => boolean; anchor: unknown}[] = [];
  const menuListeners = new Set<() => void>();
  let menuEntry: ContentEntry | undefined, devices = 0, releases = 0;
  const offline = {actual: 'existing native adapter'};
  const common = {requestId: async () => 'request-id', reportError: (error: unknown) => {errors.push(error);},
    navigate: (entry: ContentEntry) => f.navigation.open({kind: 'title', entry}, f.authority)};
  const menu = {open: async (entry: ContentEntry, current: () => boolean, anchor: unknown) => {
    opened.push({entry, current, anchor}); menuEntry = entry; for (const listener of [...menuListeners]) listener();
  }, isOpen: (entry: ContentEntry) => menuEntry === entry,
  subscribe: (listener: () => void) => {menuListeners.add(listener); return () => {menuListeners.delete(listener);};}};
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react: {default: React},
    './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {openNativeReadingBookEntry: async () => false}, '../native-screen-services': {nativeScreenServices: () => common},
    '../watch': {nativeWatchEntryMenu: (context: NativeShellBootstrapContext) => {assert.equal(context, f.context); return menu;}},
  });
  const {createNativeSavedState} = await screenModule(new URL('../src-next/screens/saved/state.ts', import.meta.url), {
    './selection': {createNativeSavedViewSelection: () => ({dispose() {}})},
    react: {default: React},
    '../destinations/lifetime': {nativeDestinationLifetime}, '../destinations/entry-actions': {createNativeEntryActions},
    '../native-screen-services': {nativeScreenServices: () => common}, './navigation': savedNavigation,
    './view-summary-owner': savedSummaryOwner, './rename-view': savedRename,
    '../home/customize-intent': customizeIntent, '../library/saved-view-intent': librarySavedViewIntent,
    '../downloads/device': {createNativeDownloadsDevice: (context: NativeShellBootstrapContext) => {
      assert.equal(context, f.context); devices++; return {device: offline, dispose: () => {releases++;}};
    }},
  });
  const state = createNativeSavedState(f.context);
  try {
    assert.deepEqual(f.paths, []); assert.equal(devices, 1); assert.equal(state.offline, offline);
    await state.source.load(); const member = state.source.getSnapshot().myList.projection.sections[0].entries[0];
    const anchor = {current: {native: 'actual child ref'}};
    let menuChanges = 0; state.actions.menuState.subscribe(() => {menuChanges++;});
    state.actions.menuEntry({...member}, anchor); assert.equal(opened.length, 0);
    state.actions.menuEntry(member, anchor); assert.equal(opened.length, 1); assert.equal(opened[0].anchor, anchor);
    assert.equal(state.actions.isMenuOpen(member), true); assert.equal(menuChanges, 1); assert.equal(opened[0].current(), true);
    state.actions.openEntry(member); assert.equal(f.navigation.getSnapshot().route.kind, 'title'); assert.equal(opened[0].current(), false);
    f.navigation.back(f.authority); state.actions.openView({view: 'watchlist'});
    const route = f.navigation.getSnapshot().route;
    if (route.kind !== 'saved-view') throw new Error('expected Saved route');
    const view = state.view(route); assert.ok(view); assert.equal(state.view(route), view); assert.equal(devices, 1);
    await view.load(); const leaf = view.source.getSnapshot().saved.projection.sections[0].entries[0];
    view.actions.open(leaf); assert.equal(f.navigation.getSnapshot().route.kind, 'title');
    assert.equal(state.view(route), view); f.navigation.back(f.authority); assert.equal(state.view(route), view);
    f.navigation.back(f.authority); assert.equal(state.view(route), undefined); assert.equal(view.source.getSnapshot().phase, 'idle');
    view.actions.open(leaf); assert.equal(f.navigation.getSnapshot().route.kind, 'shell');
    f.retire(); assert.equal(releases, 1); assert.equal(menuListeners.size, 0); assert.equal(state.activation.getSnapshot().visible, false);
    assert.deepEqual(errors, []);
  } finally {state.dispose(); f.navigation.dispose();}
  assert.equal(releases, 1); assert.equal(f.listeners.size, 0);
});
