import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children), ...nodes(value.props.footer)] : [];
const text = (value: any): string => Array.isArray(value) ? value.map(text).join('') : typeof value === 'string' ? value : value?.props ? text(value.props.children) : '';
const t = (id: string) => id;
const colors = new Proxy({}, {get: (_target, key) => String(key)});
const common = {
  'react-native': {Text: 'Text', View: 'View', AppState: {}},
  '../../../../../../../packages/i18n/src': {defaultI18n: {t, time: () => '12:30', has: () => true}},
  '../../../../kit/controls': {Button: 'Button', Chip: 'Chip', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
  '../../../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'},
  '../../../../kit/shell/visual': {shellType: () => ({}), alpha: (value: string) => value},
  '../../../../kit/theme': {useTheme: () => ({colors})},
  '../../lifetime': {}, '../storage/space-source': {},
};

test('Messages filters use their exact picker opening, Live is a switch, and log metadata stays separate', async () => {
  const host = hooks();
  let current = true;
  const calls: unknown[] = [];
  const state: any = {phase: 'ready', level: '', category: '', live: true, lines: [{sequence: 1, at: '2026-10-01T12:30:00Z', level: 'warn', category: 'scan', message: 'Fixture message'}]};
  const source = {subscribe: () => () => {}, getSnapshot: () => state, load: async () => {calls.push('refresh');}};
  const group = {source, current: () => current, subscribeFailure: () => () => {}, getFailure: () => false, clearFailure() {},
    setLive: (value: boolean) => {calls.push(['live', value]); state.live = value;},
    select: async (level: string, category: string) => {calls.push([level, category]); state.level = level; state.category = category;}};
  const {Messages} = await screenModule(new URL('../src-next/screens/settings-live/server/troubleshooting/Messages.tsx', import.meta.url), {
    ...common, react: host.react,
    '../../../../../../../packages/client-core/src/server-admin/messages-source': {},
    '../../../../../../../packages/client-core/src/server-administration': {LOG_DETAIL_MINUTES: [30], logLevels: ['error', 'warn', 'info', 'debug'], logCategories: ['server', 'playback', 'scan', 'network', 'client']},
    '../storage/forms': {useFormState: () => ({}), useStorageSet: () => ({})},
  });
  const render = () => host.render(() => Messages({group, forms: {forms: {logs: {}}}, runtime: {text: {t}}}));
  const chip = (label: string) => nodes(render()).find(node => node.type === 'Chip' && node.props.accessibilityLabel === label)!;
  const menu = () => nodes(render()).find(node => node.type === 'Menu')!;
  chip('web.logs.level').props.onPress(); const first = menu();
  first.props.onOpenChange(false);
  chip('feedback.category').props.onPress(); const second = menu();
  first.props.items.find((item: any) => item.id === 'error').onSelect(); first.props.onOpenChange(false);
  assert.equal(menu().props.open, true); assert.deepEqual(calls, []);
  const choose = second.props.items.find((item: any) => item.id === 'scan').onSelect;
  choose(); choose(); assert.deepEqual(calls, [['', 'scan']]); assert.equal(menu().props.open, false);
  const live = nodes(render()).find(node => node.type === 'Switch')!; assert.equal(live.props.checked, true);
  assert.ok(nodes(render()).some(node => node.type === 'Text' && text(node) === t('web.logs.following')));
  live.props.onCheckedChange(false); assert.deepEqual(calls.at(-1), ['live', false]);
  const line = nodes(render()).find(node => node.type === 'View' && node.props.key === 1)!;
  assert.ok(line); const metadata = line.props.children[0];
  assert.deepEqual(nodes(metadata).filter(node => node.type === 'Text').map(text), ['12:30', 'web.logs.level.warn', 'server.logCategory.scan']);
  const meta = nodes(metadata).filter(node => node.type === 'Text');
  assert.ok(meta.every(node => node.props.style.fontFamily === 'monospace'));
  assert.equal(meta[1].props.style.color, 'amber');
  assert.equal(nodes(line).find(node => node.type === 'Text' && text(node) === 'Fixture message')?.props.selectable, true);
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
  const {NativeFeedback} = await screenModule(new URL('../src-next/screens/settings-live/server/troubleshooting/Feedback.tsx', import.meta.url), {
    ...common, react: host.react,
    '../../../../../../../packages/client-core/src/server-admin/feedback-source': {},
    '../../../../../../../packages/client-core/src/server-admin/console-catalogue': {},
    '../../../../../../../packages/client-core/src/server-admin/console-words': {},
    '../BroadcastSheet': {NativeBroadcastSheet: 'BroadcastSheet'},
  });
  const render = () => {let tree = host.render(() => NativeFeedback({group, ports, runtime})); if (effects.length) {effects.splice(0).forEach(effect => effect()); tree = host.render(() => NativeFeedback({group, ports, runtime}));} return tree;};
  const sheet = () => nodes(render()).find(node => node.type === 'BroadcastSheet')!;
  const launch = () => nodes(render()).find(node => node.type === 'Button' && text(node) === 'server.messageEveryone')!.props.onPress();
  assert.equal(sheet().props.open, false); launch();
  assert.equal(sheet().props.open, true); assert.equal(sheet().props.ports, ports); assert.equal(sheet().props.runtime, runtime);
  sheet().props.onOpenChange(false); current = false; launch(); assert.equal(sheet().props.open, false);
});
