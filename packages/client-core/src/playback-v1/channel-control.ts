import type {ResyncReadContext} from '../resync-coordinator.ts';
import {encodedRoutePath} from "../../../contracts/src/route-paths.ts";
/**
 * Live TV and Library Channels on a Playback v1 session (spec §11, §18.6; Plan — Client Playback
 * Migration §10, B6). The same surface as the /v2 `ChannelControl` (open, pause, resume, seek, go
 * live, retry, observe, close), so `PlaybackService` and the players don't change: each read of the
 * v1 session is presented as the channel projection the service already applies.
 *
 * - Start: `POST /v1/playback/sessions {channelId, channelGeneration, state}`. A start with every
 *   tuner in use is `409 no_tuner_available`, said as such.
 * - Join: once the stream exists, a go-live seek (`PATCH seek {live: true}`) that the server
 *   resolves to a position in the window: the engine attaches there.
 * - Controls: `PATCH` with the session's revision (`state`; `seek {positionMs}` or `{live: true}`);
 *   play on a failed source is the server's retry.
 * - Reports: the timeline (§6) is the lease and the observation. A report after the engine reached
 *   a seek acknowledges it (the server compares the position with the target). One report at least
 *   every 10 s playing and 30 s paused keeps the lease.
 * - Stop: `DELETE`. A new channel waits for the previous stop, so a one-tuner source isn't refused.
 */
import type {ChannelDesired, ChannelProjection, ChannelReference} from '../playback/linear-protocol.ts';
import {channelMessage} from '../playback/linear-protocol.ts';
import {PlaybackApiError, call, enc, idempotencyKey, type V1Http} from './http.ts';
import {serviceText} from '../presentation/service-text.ts';
import {SessionsClient, type StartContext, type StartPreparation, type Timers} from './sessions.ts';
import {ObservationReporter} from './observation.ts';

type ChannelEvents = Readonly<{onResync?(listener: () => void): () => void; onResyncRead?(read: (context: ResyncReadContext) => Promise<void>, options?: {priority?: number}): {dispose(): void}}>;
export type V1ChannelOptions = Readonly<{events?: ChannelEvents; http: V1Http; key?: () => string; now?: () => number; pollMs?: number; timers?: Timers}>;

/** What the service applies: the playback id, whether the start can't proceed, and the projection. */
export type ChannelView = Readonly<{playbackId: string; status?: string; linear: ChannelProjection}>;
export type ChannelViewSink = {state: (value: ChannelView) => void; error: (message: string, terminal: boolean) => void; preparation?: (value: StartPreparation | undefined) => void};
/** Facts of this exact original channel POST, captured before any start I/O.
 * This is not a session, cleanup authority or a permission to adopt a successor. */
export type OriginalChannelStart = Readonly<{
  body: Readonly<{channelId: string; channelGeneration: string; state: 'playing' | 'paused'}>;
  key: string; signal: AbortSignal; isCurrent(): boolean;
}>;
/** The host supplies purpose-specific never-installed resource custody. Its
 * predicate only narrows the original channel lifetime; finish releases that
 * host flight after settlement, without changing this start's result. */
export type ChannelStartReceipt = Readonly<{
  isCurrent(): boolean;
  capturePreparing: NonNullable<StartContext['capturePreparing']>;
  /** Synchronously claim this actual resource before internal installation or
   * a go-live PATCH. Only literal true permits installation; the host shares
   * its original registration with every current/in-flight channel start. */
  claimInstall(session: Readonly<{id: string; generation: number}>): boolean;
  /** Facts of a late actual 200/201, never a fabricated Session. The host
   * re-proves this original principal/id/generation and never-installed
   * registration before any bounded cleanup. No raw DELETE fallback. */
  retireUninstalled(session: Readonly<{id: string; generation: number}>): void | Promise<void>;
  finish(): void | Promise<void>;
}>;
export type ChannelStartOptions = Readonly<{state?:'playing'|'paused'; signal?: AbortSignal; isCurrent?: () => boolean; capturePreparing?: StartContext['capturePreparing']; captureStart?: (original: OriginalChannelStart) => ChannelStartReceipt}>;

