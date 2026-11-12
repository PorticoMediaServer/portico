import {PlaybackApiError, call, idempotencyKey, type V1Http} from './http.ts';
import {playbackPath} from './routes.ts';
/**
 * Playback sessions and the controller that plays one on a `MediaPlayer`.
 *
 * `SessionsClient` is the wire. A start sends an `Idempotency-Key`: the server answers 201 with
 * the session, 200 for a repeat of the same key, or 202 while the stream gets ready. A 202 already
 * is the session (it has its id); the same request is sent again after `retryAfterMs` until the
 * stream is ready. A change (`PATCH`) names only what changes. Progress is the latest position.
 * Stop is safe to repeat. A session lives in the server's memory: after a server restart it is
 * unknown (404), and the controller starts the title again where it was.
 *
 * `PlaybackSessionController` is what a platform wires: it owns the presentation gate, the
 * progress reporter and the player port, serialises changes, and ends cleanly when the server
 * ends the session (stopped from another device, removed access, an administrator). A session
 * another request made is handed to it with `attach`; a session the server no longer knows is
 * given back to the owner (`onLeaseExpired`) so the title starts again where it was instead of
 * showing an error.
 */
import {GenerationGate} from './generation.ts';
import type {MediaPlayer, PlayerEvent, PlayerObservation} from './port.ts';
import type {QualityRequest} from './quality.ts';
import type {PlayerFailureCode} from '../client-profile.ts';
import {ProgressReporter, type ProgressAnswer, type ProgressReport, type ProgressReporterOptions} from './progress.ts';
import {ContractError, obj, parseSession, parseSessionEnd, type DiscTitleRef, type Session, type SessionEnd} from './types.ts';
import {requireSessionChangeCurrent, sessionChangeCurrent, type SessionChangeContext} from './change-context.ts';
export type {SessionChangeContext} from './change-context.ts';

export type StartTarget =
  | Readonly<{itemId: string}>
  | Readonly<{channel: string}>
  | Readonly<{queue: Readonly<{queueId: string; entryId: string}>}>
  | Readonly<{transferId: string}>;

export type StartOptions = Readonly<{
  versionId?: string;
  /** One title of a disc. The server cannot play discs yet and refuses such a start by name. */
  discTitle?: DiscTitleRef;
  startPositionMs?: number;
  startFrom?: 'resume' | 'beginning' | 'live' | 'startOver';
  state?: 'playing' | 'paused';
  /** A quality the viewer chose for this playback. A start that names none gets the quality saved
   * in Settings on this device, which the server reads itself. */
  quality?: QualityRequest;
  audio?: Readonly<{trackId: string}>;
  subtitles?: Readonly<{trackId: string | null; delivery?: 'auto' | 'sidecar' | 'embeddedClient' | 'burn'}>;
  /** `cellular` when the app knows it is on mobile data; the server tells home from away. */
  network?: 'local' | 'remote' | 'cellular';
  /** The client's own estimate of what its connection to this server carries. With Automatic the
   * start lands on the widest quality that fits; with a fixed quality a start the connection
   * plainly cannot carry is refused (`connection_too_slow`). */
  throughputKbps?: number;
}>;

export type SessionChange = Readonly<{
  /** Plan again from this device's capability document, keeping this playback's choices. */
  replan?: boolean;
  state?: 'playing' | 'paused';
  versionId?: string;
  audio?: Readonly<{trackId: string}>;
  subtitles?: Readonly<{trackId: string | null; delivery?: 'auto' | 'sidecar' | 'embeddedClient' | 'burn'}>;
  quality?: QualityRequest;
  seek?: Readonly<{positionMs: number; id: string}>;
  subtitleOffsetMs?: number;
  /** Where the player is now: a change that starts a new presentation starts it there. */
  positionMs?: number;
  /** The client's current connection estimate; checked against a quality asked for in the same change. */
  throughputKbps?: number;
  /** The player could not play the current presentation. The server remembers that for this file
   * on this device and moves the session to the next way of playing it (`unsupported_media`: there is none). */
  failure?: Readonly<{code: PlayerFailureCode; detail?: string}>;
}>;

/** Whether a change makes the server plan the presentation again (so the player's position should travel with it). */
const replans = (c: SessionChange) => c.replan === true || c.versionId !== undefined || c.audio !== undefined || c.quality !== undefined || c.subtitles !== undefined || c.failure !== undefined;

export type Timers = Readonly<{now?: () => number; setTimer?: (fn: () => void, ms: number) => unknown; clearTimer?: (t: unknown) => void}>;

