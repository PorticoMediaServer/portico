import assert from 'node:assert/strict';
import test from 'node:test';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {liveGuideModule, liveTabsFixture, ownerTagFixture} from './helpers/live-fixtures.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import * as dvr from '../../../packages/client-core/src/dvr';
import * as channels from '../../../packages/client-core/src/channel-guide';
import * as guide from '../../../packages/client-core/src/guide';
import * as directory from '../../../packages/client-core/src/guide/directory';
import * as kitModel from '../../../packages/client-core/src/presentation/kit-model';
import * as availability from '../../../packages/client-core/src/presentation/live-availability';
import * as guidePresentation from '../src-next/screens/settings-live/guide-presentation';
import * as guidePan from '../src-next/screens/settings-live/guide-pan-model';
import * as rules from '../src-next/screens/settings-live/recording-rule-presentation';
import * as shellModel from '../src-next/shell/model';
import {tvFocusIds} from '../src-next/shell/tv-focus';
import {createTheme} from '../src-next/kit/theme';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';
import {nativeServerAdmission} from '../src-next/screens/settings-live/server/tv-server-reader';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree ? [tree, ...nodes(tree.children)] : [];
const textOf = (tree: any): string => nodes(tree).filter(node => node.type === 'RCTRawText').map(node => node.props.text).join('');
const noop = () => {}, subscribe = () => noop;
const jsx = (type: any, props: any, ...children: any[]) => React.createElement(type, props, ...children);

const now = Date.now();
const iso = (offsetMinutes: number) => new Date(now + offsetMinutes * 60_000).toISOString();
const rawChannel = (id = 'c1', over: Record<string, unknown> = {}): any => ({
  id, sourceId: 'src-a', provenance: 'live-source', name: `Channel ${id}`, number: id.replace(/\D/g, '') || '1',
  group: 'Sports', generation: 'g1', programmes: [], tuneAvailable: true, recordAvailable: true,
  tuneUnavailableReason: '', recordUnavailableReason: '', guide: 'full', favorite: false, hidden: false, preferenceRevision: 1, ...over,
});
const rawProgramme = (id: string, startMin: number, endMin: number, channelId = 'c1'): any => ({
  id, channelId, title: `Programme ${id}`, start: iso(startMin), end: iso(endMin),
  lineage: 'provider-id', seriesId: '', episodeId: '', newEvidence: 'unknown', description: '',
});

test('GuideProgrammeTitle renders deterministically with no timers, hidden text, or onTextLayout', async () => {
  const host = await fabricHost();
  const timers: unknown[] = [];
  const originalSetTimeout = globalThis.setTimeout;
  (globalThis as any).setTimeout = (...args: unknown[]) => {timers.push(args); return originalSetTimeout(...(args as Parameters<typeof setTimeout>));};
  try {
    const {GuideProgrammeTitle} = await screenModule(new URL('../src-next/kit/live/GuideProgrammeTitle.tsx', import.meta.url), {
      react: {...React, default: React},
      'react-native': {Text: 'Text', View: 'View'},
    });
    const heights: number[] = [];
    await host.render(jsx(GuideProgrammeTitle, {
      title: 'Evening News Special Report', lines: 2, maxHeight: 40, whole: true, style: {fontSize: 15}, onHeight: (h: number) => heights.push(h),
    }));
    const texts = nodes(host.root).filter(node => node.type === 'Text');
    assert.equal(texts.length, 1, 'exactly one Text node with no hidden measuring duplicate');
    assert.equal(texts[0].props.numberOfLines, 2);
    assert.equal(texts[0].props.ellipsizeMode, 'tail');
    assert.equal(texts[0].props.textBreakStrategy, 'simple');
    assert.equal(texts[0].props.android_hyphenationFrequency, 'none');
    assert.equal(texts[0].props.onTextLayout, undefined);
    assert.deepEqual(texts[0].props.style, [{fontSize: 15}, {maxHeight: 40}]);
    texts[0].props.onLayout({nativeEvent: {layout: {height: 32}}});
    assert.deepEqual(heights, [32]);
    assert.equal(timers.length, 0, 'no layout retry timers scheduled');

    await host.render(jsx(GuideProgrammeTitle, {title: '', lines: 1, style: {fontSize: 15}}));
    assert.equal(nodes(host.root).filter(node => node.type === 'Text').length, 0, 'empty title renders null');
  } finally {
    (globalThis as any).setTimeout = originalSetTimeout;
    await host.unmount();
  }
});

