/**
 * Component lifecycle source for the approved non-gating LoadingPaint.
 *
 * This drives the real presenter through the installed Fabric renderer
 * (`helpers/fabric-host.mjs`), so React itself owns commits, effect ordering,
 * keyed remount identity and unmount. Only the native host, the animation
 * driver and a controlled timer queue are replaced, so the presenter's own
 * hooks, effects and cleanup order run unchanged.
 *
 * It makes no claim about native layout or pixels: host nodes here are React's
 * committed native hosts, not measured views.
 */
import test, {after} from 'node:test';
import assert from 'node:assert/strict';
import {createRequire, registerHooks} from 'node:module';
import {readFileSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {transformSync} from 'rolldown/utils';
import {fabricHost, React} from './helpers/fabric-host.mjs';

// The Fabric helper runs react.production.js, so the components under test must
// share that exact React instance; the default condition would give them the
// development copy, whose hook dispatcher the production renderer never fills.
const require = createRequire(import.meta.url);
const productionReact = pathToFileURL(join(dirname(require.resolve('react')), 'cjs/react.production.js')).href;

type HostNode = {tag: number; type: unknown; props: Record<string, any>; children: HostNode[]};
type PaintValue = {value: number};
/** The presenter reads only the reduced flag and a named standard duration. */
type MotionPolicy = {reduce: boolean; duration: (name: string) => number};
type Timing = {
  config: {toValue: number; duration: number; easing: unknown};
  value: PaintValue; stopped: boolean; started: boolean; completed: boolean;
  /** Move the painted value without completing the animation. */
  tick: (to: number) => void;
  /** Complete to the configured target; the only path that calls the callback. */
  finish: () => void;
  /** Invoke the retired callback without touching the painted value. */
  retire: () => void;
};

const stateKey = '__porticoLoadingPaintHostTest';
const baseline: MotionPolicy = {reduce: false, duration: (name: string) => (name === 'standard' ? 180 : 0)};
const state: {setValues: number[]; timings: Timing[]; motion: MotionPolicy; paint: {visible: boolean; interactive: boolean; root: boolean}} = {
  setValues: [],
  timings: [],
  motion: baseline,
  paint: {visible: true, interactive: true, root: true},
};
Object.assign(globalThis, {[stateKey]: state});

const dataModule = (source: string) => `data:text/javascript,${encodeURIComponent(source)}`;
const native = dataModule(`
export const View = 'View', Text = 'Text';
export const Easing = {linear: 'linear', bezier: (...points) => ({bezier: points})};
export class Value {
  constructor(value) {this.value = value;}
  setValue(value) {this.value = value; globalThis.${stateKey}.setValues.push(value);}
}
export const Animated = {
  View: 'AnimatedView', createAnimatedComponent: component => component, Value,
  timing: (value, config) => {
    const record = {config, value, stopped: false, started: false, completed: false};
    record.tick = to => {value.value = to;};
    record.finish = () => {record.completed = true; value.value = config.toValue; if (record.callback) record.callback({finished: !record.stopped});};
    record.retire = () => {if (record.callback) record.callback({finished: false});};
    globalThis.${stateKey}.timings.push(record);
    return {
      start(callback) {record.started = true; record.callback = callback;},
      stop() {record.stopped = true;},
    };
  },
};
export const StyleSheet = {create: value => value, flatten: value => value};
export {Animated as default};
`);
const motion = dataModule(`export const useMotion = () => globalThis.${stateKey}.motion;`);
const paintMode = dataModule(`export const useOverlayPaintMode = () => globalThis.${stateKey}.paint;`);

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === 'react-native') return {url: native, shortCircuit: true};
    if (specifier === 'react' && context.parentURL?.endsWith('.tsx')) return {url: productionReact, shortCircuit: true};
    if (specifier.endsWith('/OverlayPaintContext')) return {url: paintMode, shortCircuit: true};
    if (specifier.startsWith('.') && context.parentURL?.startsWith('file:') && /\/kit\/useMotion(?:\.ts)?$/.test(new URL(specifier, context.parentURL).pathname)) return {url: motion, shortCircuit: true};
    try {return next(specifier, context);} catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ERR_MODULE_NOT_FOUND' || !specifier.startsWith('.')) throw error;
      for (const suffix of ['.ts', '.tsx', '/index.ts', '/index.tsx']) {
        try {return next(specifier + suffix, context);} catch (retry) {if ((retry as NodeJS.ErrnoException).code !== 'ERR_MODULE_NOT_FOUND') throw retry;}
      }
      throw error;
    }
  },
  load(url, context, next) {
    if (!url.endsWith('.tsx')) return next(url, context);
    const filename = fileURLToPath(url);
    const result = transformSync(filename, readFileSync(filename, 'utf8'), {lang: 'tsx', jsx: {runtime: 'automatic'}, target: 'es2022'});
    if (result.errors.length) throw new AggregateError(result.errors, `Cannot transform ${filename}`);
    return {format: 'module', source: result.code, shortCircuit: true};
  },
});

