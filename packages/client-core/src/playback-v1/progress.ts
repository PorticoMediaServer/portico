/**
 * Progress: the playing device tells the server where it is (`PUT /v1/playback/sessions/{id}/progress`).
 *
 * - The server says how often to report (`reportEveryMs`: ten seconds while playing) and how long
 *   the session lives without a word from the device. The reporter follows what it last heard.
 * - The latest reading wins. One report is in flight at a time, a failed one is simply replaced
 *   by the next, and nothing queues. A pause, an end or an error is reported at once.
 * - `playClockMs` is the wall time actually spent playing in this session (not buffering or
 *   paused, never scaled by the rate): the server credits watched time from it.
 * - A session the server no longer knows (it restarted: 404) or has ended (410) stops the
 *   reporter and tells its owner, which starts the title again where it was or ends it.
 * - An answer naming a later presentation than the one playing (the server moved the session to
 *   another way of playing it) tells the owner to read the session again.
 */
import {GenerationGate} from './generation.ts';
import type {PlayerObservation, PlayerState} from './port.ts';
import {serverNow} from '../server-clock.ts';

export type ProgressState = 'playing' | 'paused' | 'buffering' | 'ended' | 'error';
export type ProgressReport = Readonly<{state: ProgressState; positionMs: number; playClockMs: number}>;
/** What the server answers a report with. */
export type ProgressAnswer = Readonly<{expiresAt?: string; reportEveryMs?: number; generation?: number}>;

export type ProgressReporterOptions = Readonly<{
  send: (report: ProgressReport) => Promise<ProgressAnswer>;
  /** How often to report until the server says otherwise (the session's `lease.reportEveryMs`). */
  everyMs?: number;
  /** The server no longer knows the session (404) or has ended it (410). */
  onEnded?: (status: 404 | 410) => void;
  /** The server is on a later presentation of this session than the one being played. */
  onNewer?: (generation: number) => void;
  /** Sustained failure (false) and recovery (true); one failed report says nothing. */
  onConnection?: (healthy: boolean) => void;
  /** Nothing has been accepted for most of the session's life: the owner may restart from here. */
  onLeaseRisk?: () => void;
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
}>;

export type ProgressSnapshot = Readonly<{
  generation?: number;
  failures: number;
  healthy: boolean;
  stopped: boolean;
  /** The state of the last report the server accepted. */
  lastAccepted?: ProgressState;
  playClockMs: number;
}>;

const reportable: Record<PlayerState, ProgressState | undefined> = {idle: undefined, loading: 'buffering', playing: 'playing', paused: 'paused', buffering: 'buffering', ended: 'ended', error: 'error'};
const unhealthyAfterMs = 10_000;
const unhealthyAfterFailures = 3;

function status(error: unknown): number | undefined {
  const e = error as {status?: unknown} | null;
  return typeof e?.status === 'number' ? e.status : undefined;
}

export class ProgressReporter {
  private readonly gate = new GenerationGate();
  private latest?: PlayerObservation;
  private sentState?: ProgressState;
  private everyMs = 10_000;
  private playClock = 0;
  private playingSince?: number;
  private lastAccepted?: ProgressState;
  private leaseUntil?: number;
  private failures = 0;
  private firstFailure?: number;
  private healthy = true;
  private leaseWarned = false;
  private stopped = false;
  private inFlight?: Promise<void>;
  private timer?: unknown;
  private readonly now: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (timer: unknown) => void;
  private readonly o: ProgressReporterOptions;

  constructor(o: ProgressReporterOptions) {
    this.o = o;
    this.now = o.now ?? (() => globalThis.performance?.now() ?? Date.now());
    if (o.everyMs !== undefined && Number.isFinite(o.everyMs) && o.everyMs >= 1000) this.everyMs = o.everyMs;
    // A reporter never keeps a process alive by itself (Node hosts, tests); browsers have no unref.
    this.setTimer = o.setTimer ?? ((fn, ms) => { const t = setTimeout(fn, ms); (t as {unref?: () => void}).unref?.(); return t; });
    this.clearTimer = o.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
  }