/** The stream is getting ready: players show the catalogue key while the same start is sent again. */
export type StartPreparation = Readonly<{messageId: 'player.preparing.slowStart'; retryAfterMs: number}>;

/** The session a 202 named while its stream got ready. It exists on the server from that answer on. */
export type PreparingSessionView = Session;
export type StartContext = Readonly<{
  signal?: AbortSignal;
  isCurrent?: () => boolean;
  onPreparing?: (retryAfterMs: number, preparation: StartPreparation) => void;
  /** Called once with the session a 202 named. What it returns is run if the start is then
   * abandoned, so a session nobody will play is stopped rather than left to run out. */
  capturePreparing?: (session: PreparingSessionView) => (() => void | Promise<void>) | undefined;
}>;
const startRetired = () => Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
const freezeWire = <T,>(value: T): T => {
  if (value !== null && typeof value === 'object') {
    for (const child of Object.values(value)) freezeWire(child);
    Object.freeze(value);
  }
  return value;
};

/** A 202's wait and the session it names. */
export function parseStartPreparation(value: unknown): Readonly<{retryAfterMs: number; session?: PreparingSessionView}> {
  const wait = obj(value) ? value.retryAfterMs : undefined;
  const retryAfterMs = typeof wait === 'number' && Number.isFinite(wait) ? Math.min(10_000, Math.max(250, Math.round(wait))) : 1000;
  let session: Session | undefined;
  try { session = parseSession(value); } catch { session = undefined; }
  return Object.freeze({retryAfterMs, ...(session ? {session} : {})});
}

