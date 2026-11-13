import type {ResyncReadContext} from '../resync-coordinator.ts';
import {encodedRoutePath} from "../../../contracts/src/route-paths.ts";
/**
 * Watching a channel on the rebuilt server (definition 11, rules 22-29): one playback session, the
 * same one a title uses, presented as the channel view `PlaybackService` and the players already
 * read, so neither changes.
 *
 * - Start: `POST /v1/playback/sessions {channel, state}`. A start with every tuner in use is
 *   `409 no_tuner`, and the server's own words say what is using them.
 * - A live source's channel answers a `live` session: its stream is `presentation.url`, from the
 *   live edge, and `live` is the stretch that can be rewound. A device that cannot play the stream
 *   the server chose for it (`plays`) is told so plainly and the session is stopped at once.
 * - A Library Channel answers ordinary title playback with `channel` (the programme's start and
 *   end): the engine starts where the server says (`startPositionMs`), may go back to the
 *   programme's start and never past live, and at the programme's end the channel is tuned again
 *   for the next one.
 * - Controls: `PATCH {state}`; a Library Channel's seek is `PATCH {seek}`. Go to live on a live
 *   source reloads its stream. Reports (`PUT .../progress`) keep the lease. Stop: `DELETE`.
 */
import type {ChannelDesired, ChannelProjection, ChannelReference} from '../playback/linear-protocol.ts';
import {channelMessage} from '../playback/linear-protocol.ts';
import {PlaybackApiError, call, enc, idempotencyKey, type V1Http} from './http.ts';
import {serviceText} from '../presentation/service-text.ts';
import {SessionsClient, type StartContext, type StartPreparation, type Timers} from './sessions.ts';
import {ProgressReporter} from './progress.ts';

type ChannelEvents = Readonly<{onResync?(listener: () => void): () => void; onResyncRead?(read: (context: ResyncReadContext) => Promise<void>, options?: {priority?: number}): {dispose(): void}}>;
/** What the server chose to send this device: the stream's address, how it is delivered, and whether it was converted. */
export type ChannelPresentation = Readonly<{mode: string; route: string; url: string; container?: string}>;
export type V1ChannelOptions = Readonly<{events?: ChannelEvents; http: V1Http; key?: () => string; now?: () => number; pollMs?: number; timers?: Timers;
  /** This server's time as this device knows it (`serverNow`), for a programme's end and a tuner countdown. */
  serverNow?: () => number;
  /** Whether this device can play the stream the server chose for it. A browser cannot play a live source's own MPEG-TS. */
  plays?: (presentation: ChannelPresentation) => boolean}>;

/** What the service applies: the playback id, whether the start can't proceed, and the projection. */
export type ChannelView = Readonly<{playbackId: string; status?: string; linear: ChannelProjection}>;
export type ChannelViewSink = {state: (value: ChannelView) => void; error: (message: string, terminal: boolean) => void; preparation?: (value: StartPreparation | undefined) => void};
/** Facts of this exact original channel POST, captured before any start I/O.
 * This is not a session, cleanup authority or a permission to adopt a successor. */
export type OriginalChannelStart = Readonly<{
  body: Readonly<{channel: string; state: 'playing' | 'paused'}>;
  key: string; signal: AbortSignal; isCurrent(): boolean;
}>;
/** The host supplies purpose-specific never-installed resource custody. Its
 * predicate only narrows the original channel lifetime; finish releases that
 * host flight after settlement, without changing this start's result. */
export type ChannelStartReceipt = Readonly<{
  isCurrent(): boolean;
  capturePreparing: NonNullable<StartContext['capturePreparing']>;
  /** Synchronously claim this actual resource before internal installation. Only literal true
   * permits installation; the host shares its original registration with every current/in-flight
   * channel start. */
  claimInstall(session: Readonly<{id: string; generation: number}>): boolean;
  /** Facts of a late actual 200/201, never a fabricated Session. The host
   * re-proves this original principal/id/generation and never-installed
   * registration before any bounded cleanup. No raw DELETE fallback. */
  retireUninstalled(session: Readonly<{id: string; generation: number}>): void | Promise<void>;
  finish(): void | Promise<void>;
}>;
export type ChannelStartOptions = Readonly<{state?:'playing'|'paused'; signal?: AbortSignal; isCurrent?: () => boolean; capturePreparing?: StartContext['capturePreparing']; captureStart?: (original: OriginalChannelStart) => ChannelStartReceipt}>;

