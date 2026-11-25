// @fabric-react
import {savedFixture, tvSavedDirectoryFixture} from './helpers/saved-fixture.mjs';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {focusSpec} from '@portico/design';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType, fontFamilies} from '../src-next/kit/theme';
import * as registryModule from '../src-next/kit/focus/registry';
import * as hostRef from '../src-next/kit/focus/useHostRef';
import * as destination from '../src-next/kit/focus/destination-entry';
import * as rowState from '../src-next/kit/layout/row-state';
import * as layoutTypes from '../src-next/kit/layout/types';
import * as controlModel from '../src-next/kit/controls/control-model';
import {useTVBusyEntryRevision} from '../src-next/tv/auth/entry-revision';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {registerLibrarySelectionFooterTests} from './helpers/tv-library-selection-footer.mts';
import * as kitModel from '../../../packages/client-core/src/presentation/kit-model/index.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.grid)] : [];
const flush = async () => {await act(() => {});};
// Separate probe roots must commit in the sibling order the composed tree uses.
async function mountInOrder<T, R>(values: readonly T[], mount: (value: T) => Promise<R>): Promise<R[]> {
  const result: R[] = [];
  for (const value of values) result.push(await mount(value));
  return result;
}
/** Actual region/target/ref/entry/paint hooks; only animation, scrolling and native handles are controlled. */
async function bindings() {
  let instance = 0;
  const contexts: any[] = [], registry = registryModule.createFocusRegistry(), theme = createTheme({isTV: true, width: 1920});
  const react: any = {...React, useId: () => React.useRef(`native-instance-${++instance}`).current, createContext: (current: any) => {const context = {current, Provider: 'Provider'}; contexts.push(context); return context;}, useContext: (context: any) => context.current}; react.default = react;
  const flatten = (value: any): any => Array.isArray(value) ? Object.assign({}, ...value.map(flatten)) : value ?? {};
  const listeners = new Set<(event: any) => void>();
  const native = {Platform: {OS: 'android', isTV: true}, View: 'View', Text: 'Text', Pressable: 'Pressable', ScrollView: 'ScrollView', TVFocusGuideView: 'Guide',
    AccessibilityInfo: {announceForAccessibility() {}},
    // Search registration reads the real app-state surface; this fixture only drives focus.
    AppState: {currentState: 'active', addEventListener: () => ({remove() {}})},
    BackHandler: {addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: 1920, height: 1080}),
    TVEventHandler: {addListener: (listener: (event: any) => void) => {listeners.add(listener); return {remove: () => listeners.delete(listener)};}},
    findNodeHandle: (host: any) => host.nativeTag, StyleSheet: {create: (value: any) => value, flatten, absoluteFill: {position: 'absolute'}}};
  const focus = {...await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {react, 'react-native': native, '../theme': {useTheme: () => theme}, './registry': registryModule, './CoveredLayer': {useLayerCovered: () => false}, './useHostRef': hostRef}), focusTestID: registryModule.focusTestID, useLayerCovered: () => false};
  contexts[0].current = registry;
  const paint = await screenModule(new URL('../src-next/kit/overlays/OverlayPaintContext.tsx', import.meta.url), {react});
  const animation = {default: {View: 'AnimatedView', ScrollView: 'AnimatedScrollView', createAnimatedComponent: (component: any) => component},
    useAnimatedStyle: (fn: () => any) => fn(), useSharedValue: (value: any) => React.useRef({value}).current, withTiming: (value: any) => value,
    useAnimatedScrollHandler: (fn: any) => fn, runOnJS: (fn: any) => fn};
  const {Focusable} = await screenModule(new URL('../src-next/kit/controls/Focusable.tsx', import.meta.url), {react, 'react-native': native, 'react-native-reanimated': animation,
    '@portico/design': {focusSpec}, '../theme': {useTheme: () => theme}, '../focus': {...focus, useCovered: () => false}, './control-model': controlModel,
    '../useMotion': {useMotion: () => ({transforms: false, reduce: true, timing: () => ({})})}, './useFocusedMenu': {useFocusedMenu() {}},
    '../overlays/OverlayHost': {useInTopLayer: () => true}, '../overlays/OverlayPaintContext': paint, '../dev-observation': {useNativeObservation: (_: any, ref: any) => ({ref, recordFocus() {}, recordDeclaredStyle() {}})}});
  const entry = await screenModule(new URL('../src-next/kit/focus/useDestinationEntryFocus.ts', import.meta.url), {react, './FocusRegion': focus, './destination-entry': destination});
  const drivers: Awaited<ReturnType<typeof createFabricHookHost>>[] = []; let tag = 1;
  async function region(get: () => any) {
    let outer: any, guide: any;
    const a = createFabricHookHost(() => {outer = focus.FocusRegion.render(get(), null);}); await a.mount(); drivers.push(a);
    const b = createFabricHookHost(() => {contexts[1].current = undefined; contexts[2].current = undefined; guide = outer.type.render(outer.props, null);}); await b.mount(); drivers.push(b);
    guide.props.ref({nativeTag: tag++, requestTVFocus() {}, setDestinations() {}});
    return {get guide() {return guide;}, async refresh() {await a.rerender(); await b.rerender();}, async dispose() {await b.unmount(); await a.unmount();}};
  }
  async function target(regionId: string, get: () => any, item?: any) {
    let pressable: any, calls = 0;
    const driver = createFabricHookHost(() => {
      contexts[1].current = regionId; contexts[2].current = item;
      const props = get(), bound = Focusable.render(props, props.ref ?? null); pressable = bound.type.render(bound.props, bound.props.ref);
    }); await driver.mount(); drivers.push(driver);
    const host = {nativeTag: tag++, requestTVFocus() {calls++;}}; pressable.props.ref(host); await driver.flush();
    return {host, get calls() {return calls;}, get props() {return pressable.props;}, async refresh() {await driver.rerender();}, async dispose() {await driver.unmount();}};
  }
  return {react, native, animation, focus, paint, theme, registry, contexts, Focusable, entry, listeners, drivers, region, target,
    async dispose() {for (const driver of drivers.splice(0).reverse()) await driver.unmount();}};
}


async function rowComposition(b: Awaited<ReturnType<typeof bindings>>) {
  const page = await screenModule(new URL('../src-next/kit/focus/page-boundary.ts', import.meta.url), {
    '../../platform/tv-key-adapter': {createTVKeyAdapter}, '../../platform/tv-key-source': {tvKeySource: 'native'},react: b.react, 'react-native': b.native});
  const row = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {react: b.react, 'react-native': b.native,
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop'}, 'react-native-reanimated': b.animation,
    '../../../../../packages/i18n/src': {defaultI18n}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => b.theme}, '../focus': {...b.focus, useCovered: () => false}, './model': layoutModel, './row-state': rowState,
    '../focus/page-boundary': page, './FocusAnchor': {useFocusAnchor: () => ({anchor() {}, repeating: false, offset: {value: 0}, scrollRef: {current: null}, stop() {}, move() {}})},
    './MotionItem': {MotionItem: 'MotionItem'}, './types': layoutTypes});
  const shelf = await screenModule(new URL('../src-next/tv/layout/TVShelf.tsx', import.meta.url), {react: b.react, 'react-native': b.native, '../../kit/theme': {useTheme: () => b.theme}, '../../kit/layout/Row': row});
  const people = await screenModule(new URL('../src-next/kit/title/TitlePeople.tsx', import.meta.url), {'../states': {SkeletonBlock: 'SkeletonBlock'}, '../focus': b.focus, '../../../../../packages/i18n/src': {defaultI18n}, react: b.react, 'react-native': b.native,
    '../artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork', PersonCircle: 'PersonCircle'}, '../../tv/layout/TVShelf': shelf, '../../tv/layout/TVArtFocus': {TVArtFocus: 'TVArtFocus'},
    '../controls': {Focusable: b.Focusable}, '../theme': {useTheme: () => b.theme, textLines: (count: number) => ({numberOfLines: count, ellipsizeMode: 'tail'})}, '../shell/visual': {shellType: (size: number, weight: any, lineHeight: any = 'normal', tracking = 0, scale = 1) => nativeType({size, weight, lineHeight, tracking}, scale)}});
  return {row, shelf, people};
}

for (const [name, actionable] of [['leading', [false, true, true]], ['interleaved', [true, false, true]], ['tail', [true, true, false]], ['all-inert', [false, false, false]]] as const) {
  test(`actual TitlePeople/Shelf/Row ${name} preserves paint and binds only actionable native stops`, async () => {
    const b = await bindings(), c = await rowComposition(b); let tree: any; const commands: string[] = [];
    const credits = actionable.map((active, index) => ({id: `credit-${index}`, name: `Name ${index}`, role: 'Actor', ...(active ? {personId: `person-${index}`} : {})}));
    let people: any; const peopleDriver = createFabricHookHost(() => {people = c.people.TitlePeople({credits, artwork: () => undefined, onPerson: (id: string) => commands.push(id), focus: {id: 'cast', edges: {left: 'rail', right: 'next'}}});}); await peopleDriver.mount();
    // TVShelf draws its Row inside the W-11 focus bleed.
    const bled = people.type(people.props), shelf = [bled.props.children].flat(Infinity).find((child: any) => typeof child?.type === 'function'), wrapper = shelf.type({...shelf.props, initialViewportWidth: 1920}), row = wrapper.props.children;
    const driver = createFabricHookHost(() => {tree = row.type(row.props);}); await driver.mount();
    const regionNode = nodes(tree).find(node => node.type?.name === 'RowRegion'), declared = regionNode.type(regionNode.props);
    const guide = await b.region(() => declared.props), rail = {nativeTag: 900, requestTVFocus() {}}, next = {nativeTag: 901, requestTVFocus() {}};
    b.registry.attachRegion('rail', rail); b.registry.attachRegion('next', next);
    const slots = nodes(tree).filter(node => node.type === b.focus.FocusItem), targets: Awaited<ReturnType<typeof b.target>>[] = [];
    try {
      assert.deepEqual(slots.map(slot => slot.props.focusId), credits.map(credit => credit.id), 'all original paint identities remain');
      for (const slot of slots) if (slot.props.children.type === b.Focusable) targets.push(await b.target('cast', () => slot.props.children.props, b.focus.FocusItem(slot.props).props.value));
      await flush(); for (const target of targets) await target.refresh();
      const order = credits.filter((_, i) => actionable[i]).map(credit => credit.id);
      assert.deepEqual(b.registry.config('cast')?.order, order); assert.equal(guide.guide.props.focusable, order.length > 0);
      if (!order.length) {assert.equal(b.registry.entryTarget('cast'), undefined); return;}
      assert.equal(b.registry.entryTarget('cast'), targets[0]!.host); assert.equal(targets[0]!.props.nextFocusLeft, 900);
      assert.equal(targets.at(-1)!.props.nextFocusRight, 901);
      if (targets.length > 1) {assert.equal(targets.at(-1)!.props.nextFocusLeft, undefined); assert.equal(targets[0]!.props.nextFocusRight, undefined);}
      targets[0]!.props.onPress(); assert.deepEqual(commands, [credits.find((_, i) => actionable[i])!.personId]);
    } finally {await driver.unmount(); await peopleDriver.unmount(); await b.dispose();}
  });
}

