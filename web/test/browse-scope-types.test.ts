import test from 'node:test';
import assert from 'node:assert/strict';
import {scopeTypes} from '../src/screens/browse/scope-types.ts';
test('scope chips use authorized kinds and actual ids, independently of owner library names', () => {
  assert.deepEqual(scopeTypes([{id: 'one', kind: 'movie', name: 'TV'}, {id: 'two', kind: 'movie', name: 'Another'},
    {id: 'three', kind: 'tv', name: 'Movies'}, {id: 'four', kind: 'anime', name: 'Personal name'}]), [
    {kind: 'movie', label: 'search.chip.movies', libraryIds: ['one', 'two']},
    {kind: 'tv', label: 'next.browse.tvShows', libraryIds: ['three']},
    {kind: 'anime', label: 'next.browse.kind.anime', libraryIds: ['four']},
  ]);
});
