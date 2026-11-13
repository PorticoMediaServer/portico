/** A film's or episode's chapters as the player draws them: ticks on the seek line and the Chapters drawer (a title
 * with chapters but no episodes, INTEGRATION.md §7). They come with the title's playback options (`chapters`, start in
 * milliseconds and a title); each runs to the next one's start, the last to the end. */
export type VideoChapter = Readonly<{id: string; index: number; title: string; startSeconds: number; endSeconds: number; current: boolean}>;
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