test('Row predicate preserves waiting host while actual page boundary belongs only to usable nonwaiting tail; defaults retain explicit entry', async () => {
  const b = await bindings(), {row} = await rowComposition(b); let tree: any, retries = 0;
  const items = [{id: 'inert', active: false}, {id: 'usable', active: true}, {id: 'tail', active: false}];
  const props: any = {title: '', items, getKey: (item: any) => item.id, isFocusable: (item: any) => item.active, itemWidth: 100, itemHeight: 100, initialViewportWidth: 500,
    selection: {active: false, isSelected: () => false, onToggle() {}, onEnter() {}}, focus: {id: 'paged', entry: 'inert'},
    paging: {waitingId: 'cursor', waitingItem: {id: 'waiting', active: false}, hasMore: true, failed: false, loading: true, requestNext: () => {retries++;}},
    renderItem: (_: any, slot: any) => React.createElement(b.Focusable, {focusId: slot.focusId})};
  const wrapped = row.Row(props), content = wrapped.props.children; let current = content.props;
  const driver = createFabricHookHost(() => {tree = content.type(current);}); await driver.mount();
  let regionNode = nodes(tree).find(node => node.type?.name === 'RowRegion'), declared = regionNode.type(regionNode.props);
  const guide = await b.region(() => declared.props);
  try {
    assert.deepEqual(b.registry.config('paged')?.order, ['usable', 'cursor']);
    const usable = await b.target('paged', () => ({focusId: 'usable'})), waiting = await b.target('paged', () => ({focusId: 'cursor'})); await flush();
    assert.equal(b.registry.child({regionId: 'paged', childId: 'cursor'}), waiting.host);
    current = {...current, paging: {...current.paging, failed: true, loading: false}}; await driver.rerender();
    regionNode = nodes(tree).find(node => node.type?.name === 'RowRegion'); declared = regionNode.type(regionNode.props); await guide.refresh();
    waiting.props.ref(null); assert.equal(b.registry.child({regionId: 'paged', childId: 'cursor'}), undefined);
    assert.deepEqual(b.registry.config('paged')?.order, ['usable']);
    const before = retries;
    for (const target of [waiting, usable]) {
      target.props.onFocus({}); for (const listener of b.listeners) {listener({eventType: 'right', eventKeyAction: 0, tag: target.host.nativeTag}); listener({eventType: 'right', eventKeyAction: 1});}
    }
    assert.equal(retries, before + 1, 'waiting and inert paint tail never own the retry boundary');
    current = {...current, items: items.filter(item => !item.active)}; await driver.rerender();
    assert.equal(b.listeners.size, 0, 'no usable nonwaiting slot means no boundary listener');
    current = {...current, isFocusable: undefined, focus: {id: 'paged', entry: 'external-entry'}}; await driver.rerender();
    regionNode = nodes(tree).find(node => node.type?.name === 'RowRegion'); declared = regionNode.type(regionNode.props); await guide.refresh();
    assert.equal(b.registry.config('paged')?.entry, 'external-entry', 'default entry remains unchanged outside declared paint order');
    assert.deepEqual(b.registry.config('paged')?.order, ['inert', 'tail']);
  } finally {await driver.unmount(); await b.dispose();}
});

