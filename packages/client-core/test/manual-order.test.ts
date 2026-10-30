import test from 'node:test';
import assert from 'node:assert/strict';
import {manualMoves, movedOrder, moveRequest, moveRequestAt, moveTarget, placeBeforeMoves} from '../src/presentation/manual-order.ts';

test('a move sends the entry and the one it follows, never the whole order', () => {
  const ids = ['a', 'b', 'c', 'd'];
  assert.deepEqual(moveRequest(ids, 2, 0), {entryId: 'c', afterEntryId: ''}, 'to the front');
  assert.deepEqual(moveRequest(ids, 0, 1), {entryId: 'a', afterEntryId: 'b'}, 'down one');
  assert.deepEqual(moveRequest(ids, 3, 1), {entryId: 'd', afterEntryId: 'a'}, 'a drag up');
  assert.deepEqual(moveRequest(ids, 1, 3), {entryId: 'b', afterEntryId: 'd'}, 'to the end');
  assert.equal(moveRequest(ids, 1, 1), undefined);
  assert.equal(moveRequest(ids, 9, 0), undefined);
  for (const [from, to] of [[2, 0], [0, 1], [3, 1], [1, 3], [0, 3]] as const) {
    const req = moveRequest(ids, from, to)!;
    const after = movedOrder(ids, from, to);
    assert.equal(after.indexOf(req.entryId), req.afterEntryId === '' ? 0 : after.indexOf(req.afterEntryId) + 1, `${from}→${to}: the server's anchor gives the order shown`);
  }
});

test('the moves offered for an entry; to the bottom only with the whole list loaded', () => {
  assert.deepEqual(manualMoves(0, 3, true), ['down', 'bottom']);
  assert.deepEqual(manualMoves(1, 3, true), ['top', 'up', 'down', 'bottom']);
  assert.deepEqual(manualMoves(1, 3, false), ['top', 'up', 'down']);
  assert.deepEqual(manualMoves(2, 3, true), ['top', 'up']);
  assert.deepEqual(manualMoves(0, 1, true), []);
  assert.equal(moveTarget('up', 0, 3), 0);
  assert.equal(moveTarget('bottom', 0, 3), 2);
});

test('a move in a list only partly on hand needs only the moved entry and its new neighbour, and shows at once', () => {
  const ids = ['a', 'b', 'c', 'd', 'e'];
  const loaded = (i: number) => ids[i];
  for (let from = 0; from < ids.length; from++) for (let to = 0; to < ids.length; to++) {
    assert.deepEqual(moveRequestAt(loaded, ids.length, from, to), moveRequest(ids, from, to), `${from}->${to}`);
    if (from === to) continue;
    const after = movedOrder(ids, from, to);
    assert.deepEqual(after.map((_, i) => ids[placeBeforeMoves(i, [{from, to}])]), after, `shown ${from}->${to}`);
  }
  // Two moves before the list is read again.
  const twice = movedOrder(movedOrder(ids, 4, 0), 1, 3);
  assert.deepEqual(twice.map((_, i) => ids[placeBeforeMoves(i, [{from: 4, to: 0}, {from: 1, to: 3}])]), twice);
  // The neighbour isn't loaded: nothing is sent.
  assert.equal(moveRequestAt(i => (i === 4 ? 'e' : undefined), 5, 4, 2), undefined);
  assert.deepEqual(moveRequestAt(i => (i === 4 ? 'e' : undefined), 5, 4, 0), {entryId: 'e', afterEntryId: ''});
});
