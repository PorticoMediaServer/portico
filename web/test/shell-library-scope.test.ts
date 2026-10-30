import test from 'node:test';
import assert from 'node:assert/strict';
import {activeLibraryId} from '../src/shell/model.ts';

test('a library page always marks the library in its own address', () => {
  assert.equal(activeLibraryId('/library/movies', undefined), 'movies');
  assert.equal(activeLibraryId('/library/movies', 'tv'), 'movies');
  assert.equal(activeLibraryId('/library/tv%20shows', 'movies'), 'tv shows');
});

test('a malformed library address claims no library rather than borrowing the remembered one', () => {
  assert.equal(activeLibraryId('/library/%', 'movies'), undefined);
  assert.equal(activeLibraryId('/library/', 'movies'), undefined);
});

test('title and person pages stay in the library that opened them', () => {
  assert.equal(activeLibraryId('/item/abc', 'movies'), 'movies');
  assert.equal(activeLibraryId('/item/abc/', 'tv'), 'tv');
  assert.equal(activeLibraryId('/person/abc', 'music'), 'music');
});

test('a direct title visit with no remembered library claims none', () => {
  assert.equal(activeLibraryId('/item/abc', undefined), undefined);
  assert.equal(activeLibraryId('/person/abc', undefined), undefined);
});

test('destinations outside a library do not inherit the remembered library', () => {
  for (const path of ['/', '/search', '/saved', '/live', '/libraries', '/settings', '/home/rows/row', '/read/book'])
    assert.equal(activeLibraryId(path, 'movies'), undefined, path);
});
