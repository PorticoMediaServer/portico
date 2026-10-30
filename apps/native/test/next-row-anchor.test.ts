// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {pathToFileURL} from 'node:url';
import {readFile} from 'node:fs/promises';
import {compileFunction} from 'node:vm';
import {createRequire} from 'node:module';
import {reducedMotion} from '@portico/design';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import * as rowState from '../src-next/kit/layout/row-state';
import * as layoutModel from '../src-next/kit/layout/model';
import * as registryModule from '../src-next/kit/focus/registry';
import * as hostRefModule from '../src-next/kit/focus/useHostRef';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter';

// Run the same component/worklet tests against git-show baseline files for red evidence.
const source = (name: string) => process.env.PORTICO_ROW_ANCHOR_BASE
  ? pathToFileURL(`${process.env.PORTICO_ROW_ANCHOR_BASE}/${name}`)
  : new URL(`../src-next/kit/layout/${name}`, import.meta.url);
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : React.isValidElement<any>(node) ? [node, ...nodes(node.props.children)] : [];
type Animation = {to: number; config: any; cell?: any; cancelled?: boolean};
type Item = {id: string; title?: string};
const key = (item: Item) => item.id;
const require = createRequire(import.meta.url);
async function anchorCode(file: URL, modules: Record<string, any>) {
  const {code} = require('@babel/core').transformSync(await readFile(file, 'utf8'), {filename: file.pathname, configFile: false, babelrc: false,
    presets: [require.resolve('@react-native/babel-preset')], plugins: [require.resolve('@babel/plugin-transform-modules-commonjs')]});
  const module = {exports: {}};
  compileFunction(code, ['require', 'exports', 'module'], {filename: file.pathname})((name: string) => name in modules ? {__esModule: true, ...modules[name]} : require(name), module.exports, module);
  return module.exports as any;
}

