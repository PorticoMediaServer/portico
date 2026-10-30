/**
 * The observation reporter (P2-20): the playing device says where it is about once a second and
 * the latest wins.
 *
 * - One claim per occurrence returns the epoch the samples carry. A restarted server or a
 *   handover answers `observation_epoch_changed`; the reporter claims again, naming its old epoch.
 * - Each tick sends the newest reading, once, with a short deadline. A tick never waits for an
 *   earlier one and is never resent: a failed one is simply replaced by the next. Nothing queues.
 * - The device reports its own clocks: `monotonicMs` since the claim and `playClockMs`, the wall
 *   time actually spent playing (not buffering or paused, never scaled by the rate). The server
 *   credits watched time from them, bounded by its own elapsed time.
 * - Failure is quiet: the connection is reported unhealthy only after 10 s and 5 failed samples
 *   in a row, and healthy again on the next success.
 * - Commands (pause, seek, stop) are not observations: they keep their own reliable path, and the
 *   server writes the latest sample before applying them.
 */
import {GenerationGate} from './generation.ts';
import {parseObservedPlayback, type ObservedPlayback} from './observed-playback.ts';
import type {PlayerObservation, PlayerState} from './port.ts';
import type {Session} from './types.ts';

export type ObservationState = 'playing' | 'paused' | 'buffering' | 'ended' | 'error';

export type ObservationSample = Readonly<{
  epoch: string;
  seq: number;
  generation: number;
  state: ObservationState;
  positionMs: number;
  partIndex?: number;
  rate: number;
  monotonicMs: number;
  playClockMs: number;
  seekSerial: number;
  bufferedMs?: number;
  bandwidthKbps?: number;
  droppedFrames?: number;
  audioTrackId?: string;
  subtitleTrackId?: string | null;
  skipped?: Readonly<{markerId: string; mode: 'automatic' | 'manual'; positionMs: number}>;
  error?: Readonly<{code: string; detail?: string}>;
  observedPlayback?: ObservedPlayback;
  viewTag?: string;
}>;

export type ObserverGrant = Readonly<{epoch: string; everyMs: number; leaseUntil?: string}>;
export type ObservationAck<S = Session> = Readonly<{acceptedSeq: number; leaseUntil?: string; viewTag?: string; session?: S}>;

export type ObservationReporterOptions<S = Session> = Readonly<{
  claim: (body: Readonly<{generation: number; replaces?: string}>) => Promise<ObserverGrant>;
  send: (sample: ObservationSample) => Promise<ObservationAck<S>>;
  /** A channel's current view arrived with an ack (only when it changed). */
  onView?: (session: S) => void;
  /** The durable lease the server last wrote. */
  onLease?: (leaseUntil: string) => void;
  onEnded?: () => void;
  /** Sustained failure (false) and recovery (true); one failed sample says nothing. */
  onConnection?: (healthy: boolean) => void;
  /** No sample has been written for most of the lease: the controller may restart from here. */
  onLeaseRisk?: () => void;
  /** A fixed cadence instead of the server's (hosts that pace their own samples, tests). */
  everyMs?: number;
  /** The epoch of the owner this one takes over from (a native owner handing back). */
  replaces?: string;
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
}>;

export type ObservationSnapshot = Readonly<{
  epoch?: string;
  seq: number;
  generation?: number;
  failures: number;
  healthy: boolean;
  stopped: boolean;
  /** The state of the last sample the server accepted. */
  lastAccepted?: ObservationState;
  playClockMs: number;
  seekSerial: number;
}>;

const reportable: Record<PlayerState, ObservationState | undefined> = {idle: undefined, loading: 'buffering', playing: 'playing', paused: 'paused', buffering: 'buffering', ended: 'ended', error: 'error'};

function code(error: unknown): string | undefined {
  const e = error as {code?: unknown} | null;
  return typeof e?.code === 'string' ? e.code : undefined;
}

function status(error: unknown): number | undefined {
  const e = error as {status?: unknown} | null;
  return typeof e?.status === 'number' ? e.status : undefined;
}

const unhealthyAfterMs = 10_000;
const unhealthyAfterFailures = 5;

export class ObservationReporter<S = Session> {
  private readonly gate = new GenerationGate();
  private latest?: PlayerObservation;
  private epoch?: string;
  private replaces?: string;
  private claimedAt = 0;
  private everyMs = 1000;
  private seq = 0;
  private seekSerial = 0;
  private playClock = 0;
  private playingSince?: number;
  private pendingSkip?: ObservationSample['skipped'];
  private pendingError?: ObservationSample['error'];
  private viewTag?: string;
  private lastAccepted?: ObservationState;
  private leaseUntil?: number;
  private failures = 0;
  private firstFailure?: number;
  private healthy = true;
  private leaseWarned = false;
  private stopped = false;
  private claiming?: Promise<void>;
  private inFlight?: Promise<void>;
  private timer?: unknown;
  private readonly now: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (timer: unknown) => void;

