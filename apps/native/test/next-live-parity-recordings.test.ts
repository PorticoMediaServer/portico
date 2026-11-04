import assert from 'node:assert/strict';
import test from 'node:test';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n, catalogue, enUS} from '../../../packages/i18n/src';
import * as dvr from '../../../packages/client-core/src/dvr';
import * as channels from '../../../packages/client-core/src/channel-guide';
import * as guide from '../../../packages/client-core/src/guide';
import * as directory from '../../../packages/client-core/src/guide/directory';
import * as kitModel from '../../../packages/client-core/src/presentation/kit-model';
import * as guidePresentation from '../src-next/screens/settings-live/guide-presentation';
import * as guidePan from '../src-next/screens/settings-live/guide-pan-model';
import * as rules from '../src-next/screens/settings-live/recording-rule-presentation';
import * as overlap from '../src-next/screens/settings-live/recording-overlap-window';
import * as entryLifetime from '../src-next/screens/home/entry-lifetime';
import * as mediaItem from '../../../packages/client-core/src/media-item';
import * as content from '../../../packages/client-core/src/library-content';
import * as routes from '../../../packages/contracts/src/routes';
import * as titlePage from '../../../packages/client-core/src/presentation/title-page';
import * as shellModel from '../src-next/shell/model';
import {tvFocusIds} from '../src-next/shell/tv-focus';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {stateSentenceParts} from '../src-next/kit/states/sentence-parts';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeRecordingsController} from '../src-next/screens/settings-live/recordings-controller';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';
import {nativeServerAdmission} from '../src-next/screens/settings-live/server/tv-server-reader';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus';
const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree ? [tree, ...nodes(tree.children)] : [];
const noop = () => {}, subscribe = () => noop;
const jsx = (type: any, props: any) => React.createElement(type, props);

