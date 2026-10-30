export const COMPACT_SESSION_HEIGHT = 66;
export const SESSION_PREVIEW_COUNT = 10;
export const DASHBOARD_SESSION_PAGE_SIZE = 100;

/** Mount only the viewport plus a small overscan and the row owning focus/menu. */
export function sessionWindow(total: number, top: number, height: number, pinned: readonly number[] = []): readonly number[] {
  if (!Number.isSafeInteger(total) || total < 0) throw new Error('Invalid session count');
  if (!total) return [];
  const viewport = Math.max(COMPACT_SESSION_HEIGHT, Number.isFinite(height) ? height : COMPACT_SESSION_HEIGHT);
  const offset = Math.max(0, Math.min(Number.isFinite(top) ? top : 0, Math.max(0, total * COMPACT_SESSION_HEIGHT - viewport)));
  const first = Math.max(0, Math.floor(offset / COMPACT_SESSION_HEIGHT) - 2);
  const end = Math.min(total, Math.ceil((offset + viewport) / COMPACT_SESSION_HEIGHT) + 2);
  const indices = new Set(Array.from({length: end - first}, (_, index) => first + index));
  for (const index of pinned) if (Number.isSafeInteger(index) && index >= 0 && index < total) indices.add(index);
  return [...indices].sort((a, b) => a - b);
}
