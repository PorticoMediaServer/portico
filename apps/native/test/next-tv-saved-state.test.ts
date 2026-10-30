import * as librarySavedIntent from '../src-next/screens/library/saved-view-intent';
import * as customizeIntent from '../src-next/screens/home/customize-intent';
import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import {NativeSavedViewDestination} from '../src-next/screens/saved/view-model';
import * as savedNavigation from '../src-next/screens/saved/navigation';
import * as savedSummaryOwner from '../src-next/screens/saved/view-summary-owner';
import * as savedRename from '../src-next/screens/saved/rename-view';
import {screenModule} from './helpers/screen-harness.mjs';

const title: ContentEntry = {kind: 'movie', id: 'actual-film', libraryId: 'movies', title: 'Stored title'};
const resource = {serverId: 'server', viewerFence: 'resources-fence', id: 'actual-collection', kind: 'collection', name: 'Actual collection', summary: '', visibility: 'private',
  revision: 7, role: 'owner', entryCount: 2, actions: ['entries'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 0, shares: []};
const directory = {serverId: 'server', viewerFence: 'resources-fence', revision: 7, resources: [resource], nextCursor: ''};
const summary = {serverId: 'server', viewerFence: 'summary-fence', revision: {catalog: 4, viewer: 8},
  myList: {posters: 12, squares: 3}, favorites: {posters: 5, squares: 2}, playlists: 4, collections: 7, savedViews: 6};
async function setup() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'Home-only'};
  let active = true, devices = 0, releases = 0;
  const navigation = new NativeShellNavigation(authority, () => authority), listeners = new Set<() => void>();
  const paths: string[] = [], signals: AbortSignal[] = [], errors: unknown[] = [], menus: {entry: ContentEntry; current: () => boolean; anchor: unknown}[] = [];
  const menuListeners = new Set<() => void>(), offline = {actual: 'borrowed device'};
  const session: LocalSession = {viewer: {...authority, authority: 'local', role: 'member'}, accessToken: '', expiresAt: '2026-10-25T00:00:00Z',
    authorizationHorizon: '2026-10-26T00:00:00Z', sessionFamilyId: 'fixture-family', tokenGeneration: '1'};
  let reply = async (url: URL): Promise<unknown> => {
    if (url.pathname.endsWith('/saved/summary')) return summary;
    if (url.pathname.includes('/saved-resources')) return directory;
    const view = url.searchParams.get('view') ?? (url.pathname.endsWith('/playlists') ? 'playlists' : 'watchlist');
    const entries = view === 'playlists' ? [{kind: 'playlist', id: 'actual-playlist', title: 'Actual playlist', count: 2,
      navigation: {view: 'playlist', entityId: 'actual-playlist'}}] : [title];
    return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view, entityId: '', viewerFence: 'Saved-own-fence'},
      revision: {catalog: 9, viewer: 13}, heading: {key: 'saved.title', fallback: ''}, navigation: [{id: view, labelKey: 'saved.title', view}],
      ...(view === 'playlists' ? {actions: []} : {}),
      query: {sort: 'title', direction: 'asc', category: view === 'watchlist' || view === 'favorites' ? 'all' : '', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [],
      sections: [{id: 'actual-section', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries, totalCount: 12, nextCursor: ''}]};
  };
  const unavailable = (): never => {throw new Error('unexpected_tv_saved_port');};
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
  const common = {requestId: async () => 'request-id', reportError: (error: unknown) => {errors.push(error);},
    navigate: (entry: ContentEntry) => navigation.open({kind: 'title', entry}, authority)};
  const menu = {open: async (entry: ContentEntry, current: () => boolean, anchor: unknown) => {menus.push({entry, current, anchor});},
    isOpen: (entry: ContentEntry) => menus.at(-1)?.entry === entry,
    subscribe: (listener: () => void) => {menuListeners.add(listener); return () => {menuListeners.delete(listener);};}};
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react: {default: {}}, './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {openNativeReadingBookEntry: async () => false}, '../native-screen-services': {nativeScreenServices: () => common}, '../watch': {nativeWatchEntryMenu: () => menu},
  });
  const {createNativeSavedState} = await screenModule(new URL('../src-next/screens/saved/state.ts', import.meta.url), {
    './selection': {createNativeSavedViewSelection: () => ({dispose() {}})},
    '../home/customize-intent': customizeIntent,
    '../library/saved-view-intent': librarySavedIntent,
    react: {default: {}}, '../destinations/lifetime': {nativeDestinationLifetime}, '../destinations/entry-actions': {createNativeEntryActions},
    '../native-screen-services': {nativeScreenServices: () => common}, './navigation': savedNavigation,
    './view-summary-owner': savedSummaryOwner, './rename-view': savedRename,
    '../downloads/device': {createNativeDownloadsDevice: () => {devices++; return {device: offline, dispose: () => {releases++;}};}},
  });
  const {createNativeTVSavedState} = await screenModule(new URL('./fixtures/superseded/tv-saved-state.ts', import.meta.url), {
    react: {default: {}}, '../../../src-next/screens/destinations/lifetime': {nativeDestinationLifetime}, '../../../src-next/screens/destinations/entry-actions': {createNativeEntryActions},
    '../../../src-next/screens/native-screen-services': {nativeScreenServices: () => common}, '../../../src-next/screens/saved/state': {createNativeSavedState},
    '../../../src-next/screens/saved/navigation': savedNavigation,
    '../../../src-next/screens/saved/view-model': {NativeSavedViewDestination},
  });
  const state = createNativeTVSavedState(context);
  return {state, context, navigation, authority, paths, signals, errors, menus, menuListeners, offline, listeners,
    devices: () => devices, releases: () => releases, setReply: (next: typeof reply) => {reply = next;},
    retire: () => {active = false; for (const listener of [...listeners]) listener();}};
}