/** The service's channel control. */
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

/** Whether the server plays channels: the rebuilt server always does, unless it says otherwise. */
export function serverSupportsV1Channels(capabilities: unknown): boolean {
  const features = obj(capabilities) && obj(capabilities.features) ? capabilities.features : undefined;
  return features?.playback_v1_channels !== 'disabled';
}

type Session = Readonly<{
  id: string;
  /** `live`: a live source's channel. `title`: a Library Channel, played as its programme's title. */
  kind: 'live' | 'title';
  state: string;
  itemId?: string;
  presentation: Readonly<{generation: number; mode: string; route: string; url: string; container?: string; startPositionMs: number}>;
  /** A Library Channel's programme: where it started and where it ends, as server instants. */
  channel?: Readonly<{id: string; startsAtMs?: number; endsAtMs?: number; maxKbps?: number}>;
  /** A live channel's rewind stretch, as server instants. */
  window?: Readonly<{oldestAtMs: number; liveAtMs: number}>;
  /** When a recording takes this stream's tuner, as a server instant, and why. */
  countdown?: Readonly<{endsAtMs: number; reason: string}>;
  end?: Readonly<{reason: string; message?: string}>;
}>;

const obj = (v: unknown): v is Record<string, any> => !!v && typeof v === 'object' && !Array.isArray(v);
const int = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v);
const us = (ms: number) => String(Math.round(ms) * 1000);
const instant = (v: unknown): number | undefined => {
  if (typeof v !== 'string') return undefined;
  const ms = Date.parse(v);
  return Number.isFinite(ms) ? ms : undefined;
};
/** How far behind the live edge a playing viewer still counts as watching at live. */
const LIVE_SLACK_MS = 10_000;
/** How near a programme's end the engine's position counts as having reached it. */
const END_SLACK_MS = 1_000;
/** A day: the reach of a live stream's window as the player sees it, so a long watch never leaves it. */
const DAY_MS = 86_400_000;

/** Reads a rebuilt playback session for a channel, as it arrives. */
export function parseChannelSession(v: unknown): Session {
  if (!obj(v) || typeof v.id !== 'string' || !v.id || typeof v.state !== 'string' || !obj(v.presentation) || typeof v.presentation.url !== 'string') throw new Error('Unreadable channel session.');
  const p = v.presentation, kind = v.kind === 'live' ? 'live' : 'title';
  // A live session's stream exists as soon as it starts; the server numbers no generation for it.
  const generation = int(p.generation) && p.generation > 0 ? p.generation : kind === 'live' && p.url ? 1 : 0;
  const channel = obj(v.channel) && typeof v.channel.id === 'string' ? Object.freeze({id: v.channel.id,
    ...(instant(v.channel.startsAt) !== undefined ? {startsAtMs: instant(v.channel.startsAt)} : {}),
    ...(instant(v.channel.endsAt) !== undefined ? {endsAtMs: instant(v.channel.endsAt)} : {}),
    ...(int(v.channel.maxKbps) && v.channel.maxKbps > 0 ? {maxKbps: v.channel.maxKbps} : {})}) : undefined;
  const oldest = obj(v.live) ? instant(v.live.oldestAt) : undefined, live = obj(v.live) ? instant(v.live.liveAt) : undefined;
  const countdownAt = obj(v.countdown) ? instant(v.countdown.endsAt) : undefined;
  return Object.freeze({id: v.id, kind, state: v.state, ...(typeof v.itemId === 'string' && v.itemId ? {itemId: v.itemId} : {}),
    presentation: Object.freeze({generation, mode: typeof p.mode === 'string' ? p.mode : '', route: typeof p.route === 'string' ? p.route : '', url: p.url,
      ...(typeof p.container === 'string' && p.container ? {container: p.container} : {}), startPositionMs: int(p.startPositionMs) && p.startPositionMs > 0 ? p.startPositionMs : 0}),
    ...(channel ? {channel} : {}),
    ...(oldest !== undefined && live !== undefined ? {window: Object.freeze({oldestAtMs: oldest, liveAtMs: Math.max(oldest, live)})} : {}),
    ...(countdownAt !== undefined ? {countdown: Object.freeze({endsAtMs: countdownAt, reason: typeof v.countdown.reason === 'string' ? v.countdown.reason : ''})} : {}),
    ...(obj(v.end) && typeof v.end.reason === 'string' && v.end.reason ? {end: Object.freeze({reason: v.end.reason, ...(typeof v.end.message === 'string' && v.end.message ? {message: v.end.message} : {})})} : {})});
}