async function overlayBindings(b: Awaited<ReturnType<typeof bindings>>) {
  const geometry = await import('../src-next/kit/overlays/geometry');
  const stack = await import('../src-next/kit/overlays/overlay-stack');
  const anchored = await import('../src-next/kit/overlays/anchored-menu-geometry');
  const touch = await import('../src-next/kit/browse/touch-menu-geometry');
  const canvas = await import('../src-next/platform/layout/tv-canvas');
  const design = await import('@portico/design');
  const back = new Set<() => boolean>();
  const native = {...b.native, Modal: 'Modal', Keyboard: {addListener: () => ({remove() {}})}, BackHandler: {addEventListener: (_: any, fn: () => boolean) => {back.add(fn); return {remove: () => back.delete(fn)};}}};
  const motion = {useMotion: () => ({transforms: false, reduce: true, timing: () => ({}), spring: () => ({})})};
  const animation = {...b.animation, cancelAnimation() {}, withSpring: (value: any) => value};
  const svg = {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', RadialGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop'};
  const host = await screenModule(new URL('../src-next/kit/overlays/OverlayHost.tsx', import.meta.url), {
    react: b.react, 'react-native': native, '../../platform/overlay-keys': {registerOverlayKeys: () => () => {}}, '../../platform/tv-menu': {captureTVMenuKey: () => () => {}},
    './OverlayPaintContext': b.paint, '../theme': {useTheme: () => b.theme}, '../focus': {useCovered: () => false, ...b.focus, requestNativeFocus: registryModule.requestNativeFocus}, './geometry': geometry, './overlay-stack': stack});
  const {Button} = await screenModule(new URL('../src-next/kit/controls/Button.tsx', import.meta.url), {react: b.react, 'react-native': native,
    '@portico/design': design, '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false}), BlurView: 'Blur'}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => b.theme}, './Focusable': {Focusable: b.Focusable}, './control-model': controlModel,
    '../dev-observation': {useNativeObservation: (_: any, ref: any) => ({ref, textProps: {}, recordFocus() {}, recordDeclaredStyle() {}})}});
  const common = {react: b.react, 'react-native': native, 'react-native-reanimated': animation, 'react-native-svg': svg,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, left: 0, right: 0, bottom: 0})},
    '../../../../../packages/i18n/src/index': {defaultI18n}, '../controls/Button': {Button}, '../controls/Focusable': {Focusable: b.Focusable},
    '../focus': b.focus, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => b.theme, nativeType, textLines: (count: number) => ({numberOfLines: count, ellipsizeMode: 'tail'})}, '../useMotion': motion,
    './OverlayHost': host, './OverlayPaintContext': b.paint, './TVDesignCanvas': {TVDesignCanvas: 'DesignCanvas'}, '../focus/FocusRegion': {FocusWithin: ({children}: any) => children}, '../layout/ListEdgeFade': {ListEdgeFade: () => null, useListEdges: () => ({edges: {top: false, bottom: false}, scroll: {}})},
    './native-dismissal': await screenModule(new URL('../src-next/kit/overlays/native-dismissal.ts', import.meta.url), {react: b.react, 'react-native': native, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: 1000}})};
  const surface = await screenModule(new URL('../src-next/kit/overlays/surface.tsx', import.meta.url), {...common, '../../platform/layout/tv-canvas': canvas, './BottomSheet': {BottomSheet: 'UnusedTouchSheet'}});
  const pan: any = new Proxy({}, {get: () => () => pan});
  const sheet = await screenModule(new URL('../src-next/kit/overlays/BottomSheet.tsx', import.meta.url), {'../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true}, ...common, './surface': surface, './geometry': geometry,
    'react-native-gesture-handler': {Gesture: {Pan: () => pan}, GestureDetector: 'Gesture'}});
  const menu = await screenModule(new URL('../src-next/kit/overlays/Menu.tsx', import.meta.url), {...common, './surface': surface, './BottomSheet': sheet, './geometry': geometry,
    // R11: a TV menu's Back/Close hint is words only (aria-hidden); the fixture draws none.
    '../controls/KeyHints': {BackCloseHints: () => null},
    '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false}), BlurView: 'Blur'}, './anchored-menu-geometry': anchored, '../browse/touch-menu-geometry': touch});
  return {menu, surface, sheet, host, Button, back};
}

/** R9: on TV a popover is drawn as the side sheet (`BottomSheet`); its surface is the sheet's own. */
function tvPopover(o: any, props: any) {
  const drawn = o.menu.Popover(props);
  return drawn?.type === o.sheet.BottomSheet ? o.sheet.BottomSheet(drawn.props) : drawn;
}

for (const kind of ['home'] as const) test(`${kind} actual sort overlay scopes real rows and returns to its original opener`, async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const b = await bindings(), o = await overlayBindings(b);
  const presentation = await import('../src-next/kit/browse/presentation');
  const direction = await import('../../../packages/client-core/src/presentation/browse-toolbar');
  const controls = {Button: o.Button, Focusable: b.Focusable, Chip: 'UnusedChip', Segmented: 'Segmented'};
  const modules = {react: b.react, 'react-native': {...b.native, useWindowDimensions: () => ({width: 1920, height: 1080})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/browse-toolbar': direction,
    '../controls': controls, '../../kit/controls': controls, '../overlays': o.menu, '../../kit/overlays': o.menu,
    '../Icon': {Icon: 'Icon'}, '../../kit/Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => b.theme}, '../../kit/theme': {useTheme: () => b.theme},
    '@react-native-masked-view/masked-view': {default: 'MaskedView'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, './presentation': presentation, '../../kit/browse/presentation': presentation};
  // The library's own sort menu was unused and is gone; Library and Home both use the kit's Sort menu.
  const file = '../src-next/kit/browse/Toolbar.tsx';
  const mod = await screenModule(new URL(file, import.meta.url), {...modules, '../focus': b.focus, '../controls/control-model': controlModel}), component = mod.SortMenu;
  let tree: any, overlay: any, changes = 0;
  const scope = `${kind}:original-authority:original-route`, props: any = {scope, focusScope: scope,
    sorts: [{id: 'title', directions: ['asc', 'desc'], defaultDirection: 'asc'}, {id: 'added', directions: ['desc'], defaultDirection: 'desc'}],
    value: {field: 'title', direction: 'asc', flippable: true}, label: (value: any) => value.id, onValueChange: () => {changes++;}, onReshuffle() {}};
  const source = createFabricHookHost(() => {tree = component(props);}); await source.mount();
  // The kit's Sort menu draws the kit SortButton (its ref is the opener).
  const trigger = () => nodes(tree).find(node => node.type === b.Focusable || node.type?.name === 'SortButton');
  const anchor = trigger().props.ref ?? trigger().props.anchorRef;
  let restored = 0; anchor.current = {nativeTag: 800, requestTVFocus() {restored++;}, measureInWindow: (fn: any) => fn(0, 0, 40, 40)};
  trigger().props.onPress(); await source.rerender();
  let popover = nodes(tree).find(node => node.type === o.menu.Popover);
  const renderer = createFabricHookHost(() => {overlay = tvPopover(o, popover.props);}); await renderer.mount(); await renderer.flush();
  const boundary = nodes(overlay).find(node => node.type === o.surface.FocusBoundary), actual = boundary.type(boundary.props);
  const guide = await b.region(() => actual.props);
  const rows = nodes(tree).filter(node => node.type === o.menu.MenuRow);
  // These are the actual MenuRow→Focusable hosts, not declarations copied into a registry.
  const targets = await mountInOrder(rows, row => b.target(actual.props.id, () => row.type.render(row.props, row.props.ref).props));
  try {
    assert.equal(guide.guide.props.testID, `portico.region.${encodeURIComponent(scope + '.sort-menu')}`);
    assert.deepEqual(rows.map(row => row.props.focusId), ['sort:title', 'sort:added']);
    const segmented = nodes(tree).find(node => node.type === 'Segmented');
    assert.deepEqual(['asc', 'desc'].map(value => segmented.props.itemProps({value}).focusId), ['order:asc', 'order:desc']);
    assert.equal(new Set(targets.map(target => target.props.testID)).size, 2);
    targets[1]!.props.onPress(); await source.rerender(); assert.equal(changes, 1);
    popover = nodes(tree).find(node => node.type === o.menu.Popover); await renderer.rerender();
    t.mock.timers.tick(500); await renderer.rerender(); assert.equal(restored, 1, 'actual lifecycle restores the captured original anchor');
    props.focusScope = props.scope = scope + ':retained-second'; await source.rerender();
    assert.notEqual(nodes(tree).find(node => node.type === o.menu.Popover).props.testID, actual.props.id);
  } finally {await renderer.unmount(); await source.unmount(); await b.dispose(); t.mock.timers.reset();}
});

async function mountActualMenu(b: Awaited<ReturnType<typeof bindings>>, o: Awaited<ReturnType<typeof overlayBindings>>, get: () => any) {
  let menuTree: any, sheetTree: any;
  const menuDriver = createFabricHookHost(() => {menuTree = o.menu.Menu(get());}); await menuDriver.mount();
  const sheetDriver = createFabricHookHost(() => {sheetTree = menuTree.type(menuTree.props);}); await sheetDriver.mount();
  const boundary = nodes(sheetTree).find(node => node.type === o.surface.FocusBoundary), declaration = boundary.type(boundary.props);
  const guide = await b.region(() => declaration.props);
  const rows = nodes(menuTree).filter(node => node.type === o.menu.MenuRow);
  const targets = await mountInOrder(rows, row => b.target(declaration.props.id, () => row.type.render(row.props, row.props.ref).props));
  return {guide, rows, targets, async refresh() {await menuDriver.rerender(); await sheetDriver.rerender();}, async dispose() {await sheetDriver.unmount(); await menuDriver.unmount();}};
}

test('season caller supplies its actual section root to the real Menu and Back restores the season host', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const b = await bindings(), o = await overlayBindings(b);
  const labels = await import('../../../packages/client-core/src/presentation/kit-model');
  const words = await import('../../../packages/client-core/src/presentation/kit-model/text');
  const regionMap = await import('../src-next/screens/watch/tv-title-focus');
  const presentation = await import('../src-next/kit/browse/presentation');
  const {TVEpisodeBrowser} = await screenModule(new URL('../src-next/kit/frames/TVEpisodeBrowser.tsx', import.meta.url), {react: b.react, 'react-native': b.native,
    '../../../../../packages/client-core/src/presentation/kit-model': labels, '../../../../../packages/client-core/src/presentation/kit-model/text': words,
    '../../../../../packages/i18n/src': {defaultI18n}, '../../screens/watch/tv-title-focus': regionMap, '../../kit/focus': b.focus,
    '../../tv/layout/TVShelf': {TVShelf: 'Shelf', tvItemKey: (entry: any) => entry.id}, '../../kit/controls': {Focusable: b.Focusable, KeyHints: 'KeyHints'}, '../../kit/frames/title-parts': {SeasonListRow: 'SeasonListRow'},
    '../../kit/cards': {EpisodeTile: 'EpisodeTile'}, '../../kit/overlays': o.menu, '../../kit/browse/presentation': presentation, '../../kit/theme': {useTheme: () => b.theme, textLines: (count: number) => ({numberOfLines: count, ellipsizeMode: 'tail'})}});
  let tree: any, section = {id: 'original-show.episodes'}, commands = 0;
  const props: any = {seasons: [{id: 'season', showId: 'show', number: 1, episodeCount: 3, watchedCount: 1}], selectedSeasonId: 'season', episodes: [], show: {title: 'Show'},
    open: {phase: 'idle'}, text: {t: defaultI18n.t}, artwork: () => undefined, more() {}, onSeasonFocus() {}, onEpisodeFocus() {}, onSelectSeason() {},
    onPlayEpisode() {}, onPlaySeason() {commands++;}, onMarkSeason() {}, onMoreSeasons() {}, onMoreEpisodes() {}, onRetryEpisode() {}, renderError: () => null};
  const source = createFabricHookHost(() => {tree = TVEpisodeBrowser({...props, focus: section});}); await source.mount();
  // The row is the kit's SeasonListRow, a Focusable with the row's focus id and commands.
  const season = () => nodes(tree).find(node => node.type === 'SeasonListRow' && node.props.testID === 'tv-season-season');
  const seasonRegion = nodes(tree).find(node => node.type === b.focus.FocusRegion && node.props.id.endsWith('.seasons'));
  await b.region(() => seasonRegion.props); const opener = await b.target(seasonRegion.props.id, () => season().props); opener.props.onFocus({});
  season().props.onLongPress(); await source.rerender();
  const menu = () => nodes(tree).find(node => node.type === o.menu.Menu).props;
  const mounted = await mountActualMenu(b, o, menu);
  try {
    assert.equal(menu().testID, 'original-show.episodes.season-menu');
    t.mock.timers.tick(160); assert.equal(mounted.targets[0]!.calls, 1, 'actual safe initial ref requests its real native host');
    assert.equal(new Set(mounted.targets.map(target => target.props.testID)).size, 2);
    assert.equal([...o.back][0]!(), true); await source.rerender(); await mounted.refresh(); t.mock.timers.tick(500); await mounted.refresh();
    assert.equal(opener.calls, 1, 'actual lifecycle restores the original season address'); assert.equal(commands, 0);
    section = {id: 'retained-other-show.episodes'}; await source.rerender();
    assert.equal(menu().testID, 'retained-other-show.episodes.season-menu');
  } finally {await mounted.dispose(); await source.unmount(); await b.dispose(); t.mock.timers.reset();}
});

test('TV Saved caller passes original grid sort/filter roots through actual Menu and real host identities', async () => {
  const b = await bindings(), o = await overlayBindings(b);
  const buckets = await import('../../../packages/client-core/src/browse-buckets');
  const saved = await import('../../../packages/client-core/src/saved');
  const {TVSavedScreen} = await savedFixture('TVSaved', {
    react: b.react, 'react-native': b.native, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/browse-buckets': buckets,
    '../../../../../../packages/client-core/src/saved': saved, '../../../tv/layout/TVFocusGrid': {TVFocusGrid: 'Grid'}, '../../kit/saved/SavedForms': {SavedConfirmSheet: 'Confirm'},
    '../../../kit/layout': {AlphabetRail: 'Alphabet'}, '../useTVPageBack': {useTVPageBack() {}}, '../useEntryFocus': {useDestinationEntryFocus() {}},
    '../../../shell/model': {sameAuthority: () => true}, '../../../../../../packages/client-core/src/presentation/kit-model/text': {durationText: String},
    '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../../kit/controls': {Button: o.Button, Chip: 'Chip', Focusable: b.Focusable},
    '../../../kit/cards': {PosterCard: 'Poster', SquareCard: 'Square'}, '../../../kit/overlays': {...o.menu, Dialog: 'Dialog'}, '../../../kit/states': {StateView: 'State'},
    '../../../kit/focus': b.focus, '../../../kit/browse/presentation': {browseType: () => ({})}, '../../../kit/theme': {useTheme: () => b.theme},
    '../focus': {useCovered: () => false, nativeDestinationFocusIds: (authority: any) => ({savedTabs: 'tabs', savedGrid: authority.grid, savedToolbar: 'toolbar', rail: 'rail'})},
    '../../kit/saved/SavedTVLoading': {SavedTVLoading: 'SavedTVLoading'}, '../../saved/useSavedTVTabs': {useSavedTVTabs: () => undefined}, '../../../pages/tv/saved/SavedPage': {TVSavedPage: 'TVSavedPage'}, './SavedDirectoryPage': {SavedTVDirectory: 'Directory'}});
  let tree: any, scope = 'original.saved'; const selected: any[] = [];
  const snapshot = {phase: 'ready', entries: [], resources: [], posters: {sections: []}, squares: {sections: []}};
  const props: any = {controller: {subscribe: () => () => {}, getSnapshot: () => snapshot, source: () => ({}), summary: async () => {}, select: async (...value: any[]) => {selected.push(value);}},
    activation: {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {visible: true}; return () => snapshot;})()},
    runtime: {reportError() {}, text: {}, artwork: () => undefined, getSnapshot: (() => {const snapshot = {libraries: []}; return () => snapshot;})()}, actions: {isMenuOpen: () => false}, initialTab: 'watchlist',
    openSaved() {}, openEntry() {}, browseHome() {}};
  const source = createFabricHookHost(() => {tree = TVSavedScreen({...props, authority: {grid: scope}});}); await source.mount(); selected.length = 0;
  try {
    const menus = () => nodes(tree).filter(node => node.type === o.menu.Menu);
    assert.deepEqual(menus().map(node => node.props.testID), ['original.saved.sort-menu', 'original.saved.filter-menu']);
    for (const node of menus()) {
      let tree: any; const driver = createFabricHookHost(() => {tree = o.menu.Menu({...node.props, open: true});}); await driver.mount();
      const actualRows = nodes(tree).filter(node => node.type === o.menu.MenuRow);
      await b.region(() => ({id: node.props.testID}));
      const hosts = await mountInOrder(actualRows, row => b.target(node.props.testID, () => row.type.render(row.props, row.props.ref).props));
      assert.ok(hosts.length); assert.equal(new Set(hosts.map(host => host.props.testID)).size, hosts.length);
      hosts[0]!.props.onPress(); await driver.unmount();
    }
    assert.equal(selected.length, 2); scope = 'retained.saved'; await source.rerender();
    assert.deepEqual(menus().map(node => node.props.testID), ['retained.saved.sort-menu', 'retained.saved.filter-menu']);
  } finally {await source.unmount(); await b.dispose();}
});

test('MusicMixes actual original caller scope names its panel through failed busy work without enabling commands', async () => {
  const b = await bindings(), o = await overlayBindings(b), presentation = await import('../src-next/kit/browse/presentation');
  const {MusicMixesRow} = await screenModule(new URL('../src-next/kit/music/MusicMixesRow.tsx', import.meta.url), {
    react: b.react, 'react-native': b.native, 'react-native-svg': {default: 'Svg', Defs: 'Defs', RadialGradient: 'Gradient', Rect: 'Rect', Stop: 'Stop'}, '../../../../../packages/client-core/src/music-mixes-controller': {mixSaveOnServer: false},
    '../../../../../packages/i18n/src': {defaultI18n}, '../artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../controls': {Focusable: b.Focusable},
    '../focus': b.focus, '../overlays': {...o.menu, SidePanel: o.surface.SidePanel}, '../theme': {useTheme: () => b.theme, nativeType, textLines: (count: number) => ({numberOfLines: count, ellipsizeMode: 'tail'})},
    '../browse/presentation': presentation});
  const row = {id: 'original-mix', name: 'Mix', tint: '#123456', artists: [], more: false, songCount: 20, covers: []};
  const snapshot = {phase: 'ready', rows: [row]}, lifetime = {isCurrent: () => true};
  let reject!: (error: Error) => void, calls = 0, tree: any, mixScope = 'original-library-scope';
  const port = {subscribe: () => () => {}, getSnapshot: () => snapshot, subscribeRevision: () => () => {}, getRevision: () => 1,
    captureCaller: () => ({lifetime, dispose() {}}), load: async () => {}, play: async (_row: any, actual: any) => {assert.equal(actual, lifetime); calls++; await new Promise<void>((_, no) => {reject = no;});}};
  const source = createFabricHookHost(() => {tree = MusicMixesRow({port, artwork: () => undefined, reportError() {}, focusScope: mixScope});}); await source.mount(); await source.flush();
  const tile = nodes(tree).find(node => node.type?.name === 'MusicMixTile'), opener = {current: {nativeTag: 700, requestTVFocus() {}}};
  tile.props.onMenu(opener); await source.rerender();
  const panel = () => nodes(tree).find(node => node.type === o.surface.SidePanel);
  await b.region(() => ({id: panel().props.testID}));
  const rows = () => nodes(panel()).filter(node => node.type === o.menu.MenuRow);
  const target = await b.target(panel().props.testID, () => rows()[0]!.type.render(rows()[0]!.props, rows()[0]!.props.ref).props);
  try {
    assert.equal(panel().props.testID, 'original-library-scope.original-mix.menu'); assert.equal(panel().props.returnFocusRef, opener);
    target.props.onPress(); mixScope = 'successor-scope'; await source.rerender(); await target.refresh();
    assert.equal(calls, 1); assert.equal(target.props.onPress, undefined); assert.equal(panel().props.dismissOnBack, false);
    reject(new Error('fixture failure')); await flush(); await target.refresh();
    assert.equal(panel().props.testID, 'original-library-scope.original-mix.menu'); assert.equal(typeof target.props.onPress, 'function');
    assert.equal(panel().props.returnFocusRef, opener); assert.equal(calls, 1, 're-enabling the same menu sends no request');
  } finally {await source.unmount(); await b.dispose();}
});

