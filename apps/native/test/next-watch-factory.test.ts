import {episodeLabel} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {setContinueDismissed} from '../../../packages/client-core/src/continue-watching.ts';
import {PlaybackOptionsClient} from '../../../packages/client-core/src/playback-v1/options.ts';
import {localV1Http} from '../../../packages/client-core/src/playback-v1/local-http.ts';
import {sameFence, playbackFence, type PlaybackFence} from '../../../packages/player-core/src/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import {NativeTitleController} from '../src-next/screens/watch/title-controller.ts';
import {NativeTitleStarts} from '../src-next/screens/watch/title-starts.ts';
import {NativeWatchControls} from '../src-next/screens/watch/controls.ts';
import {FileInfoSession, fileInfoUsesGroups} from '../../../packages/client-core/src/file-info-session.ts';
import {groupRow, groupsPage, ownerSurface} from './helpers/file-info-windows.ts';
import {readNativePlaybackInfo} from '../src-next/screens/watch/playback-info.ts';
import type {NativePlaybackAdapter} from '../src-next/platform/playback/types.ts';
import {infoWords} from './helpers/playback-info.ts';
import {nativeWatchTracks} from '../src-next/screens/watch/track-identity.ts';
import {NativeVersionChooserController} from '../src-next/screens/watch/version-destination.ts';
import {nativeVersionChoices, nativeVersionNames} from '../src-next/screens/watch/version-options.ts';
import {NativeDiscTitlesController} from '../src-next/screens/watch/disc-titles-controller.ts';
import {prepareDiscTitle, readDiscTitles} from '../src-next/screens/watch/disc-titles.ts';
import {NativeWatchPresenters} from '../src-next/screens/watch/presenters.ts';
import {NativeScreenServices} from '../src-next/screens/screen-services.ts';
import type {NativeWatchCapture, NativeVersionDestination, NativeVersionsOpening} from '../src-next/screens/watch/factory.ts';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap.ts';

const {react} = hooks();
let detailProjection: any = null;
const detailStub = {DetailService: class {snapshot: any = {phase: 'idle', data: null}; listeners = new Set<() => void>();
  async select() {this.snapshot = {phase: detailProjection ? 'ready' : 'error', data: detailProjection};}
  getSnapshot = () => this.snapshot; subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  mutate = async (intent: unknown) => {mutations.push(intent);}; dispose = () => {this.snapshot = {phase: 'idle', data: null};};}};
const mutations: unknown[] = [];
const episodeAtoms = {react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 800, height: 600})},
  '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/Icon': {Icon: 'Icon'},
  '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch', TextField: 'TextField'},
  '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/overlays/TVPanelMotion': {useTVPanelInteractive: () => true},
  '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/theme': {useTheme: () => ({colors: {}, type: {}})}};
