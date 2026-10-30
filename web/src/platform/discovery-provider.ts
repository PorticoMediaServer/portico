import type {ConnectionEnvironment} from '@core/server-connections.ts';

type DiscoveryProvider = NonNullable<ConnectionEnvironment['discover']>;
type DiscoveryLease = {provider: DiscoveryProvider; closed: boolean; requests: Set<AbortController>};
let installed: DiscoveryLease | undefined;
const retired = () => new DOMException('Discovery provider retired.', 'AbortError');
function close(lease: DiscoveryLease) {
  if (lease.closed) return;
  lease.closed = true;
  for (const request of lease.requests) request.abort(retired());
  lease.requests.clear();
}

/** Native installs before main; installation itself performs no bridge, network
 * or storage work. Native still owns disposal of its discovery producer. */
export function installBrowserDiscoveryProvider(discover: DiscoveryProvider): () => void {
  if (typeof discover !== 'function') throw new TypeError('Invalid discovery provider.');
  const previous = installed, lease: DiscoveryLease = {provider: discover, closed: false, requests: new Set()};
  installed = lease;
  if (previous) close(previous);
  return () => {if (installed === lease) installed = undefined;close(lease);};
}

/** Capture a lease rather than retarget an existing connection to a successor.
 * These are only candidate facts; the core identity/proof guards admit routes. */
export function browserDiscoveryProvider(): DiscoveryProvider | undefined {
  const lease = installed;
  if (!lease) return;
  return async caller => {
    caller.throwIfAborted();
    if (lease.closed || installed !== lease) throw retired();
    const request = new AbortController(), abort = () => request.abort(caller.reason);
    const signal = request.signal;
    let stop!: () => void;
    const cancelled = new Promise<never>((_resolve, reject) => {
      stop = () => reject(signal.reason);
      signal.addEventListener('abort', stop, {once: true});
    });
    lease.requests.add(request);
    caller.addEventListener('abort', abort, {once: true});
    try {
      // Value-call the transport; do not supply the lease as its receiver.
      const provider = lease.provider;
      const result = await Promise.race([Promise.resolve().then(() => {
        signal.throwIfAborted();
        return provider(signal);
      }), cancelled]);
      signal.throwIfAborted();
      if (lease.closed || installed !== lease) throw retired();
      return result;
    } finally {
      caller.removeEventListener('abort', abort);
      signal.removeEventListener('abort', stop);
      lease.requests.delete(request);
    }
  };
}
