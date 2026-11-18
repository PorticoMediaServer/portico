import {routeBytes, RouteError} from '../../../../packages/client-core/src/route-identity';

/** The rebuilt server's book bytes, at the media address the book route gave: a PDF by range (or whole, when
 * saved), one EPUB part, one comic page. Each comes through the native byte exchange with the old limits. */
const grant = '/v1/media/[A-Za-z0-9._-]{1,512}';
const media: readonly Readonly<{methods: readonly string[]; pattern: RegExp; limit: (headers: Headers, method: string) => number}>[] = [
  {methods: ['HEAD', 'GET'], pattern: new RegExp(`^${grant}/source$`), limit: (headers, method) => method === 'HEAD' || headers.has('Range') ? 65536 : 64 * 1024 * 1024},
  {methods: ['GET'], pattern: new RegExp(`^${grant}/parts/[^?#]{1,1024}$`), limit: () => 32 * 1024 * 1024},
  {methods: ['GET'], pattern: new RegExp(`^${grant}/pages/[0-9]{1,10}$`), limit: () => 16 * 1024 * 1024},
];
/** Only a book's bytes at the media address its book route gave use the native byte exchange, never an arbitrary URL. */
export function nativeReadingPolicy(url: string, method: string, headers?: HeadersInit): Readonly<{binaryLimit: number}> | undefined {
  const path = new URL(url).pathname;
  const book = media.find(route => route.methods.includes(method) && route.pattern.test(path));
  return book ? {binaryLimit: book.limit(new Headers(headers), method)} : undefined;
}
type NativeReply = Readonly<{status: number; headers: Record<string, string>; body?: string; bodyBase64?: string}>;
const invalid = () => new RouteError('invalid_response', 'Invalid native Reading response.');

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