const seasons = await screenModule(new URL('../src-next/screens/watch/SeasonsSheet.tsx', import.meta.url), episodeAtoms);
const episodes = await screenModule(new URL('../src-next/screens/watch/EpisodePanel.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, ...episodeAtoms,
  '../../../../../packages/client-core/src/presentation/kit-model': {episodeLabel},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText}, './SeasonsSheet': seasons,
  '../../kit/cards': {EpisodeTile: 'EpisodeTile'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop'}});
const {createNativeWatchScreens} = await screenModule(new URL('../src-next/screens/watch/factory.tsx', import.meta.url), {'../share/share-actions': {shareLinkActions: () => []}, 
  react, '../../../../../packages/client-core/src/playback-v1/options': {PlaybackOptionsClient},
  '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http},
  '../../../../../packages/client-core/src/detail': detailStub,
  '../../../../../packages/client-core/src/continue-watching': {setContinueDismissed},
  '../../platform/layout/full-window': {nativeFullWindowOrientation: undefined},
  './disc-titles-controller': {NativeDiscTitlesController}, './disc-titles': {prepareDiscTitle, readDiscTitles},
  '../../../../../packages/player-core/src': {sameFence}, '../../../../../packages/i18n/src': {defaultI18n},
  '../../shell/model': {sameAuthority}, './controls': {NativeWatchControls}, '../../../../../packages/client-core/src/file-info-session': {FileInfoSession, fileInfoUsesGroups}, './FileInfoPanel': {NativeFileInfoPanel: 'FileInfoPanel'}, './playback-info': {readNativePlaybackInfo},
  './track-identity': {nativeWatchTracks}, './EpisodePanel': episodes,
  './title-controller': {NativeTitleController}, './title-starts': {NativeTitleStarts}, './TitleScreen': {NativeTitleScreen: 'Title'},
  './version-destination': {NativeVersionChooserController}, './version-options': {nativeVersionChoices, nativeVersionNames},
  './WatchHost': {NativeWatchHost: 'WatchHost'}, './WatchChrome': {NativeWatchChrome: 'Chrome'}, './picture-slot': {NativeWatchPicture: 'Picture'},
});
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const entry = {id: 'episode', kind: 'episode', title: 'Actual episode', libraryId: 'library', playback: {itemId: 'episode', startSeconds: 17}};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'episode', sourceId: 'v1', entryId: 'entry', generation: 1});
const offered = {itemId: 'episode', resume: {positionMs: 17000, partIndex: 0}, versions: [{id: 'v1', video: [{id: 'p1', codec: 'hevc', width: 3840, height: 2160}]}, {id: 'v2', video: [{id: 'p2', codec: 'h264', width: 1920, height: 1080, interlaced: false}]}]};
/** The accepted Detail the menu showed: its `fileInfoOpening` is the only way into File info. */
const withOpening = {item: {id: 'episode', kind: 'episode', title: 'Actual episode'}, scope: {serverId: 'server', libraryId: 'library', itemId: 'episode', viewerFence: 'viewer'},
  revision: {catalog: 1, viewer: 1}, fileInfoOpening: 'o1'} as any;
