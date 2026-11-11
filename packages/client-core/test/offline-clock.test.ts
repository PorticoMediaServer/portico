import test from 'node:test';
import assert from 'node:assert/strict';
import {clockAtContact, freshOfflineClock, mayPlayOffline} from '../src/offline-clock.ts';

const day = 86_400_000, at = Date.parse('2026-11-11T12:00:00Z');
// The server answered at its noon; this device's clock read two hours behind (10:00).
const permission = {receivedAt: at - 2 * 3_600_000, playableForMs: 30 * day, playableUntil: new Date(at + 30 * day).toISOString()};

test('downloads play for the 30 days the server gave, judged by the server\'s time whatever the device\'s clock said', () => {
  const clock = clockAtContact(freshOfflineClock, permission);
  const wall = (serverMs: number) => serverMs - 2 * 3_600_000;
  assert.equal(mayPlayOffline(permission, clock, wall(at + 29 * day)).playable, true);
  assert.equal(mayPlayOffline(permission, clock, wall(at + 30 * day)).playable, false);
  assert.equal(mayPlayOffline(undefined, clock, wall(at)).playable, false);
});

test('turning the clock back after a judgement gives no more time, and the next contact sets the clock right', () => {
  let clock = clockAtContact(freshOfflineClock, permission);
  const late = mayPlayOffline(permission, clock, permission.receivedAt + 31 * day);
  assert.equal(late.playable, false);
  clock = late.clock;
  // Back to the day of the contact: still past the end.
  assert.equal(mayPlayOffline(permission, clock, permission.receivedAt).playable, false);
  // Online again: a new 30 days from the server's time, even though the floor was ahead of it.
  const renewed = {receivedAt: permission.receivedAt + 31 * day, playableForMs: 30 * day, playableUntil: new Date(at + 61 * day).toISOString()};
  clock = clockAtContact(clock, renewed);
  assert.equal(mayPlayOffline(renewed, clock, renewed.receivedAt + day).playable, true);
});
