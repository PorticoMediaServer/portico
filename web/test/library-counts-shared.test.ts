import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {libraryCountText} from '@core/presentation/library-counts.ts';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('every library count kind the server publishes is named, not "title" (survey A13, A14)', () => {
  assert.equal(libraryCountText({kind: 'author', count: 7}), '7 authors');
  assert.equal(libraryCountText({kind: 'author', count: 1}), '1 author');
  assert.equal(libraryCountText({kind: 'book', count: 8}), '8 books');
  assert.equal(libraryCountText({kind: 'episode', count: 96}), '96 episodes');
  assert.equal(libraryCountText({kind: 'ebook', count: 2}), '2 books');
  assert.equal(libraryCountText({kind: 'comic', count: 6}), '6 comics');
  assert.equal(libraryCountText({kind: 'show', count: 24}), '24 shows');
});

test('one map serves every surface that prints library counts', () => {
  for (const file of ['../src/screens/browse/Libraries.tsx', '../src/screens/settings/libraries/LibraryHeader.tsx', '../src/screens/settings/libraries/LibrariesPanel.tsx', '../src/screens/read/useReadLibrarySummary.tsx', '../src/screens/browse/Library.route.tsx']) {
    const source = read(file);
    assert.match(source, /from '@core\/presentation\/library-counts\.ts'/, file);
    assert.doesNotMatch(source, /song: 'songs'/, file);
  }
});

test('A13: an Audiobooks library\'s header carries its counts (desktop-listen #books)', () => {
  const route = read('../src/screens/browse/Library.route.tsx');
  assert.match(route, /const audiobookRead = useMusicLibrarySummary\(captured, library\.id, library\.kind === 'audiobook', 'audiobook'\);/);
  assert.match(route, /counts: library\.kind === 'music' \? musicCounts : library\.kind === 'audiobook' \? audiobookCounts : readingHeader\.counts,/);
});
