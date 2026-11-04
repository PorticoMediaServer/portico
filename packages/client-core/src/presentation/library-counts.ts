import {defaultI18n} from '../../../i18n/src/index.ts';

/** A library summary's count kinds as `lib.count` names them ("12 movies", "8 books", "7 authors"). One map for every
 * surface that prints a library's counts, so a kind the server adds is named the same way everywhere. */
const pivots: Readonly<Record<string, string>> = Object.freeze({
  movie: 'movies', show: 'shows', episode: 'episodes', artist: 'artists', album: 'albums', song: 'songs', book: 'books', ebook: 'books',
  author: 'authors', comic: 'comics', magazine: 'magazines', newspaper: 'newspapers', collection: 'collections', genre: 'genres', series: 'series',
});
export type LibraryCount = Readonly<{kind: string; count: number; atLeast?: boolean}>;
export const libraryCountPivot = (kind: string): string => pivots[kind] ?? kind;
export function libraryCountText(count: LibraryCount): string {
  return defaultI18n.t(count.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: count.count, pivot: libraryCountPivot(count.kind)});
}