const itemFence = {serverId: 'server', libraryId: 'library', itemId: 'episode', viewerFence: 'viewer'};
const episodeGroups = (queryId = 'q1', window: Record<string, unknown> = {}) => ({...groupsPage(queryId, ownerSurface, [groupRow(1)], window), scope: itemFence, detail: {scope: itemFence, revision: withOpening.revision}});
function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};}
function fixture(hasDestination = true, readSession?: NativePlaybackAdapter['readSession']) {
  const playbackListeners = new Set<() => void>(), routeListeners = new Set<() => void>(), authorityListeners = new Set<() => void>();
  let bound = authority, revision = 3, route: any = {kind: 'title', entry: {id: 'show', libraryId: 'library', kind: 'show'}}, holdStart: Promise<void> | undefined, source = {authority, retired: false, fence: undefined as PlaybackFence | undefined, dock: {local: {expanded: true, fence: undefined as PlaybackFence | undefined}}, playback: {intent: 'playing', phase: 'ready'}, session: undefined as {itemId: string; versionId: string; role?: 'local' | 'group_member'} | undefined};
  let respond = async (_url: URL) => offered as unknown;
  const requests: {path: string; signal?: AbortSignal | null}[] = [], menus: any[] = [], versions: any[] = [], starts: any[] = [], errors: unknown[] = [], navigations: unknown[] = [], enqueues: unknown[][] = [], playlistOpens: unknown[][] = [], fileInfos: unknown[][] = [];
  const api = new HttpLocalApi('https://portico.test', '', async (input, init) => {
    const url = new URL(String(input)); requests.push({path: url.pathname, signal: init?.signal});
    return new Response(JSON.stringify(await respond(url)), {status: 200});
  });
  const subscribe = (listeners: Set<() => void>) => (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};};
  // The real presenter's own Versions opening and destination lifetime, over the
  // same route, authority and playback ports the factory reads. Its constructor
  // is the only source of the opening's identity and currentness checks.
  const navigation = {getSnapshot: () => ({authority: bound, revision, route}), subscribe: subscribe(routeListeners), back() {}, open(entry: unknown) {navigations.push(entry); return true;}};
  const playback = {getSnapshot: () => source, subscribe: subscribe(playbackListeners), start: async (input: unknown) => {starts.push(input); await holdStart;}, cataloguePlayback: () => ({enqueueSegments: async (...args: unknown[]) => {enqueues.push(args); return {} as never;}}), ...(readSession ? {readSession} : {})};
  const common = new NativeScreenServices({navigation, current: () => bound === authority, authority: () => bound, subscribeAuthority: subscribe(authorityListeners)}, async () => 'request');
  const presenters = new NativeWatchPresenters({api, navigation, playback, current: () => bound === authority, authority: () => bound,
    subscribeAuthority: subscribe(authorityListeners)} as unknown as NativeShellBootstrapContext, common);
  const factory = createNativeWatchScreens({api, scope: {serverId: 'server', viewerId: 'viewer'}, authority, current: () => bound,
    subscribeCurrent: subscribe(authorityListeners), navigation, playback,
    playlistPicker: {open: (...args: unknown[]) => {playlistOpens.push(args);}, openAlbum: () => {}, render: () => null, dispose: () => {}}, watch: {register() {throw new Error('not mounted');}}, requestId: async () => 'request', isTV: false,
    startVersion: async (...args: unknown[]) => {starts.push(args);}, manageFile() {}, openFileInfo: (...args: unknown[]) => {fileInfos.push(args);},
    ...(hasDestination ? {
      // Record the exact opening the factory dispatched, then let the real
      // presenter present that destination under its own current() guard.
      openVersions: (capture: NativeWatchCapture, destination: NativeVersionDestination, opening: NativeVersionsOpening) => {
        versions.push([capture, destination, opening]);
        presenters.openVersions(capture, destination, opening);
      },
      beginVersions: presenters.beginVersions,
    } : {}),
    openMenu: (...args: unknown[]) => {menus.push(args);},
    title: {aboutLabel: 'About', navigate(entry: unknown) {navigations.push(entry);}, myList() {}, renderError() {}, reportError: (error: unknown) => {errors.push(error);}},
  });
  // The fixture's existing dispose is the one cleanup every test already calls,
  // so it retires the presenter and its services before the subscription counts.
  const screens = {...factory, dispose: () => {factory.dispose(); presenters.dispose(); common.dispose();}};
  return {screens, requests, fileInfos, menus, versions, starts, errors, navigations, enqueues, playlistOpens,
    session: (role?: 'local' | 'group_member') => {source = {...source, session: role ? {itemId: entry.id, versionId: 'v1', role} : undefined}; for (const listener of playbackListeners) listener();},
    holdStart: (held: Promise<void>) => {holdStart = held;},
    title: () => {route = {kind: 'title', entry}; revision++; for (const listener of routeListeners) listener(); return screens.title.render(route, {} as any) as any;},
    respond: (value: typeof respond) => {respond = value;},
    failure: (versionId: string) => {source = {...source, playback: {...source.playback, phase: 'error'}, session: {itemId: entry.id, versionId}};},
    source: (next: PlaybackFence) => {source = {...source, fence: next, dock: {local: {...source.dock.local, fence: next}}}; for (const listener of playbackListeners) listener();},
    route: () => {revision++; for (const listener of routeListeners) listener();},
    authority: () => {bound = {...bound, epoch: 'next'}; for (const listener of authorityListeners) listener();},
    subscriptions: () => playbackListeners.size + routeListeners.size + authorityListeners.size,
  };
}

test('the Watch factory aborts its Info read on caller, route, authority or factory retirement', async () => {
  for (const retire of ['caller', 'route', 'authority', 'factory'] as const) {
    const result = deferred<undefined>(); let signal: AbortSignal | undefined;
    const d = fixture(true, async (captured, lifetime) => {assert.deepEqual(captured, fence); signal = lifetime.signal; return result.promise;});
    try {
      d.source(fence); const caller = new AbortController();
      const pending = d.screens.readPlaybackInfo(fence, infoWords().words, {signal: caller.signal, isCurrent: () => true});
      assert.ok(signal, 'the factory borrows the actual installed read port');
      if (retire === 'caller') caller.abort(); else if (retire === 'route') d.route();
      else if (retire === 'authority') d.authority(); else d.screens.dispose();
      assert.equal(signal.aborted, true, retire);
      result.resolve(undefined); assert.equal(await pending, undefined);
    } finally {d.screens.dispose();}
    assert.equal(d.subscriptions(), 0);
  }
});

