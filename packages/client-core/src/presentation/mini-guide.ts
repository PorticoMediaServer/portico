/**
 * MU4 FEAT-07: mini guide data. Pure and platform-free.
 *
 * The overlay shows three rows — previous, current, next channel — each with
 * its now/next programme, and tunes on select. It works only on the loaded
 * page the caller passes (O(visible)): it never loads a lineup itself, and it
 * wraps at the ends of that page.
 */
export type MiniGuideChannel = Readonly<{id: string; number?: unknown; name?: unknown}>;
export type MiniGuideProgramme = Readonly<{id: string; title: string; startMs: number; endMs: number}>;
export type MiniGuideRow<TChannel extends MiniGuideChannel, TProgramme extends MiniGuideProgramme> = Readonly<{
  channel: TChannel;
  now: TProgramme | null;
  next: TProgramme | null;
}>;

/** The programme on at `nowMs` and the one after it (programmes need only be time-ordered). */
export function nowNextProgrammes<TProgramme extends MiniGuideProgramme>(programmes: readonly TProgramme[] | null | undefined, nowMs: number): {now: TProgramme | null; next: TProgramme | null} {
  if (!Array.isArray(programmes)) return {now: null, next: null};
  let now: TProgramme | null = null, next: TProgramme | null = null;
  for (const p of programmes) {
    if (!p || !Number.isFinite(p.startMs) || !Number.isFinite(p.endMs)) continue;
    if (p.startMs <= nowMs && nowMs < p.endMs) now = p;
    else if (p.startMs >= nowMs && !next) next = p;
  }
  return {now, next};
}

/**
 * Previous/current/next rows for `currentId` within the loaded, ordered page.
 * Wraps at the ends. Unknown id reads as the head of the page. A page of one
 * yields that row three times over (prev/current/next are the same channel),
 * so the overlay always has its shape; callers with fewer than three distinct
 * rows may collapse them.
 */
export function miniGuideNeighbors<TChannel extends MiniGuideChannel>(
  page: readonly TChannel[] | null | undefined,
  currentId: string,
): {previous: TChannel | null; current: TChannel | null; next: TChannel | null} {
  if (!Array.isArray(page) || page.length === 0) return {previous: null, current: null, next: null};
  const at = page.findIndex(c => c && c.id === currentId);
  const i = at >= 0 ? at : 0;
  const n = page.length;
  return {
    previous: page[((i - 1) % n + n) % n] ?? null,
    current: page[i] ?? null,
    next: page[((i + 1) % n)] ?? null,
  };
}

/** Channel-number ordering for a tune history (numeric-aware, stable on ties). */
export function sortChannelsByNumber<TChannel extends MiniGuideChannel>(channels: readonly TChannel[]): TChannel[] {
  const key = (c: TChannel): string => (typeof c.number === 'string' && c.number ? c.number : typeof c.name === 'string' ? c.name : c.id);
  return [...channels].sort((a, b) => key(a).localeCompare(key(b), undefined, {numeric: true}));
}
