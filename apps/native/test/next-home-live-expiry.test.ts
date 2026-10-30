import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {LIVE_REREAD_JITTER_MS, LIVE_REREAD_MAX_MS, LIVE_REREAD_MIN_MS, liveRereadPlan, type LiveRereadMemory} from '../src-next/screens/home/live-expiry.ts';

const liveRow = (observedAt: number, expiresAt: number) => ({id: 'live', live: {observedAt: new Date(observedAt).toISOString(), expiresAt: new Date(expiresAt).toISOString()}}) as never;

test('E-mobile-29: a server that always answers with an expiry 2 s ahead is read at most once per 30 s, never in a loop', () => {
  let now = Date.parse('2026-10-29T12:00:00Z'), memory: LiveRereadMemory = {stalls: 0};
  const reads: number[] = [];
  while (now < Date.parse('2026-10-29T12:10:00Z')) {
    const plan = liveRereadPlan([liveRow(now, now + 2_000)], memory, () => 0.5)!;
    assert.ok(plan.delayMs >= LIVE_REREAD_MIN_MS, `never sooner than 30 s (${plan.delayMs})`);
    memory = {expiry: plan.expiry, stalls: plan.stalls};
    now += plan.delayMs; reads.push(now);
  }
  assert.ok(reads.length <= 20, `${reads.length} reads in 10 minutes`);
  for (let i = 1; i < reads.length; i++) assert.ok(reads[i]! - reads[i - 1]! >= LIVE_REREAD_MIN_MS);
});

test('the same or an already-expired expiry backs off exponentially, up to 10 minutes', () => {
  const at = Date.parse('2026-10-29T12:00:00Z');
  let memory: LiveRereadMemory = {stalls: 0};
  const delays: number[] = [];
  for (let i = 0; i < 7; i++) {
    const plan = liveRereadPlan([liveRow(at, at + 45_000)], memory, () => 0)!;
    delays.push(plan.delayMs); memory = {expiry: plan.expiry, stalls: plan.stalls};
  }
  assert.deepEqual(delays, [45_000, 60_000, 120_000, 240_000, 480_000, LIVE_REREAD_MAX_MS, LIVE_REREAD_MAX_MS]);
  const expired = liveRereadPlan([liveRow(at, at - 5_000)], {stalls: 0}, () => 0)!;
  assert.equal(expired.delayMs, 60_000, 'an expiry already past never fires at once');
});

test('time left is measured on the server clock, and clients spread out with jitter', () => {
  // A device clock far ahead of the server changes nothing: only expiresAt − observedAt counts.
  const server = Date.parse('2026-10-29T12:00:00Z');
  assert.equal(liveRereadPlan([liveRow(server, server + 50_000)], {stalls: 0}, () => 0)!.delayMs, 50_000);
  const spread = liveRereadPlan([liveRow(server, server + 50_000)], {stalls: 0}, () => 0.999)!.delayMs;
  assert.ok(spread > 50_000 && spread < 50_000 + LIVE_REREAD_JITTER_MS);
  assert.equal(liveRereadPlan([{id: 'films'} as never], {stalls: 0}), undefined, 'no live row, no timer');
});

test('Home keeps one timer per live expiry, only for live rows in view, so neither hydration nor a stale off-screen row drives re-reads', () => {
  const home = readFileSync(new URL('../src-next/screens/home/HomeScreen.tsx', import.meta.url), 'utf8');
  assert.match(home, /const plan = liveRereadPlan\(document\.rows\.filter\(row => demanded\.has\(row\.id\)\), liveMemory\.current\);/);
  assert.match(home, /if \(liveTimer\.current\?\.expiry === plan\.expiry\) return;/);
  assert.doesNotMatch(home, /Math\.max\(0, expiry - Date\.now\(\)\)/);
});
