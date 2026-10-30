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

// The installed Fabric reconciler runs actual screen branches/effects. Native host
// widgets and editor outlets are controlled; even a closed editor mount is observed.
async function fixture({isTV = true, role = 'owner', primary = 'main', canManage = true, disabled = false, failed = false, populated = false, realRecordings = false, origin = 'https://server.example/'} = {}) {
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
    return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'guide', revision: 'one', sources: populated ? [{id: 'source', name: 'A real source', generation: 'one', publishedAt: '2026-10-26T00:00:00Z', availableStart: '2026-10-26T00:00:00Z', availableEnd: '2026-10-28T00:00:00Z', provenance: 'live-source', refreshState: 'ready', position: 0, recordAvailable: true, guideDays: 2, channelCount: 0, favoriteCount: 0, groups: [], groupCounts: [], unavailableReasons: {}}] : [], total: 0, channels: [], nextCursor: ''} as T;
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
  const focus = {FocusRegion: 'Region', FocusItem: 'FocusItem', useFocusRegistry: () => ({subscribe, cellPosition: () => undefined, focus: (target: unknown) => {focusRequests.push(target); return true;}})};
  const handoff = await screenModule(new URL('../src-next/tv/auth/TVHandoff.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': native, 'react-native-qrcode-svg': {default: 'QR'}, '../../../../../packages/i18n/src': {defaultI18n, catalogue, enUS},
    '../../kit/controls': controls, '../../kit/focus': focus, '../../kit/theme': {useTheme: () => theme, nativeType}, '../../kit/states/sentence-parts': {stateSentenceParts},
    '../../platform/native-blur': {}, '../../screens/destinations/useEntryFocus': {}, './TVAuthLayout': {}, './entry-revision': {},
  });
  const common: any = {react: {...React, default: React}, 'react-native': native,
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Pattern: 'Pattern', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/dvr': dvr, '../../../../../packages/client-core/src/presentation/kit-model': kitModel,
    '../../kit/controls': controls, '../../kit/focus': focus, '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu', ToastHost: 'ToastHost'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, '../../kit/Icon': {Icon: 'Icon'},
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
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, '../../shell/model': shellModel, './RecordingsScreen': recordingModule, '../../tv/layout/guide-focus': {},
    '../destinations/useTVPageBack': {useTVPageBack: (_r: unknown, _d: unknown, enabled: boolean, page: any) => {pageBack.enabled = enabled; pageBack.page = page;}},
    '../destinations/useEntryFocus': {useDestinationEntryFocus: (...args: any[]) => entries.push(args)}, './guide-presentation': guidePresentation,
    '../../kit/useScreenReader': {useScreenReader: () => false},
    './useGuideTimePan': {useGuideTimePan: () => ({active: false, position: {value: 0}})}, './guide-pan-model': guidePan, './LivePhonePicker': {NativeLivePhonePicker: 'PhonePicker'},
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

for (const entry of ['manage', 'primaryAction', 'secondaryAction']) test(`TV ${entry} offers only public web management and returns through Back`, async () => {
  const f = await fixture(); try {
    const oldGuide = f.pageBack.page;
    if (entry === 'manage') await f.press(); else await f.action(entry);
    assert.equal(f.all().find(node => node.type === 'QR')?.props.value, 'https://server.example/settings?scope=server&section=live');
    assert.equal(f.pageBack.enabled, false); assert.equal(f.pageBack.page.current(), false);
    assert.equal(oldGuide.current(), false, 'retained Guide ownership cannot react while handoff owns Back');
    assert.ok(f.backs.size); [...f.backs][0]!(); await f.host.settle();
    assert.ok(f.find('live-no-sources')); assert.ok(!f.find('tv-management-handoff'));
    assert.deepEqual(f.editors, []); assert.deepEqual(f.admin, []);
    assert.ok(f.entries.some(([value, target]) => value.visible && target.childId === 'live-owner-open'));
  } finally {await f.close();}
});
for (const change of ['viewer', 'route', 'secondary', 'hidden', 'retire', 'disabled', 'canManage']) test(`TV management retires original opening and callbacks on ${change}`, async () => {
  const f = await fixture(); try {
    const oldOpen = f.find('live-owner-open').props.onPress; await f.press(); const oldBack = [...f.backs][0]!;
    await f.replace(change); oldOpen(); oldBack(); await f.host.settle();
    assert.ok(!f.find('tv-management-handoff')); assert.deepEqual(f.editors, []); assert.deepEqual(f.admin, []);
    if (change === 'route') {await f.press(); oldBack(); await f.host.settle(); assert.ok(f.find('tv-management-handoff'));}
  } finally {await f.close();}
});
for (const [role, primary] of [['member', 'main'], ['admin', 'main'], ['owner', 'other'], ['owner', 'main']]) test(`TV ${role}/${primary} never mounts administrative New-rule`, async () => {
  const f = await fixture({role, primary}); try {
    if (role !== 'owner' || primary !== 'main') {assert.ok(!f.find('live-owner-open')); assert.equal(f.find('live-no-sources')?.props.secondaryAction, undefined);}
    await f.recordings(); assert.ok(!f.find('recordings-new-rule')); assert.deepEqual(f.editors, []); assert.deepEqual(f.admin, []);
  } finally {await f.close();}
});
test('private or invalid origins cannot enable setup actions or publish QR data', async () => {
  for (const origin of ['invalid', 'file://server/', 'https://user:secret@server/', 'https://server/?token=private', 'https://server/#private']) {
    const f = await fixture({origin}); try {
      assert.equal(f.find('live-owner-open')?.props.disabled, true);
      for (const action of ['primaryAction', 'secondaryAction']) assert.equal(f.find('live-no-sources')?.props[action]?.disabled, true);
      f.find('live-owner-open').props.onPress(); await f.host.settle(); assert.ok(!f.all().some(node => node.type === 'QR')); assert.deepEqual(f.admin, []);
    } finally {await f.close();}
  }
});
for (const action of ['primaryAction', 'secondaryAction']) test(`mobile ${action} still opens its editor; retained initial intent cannot mount on TV`, async () => {
  const f = await fixture({isTV: false}); try {
    await f.action(action); assert.ok(f.all().some(node => node.type === 'OwnerEditor'));
    f.editors.length = 0; await f.tv(); assert.ok(!f.all().some(node => node.type === 'OwnerEditor')); assert.deepEqual(f.editors, []);
    await f.recordings(); assert.ok(!f.find('recordings-new-rule')); assert.deepEqual(f.editors, []); assert.deepEqual(f.admin, []);
  } finally {await f.close();}
});
test('mobile owner retains New-rule; a retained open sheet is excluded when the same screen becomes TV', async () => {
  const f = await fixture({isTV: false}); try {
    await f.recordings(); const sheet = f.all().find(node => node.type === 'NewRuleEditor'); assert.ok(sheet); sheet.props.onOpenChange(true); await f.host.settle();
    assert.ok(f.editors.includes('new-rule:true')); f.editors.length = 0;
    await f.tv(); assert.deepEqual(f.editors, []); assert.deepEqual(f.admin, []);
  } finally {await f.close();}
});

for (const state of ['failed', 'populated']) test(`TV management remains view-only with ${state} Guide sources`, async () => {
  const f = await fixture({failed: state === 'failed', populated: state === 'populated'}); try {
    assert.ok(!f.find('live-no-sources'));
    await f.press(); assert.ok(f.find('tv-management-handoff'));
    assert.deepEqual(f.admin, []); assert.deepEqual(f.editors, []);
  } finally {await f.close();}
});

for (const denied of [{disabled: true}, {canManage: false}]) test(`TV initial admission refuses ${JSON.stringify(denied)} without URL publication`, async () => {
  const f = await fixture(denied); try {
    assert.ok(!f.find('live-owner-open'));
    assert.equal(f.find('live-no-sources')?.props.secondaryAction, undefined);
    assert.ok(!f.all().some(node => node.type === 'QR')); assert.deepEqual(f.editors, []); assert.deepEqual(f.admin, []);
  } finally {await f.close();}
});

test('disposed original runtime refuses retained initial setup and Back before either guarded getter', async () => {
  const f = await fixture(); try {
    const initial = f.find('live-owner-open').props.onPress, add = f.find('live-no-sources').props.primaryAction.onPress;
    await f.press(); const oldBack = [...f.backs][0]!;
    f.disposeRuntime();
    assert.doesNotThrow(() => {initial(); add(); oldBack();});
    assert.equal(f.disposedReads(), 0); assert.deepEqual(f.admin, []);
  } finally {await f.close();}
});

test('TV Recordings uses its original viewer window for tail events and keeps FX-234 guards', async () => {
  const f = await fixture();
  try {
    const row = {id: 'recording', revision: 1, state: 'scheduled', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', programme: {id: 'p', title: 'Programme'}, options: {priority: 0}};
    const original = {scope: {}, generation: 1, items: [{kind: 'recording', value: row}], chunks: [{cursor: '', items: []}], bookmarks: [''], nextCursor: 'next', pending: false, refreshRequired: false};
    f.recordingState.page = {recordings: [row], rules: [], captureAvailable: true}; f.recordingState.viewerWindow = original;
    await f.recordings();
    const list = f.all().find(node => node.type === 'FlatList'); assert.ok(list);
    list.props.onEndReached(); assert.deepEqual(f.viewerCalls, [], 'layout alone does not drain pages');
    list.props.onScrollBeginDrag(); list.props.onEndReached(); assert.deepEqual(f.viewerCalls, [original]);
    assert.ok(f.find('recordings-page-next')); assert.equal(f.editors.some(value => value.startsWith('new-rule:')), false);
    const rendered = list.props.renderItem({item: {kind: 'recording', recording: row}, index: 0});
    const saved = rendered.props.onOpen;
    f.publishRecordingState({...f.recordingState, viewerWindow: {...original, scope: {}, generation: 2}}); await f.recordings();
    saved(); await f.host.settle(); assert.equal(f.find('recordings-details'), undefined, 'a stale row cannot open a successor');
  } finally {await f.close();}
});

for (const change of ['route', 'query', 'revision', 'normal-close'] as const) test(`recording Play keeps its original action receipt across ${change}`, async () => {
  const f = await fixture();
  try {
    const row = {id: 'recording', revision: 1, state: 'completed', itemId: 'item', libraryId: 'library', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', programme: {id: 'p', title: 'Programme'}, options: {priority: 0}};
    const original = {scope: {}, generation: 1, items: [{kind: 'recording', value: row}], chunks: [{cursor: '', items: []}], bookmarks: [''], nextCursor: '', pending: false, refreshRequired: false};
    f.recordingState.page = {recordings: [row], rules: [], captureAvailable: true}; f.recordingState.viewerWindow = original;
    let finish!: (value: unknown) => void, reads = 0, plays = 0;
    f.context.api.request = () => {reads++; return new Promise(resolve => {finish = resolve;});};
    f.context.playback = {start: async () => {plays++;}};
    await f.recordings(); const list = f.all().find(node => node.type === 'FlatList')!;
    const rendered = list.props.renderItem({item: {kind: 'recording', recording: row}, index: 0}); rendered.props.onMenu(); await f.host.settle();
    const menu = f.find('recordings-menu'); assert.ok(menu); const play = menu.props.items.find((item: any) => item.id === 'play'); assert.ok(play); play.onSelect(); assert.equal(reads, 1);
    menu.props.onOpenChange(false, 'action'); await f.host.settle();
    if (change === 'route') await f.replace('route');
    if (change === 'query') {f.recordingState.query = {view: 'history'}; f.recordingState.viewerWindow = {...original, scope: {}, generation: 2}; await f.recordings();}
    if (change === 'revision') {f.recordingState.viewerWindow = {...original, items: [{kind: 'recording', value: {...row, revision: 2}}]}; await f.recordings();}
    finish({id: 'item', kind: 'movie', title: 'Programme', libraryId: 'library', available: true, durationMs: 60000, progressMs: 0}); await f.host.settle();
    assert.equal(plays, change === 'normal-close' ? 1 : 0);
  } finally {await f.close();}
});

for (const kind of ['recording', 'group', 'rule'] as const) test(`old drawn ${kind} opener cannot adopt a new route occurrence`, async () => {
  const f = await fixture();
  try {
    const recording = {id: 'recording', revision: 1, state: 'completed', itemId: 'item', libraryId: 'library', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', programme: {id: 'p', title: 'Programme'}, options: {priority: 0}};
    const group = {key: 'group', title: 'Group', recordings: 1, unwatched: 0, recording};
    const rule = {id: 'rule', revision: 1, config: {name: 'Rule', sourceId: 'source', enabled: true, episodes: 'all', allowedChannels: [], blockedChannels: [], keywords: [], blockedKeywords: [], options: {priority: 0, retentionDays: 0, episodeLimit: 0, beforeSeconds: 0, afterSeconds: 0}}};
    const item = kind === 'recording' ? recording : kind === 'group' ? group : rule;
    const original = {scope: {}, generation: 1, items: [{kind, value: item}], chunks: [{cursor: '', items: []}], bookmarks: [''], nextCursor: '', pending: false, refreshRequired: false};
    f.recordingState.query = {view: kind === 'group' ? 'recorded' : kind === 'rule' ? 'rules' : 'upcoming'};
    f.recordingState.page = {recordings: kind === 'recording' ? [recording] : [], rules: kind === 'rule' ? [rule] : [], captureAvailable: true};
    f.recordingState.groups = kind === 'group' ? {groups: [group]} : undefined; f.recordingState.viewerWindow = original;
    await f.recordings(); let list = f.all().find(node => node.type === 'FlatList')!;
    const args = {item: kind === 'recording' ? {kind: 'recording', recording} : item, index: 0};
    const old = list.props.renderItem(args).props.onOpen; await f.replace('route');
    const before = f.viewerLoads.length; old(); await f.host.settle(); assert.equal(f.viewerLoads.length, before);
    assert.equal(f.find('recordings-details'), undefined); assert.equal(f.find('recordings-rule-editor'), undefined);
    list = f.all().find(node => node.type === 'FlatList')!; list.props.renderItem(args).props.onOpen(); await f.host.settle();
    if (kind === 'group') assert.equal(f.viewerLoads.length, before + 1);
    if (kind === 'recording') assert.ok(f.find('recordings-details'));
    if (kind === 'rule') assert.ok(f.find('recordings-rule-editor'));
  } finally {await f.close();}
});

test('same recording detail reopen retires the old opening and follows the fresh overlap cursor', async () => {
  const f = await fixture();
  try {
    const row: any = {id: 'recording', revision: 1, state: 'conflicted', itemId: '', libraryId: '', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', reason: '', programme: {id: 'p', title: 'Programme'}, options: {priority: 0}};
    const window = {scope: {}, generation: 1, items: [{kind: 'recording', value: row}], chunks: [{cursor: '', items: []}], bookmarks: [''], nextCursor: '', pending: false, refreshRequired: false};
    f.recordingState.page = {recordings: [row], rules: [], captureAvailable: true}; f.recordingState.viewerWindow = window;
    const reads: {cursor?: string; resolve: (detail: any) => void}[] = [];
    f.recordingsController.detail = (_id: string, cursor?: string) => new Promise(resolve => {reads.push({cursor, resolve});});
    const detail = (nextOverlap: string, id: string) => ({recording: row, capacity: {known: true, effective: 1}, overlaps: [{id, title: id, start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', priority: 1, state: 'scheduled'}], nextOverlap});
    await f.recordings(); const list = f.all().find(node => node.type === 'FlatList')!;
    const open = list.props.renderItem({item: {kind: 'recording', recording: row}, index: 0}).props.onOpen;
    open(); await f.host.settle(); assert.equal(reads.length, 1); reads[0].resolve(detail('p2', 'old')); await f.host.settle();
    const oldDialog = f.find('recordings-details'), oldMore = f.find('recordings-overlap-next'); assert.ok(oldDialog); assert.ok(oldMore);
    oldMore.props.onPress(); await f.host.settle(); assert.equal(reads[1].cursor, 'p2');
    oldDialog.props.onOpenChange(false); open(); await f.host.settle(); assert.equal(reads.length, 3);
    reads[1].resolve(detail('', 'late-old')); await f.host.settle(); oldMore.props.onPress(); oldDialog.props.onOpenChange(false); await f.host.settle(); assert.equal(reads.length, 3);
    reads[2].resolve(detail('p2', 'fresh')); await f.host.settle(); const newMore = f.find('recordings-overlap-next'); assert.ok(newMore);
    newMore.props.onPress(); await f.host.settle(); assert.equal(reads[3].cursor, 'p2');
    reads[3].resolve(detail('', 'fresh-later')); await f.host.settle(); assert.equal(f.find('recordings-overlap-next'), undefined);
  } finally {await f.close();}
});


for (const change of ['route', 'query', 'revision', 'normal-close'] as const) test(`recording playback start retains caller lifetime across ${change}`, async () => {
  const f = await fixture();
  try {
    const row = {id: 'recording', revision: 1, state: 'completed', itemId: 'item', libraryId: 'library', start: '2026-10-21T12:00:00Z', end: '2026-10-21T13:00:00Z', programme: {id: 'p', title: 'Programme'}, options: {priority: 0}};
    const original = {scope: {}, generation: 1, items: [{kind: 'recording', value: row}], chunks: [{cursor: '', items: []}], bookmarks: [''], nextCursor: '', pending: false, refreshRequired: false};
    f.recordingState.page = {recordings: [row], rules: [], captureAvailable: true}; f.recordingState.viewerWindow = original;
    f.context.api.request = async () => ({id: 'item', kind: 'movie', title: 'Programme', libraryId: 'library', available: true, durationMs: 60000, progressMs: 0});
    let caller: any, finish!: () => void, starts = 0;
    f.context.playback = {start: (input: any) => {starts++; caller = input.lifetime; return new Promise<void>(resolve => {finish = resolve;});}};
    await f.recordings(); const list = f.all().find(node => node.type === 'FlatList')!;
    list.props.renderItem({item: {kind: 'recording', recording: row}, index: 0}).props.onMenu(); await f.host.settle();
    const menu = f.find('recordings-menu'); menu.props.items.find((item: any) => item.id === 'play').onSelect(); menu.props.onOpenChange(false, 'action'); await f.host.settle();
    assert.equal(starts, 1); assert.equal(caller.isCurrent(), true);
    if (change === 'route') await f.replace('route');
    if (change === 'query') {f.recordingState.query = {view: 'history'}; f.recordingState.viewerWindow = {...original, scope: {}, generation: 2}; f.notify();}
    if (change === 'revision') {f.recordingState.viewerWindow = {...original, items: [{kind: 'recording', value: {...row, revision: 2}}]}; f.notify();}
    assert.equal(caller.isCurrent(), change === 'normal-close'); assert.equal(caller.signal.aborted, change !== 'normal-close');
    finish(); await f.host.settle(); assert.equal(starts, 1);
  } finally {await f.close();}
});


test('TV Recordings Back returns focused body to tabs, then group to list, then guide', async () => {
  const f = await fixture();
  try {
    f.recordingState.query = {view: 'recorded', group: 'group', sort: 'recent'};
    await f.recordings();
    const body = f.all().find(node => node.type === 'Region' && node.props.onFocusChange); assert.ok(body);
    const before = f.viewerLoads.length; body.props.onFocusChange(true); assert.equal([...f.backs][0]!(), true);
    assert.equal(f.focusRequests.length, 1); assert.equal(f.viewerLoads.length, before);
    body.props.onFocusChange(false); assert.equal([...f.backs][0]!(), true);
    assert.deepEqual(f.viewerLoads.at(-1), {view: 'recorded'});
    f.recordingState.query = {view: 'recorded'}; await f.recordings();
    assert.equal([...f.backs][0]!(), true); assert.equal(f.guideCalls(), 1);
  } finally {await f.close();}
});


test('Live Guide departure retires only the original Recordings pagination opening', async () => {
  const f = await fixture({populated: true, realRecordings: true});
  try {
    await f.press('live-recordings'); await f.host.settle();
    const more = f.find('recordings-page-next'); assert.ok(more); const oldMore = more.props.onPress;
    oldMore(); await f.host.settle(); assert.deepEqual(f.pageCalls, [undefined, 'next']);
    const pendingRows = f.recordingsController.getSnapshot().viewerWindow.items;
    assert.equal([...f.backs][0]!(), true); await f.host.settle();
    assert.ok(f.find('live-recordings')); assert.equal(f.recordingsController.getSnapshot().viewerWindow.pending, false);
    oldMore(); await f.host.settle(); assert.deepEqual(f.pageCalls, [undefined, 'next']);
    f.finishPage({...f.firstPage, recordings: [{...f.firstPage.recordings[0], id: 'late'}], nextCursor: ''}); await f.host.settle();
    assert.deepEqual(f.recordingsController.getSnapshot().viewerWindow.items, pendingRows);
    await f.press('live-recordings'); await f.host.settle();
    const fresh = f.find('recordings-page-next'); assert.ok(fresh); fresh.props.onPress(); await f.host.settle();
    assert.deepEqual(f.pageCalls, [undefined, 'next', undefined, 'next']);
  } finally {await f.close();}
});


test('Live Guide departure refuses the original Recordings Retry callback before IO', async () => {
  const f = await fixture({populated: true, realRecordings: true});
  try {
    await f.press('live-recordings'); await f.host.settle(); f.failNextPage();
    const more = f.find('recordings-page-next'); assert.ok(more); more.props.onPress(); await f.host.settle();
    const error = f.find('recordings-page-error'); assert.ok(error); const oldRetry = error.props.primaryAction.onPress;
    assert.deepEqual(f.pageCalls, [undefined, 'next']); assert.equal([...f.backs][0]!(), true); await f.host.settle();
    oldRetry(); await f.host.settle(); assert.deepEqual(f.pageCalls, [undefined, 'next']);
    await f.press('live-recordings'); await f.host.settle();
    const fresh = f.find('recordings-page-next'); assert.ok(fresh); fresh.props.onPress(); await f.host.settle();
    assert.deepEqual(f.pageCalls, [undefined, 'next', undefined, 'next']);
  } finally {await f.close();}
});


test('Live Guide departure inside continuation admission refuses the retired opening before IO', async () => {
  const f = await fixture({populated: true, realRecordings: true});
  try {
    await f.press('live-recordings'); await f.host.settle();
    const original = f.recordingsController.getSnapshot().viewerWindow, opening = f.opening(); assert.ok(original); assert.ok(opening);
    f.armCurrent(3, () => {assert.equal([...f.backs][0]!(), true);});
    await f.recordingsController.nextViewerPage(original, opening); await f.host.settle();
    assert.ok(f.find('live-recordings')); assert.deepEqual(f.pageCalls, [undefined]);
    assert.deepEqual(f.recordingsController.getSnapshot().viewerWindow.items, original.items);
    await f.press('live-recordings'); await f.host.settle();
    const fresh = f.find('recordings-page-next'); assert.ok(fresh); fresh.props.onPress(); await f.host.settle();
    assert.deepEqual(f.pageCalls, [undefined, undefined, 'next']);
  } finally {await f.close();}
});
