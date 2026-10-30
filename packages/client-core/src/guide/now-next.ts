/**
 * Now and next (Spec — Channels and Guide §5, §6, §9.3): what a channel is showing, what comes
 * after it, and when that changes, so lists re-render at program boundaries rather than on a timer.
 * `programs` are normalized (layout.ts `normalizePrograms`).
 */
import type {GuideProgram} from './types.ts';

export type NowNext = Readonly<{
  now?: GuideProgram;
  next?: GuideProgram;
  /** 0..1 through `now` (0 in a gap). */
  progress: number;
  /** Until `now` ends, or until `next` starts in a gap; undefined when nothing follows. */
  remainingMs?: number;
  /** `now` is a hole in the guide ("No information"). */
  gap: boolean;
}>;

function firstEndingAfter(programs: readonly GuideProgram[], t: number): number {
  let lo = 0, hi = programs.length;
  while (lo < hi) { const mid = (lo + hi) >> 1; if (programs[mid]!.end <= t) lo = mid + 1; else hi = mid; }
  return lo;
}

export function nowNext(programs: readonly GuideProgram[], now: number): NowNext {
  const i = firstEndingAfter(programs, now);
  const candidate = programs[i];
  if (candidate && candidate.start <= now) {
    const next = programs[i + 1];
    return {now: candidate, ...(next ? {next} : {}), progress: (now - candidate.start) / (candidate.end - candidate.start), remainingMs: candidate.end - now, gap: false};
  }
  return {...(candidate ? {next: candidate, remainingMs: candidate.start - now} : {}), progress: 0, gap: true};
}

/** The next instant at which `nowNext` changes (a boundary), or undefined when nothing follows. */
export function nextChange(programs: readonly GuideProgram[], now: number): number | undefined {
  const i = firstEndingAfter(programs, now);
  const p = programs[i];
  if (!p) return undefined;
  return p.start > now ? p.start : p.end;
}
