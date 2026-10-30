import {routes, type RouteId} from '../../../../packages/contracts/src/routes';
import {routeBytes, RouteError} from '../../../../packages/client-core/src/route-identity';

const operations = new Set<RouteId>(['get_reading_items', 'get_reading_continue', 'get_reading_discover', 'get_reading_series_items', 'get_items_itemId_reading_versions', 'post_reading_sessions',
  'get_reading_sessions_sessionId_progress', 'put_reading_sessions_sessionId_progress', 'delete_reading_sessions_sessionId',
  'head_reading_sessions_sessionId_source', 'get_reading_sessions_sessionId_source', 'get_reading_sessions_sessionId_assets', 'get_reading_sessions_sessionId_pages_pageIndex']);
const compiled = routes.filter(route => operations.has(route.id)).map(route => ({...route,
  pattern: new RegExp('^' + route.path.split(/(\{[^}]+\})/).map(part => part.startsWith('{') ? '[A-Za-z0-9_-]{1,128}' : part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('') + '$')}));
const binary = new Map<RouteId, number>([['head_reading_sessions_sessionId_source', 65536], ['get_reading_sessions_sessionId_source', 65536],
  ['get_reading_sessions_sessionId_assets', 32 * 1024 * 1024], ['get_reading_sessions_sessionId_pages_pageIndex', 16 * 1024 * 1024]]);
/** Generated route identities select the local byte adapter, never an arbitrary URL. */
export function nativeReadingPolicy(url: string, method: string, headers?: HeadersInit): Readonly<{binaryLimit?: number; sequential?: true}> | undefined {
  const path = new URL(url).pathname, operation = compiled.find(route => route.method === method && route.pattern.test(path));
  if (operation?.id === 'get_reading_sessions_sessionId_source' && !new Headers(headers).has('Range')) return {sequential: true};
  return operation ? {binaryLimit: binary.get(operation.id)} : undefined;
}
type NativeReply = Readonly<{status: number; headers: Record<string, string>; body?: string; bodyBase64?: string}>;
const invalid = () => new RouteError('invalid_response', 'Invalid native Reading response.');

/** Metadata arrives no later than the first upstream body byte or end of body
 * (Apple §44); the first chunk stays native until read. Every pull is <=48KiB.
 * Neither Response nor this adapter accumulates the publication. */
export function nativeReadingStreamResponse(raw: Pick<NativeReply, 'status' | 'headers'>,
  transport: Readonly<{read: () => Promise<Readonly<{done: boolean; bodyBase64: string}>>; cancel: () => void}>, signal?: AbortSignal): Response {
  let ended = false, failure: unknown, locked = false, pending = false, used = false;
  let rejectPending: ((reason: unknown) => void) | undefined;
  const finish = (error?: unknown) => {
    if (ended) return;
    ended = true; failure = error; signal?.removeEventListener('abort', aborted);
    try {transport.cancel();} catch { /* runtime already invalidated */ }
    if (error !== undefined) rejectPending?.(error);
  };
  const aborted = () => finish(new RouteError('cancelled', 'Connection request cancelled.'));
  const cancel = async () => {aborted();};
  const body = {
    get locked() {return locked;}, cancel,
    getReader() {
      if (locked) throw invalid(); locked = true;
      let released = false;
      return {
        async read(): Promise<ReadableStreamReadResult<Uint8Array>> {
          if (released || pending) throw invalid();
          if (signal?.aborted) aborted();
          if (failure !== undefined) throw failure;
          if (ended) return {done: true, value: undefined};
          pending = true; used = true;
          const stopped = new Promise<never>((_resolve, reject) => {rejectPending = reject;});
          try {
            const next = await Promise.race([Promise.resolve().then(() => {if (ended) throw failure ?? invalid(); return transport.read();}), stopped]);
            if (failure !== undefined) throw failure;
            if (ended || typeof next.done !== 'boolean' || typeof next.bodyBase64 !== 'string') throw invalid();
            if (next.done) {if (next.bodyBase64 !== '') throw invalid(); finish(); return {done: true, value: undefined};}
            const text = next.bodyBase64;
            if (!text || text.length > 65536 || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(text)) throw invalid();
            const bytes = routeBytes(text.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, ''), undefined, 49152);
            return {done: false, value: bytes};
          } catch (error) {finish(error); throw error;}
          finally {pending = false; rejectPending = undefined;}
        },
        cancel,
        releaseLock() {if (pending) throw invalid(); released = true; locked = false;},
      };
    },
  };
  signal?.addEventListener('abort', aborted, {once: true});
  if (signal?.aborted) {aborted(); throw failure;}
  try {
    const response = new Response(null, {status: raw.status, headers: raw.headers});
    Object.defineProperties(response, {body: {value: body}, bodyUsed: {get: () => used}});
    return response;
  } catch (error) {finish(error); throw error;}
}

/** A bounded body reader on this response only. Hermes's ordinary fetch Response
 * has no ReadableStream body; the captured facade still consumes actual bytes
 * using its exact-length/MIME/revision checks. No global stream/Response patch. */
export function nativeReadingResponse(raw: NativeReply, binaryLimit: number | undefined, signal?: AbortSignal): Response {
  if (signal?.aborted) throw new RouteError('cancelled', 'Connection request cancelled.');
  let bytes: Uint8Array;
  if (binaryLimit !== undefined) {
    const text = raw.bodyBase64;
    if (typeof text !== 'string' || text.length > Math.ceil(binaryLimit / 3) * 4 || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(text)) throw invalid();
    bytes = text ? routeBytes(text.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, ''), undefined, binaryLimit) : new Uint8Array();
  } else {
    if (typeof raw.body !== 'string' || raw.body.length > 2 * 1024 * 1024) throw invalid();
    bytes = new TextEncoder().encode(raw.body);
    if (bytes.length > 2 * 1024 * 1024) throw invalid();
  }
  const empty = [204, 205, 304].includes(raw.status);
  if (empty && bytes.length) throw invalid();
  const buffer = bytes.buffer;
  if (!(buffer instanceof ArrayBuffer)) throw invalid();
  const response = new Response(empty ? null : buffer, {status: raw.status, headers: raw.headers});
  if (empty) return response;
  let data: Uint8Array | undefined = bytes, offset = 0, locked = false, used = false;
  const cancel = () => {data = undefined; signal?.removeEventListener('abort', cancel);};
  signal?.addEventListener('abort', cancel, {once: true});
  const body = {
    get locked() {return locked;},
    async cancel() {cancel();},
    getReader() {
      if (locked) throw invalid(); locked = true;
      let released = false;
      return {
        async read() {
          if (released) throw invalid();
          if (signal?.aborted) throw new RouteError('cancelled', 'Connection request cancelled.');
          used = true;
          if (!data || offset === data.length) {cancel(); return {done: true as const, value: undefined};}
          const value = data.slice(offset, offset + 65536); offset += value.length;
          return {done: false as const, value};
        },
        async cancel() {cancel();},
        releaseLock() {released = true; locked = false; cancel();},
      };
    },
  };
  Object.defineProperties(response, {body: {value: body}, bodyUsed: {get: () => used}});
  return response;
}
