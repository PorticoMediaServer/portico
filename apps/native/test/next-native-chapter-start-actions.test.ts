import * as mutationRecovery from '../../../packages/client-core/src/user-mutation-recovery.ts';
import {NativePendingChanges} from '../src-next/shell/pending-changes.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {nativeDispatchedPlayLifetime, nativeHomeEntryLifetime, reportDispatchedPlayFailure} from '../src-next/screens/home/entry-lifetime.ts';
import {nativeStartSeconds} from '../src-next/platform/playback/start-intent.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const subscribe = () => () => {};
const entry = (kind: string, startSeconds: number | undefined = 125) => ({id: kind === 'chapter' ? 'file:2' : kind, kind, libraryId: 'library', title: kind,
  playback: {itemId: kind === 'chapter' ? 'file' : kind, ...(startSeconds === undefined ? {} : {startSeconds})}});
const shellModel = screenModule(new URL('../src-next/shell/model.ts', import.meta.url), {
  react: hooks().react, '../screens/read/platform': {}, '../platform/layout/tv-canvas': {},
});

test('generic destination Play retains chapter offsets but leaves ordinary cards to authoritative resume', async () => {
  const react = hooks().react, model = await shellModel;
  const {nativeDestinationLifetime} = await screenModule(new URL('../src-next/screens/destinations/lifetime.ts', import.meta.url), {
    react, '../../shell/model': model,
  });
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react, './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {}, '../watch': {},
    '../native-screen-services': {nativeScreenServices: () => ({reportError: assert.fail})},
  });
  for (const selected of [entry('chapter'), entry('movie'), entry('audiobook_file'), {...entry('chapter'), playback: {itemId: 'file', startSeconds: undefined}}]) {
    const starts: any[] = [];
    const context = {current: () => true, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local'}}), subscribeAuthority: subscribe,
      navigation: {getSnapshot: () => ({revision: 1}), subscribe}, playback: {start: async (input: any) => {starts.push(input);}}};
    const actions = createNativeEntryActions(context, {current: () => true, contains: (value: any) => value === selected, subscribe});
    try {
      actions.play(selected); assert.equal(starts.length, 1);
      assert.equal(starts[0].target, selected.playback);
      assert.equal(nativeStartSeconds(starts[0]), selected.kind === 'chapter' ? selected.playback.startSeconds : undefined);
      assert.equal(starts[0].lifetime.isCurrent(), true);
    } finally {actions.dispose();}
  }
});

test('See all entry Play preserves a chapter seek and retains the original row lifetime', async () => {
  const react = hooks().react;
  const {createNativeHomeRows} = await screenModule(new URL('../src-next/screens/home/SeeAllRoute.tsx', import.meta.url), {...stackSlotFixture, '../destinations/entry-actions': {createNativeEntryActions: () => ({menu() {}, dispose() {}})}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, 
    react, 'react-native': {Platform: {isTV: false}}, '../../kit/focus': {}, '../../kit/overlays': {}, '../../kit/controls': {},
    '../../../../../packages/client-core/src/recommendation-feedback': {}, '../../../../../packages/i18n/src': {},
    './HomeSeeAll': {}, './HomeRowFilters': {},
    './row-controller': {NativeHomeRowController: class {subscribe = subscribe; collection = {subscribe}; dispose() {}}},
    '../../platform/server-connections': {}, '../../shell/routes': {}, '../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}},
    '../../shell/model': {}, '../../kit/theme': {}, '../../kit/useScreenReader': {useScreenReader: () => false}, '../read/platform': {}, '../read/reading-books-open': {},
    './entry-lifetime': {nativeHomeEntryLifetime},
  });
  const route = {kind: 'home-see-all', destination: {type: 'row', rowId: 'chapters'}};
  let navigation = {route, revision: 1};
  const starts: any[] = [];
  const context = {current: () => true, authority: () => authority, libraries: () => [], api: {}, subscribeAuthority: subscribe,
    navigation: {getSnapshot: () => navigation, retainedRoutes: () => [], subscribe}, playback: {start: async (input: any) => {starts.push(input);}}};
  const rows = createNativeHomeRows(context, assert.fail); rows.reconcile();
  try {
    const page = rows.render({})[0];
    for (const selected of [entry('chapter'), entry('movie'), entry('audiobook_file')]) {
      page.props.onPlayEntry(selected, () => true);
      const input = starts.at(-1); assert.equal(input.target, selected.playback);
      assert.equal(nativeStartSeconds(input), selected.kind === 'chapter' ? 125 : undefined);
    }
    assert.equal(starts.length, 3); assert.equal(starts[0].lifetime.isCurrent(), true);
    navigation = {...navigation, revision: 2}; assert.equal(starts[0].lifetime.isCurrent(), false);
    page.props.onPlayEntry(entry('chapter'), () => false); assert.equal(starts.length, 3);
  } finally {rows.dispose();}
});

