import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import * as React from 'react';
import {Children, isValidElement, type Context, type ReactNode} from 'react';
import {easings, reducedMotion, springs, springTiming} from '@design';
import type {QueuePlaybackFence} from '../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot} from '../src/platform/playback/types.ts';
import type {WatchPolicyBinding} from '../src/screens/watch/policy.ts';
import type {WatchNextBinding} from '../src/screens/watch/up-next.ts';
import {transformTsx} from './transform-tsx.mjs';

type Ref = {current: unknown};
type Effect = {deps?: readonly unknown[]; cleanup?: () => void; phase: 'layout' | 'passive'};
const changed = (a: readonly unknown[] | undefined, b: readonly unknown[] | undefined) => !a || !b || a.length !== b.length || a.some((value, index) => !Object.is(value, b[index]));

const bridgeKey = '__porticoPromptRegressionHooks';
const reactKey = '__porticoPromptRegressionReact';
const bridge = {current: undefined as HookFrame | undefined};
const hostGlobals = globalThis as Record<string, unknown>;
hostGlobals[bridgeKey] = bridge;
hostGlobals[reactKey] = React;
// The fixture is the renderer, so every hook the real prompt modules call must
// reach this frame. The non-hook exports they also import need no dispatcher.
const hookNames = ['useRef', 'useState', 'useMemo', 'useEffect', 'useLayoutEffect', 'useContext'];
const hookModule = 'data:text/javascript,' + encodeURIComponent(`const bridge = globalThis.${bridgeKey}, react = globalThis.${reactKey};
${hookNames.map(name => `export const ${name} = (...args) => bridge.current.${name}(...args);`).join('\n')}
export const createContext = react.createContext;
export const Component = react.Component;`);

