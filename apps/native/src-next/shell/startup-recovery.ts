/** One captured root owns this clock and every restore it starts. A timer never
 * selects another server, installation or session, and only one read is in flight. */
export type NativeStartupWait = Readonly<{
  stage: 'boot' | 'recovery';
  spinner: boolean;
  message?: 'connecting' | 'still-connecting';
  offline: boolean;
  pending: boolean;
  retryAt?: number;
}>;

/** No restore attempt waits longer than this before the person sees why. */
export const RESTORE_ATTEMPT_TIMEOUT_MS = 30_000;
export const restoreAttemptTimeout = () => Object.assign(new Error('native_restore_timeout'), {code: 'timeout', retryable: true});

export const startupRetryDelays = [2_000, 4_000, 8_000, 15_000, 30_000, 60_000] as const;

export function startupWait(elapsedMs: number, online?: boolean, pending = false, retryAt?: number): NativeStartupWait {
  const offline = online === false;
  return Object.freeze({stage: elapsedMs >= (offline ? 1_000 : 15_000) ? 'recovery' : 'boot',
    spinner: elapsedMs >= 1_000, message: elapsedMs >= 8_000 ? 'still-connecting' : elapsedMs >= 3_000 ? 'connecting' : undefined,
    offline, pending, retryAt});
}

export function startupRetrySeconds(retryAt: number, now: number): number {
  return Math.max(0, Math.ceil((retryAt - now) / 1_000));
}

type Cancel = () => void;
type Options<T> = Readonly<{
  startedAt: number;
  current: () => boolean;
  online?: boolean;
  foreground: boolean;
  restore: (signal: AbortSignal) => Promise<T>;
  discard: (result: T) => void;
  retryable: (error: unknown) => boolean;
  /** One restore attempt's bound (E-mobile-01): past it the attempt ends as a retryable timeout,
   * shown like any other, and its slot is released even if a native read never answers. */
  attemptTimeoutMs?: number;
  onAttemptTimeout?: () => void;
  onReady: (result: T) => void;
  onError: (error: unknown, retrying: boolean) => void;
  onWait: (wait: NativeStartupWait) => void;
  now?: () => number;
  schedule?: (delayMs: number, callback: () => void) => Cancel;
}>;

/** The four one-shot milestones replace the shell's old 120 ms render timer.
 * Countdown painting has its own small component; it never ticks this root. */