/** The service's channel control, whichever protocol (the /v2 `ChannelControl` or this). */
export interface ChannelPlayback {
  open(channel: ChannelReference, sink: ChannelViewSink, options?:ChannelStartOptions): Promise<void>;
  recover(): Promise<void>;
  pause(): void;
  resume(): void;
  seek(seconds: number): void;
  goLive(): void;
  retry(): void;
  observe(seconds: number, seekId?: string | null, errorCode?: string | null): Promise<boolean>;
  close(): void;
}

/** Whether the server plays channels as v1 sessions (`features.playback_v1_channels`, spec §18.6). */
export function serverSupportsV1Channels(capabilities: unknown): boolean {
  const features = obj(capabilities) && obj(capabilities.features) ? capabilities.features : undefined;
  return features?.playback_v1_channels === 'enabled';
}

/** A v1 channelId: `live:<sourceId>:<channelId>` or `library:<channelId>` (spec §18.6). */
export function v1ChannelId(ref: ChannelReference): string {
  return ref.kind === 'live-source' ? `live:${ref.sourceId}:${ref.channelId}` : `library:${ref.channelId}`;
}

type Programme = Readonly<{id: string; itemId?: string; title: string; startMs: number; endMs: number}>;
type Linear = Readonly<{
  reservationRequiredAtMs?:number;nowMs?:number;name: string; programme?: Programme; next?: Programme; originMs: number; windowStartMs: number; windowEndMs: number; liveEdgeMs: number;
  state: string; errorCode?: string; seek?: Readonly<{id: string; live?: boolean; positionMs?: number; acknowledged: boolean; error?: string}>; confirmedPositionMs?: number;
  clocks?: readonly Readonly<{startMs: number; endMs: number; offsetMs: number}>[];
}>;
type Session = Readonly<{id: string; revision: string; state: string; presentation: Readonly<{generation: number; url: string; linear?: Linear}>; end?: Readonly<{reason: string; message?: string}>}>;

const obj = (v: unknown): v is Record<string, any> => !!v && typeof v === 'object' && !Array.isArray(v);
const int = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v);
const us = (ms: number) => String(Math.round(ms) * 1000);

function parseProgramme(v: unknown): Programme | undefined {
  if (v === undefined) return undefined;
  if (!obj(v) || typeof v.id !== 'string' || typeof v.title !== 'string' || !int(v.startMs) || !int(v.endMs)) throw new Error('Unreadable channel programme.');
  return v as Programme;
}

/**
 * Reads a v1 channel session (the fields this control uses), as it arrives. A tuner deadline comes
 * with the server's clock (`nowMs`); it is moved onto this device's clock here, at receipt, so
 * every player's countdown and the automatic stop run against server time whatever this device's
 * clock says (off only by the response's travel time).
 */
export function parseChannelSession(v: unknown, now: () => number = Date.now): Session {
  if (!obj(v) || typeof v.id !== 'string' || typeof v.revision !== 'string' || typeof v.state !== 'string' || !obj(v.presentation) || !int(v.presentation.generation) || typeof v.presentation.url !== 'string') throw new Error('Unreadable channel session.');
  const l = v.presentation.linear;
  if (l !== undefined) {
    if (!obj(l) || typeof l.name !== 'string' || !int(l.originMs) || !int(l.windowStartMs) || !int(l.windowEndMs) || !int(l.liveEdgeMs) || typeof l.state !== 'string') throw new Error('Unreadable channel clock.');
    if(l.reservationRequiredAtMs!==undefined&&(!int(l.reservationRequiredAtMs)||l.reservationRequiredAtMs<0))throw new Error("Unreadable tuner deadline.");
    parseProgramme(l.programme); parseProgramme(l.next);
    if (l.seek !== undefined && (!obj(l.seek) || typeof l.seek.id !== 'string' || typeof l.seek.acknowledged !== 'boolean' || l.seek.positionMs !== undefined && !int(l.seek.positionMs))) throw new Error('Unreadable channel seek.');
    if (l.confirmedPositionMs !== undefined && !int(l.confirmedPositionMs)) throw new Error('Unreadable channel position.');
    if (l.clocks !== undefined && !(Array.isArray(l.clocks) && l.clocks.length <= 64 && l.clocks.every((c: unknown) => obj(c) && int(c.startMs) && int(c.endMs) && int(c.offsetMs)))) throw new Error('Unreadable channel clock.');
    if (l.nowMs !== undefined && !int(l.nowMs)) throw new Error('Unreadable server clock.');
    if (l.reservationRequiredAtMs !== undefined && l.nowMs !== undefined) {
      const {nowMs, ...rest} = l;
      return {...v, presentation: {...v.presentation, linear: {...rest, reservationRequiredAtMs: Math.max(0, l.reservationRequiredAtMs - nowMs + Math.round(now()))}}} as Session;
    }
  }
  return v as Session;
}