const loader = registerHooks({load(url, context, next) {
  const module = (source: string, format = 'module') => ({format, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get: (_, key) => String(key)});');
  if (url.endsWith('/src/screens/watch/prompt-motion.ts')) {
    const source = readFileSync(new URL(url), 'utf8')
      .replace("from 'react'", `from ${JSON.stringify(hookModule)}`)
      .replace("export {useReducedMotion as usePromptReducedMotion} from '../../kit/overlays/shared';",
        'export const usePromptReducedMotion = () => !!globalThis.__promptReduced;');
    return module(source, 'module-typescript');
  }
  // The prompt's context/provider boundary and retirement lifecycle, on this frame.
  if (url.endsWith('/src/screens/watch/prompt-focus.tsx')) {
    const source = transformTsx(url).replace(/from (['"])react\1/g, `from ${JSON.stringify(hookModule)}`);
    return module(source);
  }
  if (url.endsWith('/src/screens/watch/UpNextPrompt.tsx')) {
    const source = transformTsx(url).replace(/from (['"])react\1/g, `from ${JSON.stringify(hookModule)}`);
    return module(source);
  }
  if (url.endsWith('/src/kit/artwork/index.ts')) return module("export const Artwork = 'artwork';");
  if (url.endsWith('.tsx')) return module(transformTsx(url));
  return next(url, context);
}});

const {WatchSkipPrompt} = await import('../src/screens/watch/SkipPrompt.tsx');
const {WatchUndoPrompt} = await import('../src/screens/watch/UndoPrompt.tsx');
const {WatchUpNextPrompt} = await import('../src/screens/watch/UpNextPrompt.tsx');
test.after(() => {
  loader.deregister();
  delete hostGlobals[bridgeKey];
  delete hostGlobals[reactKey];
  delete (globalThis as Record<string, unknown>).__promptReduced;
});

/** A deterministic hook lifecycle for the real prompt component functions. It
 * runs layout/passive effects, refs, state settlements and timers; it is not a
 * browser, React DOM or visual-animation substitute. */
class HookFrame {
  private slots: any[] = [];
  private cursor = 0;
  private dirty = false;
  private mounted = true;
  private queued: {effect: Effect; run: () => void | (() => void)}[] = [];
  private classes = new Map<number, {type: unknown; props: any; instance: any}>();
  private classCursor = 0;
  private mounts: any[] = [];
  private updates: {instance: any; previous: any}[] = [];
  private readonly nodes = new Map<Ref, PromptNode>();
  private readonly keys = new Map<Ref, string | null>();
  useRef(value: unknown) { const i = this.cursor++; return this.slots[i] ??= {current: value}; }
  useState(initial: unknown) {
    const i = this.cursor++;
    this.slots[i] ??= {value: initial};
    return [this.slots[i].value, (next: unknown) => {
      if (!this.mounted) return;
      const value = typeof next === 'function' ? (next as (value: unknown) => unknown)(this.slots[i].value) : next;
      if (!Object.is(value, this.slots[i].value)) { this.slots[i].value = value; this.dirty = true; }
    }];
  }
  useMemo(make: () => unknown, deps?: readonly unknown[]) {
    const i = this.cursor++, held = this.slots[i];
    if (!held || changed(held.deps, deps)) this.slots[i] = {value: make(), deps};
    return this.slots[i].value;
  }
  useLayoutEffect(run: () => void | (() => void), deps?: readonly unknown[]) { this.effect('layout', run, deps); }
  useEffect(run: () => void | (() => void), deps?: readonly unknown[]) { this.effect('passive', run, deps); }
  /** No WatchPromptFocus provider is committed above these prompt trees, so the
   * actual read is the context's own declared default — exactly what React answers. */
  useContext<T>(context: Context<T>): T {
    return (context as unknown as {_currentValue: T})._currentValue;
  }
  private effect(phase: Effect['phase'], run: () => void | (() => void), deps?: readonly unknown[]) {
    const i = this.cursor++, previous: Effect | undefined = this.slots[i];
    if (!previous || changed(previous.deps, deps)) {
      const effect: Effect = {deps, cleanup: previous?.cleanup, phase}; this.slots[i] = effect; this.queued.push({effect, run});
    }
  }
  render(component: () => ReactNode): ReactNode {
    for (let attempt = 0; attempt < 8; attempt++) {
      bridge.current = this; this.cursor = 0; this.dirty = false; this.queued = [];
      this.classCursor = 0; this.mounts = []; this.updates = [];
      const result = this.resolve(component()), seen = new Set<Ref>();
      // React's before-mutation lifecycle sees the old focused prompt, before refs move.
      for (const job of this.updates) job.instance.getSnapshotBeforeUpdate?.(job.previous);
      const commit = (children: ReactNode) => Children.forEach(children, child => {
        if (!isValidElement<any>(child)) return;
        const ref = child.props.ref;
        if (ref && typeof ref === 'object') {
          let node = this.nodes.get(ref), key = child.key;
          if (!node || this.keys.get(ref) !== key) {
            if (node) node.connected = false;
            node = new PromptNode(String(key ?? 'root')); this.nodes.set(ref, node);
          }
          this.keys.set(ref, key); ref.current = node; node.connected = true; seen.add(ref);
        }
        commit(child.props.children);
      });
      commit(result);
      for (const [ref, node] of this.nodes) if (!seen.has(ref)) { ref.current = null; node.connected = false; }
      for (const instance of this.mounts) instance.componentDidMount?.();
      for (const job of this.updates) job.instance.componentDidUpdate?.(job.previous);
      for (const phase of ['layout', 'passive'] as const) {
        const pending = this.queued.filter(job => job.effect.phase === phase);
        pending.forEach(job => job.effect.cleanup?.());
        pending.forEach(job => { job.effect.cleanup = job.run() ?? undefined; });
      }
      if (!this.dirty) return result;
    }
    throw new Error('Prompt effects did not settle within eight renders');
  }
  /** The real component chain above the painted prompt, resolved the way the app's
   * own renderer would: functions are called, class boundaries are instantiated and
   * rendered. The promise is the prompt element the original assertions read. */
  private resolve(node: ReactNode): ReactNode {
    let current = node;
    for (let depth = 0; depth < 16; depth++) {
      if (!isValidElement<any>(current)) return current;
      const type = current.type as any;
      if (typeof type === 'string' || type === null || type === undefined) return current;
      if (typeof type === 'object' && typeof type.render === 'function') { current = type.render(current.props); continue; }
      if (typeof type !== 'function') return current;
      if (type.prototype?.isReactComponent) { current = this.instantiate(type, current.props).render(current.props); continue; }
      current = type(current.props);
    }
    throw new Error('Prompt component chain did not reach an element within sixteen components');
  }
  private instantiate(type: any, props: any) {
    const slot = this.classCursor++;
    let held = this.classes.get(slot);
    if (!held || held.type !== type) {
      held = {type, props, instance: new type(props)};
      this.classes.set(slot, held);
      this.mounts.push(held.instance);
    } else if (held.props !== props) {
      this.updates.push({instance: held.instance, previous: held.props});
      held.props = props;
    }
    held.instance.props = props;
    return held.instance;
  }
  unmount() {
    if (!this.mounted) return;
    this.mounted = false;
    for (const slot of this.slots) { slot?.cleanup?.(); slot.cleanup = undefined; }
    for (const held of this.classes.values()) held.instance.componentWillUnmount?.();
    this.classes.clear();
  }
}

class ControlledAnimation {
  cancelled = 0;
  constructor(readonly frames: Keyframe[], readonly options: KeyframeAnimationOptions) {}
  cancel() { this.cancelled += 1; }
}

class PromptNode {
  connected = true;
  opacity = 1;
  translateValue = '0px';
  reduceMotion = false;
  readonly animations: ControlledAnimation[] = [];
  readonly style: {translate: string; removeProperty: (name: string) => void};
  constructor(readonly name: string) {
    const node = this;
    this.style = {
      get translate() { return node.translateValue; },
      set translate(value: string) { node.translateValue = value; },
      removeProperty(name: string) { if (name === 'translate') node.translateValue = '0px'; },
    };
  }
  closest(selector: string) { return selector === '[data-reduce-motion="true"]' && this.reduceMotion ? this : null; }
  animate(frames: Keyframe[], options: KeyframeAnimationOptions) {
    const animation = new ControlledAnimation(frames, options); this.animations.push(animation); return animation;
  }
  pose(opacity: number, translate: string) { this.opacity = opacity; this.style.translate = translate; }
}

let systemReduced = false, appReduced = false;
const originalGlobals = {window: globalThis.window, document: globalThis.document, getComputedStyle: globalThis.getComputedStyle};
function installEnvironment() {
  systemReduced = false; appReduced = false;
  (globalThis as Record<string, unknown>).__promptReduced = false;
  const globals = globalThis as Record<string, unknown>;
  globals.window = {matchMedia: (query: string) => ({matches: query.includes('prefers-reduced-motion') && systemReduced, addEventListener() {}, removeEventListener() {}})};
  globals.document = {documentElement: {dataset: {}}, body: {dataset: {}}, activeElement: null};
  globals.getComputedStyle = (node: PromptNode) => ({opacity: String(node.opacity), translate: node.translateValue});
}
function setReduced(system: boolean, app: boolean) {
  systemReduced = system; appReduced = app;
  (globalThis as Record<string, unknown>).__promptReduced = system || app;
  (globalThis.document.documentElement.dataset as Record<string, string | undefined>).reduceMotion = app ? 'true' : undefined;
}
test.beforeEach(installEnvironment);
test.afterEach(() => Object.assign(globalThis as Record<string, unknown>, originalGlobals));

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const authority = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', epoch: '1', viewerFence: 'viewer'};
const fence = (entryId = 'e1', sessionId = 's1'): QueuePlaybackFence => ({kind: 'queue', playback: {scope, run: 1}, sessionId, itemId: entryId, sourceId: 'source', entryId, generation: 1});
const segment = (id: string, kind = 'intro') => ({id, kind, startSeconds: 0, endSeconds: 10, automaticSafe: false});
const policy = (parts: {prompt?: unknown; undo?: unknown; attention?: string | null; finished?: boolean} = {}) => ({prompt: parts.prompt ?? null, undo: parts.undo ?? null, markers: [], finished: parts.finished ?? false, replay: false, attention: parts.attention ?? null});

function queueView(f: QueuePlaybackFence, nextEntry = 'e2') {
  return {
    queue: {id: 'q1', scope, revision: '7', highestSeenSequence: '1', replayWindow: {results: 256, bodies: 32}, repeat: 'off', shuffled: false,
      currentEntryId: f.entryId, currentPlaybackId: f.sessionId, entries: [
        {id: f.entryId, removed: false, hidden: false, itemId: f.itemId, editionId: null, partId: null, sourceContext: {kind: 'item', id: f.itemId, revision: null, entryId: f.entryId}},
        {id: nextEntry, removed: false, hidden: false, itemId: nextEntry, editionId: null, partId: null, sourceContext: {kind: 'item', id: nextEntry, revision: null, entryId: nextEntry}},
      ]},
    highestSequence: '1', current: {id: f.sessionId, generation: f.generation, state: 'playing'},
    next: {entryId: nextEntry, available: true, reason: 'ready'},
    items: [{entryId: nextEntry, itemId: nextEntry, title: `Next ${nextEntry}`, kind: 'episode', durationMs: 120000, stillUrl: 'next.jpg', episode: {seasonNumber: 1, number: 2}}],
    postPlay: {nextEntryId: nextEntry, available: true, reason: 'ready', countdownSeconds: 10, autoplay: true, passoutCheckDue: false, automaticAdvances: 0, startDuringCredits: false},
    total: 2, remainingCount: 1, totalDurationMs: 1, durationKnown: true,
  };
}
function snapshot(f: QueuePlaybackFence, policyView: unknown, nextEntry = 'e2', quiet = false): WebPlaybackRuntimeSnapshot {
  return {
    nativeVideoSurfaceActive: false, authority, sequence: 1, playback: {} as never,
    dock: {local: {kind: 'video', fence: f, positionSeconds: 100, playing: true, expanded: true, queue: null}, parkedAudio: null, resumeVideo: null,
      remote: quiet ? {id: 'receiver', kind: 'cast', phase: 'playing', sequence: '1', playback: {kind: 'video', fence: f, positionSeconds: 100, playing: true, expanded: true, queue: null}, positionSeconds: 100} : null},
    fence: f,
    presentation: {fence: f, subject: {kind: 'video', item: {kind: 'episode', id: f.itemId, title: 'Episode'}}, kind: 'video', durationSeconds: 1000, positionSeconds: 100,
      controls: ['seek', 'play', 'pause'], playing: true, expanded: true, finished: false, rate: 1, volume: 1, muted: false, policy: policyView},
    presentations: [], queueView: queueView(f, nextEntry), offers: null, options: null, disposed: false,
  } as unknown as WebPlaybackRuntimeSnapshot;
}
function bindingFor(getLive: () => WebPlaybackRuntimeSnapshot) {
  const actions: unknown[] = [];
  const binding = {getSnapshot: getLive, isCurrent: () => true,
    policyAction: async (_fence: QueuePlaybackFence, action: unknown) => {actions.push(action);},
    reportError: (error: unknown) => {throw error;}};
  return {binding, actions};
}
function timerHarness(t: TestContext) {
  const callbacks = new Map<number, () => void>(), cleared: number[] = [];
  let serial = 0;
  const original = {set: globalThis.setTimeout, clear: globalThis.clearTimeout};
  const set = ((callback: () => void) => { const id = ++serial; callbacks.set(id, callback); return id as unknown as ReturnType<typeof setTimeout>; }) as typeof setTimeout;
  const clear = ((id: number) => { cleared.push(id); }) as typeof clearTimeout;
  (globalThis as Record<string, unknown>).setTimeout = set;
  (globalThis as Record<string, unknown>).clearTimeout = clear;
  t.after(() => Object.assign(globalThis as Record<string, unknown>, {setTimeout: original.set, clearTimeout: original.clear}));
  return {ids: () => [...callbacks.keys()], cleared, fire(id: number) { const callback = callbacks.get(id); callbacks.delete(id); callback?.(); }};
}
function buttons(element: ReactNode): any[] {
  const found: any[] = [];
  const visit = (node: ReactNode) => { if (!isValidElement<any>(node)) return; if (node.props.type === 'button') found.push(node); Children.forEach(node.props.children, visit); };
  visit(element); return found;
}
const rootNode = (element: ReactNode) => (element as any).props.ref.current as PromptNode;

function skipSnapshot(f: QueuePlaybackFence, id = 'intro') { return snapshot(f, policy({prompt: {kind: 'skip', marker: segment(id)}})); }
function undoSnapshot(f: QueuePlaybackFence, id = 'undo-1') { return snapshot(f, policy({undo: {marker: segment(id), remainingSeconds: 3}})); }
function upNextSnapshot(f: QueuePlaybackFence, offerId = 'offer:1', nextEntry = 'e2') {
  return snapshot(f, policy({prompt: {kind: 'up-next', offer: {id: offerId, marker: {id: `credits-${offerId}`, kind: 'credits', startSeconds: 900, endSeconds: 1000}},
    countdown: {automatic: false, seconds: 42}, sceneAfterCredits: false}}), nextEntry);
}

test('Skip withdraws action eligibility at once and exits from the painted pose', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const f = fence(), first = skipSnapshot(f), live = {value: first};
  const {binding, actions} = bindingFor(() => live.value);
  const frame = new HookFrame();
  t.after(() => frame.unmount());
  const render = (snapshotValue = first) => frame.render(() => WatchSkipPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: snapshotValue,
    expanded: true, controlsVisible: false, layout: 'desktop'}));

  const active = render() as any;
  assert.equal(active.props['data-watch-skip'], true);
  assert.equal(active.props['data-prompt-phase'], 'in');
  const node = rootNode(active);
  assert.equal(node.animations.length, 1);
  assert.deepEqual(node.animations[0].options, {duration: 180, easing: easings.out, fill: 'none'});
  assert.deepEqual(node.animations[0].frames, [{opacity: 0, translate: '0px 12px'}, {opacity: 1, translate: '0px'}]);

  buttons(active)[1].props.onClick(); await Promise.resolve();
  assert.deepEqual(actions, [{kind: 'skip', markerId: 'intro'}]); actions.length = 0;

  node.pose(0.42, '0px 7px');
  live.value = snapshot(f, policy({prompt: null}));
  const exiting = render() as any;
  assert.equal(exiting.props['data-prompt-phase'], 'out');
  assert.equal(exiting.props.inert, true);
  assert.equal(exiting.props['aria-hidden'], true);
  assert.equal(node.animations.length, 2);
  assert.equal(node.animations[0].cancelled, 1);
  assert.deepEqual(node.animations[1].frames, [{opacity: 0.42, translate: '0px 7px'}, {opacity: 0, translate: '0px 7px'}]);
  assert.deepEqual(node.animations[1].options, {duration: 120, easing: easings.out, fill: 'forwards'});
  assert.equal((exiting.props.children as unknown[]).length, 2, 'one retained prompt tree, not a duplicate actionable copy');

  buttons(exiting)[1].props.onClick(); await Promise.resolve();
  assert.equal(actions.length, 0, 'the retained action cannot operate a withdrawn marker');
  t.mock.timers.tick(120);
  assert.equal(render(), null, 'the outgoing paint leaves after its 120 ms exit');
});

test('a quiet dock drops the prompt without starting an exit paint', t => {
  const f = fence(), first = skipSnapshot(f), live = {value: first};
  const {binding} = bindingFor(() => live.value);
  const frame = new HookFrame();
  t.after(() => frame.unmount());
  const render = (shown: WebPlaybackRuntimeSnapshot) => frame.render(() => WatchSkipPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: shown,
    expanded: true, controlsVisible: false, layout: 'desktop'}));
  const active = render(first) as any, node = rootNode(active);
  assert.equal(node.animations.length, 1);
  live.value = snapshot(f, policy({prompt: null}), 'e2', true);
  assert.equal(render(live.value), null);
  assert.equal(node.animations.length, 1, 'no 120 ms exit is created for dock suppression');
  assert.equal(node.animations[0].cancelled, 1);
});

test('the authored snap recipe and prompt identity survive control toggles (source-level only)', t => {
  const f = fence(), shown = skipSnapshot(f);
  const {binding} = bindingFor(() => shown);
  const frame = new HookFrame();
  t.after(() => frame.unmount());
  const render = (controlsVisible: boolean) => frame.render(() => WatchSkipPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: shown,
    expanded: true, controlsVisible, layout: 'desktop'}));
  const settled = render(false) as any, prompt = rootNode(settled), snap = springTiming(springs.snap);
  // Authored recipe/identity only. This hook seam cannot supply the real seek row, computed
  // transform/focus or a genuine current-paint reversal; that remains a DOM integration check.
  assert.equal(settled.props.style['--prompt-snap-ms'], `${snap.durationMs}ms`);
  assert.equal(settled.props.style['--prompt-spring'], snap.css);
  assert.equal(prompt.animations.length, 1);
  for (const controlsVisible of [true, false, true]) {
    const next = render(controlsVisible) as any;
    assert.equal(next.props['data-controls-visible'], controlsVisible);
    assert.equal(rootNode(next), prompt, 'the control update keeps the same prompt node');
    assert.equal(prompt.animations.length, 1, 'the authored place change does not queue another JS animation');
  }
});