test('episode menu retains its real show owner and sends the exact offered version and resume position', async () => {
  const d = fixture();
  try {
    await d.screens.menu(entry, () => true);
    assert.equal(d.requests[0]?.path, '/v1/items/episode/playback-options');
    await d.menus[0][1].differentVersion();
    assert.equal(d.requests.length, 2);
    const destination = d.versions[0][1];
    assert.equal(destination.rows[1].heading, '1080p', 'actual JSON Options preserves the supplied progressive scan fact');
    await destination.choose('unknown'); assert.equal(d.starts.length, 0);
    await destination.choose('v2');
    assert.equal(d.starts[0][0].target.startSeconds, 17);
    assert.equal(d.starts[0][1].versionId, 'v2');
    assert.equal(d.starts[0][1].options.itemId, 'episode');
    assert.equal(d.starts[0][1].expectedFence, undefined);
    d.route(); await destination.choose('v1'); assert.equal(d.starts.length, 1); assert.equal(destination.isCurrent(), false);
  } finally {d.screens.dispose();}
  assert.equal(d.subscriptions(), 0);
});

test('no destination action is offered until a genuine approved presenter is installed', async () => {
  const d = fixture(false);
  try {
    await d.screens.menu(entry, () => true);
    assert.equal(d.menus[0][1].differentVersion, undefined);
    assert.equal(typeof d.menus[0][1].fileInfo, 'function');
    const captured = d.screens.capture(entry, () => true);
    await d.screens.versions(captured);
    assert.equal(d.requests.length, 1); assert.equal(d.versions.length, 0); assert.equal(d.starts.length, 0);
  } finally {d.screens.dispose();}
});

test('genuine version eligibility does not require optional display metadata or invent missing facts', async () => {
  const d = fixture();
  d.respond(async () => ({...offered, versions: [{id: 'v1'}, {id: 'v2'}]}));
  try {
    await d.screens.menu(entry, () => true);
    assert.equal(typeof d.menus[0][1].differentVersion, 'function');
    await d.menus[0][1].differentVersion();
    const destination = d.versions[0][1];
    assert.deepEqual(destination.rows, [{id: 'v1', heading: '', detail: ''}, {id: 'v2', heading: '', detail: ''}]);
    await destination.choose('v2');
    assert.equal(d.starts[0][1].versionId, 'v2');
    assert.equal(d.starts[0][1].options.versions.length, 2);
  } finally {d.screens.dispose();}
});

test('opening the chooser refreshes the canonical offer and withholds a removed version group', async () => {
  const d = fixture();
  try {
    await d.screens.menu(entry, () => true);
    d.respond(async () => ({...offered, versions: [{id: 'v1'}]}));
    await d.menus[0][1].differentVersion();
    assert.equal(d.requests.length, 2); assert.equal(d.versions.length, 0); assert.equal(d.starts.length, 0);
  } finally {d.screens.dispose();}
});

test('delayed options cannot open after route/authority/absent-to-new source or disposal', async () => {
  for (const change of ['route', 'authority', 'source', 'dispose'] as const) {
    const d = fixture(), held = deferred<unknown>();
    d.respond(async () => held.promise);
    const request = d.screens.menu(entry, () => true);
    await new Promise(resolve => setImmediate(resolve));
    if (change === 'source') d.source(fence); else if (change === 'dispose') d.screens.dispose(); else d[change]();
    assert.equal(d.requests[0]?.signal?.aborted, true);
    held.resolve(offered); await request;
    assert.equal(d.menus.length, 0); assert.equal(d.versions.length, 0);
    d.screens.dispose();
  }
});

test('one/duplicate/removed versions never grant a chooser; options failure keeps File info independent', async () => {
  for (const versions of [[{id: 'v1'}], [{id: 'v1'}, {id: 'v1'}]]) {
    const d = fixture(); d.respond(async () => ({...offered, versions}));
    try {await d.screens.menu(entry, () => true); assert.equal(d.menus[0][1].differentVersion, undefined);} finally {d.screens.dispose();}
  }
  const d = fixture(); let current = true;
  d.respond(async () => {throw new Error('actual options read failed');});
  try {
    await d.screens.menu(entry, () => current);
    assert.equal(typeof d.menus[0][1].fileInfo, 'function');
    assert.equal(d.menus[0][1].differentVersion, undefined);
    current = false; await d.menus[0][1].fileInfo(withOpening);
    assert.equal(d.requests.length, 1, 'a stale capture reads nothing');
  } finally {d.screens.dispose();}
});


