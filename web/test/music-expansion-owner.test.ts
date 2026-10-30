import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {springTiming, springs} from '@design';
import {MusicExpansionPaint, type MusicPaintNodes} from '../src/screens/watch/music/expansion-paint.ts';
import {claimDockWords, dockWordsOwner, releaseDockWords} from '../src/kit/dock/dock-words.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer} from './helpers/interaction-harness.mjs';
import {fenceKey} from '../src/shell/playback-model.ts';

// UNRUN production visual-owner vectors. DOM/WAAPI/style observations below are controlled;
// they are not actual browser geometry, a fake playable Session, or music runtime acceptance.
type Box = {left: number; top: number; width: number; height: number};
class Job {
  onfinish: (() => void) | null = null;
  canceled = 0;
  constructor(readonly frames: Keyframe[], readonly options: KeyframeAnimationOptions) {}
  cancel() {this.canceled++;}
}
class Matrix {
  is2D = true;a: number;b: number;c: number;d: number;e: number;f: number;
  constructor(value: string) {const values = value.slice(value.indexOf('(') + 1, -1).split(',').map(Number);[this.a,this.b,this.c,this.d,this.e,this.f] = values;}
}
const document = {};
class Node {
  isConnected = true;
  ownerDocument = document;
  style = {opacity: '', transform: '', width: '', height: '', visibility: '', borderRadius: '', boxShadow: ''};
  paint = {opacity: '1', transform: 'none', borderRadius: '8px'};
  dataset: {musicContentGroup?: string} = {};
  attributes = new Set<string>();
  children: Node[] = [];
  parentElement: Node | null = null;
  jobs: Job[] = [];
  decoded = true;
  image = {isConnected: true, complete: true, naturalWidth: 560};
  constructor(public box: Box) {}
  getBoundingClientRect() {
    // Native DOMRect fields are not enumerable; a spread must not erase width/height.
    const value = {} as Box;
    for (const field of ['left','top','width','height'] as const) Object.defineProperty(value, field, {get: () => this.box[field]});
    return value;
  }
  querySelector() {return this.decoded ? this.image : null;}
  contains(value: unknown) {return value === this || this.children.includes(value as Node);}
  hasAttribute(name: string) {return this.attributes.has(name);}
  animate(frames: Keyframe[], options: KeyframeAnimationOptions) {const job = new Job(frames, options);this.jobs.push(job);return job as unknown as Animation;}
}
const dom = (node: Node) => node as unknown as HTMLElement;
function fixture(t: TestContext) {
  const overrides = {getComputedStyle: (node: Node) => ({...node.paint, getPropertyValue: () => 'none'}), DOMMatrixReadOnly: Matrix};
  for (const [name, value] of Object.entries(overrides)) {
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);Object.defineProperty(globalThis, name, {configurable: true, writable: true, value});
    t.after(() => {if (descriptor) Object.defineProperty(globalThis, name, descriptor);else Reflect.deleteProperty(globalThis, name);});
  }
  let now = 100, live = true, collapsed = 0;t.mock.method(performance, 'now', () => now);
  const dock = new Node({left: 12, top: 700, width: 56, height: 56});
  const surface = new Node({...dock.box}), frame = new Node({left: 0, top: 0, width: 1440, height: 900});
  const cover = new Node({left: 260, top: 100, width: 480, height: 480});cover.paint.borderRadius = '14px';frame.children = [cover];
  const copy = new Node({...dock.box});copy.attributes.add('data-dock-identity');
  const groups = [0,1,2].map(index => {const node = new Node({...cover.box});node.dataset.musicContentGroup = String(index);return node;});
  const nodes: MusicPaintNodes = {dock: dom(dock), surface: dom(surface), frame: dom(frame), cover: dom(cover), copies: [dom(copy)], groups: groups.map(dom)};
  const owner = new MusicExpansionPaint(() => live, () => {collapsed++;});t.after(() => owner.dispose());
  const closed = () => owner.update({...nodes, frame: undefined, cover: undefined, groups: []}, false, false);
  return {owner, nodes, dock, surface, frame, cover, copy, groups, closed, now: (value: number) => {now = value;}, retire: () => {live = false;}, collapsed: () => collapsed};
}

