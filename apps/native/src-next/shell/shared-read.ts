/** One in-flight read per key: callers that ask while it runs share its answer. Each caller's own
 * signal only releases that caller; the read itself stops when every caller has gone. */
export function createSharedReads() {
  const flights = new Map<string, {promise: Promise<unknown>; controller: AbortController; callers: number}>();
  return function shared<T>(key: string, signal: AbortSignal | undefined, read: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (signal?.aborted) return Promise.reject(Object.assign(new Error('shared_read_cancelled'), {name: 'AbortError'}));
    let flight = flights.get(key);
    if (!flight) {
      const controller = new AbortController();
      const promise = read(controller.signal).finally(() => {if (flights.get(key) === flight) flights.delete(key);});
      flight = {promise, controller, callers: 0};
      flights.set(key, flight);
    }
    const joined = flight;
    joined.callers++;
    return new Promise<T>((resolve, reject) => {
      let left = false;
      const leave = () => {
        if (left) return; left = true;
        signal?.removeEventListener('abort', abort);
        if (--joined.callers === 0 && flights.get(key) === joined) {flights.delete(key); joined.controller.abort();}
      };
      const abort = () => {leave(); reject(Object.assign(new Error('shared_read_cancelled'), {name: 'AbortError'}));};
      signal?.addEventListener('abort', abort, {once: true});
      (joined.promise as Promise<T>).then(value => {if (!left) {left = true; signal?.removeEventListener('abort', abort); joined.callers--; resolve(value);}},
        error => {if (!left) {left = true; signal?.removeEventListener('abort', abort); joined.callers--; reject(error);}});
    });
  };
}
