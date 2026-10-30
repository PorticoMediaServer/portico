import type {ByteReader} from './types';

/**
 * The compressed-bytes cache and the Range reader over it (§9.6 rule 1): files are fetched in
 * aligned blocks with `Range`, kept in one bounded LRU shared by every track (so a track played
 * again, or the next track fetched ahead, costs no second download), and evicted least recently
 * used first. Memory is bounded by the byte budget, never by library or queue size.
 */
export const BLOCK = 256 * 1024;

export class ByteCache {
  private readonly blocks = new Map<string, Uint8Array>();
  private used = 0;
  readonly budget: number;
  constructor(budget = 64 * 1024 * 1024) { this.budget = budget; }
  get(key: string): Uint8Array | undefined {
    const v = this.blocks.get(key);
    if (v) { this.blocks.delete(key); this.blocks.set(key, v); } // most recent last
    return v;
  }
  put(key: string, value: Uint8Array) {
    const old = this.blocks.get(key);
    if (old) { this.used -= old.length; this.blocks.delete(key); }
    this.blocks.set(key, value);
    this.used += value.length;
    for (const [k, v] of this.blocks) {
      if (this.used <= this.budget) break;
      this.blocks.delete(k); this.used -= v.length;
    }
  }
  get size() { return this.used; }
  /** Drops everything (sign-out, or a switch of profile or server: nothing fetched for one viewer
   * outlives it in this tab, as the native engine's cache does). */
  clear() { this.blocks.clear(); this.used = 0; }
  /** Drops one file's blocks (its media changed). */
  forget(prefix: string) { for (const [k, v] of this.blocks) if (k.startsWith(prefix + ':')) { this.blocks.delete(k); this.used -= v.length; } }
}

/** One compressed-bytes cache for every track in this tab (64 MB). */
export const audioByteCache = new ByteCache(64 * 1024 * 1024);

export class MediaReadError extends Error {
  readonly code: string;
  readonly status?: number;
  constructor(code: string, message: string, status?: number) { super(message); this.code = code; this.status = status; }
}

export type RangeFetch = (url: string, init: RequestInit) => Promise<Response>;

/**
 * Reads one file by `Range`. `limit` caps the bytes this reader may ask for in total (a prepared,
 * private presentation serves at most `prefetchBytes`, §18.1); reads past it wait until
 * `unlock()` (at commit) or fail when the reader is closed.
 */
export class RangeReader implements ByteReader {
  readonly size?: number;
  private readonly url: string;
  private readonly key: string;
  private readonly cache: ByteCache;
  private readonly fetcher: RangeFetch;
  private readonly inflight = new Map<number, Promise<Uint8Array>>();
  private limit: number;
  private requested = 0;
  private waiting: (() => void)[] = [];
  private readonly abort = new AbortController();
  private readonly onBytes?: (count: number) => void;
  constructor(o: {url: string; key: string; size?: number; cache: ByteCache; fetch?: RangeFetch; limit?: number; /** Bytes arrived from the server (not from the cache). */ onBytes?: (count: number) => void}) {
    this.url = o.url; this.key = o.key; this.size = o.size; this.cache = o.cache; this.onBytes = o.onBytes;
    // An injected browser fetch is a callback, not a method of this reader.
    const fetcher = o.fetch ?? ((u, i) => fetch(u, i));
    this.fetcher = (u, i) => fetcher(u, i);
    this.limit = o.limit ?? Infinity;
  }
  /** At commit: the whole file may now be fetched. */
  unlock() { this.limit = Infinity; const w = this.waiting; this.waiting = []; for (const f of w) f(); }
  close() { this.abort.abort(); const w = this.waiting; this.waiting = []; for (const f of w) f(); }
  get locked() { return this.limit !== Infinity; }

