/** A film's or episode's chapters as the player draws them: ticks on the seek line and the Chapters drawer (a title
 * with chapters but no episodes, INTEGRATION.md §7). They come with the title's playback options (`chapters`, start in
 * milliseconds and a title); each runs to the next one's start, the last to the end. */
export type VideoChapter = Readonly<{id: string; index: number; title: string; startSeconds: number; endSeconds: number; current: boolean}>;
/** INTEGRATION.md §7: every title with chapters draws them as ticks on the seek line; only a title without episodes
 * also gets the Chapters drawer (a show keeps its Episodes drawer). */
export function videoChapterOffer(chapters: readonly VideoChapter[], hasEpisodes: boolean, durationSeconds: number): Readonly<{ticks: readonly Readonly<{id: string; kind: 'chapter'; start: number}>[]; drawer: boolean}> {
  const ticks = durationSeconds > 0 ? chapters.filter(chapter => chapter.startSeconds > 0).map(chapter => Object.freeze({id: chapter.id, kind: 'chapter' as const, start: chapter.startSeconds / durationSeconds})) : [];
  return Object.freeze({ticks: Object.freeze(ticks), drawer: !hasEpisodes && chapters.length > 0});
}
export function videoChapters(chapters: readonly Readonly<{startMs: number; title: string}>[] | undefined, durationSeconds: number, positionSeconds: number,
  untitled: (number: number) => string): readonly VideoChapter[] {
  const starts = (chapters ?? []).filter(chapter => Number.isFinite(chapter.startMs) && chapter.startMs >= 0 && (!(durationSeconds > 0) || chapter.startMs / 1000 < durationSeconds))
    .slice().sort((a, b) => a.startMs - b.startMs);
  // One chapter is the whole title: nothing to draw or choose.
  if (starts.length < 2) return Object.freeze([]);
  return Object.freeze(starts.map((chapter, index) => {
    const startSeconds = chapter.startMs / 1000, endSeconds = index + 1 < starts.length ? starts[index + 1]!.startMs / 1000 : durationSeconds > 0 ? durationSeconds : startSeconds;
    const title = chapter.title.trim() || untitled(index + 1);
    return Object.freeze({id: `chapter-${index}`, index: index + 1, title, startSeconds, endSeconds, current: positionSeconds >= startSeconds && (positionSeconds < endSeconds || index === starts.length - 1)});
  }));
}
