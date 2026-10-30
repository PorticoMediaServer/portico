/** Display-only progress. Unknown work never acquires an invented arc. */
export function knownProgress(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, Math.min(1, value)) : null;
}

export function loadingPhase(elapsedMs: number): 'waiting' | 'shapes' | 'slow' {
  const elapsed = Number.isFinite(elapsedMs) ? Math.max(0, elapsedMs) : 0;
  return elapsed < 300 ? 'waiting' : elapsed < 8000 ? 'shapes' : 'slow';
}

export type SkeletonWindow = Readonly<{
  width: number;
  height: number;
  scrollTop: number;
}>;
export type SkeletonCell = Readonly<{index: number; x: number; y: number; width: number; height: number}>;
export type SkeletonGridGeometry = Readonly<{
  columns: number;
  cellWidth: number;
  rowHeight: number;
  totalHeight: number;
  cells: readonly SkeletonCell[];
}>;
export type SkeletonCaptionLines = 0 | 1 | 2;

/** Only intersecting rows mount. Library size changes the spacer, never the render work. */
export function skeletonGridWindow(viewport: SkeletonWindow, itemCount: number, options: Readonly<{
  columns?: number; gutter?: number; columnGap?: number; rowGap?: number; ratio?: number; captions?: boolean;
  /** Known leading row extents; never a catalogue-sized list. Unspecified rows use captions. */
  leadingCaptionLines?: readonly SkeletonCaptionLines[];
}> = {}): SkeletonGridGeometry {
  const width = Number.isFinite(viewport.width) ? Math.max(0, viewport.width) : 0;
  const height = Number.isFinite(viewport.height) ? Math.max(0, viewport.height) : 0;
  const phone = width < 1024;
  const option = (value: number | undefined, fallback: number) => value !== undefined && Number.isFinite(value) ? Math.max(0,value) : fallback;
  const gutter = option(options.gutter, phone ? 16 : width < 1440 ? 32 + (width - 1024) / 13 : 64);
  const columnGap = option(options.columnGap, phone ? 10 : 20);
  const columns = Math.max(1, Math.floor(option(options.columns, phone ? Math.max(3, Math.floor((width - gutter * 2 + columnGap) / 120)) : Math.max(1, Math.floor((width - gutter * 2 + columnGap) / 190)))));
  const cellWidth = Math.max(0, (width - gutter * 2 - columnGap * (columns - 1)) / columns);
  const ratio = options.ratio && Number.isFinite(options.ratio) && options.ratio > 0 ? options.ratio : 2 / 3;
  const cellHeight = cellWidth / ratio;
  const rowGap = option(options.rowGap, phone ? 16 : 30);
  // Reserve the real card's two text lines, including their margins, as it arrives.
  const captionHeight = options.captions === false ? 0 : phone ? 41 : 50;
  const rowHeight = cellHeight + captionHeight + rowGap;
  const count = Number.isFinite(itemCount) ? Math.min(Number.MAX_SAFE_INTEGER, Math.max(0, Math.floor(itemCount))) : 0;
  const rows = Math.ceil(count / columns);
  const deltas = [0];
  for (const lines of (options.leadingCaptionLines ?? []).slice(0, rows)) {
    const extent = lines === 0 ? 0 : lines === 1 ? phone ? 20 : 26 : phone ? 41 : 50;
    deltas.push(deltas[deltas.length - 1]! + extent - captionHeight);
  }
  const rowTop = (row: number) => row * rowHeight + deltas[Math.min(row, deltas.length - 1)]!;
  const totalHeight = rows === 0 ? 0 : rowTop(rows) - rowGap;
  const scrollTop = Number.isFinite(viewport.scrollTop) ? Math.max(0, viewport.scrollTop) : 0;
  const lowerBound = (position: number) => {
    let low = 0, high = rows;
    while (low < high) { const middle = low + Math.floor((high - low) / 2); if (rowTop(middle) < position) low = middle + 1; else high = middle; }
    return low;
  };
  const firstRow = scrollTop >= totalHeight ? rows : Math.max(0, lowerBound(scrollTop + Number.EPSILON * Math.max(1, scrollTop)) - 1);
  const lastRow = lowerBound(scrollTop + height);
  const cells: SkeletonCell[] = [];
  if (width > 0 && height > 0) for (let row = firstRow; row < lastRow; row++) {
    for (let column = 0; column < columns; column++) {
      const index = row * columns + column;
      if (index >= count) break;
      cells.push({index, x: gutter + column * (cellWidth + columnGap), y: rowTop(row), width: cellWidth, height: cellHeight});
    }
  }
  return {columns, cellWidth, rowHeight, totalHeight, cells};
}