test('Home distinguishes a direct chapter play from a container Resume with a chapter target', async () => {
  const react = hooks().react, model = await shellModel;
  const {homeSpotlightModels} = await screenModule(new URL('../../../packages/client-core/src/presentation/home-spotlight.ts', import.meta.url), {
    react, './kit-model/index.ts': {},
  });
  const homeModel = await screenModule(new URL('../src-next/screens/home/model.ts', import.meta.url), {
    react, '../../../../../packages/client-core/src/presentation/home-spotlight': {}, '../../../../../packages/client-core/src/presentation/kit-model': {},
    '../../../../../packages/client-core/src/presentation/content-entry-subject': {},
  });
  const chapter = entry('chapter'), movie = entry('movie'), book = {...entry('book'), playback: undefined};
  const resumedChapter = {...chapter, playback: {...chapter.playback, startSeconds: 450}};
  const document = {serverId: 'server', viewerFence: 'viewer', revision: 1, rows: [], spotlight: [{entry: chapter}, {entry: movie}, {entry: book, resumeTarget: resumedChapter}]};
  const selectionContext = (scope: unknown, value: any) => ({scope, revision: value.revision});
  const selection = (value: any) => ({kind: value.kind, id: value.id, libraryId: value.libraryId});
  const inactiveOverlay = () => ({dispose() {}});
  const {createNativeShellBindings} = await screenModule(new URL('../src-next/shell/assembly.tsx', import.meta.url), {
    '../screens/library/selection-actions': {}, '../screens/downloads/request': {}, '../platform/offline-downloads': {}, './selection-display': {},
    react, 'react-native': {Platform: {isTV: false}, BackHandler: {addEventListener: () => ({remove() {}})}},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../../../packages/client-core/src/user-mutation-recovery': mutationRecovery,
    './pending-changes': {NativePendingChanges}, './PendingChanges': {PendingChanges: 'PendingChanges'},
    '../../../../packages/client-core/src/home-selection/index': {homeSelectionContext: selectionContext,
      HomeSelectionController: class {
        options: any; constructor(options: any) {this.options = options;} subscribe = subscribe;
        getSnapshot() {return {context: this.options.context, selection: {active: false}};}
        registerSpotlight() {return {ok: true, unregister() {}};} key(value: any) {return JSON.stringify(value);} dispose() {}
      }},
    '../../../../packages/client-core/src/home-selection': {homeEntrySelection: selection, homeEntryCapabilities: () => ({playable: true})},
    '../../../../packages/client-core/src/library-management': {}, '../../../../packages/client-core/src/presentation/home-spotlight': {homeSpotlightModels},
    '../platform/haptics': {}, '../platform/home-action-diagnostics': {homeActionDiagnostics: {action() {}, error() {}}},
    '../screens/native-screen-services': {nativeScreenServices: () => ({reportError: assert.fail, requestId: async () => 'request'})},
    '../kit/shell': {OwnerTag: 'Owner'}, './HomeSelectionBars': {}, './HomeSelectionOverlays': {},
    './account': {nativeMenuRouteAvailable: () => false}, './library-scan': {}, '../screens/downloads/DownloadSheet': {},
    './selection-scope': {nativeHomeSelectionScope: (viewer: any) => viewer}, './routes': {},
    './route-registry': {nativeShellRoutes: {subscribe, bind: () => ({dispose() {}})}}, './model': model,
    './music-discover-navigation': {createNativeMusicDiscoverNavigation: inactiveOverlay}, '../screens/home/model': homeModel,
    './home-windows': {}, '../screens/read/platform': {}, '../screens/read/reading-books-open': {},
    '../screens/home/entry-lifetime': {nativeDispatchedPlayLifetime, nativeHomeEntryLifetime, reportDispatchedPlayFailure}, '../screens/destinations/music/mixes': {createNativeMusicMixes: inactiveOverlay},
    '../screens/settings-live/NativeTVReceiver': {}, '../screens/home/HomeEmpty': {},
    './listening-playlist': {createNativeListeningPlaylist: inactiveOverlay}, './listening-queue-policy': {createNativeListeningQueuePolicy: inactiveOverlay},
    './listening-song-detail': {createNativeListeningSongDetail: inactiveOverlay},
  });
  const starts: any[] = [], navigation = {authority, revision: 1, route: {kind: 'shell'}, destination: {kind: 'home'}};
  const bindings = createNativeShellBindings({current: () => true, authority: () => authority, subscribeAuthority: subscribe,
    home: {getSnapshot: () => ({document}), subscribe}, session: () => ({viewer: {...authority, authority: 'local'}}),
    navigation: {getSnapshot: () => navigation, subscribe}, api: {baseUrl: 'https://portico.test', getUserMutationCoordinator: () => undefined, getUserMutationScope: () => undefined}, account: {account: {role: 'member'}},
    playback: {selectionPlayback: () => ({}), start: async (input: any) => {starts.push(input);}}});
  try {
    bindings.actions.play(chapter, chapter.playback);
    bindings.actions.play(movie, movie.playback);
    bindings.actions.play(book, resumedChapter.playback);
    assert.equal(starts.length, 3);
    assert.equal(nativeStartSeconds(starts[0]), 125);
    assert.equal(nativeStartSeconds(starts[1]), undefined);
    assert.equal(nativeStartSeconds(starts[2]), undefined, 'container Resume never turns cached chapter progress into an explicit seek');
    assert.equal(starts[2].target, resumedChapter.playback);
  } finally {bindings.dispose();}
});
