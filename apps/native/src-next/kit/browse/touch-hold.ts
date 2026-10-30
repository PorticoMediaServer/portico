export const touchHoldMs = 500;
export const touchHoldSlop = 10;
export type TouchHoldPhase = 'idle' | 'pending' | 'fired';
type Point = Readonly<{x: number; y: number}>;
type Clock = {schedule: (run: () => void, delay: number) => unknown; cancel: (ticket: unknown) => void};

/** Q6: window coordinates keep scrolling and card transforms out of the slop test. */
export function createTouchHold(onFire: () => void, onPhase: (phase: TouchHoldPhase) => void, clock: Clock) {
  let origin: Point | undefined, ticket: unknown, phase: TouchHoldPhase = 'idle', consumed = false, epoch = 0;
  const clear = () => {epoch++; if (ticket !== undefined) clock.cancel(ticket); ticket = undefined;};
  const change = (next: TouchHoldPhase) => {phase = next; onPhase(next);};
  const cancel = () => {if (origin) consumed = true; clear(); origin = undefined; change('idle');};
  return {
    start(point: Point) {
      clear(); origin = point; consumed = false; change('pending');
      const started = epoch;
      ticket = clock.schedule(() => {
        if (started !== epoch) return;
        ticket = undefined;
        if (!origin || phase !== 'pending') return;
        consumed = true; change('fired'); onFire();
      }, touchHoldMs);
    },
    move(point: Point) {if (origin && phase === 'pending' && Math.hypot(point.x - origin.x, point.y - origin.y) > touchHoldSlop) cancel();},
    end() {clear(); origin = undefined; change('idle');},
    cancel,
    consumePress() {const result = consumed; consumed = false; return result;},
    dispose() {clear(); origin = undefined;},
  };
}
