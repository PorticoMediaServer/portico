import type {Planar} from './decoder';
import type {PcmSource} from './engine';
import {MediaReadError} from './bytes';
import {TrackStream, type TrackPlan} from './track';
import type {ByteReader} from './types';

/**
 * A converted presentation (spec §18.1 `mode: "converted"`): FLAC or Ogg Opus that the server
 * encodes from the trimmed PCM, streamed without Range. `?fromFrame=N` starts at output frame N,
 * and each stream says its own trim (`X-Audio-Trim-Start`/`-End`; Opus's is its pre-skip).
 *
 * Before commit the server serves only frame 0, and only `prefetchBytes`: the stream simply ends
 * there. The source then waits for `unlock()` (commit) and continues from exactly the next frame,
 * so the track still plays every frame once.
 */
export type ConvertedPlan = TrackPlan & Readonly<{url: string}>;
export type StreamFetch = (url: string, init: RequestInit) => Promise<Response>;

/** Sequential bytes of one response body, as a ByteReader. The first 256 KB (headers) are kept,
 * and a 4 MB window behind the furthest read. */
export class StreamReader implements ByteReader {
  readonly size = undefined;
  private readonly body: ReadableStreamDefaultReader<Uint8Array>;
  private head = new Uint8Array(0); // bytes [0, head.length)
  private window = new Uint8Array(0); // bytes [windowStart, windowStart + window.length)
  private windowStart = 0;
  private total = 0;
  private done = false;
  constructor(body: ReadableStream<Uint8Array>) { this.body = body.getReader(); }
  private async pull(): Promise<boolean> {
    if (this.done) return false;
    const r = await this.body.read();
    if (r.done) { this.done = true; return false; }
    const chunk = r.value;
    const keepHead = Math.max(0, Math.min(chunk.length, (1 << 18) - this.total));
    if (keepHead) { const h = new Uint8Array(this.head.length + keepHead); h.set(this.head); h.set(chunk.subarray(0, keepHead), this.head.length); this.head = h; }
    const w = new Uint8Array(this.window.length + chunk.length); w.set(this.window); w.set(chunk, this.window.length); this.window = w;
    this.total += chunk.length;
    return true;
  }
  async read(offset: number, length: number): Promise<Uint8Array> {
    while (this.total < offset + length && await this.pull()) { /* more */ }
    const end = Math.min(this.total, offset + length);
    if (end <= offset) return new Uint8Array(0);
    const out = new Uint8Array(end - offset);
    for (let i = offset; i < end;) {
      if (i < this.head.length) { const n = Math.min(end, this.head.length) - i; out.set(this.head.subarray(i, i + n), i - offset); i += n; continue; }
      if (i < this.windowStart) throw new MediaReadError('invalid_read', 'The converted stream was read out of order.');
      const n = end - i; out.set(this.window.subarray(i - this.windowStart, i - this.windowStart + n), i - offset); i += n;
    }
    // Forget what lies far behind this read.
    const keepFrom = Math.max(0, offset - (4 << 20));
    if (keepFrom > this.windowStart) { this.window = this.window.slice(keepFrom - this.windowStart); this.windowStart = keepFrom; }
    return out;
  }
  close() { void this.body.cancel().catch(() => {}); }
}

export class ConvertedSource implements PcmSource {
  readonly sampleRate: number;
  readonly channels: number;
  private readonly plan: ConvertedPlan;
  private readonly fetcher: StreamFetch;
  private readonly abort = new AbortController();
  private stream?: TrackStream;
  private reader?: StreamReader;
  private at: number; // next track frame to deliver
  private locked: boolean;
  private waiting: (() => void)[] = [];
  private closed = false;

  constructor(plan: ConvertedPlan, fromFrame: number, o: {locked: boolean; fetch?: StreamFetch}) {
    this.plan = plan;
    this.sampleRate = plan.sampleRate;
    this.channels = plan.channels;
    this.at = fromFrame;
    this.locked = o.locked;
    // Preserve a neutral receiver for an injected native browser fetch.
    const fetcher = o.fetch ?? ((u, i) => fetch(u, i));
    this.fetcher = (u, i) => fetcher(u, i);
  }

  unlock() { this.locked = false; const w = this.waiting; this.waiting = []; for (const f of w) f(); }

  private async open(): Promise<TrackStream> {
    // Before commit only frame 0 is served; later frames wait for the commit.
    while (this.locked && this.at > 0 && !this.closed) await new Promise<void>(r => this.waiting.push(r));
    if (this.closed) throw new MediaReadError('cancelled', 'Reading was canceled.');
    const url = this.at > 0 ? `${this.plan.url}${this.plan.url.includes('?') ? '&' : '?'}fromFrame=${this.at}` : this.plan.url;
    const response = await this.fetcher(url, {signal: this.abort.signal, credentials: 'omit', cache: 'no-store', redirect: 'error'});
    if (!response.ok || !response.body) throw new MediaReadError(response.status === 403 ? 'prepared_limit' : 'media_unavailable', 'The converted audio could not be read.', response.status);
    const header = (name: string) => { const v = Number(response.headers.get(name)); return Number.isSafeInteger(v) && v >= 0 ? v : undefined; };
    const trim = {startFrames: header('X-Audio-Trim-Start') ?? (this.at === 0 ? this.plan.trim.startFrames : 0), endFrames: header('X-Audio-Trim-End') ?? 0};
    this.reader = new StreamReader(response.body);
    const segment: TrackPlan = {...this.plan, durationFrames: this.plan.durationFrames - this.at, trim};
    const stream = await TrackStream.open(segment, this.reader, 0, this.abort.signal, {pad: false});
    if (!stream) throw new MediaReadError('unsupported', 'This browser can’t decode the converted audio.');
    return stream;
  }

  async read(max: number): Promise<Planar | undefined> {
    for (let attempt = 0; this.at < this.plan.durationFrames && !this.closed; ) {
      if (!this.stream) this.stream = await this.open();
      const block = await this.stream.read(max);
      if (block) { this.at += block.frames; return block; }
      // The stream ended before the track did (a prepared budget, or a dropped connection):
      // continue from the next frame, a bounded number of times without progress.
      this.stream.close(); this.reader?.close(); this.stream = undefined;
      if (++attempt > 3) throw new MediaReadError('media_unavailable', 'The converted audio stream kept ending early.');
    }
    return undefined;
  }

  close() { this.closed = true; this.abort.abort(); this.stream?.close(); this.reader?.close(); this.unlock(); }
}