  private async block(index: number, signal?: AbortSignal): Promise<Uint8Array> {
    if (signal?.aborted || this.abort.signal.aborted) throw new MediaReadError('cancelled', 'Reading was canceled.');
    const key = `${this.key}:${index}`;
    const hit = this.cache.get(key);
    if (hit) return hit;
    const pending = this.inflight.get(index);
    if (pending) return waitFor(pending, signal);
    const request = linkedSignal(signal, this.abort.signal);
    const task = (async () => {
      const start = index * BLOCK, end = this.size !== undefined ? Math.min(this.size, start + BLOCK) : start + BLOCK;
      while (this.requested + (end - start) > this.limit && !request.signal.aborted) {
        await new Promise<void>(resolve => {
          const wake = () => { request.signal.removeEventListener('abort', wake); const i = this.waiting.indexOf(wake); if (i >= 0) this.waiting.splice(i, 1); resolve(); };
          this.waiting.push(wake); request.signal.addEventListener('abort', wake, {once: true});
          if (request.signal.aborted) wake();
        });
      }
      if (request.signal.aborted) throw new MediaReadError('cancelled', 'Reading was canceled.');
      this.requested += end - start;
      const response = await this.fetcher(this.url, {headers: {Range: `bytes=${start}-${end - 1}`}, signal: request.signal, credentials: 'omit', cache: 'no-store', redirect: 'error'});
      if (response.status === 416) return new Uint8Array(0);
      if (response.status === 403) throw new MediaReadError('prepared_limit', 'The prepared track can’t be read further before it is committed.', 403);
      if (!response.ok) throw new MediaReadError('media_unavailable', 'The audio file could not be read.', response.status);
      let expected = end - start;
      const invalid = async () => { try { await response.body?.cancel(); } catch {} throw new MediaReadError('invalid_range', 'The audio range could not be read.'); };
      if (response.status === 206) {
        const range = /^bytes (\d+)-(\d+)\/(\d+|\*)$/.exec(response.headers.get('Content-Range') ?? '');
        if (!range) return invalid();
        const first = Number(range[1]), last = Number(range[2]), total = range[3] === '*' ? undefined : Number(range[3]);
        if (!Number.isSafeInteger(first) || !Number.isSafeInteger(last) || first !== start || last < first || last >= end || (total !== undefined && (!Number.isSafeInteger(total) || total <= last || (this.size !== undefined && total !== this.size))) || (last < end - 1 && total !== last + 1)) return invalid();
        expected = last - first + 1;
      } else if (response.status !== 200 || start !== 0) return invalid();
      const bytes = await boundedBlock(response, expected, request.signal, count => { try { this.onBytes?.(count); } catch { /* a listener never breaks the read */ } });
      if (response.status === 206 && bytes.length !== expected) throw new MediaReadError('invalid_range', 'The audio range ended too soon.');
      // A small complete 200 response at the start is usable within the same byte bound.
      const data = bytes;
      this.cache.put(key, data);
      return data;
    })().finally(() => { request.release(); this.inflight.delete(index); });
    this.inflight.set(index, task);
    return task;
  }

  async read(offset: number, length: number, signal?: AbortSignal): Promise<Uint8Array> {
    if (signal?.aborted || this.abort.signal.aborted) throw new MediaReadError('cancelled', 'Reading was canceled.');
    if (!Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(length) || length < 0 || length > 64 * 1024 * 1024 || !Number.isSafeInteger(offset + length)) throw new MediaReadError('invalid_range', 'The audio range is too large.');
    if (this.size !== undefined) length = Math.max(0, Math.min(length, this.size - offset));
    if (length <= 0) return new Uint8Array(0);
    const first = Math.floor(offset / BLOCK), last = Math.floor((offset + length - 1) / BLOCK);
    const parts: Uint8Array[] = [];
    // A large MP4 index does not occupy every browser connection at once.
    for (let index = first; index <= last; index += 2) parts.push(...await Promise.all([this.block(index, signal), ...(index < last ? [this.block(index + 1, signal)] : [])]));
    const out = new Uint8Array(length);
    let written = 0;
    for (let i = 0; i < parts.length; i++) {
      const part = parts[i]!, blockStart = (first + i) * BLOCK;
      const from = Math.max(0, offset - blockStart), to = Math.min(part.length, offset + length - blockStart);
      if (to <= from) break;
      out.set(part.subarray(from, to), written);
      written += to - from;
      if (part.length < BLOCK) break; // end of file
    }
    return written === length ? out : out.subarray(0, written);
  }

  /** Fetches the rest of the file into the cache ahead of need (the next track, before its
   * boundary), when it fits the cache's budget. */
  async prefetchAll(signal?: AbortSignal): Promise<void> {
    if (this.size === undefined || this.size > this.cache.budget / 2) return;
    for (let i = 0; i * BLOCK < this.size; i++) { if (signal?.aborted) return; await this.block(i, signal); }
  }
}

function linkedSignal(a: AbortSignal | undefined, b: AbortSignal): {signal: AbortSignal; release(): void} {
  const controller = new AbortController(), stop = () => controller.abort();
  for (const signal of [a, b]) { signal?.addEventListener('abort', stop, {once: true}); if (signal?.aborted) stop(); }
  return {signal: controller.signal, release: () => { a?.removeEventListener('abort', stop); b.removeEventListener('abort', stop); }};
}

function waitFor<T>(promise: Promise<T>, signal?: AbortSignal): Promise<T> {
  if (!signal) return promise;
  return new Promise((resolve, reject) => {
    const abort = () => reject(new MediaReadError('cancelled', 'Reading was canceled.'));
    signal.addEventListener('abort', abort, {once: true});
    if (signal.aborted) abort();
    promise.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
  });
}

async function boundedBlock(response: Response, max: number, signal: AbortSignal, onBytes: (count: number) => void): Promise<Uint8Array> {
  const length = response.headers.get('Content-Length');
  const tooLarge = () => new MediaReadError('invalid_range', 'The audio range is too large.');
  if (length !== null && (!/^\d+$/.test(length) || Number(length) > max)) { try { await response.body?.cancel(); } catch {} throw tooLarge(); }
  if (!response.body) return new Uint8Array(0);
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = []; let count = 0;
  try {
    for (;;) {
      const next = await waitFor(reader.read(), signal);
      if (next.done) break;
      count += next.value.length;
      if (count > max) throw tooLarge();
      chunks.push(next.value);
      onBytes(next.value.length);
    }
    const bytes = new Uint8Array(count); let at = 0;
    for (const chunk of chunks) { bytes.set(chunk, at); at += chunk.length; }
    return bytes;
  } finally { void reader.cancel().catch(() => {}); reader.releaseLock(); }
}