const {LoadingPaint} = await import('../src-next/kit/states/LoadingPaint.tsx');
const {LoadingBoundary} = await import('../src-next/kit/states/LoadingBoundary.tsx');

/** Controlled timer queue: the callback is captured so a retired one can be invoked late. */
const realSetTimeout = globalThis.setTimeout, realClearTimeout = globalThis.clearTimeout;
const timers = new Map<number, {fn: () => void; delay: number}>(); let nextTimer = 1;
globalThis.setTimeout = ((fn: () => void, delay = 0) => {const id = nextTimer++; timers.set(id, {fn, delay}); return id;}) as unknown as typeof setTimeout;
globalThis.clearTimeout = ((id?: number) => {if (id !== undefined) timers.delete(id);}) as unknown as typeof clearTimeout;
after(() => {globalThis.setTimeout = realSetTimeout; globalThis.clearTimeout = realClearTimeout;});

const live = () => [...timers.entries()];
const liveTimer = () => live().at(-1)![0];
const invoke = (id: number) => {const timer = timers.get(id); assert.ok(timer, `timer ${id} must exist before invoking it`); timers.delete(id); timer.fn();};

/** The real component mounted on the Fabric renderer; each update is one React commit. */
async function mount(component: any, initial: any) {
  const host = await fabricHost();
  const element = (props: any) => React.createElement(component, props);
  await host.render(element(initial));
  return {host, get tree() {return host.root[0] as HostNode;},
    async update(next: any) {await host.render(element(next));},
    async act(work: () => void | Promise<void>) {await work(); await host.settle();},
    unmount: () => host.unmount()};
}
const mountPaint = (props: any) => mount(LoadingPaint, props);

const shape = React.createElement('Shape'), child = React.createElement('ChildHost');
/** The content view is the first committed child; the overlay is the optional second. */
function contentView(tree: HostNode): HostNode {return tree.children[0]!;}
function overlay(tree: HostNode): HostNode | undefined {return tree.children[1];}
/** The Value actually rendered as the content opacity, not a discarded render-local one. */
function paintValue(tree: HostNode): PaintValue {
  return contentView(tree).props.style.opacity as PaintValue;
}

test('a cached initial ready mount paints immediately with no quiet timer', async () => {
  const paint = await mountPaint({ready: true, immediate: true, skeleton: shape, children: child});
  try {
    assert.equal(live().length, 0, 'cached ready queues no quiet timer');
    assert.equal(state.timings.length, 0, 'cached ready starts no fade');
    assert.equal(paintValue(paint.tree).value, 1, 'cached ready paints at full opacity');
    assert.equal(overlay(paint.tree), undefined);

    await paint.update({ready: false, immediate: true, skeleton: shape, children: child});
    assert.equal(live().length, 1, 'the next loading cycle starts its own quiet window');
    assert.equal(overlay(paint.tree), undefined, 'quiet hides the shape until the window elapses');
    await paint.act(() => {invoke(liveTimer());});
    assert.ok(overlay(paint.tree), 'the later loading cycle paints the shape');

    await paint.update({ready: true, immediate: true, skeleton: shape, children: child});
    assert.equal(overlay(paint.tree), undefined);
    const fade = state.timings.at(-1)!;
    assert.equal(fade.config.duration, 180, 'cached is not sticky: later content fades');
    assert.equal(fade.started, true);
    assert.equal(fade.value, paintValue(paint.tree), 'the fade drives the rendered opacity Value');
    fade.finish();
    assert.equal(paintValue(paint.tree).value, 1);
  } finally {await paint.unmount();}
});