test('File info opens from the accepted Detail\'s opening: first window read, session handed to the overlay', async () => {
  const d = fixture(); d.respond(async () => episodeGroups());
  try {
    await d.screens.menu(entry, () => true);
    const before = d.requests.length;
    await d.menus[0][1].fileInfo(withOpening);
    assert.deepEqual(d.requests.slice(before).map(request => request.path), ['/v1/items/episode/file-info/groups', '/v1/file-info-queries/q1']);
    assert.equal(d.fileInfos.length, 1);
    const [captured, panel, owner] = d.fileInfos[0] as [any, any, any];
    assert.equal(captured.entry, entry);
    assert.equal(panel.type, 'FileInfoPanel'); assert.equal(panel.props.session, owner.session);
    assert.ok(owner.session instanceof FileInfoSession); assert.equal(owner.kind, 'episode'); assert.equal(typeof owner.changed, 'function');
    assert.equal(owner.session.opening.token, 'o1');
    assert.deepEqual(owner.session.opening.revision, withOpening.revision);
    assert.equal(owner.session.getSnapshot().groups.rows.rows[0].id, 'g1');
    owner.session.retire();
  } finally {d.screens.dispose();}
});

test('File info without an accepted opening rejects as unavailable and reads nothing', async () => {
  for (const detail of [undefined, {...withOpening, fileInfoOpening: undefined}, {...withOpening, item: {...withOpening.item, id: 'another'}}]) {
    const d = fixture();
    try {
      await d.screens.menu(entry, () => true);
      const before = d.requests.length;
      await assert.rejects(d.menus[0][1].fileInfo(detail), (error: any) => error.code === 'metadata_unavailable' && error.retryable === true);
      assert.equal(d.requests.length, before); assert.equal(d.fileInfos.length, 0);
    } finally {d.screens.dispose();}
  }
});

test('a failed first window is thrown, not opened, and an aborted one opens nothing', async () => {
  const d = fixture();
  try {
    await d.screens.menu(entry, () => true);
    d.respond(async () => new Response(JSON.stringify({code: 'server_error'}), {status: 500}) as unknown);
    await assert.rejects(d.menus[0][1].fileInfo(withOpening));
    assert.equal(d.fileInfos.length, 0);
  } finally {d.screens.dispose();}
  const held = deferred<unknown>(), e = fixture();
  try {
    await e.screens.menu(entry, () => true);
    e.respond(async () => held.promise);
    const reading = e.menus[0][1].fileInfo(withOpening);
    await new Promise(resolve => setImmediate(resolve));
    e.authority(); held.resolve(episodeGroups());
    await reading;
    assert.equal(e.fileInfos.length, 0);
  } finally {e.screens.dispose();}
});

test('menu forwards the measured card ref and retains the real owner lifetime', async () => {
  const d = fixture(), anchor = {current: {nativeTag: 37}}; let memberCurrent = true;
  try {
    await d.screens.menu(entry, () => memberCurrent, anchor);
    assert.equal(d.menus[0][2], anchor);
    assert.equal(d.menus[0][0].entry, entry);
    memberCurrent = false; await d.menus[0][1].fileInfo(withOpening);
    assert.equal(d.requests.length, 1, 'a stale capture reads nothing');
    assert.equal(d.fileInfos.length, 0);
  } finally {d.screens.dispose();}
});

test('actual title controller start receives pure current route/member lifetime and navigation aborts its pending command', async () => {
  const d = fixture(), held = deferred<void>(); d.holdStart(held.promise);
  try {
    const title = d.title(), pending = title.props.controller.play();
    const input = d.starts[0]; assert.equal(input.target.startSeconds, 17);
    assert.equal(input.lifetime.isCurrent(), true); assert.equal(input.lifetime.signal.aborted, false);
    // The runtime's own new accepted output is not a caller retirement.
    d.source(fence); assert.equal(input.lifetime.isCurrent(), true); assert.equal(input.lifetime.signal.aborted, false);
    d.route(); assert.equal(input.lifetime.signal.aborted, true); assert.equal(input.lifetime.isCurrent(), false);
    held.resolve(); await pending;
  } finally {held.resolve(); d.screens.dispose();}
});


