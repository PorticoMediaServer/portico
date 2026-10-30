import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {releaseSheet, resistSheet, sheetHeights} from '../src-next/kit/overlays/geometry.ts';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children)] : [];
const flatStyle = (value: any): any => Array.isArray(value) ? Object.assign({}, ...value.map(flatStyle)) : value || {};
function ancestry(tree: any, id: string, path: any[] = []): any[] | undefined {
  if (Array.isArray(tree)) {for (let index = 0; index < tree.length; index++) {const found = ancestry(tree[index], id, [...path, index]); if (found) return found;} return undefined;}
  if (!tree || typeof tree !== 'object') return undefined;
  const next = [...path, {type: tree.type, key: tree.props?.key}];
  return tree.props?.testID === id ? next : ancestry(tree.props?.children, id, next);
}

async function fixture() {
  const host = hooks(), lifecycle: any[][] = [], dismissals: any[][] = [];
  const theme: any = {isTV: false, platform: 'phone', colors: new Proxy({}, {get: (_target, key) => String(key)})};
  let viewport = {width: 390, height: 844}, gesture: any;
  const native = {Keyboard: {}, Modal: 'Modal', ScrollView: 'ScrollView', TextInput: {}, Text: 'Text', View: 'View',
    StyleSheet: {create: (value: any) => value, absoluteFill: {position: 'absolute', inset: 0}}, useWindowDimensions: () => viewport};
  const motion = {reduce: false, timing: () => ({}), spring: () => ({})};
  const common = {react: host.react, 'react-native': native, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34, left: 0, right: 0})},
    '../../../../../packages/i18n/src/index': {defaultI18n: {t: (id: string) => id}}, '../theme': {useTheme: () => theme}, '../controls/Button': {Button: 'Button'}};
  const {BottomSheet} = await screenModule(new URL('../src-next/kit/overlays/BottomSheet.tsx', import.meta.url), {'../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true}, 
    ...common, './OverlayPaintContext': {OverlayPaintDescendants: 'OverlayPaintDescendants', useOverlayPaintMode: () => ({visible: true, interactive: true, root: true})},
    'react-native-gesture-handler': {GestureDetector: 'GestureDetector', Gesture: {Pan: () => {gesture = {}; for (const name of ['enabled', 'onBegin', 'onUpdate', 'onEnd', 'onFinalize']) gesture[name] = (value: any) => {gesture[`${name}Value`] = value; return gesture;}; return gesture;}}},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, cancelAnimation() {}, runOnJS: (fn: any) => fn, useAnimatedStyle: (fn: any) => fn(), useSharedValue: (value: any) => host.react.useRef({value}).current, withSpring: (value: any) => value, withTiming: (value: any) => value},
    '../useMotion': {useMotion: () => motion}, './TVDesignCanvas': {TVDesignCanvas: 'TVDesignCanvas'}, './OverlayHost': {useOverlayLifecycle: (...args: any[]) => lifecycle.push(args)},
    './geometry': {releaseSheet, resistSheet, sheetHeights}, './surface': {alpha: (value: string) => value, FocusBoundary: 'FocusBoundary', PanelBackground: 'PanelBackground', SurfaceHeader: 'SurfaceHeader', usePresence: (open: boolean) => open},
  });
  const {SettingsSheet} = await screenModule(new URL('../src-next/kit/shell/SettingsShell.tsx', import.meta.url), {
    ...common, '../controls/Focusable': {Focusable: 'Focusable'}, '../overlays/BottomSheet': {BottomSheet}, '../Icon': {Icon: 'Icon'}, '../focus/registry': {focusTestID: (prefix: string, key: string) => `${prefix}:${key}`},
    './HighlightedText': {HighlightedText: 'HighlightedText'}, './visual': {alpha: (value: string) => value, shellType: () => ({})}, './ServerIdentity': {ServerHeader: 'ServerHeader'}, './authority': {canSeeServerPage: () => true},
  });
  const child = {type: 'DraftEditor', props: {testID: 'retained-draft', children: []}}, onOpenChange = (...args: any[]) => dismissals.push(args);
  return {lifecycle, dismissals, gesture: () => gesture,
    resize: (width: number) => {viewport = {...viewport, width}; theme.platform = width < 600 ? 'phone' : 'tablet';},
    renderDefault: (extra: any = {}) => host.render(() => BottomSheet({open: true, onOpenChange, children: child, testID: 'default-sheet', ...extra})),
    renderSettings: (presentation: 'page' | 'sheet') => {
      const node = SettingsSheet({open: true, host: 'inline', presentation, onOpenChange, profile: null, shortcuts: null, scrollable: false, testID: 'settings-sheet', children: child});
      return host.render(() => BottomSheet(node.props));
    },
  };
}

