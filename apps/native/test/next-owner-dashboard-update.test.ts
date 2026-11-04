import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as model from '../src-next/screens/settings-live/owner-dashboard/model';
import * as nowPlaying from '../../../packages/client-core/src/server-admin/now-playing';

// owner-dashboard 2b: the update has its own tinted card at the end of Alerts, and Portico never installs an update by itself.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const texts = (tree: any) => nodes(tree).filter(node => node.type === 'Text').map(node => [node.props.children].flat().join(''));
const t = (id: string, values?: Record<string, unknown>) => values ? `${id}:${JSON.stringify(values)}` : id;
const build = (version: string, url = '') => ({version, buildId: 'b', sourceDigest: 'd', builtAt: '', channel: 'stable', notes: '', url});
const report = (latest?: ReturnType<typeof build>) => ({current: build('0.1.0'), channel: 'stable', feedConfigured: true, latest: latest ?? null, updateAvailable: !!latest, checkedAt: '', status: latest ? 'update-available' : 'current', message: '', autoInstall: false});

const cardModule = async (react: any) => screenModule(new URL('../src-next/screens/settings-live/owner-dashboard/update-card.tsx', import.meta.url), {
  react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
  '../../../kit/controls': {Button: 'Button'}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/theme': {useTheme: () => ({isTV: false, colors: new Proxy({}, {get: (_t, key) => String(key)})})},
  '../../../kit/shell/visual': {alpha: (value: string) => value, shellType: () => ({})},
});

test('the card is drawn only while the release check reports a newer build, and release notes open only an https page', async () => {
  const {ownerUpdateView} = await cardModule(hooks().react);
  assert.equal(ownerUpdateView({phase: 'idle', busy: false}), undefined);
  assert.equal(ownerUpdateView({phase: 'ready', busy: false, report: report()}), undefined, 'nothing newer: no card');
  assert.deepEqual(ownerUpdateView({phase: 'ready', busy: true, report: report(build('0.2.0', 'https://example.test/notes'))}),
    {latest: '0.2.0', current: '0.1.0', notesURL: 'https://example.test/notes', checking: true, checkFailed: false});
  for (const url of ['', 'http://example.test/notes', 'javascript:alert(1)', 'portico://x'])
    assert.equal(ownerUpdateView({phase: 'ready', busy: false, report: report(build('0.2.0', url))})?.notesURL, undefined, url);
});

test('the card names both versions, says Portico never installs it, and offers release notes and a check', async () => {
  const h = hooks(), {OwnerUpdateCard} = await cardModule(h.react);
  const calls: string[] = [];
  const draw = (update: any) => h.render(() => OwnerUpdateCard({update, t, onReleaseNotes: () => calls.push('notes'), onCheck: () => calls.push('check')}));
  const tree = draw({latest: '0.2.0', current: '0.1.0', notesURL: 'https://example.test/notes', checking: false});
  assert.deepEqual(texts(tree), ['next.shell.updateAvailable', 'next.shell.version:{"version":"0.2.0"} · next.shell.youHaveVersion:{"version":"0.1.0"}', 'web.maintenance.updatesLede']);
  const buttons = nodes(tree).filter(node => node.type === 'Button');
  assert.deepEqual(buttons.map(button => [button.props.testID, [button.props.children].flat().join('')]), [['owner-update-notes', 'web.maintenance.releaseNotes'], ['owner-update-check', 'server.updates.check']]);
  buttons.forEach(button => button.props.onPress());
  assert.deepEqual(calls, ['notes', 'check']);
  const checking = nodes(draw({latest: '0.2.0', current: '0.1.0', checking: true})).filter(node => node.type === 'Button');
  assert.deepEqual(checking.map(button => button.props.testID), ['owner-update-check'], 'no release notes without a page to open');
  assert.equal(checking[0].props.disabled, true); assert.equal(checking[0].props.loading, true);
});

const pageProps = (role: string, update?: unknown) => ({t, role, serverName: 'Portico demo', topInset: 47, health: {degraded: false, status: 'Healthy', line: ''}, loading: false,
  streams: [], streamTotals: {streams: 0, converting: 0, bandwidth: 0}, expanded: false, allStreams: [], people: '', alerts: [], jobs: [], healthWindow: '1h', viewingPeriod: '7d', update,
  onShowAll() {}, onShowFewer() {}, onRetryAll() {}, onBack() {}, onRetry() {}, onHealthWindow() {}, onViewingPeriod() {}, onAcknowledge() {}, onFixPermissions() {}, onJobMenu() {}, onStop() {}, onStreamMenu() {}});

