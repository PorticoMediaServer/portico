/**
 * A list the server says changed under the page (`refresh-required`: even its first page was
 * refused as out of date) is read again by itself, quietly, with what is shown kept in place:
 * open screens stay current without anyone pressing Refresh. Only a list that is still refused
 * after a few tries (about half a minute) says so, with the notice's own Refresh. One rule for
 * the web and the apps; each client runs the tries only while it is in front.
 *
 * Tries back off with jitter; a list that has stayed readable for a minute counts from zero again.
 */
export const QUIET_REFRESH_TRIES = 3;
const BASE_MS = 1500, MAX_MS = 12000, SETTLED_MS = 60_000;

export type QuietRefreshState = Readonly<{tries: number; lastAt: number}>;

/** The pure rule: whether to try again now (and after how long), or say the list is stuck. */
export function quietRefreshStep(state: QuietRefreshState, now: number, random: () => number = Math.random): {state: QuietRefreshState; delayMs?: number; stuck: boolean} {
  const tries = now - state.lastAt > SETTLED_MS ? 0 : state.tries;
  if (tries >= QUIET_REFRESH_TRIES) return {state: {tries, lastAt: state.lastAt}, stuck: true};
  if (tries === 0) return {state: {tries: 1, lastAt: now}, delayMs: 0, stuck: false};
  const base = Math.min(MAX_MS, BASE_MS * 2 ** (tries - 1));
  return {state: {tries: tries + 1, lastAt: now}, delayMs: Math.round(base + random() * base / 2), stuck: false};
}
