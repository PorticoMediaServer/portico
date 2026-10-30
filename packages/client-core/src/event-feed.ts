/**
 * The device's one event feed on every platform: how its stream is opened, when it runs, and how
 * several windows of one browser share it.
 *
 * - `eventStreamOpener` turns "fetch this path with these headers" (the browser's streaming
 *   `fetch`, the apps' native reader, Vega's) into the opener `EventsClient` takes. It adds what
 *   every platform needs and none of them gives for free: the resume header, a refusal read as a
 *   status, and a watchdog. A proxy that holds the response back, or a connection that has died
 *   without closing, looks like a stream with nothing to say; the server sends a line every 25
 *   seconds, so silence for much longer than that is a failure and the client goes on to its
 *   next attempt (and, when streams keep failing, to the long poll the server kept).
 * - `runEventFeed` starts the feed while the app is in front (or playing) and stops it a little
 *   after it leaves. A stopped feed makes no requests; a restarted one resumes from the last
 *   event it saw, so nothing in between is lost.
 * - `sharedStreamOpener` lets the windows of one browser hold one stream between them. The
 *   server keeps one stream per installation and retires the old one when a new one opens, so
 *   two tabs that each opened their own would take it from each other for ever. One tab holds
 *   it and passes each event to the others.
 *
 * No DOM types and no React Native: the platform hands in the few things that differ.
 */
import {readEventStream} from './sse.ts';
import type {EventsClient, RawStreamEvent, StreamOpener} from './playback-v1/events.ts';
import type {FreshnessHub} from './freshness-hub.ts';

type Timers = Readonly<{setTimer?: (fn: () => void, ms: number) => unknown; clearTimer?: (timer: unknown) => void}>;
const setT = (t: Timers | undefined) => t?.setTimer ?? ((fn: () => void, ms: number) => setTimeout(fn, ms));
const clearT = (t: Timers | undefined) => t?.clearTimer ?? ((timer: unknown) => clearTimeout(timer as ReturnType<typeof setTimeout>));

export type EventStreamFetch = (headers: Readonly<Record<string, string>>, signal: AbortSignal) => Promise<Response>;

export type EventStreamOpenerOptions = Timers & Readonly<{
  /** The longest the first bytes may take once the response has begun. */
  firstByteMs?: number;
  /** The longest an open stream may say nothing at all (the server's heartbeat is 25 seconds). */
  quietMs?: number;
}>;

function failure(code: string, extra: Record<string, unknown> = {}): Error {
  return Object.assign(new Error(code), {code}, extra);
}

function retryAfterMs(response: Response): number | undefined {
  let header: string | null = null;
  try { header = response.headers?.get?.('Retry-After') ?? null; } catch { header = null; }
  const text = header?.trim();
  if (!text) return undefined;
  if (/^\d+$/.test(text)) return Number(text) * 1000;
  const at = Date.parse(text);
  return Number.isFinite(at) && at > Date.now() ? at - Date.now() : undefined;
}

/** An `EventsClient` stream opener over any way of fetching a streaming response. */
export function eventStreamOpener(fetchStream: EventStreamFetch, options: EventStreamOpenerOptions = {}): StreamOpener {
  const firstByteMs = options.firstByteMs ?? 10_000, quietMs = options.quietMs ?? 70_000;
  const setTimer = setT(options), clearTimer = clearT(options);
  return async (lastEventId, onEvent, signal) => {
    const inner = new AbortController();
    let problem: Error | undefined, timer: unknown;
    const stop = () => inner.abort();
    if (signal.aborted) return;
    signal.addEventListener('abort', stop, {once: true});
    const watch = (ms: number, code: string) => {
      if (timer !== undefined) clearTimer(timer);
      timer = setTimer(() => { timer = undefined; problem = failure(code); inner.abort(); }, ms);
    };
    try {
      watch(firstByteMs + quietMs, 'event_stream_unanswered');
      const response = await fetchStream({Accept: 'text/event-stream', ...(lastEventId ? {'Last-Event-ID': lastEventId} : {})}, inner.signal);
      if (!response.ok) {
        try { await (response.body as unknown as {cancel?: () => Promise<void>} | null)?.cancel?.(); } catch { /* nothing to release */ }
        throw failure('event_stream_refused', {status: response.status, retryAfterMs: retryAfterMs(response)});
      }
      if (!response.body) throw failure('event_stream_unsupported', {status: 405});
      watch(firstByteMs, 'event_stream_silent');
      // Every read that brings bytes (an event, or only the server's heartbeat) restarts the watchdog.
      const source = (response.body as unknown as {getReader(): {read(): Promise<{value?: Uint8Array | string; done: boolean}>; cancel(): Promise<void>; releaseLock(): void}}).getReader();
      const watched = {
        ok: true, status: response.status, body: {getReader: () => ({
          read: async () => { const piece = await source.read(); if (!piece.done) watch(quietMs, 'event_stream_silent'); return piece; },
          cancel: () => source.cancel(),
          releaseLock: () => source.releaseLock(),
        })},
      } as unknown as Response;
      await readEventStream(watched, event => onEvent({id: event.id || undefined, event: event.event, data: event.data}), inner.signal);
      if (problem) throw problem;
    } catch (error) {
      if (problem) throw problem;
      if (signal.aborted) return;
      throw error;
    } finally {
      if (timer !== undefined) clearTimer(timer);
      signal.removeEventListener('abort', stop);
      inner.abort();
    }
  };
}