  private readonly o: ObservationReporterOptions<S>;

  constructor(o: ObservationReporterOptions<S>) {
    this.o = o;
    this.now = o.now ?? (() => globalThis.performance?.now() ?? Date.now());
    if (o.everyMs !== undefined && Number.isFinite(o.everyMs) && o.everyMs > 0) this.everyMs = o.everyMs;
    if (o.replaces) this.replaces = o.replaces;
    // A reporter never keeps a process alive by itself (Node hosts, tests); browsers have no unref.
    this.setTimer = o.setTimer ?? ((fn, ms) => { const t = setTimeout(fn, ms); (t as {unref?: () => void}).unref?.(); return t; });
    this.clearTimer = o.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
  }

  snapshot(): ObservationSnapshot {
    return Object.freeze({epoch: this.epoch, seq: this.seq, generation: this.gate.value, failures: this.failures, healthy: this.healthy, stopped: this.stopped, ...(this.lastAccepted ? {lastAccepted: this.lastAccepted} : {}), playClockMs: this.clock(), seekSerial: this.seekSerial});
  }

  /** A new presentation is in force: forget readings of older ones. */
  setGeneration(generation: number): void {
    if (this.stopped || !this.gate.advance(generation)) return;
    this.mark();
    this.latest = undefined;
    this.pendingError = undefined;
    this.schedule();
  }

  /** A player reading. A seek moves the seek serial; an end or error is sent at once. */
  observe(observation: PlayerObservation, cause: 'time' | 'state' | 'seeked' = 'time'): void {
    if (this.stopped || !this.gate.accepts(observation.generation) || !reportable[observation.state]) return;
    if (cause === 'seeked') this.seekSerial++;
    let output: ObservedPlayback | undefined;
    try { output = parseObservedPlayback(observation.observedPlayback); }
    catch { output = Object.freeze({}); } // Invalid output never interrupts reporting.
    this.mark();
    this.latest = Object.freeze({...observation, observedPlayback: output});
    this.playingSince = observation.state === 'playing' ? this.now() : undefined;
    const state = reportable[observation.state];
    // Claim on the first reading, so the device's clocks start with the playback.
    if (!this.epoch && !this.claiming) void this.ensureClaim(observation.generation);
    if (state === 'ended' || state === 'error') void this.tick();
    else this.schedule();
  }

  /** The viewer skipped a marker: carried by every sample until one is accepted. */
  skipped(marker: NonNullable<ObservationSample['skipped']>): void {
    if (!this.stopped) this.pendingSkip = marker;
  }

  /** A client-side failure for the current presentation. */
  failed(generation: number, failure: string, detail?: string): void {
    if (this.stopped || !this.gate.accepts(generation)) return;
    this.pendingError = {code: failure.slice(0, 64), ...(detail ? {detail: detail.slice(0, 500)} : {})};
    if (this.latest) { this.mark(); this.latest = {...this.latest, state: 'error'}; this.playingSince = undefined; }
    void this.tick();
  }

  /** Send the newest reading now (before backgrounding): one attempt, never a retry. */
  flush(): Promise<void> {
    return this.tick();
  }

  /** No more samples for this session. */
  stop(): void {
    this.stopped = true;
    if (this.timer !== undefined) this.clearTimer(this.timer);
    this.timer = undefined;
  }

  /** Fold time spent playing since the last reading into the play clock. */
  private mark() {
    if (this.playingSince !== undefined) {
      const now = this.now();
      this.playClock += Math.max(0, now - this.playingSince);
      this.playingSince = now;
    }
  }

  private clock(): number {
    let played = this.playClock;
    if (this.playingSince !== undefined) played += Math.max(0, this.now() - this.playingSince);
    return Math.floor(played);
  }

  private schedule() {
    if (this.stopped || this.timer !== undefined || !this.latest) return;
    this.timer = this.setTimer(() => { this.timer = undefined; void this.tick().finally(() => this.schedule()); }, this.everyMs);
  }

  private async ensureClaim(generation: number): Promise<boolean> {
    if (this.epoch) return true;
    this.claiming ??= (async () => {
      try {
        const grant = await this.o.claim({generation, ...(this.replaces ? {replaces: this.replaces} : {})});
        if (this.stopped) return;
        // The device's clocks for this epoch start now; play before the claim is not evidence.
        this.mark();
        this.epoch = grant.epoch;
        this.replaces = undefined;
        this.claimedAt = this.now();
        this.playClock = 0;
        if (this.playingSince !== undefined) this.playingSince = this.claimedAt;
        this.seq = 0;
        if (this.o.everyMs === undefined && Number.isSafeInteger(grant.everyMs) && grant.everyMs >= 250) this.everyMs = grant.everyMs;
        this.lease(grant.leaseUntil);
      } catch (error) {
        this.failure(error, true);
      } finally {
        this.claiming = undefined;
      }
    })();
    await this.claiming;
    return !!this.epoch && !this.stopped;
  }

