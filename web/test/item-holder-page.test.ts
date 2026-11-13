import test from 'node:test';
import assert from 'node:assert/strict';
import {holderPage} from '../src/screens/detail/model.ts';
import {entryFromItem} from '@core/items.ts';
import {redirectOnce, type RedirectMemory} from '../src/shell/navigation.ts';

test('a title with a page of its own, opened by its address, goes to that page in its library; a single title stays', () => {
  for (const [kind, view] of [['show', 'show'], ['season', 'season'], ['artist', 'artist'], ['album', 'album'], ['book', 'book'], ['book_series', 'book_series'], ['collection', 'collection']])
    assert.deepEqual(holderPage({id: 'itm_' + kind, kind}, 'lib'), {libraryId: 'lib', view, entityId: 'itm_' + kind}, kind);
  for (const kind of ['movie', 'episode', 'song', 'ebook', 'comic']) assert.equal(holderPage({id: 'itm_x', kind}, 'lib'), undefined, kind);
  assert.equal(holderPage({id: 'itm_show', kind: 'show'}, ''), undefined, 'no library, no page to go to');
});

// An address and a click land on the same page: the title page's kind and a card's navigation agree for every kind.
test('every kind the server lists opens the same page by its address as by its card', () => {
  for (const kind of ['movie', 'show', 'season', 'episode', 'artist', 'album', 'track', 'audiobook', 'book', 'comic', 'issue', 'series', 'collection']) {
    const card = entryFromItem({id: 'itm_1', kind, title: 'T', libraryId: 'lib'} as never)!;
    assert.equal(holderPage({id: 'itm_1', kind: card.kind}, 'lib')?.view ?? 'item', card.navigation?.view, kind);
  }
});

test('a show, a season or an album opened by its address is redirected once per title, however often the route resolves it', () => {
  const memory: RedirectMemory = {}, went: string[] = [];
  // The route resolves the same title again while the router is still on its way: no second navigation.
  for (let resolved = 0; resolved < 5; resolved++) redirectOnce(memory, 'itm_show|show:lib_tv', () => went.push('show'));
  assert.deepEqual(went, ['show']);
  // Another title (or the same title now in another library) is a new redirect.
  redirectOnce(memory, 'itm_album|album:lib_music', () => went.push('album'));
  redirectOnce(memory, 'itm_album|album:lib_music', () => went.push('album'));
  assert.deepEqual(went, ['show', 'album']);
});