const pageModule = (react: any) => screenModule(new URL('../src-next/screens/settings-live/owner-dashboard/page.tsx', import.meta.url), {
  react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
  '../../../kit/controls': {Focusable: 'Focusable'}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../platform/native-blur': {BlurView: 'BlurView'},
  '../../../kit/shell': {ServerBand: 'ServerBand'}, '../../../kit/theme': {useTheme: () => ({isTV: false, colors: {}})}, '../../../kit/shell/visual': {alpha: (value: string) => value, shellType: () => ({})},
  './model': model, './parts': {OwnerAlertCard: 'OwnerAlertCard', OwnerCalm: 'OwnerCalm', OwnerJobGroup: 'OwnerJobGroup', OwnerJobRow: 'OwnerJobRow', OwnerSection: 'OwnerSection', OwnerSubheading: 'OwnerSubheading'},
  './health': {OwnerHealthSection: 'OwnerHealthSection'}, './statistics': {OwnerStatisticsSection: 'OwnerStatisticsSection'},
  './streams': {OwnerCompactRows: 'OwnerCompactRows', OwnerNowPlayingHeader: 'OwnerNowPlayingHeader', OwnerShowAll: 'OwnerShowAll', OwnerStreamCards: 'OwnerStreamCards', OwnerStreamTotals: 'OwnerStreamTotals'},
  '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, './update-card': {OwnerUpdateCard: 'OwnerUpdateCard'},
});

test('the owner\'s Alerts section ends with the update card, after the alerts or the calm line; administrators never see it', async () => {
  const h = hooks();
  const {OwnerDashboardPage} = await pageModule(h.react);
  const update = {latest: '0.2.0', current: '0.1.0', checking: false};
  const alerts = nodes(h.render(() => OwnerDashboardPage(pageProps('owner', update)))).find(node => node.type === 'OwnerSection' && node.props.title === 'next.dashboard.alerts');
  const order = nodes(alerts.props.children).map(node => node.type).filter(type => type === 'OwnerCalm' || type === 'OwnerUpdateCard');
  assert.deepEqual(order, ['OwnerCalm', 'OwnerUpdateCard']);
  assert.equal(nodes(alerts).find(node => node.type === 'OwnerUpdateCard').props.update, update);
  assert.equal(alerts.props.sub, undefined, 'the update is not counted as an alert');
  assert.ok(!nodes(h.render(() => OwnerDashboardPage(pageProps('owner')))).some(node => node.type === 'OwnerUpdateCard'), 'no report, no card');
  assert.ok(!nodes(h.render(() => OwnerDashboardPage(pageProps('administrator', update)))).some(node => node.type === 'OwnerUpdateCard'));
});

async function phone(admission: 'owner' | 'administrator') {
  const h = hooks(), sources: any[] = [], opened: string[] = [], calls: string[] = [];
  let effects: (() => void | (() => void))[] = [];
  const cleanups: (() => void)[] = [];
  Object.assign(h.react, {useEffect: (fn: () => void | (() => void)) => {effects.push(fn);}}); Object.assign(h.react.default, {useEffect: h.react.useEffect});
  const snapshot = {admission, loading: false, sessions: [], alerts: [], jobs: []};
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, lifetime: {authority: {}, current: () => true}, refresh: async () => {calls.push('refresh');}};
  const updatesApi = {request: async () => ({})};
  const reader = {dispose() {}, telemetry: async () => ({}), playSummary: async () => ({}), sessions: async () => ({items: []}), updates: updatesApi};
  class SettingsUpdatesSource {
    state: any = {phase: 'ready', busy: false, report: report(build('0.2.0', 'https://example.test/notes'))};
    constructor(readonly api: unknown, readonly isCurrent: () => boolean) {sources.push(this);}
    subscribe = () => () => {}; getSnapshot = () => this.state;
    load = async () => {calls.push('load');}; check = async () => {calls.push('check');}; dispose = () => {calls.push('dispose');};
  }
  const card = await cardModule(h.react);
  const {NativeOwnerDashboardPhone} = await screenModule(new URL('../src-next/screens/settings-live/owner-dashboard/use-owner-dashboard.tsx', import.meta.url), {
    react: h.react, 'react-native': {View: 'View', Linking: {openURL: async (url: string) => {opened.push(url);}}}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 0})},
    '../../../../../../packages/client-core/src/server-clock': {serverNow: () => 0}, '../../../../../../packages/i18n/src': {defaultI18n: {has: () => true}},
    '../../../../../../packages/client-core/src/timer': {repeat: () => ({stop() {}})}, '../../../../../../packages/client-core/src/server-admin/now-playing': nowPlaying,
    '../../../../../../packages/client-core/src/server-admin/updates-source': {SettingsUpdatesSource},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'}, '../../../kit/theme': {useTheme: () => ({isTV: false, colors: {}})},
    '../../../kit/states/usePullRefresh': {usePullRefresh: (run: () => Promise<unknown>) => ({type: 'Pull', props: {run}})},
    './reader': {createNativeOwnerDashboardReader: () => reader}, './model': model, './move-sheet': {OwnerMoveSheet: 'OwnerMoveSheet'}, './page': {OwnerDashboardPage: 'OwnerDashboardPage'},
    './update-card': card,
  });
  const runtime = {text: {t: (id: string) => id}, artwork: () => undefined, reportError() {}};
  const ports = {serverName: () => 'Portico demo', context: {navigation: {back() {}}}, requestId: () => 'request'};
  const render = () => {const tree = h.render(() => NativeOwnerDashboardPhone({controller, ports, runtime})); const run = effects; effects = []; run.forEach(effect => {const cleanup = effect(); if (cleanup) cleanups.push(cleanup);}); return nodes(tree).find(node => node.type === 'OwnerDashboardPage');};
  // The page's clock is an interval: every test ends by running the effects' cleanups, as an unmount does.
  return {render, sources, opened, calls, updatesApi, unmount: () => cleanups.splice(0).forEach(cleanup => cleanup())};
}

