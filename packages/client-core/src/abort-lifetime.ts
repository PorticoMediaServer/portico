/** AbortSignal.any/timeout/throwIfAborted are not supplied by the native
 * abort-controller runtime. Every composed operation owns its own listeners
 * and optional deadline, and releases them on cancellation or completion. */
const reasons = new WeakMap<AbortSignal, unknown>();
const cancellation = () => Object.assign(new Error('The operation was aborted.'), {name: 'AbortError'});
const reasonOf = (signal: AbortSignal): unknown => signal.reason === undefined ? reasons.get(signal) : signal.reason;

export function linkAbortSignals(signals: readonly (AbortSignal | undefined)[], timeoutMs?: number): Readonly<{signal: AbortSignal; dispose: () => void}> {
  if (timeoutMs !== undefined && (!Number.isSafeInteger(timeoutMs) || timeoutMs < 0 || timeoutMs > 2147483647))
    throw new RangeError('Invalid abort deadline');
  const sources = [...new Set(signals.filter((signal): signal is AbortSignal => signal !== undefined))];
  const controller = new AbortController(), attached: {source: AbortSignal; listener: () => void}[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  const dispose = () => {
    if (timer !== undefined) {clearTimeout(timer); timer = undefined;}
    for (const {source, listener} of attached.splice(0)) source.removeEventListener('abort', listener);
  };
  const stop = (reason: unknown) => {
    if (controller.signal.aborted) return;
    const first = reason === undefined ? cancellation() : reason;
    // Native abort-controller ignores the argument and has no reason property.
    // Keep the same first cause available to our portable cancellation check.
    reasons.set(controller.signal, first);
    try {controller.abort(first);} finally {dispose();}
  };
  try {
    for (const source of sources) {
      if (source.aborted) {stop(reasonOf(source)); break;}
      const listener = () => stop(reasonOf(source));
      attached.push({source, listener});
      source.addEventListener('abort', listener, {once: true});
      // Also cover a synchronous abort during listener installation.
      if (source.aborted) {stop(reasonOf(source)); break;}
    }
    if (!controller.signal.aborted && timeoutMs !== undefined)
      timer = setTimeout(() => stop(Object.assign(new Error('The operation timed out.'), {name: 'TimeoutError'})), timeoutMs);
    return Object.freeze({signal: controller.signal, dispose});
  } catch (error) {dispose(); throw error;}
}

/** Always an Error-shaped cancellation, including on native AbortSignal
 * implementations that expose neither reason nor throwIfAborted. */
export function throwIfAborted(signal: AbortSignal | undefined): void {
  if (!signal?.aborted) return;
  const reason = reasonOf(signal);
  if (reason instanceof Error || reason && typeof reason === 'object' && typeof (reason as {name?: unknown}).name === 'string' && typeof (reason as {message?: unknown}).message === 'string') throw reason;
  throw cancellation();
}
