import test from 'node:test';
import assert from 'node:assert/strict';
import {VideoGestureOwner} from '../src/screens/watch/video-gesture-owner';
import type {VideoSurfacePaint, VideoPointer} from '../src/kit/dock/video-gesture-port';

// Regression SOURCE UNRUN. This models node/job ownership, not a browser or native-paint acceptance test.
class Job {
  finished: Promise<void>; done!: () => void;
  cancelled = false;
  constructor(readonly frames: Keyframe[], readonly options: KeyframeAnimationOptions) {this.finished = new Promise(resolve => {this.done = resolve;});}
  cancel() {this.cancelled = true;}
}
class Node {
  isConnected = true;parentElement: Node | null = null;inert = false;
  style = {width: '', height: '', transform: '', opacity: '', background: '', visibility: ''};
  dataset: Record<string, string> = {};jobs: Job[] = [];captured = new Set<number>();
  constructor(public rect: {left: number; top: number; width: number; height: number}) {}
  getBoundingClientRect() {return {...this.rect};}
  contains(node: Node) {let value: Node | null = node;while (value) {if (value === this) return true;value = value.parentElement;}return false;}
  hasPointerCapture(id: number) {return this.captured.has(id);}
  releasePointerCapture(id: number) {this.captured.delete(id);}
  animate(frames: Keyframe[], options: KeyframeAnimationOptions) {const job = new Job(frames, options);this.jobs.push(job);return job as unknown as Animation;}
}
Object.assign(globalThis, {Element: Node, document: {activeElement: null}, getComputedStyle: (node: Node) => ({opacity: node.style.opacity || '1'})});
const pointer = (y: number, id = 4, timeStamp = 1000): VideoPointer => ({pointerId: id, clientX: 30, clientY: y, pointerType: 'touch', button: 0, timeStamp});
const html = (node: Node) => node as unknown as HTMLElement;
const rect = {left: 0, top: 0, width: 390, height: 220};
function fixture(reduced = false, start: 0 | 1 = 0) {
  const screen = new Node({...rect}), full = new Node({...rect}), surface = new Node({...rect}), bar = new Node({left: 8, top: 700, width: 374, height: 62});
  const dock = new Node({left: 15, top: 710, width: 80, height: 45}), picture = new Node({...rect}), chrome = new Node({...rect});
  const parent = new Node({...rect});screen.parentElement = parent;surface.parentElement = parent;bar.parentElement = parent;
  full.parentElement = screen;picture.parentElement = screen;chrome.parentElement = screen;dock.parentElement = bar;
  let current = true, quiet = reduced, resumed = 0, expanded = start === 0;
  const actions: boolean[] = [], states: boolean[] = [];
  let paint: VideoSurfacePaint = {element: surface as unknown as HTMLDivElement, freeze: () => surface.getBoundingClientRect(), resume: () => {resumed++;}};
  const nodes = () => ({surface: paint, dock: html(dock), full: html(full), screen: html(screen), bar: html(bar), copies: [html(chrome)]});
  const owner = new VideoGestureOwner({current: () => current, nodes, expanded: () => expanded, reduced: () => quiet, state: (preview, active) => {states.push(active);}, commit: next => {actions.push(next);expanded = next;return true;}});
  const jobs = () => [surface, screen, bar, chrome].flatMap(node => node.jobs).filter(job => !job.cancelled);
  return {owner, screen, full, surface, bar, dock, picture, chrome, actions, states, jobs, nodes,
    retire: () => {current = false;}, allow: () => {current = true;}, reduce: () => {quiet = true;owner.motionChanged();}, resumeCount: () => resumed,
    replacePort: () => {paint = {...paint};}, expandElsewhere: () => {expanded = !expanded;}, acknowledge: () => owner.committed(expanded)};
}
async function finish(jobs: Job[]) {jobs.forEach(job => job.done());await Promise.all(jobs.map(job => job.finished));await Promise.resolve();await Promise.resolve();}
function drag(f: ReturnType<typeof fixture>, start: 0 | 1, from: number, to: number) {
  assert.equal(f.owner.begin(pointer(from), html(start ? f.bar : f.picture), start), true);
  assert.equal(f.owner.move(pointer(to, 4, 1200)), true);f.owner.refresh();
  return f.owner.release(pointer(to, 4, 1400));
}

