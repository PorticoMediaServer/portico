import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactNode} from 'react';
import {PlaybackDock, type QueuePlaybackFence} from '../../packages/player-core/src/index.ts';
import type {DockAction, DockRendererProps} from '../src/kit/dock/types';
import type {VideoGesturePort} from '../src/kit/dock/video-gesture-port';
import {dockView, dispatchDockAction, commandFor} from '../src/kit/dock/model';
import {transformTsx} from './transform-tsx.mjs';

// SOURCE UNRUN: actual MediaBar handlers with a supplied private port. Native
// painting/scope ownership is tested separately in video-gesture-owner source.
const bridge = {slots: [] as any[], cursor: 0, dispatchDockAction, commandFor};
(globalThis as any).__mediaBarComposition = bridge;
const hooks = `const h=globalThis.__mediaBarComposition;
export const useRef=v=>h.slots[h.cursor++]??=( {current:v} );
export const useState=v=>{const i=h.cursor++;h.slots[i]??={value:typeof v==='function'?v():v};return[h.slots[i].value,n=>{h.slots[i].value=typeof n==='function'?n(h.slots[i].value):n;}];};
export const useEffect=()=>{};export const useLayoutEffect=()=>{};`;
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get:(_,key)=>String(key)});');
  if (url.endsWith('/kit/dock/use-motion.ts')) return module('export const useDockReducedMotion=()=>false,useDockHandover=()=>{};');
  if (url.endsWith('/kit/dock/parts.tsx')) return module(`const h=globalThis.__mediaBarComposition;
    export const Control='control',Identity='identity',Progress='progress',StartingStop='starting-stop',TerminalActions='terminal',Transport='transport';
    export const useDockActions=({view,binding})=>action=>h.dispatchDockAction(binding,h.commandFor(view,action));`);
  if (url.endsWith('/kit/overlays/index.ts')) return module("export const Menu='menu';");
  if (url.endsWith('/kit/dock/MediaBar.tsx')) return module(transformTsx(url).replace(/from (['"])react\1/g, `from ${JSON.stringify('data:text/javascript,' + encodeURIComponent(hooks))}`));
  return next(url, context);
}});
const {MediaBar} = await import('../src/kit/dock/MediaBar.tsx');
test.after(() => {loader.deregister();delete (globalThis as any).__mediaBarComposition;});
class Root {
  style = {transform: '', opacity: ''};clientWidth = 390;captures = new Set<number>();
  finished: (() => void)[] = [];
  closest() {return null;}
  getBoundingClientRect() {return {left: 0, top: 700, width: 390, height: 62};}
  setPointerCapture(id: number) {this.captures.add(id);}
  hasPointerCapture(id: number) {return this.captures.has(id);}
  releasePointerCapture(id: number) {this.captures.delete(id);}
  dispatchEvent() {return true;}
  animate() {return {cancel() {}, finished: new Promise<void>(resolve => this.finished.push(resolve))};}
}
const original = {Element: globalThis.Element, getComputedStyle: globalThis.getComputedStyle};
Object.assign(globalThis, {Element: Root, getComputedStyle: (node: Root) => ({opacity: node.style.opacity || '1', transform: node.style.transform || 'none'})});
test.after(() => Object.assign(globalThis, original));
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope: {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p', controllerId: 'c', controllerEpoch: 'e', commandLaneId: 'l'}, run: 1}, sessionId: 'session', generation: 1, itemId: 'i', sourceId: 'source', entryId: 'entry'};
function section(tree: ReactNode): any {
  for (const node of Children.toArray(tree)) if (isValidElement<any>(node)) {if (node.type === 'section') return node;const found = section((node.props as {children?: ReactNode}).children);if (found) return found;}
}
function fixture(port: VideoGesturePort) {
  const dock = new PlaybackDock(fence.playback.scope);dock.start({kind: 'video', fence, queue: null, expanded: false, playing: true, positionSeconds: 10});
  const presentation = {fence, identity: {id: 'i', kind: 'video' as const, title: 'Original', subtitle: []}, controls: ['pause'] as const, skipBackSeconds: 10, skipForwardSeconds: 30};
  const actions: DockAction[] = [], binding = {getSnapshot: dock.getSnapshot, onAction: (action: DockAction) => actions.push(action)};
  const props: DockRendererProps = {view: dockView(dock.getSnapshot(), [presentation])!, binding, videoGesture: port};
  bridge.slots = [];bridge.cursor = 0;const node = section(MediaBar(props)), root = new Root();node.props.ref.current = root;
  const event = (x: number, y: number, timeStamp: number) => ({pointerId: 7, pointerType: 'touch', button: 0, currentTarget: root, target: root, clientX: x, clientY: y, timeStamp, preventDefault() {}});
  const swipe = (dx: number, dy: number) => {node.props.onPointerDown(event(0, 700, 1000));node.props.onPointerMove(event(dx, 700 + dy, 1200));node.props.onPointerUp(event(dx, 700 + dy, 1400));};
  return {swipe, actions, root};
}
const refused = (begin: boolean): VideoGesturePort => ({begin: () => begin, move: () => begin, release: () => false, cancel() {}});
test('installed port missing full geometry is terminal for a moved upward gesture', () => {
  const f = fixture(refused(true));f.swipe(0, -300);assert.equal(f.actions.length, 0);
});
test('installed port refused begin cannot borrow the legacy upward expand action', () => {
  const f = fixture(refused(false));f.swipe(0, -300);assert.equal(f.actions.length, 0);
});
test('an admitted original port delegates exactly once after its settle, not on MediaBar release', () => {
  let settle: (() => void) | undefined;
  const port: VideoGesturePort = {begin: () => true, move: () => true, cancel() {}, release: () => {settle = () => {f.actions.push({kind: 'expand', fence});};return true;}};
  const f = fixture(port);f.swipe(0, -300);assert.equal(f.actions.length, 0);settle!();assert.equal(f.actions.length, 1);assert.equal(f.actions[0].kind, 'expand');
});
test('a horizontal dismiss still uses the original guarded stop path', async () => {
  const f = fixture(refused(false));f.swipe(220, 0);assert.equal(f.actions.length, 0);
  f.root.finished.forEach(resolve => resolve());await Promise.resolve();await Promise.resolve();
  assert.deepEqual(f.actions.map(action => action.kind), ['stop']);
});