/** Real Row, FocusAnchor, native target bindings and scroll callbacks; controlled UI frames only. */
async function rig({count = 3, tv = true, reduced = false, initialWidth = 1920}: {count?: number; tv?: boolean; reduced?: boolean; initialWidth?: number} = {}) {
  let theme = createTheme({isTV: tv, width: 1920, reduceMotion: reduced});
  const animations: Animation[] = [], commands: number[] = [], requests: string[] = [], focused: string[] = [], ranges: string[][] = [];
  const reactions: any[] = [], listeners = new Set<(event: any) => void>(), js: (() => void)[] = [];
  const registry = registryModule.createFocusRegistry(), contexts: any[] = [], targets = new Map<string, any>();
  registry.attachRegion('rail', {nativeTag: 700, requestTVFocus() {requests.push('rail');}} as any);
  let tree: any, guide: any, offset = 0, renders = 0, nextTag = 1, closed = false;
  let motionPolicy: {reduce: boolean; value: any} | undefined;
  const useSharedValue = (initial: any) => {
    const cell = React.useRef<any>(null);
    if (!cell.current) {
      let value = initial, animation: Animation | undefined;
      cell.current = {get value() {return value;}, set value(next: any) {
        if (animation) animation.cancelled = true;
        animation = undefined;
        if (next && typeof next === 'object' && 'to' in next) {animation = next; next.cell = cell.current;}
        else value = next;
      }, sample(next: number) {value = next;}, cancel() {if (animation) animation.cancelled = true; animation = undefined;}};
    }
    return cell.current;
  };
  const scroll = () => nodes(tree).find(node => node.type === 'ScrollView');
  const extent = () => scroll().props.children.props.style.width;
  const width = () => measuredWidth;
  let measuredWidth = initialWidth;
  const animated = {default: {ScrollView: 'ScrollView'}, useSharedValue,
    useAnimatedStyle: (fn: () => unknown) => fn(),
    useAnimatedRef: () => React.useRef({nativeTag: 900}), useAnimatedScrollHandler: (fn: any) => fn,
    useAnimatedReaction(read: any, callback: any) {
      const ref = React.useRef<any>({previous: null}); ref.current.read = read; ref.current.callback = callback;
      React.useEffect(() => {const reaction = ref.current; reactions.push(reaction); return () => {reactions.splice(reactions.indexOf(reaction), 1);};}, []);
    },
    runOnJS: (fn: any) => (...args: any[]) => {js.push(() => fn(...args));},
    cancelAnimation: (cell: any) => cell.cancel(),
    withTiming(to: number, config: any) {const animation = {to, config}; animations.push(animation); return animation;},
    scrollTo(_ref: any, x: number, y: number, smooth: boolean) {
      assert.equal(y, 0); assert.equal(smooth, false, 'Reanimated owns timing, never a native smooth-scroll queue');
      commands.push(x); offset = Math.max(0, Math.min(Math.max(0, extent() - width()), x));
      scroll().props.onScroll({contentOffset: {x: offset, y: 0}});
    },
  };
  const react = {...React, default: React};
  const focusReact = {...react, createContext(value: any) {const context = {current: value}; contexts.push(context); return context;}, useContext: (context: any) => context.current};
  const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react: focusReact, 'react-native': {View: 'View', TVFocusGuideView: 'TVFocusGuideView', findNodeHandle: (host: any) => host.nativeTag},
    '../theme': {useTheme: () => theme}, './registry': registryModule, './useHostRef': hostRefModule,
  });
  contexts[0].current = registry;
  const motion = () => {
    // The real useMotion memoizes one policy per reduction state, and under
    // reduced motion it publishes the 160 ms linear envelope for opacity only.
    const reduce = theme.reduceMotion;
    if (motionPolicy?.reduce !== reduce) motionPolicy = {reduce, value: {
      reduce, transforms: !reduce,
      timing: (name: keyof typeof theme.motion = 'standard', curve = 'out') =>
        ({duration: reduce ? reducedMotion.durationMs : theme.motion[name], curve: reduce ? 'linear' : curve}),
    }};
    return motionPolicy.value;
  };
  const nativeKeys = {useTVEventHandler(callback: any) {React.useEffect(() => {listeners.add(callback); return () => {listeners.delete(callback);};}, [callback]);}};
  const keyModule = await screenModule(new URL('../src-next/platform/tv-keys.ts', import.meta.url), {react, 'react-native': nativeKeys,
    './tv-key-adapter': {createTVKeyAdapter}, './tv-key-source': {tvKeySource: 'native'}});
  const anchorModule = await anchorCode(source('FocusAnchor.ts'), {react,
    'react-native': nativeKeys, '../../platform/tv-keys': keyModule,
    'react-native-reanimated': animated, '../theme': {useTheme: () => theme}, '../useMotion': {useMotion: motion}, './model': layoutModel,
  });
  const {Row} = await screenModule(source('Row.tsx'), {react, 'react-native': {View: 'View', Text: 'Text', Pressable: 'Pressable'},
    'react-native-reanimated': animated, 'react-native-svg': {}, '../../../../../packages/i18n/src': {defaultI18n},
    '../Icon': {}, '../theme': {useTheme: () => theme}, '../focus': {...focus, focusTestID: registryModule.focusTestID}, '../focus/page-boundary': {useRowPageBoundary() {}},
    './model': layoutModel, './row-state': rowState, './FocusAnchor': anchorModule, './MotionItem': {MotionItem: 'MotionItem'}, './types': {itemSelection: () => undefined},
  });
  let props: any = {title: 'Continue', items: Array.from({length: count}, (_, index) => ({id: `item-${index}`})), getKey: key,
    renderItem: (item: Item, context: any) => React.createElement('Card', {item, context}), itemWidth: 400, itemHeight: 225, gap: 28,
    gutter: 200, rightGutter: 96, initialViewportWidth: initialWidth, focus: {id: 'row', edges: {left: 'rail'}},
    onItemFocus: (item: Item) => {focused.push(item.id);}, onVisibleKeys: (keys: string[]) => {ranges.push([...keys]);}};
  const host = createFabricHookHost(() => {renders++; const content = Row(props).props.children; tree = content.type(content.props);});
  const guideHost = createFabricHookHost(() => {
    if (!tv) {guide = {props: {ref() {}}}; return;}
    const region = nodes(tree).find(node => node.type?.name === 'RowRegion');
    const outer = region.type(region.props), inner = outer.type.render(outer.props, null);
    guide = inner.type.render(inner.props, null);
  });
  async function syncTargets() {
    const cards = nodes(tree).filter(node => node.type === focus.FocusItem);
    for (const [id, target] of targets) if (!cards.some(node => node.props.focusId === id)) {target.bound.ref(null); await target.host.unmount(); targets.delete(id);}
    for (const card of cards) {
      const id = card.props.focusId;
      let target = targets.get(id);
      if (!target) {
        target = {card, native: {nativeTag: nextTag++, requestTVFocus() {requests.push(id);}}, bound: undefined};
        const current = target;
        current.host = createFabricHookHost(() => {contexts[1].current = 'row'; contexts[2].current = current.card.props; current.bound = focus.useFocusTarget({});});
        targets.set(id, current); await current.host.mount(); current.bound.ref(current.native);
      } else {target.card = card; await target.host.rerender();}
    }
  }
  async function settle() {
    for (let pass = 0; pass < 12; pass++) {
      const before = renders;
      await act(() => {while (js.length) js.shift()!();});
      await guideHost.rerender(); await syncTargets();
      await act(() => {});
      if (renders === before) return;
    }
    throw new Error('Row effects did not settle');
  }
  function frame() {
    for (const reaction of reactions) {const next = reaction.read(); reaction.callback(next, reaction.previous); reaction.previous = next;}
  }
  await host.mount(); await guideHost.mount(); guide.props.ref({nativeTag: 901, requestTVFocus() {requests.push('guide');}, setDestinations() {}}); await syncTargets(); await settle();
  return {
    animations, commands, requests, focused, ranges, targets, registry,
    get tree() {return tree;}, get guide() {return guide;}, get offset() {return offset;}, get extent() {return extent();}, get renders() {return renders;},
    position(id: string) {const card = nodes(tree).find(node => node.type === 'MotionItem' && node.props.children.props.focusId === id); return card?.props.style.left - offset;},
    async focus(id: string) {
      const target = targets.get(id); assert.ok(target, `${id} must be mounted for native focus`);
      const previous = registry.capture()?.childId;
      if (previous && previous !== id && targets.has(previous)) {targets.get(previous).bound.onBlur(); targets.get(previous).card.props.children.props.context.onBlur();}
      target.bound.onFocus(); target.card.props.children.props.context.onFocus(); await settle();
    },
    async blur() {const id = registry.capture()?.childId; if (id && targets.has(id)) {targets.get(id).bound.onBlur(); targets.get(id).card.props.children.props.context.onBlur();} await settle();},
    key(type: string, action = 0) {for (const listener of [...listeners]) listener({eventType: type, eventKeyAction: action});},
    async finish() {for (const animation of animations) if (!animation.cancelled && animation.cell) animation.cell.sample(animation.to); frame(); await settle();},
    async sample(value: number) {const animation = animations.filter(candidate => !candidate.cancelled && candidate.cell).at(-1); assert.ok(animation); animation.cell.sample(value); frame(); await settle();},
    async layout(value: number) {measuredWidth = value; scroll().props.onLayout({nativeEvent: {layout: {width: value}}}); await settle();},
    async update(next: any) {props = {...props, ...next}; await host.rerender(); await settle();},
    async reduce() {theme = {...theme, reduceMotion: true}; await host.rerender(); await settle(); frame(); await settle();},
    async drag() {scroll().props.onScrollBeginDrag(); frame(); await settle();},
    async scroll(value: number) {offset = value; scroll().props.onScroll({contentOffset: {x: value, y: 0}}); await settle();},
    frame,
    async close() {if (closed) return; closed = true; for (const target of targets.values()) {target.bound.ref(null); await target.host.unmount();} await guideHost.unmount(); await host.unmount();},
  };
}