/**
 * Where this device is with a session's stream: which attachment of it (a new stream, or Go to
 * live reloading one, is a new attachment the engine joins), the position it was asked to start or
 * seek to, and the seek it has reached.
 */
export type ChannelAttachment = Readonly<{generation: number; key: string; seekId: string; targetMs: number; ackId?: string; confirmedMs?: number}>;

/** How the engine takes the stream: a file or a live source's own MPEG-TS directly, anything else as HLS. */
const engineMode = (s: Session): 'direct' | 'hls' =>
  s.kind === 'live' ? (s.presentation.route === 'original' ? 'direct' : 'hls') : s.presentation.mode === 'direct' ? 'direct' : 'hls';
/**
 * Where a live stream is joined: its live edge, now. Live HLS is read by its program dates from the window's oldest moment, so
 * the edge is that far along, and never earlier than the server's time (a window read a while ago is behind it); a live
 * source's own stream starts where this device joined it, at media time 0.
 */
const liveJoinMs = (s: Session, serverNow: number) => engineMode(s) === 'hls' && s.window ? Math.max(s.window.liveAtMs, serverNow) - s.window.oldestAtMs : 0;

/** The session as the channel projection `PlaybackService.applyChannel` reads. */
export function channelView(s: Session, channel: ChannelReference, at: ChannelAttachment, timing: Readonly<{now: number; serverNow: number}>): ChannelView {
  const generation = String(at.generation), programme = s.kind === 'title' ? s.channel : undefined;
  const start = programme?.startsAtMs, end = programme?.endsAtMs;
  // A Library Channel's timeline is its programme's title, from the programme's start; a live
  // stream's is the stream from where this device joined it.
  const originMs = start ?? s.window?.oldestAtMs ?? 0;
  // Live HLS: the rewind stretch, from its oldest moment to live now. A live source's own stream (counted from where this
  // device joined) has no stretch to draw: its window reaches a day so a long watch never leaves it.
  const datedLive = s.kind === 'live' && engineMode(s) === 'hls' && s.window ? Math.max(s.window.liveAtMs, timing.serverNow) - s.window.oldestAtMs : undefined;
  const windowEndMs = start !== undefined && end !== undefined ? Math.max(0, end - start) : datedLive ?? DAY_MS;
  const liveEdgeMs = start !== undefined ? Math.min(windowEndMs, Math.max(0, timing.serverNow - start)) : datedLive ?? (s.window ? s.window.liveAtMs - s.window.oldestAtMs : 0);
  const seek = Object.freeze({seekCommandId: at.seekId, bufferGeneration: generation, kind: 'position' as const, positionUs: us(at.targetMs)});
  const desired: ChannelDesired = Object.freeze({state: s.state === 'paused' ? 'paused' : 'playing', seek, retryRequestId: null});
  // The countdown is a server instant: moved onto this device's clock here, at receipt.
  const reservation = s.countdown ? Math.max(0, s.countdown.endsAtMs - timing.serverNow + Math.round(timing.now)) : undefined;
  const linear: ChannelProjection = Object.freeze({
    channel, name: channel.name ?? '',
    programme: programme && start !== undefined && end !== undefined ? Object.freeze({id: `${programme.id}@${start}`, itemId: s.itemId ?? '', title: '', startMs: start, endMs: end}) : null,
    next: null, desired,
    media: Object.freeze({...(reservation === undefined ? {} : {reservationRequiredAtMs: reservation}), streamUrl: s.presentation.url, mode: engineMode(s),
      // Live HLS dates its segments; a title, or a live source's own stream, counts media time from its start.
      clock: s.kind === 'live' && engineMode(s) === 'hls' ? 'dates' as const : 'media' as const,
      bufferGeneration: generation, presentationGeneration: generation, originMs, windowRevision: generation, windowStartUs: '0', windowEndUs: us(windowEndMs),
      liveEdgeUs: us(liveEdgeMs), gaps: [], state: 'playing', errorCode: '', logoUrl: channel.logoUrl ?? ''}),
    overlay: {enabled: false, corner: 'top-right', sizePercent: 8, insetPercent: 3, treatment: 'original'},
    confirmedPositionUs: at.confirmedMs !== undefined ? us(at.confirmedMs) : null,
    acknowledgedSeekCommandId: at.ackId ?? null,
    seekError: '',
  });
  return Object.freeze({playbackId: s.id, linear});
}

