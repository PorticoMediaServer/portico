import test from 'node:test';
import assert from 'node:assert/strict';
import type {AccountEntryState} from '../src/platform/account-entry.ts';
import {AccountSignInForm} from '../src/screens/accounts/sign-in-form.ts';

function fixture() {
  let state: AccountEntryState = {phase: 'move-local'};
  let result: AccountEntryState['phase'] = 'profiles';
  let write: Promise<void> | undefined;
  const calls: unknown[] = [], retired = new Set<() => void>();
  const entry = {
    getSnapshot: () => state,
    subscribeAuthorityRetired: (listener: () => void) => {retired.add(listener);return () => {retired.delete(listener);};},
    signIn: async () => {throw new Error('A member move must not fall back to ordinary sign-in.');},
    resetLocalPassword: async () => {throw new Error('A member move must not use a password-reset proof.');},
    redeemLocalMove: async (username: string, password: string) => {calls.push(['move', username, password]);await write;state = {phase: result};},
    completeChallenge: async (code: string) => {calls.push(['challenge', code]);state = {phase: 'two-step-setup'};},
    beginRequiredTwoStep: async (password: string) => {
      calls.push(['enrol', password]);
      return Object.freeze({secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Portico?secret=ABCDEFGHIJKLMNOP', recoveryCodes: Object.freeze(['one', 'two'])});
    },
    confirmRequiredTwoStep: async (password: string, code: string) => {calls.push(['confirm', password, code]);state = {phase: 'profiles'};return true;},
  };
  const form = new AccountSignInForm(entry);
  return {form, calls, entry, phase: (phase: AccountEntryState['phase']) => {state = {phase};}, result: (phase: AccountEntryState['phase']) => {result = phase;},
    hold: (value: Promise<void>) => {write = value;}, retire: () => {for (const listener of retired) listener();}};
}

test('a local move dispatches only the canonical member command without exposing its submitted password', async () => {
  const f = fixture();
  try {
    assert.deepEqual(f.calls, []);
    await f.form.signIn('new-user', 'new-local-password');assert.deepEqual(f.calls, []);
    await f.form.redeemLocalMove('new-user', 'new-local-password');
    assert.deepEqual(f.calls, [['move', 'new-user', 'new-local-password']]);
    assert.equal(f.form.getSnapshot().busy, false);
    assert.equal(f.form.getSnapshot().username, 'new-user');
    assert.doesNotMatch(JSON.stringify(f.form.getSnapshot()), /new-local-password/);
  } finally {f.form.dispose();}
});

test('a local move cannot dispatch from another continuation, without credentials or after disposal', async () => {
  const f = fixture();
  try {
    for (const phase of ['password', 'portico', 'move-portico', 'challenge', 'profiles', 'ready'] as const) {
      f.phase(phase);await f.form.redeemLocalMove('new-user', 'new-local-password');
    }
    f.phase('move-local');
    await f.form.redeemLocalMove('', 'new-local-password');await f.form.redeemLocalMove('new-user', '');
    f.form.dispose();await f.form.redeemLocalMove('new-user', 'new-local-password');
    assert.deepEqual(f.calls, []);
  } finally {f.form.dispose();}
});

test('the original live move password can continue through its challenge into required setup', async () => {
  const f = fixture();f.result('challenge');
  try {
    await f.form.redeemLocalMove('new-user', 'new-local-password');
    assert.equal(f.form.getSnapshot().factor, undefined);
    await f.form.completeChallenge('123456');
    const factor = f.form.getSnapshot().factor;
    assert.ok(factor);assert.equal(factor.getSnapshot().phase, 'code');
    assert.doesNotMatch(JSON.stringify(f.form.getSnapshot()), /new-local-password/);
    assert.deepEqual(f.calls, [['move', 'new-user', 'new-local-password'], ['challenge', '123456'], ['enrol', 'new-local-password']]);
    await factor.confirm('654321');
    assert.deepEqual(f.calls.at(-1), ['confirm', 'new-local-password', '654321']);
  } finally {f.form.dispose();}
});

test('a duplicate move gesture is refused and retirement discards late setup continuation', async () => {
  const f = fixture();let release!: () => void;
  f.hold(new Promise<void>(resolve => {release = resolve;}));f.result('two-step-setup');
  try {
    const moving = f.form.redeemLocalMove('original', 'original-password');
    await f.form.redeemLocalMove('replacement', 'replacement-password');
    assert.deepEqual(f.calls, [['move', 'original', 'original-password']]);
    f.retire();release();await moving;
    assert.equal(f.form.getSnapshot().factor, undefined);assert.equal(f.form.getSnapshot().busy, false);
    assert.doesNotMatch(JSON.stringify(f.form.getSnapshot()), /original-password|replacement-password/);
    assert.deepEqual(f.calls, [['move', 'original', 'original-password']]);
  } finally {f.form.dispose();}
});

test('a failed move never falls back to sign-in or retains its password for a later challenge', async () => {
  const f = fixture(), failure = new Error('move refused');
  f.entry.redeemLocalMove = async () => {throw failure;};
  try {
    await assert.rejects(f.form.redeemLocalMove('new-user', 'new-local-password'), error => error === failure);
    assert.equal(f.form.getSnapshot().busy, false);
    f.phase('challenge');await f.form.completeChallenge('123456');
    assert.equal(f.form.getSnapshot().factor, undefined);
    assert.deepEqual(f.calls, [['challenge', '123456']]);
  } finally {f.form.dispose();}
});
