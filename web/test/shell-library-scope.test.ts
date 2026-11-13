import test from 'node:test';
import assert from 'node:assert/strict';
import {activeLibraryId} from '../src/shell/model.ts';
import {claimTitleLibrary, forgetTitleLibrary, noteLibraryVisit, titleLibrary} from '../src/shell/title-library.ts';

test('a library page always marks the library in its own address', () => {
  assert.equal(activeLibraryId('/library/movies'), 'movies');
  assert.equal(activeLibraryId('/library/movies', {last: 'tv'}), 'movies');
  assert.equal(activeLibraryId('/library/tv%20shows', {last: 'movies'}), 'tv shows');
});

test('a malformed library address claims no library rather than borrowing another', () => {
  assert.equal(activeLibraryId('/library/%', {last: 'movies'}), undefined);
  assert.equal(activeLibraryId('/library/', {last: 'movies'}), undefined);
});

test('a title page marks its own library, not the last one visited', () => {
  assert.equal(activeLibraryId('/item/abc', {title: {itemId: 'abc', libraryId: 'movies'}, last: 'tv'}), 'movies');
  assert.equal(activeLibraryId('/item/abc/', {title: {itemId: 'abc', libraryId: 'movies'}}), 'movies');
  // Before its own identity arrives (or a claim for another title), a title page marks none.
  assert.equal(activeLibraryId('/item/abc', {title: {itemId: 'other', libraryId: 'tv'}, last: 'tv'}), undefined);
});

test('a person page keeps the last library claimed in this session, and a direct visit claims none', () => {
  assert.equal(activeLibraryId('/person/abc', {last: 'music'}), 'music');
  assert.equal(activeLibraryId('/person/abc'), undefined);
});

test('claims from a title and from a library visit both become the last library', () => {
  claimTitleLibrary('abc', 'movies');
  assert.deepEqual(titleLibrary.getSnapshot(), {title: {itemId: 'abc', libraryId: 'movies'}, last: 'movies'});
  noteLibraryVisit('tv');
  assert.equal(titleLibrary.getSnapshot().last, 'tv');
  assert.equal(activeLibraryId('/item/abc', titleLibrary.getSnapshot()), 'movies');
});

test('destinations outside a library claim none', () => {
  for (const path of ['/', '/search', '/saved', '/live', '/libraries', '/settings', '/home/rows/row', '/read/book'])
    assert.equal(activeLibraryId(path, {title: {itemId: 'book', libraryId: 'books'}, last: 'movies'}), undefined, path);
});

test('a sign-out or another profile starts with no library claimed: the next viewer never sees the last one highlighted', () => {
  claimTitleLibrary('abc', 'movies');
  forgetTitleLibrary();
  assert.deepEqual(titleLibrary.getSnapshot(), {});
  assert.equal(activeLibraryId('/person/abc', titleLibrary.getSnapshot()), undefined);
});