type Active = {wake?: () => void; serial: number; channel: ChannelReference; startAbort: AbortController; id?: string; session?: Session; at: ChannelAttachment;
  joined: boolean; reporter?: ProgressReporter; tail: Promise<unknown>; closed: boolean; retune?: ReturnType<typeof setTimeout>;
  /** Where the engine last said it was, in the stream's own time. */
  positionMs?: number;
  /** This start places the viewer at the start of the programme now on (they finished, or were away past, the one before). */
  placeAtStart?: boolean;
  /** A new tune of this channel is under way: no second one. */
  retuning?: boolean};

const message = (e: unknown): string => {
  if (e instanceof PlaybackApiError) {
    // The server names what is using every tuner (rule 25): its words, as it says them.
    if (e.code === 'no_tuner') return e.serverMessage ?? channelMessage('tuner_busy');
    if (e.code === 'not_found') return 'This channel isn’t available.';
    if (e.code === 'limit_reached' || e.code === 'stream_limit_reached') return e.serverMessage ?? 'This account is already playing as many streams as it is allowed.';
    if (e.code === 'source_sign_in') return e.serverMessage ?? 'This channel’s source needs its sign-in again.';
    if (e.code === 'session_ended') return 'This channel playback has ended. Select the channel again.';
    // Any other refusal the server explains (a server converting all it may: 429 conversion_capacity) is said in its words.
    if (e.serverMessage) return e.serverMessage;
  }
  return 'Channel playback could not connect. Check the server and retry.';
};
/** Why a channel session ended, in words: the server's message, a profile without Live TV, or the generic end. */
export const channelEndMessage = (end: Session['end']): string => {
  if (end?.reason === 'feature_restricted') return serviceText('live.offForProfile');
  if (end?.message) return end.message;
  return 'This channel playback has ended. Select the channel again.';
};
const terminal = (e: unknown) => e instanceof PlaybackApiError && ([401, 403, 404, 410].includes(e.status) || ['no_tuner', 'limit_reached', 'stream_limit_reached', 'source_sign_in', 'conversion_capacity'].includes(e.code));

/** How long a started channel may take to have its stream, and how often to look. */
export const CHANNEL_STREAM_WAIT_MS = 60_000, CHANNEL_STREAM_POLL_MS = 500;
/** The plain, terminal words for a start the host refused and one whose stream never came. */
export const CHANNEL_START_REFUSED = 'This channel could not start. Try again.', CHANNEL_START_TIMEOUT = 'This channel did not start in time. Try again.';
/** A stream this device cannot play. For now a browser cannot play a live source's own stream; the server will convert it. */
export const CHANNEL_NOT_PLAYABLE_HERE = 'This channel can’t be played in a browser yet.';

