// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {buttonSpec, focusSpec} from '@portico/design';
import {createTheme} from '../src-next/kit/theme.ts';
import * as controlModel from '../src-next/kit/controls/control-model.ts';
import {useFocusedMenu} from '../src-next/kit/controls/useFocusedMenu.ts';
import {dispatchMenuKey} from '../src-next/platform/menu-key.ts';
import * as shellModel from '../src-next/shell/model.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {liveOverlayPaint, screenModule} from './helpers/screen-harness.mjs';

type Element = React.ReactElement<Record<string, any>>;
type NativeState = {pressed: boolean; focused: boolean};
const idle: NativeState = {pressed: false, focused: false};
const react = {...React, default: React};
const flatten = (style: any): Record<string, any> => Array.isArray(style)
  ? Object.assign({}, ...style.map(flatten))
  : style && typeof style === 'object' ? style : {};
const nativeStyle = (element: Element, state = idle) => flatten(typeof element.props.style === 'function' ? element.props.style(state) : element.props.style);
const menu = () => dispatchMenuKey({eventType: 'menu', eventKeyAction: 1}, 'android', true);

// Execute the INSTALLED array filter, rather than createAnimatedComponent:c=>c.
// No native worklet runtime is loaded. Unused inline/CSS/event branches fail
// loudly; these cases exercise ordinary caller styles and animated handles.
async function installedFilter() {
  const root = new URL('./src/', import.meta.resolve('react-native-reanimated/package.json'));
  const utils = await screenModule(new URL('createAnimatedComponent/utils.ts', root), {react});
  const unused = () => {throw new Error('Unexpected Reanimated filter branch.');};
  const {PropsFilter} = await screenModule(new URL('createAnimatedComponent/PropsFilter.tsx', root), {
    react, '../animation': {initialUpdaterRun: (update: () => unknown) => update()},
    '../css/utils': {isCSSConfigProp: unused, isPseudoSelectorValue: unused},
    '../isSharedValue': {isSharedValue: () => false},
    '../WorkletEventHandler': {WorkletEventHandler: class {}},
    './InlinePropManager': {hasInlineStyles: () => false, getInlineStyle: unused},
    './platform': {svgHitTestResponder: undefined}, './utils': utils,
  });
  return PropsFilter;
}

function render(node: any): any {
  if (Array.isArray(node)) return node.map(render);
  if (!React.isValidElement<Record<string, any>>(node)) return node;
  const type: any = node.type;
  if (typeof type === 'function') return render(type(node.props));
  if (typeof type?.render === 'function') return render(type.render(node.props, node.props.ref ?? null));
  return {...node, props: {...node.props, children: render(node.props.children)}};
}
function elements(node: any): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!React.isValidElement<Record<string, any>>(node)) return [];
  return [node, ...elements(node.props.children)];
}
async function mount(component: any, initial: Record<string, any>) {
  let props = initial, tree: any;
  const host = createFabricHookHost(() => {tree = render(React.createElement(component, props));});
  await host.mount();
  return {host, get tree() {return tree;}, async update(next: Record<string, any>) {props = next; await host.rerender();},
    native() {const found = elements(tree).filter(node => node.type === 'Pressable'); assert.equal(found.length, 1); return found[0]!;}};
}