export type EventFeedEnvironment = Timers & Readonly<{
  /** The app is in front of the person (a visible tab, an active app). */
  visible(): boolean;
  onVisibility(listener: () => void): () => void;
  /** Something that needs the feed while out of sight is going on (playback, a room, casting). */
  busy?(): boolean;
  onBusy?(listener: () => void): () => void;
  /** Opt-in runnable ownership (paused/parked/resumable work), independent of playing. */
  retain?(): boolean;
  onRetain?(listener: () => void): () => void;
  /** How long after leaving the foreground the feed is held before it is stopped. */
  graceMs?: number;
}>;

/**
 * Runs `events` while the app is in front or busy, tells `hub` whether the app is in front, and
 * returns the way to stop. The feed is the caller's: it is stopped here, never disposed.
 */
export function runEventFeed(events: EventsClient, hub: FreshnessHub | undefined, env: EventFeedEnvironment): () => void {
  const setTimer = setT(env), clearTimer = clearT(env), graceMs = env.graceMs ?? 30_000;
  let timer: unknown, stopped = false;
  const wanted = () => env.visible() || !!env.busy?.() || !!env.retain?.();
  let was: string | undefined;
  const follow = () => {
    if (stopped) return;
    // `onBusy` may fire with every tick of a player: only a change of state does anything.
    const state = `${env.visible()}:${wanted()}`;
    if (state === was) return;
    was = state;
    events.resyncReads.setActive(env.visible());
    hub?.setActive(env.visible());
    if (timer !== undefined) { clearTimer(timer); timer = undefined; }
    if (wanted()) { events.start(); return; }
    if (events.getStatus().mode === 'stopped') return;
    timer = setTimer(() => { timer = undefined; if (!stopped && !wanted()) events.stop(); }, graceMs);
  };
  const offVisibility = env.onVisibility(follow);
  const offBusy = env.onBusy?.(follow);
  const offRetain = env.onRetain?.(follow);
  follow();
  return () => {
    stopped = true;
    if (timer !== undefined) clearTimer(timer);
    offVisibility(); offBusy?.(); offRetain?.();
    events.stop();
  };
}

/** What `sharedStreamOpener` needs of the browser: a named exclusive lock and a channel between windows. */
export type SharedStreamEnvironment = Readonly<{
  /**
   * Runs `work` while holding the lock `name`; resolves when `work` does. Rejects if `signal` aborts
   * first. (Web Locks, or leases where a plain-HTTP page has none.) A lock that can be lost while
   * held (a lease another window took over) aborts `held`, and the holder stops.
   */
  lock(name: string, signal: AbortSignal, work: (held?: AbortSignal) => Promise<void>): Promise<void>;
  /** A channel every window of this browser on this origin can post to and hear. (BroadcastChannel.) */
  channel(name: string): {post(message: unknown): void; listen(listener: (message: unknown) => void): () => void; close(): void};
}>;

