/** Authored UNRUN pure UI-packet vectors. No command admission, recogniser,
 * native scheduling, renderer or playback acceptance is implied. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {GestureCommitScope} from '../src-next/kit/controls/gesture-commit.ts';

test('queued begin/commit cannot revive an original unmounted UI occurrence', () => {
  const original = new GestureCommitScope<object>(), ticket = original.activate(), pair = {};
  original.retire(ticket); original.bind(ticket, 1, pair);
  assert.equal(original.claim(ticket, 1), undefined);
});
test('effect replay issues a fresh ticket and refuses the old callback instead of adopting it', () => {
  const original = new GestureCommitScope<object>(), first = original.activate(), pair = {};
  original.retire(first); const second = original.activate();
  assert.notEqual(second, first); original.bind(first, 1, pair);
  assert.equal(original.claim(second, 1), undefined);
  original.bind(second, 1, pair); assert.equal(original.claim(second, 1), pair);
});
test('out-of-order begin/finalize/release cannot replace or clear a newer actual finger run', () => {
  const scope = new GestureCommitScope<object>(), ticket = scope.activate(), first = {}, second = {};
  scope.bind(ticket, 1, first); scope.bind(ticket, 2, second);
  scope.bind(ticket, 1, first); scope.discard(ticket, 1);
  assert.equal(scope.claim(ticket, 1), undefined);
  assert.equal(scope.claim(ticket, 2), second);
  assert.equal(scope.claim(ticket, 2), undefined);
});
test('claim is consumed before callback reentry, and stale retirement never clears a fresh scope', () => {
  const scope = new GestureCommitScope<object>(), first = scope.activate(), packet = {};
  scope.bind(first, 1, packet); assert.equal(scope.claim(first, 1), packet);
  scope.bind(first, 1, packet); assert.equal(scope.claim(first, 1), undefined);
  const second = scope.activate(); scope.bind(second, 1, packet);
  assert.equal(scope.retire(first), false);
  assert.equal(scope.claim(second, 1), packet);
});
