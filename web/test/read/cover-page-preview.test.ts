import {test} from 'node:test';
import assert from 'node:assert/strict';
import {coverPagePreview} from '../../src/screens/read/cover-page-preview.ts';

// RD-2 (#699): the owner's "choose cover from pages" picker.
const png = new Uint8Array(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jC1sAAAAASUVORK5CYII=', 'base64'));
const session = (fail?: unknown) => ({async page(index: number) {if (fail) throw fail; return {index, name: 'p.png', mime: 'image/png', bytes: png};}}) as never;

test('RD-2 (#699): a page the server rejected as content has no preview, so it is a plain tile that can\'t be chosen instead of failing the picker', async () => {
  const rejected = Object.assign(new Error('reading_member_unreadable'), {name: 'ReadingError', code: 'reading_member_unreadable', status: 422});
  assert.deepEqual(await coverPagePreview(session(rejected), 4, new AbortController().signal), {index: 4});
  const readable = await coverPagePreview(session(), 2, new AbortController().signal);
  assert.equal(readable.index, 2); assert.match(readable.url!, /^blob:/);
  assert.deepEqual(new Uint8Array(await (await fetch(readable.url!)).arrayBuffer()), png); URL.revokeObjectURL(readable.url!);
});

test('RD-2 (#699): any other page failure, or one after the picker closed, still reaches the picker', async () => {
  const other = Object.assign(new Error('reading_source_changed'), {code: 'reading_source_changed', status: 409});
  await assert.rejects(coverPagePreview(session(other), 0, new AbortController().signal), other);
  const closed = new AbortController(); closed.abort();
  const rejected = Object.assign(new Error('reading_member_unreadable'), {code: 'reading_member_unreadable', status: 422});
  await assert.rejects(coverPagePreview(session(rejected), 0, closed.signal), rejected);
});

test('coverPagePreview does not allocate a blob URL when aborted before or during page fetch', async () => {
  const originalCreate = URL.createObjectURL;
  let created = 0;
  URL.createObjectURL = ((...args: Parameters<typeof URL.createObjectURL>) => {created++; return originalCreate(...args);}) as typeof URL.createObjectURL;
  try {
    const aborted = new AbortController();
    const slowSession = {async page(index: number) {aborted.abort(new DOMException('Aborted', 'AbortError')); return {index, name: 'p.png', mime: 'image/png', bytes: png};}} as never;
    await assert.rejects(coverPagePreview(slowSession, 0, aborted.signal), {name: 'AbortError'});
    assert.equal(created, 0);
  } finally {
    URL.createObjectURL = originalCreate;
  }
});
