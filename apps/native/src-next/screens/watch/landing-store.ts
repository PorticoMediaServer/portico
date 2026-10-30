/** The landing time of a scrub in progress. The chrome holds only whether a scrub is under way;
 * the few nodes that print the time subscribe here, so a held scrub re-renders the scrubber and
 * its preview, never the whole player chrome (P2-71-PERF). */
export type LandingStore = Readonly<{get: () => number | undefined; set: (seconds: number | undefined) => void; subscribe: (listener: () => void) => () => void}>;
export function createLandingStore(): LandingStore {
  let value: number | undefined;
  const listeners = new Set<() => void>();
  return Object.freeze({
    get: () => value,
    set: (next: number | undefined) => {if (next === value) return; value = next; for (const listener of [...listeners]) listener();},
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
  });
}