async function controls(isTV = false, reduce = false, interactive = true) {
  const PropsFilter = await installedFilter();
  const theme = createTheme({isTV, width: isTV ? 1920 : 390, reduceMotion: reduce});
  const motion = {transforms: !reduce, timing: () => ({duration: reduce ? 160 : 120})};
  const handles: {read: () => any}[] = [], timings: {value: number; duration: number}[] = [];
  const animatedInputs: Record<string, any>[] = [];
  const native = {Pressable: 'Pressable', View: 'View', Text: 'Text', ActivityIndicator: 'ActivityIndicator', useWindowDimensions: () => ({width: 390, height: 844}),
    StyleSheet: {create: (styles: unknown) => styles, flatten, absoluteFill: {position: 'absolute', inset: 0}}};
  const animated = {
    default: {View: 'AnimatedView', createAnimatedComponent: (Component: any) => React.forwardRef(function Filtered(props: any, ref) {
      const filter = React.useRef(new PropsFilter()), first = React.useRef(true);
      animatedInputs.push(props);
      const filtered = filter.current.filterNonAnimatedProps({props, _isFirstRender: first.current});
      first.current = false;
      return React.createElement(Component, {...filtered, ref});
    })},
    useSharedValue: (value: number) => React.useRef({value}).current,
    useAnimatedStyle: (read: () => unknown) => {
      const handle = React.useRef<any>(null);
      if (!handle.current) {
        const value: {read: () => unknown; viewDescriptors: object; initial: {value: unknown; updater: () => unknown}} =
          {read, viewDescriptors: {}, initial: {value: read(), updater: () => value.read()}};
        handle.current = value; handles.push(value);
      }
      handle.current.read = read;
      return handle.current;
    },
    withTiming: (value: number, config: {duration: number}) => {timings.push({value, ...config}); return value;},
  };
  const focusModule = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react, 'react-native': {...native, findNodeHandle: () => null, TVFocusGuideView: 'TVFocusGuideView'},
    './useHostRef': await import('../src-next/kit/focus/useHostRef.ts'),
    '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': {useTheme: () => theme}, './registry': await import('../src-next/kit/focus/registry.ts'),
  });
  const {Focusable} = await screenModule(new URL('../src-next/kit/controls/Focusable.tsx', import.meta.url), {
    '../overlays/OverlayPaintContext': {...liveOverlayPaint, useOverlayPaintMode: () => ({...liveOverlayPaint.useOverlayPaintMode(), interactive})},
    react, 'react-native': native, 'react-native-reanimated': animated, '@portico/design': {focusSpec},
    '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': {useTheme: () => theme}, '../useMotion': {useMotion: () => motion},
    './control-model': controlModel, './useFocusedMenu': {useFocusedMenu}, '../focus': focusModule,
  });
  const {Button} = await screenModule(new URL('../src-next/kit/controls/Button.tsx', import.meta.url), {
    react, 'react-native': native, '@portico/design': {buttonSpec}, './Focusable': {Focusable},
    '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: false})},
    '../Icon': {Icon: 'Icon'}, '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': {useTheme: () => theme}, './control-model': controlModel,
  });
  const {PhoneTabBar} = await screenModule(new URL('../src-next/shell/PhoneTabBar.tsx', import.meta.url), {
    react, 'react-native': native, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({bottom: 34})},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../platform/native-blur': {BlurView: 'BlurView', phoneTabBarRecipe: () => ({paddingX: 12, paddingTop: 9, gap: 3})},
    '../kit/controls': {Focusable}, '../kit/Icon': {Icon: 'Icon'}, '../kit/theme': {useTheme: () => theme}, './model': shellModel,
  });
  return {Focusable, Button, PhoneTabBar, handles, timings, animatedInputs, theme};
}

test('installed Reanimated filtering preserves the original failure boundary for a direct style callback', async () => {
  const PropsFilter = await installedFilter();
  let calls = 0;
  const style = () => {calls++; return {height: 46, flex: 1};};
  const props = new PropsFilter().filterNonAnimatedProps({props: {style}, _isFirstRender: true});
  assert.deepEqual(props.style, [style]);
  assert.deepEqual(nativeStyle(React.createElement('Pressable', props)), {});
  assert.equal(calls, 0, 'native Pressable only invokes a top-level style function');
});

test('actual hero Button and every PhoneTabBar item deliver their geometry across the installed filter', async () => {
  const {Button, PhoneTabBar, animatedInputs} = await controls();
  const button = await mount(Button, {size: 'hero', children: 'Actual action'});
  try {
    const style = nativeStyle(button.native());
    assert.equal(style.height, 46); assert.equal(style.paddingHorizontal, 20);
    assert.equal(style.borderRadius, 10); assert.equal(style.alignItems, 'center');
    assert.deepEqual(style.transform, [{scale: 1}]);
    assert.ok(animatedInputs.every(props => Array.isArray(props.style)));
  } finally {await button.host.unmount();}
  const selected: string[] = [];
  const bar = await mount(PhoneTabBar, {destination: {kind: 'home'}, hasLiveTV: true, onSelect: (id: string) => selected.push(id)});
  try {
    const tabs = elements(bar.tree).filter(node => node.type === 'Pressable');
    assert.equal(tabs.length, 5);
    for (const tab of tabs) {
      const style = nativeStyle(tab);
      assert.equal(style.flex, 1); assert.equal(style.height, 44);
      assert.equal(style.alignItems, 'center'); assert.equal(style.gap, 3);
      assert.equal(tab.props.accessibilityRole, 'tab');
      tab.props.onPress();
    }
    assert.deepEqual(selected, ['home', 'search', 'libraries', 'live', 'saved']);
    assert.equal(tabs[0]!.props.accessibilityState.selected, true);
  } finally {await bar.host.unmount();}
});

