import type {ListeningRequest} from './listening.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {serverPath} from './query/server-api.ts';

/** One chapter of a whole book: the file part it is in (`itemId`), its place in that part
 * (`index`), its 1-based place in the book (`number`) and its span in the part's own seconds. */
export type BookChapter = Readonly<{id: string; itemId: string; index: number; number: number; title: string; startSeconds: number; endSeconds: number}>;
export type BookChapters = Readonly<{bookId: string; chapters: readonly BookChapter[]}>;

/** The playing book's chapters for a player's list: every part's chapters in book order, and
 * where the playing position is (`current`: an index into `chapters`, -1 before the first). */
export type BookChaptersView = Readonly<{bookId: string; itemId: string; phase: 'loading' | 'ready' | 'error'; chapters: readonly BookChapter[]; current: number}>;

/** No real book comes near this; a longer list stops here rather than reading on. */
const MAX_CHAPTERS = 4096;
const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 2048): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const id = (v: unknown): v is string => text(v, 256) && /^[A-Za-z0-9_-]+$/.test(v);
function bad(): never {throw new Error(unreadableServerResponse);}

/** One chapter's span on the clock of the version playing. */
export type ItemChapterSpan = Readonly<{title: string; startMs: number; endMs: number}>;
export type ItemChapters = Readonly<{versionId: string; durationMs: number; spans: readonly ItemChapterSpan[]}>;
const ms = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0;

/** An audiobook's chapters from its item read (`GET /v1/items/{id}`), for the version `versionId`
 * (none when the item has no such version; the first when none is named): its chapter marks, or, for a book in several files without marks, its parts in
 * order, since such a book plays as one book on one clock (definition 07, rule 60). */
export function itemChapters(item: unknown, versionId?: string): ItemChapters | undefined {
  if (!obj(item) || !Array.isArray(item.versions)) return undefined;
  const versions = item.versions.filter(obj);
  const version = versionId === undefined ? versions[0] : versions.find(v => v.id === versionId);
  if (!version || !id(version.id)) return undefined;
  const durationMs = ms(version.durationMs) && version.durationMs > 0 ? version.durationMs : ms(item.durationMs) ? item.durationMs : 0;
  const marks = (Array.isArray(version.markers) ? version.markers : []).filter(obj)
    .filter(m => m.kind === 'chapter' && ms(m.startMs) && ms(m.endMs) && m.endMs > m.startMs).sort((a, b) => (a.startMs as number) - (b.startMs as number));
  const parts = (Array.isArray(version.parts) ? version.parts : []).filter(obj);
  const raw: {title: string; startMs: number; endMs: number}[] = marks.length
    ? marks.map((m, n) => ({title: text(m.title) && m.title.trim() ? m.title : `Chapter ${n + 1}`, startMs: m.startMs as number, endMs: m.endMs as number}))
    : parts.length > 1 && parts.every(p => ms(p.offsetMs) && ms(p.durationMs) && p.durationMs > 0)
      ? parts.map((p, n) => ({title: `Part ${n + 1}`, startMs: p.offsetMs as number, endMs: (p.offsetMs as number) + (p.durationMs as number)}))
      : [];
  // A chapter ends where the next begins and never past the end of the book.
  const spans = raw.map((span, n) => {
    const next = raw[n + 1]?.startMs, end = Math.min(span.endMs, next ?? span.endMs, durationMs || span.endMs);
    return Object.freeze({title: span.title, startMs: span.startMs, endMs: end});
  }).filter(span => span.endMs > span.startMs);
  return Object.freeze({versionId: version.id, durationMs, spans: Object.freeze(spans)});
}

/** The whole book's chapters in book order, from its item read. A book is one item on the rebuilt
 * server, so every chapter is in the book itself (`itemId` is the book). */
