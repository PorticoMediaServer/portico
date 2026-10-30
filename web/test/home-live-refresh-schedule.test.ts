import assert from 'node:assert/strict';
import {test} from 'node:test';
import {homeLiveDelay, HOME_LIVE_MIN_MS, HOME_LIVE_MAX_MS, HOME_LIVE_JITTER_MS} from '../src/screens/home/model.ts';

// Native re-read Home every 2 s on a Live row whose expiresAt was past or always ~2 s ahead.
test('a past or near live expiry never schedules a re-read sooner than the floor', () => {
  const now = 1_000_000;
  for (const deadline of [now - 60_000, now - 1, now, now + 2_000, now + 29_999]) {
    const {delayMs} = homeLiveDelay(deadline, now, {streak: 0}, 0);
    assert.ok(delayMs >= HOME_LIVE_MIN_MS, `delay ${delayMs} for deadline ${deadline - now}`);
  }
  const jittered = homeLiveDelay(now + 2_000, now, {streak: 0}, 1).delayMs;
  assert.equal(jittered, HOME_LIVE_MIN_MS + HOME_LIVE_JITTER_MS);
});

test('an expiry that does not advance backs off up to the ceiling; one that advances resets', () => {
  let now = 1_000_000, schedule = {streak: 0} as Parameters<typeof homeLiveDelay>[2];
  const delays: number[] = [];
  for (let i = 0; i < 8; i++) {
    const next = homeLiveDelay(now + 2_000, now, schedule, 0);
    delays.push(next.delayMs); schedule = next.schedule; now += 2_000; // the server's expiry stays ~2 s ahead
  }
  // The expiry did move by 2 s each time, so it advanced: no back-off, but never under the floor.
  assert.ok(delays.every(delay => delay >= HOME_LIVE_MIN_MS));
  // The same expiry again and again (it did not advance) doubles the wait, capped.
  schedule = {streak: 0}; const fixed = 2_000_000; const stuck: number[] = [];
  for (let i = 0; i < 8; i++) { const next = homeLiveDelay(fixed, fixed - 5_000, schedule, 0); stuck.push(next.delayMs); schedule = next.schedule; }
  assert.deepEqual(stuck.slice(0, 4), [HOME_LIVE_MIN_MS, HOME_LIVE_MIN_MS * 2, HOME_LIVE_MIN_MS * 4, HOME_LIVE_MIN_MS * 8]);
  assert.ok(stuck.every(delay => delay <= HOME_LIVE_MAX_MS) && stuck.at(-1) === HOME_LIVE_MAX_MS);
  // A genuinely later expiry resets the back-off.
  assert.equal(homeLiveDelay(fixed + 120_000, fixed, schedule, 0).delayMs, 60_000);
});