// Copied from next-live-tv-management.test.ts: the installed Fabric reconciler runs actual screen branches/effects. Native host
// widgets and editor outlets are controlled; even a closed editor mount is observed.
async function fixture({isTV = true, role = 'owner', primary = 'main', canManage = true, disabled = false, failed = false, populated = false, recordAvailable = true, realRecordings = false, origin = 'https://server.example/'} = {}) {
  const host = await fabricHost(), listeners = new Set<() => void>(), admin: string[] = [], editors: string[] = [], backs = new Set<() => boolean>();
  const retirement = new AbortController(); let disposed = false, disposedReads = 0;
  let theme = createTheme({isTV, width: isTV ? 1920 : 400}), current = true;
  let currentProbe: (() => void) | undefined;
  let authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'one', viewerFence: 'fence'};
  let route: any = {revision: 1, route: {kind: 'main'}, destination: {kind: 'live'}};
  let shell: any = {authority, destination: route.destination, contentVisible: true, mediaExpanded: false};
  const listen = (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => {currentProbe?.(); return current;}, subscribeAuthority: listen});
  const api = {request: async <T,>(path: string) => {
    if (path.includes('/admin/')) {admin.push(path); throw new Error('No TV administration allowed');}
    if (failed) throw new Error('Guide unavailable');
    return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'guide', revision: 'one', sources: populated ? [{id: 'source', name: 'A real source', generation: 'one', publishedAt: '2026-10-26T00:00:00Z', availableStart: '2026-10-26T00:00:00Z', availableEnd: '2026-10-28T00:00:00Z', provenance: 'live-source', refreshState: 'ready', position: 0, recordAvailable, guideDays: 2, channelCount: 0, favoriteCount: 0, groups: [], groupCounts: [], unavailableReasons: {}}] : [], total: 0, channels: [], nextCursor: ''} as T;
  }};
  const controller = new NativeLiveController(lifetime, api, 'America/Halifax'); await controller.loadSources();
  if (populated) {assert.equal(controller.getSnapshot().sources.length, 1); assert.equal(controller.getSnapshot().error, undefined);}
  if (failed) assert.ok(controller.getSnapshot().error);
  const owner = new NativeLiveOwnerController(lifetime, api, () => 'operation');
  const account: DirectSnapshot = {authority: 'local', serverId: 'server', canManage,
    capabilities: {manageAccount: canManage, managePeople: canManage && role !== 'member', manageServer: canManage && role !== 'member', manageSignIn: canManage && role === 'owner'},
    account: {id: 'owner', username: 'Owner', role: role as DirectSnapshot['account']['role'], primaryProfileId: primary, revision: 1, disabled, allowedLibraries: []},
    profiles: [{id: primary, name: 'Main', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 0, pinRequired: false}]};
  const context: any = {api: {baseUrl: origin}, account, authority: () => authority,
    current: () => current, navigation: {subscribe: listen, getSnapshot: () => {if (disposed) {disposedReads++; throw new Error('native_shell_cancelled');} return route;}, isCurrent: () => current}};
  const runtime: any = {retirementSignal: retirement.signal, subscribe: listen, getSnapshot: () => {if (disposed) {disposedReads++; throw new Error('native_shell_cancelled');} return shell;}, text: {t: defaultI18n.t, locale: 'en-US'},
    error: (_: unknown, retry: () => void) => ({title: 'Unavailable', primaryAction: {id: 'retry', label: 'Retry', onPress: retry}}), reportError: (error: unknown) => {throw error;}, onDestination: noop};
  const useScreenBack = (_: unknown, fn: () => boolean, enabled: boolean) => React.useEffect(() => {if (enabled) {backs.add(fn); return () => {backs.delete(fn);};}}, [fn, enabled]);
  const pageBack = {enabled: false, page: undefined as any}, entries: any[] = [];
  const native = {View: 'View', Text: 'Text', ScrollView: 'ScrollView', FlatList: 'FlatList', VirtualizedList: 'VirtualizedList', Platform: {OS: 'android'},
    BackHandler: {addEventListener: (_: string, fn: () => boolean) => {backs.add(fn); return {remove: () => backs.delete(fn)};}}, useWindowDimensions: () => ({width: theme.width, height: 1080})};
  const controls = {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Segmented: 'Segmented', Select: 'Select', Switch: 'Switch'};
  const focusRequests: unknown[] = []; let guideCalls = 0;
  const focus = {FocusRegion: 'Region', FocusItem: 'FocusItem', useCovered: () => false, useFocusRegistry: () => ({subscribe, cellPosition: () => undefined, attachedFocus: () => undefined, focus: (target: unknown) => {focusRequests.push(target); return true;}})};
  const handoff = await screenModule(new URL('../src-next/tv/auth/TVHandoff.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': native, 'react-native-qrcode-svg': {default: 'QR'}, '../../../../../packages/i18n/src': {defaultI18n, catalogue, enUS},
    '../../kit/controls': controls, '../../kit/focus': focus, '../../kit/theme': {useTheme: () => theme, nativeType}, '../../kit/states/sentence-parts': {stateSentenceParts},
    '../../platform/native-blur': {}, '../../screens/destinations/useEntryFocus': {}, './TVAuthLayout': {}, './entry-revision': {},
  });
  const common: any = {react: {...React, default: React}, 'react-native': native,
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Pattern: 'Pattern', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/dvr': dvr, '../../../../../packages/client-core/src/presentation/kit-model': kitModel,
    '../../kit/controls': controls, '../../kit/focus': focus, '../../kit/overlays': {Banner: 'Banner', Dialog: 'Dialog', Menu: 'Menu', ToastHost: 'ToastHost'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {Quiet: ({children}: any) => children, StateView: (props: any) => jsx('StateView', props)}, '../../kit/shell/visual': {alpha: (value: string) => value, shellType: () => ({})},
    '../../kit/theme': {useTheme: () => theme}, '../../shell/tv-focus': {tvFocusIds}, './Screen': {Screen: 'Screen', ScreenText: 'ScreenText', useScreenBack, useScreenEntry: () => noop}, './recording-rule-presentation': rules};
  const recordingModule = await screenModule(new URL('../src-next/screens/settings-live/RecordingsScreen.tsx', import.meta.url), {...common,
    '../../../../../packages/client-core/src/presentation/title-page': titlePage, '../../../../../packages/client-core/src/media-item': mediaItem, '../../../../../packages/client-core/src/library-content': content, '../../../../../packages/contracts/src/routes': routes,
    './recording-overlap-window': overlap, '../home/entry-lifetime': entryLifetime, './LiveOwnerRecording': {NewRuleSheet: (props: any) => {editors.push(`new-rule:${props.open}`); return jsx('NewRuleEditor', props);}},
  });
  let recordingState: any = {query: {view: 'upcoming'}, loading: false, busy: false, accessDenied: false, page: {recordings: [], rules: [], captureAvailable: true}};
  // A real controller publishes a new frozen snapshot; a drawn row therefore keeps the exact
  // window it was drawn for. Mutating one snapshot in place would let a stale handler read the
  // successor window instead, which no real getSnapshot does.
  const publishRecordingState = (next: any) => {recordingState = next;};
  const viewerCalls: any[] = [], viewerLoads: any[] = [];
  let fakeOpening: object | undefined;
  const fakeRecordings: any = {lifetime, beginViewerOpening: (opening: object) => {fakeOpening = opening;}, endViewerOpening: (opening: object) => {if (fakeOpening === opening) fakeOpening = undefined;}, viewerOpeningCurrent: (opening: object) => fakeOpening === opening, subscribe: listen, getSnapshot: () => recordingState, load: async (query: unknown) => {viewerLoads.push(query);}, loadFacts: async () => {}, detail: () => new Promise(() => {}), nextViewerPage: async (window: unknown) => {viewerCalls.push(window);}, previousViewerPage: async () => {}, retryViewerPage: async () => {}, viewerFocusToken: () => undefined, focusViewerRow: () => {}, blurViewerRow: () => {}, focusViewerHost: () => {}, pinViewerRow: () => true, unpinViewerRow: () => {}};
  fakeRecordings.detailOpening = (recording: any) => ({read: () => fakeRecordings.detail(recording.id), canResolve: () => false, resolve: async () => undefined, dispose: noop});
  const pageCalls: (string | undefined)[] = []; let failNext = false; let finishPage: ((value: any) => void) | undefined;
  const firstRecording = {id: 'first', revision: 1, state: 'completed', itemId: 'item', libraryId: 'library', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', programme: {id: 'p', title: 'Programme'}, options: {priority: 0}};
  const firstPage = {recordings: [firstRecording], rules: [], captureAvailable: true, revision: 1, nextCursor: 'next'};
  const realService: any = {onAccessChanged: noop, dispose: noop, list: async (_view: unknown, cursor?: string) => {pageCalls.push(cursor); if (cursor && failNext) {failNext = false; throw new Error('offline');} return cursor ? new Promise(resolve => {finishPage = resolve;}) : firstPage;}, summary: async () => ({upcoming: 2, recorded: 0, rules: 0}), storage: async () => ({policy: {revision: 1, retentionDays: 0, episodeLimit: 0, floorBytes: 0, capBytes: 0}, measurement: {freeBytes: 0, usedBytes: 0, reservedBytes: 0, writeHealthy: true, measuredAt: '2026-10-26T00:00:00Z'}, pendingDeleteBytes: 0, forecastBytes: 0, forecastHours: 1, forecastDescription: '', captureAvailable: true, warning: ''})};
  const recordings: any = realRecordings ? new NativeRecordingsController(lifetime, realService) : fakeRecordings;
  let activeOpening: object | undefined;
  if (realRecordings) {const begin = recordings.beginViewerOpening; recordings.beginViewerOpening = (opening: object) => {activeOpening = opening; begin(opening);};}
  const {NativeLiveScreen} = await screenModule(new URL('../src-next/screens/settings-live/LiveScreen.tsx', import.meta.url), {...common,
    './tv-settings-focus': tvSettingsFocus,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, 'react-native-gesture-handler': {GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn()}, '../../../../../packages/client-core/src/channel-guide': channels,
    '../../../../../packages/client-core/src/guide': guide, '../../../../../packages/client-core/src/guide/directory': directory, './GuideLoadingMarker': {GuideLoadingMarker: 'Loading'},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, '../../platform/tv-keys': {useTVRemoteActivity() {}}, '../../shell/model': shellModel, './RecordingsScreen': recordingModule, '../../tv/layout/guide-focus': {},
    '../destinations/useTVPageBack': {useTVPageBack: (_r: unknown, _d: unknown, enabled: boolean, page: any) => {pageBack.enabled = enabled; pageBack.page = page;}},
    '../destinations/useEntryFocus': {useDestinationEntryFocus: (...args: any[]) => entries.push(args)}, './guide-presentation': guidePresentation,
    '../../kit/useScreenReader': {useScreenReader: () => false},
    './useGuideTimePan': {useGuideTimePan: () => ({active: false, position: {value: 0}})}, '../../../../../packages/client-core/src/presentation/live-availability': {channelUnavailableMessage: () => undefined, liveServerWideNotice: () => undefined, liveGuideStale: () => false}, './guide-pan-model': guidePan, './LivePhonePicker': {NativeLivePhonePicker: 'PhonePicker'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: (props: any) => {editors.push(`owner:${props.initialAction}`); return jsx('OwnerEditor', props);}},
    './LiveSeriesRule': {NativeSeriesRuleSheet: 'SeriesRule'}, './server/tv-server-reader': {nativeServerAdmission}, '../../tv/auth': handoff, '../../tv/auth/TVHandoff': handoff,
  });
  const ports = {context};
  let showingRecordings = false;
  const render = () => host.render(showingRecordings ? jsx(recordingModule.NativeRecordingsScreen, {controller: recordings, ports, runtime, owner, onGuide: () => {guideCalls++;}}) : jsx(NativeLiveScreen, {controller, recordings, owner, ports, runtime})); await render();
  const all = () => nodes(host.root), find = (id: string) => all().find(node => node.props?.testID === id);
  return {host, all, find, admin, editors, backs, pageBack, entries, get recordingState() {return recordingState;}, publishRecordingState, viewerCalls, viewerLoads, context, recordingsController: recordings, runtime, focusRequests, guideCalls: () => guideCalls, opening: () => activeOpening, armCurrent: (after: number, action: () => void) => {let count = 0; currentProbe = () => {if (++count === after) {currentProbe = undefined; action();}};}, pageCalls, failNextPage: () => {failNext = true;}, finishPage: (value: unknown) => {assert.ok(finishPage); finishPage(value);}, firstPage,
    notify() {for (const fn of [...listeners]) fn();},
    disposeRuntime() {retirement.abort(); disposed = true;}, disposedReads: () => disposedReads,
    async press(id = 'live-owner-open') {const node = find(id); assert.ok(node, id); assert.ok(!node.props.disabled); node.props.onPress(); await host.settle();},
    async action(which: string) {const action = find('live-no-sources')?.props[which]; assert.ok(action); action.onPress(); await host.settle();},
    async replace(change: string) {
      if (change === 'viewer') {authority = {...authority, profileId: 'other'}; shell = {...shell, authority};}
      if (change === 'route') route = {...route, revision: route.revision + 1};
      if (change === 'secondary') context.account = {...context.account, account: {...context.account.account, primaryProfileId: 'other'}};
      if (change === 'disabled') context.account = {...context.account, account: {...context.account.account, disabled: true}};
      if (change === 'canManage') context.account = {...context.account, canManage: false};
      if (change === 'hidden') shell = {...shell, contentVisible: false};
      if (change === 'retire') current = false;
      for (const fn of [...listeners]) fn(); await render();
    },
    async tv() {theme = createTheme({isTV: true, width: 1920}); await render();},
    async recordings() {showingRecordings = true; await render();},
    async close() {await host.unmount(); if (realRecordings) recordings.dispose(); controller.dispose(); owner.dispose(); lifetime.dispose();},
  };
}

const elements = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(elements) : tree && typeof tree === 'object' && tree.props ? [tree, ...elements(tree.props.children)] : [];
const row = (extra: object = {}) => ({id: 'recording', revision: 1, state: 'completed', keep: false, itemId: 'item', libraryId: 'library', occurrence: {channelId: 'channel'}, reason: '', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', programme: {id: 'p', title: 'Programme', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z'}, options: {priority: 0}, ...extra});
const windowOf = (rows: any[]) => ({scope: {}, generation: 1, items: rows.map(value => ({kind: 'recording', value})), chunks: [{cursor: '', items: []}], bookmarks: [''], nextCursor: '', pending: false, refreshRequired: false});
const summary = {upcoming: 0, recorded: 3, rules: 0, groups: 2, unwatched: 1};

