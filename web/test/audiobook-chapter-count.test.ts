import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {audiobookChapterCount} from '../src/screens/detail/audiobooks/chapter-count.ts';

test('a one-file book with ten listed chapters says ten, not one', () => {
  assert.equal(audiobookChapterCount({entries: new Array(10).fill({}), totalCount: 10}, 1), 10);
});
test('the header keeps the full total when only a page of chapters has loaded', () => {
  assert.equal(audiobookChapterCount({entries: new Array(11).fill({}), totalCount: 40}, 1), 40);
});
test('without a chapter list the entity count stands', () => {
  assert.equal(audiobookChapterCount({entries: [], totalCount: 0}, 3), 3);
  assert.equal(audiobookChapterCount(undefined, undefined), undefined);
});
test('the book header uses the chapter list count', () => {
  assert.match(readFileSync(new URL('../src/screens/detail/audiobooks/Entry.tsx', import.meta.url), 'utf8'), /title\.chapterCount', \{count: chapterCount\}/);
});
