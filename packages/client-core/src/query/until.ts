/**
 * `value`, or a rejection as soon as `signal` aborts. A read in the viewer's cache is shared with
 * its other owners and goes on for them when one owner stops waiting for it.
 */
export function until<T>(value: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const stop = () => reject(Object.assign(new Error('aborted'), {name: 'AbortError'}));
    if (signal.aborted) { stop(); return; }
    signal.addEventListener('abort', stop, {once: true});
    value.then(v => { signal.removeEventListener('abort', stop); resolve(v); }, e => { signal.removeEventListener('abort', stop); reject(e); });
  });
}
