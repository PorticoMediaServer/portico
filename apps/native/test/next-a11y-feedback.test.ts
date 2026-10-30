import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {fabricHost, React as FabricReact} from './helpers/fabric-host.mjs';
import {createHookHost} from './hook-driver.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {createToastClock} from '../src-next/kit/overlays/toast-clock.ts';
import {dismissToastGesture} from '../src-next/kit/overlays/geometry.ts';
import {toastClearance} from '../src-next/kit/overlays/toast-clearance.ts';

const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants)
  : React.isValidElement<any>(node) ? [node, ...descendants(node.props.children)] : [];

async function feedback(screenReader: {enabled: boolean}, spoken: string[], base: any = FabricReact) {
  const theme = {isTV: false, textScale: 1, colors: {}, type: {}, layers: {notice: 50},
    spec: {feedback: {toast: {durationMs: 4000, durationWithActionMs: 8000}}}};
  const motion = {reduce: false, timing: () => ({duration: 0}), duration: () => 0, spring: () => ({})};
  const gesture = () => {
    const value: Record<string, any> = {};
    for (const name of ['enabled', 'minDistance', 'activeOffsetX', 'activeOffsetY', 'onStart', 'onBegin', 'onUpdate', 'onEnd', 'onFinalize']) value[name] = () => value;
    return value;
  };
  return screenModule(new URL('../src-next/kit/overlays/Feedback.tsx', import.meta.url), {
    react: {...base, default: base},
    'react-native': {View: 'View', Text: 'Text', StyleSheet: {absoluteFill: {}},
      AppState: {currentState: 'active', addEventListener: () => ({remove() {}})},
      AccessibilityInfo: {announceForAccessibility: (message: string) => spoken.push(message)}},
    'react-native-gesture-handler': {Gesture: {Pan: gesture}, GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, cancelAnimation() {}, runOnJS: (run: unknown) => run,
      useSharedValue: (value: unknown) => base.useRef({value}).current, useAnimatedStyle: (read: () => unknown) => read(),
      withSpring: (value: unknown) => value, withTiming: (value: unknown) => value},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    '../../../../../packages/i18n/src/index': {defaultI18n: {t: (key: string) => key}},
    '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false})},
    '../controls/Button': {Button: 'Button'}, '../Icon': {Icon: 'Icon'},
    '../theme': {nativeType: () => ({}), useTheme: () => theme}, '../useMotion': {useMotion: () => motion},
    '../useScreenReader': {useScreenReader: () => screenReader.enabled},
    './geometry': {dismissToastGesture}, './toast-clearance': {toastClearance}, './toast-clock': {createToastClock},
    './surface': {alpha: () => 'transparent', useSurfaceMotion: () => ({present: true, style: {}})},
  });
}

/** The real toast host on the installed Fabric renderer: React owns the toast
 * clock's passive effects, its cleanup and the hold across a replacement. */
async function mountToast(element: () => any) {
  const host = await fabricHost();
  const render = () => host.render(element());
  await render();
  return {rerender: render, settle: () => host.settle(), unmount: () => host.unmount()};
}

test('actionable shared notices announce Undo and stay until the screen-reader hold is released', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const screenReader = {enabled: true}, spoken: string[] = [], dismissed: string[] = [];
  const mod = await feedback(screenReader, spoken);
  let toast = {id: 'first', message: 'Removed', action: {label: 'Undo', onPress() {}}};
  const session = await mountToast(() => FabricReact.createElement(mod.ToastHost, {toast, onDismiss: (id: string) => dismissed.push(id)}));
  try {
    t.mock.timers.tick(60_000); await session.settle();
    assert.deepEqual(dismissed, []);
    assert.deepEqual(spoken, ['Removed, Undo']);
    toast = {...toast, id: 'replacement'}; await session.rerender();
    t.mock.timers.tick(0); await session.settle();
    t.mock.timers.tick(60_000); await session.settle();
    assert.deepEqual(dismissed, [], 'replacement also stays persistent');
    screenReader.enabled = false; await session.rerender();
    t.mock.timers.tick(7999); await session.settle(); assert.deepEqual(dismissed, []);
    t.mock.timers.tick(1); await session.settle(); assert.deepEqual(dismissed, ['replacement']);
  } finally {await session.unmount(); t.mock.timers.reset();}
});

test('non-action notices retain their authored timeout with a screen reader', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const mod = await feedback({enabled: true}, []), dismissed: string[] = [];
  const session = await mountToast(() => FabricReact.createElement(mod.ToastHost, {toast: {id: 'information', message: 'Saved', durationMs: 6000}, onDismiss: (id: string) => dismissed.push(id)}));
  try {
    t.mock.timers.tick(5999); await session.settle(); assert.deepEqual(dismissed, []);
    t.mock.timers.tick(1); await session.settle(); assert.deepEqual(dismissed, ['information']);
  } finally {await session.unmount(); t.mock.timers.reset();}
});

// Pure presentation: the toast element's accessibility actions and the callbacks
// they dispatch carry no lifecycle claim, so this case stays on the driver.
test('persistent toast exposes named Undo and Dismiss without treating unknown actions as dismissal', async () => {
  const mod = await feedback({enabled: true}, [], React), calls: string[] = [];
  let tree: any;
  const host = createHookHost(() => {tree = mod.Toast({id: 'undo', message: 'Removed', action: {label: 'Undo', onPress: () => calls.push('undo')},
    onDismiss: (_id: string, reason: string) => calls.push(reason)});});
  host.mount();
  try {
    const notice = descendants(tree).find(node => node.props.accessibilityActions);
    assert.deepEqual(notice.props.accessibilityActions.map((action: any) => action.name), ['dismiss', 'activate']);
    notice.props.onAccessibilityAction({nativeEvent: {actionName: 'unknown'}});
    assert.deepEqual(calls, []);
    notice.props.onAccessibilityAction({nativeEvent: {actionName: 'activate'}});
    assert.deepEqual(calls, ['undo', 'action']);
    notice.props.onAccessibilityAction({nativeEvent: {actionName: 'dismiss'}});
    assert.deepEqual(calls, ['undo', 'action', 'accessibility']);
  } finally {host.unmount();}
});

test('initial screen-reader query cannot overtake a newer accessibility event', async () => {
  let resolve!: (enabled: boolean) => void, changed!: (enabled: boolean) => void, removed = 0, enabled = false;
  const pending = new Promise<boolean>(done => {resolve = done;});
  const {useScreenReader} = await screenModule(new URL('../src-next/kit/useScreenReader.ts', import.meta.url), {
    react: {...FabricReact, default: FabricReact}, 'react-native': {AccessibilityInfo: {
      isScreenReaderEnabled: () => pending,
      addEventListener: (_event: string, listener: typeof changed) => {changed = listener; return {remove: () => removed++};},
    }},
  });
  const host = await fabricHost();
  const Hook = () => {enabled = useScreenReader(true); return null;};
  const render = () => host.render(FabricReact.createElement(Hook));
  const settle = () => host.settle();
  await render();
  try {
    assert.equal(enabled, true, 'Undo stays available while the initial query is pending');
    changed(false); await settle();
    assert.equal(enabled, false);
    resolve(true); await settle();
    assert.equal(enabled, false, 'the older query does not undo a newer OS event');
  } finally {await host.unmount();}
  assert.equal(removed, 1);
});
