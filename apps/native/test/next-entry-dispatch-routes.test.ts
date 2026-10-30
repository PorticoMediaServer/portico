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
