import {ResyncCoordinator, type ResyncReadContext, type ResyncRegistration} from '../resync-coordinator.ts';
import {routePath} from "../../../contracts/src/route-paths.ts";
/**
 * The one event feed (spec §15; style guide amendment 8; Public API ARCH-API-07): `/v1/events` as
 * SSE, with a long-poll fallback (`GET /v1/events?after=<id>&waitMs=25000` → `{events, nextAfter}`).
 *
 * - One subscription per device; features subscribe by type (`session.updated`) or prefix
 *   (`group.`), never open their own streams or poll.
 * - The cursor is the last event id, an opaque token; a reconnect resumes from it
 *   (`Last-Event-ID` or `after`). Never build or compare one.
 * - Every read is one bounded page. `stream.resync` (the client fell more than a page behind, its
 *   cursor is from another profile or server run, or it left the server's ring) tells every
 *   subscriber to refetch what it shows; they are told before the cursor moves past it.
 *   `stream.closed` reconnects after `reconnectAfterMs`. A poll page with `more` is continued a
 *   second later, never at once.
 * - An installation may park four long polls. Told `event_wait_limit`, the client keeps its cursor
 *   and reads without parking, every 2 seconds or more, and tries parking again after 30.
 * - Streaming falls back to long-poll when the platform can't stream, the server refuses
 *   (404 or 405), or the stream keeps failing.
 * - Redelivered events (after a reconnect) are dropped by id.
 */
import {call, PlaybackApiError, type V1Http} from './http.ts';
import {parseEvent, type ServerEvent} from './types.ts';

/** A raw SSE message (`readEventStream` in `sse.ts` produces these on web; Apple's XHR adapter too). */
export type RawStreamEvent = Readonly<{id?: string; event?: string; data: string; /** Local shared-stream invalidation, never a server cursor. */ resync?: boolean}>;

/**
 * Opens the SSE stream from `lastEventId`, calls `onEvent` for each message, and resolves when the
 * stream ends or rejects on failure (with `status` for an HTTP refusal).
 */
export type StreamOpener = (lastEventId: string | undefined, onEvent: (event: RawStreamEvent) => void, signal: AbortSignal) => Promise<void>;

export type EventsClientOptions = Readonly<{
  http: V1Http;
  /** Omit to use long-poll only (constrained clients). */
  openStream?: StreamOpener;
  cursor?: string;
  waitMs?: number;
  backoffMs?: readonly number[];
  /** Consecutive stream failures before switching to long-poll for this run. */
  streamFailuresBeforePoll?: number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
  /** Retry jitter source; injectable for deterministic tests. */
  random?: () => number;
  /** Clock for the unparked-read period; injectable for deterministic tests. */
  now?: () => number;
}>;

/** How long the feed reads without parking after the server refused a further parked poll. */
const UNPARKED_MS = 30_000;
/** The least time between reads that do not park, and between a page and its continuation. */
const UNPARKED_READ_FLOOR_MS = 2000, CONTINUATION_MS = 1000;
/** 512 validated 128-character IDs fit, including conservative indexing overhead. */
const RECENT_BYTES = 192 * 1024;
const recentBytes = (id: string): number => 96 + id.length * 2;

export type EventsStatus = Readonly<{mode: 'stream' | 'poll' | 'stopped'; connected: boolean; cursor?: string; failures: number}>;

type Listener = (event: ServerEvent) => void;

export class EventsClient {
  private readonly o: EventsClientOptions;
  /** Shared by imperative resync reads and the attached freshness hub. */
  readonly resyncReads: ResyncCoordinator;
  private listeners = new Map<string, Set<Listener>>();
  private anyListeners = new Set<Listener>();
  private resyncListeners = new Set<() => void>();
  private statusListeners = new Set<() => void>();
  private recent: string[] = [];
  private recentSet = new Set<string>();
  private recentSize = 0;
  private controller?: AbortController;
  private running = false;
  private status: EventsStatus;
  private streamFailures = 0;
  private setTimer: (fn: () => void, ms: number) => unknown;
  private clearTimer: (t: unknown) => void;
  private sleeping?: {timer: unknown; resolve: () => void};
  /** Until then (by `now`), polls read with `waitMs=0`: the installation's parked polls are taken. */
  private unparkedUntil = 0;

  constructor(options: EventsClientOptions) {
    this.o = options;
    this.resyncReads = new ResyncCoordinator(options);
    this.status = Object.freeze({mode: 'stopped', connected: false, cursor: options.cursor, failures: 0});
    this.setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
  }

  getStatus = (): EventsStatus => this.status;
  subscribeStatus = (fn: () => void) => { this.statusListeners.add(fn); return () => { this.statusListeners.delete(fn); }; };
  private setStatus(p: Partial<EventsStatus>) { this.status = Object.freeze({...this.status, ...p}); for (const l of [...this.statusListeners]) l(); }