for (const [name, system, app] of [['app', false, true], ['system', true, false]] as const) {
  test(`${name} reduction renders Undo as a 160 ms opacity-only fade`, t => {
    setReduced(system, app);
    const f = fence(), shown = undoSnapshot(f), live = {value: shown};
    const {binding} = bindingFor(() => live.value);
    const frame = new HookFrame();
    t.after(() => frame.unmount());
    const root = frame.render(() => WatchUndoPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: shown, expanded: true, controlsVisible: false, layout: 'desktop'})) as any;
    const node = rootNode(root);
    assert.equal(root.props['data-watch-undo'], true);
    assert.deepEqual(node.animations[0].frames, [{opacity: 0}, {opacity: 1}]);
    assert.deepEqual(node.animations[0].options, {duration: reducedMotion.durationMs, easing: easings.linear, fill: 'none'});
    assert.equal(node.translateValue, 'none');
  });
}

test('a successor Up next offer cancels the retired animation and keeps its own target', async t => {
  const f = fence(), first = upNextSnapshot(f, 'offer:1', 'e2'), successor = upNextSnapshot(f, 'offer:2', 'e3');
  const live = {value: first}, {binding, actions} = bindingFor(() => live.value);
  const artwork = {foreground: () => ({src: 'next.jpg'})} as never;
  const environment = {t: (key: string) => key} as never;
  const frame = new HookFrame();
  t.after(() => frame.unmount());
  const render = (shown: WebPlaybackRuntimeSnapshot) => frame.render(() => WatchUpNextPrompt({binding: binding as unknown as WatchNextBinding, snapshot: shown, artwork, environment,
    expanded: true, controlsVisible: false, layout: 'desktop'}));
  const active = render(first) as any, node = rootNode(active);
  assert.equal(active.props['data-watch-up-next'], true);
  assert.equal(node.animations.length, 1);
  live.value = successor;
  const next = render(successor) as any, successorNode = rootNode(next);
  assert.notEqual(successorNode, node);
  assert.equal(node.animations[0].cancelled, 1);
  assert.equal(next.props['data-prompt-phase'], 'in');
  assert.equal(successorNode.animations.length, 1);
  assert.match(JSON.stringify(next), /Next e3/);
  buttons(next)[0].props.onClick(); await Promise.resolve();
  assert.deepEqual(actions, [{kind: 'play-now', nextEntryId: 'e3'}]);
  frame.unmount();
  assert.equal(successorNode.animations[0].cancelled, 1);
});