test('a retired quiet completion cannot paint in the next cycle, cannot hide the current shape, and is inert after unmount', async () => {
  const paint = await mountPaint({ready: false, skeleton: shape, children: child});
  const retired = timers.get(liveTimer())!.fn;
  try {
    await paint.update({ready: true, skeleton: shape, children: child});
    await paint.update({ready: false, skeleton: shape, children: child});
    const current = liveTimer();
    assert.equal(overlay(paint.tree), undefined, 'still quiet in the new cycle');
    await paint.act(() => {retired();});
    assert.equal(overlay(paint.tree), undefined, 'the retired completion reveals nothing');
    await paint.act(() => {invoke(current);});
    assert.ok(overlay(paint.tree), 'only the current cycle completion paints');
    // Exercise the published live-timer guard even when cancellation loses a race.
    await paint.act(() => {retired();});
    assert.ok(overlay(paint.tree), 'a late retired timer leaves the current shape visible');
  } finally {
    await paint.unmount();
    const timings = state.timings.length, queued = live().length;
    assert.doesNotThrow(() => {retired();}, 'the captured retired callback is inert after unmount');
    assert.equal(state.timings.length, timings, 'a late retired callback starts no animation');
    assert.equal(live().length, queued, 'a late retired callback queues no timer');
  }
});

test('changing the motion policy during quiet never restarts the 300 ms window', async () => {
  const paint = await mountPaint({ready: false, skeleton: shape, children: child});
  try {
    const [timerId, timer] = live()[0]!;
    assert.equal(timer.delay, 300);
    state.motion = {reduce: true, duration: (name: string) => (name === 'standard' ? 160 : 0)};
    await paint.update({ready: false, skeleton: shape, children: child});
    assert.equal(live().length, 1, 'no second quiet timer');
    assert.equal(live()[0]![0], timerId, 'the original timer is still the live one');
    await paint.act(() => {invoke(timerId);});
    assert.ok(overlay(paint.tree), 'the original window still reveals the shape');
  } finally {await paint.unmount(); state.motion = baseline;}
});

test('a policy change retargets the painted value, and a retired completion leaves its successor live', async () => {
  const paint = await mountPaint({ready: false, skeleton: shape, children: child});
  try {
    await paint.update({ready: true, skeleton: shape, children: child});
    const first = state.timings.at(-1)!;
    assert.equal(first.config.duration, 180);
    assert.deepEqual(first.config.easing, {bezier: [0.2, 0.8, 0.2, 1]}, 'default motion uses the out curve');
    assert.equal(first.value, paintValue(paint.tree));
    first.tick(0.42);
    assert.equal(paintValue(paint.tree).value, 0.42, 'partial paint is the value the retarget must see');
    const resets = state.setValues.length;

    state.motion = {reduce: true, duration: (name: string) => (name === 'standard' ? 160 : 0)};
    await paint.update({ready: true, skeleton: shape, children: child});
    const second = state.timings.at(-1)!;
    assert.equal(second.config.duration, 160, 'reduced motion retargets at 160');
    assert.equal(second.config.easing, 'linear');
    assert.equal(second.value, paintValue(paint.tree), 'the retarget drives the same rendered Value');
    assert.equal(paintValue(paint.tree).value, 0.42, 'the retarget starts from the painted value');
    assert.equal(state.setValues.length, resets, 'no reset to zero on a policy change');
    assert.equal(first.stopped, true);

    // The retired completion runs despite its stop and writes no value; the
    // component's own handle check must keep the successor authoritative.
    first.retire();
    assert.equal(second.stopped, false, 'the successor is still the live fade');
    assert.equal(paintValue(paint.tree).value, 0.42, 'a retired callback writes no paint');
    second.tick(0.9);
    assert.equal(paintValue(paint.tree).value, 0.9, 'the successor holds its own partial paint');

    state.motion = baseline;
    await paint.update({ready: true, skeleton: shape, children: child});
    const third = state.timings.at(-1)!;
    assert.equal(third.config.duration, 180, 'a later retarget still owns the fade');
    assert.deepEqual(third.config.easing, {bezier: [0.2, 0.8, 0.2, 1]});
    assert.equal(second.stopped, true, 'the retarget stops the previous live fade');
    assert.equal(paintValue(paint.tree).value, 0.9, 'the retarget starts from the painted value');
    third.finish();
    assert.equal(paintValue(paint.tree).value, 1, 'the live successor reaches full opacity');
  } finally {await paint.unmount(); state.motion = baseline;}
});