/** The v1 session as the channel projection `PlaybackService.applyChannel` reads. */
export function channelView(s: Session, channel: ChannelReference): ChannelView {
  const l = s.presentation.linear;
  const generation = s.presentation.generation > 0 ? String(s.presentation.generation) : '';
  const programme = (p?: Programme) => (p ? Object.freeze({id: p.id, itemId: p.itemId ?? '', title: p.title, startMs: p.startMs, endMs: p.endMs}) : null);
  const seek = l?.seek && l.seek.positionMs !== undefined ? Object.freeze({seekCommandId: l.seek.id, bufferGeneration: generation, kind: 'position' as const, positionUs: us(l.seek.positionMs)}) : null;
  const desired: ChannelDesired = Object.freeze({state: s.state === 'paused' ? 'paused' : 'playing', seek, retryRequestId: null});
  const linear: ChannelProjection = Object.freeze({
    channel, name: l?.name ?? '', programme: programme(l?.programme), next: programme(l?.next), desired,
    media: Object.freeze({... (l?.reservationRequiredAtMs===undefined?{}:{reservationRequiredAtMs:l.reservationRequiredAtMs}),streamUrl: s.presentation.url, bufferGeneration: generation, presentationGeneration: generation, originMs: l?.originMs ?? 0, windowRevision: '1',
      windowStartUs: us(l?.windowStartMs ?? 0), windowEndUs: us(l?.windowEndMs ?? 0), liveEdgeUs: us(l?.liveEdgeMs ?? 0), gaps: [], state: l?.state ?? 'preparing', errorCode: l?.errorCode ?? '', logoUrl: '', ...(l?.clocks?.length ? {clocks: Object.freeze(l.clocks.map(c => Object.freeze({startMs: c.startMs, endMs: c.endMs, offsetMs: c.offsetMs})))} : {})}),
    overlay: {enabled: false, corner: 'top-right', sizePercent: 8, insetPercent: 3, treatment: 'original'},
    confirmedPositionUs: l?.confirmedPositionMs !== undefined ? us(l.confirmedPositionMs) : null,
    acknowledgedSeekCommandId: l?.seek?.acknowledged ? l.seek.id : null,
    seekError: l?.seek?.error ?? '',
  });
  return Object.freeze({playbackId: s.id, status: l?.state === 'recoverable' ? 'recoverable' : undefined, linear});
}

type Active = {wake?: () => void; serial: number; channel: ChannelReference; startAbort: AbortController; id?: string; session?: Session; joined: boolean; reporter?: ObservationReporter<Session>; tail: Promise<unknown>; closed: boolean};

const message = (e: unknown): string => {
  if (e instanceof PlaybackApiError) {
    if (e.code === 'no_tuner_available') return channelMessage('tuner_busy');
    if (e.code === 'not_found') return 'This channel isn’t available.';
    if (e.code === 'seek_unavailable') return channelMessage('window_expired');
    if (e.code === 'stream_limit_reached') return 'This account is already playing as many streams as it is allowed.';
    if (e.code === 'channel_unavailable') return 'This channel can’t be played right now. Try again in a moment.';
    if (e.code === 'session_ended') return 'This channel playback has ended. Select the channel again.';
  }
  return 'Channel playback could not connect. Check the server and retry.';
};
/** Why a channel session ended, in words: an administrator's message, a profile without Live TV, or the generic end. */
export const channelEndMessage = (end: Session['end']): string => {
  if (end?.reason === 'terminated' && end.message) return end.message;
  if (end?.reason === 'feature_restricted') return serviceText('live.offForProfile');
  return 'This channel playback has ended. Select the channel again.';
};
const terminal = (e: unknown) => e instanceof PlaybackApiError && [401, 403, 404, 410].includes(e.status) || e instanceof PlaybackApiError && e.code === 'no_tuner_available';

