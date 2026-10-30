import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as registryModule from '../src-next/kit/focus/registry';
import * as cardModel from '../src-next/kit/cards/model';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {screenModule} from './helpers/screen-harness.mjs';
import {fabricHost, React} from './helpers/fabric-host.mjs';

// Authored UNRUN. Actual production Fabric reconciler, CardFrame and focus
// binding; native hosts, animation values and artwork pixels are controlled.
// Committed host tags/ref identity are source evidence, not OS focus/key proof.
test('the real sparse CardFrame keeps one native host from waiting through two live card paints', async () => {
  const renderer = await fabricHost(), registry = registryModule.createFocusRegistry();
  const theme = createTheme({isTV: true, width: 1920}), menus: (() => void)[] = [];
  const native = (type: string) => React.forwardRef((props: any, ref: any) => {
    const own = React.useRef(null);
    React.useImperativeHandle(ref, () => ({tag: own.current.tag, requestTVFocus() {}, setDestinations() {}}), []);
    return React.createElement(type, {...props, ref: own});
  });
  const View = native('RCTView'), Pressable = native('RCTPressable'), Guide = native('RCTTVFocusGuideView');
  const flatten = (value: any): any => Array.isArray(value) ? Object.assign({}, ...value.map(flatten)) : value ?? {};
  const react = {...React, default: React};
  const nativeModule = {View, Pressable, TVFocusGuideView: Guide, Text: 'RCTText',
    findNodeHandle: (host: any) => host?.tag ?? null, StyleSheet: {flatten, create: (value: any) => value, absoluteFill: {position: 'absolute', inset: 0}}};
  const animated = {default: {View, Text: 'RCTText'}, useAnimatedStyle: (read: () => unknown) => read(),
    useSharedValue: (value: number) => React.useRef({value}).current, withTiming: (value: number) => value};
  const hostRefs = await screenModule(new URL('../src-next/kit/focus/useHostRef.ts', import.meta.url), {react});
  const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react, 'react-native': nativeModule, '../theme': {useTheme: () => theme}, './registry': registryModule, './useHostRef': hostRefs,
  });
  const primitives = await screenModule(new URL('../src-next/kit/cards/primitives.tsx', import.meta.url), {
    react, 'react-native': nativeModule, 'react-native-reanimated': animated,
    'react-native-svg': {default: 'RCTSvg', Defs: 'RCTDefs', LinearGradient: 'RCTGradient', Rect: 'RCTRect', Stop: 'RCTStop'},
    '../../../../../packages/i18n/src/index': {defaultI18n},
    '../browse/useTouchHold': {useTouchHold: () => ({phase: 'idle', consumePress: () => false, end() {}, cancel() {},
      handlers: {onTouchStart() {}, onTouchMove() {}, onTouchEnd() {}, onTouchCancel() {}}})},
    '../../platform/haptics': {touchMenuHaptic() {}},
    '../../platform/menu-key': {registerFocusedMenu: (_owner: unknown, callback: () => void) => {menus.push(callback); return () => {}; }},
    '../Icon': {Icon: 'RCTIcon'}, '../selection/index': {SelectionHoldFeedback: 'RCTSelection'},
    '../theme': {nativeType, useTheme: () => theme}, '../useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})},
    '../focus': focus, './model': cardModel,
  });
  const sparse = await screenModule(new URL('../src-next/kit/cards/TVSparseCardFrame.tsx', import.meta.url), {react, './primitives': primitives});
  const media = await screenModule(new URL('../src-next/kit/cards/MediaCards.tsx', import.meta.url), {
    react, 'react-native': nativeModule, 'react-native-svg': {default: 'RCTSvg', Text: 'RCTSvgText'},
    '../../../../../packages/i18n/src/index': {defaultI18n}, '../theme': {useTheme: () => theme},
    '../artwork/index': {MediaArt: 'RCTMediaArt', CircleArt: 'RCTCircleArt', PersonCircle: 'RCTPersonArt'},
    '../controls/Focusable': {Focusable: 'RCTUnusedTouchControl'}, '../selection/index': {SelectionCircle: 'RCTSelection'}, './primitives': primitives,
  });
  const address = {regionId: 'actual-grid', childId: 'original-pending-host'};
  let commands = 0;
  const open = () => {commands++;};
  function Slot({kind}: {kind: 'waiting' | 'poster' | 'square'}) {
    const props = {title: kind === 'waiting' ? '' : 'Actual title', width: 214, onNavigate: open, frame: sparse.TVCardPaintFrame};
    return React.createElement(focus.FocusItem, {focusId: address.childId, entityId: kind === 'waiting' ? undefined : 'actual-entity', waiting: kind === 'waiting'},
      React.createElement(sparse.TVSparseCardFrame, {title: props.title, width: 214, onAccessibilityTap: open, onMagicTap: open, ...(kind === 'waiting' ? {} : {onNavigate: open})},
        kind === 'waiting' ? React.createElement(sparse.TVPendingCardPaint, {height: 321})
          : React.createElement(kind === 'poster' ? media.PosterCard : media.SquareCard, props)));
  }
  const render = (kind: 'waiting' | 'poster' | 'square') => renderer.render(React.createElement(focus.FocusProvider, {registry},
    React.createElement(focus.FocusRegion, {id: address.regionId, grid: {columns: 1, count: 1}}, React.createElement(Slot, {kind}))));
  const nodes = (): any[] => {const visit = (items: any[]): any[] => items.flatMap(node => [node, ...visit(node.children)]); return visit(renderer.root);};
  const host = () => nodes().find(node => node.type === 'RCTPressable');
  try {
    await render('waiting');
    const tag = host().tag, ref = registry.child(address), oldPress = host().props.onPress, oldHold = host().props.onLongPress;
    assert.ok(ref); assert.equal(host().props.focusable, true); assert.equal(host().props.disabled, true);
    assert.equal(host().props.onAccessibilityTap, undefined); assert.equal(host().props.onMagicTap, undefined);
    host().props.onFocus(); await renderer.settle();
    host().props.onPress(); host().props.onLongPress(); assert.equal(commands, 0); assert.equal(menus.length, 0);
    for (const kind of ['poster', 'square'] as const) {
      await render(kind);
      assert.equal(nodes().filter(node => node.type === 'RCTPressable').length, 1);
      assert.equal(host().tag, tag); assert.strictEqual(registry.child(address), ref);
      assert.equal(registry.capture()?.childId, address.childId);
      assert.deepEqual(registry.entityAddress(address.regionId, 'actual-entity'), address);
      assert.equal(host().props.disabled, false); assert.equal(host().props.focusable, true);
      const before: number = commands; oldPress(); oldHold(); assert.equal(commands, before, 'a retained waiting callback cannot acquire the newly offered action');
      host().props.onPress();
    }
    assert.equal(commands, 2);
  } finally {await renderer.unmount();}
});