  /** Listen for one type (`session.updated`) or a prefix ending in a dot (`group.`). */
  on(typeOrPrefix: string, listener: Listener): () => void {
    let set = this.listeners.get(typeOrPrefix);
    if (!set) { set = new Set(); this.listeners.set(typeOrPrefix, set); }
    set.add(listener);
    return () => { set!.delete(listener); };
  }

  /** Every event the feed delivers, whatever its type (the freshness hub reads them all). Stream control frames are not events. */
  onAny(listener: Listener): () => void { this.anyListeners.add(listener); return () => { this.anyListeners.delete(listener); }; }

  /** Refetch what you show: the server couldn't deliver everything since the cursor. */
  onResync(listener: () => void): () => void { this.resyncListeners.add(listener); return () => { this.resyncListeners.delete(listener); }; }

  /** Register current-state recovery; barriers install debt synchronously. */
  onResyncRead(read: (context: ResyncReadContext) => Promise<void>, options: {priority?: number; open?: boolean; admission?: 'external'} = {}): ResyncRegistration {
    return this.resyncReads.register(read, options);
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.streamFailures = 0;
    this.controller = new AbortController();
    void this.loop(this.controller.signal);
  }

  stop(): void {
    this.running = false;
    this.controller?.abort();
    if (this.sleeping) { this.clearTimer(this.sleeping.timer); this.sleeping.resolve(); this.sleeping = undefined; }
    this.setStatus({mode: 'stopped', connected: false});
  }

  /** Deliver one envelope (also used by tests and by platforms that receive events elsewhere). */
  deliver(event: ServerEvent): void {
    if (!event.id || event.id.length > 128) {
      // Alternate platform delivery has the same ID bound as the wire parser.
      return;
    }
    if (event.type === 'stream.resync') {
      // Every subscriber marks what it shows as stale before the cursor moves past the gap; a
      // throwing one must not keep the others from hearing it.
      this.resyncReads.invalidate();
      this.recent = []; this.recentSet.clear(); this.recentSize = 0;
      for (const listener of [...this.resyncListeners]) { try { listener(); } catch { /* the others still refetch */ } }
      this.setStatus({cursor: event.id});
      return;
    }
    if (this.recentSet.has(event.id)) return;
    const bytes = recentBytes(event.id);
    if (bytes <= RECENT_BYTES) { this.recent.push(event.id); this.recentSet.add(event.id); this.recentSize += bytes; }
    while (this.recent.length > 512 || this.recentSize > RECENT_BYTES) {
      const oldest = this.recent.shift()!;
      this.recentSet.delete(oldest); this.recentSize -= recentBytes(oldest);
    }
    // Dropping a remembered ID is ordinary bounded deduplication: if it is ever
    // replayed, normal event delivery invalidates its dependants. No global resync.
    this.setStatus({cursor: event.id});
    if (event.type.startsWith('stream.')) return;
    for (const l of [...this.anyListeners]) l(event);
    const exact = this.listeners.get(event.type);
    if (exact) for (const l of [...exact]) l(event);
    for (const [key, set] of this.listeners) {
      if (key.endsWith('.') && event.type.startsWith(key) && key !== event.type) for (const l of [...set]) l(event);
    }
  }

  private async sleep(ms: number, signal: AbortSignal): Promise<void> {
    let remaining = ms;
    do {
      if (signal.aborted) return;
      // Long Retry-After values must not overflow a platform timer into an immediate retry.
      const chunk = Math.min(remaining, 2_147_483_647);
      await new Promise<void>(resolve => {
        const timer = this.setTimer(() => { this.sleeping = undefined; resolve(); }, chunk);
        this.sleeping = {timer, resolve};
      });
      remaining -= chunk;
    } while (remaining > 0 && !signal.aborted);
  }

  private backoff(failures: number): number {
    const b = this.o.backoffMs ?? [1000, 2000, 5000, 10_000, 30_000];
    const base = Math.max(1, b[Math.min(failures - 1, b.length - 1)] ?? 30_000);
    const random = Math.max(0, Math.min(1, (this.o.random ?? Math.random)()));
    return Math.round(base * (0.75 + random * 0.5));
  }

  /**
   * A wait the server asked for is a floor, and every client was told the same one: each waits
   * up to a quarter longer, at random, so they do not all reconnect in the same instant.
   */
  private spread(floorMs: number): number {
    const random = Math.max(0, Math.min(1, (this.o.random ?? Math.random)()));
    return Math.round(floorMs * (1 + random * 0.25));
  }

