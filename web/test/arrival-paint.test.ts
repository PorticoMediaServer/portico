import test from 'node:test';
import assert from 'node:assert/strict';
import {hasExitPaint, ownArrivalPaint, takeArrivalPaint} from '../src/kit/browse/arrival-paint.ts';

// UNRUN exact-node/source ownership vectors; no browser or elapsed-time claim.
test('another source cannot take the original opacity lease', () => {
  const node = {} as HTMLElement, source = {};let calls = 0;
  const release = ownArrivalPaint(node, source, () => {calls++;return .37;});
  assert.equal(takeArrivalPaint(node, {}), undefined);assert.equal(calls, 0);
  assert.equal(takeArrivalPaint(node, source), .37);assert.equal(calls, 1);
  assert.equal(takeArrivalPaint(node, source), undefined);release.release();
});
test('an old cleanup cannot erase the successor on a reused node', () => {
  const node = {} as HTMLElement, source = {};
  const old = ownArrivalPaint(node, source, () => .1), current = ownArrivalPaint(node, source, () => .8);
  old.release();assert.equal(old.current(), false);assert.equal(current.current(), true);
  assert.equal(takeArrivalPaint(node, source), .8);current.release();
});
test('taking an old job cannot erase a synchronously installed successor', () => {
  const node = {} as HTMLElement, source = {};let successor = {release: () => {}, current: () => false};
  const old = ownArrivalPaint(node, source, () => {successor = ownArrivalPaint(node, source, () => .6);return .3;});
  assert.equal(takeArrivalPaint(node, source), .3);old.release();assert.equal(takeArrivalPaint(node, source), .6);successor.release();
});
test('unknown opacity is never normalised into invented paint', () => {
  for (const value of [NaN, Infinity, -.1, 1.1, undefined]) {
    const node = {} as HTMLElement, source = {};ownArrivalPaint(node, source, () => value);
    assert.equal(takeArrivalPaint(node, source), undefined);
  }
});
test('only the same original source can observe and consume a returned exit lease', () => {
  const node = {} as HTMLElement, source = {};let lease = {release() {}, current: () => false};
  assert.equal(hasExitPaint(node, undefined), false);
  lease = ownArrivalPaint(node, source, () => {assert.equal(lease.current(), false);return .42;}, 'exit');
  assert.equal(hasExitPaint(node, {}), false);assert.equal(hasExitPaint(node, source), true);
  assert.equal(takeArrivalPaint(node, source), .42);assert.equal(hasExitPaint(node, source), false);
  ownArrivalPaint(node, source, () => .7);assert.equal(hasExitPaint(node, source), false);
});
