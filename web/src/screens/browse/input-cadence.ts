/** One pending text dispatch for a filter opening. Rapid typing replaces the
 * pending value; closing or replacing the original opening removes it, so a
 * late timer can never apply a retired value to the next opening. */
export type CadenceClock = Readonly<{set: (run: () => void, milliseconds: number) => unknown; clear: (handle: unknown) => void}>;
export const timerClock: CadenceClock = {
 set: (run, milliseconds) => setTimeout(run, milliseconds),
 clear: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
};
export function createInputCadence(delay = 250, clock: CadenceClock = timerClock) {
 let handle: unknown, generation = 0, closed = false, key = '';
 const cancelTimer = () => { if (handle !== undefined) { clock.clear(handle); handle = undefined; } };
 return {
  /** Replace any pending text with this value for this field; the newest wins. */
  schedule(nextKey: string, value: string, dispatch: (value: string) => void) {
   if (closed) return;
   cancelTimer(); key = nextKey;
   const run = ++generation;
   let own: unknown;
   own = clock.set(() => {
    // Validate this exact ticket before touching shared state: a late timer from
    // a replaced value can neither dispatch nor clear the replacement's handle.
    if (closed || run !== generation || handle !== own) return;
    handle = undefined;
    dispatch(value);
   }, delay);
   handle = own;
  },
  /** A replaced opening or field retires its pending value without closing. */
  reset() { generation++; key = ''; cancelTimer(); },
  /** Closing retires the opening for good; later timers are inert. */
  close() { if (closed) return; closed = true; generation++; key = ''; cancelTimer(); },
  pendingKey() { return closed || handle === undefined ? '' : key; },
 };
}
export type InputCadence = ReturnType<typeof createInputCadence>;
