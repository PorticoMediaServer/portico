import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children), ...nodes(value.props.footer)] : [];
const text = (value: any): string => Array.isArray(value) ? value.map(text).join('') : typeof value === 'string' ? value : value?.props ? text(value.props.children) : '';
const t = (id: string) => id;
const colors = new Proxy({}, {get: (_target, key) => String(key)});

/** Troubleshooting's shared hooks with these namespaces (any other import an empty one), and the page on them. */
async function troubleshootingPage(react: unknown, hookModules: Record<string, unknown>) {
  const core = ['server-clock', 'timer', 'server-admin/messages-source', 'server-administration', 'admin/skipped-files', 'server-admin/feedback-source', 'console',
    'server-admin/console-catalogue', 'server-admin/console-words', 'server-admin/owner-app-logs', 'server-admin/support-next', 'tolerant'].map(path => `../../../../../../packages/client-core/src/${path}`);
  const empty = Object.fromEntries([...core, '../../../platform/abort-signals', '../../../platform/diagnostics-save', '../../../screens/settings-live/lifetime',
    '../../../screens/settings-live/server/storage/space-source'].map(path => [path, {}]));
  const hook = await screenModule(new URL('../src-next/pages/shared/settings/useServerTroubleshooting.ts', import.meta.url), {...empty, react, 'react-native': {AppState: {}},
    '../../../../../../packages/i18n/src': {defaultI18n: {t, time: () => '12:30', has: () => true}},
    '../../../../../../packages/client-core/src/server-clock': {serverNow: () => 0},
    '../../../../../../packages/client-core/src/timer': {repeat: () => ({stop() {}})},
    '../../../../../../packages/client-core/src/server-admin/support-next': {OWNER_SUPPORT_WINDOWS: [], SUPPORT_COMPONENTS: []}, ...hookModules});
  const parts = Object.fromEntries(['DetailWindowCard', 'FactCard', 'LiveMark', 'MessageLine', 'MessageLog', 'ReportText', 'TroubleshootingRow'].map(name => [name, name]));
  const text = Object.fromEntries(['PageActions', 'PageColumn', 'PageHeading', 'PageList', 'PageNote', 'SheetBody'].map(name => [name, name]));
  return screenModule(new URL('../src-next/pages/touch/settings/ServerTroubleshootingPage.tsx', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../../kit/controls': {Button: 'Button', Chip: 'Chip', Select: 'Select', Switch: 'Switch', TextField: 'TextField', Tabs: 'Tabs'},
    '../../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame'}, '../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'},
    '../../../kit/overlays/surface': {OverlayFootRow: 'OverlayFootRow'}, '../../../kit/settings/PageText': text, '../../../kit/settings/TroubleshootingParts': parts,
    '../../shared/settings/ServerBroadcastSheet': {ServerBroadcastSheet: 'BroadcastSheet'}, '../../shared/settings/useServerTroubleshooting': hook});
}

