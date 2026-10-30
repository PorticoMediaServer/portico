/**
 * Plays Playback Protocol v1 sessions through `PlaybackService` and the native engines (Apple
 * AVPlayer, Android). The web no longer uses this file: it runs on `PlaybackSessionController`
 * (see the web app's `playback-v1/web-playback.ts`), and this bridge goes when the native apps
 * make the same move. `V1PlaybackApi` is the `PlaybackApi` the
 * service calls for a session's progress and stop, spoken as v1: observations (one reporter per
 * session: the latest reading once a second, never queued or resent) and `DELETE` with the last
 * position. `toPlaybackSession` turns a v1 session into the service's session shape.
 */
import type {PlaybackApi, PlaybackSession} from '../index.ts';
import {idempotencyKey, PlaybackApiError, type V1Http} from './http.ts';
import {PlaybackOptionsClient} from './options.ts';
import {SessionsClient, type SessionChange, type StartContext, type StartOptions, type StartTarget, type Timers} from './sessions.ts';
import {requireSessionChangeCurrent, type SessionChangeContext} from './change-context.ts';
import type {PlayerFailureCode} from '../client-profile.ts';
import {ObservationReporter, type ObservationSample, type ObservationState} from './observation.ts';
import {parseSessionEnd, type DiscTitleRef, type Session, type SessionEnd} from './types.ts';
import {toPlaybackSession} from './session-shape.ts';
import {QueueSelectionRegistrations, type QueueSelectionRegistration} from './selection-registration.ts';

export {toPlaybackSession};

type Tracked = {reporter: ObservationReporter; stopping?: boolean; stopPromise?: Promise<void>; generation: number; positionMs: number; state: ObservationState;
  /** When `positionMs` was last set by the player. */
  positionAt?: number};

export class V1PlaybackApi implements PlaybackApi {
  readonly sessions: SessionsClient;
  private readonly optionsClient: PlaybackOptionsClient;
  private readonly tracked = new Map<string, Tracked>();
  private readonly registrations = new QueueSelectionRegistrations();
  private readonly selectionAdoptions = new WeakMap<PlaybackSession, {id: string; registration: QueueSelectionRegistration}>();
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (t: unknown) => void;
  private readonly key: () => string;
  private readonly now: () => number;
  /** A clock and timers the caller supplied (tests); reporters otherwise keep their own. */
  private readonly clock?: () => number;
  private readonly timers?: Timers;
  private readonly random: () => number;
  private readonly endedListeners = new Set<(sessionId: string, end: SessionEnd) => void>();
  private readonly changedListeners = new Set<(session: Session) => void>();
  /** Sessions a committed audio edge ended on the server (§18.3): their last reports and end
   * notices are dropped, since the audio already belongs to the next session. */
  private readonly retired = new Set<string>();

  constructor(http: V1Http, options: {timers?: Timers; key?: () => string; now?: () => number; random?: () => number} = {}) {
    this.now = options.now ?? (() => Date.now());
    this.clock = options.now;
    this.timers = options.timers;
    this.random = options.random ?? Math.random;
    this.sessions = new SessionsClient(http, options.timers);
    this.optionsClient = new PlaybackOptionsClient(http);
    this.setTimer = options.timers?.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.timers?.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
    this.key = options.key ?? (() => idempotencyKey());
  }

  /** A title's duration from its playback options (the default version's), in seconds. */
  async duration(itemId: string, versionId?: string, signal?: AbortSignal, discTitle?: DiscTitleRef): Promise<number> {
    try {
      const o = await this.optionsClient.options(itemId, discTitle ? {versionId, discTitle} : {}, signal);
      const v = o.versions.find(x => x.id === (versionId ?? o.preferred?.versionId)) ?? (discTitle ? undefined : o.versions[0]);
      return v ? v.durationMs / 1000 : 0;
    } catch {
      return 0; // The engine reports the duration once it has the media.
    }
  }

  /** Starts a session and returns it in the service's shape; the session is then tracked. */
  async start(target: StartTarget, options: StartOptions, key: string = this.key(), durationSeconds?: number, signal?: AbortSignal,
    context: Pick<StartContext, 'isCurrent' | 'capturePreparing' | 'onPreparing'> = {}): Promise<{session: Session; playback: PlaybackSession}> {
    const session = await this.sessions.start(target, options, key, {...context, signal});
    return {session, playback: this.adopt(session, session.discTitle
      ? await this.duration(session.itemId ?? '', session.versionId, signal, session.discTitle)
      : durationSeconds ?? await this.duration(session.itemId ?? '', session.versionId, signal))};
  }

