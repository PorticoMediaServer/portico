import {routePath, encodedRoutePath} from "../../../contracts/src/route-paths.ts";
import {decodeOpen} from '../../../contracts/src/runtime.ts';
import type {StartPlaybackSessionResponse} from '../../../contracts/src/types.ts';
/**
 * Playback sessions (spec §5) and the controller that plays one on a `MediaPlayer`.
 *
 * `SessionsClient` is the wire: idempotent start (201, or 202 while preparing: the same request
 * is replayed with the same `Idempotency-Key` after `retryAfterMs`), `PATCH` with `If-Match`
 * (412 carries the current session), `DELETE`, timeline.
 *
 * `PlaybackSessionController` is what a platform wires: it owns the generation gate, the timeline
 * reporter and the player port, serialises changes, recovers from 412 by adopting the server's
 * session and re-applying only the newest intent, and ends cleanly when the server ends the
 * session (stop elsewhere, transfer, admin terminate). A session another request made (a queue
 * create or advance) is handed to it with `attach`; a lapsed lease is given back to the owner
 * (`onLeaseExpired`) so the title starts again where it was instead of showing an error.
 */
import {GenerationGate} from './generation.ts';
import {PlaybackApiError, call, callOnce, enc, header, idempotencyKey, type V1Http} from './http.ts';
import type {ObservationAck, ObservationSample, ObserverGrant} from './observation.ts';

/** An observation that has not answered in this long is replaced by the next one. */
const observationDeadlineMs = 1500;
import type {MediaPlayer, PlayerEvent, PlayerObservation} from './port.ts';
import type {QualityRequest} from './quality.ts';
import type {PlayerFailureCode} from '../client-profile.ts';
import {ObservationReporter, type ObservationReporterOptions} from './observation.ts';
import {ContractError, id, parseDiscTitleRef, parseSession, parseSessionEnd, parsePresentation, type Presentation, type DiscTitleRef, type Session, type SessionEnd} from './types.ts';
import {requireSessionChangeCurrent, sessionChangeCurrent, type SessionChangeContext} from './change-context.ts';
export type {SessionChangeContext} from './change-context.ts';

export type StartTarget =
  | Readonly<{itemId: string}>
  | Readonly<{channelId: string; channelGeneration?: string}>
  | Readonly<{queue: Readonly<{queueId: string; entryId: string}>}>
  | Readonly<{transferId: string}>;

export type StartOptions = Readonly<{
  versionId?: string;
  /** Exact measured title; never substituted by a later main-title choice. */
  discTitle?: DiscTitleRef;
  partIndex?: number;
  startPositionMs?: number;
  startFrom?: 'resume' | 'beginning' | 'live' | 'startOver';
  state?: 'playing' | 'paused';
  quality?: QualityRequest;
  audio?: Readonly<{trackId: string; channels?: number}>;
  subtitles?: Readonly<{trackId: string | null; delivery?: 'auto' | 'sidecar' | 'embeddedClient' | 'burn'}>;
  network?: 'local' | 'remote' | 'cellular';
  replacesSessionId?: string;
  /** The client's own estimate of what its connection to this server carries. With Automatic the
   * start lands on the widest quality that fits; with any other quality a start the connection
   * plainly cannot carry is refused (`connection_too_slow`, whose `reason` names a quality that fits). */
  throughputKbps?: number;
}>;

export type SessionChange = Readonly<{
  /** Re-read this device's capability document and plan again, keeping this occurrence's choices. */
  replan?: boolean;
  state?: 'playing' | 'paused';
  versionId?: string;
  partIndex?: number;
  audio?: Readonly<{trackId: string; channels?: number}>;
  subtitles?: Readonly<{trackId: string | null; delivery?: 'auto' | 'sidecar' | 'embeddedClient' | 'burn'}>;
  quality?: QualityRequest;
  seek?: Readonly<{positionMs: number; id: string}>;
  subtitleOffsetUs?: string;
  subtitleOffsetMs?: number;
  /** Where the player is now: a change that starts a new presentation starts it there. */
  positionMs?: number;
  /** The client's current connection estimate; checked against a quality asked for in the same change. */
  throughputKbps?: number;
  /** The player could not play the current presentation. The server remembers that for this file
   * on this device and re-plans the same session the next way up (`unsupported_media`: there is none). */
  failure?: Readonly<{code: PlayerFailureCode; detail?: string}>;
}>;

