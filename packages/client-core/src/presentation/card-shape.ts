import type {ContentEntryKind} from '../library-content.ts';

export type CardShape = 'poster' | 'square' | 'landscape' | 'circle';

/**
 * Where a card is drawn. Justin's rule (Audit Decisions, "Card shapes"):
 * - `shelf` / `grid`: Home rows, library grids, search, Saved, related rows.
 *   Movies, TV and anime are posters, episodes included; music and audiobooks are square
 *   (audiobook covers are square: Justin, 2 Oct 2026).
 * - `showWorkspace`: the episode list inside a TV or anime show's own page — the
 *   only place an episode is landscape.
 * - `detailExtras`: trailers and extras on a detail page (video clips, landscape).
 * - `hero`: the large artwork at the top of a detail page (the artist hero is a circular cut-out).
 * - `list`: list rows (thumbnail follows the shelf shape).
 */
export type ShapeContext = 'shelf' | 'grid' | 'list' | 'showWorkspace' | 'detailExtras' | 'hero';

/** The library family a card belongs to, when the kind alone is ambiguous (collections, categories). */
export type ShapeFamily = 'movie' | 'tv' | 'anime' | 'audiobook' | 'music';

const musicKinds: ReadonlySet<ContentEntryKind> = new Set(['artist', 'album', 'song', 'disc']);
const bookKinds: ReadonlySet<ContentEntryKind> = new Set(['book', 'audiobook_file', 'book_series', 'chapter']);
const personKinds: ReadonlySet<ContentEntryKind> = new Set(['author']);

/** One card geometry rule for every client (X-14, V-3, PC-VISUAL §13.2–13.4). */
export function shapeFor(kind: ContentEntryKind, context: ShapeContext = 'shelf', family?: ShapeFamily): CardShape {
  if (kind === 'episode') return context === 'showWorkspace' ? 'landscape' : 'poster';
  if (kind === 'extra') return context === 'detailExtras' || context === 'showWorkspace' ? 'landscape' : 'poster';
  if (kind === 'artist') return context === 'hero' ? 'circle' : 'square';
  if (musicKinds.has(kind) || bookKinds.has(kind)) return 'square';
  if (personKinds.has(kind)) return 'circle';
  if ((kind === 'collection' || kind === 'category') && (family === 'music' || family === 'audiobook')) return 'square';
  // movie, show, season, collection, category
  return 'poster';
}

/** Width ÷ height for a shape, for reserving geometry before artwork loads (PC-VISUAL §13.1). */
export function shapeAspect(shape: CardShape): number {
  return shape === 'poster' ? 2 / 3 : shape === 'landscape' ? 16 / 9 : 1;
}

/** People (cast, crew, authors, members) are always circles. */
export const personShape: CardShape = 'circle';