  /** Tracks a session another request started (a queue create with startPlayback, an advance). */
  adopt(session: Session, durationSeconds: number): PlaybackSession {
    const prior = this.tracked.get(session.id);
    // A presentation change keeps the same session, its observer epoch and its pending skip.
    const tracked = prior ?? {reporter: this.reporterFor(session.id), generation: session.presentation.generation, positionMs: session.presentation.startPositionMs, state: 'playing' as ObservationState};
    tracked.generation = session.presentation.generation;
    tracked.positionMs = session.presentation.startPositionMs;
    tracked.positionAt = this.now();
    tracked.state = session.state === 'paused' ? 'paused' : 'playing';
    tracked.reporter.setGeneration(session.presentation.generation);
    this.registrations.changed(session.id);
    this.tracked.set(session.id, tracked);
    const playback = toPlaybackSession(session, durationSeconds);
    this.selectionAdoptions.set(playback, {id: session.id, registration: this.captureSelectionRegistration(session.id)});
    return playback;
  }

  /**
   * One re-plan of a playing session (`PATCH`, spec §5.7): a quality, version, audio or subtitle
   * change, or a reported player failure. The session keeps its id, its queue and its place in
   * it; when the bytes change it comes back as a new generation (a new URL, possibly another
   * engine) at the player's position. A revision that moved meanwhile is read again once, so the
   * viewer only ever sees a conflict when the session really ended.
   */
  async change(id: string, change: SessionChange, durationSeconds?: number, context?: SessionChangeContext): Promise<{session: Session; playback: PlaybackSession}> {
    requireSessionChangeCurrent(context);
    const t = this.tracked.get(id);
    // The new stream starts where this device is: the change says so itself, and the server is
    // told first as well, so its record of the old presentation ends at the same place.
    const positionMs = change.positionMs ?? change.seek?.positionMs ?? t?.positionMs;
    const body: SessionChange = positionMs !== undefined && change.positionMs === undefined && change.seek === undefined ? {...change, positionMs: Math.max(0, Math.round(positionMs))} : change;
    // The PATCH carries the measured position, and the server writes the latest observation
    // before applying any change, so nothing is sent ahead of it.
    const checkSession = (session: Session) => {
      requireSessionChangeCurrent(context);
      if (context && session.id !== id) throw new Error('Playback session changed during replan');
      if (context?.recoverEnded === false && session.state === 'ended') throw new PlaybackApiError(410, 'session_ended', 'never');
    };
    let current = await this.sessions.get(id, context?.signal);checkSession(current);
    let updated: Session;
    try {
      updated = await this.sessions.patch(id, current.revision, body, context?.signal);
    } catch (e) {
      requireSessionChangeCurrent(context);
      if (!(e instanceof PlaybackApiError) || e.status !== 412) throw e;
      current = (e.current as Session | undefined) ?? await this.sessions.get(id, context?.signal);checkSession(current);
      updated = await this.sessions.patch(id, current.revision, body, context?.signal);
    }
    checkSession(updated);
    const duration = updated.discTitle ? await this.duration(updated.itemId ?? '', updated.versionId, context?.signal, updated.discTitle)
      : durationSeconds ?? await this.duration(updated.itemId ?? '', updated.versionId, context?.signal);
    checkSession(updated);
    return {session: updated, playback: this.adopt(updated, duration)};
  }

  /** The engine could not play a session's presentation: the server re-plans it another way. */
  async reportPlayerFailure(sessionId: string, code: PlayerFailureCode, detail: string, positionSeconds: number): Promise<{escalates: boolean; session?: PlaybackSession; v1?: Session} | null> {
    try {
      const r = await this.change(sessionId, {failure: {code, detail: detail.replace(/[\x00-\x1f\x7f]/g, ' ').slice(0, 500)}, positionMs: Math.max(0, Math.round(positionSeconds * 1000))});
      for (const listener of this.changedListeners) listener(r.session);
      return {escalates: true, session: r.playback, v1: r.session};
    } catch (e) {
      // unsupported_media: this was the last way to deliver the file.
      if (e instanceof PlaybackApiError && (e.code === 'unsupported_media' || e.status === 404 || e.status === 410)) return {escalates: false};
      return null;
    }
  }

