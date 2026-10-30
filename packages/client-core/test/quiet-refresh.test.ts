import test from 'node:test';
import assert from 'node:assert/strict';
import {QUIET_REFRESH_TRIES, quietRefreshStep} from '../src/quiet-refresh.ts';

test('a list the server says changed is read again quietly, a few times, before the notice shows', () => {
  let state = {tries: 0, lastAt: 0}, now = 100_000;
  const delays: number[] = [];
  for (let i = 0; i < QUIET_REFRESH_TRIES; i++) {
    const step = quietRefreshStep(state, now, () => 0);
    assert.equal(step.stuck, false);
    delays.push(step.delayMs!);
    state = step.state; now += 2000;
  }
  assert.deepEqual(delays, [0, 1500, 3000], 'at once, then backing off');
  assert.equal(quietRefreshStep(state, now).stuck, true, 'still refused: now it says so');
  // A list that stayed readable for a minute counts from zero again.
  assert.equal(quietRefreshStep(state, now + 61_000, () => 0).delayMs, 0);
});