test('TV Saved keeps inline tabs in the exact root history entry and borrows one device across resource routes', async () => {
  const f = await setup();
  try {
    assert.deepEqual(f.paths, []); assert.equal(f.devices(), 1); assert.equal(f.state.offline, f.offline);
    f.navigation.open({kind: 'shell', destination: {kind: 'saved'}}, f.authority);
    const target = f.navigation.getSnapshot().route, root = f.state.root(target); assert.ok(root);
    assert.equal(f.state.root(target), root);
    await root.load(); assert.equal(root.source.getSnapshot().route.view, 'watchlist');
    const revision = f.navigation.getSnapshot().revision;
    await root.selectTab('favorites');
    assert.equal(f.navigation.getSnapshot().revision, revision, 'focus/OK on a tab never adds a navigation entry');
    assert.equal(root.source.getSnapshot().route.view, 'favorites');
    const member = root.source.getSnapshot().saved.projection.sections[0].entries[0], anchor = {current: {native: 'actual card'}};
    assert.equal(root.openPlaylist(member), false, 'a genuine movie member cannot become a playlist destination');
    root.actions.menu({...member}, anchor); assert.equal(f.menus.length, 0);
    root.actions.menu(member, anchor); assert.equal(f.menus[0].anchor, anchor); assert.equal(f.menus[0].current(), true);
    root.actions.open(member);
    assert.equal(f.navigation.getSnapshot().route.kind, 'title'); assert.equal(root.activation.getSnapshot().visible, false);
    const reads = f.paths.length; await root.selectTab('history'); assert.equal(f.paths.length, reads, 'a late focus event cannot select a hidden tab');
    assert.equal(f.menus[0].current(), false);
    f.navigation.back(f.authority);
    assert.equal(f.state.root(target), root); assert.equal(root.activation.getSnapshot().intent, 'restore');
    assert.equal(root.source.getSnapshot().route.view, 'favorites');
    savedNavigation.openNativeSavedView(f.context, {view: 'watchlist'});
    const route = f.navigation.getSnapshot().route;
    if (route.kind !== 'saved-view') throw new Error('expected exact shared Saved route');
    const view = f.state.view(route); await view.load();
    assert.equal(f.state.view(route), view); assert.equal(f.devices(), 1);
    f.navigation.back(f.authority);
    assert.equal(f.state.view(route), undefined); assert.equal(root.source.getSnapshot().route.view, 'favorites');
    await root.selectTab('collections');
    const collection = root.source.getSnapshot().personal.resources[0];
    assert.equal(root.openResource({...collection}), false);
    assert.equal(root.openResource(collection), true);
    const captured = f.navigation.getSnapshot().route;
    assert.deepEqual(captured, {kind: 'saved-view', view: {view: 'resource', resourceId: collection.id}});
    assert.ok(f.state.view(captured)); assert.equal(f.devices(), 1);
    f.navigation.back(f.authority);
    assert.equal(root.source.getSnapshot().route.view, 'collections');
    await root.selectTab('playlists');
    const playlistState = root.source.getSnapshot();
    assert.equal(playlistState.phase, 'ready', playlistState.error?.message);
    const playlist = playlistState.saved.projection.sections[0].entries[0];
    assert.equal(root.openPlaylist({...playlist}), false); assert.equal(root.openPlaylist(playlist), true);
    assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'saved-view', view: {view: 'playlist', playlistId: playlist.id}});
    f.navigation.back(f.authority);
    f.navigation.back(f.authority);
    assert.equal(f.state.root(target), undefined); assert.equal(root.source.getSnapshot().phase, 'idle');
    const before = f.paths.length; await root.load(); await root.selectTab('watchlist'); assert.equal(f.paths.length, before);
    assert.deepEqual(f.errors, []);
  } finally {f.state.dispose(); f.navigation.dispose();}
  assert.equal(f.releases(), 1); assert.equal(f.listeners.size, 0); assert.equal(f.menuListeners.size, 0);
});

test('retiring the TV Saved viewer aborts its inline query and prevents late data or actions under another principal', async () => {
  const f = await setup(), pending: {url: URL; resolve: (value: unknown) => void}[] = [];
  f.setReply(url => new Promise(resolve => {pending.push({url, resolve});}));
  try {
    f.navigation.open({kind: 'shell', destination: {kind: 'saved'}}, f.authority);
    const target = f.navigation.getSnapshot().route, root = f.state.root(target);
    const loading = root.selectTab('collections'); await new Promise<void>(resolve => setImmediate(resolve));
    assert.equal(pending.length, 2);
    root.actions.menuState.subscribe(() => {});
    f.retire();
    assert.ok(f.signals.every(signal => signal.aborted)); assert.equal(f.releases(), 1);
    assert.equal(root.activation.getSnapshot().visible, false); assert.equal(root.source.getSnapshot().phase, 'idle');
    for (const read of pending) read.resolve(read.url.pathname.endsWith('/saved/summary') ? summary : directory);
    await loading;
    assert.equal(root.source.getSnapshot().phase, 'idle'); assert.equal(f.state.root(target), undefined);
    const before = f.paths.length; await root.selectTab('favorites'); assert.equal(f.paths.length, before);
    assert.equal(f.menuListeners.size, 0); assert.equal(f.listeners.size, 0); assert.deepEqual(f.errors, []);
  } finally {f.state.dispose(); f.navigation.dispose();}
  assert.equal(f.releases(), 1);
});