export class SessionsClient {
  private http: V1Http;
  private setTimer: (fn: () => void, ms: number) => unknown;
  private clearTimer: (timer: unknown) => void;
  constructor(http: V1Http, timers: Timers = {}) {
    this.http = http;
    this.setTimer = timers.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = timers.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>));
  }

  /** Start, or send the same start again. A 202 waits and sends it again, with the same key and
   * body, until the stream is ready, the server refuses, or the caller moves on. */
  async start(target: StartTarget, options: StartOptions, key: string, extra: StartContext = {}): Promise<Session> {
    return this.startOriginal(target, options, key, extra, parseSession);
  }

  /** `start` with the caller's own reading of the answer (a channel's view beside the session). */
  /** `identify` reads the answer's id and generation for a session the title parser does not take (a live channel's). */
  async startResponse<T>(target: StartTarget, options: StartOptions, key: string, extra: StartContext, decode: (body: unknown) => T,
    identify?: (body: unknown) => Readonly<{id: string; generation: number}>): Promise<Readonly<{id: string; generation: number; body: T}>> {
    return this.startOriginal(target, options, key, extra, body => {
      const session = identify ? identify(body) : (({id, presentation}) => ({id, generation: presentation.generation}))(parseSession(body));
      return {id: session.id, generation: session.generation, body: decode(body)};
    });
  }

  private async startOriginal<T>(target: StartTarget, options: StartOptions, key: string, extra: StartContext, read: (body: unknown) => T): Promise<T> {
    const http = this.http, signal = extra.signal, isCurrent = extra.isCurrent;
    const onPreparing = extra.onPreparing, capturePreparing = extra.capturePreparing;
    const setTimer = this.setTimer, clearTimer = this.clearTimer;
    if (typeof key !== 'string') throw new ContractError('start key');
    let body: Record<string, unknown>;
    try {body = JSON.parse(JSON.stringify({...target, ...options}));} catch {throw new ContractError('start request');}
    const request = Object.freeze({method: 'POST' as const, path: playbackPath('POST /v1/playback/sessions'),
      headers: Object.freeze({'Idempotency-Key': key}), body: Object.freeze(body), signal});
    const current = () => {try {return (isCurrent ? isCurrent() === true : true) && !signal?.aborted;} catch {return false;}};
    const check = () => {if (!current()) throw startRetired();};
    let prepared: Session | undefined;
    let abandon: (() => void | Promise<void>) | undefined;
    try {
      for (;;) {
        check();
        const r = await call(http, request, [201, 200, 202]);
        if (r.status !== 202) {
          const result = read(r.body);
          if (prepared && parseSession(r.body).id !== prepared.id) throw new ContractError('preparation session changed');
          return result;
        }
        const preparing = parseStartPreparation(r.body);
        if (preparing.session && !prepared) {
          prepared = preparing.session;
          abandon = capturePreparing?.(prepared);
        }
        check();
        const wait = preparing.retryAfterMs;
        onPreparing?.(wait, Object.freeze({messageId: 'player.preparing.slowStart', retryAfterMs: wait}));
        check();
        await new Promise<void>((resolve, reject) => {
          let done = false, timer: unknown, assigned = false;
          const finish = (error?: unknown) => {
            if (done) return;
            done = true;
            signal?.removeEventListener('abort', aborted);
            if (assigned) {try {clearTimer(timer);} catch { /* Listener and promise still retire. */ }}
            if (error === undefined) resolve(); else reject(error);
          };
          const aborted = () => finish(startRetired());
          signal?.addEventListener('abort', aborted, {once: true});
          if (!current()) {aborted(); return;}
          try {
            timer = setTimer(() => finish(), wait); assigned = true;
            if (done) {try {clearTimer(timer);} catch { /* A supplied timer may settle synchronously. */ }}
          } catch (error) {finish(error);}
        });
      }
    } catch (error) {
      // A session the server made while its stream got ready is stopped when nobody will play it.
      if (prepared) {
        if (abandon) {try {void Promise.resolve(abandon()).catch(() => {});} catch { /* Keep the start's own failure. */ }}
        else void this.stop(prepared.id).catch(() => {});
      }
      throw error;
    }
  }

  async get(id: string, signal?: AbortSignal): Promise<Session> {
    return parseSession((await call(this.http, {method: 'GET', path: playbackPath('GET /v1/playback/sessions/{id}', {id}), signal})).body);
  }

  /** A change to a running session. Only what is sent changes. */
  async patch(id: string, change: SessionChange, signal?: AbortSignal): Promise<Session> {
    return parseSession((await call(this.http, {method: 'PATCH', path: playbackPath('PATCH /v1/playback/sessions/{id}', {id}), body: change, signal})).body);
  }

  /** Stop, with the final position. A session the server no longer knows (it restarted, or already
   * ended it) is stopped. So is one whose stop got no answer: the outbox keeps it, and the final
   * position is sent when the server is back. */
  async stop(id: string, positionMs?: number, signal?: AbortSignal): Promise<void> {
    const request = {method: 'DELETE', path: playbackPath('DELETE /v1/playback/sessions/{id}', {id}), ...(signal ? {signal} : {}),
      ...(positionMs !== undefined ? {body: {positionMs: Math.max(0, Math.round(positionMs))}} : {})} as const;
    await call(this.http, request, [204, 200]).catch(e => {
      const gone = e instanceof PlaybackApiError && (e.status === 404 || e.status === 410);
      if (!gone && (e as {code?: unknown} | null)?.code !== 'sync_pending') throw e;
    });
  }

  /** One progress report: a single attempt with a short deadline. A failed one is replaced by the next. */
  async progress(id: string, report: ProgressReport, signal?: AbortSignal): Promise<ProgressAnswer> {
    const r = await call(this.http, {method: 'PUT', path: playbackPath('PUT /v1/playback/sessions/{id}/progress', {id}), body: report, deadlineMs: 5000, singleAttempt: true, ...(signal ? {signal} : {})}, [200]);
    const v = obj(r.body) ? r.body : {};
    return Object.freeze({...(typeof v.expiresAt === 'string' ? {expiresAt: v.expiresAt} : {}), ...(typeof v.reportEveryMs === 'number' ? {reportEveryMs: v.reportEveryMs} : {}),
      ...(typeof v.generation === 'number' ? {generation: v.generation} : {})});
  }
}

export type ControllerPhase = 'idle' | 'starting' | 'preparing' | 'active' | 'ended' | 'error';

export type ControllerSnapshot = Readonly<{
  phase: ControllerPhase;
  /** Present only after a genuine 202; no fabricated session is installed. */
  preparation?: StartPreparation;
  session?: Session;
  /** The last reading of the current presentation (position, state). */
  observation?: PlayerObservation;
  error?: PlaybackApiError | Error;
  /** Why the server ended the session (phase "ended"): "terminated" by an administrator with
   * their message, "access_removed"… Absent when this device stopped it. Show it with
   * `sessionEndMessage`. */
  ended?: SessionEnd;
  /** A change is being sent. */
  changing: boolean;
}>;

