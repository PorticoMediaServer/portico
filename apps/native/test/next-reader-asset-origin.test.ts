import test from 'node:test';
import assert from 'node:assert/strict';
import {appleReaderEntry, androidReaderEntry, readerNavigationAllowed, readerDelivery} from '../src-next/screens/read/asset-origin';
test('trusted reader entry rejects every outside top navigation and only admits renderer subdocuments', () => {
  for (const entry of [appleReaderEntry, androidReaderEntry]) {
    assert.equal(readerNavigationAllowed(entry, entry, true), true);
    for (const url of ['https://selected-server.test/frontend', 'javascript:alert(1)', 'file:///private/book.epub', 'about:blank', 'blob:book', entry + '?server=x', entry.replace('index.html', '../private')])
      assert.equal(readerNavigationAllowed(entry, url, true), false);
    assert.equal(readerNavigationAllowed(entry, 'blob:renderer-owned', false), true);
    assert.equal(readerNavigationAllowed(entry, 'about:blank', false), true);
    assert.equal(readerNavigationAllowed(entry, 'https://outside.test', false), false);
  }
});
test('delivery encodes an opaque packet as data, never executable selected-server text', () => {
  const value = '</script>\u2028\u2029";throw new Error("bad")';
  let received: unknown;
  Function('window', readerDelivery(value))({PorticoReader: {receive: (message: unknown) => {received = message;}}});
  assert.equal(received, value);
  assert.throws(() => readerDelivery('x'.repeat(2 * 1024 * 1024 + 1)));
});
