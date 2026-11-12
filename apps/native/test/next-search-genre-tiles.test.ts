import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles.ts';
import type {NativeSearchGenre} from '../src-next/screens/search/types.ts';

test('ruling 36 surface prefixes preserve producer order and skip only the art missing for that frame', () => {
  const genres: NativeSearchGenre[] = Array.from({length: 10}, (_, index) => ({id: `facet-${index}`, name: `Metadata ${index}`, count: 100 - index,
    backdropPath: index === 0 ? undefined : `/actual/backdrop/${index}`, coverPaths: index === 1 ? ['/actual/only-poster'] : [`/actual/poster/${index}-a`, `/actual/poster/${index}-b`]}));
  assert.deepEqual(nativeSearchGenreTiles(genres, 'phone').map(genre => genre.id), ['facet-0', 'facet-2', 'facet-3', 'facet-4', 'facet-5', 'facet-6']);
  for (const surface of ['phone-empty', 'tv'] as const) {
    const shown = nativeSearchGenreTiles(genres, surface);
    assert.deepEqual(shown.map(genre => genre.id), ['facet-1', 'facet-2', 'facet-3', 'facet-4']);
    assert.equal(shown[0], genres[1], 'metadata labels, counts and representative art remain the producer objects');
  }
  assert.deepEqual(nativeSearchGenreTiles([{id: 'none', name: 'Actual metadata', count: 3, coverPaths: []}], 'phone'), []);
  assert.deepEqual(nativeSearchGenreTiles([{id: 'none', name: 'Actual metadata', count: 3, coverPaths: []}], 'phone-empty'), []);
  assert.equal(genres.length, 10, 'the full producer list remains available to All genres');
});