/** How long a started channel may take to have its stream, and how often to look. */
export const CHANNEL_STREAM_WAIT_MS = 60_000, CHANNEL_STREAM_POLL_MS = 500;
/** The plain, terminal words for a start the host refused and one whose stream never came. */
export const CHANNEL_START_REFUSED = 'This channel could not start. Try again.', CHANNEL_START_TIMEOUT = 'This channel did not start in time. Try again.';

export class V1ChannelControl implements ChannelPlayback {
  private readonly http: V1Http;
  private readonly sessions: SessionsClient;
  private readonly key: () => string;
  private readonly now: () => number;
  private readonly timers?: Timers;
  private readonly pollMs?: number;
  private readonly events?: ChannelEvents;
  private offResync?: () => void;
  private serial = 0;
  private active?: Active;
  private sink?: ChannelViewSink;
  private wanted: 'playing' | 'paused' = 'playing';
  private stopping: Promise<unknown> = Promise.resolve();

  constructor(options: V1ChannelOptions) {
    this.http = options.http;
    this.sessions = new SessionsClient(options.http, options.timers);
    this.key = options.key ?? (() => idempotencyKey());
    this.now = options.now ?? (() => Date.now());
    this.timers = options.timers;
    this.pollMs = options.pollMs;
    this.events = options.events;
  }

  /** Nothing to recover: a v1 session a crash left behind lapses with its lease. */
  recover(): Promise<void> { return Promise.resolve(); }

  private current(a: Active) { return this.active === a && !a.closed; }
  private enqueue<T>(a: Active, work: () => Promise<T>): Promise<T> {
    const result = a.tail.then(work);
    a.tail = result.catch(() => {});
    return result;
  }