  private async loop(signal: AbortSignal): Promise<void> {
    let failures = 0;
    const pollOnly = () => !this.o.openStream || this.streamFailures >= (this.o.streamFailuresBeforePoll ?? 3);
    while (this.running && !signal.aborted) {
      const streaming = !pollOnly();
      this.setStatus({mode: streaming ? 'stream' : 'poll'});
      try {
        const reconnectAfter = streaming ? await this.streamOnce(signal) : await this.pollOnce(signal);
        if (signal.aborted) break;
        failures = 0;
        this.setStatus({failures: 0});
        if (reconnectAfter) await this.sleep(this.spread(reconnectAfter), signal);
      } catch (e) {
        if (signal.aborted) break;
        const status = (e as {status?: unknown} | null)?.status;
        if (streaming) {
          // An overlapping reconnect retires the previous stream. It is a
          // transient conflict, so keep streaming and retry with the cursor.
          if (status === 404 || status === 405) this.streamFailures = Infinity;
          else if (status !== 409) this.streamFailures++;
        }
        failures++;
        this.setStatus({connected: false, failures});
        const directed = (e as {retryAfterMs?: unknown} | null)?.retryAfterMs;
        const minimum = typeof directed === 'number' && Number.isFinite(directed) && directed > 0 ? directed : 0;
        if (streaming && pollOnly() && !minimum) continue; // unsupported streams can fall back immediately
        await this.sleep(Math.max(this.backoff(failures), minimum ? this.spread(minimum) : 0), signal);
      }
    }
  }

  /** One SSE connection. Resolves with a delay before reconnecting (`stream.closed`), or 0. */
  private async streamOnce(signal: AbortSignal): Promise<number> {
    let reconnectAfter: number | undefined;
    const inner = new AbortController();
    const abort = () => inner.abort();
    signal.addEventListener('abort', abort, {once: true});
    try {
      await this.o.openStream!(this.status.cursor, raw => {
        if (signal.aborted || inner.signal.aborted) return;
        this.setStatus({connected: true});
        if (signal.aborted) return;
        if (raw.resync) {
          this.resyncReads.invalidate();
          for (const listener of [...this.resyncListeners]) {try {listener();} catch { /* Install every consumer’s debt. */ }}
          return;
        }
        let parsed: unknown;
        try { parsed = JSON.parse(raw.data); } catch { return; }
        const event = parseEvent(parsed);
        if (!event) return;
        if (event.type === 'stream.closed') {
          const after = (event.data as {reconnectAfterMs?: unknown} | undefined)?.reconnectAfterMs;
          reconnectAfter = typeof after === 'number' && Number.isFinite(after) && after >= 0 ? Math.max(1000, after) : 1000;
          inner.abort();
          return;
        }
        // A greeting (or malformed/control frame) proves only that headers arrived.
        // Require useful new data before forgiving repeated short-lived streams.
        if (!event.type.startsWith('stream.') && !this.recentSet.has(event.id)) this.streamFailures = 0;
        this.deliver(event);
      }, inner.signal);
    } catch (e) {
      if (!inner.signal.aborted || signal.aborted) throw e;
    } finally {
      signal.removeEventListener('abort', abort);
      if (!signal.aborted) this.setStatus({connected: false});
    }
    // A proxy/platform may close SSE without a stream.closed envelope. Treat it as a failure
    // so repeated quiet EOFs back off and eventually fall back instead of spinning.
    if (reconnectAfter === undefined && !signal.aborted) throw new Error('event_stream_closed');
    return reconnectAfter ?? 0;
  }

  /**
   * One long-poll round. Resolves with how long to wait before the next: nothing after a parked
   * poll, a second before continuing a page that stopped at the server's budget (`more`), and
   * at least 2 seconds between reads that do not park.
   */
  private async pollOnce(signal: AbortSignal): Promise<number> {
    const now = this.o.now ?? Date.now;
    const parked = now() >= this.unparkedUntil;
    const wait = parked ? Math.max(1000, Math.min(this.o.waitMs ?? 25000, 25000)) : 0;
    const cursor = this.status.cursor;
    let r: Awaited<ReturnType<typeof call>>;
    try {
      r = await call(this.o.http, {method: 'GET', path: routePath("get_events") + "?waitMs=" + (wait) + (cursor ? `&after=${encodeURIComponent(cursor)}` : ''), signal});
    } catch (e) {
      // Not a failure: the installation's parked polls are all taken (another tab, a widget). Keep
      // the cursor and read without parking until the server's wait and 30 seconds have passed.
      if (!signal.aborted && e instanceof PlaybackApiError && e.status === 429 && e.code === 'event_wait_limit') {
        const floor = Math.max(UNPARKED_READ_FLOOR_MS, e.retryAfterMs ?? 0);
        this.unparkedUntil = now() + Math.max(UNPARKED_MS, floor);
        return floor;
      }
      throw e;
    }
    if (signal.aborted) return 0;
    const b = r.body as {events?: unknown; nextAfter?: unknown; more?: unknown} | undefined;
    if (!b || typeof b !== 'object' || Array.isArray(b) || !Array.isArray(b.events) || b.events.length > 100 || typeof b.nextAfter !== 'string' || b.nextAfter.length > 128) throw new Error('invalid_event_page');
    this.setStatus({connected: true});
    for (const raw of b.events) { if (signal.aborted) return 0; const e = parseEvent(raw); if (e) this.deliver(e); }
    if (!signal.aborted && b.nextAfter) this.setStatus({cursor: b.nextAfter});
    if (b.more === true) return parked ? CONTINUATION_MS : UNPARKED_READ_FLOOR_MS;
    return parked ? 0 : UNPARKED_READ_FLOOR_MS;
  }
}
