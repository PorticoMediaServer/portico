import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryNavigationIcon} from '../src/index.ts';
import {nextIcons, type IconName} from '../../design/src/next-icons.ts';

test('actual backend library kinds keep distinct approved navigation glyphs through the root API', () => {
  const kinds = ['movie', 'tv', 'anime', 'music', 'audiobook'];
  assert.deepEqual(kinds.map(kind => libraryNavigationIcon(kind, 'touch')), ['i-film', 'i-tvlib', 'i-category', 'i-music', 'i-headphones']);
  assert.deepEqual(kinds.map(kind => libraryNavigationIcon(kind, 'tv')), ['i-film', 'i-tvlib', 'i-category', 'i-music', 'i-book']);
});
test('explicit read kinds distinguish books and comics from listening, without manufacturing libraries', () => {
  for (const platform of ['touch', 'tv'] as const) {
    assert.equal(libraryNavigationIcon('book', platform), 'i-book');
    assert.equal(libraryNavigationIcon('books', platform), 'i-book');
    assert.equal(libraryNavigationIcon('comics', platform), 'i-panels');
  }
});
test('unknown kinds use the existing film fallback; names and unsupported aliases are not kind inference', () => {
  for (const kind of ['', 'Movies', 'audiobooks', 'TV Shows', 'future-kind', '__proto__']) {
    assert.equal(libraryNavigationIcon(kind, 'touch'), 'i-film');
    assert.equal(libraryNavigationIcon(kind, 'tv'), 'i-film');
  }
});
test('every output is available in the canonical generated catalogue for both consumers', () => {
  for (const platform of ['touch', 'tv'] as const) for (const kind of ['movie', 'tv', 'anime', 'music', 'audiobook', 'book', 'books', 'comics', 'unknown']) {
    const icon: IconName = libraryNavigationIcon(kind, platform);
    assert.ok(Object.hasOwn(nextIcons, icon));
  }
  // Other approved surfaces add glyphs independently. Navigation's invariant
  // is that every returned ID exists, not a fixed size for the entire sprite.
});
