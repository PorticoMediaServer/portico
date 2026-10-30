import test from 'node:test';
import assert from 'node:assert/strict';
import {commitPreparedResourceOrder, resourceOrderEdge, resourceOrderIds, resourceOrderMove, resourceOrderPosition, type ResourceOrderPage} from '../src/screens/saved/resource-order.ts';

test('the final reorder admission and mutation are atomic after asynchronous preparation', async () => {
  let revision = 7;
  const writes: number[] = [];
  await assert.rejects(commitPreparedResourceOrder({current: () => revision === 7, prepare: async () => {
    assert.equal(revision, 7);
    queueMicrotask(() => {revision = 8;});
  }}, async () => {writes.push(revision);}), error => (error as {code?: string}).code === 'revision_mismatch');
  assert.deepEqual(writes, [], 'a newer revision cannot authorize the old page permutation');
  revision = 7;
  await commitPreparedResourceOrder({prepare: async () => {}, current: () => {
    queueMicrotask(() => {revision = 8;});
    return revision === 7;
  }}, async () => {writes.push(revision);});
  assert.deepEqual(writes, [7], 'mutation captures the admitted revision without another yield');
});

test('Reorder preserves occurrence identity and the supplied order window', () => {
  const ids = Object.freeze(['outside-left', 'a', 'hidden', 'b', 'outside-right']);
  assert.deepEqual(resourceOrderMove(ids, 'a', 'b'), {action: 'reorder', entryIds: ['hidden', 'b', 'a']});
  assert.deepEqual(resourceOrderMove(ids, 'b', 'a'), {action: 'reorder', entryIds: ['b', 'a', 'hidden']});
  assert.deepEqual(ids, ['outside-left', 'a', 'hidden', 'b', 'outside-right']);
  // Duplicate media can have distinct occurrence ids and must both survive.
  assert.deepEqual(resourceOrderMove(['movie-first', 'movie-second'], 'movie-first', 'movie-second'),
    {action: 'reorder', entryIds: ['movie-second', 'movie-first']});
});

test('Reorder refuses unknown, duplicate, unchanged or oversized windows', () => {
  assert.equal(resourceOrderMove(['a', 'b'], 'absent', 'b'), undefined);
  assert.equal(resourceOrderMove(['a', 'b'], 'a', 'absent'), undefined);
  assert.equal(resourceOrderMove(['a', 'a', 'b'], 'a', 'b'), undefined);
  assert.equal(resourceOrderMove(['a', 'b'], 'a', 'a'), undefined);
  const ids = Array.from({length: 201}, (_, i) => String(i));
  assert.equal(resourceOrderMove(ids, '0', '200'), undefined);
  assert.equal(resourceOrderMove(ids, '0', '199')?.entryIds.length, 200);
});

// A page of 40..41 in a 300-entry playlist: its neighbours and tail come from the server.
const middle: ResourceOrderPage = {ids: ['a', 'hidden', 'b'], start: 40, count: 300, witness: {previous: 'before', next: 'after', last: 'tail'}};

test('A page reaches across its edges through its witnessed neighbours', () => {
  const ids = resourceOrderIds(middle);
  assert.deepEqual(ids, ['before', 'a', 'hidden', 'b', 'after']);
  assert.deepEqual(resourceOrderMove(ids, 'a', 'before'), {action: 'reorder', entryIds: ['a', 'before']});
  assert.deepEqual(resourceOrderMove(ids, 'b', 'after'), {action: 'reorder', entryIds: ['after', 'b']});
  assert.deepEqual(resourceOrderIds({...middle, start: 0, witness: {last: 'b'}}), ['a', 'hidden', 'b']);
});

test('Move to top uses the front sentinel; Move to bottom only the witnessed tail', () => {
  assert.deepEqual(resourceOrderEdge(middle, 'a', 'top'), {action: 'reorder', entryIds: ['a'], afterEntryId: ''});
  assert.deepEqual(resourceOrderEdge(middle, 'b', 'bottom'), {action: 'reorder', entryIds: ['b'], afterEntryId: 'tail'});
  assert.equal(resourceOrderEdge({...middle, start: 0}, 'a', 'top'), undefined, 'already first');
  assert.equal(resourceOrderEdge({...middle, witness: {...middle.witness, last: 'b'}}, 'b', 'bottom'), undefined, 'already last');
  assert.equal(resourceOrderEdge({...middle, witness: undefined}, 'a', 'top'), undefined, 'no witness, no move');
  assert.equal(resourceOrderEdge(middle, 'missing', 'top'), undefined);
});

test('Announced positions are absolute in the whole resource', () => {
  const ids = resourceOrderIds(middle);
  assert.equal(resourceOrderPosition(resourceOrderMove(ids, 'a', 'b')!, middle, 'a'), 43);
  assert.equal(resourceOrderPosition(resourceOrderMove(ids, 'a', 'before')!, middle, 'a'), 40);
  assert.equal(resourceOrderPosition(resourceOrderEdge(middle, 'b', 'top')!, middle, 'b'), 1);
  assert.equal(resourceOrderPosition(resourceOrderEdge(middle, 'a', 'bottom')!, middle, 'a'), 300);
  assert.equal(resourceOrderPosition({action: 'reorder', entryIds: ['a'], afterEntryId: 'b'}, middle, 'a'), undefined);
});
