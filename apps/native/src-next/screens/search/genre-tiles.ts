import type {NativeSearchGenre} from './types';

/** Ruling 36: the producer owns ranking and representatives. Each authored
 * surface takes its prefix after excluding genres without that surface's art. */
export function nativeSearchGenreTiles(genres: readonly NativeSearchGenre[], surface: 'phone' | 'phone-empty' | 'tv'): readonly NativeSearchGenre[] {
  const covers = surface === 'phone';
  // Stopgap, not a deferral: a phone genre without its two posters (the server does not send them yet; being
  // built by the browse-reads lane) is drawn with its backdrop instead.
  const twoCovers = (genre: NativeSearchGenre) => genre.coverPaths.length >= 2 && genre.coverPaths.slice(0, 2).every(path => !!path);
  return genres.filter(genre => covers ? twoCovers(genre) || !!genre.backdropPath : !!genre.backdropPath)
    .slice(0, covers ? 6 : 4);
}