export class V1ChannelControl implements ChannelPlayback {
  private readonly http: V1Http;
  private readonly sessions: SessionsClient;
  private readonly key: () => string;
  private readonly now: () => number;
  private readonly serverNow: () => number;
  private readonly plays: (presentation: ChannelPresentation) => boolean;
  private readonly timers?: Timers;
  private readonly pollMs?: number;
  private readonly events?: ChannelEvents;
  private offResync?: () => void;
  private serial = 0;
  private active?: Active;
  private sink?: ChannelViewSink;
  private wanted: 'playing' | 'paused' = 'playing';
  /** The next start places the viewer at the start of the programme now on. */
  private placeNextAtStart = false;
  /** Attachments count across starts: a channel tuned again is a new stream the engine must join, never the old one. */
  private attachments = 0;
  private stopping: Promise<unknown> = Promise.resolve();

  constructor(options: V1ChannelOptions) {
    this.http = options.http;
    this.sessions = new SessionsClient(options.http, options.timers);
    this.key = options.key ?? (() => idempotencyKey());
    this.now = options.now ?? (() => Date.now());
    this.serverNow = options.serverNow ?? this.now;
    this.plays = options.plays ?? (() => true);
    this.timers = options.timers;
    this.pollMs = options.pollMs;
    this.events = options.events;
  }

  /** Nothing to recover: a session a crash left behind lapses with its lease. */
  recover(): Promise<void> { return Promise.resolve(); }