  async open(channel: ChannelReference, sink: ChannelViewSink, options:ChannelStartOptions={}): Promise<void> {
    const entered = this.serial;
    const state=options.state??'playing';
    if(state!=='playing'&&state!=='paused')throw new Error('invalid_channel_start_state');
    const signal = options.signal, originalCurrent = options.isCurrent, capturePreparing = options.capturePreparing, captureStart = options.captureStart;
    const originalChannel: ChannelReference = Object.freeze({...channel});
    const starting = () => {try {return !signal?.aborted && (originalCurrent ? originalCurrent() === true : true);} catch {return false;}};
    if (!starting() || entered !== this.serial) throw Object.assign(new Error('cancelled'), {name: 'AbortError'});
    this.close();
    if (!starting() || this.serial !== entered + 1) return;
    this.sink = sink;
    this.wanted = state;
    const a: Active = {serial: ++this.serial, channel: originalChannel, startAbort: new AbortController(), joined: false, tail: Promise.resolve(), closed: false};
    this.active = a;
    const currentStart = () => this.current(a) && starting() && this.current(a);
    let admitted = currentStart, finish: ChannelStartReceipt['finish'] | undefined;
    let claimInstall: ChannelStartReceipt['claimInstall'] | undefined;
    let retireUninstalled: ChannelStartReceipt['retireUninstalled'] | undefined, retiring: Promise<void> | undefined;
    const retireLate = (session: Readonly<{id: string; generation: number}>) => {
      if (!captureStart) {void this.stop(session.id, session.generation).catch(error => this.sink?.error(message(error), true)); return;}
      if (retireUninstalled) {try {retiring = Promise.resolve(retireUninstalled(Object.freeze({id: session.id, generation: session.generation}))).catch(() => {});} catch { /* Original host retains cleanup uncertainty; never downgrade it. */ }}
    };
    const cancel = () => {if (this.current(a)) this.close();};
    signal?.addEventListener('abort', cancel, {once: true});
    try {
      const stopping = this.stopping;
      const body = Object.freeze({channelId: v1ChannelId(originalChannel), channelGeneration: originalChannel.generation, state});
      const key = this.key();
      if (typeof key !== 'string') throw new Error('invalid_channel_start_key');
      if (!currentStart()) {cancel();return;}
      const captured = captureStart?.(Object.freeze({body, key, signal: a.startAbort.signal, isCurrent: currentStart}));
      finish = captured?.finish;
      retireUninstalled = captured?.retireUninstalled;
      claimInstall = captured?.claimInstall;
      const hostCurrent = captured?.isCurrent, originalPreparing = captured?.capturePreparing;
      if (captureStart && (typeof finish !== 'function' || typeof hostCurrent !== 'function' || typeof originalPreparing !== 'function' || typeof retireUninstalled !== 'function' || typeof claimInstall !== 'function')) throw new Error('invalid_channel_start_receipt');
      admitted = () => {try {return currentStart() && (!hostCurrent || hostCurrent() === true) && currentStart();} catch {return false;}};
      await stopping.catch(() => {});
      if (!admitted()) {cancel();return;}
      const result = await this.sessions.startResponse({channelId: body.channelId, channelGeneration: body.channelGeneration}, {state: body.state}, key, {
        signal: a.startAbort.signal, isCurrent: admitted, capturePreparing: captureStart ? originalPreparing : capturePreparing,
        onPreparing: (_wait, preparation) => {if (admitted()) sink.preparation?.(preparation);},
      }, body => parseChannelSession(body, this.now));
      if (!admitted()) {retireLate(result);cancel();return;}
      const s = result.body;
      sink.preparation?.(undefined);
      if (!admitted()) {retireLate(result);cancel();return;}
      let install = !captureStart;
      try {if (claimInstall) install = claimInstall(Object.freeze({id: s.id, generation: s.presentation.generation})) === true;} catch { /* A host refusal cannot install or downgrade its cleanup proof. */ }
      if (!install || !admitted()) {
        const refused = !install && admitted();
        retireLate(result);cancel();
        // An admitted start the host refused to install is a failure, never a silent no-op
        // (P2-74 E-web-25): the caller draws its error instead of waiting forever.
        if (refused) sink.error(CHANNEL_START_REFUSED, true);
        return;
      }
      a.id = s.id;
      a.reporter = this.reporterFor(a, s.id);
      this.accept(a, s);
      if (this.current(a)) {
        const recover = async (signal?: AbortSignal) => {
          try {await this.read(a, signal, true);}
          catch (error) {
            if (!this.current(a) || signal?.aborted) return;
            if (terminal(error)) {a.reporter?.stop(); this.offResync?.(); this.offResync = undefined; this.active = undefined; sink.error(message(error), true);}
            else throw error;
          }
        };
        const recovery = this.events?.onResyncRead?.(({signal}) => recover(signal), {priority: 0});
        this.offResync = recovery ? () => recovery.dispose() : this.events?.onResync?.(() => {void recover().catch(error => {if (this.current(a)) sink.error(message(error), false);});});
        // A start answered before the channel's stream exists (generation 0, no URL) has no
        // reading or observation that may create its first join. Follow this start's own
        // session until the server has the stream, then join (P2-74 E-web-25); never wait
        // silently forever.
        const deadline = this.now() + CHANNEL_STREAM_WAIT_MS;
        // One pending wait at a time, cleared at once when the channel closes (Stop, a newer start).
        // Real time, like the channel's own clock; it never holds a process open (unref where the
        // platform has it) and a closed channel clears it at once.
        const wait = (ms: number) => new Promise<void>(resolve => {
          const timer = setTimeout(() => {a.wake = undefined; resolve();}, ms);
          (timer as {unref?: () => void}).unref?.();
          a.wake = () => {clearTimeout(timer); a.wake = undefined; resolve();};
        });
        while (currentStart() && !a.joined) {
          const ready = a.session && a.session.presentation.url && a.session.presentation.generation > 0;
          if (ready) {this.accept(a, a.session!); break;}
          if (this.now() >= deadline) {
            // The hard bound: end this start (Stop frees the tuner) and say so, terminally.
            this.close(); sink.error(CHANNEL_START_TIMEOUT, true); return;
          }
          await wait(CHANNEL_STREAM_POLL_MS);
          if (!currentStart()) break;
          // The read ends with this channel (Stop, close) as well as with the caller.
          await this.read(a, signal ? AbortSignal.any([signal, a.startAbort.signal]) : a.startAbort.signal, false);
        }
      }
    } catch (e) {
      if (!admitted()) {cancel();return;}
      if (this.current(a)) {
        sink.preparation?.(undefined);
        if (admitted()) {this.active = undefined; a.closed = true; a.startAbort.abort(); sink.error(message(e), true);}
      }
    } finally {
      signal?.removeEventListener('abort', cancel);
      const settle = () => {if (typeof finish === 'function') {try {void Promise.resolve(finish()).catch(() => {});} catch { /* Host settlement cannot retarget an accepted channel or replace its result. */ }}};
      if (retiring) void retiring.then(settle); else settle();
    }
  }

