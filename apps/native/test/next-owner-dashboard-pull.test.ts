import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as model from '../src-next/screens/settings-live/owner-dashboard/model';
import * as nowPlaying from '../../../packages/client-core/src/server-admin/now-playing';

// owner-dashboard 2: the phone Dashboard refreshes when pulled down, with the system ring under its top bar;
// the frame draws no Refresh button (web keeps one only because a browser can't draw the ring).
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.panel)] : [];

async function fixture(inline = false) {
  const h = hooks(), pulls: any[] = [], calls: string[] = [];
  const snapshot = {admission: 'owner', loading: false, sessions: [], alerts: [], jobs: []};
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, lifetime: {authority: {}}, refresh: async () => {calls.push('refresh');}};
  const reader = {dispose() {}, telemetry: async () => ({}), viewing: async () => ({})};
  const imports = {
    '../../../../../../packages/client-core/src/admin/data-folder': {dataFolderConfirmText: () => ''},
    '../../../../../../packages/client-core/src/http': {ApiError: class extends Error {}},
    react: h.react, 'react-native': {View: 'View'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 0})},
    '../../../../../../packages/client-core/src/server-clock': {serverNow: () => 0}, '../../../../../../packages/i18n/src': {defaultI18n: {has: () => true}},
    '../../../../../../packages/client-core/src/timer': {repeat: () => ({stop() {}})}, '../../../../../../packages/client-core/src/server-admin/now-playing': nowPlaying,
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'}, '../../../kit/theme': {useTheme: () => ({isTV: false, colors: {}})},
    '../../../kit/states/usePullRefresh': {usePullRefresh: (run: () => Promise<unknown>, onError: unknown, offset: number) => {const control = {type: 'Pull', props: {run, onError, offset}}; pulls.push(control); return control;}},
    '../../../screens/settings-live/owner-dashboard/reader': {createNativeOwnerDashboardReader: () => reader}, '../../../screens/settings-live/owner-dashboard/model': model,
    '../../../../../../packages/client-core/src/server-admin/updates-source': {SettingsUpdatesSource: class {subscribe = () => () => {}; getSnapshot = () => ({phase: 'idle', busy: false}); load = async () => {}; check = async () => {}; dispose() {}}},
    '../../../kit/settings/dashboard/update-card': {ownerUpdateView: () => undefined},
  };
  Object.assign(h.react, {useLayoutEffect: h.react.useEffect});
  Object.assign(h.react.default, {useLayoutEffect: h.react.useEffect});
  const modelHook = await screenModule(new URL('../src-next/pages/shared/settings/useOwnerDashboard.ts', import.meta.url), imports);
  const {OwnerDashboardPage: NativeOwnerDashboardPhone} = await screenModule(new URL('../src-next/pages/touch/settings/OwnerDashboardPage.tsx', import.meta.url), {
    ...imports,
    '../../shared/settings/useOwnerDashboard': modelHook,
    '../../../kit/frames': {SettingsPageFrame: 'SettingsPageFrame'},
    '../../../kit/settings/dashboard/OwnerDashboardContent': {OwnerDashboardContent: 'OwnerDashboardPage'},
  });
  const runtime = {text: {t: (id: string) => id}, artwork: () => undefined};
  const ports = {serverName: () => 'Portico demo', context: {navigation: {back() {}}}, requestId: () => 'request'};
  const tree = h.render(() => NativeOwnerDashboardPhone({controller, ports, runtime, inline}));
  const page = nodes(tree).find(node => node.type === 'OwnerDashboardPage');
  return {page, pulls, calls};
}

test('pulling the phone Dashboard down refreshes the Dashboard\'s own read, with the ring under the top bar', async () => {
  const f = await fixture();
  assert.ok(f.page);
  assert.equal(f.page.props.refreshControl, f.pulls.at(-1), 'the page scrolls under the pull control');
  assert.equal(f.page.props.onRefresh, undefined, 'no Refresh button is drawn');
  assert.equal(f.pulls.at(-1).props.offset, 47 + 48, 'the ring appears under the safe area and the 48 pt bar');
  await f.pulls.at(-1).props.run();
  assert.deepEqual(f.calls, ['refresh'], 'and the ring waits on that read');
  const inline = await fixture(true);
  assert.equal(inline.pulls.at(-1).props.offset, 0, 'inline, the page has no bar of its own');
});

test('the Dashboard page draws its scroll view with the pull control and no Refresh button', async () => {
  const h = hooks();
  const {OwnerDashboardContent: OwnerDashboardPage} = await screenModule(new URL('../src-next/kit/settings/dashboard/OwnerDashboardContent.tsx', import.meta.url), {
    '../../layout/TopBar': {TopBar: 'TopBar', BarSurface: 'BarSurface'},
    react: h.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    '../../controls': {Focusable: 'Focusable'}, '../../Icon': {Icon: 'Icon'}, '../../../platform/native-blur': {BlurView: 'BlurView'},
    '../../shell': {ServerHeader: 'ServerHeader'}, '../../theme': {useTheme: () => ({isTV: false, colors: {}, space: {}})}, '../../shell/visual': {alpha: (value: string) => value, shellType: () => ({})},
    '../../../screens/settings-live/owner-dashboard/model': model, './parts': {OwnerAlertCard: 'OwnerAlertCard', OwnerCalm: 'OwnerCalm', OwnerJobGroup: 'OwnerJobGroup', OwnerJobRow: 'OwnerJobRow', OwnerSection: 'OwnerSection', OwnerSubheading: 'OwnerSubheading'},
    './health': {OwnerHealthSection: 'OwnerHealthSection'}, './statistics': {OwnerStatisticsSection: 'OwnerStatisticsSection'},
    './streams': {OwnerCompactRows: 'OwnerCompactRows', OwnerNowPlayingHeader: 'OwnerNowPlayingHeader', OwnerShowAll: 'OwnerShowAll', OwnerStreamCards: 'OwnerStreamCards', OwnerStreamTotals: 'OwnerStreamTotals'},
    '../../artwork': {StandardBackdrop: 'StandardBackdrop'}, './update-card': {OwnerUpdateCard: 'OwnerUpdateCard'},
  });
  const pull = {type: 'Pull', props: {}};
  const props = {t: (id: string) => id, role: 'owner', serverName: 'Portico demo', topInset: 47, health: {degraded: false, status: 'Healthy', line: ''}, updated: 'Updated just now', loading: false,
    streams: [], streamTotals: {streams: 0, converting: 0, bandwidth: 0}, expanded: false, allStreams: [], people: '', alerts: [], jobs: [], healthWindow: 'live', viewingPeriod: '30d', refreshControl: pull,
    onShowAll() {}, onShowFewer() {}, onRetryAll() {}, onBack() {}, onRetry() {}, onHealthWindow() {}, onViewingPeriod() {}, onAcknowledge() {}, onFixPermissions() {}, onJobMenu() {}, onStop() {}, onStreamMenu() {}};
  const tree = h.render(() => OwnerDashboardPage(props));
  const header = nodes(tree).find(node => node.type === 'ServerHeader');
  const scroll = nodes(tree).find(node => node.type === 'ScrollView');
  assert.equal(scroll.props.refreshControl, pull);
  assert.ok(header, 'the header is drawn');
  assert.equal(nodes(tree).find(node => node.props?.testID === 'owner-dashboard-refresh'), undefined, 'no Refresh button in the header');
  assert.ok(header.props.facts.some((fact: {text: string}) => fact.text === 'Updated just now'), 'when it was last read stays');
});
