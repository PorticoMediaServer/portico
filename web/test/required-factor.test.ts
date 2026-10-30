import test from 'node:test';
import assert from 'node:assert/strict';
import {RequiredFactorForm} from '../src/screens/accounts/required-factor.ts';

const enrolment = Object.freeze({secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Portico?secret=ABCDEFGHIJKLMNOP', recoveryCodes: Object.freeze(['ACTUAL-ONE', 'ACTUAL-TWO'])});
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};

test('the submitted proof stays private and is sent only to the canonical enrol/confirm methods', async () => {
  const calls: unknown[] = [];
  const form = new RequiredFactorForm({beginRequiredTwoStep: async password => {calls.push(['enrol', password]); return enrolment;}, confirmRequiredTwoStep: async (password, code) => {calls.push(['confirm', password, code]); return true;}}, 'submitted-password', () => true);
  assert.equal(calls.length, 0);
  assert.doesNotMatch(JSON.stringify(form), /submitted-password/);
  await form.enrol();
  assert.doesNotMatch(JSON.stringify(form.getSnapshot()), /submitted-password/);
  await form.confirm('123456');
  assert.deepEqual(calls, [['enrol', 'submitted-password'], ['confirm', 'submitted-password', '123456']]);
  assert.deepEqual(form.getSnapshot(), {phase: 'recovery', busy: false, codes: enrolment.recoveryCodes});
  assert.equal(form.done(), true); assert.equal(form.done(), false);
  assert.deepEqual(form.getSnapshot(), {phase: 'closed', busy: false});
});
test('reload without a submitted password cannot generate enrollment or a proof prompt', async () => {
  let called = false;
  const form = new RequiredFactorForm({beginRequiredTwoStep: async () => {called = true; return enrolment;}, confirmRequiredTwoStep: async () => {called = true; return true;}}, '', () => true);
  await form.enrol(); await form.confirm('123456'); assert.equal(called, false);
});
test('retirement while enrollment is pending never publishes the late key or codes', async () => {
  const pending = deferred<typeof enrolment>();
  const form = new RequiredFactorForm({beginRequiredTwoStep: () => pending.promise, confirmRequiredTwoStep: async () => true}, 'submitted-password', () => true);
  const work = form.enrol(); form.dispose(); pending.resolve(enrolment); await work;
  assert.deepEqual(form.getSnapshot(), {phase: 'closed', busy: false});
});
test('account replacement during confirmation cannot reveal the predecessor recovery codes', async () => {
  let current = true; const pending = deferred<boolean>();
  const form = new RequiredFactorForm({beginRequiredTwoStep: async () => enrolment, confirmRequiredTwoStep: () => pending.promise}, 'submitted-password', () => current);
  await form.enrol(); const work = form.confirm('123456'); current = false; form.dispose(); pending.resolve(true); await work;
  assert.deepEqual(form.getSnapshot(), {phase: 'closed', busy: false}); assert.equal(form.done(), false);
});
test('no duplicate submission, partial code, or failed confirmation can advance to recovery', async () => {
  const pending = deferred<boolean>(); let calls = 0;
  const form = new RequiredFactorForm({beginRequiredTwoStep: async () => enrolment, confirmRequiredTwoStep: () => {calls++; return pending.promise;}}, 'submitted-password', () => true);
  await form.enrol(); await form.confirm('123'); const work = form.confirm('123456'); await form.confirm('654321');
  assert.equal(calls, 1); pending.resolve(false); await work;
  assert.equal(form.getSnapshot().phase, 'code'); assert.equal(form.done(), false); form.dispose();
});
test('a stale family answer closes and clears private facts even before the caller unmounts', async () => {
  let current = true; const pending = deferred<typeof enrolment>();
  const form = new RequiredFactorForm({beginRequiredTwoStep: () => pending.promise, confirmRequiredTwoStep: async () => true}, 'submitted-password', () => current);
  const work = form.enrol(); current = false; pending.resolve(enrolment); await work;
  assert.deepEqual(form.getSnapshot(), {phase: 'closed', busy: false});
  assert.doesNotMatch(JSON.stringify(form), /submitted-password|ABCDEFGHIJKLMNOP|ACTUAL-ONE/);
});