for (const isTV of [false, true]) test(`${isTV ? 'TV' : 'phone'} Live TV draws the Recordings entry when no source reports it can record`, async () => {
  const f = await fixture({isTV, role: 'member', canManage: false, populated: true, recordAvailable: false}); try {
    const entry = f.find('live-recordings'); assert.ok(entry, 'the entry is drawn so the can’t-record state is reachable');
    await f.press('live-recordings'); await f.host.settle();
    assert.ok(!f.find('live-recordings'), 'the entry opened Recordings');
  } finally {await f.close();}
});

test('Recordings entry follows web: absent with no sources and no way to add one, present for someone who manages the server', async () => {
  const member = await fixture({isTV: false, role: 'member', canManage: false}); try {assert.equal(member.find('live-recordings'), undefined);} finally {await member.close();}
  const owner = await fixture({isTV: false}); try {assert.ok(owner.find('live-recordings'));} finally {await owner.close();}
});

test('a server that cannot record shows its unavailable state inside Recordings', async () => {
  const f = await fixture({isTV: false, role: 'member', canManage: false, populated: true, recordAvailable: false}); try {
    f.recordingState.page = {recordings: [], rules: [], captureAvailable: false};
    await f.press('live-recordings'); await f.host.settle();
    const state = f.all().find(node => node.type === 'StateView' && node.props.title === defaultI18n.t('recordings.unavailableTitle'));
    assert.ok(state); assert.equal(state.props.body, defaultI18n.t('recordings.unavailableBody'));
  } finally {await f.close();}
});

