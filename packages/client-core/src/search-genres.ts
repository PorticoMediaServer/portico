import type {BrowseRequestBody} from './browse.ts';
import type {LibraryContentApi} from './library-content.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {callServer} from './query/server-api.ts';

export type SearchGenreArt = Readonly<{kind: 'item' | 'show'; id: string; path: string}>;
export type SearchGenre = Readonly<{
  value: string; label: string; count: number;
  backdrop?: SearchGenreArt; covers: readonly SearchGenreArt[];
}>;
export type SearchGenrePage = Readonly<{
  queryId: string; revision: string; genres: readonly SearchGenre[]; hasGenres?: boolean;
  nextCursor?: string; previousCursor?: string; reloadCursor?: string;
}>;
export type SearchGenreRequest = Readonly<{limit?: number; cursor?: string; preview?: '' | 'backdrop' | 'covers'}>;
export type SearchGenreReadOptions = Readonly<{
  isCurrent?: () => boolean;
  // Existing controllers still pass these; the rebuilt directory has no preparation or leases.
  previousPage?: SearchGenrePage;
  onFallback?: () => void;
}>;
export type SearchGenreReadMetadata = Readonly<{reanchored: boolean; previousRevision?: string}>;
// Kept for the controllers' existing retirement and continuation calls. There is no server handle to release.
export function releaseSearchGenres(_page: SearchGenrePage | undefined): void {}
export function searchGenresReadMetadata(_page: SearchGenrePage): SearchGenreReadMetadata | undefined {return undefined;}

const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\x00-\x1f\x7f]/.test(value);
const safeCount = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;

function current(signal?: AbortSignal, isCurrent: () => boolean = () => true): void {
  if (signal?.aborted || !isCurrent()) throw Object.assign(new Error('Genre read cancelled.'), {name: 'AbortError'});
}

/** The rebuilt server's genres (`GET /v1/genres?counts=true`): the films' and shows' genres the profile may see, by
 * name, at most 200, each with its count and one cover. A tile's slot keeps only the genres with its art: `backdrop`
 * the cover; `covers`, two posters (`posters`, two of its titles' posters). The list is read whole and handed out a slice at a time. */
export async function readSearchGenres(api: LibraryContentApi, request: SearchGenreRequest = {}, signal?: AbortSignal,
  options: SearchGenreReadOptions = {}): Promise<SearchGenrePage> {
  const isCurrent = options.isCurrent;
  current(signal, isCurrent);
  const answer = await callServer(api, 'GET /v1/genres', {query: {counts: true}, signal});
  current(signal, isCurrent);
  if (!object(answer) || !Array.isArray(answer.genres) || answer.genres.length > 2000) throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_genre_response'});
  const genres = answer.genres.flatMap((entry): SearchGenre[] => {
    if (!object(entry) || !text(entry.value, 512) || !text(entry.label, 512)) return [];
    const cover = object(entry.cover) ? entry.cover : {};
    const backdrop = typeof cover.backdrop === 'string' ? cover.backdrop : typeof cover.poster === 'string' ? cover.poster : typeof cover.cover === 'string' ? cover.cover : undefined;
    // Two posters from two of its titles (`posters`, each an image map), for the phone's tile.
    const posters = (Array.isArray(entry.posters) ? entry.posters : []).flatMap(images => object(images) && typeof (images.poster ?? images.cover) === 'string' ? [String(images.poster ?? images.cover)] : []).slice(0, 2);
    const art = (path: string): SearchGenreArt => Object.freeze({kind: 'item' as const, id: entry.value as string, path});
    return [Object.freeze({value: entry.value, label: entry.label, count: safeCount(entry.count) ? entry.count : 0,
      ...(backdrop ? {backdrop: art(backdrop)} : {}), covers: Object.freeze(posters.map(art))})];
  });
  const eligible = request.preview === 'backdrop' ? genres.filter(genre => genre.backdrop) : request.preview === 'covers' ? genres.filter(genre => genre.covers.length === 2) : genres;
  const limit = Math.max(1, Math.min(request.limit ?? 50, 50)), offset = request.cursor && /^o\d{1,5}$/.test(request.cursor) ? Number(request.cursor.slice(1)) : 0;
  const shown = eligible.slice(offset, offset + limit), more = !request.preview && offset + limit < eligible.length;
  return Object.freeze({queryId: 'genres', revision: 'genres', hasGenres: genres.length > 0, genres: Object.freeze(shown),
    ...(more ? {nextCursor: `o${offset + limit}`} : {})});
}

/** A tile opens the exact movie/show domain its count describes through the
 * existing across-library browse read. Use this pivot for its capabilities
 * and context facets as well; the parent retains viewer/cursor authority. */
export const searchGenreBrowsePivot = 'video-titles';
export function searchGenreBrowseRequest(value: string): BrowseRequestBody {
  if (typeof value !== 'string' || value.length === 0) throw new Error('Invalid genre value.');
  return Object.freeze({pivot: searchGenreBrowsePivot,
    query: Object.freeze({variant: 'predicate' as const, field: 'genre', operator: 'equals' as const, value}),
    sort: Object.freeze([Object.freeze({field: 'title', direction: 'asc' as const})])});
}
