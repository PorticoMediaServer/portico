import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus.ts';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children)] : [];
const find = (tree: any, id: string) => nodes(tree).find(node => node.props?.testID === id);
const colors = new Proxy({}, {get: (_target, key) => String(key)});
const theme = {isTV: false, width: 1180, platform: 'web', colors};
const t = (id: string, values?: any) => id === 'player.clockOf' ? `${values.position} of ${values.duration}` : id;
const visual = {alpha: (color: string) => color, shellType: () => ({})};
function fixture() {
  const host = hooks();
  const extra = {useId: () => 'fixture', useLayoutEffect: () => {}, Children: {toArray: (children: any) => [children].flat().filter(Boolean)}};
  Object.assign(host.react, extra); Object.assign(host.react.default, extra);
  return host;
}
const native = {Text: 'Text', View: 'View', PanResponder: {create: () => ({panHandlers: {}})}};

test('Health tiles follow their measured owner pane when the pane resizes', async () => {
  const host = fixture();
  const {OwnerHealthSection} = await screenModule(new URL('../src-next/screens/settings-live/owner-dashboard/health.tsx', import.meta.url), {
    react: host.react, 'react-native': native, 'react-native-svg': {default: 'Svg', Path: 'Path'},
    '../../../kit/controls': {Segmented: 'Segmented'}, '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/shell': {StatTiles: 'StatTiles'},
    '../../../kit/theme': {useTheme: () => theme}, '../../../kit/shell/visual': visual,
    '../../../../../../packages/client-core/src/server-admin/console-words': {when: String},
    '../../../../../../packages/client-core/src/presentation/content': {formatBytes: String},
    './parts': {OwnerCalm: 'OwnerCalm', OwnerSection: 'OwnerSection'},
    './model': {OWNER_HEALTH_WINDOWS: ['hour'], ownerHealthTiles: () => [{id: 'cpu'}], ownerHealthWindowLabel: String, ownerHealthDetail: () => undefined},
  });
  const render = () => host.render(() => OwnerHealthSection({reading: {}, window: 'hour', onWindow() {}, onRetry() {}, t}));
  let tree = render();
  assert.equal(find(tree, 'owner-health'), undefined, 'wait for the containing pane to report its size');
  nodes(tree).find(node => node.props?.onLayout).props.onLayout({nativeEvent: {layout: {width: 652}}});
  tree = render(); assert.equal(find(tree, 'owner-health').props.width, 652);
  nodes(tree).find(node => node.props?.onLayout).props.onLayout({nativeEvent: {layout: {width: 420}}});
  tree = render(); assert.equal(find(tree, 'owner-health').props.width, 420);
});

test('Background job groups keep every row full width and indent only separators', async () => {
  const host = fixture();
  const {OwnerJobGroup} = await screenModule(new URL('../src-next/screens/settings-live/owner-dashboard/parts.tsx', import.meta.url), {
    react: host.react, 'react-native': native, 'react-native-svg': {default: 'Svg', Circle: 'Circle'},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../../kit/Icon': {Icon: 'Icon'},
    '../../../kit/theme': {useTheme: () => theme}, '../../../kit/shell/visual': visual,
  });
  const children = ['first', 'second', 'third'];
  const tree = host.render(() => OwnerJobGroup({children}));
  const rows = tree.props.children[0];
  assert.equal(rows.length, 3);
  rows.forEach((row: any, index: number) => {assert.equal(row.props.children[0], children[index]); assert.equal(row.props.style?.marginLeft, undefined);});
  const lines = nodes(tree).filter(node => node.props?.pointerEvents === 'none');
  assert.equal(lines.length, 2);
  lines.forEach(line => {assert.equal(line.props.style.left, 72); assert.equal(line.props.style.right, 0); assert.equal(line.props.style.position, 'absolute');});
});