  private build(): ObservationSample | undefined {
    const o = this.latest, generation = this.gate.value, epoch = this.epoch;
    if (!o || !epoch || generation === undefined || o.generation !== generation) return undefined;
    const state = reportable[o.state];
    if (!state) return undefined;
    const monotonicMs = Math.max(0, Math.floor(this.now() - this.claimedAt));
    return Object.freeze({
      epoch, seq: ++this.seq, generation, state, positionMs: Math.max(0, Math.round(o.positionMs)),
      rate: Number.isFinite(o.rate) && o.rate >= 0 && o.rate <= 4 ? o.rate : 0,
      monotonicMs, playClockMs: Math.min(this.clock(), monotonicMs), seekSerial: this.seekSerial,
      ...(o.observedPlayback !== undefined ? {observedPlayback: o.observedPlayback} : {}),
      ...(o.partIndex !== undefined ? {partIndex: o.partIndex} : {}),
      ...(o.bufferedMs !== undefined ? {bufferedMs: Math.max(0, Math.round(o.bufferedMs))} : {}),
      ...(o.bandwidthKbps !== undefined ? {bandwidthKbps: Math.max(0, Math.round(o.bandwidthKbps))} : {}),
      ...(o.droppedFrames !== undefined ? {droppedFrames: Math.max(0, Math.round(o.droppedFrames))} : {}),
      ...(o.audioTrackId !== undefined ? {audioTrackId: o.audioTrackId} : {}),
      ...(o.subtitleTrackId !== undefined ? {subtitleTrackId: o.subtitleTrackId} : {}),
      ...(this.pendingSkip ? {skipped: this.pendingSkip} : {}),
      ...(this.pendingError ? {error: this.pendingError} : {}),
      ...(this.viewTag ? {viewTag: this.viewTag} : {}),
    });
  }

  /** One sample, if none is in flight: the latest wins and nothing waits. */
  private tick(): Promise<void> {
    if (this.stopped) return Promise.resolve();
    if (this.inFlight) return this.inFlight;
    this.inFlight = this.send().finally(() => { this.inFlight = undefined; });
    return this.inFlight;
  }

  private async send() {
    const generation = this.gate.value;
    if (generation === undefined || !this.latest || !(await this.ensureClaim(generation))) return;
    const sample = this.build();
    if (!sample) return;
    try {
      const ack = await this.o.send(sample);
      if (this.stopped) return;
      this.lastAccepted = sample.state;
      this.failures = 0;
      this.firstFailure = undefined;
      this.leaseWarned = false;
      if (!this.healthy) { this.healthy = true; this.o.onConnection?.(true); }
      if (this.pendingSkip === sample.skipped) this.pendingSkip = undefined;
      if (this.pendingError === sample.error) this.pendingError = undefined;
      if (ack.viewTag) this.viewTag = ack.viewTag;
      this.lease(ack.leaseUntil);
      if (ack.session) this.o.onView?.(ack.session);
      if (sample.state === 'ended') this.stop();
    } catch (error) {
      if (!this.stopped) this.failure(error, false);
    }
  }

  private lease(leaseUntil: string | undefined) {
    const at = leaseUntil ? Date.parse(leaseUntil) : NaN;
    if (!Number.isFinite(at)) return;
    this.leaseUntil = at;
    this.o.onLease?.(leaseUntil as string);
  }

  private failure(error: unknown, claiming: boolean) {
    const c = code(error), s = status(error);
    if (c === 'observation_epoch_changed') {
      // Another process took over, or the server restarted: claim again, naming this epoch.
      if (this.epoch) this.replaces = this.epoch;
      this.epoch = undefined;
      return;
    }
    // Another process on this device still reports (a handover in progress): claim again on the
    // next tick; the server grants it once that owner has been silent for a few seconds.
    if (c === 'observation_owned') return;
    if (c === 'session_ended' || s === 404 || s === 410) {
      this.stop();
      this.o.onEnded?.();
      return;
    }
    if (claiming && c === 'client_update_required') { this.stop(); return; }
    const now = this.now();
    this.failures++;
    this.firstFailure ??= now;
    if (this.healthy && this.failures >= unhealthyAfterFailures && now - this.firstFailure >= unhealthyAfterMs) {
      this.healthy = false;
      this.o.onConnection?.(false);
    }
    // The lease is a server wall-clock deadline.
    if (!this.leaseWarned && this.leaseUntil !== undefined && Date.now() >= this.leaseUntil - 30_000) {
      this.leaseWarned = true;
      this.o.onLeaseRisk?.();
    }
  }
}