test('ViewToggle caller binds both actual hosts independently with semantic IDs', async () => {
  const b = await bindings(), o = await overlayBindings(b), presentation = await import('../src-next/kit/browse/presentation');
  const direction = await import('../../../packages/client-core/src/presentation/browse-toolbar');
  const mod = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {react: b.react, 'react-native': b.native, '@react-native-masked-view/masked-view': {default: 'MaskedView'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, 
    '../focus': b.focus, '../controls/control-model': controlModel,
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/browse-toolbar': direction,
    '../controls': {Button: o.Button, Focusable: b.Focusable}, '../overlays': o.menu, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => b.theme}, './presentation': presentation});
  const selected: string[] = []; const tree = mod.ViewToggle({value: 'grid', onValueChange: (value: string) => selected.push(value), itemProps: (value: string) => ({focusId: `view:${value}`})});
  await b.region(() => ({id: 'actual-toolbar'}));
  const targets = await mountInOrder(nodes(tree).filter(node => node.type === b.Focusable), node => b.target('actual-toolbar', () => node.props));
  try {
    assert.equal(targets.length, 2); assert.notEqual(targets[0]!.props.testID, targets[1]!.props.testID);
    assert.equal(b.registry.child({regionId: 'actual-toolbar', childId: 'view:grid'}), targets[0]!.host);
    assert.equal(b.registry.child({regionId: 'actual-toolbar', childId: 'view:list'}), targets[1]!.host);
    targets[1]!.props.onPress(); assert.deepEqual(selected, ['list']);
  } finally {await b.dispose();}
});

test('TV Now Playing names More and Lyrics from the actual mounted instance and restores original More host', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let motionReady = true;
  const b = await bindings(), o = await overlayBindings(b), dock = await import('../src-next/kit/dock/model');
  const text = await import('../../../packages/client-core/src/presentation/kit-model/text');
  const playerBindings = {
    react: b.react, 'react-native': b.native, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/kit-model/text': text,
    '../../../../../../packages/player-core/src': {sameFence: (a: any, c: any) => a === c}, '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'Ambient', Artwork: 'Artwork'},
    '../../../kit/frames': {AudioPlayerFrame: 'TVAudioPlayerPage'}, '../../../kit/player/TVAudioSurface': {TVAudioCanvas: 'TVAudioCanvas', TVAudioLyrics: o.surface.SidePanel, TVAudioEqualizer: 'TVAudioEqualizer', TVAudioSurface: 'TVAudioSurface'}, '../../../kit/focus': {requestNativeFocus: registryModule.requestNativeFocus, provideTVLayerClaim: (key: string, claim: () => boolean) => {provided.push({key, claim}); return () => {released.push(claim);};}}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/overlays': {...o.menu, SidePanel: o.surface.SidePanel},
    '../../../kit/theme': {useTheme: () => b.theme, nativeType}, '../../../kit/dock/model': dock,
    '../../../shell/model': {shellLayout: () => ({width: 1920, height: 1080, left: 0, top: 0, scale: 1})},
    '../../../platform/playback/equalizer': {nativeEqualizer: {subscribe: () => () => {}, getSnapshot: (() => {const snapshot = {supported: true}; return () => snapshot;})()}},
    '../../../platform/overlay-keys': {registerOverlayKeys: () => () => {}}, '../../shared/player/EqualizerPage': {EqualizerPage: 'Equalizer'},
    '../../../kit/player/SignalPath': {SignalLine: 'Signal', SignalSheet: 'SignalSheet', TVSignalSheet: 'SignalSheet'}, '../../../kit/player/ListeningNotice': {NativeListeningNotice: 'Notice'},
    './useNowPlayingQueue': {nowPlayingSource: () => undefined, nowPlayingQueueRows: () => [], nowPlayingQueueFocusScope: () => '', nowPlayingQueueEditPort: () => undefined},
    '../../../kit/player/ListeningSongActions': {listeningSongFavorite: () => undefined}, '../../../kit/player/TVMusicFocus': {MusicFocusRegion: b.focus.FocusRegion, TVMusicFocus: 'MusicFocus', TVMusicSeek: 'Seek'}, './useTVQueuePolicy': {useTVQueuePolicy: () => ({})},
    '../../../tv/layout/TVPageMotion': {TVPageGroup: 'PageGroup', TVPageTransition: 'Transition', useTVPageMotion: () => ({open: true, ready: motionReady})}};
  const playerHook = await screenModule(new URL('../src-next/pages/shared/player/useTVAudioPlayer.ts', import.meta.url), playerBindings);
  const {TVAudioPlayerPage: TVNowPlayingScreen} = await screenModule(new URL('../src-next/pages/tv/player/AudioPlayerPage.tsx', import.meta.url), {
    ...playerBindings, '../../shared/player/useTVAudioPlayer': playerHook,
    '../../../kit/player/NowPlayingQueue': {NowPlayingQueue: 'Queue'},
    '../../shared/player/useNowPlayingQueue': playerBindings['./useNowPlayingQueue'],
  });
  const props: any = {occupant: {mode: 'music', playback: {fence: {}, kind: 'music', queue: null, positionSeconds: 5, playing: true},
    presentation: {identity: {title: 'Song', subtitle: []}, durationSeconds: 100, capabilities: {play: true, pause: true, previous: false, next: true}}},
    width: 1920, height: 1080, artwork: () => undefined, onAction() {}, onQueuePlay() {}, onClose() {}, lyrics: React.createElement('Lyrics'), equalizerCopy: {}};
  const wrap = TVNowPlayingScreen(props).props.children; let tree: any, second: any;
  // W-9, TVF-A1: the listening screen tells the TV layer stack which control it opens on; the stack asks when the
  // layer goes on top and again as elements attach. Nothing here loops on frames.
  const provided: {key: string; claim: () => boolean}[] = [], released: (() => boolean)[] = [];
  const realFrame = globalThis.requestAnimationFrame, frames: unknown[] = [];
  globalThis.requestAnimationFrame = ((callback: () => void) => frames.push(callback)) as never;
  t.after(() => {globalThis.requestAnimationFrame = realFrame;});
  const source = createFabricHookHost(() => {tree = wrap.type(wrap.props);}); await source.mount();
  const button = (name: string, from = tree) => {
    const frame = nodes(from).find(node => node.type === 'TVAudioPlayerPage').props;
    return [...frame.transport, frame.lyrics, frame.playlist, frame.more].find(control => control?.testID === `now-playing-${name}`);
  };
  const claimed: string[] = [];
  for (const name of ['toggle', 'next', 'previous', 'lyrics']) button(name).anchorRef.current = {requestTVFocus() {claimed.push(name);}};
  assert.equal(provided.length, 1); assert.equal(provided[0]!.key, 'listening');
  assert.equal(provided[0]!.claim(), true, 'a request was sent');
  assert.deepEqual(claimed, ['toggle'], 'the player opens with focus on Play/Pause, off the covered page underneath');
  assert.equal(frames.length, 0, 'no frame loop');
  // At the end of a track Play is not available: the first control that is takes focus instead.
  const ended = {...props, occupant: {...props.occupant, playback: {...props.occupant.playback, playing: false},
    presentation: {...props.occupant.presentation, capabilities: {play: false, pause: false, previous: false, next: false}}}};
  const endedWrap = TVNowPlayingScreen(ended).props.children; let endedTree: any;
  const endedHost = createFabricHookHost(() => {endedTree = endedWrap.type(endedWrap.props);}); await endedHost.mount();
  const endedClaims: string[] = [];
  for (const name of ['toggle', 'next', 'previous', 'lyrics']) button(name, endedTree).anchorRef.current = {requestTVFocus() {endedClaims.push(name);}};
  provided.at(-1)!.claim();
  assert.deepEqual(endedClaims, ['lyrics'], 'a disabled Play/Pause, Next and Previous are skipped');
  const endedClaim = provided.at(-1)!.claim;
  await endedHost.unmount();
  assert.ok(released.includes(endedClaim), 'a screen that goes stops providing its claim');
  // 2eec0520: before its views attach a request can't land: the claim says so, and the stack asks again as they attach.
  const retryWrap = TVNowPlayingScreen(props).props.children; let retryTree: any;
  const retryHost = createFabricHookHost(() => {retryTree = retryWrap.type(retryWrap.props);}); await retryHost.mount();
  const retryClaim = provided.at(-1)!.claim;
  assert.equal(retryClaim(), false, 'nothing attached yet: no request sent');
  const retryClaims: string[] = [];
  button('toggle', retryTree).anchorRef.current = {requestTVFocus() {retryClaims.push('toggle');}};
  assert.equal(retryClaim(), true); assert.deepEqual(retryClaims, ['toggle']);
  await retryHost.unmount();
  // Reviewer (#577): while the page is still moving in (not ready) the claim declines even with its views attached;
  // once ready, the same provided claim lands. The stack asks it again when the transport region turns enabled
  // (next-tv-layer-stack covers that re-ask).
  motionReady = false;
  const movingWrap = TVNowPlayingScreen(props).props.children; let movingTree: any;
  const movingHost = createFabricHookHost(() => {movingTree = movingWrap.type(movingWrap.props);}); await movingHost.mount();
  const movingClaim = provided.at(-1)!.claim, movingClaims: string[] = [];
  button('toggle', movingTree).anchorRef.current = {requestTVFocus() {movingClaims.push('toggle');}};
  assert.equal(movingClaim(), false, 'not ready: no request, even attached');
  assert.deepEqual(movingClaims, []);
  motionReady = true; await movingHost.rerender();
  assert.equal(provided.at(-1)!.claim, movingClaim, 'the same claim stays provided across the render');
  assert.equal(movingClaim(), true, 'ready: it lands'); assert.deepEqual(movingClaims, ['toggle']);
  await movingHost.unmount();
  const another = createFabricHookHost(() => {second = wrap.type(wrap.props);}); await another.mount();
  frames.length = 0;
  const ref = button('more').anchorRef; let restored = 0; ref.current = {nativeTag: 701, requestTVFocus() {restored++;}};
  button('more').onPress(); await source.rerender();
  const menu = () => nodes(tree).find(node => node.type === o.menu.Menu).props;
  const mounted = await mountActualMenu(b, o, menu);
  try {
    assert.ok(menu().testID.endsWith('.more')); assert.notEqual(menu().testID, nodes(second).find(node => node.type === o.menu.Menu).props.testID);
    assert.equal(menu().returnFocusRef, ref); t.mock.timers.tick(160); assert.equal(mounted.targets[0]!.calls, 1);
    assert.equal([...o.back][0]!(), true); await source.rerender(); await mounted.refresh(); t.mock.timers.tick(500); await mounted.refresh(); assert.equal(restored, 1);
    button('lyrics').onPress(); await source.rerender();
    const lyrics = nodes(tree).find(node => node.type === o.surface.SidePanel);
    assert.equal(lyrics.props.testID, menu().testID.replace(/\.more$/, '.lyrics'));
    let panel: any; const driver = createFabricHookHost(() => {panel = o.surface.SidePanel(lyrics.props);}); await driver.mount();
    const boundary = nodes(panel).find(node => node.type === o.surface.FocusBoundary), actual = boundary.type(boundary.props); await b.region(() => actual.props);
    assert.equal(actual.props.id, lyrics.props.testID); await driver.unmount();
  } finally {await mounted.dispose(); await source.unmount(); await another.unmount(); await b.dispose(); t.mock.timers.reset();}
});