test('the owner\'s Dashboard reads the release check on its own lifetime, again on pull, and a pressed check asks the server once', async () => {
  const f = await phone('owner');
  try {
  const page = f.render();
  assert.equal(f.sources.length, 1); assert.equal(f.sources[0].api, f.updatesApi);
  assert.deepEqual(page.props.update, {latest: '0.2.0', current: '0.1.0', notesURL: 'https://example.test/notes', checking: false, checkFailed: false});
  assert.ok(f.calls.includes('load'), 'read when the Dashboard shows');
  f.calls.length = 0; await page.props.refreshControl.props.run();
  assert.deepEqual(f.calls.filter(call => call !== 'refresh'), ['load'], 'pulling reads it again');
  f.calls.length = 0; page.props.onCheckUpdate(); assert.deepEqual(f.calls, ['check']);
  page.props.onReleaseNotes(); assert.deepEqual(f.opened, ['https://example.test/notes']);
  f.unmount(); assert.ok(f.calls.includes('dispose'), 'leaving the Dashboard retires its release check');
  } finally {f.unmount();}
});

test('an administrator\'s Dashboard never reads the owner\'s release check', async () => {
  const f = await phone('administrator');
  try {
    const page = f.render();
    assert.equal(f.sources.length, 0); assert.equal(page.props.update, undefined); assert.ok(!f.calls.includes('load'));
  } finally {f.unmount();}
});

test('Sol 8 on #830: a pressed check that fails keeps the last report and draws frame 8\'s error card under the update, with Try again', async () => {
  const f = await phone('owner');
  try {
    f.render(); const source = f.sources[0], before = source.state.report;
    source.state = {phase: 'ready', busy: false, report: before, error: 'check'};
    const page = f.render();
    assert.equal(page.props.update.latest, '0.2.0', 'the old report stays for a retry');
    assert.equal(page.props.update.checkFailed, true, 'the failure reaches the page');
    source.state = {phase: 'ready', busy: false, report: before, error: 'read'};
    assert.equal(f.render().props.update.checkFailed, false, 'a failed background read stays quiet');
  } finally {f.unmount();}
});

test('the page draws the failed check as an error card after the update card; Try again checks again', async () => {
  const h = hooks(), calls: string[] = [];
  const {OwnerDashboardPage} = await pageModule(h.react);
  const update = {latest: '0.2.0', current: '0.1.0', checking: false, checkFailed: true};
  const alerts = nodes(h.render(() => OwnerDashboardPage({...pageProps('owner', update), onCheckUpdate: () => calls.push('check')}))).find(node => node.type === 'OwnerSection' && node.props.title === 'next.dashboard.alerts');
  const after = nodes(alerts.props.children).filter(node => node.type === 'OwnerCalm' || node.type === 'OwnerUpdateCard');
  assert.deepEqual(after.map(node => node.type), ['OwnerCalm', 'OwnerUpdateCard', 'OwnerCalm']);
  const failed = after[2];
  assert.equal(failed.props.tone, 'error'); assert.equal(failed.props.title, 'next.dashboard.updateCheckFailed'); assert.equal(failed.props.body, 'next.dashboard.updateCheckFailedBody');
  assert.equal(failed.props.action.label, 'next.shell.tryAgain'); failed.props.action.onPress(); assert.deepEqual(calls, ['check']);
  assert.ok(!nodes(h.render(() => OwnerDashboardPage(pageProps('owner', {...update, checkFailed: false})))).some(node => node.type === 'OwnerCalm' && node.props.tone === 'error'));
});