test('Guide onViewableItemsChanged stays stable across horizontal pans and uses latest start/end; reminders clear on selection change; LiveRow props stay stable', async () => {
  const host = await fabricHost({publicInstance: tag => ({tag, measureInWindow: (cb: (x: number, y: number, w: number, h: number) => void) => cb(0, 0, 1180, 820)})});
  const listeners = new Set<() => void>();
  const retirement = new AbortController();
  const theme = createTheme({isTV: false, width: 1180});
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'one', viewerFence: 'fence'};
  const route: any = {revision: 1, route: {kind: 'shell', destination: {kind: 'live'}}, destination: {kind: 'live'}};
  const shell: any = {authority, destination: route.destination, contentVisible: true, mediaExpanded: false, serverName: 'Portico'};
  const listen = (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: listen});
  const ch0 = rawChannel('c1'), ch1 = rawChannel('c2');
  const api = {request: async <T,>(path: string) => {
    if (path.startsWith('/v1/live/channels')) {
      return {
        items: [
          {id: ch0.id, name: ch0.name, number: ch0.number, kind: 'source', favorite: false, hidden: false},
          {id: ch1.id, name: ch1.name, number: ch1.number, kind: 'source', favorite: false, hidden: false},
        ],
        sources: [{id: 'src-a', name: 'Source A', kind: 'live', channels: 2, favorites: 0, groups: [], outOfDate: false,
          guideFrom: iso(-120), guideThrough: iso(720)}],
      } as T;
    }
    if (path.startsWith('/v1/live/guide?')) return {items: [{channel: ch0, programmes: []}, {channel: ch1, programmes: []}]} as T;
    throw new Error('Unexpected read ' + path);
  }};
  const controller = new NativeLiveController(lifetime, api, 'America/Halifax');
  await controller.loadSources();
  const viewports: {firstRow: number; lastRow: number; start: number; end: number}[] = [];
  const origSetViewport = controller.setViewport.bind(controller);
  controller.setViewport = vp => {viewports.push(vp); origSetViewport(vp);};

  const account: DirectSnapshot = {authority: 'local', serverId: 'server', canManage: true,
    capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
    account: {id: 'owner', username: 'Owner', role: 'owner', primaryProfileId: 'main', disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Main', primary: true, position: 0, art: 'blue', accessMarker: 'a', pinRequired: false}]};
  const context: any = {api: {baseUrl: 'https://server.example/'}, account, authority: () => authority, current: () => true,
    navigation: {subscribe: listen, getSnapshot: () => route, isCurrent: () => true, open: noop}};
  const runtime: any = {retirementSignal: retirement.signal, subscribe: listen, getSnapshot: () => shell,
    text: {t: defaultI18n.t, locale: 'en-US'}, error: () => ({title: 'Err'}), reportError: (e: unknown) => {throw e;}, onDestination: noop, artwork: () => undefined};
  const native = {View: 'View', Text: 'Text', ScrollView: 'ScrollView', VirtualizedList: 'VirtualizedList', FlatList: 'FlatList',
    Platform: {OS: 'android'}, useWindowDimensions: () => ({width: 1180, height: 820})};
  const controls = {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Segmented: 'Segmented', Select: 'Select'};
  const focus = {FocusRegion: 'Region', FocusItem: 'FocusItem', useCovered: () => false,
    useFocusRegistry: () => ({subscribe, cellPosition: () => undefined, attachedFocus: () => undefined, focus: () => true})};

  let commitPan!: (nextStart: number) => void;
  let holdReminders = false;
  let resolveReminders: (() => void) | undefined;
  let activeDetail: any;
  let activeReminders: any[] = [];
  class FakeFeatures {
    dispose() {}
    async programme() {
      return {...activeDetail, channel: {...activeDetail.channel, programmes: [activeDetail.programme]}, airings: []};
    }
    async later() {return [];}
    async remindersFor() {
      if (holdReminders) await new Promise<void>(resolve => {resolveReminders = resolve;});
      return {items: activeReminders};
    }
    async remindProgramme() {return undefined;}
    async dismissProgramme() {return undefined;}
  }

  const recordingSnapshot = {};
  const recordings: any = {getSnapshot: () => recordingSnapshot, lifetime, subscribe: () => noop, loadSummary: async () => {}};
  const {NativeLiveScreen} = await liveGuideModule({...liveTabsFixture(React), ...ownerTagFixture,
    react: {...React, default: React}, 'react-native': native,
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Pattern: 'Pattern', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/dvr': dvr,
    '../../../../../packages/client-core/src/presentation/kit-model': kitModel,
    '../../kit/controls': controls, '../../kit/focus': focus,
    '../../kit/overlays': {Banner: 'Banner', Dialog: 'Dialog', Menu: 'Menu', ToastHost: 'ToastHost'},
    '../../kit/artwork': {Artwork: 'Artwork', ChannelMark: 'ChannelMark'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0},
    '../../kit/states': {StateView: 'StateView'}, '../../kit/shell/visual': {alpha: (v: string) => v, shellType: () => ({})},
    '../../kit/theme': {useTheme: () => theme, fontFamilies: {}}, '../../shell/tv-focus': {tvFocusIds},
    '../../kit/layout/Screen': {Screen: 'Screen', ScreenText: 'ScreenText', useScreenBack: noop, useScreenEntry: () => noop},
    './recording-rule-presentation': rules, './tv-settings-focus': tvSettingsFocus,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    'react-native-gesture-handler': {GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {useDerivedValue: (fn: () => unknown) => ({value: fn()}), default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn()},
    '../../../../../packages/client-core/src/channel-guide': channels, '../../../../../packages/client-core/src/guide': guide,
    '../../../../../packages/client-core/src/guide/directory': {...directory, GuideFeatureClient: FakeFeatures},
    '../../kit/live/NativeGuideRow': {LiveRow: 'LiveRow', GuideChannelArt: 'GuideChannelArt'},
    '../../kit/live/ProgrammeDetails': {ProgrammeDetails: 'ProgrammeDetails'}, '../../kit/live/GuideRuler': {GuideRuler: 'GuideRuler'},
    '../../kit/live/Guide': {guideSpec: {ruler: 36, nowPill: 120}},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})},
    '../../platform/tv-keys': {useTVRemoteActivity: noop}, '../../shell/model': shellModel,
    './RecordingsScreen': {NativeRecordingsScreen: 'Recordings'}, '../../tv/layout/guide-focus': {},
    '../destinations/useTVPageBack': {useTVPageBack: noop}, '../destinations/useEntryFocus': {useDestinationEntryFocus: noop},
    './guide-presentation': guidePresentation, '../../kit/useScreenReader': {useScreenReader: () => false},
    './useGuideTimePan': {useGuideTimePan: (opts: any) => {commitPan = opts.commit; return {active: true, position: {value: 0}, adjust: noop};}},
    '../../../../../packages/client-core/src/presentation/live-availability': availability,
    './guide-pan-model': guidePan, './LivePhonePicker': {NativeLivePhonePicker: 'PhonePicker'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: 'OwnerEditor'}, '../../kit/live/SeriesRuleSheet': {NativeSeriesRuleSheet: 'SeriesRule'},
    './server/tv-server-reader': {nativeServerAdmission}, '../../tv/auth': {TVManagementHandoff: 'Handoff'},
    '../../tv/auth/TVHandoff': {publicTVHandoffAddress: (address: string) => ({url: address})},
  });

  try {
    await host.render(jsx(NativeLiveScreen, {controller, recordings, owner: {}, ports: {context}, runtime}));
    await host.settle();
    const list = () => nodes(host.root).find(node => node.type === 'VirtualizedList')!;
    const mountedOnViewableItemsChanged = list().props.onViewableItemsChanged;
    const initialRow0 = list().props.renderItem({item: 0});
    const initialStart = initialRow0.props.start;

    // Simulate horizontal time pan (+2 hours)
    const pannedStart = initialStart + 4 * guide.SLOT_MS;
    commitPan(pannedStart);
    await host.settle();

    assert.equal(list().props.onViewableItemsChanged, mountedOnViewableItemsChanged, 'onViewableItemsChanged keeps stable identity across pans');
    const afterPanRow0 = list().props.renderItem({item: 0});
    assert.equal(afterPanRow0.props.start, pannedStart);
    assert.equal(afterPanRow0.props.onTVFocus, initialRow0.props.onTVFocus, 'revealTVCell callback is stable');
    assert.equal(afterPanRow0.props.entryOpen, initialRow0.props.entryOpen, 'entryOpen callback is stable');
    assert.equal(afterPanRow0.props.onOpen, initialRow0.props.onOpen, 'open callback is stable');
    assert.equal(afterPanRow0.props.onChannelMenu, initialRow0.props.onChannelMenu, 'openChannelMenu callback is stable');
    assert.equal(afterPanRow0.props.watch, initialRow0.props.watch, 'watch callback is stable');
    assert.equal(afterPanRow0.props.tvRange, initialRow0.props.tvRange, 'tvRange object is memoized across same-span pans');

    // Call the MOUNT-CAPTURED callback (as React Native VirtualizedList does!) and verify it forwards the NEW start/end
    viewports.length = 0;
    mountedOnViewableItemsChanged({viewableItems: [{index: 2}, {index: 3}]});
    assert.equal(viewports.length, 1);
    assert.deepEqual(viewports[0], {firstRow: 2, lastRow: 4, start: pannedStart, end: afterPanRow0.props.end});

    // Enable two-pane layout so `selected` is passed to LiveRow, and verify only the matching row receives `selected`
    const contentView = nodes(host.root).find(node => node.type === 'View' && typeof node.props.onLayout === 'function');
    assert.ok(contentView);
    contentView.props.onLayout({nativeEvent: {layout: {width: 1180}}});
    await host.settle();

    // Open programme p1 with an active reminder
    const p1 = rawProgramme('p1', 30, 60, 'c1');
    const rowChannel0 = controller.guide.channelAt(0) ?? guide.legacyChannelRow(ch0);
    const rowChannel1 = controller.guide.channelAt(1) ?? guide.legacyChannelRow(ch1);
    const detailCh0 = {...ch0, sourceId: rowChannel0.sourceId};
    const detailCh1 = {...ch1, sourceId: rowChannel1.sourceId};
    activeDetail = {channel: detailCh0, programme: p1};
    activeReminders = [{id: 'rem-1', airing: {channel: detailCh0, programme: p1}}];
    list().props.renderItem({item: 0}).props.onOpen(rowChannel0, guide.legacyProgram(p1), null);
    await host.settle(); await host.settle();

    // Verify row 0 gets `selected`, while row 1 gets `undefined`
    assert.ok(list().props.renderItem({item: 0}).props.selected, 'matching row receives selected');
    assert.equal(list().props.renderItem({item: 1}).props.selected, undefined, 'unrelated rows receive undefined selected');

    // Verify reminder button shows "Stop reminding" for p1
    const remindButton = () => nodes(host.root).find(node => node.props?.testID === 'live-remind');
    assert.equal(textOf(remindButton()), defaultI18n.t('guide.stopRemind'));
    assert.equal(remindButton()?.props.icon, 'i-bell-f');

    // Close selection (!selected) -> reminders must clear; reopening p1 while remindersFor is pending must NOT show stale reminder
    const closeBtn = nodes(host.root).find(node => node.props?.testID === 'live-programme-close');
    assert.ok(closeBtn);
    closeBtn.props.onPress();
    await host.settle();
    holdReminders = true;
    activeReminders = [];
    list().props.renderItem({item: 0}).props.onOpen(rowChannel0, guide.legacyProgram(p1), null);
    await host.settle();
    assert.equal(textOf(remindButton()), defaultI18n.t('guide.remind'), 'stale reminders cleared on deselect before new remindersFor resolves');
    assert.equal(remindButton()?.props.icon, 'i-bell');
    holdReminders = false;
    resolveReminders?.();
    await host.settle();

    // Selecting row 1 clears row 0 selected and sets row 1 selected
    const p2 = rawProgramme('p2', 30, 60, 'c2');
    activeDetail = {channel: detailCh1, programme: p2};
    activeReminders = [];
    list().props.renderItem({item: 1}).props.onOpen(rowChannel1, guide.legacyProgram(p2), null);
    await host.settle(); await host.settle();
    assert.equal(list().props.renderItem({item: 0}).props.selected, undefined);
    assert.ok(list().props.renderItem({item: 1}).props.selected);
  } finally {
    await host.unmount();
    controller.dispose();
    lifetime.dispose();
  }
});