for (const count of [1, 3, 12]) test(`actual TV Row of ${count}: first/middle/last native focus anchors at the left safe edge`, async () => {
  const row = await rig({count});
  try {
    assert.ok(row.extent >= 1920 + (count - 1) * 428, 'the last actual card has enough nonfocusable trailing scroll extent');
    for (let index = 0; index < count; index++) {
      const id = `item-${index}`; await row.focus(id); await row.finish();
      assert.equal(row.position(id), 200, `${id} fixed drawn slot`); assert.equal(row.offset, index * 428);
      assert.equal(row.registry.capture()?.childId, id); assert.equal(row.registry.remembered('row'), id);
      assert.equal(row.targets.get(id).bound.testID, registryModule.focusTestID('row', id));
      assert.equal(row.targets.get(id).bound.nextFocusLeft, index === 0 ? 700 : undefined, 'native rail edge applies only to the genuine first card');
      assert.equal(row.targets.get(id).bound.nextFocusRight, undefined, 'native search owns every intra-row Right target');
    }
    assert.deepEqual(row.requests, [], 'scrolling never requests focus on a different native host');
    assert.equal(row.registry.order('row').length, count, 'blank scroll extent creates no extra focus targets');
    assert.equal(row.guide.props.trapFocusRight, true); assert.equal(row.guide.props.onKeyDown, undefined);
    const before = row.animations.length; row.key('right'); row.key('right', 1); await row.finish();
    assert.equal(row.animations.length, before, 'Right at the true end without a native focus change creates no movement');
    for (let index = count - 2; index >= 0; index--) {
      await row.focus(`item-${index}`); await row.finish(); assert.equal(row.position(`item-${index}`), 200);
    }
  } finally {await row.close();}
});