test('Move playback announces the same readable position it submits and clamps adjustments', async () => {
  const host = fixture(); let sent: number | undefined;
  const {OwnerMoveSheet} = await screenModule(new URL('../src-next/screens/settings-live/owner-dashboard/move-sheet.tsx', import.meta.url), {
    react: host.react, 'react-native': native,
    '../../../../../../packages/client-core/src/server-admin/now-playing': {clampToLength: (value: number, length: number) => Math.max(0, Math.min(length, value)), positionClock: (value: number) => `${value / 1000}s`, sessionViewer: () => 'Viewer'},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/overlays': {BottomSheet: 'BottomSheet'},
    '../../../kit/theme': {useTheme: () => theme}, '../../../kit/shell/visual': visual,
  });
  const render = () => host.render(() => OwnerMoveSheet({session: {id: 'session', durationMs: 60_000, positionMs: 0}, t, busy: false, onCancel() {}, onMove: (position: number) => {sent = position;}}));
  let tree = render(), track = find(tree, 'owner-move-track');
  assert.equal(track.props.accessible, true); assert.equal(track.props.accessibilityValue.text, '0s of 60s');
  for (let step = 0; step < 3; step++) {track.props.onAccessibilityAction({nativeEvent: {actionName: 'increment'}}); tree = render(); track = find(tree, 'owner-move-track');}
  assert.equal(track.props.accessibilityValue.now, 60_000); assert.equal(track.props.accessibilityValue.text, '60s of 60s');
  track.props.onAccessibilityAction({nativeEvent: {actionName: 'activate'}}); tree = render();
  nodes(tree.props.footer).find(node => node.props?.variant === 'primary').props.onPress(); assert.equal(sent, 60_000);
  find(tree, 'owner-move-track').props.onAccessibilityAction({nativeEvent: {actionName: 'decrement'}}); tree = render();
  nodes(tree.props.footer).find(node => node.props?.variant === 'primary').props.onPress(); assert.equal(sent, 30_000);
});

async function guideFixture(width = 1180, screenReader = false) {
  const host = fixture(), viewTheme = {...theme, width};
  let activeHost = host;
  const react: any = {...host.react};
  for (const name of ['useState', 'useRef', 'useMemo', 'useCallback', 'useEffect', 'useSyncExternalStore'] as const) react[name] = (...args: any[]) => (activeHost.react[name] as (...values: any[]) => any)(...args);
  const adjustments: number[] = []; let panOptions: any;
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch'};
  const lifetime = {authority, current: () => true, signal: new AbortController().signal};
  const source = {id: 'source', name: 'Source', guideDays: 2, groups: [], provenance: 'live-source'};
  const channel = {id: 'channel', sourceId: 'source', kind: 'live', name: 'Channel', number: '1', favorite: false, provenance: 'live-source', hidden: false};
  const programme = {id: 'programme', title: 'Programme', start: Date.now() - 60_000, end: Date.now() + 60_000};
  const data = {total: 1, version: 0}, state = {sources: [source], options: {}, revision: 0, view: 'guide', mutating: false};
  const guide = {subscribe: () => () => {}, getSnapshot: () => data, channelAt: () => channel, programsFor: () => ({programs: [programme], complete: true, failed: false}), subscribePage: () => () => {}, pageOf: () => 0};
  const controller = {subscribe: () => () => {}, getSnapshot: () => state, guide, lifetime, clearMutationError() {}};
  const shell = {authority, destination: {kind: 'live'}, contentVisible: true};
  const runtime = {text: {t}, subscribe: () => () => {}, getSnapshot: () => shell, artwork: () => undefined, error: () => ({title: 'Error'})};
  const context = {account: {account: {role: 'owner', primaryProfileId: 'profile'}, canManage: true}, api: {baseUrl: 'https://example.invalid'}, current: () => true, authority: () => authority,
    navigation: {subscribe: () => () => {}, getSnapshot: () => ({revision: 1, destination: {kind: 'live'}}), isCurrent: () => true}};
  const {NativeLiveScreen} = await screenModule(new URL('../src-next/screens/settings-live/LiveScreen.tsx', import.meta.url), { './tv-settings-focus': tvSettingsFocus,
    react, 'react-native': {...native, BackHandler: {}, FlatList: 'FlatList', ScrollView: 'ScrollView', VirtualizedList: 'VirtualizedList', Platform: {OS: 'web'}, useWindowDimensions: () => ({width, height: 900})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, left: 0, right: 0, bottom: 0})},
    'react-native-gesture-handler': {GestureDetector: 'GestureDetector'}, 'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: () => ({})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Pattern: 'Pattern', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/client-core/src/dvr': {defaultRecordingOptions: {}},
    '../../../../../packages/client-core/src/channel-guide': {dayPrimeTime: (value: number) => value, stripDays: () => [0, 86_400_000]},
    '../../../../../packages/client-core/src/presentation/kit-model': {episodeLabel: () => undefined, programmeFactParts: () => [], programmeTimeRange: () => undefined},
    '../../../../../packages/client-core/src/guide': {legacyChannel: (value: any) => value, floorTo: (value: number) => value, localDayStart: () => 0, SLOT_MS: 1_800_000, pxPerMsFor: () => 1 / 15_000, rulerTicks: () => [], nowNext: () => ({now: programme}), layoutRow: () => [{kind: 'programme', program: programme, start: programme.start, end: programme.end, x: 0, width: 200}]},
    '../../../../../packages/client-core/src/guide/directory': {GuideFeatureClient: class {}},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Segmented: 'Segmented', Select: 'Select'},
    '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => ({})},
    '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu', ToastHost: 'ToastHost'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork', ChannelMark: 'ChannelMark'}, '../../kit/Icon': {Icon: 'Icon'},
    './GuideLoadingMarker': {GuideLoadingMarker: 'GuideLoadingMarker'}, '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {StateView: 'StateView'}, '../../kit/shell/visual': visual, '../../kit/theme': {useTheme: () => viewTheme},
    '../../kit/useScreenReader': {useScreenReader: () => screenReader}, '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})},
    '../../shell/model': {sameAuthority: () => true, shellLayout: () => ({kind: width < 600 ? 'phone' : 'expanded'})}, '../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})},
    './Screen': {Screen: 'Screen', ScreenText: 'ScreenText', useScreenBack() {}, useScreenEntry() {}}, './RecordingsScreen': {NativeRecordingsScreen: 'Recordings'},
    '../../tv/layout/guide-focus': {}, '../destinations/useTVPageBack': {useTVPageBack() {}}, '../destinations/useEntryFocus': {useDestinationEntryFocus() {}},
    './guide-presentation': {nativeGuideClock: () => () => '12:00', nativeGuideDate: () => () => 'Today', nativeGuideRegion: () => ({locale: 'en-US', hour12: false}), nativeGuideNext: () => undefined},
    './useGuideTimePan': {useGuideTimePan: (options: any) => {panOptions = options; return {active: true, position: {value: 0}, adjust: (delta: number) => adjustments.push(delta)};}},
    './guide-pan-model': {nativeGuidePanRange: () => ({first: 0, last: Number.MAX_SAFE_INTEGER})}, './LivePhonePicker': {NativeLivePhonePicker: 'Picker'}, './LiveOwnerScreen': {NativeLiveOwnerScreen: 'Owner'}, './LiveSeriesRule': {NativeSeriesRuleSheet: 'SeriesRule'},
    '../../tv/auth': {TVManagementHandoff: 'Handoff'}, '../../tv/auth/TVHandoff': {publicTVHandoffAddress: (url: string) => ({url})}, './recording-rule-presentation': {}, './server/tv-server-reader': {nativeServerAdmission: () => 'owner'},
  });
  const render = () => host.render(() => NativeLiveScreen({controller, recordings: {}, owner: {}, ports: {context}, runtime}));
  return {render, adjustments, channel, programme, renderRow: (row: any) => {activeHost = fixture(); try {return activeHost.render(() => row.type(row.props));} finally {activeHost = host;}}, get panOptions() {return panOptions;}};
}

