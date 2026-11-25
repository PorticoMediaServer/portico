import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {Timers} from '../../../../../packages/client-core/src/playback-v1/sessions';
import type {Session} from '../../../../../packages/client-core/src/playback-v1/types';
import {sameFence, type DockPlayback, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {NativePlaybackRuntimeSnapshot, NativePlaybackStarting, NativePlaybackStartTimedOut, NativeStartDisplay} from './types';

/** O-159: something (the starting dock or the real dock) is drawn within this time of Play. */
export const NATIVE_START_DRAWN_MS = 5000;
/** O-159: the start deadline, as web's START_DEADLINE_MS. From Play, the real dock or player is
 * drawn within this time, accepted or not; otherwise "Couldn't start playback". */
export const NATIVE_START_DEADLINE_MS = 20_000;

export const entryDisplay = (entry: ContentEntry): NativeStartDisplay =>
  Object.freeze({title: entry.title, ...(entry.posterUrl ?? entry.backdropUrl ? {posterUrl: entry.posterUrl ?? entry.backdropUrl} : {})});

export type NativeStartHandle = Readonly<{
  stopWith: (stop: () => void) => void;
  accepted: (fence: PlaybackFence | undefined) => void;
  end: () => void;
}>;

export type NativeStartWatchHost = Readonly<{
  timers?: Timers;
  isRetired: () => boolean;
  hasSnapshot: () => boolean;
  publish: () => void;
  beginOperation: () => number;
  fence: () => PlaybackFence | undefined;
  currentSession: () => Session | undefined;
  hasOptions: () => boolean;
  localDock: () => DockPlayback | null;
  fallbackDisplay: () => NativeStartDisplay | undefined;
  diagnosticKind: () => string;
  isChannelStalled: () => boolean;
  stop: (fence: PlaybackFence) => Promise<void>;
}>;

type StartingState = {
  key: string;
  display?: NativeStartDisplay;
  accepted: boolean;
  timer: unknown;
  drawnTimer: unknown;
  deadlineFrom: 'play' | 'none';
  stop: () => void;
  retry: () => Promise<void>;
  view?: NativePlaybackStarting;
  fence?: PlaybackFence;
};

/** O-159, two clocks as on web. (a) By NATIVE_START_DRAWN_MS something is drawn: the starting
 * dock drawn at Play from the pressed card satisfies it at once. (b) By NATIVE_START_DEADLINE_MS
 * from Play the real dock or player replaces it in place, accepted or not; otherwise the start
 * is stopped, recorded locally and offered again. Slow buffering never trips it, because a drawn
 * real dock ends the watch, and a Stop-only dock never stays forever. */
export class NativeStartWatch {
  private state?: StartingState;
  private timedOut?: NativePlaybackStartTimedOut;
  private keys = 0;
  private readonly host: NativeStartWatchHost;

  constructor(host: NativeStartWatchHost) {
    this.host = host;
  }

  /** O-159 for every other start path: drawn at once (generic until its title is known), bounded
   * by NATIVE_START_DEADLINE_MS from Play, and cancelled for real by Stop. A Live tune, as on web,
   * has no start deadline: it is drawn at tune acceptance, and the channel's own 60 s bound
   * (CHANNEL_STREAM_WAIT_MS) ends a stream that never comes up. A start that ends without a
   * session or fence was not accepted and is never reported as a silent no-op. */
  async watched<T>(display: NativeStartDisplay | undefined, retry: () => Promise<void>, run: () => Promise<T>, deadlineFrom: 'play' | 'none' = 'play'): Promise<T> {
    let stopped = false;
    const starting = this.begin(display, retry, deadlineFrom);
    starting.stopWith(() => {stopped = true; this.host.beginOperation();});
    try {
      const result = await run();
      if (stopped) throw Object.assign(new Error('Playback start stopped'), {name: 'AbortError'});
      const fence = this.host.fence();
      if (!fence && !this.host.currentSession()) {starting.end(); return result;}
      starting.accepted(fence);
      starting.stopWith(() => {stopped = true; this.host.beginOperation(); if (fence) void this.host.stop(fence).catch(() => {});});
      return result;
    } catch (error) {
      starting.end();
      if (stopped) throw Object.assign(new Error('Playback start stopped'), {name: 'AbortError'});
      throw error;
    }
  }

  begin(display: NativeStartDisplay | undefined, retry: () => Promise<void>, deadlineFrom: 'play' | 'none' = 'play'): NativeStartHandle {
    this.end(); this.timedOut = undefined;
    const state: StartingState = {key: String(++this.keys), display, accepted: false, timer: undefined, drawnTimer: undefined, deadlineFrom, stop: () => {}, retry};
    state.drawnTimer = this.setTimer(() => this.checkStartDrawn(state), NATIVE_START_DRAWN_MS);
    if (deadlineFrom === 'play') state.timer = this.setTimer(() => this.expire(state), NATIVE_START_DEADLINE_MS);
    this.state = state; if (this.host.hasSnapshot()) this.host.publish();
    const mine = () => this.state === state;
    return {
      stopWith: (stop: () => void) => {if (mine()) state.stop = stop;},
      accepted: (fence: PlaybackFence | undefined) => {if (mine()) {state.accepted = true; state.fence = fence; if (this.host.hasSnapshot()) this.host.publish();}},
      end: () => {if (mine()) {this.end(); if (this.host.hasSnapshot()) this.host.publish();}},
    };
  }

  end(): void {
    const state = this.state;
    if (!state) return;
    this.state = undefined;
    this.clearTimer(state.timer); this.clearTimer(state.drawnTimer);
  }

  dispose(): void {
    this.end();
    this.timedOut = undefined;
  }

  /** Snapshot fields; the start's own fence on the real dock ends the watch (drawn). */
  fields(fence: PlaybackFence | undefined): Partial<Pick<NativePlaybackRuntimeSnapshot, 'starting' | 'startTimedOut'>> {
    const state = this.state;
    const local = this.host.localDock();
    if (state?.accepted && local && fence && (!state.fence || sameFence(state.fence, fence)) && sameFence(local.fence, fence)) this.end();
    const live = this.state;
    // A start that came without a card is named by what the runtime now knows it is playing;
    // until then it is drawn as a generic "Starting…" (with its kind's icon, when known).
    if (live && !live.display?.title) {
      const known = this.host.fallbackDisplay();
      if (known) live.display = {...(live.display?.icon ? {icon: live.display.icon} : {}), ...known};
      else live.display ??= {};
    }
    // A Live start the server reports recoverable says so while it keeps trying (never a silent "Starting…").
    const reason = this.host.isChannelStalled() ? 'channel_source' as const : undefined;
    if (live?.display && (live.view?.display !== live.display || live.view?.reason !== reason)) {
      live.view = Object.freeze({
        key: live.key,
        display: live.display,
        ...(reason ? {reason} : {}),
        stop: () => {if (this.state === live) {this.end(); live.stop(); if (this.host.hasSnapshot()) this.host.publish();}},
      });
    }
    return {...(live?.view ? {starting: live.view} : {}), ...(this.timedOut ? {startTimedOut: this.timedOut} : {})};
  }

  private setTimer(run: () => void, ms: number): unknown {
    return this.host.timers?.setTimer ? this.host.timers.setTimer(run, ms) : setTimeout(run, ms);
  }

  private clearTimer(timer: unknown): void {
    if (timer === undefined) return;
    if (this.host.timers?.clearTimer) this.host.timers.clearTimer(timer); else clearTimeout(timer as ReturnType<typeof setTimeout>);
  }

  /** Clock (a): every start draws its starting dock at once (a generic one when untitled), so this
   * only notes in the journal a start that somehow drew nothing; the deadline still bounds it. */
  private checkStartDrawn(state: StartingState): void {
    if (this.state !== state || this.host.isRetired()) return;
    state.drawnTimer = undefined;
    if (state.view || this.host.localDock()) return;
    try {console.warn(`portico_playback_not_drawn ${JSON.stringify({code: 'playback_not_drawn_dock', kind: this.host.diagnosticKind()})}`);} catch { /* passive */ }
  }

  private expire(state: StartingState): void {
    if (this.state !== state || this.host.isRetired()) return;
    const code = !state.accepted ? 'playback_start_timeout' : !this.host.currentSession() ? 'playback_not_drawn_session' : !this.host.hasOptions() ? 'playback_not_drawn_offers' : 'playback_not_drawn_presentation';
    this.end(); state.stop();
    // The local diagnostic (ErrorJournal) names the read that never became current.
    try {console.warn(`portico_playback_not_drawn ${JSON.stringify({code, kind: this.host.diagnosticKind()})}`);} catch { /* passive */ }
    const timedOut: NativePlaybackStartTimedOut = Object.freeze({
      key: state.key,
      retry: async () => {if (this.timedOut !== timedOut) return; this.timedOut = undefined; if (this.host.hasSnapshot()) this.host.publish(); await state.retry();},
      close: () => {if (this.timedOut === timedOut) {this.timedOut = undefined; if (this.host.hasSnapshot()) this.host.publish();}},
    });
    this.timedOut = timedOut; if (this.host.hasSnapshot()) this.host.publish();
  }
}