test('native onLayout and focus flush the actual component without a manual parent render', async () => {
  const row = await rig({initialWidth: 0});
  try {await row.focus('item-1'); assert.equal(row.animations.length, 0); await row.layout(1920); await row.finish(); assert.equal(row.position('item-1'), 200);}
  finally {await row.close();}
});

test('actual row cancels/retargets from the sampled scroll offset; repeats use slow linear and release restores in-out', async () => {
  const row = await rig();
  try {
    await row.focus('item-0'); row.key('right'); await row.focus('item-1');
    assert.deepEqual(row.animations.at(-1)?.config, {duration: 320, curve: 'inOut'});
    await row.sample(120); const previous = row.animations.at(-1)!;
    row.key('longRight'); await row.focus('item-2'); assert.equal(previous.cancelled, true);
    assert.deepEqual(row.animations.at(-1)?.config, {duration: 320, curve: 'linear'});
    row.frame(); assert.equal(row.commands.at(-1), 120, 'a retarget starts at actual onScroll position');
    await row.finish(); assert.equal(row.position('item-2'), 200);
    row.key('longRight', 1); row.key('left'); await row.focus('item-1');
    assert.deepEqual(row.animations.at(-1)?.config, {duration: 320, curve: 'inOut'});
    await row.finish(); assert.deepEqual(row.requests, []);
  } finally {await row.close();}
});

test('release-only short Right reaches useFocusAnchor and anchors the native focus target', async () => {
  const row = await rig();
  try {
    await row.focus('item-0');
    row.key('right', 1); await row.focus('item-1');
    assert.deepEqual(row.animations.at(-1)?.config, {duration: 320, curve: 'inOut'});
    await row.finish(); assert.equal(row.position('item-1'), 200); assert.equal(row.offset, 428);
    row.key('right', 1); await row.focus('item-2');
    assert.deepEqual(row.animations.at(-1)?.config, {duration: 320, curve: 'linear'}, 'the second release-only tap reaches the anchor repeat policy');
    await row.finish(); assert.equal(row.position('item-2'), 200); assert.equal(row.offset, 856);
    assert.deepEqual(row.requests, [], 'native focus owns the target; the hook owns only its scroll');
  } finally {await row.close();}
});

test('reduced motion settles the fixed slot immediately, including a preference change mid-scroll', async () => {
  const row = await rig({reduced: true});
  try {
    await row.focus('item-2'); await row.finish();
    // FX-082 / RV-MOB-023: the slot jumps; only its surface takes the 160 ms fade.
    assert.equal(row.offset, 856);
    assert.deepEqual(row.animations.map(animation => animation.config),
      [{duration: reducedMotion.durationMs, curve: 'linear'}], 'reduced motion fades the surface rather than animating the slot');
  }
  finally {await row.close();}
  const changing = await rig();
  try {
    await changing.focus('item-1'); await changing.sample(100); await changing.reduce();
    assert.equal(changing.offset, 428);
    const envelope = changing.animations.at(-1)!;
    assert.equal(changing.animations.at(-2)?.cancelled, true, 'the mid-scroll transform is cancelled');
    assert.deepEqual(envelope.config, {duration: reducedMotion.durationMs, curve: 'linear'});
  }
  finally {await changing.close();}
});