test('Existing BottomSheet callers keep their native modal, gesture and lifecycle defaults', async () => {
  const f = await fixture(), tree = f.renderDefault();
  const modal = nodes(tree).find(node => node.type === 'Modal'); assert.ok(modal); assert.equal(modal.props.transparent, true);
  assert.equal(f.lifecycle.at(-1)![0], true); assert.equal(f.lifecycle.at(-1)![7], true); assert.equal(f.gesture().enabledValue, true);
  modal.props.onRequestClose(); assert.deepEqual(f.dismissals, [[false, 'back']]);
  assert.equal(nodes(tree).find(node => node.props?.testID === 'default-sheet-keyboard-boundary').props.accessibilityViewIsModal, true);
});

test('Inline Settings retains one child ancestry through sheet-page-sheet and releases only the page overlay lifecycle', async () => {
  const f = await fixture();
  let tree = f.renderSettings('sheet');
  const path = ancestry(tree, 'retained-draft'); assert.ok(path); assert.equal(nodes(tree).some(node => node.type === 'Modal'), false);
  assert.equal(f.lifecycle.at(-1)![0], true); assert.equal(f.gesture().enabledValue, true);
  f.resize(800); tree = f.renderSettings('page');
  assert.deepEqual(ancestry(tree, 'retained-draft'), path); assert.equal(nodes(tree).some(node => node.type === 'Modal'), false);
  assert.equal(f.lifecycle.at(-1)![0], false, 'the ordinary pane must not register Back/Escape handlers');
  assert.equal(f.lifecycle.at(-1)![7], false, 'resizing must not restore focus to the old sheet opener');
  assert.equal(f.gesture().enabledValue, false);
  const boundary = nodes(tree).find(node => node.props?.testID === 'settings-sheet-keyboard-boundary');
  assert.equal(boundary.props.accessibilityViewIsModal, false); boundary.props.onAccessibilityEscape();
  const outside = nodes(tree).find(node => node.props?.onResponderRelease); assert.equal(outside.props.pointerEvents, 'none'); assert.equal(outside.props.onStartShouldSetResponder(), false); outside.props.onResponderRelease();
  assert.deepEqual(f.dismissals, []); assert.equal(flatStyle(outside.props.style).display, 'none');
  const pane = nodes(tree).find(node => node.props?.testID === 'settings-sheet'); assert.equal(flatStyle(pane.props.style).position, undefined); assert.equal(flatStyle(pane.props.style).borderTopLeftRadius, undefined);
  f.resize(390); tree = f.renderSettings('sheet');
  assert.deepEqual(ancestry(tree, 'retained-draft'), path); assert.equal(f.lifecycle.at(-1)![0], true); assert.equal(f.gesture().enabledValue, true);
});

test('Page presentation is opt-in to the inline host and does not alter default modal callers', async () => {
  const f = await fixture(); f.resize(800);
  const tree = f.renderDefault({presentation: 'page'});
  assert.equal(nodes(tree).some(node => node.type === 'Modal'), true);
  assert.equal(f.lifecycle.at(-1)![0], true); assert.equal(nodes(tree).find(node => node.type === 'FocusBoundary').props.modal, true);
});