test('Home See all actual toolbar binds sort/filter/kind/quick leaves without changing its captured open callbacks', async () => {
  const b = await bindings(), o = await overlayBindings(b), design = await import('@portico/design');
  const presentation = await import('../src-next/kit/browse/presentation');
  const direction = await import('../../../packages/client-core/src/presentation/browse-toolbar');
  const value = await import('../src-next/kit/controls/useControlValue');
  const {Chip} = await screenModule(new URL('../src-next/kit/controls/Chip.tsx', import.meta.url), {react: b.react, 'react-native': b.native, '@portico/design': design,
    '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => b.theme, fontFamilies}, './Focusable': {Focusable: b.Focusable}, './control-model': controlModel, './useControlValue': value});
  const browse = await screenModule(new URL('../src-next/kit/browse/Toolbar.tsx', import.meta.url), {react: b.react, 'react-native': b.native, '@react-native-masked-view/masked-view': {default: 'MaskedView'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, 
    '../focus': b.focus, '../controls/control-model': controlModel,
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/browse-toolbar': direction,
    '../controls': {Button: o.Button, Chip, Focusable: b.Focusable, Segmented: 'Segmented'}, '../overlays': o.menu, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => b.theme}, './presentation': presentation});
  const sort = {field: 'title', direction: 'asc'}, quick = [{id: 'unwatched', active: false, query: {variant: 'predicate', field: 'watched', operator: 'equals', value: false}}];
  const toolbar = {sorts: [{id: 'title', directions: ['asc', 'desc'], defaultDirection: 'asc'}], sort, quick, applied: [], groups: [{id: 'activity', fields: [{id: 'playState'}]}]};
  const {SeeAllPage} = await screenModule(new URL('../src-next/pages/tv/library/SeeAllPage.tsx', import.meta.url), {
    react: b.react, '../../../kit/frames': {BrowseFrame: 'BrowseFrame'}, '../../../kit/frames/BrowseGrid': {BrowseGrid: 'BrowseGrid'},
    '../../../kit/browse': {...browse, FilteredEmpty: 'FilteredEmpty'}, '../../../kit/browse/HomeRowCard': {HomeRowCard: 'HomeRowCard'},
    '../../../kit/controls': {Button: o.Button, Chip}, '../../../kit/focus': b.focus, '../../../kit/artwork': {AmbientArt: 'Ambient'},
    '../../../kit/states': {StateView: 'StateView'},
    '../../shared/library/useSeeAll': {useSeeAll: ({controller}: any) => ({
      t: defaultI18n.t, ready: true, model: toolbar, labels: {sort: (value: any) => value.id, quick: (value: any) => value.id}, title: 'Row', applied: [],
      kinds: [{id: 'all', onPress() {}}, {id: 'movies', onPress: () => controller.selectKind('movies')}],
      focus: {header: {entry: 'back'}, toolbar: {id: 'home-see-all:original-row.toolbar'}, grid: {id: 'grid'}},
      isCurrent: () => true, squareCard: () => false,
    })},
  });
  const calls: any[] = [], open = (...args: unknown[]) => calls.push(args), playEntry = (...args: unknown[]) => calls.push(args);
  const controller = {selectKind: (id: string) => calls.push(id), setSort() {}};
  const page = SeeAllPage({controller, runtime: {}, active: true, onOpen: open, onPlayEntry: playEntry,
    onFilters: () => calls.push('filters'), kindLabel: (id: string) => id});
  const tree = page.props.toolbar.type(page.props.toolbar.props);
  const region = nodes(tree).find(node => node.type === b.focus.FocusRegion); await b.region(() => region.props);
  const slots = nodes(tree).filter(node => node.type === b.focus.FocusItem);
  const leaf = (element: any): any => {
    if (element.type === b.Focusable) return element.props;
    const rendered = typeof element.type === 'function' ? element.type(element.props) : element.type.render(element.props, element.props.ref);
    const direct = nodes(rendered).find(node => node.type === b.Focusable);
    if (direct) return direct.props;
    const next = nodes(rendered).find(node => node.type === Chip || node.type === o.Button); return leaf(next);
  };
  const targets = await mountInOrder(slots, slot => b.target(region.props.id, () => leaf(slot.props.children), b.focus.FocusItem(slot.props).props.value));
  try {
    assert.deepEqual(slots.map(slot => slot.props.focusId), ['filters', 'quick:unwatched', 'kind:all', 'kind:movies']);
    assert.equal(new Set(targets.map(target => target.props.testID)).size, slots.length);
    const sortMenu = nodes(tree).find(node => node.type === browse.SortMenu);
    assert.equal(sortMenu.props.scope, region.props.id);
    targets[0]!.props.onPress({}); targets[3]!.props.onPress({defaultPrevented: false}); assert.deepEqual(calls, ['filters', 'movies']);
    const entry = {id: 'film', kind: 'movie', title: 'Film'};
    const card = page.props.grid.props.renderItem({membershipId: 'entry', entry}, {index: 0}, 200).props.children;
    card.props.onOpen(); card.props.onPlay();
    assert.equal(calls[2][0], entry); assert.equal(calls[3][0], entry);
  } finally {await b.dispose();}
});

test('retained Home recommendation menu delegates the original entry, anchor and authority to the shared owner', async () => {
  const b = await bindings();
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  const route = {kind: 'home-see-all', destination: {type: 'row', rowId: 'original-recommendations'}};
  let currentRoute = route, live = true, receivedContext: any, receivedMembership: any;
  const calls: any[] = [], entry = {id: 'entry', kind: 'movie'};
  class Controller {
    rowId: string; constructor(options: any) {this.rowId = options.rowId;}
    collection = {getSnapshot: (() => {const snapshot = {refreshing: false}; return () => snapshot;})(), residentItems: () => [{entry}], subscribe: () => () => {}};
    subscribe = () => () => {}; snapshot: any; getSnapshot = () => this.snapshot ??= {phase: 'ready', row: {id: this.rowId, kind: 'recommendation'}}; dispose() {}
  }
  const mod = await screenModule(new URL('../src-next/shell/see-all-route.tsx', import.meta.url), {...stackSlotFixture, react: b.react,
    'react-native': {...b.native, Platform: {isTV: true}, AccessibilityInfo: {isScreenReaderEnabled: async () => false, addEventListener: () => ({remove() {}})}}, '../kit/focus': b.focus,
    '../../../../packages/client-core/src/recommendation-feedback': {isRecommendationRow: () => true}, '../kit/overlays': {ToastHost: 'ToastHost'}, '../kit/controls': {},
    '../../../../packages/i18n/src': {defaultI18n}, '@portico/design': {}, '../pages/touch/library/SeeAllPage': {}, '../pages/tv/library/SeeAllPage': {SeeAllPage: 'HomeSeeAll'}, '../pages/shared/home/HomeRowFiltersSheet': {HomeRowFiltersSheet: 'Filters'}, '../screens/home/row-controller': {NativeHomeRowController: Controller},
    '../platform/server-connections': {}, './routes': {}, './route-registry': {nativeShellRoutes: {registerRoute() {}}}, './model': {},
    '../kit/theme': {}, '../kit/dock/DockClearance': {useDockClearance: () => 0},
    '../screens/destinations/entry-actions': {createNativeEntryActions: (context: any, membership: any) => {
      receivedContext = context; receivedMembership = membership;
      return {menu: (...args: any[]) => calls.push(args), dispose() {}};
    }}, '../screens/read/platform': {hiddenReadingLibraryOnTV: () => false}, '../screens/read/reading-books-open': {}, '../screens/home/entry-lifetime': {}});
  const context: any = {current: () => live, authority: () => authority, libraries: () => [], api: {}, navigation: {retainedRoutes: () => [], getSnapshot: () => ({route: currentRoute})}};
  const rows = mod.createNativeHomeRows(context, () => {}); rows.reconcile();
  const runtime = {getSnapshot: (() => {const snapshot = {authority}; return () => snapshot;})()}, retained = nodes(rows.render(runtime))[0]; let tree: any;
  const source = createFabricHookHost(() => {tree = retained.type(retained.props);}); await source.mount();
  try {
    const home = nodes(tree).find(node => node.type === 'HomeSeeAll'), opener = {current: {nativeTag: 730, requestTVFocus() {}}};
    home.props.onMenu(entry, opener);
    assert.equal(receivedContext, context, 'the shared owner receives the original bootstrap authority');
    assert.equal(receivedMembership.current(), true);
    assert.equal(receivedMembership.contains(entry), true);
    assert.equal(receivedMembership.contains({...entry}), false, 'same-ID replacements are not the loaded member');
    assert.equal(calls.length, 1); assert.equal(calls[0][0], entry); assert.equal(calls[0][1], opener);
    assert.equal(calls[0][2], undefined); assert.equal(calls[0][3][0].id, 'not-interested');
    assert.equal(calls[0][3][0].label, defaultI18n.t('entry.notInterested'));
    currentRoute = {...route}; assert.equal(receivedMembership.current(), false, 'the original route cannot borrow its replacement');
    currentRoute = route; live = false; assert.equal(receivedMembership.current(), false);
  } finally {await source.unmount(); rows.dispose(); await b.dispose();}
});

test('retained Home Undo uses the shared toast with the original entry command', async () => {
  // SUP-029: See all's Undo is the shared ToastHost. Its announcement, accessible
  // actions and screen-reader hold are the host's own (next-a11y-feedback).
  const b = await bindings();
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  const route = {kind: 'home-see-all', destination: {type: 'row', rowId: 'recommendations'}};
  const commands: boolean[] = []; let extra: any;
  class Controller {
    rowId: string; constructor(options: any) {this.rowId = options.rowId;}
    collection = {getSnapshot: (() => {const snapshot = {refreshing: false}; return () => snapshot;})(), residentItems: () => [], subscribe: () => () => {}};
    subscribe = () => () => {}; snapshot: any; getSnapshot = () => this.snapshot ??= {phase: 'ready', row: {id: this.rowId, kind: 'recommendation'}}; dispose() {}
  }
  const mod = await screenModule(new URL('../src-next/shell/see-all-route.tsx', import.meta.url), {...stackSlotFixture, react: b.react,
    'react-native': {...b.native, Platform: {isTV: true}}, '../kit/focus': b.focus,
    '../../../../packages/client-core/src/recommendation-feedback': {isRecommendationRow: () => true, notInterestedTarget: (entry: unknown) => entry, setNotInterested: async () => true},
    '../kit/overlays': {ToastHost: 'ToastHost'}, '../kit/controls': {},
    '../../../../packages/i18n/src': {defaultI18n}, '@portico/design': {}, '../pages/touch/library/SeeAllPage': {}, '../pages/tv/library/SeeAllPage': {SeeAllPage: 'HomeSeeAll'}, '../pages/shared/home/HomeRowFiltersSheet': {HomeRowFiltersSheet: 'Filters'}, '../screens/home/row-controller': {NativeHomeRowController: Controller},
    '../platform/server-connections': {}, './routes': {}, './route-registry': {nativeShellRoutes: {registerRoute() {}}}, './model': {},
    '../kit/theme': {}, '../kit/dock/DockClearance': {useDockClearance: () => 0},
    '../screens/destinations/entry-actions': {createNativeEntryActions: () => ({menu: (_entry: unknown, _anchor: unknown, _select: unknown, extras: any[]) => {extra = extras?.[0];}, dispose() {}})},
    '../screens/read/platform': {hiddenReadingLibraryOnTV: () => false}, '../screens/read/reading-books-open': {}, '../screens/home/entry-lifetime': {}});
  const context: any = {current: () => true, authority: () => authority, libraries: () => [], api: {}, navigation: {retainedRoutes: () => [], getSnapshot: (() => {const snapshot = {route}; return () => snapshot;})()}};
  const rows = mod.createNativeHomeRows(context, () => {}); rows.reconcile();
  const runtime = {getSnapshot: (() => {const snapshot = {authority}; return () => snapshot;})(), reportError: (error: unknown) => {throw error;}}, retained = nodes(rows.render(runtime))[0];
  const entry = {id: 'entry', kind: 'movie', title: 'Original title'}, anchor = {current: null};
  let tree: any, active = true;
  const source = createFabricHookHost(() => {tree = retained.type({...retained.props, active, onNotInterested: async (target: unknown, value: boolean) => {assert.equal(target, entry); commands.push(value); return true;}});}); await source.mount();
  const toast = () => nodes(tree).find(node => node.type === 'ToastHost')?.props.toast;
  const remove = async () => {nodes(tree).find(node => node.type === 'HomeSeeAll').props.onMenu(entry, anchor); extra.onSelect(); await flush(); await source.rerender();};
  try {
    await remove();
    assert.equal(toast().message, defaultI18n.t('entry.notInterestedDone', {title: entry.title}));
    assert.equal(toast().action.label, defaultI18n.t('action.undo'));
    assert.equal(toast().actionOnTV, true, 'TV keeps its Undo');
    toast().action.onPress(); await flush(); await source.rerender();
    assert.deepEqual(commands, [true, false], 'Undo uses the same original entry command');
    assert.equal(toast(), null);
    await remove(); const dismissed = toast();
    nodes(tree).find(node => node.type === 'ToastHost').props.onDismiss(dismissed.id); await source.rerender(); dismissed.action.onPress(); await flush();
    assert.deepEqual(commands, [true, false, true], 'Dismiss does not invoke Undo'); assert.equal(toast(), null);
    // A result that settles while the retained row is hidden shows nothing.
    nodes(tree).find(node => node.type === 'HomeSeeAll').props.onMenu(entry, anchor); extra.onSelect(); active = false; await source.rerender(); await flush(); await source.rerender();
    assert.equal(toast(), null);
  } finally {await source.unmount(); rows.dispose(); await b.dispose();}
});

test('Home filters footer has an actual named Button host and preserves apply-then-close', async () => {
  const b = await bindings(), o = await overlayBindings(b), filters = await import('../../../packages/client-core/src/presentation/browse-toolbar');
  const year = {id: 'year', type: 'number', controlHint: 'number-range', operators: ['between']};
  const {FilterSheet} = await screenModule(new URL('../src-next/kit/browse/FilterSheet.tsx', import.meta.url), {react: b.react, 'react-native': b.native,
    '../controls': {Button: o.Button}, '../overlays': {SidePanel: o.surface.SidePanel, BottomSheet: o.sheet.BottomSheet}, '../theme': {useTheme: () => b.theme}});
  const hook = await screenModule(new URL('../src-next/pages/shared/home/useHomeRowFilters.ts', import.meta.url), {react: b.react,
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/browse-toolbar': filters,
    '../../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel: () => ({groups: [{id: 'title', fields: [year]}]})},
    '../../../kit/theme': {useTheme: () => b.theme}, '../../../screens/home/row-controller': {}, '../../../screens/home/row-filters': {}, '../../../screens/home/browse-label': {homeBrowseLabel: () => ''}});
  const {HomeRowFiltersSheet} = await screenModule(new URL('../src-next/pages/shared/home/HomeRowFiltersSheet.tsx', import.meta.url), {react: b.react,
    '../../../kit/browse': {FilterBuilder: 'Builder', FilterSheet}, './useHomeRowFilters': hook});
  // The page draws the kit sheet; the sheet draws the TV side panel this test reads.
  const HomeRowFilters = (props: any) => {const page = HomeRowFiltersSheet(props); return page.type(page.props);};
  const state = {filters: [], capabilities: {fields: [year], library: {kind: 'movie'}}, sort: {field: 'title', direction: 'asc'}};
  const calls: string[] = []; let tree: any;
  const controller = {rowId: 'row', subscribe: () => () => {}, getSnapshot: () => state, previewCounts: async () => [{id: 'all', total: 4, totalIsLowerBound: false}],
    setFilters: () => {calls.push('apply'); return true;}};
  const runtime = {text: {t: defaultI18n.t}, getSnapshot: (() => {const snapshot = {account: {viewerRole: 'member'}}; return () => snapshot;})(), reportError() {}};
  const source = createFabricHookHost(() => {tree = HomeRowFilters({controller, runtime, onClose: () => calls.push('close')});}); await source.mount(); await flush();
  const footer = nodes(tree.props.footer).find(node => node.type === o.Button);
  const yearControl = nodes(tree).find(node => node.type === 'Builder').props.fields.year;
  assert.equal(yearControl.kind, 'range');
  assert.ok(yearControl.presets.some((preset: {label: string}) => preset.label === '1980s'));
  await b.region(() => ({id: tree.props.testID}));
  const target = await b.target(tree.props.testID, () => {const outer = o.Button.render(footer.props, null); return outer.type.render(outer.props, outer.props.ref).props;});
  try {
    assert.equal(b.registry.child({regionId: 'home-row-filters', childId: 'show-results'}), target.host);
    target.props.onPress(); assert.deepEqual(calls, ['apply', 'close']);
  } finally {await source.unmount(); await b.dispose();}
});

test('actual Search registration keeps equal-value retained genre R1/R2 focus roots and sort commands distinct', async () => {
  const b = await bindings(), direction = await import('../../../packages/client-core/src/presentation/browse-toolbar');
  const {authorityKey} = await import('../src-next/shell/model');
  const genres = await import('../../../packages/client-core/src/search-genres');
  const sorts = [{id: 'title', labelKey: 'next.browse.native.sort', directions: ['asc', 'desc'], defaultDirection: 'asc'}, {id: 'added', labelKey: 'next.browse.native.sort', directions: ['desc'], defaultDirection: 'desc'}];
  const sort = {field: 'title', direction: 'asc', flippable: true}, toolbar = {sorts, sort, applied: [], quick: []};
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  const r1 = {kind: 'search-genre', label: 'Drama', value: 'Drama'}, r2 = {...r1}; let showing = r2;
  const commands: {route: unknown; sort: unknown}[] = [], collection = {subscribe: () => () => {}, subscribePage: () => () => {}, getSnapshot: (() => {const snapshot = {status: 'ready', total: 0}; return () => snapshot;})(), pageOf: () => 0, getPageVersion: () => 0};
  const register = await screenModule(new URL('../src-next/shell/search-route.tsx', import.meta.url), {
    '@portico/design': {frameForm: () => 'tv'}, '../pages/touch/search/SearchPage': {SearchPage: 'SearchPage'},
    '../screens/search/on-now': {NativeSearchOnNow: class {dispose() {}}},...stackSlotFixture, react: b.react, 'react-native': b.native,
    './route-registry': {nativeShellRoutes: {registerRoute() {}, registerDestination() {}}}, '../screens/destinations/lifetime': {nativeDestinationLifetime: () => ({current: () => true})},
    '../screens/destinations/entry-actions': {createNativeEntryActions: () => ({dispose() {}, menuState: {subscribe: () => () => {}, getSnapshot: () => 0}})},
    '../screens/search/controller': {}, '../screens/search/actions': {}, '../screens/search/explore': {}, '../screens/search/selection': {}, './search-page': {SearchPageFor: 'SearchPageFor'}, '../pages/touch/search/AllGenresPage': {}, '../pages/tv/search/AllGenresPage': {}, './reading-search-route': {},
    '../screens/search/navigation': {createNativeSearchGenreController: (_: any, route: any) => {
      const snapshot = {phase: 'ready', capabilities: {library: {kind: 'movie'}, sorts}, sort, predicates: [], collection, queryRevision: 0};
      return {source: {subscribe: () => () => {}, getSnapshot: () => snapshot, getMemory: () => ({firstVisibleIndex: 0, offset: 0}), contains: () => false,
        setQuery: (_: any, sort: any) => commands.push({route, sort})}, activation: {subscribe: () => () => {}, getSnapshot: (() => {const visible = {visible: true}, hidden = {visible: false}; return () => showing === route ? visible : hidden;})()}, dispose() {}};
    }}, '../pages/tv/search/GenreBrowsePage': {GenreBrowsePage: 'TVGenreBrowsePage'}, '../pages/touch/search/GenreBrowsePage': {GenreBrowsePage: 'GenreBrowsePage'},
    '../kit/focus': {CoveredLayer: 'CoveredLayer'}, './model': {authorityKey}});
  // The TV page's regions and sort come from its hook over the shared genre read.
  const shared = await screenModule(new URL('../src-next/pages/shared/search/useGenreBrowse.ts', import.meta.url), {react: b.react,
    '../../../kit/theme': {useTheme: () => ({isTV: true})},
    '../../../../../../packages/client-core/src/presentation/browse-toolbar': direction, '../../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel: () => toolbar},
    '../../../../../../packages/client-core/src/search-genres': genres, '../../../../../../packages/i18n/src': {defaultI18n}, '../library/useLibrary': {browseFailure: (value: unknown) => value}});
  const {useTVGenreBrowse} = await screenModule(new URL('../src-next/pages/shared/search/useTVGenreBrowse.ts', import.meta.url), {react: b.react, './useGenreBrowse': shared});
  const context = {authority: () => authority, subscribeAuthority: () => () => {}, session: () => ({viewer: {role: 'member'}}),
    playback: {subscribe: () => () => {}, getSnapshot: () => ({dock: {}})}, navigation: {
    subscribe: () => () => {}, retainedRoutes: () => [r1], getSnapshot: () => ({route: showing}), back() {} }};
  let latestReads = 0;
  const controller = register.createNativeSearchModule(context), runtime = {text: {t: defaultI18n.t, locale: 'en-US'}, getSnapshot() {latestReads++; return {authority};}};
  const pages = () => nodes(controller.renderRetained(runtime)).filter(node => typeof node.props?.focusScope === 'string' && node.props?.destination);
  const drivers: Awaited<ReturnType<typeof createFabricHookHost>>[] = [];
  try {
    const elements = pages(), scopes = elements.map(element => element.props.focusScope);
    assert.equal(elements.length, 2); assert.notEqual(elements[0]!.key, elements[1]!.key);
    assert.notEqual(scopes[0], scopes[1], 'same genre value/authority has two distinct actual retained occurrence roots');
    assert.ok(scopes.every(scope => scope.startsWith(`genre:${authorityKey(authority)}:"Drama":`)));
    for (const element of elements) {
      let view: any;
      const driver = createFabricHookHost(() => {view = useTVGenreBrowse(element.props);}); await driver.mount(); drivers.push(driver);
      assert.deepEqual([view.focus.header.id, view.focus.toolbar.id, view.focus.grid.id], [element.props.focusScope, `${element.props.focusScope}.toolbar`, `${element.props.focusScope}.grid`],
        'the page\'s regions are named by its own occurrence');
      view.onSort({field: 'added', direction: 'desc'});
    }
    assert.deepEqual(commands.map(command => command.route), [r1, r2], 'each real sort callback retains its original controller');
    showing = r1;
    // Back to R1: R2 is no longer retained; R1 is drawn again under its original name.
    assert.deepEqual(pages().map(element => element.props.focusScope), [scopes[0]], 'Back/retained visibility never renames the occurrence that stays');
    assert.equal(latestReads, 0, 'scope uses the original registration authority, never a latest runtime lookup');
  } finally {for (const driver of drivers.reverse()) await driver.unmount(); controller.dispose(); await b.dispose();}
});

function actualButtonProps(o: Awaited<ReturnType<typeof overlayBindings>>, props: any) {
  const outer = o.Button.render(props, props.ref ?? null);
  return outer.type.render(outer.props, outer.props.ref).props;
}


for (const kind of ['trusted', 'identity'] as const) for (const closeBy of ['cancel', 'back'] as const) test(`${kind} Forget caller binds safe Cancel and distinct real footer hosts, re-enables after failure, then returns by ${closeBy}`, async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const b = await bindings(), o = await overlayBindings(b);
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'require');
  Object.defineProperty(globalThis, 'require', {configurable: true, writable: true, value: (path: string) => {assert.equal(path, '../../../assets/wordmark.png'); return 1;}});
  let tree: any, reject!: (error: Error) => void, operations = 0, errors = 0;
  const certificate = {address: 'https://original.example', fingerprint: 'sha256:original', serverId: 'original-server'};
  const forgotten: any[] = [], failure = new Promise<void>((_resolve, refuse) => {reject = refuse;});
  const common = {react: b.react, 'react-native': b.native, '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/controls': {Button: o.Button},
    '../../kit/overlays': {...o.surface, OverlaysProvider: o.host.OverlaysProvider}, '../../kit/theme': {useTheme: () => b.theme, nativeType},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ColourBackdrop: 'Backdrop'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../shell/model': {shellLayout: () => ({left: 0, top: 0, width: 1920, height: 1080, scale: 1})},
    'react-native-svg': {default: 'Svg', Circle: 'Circle', Path: 'Path'}};
  const errors_ = {presentError: () => ({body: 'Refused'})};
  const connections = {savedCertificates: async () => [certificate], forgetCapturedCertificate: async (value: any) => {operations++; forgotten.push(value); await failure;}};
  // The trusted certificates page loads its own hook and kit part for real; their relative imports get the same doubles.
  const mod = await screenModule(new URL(kind === 'trusted' ? '../src-next/pages/shared/accounts/TrustedCertificatesPage.tsx' : '../src-next/pages/shared/accounts/ServerIdentityFailurePage.tsx', import.meta.url), {
    ...common, '../../../../../packages/client-core/src/presentation/errors': errors_, '../../platform/server-connections': connections,
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/errors': errors_,
    '../../../platform/server-connections': connections, '../../../kit/controls': {Button: o.Button, FormError: 'FormError'},
    '../../../kit/overlays': {...o.surface, OverlaysProvider: o.host.OverlaysProvider}, '../../../kit/theme': {useTheme: () => b.theme, nativeType},
    '../../../kit/controls/TVAccountRow': {TVAccountRow: 'TVAccountRow'},
    './useTrustedCertificates': kind === 'trusted' ? await screenModule(new URL('../src-next/pages/shared/accounts/useTrustedCertificates.ts', import.meta.url),
      {react: b.react, '../../../platform/server-connections': connections}) : {},
    './useServerIdentityFailure': kind === 'trusted' ? {} : await screenModule(new URL('../src-next/pages/shared/accounts/useServerIdentityFailure.ts', import.meta.url), {react: b.react}),
    '../../../kit/Icon': {Icon: 'Icon'}, '../../../shell/model': {shellLayout: () => ({left: 0, top: 0, width: 1920, height: 1080, scale: 1})}});
  // The page draws through its own kit parts; they are expanded in place (they hold no hooks of their own beyond the
  // theme) so the real Buttons and Dialog stay where the focus bindings look for them.
  const kitParts = new Set(['TrustedCertificatesGroup', 'TrustedCertificateRow', 'ForgetCertificateDialog', 'ServerIdentityFailureView']);
  const expandParts = (node: any): any => Array.isArray(node) ? node.map(expandParts)
    : typeof node?.type === 'function' && kitParts.has(node.type.name) ? expandParts(node.type(node.props))
    : node?.props ? {...node, props: {...node.props, children: expandParts(node.props.children), footer: expandParts(node.props.footer)}} : node;
  const source = createFabricHookHost(() => {tree = kind === 'trusted' ? expandParts(mod.TrustedCertificatesPage({})) : expandParts(mod.ServerIdentityFailurePage({
    failure: {changed: true, server: {name: 'Original server'}}, onRetry() {}, onChoose() {}, onForget: async () => {operations++; await failure;}, onError: () => {errors++;}}));});
  let renderer: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    await source.mount(); await flush();
    const openerNode = () => nodes(tree).find(node => node.type === o.Button && (kind === 'trusted'
      ? node.props.children === defaultI18n.t('connection.certificates.forget')
      : node.props.children === defaultI18n.t('connection.identity.forget', {server: 'Original server'})));
    // The outer account focus receipt is a controlled native boundary. The identity
    // caller additionally supplies its actual captured returnFocusRef.
    await b.region(() => ({id: 'original.account.page', order: ['original-opener']}));
    const original = await b.target('original.account.page', () => ({focusId: 'original-opener'})); original.props.onFocus({});
    const opener = await b.target(undefined as any, () => actualButtonProps(o, openerNode().props));
    opener.props.onPress(); await flush();
    const dialog = () => nodes(tree).find(node => node.type === o.surface.Dialog).props;
    const expected = kind === 'trusted' ? 'tv-trusted-certificate-forget' : 'identity-forget';
    // The TV confirmation's title keeps its own 48/800 line, now from the design scale.
    if (kind !== 'trusted') assert.deepEqual(dialog().titleType, {size: 48, lineHeight: 'normal', weight: 800, tracking: -.035});
    assert.equal(dialog().testID, expected);
    let overlay: any;
    renderer = createFabricHookHost(() => {const node = o.surface.Dialog(dialog()); overlay = node.type(node.props);}); await renderer.mount();
    const boundary = () => {const node = nodes(overlay).find(node => node.type === o.surface.FocusBoundary); return node.type(node.props);};
    const guide = await b.region(() => boundary().props);
    const footer = (id: string) => nodes(dialog().footer).find(node => node.type === o.Button && node.props.focusId === id).props;
    const cancel = await b.target(expected, () => actualButtonProps(o, footer('cancel'))), forget = await b.target(expected, () => actualButtonProps(o, footer('forget')));
    const refresh = async () => {
      await source.rerender(); await renderer!.rerender();
      if (!overlay) {await cancel.dispose(); await forget.dispose(); await guide.dispose(); return;}
      await guide.refresh(); await cancel.refresh(); await forget.refresh();
    };
    assert.deepEqual(nodes(dialog().footer).filter(node => node.type === o.Button).map(node => node.props.focusId), kind === 'trusted' ? ['cancel', 'forget'] : ['forget', 'cancel']);
    assert.equal(cancel.props.testID, registryModule.focusTestID(expected, 'cancel')); assert.equal(forget.props.testID, registryModule.focusTestID(expected, 'forget'));
    t.mock.timers.tick(180); assert.equal(cancel.calls, 1, 'actual Dialog initial ref enters safe Cancel'); assert.equal(forget.calls, 0); assert.equal(operations, 0);
    const refs = [cancel.props.ref, forget.props.ref];
    forget.props.onPress(); await refresh();
    assert.equal(operations, 1); assert.equal(cancel.props.disabled, true); assert.equal(forget.props.disabled, true);
    assert.equal(cancel.props.onPress, undefined); assert.equal(forget.props.onPress, undefined);
    assert.equal([...o.back][0]!(), true); await refresh(); assert.equal(dialog().open, true, 'original busy Back refusal is preserved');
    reject(new Error('original operation refused')); await flush(); await refresh();
    assert.equal(dialog().open, true); assert.equal(!!cancel.props.disabled, false); assert.equal(!!forget.props.disabled, false);
    assert.equal(typeof cancel.props.onPress, 'function'); assert.equal(typeof forget.props.onPress, 'function');
    assert.equal(cancel.props.ref, refs[0]); assert.equal(forget.props.ref, refs[1]);
    assert.equal(b.registry.child({regionId: expected, childId: 'cancel'}), cancel.host); assert.equal(b.registry.child({regionId: expected, childId: 'forget'}), forget.host);
    assert.equal(operations, 1, 'failure/re-enable never invokes another command');
    if (kind === 'trusted') {assert.deepEqual(forgotten, [certificate]); assert.notEqual(forgotten[0], certificate); assert.equal(Object.isFrozen(forgotten[0]), true);}
    else assert.equal(errors, 1);
    if (closeBy === 'cancel') cancel.props.onPress(); else assert.equal([...o.back][0]!(), true);
    await refresh(); assert.equal(dialog().open, false); t.mock.timers.tick(500); await refresh();
    assert.equal(overlay, null); assert.equal(b.registry.child({regionId: expected, childId: 'cancel'}), undefined);
    assert.equal(b.registry.child({regionId: expected, childId: 'forget'}), undefined);
    assert.equal(kind === 'identity' ? opener.calls : original.calls, 1, 'actual Dialog lifecycle restores its original explicit ref or captured registry receipt');
    assert.equal(operations, 1);
  } finally {
    await renderer?.unmount(); await source.unmount(); await b.dispose(); t.mock.timers.reset();
    if (descriptor) Object.defineProperty(globalThis, 'require', descriptor); else delete (globalThis as any).require;
  }
});

