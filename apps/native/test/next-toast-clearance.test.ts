import assert from 'node:assert/strict';
import test from 'node:test';
import {TOAST_GAP, toastClearance} from '../src-next/kit/overlays/toast-clearance';

/** Controlled source fixtures for FX-075's toast clearance. These are the reviewed
 * vectors, not measured device geometry: the helper is pure, so every case below is
 * the exact arithmetic the host now performs. */
test('without an obstruction the original fallback is preserved (safe 34, base 84 -> 130)', () => {
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34}), 130);
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: undefined}), 130);
  assert.equal(TOAST_GAP, 12);
});

test('a supplied occupied edge clears the larger value with the safe area counted once (154 -> 166)', () => {
  // base = 84 + 34 = 118, then the real obstruction at 154 wins and adds one gap.
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: 154}), 166);
  // An obstruction below the base never shrinks the drawn clearance.
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: 100}), 130);
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: 118}), 130);
});

test('an absent or retired obstruction returns to the same fallback', () => {
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: 154}), 166);
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: undefined}), 130);
  // Retired means the caller stops supplying it; zero and negatives are no obstruction either.
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: 0}), 130);
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: -20}), 130);
});

test('safe-area and size changes move the fallback linearly, and TV keeps its own numbers', () => {
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 0}), 96);
  assert.equal(toastClearance({bottomOffset: 120, safeBottom: 0}), 132);
  assert.equal(toastClearance({bottomOffset: 0, safeBottom: 34}), 46);
  // TV: the top placement path passes no obstruction and keeps the same bottom maths.
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 0, obstructionTop: undefined}), 96);
});

test('non-finite geometry is rejected safely instead of producing a broken padding', () => {
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: Number.NaN}), 130);
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: 34, obstructionTop: Number.POSITIVE_INFINITY}), 130);
  assert.equal(toastClearance({bottomOffset: Number.NaN, safeBottom: 34}), 46);
  assert.equal(toastClearance({bottomOffset: 84, safeBottom: Number.NaN}), 96);
  assert.equal(toastClearance({bottomOffset: Number.NaN, safeBottom: Number.POSITIVE_INFINITY}), 12);
});
