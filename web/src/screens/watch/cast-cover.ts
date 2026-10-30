import type {ItemResponse} from '@core/media-item.ts';

/** The one related catalogue entity a Cast display may read for a cover when the
 * playing item's own projection has none. A song points at its album; an
 * audiobook file or chapter points at its book container (`book`), the same
 * expected kind the receiver's `relatedCover` uses. */
export type CastCoverTarget = Readonly<{id: string; kind: 'album' | 'book'}>;

const DIGIT = (code: number) => code >= 0x30 && code <= 0x39;
const UPPER = (code: number) => code >= 0x41 && code <= 0x5a;
const LOWER = (code: number) => code >= 0x61 && code <= 0x7a;

/** A complete bounded catalogue id: no line terminator, NUL, whitespace,
 * backslash, control or non-ASCII code unit, and at least one alphanumeric so
 * a dot-only id cannot normalise the item route. The caller still encodes it. */
function coverId(value: unknown): value is string {
  if (typeof value !== 'string' || value.length < 1 || value.length > 128) return false;
  let alphanumeric = false;
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    const alnum = DIGIT(code) || UPPER(code) || LOWER(code);
    if (!alnum && code !== 0x2e /* . */ && code !== 0x5f /* _ */ && code !== 0x3a /* : */ && code !== 0x2d /* - */) return false;
    if (alnum) alphanumeric = true;
  }
  return alphanumeric;
}

/** The receiver's single-slash catalogue-path shape with a complete printable
 * ASCII whitelist: no line terminator, NUL, whitespace, backslash, control or
 * non-ASCII code unit. At most 501 code units (one slash plus the receiver's
 * 500), and at least one character after the slash. */
function cataloguePath(value: unknown): value is string {
  if (typeof value !== 'string' || value.length < 2 || value.length > 501) return false;
  if (value.charCodeAt(0) !== 0x2f || value.charCodeAt(1) === 0x2f) return false;
  for (let index = 1; index < value.length; index++) {
    const code = value.charCodeAt(index);
    // Printable ASCII only, minus the backslash: this refuses space, NUL,
    // every control and line terminator, DEL, C1 and all non-ASCII code units.
    if (code < 0x21 || code > 0x7e || code === 0x5c /* \ */) return false;
  }
  return true;
}

function coverTarget(id: unknown, kind: CastCoverTarget['kind']): CastCoverTarget | undefined {
  return coverId(id) ? Object.freeze({id, kind}) : undefined;
}

/** `track` is the playback alias the server's audio choice accepts for a song;
 * `chapter` is the canonical audiobook chapter item kind. */
function songItem(kind: string): boolean { return kind === 'song' || kind === 'track'; }
function bookItem(kind: string): boolean { return kind === 'audiobook_file' || kind === 'chapter'; }

/**
 * The bounded one-hop cover target for an item with no poster and no backdrop of
 * its own. The item's actual kind selects the only relation it may use; presence
 * of the projected relation is the fact. No title, filename, current item or
 * other item is guessed, and a malformed relation on a non-audio kind reads
 * nothing. Returns undefined when the item already has display art or has no
 * exactly typed related id.
 */
export function castCoverTarget(item: ItemResponse): CastCoverTarget | undefined {
  if (item.posterUrl || item.backdropUrl) return undefined;
  if (songItem(item.kind)) return coverTarget(item.song?.albumId, 'album');
  if (bookItem(item.kind)) return coverTarget(item.bookFile?.bookId, 'book');
  return undefined;
}

/**
 * The related item's own poster, selected only when the caller's read is the
 * exact expected catalogue entity, proves `missing === false`, and carries a
 * safe complete catalogue path. Never falls back to a backdrop, another id or a
 * guessed URL. The caller owns the read, its currentness and the guard around
 * caching/publication; this helper performs no IO and holds no state.
 */
export function castCoverPath(expected: CastCoverTarget | undefined, related: ItemResponse | undefined): string | undefined {
  if (!expected || !related || related.id !== expected.id || related.missing !== false || related.kind !== expected.kind) return undefined;
  const poster = related.posterUrl;
  return cataloguePath(poster) ? poster : undefined;
}
