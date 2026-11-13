import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeEntryRoute, nativeEntryRouteSupported} from '../src-next/shell/routes.ts';
import type {ContentEntry, ContentEntryKind} from '../../../packages/client-core/src/library-content.ts';

const entry = (kind: ContentEntryKind): ContentEntry => ({id: 'actual', libraryId: 'library', kind, title: 'Actual'});

test('catalogue routes preserve real owners and refuse unsupported fallthrough', () => {
  for (const [kind, route] of Object.entries({movie: 'title', show: 'title', season: 'title', episode: 'title', extra: 'title',
    album: 'music', artist: 'music', book: 'book', author: 'audiobook', book_series: 'audiobook', playlist: 'saved-destination', collection: 'collection'})) {
    const offered = entry(kind as ContentEntryKind);
    assert.equal(nativeEntryRouteSupported(offered), true, kind);
    assert.equal(nativeEntryRoute(offered).kind, route, kind);
  }
  assert.deepEqual(nativeEntryRoute(entry('collection')), {kind: 'collection', libraryId: 'library', collectionId: 'actual', title: 'Actual'});
  for (const kind of ['ebook', 'comic', 'song', 'audiobook_file', 'chapter', 'disc', 'radio', 'category'] as const)
    assert.equal(nativeEntryRouteSupported(entry(kind)), false, kind);
  assert.equal(nativeEntryRouteSupported({...entry('collection'), libraryId: undefined}), false);
});

test("a reading library's series card opens the reading series page; an audiobook series keeps the audiobook page", () => {
  // A book or comic library's series card carries the reading series navigation (client-core entryFromItem, from its publication).
  const reading = {...entry('book_series'), navigation: {view: 'series', entityId: 'actual'}} as ContentEntry;
  assert.equal(nativeEntryRouteSupported(reading), true);
  assert.deepEqual(nativeEntryRoute(reading), {kind: 'read-series', libraryId: 'library', seriesId: 'actual'});
  const audiobook = {...entry('book_series'), navigation: {view: 'book_series', entityId: 'actual'}} as ContentEntry;
  assert.equal(nativeEntryRoute(audiobook).kind, 'audiobook');
  assert.equal(nativeEntryRoute(entry('book_series')).kind, 'audiobook', 'no navigation: the audiobook page, as before');
});

test("a reading series or periodical page takes the rebuilt server's item ids, and only URL-safe ones", async () => {
  // A rebuilt series id (itm_…) once failed the old 43-character check, so the series page drew nothing.
  const {readingRouteId} = await import('../src-next/screens/read/platform.ts');
  for (const id of ['itm_5c6x6e3jzejgb5wz', 'a'.repeat(43), 'A-b_9']) assert.equal(readingRouteId(id), true, id);
  for (const id of ['', 'itm/../x', 'itm 1', 'itm?x', 'a'.repeat(129)]) assert.equal(readingRouteId(id), false, id);
});
