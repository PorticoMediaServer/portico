import * as mutationRecovery from '../../../packages/client-core/src/user-mutation-recovery.ts';
import {NativePendingChanges} from '../src-next/shell/pending-changes.ts';
import {homeActionDiagnostics} from '../src-next/platform/home-action-diagnostics.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as homeSelection from '../../../packages/client-core/src/home-selection/index';
import * as homeCapabilities from '../../../packages/client-core/src/home-selection';
import {HomeService} from '../../../packages/client-core/src/home-service';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import {nativeHomeSelectionScope} from '../src-next/shell/selection-scope';
import {NativeShellNavigation, nativeRailDestination, titleRoute} from '../src-next/shell/routes';
import {planHomeWindows} from '../src-next/shell/home-windows';
import {homePlayTarget} from '../src-next/screens/home/model';
import * as shellModel from '../src-next/shell/model';
import * as readPlatform from '../src-next/screens/read/platform';
import {nativeHomeEntryLifetime} from '../src-next/screens/home/entry-lifetime';
import {NativeScreenServices} from '../src-next/screens/screen-services';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('actual assembly sends Back to topmost Home menu before player, then TV page, then OS', async () => {
  const h = hooks(), authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  const entry: ContentEntry = {id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Movie', available: true,
    navigation: {view: 'item', entityId: 'movie'}, playback: {itemId: 'movie'}};
  const row = {id: 'continue', viewerFence: 'fence', title: 'Continue', kind: 'continue', artworkShape: 'landscape', entries: [entry, {...entry, id: 'second'}, {...entry, id: 'third'}],
    privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: false,
    total: 3, start: 0, limit: 20, hasMore: false, nextCursor: '', endpoint: '', revision: {catalog: 1, viewer: 1}};
  const raw = {serverId: 'server', viewerFence: 'fence', rows: [row], spotlight: [], layout: {revision: 1, rowOrder: ['continue'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T19:00:00Z'};
  // A real HomeService parses and owns the in-memory document; no transport/API call occurs.
  const home = new HomeService({request: async () => JSON.parse(JSON.stringify(raw))});
  await home.load();
  const renderOrder: string[] = [];
  let routeConsumes = false;
  let back: () => boolean = () => false, expanded = false, pageCalls = 0, pageConsumes = true;
  const screenServices = await screenModule(new URL('../src-next/screens/native-screen-services.tsx', import.meta.url), {
    react: h.react, 'react-native': {NativeModules: {}}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})}, '../kit/dock/DockClearance': {useDockClearance: () => 0, feedbackBottomOffset: () => 0}, './screen-services': {NativeScreenServices},
    '../kit/overlays': {ToastHost: 'Toast'}, '../kit/states': {StateView: 'State'},
  });
  const {createNativeShellBindings} = await screenModule(new URL('../src-next/shell/assembly.tsx', import.meta.url), {
    '../screens/library/selection-actions': {}, '../screens/downloads/request': {}, '../platform/offline-downloads': {}, './selection-display': {},
    react: h.react, 'react-native': {BackHandler: {addEventListener: (_: string, fn: () => boolean) => {back = fn; return {remove() {}};}}, NativeModules: {}, Platform: {isTV: true}, View: 'View'},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../packages/client-core/src/user-mutation-recovery': mutationRecovery,
    './pending-changes': {NativePendingChanges}, './PendingChanges': {PendingChanges: 'PendingChanges'},
    '../../../../packages/client-core/src/home-selection/index': homeSelection,
    '../../../../packages/client-core/src/home-selection': homeCapabilities,
    '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({silent: true})},
    '../../../../packages/client-core/src/library-management': {}, '../../../../packages/client-core/src/presentation/home-spotlight': {},
    '../platform/home-action-diagnostics': {homeActionDiagnostics}, '../platform/haptics': {}, '../kit/overlays': {ToastHost: 'Toast'}, '../kit/states': {StateView: 'State'}, '../kit/shell': {OwnerTag: 'Owner'},
    './HomeSelectionBars': {HomeSelectionBars: 'Bars'}, './HomeSelectionOverlays': {HomeSelectionOverlays: 'Overlays'}, './account': {nativeAccountMenu() {}, nativeMenuRouteAvailable: () => true}, './library-scan': {libraryScanReadiness: () => ({ok: true})}, '../screens/downloads/DownloadSheet': {NativeDownloadSheet: 'NativeDownloadSheet'},
    '../screens/home/HomeEmpty': {NativeHomeEmpty: 'NativeHomeEmpty'},
    './model': shellModel,
    './home-windows': {planHomeWindows},
    './listening-playlist': {createNativeListeningPlaylist: () => ({dispose() {}})},
    './listening-queue-policy': {createNativeListeningQueuePolicy: () => ({dispose() {}})},
    './listening-song-detail': {createNativeListeningSongDetail: () => ({dispose() {}})},
    './music-discover-navigation': {createNativeMusicDiscoverNavigation: () => ({open() {}, render() {}, dispose() {}})},
    '../screens/destinations/music/mixes': {createNativeMusicMixes: () => ({dispose() {}})},
    './selection-scope': {nativeHomeSelectionScope}, './routes': {nativeRailDestination, titleRoute}, './route-registry': {nativeShellRoutes: {subscribe: () => () => {}, bind: () => ({render: () => {renderOrder.push('active'); return 'active';}, renderWatch: () => null, renderRetained: () => {renderOrder.push('retained'); return ['retained'];}, renderOverlays: () => ['overlay'], back: () => routeConsumes, scrollToTop() {}, dispose() {}})}}, '../screens/home/model': {homePlayTarget},
    '../screens/read/platform': readPlatform, '../screens/settings-live/NativeTVReceiver': {NativeTVReceiver: 'NativeTVReceiver'},
    '../screens/native-screen-services': screenServices,
    '../screens/read/reading-books-open': {openNativeReadingBookEntry: async () => false},
    '../screens/home/entry-lifetime': {nativeHomeEntryLifetime},
  });
  const navigation = new NativeShellNavigation(authority, () => authority);
  const bindings = createNativeShellBindings({home, session: () => ({viewer: {...authority, authority: 'local', role: 'member'}}), current: () => true,
    authority: () => authority, navigation, subscribeAuthority: navigation.subscribe, libraries: () => [{id: 'movies'}], api: {getUserMutationCoordinator: () => undefined, getUserMutationScope: () => undefined, request() {throw new Error('unexpected request');}}, events: {on: () => () => {}},
    account: {account: {role: 'member'}}, playback: {selectionPlayback: () => ({play() {throw new Error('unused playback');}, enqueue() {throw new Error('unused enqueue');}, playChannel() {throw new Error('unused channel');}}), getSnapshot: () => ({dock: {local: {expanded}}})}});
  bindings.attach({getSnapshot: () => ({authority})});
  const cleanup = bindings.registerTVPageBack(() => {pageCalls++; return pageConsumes;});
  bindings.setHomeWindow(home.getSnapshot().document!.rows[0], 0, 0, 0, true, ['third', 'movie', 'second']);
  const anchor = {current: {native: 'third-card'}};
  bindings.actions.more(home.getSnapshot().document!.rows[0].entries[2], 'continue', anchor);
  const overlay = () => bindings.renderSelection().props.children.find((node: any) => node.type === 'Overlays');
  assert.equal(overlay().props.anchorRef, anchor, 'the actual child ref reaches the existing native overlay');
  assert.equal(bindings.selection('continue').isMenuOpen('third'), true);
  assert.equal(bindings.selection('continue').isMenuOpen('movie'), false);
  const controller = bindings.renderSelection().props.children.find((node: any) => node.type === 'Overlays').props.controller;
  assert.ok(controller.getSnapshot().menu, 'the genuine registered member opened the controller menu');
  assert.equal(controller.getSnapshot().menu.member.entry.id, 'third', 'physical first slot is admitted through its genuine source index');
  assert.equal(bindings.homeMenuRow(controller.getSnapshot().menu.windowId), home.getSnapshot().document!.rows[0]);
  controller.closeMenu();
  assert.equal(bindings.selection('continue').isMenuOpen('third'), false);
  assert.equal(overlay().props.anchorRef, undefined);
  bindings.actions.more(home.getSnapshot().document!.rows[0].entries[2], 'continue', anchor);
  bindings.setHomeWindow(home.getSnapshot().document!.rows[0], 0, 0, 0, true, ['third', 'movie', 'second']);
  assert.equal(bindings.selection('continue').isMenuOpen('third'), true, 'unchanged mounted window preserves its actual opener');
  raw.rows[0].entries[2] = {...raw.rows[0].entries[2], title: 'Current server title'};
  await home.revalidate();
  const latest = home.getSnapshot().document!.rows[0].entries.find(value => value.id === 'third')!;
  assert.equal(controller.getSnapshot().menu?.member.entry, latest, 'same-authority menu actions use the actual latest source object after refresh');
  assert.equal(controller.getSnapshot().menu?.member.entry.title, 'Current server title');
  assert.equal(bindings.selection('continue').isMenuOpen('third'), false, 'a replaced registration cannot revive the prior captured native opener');
  assert.equal(overlay().props.anchorRef, undefined);
  const freshAnchor = {current: {native: 'current-third-card'}};
  bindings.actions.more(latest, 'continue', freshAnchor);
  assert.equal(overlay().props.anchorRef, freshAnchor);
  assert.equal(bindings.selection('continue').isMenuOpen('third'), true);
  expanded = true;
  assert.equal(back(), true);
  assert.equal(controller.getSnapshot().menu, null, 'menu closes before expanded player handles Back');
  assert.equal(bindings.selection('continue').isMenuOpen('third'), false);
  assert.equal(overlay().props.anchorRef, undefined);
  assert.equal(pageCalls, 0);
  assert.equal(back(), false, 'expanded player retains its own Back handling');
  expanded = false;
  assert.equal(back(), true); assert.equal(pageCalls, 1);
  pageConsumes = false;
  assert.equal(back(), false, 'Home opening state falls through to OS on second Back');
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  navigation.open({kind: 'shell', destination: {kind: 'home'}}, authority);
  assert.equal(back(), false, 'Home opening state still delegates to OS despite retained history');
  assert.deepEqual(navigation.getSnapshot().destination, {kind: 'home'});
  const removeRail = bindings.registerTVRailBack(() => true);
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  assert.equal(back(), true, 'rail Back returns to its actual page even away from Home');
  removeRail();
  let searchCalls = 0;
  const firstSearch = bindings.registerTVPageBack(() => {throw new Error('retired Search handler');}, {kind: 'search'});
  const removeSearch = bindings.registerTVPageBack(() => {searchCalls++; return true;}, {kind: 'search'});
  firstSearch();
  navigation.open({kind: 'shell', destination: {kind: 'search'}}, authority);
  routeConsumes = true; assert.equal(back(), true); assert.equal(searchCalls, 0, 'registered overlay/Watch consumes before page');
  routeConsumes = false; expanded = true;
  assert.equal(back(), false); assert.equal(searchCalls, 0, 'expanded player retains Back before Search');
  expanded = false;
  const railSearch = bindings.registerTVRailBack(() => true);
  assert.equal(back(), true); assert.equal(searchCalls, 0, 'rail handles Back before Search'); railSearch();
  assert.equal(back(), true); assert.equal(searchCalls, 1, 'visible Search has its own page Back');
  navigation.open(titleRoute(entry), authority);
  const beforeTitle = searchCalls;
  back(); assert.equal(searchCalls, beforeTitle, 'retained Search cannot consume Title Back');
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  back(); assert.equal(searchCalls, beforeTitle, 'Search cannot consume another destination Back');
  assert.deepEqual(navigation.getSnapshot().destination, {kind: 'home'}, 'rail Back goes Home rather than popping Search history');
  let arrivedTop = 0;
  const removeTop = bindings.registerScrollToTop({kind: 'home'}, () => {arrivedTop++;});
  assert.equal(arrivedTop, 1, 'a newly attached Home scroll host receives the pending opening request');
  assert.equal(back(), false, 'Home opening state yields to OS despite prior destinations');
  removeTop();
  removeSearch();
  const liveCalls: string[] = [];
  const parentGuide = bindings.registerTVPageBack(() => {liveCalls.push('guide'); return true;}, {kind: 'live'});
  const recordings = bindings.registerTVPageBack(() => {liveCalls.push('recordings'); return true;}, {kind: 'live'});
  navigation.open({kind: 'shell', destination: {kind: 'live'}}, authority);
  const hiddenParent = bindings.registerTVPageBack(() => false, {kind: 'live'});
  back(); hiddenParent(); recordings(); back();
  assert.deepEqual(liveCalls, ['recordings', 'guide'], 'closing a nested route restores the still-mounted page Back owner');
  parentGuide();
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  navigation.open(titleRoute(entry), authority);
  assert.deepEqual(bindings.getState().destination, {kind: 'saved'}, 'a title retains its real origin for Back');
  assert.deepEqual(bindings.getState().railDestination, {kind: 'library', libraryId: 'movies'}, 'rail highlights the authorized title library');
  assert.equal(bindings.getState().contentVisible, false);
  navigation.back(authority);
  assert.deepEqual(bindings.getState().railDestination, {kind: 'saved'});
  assert.equal(bindings.getState().contentVisible, true);
  navigation.open({kind: 'home-customize'}, authority);
  const routes = bindings.renderRoutes();
  assert.deepEqual(renderOrder, ['active', 'retained'], 'current module exists before its retained native views are requested');
  const flattened = (node: any): string[] => typeof node === 'string' ? [node] : Array.isArray(node) ? node.flatMap(flattened) : node?.props?.children ? flattened(node.props.children) : [];
  assert.deepEqual(routes.props.children.flatMap(flattened), ['retained', 'active', 'overlay'], 'retained views stay below the active route and module overlays above it');
  bindings.openServers?.('consent-kind');
  assert.deepEqual(navigation.getSnapshot().route, {kind: 'servers', initialPanel: 'consent-kind'}, 'Play on enters Link a TV directly');
  bindings.openServers?.();
  assert.deepEqual(navigation.getSnapshot().route, {kind: 'servers'}, 'ordinary Switch server still opens the chooser');
  cleanup(); bindings.dispose(); navigation.dispose(); home.dispose();
});