test('Expanded and screen-reader guides expose independent two-hour navigation without grouping programme cells', async () => {
  for (const [width, screenReader] of [[1180, false], [390, true]] as const) {
    const f = await guideFixture(width, screenReader), tree = f.render();
    const earlier = find(tree, 'live-earlier'), later = find(tree, 'live-later');
    assert.ok(earlier); assert.ok(later); earlier.props.onPress(); later.props.onPress();
    assert.deepEqual(f.adjustments, [-7_200_000, 7_200_000]);
    const gestureChild = nodes(tree).find(node => node.type === 'GestureDetector').props.children[0];
    assert.equal(gestureChild.props.accessible, undefined); assert.equal(gestureChild.props.accessibilityActions, undefined);
  }
});

test('A docked programme keeps guide panning active and marks only its matching channel/programme cell', async () => {
  const f = await guideFixture(); let tree = f.render();
  const content = nodes(tree).find(node => node.props?.ref && node.props?.onLayout && node.type === 'View');
  content.props.ref.current = {measureInWindow: (done: any) => done(0, 0, 1100)};
  content.props.onLayout({nativeEvent: {layout: {width: 1100}}}); tree = f.render();
  let row = nodes(tree).find(node => node.type === 'VirtualizedList').props.renderItem({item: 0});
  row.props.onOpen(f.channel, f.programme, null); tree = f.render();
  assert.ok(find(tree, 'live-programme-pane')); assert.equal(f.panOptions.enabled, true);
  row = nodes(tree).find(node => node.type === 'VirtualizedList').props.renderItem({item: 0});
  assert.equal(row.props.selected.channel, f.channel); assert.equal(row.props.selected.program, f.programme);
  const rowTree = f.renderRow(row);
  const paint = find(rowTree, 'live-programme-channel-programme');
  assert.equal(paint.props.selected, true);
  const otherTree = f.renderRow({...row, props: {...row.props, selected: {channel: {...f.channel, sourceId: 'other-source'}, program: f.programme}}});
  assert.equal(find(otherTree, 'live-programme-channel-programme').props.selected, false);
});