test('failure entry excludes only the actual failed grouped version and may leave one valid row', async () => {
  const d = fixture();
  try {
    d.source(fence); d.failure('v1');
    const capture = d.screens.capture(entry, () => true);
    await d.screens.versions(capture, 'v2'); assert.equal(d.versions.length, 0, 'an invented failed version cannot be excluded');
    await d.screens.versions(capture, 'v1');
    assert.deepEqual(d.versions[0][1].rows.map((row: any) => row.id), ['v2']);
    await d.versions[0][1].choose('v1'); assert.equal(d.starts.length, 0);
    await d.versions[0][1].choose('v2'); assert.equal(d.starts[0][1].versionId, 'v2');
    assert.ok(d.starts[0][1].lifetime.signal instanceof AbortSignal);
  } finally {d.screens.dispose();}
});

test('menu carries the captured title real personal actions, its own Play target and the title route', async () => {
  detailProjection = {
    item: {id: 'episode', kind: 'episode', title: 'Actual episode'}, languages: {audio: [], subtitles: []},
    scope: {serverId: 'server', libraryId: 'library', itemId: 'episode', viewerFence: 'viewer'}, revision: {catalog: 1, viewer: 1}, markers: [],
    personal: {watchlisted: false, favorite: false, watched: false, progressSeconds: 30, revision: 1, rating: null, lastPlayedAt: '', status: 'current', conflicts: []},
    actions: [{id: 'resume', enabled: true, playback: {itemId: 'episode', startSeconds: 30}}, {id: 'watchlist', enabled: true},
      {id: 'favorite', enabled: true}, {id: 'watched', enabled: true}],
    metadata: {genres: [], ratings: [], credits: [], status: 'available'},
  };
  const d = fixture();
  try {
    await d.screens.menu(entry, () => true);
    const menu = d.menus[0][1];
    assert.equal(menu.entry.current().personal.watchlisted, false);
    await menu.entry.play();
    assert.deepEqual(d.starts[0].target, {itemId: 'episode', startSeconds: 30});
    await menu.entry.mutate({action: 'favorite', value: true});
    assert.deepEqual(mutations.at(-1), {action: 'favorite', value: true});
    menu.entry.details?.();
    assert.deepEqual(d.navigations, [entry]);
    menu.entry.dispose();
  } finally {detailProjection = null; d.screens.dispose();}
});

test('an unreadable detail keeps the independent File info menu and offers no personal row', async () => {
  detailProjection = null;
  const d = fixture();
  try {
    await d.screens.menu(entry, () => true);
    assert.equal(typeof d.menus[0][1].fileInfo, 'function');
    assert.equal(d.menus[0][1].entry, undefined);
    assert.equal(d.errors.length, 0, 'the unreadable detail is not a user-facing menu failure');
  } finally {d.screens.dispose();}
});

test('a readable Detail that withholds Play never falls back to the drawn entry playback', async () => {
  for (const actions of [[], [{id: 'resume', enabled: false, playback: {itemId: 'episode', startSeconds: 30}}],
    [{id: 'start_over', enabled: true, playback: {itemId: 'episode', startSeconds: 0}}]]) {
    detailProjection = {
      item: {id: 'episode', kind: 'episode', title: 'Actual episode'}, languages: {audio: [], subtitles: []},
      scope: {serverId: 'server', libraryId: 'library', itemId: 'episode', viewerFence: 'viewer'}, revision: {catalog: 1, viewer: 1}, markers: [],
      personal: {watchlisted: false, favorite: false, watched: false, progressSeconds: 30, revision: 1, rating: null, lastPlayedAt: '', status: 'current', conflicts: []},
      actions, metadata: {genres: [], ratings: [], credits: [], status: 'available'},
    };
    const d = fixture();
    try {
      assert.ok(entry.playback, 'the drawn card still carries an earlier playback target');
      await d.screens.menu(entry, () => true);
      const menu = d.menus[0][1];
      assert.ok(menu.entry, 'the actual Detail projection was read successfully');
      assert.equal(menu.entry.play, undefined, 'absent, disabled, or start-over-only grants do not offer Play');
      assert.equal(typeof menu.fileInfo, 'function', 'the independent information action remains available');
      assert.equal(d.starts.length, 0);
      menu.entry.dispose();
    } finally {detailProjection = null; d.screens.dispose();}
  }
});