for (const kind of ['sort', 'filter'] as const) test(`actual Saved directory supplies retained toolbar scopes to the real ${kind} Popover and keyed rows`, async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const b = await bindings(), o = await overlayBindings(b), direction = await import('../../../packages/client-core/src/presentation/browse-toolbar');
  const presentation = await import('../src-next/kit/browse/presentation'), savedText = await import('../src-next/screens/saved/presentation');
  const react: any = {...b.react, useImperativeHandle() {}}; react.default = react; // Scroll-page handle is outside this caller-ID vector.
  const toolbar = await screenModule(new URL('../src-next/kit/saved/PlaylistToolbar.tsx', import.meta.url), {
    react, 'react-native': b.native, '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/browse-toolbar': direction,
    '../controls': {Focusable: b.Focusable, Segmented: 'Segmented'}, '../overlays': o.menu, '../Icon': {Icon: 'Icon'},
    '../browse/presentation': presentation, '../theme': {useTheme: () => b.theme}});
  const {SavedTVDirectory} = await tvSavedDirectoryFixture({
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Saved failed'})},
    '../../kit/saved/SavedForms': {SavedNameSheet: 'SavedNameSheet'}, './SavedTVLoading': {SavedTVLoading: 'SavedTVLoading'},
    react, 'react-native': b.native, '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/controls': {Focusable: b.Focusable},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, '../../kit/cards/primitives': {}, '../../kit/focus': b.focus, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states': {StateView: 'State'},
    '../../kit/browse/presentation': presentation, '../../kit/theme': {useTheme: () => b.theme}, './PlaylistToolbar': toolbar, './presentation': savedText});
  const snapshot = {phase: 'ready', saved: {projection: {sections: [], query: {sort: 'title', direction: 'asc', category: 'all'},
    sorts: [{id: 'title', directions: ['asc', 'desc']}], filters: [{id: 'ownership', options: [{id: 'all', count: 4}]}, {id: 'visibility', options: [{id: 'all', count: 3}]}]}, pagination: {next: []}}};
  const routes: any[][] = [[], []], trees: any[] = [], tools: any[] = [], source: Awaited<ReturnType<typeof createFabricHookHost>>[] = [], drivers: Awaited<ReturnType<typeof createFabricHookHost>>[] = [];
  const destinations = [0, 1].map(() => ({subscribe: () => () => {}, getSnapshot: () => snapshot}));
  const selects = [0, 1].map(index => (value: any) => routes[index]!.push(value));
  let enabled = true, overlay: any, renderer: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  const directoryProps = (index: number) => ({view: 'playlists', destination: destinations[index], runtime: {}, libraries: [], enabled: index ? true : enabled,
    activation: {subscribe: () => () => {}, getSnapshot: (() => {const visible = {visible: true, revision: 1, intent: 'enter'}, hidden = {...visible, visible: false}; return () => index || enabled ? visible : hidden;})()}, regionId: `saved.original-${index}`, select: selects[index], openPlaylist() {}, openResource() {}});
  try {
    for (const index of [0, 1]) {
      source[index] = createFabricHookHost(() => {trees[index] = SavedTVDirectory(directoryProps(index));}); await source[index]!.mount();
      drivers[index] = createFabricHookHost(() => {const node = nodes(trees[index]).find(node => node.type === toolbar.PlaylistToolbar); tools[index] = node.type(node.props);}); await drivers[index]!.mount();
    }
    const menu = (index = 0) => nodes(tools[index]).find(node => node.type === o.menu.Popover && node.props.testID.endsWith(`.${kind}-menu`)).props;
    assert.equal(menu().testID, `saved.original-0.toolbar.${kind}-menu`); assert.equal(menu(1).testID, `saved.original-1.toolbar.${kind}-menu`);
    const declared = () => nodes(trees[0]).find(node => node.type === b.focus.FocusRegion && node.props.id === 'saved.original-0.toolbar');
    const parent = await b.region(() => declared().props), openerNode = () => nodes(tools[0]).find(node => node.type === b.Focusable && node.props.focusId === `tv-saved-${kind}`);
    const opener = await b.target('saved.original-0.toolbar', () => openerNode().props), originalRef = opener.props.ref;
    (opener.host as any).measureInWindow = (callback: any) => callback(10, 10, 100, 50);
    enabled = false; await source[0]!.rerender(); await drivers[0]!.rerender(); await parent.refresh(); await opener.refresh();
    assert.equal(opener.props.disabled, true); assert.equal(opener.props.onPress, undefined); assert.deepEqual(routes, [[], []]);
    enabled = true; await source[0]!.rerender(); await drivers[0]!.rerender(); await parent.refresh(); await opener.refresh();
    assert.equal(opener.props.ref, originalRef); opener.props.onFocus({}); opener.props.onPress(); await drivers[0]!.rerender();
    renderer = createFabricHookHost(() => {overlay = tvPopover(o, menu());}); await renderer.mount(); await renderer.flush();
    const boundary = nodes(overlay).find(node => node.type === o.surface.FocusBoundary), declaration = boundary.type(boundary.props);
    const guide = await b.region(() => declaration.props), rows = nodes(menu().children).filter(node => node.type === o.menu.MenuRow);
    const targets = await mountInOrder(rows, row => b.target(menu().testID, () => row.type.render(row.props, row.props.ref).props));
    assert.equal(guide.guide.props.testID, `portico.region.${encodeURIComponent(menu().testID)}`);
    assert.equal(b.registry.entryTarget(menu().testID), targets[0]!.host, 'the actual guide entry resolves to its first real row');
    assert.equal(new Set(targets.map(target => target.props.testID)).size, rows.length);
    if (kind === 'filter') {
      assert.deepEqual(rows.map(row => row.props.focusId), ['tv-saved-show:ownership:all', 'tv-saved-show:visibility:all']);
      targets[1]!.props.onPress(); assert.deepEqual(routes[0], [{view: 'playlists', filter: 'all'}]);
    } else {
      assert.deepEqual(rows.map(row => row.props.focusId), ['tv-saved-sort:title']);
      const segmented = nodes(menu().children).find(node => node.type === 'Segmented');
      assert.deepEqual(['asc', 'desc'].map(value => segmented.props.itemProps({value}).focusId), ['tv-saved-order:asc', 'tv-saved-order:desc']);
      assert.equal([...o.back][0]!(), true); assert.deepEqual(routes[0], []);
    }
    assert.deepEqual(routes[1], [], 'the other retained directory receives no command');
    await drivers[0]!.rerender(); await renderer.rerender(); t.mock.timers.tick(500); await renderer.rerender();
    assert.equal(opener.calls, 1, 'actual Popover lifecycle restores the original directory chip');
    for (const target of targets) await target.dispose(); await guide.dispose();
  } finally {await renderer?.unmount(); for (const driver of drivers) await driver.unmount(); for (const driver of source) await driver.unmount(); await b.dispose(); t.mock.timers.reset();}
});

