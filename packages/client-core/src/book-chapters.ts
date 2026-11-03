import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import type {ListeningRequest} from './listening.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {wireDurations} from './wire-units.ts';

/** One chapter of a whole book: the file part it is in (`itemId`), its place in that part
 * (`index`), its 1-based place in the book (`number`) and its span in the part's own seconds. */
export type BookChapter = Readonly<{id: string; itemId: string; index: number; number: number; title: string; startSeconds: number; endSeconds: number}>;
export type BookChapters = Readonly<{bookId: string; chapters: readonly BookChapter[]}>;

/** The playing book's chapters for a player's list: every part's chapters in book order, and
 * where the playing position is (`current`: an index into `chapters`, -1 before the first). */
export type BookChaptersView = Readonly<{bookId: string; itemId: string; phase: 'loading' | 'ready' | 'error'; chapters: readonly BookChapter[]; current: number}>;

const PAGE = 100;
/** No real book comes near this; a longer list stops here rather than reading on. */
const MAX_CHAPTERS = 4096;
const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 2048): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const id = (v: unknown): v is string => text(v, 256) && /^[A-Za-z0-9_-]+$/.test(v);
const counter = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) >= 0;
function bad(): never {throw new Error(unreadableServerResponse);}

function parseRow(v: unknown, number: number): BookChapter {
  const r = wireDurations(v, {startSeconds: 'startMs', endSeconds: 'endMs'});
  if (!obj(r) || !text(r.id, 512) || !id(r.itemId) || r.id !== `${r.itemId}:${r.index}` || !counter(r.index) || r.number !== number
    || !text(r.title) || typeof r.startSeconds !== 'number' || typeof r.endSeconds !== 'number' || r.endSeconds <= r.startSeconds) bad();
  return Object.freeze({id: r.id, itemId: r.itemId, index: r.index, number, title: r.title, startSeconds: r.startSeconds, endSeconds: r.endSeconds});
}

/** The whole book's chapters in book order, across its file parts (`GET /v1/books/{id}/chapters`,
 * every page). One named read, so the client-data cache can take it over unchanged. */
export async function readBookChapters(request: ListeningRequest, bookId: string, signal?: AbortSignal): Promise<BookChapters> {
  if (!id(bookId)) throw new Error('Invalid book.');
  const chapters: BookChapter[] = [], seen = new Set<string>();
  let cursor = '';
  do {
    const query = new URLSearchParams({limit: String(PAGE), ...(cursor ? {cursor} : {})});
    const raw = await request(`${encodedRoutePath('get_books_id_chapters', {id: encodeURIComponent(bookId)})}?${query}`, undefined, 'GET', signal);
    if (!obj(raw) || !Array.isArray(raw.chapters) || raw.chapters.length > PAGE || !text(raw.nextCursor, 4096)) bad();
    for (const row of raw.chapters) {
      const chapter = parseRow(row, chapters.length + 1);
      const previous = chapters.at(-1);
      // Within a part, chapters run forward; a new part starts its own timeline.
      if (seen.has(chapter.id) || previous?.itemId === chapter.itemId && (chapter.index <= previous.index || chapter.startSeconds < previous.startSeconds)) bad();
      seen.add(chapter.id);
      chapters.push(chapter);
    }
    if (raw.nextCursor && (raw.nextCursor === cursor || !raw.chapters.length)) bad();
    cursor = raw.nextCursor;
  } while (cursor && chapters.length < MAX_CHAPTERS);
  return Object.freeze({bookId, chapters: Object.freeze(chapters)});
}

/** The playing book chapter, when it is in the playing part. Before a part's first chapter the
 * chapter playing is the previous part's last, whose times don't apply to this part. */
export function playingBookChapter(book: BookChaptersView | null | undefined): BookChapter | undefined {
  const chapter = book?.phase === 'ready' ? book.chapters[book.current] : undefined;
  return chapter && chapter.itemId === book?.itemId ? chapter : undefined;
}

/** A chapter that has just started counts as current this long before its recorded start, so a
 * seek that lands a little early (rounding, a coarse seek) is not read as the chapter before. */
const START_TOLERANCE = 0.5;
/** Previous restarts the chapter after this long into it, else goes to the chapter before, as
 * Previous does for a track. */
const RESTART_AFTER = 3;

/** The place in `chapters` of the chapter playing at `seconds` in part `itemId`: -1 before the
 * book's first chapter, `undefined` when the list has no chapter in this part. */
export function bookChapterIndex(chapters: readonly BookChapter[], itemId: string, seconds: number): number | undefined {
  const first = chapters.findIndex(chapter => chapter.itemId === itemId);
  if (first < 0 || !Number.isFinite(seconds)) return undefined;
  let current = first - 1;
  for (let i = first; i < chapters.length && chapters[i]!.itemId === itemId; i++) if (chapters[i]!.startSeconds <= seconds + START_TOLERANCE) current = i;
  return current;
}

/** Where Previous or Next chapter goes from `seconds` in part `itemId`. `undefined`: the list
 * says nothing about this part, so the caller moves the queue instead. `null`: nowhere to go. */
export function bookChapterTarget(chapters: readonly BookChapter[], itemId: string, seconds: number, direction: 'previous' | 'next'): BookChapter | null | undefined {
  const current = bookChapterIndex(chapters, itemId, seconds);
  if (current === undefined) return undefined;
  if (direction === 'next') return chapters[current + 1] ?? null;
  const playing = chapters[current];
  if (playing?.itemId === itemId && seconds - playing.startSeconds > RESTART_AFTER) return playing;
  return chapters[current - 1] ?? null;
}
