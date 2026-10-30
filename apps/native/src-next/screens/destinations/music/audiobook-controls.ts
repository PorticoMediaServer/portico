import type {NativeListeningReadPort, NativeListeningReadSnapshot} from '../../../platform/playback/types';

type Facts = Extract<NativeListeningReadSnapshot, {phase: 'current'}>;
export type ListeningChapter = NonNullable<Facts['chapters']['data']>['chapters'][number];

/** Chapter positions are the canonical reader's source-local seconds. Never use
 * queue Previous/Next here: those commands change the audiobook, not its chapter. */
export function currentListeningChapter(snapshot: NativeListeningReadSnapshot, seconds: number): ListeningChapter | undefined {
  if (snapshot.phase !== 'current' || snapshot.chapters.phase !== 'ready' || snapshot.chapters.data?.status !== 'available' || !Number.isFinite(seconds)) return;
  const data = snapshot.chapters.data;
  return data.chapters.find(chapter => seconds >= chapter.startSeconds
    && (seconds < chapter.endSeconds || seconds === data.duration && chapter.index === data.totalCount));
}

/** Keep one canonical page, paging towards the current position as necessary.
 * The reader checks every response's source/revision and retires on a new output. */
export async function locateListeningChapter(reader: NativeListeningReadPort, seconds: number): Promise<ListeningChapter | undefined> {
  if (!Number.isFinite(seconds) || seconds < 0) return;
  const visited = new Set<string | null>();
  while (reader.current()) {
    const snapshot = reader.getSnapshot(), chapter = currentListeningChapter(snapshot, seconds);
    if (chapter) return chapter;
    if (snapshot.phase !== 'current' || snapshot.chapters.phase !== 'ready' || snapshot.chapters.data?.status !== 'available') return;
    const page = snapshot.chapters, data = page.data!, first = data.chapters[0], last = data.chapters.at(-1);
    if (!first || !last || visited.has(page.pagination.cursor)) return;
    visited.add(page.pagination.cursor);
    if (seconds < first.startSeconds && page.pagination.canPrevious) await reader.readPreviousChapterPage();
    else if (seconds >= last.endSeconds && data.nextCursor) await reader.readNextChapterPage();
    else return;
  }
}

/** A chapter move can cross a page boundary, but may never borrow a successor
 * reader or change the seek target after the original reader retires. */
export async function moveListeningChapter(reader: NativeListeningReadPort, seconds: number, direction: -1 | 1, seek: (seconds: number) => void): Promise<void> {
  const chapter = await locateListeningChapter(reader, seconds);
  if (!chapter || !reader.current()) return;
  const original = reader.getSnapshot();
  if (original.phase !== 'current' || original.chapters.phase !== 'ready' || !original.chapters.data) return;
  const index = chapter.index + direction, data = original.chapters.data;
  if (index < 1 || index > data.totalCount) return;
  if (!data.chapters.some(value => value.index === index)) {
    if (direction < 0 && original.chapters.pagination.canPrevious) await reader.readPreviousChapterPage();
    else if (direction > 0 && data.nextCursor) await reader.readNextChapterPage();
    else return;
  }
  if (!reader.current()) return;
  const next = reader.getSnapshot();
  if (next.phase !== 'current' || next.chapters.phase !== 'ready' || !next.chapters.data
    || next.chapters.data.revision !== data.revision || next.chapters.data.scope.sourceId !== data.scope.sourceId
    || next.chapters.data.scope.sessionId !== data.scope.sessionId || next.chapters.data.scope.generation !== data.scope.generation) return;
  const target = next.chapters.data.chapters.find(value => value.index === index);
  if (target) seek(target.startSeconds);
}

/** A press belongs to the exact page it drew; progress publishes may replace
 * the wrapper, but the source, revision and chapter tuple must still match. */
export function listeningChapterSeek(reader: NativeListeningReadPort, drawn: NativeListeningReadSnapshot, id: string): number | undefined {
  if (!reader.current() || drawn.phase !== 'current' || drawn.chapters.phase !== 'ready' || !drawn.chapters.data) return;
  const live = reader.getSnapshot();
  if (live.phase !== 'current' || live.chapters.phase !== 'ready' || !live.chapters.data) return;
  const before = drawn.chapters.data, after = live.chapters.data;
  if (before.revision !== after.revision || before.scope.sourceId !== after.scope.sourceId
    || before.scope.sessionId !== after.scope.sessionId || before.scope.generation !== after.scope.generation) return;
  const target = before.chapters.find(chapter => chapter.id === id);
  return target && after.chapters.some(chapter => chapter.id === id && chapter.startSeconds === target.startSeconds && chapter.endSeconds === target.endSeconds)
    ? target.startSeconds : undefined;
}
