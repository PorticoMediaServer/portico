import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createSecureRecordsRecovery} from '../src-next/shell/secure-records-recovery.ts';

function fixture(overrides: Partial<{reset: () => Promise<boolean>; restart: () => Promise<boolean>}> = {}) {
  let root = true, installation = true, resets = 0, restarts = 0, retries = 0;
  const recovery = createSecureRecordsRecovery({current: () => installation && root, ownsRoot: () => root,
    retry: () => {retries++; root = false;}, reset: overrides.reset ?? (async () => {resets++; return true;}),
    restart: overrides.restart ?? (async () => {restarts++; return false;})});
  return {recovery, calls: () => ({resets, restarts, retries}), retire: () => {root = false;}, keyChanged: () => {installation = false;}};
}

test('opening and cancelling confirmation never removes records; Try again only retires into normal restore', async () => {
  const f = fixture();
  await f.recovery.confirm();
  assert.deepEqual(f.calls(), {resets: 0, restarts: 0, retries: 0});
  f.recovery.open(); f.recovery.cancel();
  assert.equal(f.recovery.getSnapshot().phase, 'idle');
  f.recovery.retry(); f.recovery.retry();
  assert.deepEqual(f.calls(), {resets: 0, restarts: 0, retries: 1});
});

test('duplicate confirmation shares the original pending reset and restart follows it once', async () => {
  let finish!: (value: boolean) => void, calls = 0, restarted = 0;
  const f = fixture({reset: () => {calls++; return new Promise(resolve => {finish = resolve;});}, restart: async () => {restarted++; return false;}});
  f.recovery.open(); const first = f.recovery.confirm();
  await f.recovery.confirm(); f.recovery.cancel();
  assert.equal(calls, 1); assert.equal(restarted, 0); assert.equal(f.recovery.getSnapshot().phase, 'working');
  finish(true); await first;
  assert.equal(restarted, 1); assert.equal(f.recovery.getSnapshot().phase, 'reopen');
  await f.recovery.confirm(); f.recovery.retry();
  assert.equal(calls, 1);
});

test('a reset that finds the key recovered still restarts and never continues old shell objects', async () => {
  let restarted = 0;
  const f = fixture({reset: async () => false, restart: async () => {restarted++; return true;}});
  f.recovery.open(); await f.recovery.confirm();
  assert.equal(restarted, 1); assert.equal(f.recovery.getSnapshot().phase, 'working');
});

test('reset may change installation identity; only the original root can finish its closed restart', async () => {
  let restarted = 0;
  const f = fixture({reset: async () => {f.keyChanged(); return true;}, restart: async () => {restarted++; return false;}});
  f.recovery.open(); await f.recovery.confirm();
  assert.equal(restarted, 1); assert.equal(f.recovery.getSnapshot().phase, 'reopen');
});

test('root retirement before confirmation or during reset cannot restart a successor', async () => {
  const before = fixture(); before.recovery.open(); before.retire(); await before.recovery.confirm();
  assert.deepEqual(before.calls(), {resets: 0, restarts: 0, retries: 0});
  let finish!: (value: boolean) => void, restarted = 0;
  const during = fixture({reset: () => new Promise(resolve => {finish = resolve;}), restart: async () => {restarted++; return true;}});
  during.recovery.open(); const work = during.recovery.confirm(); during.retire(); finish(true); await work;
  assert.equal(restarted, 0);
});

test('reset failure stays in confirmation with retryable error; unavailable restart shows reopen only', async () => {
  let fails = true;
  const f = fixture({reset: async () => {if (fails) throw new Error('storage'); return true;}});
  f.recovery.open(); await f.recovery.confirm();
  assert.deepEqual(f.recovery.getSnapshot(), {phase: 'confirming', failed: true});
  fails = false; await f.recovery.confirm();
  assert.deepEqual(f.recovery.getSnapshot(), {phase: 'reopen', failed: false});
});

test('disposed recovery cannot reset or publish into a replacement mount', async () => {
  const f = fixture(); f.recovery.open(); f.recovery.dispose(); await f.recovery.confirm();
  assert.deepEqual(f.calls(), {resets: 0, restarts: 0, retries: 0});
});

test('busy confirmation refuses gesture dismissal and reset failure retains the open confirmation', async () => {
  // Caller contract: BottomSheet must not start an exit gesture that this
  // controller cannot accept while the native reset is pending.
  const caller = readFileSync(new URL('../src-next/shell/SecureRecordsRecovery.tsx', import.meta.url), 'utf8');
  assert.match(caller, /dismissOnGesture=\{!busy\}/);
  let reject!: (error: Error) => void;
  const f = fixture({reset: () => new Promise((_, fail) => {reject = fail;})});
  f.recovery.open(); const work = f.recovery.confirm();
  f.recovery.cancel();
  assert.equal(f.recovery.getSnapshot().phase, 'working');
  reject(new Error('native reset refused')); await work;
  assert.deepEqual(f.recovery.getSnapshot(), {phase: 'confirming', failed: true});
});
