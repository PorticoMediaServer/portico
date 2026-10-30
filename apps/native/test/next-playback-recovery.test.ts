import test from 'node:test';
import assert from 'node:assert/strict';
import {mediaRecovered, NativePlaybackRecovery} from '../src-next/platform/playback/playback-recovery.ts';

function clock() {
  let now = 0, serial = 0;
  const scheduled = new Map<number, {at: number; run: () => void}>();
  return {scheduled,
    setTimer(run: () => void, ms: number) {const id = serial++; scheduled.set(id, {at: now + ms, run}); return id;},
    clearTimer(id: unknown) {scheduled.delete(id as number);},
    advance(ms: number) {const target = now + ms;
      for (;;) {
        const next = [...scheduled.entries()].filter(([, task]) => task.at <= target).sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break; now = next[1].at; scheduled.delete(next[0]); next[1].run();
      }
      now = target;
    },
  };
}

test('one network error fails independently after 90 seconds, with a separate route probe', () => {
  const timers = clock(), recovery = new NativePlaybackRecovery(timers);
  let failed = 0, probed = 0;
  recovery.start('1/1', () => true, () => failed++, () => probed++);
  timers.advance(1000); assert.equal(probed, 1); assert.equal(failed, 0);
  timers.advance(88_999); assert.equal(failed, 0);
  timers.advance(1); assert.equal(failed, 1); assert.equal(timers.scheduled.size, 0);
});

test('repeated errors and generation changes in the same run cannot renew the outage', () => {
  const timers = clock(), recovery = new NativePlaybackRecovery(timers); let failed = 0;
  const start = () => recovery.start('1/7', () => true, () => failed++, () => {});
  start(); timers.advance(50_000); recovery.retain('1/7'); start();
  timers.advance(40_000); assert.equal(failed, 1);
});

test('verified recovery, retirement and a new run cancel the original deadline', () => {
  for (const finish of ['recovered', 'retired', 'replaced'] as const) {
    const timers = clock(), recovery = new NativePlaybackRecovery(timers); let failed = 0;
    recovery.start('1/1', () => true, () => failed++, () => {});
    timers.advance(20_000);
    if (finish === 'recovered') recovery.clear(); else recovery.retain(finish === 'retired' ? undefined : '2/2');
    timers.advance(90_000); assert.equal(failed, 0, finish); assert.equal(timers.scheduled.size, 0);
  }
});

test('queued old callbacks cannot cancel a later outage of the same run', () => {
  const timers = clock(), recovery = new NativePlaybackRecovery(timers); let failed = 0, probed = 0;
  const start = () => recovery.start('1/1', () => true, () => failed++, () => probed++);
  start(); const old = [...timers.scheduled.values()]; recovery.clear(); start();
  old.forEach(task => task.run()); assert.equal(failed, 0); assert.equal(probed, 0); assert.equal(timers.scheduled.size, 2);
  timers.advance(90_000); assert.equal(failed, 1); assert.equal(probed, 1);
});

test('expiry checks the original playback owner even if a retirement notification was missed', () => {
  const timers = clock(), recovery = new NativePlaybackRecovery(timers); let current = true, failed = 0;
  recovery.start('1/1', () => current, () => failed++, () => {}); current = false;
  timers.advance(90_000); assert.equal(failed, 0); assert.equal(timers.scheduled.size, 0);
});

test('stationary buffering and paused ticks are not recovery; ready, actual play or advancement are', () => {
  assert.equal(mediaRecovered({type: 'progress', position: 42}, 42), false);
  assert.equal(mediaRecovered({type: 'progress', position: 41}, 42), false);
  assert.equal(mediaRecovered({type: 'progress', position: NaN}, 42), false);
  assert.equal(mediaRecovered({type: 'progress', position: 42.1}, 42), true);
  assert.equal(mediaRecovered({type: 'ready', position: 42}, 42), true);
  assert.equal(mediaRecovered({type: 'progress', position: 42, isPlaying: true}, 42), true);
  assert.equal(mediaRecovered({type: 'progress', position: 43, seeking: true}, 42), false);
});
