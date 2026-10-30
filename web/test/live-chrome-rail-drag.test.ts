import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactNode} from 'react';
import {transformTsx} from './transform-tsx.mjs';
import type {LiveChromePort, LiveChromeWindow} from '../src/screens/live/live-chrome-types';

// The real LiveChrome component functions and pointer handlers run here with a deterministic
// hook lifecycle. No DOM, media or visual evidence is claimed: this is the fence-binding proof.
const changed = (a: readonly unknown[] | undefined, b: readonly unknown[] | undefined) =>
  !a || !b || a.length !== b.length || a.some((value, index) => !Object.is(value, b[index]));
const bridgeKey = '__porticoLiveChromeRailDrag';
const bridge = {current: undefined as HookFrame | undefined};
(globalThis as Record<string, unknown>)[bridgeKey] = bridge;
const hookModule = 'data:text/javascript,' + encodeURIComponent(`const bridge = globalThis.${bridgeKey}; ${['useRef', 'useState', 'useEffect', 'useLayoutEffect'].map(name => `export const ${name} = (...args) => bridge.current.${name}(...args);`).join('\n')}`);

class HookFrame {
  private slots: any[] = [];
  private cursor = 0;
  private dirty = false;
  private effects: {deps?: readonly unknown[]; cleanup?: () => void; run: () => void | (() => void)}[] = [];
  useRef(value: unknown) { const i = this.cursor++; return this.slots[i] ??= {current: value}; }
  useState(initial: unknown) {
    const i = this.cursor++;
    this.slots[i] ??= {value: typeof initial === 'function' ? initial() : initial};
    return [this.slots[i].value, (next: any) => {
      const value = typeof next === 'function' ? next(this.slots[i].value) : next;
      if (!Object.is(value, this.slots[i].value)) { this.slots[i].value = value; this.dirty = true; }
    }];
  }
  useEffect(run: () => void | (() => void), deps?: readonly unknown[]) {
    const i = this.cursor++, previous = this.slots[i];
    if (!previous || changed(previous.deps, deps)) this.effects.push({deps, cleanup: previous?.cleanup, run});
  }
  useLayoutEffect(run: () => void | (() => void), deps?: readonly unknown[]) { this.useEffect(run, deps); }
  render(component: () => ReactNode): ReactNode {
    for (let attempt = 0; attempt < 8; attempt++) {
      bridge.current = this; this.cursor = 0; this.effects = []; this.dirty = false;
      const tree = component();
      const commit = (children: ReactNode) => Children.forEach(children, child => {
        if (!isValidElement<any>(child)) return;
        if (child.props.ref && typeof child.props.ref === 'object') child.props.ref.current ??= {element: child.type};
        commit(child.props.children);
      });
      commit(tree);
      const pending = this.effects; this.effects = [];
      pending.forEach(job => job.cleanup?.());
      pending.forEach(job => { job.cleanup = job.run() ?? undefined; });
      if (!this.dirty) return tree;
    }
    throw new Error('Live chrome effects did not settle within eight renders');
  }
  unmount() { for (const slot of this.slots) slot?.cleanup?.(); }
}

const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get: (_, key) => String(key)});');
  if (url.endsWith('/kit/artwork/index.ts')) return module("export const ChannelMark = 'channel-mark';");
  if (url.endsWith('/kit/controls/index.ts')) return module("export const Button = 'button';");
  if (url.endsWith('/kit/Icon.tsx')) return module("export const Icon = 'icon';");
  // A-05 presence wrapper: an open guide renders as is; this test proves rail fencing, not panel motion.
  if (url.endsWith('/kit/overlays/index.ts')) return module("export const PanelPresence = ({children}) => children; export const useLeavingPresence = value => ({shown: value ?? undefined, leaving: false});");
  if (url.endsWith('.tsx')) {
    let source = transformTsx(url);
    if (url.endsWith('/screens/live/LiveChrome.tsx')) source = source.replace(/from (['"])react\1/g, `from ${JSON.stringify(hookModule)}`);
    return module(source);
  }
  return next(url, context);
}});
const {LiveChrome} = await import('../src/screens/live/LiveChrome.tsx');
test.after(() => {loader.deregister(); delete (globalThis as Record<string, unknown>)[bridgeKey];});

function byRole(tree: ReactNode, role: string): any {
  for (const child of Children.toArray(tree)) if (isValidElement<any>(child)) {
    if (child.props.role === role) return child;
    const found = byRole(child.props.children, role); if (found) return found;
  }
}

const scope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
const fence = (channelId: string) => ({kind: 'channel', playback: {scope, run: 1}, sessionId: 'session', generation: 1,
  channelKind: 'live-source', sourceId: 'source', channelId, channelGeneration: `generation-${channelId}`}) as never;
const windowFor = (origin: number): LiveChromeWindow => ({mediaOriginMs: origin, programmeStartMs: origin, programmeEndMs: origin + 3_600_000,
  seekStartMs: origin, seekEndMs: origin + 600_000, liveEdgeMs: origin + 600_000, positionMs: origin + 300_000, behindSeconds: 300, atLive: false});
