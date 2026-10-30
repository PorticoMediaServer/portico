import test from 'node:test';
import assert from 'node:assert/strict';
import type {AccountEntryState} from '../src/platform/account-entry.ts';
import {AccountSignInForm} from '../src/screens/accounts/sign-in-form.ts';

function fixture() {
  let state: AccountEntryState = {phase: 'password'}, result: AccountEntryState['phase'] = 'profiles';
  let pending: Promise<void> | undefined;
  const calls: unknown[] = [], retired = new Set<() => void>();
  const entry = {
    getSnapshot: () => state,
    subscribeAuthorityRetired: (listener: () => void) => {retired.add(listener);return () => {retired.delete(listener);};},
    signIn: async () => {throw new Error('Entry already owns ordinary sign-in after the reset.');},
    redeemLocalMove: async () => {throw new Error('A member reset is not a move.');},
    resetLocalPassword: async (proof: Readonly<{kind: 'owner'; code: string; turnOffTwoStep?: boolean} | {kind: 'member'; username: string; code: string}>, password: string, factor = '') => {
      calls.push(['reset', proof, password, factor]);await pending;state = {phase: result};
    },
    completeChallenge: async (code: string) => {calls.push(['challenge', code]);state = {phase: 'two-step-setup'};},
    beginRequiredTwoStep: async (password: string) => {calls.push(['enrol', password]);return {secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Portico?secret=ABCDEFGHIJKLMNOP', recoveryCodes: ['one', 'two']};},
    confirmRequiredTwoStep: async (password: string, code: string) => {calls.push(['confirm', password, code]);state = {phase: 'profiles'};return true;},
  };
  const form = new AccountSignInForm(entry);
  return {form, entry, calls, phase: (phase: AccountEntryState['phase']) => {state = {phase};}, result: (phase: AccountEntryState['phase']) => {result = phase;},
    hold: (promise: Promise<void>) => {pending = promise;}, retire: () => {for (const listener of retired) listener();}};
}

test('member reset forwards only its genuine member proof and optional factor, without another sign-in or secret snapshot', async () => {
  const f = fixture();
  try {
    assert.deepEqual(f.calls, []);
    await f.form.resetMemberPassword('member', 'one-use-reset-code', 'new-member-password', '123456');
    assert.deepEqual(f.calls, [['reset', {kind: 'member', username: 'member', code: 'one-use-reset-code'}, 'new-member-password', '123456']]);
    assert.equal(f.form.getSnapshot().busy, false);assert.equal(f.form.getSnapshot().username, 'member');
    assert.doesNotMatch(JSON.stringify(f.form.getSnapshot()), /one-use-reset-code|new-member-password|123456/);
  } finally {f.form.dispose();}
});

test('only the original new password continues through a real post-reset challenge and required setup', async () => {
  const f = fixture();f.result('challenge');
  try {
    await f.form.resetMemberPassword('member', 'one-use-reset-code', 'new-member-password');
    assert.equal(f.form.getSnapshot().factor, undefined);
    await f.form.completeChallenge('654321');
    const factor = f.form.getSnapshot().factor;assert.ok(factor);
    assert.deepEqual(f.calls, [['reset', {kind: 'member', username: 'member', code: 'one-use-reset-code'}, 'new-member-password', ''], ['challenge', '654321'], ['enrol', 'new-member-password']]);
    assert.doesNotMatch(JSON.stringify(f.form.getSnapshot()), /one-use-reset-code|new-member-password/);
    await factor.confirm('123456');assert.deepEqual(f.calls.at(-1), ['confirm', 'new-member-password', '123456']);
  } finally {f.form.dispose();}
});

test('wrong phases, empty fields and a disposed form never dispatch a reset', async () => {
  const f = fixture();
  try {
    for (const phase of ['move-local', 'move-portico', 'owner-reset-password', 'challenge', 'profiles', 'ready'] as const) {
      f.phase(phase);await f.form.resetMemberPassword('member', 'reset-code', 'new-password');
    }
    f.phase('password');
    await f.form.resetMemberPassword('', 'reset-code', 'new-password');
    await f.form.resetMemberPassword('member', '', 'new-password');
    await f.form.resetMemberPassword('member', 'reset-code', '');
    f.form.dispose();await f.form.resetMemberPassword('member', 'reset-code', 'new-password');
    assert.deepEqual(f.calls, []);
  } finally {f.form.dispose();}
});

test('a duplicate reset never spends another proof and retirement prevents late enrollment', async () => {
  const f = fixture();let release!: () => void;
  f.hold(new Promise<void>(resolve => {release = resolve;}));f.result('two-step-setup');
  try {
    const resetting = f.form.resetMemberPassword('member', 'original-code', 'original-password');
    await f.form.resetMemberPassword('another', 'another-code', 'another-password');
    assert.equal(f.calls.length, 1);
    f.retire();release();await resetting;
    assert.equal(f.form.getSnapshot().busy, false);assert.equal(f.form.getSnapshot().factor, undefined);
    assert.equal(f.calls.length, 1);assert.doesNotMatch(JSON.stringify(f.form.getSnapshot()), /original-code|original-password|another-password/);
  } finally {f.form.dispose();}
});

test('a thrown reset cannot reuse its password for an unrelated later challenge or replay the reset', async () => {
  const f = fixture(), failure = new Error('reset refused');
  f.entry.resetLocalPassword = async (...args) => {f.calls.push(['refused', ...args]);throw failure;};
  try {
    await assert.rejects(f.form.resetMemberPassword('member', 'reset-code', 'new-password'), error => error === failure);
    f.phase('challenge');await f.form.completeChallenge('123456');
    assert.equal(f.form.getSnapshot().factor, undefined);assert.equal(f.form.getSnapshot().busy, false);
    assert.equal(f.calls.length, 2);assert.deepEqual(f.calls[1], ['challenge', '123456']);
  } finally {f.form.dispose();}
});

test('authority retirement during the busy notification prevents dispatch before a proof can be spent', async () => {
  const f = fixture(), off = f.form.subscribe(() => {if (f.form.getSnapshot().busy) f.retire();});
  try {
    await f.form.resetMemberPassword('member', 'reset-code', 'new-password');
    assert.deepEqual(f.calls, []);assert.equal(f.form.getSnapshot().busy, false);
  } finally {off();f.form.dispose();}
});