test('NativeGuideRow renders gap cells without Svg/Pattern and exports React.memo LiveRow', async () => {
  const host = await fabricHost();
  const theme = createTheme({isTV: false, width: 1180});
  const native = {View: 'View', Text: 'Text'};
  const {LiveRow} = await screenModule(new URL('../src-next/kit/live/NativeGuideRow.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': native,
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn()},
    '../../../../../packages/client-core/src/guide': guide,
    '../../../../../packages/client-core/src/presentation/live-availability': availability,
    '../../../../../packages/client-core/src/presentation/kit-model': kitModel,
    '../artwork': {ChannelMark: 'ChannelMark'},
    '../controls': {Focusable: 'Focusable'},
    '../focus': {FocusItem: 'FocusItem', useCovered: () => false},
    '../Icon': {Icon: 'Icon'},
    '../shell/visual': {alpha: (color: string, opacity: number) => `${color}@${opacity}`},
    '../theme': {useTheme: () => theme},
    './GuideLoadingMarker': {GuideLoadingMarker: 'GuideLoadingMarker'},
    './GuideProgrammeTitle': {GuideProgrammeTitle: 'GuideProgrammeTitle'},
    './ProgrammeDetails': {ProgrammeFlags: 'ProgrammeFlags'},
    '../layout/Screen': {useScreenEntry: () => noop},
    '../../screens/settings-live/guide-pan-model': guidePan,
    '../../screens/settings-live/guide-presentation': guidePresentation,
    '../../tv/layout/guide-focus': {},
    '../../screens/settings-live/tv-settings-focus': tvSettingsFocus,
  });

  // Verify LiveRow is wrapped in React.memo
  assert.equal((LiveRow as any).$$typeof, Symbol.for('react.memo'), 'LiveRow is memoized with React.memo');

  let renders = 0;
  const channel = guide.legacyChannelRow(rawChannel('c1'));
  const start = 1_000_000, end = start + 2 * guide.SLOT_MS;
  const guideStore: any = {
    subscribePage: () => noop,
    pageOf: () => 0,
    getSnapshot: () => ({version: 1}),
    channelAt: () => {renders++; return channel;},
    programsFor: () => ({programs: [], complete: true, failed: false}),
    invalidate: noop,
  };
  const controller: any = {guide: guideStore, lifetime: {current: () => true}};
  const runtime: any = {text: {t: defaultI18n.t, locale: 'en-US'}, artwork: () => undefined, getSnapshot: () => ({destination: {kind: 'live'}, contentVisible: true, mediaExpanded: false})};
  const sharedProps = {
    tvRange: undefined, onTVFocus: noop, panPosition: {value: 0}, panActive: false, laneWidth: 800,
    clock: () => '12:00', entryEnabled: false, entryOpen: () => true, index: 0, controller, runtime,
    start, end, now: start, channelWidth: 192, rowHeight: 72, px: 120 / guide.SLOT_MS, list: false,
    onOpen: noop, onChannelMenu: noop, selected: undefined, grid: 'grid', watch: noop,
  };

  try {
    await host.render(jsx(LiveRow, sharedProps));
    const all = nodes(host.root);
    // Gap cell rendered without Svg or Pattern
    assert.equal(all.some(node => node.type === 'Svg' || node.type === 'Pattern' || node.type === 'Defs'), false, 'no Svg/Defs/Pattern in gap cells');
    const gapTint = all.find(node => node.type === 'View' && node.props?.pointerEvents === 'none' && node.props?.style?.backgroundColor === `${theme.colors.text}@0.03`);
    assert.ok(gapTint, 'gap cell renders lightweight tinted View');

    // Re-rendering with identical props skips LiveRow body thanks to React.memo
    const countAfterFirst = renders;
    await host.render(jsx(LiveRow, sharedProps));
    assert.equal(renders, countAfterFirst, 'React.memo skips re-render when props are unchanged');
  } finally {
    await host.unmount();
  }
});