const port = (channelId: string, origin: number): LiveChromePort => ({fence: fence(channelId),
  channel: {id: channelId, sourceId: 'source', kind: 'live-source', generation: `generation-${channelId}`, number: '101', name: `Channel ${channelId}`,
    favorite: false, recordAvailable: false, tuneAvailable: true},
  programme: {id: `programme-${channelId}`, title: `Programme ${channelId}`, startMs: origin, endMs: origin + 3_600_000},
  window: windowFor(origin), miniGuide: [], skipBackSeconds: 30, skipForwardSeconds: 30,
  isCurrent: () => true, record: async () => {}, goLive: async () => {}, tune: async () => {}, tuneTo: async () => {},
  openFullGuide() {}, openRecordings() {}});
const props = (live: LiveChromePort, onSeek: (seconds: number) => void) => ({port: live, playing: true, onCollapse() {}, onStop() {},
  onPlayPause() {}, onSkip() {}, onSeek, onMore() {}, right: null, reportError(error: unknown) {throw error;}});

const railElement = {getBoundingClientRect: () => ({left: 0, width: 1000})};
const pointer = () => {
  const capture = {id: 0, released: 0};
  const target = {setPointerCapture(id: number) {capture.id = id;}, hasPointerCapture(id: number) {return capture.id === id;},
    releasePointerCapture(id: number) {if (capture.id === id) capture.id = 0; capture.released++;}};
  return {capture, target};
};

test('pointer-down on one channel, rerender on another, pointer-up seeks neither channel', () => {
  const frame = new HookFrame(), seeks: number[] = [], events = pointer();
  let tree = frame.render(() => LiveChrome(props(port('a', 1_000_000), seconds => seeks.push(seconds))));
  byRole(tree, 'slider').props.ref.current = railElement;
  byRole(tree, 'slider').props.onPointerDown({pointerId: 1, clientX: 100, currentTarget: events.target});
  tree = frame.render(() => LiveChrome(props(port('b', 2_000_000), seconds => seeks.push(seconds))));
  byRole(tree, 'slider').props.onPointerUp({pointerId: 1, currentTarget: events.target});
  assert.deepEqual(seeks, [], 'the released pointer never seeks the channel it did not start on');
  assert.equal(events.capture.released, 1);
  frame.unmount();
});

test('the held rail drag keeps the media origin captured at pointer-down', () => {
  const frame = new HookFrame(), seeks: number[] = [], events = pointer();
  let tree = frame.render(() => LiveChrome(props(port('a', 1_000_000), seconds => seeks.push(seconds))));
  byRole(tree, 'slider').props.ref.current = railElement;
  byRole(tree, 'slider').props.onPointerDown({pointerId: 1, clientX: 100, currentTarget: events.target});
  tree = frame.render(() => LiveChrome(props(port('a', 1_100_000), seconds => seeks.push(seconds))));
  byRole(tree, 'slider').props.onPointerUp({pointerId: 1, currentTarget: events.target});
  assert.deepEqual(seeks, [360], 'the seek is measured from the captured origin, not a later render');
  frame.unmount();
});

test('a full-fence replacement during a drag cancels the scrub before pointer-up', () => {
  const frame = new HookFrame(), seeks: number[] = [], events = pointer();
  let tree = frame.render(() => LiveChrome(props(port('a', 1_000_000), seconds => seeks.push(seconds))));
  byRole(tree, 'slider').props.ref.current = railElement;
  byRole(tree, 'slider').props.onPointerDown({pointerId: 1, clientX: 100, currentTarget: events.target});
  tree = frame.render(() => LiveChrome(props(port('b', 2_000_000), seconds => seeks.push(seconds))));
  byRole(tree, 'slider').props.onPointerMove({pointerId: 1, clientX: 900, currentTarget: events.target});
  tree = frame.render(() => LiveChrome(props(port('b', 2_000_000), seconds => seeks.push(seconds))));
  assert.equal(byRole(tree, 'slider').props['aria-valuenow'], 300, 'the replaced channel keeps its own position, not the dragged value');
  byRole(tree, 'slider').props.onPointerUp({pointerId: 1, currentTarget: events.target});
  assert.deepEqual(seeks, []);
  frame.unmount();
});

test('a held rail drag tells the player it is dragging until release or cancel, once each way', () => {
  const frame = new HookFrame(), seeks: number[] = [], drags: boolean[] = [], events = pointer();
  const withDrag = (live: LiveChromePort) => ({...props(live, seconds => seeks.push(seconds)), onDragChange: (dragging: boolean) => drags.push(dragging)});
  let tree = frame.render(() => LiveChrome(withDrag(port('a', 1_000_000))));
  byRole(tree, 'slider').props.ref.current = railElement;
  byRole(tree, 'slider').props.onPointerDown({pointerId: 1, clientX: 100, currentTarget: events.target});
  assert.deepEqual(drags, [true], 'the idle timer is told as the drag starts');
  tree = frame.render(() => LiveChrome(withDrag(port('a', 1_000_000))));
  byRole(tree, 'slider').props.onPointerUp({pointerId: 1, currentTarget: events.target});
  assert.deepEqual(drags, [true, false], 'release ends it');
  byRole(tree, 'slider').props.onPointerDown({pointerId: 2, clientX: 200, currentTarget: events.target});
  tree = frame.render(() => LiveChrome(withDrag(port('a', 1_000_000))));
  byRole(tree, 'slider').props.onPointerCancel();
  byRole(tree, 'slider').props.onPointerCancel();
  assert.deepEqual(drags, [true, false, true, false], 'a cancel ends it once; a second cancel reports nothing');
  frame.unmount();
});
