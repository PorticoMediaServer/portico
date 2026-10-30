import test from 'node:test';
import assert from 'node:assert/strict';
import {askNativeDownload, closeNativeDownload, getNativeDownloadSnapshot, subscribeNativeDownload} from '../src-next/screens/downloads/request.ts';

test('a title page raises exactly one real download intent and the shell can close it', () => {
  const seen: unknown[] = [], off = subscribeNativeDownload(() => seen.push(getNativeDownloadSnapshot()));
  try {
    askNativeDownload({itemId: 'movie-1', title: 'The Film'});
    assert.deepEqual(getNativeDownloadSnapshot(), {itemId: 'movie-1', title: 'The Film'});
    askNativeDownload({container: {kind: 'season', id: 'season-1'}, title: 'The Show'});
    assert.deepEqual(getNativeDownloadSnapshot(), {container: {kind: 'season', id: 'season-1'}, title: 'The Show'}, 'a new intent replaces the sheet request');
    assert.equal(seen.length, 2);
    closeNativeDownload();
    assert.equal(getNativeDownloadSnapshot(), undefined);
    closeNativeDownload();
    assert.equal(seen.length, 3, 'closing twice does not publish an empty change');
  } finally {off();}
});

test('an intent without a real item or container is refused, never a blank sheet', () => {
  closeNativeDownload();
  askNativeDownload({title: 'Nothing'});
  assert.equal(getNativeDownloadSnapshot(), undefined);
  askNativeDownload({itemId: '', title: 'Nothing'});
  assert.equal(getNativeDownloadSnapshot(), undefined);
  askNativeDownload({container: {kind: 'season', id: ''}, title: 'Nothing'});
  assert.equal(getNativeDownloadSnapshot(), undefined);
});