test('an Undo policy switch uses only the successor marker', async t => {
  const f = fence(), first = undoSnapshot(f, 'undo-1'), successor = undoSnapshot(f, 'undo-2');
  const live = {value: first}, {binding, actions} = bindingFor(() => live.value);
  const frame = new HookFrame();
  t.after(() => frame.unmount());
  const render = (shown: WebPlaybackRuntimeSnapshot) => frame.render(() => WatchUndoPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: shown, expanded: true, controlsVisible: false, layout: 'desktop'}));
  const active = render(first) as any, node = rootNode(active);
  live.value = successor;
  const next = render(successor) as any, successorNode = rootNode(next);
  assert.notEqual(successorNode, node);
  assert.equal(node.animations[0].cancelled, 1);
  buttons(next)[0].props.onClick(); await Promise.resolve();
  assert.deepEqual(actions, [{kind: 'undo', markerId: 'undo-2'}]);
  frame.unmount();
});

for (const [name, system, app] of [['app', false, true], ['system', true, false]] as const) {
  test(`${name} reduction retargets an active Undo from its painted opacity to the original action`, async t => {
    const f = fence(), shown = undoSnapshot(f, 'undo-live'), live = {value: shown};
    const {binding, actions} = bindingFor(() => live.value);
    const frame = new HookFrame(); t.after(() => frame.unmount());
    const render = () => frame.render(() => WatchUndoPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: shown, expanded: true, controlsVisible: false, layout: 'desktop'}));
    const active = render() as any, node = rootNode(active);
    assert.equal(node.animations[0].options.duration, 180);
    assert.ok(node.animations[0].frames.some(frame => 'translate' in frame));
    node.pose(0.55, '0px 6px');
    setReduced(system, app);
    const reduced = render() as any;
    assert.equal(rootNode(reduced), node);
    assert.equal(node.animations[0].cancelled, 1);
    assert.deepEqual(node.animations[1].frames, [{opacity: 0.55}, {opacity: 1}]);
    assert.deepEqual(node.animations[1].options, {duration: reducedMotion.durationMs, easing: easings.linear, fill: 'none'});
    assert.equal(node.translateValue, 'none');
    buttons(reduced)[0].props.onClick(); await Promise.resolve();
    assert.deepEqual(actions, [{kind: 'undo', markerId: 'undo-live'}]);
  });
  test(`${name} reduction retargets an active Up next from its painted opacity to the original entry`, async t => {
    const f = fence(), shown = upNextSnapshot(f, 'offer:live', 'e2'), live = {value: shown};
    const {binding, actions} = bindingFor(() => live.value);
    const artwork = {foreground: () => ({src: 'next.jpg'})} as never, environment = {t: (key: string) => key} as never;
    const frame = new HookFrame(); t.after(() => frame.unmount());
    const render = () => frame.render(() => WatchUpNextPrompt({binding: binding as unknown as WatchNextBinding, snapshot: shown, artwork, environment,
      expanded: true, controlsVisible: false, layout: 'desktop'}));
    const active = render() as any, node = rootNode(active);
    assert.equal(node.animations[0].options.duration, 180);
    assert.ok(node.animations[0].frames.some(frame => 'translate' in frame));
    node.pose(0.48, '0px 5px');
    setReduced(system, app);
    const reduced = render() as any;
    assert.equal(rootNode(reduced), node);
    assert.equal(node.animations[0].cancelled, 1);
    assert.deepEqual(node.animations[1].frames, [{opacity: 0.48}, {opacity: 1}]);
    assert.deepEqual(node.animations[1].options, {duration: reducedMotion.durationMs, easing: easings.linear, fill: 'none'});
    assert.equal(node.translateValue, 'none');
    buttons(reduced)[0].props.onClick(); await Promise.resolve();
    assert.deepEqual(actions, [{kind: 'play-now', nextEntryId: 'e2'}]);
  });
}