export type ControllerOptions = Readonly<{
  http: V1Http;
  player: MediaPlayer;
  timers?: Timers;
  key?: () => string;
  reporter?: Partial<Pick<ProgressReporterOptions, 'onConnection'>>;
  /** Called when the server ended the session (transfer committed, admin terminate, stop elsewhere). */
  onServerEnded?: (session: Session, end: SessionEnd) => void;
  /**
   * The server no longer knows the session (it restarted, or the session ran out while this
   * device could not report: a sleeping laptop, a long outage). The owner starts the title again
   * at `positionMs` (a queue restarts its entry). Without it the controller restarts a plain item
   * start by itself; anything else ends.
   */
  onLeaseExpired?: (session: Session, positionMs: number) => void;
  /**
   * Whether a seek in this session goes through the server. Default `seekNeedsServer`. A platform
   * whose streams publish the whole timeline (the web: a seek is a request for a later segment and
   * the server follows) answers false.
   */
  serverSeek?: (session: Session) => boolean;
  /** The viewer's present intent, asked when a presentation loads (a change made while paused
   * stays paused). Default: the server's session state. */
  wantsPlaying?: () => boolean;
}>;

/**
 * Whether a seek must go through the server: only when something is converted or burned in, since
 * the server must start the conversion again there. Direct play and copied streams seek locally.
 */
export function seekNeedsServer(session: Session): boolean {
  const d = session.presentation.decision;
  return [d.video, d.audio, d.subtitles].some(x => x?.action === 'transcode' || x?.action === 'burn');
}

const LOST_SESSION_RESTART_MS = 5000;

export class PlaybackSessionController {
  private readonly client: SessionsClient;
  private readonly player: MediaPlayer;
  private readonly gate = new GenerationGate();
  private readonly key: () => string;
  private readonly timers: Timers;
  private readonly reporterOptions: ControllerOptions['reporter'];
  private readonly onServerEnded?: (session: Session, end: SessionEnd) => void;
  private readonly onLeaseExpired?: (session: Session, positionMs: number) => void;
  private readonly serverSeek: (session: Session) => boolean;
  private readonly wantsPlaying?: () => boolean;
  private reporter?: ProgressReporter;
  private state: ControllerSnapshot = Object.freeze({phase: 'idle', changing: false});
  private listeners = new Set<() => void>();
  private offPlayer: () => void;
  private startToken = 0;
  private disposed = false;
  private updateSerial = 0;
  private changeChain: Promise<unknown> = Promise.resolve();
  private changeSerial = 0;
  private leaseAtRisk = false;
  /** When playback was last restarted because the server no longer knew the session. */
  private lostRestartAt = -Infinity;
  private lastStart?: {target: StartTarget; options: StartOptions};
  /** Where the player last was while it played, paused or buffered: a failed element reports 0. */
  private heardMs?: number;
  private carriedFor?: number;

  constructor(o: ControllerOptions) {
    this.client = new SessionsClient(o.http, o.timers);
    this.player = o.player;
    this.key = o.key ?? (() => idempotencyKey());
    this.timers = o.timers ?? {};
    this.reporterOptions = o.reporter;
    this.onServerEnded = o.onServerEnded;
    this.onLeaseExpired = o.onLeaseExpired;
    this.serverSeek = o.serverSeek ?? seekNeedsServer;
    this.wantsPlaying = o.wantsPlaying;
    this.offPlayer = o.player.onEvent(e => this.playerEvent(e));
  }

