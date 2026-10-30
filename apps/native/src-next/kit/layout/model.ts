export type VisibleRange = Readonly<{first: number; last: number}>;

/** Grow only the authored caption allowance; artwork keeps its existing aspect and size. */
export function captionItemHeight(itemHeight: number, captionHeight: number, fontScale = 1): number {
  const scale = Number.isFinite(fontScale) ? Math.max(1, fontScale) : 1;
  return itemHeight + Math.max(0, captionHeight) * (scale - 1);
}

/** All calculations are O(visible), including a collection with an unknown tail. */
export function gridWindow(total: number, columns: number, rowHeight: number, offset: number, viewport: number, overscan = 2): VisibleRange & {startRow: number; endRow: number; height: number} {
  const count = Math.max(0, Math.floor(total)), cols = Math.max(1, Math.floor(columns));
  const stride = Math.max(1, rowHeight), rows = Math.ceil(count / cols);
  const margin = Number.isFinite(overscan) ? Math.max(0, Math.min(8, Math.floor(overscan))) : 2;
  const firstRow = Math.max(0, Math.min(rows - 1, Math.floor(Math.max(0, offset) / stride)));
  const lastRow = Math.min(rows - 1, Math.max(firstRow, Math.ceil((Math.max(0, offset) + Math.max(0, viewport)) / stride) - 1));
  return {first: count ? firstRow * cols : 0, last: count ? Math.min(count - 1, (lastRow + 1) * cols - 1) : -1,
    startRow: Math.max(0, firstRow - margin), endRow: Math.min(rows - 1, lastRow + margin), height: rows * stride};
}


export type GridAnchor = Readonly<{index: number; offset: number}>;

/** The first visible slot and its position below the sticky edge, O(1). */
export function captureGridAnchor(scroll: number, columns: number, stride: number, paddingTop: number, stickyOffset: number, total: number): GridAnchor | undefined {
  const local = scroll + stickyOffset - paddingTop;
  if (local < 0 || total <= 0) return undefined;
  const row = Math.floor(local / stride), index = Math.min(total - 1, row * columns);
  return {index, offset: row * stride - local};
}

/** Restore the same slot's top relative to that edge after columns/size change. */
export function gridAnchorOffset(anchor: GridAnchor, columns: number, stride: number, paddingTop: number, stickyOffset: number, extent: number, viewport: number): number {
  return Math.max(0, Math.min(Math.max(0, extent - viewport), paddingTop + Math.floor(anchor.index / columns) * stride - stickyOffset - anchor.offset));
}

/** Keep the focused item inside the drawn safe edges, without moving focus itself. */
export function anchoredOffset(itemStart: number, itemSize: number, viewport: number, offset: number, extent: number, leading = 0, trailing = 0): number {
  const low = itemStart - leading, high = itemStart + itemSize - viewport + trailing;
  const next = itemStart < offset + leading ? low : itemStart + itemSize > offset + viewport - trailing ? high : offset;
  return Math.max(0, Math.min(Math.max(0, extent - viewport), next));
}

export type AlphabetAnchor = Readonly<{key: string; index?: number; end?: number}>;
const latinKeys = ['#', ...'ABCDEFGHIJKLMNOPQRSTUVWXYZ'];

/** The server's order of non-Latin keys is retained; absent Latin letters stay visible. */
export function alphabetAnchors(positionIndex: Readonly<Record<string, number>>, total: number): readonly AlphabetAnchor[] {
  const supplied = Object.entries(positionIndex).filter(([, index]) => Number.isInteger(index) && index >= 0 && index < total);
  const keys = [...latinKeys, ...supplied.map(([key]) => key).filter(key => !latinKeys.includes(key))];
  const boundaries = [...new Set(supplied.map(([, index]) => index))].sort((a, b) => a - b);
  return keys.map(key => {
    const index = supplied.find(([label]) => label === key)?.[1];
    if (index === undefined) return {key};
    return {key, index, end: (boundaries.find(value => value > index) ?? total) - 1};
  });
}

export function alphabetBand(anchors: readonly AlphabetAnchor[], first: number, last: number): {first: number; last: number} | undefined {
  const visible = anchors.flatMap((anchor, i) => anchor.index !== undefined && anchor.index <= last && (anchor.end ?? anchor.index) >= first ? [i] : []);
  return visible.length ? {first: Math.min(...visible), last: Math.max(...visible)} : undefined;
}

export function flowSectionWidth(count: number, cap: number, itemWidth: number, gap: number): number {
  const shown = Math.max(0, Math.min(Math.floor(count), Math.floor(cap)));
  return shown ? shown * itemWidth + (shown - 1) * gap : 0;
}

/** Pausing preserves the current candidate's remaining dwell time. */
export function createRotationClock(intervalMs: number) {
  let remaining = intervalMs, started: number | undefined;
  return {
    resume(now: number) {if (started === undefined) started = now; return remaining;},
    pause(now: number) {if (started !== undefined) remaining = Math.max(0, remaining - (now - started)); started = undefined;},
    reset() {remaining = intervalMs; started = undefined;},
    remaining(now: number) {return Math.max(0, remaining - (started === undefined ? 0 : now - started));},
  };
}

export type PreviewScheduler = Readonly<{schedule: (run: () => void, ms: number) => unknown; cancel: (token: unknown) => void}>;
const scheduler: PreviewScheduler = {schedule: (run, ms) => setTimeout(run, ms), cancel: token => clearTimeout(token as ReturnType<typeof setTimeout>)};

/** Only the most recent bounded focus request may publish; repeated keys hold the picture. */
export function createPreviewDebounce<T>(publish: (entry: T) => void, delay = 250, timer: PreviewScheduler = scheduler) {
  let pending: T | undefined, token: unknown, disposed = false;
  const cancel = () => {if (token !== undefined) timer.cancel(token); token = undefined;};
  return {
    request(entry: T, repeating = false) {
      cancel(); pending = entry;
      if (!repeating && !disposed) token = timer.schedule(() => {token = undefined; if (!disposed && pending === entry) publish(entry);}, delay);
    },
    clear() {cancel(); pending = undefined;},
    dispose() {disposed = true; cancel(); pending = undefined;},
  };
}

/** Readiness belongs to the current mounted foreground, never a cache or a preparation promise. */
export function createArtworkReadiness<T>(publish: (entry: T) => void, failed: (entry: T, error: unknown) => void) {
  let target: T | undefined;
  return {
    stage(entry: T) {target = entry;},
    ready(entry: T) {if (target !== entry) return false; publish(entry); return true;},
    error(entry: T, error: unknown) {if (target !== entry) return false; target = undefined; failed(entry, error); return true;},
    clear() {target = undefined;},
  };
}
