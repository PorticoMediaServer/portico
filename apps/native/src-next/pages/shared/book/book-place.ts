import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';

/** Where a profile is in a book's rows (its chapters, else its parts): the row it is in, how far into that row, and the time
 * left in it. Shared by every book page (touch, tablet, TV).
 *
 * The row is the last row of the resumed file that starts at or before the resumed place: each chapter's or part's row plays
 * its file from its own start (`playback.itemId`, `playback.startSeconds`). Rows are matched by what they play, not by their
 * id, whose form is the reader's (`book:chapter:N`, `book:part:N`). Its end is the resumed chapter's own end when that is
 * this row, else the next row's start in the same file, else the row's own length. */
export type BookPlace = Readonly<{index: number; id: string; fraction?: number; secondsLeft?: number}>;

export function bookPlace(rows: readonly ContentEntry[], progress: ContentEntry['bookProgress']): BookPlace | undefined {
  if (!progress || !Number.isFinite(progress.partPositionMs)) return undefined;
  const startMs = (row: ContentEntry | undefined) => row && row.playback?.itemId === progress.itemId && Number.isFinite(row.playback.startSeconds)
    ? (row.playback.startSeconds ?? 0) * 1000 : undefined;
  // Half a second of slack: a resume saved at a chapter's first frame still lands in that chapter.
  const index = rows.reduce((found, row, at) => {const start = startMs(row); return start !== undefined && start <= progress.partPositionMs + 500 ? at : found;}, -1);
  if (index < 0) return undefined;
  const row = rows[index]!, start = startMs(row)!, chapter = progress.chapter;
  const end = chapter && Math.abs(chapter.startMs - start) < 1000 && chapter.endMs > chapter.startMs ? chapter.endMs
    : startMs(rows[index + 1]) ?? (row.duration ? start + row.duration * 1000 : undefined);
  if (end === undefined || end <= start) return {index, id: row.id};
  return {index, id: row.id, fraction: Math.max(0, Math.min(1, (progress.partPositionMs - start) / (end - start))), secondsLeft: Math.max(0, (end - progress.partPositionMs) / 1000)};
}
