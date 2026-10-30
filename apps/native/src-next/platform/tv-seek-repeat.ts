import type {TVKeyEvent} from './tv-key-adapter';

type Clock = Readonly<{
  now: () => number;
  schedule: (callback: () => void, milliseconds: number) => unknown;
  cancel: (timer: unknown) => void;
}>;
const clock: Clock = {now: Date.now, schedule: (callback, ms) => setTimeout(callback, ms), cancel: timer => clearTimeout(timer as ReturnType<typeof setTimeout>)};

/** One bounded cadence per focused seek control. Call stop on blur, coverage,
 * Back and owner retirement. A false step result also stops at the seek limit. */
export function createTVSeekRepeat(step: (direction: -1 | 1, seconds: number) => void | boolean, time: Clock = clock) {
  let held: {direction: -1 | 1; since: number; timer?: unknown} | undefined;
  const stop = () => {if (held?.timer !== undefined) time.cancel(held.timer); held = undefined;};
  const tick = (owner: NonNullable<typeof held>) => {
    if (held !== owner) return;
    if (step(owner.direction, 10) === false) {stop(); return;}
    if (held === owner) owner.timer = time.schedule(() => tick(owner), time.now() - owner.since >= 2000 ? 100 : 250);
  };
  return {
    stop,
    handle(event: TVKeyEvent) {
      const direction = event.key === 'left' ? -1 : event.key === 'right' ? 1 : undefined;
      if (direction === undefined) return;
      if (event.phase === 'long-end') {if (held?.direction === direction) stop(); return;}
      if (event.phase === 'press') {stop(); step(direction, 10); return;}
      if (held?.direction === direction) return;
      stop();
      // Vega supplies its own repeated direction events. Never add a second timer.
      if (event.source === 'vega') {step(direction, 10); return;}
      const owner: NonNullable<typeof held> = {direction, since: time.now()};
      held = owner; tick(owner);
    },
  };
}
