/** The launch clock includes time spent loading code, before React can mount. */
export function documentStartedAt(now = Date.now(), timeOrigin = globalThis.performance?.timeOrigin): number {
  return typeof timeOrigin === 'number' && Number.isFinite(timeOrigin) && timeOrigin > 0 && timeOrigin <= now ? timeOrigin : now;
}

export type BootWaitingPhase = 'picture' | 'spinner' | 'connecting' | 'still-connecting';
export function bootWaitingPhase(startedAt: number, now: number): BootWaitingPhase {
  const elapsed = Math.max(0, now - startedAt);
  return elapsed >= 8000 ? 'still-connecting' : elapsed >= 3000 ? 'connecting' : elapsed >= 1000 ? 'spinner' : 'picture';
}

/** A retry, a suspended tab and a network change never restart the launch clock. */
export function bootRecoveryDelay(startedAt: number, now: number, offline: boolean): number {
  return Math.max(0, startedAt + (offline ? 1000 : 15000) - now);
}

/** Presentation only: the connection still owns its request, retry and guards. */
export function bootRecoveryDeadline(startedAt: number, reveal: () => void, now: () => number = Date.now) {
  let live = true, shown = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const clear = () => { if (timer !== undefined) clearTimeout(timer); timer = undefined; };
  const update = (offline: boolean) => {
    clear();
    if (!live || shown) return;
    const show = () => {
      timer = undefined;
      if (!live || shown) return;
      shown = true; reveal();
    };
    const delay = bootRecoveryDelay(startedAt, now(), offline);
    if (delay === 0) show();
    else timer = setTimeout(show, delay);
  };
  return {update, dispose: () => { live = false; clear(); }};
}