test('actual Lyrics read-error Retry keeps its original prefix, native host and reader command through snapshot changes', async () => {
  const b = await bindings(), o = await overlayBindings(b), lyrics = await import('../../../packages/client-core/src/lyrics');
  const committed = await screenModule(new URL('../src-next/kit/overlays/CommittedPaint.tsx', import.meta.url), {react: b.react, './OverlayPaintContext': b.paint});
  const model = await screenModule(new URL('../src-next/pages/shared/player/useNowPlayingLyrics.ts', import.meta.url), {
    '../../../../../../packages/client-core/src/timer': await import('../../../packages/client-core/src/timer.ts'),
    react: b.react, '../../../../../../packages/client-core/src/lyrics': lyrics, '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/overlays/CommittedPaint': committed});
  const {ListeningLyricsPaint} = await screenModule(new URL('../src-next/kit/player/ListeningPanePaint.tsx', import.meta.url), {
    react: b.react, 'react-native': b.native, '../../../../../packages/i18n/src': {defaultI18n}, '../overlays/CommittedPaint': committed,
    '../../../../../packages/client-core/src/presentation/kit-model/text': await import('../../../packages/client-core/src/presentation/kit-model/text'),
    '../artwork': {Artwork: 'Artwork'}, '../browse/presentation': {browseType: () => ({})},
    '../controls': {Button: o.Button, Focusable: b.Focusable}, '../theme': {useTheme: () => b.theme},
    './QueueRow': {QueueRow: 'QueueRow', QueueGroupLabel: 'Text'}});
  const {NowPlayingLyrics} = await screenModule(new URL('../src-next/kit/player/NowPlayingLyrics.tsx', import.meta.url), {
    react: b.react, '../../pages/shared/player/useNowPlayingLyrics': model, './ListeningPanePaint': {ListeningLyricsPaint}});
  let snapshot: any = {data: null, candidates: [], warnings: [], preview: null, busy: false, error: 'Read refused', generation: 1}, tree: any, reads = 0, refreshes = 0, disposed = 0;
  const original = {subscribe: () => () => {}, getSnapshot: () => snapshot, async refresh() {refreshes++;}, dispose() {disposed++;}};
  const target = Object.freeze({libraryId: 'music', itemId: 'original-song', sessionId: 'original-session', sessionGeneration: 1, sourceId: 'original-source'});
  const read = (received: unknown) => {assert.equal(received, target); reads++; return {enabled: true, service: original};};
  const source = createFabricHookHost(() => {
    const pane = NowPlayingLyrics({read, target, positionSeconds: 0, playing: false, onSeek() {assert.fail('Retry never seeks');}, testID: 'original-lyrics'});
    tree = ListeningLyricsPaint(pane.props);
  }); await source.mount();
  const region = await b.region(() => ({id: 'original.lyrics.panel'})), button = () => nodes(tree).find(node => node.type === o.Button);
  const retry = await b.target('original.lyrics.panel', () => actualButtonProps(o, button().props));
  try {
    assert.equal(reads, 1); assert.equal(refreshes, 1, 'existing mount read is unchanged');
    assert.equal(retry.props.testID, 'original-lyrics-retry'); assert.equal(b.registry.child({regionId: 'original.lyrics.panel', childId: 'retry'}), retry.host);
    const ref = retry.props.ref;
    for (const busy of [true, false]) {snapshot = {...snapshot, busy}; await source.rerender(); await region.refresh(); await retry.refresh(); assert.equal(retry.props.ref, ref); assert.equal(reads, 1); assert.equal(refreshes, 1);}
    b.paint.OverlayPaintModeContext.current = b.paint.OVERLAY_PAINT_INERT; await retry.refresh();
    assert.equal(retry.props.onPress, undefined); assert.equal(b.registry.child({regionId: 'original.lyrics.panel', childId: 'retry'}), undefined);
    b.paint.OverlayPaintModeContext.current = b.paint.OVERLAY_PAINT_LIVE; await retry.refresh(); retry.props.onPress();
    assert.equal(refreshes, 2); assert.equal(reads, 1, 'explicit Retry keeps the original reader');
    await source.unmount(); assert.equal(disposed, 1);
  } finally {await source.unmount(); await b.dispose();}
});

export type LibraryFooterBindings = Readonly<{bindings: typeof bindings; overlayBindings: typeof overlayBindings; actualButtonProps: typeof actualButtonProps}>;
registerLibrarySelectionFooterTests({bindings, overlayBindings, actualButtonProps});