test('initial collapsed placement leaves the native entry words owner and its staging alone', t => {
  const f = fixture(t), native = Symbol();claimDockWords(dom(f.copy), native);t.after(() => releaseDockWords(dom(f.copy), native));
  f.closed();assert.equal(f.surface.style.transform, 'translate(12px, 700px)');assert.equal(f.surface.jobs.length, 0);
  assert.equal(f.copy.jobs.length, 0);assert.equal(dockWordsOwner(dom(f.copy)), native);
});
test('collapsed cover follows the measured slot and current ancestor opacity without creating another entry animation', t => {
  const f = fixture(t), bar = new Node({...f.dock.box});f.dock.parentElement = bar;
  f.owner.update({...f.nodes, frame: undefined, cover: undefined, groups: [], bar: dom(bar)}, false, false);
  f.dock.box.left = 94;f.dock.box.top = 730;bar.paint.opacity = '.42';
  f.owner.update({...f.nodes, frame: undefined, cover: undefined, groups: [], bar: dom(bar)}, false, false);
  assert.equal(f.surface.style.transform, 'translate(94px, 730px)');assert.equal(f.surface.style.opacity, '0.42');
  bar.paint.opacity = '.8';f.owner.update({...f.nodes, frame: undefined, cover: undefined, groups: [], bar: dom(bar)}, false, false);
  assert.equal(f.surface.style.opacity, '0.8');assert.equal(f.surface.jobs.length, 0);assert.equal(f.copy.jobs.length, 0);
});
test('one decoded cover uses measured slots and authored player/content timing', t => {
  const f = fixture(t);f.closed();f.owner.update(f.nodes, true, false);
  const job = f.surface.jobs.at(-1)!;assert.equal(job.options.duration, springTiming(springs.player).durationMs);assert.equal(job.frames.length, 49);
  assert.equal(job.frames[0].transform, `translate(12px, 700px) scale(${56 / 480}, ${56 / 480})`);assert.ok(String(job.frames.at(-1)!.transform).startsWith('translate(260px, 100px)'));
  assert.deepEqual(f.groups.map(node => node.jobs.at(-1)!.options.delay), [260,320,380]);
  assert.ok(f.groups.every(node => node.jobs.at(-1)!.options.duration === 180));
  job.onfinish?.();
  assert.equal(f.surface.style.width, '480px');assert.equal(f.surface.style.height, '480px');
  assert.equal(f.surface.style.transform, 'translate(260px, 100px)');assert.equal(f.surface.style.borderRadius, '14px');
  assert.equal(job.canceled, 1, 'settlement releases the scale job at the actual target size');
});
test('reverse and re-expand start from painted geometry; old completion cannot retire the new opening', t => {
  const f = fixture(t);f.closed();f.owner.update(f.nodes, true, false);
  f.now(250);f.surface.box = {left: 80, top: 430, width: 240, height: 240};f.frame.paint.transform = 'matrix(1,0,0,1,0,300)';f.cover.box.top = 400;
  f.owner.update(f.nodes, false, false);const exit = f.surface.jobs.at(-1)!, staleFinish = exit.onfinish;
  assert.ok(String(exit.frames[0].transform).startsWith('translate(80px, 430px) scale(0.5, 0.5)'));
  f.now(300);f.owner.update(f.nodes, true, false);const returning = f.surface.jobs.at(-1)!;
  assert.ok(String(returning.frames.at(-1)!.transform).startsWith('translate(260px, 100px)'), 'actual screen translation is removed from the target measurement');
  staleFinish?.();assert.equal(f.collapsed(), 0);assert.equal(returning.canceled, 0);
});
test('active reduction drops geometry travel and all group delays from the current paint', t => {
  const f = fixture(t);f.closed();f.owner.update(f.nodes, true, false);f.surface.box = {left: 80, top: 430, width: 240, height: 240};
  f.groups[0].paint.opacity = '.37';f.owner.update(f.nodes, true, true);
  const job = f.surface.jobs.at(-1)!;assert.equal(job.options.duration, 160);assert.equal(f.surface.style.transform, 'translate(260px, 100px)');
  assert.ok(f.groups.every(node => node.jobs.at(-1)!.options.delay === 0 && node.jobs.at(-1)!.options.easing === 'linear'));
  assert.equal(f.groups[0].jobs.at(-1)!.frames[0].opacity, .37);
});
test('missing decoded art or unmeasurable target never manufactures a cover path', t => {
  const f = fixture(t);f.closed();f.surface.decoded = false;f.owner.update(f.nodes, true, false);
  assert.equal(f.surface.jobs.at(-1)!.frames.length, 2);assert.ok(f.surface.jobs.at(-1)!.frames.every(frame => frame.transform === undefined));
  const count = f.surface.jobs.length;f.cover.box.width = 0;f.owner.update(f.nodes, true, false);assert.equal(f.surface.jobs.length, count);
  assert.equal(f.surface.style.visibility, 'hidden');assert.ok(f.surface.jobs.at(-1)!.canceled > 0);
});
test('retirement and successor ownership block old queued finish and cleanup', t => {
  const f = fixture(t);f.closed();f.owner.update(f.nodes, false, false);f.owner.update(f.nodes, true, false);f.owner.update(f.nodes, false, false);
  const stale = f.surface.jobs.at(-1)!.onfinish;
  const successor = new MusicExpansionPaint(() => true, () => {});t.after(() => successor.dispose());
  successor.update({...f.nodes, frame: undefined, cover: undefined, groups: []}, false, false);successor.update(f.nodes, true, false);
  const current = f.surface.jobs.at(-1)!;f.surface.style.opacity = '.63';f.retire();f.owner.dispose();stale?.();
  assert.equal(f.collapsed(), 0);assert.equal(current.canceled, 0);assert.equal(f.surface.style.opacity, '.63');
});
test('actual replaced frame nodes release old group jobs without replaying a new opening delay', t => {
  const f = fixture(t);f.closed();f.owner.update(f.nodes, true, false);const oldGroupJob = f.groups[0].jobs.at(-1)!;
  const frame = new Node({...f.frame.box}), cover = new Node({...f.cover.box}), group = new Node({...f.cover.box});group.dataset.musicContentGroup = '0';frame.children = [cover];
  f.owner.update({...f.nodes, frame: dom(frame), cover: dom(cover), groups: [dom(group)]}, true, false);
  assert.ok(oldGroupJob.canceled > 0);assert.equal(group.jobs.at(-1)!.options.delay, 0);
});