// Source vectors only. The native hosts below are React's committed nodes; they
// are not measured layout.
test('a quiet boundary keeps its supplied footprint and rejects a retired timer', async () => {
  const boundary = await mount(LoadingBoundary, {ready: false, skeleton: shape, children: child});
  const stale = timers.get(liveTimer())!.fn;
  try {
    const quiet = boundary.tree.children[0]!;
    assert.equal(boundary.tree.type, 'AnimatedView');
    assert.equal(quiet.type, 'View');
    assert.equal(quiet.children[0]!.type, 'Shape');
    assert.deepEqual(quiet.props.style, {opacity: 0});
    assert.equal(timers.get(liveTimer())!.delay, 300);
    await boundary.update({ready: true, skeleton: shape, children: child});
    assert.equal(boundary.tree.children[0]!.type, 'ChildHost', 'ready content replaces the quiet shape');
    await boundary.update({ready: false, skeleton: shape, children: child});
    await boundary.act(() => {invoke(liveTimer());});
    const skeleton = boundary.tree.children[0]!;
    assert.equal(skeleton.type, 'View');
    assert.notEqual(skeleton.tag, quiet.tag, 'the boundary key replaces the quiet host with the shape host');
    assert.equal(skeleton.props.style, undefined);
    assert.equal(skeleton.children[0]!.type, 'Shape');
    await boundary.act(() => {stale();});
    assert.equal(boundary.tree.children[0]!.type, 'View');
    assert.equal(boundary.tree.children[0]!.tag, skeleton.tag, 'a retired timer cannot replace the current host');
    assert.equal(boundary.tree.children[0]!.children[0]!.type, 'Shape');
  } finally {await boundary.unmount();}
});

test('outgoing inert loading paint cancels its quiet window without changing the supplied child', async () => {
  const paint = await mountPaint({ready: false, skeleton: shape, children: child});
  const stale = timers.get(liveTimer())!.fn;
  try {
    state.paint = {visible: true, interactive: false, root: true};
    await paint.update({ready: false, skeleton: shape, children: child});
    assert.equal(live().length, 0);
    await paint.act(() => {stale();});
    assert.equal(overlay(paint.tree), undefined);
    assert.equal(contentView(paint.tree).children[0]!.type, 'ChildHost');
  } finally {await paint.unmount(); state.paint = {visible: true, interactive: true, root: true};}
});

test('outgoing inert boundary stops a partial content fade at its current paint', async () => {
  const props = {ready: true, skeleton: shape, children: child};
  const boundary = await mount(LoadingBoundary, props);
  try {
    // A ready mount reaches content on its own commit and only then does the
    // content fade exist. Read the fade after that commit so the partial paint
    // below is the one the boundary actually renders.
    const fade = state.timings.at(-1)!;
    assert.equal(fade.value, boundary.tree.props.style[1].opacity, 'the fade drives the rendered opacity value');
    fade.tick(0.42);
    assert.equal(boundary.tree.props.style[1].opacity.value, 0.42, 'the boundary renders the partial paint');
    state.paint = {visible: true, interactive: false, root: true};
    await boundary.update(props);
    assert.equal(fade.stopped, true);
    assert.equal(boundary.tree.props.style[1].opacity.value, 0.42);
    assert.equal(boundary.tree.children[0]!.type, 'ChildHost', 'the supplied child is the committed content');
  } finally {await boundary.unmount(); state.paint = {visible: true, interactive: true, root: true};}
});
