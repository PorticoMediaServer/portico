/** Native AbortSignal has no static `any`. Link only for the owned operation,
 * then detach every upstream listener on cancellation or completion. */
export function linkNativeAbortSignals(signals: readonly (AbortSignal | undefined)[]) {
  const sources = [...new Set(signals.filter((signal): signal is AbortSignal => signal !== undefined))];
  const controller = new AbortController();
  const dispose = () => {for (const source of sources) source.removeEventListener('abort', stop);};
  const stop = () => {controller.abort(); dispose();};
  for (const source of sources) {
    if (source.aborted) {stop(); break;}
    source.addEventListener('abort', stop, {once: true});
  }
  return Object.freeze({signal: controller.signal, dispose});
}