  snapshot(): ProgressSnapshot {
    return Object.freeze({generation: this.gate.value, failures: this.failures, healthy: this.healthy, stopped: this.stopped, ...(this.lastAccepted ? {lastAccepted: this.lastAccepted} : {}), playClockMs: this.clock()});
  }

  /** A new presentation is in force: forget readings of older ones. */
  setGeneration(generation: number): void {
    if (this.stopped || !this.gate.advance(generation)) return;
    this.mark();
    this.latest = undefined;
  }

  /** A player reading. A change of state (a pause, an end, an error) is reported at once. */
  observe(observation: PlayerObservation, _cause: 'time' | 'state' | 'seeked' = 'time'): void {
    if (this.stopped || !this.gate.accepts(observation.generation)) return;
    const state = reportable[observation.state];
    if (!state) return;
    this.mark();
    this.latest = observation;
    this.playingSince = state === 'playing' ? this.now() : undefined;
    // Buffering after a seek or at the start is not news; anything else that changed is.
    if (this.sentState !== undefined && state !== this.sentState && state !== 'buffering') void this.tick();
    else this.schedule();
  }

  /** The player failed on the current presentation: say so with the next report. */
  failed(generation: number): void {
    if (this.stopped || !this.gate.accepts(generation) || !this.latest) return;
    this.mark();
    this.latest = Object.freeze({...this.latest, state: 'error'});
    this.playingSince = undefined;
    void this.tick();
  }

  /** Send the newest reading now (a stop, the page being hidden): one attempt, never a retry. */
  flush(): Promise<void> {
    return this.tick();
  }

  /** No more reports for this session. */
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
    // The first report goes soon after playing begins, so the server hears of it; then at the server's pace.
    const wait = this.sentState === undefined ? Math.min(this.everyMs, 2000) : this.everyMs;
    this.timer = this.setTimer(() => { this.timer = undefined; void this.tick().finally(() => this.schedule()); }, wait);
  }

  private tick(): Promise<void> {
    if (this.stopped) return Promise.resolve();
    if (this.inFlight) return this.inFlight;
    if (this.timer !== undefined) { this.clearTimer(this.timer); this.timer = undefined; }
    this.inFlight = this.send().finally(() => { this.inFlight = undefined; this.schedule(); });
    return this.inFlight;
  }

  private async send() {
    const o = this.latest, generation = this.gate.value;
    if (!o || generation === undefined || o.generation !== generation) return;
    const state = reportable[o.state];
    if (!state) return;
    const report: ProgressReport = Object.freeze({state, positionMs: Math.max(0, Math.round(o.positionMs)), playClockMs: this.clock()});
    this.sentState = state;
    try {
      const answer = await this.o.send(report);
      if (this.stopped) return;
      this.lastAccepted = state;
      this.failures = 0;
      this.firstFailure = undefined;
      this.leaseWarned = false;
      if (!this.healthy) { this.healthy = true; this.o.onConnection?.(true); }
      if (typeof answer.reportEveryMs === 'number' && Number.isSafeInteger(answer.reportEveryMs) && answer.reportEveryMs >= 1000) this.everyMs = answer.reportEveryMs;
      const until = answer.expiresAt ? Date.parse(answer.expiresAt) : NaN;
      if (Number.isFinite(until)) this.leaseUntil = until;
      if (typeof answer.generation === 'number' && answer.generation > generation) this.o.onNewer?.(answer.generation);
      if (state === 'ended') this.stop();
    } catch (error) {
      if (!this.stopped) this.failure(error);
    }
  }

  private failure(error: unknown) {
    const s = status(error);
    if (s === 404 || s === 410) {
      this.stop();
      this.o.onEnded?.(s);
      return;
    }
    const now = this.now();
    this.failures++;
    this.firstFailure ??= now;
    if (this.healthy && this.failures >= unhealthyAfterFailures && now - this.firstFailure >= unhealthyAfterMs) {
      this.healthy = false;
      this.o.onConnection?.(false);
    }
    // The session's life is a server wall-clock deadline, read against the server's clock.
    if (!this.leaseWarned && this.leaseUntil !== undefined && serverNow() >= this.leaseUntil - 30_000) {
      this.leaseWarned = true;
      this.o.onLeaseRisk?.();
    }
  }
}
