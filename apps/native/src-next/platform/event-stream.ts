import {NativeModules} from 'react-native';
import type {HttpLocalApi} from '../../../../packages/client-core/src/index';
import {routeBytes} from '../../../../packages/client-core/src/route-identity';

/** A bounded native SSE reader on the same verified connection as ordinary API traffic.
 * Native transports refuse redirects before credentials follow; route changes close the reader
 * so its owner reconnects with its existing cursor and retry policy. */
export async function openEventStream(api: HttpLocalApi, path: string, headers: Record<string, string>, signal: AbortSignal): Promise<Response> {
  const route = api.getRouteConnection();
  if (!route || !path.startsWith('/v1/') || path.startsWith('//') || signal.aborted) throw new Error('native_event_route_required');
  const requested = new URL(path, api.baseUrl);
  if (requested.origin !== new URL(api.baseUrl).origin || requested.username || requested.password || requested.hash) throw new Error('native_event_origin_mismatch');
  const runtime = NativeModules.PorticoRuntime;
  const id = runtime.playbackRequestId();
  let closed = false, dispatched = false, reading = false;
  let revision: number | undefined;
  let unsubscribe = () => {};
  const close = () => {
    if (closed) return;
    closed = true;
    unsubscribe();
    signal.removeEventListener('abort', close);
    runtime.cancelNetworkStream(id);
  };
  signal.addEventListener('abort', close, {once: true});
  unsubscribe = route.subscribe(() => {
    if (dispatched && route.getSnapshot().revision !== revision) close();
  });
  const recover = (error: unknown) => {
    // An idle socket, local admission failure or redirect says nothing about the API route.
    const code = error && typeof error === 'object' ? (error as {code?: unknown}).code : undefined;
    if (code === 'event_transport' && !signal.aborted && route.getSnapshot().revision === revision) void route.recover().catch(() => {});
  };
  try {
    const origin = await route.resolve(signal);
    const selected = route.getSnapshot();
    if (closed || signal.aborted || selected.phase !== 'ready' || selected.origin !== origin) throw new Error('native_event_superseded');
    revision = selected.revision;
    dispatched = true;
    const raw = await runtime.openNetworkStream(id, origin + requested.pathname + requested.search, headers);
    if (closed || signal.aborted) throw new Error('native_event_superseded');
    const body = {
      getReader: () => ({
        read: async () => {
          if (closed) throw new Error('native_event_closed');
          if (reading) throw new Error('native_event_read_pending');
          reading = true;
          try {
            const next = await runtime.readNetworkStream(id);
            if (closed) throw new Error('native_event_closed');
            if (next.done) { close(); return {done: true}; }
            return {done: false, value: routeBytes(next.data.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, ''), undefined, 16384)};
          } catch (error) {
            recover(error);
            close();
            throw error;
          } finally { reading = false; }
        },
        cancel: async () => { close(); },
        releaseLock: close,
      }),
    };
    if (raw.status < 200 || raw.status >= 300) close();
    return {ok: raw.status >= 200 && raw.status < 300, status: raw.status, headers: new Headers(raw.headers), body} as unknown as Response;
  } catch (error) {
    if (!closed) recover(error);
    close();
    throw error;
  }
}