// phone-app's Recordings frame draws no Sort and no per-row ⋯ (the lead's call: undrawn web parity is left out).
// TV keeps its drawn header, Sort and hold-OK menu; touch rows keep tap (details) and long press (the same menu).
for (const isTV of [false, true]) test(`${isTV ? 'TV' : 'phone'} Recorded: ${isTV ? 'the header and Sort open its side sheet' : 'no header Sort, as the frame draws'}`, async () => {
  const f = await fixture({isTV, role: 'member', canManage: false}); try {
    const group = {key: 'show', title: 'Show', recordings: 2, unwatched: 1, kept: 0, exceptions: 0, recording: row()};
    f.recordingState.query = {view: 'recorded'}; f.recordingState.summary = summary; f.recordingState.page = {recordings: [], rules: [], captureAvailable: true};
    f.recordingState.viewerWindow = {...windowOf([]), items: [{kind: 'group', value: group}]};
    await f.recordings();
    const sort = f.all().find(node => node.type === 'Chip' && node.props.testID === 'recordings-sort');
    if (!isTV) {assert.equal(sort, undefined); return;}
    assert.ok(sort, 'Sort chip');
    const sheet = () => f.all().find(node => node.type === 'Menu' && node.props.testID === 'recordings-sort')!;
    assert.equal(sheet().props.open, false); assert.equal(sheet().props.presentation, 'sideSheet');
    sort.props.onPress(); await f.host.settle(); assert.equal(sheet().props.open, true);
    assert.deepEqual(sheet().props.items.map((item: any) => item.id), ['recent', 'title', 'oldest']);
    sheet().props.items[1].onSelect(); await f.host.settle();
    assert.deepEqual(f.viewerLoads.at(-1), {view: 'recorded', sort: 'title'});
  } finally {await f.close();}
});