  /** Tells an owner of v1 sessions (the queue player) that a session was re-planned behind it. */
  onSessionChanged(listener: (session: Session) => void): () => void {
    this.changedListeners.add(listener);
    return () => { this.changedListeners.delete(listener); };
  }

  createPlayback(itemId: string, requestId: string, signal?: AbortSignal): Promise<PlaybackSession> {
    return this.start({itemId}, {startFrom: 'resume', state: 'playing'}, requestId, undefined, signal).then(r => r.playback);
  }

  stopPlayback(id: string, signal?: AbortSignal): Promise<void> {
    const t = this.tracked.get(id);
    if (!t) return this.sessions.stop(id, undefined, signal);
    if (t.stopPromise) return t.stopPromise;
    t.stopping = true;
    // One last attempt for the newest reading (a skip it carries), then the reliable DELETE with
    // the final position, which the server writes after the latest observation it holds.
    const work = t.reporter.flush().catch(() => {}).then(async () => {
      t.reporter.stop();
      if (this.tracked.get(id) !== t) return;
      await this.sessions.stop(id, t.positionMs, signal);
      if (this.tracked.get(id) === t) this.forget(id);
    });
    t.stopPromise = work.finally(() => { if (this.tracked.get(id) === t) t.stopPromise = undefined; });
    return t.stopPromise;
  }

  async progressPlayback(id: string, payload: {generation: number; sequence: number; positionSeconds: number; state: 'playing' | 'paused' | 'ended'}): Promise<void> {
    if (this.retired.has(id)) return;
    let t = this.tracked.get(id);
    const first = !t;
    if (!t) {
      t = {reporter: this.reporterFor(id), generation: payload.generation, positionMs: 0, state: 'playing'};
      t.reporter.setGeneration(payload.generation);
      this.registrations.changed(id);
      this.tracked.set(id, t);
    }
    if (t.stopping) return;
    const now = this.now(), positionMs = Math.max(0, Math.round(payload.positionSeconds * 1000));
    // This bridge receives positions and states, not seek events: a position that no longer
    // follows the clock is a jump, so it starts a new interval rather than claiming continuity.
    const expected = t.positionMs + (t.state === 'playing' && t.positionAt !== undefined ? now - t.positionAt : 0);
    const cause = first || payload.generation !== t.generation || payload.state !== t.state ? 'state' : Math.abs(positionMs - expected) > 2000 ? 'seeked' : 'time';
    if (payload.generation > t.generation) t.reporter.setGeneration(payload.generation);
    t.generation = payload.generation;
    t.positionMs = positionMs;
    t.positionAt = now;
    t.state = payload.state;
    t.reporter.observe({generation: payload.generation, state: payload.state, positionMs, rate: payload.state === 'playing' ? 1 : 0}, cause);
    if (payload.state !== 'ended') return;
    // The queue advances on the end only once the server has it: one attempt now, and a
    // retryable refusal the caller may repeat. An end the server already has is confirmed.
    await t.reporter.flush();
    const r = t.reporter.snapshot();
    if (r.lastAccepted !== 'ended' && !r.stopped) throw new PlaybackApiError(503, 'timeline_unconfirmed', 'same_request');
  }

  /** The viewer skipped a marker (spec §4.2): carried by the session's samples until accepted. */
  async markerSkipped(id: string, skipped: NonNullable<ObservationSample['skipped']>): Promise<void> {
    const t = this.tracked.get(id);
    if (!t || this.retired.has(id)) return;
    t.reporter.skipped(skipped);
  }

  private reporterFor(id: string): ObservationReporter {
    return new ObservationReporter({
      claim: body => this.sessions.claimObserver(id, body),
      send: sample => this.sessions.observe(id, sample),
      onEnded: () => { if (this.tracked.has(id)) void this.learnEnd(id); },
      ...(this.timers?.setTimer ? {setTimer: this.timers.setTimer} : {}), ...(this.timers?.clearTimer ? {clearTimer: this.timers.clearTimer} : {}), ...(this.clock ? {now: this.clock} : {}),
    });
  }