test('a withdrawal release queued before a successor cannot release or repaint it', async t => {
  const f = fence(), first = skipSnapshot(f, 'old'), successor = skipSnapshot(f, 'new');
  const live = {value: first}, {binding, actions} = bindingFor(() => live.value);
  const frame = new HookFrame(); t.after(() => frame.unmount());
  const timers = timerHarness(t);
  const render = (shown: WebPlaybackRuntimeSnapshot) => frame.render(() => WatchSkipPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: shown,
    expanded: true, controlsVisible: false, layout: 'desktop'}));
  render(first);
  live.value = snapshot(f, policy({prompt: null}));
  render(first);
  const [id] = timers.ids();
  assert.equal(timers.ids().length, 1);
  live.value = successor;
  const next = render(successor) as any, node = rootNode(next);
  assert.equal(next.props['data-prompt-phase'], 'in');
  assert.ok(timers.cleared.includes(id));
  timers.fire(id);
  const after = render(successor) as any;
  assert.equal(after.props['data-prompt-phase'], 'in');
  assert.equal(rootNode(after), node);
  buttons(after)[1].props.onClick(); await Promise.resolve();
  assert.deepEqual(actions, [{kind: 'skip', markerId: 'new'}]);
});

test('a withdrawal release queued before unmount is cleared and cannot repaint', t => {
  const f = fence(), first = skipSnapshot(f, 'unmount');
  const live = {value: first}, {binding} = bindingFor(() => live.value);
  const frame = new HookFrame(); t.after(() => frame.unmount());
  const timers = timerHarness(t);
  let renders = 0;
  const render = (shown: WebPlaybackRuntimeSnapshot) => { renders += 1; return frame.render(() => WatchSkipPrompt({binding: binding as unknown as WatchPolicyBinding, snapshot: shown,
    expanded: true, controlsVisible: false, layout: 'desktop'})); };
  render(first);
  live.value = snapshot(f, policy({prompt: null}));
  render(first);
  const [id] = timers.ids();
  assert.equal(timers.ids().length, 1);
  const before = renders;
  frame.unmount();
  assert.ok(timers.cleared.includes(id));
  assert.doesNotThrow(() => timers.fire(id));
  assert.equal(renders, before);
});

