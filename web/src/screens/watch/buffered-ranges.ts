export type BufferedRange = Readonly<{start: number; end: number}>;

/** Paint only observed media, clipped to the same title-time bounds as the seek input. */
export function bufferedSegments(ranges: readonly BufferedRange[], bounds: BufferedRange | null): readonly Readonly<{left: number; width: number}>[] {
  if (!bounds || !Number.isFinite(bounds.start) || !Number.isFinite(bounds.end) || bounds.end <= bounds.start) return [];
  const duration = bounds.end - bounds.start;
  return ranges.flatMap(range => {
    if (!Number.isFinite(range.start) || !Number.isFinite(range.end)) return [];
    const start = Math.max(bounds.start, range.start), end = Math.min(bounds.end, range.end);
    return end > start ? [{left: (start - bounds.start) / duration * 100, width: (end - start) / duration * 100}] : [];
  });
}