  /**
   * The server ended a session this service is playing: an administrator terminated it, it
   * moved to another device, its lease lapsed. `PlaybackService` subscribes and stops with the
   * reason. Learned from a forwarded `session.updated` event, or, when a timeline report finds the
   * session gone, from re-reading it.
   */
  onServerEnded(listener: (sessionId: string, end: SessionEnd) => void): () => void {
    this.endedListeners.add(listener);
    return () => { this.endedListeners.delete(listener); };
  }

  /** A `session.updated` event (apps forward `EventsClient`'s): an ended session is reported with
   * its reason; one without a reason in the payload is re-read for it. */
  sessionUpdated(sessionId: string, data?: unknown): void {
    if (!this.tracked.has(sessionId) || this.retired.has(sessionId)) return;
    if (data !== undefined && (data === null || typeof data !== 'object' || (data as {state?: unknown}).state !== 'ended')) return;
    const end = parseSessionEnd(data);
    if (end) this.serverEnded(sessionId, end);
    else void this.learnEnd(sessionId);
  }

  /** Current-state recovery after missed events; errors retain coordinator debt. */
  async reconcileSession(id: string, signal?: AbortSignal): Promise<void> {
    const tracked = this.tracked.get(id);
    if (!tracked || this.retired.has(id) || signal?.aborted) return;
    const generation = tracked.generation;
    const current = () => !signal?.aborted && this.tracked.get(id) === tracked && tracked.generation === generation && !this.retired.has(id);
    try {
      const session = await this.sessions.get(id, signal);
      if (current() && session.state === 'ended') this.serverEnded(id, session.end ?? {reason: 'stopped'});
    } catch (error) {
      if (!current()) return;
      if (error instanceof PlaybackApiError && (error.status === 404 || error.status === 410)) this.serverEnded(id, {reason: 'stopped'});
      else throw error;
    }
  }

  private async learnEnd(id: string): Promise<void> {
    try {
      const session = await this.sessions.get(id);
      if (session.state === 'ended') this.serverEnded(id, session.end ?? {reason: 'stopped'});
    } catch (e) {
      if (e instanceof PlaybackApiError && (e.status === 404 || e.status === 410)) this.serverEnded(id, {reason: 'stopped'});
    }
  }

  private serverEnded(id: string, end: SessionEnd) {
    if (!this.tracked.has(id)) return;
    this.forget(id);
    for (const listener of this.endedListeners) listener(id, end);
  }

  /** A session a committed audio edge ended (§18.3): no more reports, no end notice. */
  retire(id: string) {
    this.forget(id);
    this.retired.add(id);
    if (this.retired.size > 64) this.retired.delete(this.retired.values().next().value!);
  }

  /** Stops tracking (the session ended or was replaced) without a request. */
  forget(id: string) {this.forgetRegistration(id, this.tracked.get(id));}

  selectionRegistration(id: string, adopted?: PlaybackSession): QueueSelectionRegistration | undefined {
    if (adopted) {const own = this.selectionAdoptions.get(adopted); return own?.id === id ? own.registration : undefined;}
    // A returned session does not grant ownership of another caller's registration.
    return this.tracked.has(id) ? undefined : this.captureSelectionRegistration(id);
  }

  private captureSelectionRegistration(id: string): QueueSelectionRegistration {
    const original = this.tracked.get(id);
    return this.registrations.capture(id, () => this.tracked.get(id) === original, () => this.forgetRegistration(id, original));
  }

  private forgetRegistration(id: string, original: Tracked | undefined): boolean {
    if (this.tracked.get(id) !== original) return false;
    // Remove only this registration before callbacks can re-register the id.
    this.tracked.delete(id); this.registrations.changed(id);
    original?.reporter.stop();
    return true;
  }

  /** Hand a session's observations to a native owner (plan §9.3 C3): this reporter stops, and
   * the epoch it held is returned for the owner to claim with (`replaces`). */
  handOverObserver(id: string): string | undefined {
    const t = this.tracked.get(id);
    if (!t) return undefined;
    const epoch = t.reporter.snapshot().epoch;
    t.reporter.stop();
    return epoch;
  }

  /** The last position the service reported for a session, in ms. */
  positionMs(id: string): number | undefined {
    return this.tracked.get(id)?.positionMs;
  }
}
