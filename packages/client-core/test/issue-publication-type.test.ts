import test from 'node:test';
import assert from 'node:assert/strict';
import {entryFromItem, type Item} from '../src/items.ts';

const item = (kind: string, publicationType?: string) => ({id: 'itm_x', kind, title: 'Scientific American 1846-05-14', libraryId: 'lib_books', addedAt: '2026-11-21T00:00:00Z',
  images: {}, state: {watched: false, inList: false, favorite: false, notInterested: false, playCount: 0}, ...(publicationType ? {publicationType} : {})}) as unknown as Item;

test('a magazine or newspaper issue says so on its card, so its page links to the archive; other titles carry nothing', () => {
  assert.equal(entryFromItem(item('book', 'magazine'))?.publicationType, 'magazine');
  assert.equal(entryFromItem(item('issue', 'newspaper'))?.publicationType, 'newspaper');
  assert.equal(entryFromItem(item('book'))?.publicationType, undefined);
  assert.equal(entryFromItem(item('movie', 'magazine'))?.publicationType, undefined);
});