test('a real downward drag settles before the one original collapse; paint waits for that commit', async () => {
  const f = fixture();assert.equal(drag(f, 0, 0, 300), true);assert.deepEqual(f.actions, []);
  await finish(f.jobs());assert.deepEqual(f.actions, [false]);assert.ok(f.surface.dataset.videoGesture);
  f.acknowledge();assert.equal(f.surface.dataset.videoGesture, undefined);assert.equal(f.resumeCount(), 1);
  await finish(f.jobs());assert.deepEqual(f.actions, [false]);f.owner.dispose();
});
test('the same real slots reverse upward into the original full view', async () => {
  const f = fixture(false, 1);f.surface.rect = {...f.dock.rect};
  assert.equal(drag(f, 1, 700, 400), true);await finish(f.jobs());assert.deepEqual(f.actions, [true]);
  f.acknowledge();assert.equal(f.screen.inert, false);f.owner.dispose();
});
test('a short release returns without committing; no second media command is made', async () => {
  const f = fixture();drag(f, 0, 0, 40);await finish(f.jobs());assert.deepEqual(f.actions, []);f.owner.dispose();
});
test('original source retirement before a held native settle refuses every action', async () => {
  const f = fixture();drag(f, 0, 0, 300);const held = f.jobs();f.retire();f.owner.dispose();await finish(held);
  assert.deepEqual(f.actions, []);assert.equal(f.surface.dataset.videoGesture, undefined);
});
test('replacement surface port cannot receive the old settle or lend a collapse', async () => {
  const f = fixture();drag(f, 0, 0, 300);const held = f.jobs();f.replacePort();await finish(held);assert.deepEqual(f.actions, []);f.owner.dispose();
});
test('reparenting the original slot refuses the old pointer even when the key is unchanged', () => {
  const f = fixture();assert.equal(f.owner.begin(pointer(0), html(f.picture), 0), true);
  f.dock.parentElement = new Node({...rect});assert.equal(f.owner.move(pointer(300)), false);assert.deepEqual(f.actions, []);f.owner.dispose();
});
test('actual slot movement during settle cannot commit against replacement geometry', async () => {
  const f = fixture();drag(f, 0, 0, 300);f.dock.rect.top += 12;await finish(f.jobs());assert.deepEqual(f.actions, []);f.owner.dispose();
});
test('a second pointer cannot adopt the first pointer owner', () => {
  const f = fixture();assert.equal(f.owner.begin(pointer(0), html(f.picture), 0), true);
  assert.equal(f.owner.begin(pointer(0, 8), html(f.picture), 0), false);assert.equal(f.owner.move(pointer(300, 8)), false);
  assert.equal(f.owner.move(pointer(300)), true);f.owner.dispose();
});
test('pointer cancellation returns original paint and cannot commit a collapse', async () => {
  const f = fixture();f.owner.begin(pointer(0), html(f.picture), 0);f.owner.move(pointer(300, 4, 1200));
  assert.equal(f.owner.release(pointer(300, 4, 1400), true), true);await finish(f.jobs());assert.deepEqual(f.actions, []);f.owner.dispose();
});
test('missing finite full geometry leaves the existing discrete path alone', () => {
  const f = fixture();f.full.rect.height = 0;f.owner.begin(pointer(0), html(f.picture), 0);f.owner.move(pointer(300));
  assert.equal(f.owner.release(pointer(300)), false);assert.deepEqual(f.actions, []);assert.equal(f.jobs().length, 0);f.owner.dispose();
});
test('quiet release retains actual opacity and uses only opacity keyframes for160ms', async () => {
  const f = fixture(true);f.surface.style.opacity = '.42';drag(f, 0, 0, 300);
  const out = f.jobs();assert.ok(out.every(job => job.options.duration === 80));
  assert.equal(f.surface.jobs[0].frames[0].opacity, .42);
  assert.ok(out.every(job => job.frames.every(frame => !('transform' in frame) && !('width' in frame))));
  await finish(out);const enter = f.jobs().filter(job => !out.includes(job));assert.ok(enter.every(job => job.options.duration === 80));
  await finish(enter);assert.deepEqual(f.actions, [false]);f.acknowledge();f.owner.dispose();
});
test('an old job completion cannot clear the successor paint marker', async () => {
  const f = fixture();drag(f, 0, 0, 300);const held = f.jobs();f.surface.dataset.videoGesture = 'successor';
  await finish(held);assert.equal(f.surface.dataset.videoGesture, 'successor');assert.deepEqual(f.actions, []);f.owner.dispose();
});

test('another actual expansion intent retires the old gesture before its held native finish', async () => {
  const f = fixture();drag(f, 0, 0, 300);const held = f.jobs();f.expandElsewhere();f.owner.observe();await finish(held);
  assert.deepEqual(f.actions, []);f.owner.dispose();
});
test('an interrupted settle cannot commit from its cancelled original jobs', async () => {
  const f = fixture();drag(f, 0, 0, 300);const held = f.jobs();
  assert.equal(f.owner.begin(pointer(300, 9, 1500), html(f.picture), 0), true);
  await finish(held);assert.deepEqual(f.actions, []);f.owner.dispose();
});

test('a queued older pointer event cannot release the new same-id pointer lease', () => {
  const f = fixture();assert.equal(f.owner.begin(pointer(0, 4, 2000), html(f.picture), 0), true);
  assert.equal(f.owner.move(pointer(300, 4, 1000)), false);assert.equal(f.owner.release(pointer(300, 4, 1000)), false);
  assert.equal(f.owner.move(pointer(300, 4, 2200)), true);assert.deepEqual(f.actions, []);f.owner.dispose();
});
test('turning on quiet motion cancels the old spring jobs and cannot renew their deadline', async () => {
  const f = fixture();drag(f, 0, 0, 300);const springs = f.jobs();f.reduce();
  assert.ok(springs.every(job => job.cancelled));const fades = f.jobs();assert.ok(fades.every(job => Number(job.options.duration) <= 80));
  assert.ok(fades.every(job => job.frames.every(frame => !('transform' in frame))));
  await finish(springs);assert.deepEqual(f.actions, []);f.owner.dispose();
});

test('a temporary presentation gate cancels old paint but can admit a fresh same-source pointer', async () => {
  const f = fixture();drag(f, 0, 0, 300);const held = f.jobs();f.retire();f.owner.cancel();f.allow();
  assert.equal(f.owner.begin(pointer(0, 9, 2000), html(f.picture), 0), true);
  await finish(held);assert.deepEqual(f.actions, []);f.owner.dispose();
});
