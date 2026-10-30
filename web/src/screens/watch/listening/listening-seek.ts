export type ListeningSeekChapter = Readonly<{id: string; startSeconds: number; endSeconds: number}>;
export type ListeningSeekBounds = Readonly<{start: number; end: number}>;

/** Paint is derived only from the admitted seek window and real chapter boundaries. */
export function listeningSeekModel(position: number, duration: number, bounds?: ListeningSeekBounds | null, chapters: readonly ListeningSeekChapter[] = []) {
  const valid = !!bounds && Number.isFinite(bounds.start) && Number.isFinite(bounds.end) && bounds.start >= 0 && bounds.end > bounds.start;
  const start = valid ? bounds!.start : 0, end = valid ? bounds!.end : Math.max(Number.isFinite(duration) ? duration : 0, 1);
  const at = Number.isFinite(position) ? Math.max(start, Math.min(end, position)) : start;
  const fraction = (value: number) => Math.max(0, Math.min(1, (value - start) / (end - start)));
  const realChapters = valid ? chapters.filter(chapter => Number.isFinite(chapter.startSeconds) && Number.isFinite(chapter.endSeconds)
    && chapter.startSeconds >= 0 && chapter.endSeconds > chapter.startSeconds) : [];
  const ticks = [...new Set(realChapters.map(chapter => chapter.startSeconds).filter(value => value > start && value < end))].sort((a, b) => a - b).map(fraction);
  const active = realChapters.find(chapter => at >= chapter.startSeconds && at < chapter.endSeconds);
  return {valid, start, end, at, progress: fraction(at), ticks,
    remaining: Number.isFinite(duration) && duration > 0 ? Math.max(0, duration - at) : undefined,
    chapter: active ? {start: fraction(active.startSeconds), end: fraction(active.endSeconds)} : undefined};
}
