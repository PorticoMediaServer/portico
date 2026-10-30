import test from 'node:test';
import assert from 'node:assert/strict';
import {createPageTurnAnimation, pageGesture, pageOut, settleFrames, type PageTurnPaint} from '../src/epub/page-motion.ts';
import {captureReaderMotion, watchReaderMotion} from '../src/motion-policy.ts';

function fixture(options: {from?: number; to?: number; velocity?: number; spring?: boolean; reduced?: boolean} = {}) {
  let sequence = 0, live = true, reduce = options.reduced ?? false;
  const frames = new Map<number, (now: number) => void>(), paints: PageTurnPaint[] = [], commits: number[] = [];
  const animation = createPageTurnAnimation({from: options.from ?? 0, to: options.to ?? -100, offset: 200, velocity: options.velocity, spring: options.spring,
    ports: {frame(callback) {const id = ++sequence;frames.set(id, callback);return id;}, cancelFrame: id => {frames.delete(id);}, current: () => live,
      reduced: () => reduce, paint: value => paints.push(value), commit: value => commits.push(value)}});
  const step = (now: number) => {const work = [...frames.values()];frames.clear();for (const callback of work) callback(now);};
  return {animation, frames, paints, commits, step, retire() {live = false;}, reduce() {reduce = true;}};
}

test('tap turns use canonical320/out and commit one real offset only after transform completes', async () => {
  const f = fixture();assert.equal(f.frames.size, 0);assert.equal(f.paints.length, 0);f.animation.start();
  f.step(0);f.step(160);assert.ok(Math.abs(f.paints.at(-1)!.translation + 100 * pageOut(.5)) < .001);assert.deepEqual(f.commits, []);
  f.step(319);assert.deepEqual(f.commits, []);f.step(320);assert.equal(await f.animation.finished, true);
  assert.deepEqual(f.commits, [200]);assert.deepEqual(f.paints.at(-1), {translation: 0, opacity: 1});assert.equal(f.frames.size, 0);
});

test('quarter-width and actual flick decisions mirror RTL, retaining canceled physical gestures', () => {
  assert.equal(pageGesture(400, -99, 0), 0);assert.equal(pageGesture(400, -100, 0), 1);assert.equal(pageGesture(400, 100, 0), -1);
  assert.equal(pageGesture(400, -10, -600), 1);assert.equal(pageGesture(400, 10, 600), -1);
  assert.equal(pageGesture(400, 100, 0, true), 1);assert.equal(pageGesture(400, -100, 0, true), -1);
  assert.equal(pageGesture(0, -100, 0), 0);assert.equal(pageGesture(400, NaN, 1000), 0);
});

test('release spring starts from the finger paint and its physical velocity', async () => {
  const a = fixture({from: -30, velocity: -1000, spring: true}), b = fixture({from: -30, velocity: 0, spring: true});
  a.animation.start();b.animation.start();a.step(0);b.step(0);assert.equal(a.paints[0].translation, -30);
  a.step(16);b.step(16);assert.ok(a.paints.at(-1)!.translation < b.paints.at(-1)!.translation);
  for (let time = 32; time <= 3104; time += 16) {a.step(time);b.step(time);}
  assert.equal(await a.animation.finished, true);assert.equal(await b.animation.finished, true);assert.deepEqual(a.commits, [200]);
});

test('reduced release fades160, snaps at zero opacity, and never interpolates transforms', async () => {
  const f = fixture({from: -30, reduced: true, spring: true});f.animation.start();f.step(0);f.step(40);
  assert.equal(f.paints.at(-1)!.translation, -30);assert.equal(f.paints.at(-1)!.opacity, .5);assert.deepEqual(f.commits, []);
  f.step(80);assert.deepEqual(f.commits, [200]);assert.deepEqual(f.paints.at(-1), {translation: 0, opacity: 0});
  f.step(160);assert.equal(await f.animation.finished, true);assert.deepEqual(f.paints.at(-1), {translation: 0, opacity: 1});
});

test('switching to reduce during an original turn shortens it from current paint', async () => {
  const f = fixture();f.animation.start();f.step(0);f.step(80);const physical = f.paints.at(-1)!.translation;
  f.reduce();f.step(81);f.step(120);assert.equal(f.paints.at(-1)!.translation, physical);
  f.step(161);assert.deepEqual(f.commits, [200]);f.step(241);assert.equal(await f.animation.finished, true);
});

test('retired/canceled turns settle and cannot paint or commit a later mounted view', async () => {
  const f = fixture();f.animation.start();f.step(0);const saved = [...f.frames.values()];f.retire();f.animation.cancel();
  const count = f.paints.length;for (const callback of saved) callback(320);
  assert.equal(await f.animation.finished, false);assert.equal(f.paints.length, count);assert.deepEqual(f.commits, []);assert.equal(f.frames.size, 0);
});

test('a true current result after reentrant cancel cannot revive the original turn', async () => {
  let callback!: (now: number) => void, paints = 0;
  let first = true;
  const other = createPageTurnAnimation({from: 0, to: -100, offset: 200, ports: {frame: value => {callback = value;return 2;}, cancelFrame() {},
    current: () => {if (first) {first = false;other.cancel();}return true;}, reduced: () => false, paint: () => {paints++;}, commit: () => assert.fail('retired commit')}});
  other.start();callback(0);assert.equal(await other.finished, false);assert.equal(paints, 1, 'only original explicit cancellation cleanup may paint');
});

test('motion capture binds original callbacks and the system can never be weakened', () => {
  let app = false, listener: (() => void) | undefined, stopped = 0;
  const raw = {reduced: () => app, subscribe: (read: () => void) => {listener = read;return () => {stopped++;};}};
  const original = captureReaderMotion(raw)!;raw.reduced = () => true;
  const system = {matches: true, addEventListener() {}, removeEventListener() {}};
  const changes: boolean[] = [], watch = watchReaderMotion(original, system, value => changes.push(value));
  assert.equal(watch.reduced(), true);system.matches = false;assert.equal(watch.reduced(), false);
  app = true;listener!();assert.deepEqual(changes, [true]);watch.dispose();watch.dispose();listener!();assert.equal(stopped, 1);assert.deepEqual(changes, [true]);
});

test('unknown or refusing policy reduces without affecting source authority', () => {
  const system = {matches: false, addEventListener() {}, removeEventListener() {}};
  for (const raw of [{reduced: () => {throw new Error('refused');}, subscribe: () => () => {}}, {reduced: () => false, subscribe: () => {throw new Error('refused');}}]) {
    const watch = watchReaderMotion(captureReaderMotion(raw), system, () => {});assert.equal(watch.reduced(), true);watch.dispose();
  }
  assert.equal(watchReaderMotion(undefined, undefined, () => {}).reduced(), true);
});

test('a released swipe settles on the settle spring from the finger, carrying its speed, and ends exactly at rest', () => {
  const slow = settleFrames(-300, -1000, 0), fast = settleFrames(-300, -1000, -2000);
  assert.equal(slow.values[0], -300);assert.equal(slow.values.at(-1), -1000);assert.equal(fast.values.at(-1), -1000);
  assert.ok(fast.values[1]! < slow.values[1]!, 'a faster finger travels further in the first frame');
  assert.ok(slow.duration > 0 && slow.duration <= 3000);
  const back = settleFrames(180, 0, 0);
  assert.equal(back.values.at(-1), 0);assert.ok(back.values.every(x => x > -40), 'springing back barely overshoots');
});