/** Whether a change makes the server plan the presentation again (so the player's position should travel with it). */
const replans = (c: SessionChange) => c.replan === true || c.versionId !== undefined || c.partIndex !== undefined || c.audio !== undefined || c.quality !== undefined || c.subtitles !== undefined || c.failure !== undefined;

export type Timers = Readonly<{now?: () => number; setTimer?: (fn: () => void, ms: number) => unknown; clearTimer?: (t: unknown) => void}>;

/** Actual server-directed start preparation, including replies with no session.
 * Players render the catalogue key while the same original start keeps retrying. */
export type StartPreparation = Readonly<{messageId: 'player.preparing.slowStart'; retryAfterMs: number}>;

/** An actual declared session-backed 202, never a fabricated playable Session. */
export type PreparingSessionView = Extract<StartPlaybackSessionResponse, {id: string}>;
export type StartContext = Readonly<{
  signal?: AbortSignal;
  isCurrent?: () => boolean;
  onPreparing?: (retryAfterMs: number, preparation: StartPreparation) => void;
  /** Issue a purpose-specific original-start receipt when the actual view first arrives.
   * Its captured cleanup must re-prove original identity, registration and noninstallation;
   * a current queue-selection receipt or a later id lookup is not equivalent. */
  capturePreparing?: (session: PreparingSessionView) => (() => void | Promise<void>) | undefined;
}>;
const startRetired = () => Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
const startIdentity = (value: unknown): Readonly<{id: string; generation: number}> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new ContractError('start response');
  const sessionId = id(Reflect.get(value, 'id'), 'session id'), presentation = Reflect.get(value, 'presentation');
  if (!presentation || typeof presentation !== 'object' || Array.isArray(presentation)) throw new ContractError('start presentation');
  const generation: unknown = Reflect.get(presentation, 'generation');
  if (typeof generation !== 'number' || !Number.isSafeInteger(generation) || generation < 0) throw new ContractError('generation');
  return {id: sessionId, generation};
};
const freezeWire = <T,>(value: T): T => {
  if (value !== null && typeof value === 'object') {
    for (const child of Object.values(value)) freezeWire(child);
    Object.freeze(value);
  }
  return value;
};

/** Decode the official union, then impose the required 202 timing on either branch.
 * A retry-only reply never passes through parseSession or gains a resource identity. */
