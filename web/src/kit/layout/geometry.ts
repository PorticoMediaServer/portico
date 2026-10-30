export type VisibleRange = Readonly<{first: number; last: number}>;
export type PositionAnchor = Readonly<{key: string; index: number}>;

/** These bounds describe the viewport, independently of how many pages are resident. */
export function gridWindow(total: number, columns: number, rowPitch: number, top: number, height: number, overscan = 1) {
  const count = Math.max(0, Math.floor(total)), cols = Math.max(1, Math.floor(columns)), pitch = Math.max(1, rowPitch);
  const rows = Math.ceil(count / cols);
  const firstRow = Math.min(Math.max(0, rows - 1), Math.max(0, Math.floor(top / pitch)));
  const lastRow = Math.min(rows - 1, Math.max(firstRow, Math.ceil((top + height) / pitch) - 1));
  return {first: count ? firstRow * cols : -1, last: count ? Math.min(count - 1, (lastRow + 1) * cols - 1) : -1,
    renderFirst: count ? Math.max(0, firstRow - overscan) * cols : 0,
    renderLast: count ? Math.min(count - 1, (lastRow + overscan + 1) * cols - 1) : -1, rows};
}

export function gridColumns(width: number, cardWidth: number, gap: number): number {
  return Math.max(1, Math.floor((width + gap) / (Math.max(1, cardWidth) + gap)));
}

/** Keep the complete Latin rail, then append other actual server buckets in server order. */
export function alphabetKeys(anchors: readonly PositionAnchor[]): readonly string[] {
  return [...new Set(['#', ...'ABCDEFGHIJKLMNOPQRSTUVWXYZ', ...anchors.map(anchor => anchor.key)])];
}

/** Pointer coordinates and rendered cell bounds share viewport space, even when
 * the rail is scrolled or the browser uses a fractional/text-scaled cell pitch. */
export function railIndexAt(y: number, cells: readonly (Readonly<{top: number; bottom: number}> | undefined)[]): number {
  let last = -1;
  for (let index = 0; index < cells.length; index++) {
    const cell = cells[index];
    if (!cell || cell.bottom <= cell.top) continue;
    last = index;
    if (y < cell.bottom) return index;
  }
  return last;
}

export function railBand(keys: readonly string[], anchors: readonly PositionAnchor[], visible: VisibleRange): VisibleRange | undefined {
  if (visible.first < 0 || visible.last < visible.first || !anchors.length) return undefined;
  let first = anchors[0], last = anchors[0];
  for (const anchor of anchors) {
    if (anchor.index <= visible.first && anchor.index >= first.index) first = anchor;
    if (anchor.index <= visible.last && anchor.index >= last.index) last = anchor;
  }
  const a = keys.indexOf(first.key), b = keys.indexOf(last.key);
  return a < 0 || b < 0 ? undefined : {first: Math.min(a, b), last: Math.max(a, b)};
}

export function boundedIndex(index: number, count: number): number {
  return count ? Math.max(0, Math.min(count - 1, Number.isFinite(index) ? Math.floor(index) : 0)) : -1;
}

export function anchoredOffset(start: number, end: number, viewport: number, current: number, inset: number): number {
  if (start < current + inset) return Math.max(0, start - inset);
  if (end > current + viewport - inset) return Math.max(0, end - viewport + inset);
  return current;
}

/** Keep at most one active interaction mounted while the visible window moves past it. */
export function gridRenderIndices(first: number, last: number, total: number, pinned?: number): readonly number[] {
  const indices = Array.from({length: Math.max(0, last - first + 1)}, (_, offset) => first + offset);
  if (pinned !== undefined && Number.isInteger(pinned) && pinned >= 0 && pinned < total && (pinned < first || pinned > last)) indices.push(pinned);
  return indices.sort((a, b) => a - b);
}
