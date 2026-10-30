/** Display geometry only; network retries and clocks belong to the caller. */
export function ringProgress(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, Math.min(1, value)) : null;
}

export function retryProgress(remainingSeconds: number, intervalSeconds: number | null | undefined): number | null {
  if (!Number.isFinite(remainingSeconds) || typeof intervalSeconds !== 'number' || !Number.isFinite(intervalSeconds) || intervalSeconds <= 0) return null;
  return ringProgress(remainingSeconds / intervalSeconds);
}

export const skeletonTiming = {delayMs: 300, slowMs: 8000, pictureFadeMs: 240} as const;
export function showSkeleton(elapsedMs: number): boolean {
  return Number.isFinite(elapsedMs) && elapsedMs >= skeletonTiming.delayMs;
}
export function isSlowLoading(elapsedMs: number): boolean {
  return Number.isFinite(elapsedMs) && elapsedMs >= skeletonTiming.slowMs;
}

export type GridWindowOptions = Readonly<{
  columns: number;
  rowHeight: number;
  viewportHeight: number;
  scrollOffset: number;
  totalCount?: number;
  overscanRows?: number;
}>;

/** Allocate work proportional to the visible window, including for a 50,000-title grid. */
export function gridWindow({columns, rowHeight, viewportHeight, scrollOffset, totalCount, overscanRows = 1}: GridWindowOptions) {
  if (!Number.isInteger(columns) || columns < 1 || !Number.isFinite(rowHeight) || rowHeight <= 0 || !Number.isFinite(viewportHeight) || viewportHeight <= 0) return {first: 0, count: 0, top: 0, height: 0};
  const offset = Number.isFinite(scrollOffset) ? Math.max(0, scrollOffset) : 0;
  const overscan = Number.isFinite(overscanRows) ? Math.min(2, Math.max(0, Math.floor(overscanRows))) : 0;
  const known = typeof totalCount === 'number' && Number.isFinite(totalCount);
  const total = known ? Math.max(0, Math.floor(totalCount)) : undefined;
  const rows = total === undefined ? undefined : Math.ceil(total / columns);
  const startRow = Math.max(0, Math.floor(offset / rowHeight) - overscan);
  const endRow = Math.min(rows ?? Infinity, Math.ceil((offset + viewportHeight) / rowHeight) + overscan);
  const first = Math.min(total ?? Infinity, startRow * columns);
  const end = Math.min(total ?? Infinity, Math.max(startRow, endRow) * columns);
  return {first, count: Math.max(0, end - first), top: startRow * rowHeight, height: (rows ?? Math.max(startRow, endRow)) * rowHeight};
}

export type DetailsAudience<T> = Readonly<{audience: 'viewer'; owner?: never}> | Readonly<{audience: 'owner'; owner?: T}>;
/** Gate before owner content is constructed, including for untyped JavaScript callers. */
export function ownerDetails<T>(input: {audience: 'viewer' | 'owner'; owner?: T}): T | undefined {
  return input.audience === 'owner' ? input.owner : undefined;
}