export function parseStartPreparation(value: unknown): Readonly<{retryAfterMs: number; session?: PreparingSessionView}> {
  let decoded: StartPlaybackSessionResponse;
  // O-159 open tier (R-09): an optional member or list entry with a newer server's enum value is left out.
  try {decoded = decodeOpen('StartPlaybackSessionResponse', value) as StartPlaybackSessionResponse;} catch {throw new ContractError('preparation response');}
  const retryAfterMs: unknown = Reflect.get(decoded, 'retryAfterMs');
  if (typeof retryAfterMs !== 'number' || !Number.isSafeInteger(retryAfterMs) || retryAfterMs < 1 || retryAfterMs > 10_000)
    throw new ContractError('preparation retry');
  return Object.freeze({retryAfterMs, ...('id' in decoded ? {session: freezeWire(decoded)} : {})});
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

  /** Start (or replay the same start). A valid server 202 keeps this original
   * request waiting until success, a real refusal or caller retirement. Server
   * preparation/authority deadlines remain server-owned and are never renewed. */
  async start(target: StartTarget, options: StartOptions, key: string, extra: StartContext = {}): Promise<Session> {
    return this.startOriginal(target, options, key, extra, parseSession);
  }

  /** The channel adapter retains the actual declared linear projection beside
   * its final response identity. Both use this same status/retry/lifetime path;
   * an existing channel may have no media URL until its ordinary join/read.
   * A retry-only body can never be returned as a completed start. The adapter's
   * decoder runs inside the original receipt's failure/cleanup boundary. */
  async startResponse<T>(target: StartTarget, options: StartOptions, key: string, extra: StartContext, decode: (body: unknown) => T): Promise<Readonly<{id: string; generation: number; body: T}>> {
    return this.startOriginal(target, options, key, extra, body => ({...startIdentity(body), body: decode(body)}));
  }

  private async startOriginal<T>(target: StartTarget, options: StartOptions, key: string, extra: StartContext, read: (body: unknown) => T): Promise<T> {
    // Capture the original wire and lifetime once, before callbacks or asynchronous work.
    const http = this.http, signal = extra.signal, isCurrent = extra.isCurrent;
    const onPreparing = extra.onPreparing, capturePreparing = extra.capturePreparing;
    const setTimer = this.setTimer, clearTimer = this.clearTimer;
    if (typeof key !== 'string') throw new ContractError('start key');
    let body: StartOptions & StartTarget;
    try {body = JSON.parse(JSON.stringify({...target, ...options}));} catch {throw new ContractError('start request');}
    const discTitle = parseDiscTitleRef(body.discTitle);
    if (discTitle && (!body.versionId || body.partIndex !== undefined && body.partIndex !== 0
      || !('itemId' in body) && !('queue' in body))) throw new ContractError('disc title start');
    if (discTitle) body = {...body, discTitle};
    freezeWire(body);
    const request = Object.freeze({method: 'POST' as const, path: routePath('start_playback_session'),
      headers: Object.freeze({'Idempotency-Key': key}), body, signal});
    const current = () => {try {return (isCurrent ? isCurrent() === true : true) && !signal?.aborted;} catch {return false;}};
    const check = () => {if (!current()) throw startRetired();};
    let prepared: PreparingSessionView | undefined;
    let abandon: (() => void | Promise<void>) | undefined;
    try {
      for (;;) {
        check();
        const r = await call(http, request, [201, 200, 202]);
        // A late real success must reach the existing caller's unowned-session cleanup.
        if (r.status !== 202) {
          const result = read(r.body);
          const actual = startIdentity(r.body);
          if (prepared && (prepared.id !== actual.id || prepared.presentation.generation !== actual.generation))
            throw new ContractError('preparation session changed');
          return result;
        }
        const preparing = parseStartPreparation(r.body);
        if (preparing.session) {
          const offered = preparing.session;
          if ('queue' in body ? offered.queue?.queueId !== body.queue.queueId || offered.queue?.entryId !== body.queue.entryId
            : 'itemId' in body ? offered.itemId !== body.itemId : 'channelId' in body ? offered.channelId !== body.channelId : false)
            throw new ContractError('preparation target');
          if (prepared && (prepared.id !== offered.id || prepared.presentation.generation !== offered.presentation.generation))
            throw new ContractError('preparation session changed');
          if (!prepared) {
            prepared = offered;
            abandon = capturePreparing?.(offered);
            if (abandon !== undefined && typeof abandon !== 'function') throw new ContractError('preparation ownership receipt');
          }
        }
        check();
        // Retain the existing 250ms pacing floor without accepting an invalid wire wait.
        const wait = Math.max(250, preparing.retryAfterMs);
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
      // Only the captured host can retire this genuine resource, with its own
      // original principal and unowned proof. Retry-only responses supply none.
      // A cleanup receipt owns its bounded settlement; it cannot replace this
      // original start's outcome or acquire another operation while DELETE is uncertain.
      if (abandon) {try {void Promise.resolve(abandon()).catch(() => {});} catch { /* Preserve the original start failure. */ }}
      throw error;
    }
  }

  async prepareAirPlay(session: Session, positionMs: number, signal?: AbortSignal): Promise<Readonly<{sessionId: string; generation: number; choiceRevision: number; presentation: Presentation}>> {
    signal?.throwIfAborted();
    const input = {generation: session.presentation.generation, choiceRevision: session.presentation.choiceRevision ?? 1, positionMs: Math.max(0, Math.round(positionMs))};
    const raw = (await call(this.http, {method: 'POST', path: encodedRoutePath('prepare_airplay_receiver', {id: enc(session.id)}), body: input, signal})).body as Record<string, unknown>;
    signal?.throwIfAborted();
    if (raw.sessionId !== session.id || raw.generation !== input.generation || raw.choiceRevision !== input.choiceRevision) throw new ContractError('AirPlay preparation fence');
    const presentation = parsePresentation(raw.presentation);
    if (presentation.mode !== 'stream' || presentation.generation !== input.generation || presentation.choiceRevision !== input.choiceRevision || !/^\/v1\/media\/[A-Za-z0-9_-]+\/master\.m3u8$/.test(presentation.url)) throw new ContractError('AirPlay presentation');
    const selected = session.presentation.subtitle;
    if (session.presentation.audioTrackId && presentation.audioTrackId !== session.presentation.audioTrackId || (selected?.trackId ?? null) !== (presentation.subtitle?.trackId ?? null) || selected && presentation.subtitle?.delivery !== (selected.delivery === 'sidecar' || selected.kind === 'text' ? 'sidecar' : 'burn') || !selected && presentation.subtitles.length > 0) throw new ContractError('AirPlay choices');
    return Object.freeze({sessionId: session.id, ...input, presentation});
  }

  async get(id: string, signal?: AbortSignal): Promise<Session> {
    return parseSession((await call(this.http, {method: 'GET', path: encodedRoutePath("get_playback_session", {"id": enc(id)}), signal})).body);
  }

  /** `PATCH` with `If-Match`. A 412 throws `PlaybackApiError` whose `current` is the parsed session. */
  async patch(id: string, revision: string, change: SessionChange, signal?: AbortSignal): Promise<Session> {
    try {
      return parseSession((await call(this.http, {method: 'PATCH', path: encodedRoutePath("change_playback_session", {"id": enc(id)}), headers: {'If-Match': revision, 'Content-Type': 'application/merge-patch+json'}, body: change, signal})).body);
    } catch (e) {
      if (e instanceof PlaybackApiError && e.status === 412 && e.current !== undefined) {
        let current: Session | undefined;
        try { current = parseSession(e.current); } catch { current = undefined; }
        throw new PlaybackApiError(412, e.code, 'after_refresh', current, e.retryAfterMs);
      }
      throw e;
    }
  }

  async stop(id: string, positionMs?: number, signal?: AbortSignal, generation?: number): Promise<void> {
    await call(this.http, {method: 'DELETE', path: encodedRoutePath("stop_playback_session", {"id": enc(id)}), ...(signal ? {signal} : {}), ...((positionMs !== undefined || generation !== undefined) ? {body: {...(positionMs !== undefined ? {positionMs: Math.max(0, Math.round(positionMs))} : {}), ...(generation !== undefined ? {generation} : {})}} : {})}, [204, 200]);
    if (generation !== undefined) await this.http.acknowledgeUserMutation?.(`stop:${id}:${generation}`);
  }

  /** Become the player reporting this session (P2-20): the epoch its samples carry. */
  async claimObserver(id: string, body: Readonly<{generation: number; replaces?: string}>, signal?: AbortSignal): Promise<ObserverGrant> {
    const v = (await call(this.http, {method: 'POST', path: encodedRoutePath("claim_playback_observer", {"id": enc(id)}), body, ...(signal ? {signal} : {})})).body as Record<string, unknown> | undefined;
    if (!v || typeof v.epoch !== 'string' || !/^[A-Za-z0-9_-]{22}$/.test(v.epoch)) throw new ContractError('observer grant');
    return Object.freeze({epoch: v.epoch, everyMs: typeof v.everyMs === 'number' && Number.isSafeInteger(v.everyMs) ? v.everyMs : 1000, ...(typeof v.leaseUntil === 'string' ? {leaseUntil: v.leaseUntil} : {})});
  }

  /** One observation: a single attempt with a short deadline. A failed one is replaced by the next. */
  async observe<S = Session>(id: string, sample: ObservationSample, signal?: AbortSignal, parse: (body: unknown) => S = parseSession as (body: unknown) => S): Promise<ObservationAck<S>> {
    const v = (await callOnce(this.http, {method: 'PUT', path: encodedRoutePath("put_playback_observation", {"id": enc(id)}), body: sample, deadlineMs: observationDeadlineMs, singleAttempt: true, ...(signal ? {signal} : {})})).body as Record<string, unknown> | undefined;
    if (!v || typeof v.acceptedSeq !== 'number') throw new ContractError('observation ack');
    let session: S | undefined;
    if (v.session !== undefined) { try { session = parse(v.session); } catch { session = undefined; } }
    return Object.freeze({acceptedSeq: v.acceptedSeq, ...(typeof v.leaseUntil === 'string' ? {leaseUntil: v.leaseUntil} : {}), ...(typeof v.viewTag === 'string' ? {viewTag: v.viewTag} : {}), ...(session ? {session} : {})});
  }
}

export type ControllerPhase = 'idle' | 'starting' | 'preparing' | 'active' | 'ended' | 'error';

export type ControllerSnapshot = Readonly<{
  phase: ControllerPhase;
  /** Present only after a genuine 202; no fabricated session is installed. */
  preparation?: StartPreparation;
  session?: Session;
  /** The last observation of the current generation (position, state). */
  observation?: PlayerObservation;
  error?: PlaybackApiError | Error;
  /** Why the server ended the session (phase "ended"): "terminated" by an administrator with
   * their message, "transferred", "lease_expired"… Absent when this device stopped it. Show it with
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
  reporter?: Partial<Omit<ObservationReporterOptions, 'claim' | 'send' | 'onEnded' | 'onLeaseRisk' | 'now' | 'setTimer' | 'clearTimer'>>;
  /** Called when the server ended the session (transfer committed, admin terminate, stop elsewhere). */
  onServerEnded?: (session: Session, end: SessionEnd) => void;
  /** A pause or play asked elsewhere (RN-09). The host's own playback owner applies it; without
   * one the player is paused or played directly. */
  onRemoteControl?: (state: 'playing' | 'paused') => void;
  /**
   * The lease lapsed while this device could not report (a sleeping laptop, a long outage). The
   * owner starts the title again at `positionMs` (a queue restarts its entry). Without it the
   * controller restarts a plain item start by itself; anything else ends.
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
 * Whether a seek must go through the server (spec §5.7): only when something is transcoded or
 * burned, since the server must restart production. Direct play and stream copy seek locally.
 */
export function seekNeedsServer(session: Session): boolean {
  const d = session.presentation.decision;
  return [d.video, d.audio, d.subtitles].some(x => x?.action === 'transcode' || x?.action === 'burn');
}

export class PlaybackSessionController {
  private readonly client: SessionsClient;
  private readonly player: MediaPlayer;
  private readonly gate = new GenerationGate();
  private readonly key: () => string;
  private readonly timers: Timers;
  private readonly reporterOptions: ControllerOptions['reporter'];
  private readonly onServerEnded?: (session: Session, end: SessionEnd) => void;
  private readonly onRemoteControl?: (state: 'playing' | 'paused') => void;
  private readonly onLeaseExpired?: (session: Session, positionMs: number) => void;
  private readonly serverSeek: (session: Session) => boolean;
  private readonly wantsPlaying?: () => boolean;
  private reporter?: ObservationReporter;
  /** The newest command revision (RN-09) this controller has applied, per session. */
  private appliedControl?: Readonly<{sessionId: string; revision: number}>;
  private state: ControllerSnapshot = Object.freeze({phase: 'idle', changing: false});
  private listeners = new Set<() => void>();
  private offPlayer: () => void;
  private startToken = 0;
  private disposed = false;
  private updateSerial = 0;
  private changeChain: Promise<unknown> = Promise.resolve();
  private changeSerial = 0;
  private leaseAtRisk = false;
  private lastStart?: {target: StartTarget; options: StartOptions};
  private carriedFor?: number;

  constructor(o: ControllerOptions) {
    this.client = new SessionsClient(o.http, o.timers);
    this.player = o.player;
    this.key = o.key ?? (() => idempotencyKey());
    this.timers = o.timers ?? {};
    this.reporterOptions = o.reporter;
    this.onServerEnded = o.onServerEnded;
    this.onRemoteControl = o.onRemoteControl;
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
   * Play something. A session already playing on this device is replaced atomically
   * (`replacesSessionId`), so queue advance, version switch and channel surfing keep one slot.
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
    const previous = this.state.session, wasActive = this.state.phase === 'active';
    const originalTarget: StartTarget = freezeWire(JSON.parse(JSON.stringify(target)));
    const originalOptions: StartOptions = freezeWire(JSON.parse(JSON.stringify(options)));
    if (!admitted() || token !== this.startToken || this.disposed) return undefined;
    const body: StartOptions = {...originalOptions, ...(previous && wasActive && !originalOptions.replacesSessionId ? {replacesSessionId: previous.id} : {})};
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
        if (this.state.session?.id !== session.id || this.disposed) await this.client.stop(session.id, undefined, undefined, session.presentation.generation);
        return undefined;
      }
      this.gate.reset();
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
    this.leaseAtRisk = false;
    this.publish({phase: 'idle', session: undefined, observation: undefined, error: undefined, ended: undefined, changing: false});
  }

  /** Reload the current presentation after a renderer handoff. The host must retire
   * the old renderer first; the same session keeps its reporter and increasing seq. */
  reloadMedia(options: Readonly<{autoplay?: boolean; startPositionMs?: number}> = {}): void {
    const session = this.state.session;
    if (this.disposed || this.state.phase !== 'active' || !session) return;
    this.loadMedia(session, options.autoplay ?? this.autoplay(session), options.startPositionMs ?? this.state.observation?.positionMs);
  }

  private autoplay(session: Session): boolean {
    return this.wantsPlaying ? this.wantsPlaying() : session.state !== 'paused';
  }

  /** Adopt a server session (start, 412 recovery, `session.updated`): load a new generation's URL. */
  private adopt(session: Session, autoplay: boolean, startPositionMs?: number) {
    if (this.disposed || (this.gate.value !== undefined && session.presentation.generation < this.gate.value)) return;
    if (session.state === 'ended') { this.publish({session}); void this.serverEnded(session.end ?? {reason: 'stopped'}); return; }
    const fresh = this.gate.advance(session.presentation.generation);
    if (!this.reporter) this.reporter = this.newReporter(session.id);
    this.publish({session, phase: 'active', ended: undefined});
    const control = session.control ? Number(session.control.revision) : undefined;
    const applied = this.appliedControl?.sessionId === session.id ? this.appliedControl.revision : undefined;
    if (control !== undefined && (applied === undefined || control > applied)) {
      this.appliedControl = Object.freeze({sessionId: session.id, revision: control});
      // A newer command revision on the same presentation is a pause or play asked elsewhere (an
      // administrator, another device). This player's own reports never move it, so applying it
      // once cannot undo a newer local choice.
      if (!fresh && applied !== undefined && session.control) {
        const state = session.control.state;
        if (this.onRemoteControl) this.onRemoteControl(state);
        else if (state === 'paused') this.player.pause(); else this.player.play();
      }
    }
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

  private newReporter(sessionId: string): ObservationReporter {
    return new ObservationReporter({
      ...this.reporterOptions,
      now: this.timers.now, setTimer: this.timers.setTimer, clearTimer: this.timers.clearTimer,
      claim: body => this.client.claimObserver(sessionId, body),
      send: sample => this.client.observe(sessionId, sample),
      onLeaseRisk: () => { if (this.state.session?.id === sessionId) this.leaseAtRisk = true; },
      onEnded: () => { if (this.state.session?.id === sessionId) void this.serverEnded(); },
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
    if (!end && session) end = await this.client.get(session.id).then(s => s.end, () => undefined);
    if (this.disposed || token !== this.startToken || (session && this.state.session?.id !== session.id)) return; // replaced while its end was read
    // Only a lapsed lease is resumed; an administrator's stop or a move elsewhere is final. The
    // server names a lapse; when the reason can't be read, a lease this device knew was at risk is one.
    const lapsed = end ? end.reason === 'lease_expired' : this.leaseAtRisk;
    if (lapsed && session) {
      const at = this.state.observation?.positionMs ?? session.presentation.startPositionMs;
      this.leaseAtRisk = false;
      if (this.onLeaseExpired) { this.onLeaseExpired(session, at); return; }
      if (this.lastStart && 'itemId' in this.lastStart.target) {
        // Start again where we were.
        await this.start(this.lastStart.target, {...this.lastStart.options, startPositionMs: at, startFrom: undefined, replacesSessionId: undefined}).catch(() => {});
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

  /** A marker skip the caller performed itself (it seeks): evidence for the next report (spec §4.2). */
  noteSkip(skipped: Readonly<{markerId: string; mode: 'automatic' | 'manual'; positionMs: number}>): void {
    if (skipped.markerId) this.reporter?.skipped({markerId: skipped.markerId, mode: skipped.mode, positionMs: Math.max(0, Math.round(skipped.positionMs))});
  }

  /** Send the newest reading now (the page is being hidden or closed). */
  flush(): Promise<void> { return this.reporter?.flush() ?? Promise.resolve(); }

  /** Hand this session's observations to a native owner: this controller stops reporting it and
   * returns the epoch the owner claims with (`replaces`), so the server lets it take over at once. */
  handOverObserver(sessionId: string): string | undefined {
    if (this.state.session?.id !== sessionId || !this.reporter) return undefined;
    const epoch = this.reporter.snapshot().epoch;
    this.retireReporter();
    return epoch;
  }

  private playerEvent(e: PlayerEvent) {
    const o = e.observation;
    if (!this.gate.accepts(o.generation)) return;
    this.publish({observation: o});
    if (e.type === 'error') { this.reporter?.failed(o.generation, e.code, e.detail); return; }
    // PiP or AirPlay can't show an overlay the app draws: ask for the same track in the manifest,
    // which the system renders wherever the picture is (Plan §8.3). Once per generation.
    if (o.subtitlesCarried === false && o.subtitleTrackId && this.carriedFor !== o.generation) {
      this.carriedFor = o.generation;
      void this.change({subtitles: {trackId: o.subtitleTrackId, delivery: 'embeddedClient'}}).catch(() => {});
    }
    this.reporter?.observe(o, e.type === 'time' ? 'time' : e.type === 'seeked' ? 'seeked' : 'state');
  }

  /**
   * Send a change. Changes are serialised; on 412 the controller adopts the server's session and
   * re-applies this change only if no newer change is waiting (newest intent wins, invariant 2).
   */
  change(change: SessionChange, context?: SessionChangeContext): Promise<Session | undefined> {
    try {requireSessionChangeCurrent(context);} catch (error) {return Promise.reject(error);}
    const token = this.startToken, sessionId = this.state.session?.id;
    const holds = () => !this.disposed && token === this.startToken && sessionId === this.state.session?.id && this.state.phase === 'active';
    const owns = () => holds() && (!context || serial === this.changeSerial && sessionChangeCurrent(context));
    const check = () => {if (!owns()) throw new DOMException('Playback change retired', 'AbortError');};
    // A change that re-plans carries where the player is, so the new presentation starts there
    // and not at the last report (which can be a report interval old).
    const at = this.state.observation?.positionMs;
    if (replans(change) && change.positionMs === undefined && change.seek === undefined && at !== undefined) change = {...change, positionMs: Math.max(0, Math.round(at))};
    const serial = ++this.changeSerial;
    const run = async (): Promise<Session | undefined> => {
      const session = this.state.session;
      try {
        if (!session || !owns()) {if (context) check();return undefined;}
        this.publish({changing: true});
        // A change that makes new bytes resumes where the server last heard this device was:
        // tell it now, so the new stream starts at the picture's position, not ten seconds back.
        if (context?.recoverEnded !== false && (change.quality || change.audio || change.versionId || change.partIndex !== undefined)) await this.reporter?.flush().catch(() => {});
        if (!owns()) {if (context) check();return undefined;}
        let updated: Session;
        try {
          updated = await this.client.patch(session.id, session.revision, change, context?.signal);
        } catch (e) {
          if (!owns()) {if (context) check();return undefined;}
          if (!(e instanceof PlaybackApiError) || e.status !== 412 || !e.current) throw e;
          if ((e.current as Session).id !== session.id) throw e;
          if ((e.current as Session).state === 'ended') {if (context?.recoverEnded === false) throw new PlaybackApiError(410, 'session_ended', 'never');void this.serverEnded((e.current as Session).end); return undefined;}
          if (context) check();
          this.adopt(e.current as Session, this.wantsPlaying ? this.wantsPlaying() : this.state.observation?.state !== 'paused');
          if (serial !== this.changeSerial) return this.state.session;
          updated = await this.client.patch((e.current as Session).id, (e.current as Session).revision, change, context?.signal);
        }
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

  /** Skips a marker (spec §4.2): a seek to its end plus a `skipped` note in the next timeline
   * report. `automatic` is for the viewer's `auto` preference, and only on a marker the server
   * marks `automaticSafe`; the server ignores anything else as evidence. */
  skip(marker: Readonly<{id: string; endMs: number}>, mode: 'automatic' | 'manual' = 'manual') {
    if (marker.id) this.reporter?.skipped({markerId: marker.id, mode, positionMs: Math.max(0, Math.round(this.state.observation?.positionMs ?? 0))});
    return this.seek(marker.endMs);
  }

  /** A `session.updated` event for our session (with its payload when the app has it): an end
   * with its reason is taken as is; otherwise the session is re-read if its revision moved. */
  async sessionUpdated(sessionId: string, revision?: string, data?: unknown, recovery?: {signal?: AbortSignal; strict?: boolean}): Promise<void> {
    const session = this.state.session;
    if (recovery?.signal?.aborted || this.disposed || !session || session.id !== sessionId || this.state.phase !== 'active' || (revision !== undefined && revision === session.revision)) return;
    const token = this.startToken, serial = ++this.updateSerial;
    const owns = () => !recovery?.signal?.aborted && !this.disposed && token === this.startToken && serial === this.updateSerial && this.state.session?.id === sessionId && this.state.phase === 'active';
    const end = data && typeof data === 'object' && (data as {state?: unknown}).state === 'ended' ? parseSessionEnd(data) : undefined;
    if (end) { void this.serverEnded(end); return; }
    try {
      const fresh = await this.client.get(sessionId, recovery?.signal);
      if (!owns()) return;
      if (fresh.state === 'ended') { void this.serverEnded(fresh.end ?? {reason: 'stopped'}); return; }
      if (this.state.session?.revision !== session.revision && fresh.presentation.generation <= (this.gate.value ?? 0)) return;
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
    const at = this.state.observation?.positionMs;
    const finalReport = this.reporter?.flush().catch(() => {});
    this.retireReporter();
    this.player.pause();
    this.publish({phase: 'ended', changing: false});
    await finalReport;
    if (session) {try {await this.client.stop(session.id, at, undefined, session.presentation.generation);} catch (error) {this.publish({phase:'error',error:error as Error});throw error;}}
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