test('recording rows draw no ⋯ on any platform; touch keeps tap for details and long press for the menu', async () => {
  for (const isTV of [false, true]) {
    const f = await fixture({isTV, role: 'member', canManage: false}); try {
      const recording = row(); f.recordingState.page = {recordings: [recording], rules: [], captureAvailable: true, deletionAvailable: true}; f.recordingState.viewerWindow = windowOf([recording]);
      await f.recordings(); const list = f.all().find(node => node.type === 'FlatList')!;
      const rendered = list.props.renderItem({item: {kind: 'recording', recording}, index: 0});
      const tree = elements(rendered.type({...rendered.props, runtime: f.runtime}));
      assert.equal(tree.find(node => node.props.testID === 'recordings-more-recording'), undefined);
      const rowNode = tree.find(node => node.props.testID === 'recordings-row-recording')!;
      assert.equal(rowNode.props.onLongPressAction, rendered.props.onMenu, 'long press (hold OK on TV) opens the menu');
      assert.equal(rowNode.props.onPress, rendered.props.onOpen, 'a press opens the details');
    } finally {await f.close();}
  }
});

test('a show’s recordings say Kept or Unwatched; other lists do not', async () => {
  const f = await fixture({isTV: false, role: 'member', canManage: false}); try {
    const facts = (tree: any) => elements(tree).filter(node => node.type === 'ScreenText').map(node => node.props.children).filter((value: unknown) => typeof value === 'string');
    const draw = async (query: object, recording: any) => {
      f.recordingState.query = query; f.recordingState.page = {recordings: [recording], rules: [], captureAvailable: true}; f.recordingState.viewerWindow = windowOf([recording]);
      await f.recordings(); const list = f.all().find(node => node.type === 'FlatList')!;
      const rendered = list.props.renderItem({item: {kind: 'recording', recording}, index: 0});
      return facts(rendered.type({...rendered.props, runtime: f.runtime}));
    };
    const show = {view: 'recorded', group: 'show'};
    assert.ok((await draw(show, row({keep: true, watched: false}))).includes('Kept'));
    assert.ok((await draw(show, row({watched: false}))).includes('Unwatched'));
    const watched = await draw(show, row({watched: true})); assert.ok(!watched.includes('Watched') && !watched.includes('Unwatched'), 'frame 22 draws Kept or Unwatched only');
    const unknown = await draw(show, row({watched: null})); assert.ok(!unknown.includes('Unwatched') && !unknown.includes('Kept'));
    const plain = await draw({view: 'history'}, row({keep: true, watched: false})); assert.ok(!plain.includes('Kept') && !plain.includes('Unwatched'));
  } finally {await f.close();}
});
