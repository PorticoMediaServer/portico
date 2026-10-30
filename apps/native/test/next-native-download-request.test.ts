import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeDownloadRequest} from '../src-next/screens/watch/download-request.ts';

const entry = (kind: string, id = 'actual-id', title = 'Stored title', displayTitle?: string) =>
  ({kind, id, title, ...(displayTitle ? {displayTitle} : {})}) as never;

test('the title Download action names the real item or the page container', () => {
  assert.deepEqual(nativeDownloadRequest(entry('movie')), {title: 'Stored title', itemId: 'actual-id'});
  assert.deepEqual(nativeDownloadRequest(entry('episode', 'e1')), {title: 'Stored title', itemId: 'e1'});
  assert.deepEqual(nativeDownloadRequest(entry('book', 'b1')), {title: 'Stored title', itemId: 'b1'});
  assert.deepEqual(nativeDownloadRequest(entry('show', 's1')), {title: 'Stored title', container: {kind: 'show', id: 's1'}});
  assert.deepEqual(nativeDownloadRequest(entry('season', 'se1')), {title: 'Stored title', container: {kind: 'season', id: 'se1'}});
  assert.deepEqual(nativeDownloadRequest(entry('album', 'a1')), {title: 'Stored title', container: {kind: 'album', id: 'a1'}});
  assert.deepEqual(nativeDownloadRequest(entry('playlist', 'p1')), {title: 'Stored title', container: {kind: 'playlist', id: 'p1'}});
  assert.deepEqual(nativeDownloadRequest(entry('movie', 'm1', 'Stored title', 'Display edition')), {title: 'Display edition', itemId: 'm1'});
  assert.equal(nativeDownloadRequest(entry('artist')), undefined, 'a kind with no download intent never opens the sheet');
  assert.equal(nativeDownloadRequest(entry('category')), undefined);
  assert.equal(nativeDownloadRequest(entry('extra')), undefined);
});
