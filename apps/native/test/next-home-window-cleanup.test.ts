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

test('Home window unmounts and expired Live windows never throw (P2-74 E-tv-30)', async () => {
  // The real controller, with registration that can refuse an expired Live window or throw mid-storm.
  let mode: 'real' | 'stale' | 'throw' = 'real';
  const calls = {real: 0, stale: 0, throw: 0};
  class Probe extends homeSelection.HomeSelectionController {
    registerWindow(input: Parameters<homeSelection.HomeSelectionController['registerWindow']>[0]) {
      calls[mode]++;
      if (mode === 'stale') return Object.freeze({ok: false as const, reason: 'stale-home-live-window' as const});
      if (mode === 'throw') throw Object.assign(new Error('conflicting-windows'), {code: 'conflicting-windows'});
      return super.registerWindow(input);
    }
  }

  const h = hooks(), authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  const entry: ContentEntry = {id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Movie', available: true,
    navigation: {view: 'item', entityId: 'movie'}, playback: {itemId: 'movie'}};
  const row = {id: 'continue', viewerFence: 'fence', title: 'Continue', kind: 'continue', artworkShape: 'landscape', entries: [entry, {...entry, id: 'second'}, {...entry, id: 'third'}],
    privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: false,
    total: 3, start: 0, limit: 20, hasMore: false, nextCursor: '', endpoint: '', revision: {catalog: 1, viewer: 1}};
  const other = {...row, id: 'recent', title: 'Recent', entries: [{...entry, id: 'other'}]};
  const raw = {serverId: 'server', viewerFence: 'fence', rows: [row, other], spotlight: [], layout: {revision: 1, rowOrder: ['continue', 'recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T19:00:00Z'};
  // A real HomeService parses and owns the in-memory document; no transport/API call occurs.
  const home = new HomeService({request: async () => JSON.parse(JSON.stringify(raw))});
  await home.load();
  const renderOrder: string[] = [];
  let routeConsumes = false;
  const expanded = false;
  const screenServices = await screenModule(new URL('../src-next/screens/native-screen-services.tsx', import.meta.url), {
    react: h.react, 'react-native': {NativeModules: {}}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 0})}, '../kit/dock/DockClearance': {useDockClearance: () => 0, feedbackBottomOffset: () => 0}, './screen-services': {NativeScreenServices},
    '../kit/overlays': {ToastHost: 'Toast'}, '../kit/states': {StateView: 'State'},
  });
  const {createNativeShellBindings} = await screenModule(new URL('../src-next/shell/assembly.tsx', import.meta.url), {
    '../screens/library/selection-actions': {}, '../screens/downloads/request': {}, '../platform/offline-downloads': {}, './selection-display': {},
    react: h.react, 'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, NativeModules: {}, Platform: {isTV: true}, View: 'View'},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../packages/client-core/src/user-mutation-recovery': mutationRecovery,
    './pending-changes': {NativePendingChanges}, './PendingChanges': {PendingChanges: 'PendingChanges'},
    '../../../../packages/client-core/src/home-selection/index': {...homeSelection, HomeSelectionController: Probe},
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
  const [first] = home.getSnapshot().document!.rows;
  bindings.setHomeWindow(first, 0, 0, 0, true, ['movie']);
  bindings.setHomeWindow(home.getSnapshot().document!.rows[1], 1, 0, 0, true, ['other']);
  bindings.actions.more(home.getSnapshot().document!.rows[1].entries[0], 'recent', {current: null});
  assert.equal(bindings.selection('recent').isMenuOpen('other'), true, 'both rows registered');
  const reread = async (title: string) => {raw.rows[1].entries[0] = {...raw.rows[1].entries[0], title}; await home.revalidate();};
  mode = 'stale';
  await assert.doesNotReject(reread('Expired'), 'a Home re-read whose Live window has expired never throws');
  assert.equal(bindings.selection('recent').isMenuOpen('other'), false, 'the expired window is dropped and offers nothing');
  mode = 'throw';
  await assert.doesNotReject(reread('Storm'), 'a re-read whose registration throws is reported, not thrown');
  assert.doesNotThrow(() => bindings.setHomeWindow(first, 0, 0, -1, false), 'an unmount whose re-registration throws never throws');
  assert.ok(calls.stale > 0 && calls.throw > 0, `both paths ran: ${JSON.stringify(calls)}`);
  mode = 'real';
});
