import * as presentation from '../src-next/screens/settings-live/server/channels/presentation.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children), ...nodes(tree.props?.footer)] : [];
const button = (tree: any, label: string) => {const found = nodes(tree).find(node => node.type === 'Button' && node.props.children.includes(label) || node.type === 'MenuRow' && node.props.label === label); assert.ok(found, label); return found;};
const theme = {isTV: false, platform: 'phone', colors: new Proxy({}, {get: (_target, key) => String(key)})};
const t = (id: string) => id;
function layoutHost() {
  const host = hooks(), effects: (() => void)[] = [], cleanups = new Set<{cleanup?: () => void}>();
  const useLayoutEffect = (run: () => void | (() => void), deps: readonly unknown[]) => {
    const slot = host.react.useRef(undefined);
    if (!slot.current || deps.some((value, index) => value !== slot.current.deps[index])) {
      const before = slot.current, next = {deps, cleanup: undefined as (() => void) | undefined}; slot.current = next; cleanups.add(next);
      effects.push(() => {before?.cleanup?.(); next.cleanup = run() || undefined;});
    }
  };
  Object.assign(host.react, {useLayoutEffect}); Object.assign(host.react.default, {useLayoutEffect});
  return {...host, flush: () => {for (const effect of effects.splice(0)) effect();}, dispose: () => {for (const value of cleanups) value.cleanup?.();}};
}
const source = (getSnapshot: () => any) => ({subscribe: () => () => {}, getSnapshot});
const common = {'../../../../../../../packages/i18n/src': {defaultI18n: {t, date: String, dateTime: String, number: String}},
  '../../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ChannelMark: 'ChannelMark', Artwork: 'Artwork'}, '../../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch', Select: 'Select', TextField: 'TextField'},
  '../../../../kit/overlays': {BottomSheet: 'BottomSheet', MenuRow: 'MenuRow'}, './presentation': presentation, '../../../../kit/shell/visual': {shellType: () => ({})}, '../../../../kit/theme': {useTheme: () => theme}};

async function channelsFixture() {
  const host = layoutHost(), calls: any[][] = [];
  const channels = ['First', 'Second'].map((name, index) => ({config: {id: `channel-${index}`, name, enabled: true, description: ''}, candidateCount: 5, state: 'ready', generatedThrough: ''}));
  const templates = [{id: 'ready-template', name: 'Ready template', applicable: true}, {id: 'blocked-template', name: 'Blocked template', applicable: false}];
  let active = true, state: any = {phase: 'ready', channels, templates}, editing: any = {phase: 'closed', busy: false, pending: false};
  const action = {busy: false}, logo = {busy: false}, presets = {busy: false, reconcile: false};
  const reader = {...source(() => state), load: async () => {}, templates: async () => {}, health: async (channel: any) => {calls.push(['health', channel]);}, closeHealth() {}};
  const editor = {...source(() => editing), edit: (channel: any) => {calls.push(['edit', channel]); editing = {...editing, phase: 'editing'};},
    template: (template: any, timezone: string) => {calls.push(['template', template, timezone]); editing = {...editing, phase: 'editing'};}, blank: async (timezone: string) => {calls.push(['blank', timezone]);}, resume() {}};
  const group = {reader, editor, actions: {...source(() => action), perform: async (...args: any[]) => {calls.push(['perform', ...args]); return true;}}, logos: source(() => logo), presets: source(() => presets), current: () => active, timezone: 'UTC', logoCapability: async () => false};
  const {Channels} = await screenModule(new URL('../src-next/screens/settings-live/server/channels/LibraryChannelsPage.tsx', import.meta.url), {
    ...common, react: host.react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../../packages/client-core/src/presentation/kit-model/channels': {channelMonograms: (domain: any) => new Map(domain.channels.map((channel: any) => [channel.sourceId, new Map([[channel.id, {text: 'CH', seed: channel.id}]])]))},
    './logo-artwork': {nativeChannelLogoArtwork: () => undefined}, './model': {}, './BlockPresets': {NativeBlockPresets: 'BlockPresets'}, './ChannelLogo': {NativeChannelLogo: 'ChannelLogo'}, './ChannelEditor': {NativeChannelEditor: 'ChannelEditor'},
  });
  let tree: any;
  const render = () => {tree = host.render(() => Channels({group, runtime: {text: {t}}})); host.flush(); return tree;};
  const menu = () => nodes(tree).filter(node => node.type === 'BottomSheet')[1];
  const templateMenu = () => nodes(tree).filter(node => node.type === 'BottomSheet')[0];
  render();
  return {calls, channels, templates, render, tree: () => tree, menu, templateMenu,
    openMenu: (name: string) => {nodes(tree).find(node => node.type === 'Focusable' && node.props.accessibilityLabel === name).props.onPress(); render();},
    openTemplates: () => {button(tree, 'builder.newTitle').props.onPress(); render();},
    setHealth: (value: any) => {state = {...state, selected: channels[0], health: value}; render();},
    list: (next: any[]) => {state = {...state, channels: next}; render();},
    dispose: () => {active = false; host.dispose();},
  };
}