  getSnapshot = (): ControllerSnapshot => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };
  private publish(p: Partial<ControllerSnapshot>) { this.state = Object.freeze({...this.state, ...p,
    ...(p.phase && p.phase !== 'preparing' ? {preparation: undefined} : {})}); for (const l of [...this.listeners]) l(); }

  /**
   * Play something. The server ends whatever this device was playing when it starts the new
   * session, so a queue advance or a version switch keeps one stream.
   */
  async start(target: StartTarget, options: StartOptions = {}, signal?: AbortSignal,
    context: Pick<StartContext, 'isCurrent' | 'capturePreparing' | 'onPreparing'> = {}): Promise<Session | undefined> {
    if (this.disposed || signal?.aborted) return undefined;
    const before = this.startToken;
    const originalCurrent = context.isCurrent, capturePreparing = context.capturePreparing, onPreparing = context.onPreparing;
    const admitted = () => {try {return (originalCurrent ? originalCurrent() === true : true) && !signal?.aborted;} catch {return false;}};
    // Even a currentness callback may reenter; it cannot let this older invocation
    // claim over the successor. Claim before touching request getters or publishing.
    if (!admitted() || this.disposed || before !== this.startToken) return undefined;
    const token = ++this.startToken;
    if (this.state.preparation) {
      this.publish({preparation: undefined});
      if (!admitted() || token !== this.startToken || this.disposed) return undefined;
    }
    const originalTarget: StartTarget = freezeWire(JSON.parse(JSON.stringify(target)));
    const originalOptions: StartOptions = freezeWire(JSON.parse(JSON.stringify(options)));
    if (!admitted() || token !== this.startToken || this.disposed) return undefined;
    const body: StartOptions = originalOptions;
    this.lastStart = {target: originalTarget, options: originalOptions};
    this.retireReporter();
    this.publish({phase: 'starting', error: undefined, ended: undefined, changing: false});
    try {
      const session = await this.client.start(originalTarget, body, this.key(), {signal,
        isCurrent: () => admitted() && token === this.startToken && !this.disposed,
        onPreparing: (wait, preparation) => {
          if (admitted() && token === this.startToken && !this.disposed) this.publish({phase: 'preparing', preparation});
          if (admitted() && token === this.startToken && !this.disposed) onPreparing?.(wait, preparation);
        },
        capturePreparing});
      if (!admitted() || token !== this.startToken || this.disposed) {
        if (token === this.startToken && !this.disposed) this.publish({preparation: undefined});
        // The request may have created a session even though its caller moved on.
        if (this.state.session?.id !== session.id || this.disposed) await this.client.stop(session.id);
        return undefined;
      }
      this.gate.reset();
      this.heardMs = undefined;
      this.carriedFor = undefined;
      this.state = Object.freeze({...this.state, observation: undefined});
      this.leaseAtRisk = false;
      this.adopt(session, originalOptions.state !== 'paused');
      return session;
    } catch (e) {
      const current = admitted();
      if (token === this.startToken && !this.disposed) this.publish(current ? {phase: 'error', error: e as Error} : {preparation: undefined});
      throw e;
    }
  }

  /**
   * Take over a session another request made: a queue create or advance that started it, a
   * committed transfer. A different session replaces the current one here (the server already
   * replaced it); the same session with a newer generation loads that presentation.
   * `startPositionMs` overrides the presentation's (an explicit position the viewer chose).
   */
  attach(session: Session, options: Readonly<{autoplay?: boolean; startPositionMs?: number}> = {}): void {
    if (this.disposed) return;
    if (this.state.session?.id !== session.id) {
      this.startToken++;
      this.retireReporter();
      this.gate.reset();
      this.heardMs = undefined;
      this.leaseAtRisk = false;
      this.carriedFor = undefined;
      this.state = Object.freeze({...this.state, observation: undefined, error: undefined, ended: undefined, changing: false});
    }
    this.adopt(session, options.autoplay ?? this.autoplay(session), options.startPositionMs);
  }

  /** Let go of the session without ending it on the server (it was replaced or retired there, or
   * its owner stops it separately). Nothing more is reported for it. */
  detach(): void {
    this.startToken++;
    this.retireReporter();
    this.gate.reset();
    this.heardMs = undefined;
    this.leaseAtRisk = false;
    this.publish({phase: 'idle', session: undefined, observation: undefined, error: undefined, ended: undefined, changing: false});
  }

  /** Reload the current presentation after a renderer handoff. The host must retire
   * the old renderer first; the same session keeps its reporter. */
  reloadMedia(options: Readonly<{autoplay?: boolean; startPositionMs?: number}> = {}): void {
    const session = this.state.session;
    if (this.disposed || this.state.phase !== 'active' || !session) return;
    this.loadMedia(session, options.autoplay ?? this.autoplay(session), options.startPositionMs ?? this.state.observation?.positionMs);
  }

  private autoplay(session: Session): boolean {
    return this.wantsPlaying ? this.wantsPlaying() : session.state !== 'paused';
  }

  /** Adopt a server session (a start, a change, a re-read): load a new presentation's address. */
  private adopt(session: Session, autoplay: boolean, startPositionMs?: number) {
    if (this.disposed || (this.gate.value !== undefined && session.presentation.generation < this.gate.value)) return;
    if (session.state === 'ended') { this.publish({session}); void this.serverEnded(session.end ?? {reason: 'stopped'}); return; }
    const fresh = this.gate.advance(session.presentation.generation);
    if (!this.reporter) this.reporter = this.newReporter(session);
    this.publish({session, phase: 'active', ended: undefined});
    if (!fresh) return;
    this.reporter.setGeneration(session.presentation.generation);
    this.loadMedia(session, autoplay, startPositionMs);
  }

  private loadMedia(session: Session, autoplay: boolean, startPositionMs?: number) {
    if (this.disposed || this.state.session !== session || this.state.phase !== 'active') return;
    this.player.load({
      url: session.presentation.url, generation: session.presentation.generation, mode: session.presentation.mode,
      startPositionMs: startPositionMs !== undefined ? Math.max(0, Math.round(startPositionMs)) : session.presentation.startPositionMs, subtitles: session.presentation.subtitles, autoplay,
      sessionId: session.id, ...(session.itemId ? {itemId: session.itemId} : {}),
      ...(session.presentation.bitrateKbps ? {bitrateKbps: session.presentation.bitrateKbps} : {}),
    });
  }

  private newReporter(session: Session): ProgressReporter {
    const sessionId = session.id;
    return new ProgressReporter({
      ...this.reporterOptions,
      everyMs: session.lease.reportEveryMs,
      now: this.timers.now, setTimer: this.timers.setTimer, clearTimer: this.timers.clearTimer,
      send: report => this.client.progress(sessionId, report),
      onLeaseRisk: () => { if (this.state.session?.id === sessionId) this.leaseAtRisk = true; },
      onEnded: () => { if (this.state.session?.id === sessionId) void this.serverEnded(); },
      // The server moved the session to another way of playing (a failure it planned around):
      // read it again and load the new address.
      onNewer: () => { void this.sessionUpdated(sessionId); },
    });
  }

  private retireReporter() {
    this.reporter?.stop();
    this.reporter = undefined;
  }

  /** The server ended our session. Why comes with the event, or from re-reading the session. */
  private async serverEnded(end?: SessionEnd) {
    const session = this.state.session;
    const token = this.startToken;
    this.retireReporter();
    let lost = false;
    if (!end && session) end = await this.client.get(session.id).then(s => s.end, e => { lost = e instanceof PlaybackApiError && (e.status === 404 || e.status === 410); return undefined; });
    if (this.disposed || token !== this.startToken || (session && this.state.session?.id !== session.id)) return; // replaced while its end was read
    // A session the server no longer knows (it restarted, or the session ran out while this
    // device was silent) starts again where it was, quietly. An administrator's stop, removed
    // access or a stop from another device is final. A server that keeps forgetting the session
    // gets one restart every few seconds, not a loop.
    const now = this.timers.now?.() ?? Date.now();
    if (lost && now - this.lostRestartAt < LOST_SESSION_RESTART_MS) lost = false;
    else if (lost) this.lostRestartAt = now;
    const lapsed = end ? end.reason === 'lease_expired' : this.leaseAtRisk || lost;
    if (lapsed && session) {
      const at = this.heardMs ?? this.state.observation?.positionMs ?? session.presentation.startPositionMs;
      this.leaseAtRisk = false;
      if (this.onLeaseExpired) { this.onLeaseExpired(session, at); return; }
      if (this.lastStart && 'itemId' in this.lastStart.target) {
        // Start again where we were.
        await this.start(this.lastStart.target, {...this.lastStart.options, startPositionMs: at, startFrom: undefined}).catch(() => {});
        return;
      }
    }
    this.player.pause();
    const reason = end ?? {reason: 'stopped'};
    this.publish({phase: 'ended', ended: reason});
    if (session) this.onServerEnded?.(session, reason);
  }

  /**
   * A reading from an engine the port does not carry (the web's own music decoder plays while
   * the media element is idle). It is tagged with the generation in force and reported like the
   * port's own readings.
   */
  observe(reading: Readonly<{state: PlayerObservation['state']; positionMs: number; rate?: number; volume?: number; muted?: boolean}>, cause: 'time' | 'state' | 'seeked' = 'time'): void {
    const generation = this.gate.value;
    if (generation === undefined || !this.state.session || this.state.phase === 'ended') return;
    const o: PlayerObservation = Object.freeze({generation, state: reading.state, positionMs: Math.max(0, Math.round(reading.positionMs)), rate: reading.rate ?? 1, ...(reading.volume !== undefined ? {volume: reading.volume} : {}), ...(reading.muted !== undefined ? {muted: reading.muted} : {})});
    if (o.state === 'playing' || o.state === 'paused' || o.state === 'buffering') this.heardMs = o.positionMs;
    this.publish({observation: o});
    this.reporter?.observe(o, cause);
  }

  /**
   * The title played to its end: resolves once the server has that report (the queue advances
   * only after it), rejects when it could not be delivered. A session the server already ended
   * counts as delivered.
   */
  async confirmEnded(): Promise<void> {
    const reporter = this.reporter;
    if (!reporter) return;
    await reporter.flush();
    const t = reporter.snapshot();
    if (t.lastAccepted !== 'ended' && !t.stopped) throw new PlaybackApiError(503, 'timeline_unconfirmed', 'same_request');
  }


  /** Send the newest reading now (the page is being hidden or closed). */
  flush(): Promise<void> { return this.reporter?.flush() ?? Promise.resolve(); }

  /** Hand this session's progress to a native owner: this controller stops reporting it. */
  handOverObserver(sessionId: string): string | undefined {
    if (this.state.session?.id === sessionId) this.retireReporter();
    return undefined;
  }

  private playerEvent(e: PlayerEvent) {
    const o = e.observation;
    if (!this.gate.accepts(o.generation)) return;
    if (o.state === 'playing' || o.state === 'paused' || o.state === 'buffering') this.heardMs = o.positionMs;
    this.publish({observation: o});
    if (e.type === 'error') { this.reporter?.failed(o.generation); return; }
    // PiP or AirPlay can't show an overlay the app draws: ask for the same track in the manifest,
    // which the system renders wherever the picture is (Plan §8.3). Once per generation.
    if (o.subtitlesCarried === false && o.subtitleTrackId && this.carriedFor !== o.generation) {
      this.carriedFor = o.generation;
      void this.change({subtitles: {trackId: o.subtitleTrackId, delivery: 'embeddedClient'}}).catch(() => {});
    }
    this.reporter?.observe(o, e.type === 'time' ? 'time' : e.type === 'seeked' ? 'seeked' : 'state');
  }

  /** Send a change. Changes are sent one after another, so the newest intent is the one that lands. */
  change(change: SessionChange, context?: SessionChangeContext): Promise<Session | undefined> {
    try {requireSessionChangeCurrent(context);} catch (error) {return Promise.reject(error);}
    const token = this.startToken, sessionId = this.state.session?.id;
    const holds = () => !this.disposed && token === this.startToken && sessionId === this.state.session?.id && this.state.phase === 'active';
    const owns = () => holds() && (!context || serial === this.changeSerial && sessionChangeCurrent(context));
    const check = () => {if (!owns()) throw new DOMException('Playback change retired', 'AbortError');};
    // A change that re-plans carries where the player is, so the new presentation starts there
    // and not at the last report (which can be a report interval old).
    const at = this.heardMs ?? this.state.observation?.positionMs;
    if (replans(change) && change.positionMs === undefined && change.seek === undefined && at !== undefined) change = {...change, positionMs: Math.max(0, Math.round(at))};
    const serial = ++this.changeSerial;
    const run = async (): Promise<Session | undefined> => {
      const session = this.state.session;
      try {
        if (!session || !owns()) {if (context) check();return undefined;}
        this.publish({changing: true});
        const updated = await this.client.patch(session.id, change, context?.signal);
        if (!owns()) {if (context) check();return undefined;} // replaced meanwhile
        if (context && (updated.id !== session.id || updated.presentation.generation < (this.state.session?.presentation.generation ?? 0))) throw new Error('Playback session changed during replan');
        if (context?.recoverEnded === false && updated.state === 'ended') throw new PlaybackApiError(410, 'session_ended', 'never');
        // The old stream kept playing while the server prepared the new one: the new one starts
        // where the picture is now, not where it was when the change was asked for.
        const now = this.player.observe();
        const resume = !change.seek && this.gate.accepts(now.generation) && now.state !== 'idle' && now.state !== 'error' ? now.positionMs : undefined;
        this.adopt(updated, this.autoplay(updated), resume);
        return updated;
      } catch (e) {
        if (!owns()) {if (context) check();return undefined;}
        if (context?.recoverEnded !== false && e instanceof PlaybackApiError && (e.status === 404 || e.status === 410)) { void this.serverEnded(); return undefined; }
        this.publish({error: e as Error});
        throw e;
      } finally {
        if (holds() && serial === this.changeSerial) this.publish({changing: false});
      }
    };
    const p = this.changeChain.then(run, run);
    this.changeChain = p.catch(() => {});
    return p;
  }

  play() { this.player.play(); }
  pause() { this.player.pause(); }
  setRate(rate: number) { this.player.setRate(Math.max(0.25, Math.min(4, rate))); }

  /** Seek locally when bytes are direct or copied; through the server when it must re-produce. */
  seek(positionMs: number): Promise<unknown> | void {
    const session = this.state.session;
    if (!session) return;
    if (this.serverSeek(session)) return this.change({seek: {positionMs: Math.max(0, Math.round(positionMs)), id: this.key()}});
    this.player.seek(Math.max(0, positionMs));
  }

  /**
   * Subtitles already offered as a sidecar switch locally; others need the server (new generation).
   * A local switch is reported at once (`subtitleTrackId` in the timeline, spec §17).
   */
  setSubtitles(trackId: string | null): Promise<unknown> | void {
    const session = this.state.session;
    if (!session) return;
    // A track drawn by the engine or burned into the picture is the server's to turn off.
    const serverOwned = session.presentation.subtitle !== undefined && session.presentation.subtitle.delivery !== 'sidecar';
    if (!serverOwned && (trackId === null || session.presentation.subtitles.some(s => s.trackId === trackId))) {
      this.player.selectSidecarSubtitle(trackId);
      const o = this.player.observe();
      if (this.gate.accepts(o.generation)) { this.publish({observation: o}); this.reporter?.observe(o, 'state'); }
      return;
    }
    return this.change({subtitles: {trackId}});
  }

  setAudio(trackId: string) { return this.change({audio: {trackId}}); }
  /** Choose a quality for this session. `throughputKbps` lets the server refuse one the connection cannot carry. */
  setQuality(quality: QualityRequest, throughputKbps?: number) { return this.change({quality, ...(throughputKbps !== undefined ? {throughputKbps: Math.max(0, Math.round(throughputKbps))} : {})}); }
  /** Automatic quality: move one ladder step down or up from where the session is. */
  stepQuality(direction: 'down' | 'up') { return this.change({quality: {mode: 'automatic', step: direction}}); }
  setVersion(versionId: string) { return this.change({versionId}); }
  /** The engine could not play this presentation: the server re-plans the same session, at the
   * current position, another way (possibly with the other engine). Rejects with `unsupported_media`
   * when there is no other way. */
  playerFailed(code: PlayerFailureCode, detail = '') { return this.change({failure: {code, detail: detail.replace(/[\x00-\x1f\x7f]/g, ' ').slice(0, 500)}}); }

  /** Skips a marker (an intro, the credits): a seek to its end. */
  skip(marker: Readonly<{id: string; endMs: number}>, _mode: 'automatic' | 'manual' = 'manual') {
    return this.seek(marker.endMs);
  }

  /** Our session changed on the server (a progress answer named a later presentation, or the
   * owner was told): an end with its reason is taken as is; otherwise the session is read again. */
  async sessionUpdated(sessionId: string, _revision?: string, data?: unknown, recovery?: {signal?: AbortSignal; strict?: boolean}): Promise<void> {
    const session = this.state.session;
    if (recovery?.signal?.aborted || this.disposed || !session || session.id !== sessionId || this.state.phase !== 'active') return;
    const token = this.startToken, serial = ++this.updateSerial;
    const owns = () => !recovery?.signal?.aborted && !this.disposed && token === this.startToken && serial === this.updateSerial && this.state.session?.id === sessionId && this.state.phase === 'active';
    const end = data && typeof data === 'object' && (data as {state?: unknown}).state === 'ended' ? parseSessionEnd(data) : undefined;
    if (end) { void this.serverEnded(end); return; }
    try {
      const fresh = await this.client.get(sessionId, recovery?.signal);
      if (!owns()) return;
      if (fresh.state === 'ended') { void this.serverEnded(fresh.end ?? {reason: 'stopped'}); return; }
      if (this.state.session !== session && fresh.presentation.generation <= (this.gate.value ?? 0)) return;
      this.adopt(fresh, this.autoplay(fresh));
    } catch (e) {
      if (!owns()) return;
      if (e instanceof PlaybackApiError && (e.status === 404 || e.status === 410)) void this.serverEnded();
      else if (recovery?.strict) throw e;
    }
  }

  /** Stop playing: report the final position, end the session, pause the player. Idempotent. */
  async stop(): Promise<void> {
    const session = this.state.session;
    ++this.startToken;
    const at = this.heardMs ?? this.state.observation?.positionMs;
    const finalReport = this.reporter?.flush().catch(() => {});
    this.retireReporter();
    this.player.pause();
    this.publish({phase: 'ended', changing: false});
    await finalReport;
    if (session) {try {await this.client.stop(session.id, at);} catch (error) {this.publish({phase:'error',error:error as Error});throw error;}}
  }

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    ++this.startToken;
    this.retireReporter();
    this.offPlayer();
    this.listeners.clear();
  }
}