test('Messages filters by level and kind with their exact picker openings, Live is a switch, and a line\'s time, level and values stay separate from its message', async () => {
  const host = hooks();
  let current = true;
  const calls: unknown[] = [];
  const state: any = {phase: 'ready', level: '', kind: '', live: true, detailMinutes: 30, lines: [{sequence: 1, at: '2026-10-01T12:30:00Z', level: 'warn', kind: 'scan', message: 'Fixture message', detail: 'library: Films'}]};
  const source = {subscribe: () => () => {}, getSnapshot: () => state, load: async () => {calls.push('refresh');}};
  const group = {source: {...source, setAtTop() {}, showWaiting() {}}, current: () => current, subscribeFailure: () => () => {}, getFailure: () => false, clearFailure() {},
    setLive: (value: boolean) => {calls.push(['live', value]); state.live = value;},
    select: async (level: string, kind: string) => {calls.push([level, kind]); state.level = level; state.kind = kind;}};
  const {Messages} = await troubleshootingPage(host.react, {
    '../../../../../../packages/client-core/src/server-admin/messages-source': {KEEP_MESSAGE_DAYS: [7, 14, 30], MESSAGE_KINDS: ['server', 'playback', 'scan', 'network']},
    '../../../../../../packages/client-core/src/server-administration': {LOG_DETAIL_MINUTES: [30], logLevels: ['error', 'warn', 'info', 'debug']},
  });
  const render = () => host.render(() => Messages({group, runtime: {text: {t}}}));
  const chip = (label: string) => nodes(render()).find(node => node.type === 'Chip' && node.props.accessibilityLabel === label)!;
  const menu = () => nodes(render()).find(node => node.type === 'Menu')!;
  chip('web.logs.level').props.onPress(); const first = menu();
  first.props.onOpenChange(false);
  chip('feedback.category').props.onPress(); const second = menu();
  first.props.items.find((item: any) => item.id === 'error').onSelect(); first.props.onOpenChange(false);
  assert.equal(menu().props.open, true); assert.deepEqual(calls, [], 'a closed picker chooses nothing');
  const choose = second.props.items.find((item: any) => item.id === 'scan').onSelect;
  choose(); choose(); assert.deepEqual(calls, [['', 'scan']], 'the kind keeps the level chosen'); assert.equal(menu().props.open, false);
  const live = nodes(render()).find(node => node.type === 'Switch')!; assert.equal(live.props.checked, true);
  assert.equal(nodes(render()).find(node => node.type === 'LiveMark')!.props.label, t('web.logs.following'));
  live.props.onCheckedChange(false); assert.deepEqual(calls.at(-1), ['live', false]);
  // A line keeps its time, level and kind apart from its message and values (the kit line draws them in the mono face).
  const line = nodes(render()).find(node => node.type === 'MessageLine')!;
  assert.deepEqual([line.props.time, line.props.level, line.props.kind, line.props.tone], ['12:30', 'web.logs.level.warn', 'server.logCategory.scan', 'warn']);
  assert.equal(line.props.message, 'Fixture message'); assert.equal(line.props.detail, 'library: Films');
  current = false; chip('web.logs.level').props.onPress(); live.props.onCheckedChange(true);
  assert.equal(menu().props.open, false); assert.equal(calls.length, 2);
});

test('Feedback opens the shared Message everyone sheet with the same original ports and runtime', async () => {
  const host = hooks(); const effects: (() => void)[] = [];
  const layout = (fn: () => void, deps: unknown[]) => {const slot = host.react.useRef(undefined); if (!slot.current || deps.some((value, index) => value !== slot.current[index])) {slot.current = deps; effects.push(fn);}};
  Object.assign(host.react, {useLayoutEffect: layout}); Object.assign(host.react.default, {useLayoutEffect: layout});
  let current = true;
  const state = {phase: 'ready', reports: []};
  const group = {source: {subscribe: () => () => {}, getSnapshot: () => state}, current: () => current};
  const ports = {context: {}}, runtime = {text: {t}};
  const {Feedback: NativeFeedback} = await troubleshootingPage(host.react, {});
  const render = () => {let tree = host.render(() => NativeFeedback({group, ports, runtime})); if (effects.length) {effects.splice(0).forEach(effect => effect()); tree = host.render(() => NativeFeedback({group, ports, runtime}));} return tree;};
  const sheet = () => nodes(render()).find(node => node.type === 'BroadcastSheet')!;
  const launch = () => nodes(render()).find(node => node.type === 'Button' && text(node) === 'server.messageEveryone')!.props.onPress();
  assert.equal(sheet().props.open, false); launch();
  assert.equal(sheet().props.open, true); assert.equal(sheet().props.ports, ports); assert.equal(sheet().props.runtime, runtime);
  sheet().props.onOpenChange(false); current = false; launch(); assert.equal(sheet().props.open, false);
});

test('Troubleshooting is a settings page with tabs: Messages and stuck files for an administrator, then Feedback, Reports and Support for the owner', async () => {
  const host = hooks(), layout = {useLayoutEffect() {}};
  // Rendering only: the page's server groups are created in layout effects, which this host doesn't run.
  const react = {...host.react, ...layout, default: {...host.react.default, ...layout}};
  const page = await troubleshootingPage(react, {});
  const runtime = {text: {t}, reportError() {}};
  const render = (role: string) => host.render(() => page.ServerTroubleshootingPage({ports: {context: {account: {account: {role}, canManage: true}}}, runtime, back: {label: 'Settings', onPress() {}}}));
  const owner = render('owner');
  assert.equal(owner.type, 'SettingsPageFrame'); assert.equal(owner.props.title, 'settings.server.troubleshooting');
  assert.deepEqual(owner.props.tabs.props.items.map((item: any) => item.value), ['messages', 'skipped', 'feedback', 'reports', 'support']);
  assert.deepEqual(render('administrator').props.tabs.props.items.map((item: any) => item.value), ['messages', 'skipped']);
});