test('prompt press feedback remains local at 90 ms and becomes opacity-only under both reduced signals', () => {
  const skip = readFileSync(new URL('../src/screens/watch/skip-prompt.module.css', import.meta.url), 'utf8');
  const upNext = readFileSync(new URL('../src/screens/watch/up-next.module.css', import.meta.url), 'utf8');
  assert.match(skip, /\.skip \{[^}]*transition: transform 90ms var\(--ease-out\)/);
  assert.match(skip, /@media \(prefers-reduced-motion: reduce\) \{ \.skip \{ transition: opacity var\(--motion-reduced\) linear; \} \.skip:active \{ transform: none; opacity: \.85; \} \}/);
  assert.match(skip, /\[data-reduce-motion='true'\]\) \.skip:active \{ transform: none; opacity: \.85; \}/);
  assert.match(skip, /\[data-reduce-motion='true'\]\) \.skip \{ transition: opacity var\(--motion-reduced\) linear; \}/);
  assert.match(upNext, /\.actions button \{[^}]*transition: transform 90ms var\(--up-next-ease\)/);
  assert.match(upNext, /@media \(prefers-reduced-motion: reduce\) \{ \.actions button \{ transition: opacity var\(--motion-reduced\) linear; \} \.actions button:active \{ transform: none; opacity: \.85; \} \}/);
  assert.match(upNext, /\[data-reduce-motion='true'\]\) \.actions button:active \{ transform: none; opacity: \.85; \}/);
  assert.match(upNext, /\[data-reduce-motion='true'\]\) \.actions button \{ transition: opacity var\(--motion-reduced\) linear; \}/);
});
