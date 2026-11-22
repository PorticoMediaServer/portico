import {homeContinuationCard, homeContinuationSource} from '../../../packages/client-core/src/presentation/home-continuation';
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
    '@portico/design': {}, '../../pages/touch/library/SeeAllPage': {}, '../../pages/tv/library/SeeAllPage': {SeeAllPage: 'HomeSeeAll'}, './HomeRowFilters': {},
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

/** Home's real bindings over one spotlight document; a book or comic opens through the reading opener it records. */
async function homeBindings(spotlight: readonly unknown[], libraries: () => readonly unknown[] = () => []) {
  const react = hooks().react, model = await shellModel;
  const {homeSpotlightModels} = await screenModule(new URL('../../../packages/client-core/src/presentation/home-spotlight.ts', import.meta.url), {
    react, './kit-model/index.ts': {},
  });
  const homeModel = await screenModule(new URL('../src-next/screens/home/model.ts', import.meta.url), {
    react, '../../../../../packages/client-core/src/presentation/home-continuation': {homeContinuationCard, homeContinuationSource},
    '../../../../../packages/client-core/src/presentation/home-spotlight': {}, '../../../../../packages/client-core/src/presentation/kit-model': {},
    '../../../../../packages/client-core/src/presentation/content-entry-subject': {},
    '../../../../../packages/client-core/src/presentation/home-card': await import('../../../packages/client-core/src/presentation/home-card.ts'),
  });
  const document = {serverId: 'server', viewerFence: 'viewer', revision: 1, rows: [], spotlight};
  const opened: unknown[] = [];
  const selectionContext = (scope: unknown, value: any) => ({scope, revision: value.revision});
  // As client-core's homeEntrySelection: a book or comic to read has no selection identity.
  const selection = (value: any) => value.kind === 'ebook' || value.kind === 'comic' ? undefined : {kind: value.kind, id: value.id, libraryId: value.libraryId};
  const inactiveOverlay = () => ({dispose() {}});
  const {createNativeShellBindings} = await screenModule(new URL('../src-next/shell/assembly.tsx', import.meta.url), {
    '../platform/offline-downloads': {}, './selection-display': {},
    react, 'react-native': {Platform: {isTV: false}, BackHandler: {addEventListener: () => ({remove() {}})}},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
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
    './owner-activity': {createNativeOwnerActivity: () => undefined}, './account': {nativeMenuRouteAvailable: () => false}, './library-scan': {}, '../pages/touch/downloads/DownloadSheet': {},
    './selection-scope': {nativeHomeSelectionScope: (viewer: any) => viewer}, './routes': {},
    './route-registry': {nativeShellRoutes: {subscribe, bind: () => ({dispose() {}})}}, './model': model,
    './music-discover-navigation': {createNativeMusicDiscoverNavigation: inactiveOverlay}, '../screens/home/model': homeModel,
    './home-windows': {}, '../screens/read/platform': {isReadingLibraryKind: (kind: string) => kind === 'book' || kind === 'comic'},
    '../screens/read/reading-books-open': {openNativeReadingBookEntry: async (_context: unknown, target: unknown) => {opened.push(target); return true;}},
    '../screens/home/entry-lifetime': {nativeDispatchedPlayLifetime, nativeHomeEntryLifetime, reportDispatchedPlayFailure}, '../screens/destinations/music/mixes': {createNativeMusicMixes: inactiveOverlay},
    './tv-receiver': {useTVReceiver: () => ({})}, '../kit/player/ReceiverApproval': {}, '../kit/home/HomeEmpty': {},
    './listening-playlist': {createNativeListeningPlaylist: inactiveOverlay}, './listening-queue-policy': {createNativeListeningQueuePolicy: inactiveOverlay},
    './listening-song-detail': {createNativeListeningSongDetail: inactiveOverlay},
  });
  const starts: any[] = [], errors: unknown[] = [], navigation = {authority, revision: 1, route: {kind: 'shell'}, destination: {kind: 'home'}};
  const bindings = createNativeShellBindings({current: () => true, authority: () => authority, subscribeAuthority: subscribe,
    home: {getSnapshot: () => ({document}), subscribe}, session: () => ({viewer: {...authority, authority: 'local'}}),
    navigation: {getSnapshot: () => navigation, subscribe}, api: {baseUrl: 'https://portico.test', getOutbox: () => undefined, onOutboxDropped: () => () => {}}, account: {account: {role: 'member'}}, libraries,
    reportError: (error: unknown) => {errors.push(error);},
    playback: {selectionPlayback: () => ({}), start: async (input: any) => {starts.push(input);}}});
  return {bindings, starts, opened, errors};
}

test('Home distinguishes a direct chapter play from a container Resume with a chapter target', async () => {
  const chapter = entry('chapter'), movie = entry('movie'), book = {...entry('book'), playback: undefined};
  const resumedChapter = {...chapter, playback: {...chapter.playback, startSeconds: 450}};
  const {bindings, starts} = await homeBindings([{entry: chapter}, {entry: movie}, {entry: book, resumeTarget: resumedChapter}]);
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

test('a book or comic in Continue your books opens, though Home gives it no selection identity', async () => {
  // The demo's PDF in Continue your books did nothing when tapped (22 Nov): Home's lookup of its selection identity
  // threw for an entry that has none, and the open was refused as unavailable.
  for (const kind of ['ebook', 'comic']) {
    const item = {...entry(kind), playback: undefined};
    const {bindings, opened, errors} = await homeBindings([{entry: item}], () => [{id: 'library', name: 'Reading', kind: kind === 'ebook' ? 'book' : 'comic'}]);
    try {
      bindings.actions.navigate(item);
      await new Promise(resolve => setTimeout(resolve, 0));
      assert.equal(opened.length, 1, `the ${kind} opens`);
      assert.equal((opened[0] as {libraryId: string}).libraryId, 'library');
      assert.deepEqual(errors, []);
    } finally {bindings.dispose();}
  }
  // A library that is not its reading library still refuses, as before.
  const book = {...entry('ebook'), playback: undefined};
  const {bindings, opened} = await homeBindings([{entry: book}], () => [{id: 'library', name: 'Films', kind: 'movie'}]);
  try {
    assert.throws(() => bindings.actions.navigate(book));
    assert.equal(opened.length, 0);
  } finally {bindings.dispose();}
});