// Actual useMusicExpansion lifetime wiring through the published component
// renderer: no browser, media, clock or scheduler is started.
test('equal progress snapshots keep the original admitted music scope', async () => {
  const h = createRenderer();
  const app = await componentModule(new URL('../src/screens/watch/music/expansion-owner.tsx', import.meta.url), {
    react: h.react, '../../../shell/playback-model': {fenceKey}, './music-expansion-owner.module.css': {default: {}},
  });
  const fence = Object.freeze({kind: 'queue', playback: {run: 1, scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
    controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});
  const authority = Object.freeze({});
  let live: any;
  const binding = {runtime: {getSnapshot: () => live, subscribe: () => () => {}}, artwork: {foreground: () => undefined}};
  const ref = () => ({current: null}), localCurrent = () => true;
  const snapshot = (playbackRun = 2) => ({authority, disposed: false, fence, playback: {intentId: 4, playbackRun},
    presentation: {kind: 'music', subject: {kind: 'music', item: {id: 'song'}}, fence, posterUrl: 'poster', backdropUrl: 'backdrop'},
    dock: {remote: null, local: {expanded: false}}});
  const draw = () => h.render(() => app.useMusicExpansion(binding, live, localCurrent, ref(), ref(), false));
  const original = snapshot();
  live = original;
  const first = draw();
  assert.ok(first.scope, 'the first exact content admits a scope');
  // A progress tick republishes equal content through fresh fence and presentation
  // objects. The content key is the scope's lifetime, so the admission survives.
  live = {...original, fence: {...fence}, presentation: {...original.presentation, fence: {...fence}}};
  const second = draw();
  assert.equal(second.scope, first.scope, 'equal progress content keeps the original scope object');
  // Only a real playback change (a new run) admits a new lifetime.
  live = snapshot(3);
  const successor = draw();
  assert.notEqual(successor.scope, first.scope);
  assert.notEqual(successor.scope?.id, first.scope?.id);
});