test('nested static styles and both native ref forms reach the same sole Pressable host', async () => {
  const {Focusable} = await controls(true);
  const callbacks: unknown[] = [];
  for (const ref of [React.createRef(), (value: unknown) => {callbacks.push(value);}]) {
    const press = () => {}, layout = () => {};
    const control = await mount(Focusable, {ref, style: [{height: 72}, [false, {paddingHorizontal: 28, flex: 1}]],
      onPress: press, onLayout: layout, hasTVPreferredFocus: true, nextFocusDown: 12, focusRadius: 14,
      tvParallaxProperties: {shiftDistanceX: 4}, accessibilityLabel: 'Actual native control', accessibilityState: {selected: true}});
    try {
      const node = control.native(), style = nativeStyle(node);
      assert.equal(style.height, 72); assert.equal(style.paddingHorizontal, 28); assert.equal(style.flex, 1);
      const nativeHost = {requestTVFocus() {}};
      node.props.ref(nativeHost);
      assert.equal(typeof ref === 'function' ? callbacks.at(-1) : ref.current, nativeHost, 'merged binding forwards the actual sole native host');
      node.props.ref(null);
      assert.equal(typeof ref === 'function' ? callbacks.at(-1) : ref.current, null);
      assert.equal(node.props.onPress, press); assert.equal(node.props.onLayout, layout);
      assert.equal(node.props.hasTVPreferredFocus, true); assert.equal(node.props.nextFocusDown, 12);
      assert.deepEqual(node.props.tvParallaxProperties, {shiftDistanceX: 4, enabled: false});
      assert.equal(node.props.accessibilityLabel, 'Actual native control'); assert.equal(node.props.accessibilityState.selected, true);
      assert.equal('pressableStyleInput' in node.props, false, 'the adapter prop must not leak to the native host');
    } finally {await control.host.unmount();}
  }
});

test('caller styles receive the exact native render state, including press without a synthetic press event', async () => {
  const {Focusable} = await controls();
  const states: NativeState[] = [];
  const control = await mount(Focusable, {style: (state: NativeState) => {states.push(state); return [{height: state.pressed ? 48 : 46}, {opacity: state.focused ? .8 : 1}];}});
  try {
    assert.deepEqual(states, [], 'the caller callback belongs to native Pressable rendering');
    const pressed = {pressed: true, focused: true};
    assert.equal(nativeStyle(control.native(), pressed).height, 48);
    assert.equal(states[0], pressed);
    assert.equal(nativeStyle(control.native(), idle).height, 46);
    assert.equal(states[1], idle);
    await control.update({style: (state: NativeState) => ({width: state.pressed ? 90 : 80})});
    assert.equal(nativeStyle(control.native(), pressed).width, 90);
  } finally {await control.host.unmount();}
});

test('static caller animated handles remain visible to the installed Reanimated style filter', async () => {
  const {Focusable} = await controls();
  let updates = 0;
  const caller = {viewDescriptors: {}, initial: {value: {width: 100}, updater: () => {updates++; return {width: 120};}}};
  const control = await mount(Focusable, {style: [{height: 46}, [caller]]});
  try {
    const style = nativeStyle(control.native());
    assert.equal(style.width, 120); assert.equal(style.height, 46);
    assert.equal(updates, 1, 'Reanimated must discover and initialise the caller handle');
    assert.equal('viewDescriptors' in style, false, 'native style receives the processed value');
  } finally {await control.host.unmount();}
});

