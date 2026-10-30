import test from 'node:test';
import assert from 'node:assert/strict';
import {bootRecoveryDeadline, bootRecoveryDelay, bootWaitingPhase, documentStartedAt} from '../src/shell/boot-state.ts';

test('the boot ladder includes time before React mounts', () => {
  const start = documentStartedAt(10000, 1000);
  assert.equal(start, 1000);
  assert.equal(bootWaitingPhase(start, 10000), 'still-connecting');
  assert.deepEqual([0, 999, 1000, 2999, 3000, 7999, 8000].map(elapsed => bootWaitingPhase(start, start + elapsed)),
    ['picture', 'picture', 'spinner', 'spinner', 'connecting', 'connecting', 'still-connecting']);
  assert.equal(documentStartedAt(1000, undefined), 1000);
  assert.equal(documentStartedAt(1000, NaN), 1000);
  assert.equal(documentStartedAt(1000, 2000), 1000);
});

test('quiet retries and suspended tabs do not extend the original recovery deadline', () => {
  assert.equal(bootRecoveryDelay(1000, 1100, false), 14900);
  assert.equal(bootRecoveryDelay(1000, 7000, false), 9000);
  assert.equal(bootRecoveryDelay(1000, 15000, false), 1000);
  assert.equal(bootRecoveryDelay(1000, 41000, false), 0);
  assert.equal(bootRecoveryDelay(1000, 1100, true), 900);
  assert.equal(bootRecoveryDelay(1000, 2100, true), 0);
});

test('recovery appears at fifteen seconds while the same opening remains pending', t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 1000});
  let shown = 0;
  const deadline = bootRecoveryDeadline(1000, () => shown++);
  deadline.update(false);
  t.mock.timers.tick(14999);
  assert.equal(shown, 0);
  t.mock.timers.tick(1);
  assert.equal(shown, 1);
  deadline.update(false);
  deadline.update(true);
  t.mock.timers.tick(60000);
  assert.equal(shown, 1, 'a network update cannot reveal the same opening twice');
  deadline.dispose();
});

test('offline uses one second on the original clock, and online restores that clock', t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 1000});
  let shown = 0;
  const deadline = bootRecoveryDeadline(1000, () => shown++);
  deadline.update(false);
  t.mock.timers.tick(400);
  deadline.update(true);
  t.mock.timers.tick(599);
  assert.equal(shown, 0);
  deadline.update(false);
  t.mock.timers.tick(14000);
  assert.equal(shown, 0);
  t.mock.timers.tick(1);
  assert.equal(shown, 1);
  deadline.dispose();
});

test('an offline transition after one second reveals immediately without another delay', t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 1000});
  let shown = 0;
  const deadline = bootRecoveryDeadline(1000, () => shown++);
  deadline.update(false);
  t.mock.timers.tick(4000);
  deadline.update(true);
  assert.equal(shown, 1);
  deadline.dispose();
});

test('ready, signed-out or retired openings cancel their pending recovery', t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 1000});
  let shown = 0;
  const deadline = bootRecoveryDeadline(1000, () => shown++);
  deadline.update(false);
  t.mock.timers.tick(100);
  deadline.dispose();
  deadline.update(true);
  t.mock.timers.tick(60000);
  assert.equal(shown, 0);
});