export async function readBookChapters(request: ListeningRequest, bookId: string, signal?: AbortSignal): Promise<BookChapters> {
  if (!id(bookId)) throw new Error('Invalid book.');
  const read = itemChapters(await request(serverPath('GET /v1/items/{id}', {id: bookId}), undefined, 'GET', signal));
  if (!read) bad();
  const chapters = read.spans.slice(0, MAX_CHAPTERS).map((span, index) => Object.freeze({id: `${bookId}:${index}`, itemId: bookId, index, number: index + 1,
    title: span.title, startSeconds: span.startMs / 1000, endSeconds: span.endMs / 1000}));
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

/** One place Previous or Next chapter can go, for a device's own player (the lock screen, a
 * headset, a car): a chapter start in the playing part (`entryId: null`), or in another part,
 * which plays that queue entry from there. */
export type BookChapterStep = Readonly<{startSeconds: number; entryId: string | null}>;
/** The playing part's chapters with the book chapters on either side of it, as the native owner
 * holds them for its remote controls (MU-7). `before`/`after` say what lies past the listed
 * steps: `end` (the book's start or end: nothing) or `queue` (chapters whose queue entry this
 * player hasn't seen: the queue moves instead). */
export type BookChapterSteps = Readonly<{sessionId: string; itemId: string; steps: readonly BookChapterStep[]; before: 'end' | 'queue'; after: 'end' | 'queue'}>;
export type BookChapterMove = Readonly<{kind: 'seek'; seconds: number}> | Readonly<{kind: 'entry'; entryId: string; startSeconds: number}> | Readonly<{kind: 'queue'}> | null;

/** The steps a native player needs for part `itemId`: two book chapters before the part (Previous
 * from before its first chapter goes to the one before the chapter playing), its own, and the one
 * after. `entryFor` finds another part's queue entry; one it can't find ends the list there.
 * `undefined` when the list has no chapter in this part. */
export function bookChapterSteps(chapters: readonly BookChapter[], sessionId: string, itemId: string, entryFor: (itemId: string) => string | undefined): BookChapterSteps | undefined {
  const first = chapters.findIndex(chapter => chapter.itemId === itemId);
  if (first < 0) return undefined;
  let last = first;
  while (chapters[last + 1]?.itemId === itemId) last++;
  const steps: BookChapterStep[] = [];
  let before: 'end' | 'queue' = 'end', after: 'end' | 'queue' = 'end';
  for (let i = first - 1; i >= Math.max(0, first - 2); i--) {
    const entryId = entryFor(chapters[i]!.itemId);
    if (!entryId) { before = 'queue'; break; }
    steps.unshift(Object.freeze({startSeconds: chapters[i]!.startSeconds, entryId}));
  }
  for (let i = first; i <= last; i++) steps.push(Object.freeze({startSeconds: chapters[i]!.startSeconds, entryId: null}));
  const following = chapters[last + 1];
  if (following) {
    const entryId = entryFor(following.itemId);
    if (entryId) steps.push(Object.freeze({startSeconds: following.startSeconds, entryId})); else after = 'queue';
  }
  return Object.freeze({sessionId, itemId, steps: Object.freeze(steps), before, after});
}

/** Where Previous or Next chapter goes from `seconds` in the steps' part, as `bookChapterTarget`
 * decides it. The Apple and Android owners carry the same rules (PorticoListeningPolicy). */
export function bookChapterMove(plan: Pick<BookChapterSteps, 'steps' | 'before' | 'after'>, seconds: number, direction: 'previous' | 'next'): BookChapterMove {
  const {steps} = plan;
  const first = steps.findIndex(step => step.entryId === null);
  if (first < 0 || !Number.isFinite(seconds)) return {kind: 'queue'};
  let current = first - 1;
  for (let i = first; i < steps.length && steps[i]!.entryId === null; i++) if (steps[i]!.startSeconds <= seconds + START_TOLERANCE) current = i;
  const go = (step: BookChapterStep | undefined, past: 'end' | 'queue'): BookChapterMove =>
    !step ? past === 'queue' ? {kind: 'queue'} : null
      : step.entryId === null ? {kind: 'seek', seconds: step.startSeconds} : {kind: 'entry', entryId: step.entryId, startSeconds: step.startSeconds};
  if (direction === 'next') return go(steps[current + 1], plan.after);
  const playing = steps[current];
  if (playing?.entryId === null && seconds - playing.startSeconds > RESTART_AFTER) return go(playing, 'end');
  return go(current - 1 >= 0 ? steps[current - 1] : undefined, plan.before);
}
