import test from 'node:test';
import assert from 'node:assert/strict';
import type {AccountEntryState} from '../src/platform/account-entry.ts';
import {AccountSignInForm} from '../src/screens/accounts/sign-in-form.ts';

const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
const enrolment = Object.freeze({secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Portico?secret=ABCDEFGHIJKLMNOP', recoveryCodes: Object.freeze(['one', 'two'])});
function setup() {
  let state: AccountEntryState = {phase: 'password'};
  const retired = new Set<() => void>(), calls: unknown[] = [];
  const port = {
    getSnapshot: () => state,
    subscribeAuthorityRetired: (listener: () => void) => {retired.add(listener); return () => {retired.delete(listener);};},
    signIn: async (username: string, password: string) => {calls.push(['signin', username, password]); state = {phase: 'two-step-setup'};},
    redeemLocalMove: async () => {throw new Error('Ordinary sign-in cannot redeem a member move.');},
    resetLocalPassword: async () => {throw new Error('Ordinary sign-in cannot reset a password.');},
    completeChallenge: async (code: string) => {calls.push(['challenge', code]); state = {phase: 'two-step-setup'};},
    beginRequiredTwoStep: async (password: string) => {calls.push(['enrol', password]); return enrolment;},
    confirmRequiredTwoStep: async (password: string, code: string) => {calls.push(['confirm', password, code]); state = {phase: 'profiles'}; return true;},
  };
  return {port, calls, set: (value: AccountEntryState) => {state = value;}, retire: () => {for (const listener of retired) listener();}};
}
test('sign-in gesture alone starts required setup, keeping the proof out of every public snapshot', async () => {
  const f = setup(), form = new AccountSignInForm(f.port);
  assert.deepEqual(f.calls, []);
  await form.signIn('actual-user', 'live-proof');
  const factor = form.getSnapshot().factor;
  assert.ok(factor); assert.equal(factor.getSnapshot().phase, 'code');
  assert.doesNotMatch(JSON.stringify(form.getSnapshot()), /live-proof/);
  assert.deepEqual(f.calls, [['signin', 'actual-user', 'live-proof'], ['enrol', 'live-proof']]);
  await factor.confirm('123456');
  assert.equal(form.getSnapshot().factor, factor);
  assert.deepEqual(factor.getSnapshot(), {phase: 'recovery', busy: false, codes: enrolment.recoveryCodes});
  assert.equal(f.port.getSnapshot().phase, 'profiles');
  assert.equal(factor.done(), true); form.clear();
  assert.equal(form.getSnapshot().factor, undefined); form.dispose();
});
test('MFA keeps only the original live proof until its original challenge completes', async () => {
  const f = setup();
  f.port.signIn = async () => {f.set({phase: 'challenge'});};
  const form = new AccountSignInForm(f.port); await form.signIn('actual-user', 'original-proof');
  assert.equal(form.getSnapshot().factor, undefined);
  assert.doesNotMatch(JSON.stringify(form), /original-proof/);
  await form.completeChallenge('a-real-recovery-code');
  assert.deepEqual(f.calls, [['challenge', 'a-real-recovery-code'], ['enrol', 'original-proof']]);
  form.dispose();
});
test('authority retirement during a sign-in prevents the late setup from starting', async () => {
  const f = setup(), pending = deferred<void>();
  f.port.signIn = async () => {await pending.promise; f.set({phase: 'two-step-setup'});};
  const form = new AccountSignInForm(f.port), work = form.signIn('actual-user', 'retired-proof');
  f.retire(); pending.resolve(); await work;
  assert.deepEqual(f.calls, []); assert.equal(form.getSnapshot().factor, undefined);
  assert.equal(form.getSnapshot().busy, false); form.dispose();
});
test('idle replacement clears original enrollment and codes synchronously, even before unmount', async () => {
  const f = setup(), form = new AccountSignInForm(f.port); await form.signIn('actual-user', 'private-proof');
  const factor = form.getSnapshot().factor!; await factor.confirm('123456');
  f.retire();
  assert.deepEqual(factor.getSnapshot(), {phase: 'closed', busy: false});
  assert.equal(form.getSnapshot().factor, undefined);
  form.dispose();
});
test('a restored challenge without live proof never invents an enrollment proof', async () => {
  const f = setup(); f.set({phase: 'challenge'});
  const form = new AccountSignInForm(f.port); await form.completeChallenge('123456');
  assert.deepEqual(f.calls, [['challenge', '123456']]);
  assert.equal(form.getSnapshot().factor, undefined); form.dispose();
});
test('failed sign-in discards its proof and duplicate in-flight gestures cannot replace it', async () => {
  const f = setup(), pending = deferred<void>(); let count = 0;
  f.port.signIn = async () => {count++; await pending.promise;};
  const form = new AccountSignInForm(f.port), work = form.signIn('first', 'first-proof');
  await form.signIn('second', 'second-proof'); pending.resolve(); await work;
  assert.equal(count, 1); assert.equal(form.getSnapshot().username, 'first');
  f.set({phase: 'challenge'}); await form.completeChallenge('123456');
  assert.deepEqual(f.calls, [['challenge', '123456']]); form.dispose();
});

test('a local rate limit keeps its original deadline and guards new attempts until it expires', async t => {
  let now = 1000, attempts = 0;
  t.mock.method(Date, 'now', () => now);
  const f = setup();
  f.port.signIn = async () => {attempts++; f.set({phase: 'password', error: {status: 429, code: 'rate_limited', retryAfterSeconds: 32}});};
  const form = new AccountSignInForm(f.port);
  try {
    await form.signIn('first', 'synthetic-password');
    assert.equal(form.getSnapshot().busy, false);
    assert.equal(form.getSnapshot().retryAt, 33000);
    assert.doesNotMatch(JSON.stringify(form.getSnapshot()), /synthetic-password/);
    now = 32999;
    form.setUsername('second');
    await form.signIn('second', 'synthetic-password');
    assert.equal(attempts, 1);
    assert.equal(form.getSnapshot().retryAt, 33000, 'editing and rerenders cannot restart the countdown');
    now = 33000;
    assert.equal(attempts, 1, 'expiry never submits credentials automatically');
    await form.signIn('second', 'synthetic-password');
    assert.equal(attempts, 2);
    assert.equal(form.getSnapshot().retryAt, 65000);
    f.retire();
    assert.equal(form.getSnapshot().retryAt, undefined);
  } finally {form.dispose();}
});

test('a late rate limit cannot populate a retired form, and an ordinary refusal invents no wait', async () => {
  const f = setup(), pending = deferred<void>();
  f.port.signIn = async () => {await pending.promise; f.set({phase: 'password', error: {status: 429, retryAfterSeconds: 32}});};
  const form = new AccountSignInForm(f.port);
  try {
    const work = form.signIn('first', 'synthetic-password');
    f.retire(); pending.resolve(); await work;
    assert.equal(form.getSnapshot().retryAt, undefined);
    f.port.signIn = async () => {f.set({phase: 'password', error: {status: 401}});};
    await form.signIn('first', 'synthetic-password');
    assert.equal(form.getSnapshot().retryAt, undefined);
  } finally {form.dispose();}
});