test('A replaced channel menu rejects its old dismiss, edit, delete and move callbacks', async () => {
  const f = await channelsFixture();
  try {
    f.openMenu('First'); const old = f.menu();
    const edit = button(old, 'web.selection.edit').props.onPress, remove = button(old, 'web.libraryChannels.delete').props.onPress, move = button(old, 'web.profile.moveDown').props.onPress;
    old.props.onOpenChange(false); f.render(); f.openMenu('Second');
    old.props.onOpenChange(false); edit(); remove(); move(); f.render();
    assert.equal(f.menu().props.open, true); assert.equal(f.menu().props.title, 'Second'); assert.deepEqual(f.calls, []);
    button(f.menu(), 'web.selection.edit').props.onPress(); f.render(); assert.deepEqual(f.calls, [['edit', f.channels[1]]]);
  } finally {f.dispose();}
});

test('Template actions belong to their opening and an inapplicable template cannot dispatch directly', async () => {
  const f = await channelsFixture();
  try {
    f.openTemplates(); const old = f.templateMenu(), choose = button(old, 'Ready template').props.onPress, blank = button(old, 'web.libraryChannels.startFromScratch').props.onPress;
    old.props.onOpenChange(false); f.render(); f.openTemplates();
    old.props.onOpenChange(false); choose(); blank(); f.render(); assert.equal(f.templateMenu().props.open, true); assert.deepEqual(f.calls, []);
    const blocked = button(f.templateMenu(), 'Blocked template'); assert.equal(blocked.props.disabled, true); blocked.props.onPress(); f.render();
    assert.deepEqual(f.calls, []); assert.equal(f.templateMenu().props.open, true);
    button(f.templateMenu(), 'Ready template').props.onPress(); f.render(); assert.deepEqual(f.calls, [['template', f.templates[0], 'UTC']]);
  } finally {f.dispose();}
});

test('A channel-list replacement retires its menu even if an old channel object later reappears', async () => {
  const f = await channelsFixture();
  try {
    f.openMenu('First'); const old = f.menu(), edit = button(old, 'web.selection.edit').props.onPress;
    f.list([{...f.channels[0], config: {...f.channels[0].config}}, f.channels[1]]); assert.equal(f.menu().props.open, false);
    f.list(f.channels); edit(); f.render(); assert.deepEqual(f.calls, []); assert.equal(f.menu().props.open, false);
    f.openMenu('First'); assert.equal(f.menu().props.open, true);
  } finally {f.dispose();}
});

