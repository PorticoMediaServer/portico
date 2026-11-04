import test from 'node:test';
import assert from 'node:assert/strict';
import {EPUB_LIMITS, epubLimits} from '../src/epub/limits.ts';
import {resourceAssets} from '../src/epub/resources.ts';

// RD-5 (#684): accepted EPUB sessions carry the server's `limits`; the reader's checks use them, so the
// two can't disagree. A local file without a manifest uses the same defaults.
const MiB = 1024 * 1024;
const source = (sizes: number[], limits?: {memberBytes: number; totalBytes: number}) => ({id: 'book', revision: 'r', size: 1, ...(limits ? {limits} : {}),
  resources: {entries: sizes.map((size, i) => ({path: `OEBPS/m${i}.xhtml`, mime: 'application/xhtml+xml', size})), async read() {return new Uint8Array();}}});

test('the defaults are the server\'s: 16 MiB a member, 256 MiB a book', () => {
  assert.deepEqual(EPUB_LIMITS, {memberBytes: 16 * MiB, totalBytes: 256 * MiB});
  assert.equal(epubLimits(undefined), EPUB_LIMITS);
});

test('a book\'s own limits decide its members and total, above or below the defaults', () => {
  assert.throws(() => resourceAssets(source([20 * MiB]) as never), {code: 'limit_exceeded'}, 'over the default member size');
  assert.doesNotThrow(() => resourceAssets(source([20 * MiB], {memberBytes: 32 * MiB, totalBytes: 512 * MiB}) as never), 'the server allows it, so the reader does');
  assert.throws(() => resourceAssets(source([5 * MiB], {memberBytes: 4 * MiB, totalBytes: 64 * MiB}) as never), {code: 'limit_exceeded'}, 'a tighter member limit');
  assert.throws(() => resourceAssets(source([3 * MiB, 3 * MiB], {memberBytes: 4 * MiB, totalBytes: 5 * MiB}) as never), {code: 'limit_exceeded'}, 'a tighter total');
});

test('malformed limits refuse the book rather than fall back silently', () => {
  for (const limits of [{memberBytes: 0, totalBytes: 1}, {memberBytes: 10, totalBytes: 5}, {memberBytes: 1.5, totalBytes: 10}, {memberBytes: -1, totalBytes: 10}]) {
    assert.throws(() => epubLimits(limits), {code: 'invalid_source'}, JSON.stringify(limits));
  }
});