type Relay = {kind: 'event'; event: RawStreamEvent} | {kind: 'open'; cursor?: string} | {kind: 'lost'} | {kind: 'who'};
const relay = (message: unknown): Relay | undefined => {
  const m = message as {kind?: unknown; event?: {id?: unknown; event?: unknown; data?: unknown}; cursor?: unknown} | null;
  if (!m || typeof m !== 'object') return undefined;
  if (m.kind === 'open') return {kind: 'open', cursor: typeof m.cursor === 'string' && m.cursor.length <= 128 ? m.cursor : undefined};
  if (m.kind === 'lost' || m.kind === 'who') return {kind: m.kind};
  if (m.kind !== 'event' || !m.event || typeof m.event.data !== 'string' || m.event.data.length > 262144) return undefined;
  return {kind: 'event', event: {id: typeof m.event.id === 'string' ? m.event.id : undefined, event: typeof m.event.event === 'string' ? m.event.event : undefined, data: m.event.data}};
};

/**
 * One stream for every window of a browser signed in to the same server as the same viewer
 * (`name` says which). The window holding the lock opens the real stream with `open` and posts
 * what arrives; the others hear it on the channel as if it were their own stream. When the
 * holder closes, hides or loses the stream, the next window takes over from the last event it
 * heard, so the server replays anything in between.
 */
export function sharedStreamOpener(name: string, open: StreamOpener, env: SharedStreamEnvironment): StreamOpener {
  return async (lastEventId, onEvent, signal) => {
    if (signal.aborted) return;
    // Everything this call starts (its place in the queue for the lock, its stream) ends with it.
    const inner = new AbortController();
    const stop = () => inner.abort();
    signal.addEventListener('abort', stop, {once: true});
    const channel = env.channel(name);
    let acknowledged = false;
    let last = lastEventId, holding = false, failed: unknown, lost: (() => void) | undefined;
    // An empty frame is not an event; it tells the feed the stream is alive, as the first bytes of a real one would.
    const alive = () => { if (!inner.signal.aborted) onEvent({data: '{}'}); };
    const heard = (event: RawStreamEvent) => {
      if (inner.signal.aborted) return;
      if (event.id) last = event.id;
      onEvent(event);
    };
    const off = channel.listen(message => {
      const m = relay(message);
      if (!m) return;
      if (holding) { if (m.kind === 'who') channel.post({kind: 'open', cursor: last}); return; }
      if (m.kind === 'event') heard(m.event);
      else if (m.kind === 'open') {
        // The leader cannot replay what this tab missed while absent. Invalidate once, without
        // manufacturing a server cursor or refetching on every acknowledgement.
        if (!acknowledged && m.cursor !== lastEventId) onEvent({data: '{}', resync: true});
        acknowledged = true;
        alive();
      }
      else if (m.kind === 'lost') lost?.();
    });
    try {
      // While another window holds the stream, this one hears it. A holder that loses its stream
      // says so, and this call ends as a lost stream would: every window then knows the feed is down.
      const dropped = new Promise<void>(resolve => { lost = resolve; });
      channel.post({kind: 'who'});
      const held = env.lock(name, inner.signal, async lease => {
        if (inner.signal.aborted) return;
        holding = true;
        // The stream ends with this call, or when the lock is lost to another window.
        const stream = new AbortController();
        const end = () => stream.abort();
        const taken = () => { if (!inner.signal.aborted) failed = failure('event_stream_lock'); stream.abort(); };
        inner.signal.addEventListener('abort', end, {once: true});
        lease?.addEventListener('abort', taken, {once: true});
        try {
          if (lease?.aborted) taken();
          else {
            channel.post({kind: 'open', cursor: last});
            await open(last, event => { if (!stream.signal.aborted) { channel.post({kind: 'event', event}); heard(event); } }, stream.signal);
          }
        } catch (error) {
          failed ??= error ?? failure('event_stream_failed');
        } finally {
          inner.signal.removeEventListener('abort', end);
          lease?.removeEventListener('abort', taken);
          holding = false;
          channel.post({kind: 'lost'});
        }
      }).catch(error => { if (!inner.signal.aborted) failed = error ?? failure('event_stream_lock'); });
      await Promise.race([held, dropped]);
      if (holding) await held;
      else if (!failed && !signal.aborted) failed = failure('event_stream_closed');
      if (failed && !signal.aborted) throw failed;
    } finally {
      signal.removeEventListener('abort', stop);
      inner.abort();
      off();
      channel.close();
    }
  };
}