  private current(a: Active) { return this.active === a && !a.closed; }
  private enqueue<T>(a: Active, work: () => Promise<T>): Promise<T> {
    const result = a.tail.then(work);
    a.tail = result.catch(() => {});
    return result;
  }
  private emit(a: Active) {
    if (this.current(a) && a.session && a.joined) this.sink?.state(channelView(a.session, a.channel, a.at, {now: this.now(), serverNow: this.serverNow()}));
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
    const placeAtStart = this.placeNextAtStart;this.placeNextAtStart = false;
    const a: Active = {serial: ++this.serial, channel: originalChannel, startAbort: new AbortController(), at: {generation: 0, key: '', seekId: '', targetMs: 0}, joined: false, tail: Promise.resolve(), closed: false,
      ...(placeAtStart ? {placeAtStart} : {})};
    this.active = a;
    const currentStart = () => this.current(a) && starting() && this.current(a);
    let admitted = currentStart, finish: ChannelStartReceipt['finish'] | undefined;
    let claimInstall: ChannelStartReceipt['claimInstall'] | undefined;
    let retireUninstalled: ChannelStartReceipt['retireUninstalled'] | undefined, retiring: Promise<void> | undefined;
    const retireLate = (session: Readonly<{id: string; generation: number}>) => {
      if (!captureStart) {void this.stop(session.id).catch(error => this.sink?.error(message(error), true)); return;}
      if (retireUninstalled) {try {retiring = Promise.resolve(retireUninstalled(Object.freeze({id: session.id, generation: session.generation}))).catch(() => {});} catch { /* Original host retains cleanup uncertainty; never downgrade it. */ }}
    };
    const cancel = () => {if (this.current(a)) this.close();};
    signal?.addEventListener('abort', cancel, {once: true});
    try {
      const stopping = this.stopping;
      const body = Object.freeze({channel: originalChannel.channelId, state});
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
      // A new channel waits for the previous stop, so a one-tuner source isn't refused.
      await stopping.catch(() => {});
      if (!admitted()) {cancel();return;}
      const result = await this.sessions.startResponse({channel: body.channel}, {state: body.state}, key, {
        signal: a.startAbort.signal, isCurrent: admitted, capturePreparing: captureStart ? originalPreparing : capturePreparing,
        onPreparing: (_wait, preparation) => {if (admitted()) sink.preparation?.(preparation);},
      }, body => parseChannelSession(body), body => {
        // As the server answered it: a live session numbers no generation (0).
        const v = body as {id?: unknown; presentation?: {generation?: unknown}} | null;
        if (!v || typeof v.id !== 'string' || !v.id) throw new Error('Unreadable channel session.');
        return {id: v.id, generation: int(v.presentation?.generation) && v.presentation.generation >= 0 ? v.presentation.generation : 0};
      });
      if (!admitted()) {retireLate(result);cancel();return;}
      const s = result.body;
      sink.preparation?.(undefined);
      if (!admitted()) {retireLate(result);cancel();return;}
      let install = !captureStart;
      // The host's receipt names the session as the start answered it (a live session answers generation 0).
      try {if (claimInstall) install = claimInstall(Object.freeze({id: result.id, generation: result.generation})) === true;} catch { /* A host refusal cannot install or downgrade its cleanup proof. */ }
      if (!install || !admitted()) {
        const refused = !install && admitted();
        retireLate(result);cancel();
        // An admitted start the host refused to install is a failure, never a silent no-op
        // (P2-74 E-web-25): the caller draws its error instead of waiting forever.
        if (refused) sink.error(CHANNEL_START_REFUSED, true);
        return;
      }
      a.id = s.id;
      // A stream this device cannot play is said plainly, and its tuner is given back at once.
      if (s.presentation.url && !this.plays(s.presentation)) {this.close(); sink.error(CHANNEL_NOT_PLAYABLE_HERE, true); return;}
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
        // A start answered before its stream exists (generation 0, no address) is followed until
        // the server has the stream, then joined; never waited on silently forever.
        const deadline = this.now() + CHANNEL_STREAM_WAIT_MS;
        // One pending wait at a time, cleared at once when the channel closes (Stop, a newer start).
        const wait = (ms: number) => new Promise<void>(resolve => {
          const timer = setTimeout(() => {a.wake = undefined; resolve();}, ms);
          (timer as {unref?: () => void}).unref?.();
          a.wake = () => {clearTimeout(timer); a.wake = undefined; resolve();};
        });
        while (currentStart() && !a.joined) {
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
        if (admitted()) {
          this.active = undefined; a.closed = true; a.startAbort.abort();
          // A session that started before the failure is stopped, so its tuner is not held.
          if (a.id) void this.stop(a.id).catch(() => {});
          sink.error(message(e), true);
        }
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
      this.clearRetune(a);
      this.active = undefined;
      this.sink?.error(channelEndMessage(s.end), true);
      return;
    }
    a.session = s;
    const key = `${s.id}:${s.presentation.generation}:${s.presentation.url}`;
    if (key !== a.at.key) {
      // Catch-up never makes the first join; the start does, once the stream exists.
      if (observational && !a.joined) return;
      if (!s.presentation.url || s.presentation.generation <= 0) return;
      // A new stream (the start, or the server's new presentation): the engine joins it where the
      // server starts it; a live stream from its live edge.
      const fromStart = !a.joined && a.placeAtStart && s.kind === 'title';
      a.at = Object.freeze({generation: ++this.attachments, key, seekId: this.key(), targetMs: fromStart ? 0 : s.kind === 'title' ? s.presentation.startPositionMs : liveJoinMs(s, this.serverNow())});
      a.joined = true;
      // At the start of the programme now on (11 rule 109's oldest point): the server is told where the viewer is.
      if (fromStart) void this.patch(a, {state: this.wanted, seek: {id: a.at.seekId, positionMs: 0}});
      this.scheduleRetune(a, s);
    }
    if (a.reporter && a.reporter.snapshot().generation !== s.presentation.generation) {
      a.reporter.setGeneration(s.presentation.generation);
      a.reporter.observe({generation: s.presentation.generation, state: this.wanted, positionMs: a.at.confirmedMs ?? a.at.targetMs, rate: this.wanted === 'playing' ? 1 : 0}, 'state');
    }
    this.emit(a);
  }

  /**
   * A Library Channel's programme ends on the clock. A viewer at live changes programme with it. A viewer behind live
   * (paused or rewound) keeps the programme they are in until they reach its end (11 rules 27 and 109).
   */
  private scheduleRetune(a: Active, s: Session) {
    this.clearRetune(a);
    const end = s.kind === 'title' ? s.channel?.endsAtMs : undefined;
    if (end === undefined) return;
    // An end this device's view of the server's clock already passed is looked at a second later, never in a tight loop.
    const left = end - this.serverNow(), delay = left > 0 ? left : 1_000;
    a.retune = setTimeout(() => {
      a.retune = undefined;
      if (this.current(a) && this.atLive(a)) this.retune(a, false);
    }, delay);
    (a.retune as {unref?: () => void}).unref?.();
  }
  /** Whether this viewer is watching a Library Channel at live: playing, and within a few seconds of the live edge. */
  private atLive(a: Active): boolean {
    const start = a.session?.channel?.startsAtMs;
    if (this.wanted !== 'playing' || start === undefined) return false;
    return (a.positionMs ?? a.at.targetMs) >= this.serverNow() - start - LIVE_SLACK_MS;
  }
  /** The length of the programme this viewer is in, when it has one. */
  private programmeMs(a: Active): number | undefined {
    const c = a.session?.kind === 'title' ? a.session.channel : undefined;
    return c?.startsAtMs !== undefined && c.endsAtMs !== undefined ? c.endsAtMs - c.startsAtMs : undefined;
  }
  /** Tune the channel again: at live with the clock, or at the start of the programme now on for a viewer who finished one. */
  private retune(a: Active, atStart: boolean) {
    const sink = this.sink;
    if (!this.current(a) || !sink || a.retuning) return;
    a.retuning = true;
    this.placeNextAtStart = atStart;
    void this.open(a.channel, sink, {state: this.wanted}).catch(() => {});
  }
  private clearRetune(a: Active) { if (a.retune !== undefined) {clearTimeout(a.retune); a.retune = undefined;} }

  private async read(a: Active, signal?: AbortSignal, observational = false) {
    if (!a.id || !this.current(a) || signal?.aborted) return;
    const r = await call(this.http, {method: 'GET', path: encodedRoutePath("get_playback_session", {"id": enc(a.id)}), signal});
    if (!signal?.aborted) this.accept(a, parseChannelSession(r.body), observational);
  }

  /** Progress keeps the session alive; a later presentation named in its answer is read again.
   * A failed report is replaced by the next one. */
  private reporterFor(a: Active, id: string): ProgressReporter {
    return new ProgressReporter({
      send: report => this.sessions.progress(id, report),
      onNewer: () => { if (this.current(a)) void this.read(a).catch(() => {}); },
      onEnded: () => { if (this.current(a)) void this.read(a).catch(e => { if (this.current(a)) { this.sink?.error(message(e), true); this.active = undefined; } }); },
      onConnection: healthy => { if (!healthy && this.current(a)) this.sink?.error(message(undefined), false); },
      // Reports run on real time, like the channel's own clock; pollMs fixes their cadence for hosts and tests that need it.
      ...(this.pollMs !== undefined ? {everyMs: this.pollMs} : {}),
    });
  }

  private async patch(a: Active, body: Record<string, unknown>) {
    return this.enqueue(a, async () => {
      if (!a.id || !a.session || !this.current(a)) return;
      try {
        const r = await call(this.http, {method: 'PATCH', path: encodedRoutePath("change_playback_session", {"id": enc(a.id)}), body});
        this.accept(a, parseChannelSession(r.body));
      } catch (e) {
        if (!this.current(a)) return;
        // A Library Channel whose session lapsed while the viewer was away: they come back at the start of the programme now on.
        if (a.session.kind === 'title' && e instanceof PlaybackApiError && (e.status === 404 || e.status === 410)) {this.retune(a, true);return;}
        try { await this.read(a); } catch { /* the next tick reads again */ }
        this.sink?.error(message(e), terminal(e));
      }
    });
  }

  /** The engine goes to a position of this attachment: a new seek it acknowledges when it gets there. */
  private moveTo(a: Active, ms: number) {
    a.at = Object.freeze({...a.at, seekId: this.key(), targetMs: Math.max(0, Math.round(ms)), ackId: undefined, confirmedMs: undefined});
    this.emit(a);
  }
  /** A live stream joined again from its live edge: a new attachment of the same stream. */
  private reload(a: Active) {
    a.at = Object.freeze({generation: ++this.attachments, key: a.at.key, seekId: this.key(), targetMs: a.session ? liveJoinMs(a.session, this.serverNow()) : 0});
    this.emit(a);
  }

  pause() { this.wanted = 'paused'; const a = this.active; if (a) void this.patch(a, {state: 'paused'}); }
  resume() { this.wanted = 'playing'; const a = this.active; if (a) void this.patch(a, {state: 'playing'}); }
  seek(seconds: number) {
    const a = this.active, s = a?.session;
    if (!a || !s || !Number.isFinite(seconds)) return;
    if (s.kind === 'live') {
      // Within a live HLS stream the engine moves on its own; a live source's own stream cannot be
      // moved in (rewinding further back comes with the stream the server converts).
      if (engineMode(s) === 'hls') this.moveTo(a, seconds * 1000);
      return;
    }
    // A Library Channel goes back as far as its programme's start and never past live: the server
    // holds that bound, and the engine goes where it was asked.
    this.moveTo(a, seconds * 1000);
    void this.patch(a, {state: this.wanted, seek: {id: a.at.seekId, positionMs: a.at.targetMs}});
  }
  goLive() {
    this.wanted = 'playing';
    const a = this.active, s = a?.session;
    if (!a || !s) return;
    if (s.kind === 'live') {this.reload(a); void this.patch(a, {state: 'playing'}); return;}
    const start = s.channel?.startsAtMs, end = s.channel?.endsAtMs;
    if (start === undefined) {void this.patch(a, {state: 'playing'}); return;}
    // Behind live in a programme that has already ended on the clock: live is in the programme now on.
    if (end !== undefined && this.serverNow() >= end) {this.retune(a, false); return;}
    this.moveTo(a, this.serverNow() - start);
    void this.patch(a, {state: 'playing', seek: {id: a.at.seekId, positionMs: a.at.targetMs}});
  }
  /** Play again: the server's retry; a live stream is joined again from live. */
  retry() {
    const a = this.active;
    if (!a) return;
    if (a.session?.kind === 'live') this.reload(a);
    void this.patch(a, {state: 'playing'});
  }

  /** Where the engine is (a seek it reached, or ordinary progress). The latest wins: the next
   * sample (within a second) carries it, and a reached seek is sent at once. */
  async observe(seconds: number, seekId: string | null = null, _errorCode: string | null = null): Promise<boolean> {
    const a = this.active, s = a?.session;
    if (!a?.id || !s || !a.reporter || !this.current(a) || !Number.isFinite(seconds)) return false;
    if (seekId && a.at.seekId !== seekId) return false;
    const positionMs = Math.max(0, Math.round(seconds * 1000));
    a.positionMs = positionMs;
    a.reporter.observe({generation: s.presentation.generation, state: this.wanted, positionMs, rate: this.wanted === 'playing' ? 1 : 0}, seekId ? 'seeked' : 'time');
    // A viewer who reaches the end of a Library Channel's programme after it ended on the clock goes on at the start of the
    // programme now on; one at live is changed with the clock (scheduleRetune).
    const length = this.programmeMs(a), end = s.channel?.endsAtMs;
    if (length !== undefined && end !== undefined && positionMs >= length - END_SLACK_MS && this.serverNow() >= end) this.retune(a, true);
    if (seekId) {
      a.at = Object.freeze({...a.at, ackId: seekId, confirmedMs: positionMs});
      this.emit(a);
      await a.reporter.flush();
    } else if (s.kind === 'live' && engineMode(s) === 'hls') this.emit(a); // live moves on: the window's edge follows the server's time

    return this.current(a);
  }

  private stop(id: string): Promise<unknown> {
    const work = call(this.http, {method: 'DELETE', path: encodedRoutePath("stop_playback_session", {"id": enc(id)}), body: {}}, [200, 204]).catch(error => {
      // No answer: the outbox keeps the stop and sends it when the server is back.
      if ((error as {code?: unknown} | null)?.code === 'sync_pending') return undefined;
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
    this.clearRetune(a);
    a.startAbort.abort();
    sink?.preparation?.(undefined);
    a.reporter?.stop();
    // Stop frees the tuner now, including a slow start whose stream has not come yet.
    if (a.id) void this.stop(a.id).catch(error => sink?.error(message(error), true));
  }
}
