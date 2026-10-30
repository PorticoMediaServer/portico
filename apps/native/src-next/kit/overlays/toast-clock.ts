export type Clock = {now: () => number; schedule: (run: () => void, delay: number) => unknown; cancel: (handle: unknown) => void};
/** One expiry, paused independently by touch, keyboard focus and app suspension. */
export function createToastClock(clock: Clock, expire: (id: string) => void) {
  let current: {id: string; remaining: number} | undefined;
  let started = 0;
  let handle: unknown;
  let generation = 0;
  const holds = new Set<string>();
  const stop = () => {
    generation++;
    if (handle !== undefined) {
      clock.cancel(handle);
      handle = undefined;
      if (current) current.remaining = Math.max(0, current.remaining - (clock.now() - started));
    }
  };
  const resume = () => {
    if (!current || holds.size || handle !== undefined) return;
    const token = ++generation;
    const id = current.id;
    started = clock.now();
    handle = clock.schedule(() => {
      if (token !== generation || current?.id !== id) return;
      handle = undefined;
      current = undefined;
      expire(id);
    }, current.remaining);
  };
  return {
    replace(id: string | null, duration: number) {
      stop();
      // A removed toast cannot retain a touch/focus hold over its replacement.
      for (const reason of holds) if (reason !== 'background' && reason !== 'screen-reader') holds.delete(reason);
      current = id == null ? undefined : {id, remaining: Math.max(0, duration)};
      resume();
    },
    pause(reason: string, paused: boolean) {
      if (paused) {stop(); holds.add(reason);} else {holds.delete(reason); resume();}
    },
    dispose() {stop(); current = undefined; holds.clear();},
  };
}