test('native drag, blur and unmount cancel outstanding scroll ownership; remembered re-entry finishes its slot', async () => {
  const row = await rig();
  try {
    await row.focus('item-1'); await row.sample(100); await row.drag(); const commands = row.commands.length;
    await row.finish(); assert.equal(row.commands.length, commands); assert.equal(row.offset, 100);
    await row.blur(); await row.focus('item-1'); await row.finish(); assert.equal(row.offset, 428);
    await row.focus('item-2'); await row.sample(500); await row.blur(); assert.equal(row.animations.at(-1)?.cancelled, true);
    await row.focus('item-2'); const animation = row.animations.at(-1)!; await row.close(); assert.equal(animation.cancelled, true);
  } finally {await row.close();}
});

test('genuine updates/removals retain focused geometry and native refs; paging fills the same cursor host', async () => {
  const row = await rig();
  try {
    await row.focus('item-1'); await row.finish();
    const ref = row.targets.get('item-1').bound.ref, native = row.targets.get('item-1').native, animations = row.animations.length;
    await row.update({items: [{id: 'item-2'}, {id: 'item-1', title: 'updated'}, {id: 'item-0'}, {id: 'item-3'}]});
    await row.update({items: [{id: 'item-1', title: 'updated again'}, {id: 'item-3'}]});
    assert.equal(row.position('item-1'), 200); assert.equal(row.animations.length, animations, 'content alone does not retarget a slot');
    assert.equal(row.targets.get('item-1').bound.ref, ref); assert.equal(row.targets.get('item-1').native, native);
    assert.equal(row.registry.child({regionId: 'row', childId: 'item-1'}), native); assert.deepEqual(row.requests, []);
    await row.update({items: [{id: 'item-3'}]});
    assert.deepEqual(row.requests, ['item-3'], 'only genuine native host removal invokes existing registry recovery');
    await row.focus('item-3'); await row.finish(); assert.equal(row.position('item-3'), 200);
  } finally {await row.close();}
  const paging = await rig({count: 2});
  try {
    const page = {waitingId: 'cursor-a', waitingItem: {id: 'placeholder'}, hasMore: true, loading: true, requestNext() {}};
    await paging.update({paging: page}); await paging.focus('cursor-a'); await paging.finish();
    const target = paging.targets.get('cursor-a'), ref = target.bound.ref, before = paging.animations.length;
    await paging.update({items: [{id: 'item-0'}, {id: 'item-1'}, {id: 'item-2'}, {id: 'item-3'}],
      paging: {...page, waitingId: 'cursor-b', fulfilled: {waitingId: 'cursor-a', firstItemId: 'item-2'}}});
    assert.equal(paging.targets.get('cursor-a').native, target.native); assert.equal(paging.targets.get('cursor-a').bound.ref, ref);
    assert.equal(paging.targets.get('cursor-a').card.props.waiting, false); assert.equal(paging.position('cursor-a'), 200);
    assert.equal(paging.animations.length, before); assert.equal(paging.registry.capture()?.childId, 'cursor-a'); assert.deepEqual(paging.requests, []);
    assert.ok(paging.ranges.at(-1)?.includes('item-2')); assert.ok(!paging.ranges.at(-1)?.includes('cursor-b'));
  } finally {await paging.close();}
});

test('actual scroll worklet publishes intersecting keys only; overscan/focus pins do not enter visible admission', async () => {
  const row = await rig({count: 80});
  try {
    await row.focus('item-0'); await row.scroll(20 * 428); assert.ok(row.targets.has('item-0'), 'actual focused host is pinned');
    const keys = row.ranges.at(-1)!; assert.ok(keys.length <= 6 && keys.length > 0); assert.ok(!keys.includes('item-0')); assert.ok(!keys.includes('item-18'));
    assert.ok(row.targets.size < 16); assert.ok(keys.length <= 32); assert.equal(row.registry.capture()?.childId, 'item-0');
  } finally {await row.close();}
});

test('phone retains natural extent and visibility-only focus scrolling', async () => {
  const row = await rig({tv: false});
  try {assert.equal(row.extent, 1552); await row.focus('item-2'); await row.finish(); assert.equal(row.offset, 0); assert.equal(row.position('item-2'), 1056);}
  finally {await row.close();}
});
