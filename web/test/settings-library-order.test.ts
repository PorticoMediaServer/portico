import test from 'node:test';
import assert from 'node:assert/strict';
import {movedLibraryIds} from '../src/screens/settings/libraries/library-order.ts';

test('pointer drops move one stable ID and retain the full library order', () => {
  const original = Object.freeze(['a', 'b', 'c', 'd'].map(id => Object.freeze({id})));
  assert.deepEqual(movedLibraryIds(original, 'a', 3), ['b', 'c', 'd', 'a']);
  assert.deepEqual(movedLibraryIds(original, 'd', 0), ['d', 'a', 'b', 'c']);
  assert.deepEqual(movedLibraryIds(original, 'b', 2), ['a', 'c', 'b', 'd']);
  assert.deepEqual(original.map(row => row.id), ['a', 'b', 'c', 'd']);
});

test('a canceled, missing, unchanged or invalid drop emits no order', () => {
  const items = [{id: 'a'}, {id: 'b'}];
  for (const to of [-1, 2, .5, NaN, Infinity]) assert.equal(movedLibraryIds(items, 'a', to), undefined);
  assert.equal(movedLibraryIds(items, 'missing', 0), undefined);
  assert.equal(movedLibraryIds(items, 'a', 0), undefined);
  assert.equal(movedLibraryIds([], 'a', 0), undefined);
});
