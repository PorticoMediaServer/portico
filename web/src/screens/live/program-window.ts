import {nowNext, type GuideProgram} from '@core/guide/index.ts';

/** Guide browsing never changes the List view's actual now/next identities. */
export function liveProgramWindow(view: 'guide' | 'list', start: number, end: number, now: number) {
  if (view === 'guide') return {start, end};
  const current = Math.floor(now / 1_800_000) * 1_800_000;
  return {start: current, end: current + 6 * 3_600_000};
}

/** The window store already normalizes these programmes. Preserve its order and
 * leave the current slot empty in a real schedule gap. */
export function liveListPrograms(programs: readonly GuideProgram[], now: number) {
  const current = nowNext(programs, now), next = current.next;
  const nextIndex = next ? programs.indexOf(next) : -1;
  return [current.now, next, nextIndex < 0 ? undefined : programs[nextIndex + 1]] as const;
}