  private accept(a: Active, s: Session, observational = false) {
    if (!this.current(a)) return;
    if (s.state === 'ended' || s.end) {
      a.reporter?.stop(); this.offResync?.(); this.offResync = undefined;
      this.active = undefined;
      this.sink?.error(channelEndMessage(s.end), true);
      return;
    }
    a.session = s;
    // Catch-up cannot create the initial join/seek. The original start and its
    // own renderer observations retain that admission.
    if (observational && !a.joined) return;
    // The first join is a go-live seek the server resolves to a position in the window.
    if (!a.joined && s.presentation.url && s.presentation.generation > 0) {
      a.joined = true;
      void this.patch(a, {state: this.wanted, seek: {id: this.key(), positionMs: 0, live: true}});
      return;
    }
    // The engine's readings are the observations; until it has one, the confirmed position keeps
    // the samples (and so the lease and this view) coming once a second.
    if (a.reporter && a.reporter.snapshot().generation !== s.presentation.generation) {
      a.reporter.setGeneration(s.presentation.generation);
      a.reporter.observe({generation: s.presentation.generation, state: this.wanted, positionMs: s.presentation.linear?.confirmedPositionMs ?? 0, rate: this.wanted === 'playing' ? 1 : 0}, 'state');
    }
    this.sink?.state(channelView(s, a.channel));
  }

  private async read(a: Active, signal?: AbortSignal, observational = false) {
    if (!a.id || !this.current(a) || signal?.aborted) return;
    const r = await call(this.http, {method: 'GET', path: encodedRoutePath("get_playback_session", {"id": enc(a.id)}), signal});
    if (!signal?.aborted) this.accept(a, parseChannelSession(r.body), observational);
  }

  /** One sample a second carries the lease, and its ack the current channel view when it
   * changed: no second request a second. A failed sample is replaced by the next one. */
  private reporterFor(a: Active, id: string): ObservationReporter<Session> {
    return new ObservationReporter<Session>({
      claim: body => this.sessions.claimObserver(id, body),
      send: sample => this.sessions.observe(id, sample, undefined, body => parseChannelSession(body, this.now)),
      onView: session => { if (this.current(a)) this.accept(a, session); },
      onEnded: () => { if (this.current(a)) void this.read(a).catch(e => { if (this.current(a)) { this.sink?.error(message(e), true); this.active = undefined; } }); },
      onConnection: healthy => { if (!healthy && this.current(a)) this.sink?.error(message(undefined), false); },
      // Samples run on real time, like the channel's own clock (the injected timers pace start
      // preparation only); pollMs fixes their cadence for hosts and tests that need it.
      ...(this.pollMs !== undefined ? {everyMs: this.pollMs} : {}),
    });
  }