test('real event callbacks and render state retain focus, hover, press, cancellation and blur behavior', async () => {
  const {Focusable, handles, theme} = await controls();
  const calls: [string, unknown][] = [];
  const names = ['onFocus', 'onBlur', 'onHoverIn', 'onHoverOut', 'onPressIn', 'onPressOut', 'onPress', 'onLongPress'] as const;
  const props = Object.fromEntries(names.map(name => [name, (event: unknown) => calls.push([name, event])]));
  let childState: unknown;
  const control = await mount(Focusable, {...props, children: (state: unknown) => {childState = state; return null;}});
  const event = {nativeEvent: {target: 7}};
  try {
    await act(() => control.native().props.onFocus(event));
    await act(() => control.native().props.onHoverIn(event));
    await act(() => control.native().props.onPressIn(event));
    assert.equal(nativeStyle(control.native()).zIndex, theme.layers.tvFocus);
    assert.deepEqual(handles[0]!.read().transform, [{scale: controlModel.focusScale('button', false) * .97}]);
    control.native().props.children({pressed: true, focused: true});
    assert.deepEqual(childState, {pressed: true, focused: true, hovered: true});
    // Pressability calls onPressOut on responder cancellation; no onPress is synthesized.
    await act(() => control.native().props.onPressOut(event));
    assert.deepEqual(handles[0]!.read().transform, [{scale: controlModel.focusScale('button', false)}]);
    assert.equal(calls.some(([name]) => name === 'onPress'), false);
    await act(() => control.native().props.onHoverOut(event));
    await act(() => control.native().props.onPressIn(event));
    await act(() => control.native().props.onBlur(event));
    assert.deepEqual(handles[0]!.read().transform, [{scale: 1}]);
    control.native().props.children(idle);
    assert.deepEqual(childState, {...idle, hovered: false});
    control.native().props.onPress(event); control.native().props.onLongPress(event);
    assert.deepEqual(calls.map(([name]) => name), ['onFocus', 'onHoverIn', 'onPressIn', 'onPressOut', 'onHoverOut', 'onPressIn', 'onBlur', 'onPress', 'onLongPress']);
    assert.ok(calls.every(([, actual]) => actual === event));
  } finally {await control.host.unmount();}
});

test('focused TV Menu ownership, disabled native props, rings and cleanup survive the style adapter', async () => {
  const {Focusable, handles} = await controls(true);
  const calls: string[] = [];
  const props = {onLongPressAction: () => calls.push('first'), onLongPress: () => calls.push('native'), focusRadius: 14};
  const control = await mount(Focusable, props);
  try {
    assert.equal(menu(), false);
    await act(() => control.native().props.onFocus({}));
    assert.equal(menu(), true); control.native().props.onLongPress({});
    assert.deepEqual(calls, ['first', 'first', 'native']);
    assert.deepEqual(handles[0]!.read().transform, [{scale: controlModel.focusScale('button', true)}]);
    const rings = elements(control.native().props.children({pressed: false, focused: true})).filter(node => node.type === 'AnimatedView');
    assert.equal(rings.length, 2);
    assert.equal(flatten(rings[0]!.props.style).inset, -focusSpec.offsetOuter.tv);
    assert.equal(flatten(rings[1]!.props.style).borderRadius, 14 + focusSpec.offsetOuter.tv + focusSpec.ringWidth.tv);
    await control.update({...props, onLongPressAction: () => calls.push('replacement')});
    assert.equal(menu(), true); assert.equal(calls.at(-1), 'replacement');
    await control.update({...props, disabled: true});
    assert.equal(menu(), false); assert.equal(control.native().props.disabled, true);
    assert.equal(control.native().props.focusable, false); assert.equal(control.native().props.accessibilityState.disabled, true);
    assert.equal(control.native().props.onLongPress, undefined);
    assert.deepEqual(handles[0]!.read().transform, [{scale: 1}]); assert.equal(handles[1]!.read().opacity, 0);
    await act(() => control.native().props.onBlur({}));
    await control.update(props); assert.equal(menu(), false);
    await act(() => control.native().props.onFocus({})); assert.equal(menu(), true);
    await act(() => control.native().props.onBlur({})); assert.equal(menu(), false);
    await act(() => control.native().props.onFocus({}));
  } finally {await control.host.unmount();}
  assert.equal(menu(), false);
});