async function editorFixture() {
  const host = layoutHost(); let active = true, closes = 0;
  const config = {id: 'channel', name: 'Channel', description: '', timezone: 'UTC', enabled: true, defaultRuleId: 'rule', quality: {mode: 'automatic'}, blocks: [],
    rules: [{id: 'rule', name: 'Rule', mode: 'sequential', episodeMode: 'none', query: {libraryIds: ['library'], kinds: ['movie'], order: 'title', recentDays: 0, includeItemIds: [], excludeItemIds: []}}]};
  const makePreview = (title: string) => ({rules: [{ruleId: 'rule', eligible: 1, complete: true, sample: [{itemId: 'movie', title, durationMs: 3600000}]}], complete: true, durationMs: 3600000, firstDay: []});
  let state: any = {phase: 'editing', busy: false, pending: false, previewLoading: false, config, preview: makePreview('First sample')};
  const editor = {...source(() => state), close: () => {closes++; state = {...state, phase: 'closed'};}, change: (update: any) => {state = {...state, config: update(state.config), preview: undefined};}, preview: async () => {}};
  const presetState = {loaded: true, items: []}, group = {editor, fields: {select: async () => {}}, presets: source(() => presetState), current: () => active, libraries: [{id: 'library', name: 'Library'}]};
  const motion = {reduce: false, duration: () => 0};
  const {NativeChannelEditor} = await screenModule(new URL('../src-next/screens/settings-live/server/channels/ChannelEditor.tsx', import.meta.url), {'../../../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true}, 
    ...common, react: host.react, 'react-native': {Animated: {View: 'AnimatedView', Value: class {constructor(_value: number) {}}, timing: () => ({start() {}, stop() {}})}, Easing: {}, Modal: 'Modal', ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    '../../../../../../../packages/client-core/src/server-admin/channel-conditions': {applyChannelConditions: (value: any) => value}, '../../../../../../../packages/client-core/src/library-channels': {librarySampleLabel: (value: any) => value.showTitle || value.title},
    '../../../../kit/useMotion': {useMotion: () => motion}, './ChannelConditions': {NativeChannelConditions: 'ChannelConditions'}, './ChannelPins': {NativeChannelPins: 'ChannelPins'}, './ChannelHours': {NativeChannelHours: 'ChannelHours'},
  });
  const isCurrent = () => active; let tree: any;
  const render = () => {tree = host.render(() => NativeChannelEditor({group, isCurrent, runtime: {text: {t, locale: 'en-US'}, artwork: () => undefined}})); host.flush(); return tree;};
  render();
  return {render, tree: () => tree, closes: () => closes, modal: () => nodes(tree).filter(node => node.type === 'Modal')[1],
    openPreview: () => {button(nodes(tree).find(node => node.props?.testID === 'channel-preview-bar'), 'builder.preview').props.onPress(); render();},
    incompletePreview: () => {state = {...state, preview: {...state.preview, complete: false, rules: state.preview.rules.map((rule: any) => ({...rule, complete: false}))}}; render();},
    changeConfig: () => {state = {...state, config: {...state.config, name: 'Changed'}, preview: undefined}; render();},
    changePreview: () => {state = {...state, preview: makePreview('Replacement sample')}; render();},
    dispose: () => {active = false; host.dispose();},
  };
}

test('Preview Back and native close from the first opening cannot close a later preview', async () => {
  const f = await editorFixture();
  try {
    const oldEditorClose = nodes(f.tree()).find(node => node.type === 'Modal').props.onRequestClose;
    f.openPreview(); const first = f.modal(), back = button(first, 'action.back').props.onPress;
    assert.equal(first.props.visible, true); back(); f.render(); assert.equal(f.modal().props.visible, false);
    f.openPreview(); back(); first.props.onRequestClose(); oldEditorClose(); f.render();
    assert.equal(f.modal().props.visible, true); assert.equal(f.closes(), 0);
    button(f.modal(), 'action.back').props.onPress(); f.render(); button(f.tree(), 'builder.cancel').props.onPress(); assert.equal(f.closes(), 1);
  } finally {f.dispose();}
});

for (const change of ['config', 'result'] as const) test(`A changed ${change} retires the preview and leaves the current editor cancellable`, async () => {
  const f = await editorFixture();
  try {
    f.openPreview(); const old = f.modal();
    if (change === 'config') f.changeConfig(); else f.changePreview();
    assert.equal(f.modal().props.visible, false); old.props.onRequestClose(); f.render(); assert.equal(f.modal().props.visible, false);
    button(f.tree(), 'builder.cancel').props.onPress(); assert.equal(f.closes(), 1);
  } finally {f.dispose();}
});

test('A replacement preview result opens independently and cannot be closed by the prior result callback', async () => {
  const f = await editorFixture();
  try {
    f.openPreview(); const old = f.modal(); f.changePreview(); f.openPreview(); old.props.onRequestClose(); f.render();
    assert.equal(f.modal().props.visible, true);
    assert.equal(nodes(f.modal()).find(node => node.type === 'Artwork').props.alt, 'Replacement sample');
  } finally {f.dispose();}
});


test('channel toggles live in the guarded menu and health facts use the approved captions and hierarchy', async () => {
  const f = await channelsFixture();
  try {
    assert.ok(!nodes(f.tree()).some(node => node.type === 'Switch'));
    f.openMenu('First'); const toggle = button(f.menu(), 'web.libraryChannels.turnOff');
    toggle.props.onPress(); toggle.props.onPress(); f.render();
    assert.deepEqual(f.calls, [['perform', f.channels[0], 'toggle']]);
    f.openMenu('First'); button(f.menu(), 'web.libraryChannels.checkHealth').props.onPress();
    f.setHealth({healthy: false, candidates: 2, unresolved: 1, scheduledEntries: 11, pendingGeneration: true, generatedThrough: '2026-10-29T16:00:00Z'});
    const health = nodes(f.tree()).find(node => node.type === 'BottomSheet' && node.props.description === 'web.libraryChannels.healthLede')!;
    assert.equal(health.props.open, true);
    for (const label of ['web.libraryChannels.healthCandidates', 'web.libraryChannels.healthUnresolved', 'web.libraryChannels.healthNewSchedule', 'web.libraryChannels.healthBeingMade']) assert.ok(nodes(health).some(node => node.type === 'Text' && node.props.children.includes(label)), label);
    assert.equal(button(health.props.footer, 'server.remote.checkAgain').props.variant, undefined);
    assert.equal(button(health.props.footer, 'action.done').props.variant, 'glass');
    button(health.props.footer, 'server.remote.checkAgain').props.onPress();
    assert.equal(f.calls.filter(value => value[0] === 'health').length, 2);
  } finally {f.dispose();}
});


test('the fixed preview bar and full preview both show lower-bound counts and actual total hours', async () => {
  const f = await editorFixture();
  const text = (node: any): string => Array.isArray(node) ? node.map(text).join('') : node?.props ? text(node.props.children) : typeof node === 'string' || typeof node === 'number' ? String(node) : '';
  try {
    f.incompletePreview();
    const bar = nodes(f.tree()).find(node => node.props?.testID === 'channel-preview-bar');
    assert.ok(text(bar).includes('At least 1 title matches')); assert.ok(text(bar).includes('≥ 1.0 h'));
    f.openPreview(); assert.ok(text(f.modal()).includes('At least 1 title matches')); assert.ok(text(f.modal()).includes('≥ 1.0 h'));
  } finally {f.dispose();}
});