export function createNativeStartupRecovery<T>(options: Options<T>) {
  const now = options.now ?? Date.now;
  const schedule = options.schedule ?? ((delayMs: number, callback: () => void) => {
    const timer = setTimeout(callback, delayMs); return () => clearTimeout(timer);
  });
  let alive = true, settled = false, paused = false, pending = false, resumeRequested = false;
  let online = options.online, foreground = options.foreground, step = 0;
  let offlineSeen = online === false, recoveryShown = false;
  let attempt: AbortController | undefined, retryAt: number | undefined, cancelRetry: Cancel | undefined;
  let flight: AbortController | undefined;
  let snapshot: NativeStartupWait | undefined;
  const milestones: Cancel[] = [];
  const current = () => alive && options.current();
  const eligible = () => current() && !settled && !paused && foreground && online !== false;
  const publish = () => {
    if (!current() || settled) return;
    const waiting = startupWait(Math.max(0, now() - options.startedAt), online, pending, retryAt);
    recoveryShown ||= waiting.stage === 'recovery';
    const next = recoveryShown && waiting.stage === 'boot' ? Object.freeze({...waiting, stage: 'recovery' as const}) : waiting;
    if (snapshot && (Object.keys(next) as (keyof NativeStartupWait)[]).every(key => next[key] === snapshot![key])) return;
    snapshot = next; options.onWait(next);
  };
  const clearRetry = () => {cancelRetry?.(); cancelRetry = undefined; retryAt = undefined;};
  const clearTimers = () => {clearRetry(); for (const cancel of milestones.splice(0)) cancel();};
  const later = () => {
    clearRetry();
    if (!eligible()) {publish(); return;}
    const delay = startupRetryDelays[Math.min(step++, startupRetryDelays.length - 1)]!;
    retryAt = now() + delay;
    cancelRetry = schedule(delay, () => {cancelRetry = undefined; retryAt = undefined; run();});
    publish();
  };
  const run = (allowOffline = false, initial = false) => {
    // The first call must still read secure storage when offline: a signed-out
    // device can open its front door without ever needing the server.
    if (!current() || settled || paused || !initial && !foreground || !allowOffline && online === false) return;
    if (flight) {
      // A lower-level native read may not implement AbortSignal yet. Its
      // promise still owns the slot until it settles, even after pause aborts.
      if (flight.signal.aborted) {resumeRequested = true; pending = true; publish();}
      return;
    }
    clearRetry(); pending = true;
    resumeRequested = false;
    const captured = new AbortController(); attempt = captured; flight = captured;
    const attemptCurrent = () => current() && !settled && !paused && attempt === captured && !captured.signal.aborted;
    const resumeAfterRetirement = () => {
      pending = false;
      const requested = resumeRequested; resumeRequested = false;
      if (requested && current() && !settled && !paused && foreground) run(true);
    };
    publish();
    void (async () => {
      let result: T;
      let timedOut = false, cancelDeadline: Cancel | undefined;
      const work = options.restore(captured.signal);
      const deadline = new Promise<never>((_, reject) => {
        cancelDeadline = schedule(options.attemptTimeoutMs ?? RESTORE_ATTEMPT_TIMEOUT_MS, () => {timedOut = true; reject(restoreAttemptTimeout());});
      });
      try {result = await Promise.race([work, deadline]);} catch (error) {
        cancelDeadline?.();
        if (flight === captured) flight = undefined;
        if (timedOut) {
          // The stuck attempt is retired after it is reported; a late answer is discarded.
          options.onAttemptTimeout?.();
          void work.then(value => options.discard(value), () => {});
          queueMicrotask(() => captured.abort());
        }
        if (!attemptCurrent()) {resumeAfterRetirement(); return;}
        pending = false;
        const retrying = options.retryable(error);
        options.onError(error, retrying);
        if (!attemptCurrent()) return;
        if (retrying) later();
        else {settled = true; clearTimers();}
        return;
      }
      cancelDeadline?.();
      if (flight === captured) flight = undefined;
      if (!attemptCurrent()) {options.discard(result); resumeAfterRetirement(); return;}
      // The runtime now belongs to its owner: finishing or disposing recovery never aborts the
      // signal it was restored under (E-tv-21), the owner retires it explicitly.
      if (attempt === captured) attempt = undefined;
      pending = false; settled = true; clearTimers();
      options.onReady(result);
    })();
  };
  const retryNow = () => {
    if (!current() || settled || paused) return false;
    // A deliberate Try again (or foreground retry) still probes the original
    // server when the OS's last offline fact may be stale. Scheduled retries wait.
    step = 0; clearRetry(); run(true); publish(); return true;
  };
  for (const threshold of [1_000, 3_000, 8_000, 15_000]) {
    const remaining = options.startedAt + threshold - now();
    if (remaining > 0) milestones.push(schedule(remaining, publish));
  }
  run(true, true);
  return {
    retryNow,
    networkChanged(value: boolean | undefined) {
      if (!current() || settled) return;
      const reconnected = offlineSeen && value === true; online = value;
      if (value === false) {offlineSeen = true; clearRetry();}
      else if (value === true) offlineSeen = false;
      publish();
      if (reconnected) retryNow();
    },
    foregroundChanged(value: boolean) {
      if (!current() || settled) return;
      const woke = !foreground && value; foreground = value;
      if (!value) {clearRetry(); publish();}
      else if (woke) retryNow();
    },
    pause() {
      if (!current() || settled || paused) return;
      paused = true; resumeRequested = false; clearRetry(); attempt?.abort(); pending = false; publish();
    },
    resume() {
      if (!current() || settled || !paused) return;
      paused = false; retryNow();
    },
    finish() {settled = true; clearTimers(); attempt?.abort(); pending = false;},
    dispose() {alive = false; settled = true; clearTimers(); attempt?.abort(); pending = false;},
  };
}