test('reduced motion retains opacity timing and custom focus faces retain no primitive ring or scale', async () => {
  for (const focusTreatment of ['default', 'none']) {
    const {Focusable, handles, timings} = await controls(true, true);
    const control = await mount(Focusable, {style: {height: 72}, focusTreatment});
    try {
      await act(() => control.native().props.onFocus({}));
      await act(() => control.native().props.onPressIn({}));
      assert.deepEqual(handles[0]!.read().transform, [{scale: 1}]);
      assert.ok(timings.every(timing => timing.duration === 160));
      assert.ok(timings.some(timing => timing.value === 1));
      const rings = elements(control.native().props.children({pressed: true, focused: true})).filter(node => node.type === 'AnimatedView');
      assert.equal(rings.length, focusTreatment === 'none' ? 0 : 2);
      assert.equal(nativeStyle(control.native()).height, 72);
    } finally {await control.host.unmount();}
  }
});

test('inset TV focus keeps one ring and custom faces retain caller geometry without automatic scale', async () => {
  for (const focusTreatment of ['default', 'none']) {
    const {Focusable, handles} = await controls(true);
    const control = await mount(Focusable, {style: {flex: 1, height: 64}, focusInset: true, focusRadius: 12, focusTreatment});
    try {
      await act(() => control.native().props.onFocus({}));
      const rings = elements(control.native().props.children({pressed: false, focused: true})).filter(node => node.type === 'AnimatedView');
      assert.equal(rings.length, focusTreatment === 'none' ? 0 : 1);
      if (rings.length) {
        assert.ok(flatten(rings[0]!.props.style).inset === 0, 'the inset ring has exactly zero offset (including signed zero)');
        assert.equal(flatten(rings[0]!.props.style).borderRadius, 12);
      }
      assert.deepEqual(handles[0]!.read().transform, [{scale: focusTreatment === 'none' ? 1 : controlModel.focusScale('button', true)}]);
      assert.equal(nativeStyle(control.native()).height, 64); assert.equal(nativeStyle(control.native()).flex, 1);
    } finally {await control.host.unmount();}
  }
});

test('TV opt-in disabled choices retain their native stop and ring while every command channel is inert', async () => {
  for (const isTV of [true, false]) {
    const {Focusable, handles} = await controls(isTV);
    const command = () => {throw new Error('Disabled choice executed a command');};
    const control = await mount(Focusable, {focusableWhenDisabled: true, disabled: true, onPress: command, onLongPress: command,
      onLongPressAction: command, onAccessibilityAction: command, onAccessibilityTap: command, onMagicTap: command});
    try {
      const node = control.native();
      assert.equal(node.props.disabled, !isTV); assert.equal(node.props.focusable, isTV ? undefined : false);
      assert.equal(node.props.accessibilityState.disabled, true);
      for (const key of ['onPress', 'onLongPress', 'onAccessibilityAction', 'onAccessibilityTap', 'onMagicTap', 'onPressIn', 'onPressOut']) assert.equal(node.props[key], undefined, key);
      await act(() => control.native().props.onFocus({}));
      assert.equal(handles[1]!.read().opacity, isTV ? 1 : 0);
      assert.equal(menu(), false, 'an inert focused choice never owns a context action');
    } finally {await control.host.unmount();}
  }
});

test('inert outgoing paint dominates TV focusableWhenDisabled and ignores retained native events', async () => {
  for (const disabled of [false, true]) {
    const {Focusable, handles} = await controls(true, false, false);
    const command = () => {throw new Error('Outgoing paint invoked an input callback');};
    const control = await mount(Focusable, {focusableWhenDisabled: true, disabled, onPress: command, onLongPress: command,
      onLongPressAction: command, onAccessibilityAction: command, onAccessibilityTap: command, onMagicTap: command,
      onFocus: command, onBlur: command, onHoverIn: command, onHoverOut: command});
    try {
      const node = control.native(); assert.equal(node.props.disabled, true); assert.equal(node.props.focusable, false);
      for (const key of ['onPress', 'onLongPress', 'onAccessibilityAction', 'onAccessibilityTap', 'onMagicTap', 'onPressIn', 'onPressOut']) assert.equal(node.props[key], undefined);
      await act(() => {node.props.onFocus({}); node.props.onBlur({}); node.props.onHoverIn({}); node.props.onHoverOut({});});
      assert.equal(handles[1]!.read().opacity, 0); assert.equal(menu(), false);
    } finally {await control.host.unmount();}
  }
});
