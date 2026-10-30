import assert from 'node:assert/strict';
import test from 'node:test';
import {createTVKeyAdapter, type TVKeyEvent} from '../src-next/platform/tv-key-adapter.ts';
import {createTVSeekRepeat} from '../src-next/platform/tv-seek-repeat.ts';

test('ordinary native releases deliver one command for every short direction/media press', () => {
  const keys = createTVKeyAdapter();
  for (const key of ['left', 'right', 'up', 'down', 'select', 'playPause', 'rewind', 'fastForward']) {
    assert.deepEqual(keys.read({eventType: key, eventKeyAction: 1, tag: 27}), {key, phase: 'press', repeat: false, source: 'native', tag: 27});
    assert.equal(keys.read({eventType: key, eventKeyAction: 1})?.phase, 'press', 'the next release-only tap is independent');
  }
});

test('optional native down/up pairs and repeated downs issue one short command', () => {
  const keys = createTVKeyAdapter();
  for (let tap = 0; tap < 3; tap++) {
    assert.equal(keys.read({eventType: 'right', eventKeyAction: 0})?.phase, 'press');
    assert.equal(keys.read({eventType: 'right', eventKeyAction: 0}), undefined);
    assert.equal(keys.read({eventType: 'right', eventKeyAction: 1}), undefined);
  }
});

test('native long presses have one begin and one end, including an ordinary direction release', () => {
  const keys = createTVKeyAdapter();
  assert.equal(keys.read({eventType: 'longLeft', eventKeyAction: 0})?.phase, 'long-begin');
  assert.equal(keys.read({eventType: 'longLeft', eventKeyAction: 0}), undefined);
  assert.equal(keys.read({eventType: 'longLeft', eventKeyAction: 1})?.phase, 'long-end');
  assert.equal(keys.read({eventType: 'left', eventKeyAction: 1})?.phase, 'press');
  assert.equal(keys.read({eventType: 'longRight'})?.phase, 'long-begin');
  assert.equal(keys.read({eventType: 'right', eventKeyAction: 1})?.phase, 'long-end');
});

test('Vega keeps its device repeats and never duplicates a paired release', () => {
  const keys = createTVKeyAdapter({source: 'vega'});
  const events = [0, 0, 0, 1].map(eventKeyAction => keys.read({eventType: 'right', eventKeyAction})).filter(Boolean);
  assert.equal(events.length, 3);
  assert.deepEqual(events.map(event => event!.repeat), [false, true, true]);
  assert.equal(keys.read({eventType: 'playPause'})?.phase, 'press', 'shim short actions arrive without a phase');
  assert.equal(keys.read({eventType: 'enter', eventKeyAction: 0})?.key, 'select', 'the direct-listener form uses SDK names');
  assert.equal(keys.read({eventType: 'enter', eventKeyAction: 1}), undefined);
});

test('non-key native notifications and unsupported phases do not become commands', () => {
  const keys = createTVKeyAdapter();
  for (const raw of [{eventType: 'focus', eventKeyAction: 0}, {eventType: 'pan'}, {eventType: 'left', eventKeyAction: 2}, {}]) assert.equal(keys.read(raw), undefined);
  keys.read({eventType: 'left', eventKeyAction: 0}); keys.reset();
  assert.equal(keys.read({eventType: 'left', eventKeyAction: 1})?.phase, 'press');
});

function clock() {
  let now = 0, sequence = 0;
  const timers = new Map<number, {at: number; callback: () => void}>();
  return {
    time: {now: () => now, schedule: (callback: () => void, milliseconds: number) => {const id = ++sequence; timers.set(id, {at: now + milliseconds, callback}); return id;}, cancel: (id: unknown) => {timers.delete(id as number);}},
    advance(milliseconds: number) {
      const end = now + milliseconds;
      for (;;) {
        const next = [...timers].filter(([, timer]) => timer.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        now = next[1].at; timers.delete(next[0]); next[1].callback();
      }
      now = end;
    },
    pending: () => timers.size,
  };
}
const event = (key: string, phase: TVKeyEvent['phase'], source: TVKeyEvent['source'] = 'native'): TVKeyEvent => ({key, phase, source, repeat: false});

test('native held seek advances 10-second steps and accelerates after two seconds', () => {
  const time = clock(), steps: number[] = [], repeat = createTVSeekRepeat((direction, seconds) => {steps.push(direction * seconds);}, time.time);
  repeat.handle(event('right', 'long-begin'));
  time.advance(2000);
  assert.equal(steps.length, 9);
  assert.ok(steps.every(step => step === 10));
  time.advance(500);
  assert.equal(steps.length, 14);
  assert.equal(time.pending(), 1, 'one bounded timer, regardless of repeat count');
  repeat.handle(event('right', 'long-end'));
  time.advance(10000);
  assert.equal(steps.length, 14); assert.equal(time.pending(), 0);
});

test('explicit held-seek cancellation is idempotent and retires the pending callback', () => {
  const time = clock(); let steps = 0;
  const repeat = createTVSeekRepeat(() => {steps++;}, time.time);
  repeat.handle(event('left', 'long-begin')); repeat.stop(); repeat.stop(); time.advance(5000);
  assert.equal(steps, 1); assert.equal(time.pending(), 0);
});

test('a boundary or changed direction retires the previous repeat timer', () => {
  const time = clock(), steps: number[] = [];
  const repeat = createTVSeekRepeat((direction, seconds) => {steps.push(direction * seconds); return steps.length < 3;}, time.time);
  repeat.handle(event('left', 'long-begin'));
  repeat.handle(event('right', 'long-begin'));
  time.advance(5000);
  assert.deepEqual(steps, [-10, 10, 10]); assert.equal(time.pending(), 0);
});

test('Vega repeated presses have no synthetic seek timer or release command', () => {
  const time = clock(), keys = createTVKeyAdapter({source: 'vega'}); let seconds = 0;
  const repeat = createTVSeekRepeat((direction, step) => {seconds += direction * step;}, time.time);
  for (const eventKeyAction of [0, 0, 0, 1]) {const key = keys.read({eventType: 'right', eventKeyAction}); if (key) repeat.handle(key);}
  time.advance(10000);
  assert.equal(seconds, 30); assert.equal(time.pending(), 0);
});