test('the shared menu appends through the one catalogue queue and never behind a group session', async () => {
  detailProjection = {
    item: {id: 'episode', kind: 'episode', title: 'Actual episode'}, languages: {audio: [], subtitles: []},
    scope: {serverId: 'server', libraryId: 'library', itemId: 'episode', viewerFence: 'viewer'}, revision: {catalog: 1, viewer: 1}, markers: [],
    personal: {watchlisted: false, favorite: false, watched: false, progressSeconds: 30, revision: 1, rating: null, lastPlayedAt: '', status: 'current', conflicts: []},
    actions: [{id: 'resume', enabled: true, playback: {itemId: 'episode', startSeconds: 30}}], metadata: {genres: [], ratings: [], credits: [], status: 'available'},
  };
  const d = fixture();
  try {
    await d.screens.menu(entry, () => true);
    await d.menus[0][1].entry.enqueue('next');
    assert.deepEqual(d.enqueues[0][0], [{source: {items: {ids: ['episode']}}}]);
    assert.equal(d.enqueues[0][1], 'next');
    assert.ok((d.enqueues[0][2] as any).signal instanceof AbortSignal, 'the append carries the captured lifetime signal');
    d.session('group_member');
    await d.screens.menu(entry, () => true);
    assert.equal(d.menus[1][1].entry.enqueue, undefined, 'a group/follower session never offers a personal append');
  } finally {detailProjection = null; d.screens.dispose();}
});

test('a song menu reaches its real artist entry and the menu carries the queue row', async () => {
  const song = {id: 'song-1', kind: 'song', title: 'Actual song', libraryId: 'library', artist: {id: 'artist-1', name: 'Actual artist'}};
  detailProjection = {
    item: song, languages: {audio: [], subtitles: []}, scope: {serverId: 'server', libraryId: 'library', itemId: song.id, viewerFence: 'viewer'},
    revision: {catalog: 1, viewer: 1}, markers: [], personal: {watchlisted: false, favorite: false, watched: false, progressSeconds: 0, revision: 1, rating: null, lastPlayedAt: '', status: 'current', conflicts: []},
    actions: [], metadata: {genres: [], ratings: [], credits: [], status: 'available'},
  };
  const d = fixture();
  try {
    await d.screens.menu(song as never, () => true);
    const entryMenu = d.menus[0][1].entry;
    assert.equal(typeof entryMenu.enqueue, 'function');
    entryMenu.goToArtist();
    assert.deepEqual(d.navigations, [{id: 'artist-1', kind: 'artist', title: 'Actual artist', libraryId: 'library'}]);
  } finally {detailProjection = null; d.screens.dispose();}
});

test('the shared leaf menu opens the real playlist picker only for playable leaves', async () => {
  detailProjection = {
    item: {id: 'episode', kind: 'episode', title: 'Actual episode'}, languages: {audio: [], subtitles: []},
    scope: {serverId: 'server', libraryId: 'library', itemId: 'episode', viewerFence: 'viewer'}, revision: {catalog: 1, viewer: 1}, markers: [],
    personal: {watchlisted: false, favorite: false, watched: false, progressSeconds: 0, revision: 1, rating: null, lastPlayedAt: '', status: 'current', conflicts: []},
    actions: [], metadata: {genres: [], ratings: [], credits: [], status: 'available'},
  };
  const d = fixture();
  try {
    await d.screens.menu(entry, () => true);
    const menu = d.menus[0][1].entry;
    assert.equal(typeof menu.addToPlaylist, 'function');
    menu.addToPlaylist();
    assert.equal(d.playlistOpens[0][0], 'episode');
    const lifetime = d.playlistOpens[0][1] as {signal: AbortSignal; isCurrent: () => boolean};
    assert.equal(lifetime.signal.aborted, false); assert.equal(lifetime.isCurrent(), true);
    const album = {...entry, id: 'album-1', kind: 'album'};
    await d.screens.menu(album as never, () => true);
    assert.equal(d.menus[1][1].entry.addToPlaylist, undefined, 'a container never offers the leaf picker');
  } finally {detailProjection = null; d.screens.dispose();}
});