  private async patch(a: Active, body: Record<string, unknown>) {
    return this.enqueue(a, async () => {
      if (!a.id || !a.session || !this.current(a)) return;
      try {
        const r = await call(this.http, {method: 'PATCH', path: encodedRoutePath("change_playback_session", {"id": enc(a.id)}), headers: {'If-Match': `"${a.session.revision}"`}, body});
        this.accept(a, parseChannelSession(r.body));
      } catch (e) {
        if (!this.current(a)) return;
        if (e instanceof PlaybackApiError && e.status === 412) {
          // Another change landed first: take the current session and apply ours on it.
          const current = e.current ? parseChannelSession(e.current) : undefined;
          if (current) {
            a.session = current;
            const r = await call(this.http, {method: 'PATCH', path: encodedRoutePath("change_playback_session", {"id": enc(a.id)}), headers: {'If-Match': `"${current.revision}"`}, body});
            this.accept(a, parseChannelSession(r.body));
            return;
          }
        }
        try { await this.read(a); } catch { /* the next tick reads again */ }
        this.sink?.error(message(e), terminal(e));
      }
    });
  }

  pause() { this.wanted = 'paused'; const a = this.active; if (a) void this.patch(a, {state: 'paused'}); }
  resume() { this.wanted = 'playing'; const a = this.active; if (a) void this.patch(a, {state: 'playing'}); }
  seek(seconds: number) {
    const a = this.active;
    if (!a || !Number.isFinite(seconds)) return;
    void this.patch(a, {state: this.wanted, seek: {id: this.key(), positionMs: Math.max(0, Math.round(seconds * 1000))}});
  }
  goLive() {
    this.wanted = 'playing';
    const a = this.active;
    if (a) void this.patch(a, {state: 'playing', seek: {id: this.key(), positionMs: 0, live: true}});
  }
  /** Play on a failed source is the server's retry. */
  retry() { const a = this.active; if (a) void this.patch(a, {state: 'playing'}); }

  /** Where the engine is (a seek it reached, or ordinary progress). The latest wins: the next
   * sample (within a second) carries it, and a reached seek is sent at once. */
  async observe(seconds: number, seekId: string | null = null, _errorCode: string | null = null): Promise<boolean> {
    const a = this.active, s = a?.session;
    if (!a?.id || !s || !a.reporter || !this.current(a) || !Number.isFinite(seconds)) return false;
    if (seekId && s.presentation.linear?.seek?.id !== seekId) return false;
    a.reporter.observe({generation: s.presentation.generation, state: this.wanted, positionMs: Math.max(0, Math.round(seconds * 1000)), rate: this.wanted === 'playing' ? 1 : 0}, seekId ? 'seeked' : 'time');
    if (seekId) await a.reporter.flush();
    return this.current(a);
  }

  private stop(id: string, generation?: number): Promise<unknown> {
    const send = (g?: number) => call(this.http, {method: 'DELETE', path: encodedRoutePath("stop_playback_session", {"id": enc(id)}), body: g === undefined ? {} : {generation: g}}, [200, 204]);
    // A generation the stream moved past since the last reading (412) still ends this session.
    const work = send(generation).then(async response => {
      if (generation !== undefined) await this.http.acknowledgeUserMutation?.(`stop:${id}:${generation}`);
      return response;
    }, error => {
      if (generation !== undefined && error instanceof PlaybackApiError && error.status === 412) return send(undefined);
      throw error;
    });
    this.stopping = Promise.all([this.stopping.catch(() => {}), work]);
    // The barrier tracks completion even when there is no later open to await
    // it. The original work promise still reports refusal to its caller.
    void this.stopping.catch(() => {});
    return work;
  }

  close() {
    this.offResync?.(); this.offResync = undefined;
    ++this.serial;
    const a = this.active, sink = this.sink;
    this.active = undefined;
    if (!a) return;
    a.closed = true;
    a.wake?.();
    a.startAbort.abort();
    sink?.preparation?.(undefined);
    a.reporter?.stop();
    // Stop frees the tuner now, including a slow start still at stream generation 0: the
    // server refuses generation 0, so an unseen generation ends the session by id alone.
    const generation = a.session?.presentation.generation;
    if (a.id) void this.stop(a.id, generation !== undefined && generation > 0 ? generation : undefined).catch(error => sink?.error(message(error), true));
  }
}
