import type {NativeSearchGenre} from './types';

/** Ruling 36: the producer owns ranking and representatives. Each authored
 * surface takes its prefix after excluding genres without that surface's art. */
export function nativeSearchGenreTiles(genres: readonly NativeSearchGenre[], surface: 'phone' | 'phone-empty' | 'tv'): readonly NativeSearchGenre[] {
  const covers = surface === 'phone';
  return genres.filter(genre => covers ? genre.coverPaths.length >= 2 && genre.coverPaths.slice(0, 2).every(path => !!path) : !!genre.backdropPath)
    .slice(0, covers ? 6 : 4);
}
