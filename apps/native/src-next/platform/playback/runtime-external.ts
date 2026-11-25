import type {PlaybackService, PlaybackSnapshot} from '../../../../../packages/client-core/src/index';
import type {Timers} from '../../../../../packages/client-core/src/playback-v1/sessions';
import type {Session} from '../../../../../packages/client-core/src/playback-v1/types';
import type {V1QueuePlayer} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import type {SubtitleSnapshot} from '../../../../../packages/client-core/src/subtitles';
import type {PlaybackFence} from '../../../../../packages/player-core/src';
import type {NativeBinding} from './NativePlaybackCoordinator';
import {nativeExternalPresentationBinding, sameNativePresentation} from './external-presentation';
import {externalSubtitlePreparation} from './engine-media';
import type {NativePlayback} from './native-playback';
import type {NativeExternalPresentationLifetime, NativePictureInPictureState, NativePlaybackRuntimeSnapshot} from './types';

/** The retained binding a native view reports, compared field by field. */
export const sameBindingFact = (fact: unknown, binding: NativeBinding): boolean => {
  if (fact === null || typeof fact !== 'object' || Array.isArray(fact)) return false;
  const raw = fact as Record<string, unknown>;
  return raw.lease === binding.lease && raw.intentId === binding.intentId && raw.sessionId === binding.sessionId
    && raw.generation === binding.generation && raw.itemId === binding.itemId;
};

export type NativeExternalPresentationHost = Readonly<{
  playback: PlaybackService;
  path: NativePlayback;
  queue: V1QueuePlayer;
  timers?: Timers;
  pictureInPictureControls?: true;
  autoPictureInPicture: () => boolean;
  isRetired: () => boolean;
  active: () => boolean;
  matches: (fence: PlaybackFence) => boolean;
  startEpoch: () => number;
  lease: () => string;
  currentSession: () => Session | undefined;
  subtitles: () => SubtitleSnapshot | undefined;
  snapshot: () => NativePlaybackRuntimeSnapshot;
  subscribe: (listener: () => void) => () => void;
  publish: () => void;
}>;

/** External video presentation (AirPlay caption burn-in) and Picture-in-Picture attempt state machine. */
export class NativeExternalPresentation {
  private binding?: NativeBinding | null;
  private controller?: AbortController;
  private epoch = 0;
  /** SUP-007: AirPlay video carries the picture only, so captions this app draws are burned in
   * while it lasts, once per session (the next queue item is prepared again). */
  private airPlayActive = false;
  private airPlayController?: AbortController;
  private airPlaySession?: string;
  private pictureRequests = 0;
  private pictureBinding?: NativeBinding;
  /** The one request the retained view is serving, from issue to its own terminal;
   * `pictureLeave` is the later request that may end an active one (coming back). */
  private pictureAttempt?: Readonly<{request: number; state: 'pending' | 'active'}>;
  private pictureLeave?: number;
  /** The last state the native view reported for that attempt. */
  private pictureState: NativePictureInPictureState = 'idle';
  private readonly host: NativeExternalPresentationHost;

  constructor(host: NativeExternalPresentationHost) {
    this.host = host;
  }

  resetForRouteUnready(): void {
    this.binding = null;
    this.clearPicture();
    this.controller?.abort();
  }

  resetForOperation(): void {
    this.controller?.abort();
    this.clearPicture();
  }

  resetForSource(): void {
    this.binding = undefined;
    this.clearPicture();
    this.airPlayController?.abort();
    this.airPlayController = undefined;
  }

  retire(): void {
    this.binding = null;
    this.controller?.abort();
    this.airPlayController?.abort();
    this.airPlayController = undefined;
    this.clearPicture();
  }

  acceptFact(fact: unknown, nativeBinding: NativeBinding | undefined): void {
    this.binding = nativeExternalPresentationBinding(fact, nativeBinding);
  }

  /** The AirPlay route changed (`active`), or playback moved on while it lasts. */
  followAirPlay(active?: boolean): void {
    if (active !== undefined && active !== this.airPlayActive) {
      this.airPlayActive = active; this.airPlaySession = undefined;
      this.airPlayController?.abort(); this.airPlayController = undefined;
    }
    const fence = this.host.snapshot().fence, sessionId = this.host.currentSession()?.id;
    if (!this.airPlayActive || this.airPlayController || fence?.kind !== 'queue' || !sessionId || sessionId === this.airPlaySession) return;
    const controller = this.airPlayController = new AbortController();
    void this.prepare(fence, {signal: controller.signal, isCurrent: () => this.airPlayController === controller})
      .then(binding => {if (this.airPlayController === controller && binding) this.airPlaySession = sessionId;},
        // The captions stay drawn on this device; the next progress tick tries again.
        error => {if (__DEV__) console.warn('[airplay-captions]', error);})
      .finally(() => {if (this.airPlayController === controller) this.airPlayController = undefined;});
  }

  /** Same queue occurrence/session, then an actual native acknowledgement. The
   * caller owns host/navigation lifetime; the accepted new generation is ours. */
  prepare = async (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime): Promise<NativeBinding | undefined> => {
    if (fence.kind !== 'queue' || !this.host.matches(fence) || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const original = this.host.currentSession(), playback = this.host.playback.getSnapshot(), controllerState = this.host.path.controller.getSnapshot();
    if (!original || original.kind !== 'vod' || original.id !== fence.sessionId || !original.queue || original.queue.entryId !== fence.entryId || playback.nativeListening
      || playback.phase !== 'ready' || playback.pendingSeek || playback.failedSeek || controllerState.changing
      || !controllerState.observation || controllerState.observation.generation !== original.presentation.generation
      || !Number.isFinite(controllerState.observation.positionMs) || controllerState.observation.state === 'idle' || controllerState.observation.state === 'error') return;
    const subtitles = this.host.subtitles();
    const preparation = externalSubtitlePreparation(subtitles?.suppressed ? null : subtitles?.plan, original);
    if (preparation.kind === 'unavailable') return;
    this.controller?.abort();
    const controller = this.controller = new AbortController(), epoch = ++this.epoch, start = this.host.startEpoch();
    let generation: number | undefined;
    const current = () => {
      const state = this.host.playback.getSnapshot(), session = this.host.queue.currentSession(), queue = this.host.queue.getSnapshot().view?.queue;
      try {return this.host.active() && !controller.signal.aborted && !lifetime.signal.aborted && lifetime.isCurrent()
        && epoch === this.epoch && start === this.host.startEpoch() && state.intentId === playback.intentId
        && state.session?.id === original.id && session?.id === original.id
        && queue?.id === original.queue!.queueId && queue.currentEntryId === original.queue!.entryId && queue.currentPlaybackId === original.id
        && (generation === undefined || state.session.generation === generation);}
      catch {return false;}
    };
    const abort = () => controller.abort();
    lifetime.signal.addEventListener('abort', abort, {once: true});
    if (lifetime.signal.aborted) controller.abort();
    const retire = this.host.subscribe(() => {if (!current()) controller.abort();});
    try {
      const adopted = preparation.kind === 'burn'
        ? await this.host.queue.changePlayback(preparation.choices, {signal: controller.signal, isCurrent: current, recoverEnded: false}) : original;
      if (!current() || !adopted || adopted.id !== original.id || adopted.queue?.queueId !== original.queue.queueId || adopted.queue.entryId !== original.queue.entryId
        || adopted.versionId !== original.versionId || adopted.presentation.audioTrackId !== original.presentation.audioTrackId) return;
      if (preparation.kind === 'burn' && (adopted.presentation.generation <= original.presentation.generation || adopted.presentation.subtitle?.trackId !== preparation.trackId || adopted.presentation.subtitle.delivery !== 'burn')) return;
      generation = adopted.presentation.generation;
      return await this.waitForExternalPresentation(controller.signal, current, binding => {
        if (binding.sessionId !== adopted.id || binding.generation !== generation || binding.intentId !== playback.intentId || binding.itemId !== playback.itemId) return false;
        if (preparation.kind !== 'burn') return true;
        const plan = this.host.subtitles()?.plan;
        return plan?.sessionId === adopted.id && plan.generation === generation && plan.sourceId === preparation.sourceId;
      });
    } catch (error) {
      if (!current()) return;
      throw error;
    } finally {
      retire(); lifetime.signal.removeEventListener('abort', abort); controller.abort();
      if (this.controller === controller) this.controller = undefined;
    }
  };

  /** True acknowledges a fenced retained-view request, never that PiP started. */
  requestPictureInPicture = async (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime): Promise<boolean> => {
    if (!this.host.pictureInPictureControls) return false;
    // One attempt at a time, like the retained view, and a press the view could not
    // serve is a true no-op: nothing is issued, published or handed to native, so a
    // starting attempt is never retagged by its counter.
    if (!this.pictureRequestAllowed()) return false;
    // N1-6: a press while the OS's own window is up only asks it to come back. Nothing needs
    // preparing (a re-plan would retire the window's attempt and strand the answer), so the
    // press is its leaving request at once, for this fence only.
    if (this.pictureAttempt?.request === 0 && this.pictureAttempt.state === 'active') {
      if (!this.host.matches(fence) || lifetime.signal.aborted || !lifetime.isCurrent() || this.pictureRequests >= Number.MAX_SAFE_INTEGER) return false;
      this.pictureRequests++; this.pictureLeave = this.pictureRequests; this.host.publish(); return true;
    }
    const pending = this.prepare(fence, lifetime), epoch = this.epoch;
    const binding = await pending, ready = this.host.snapshot().externalPresentationBinding;
    if (!binding || !ready || !this.host.active() || epoch !== this.epoch || lifetime.signal.aborted || !lifetime.isCurrent()
      || !sameNativePresentation(binding, ready) || this.pictureRequests >= Number.MAX_SAFE_INTEGER) return false;
    // A racing call may have issued while this one prepared.
    if (!this.pictureRequestAllowed()) return false;
    this.pictureRequests++;
    this.pictureBinding = binding;
    if (this.pictureAttempt?.state === 'active') this.pictureLeave = this.pictureRequests;
    else {this.pictureAttempt = Object.freeze({request: this.pictureRequests, state: 'pending'}); this.pictureState = 'idle';}
    this.host.publish(); return true;
  };

  /** The native view's own report about a request it was handed. Admitted only for
   * the current lease and that request, so a replaced surface or a stale answer
   * cannot claim the picture; `unavailable` may also arrive before any request. */
  admitPictureInPicture = (fact: unknown): boolean => {
    if (!this.host.pictureInPictureControls || this.host.isRetired()) return false;
    if (fact === null || typeof fact !== 'object' || Array.isArray(fact)) return false;
    const raw = fact as {lease?: unknown; request?: unknown; requestId?: unknown; binding?: unknown; state?: unknown};
    const state = raw.state === 'started' || raw.state === 'restore' || raw.state === 'stopped' || raw.state === 'unavailable' ? raw.state : undefined;
    if (!state || typeof raw.lease !== 'string' || !raw.lease || raw.lease !== this.host.lease()) return false;
    // Apple names the request `requestId`; Android names it `request`. Both are the
    // number the view was handed, and 0 means the view answered without being asked.
    const named = typeof raw.requestId === 'number' ? raw.requestId : raw.request;
    const requested = typeof named === 'number' && Number.isSafeInteger(named) && named > 0 ? named : 0;
    const attempt = this.pictureAttempt;
    if (state === 'unavailable') {
      // An engine or device that cannot start may say so before anything was
      // asked; a live window is never cleared by anyone else's answer, and an
      // attempt is only ended by its own request.
      if (attempt?.state === 'active') return false;
      if (requested === 0 ? this.pictureAttempt !== undefined || this.pictureRequests > 0 : attempt?.request !== requested) return false;
    } else if (requested === 0 && (state === 'started' ? !attempt : attempt?.request === 0 && attempt.state === 'active')) {
      // N1-6 (ENGINE-CONTRACT: "0 for an automatic OS start"): Apple starts Picture in Picture by
      // itself when the app leaves with video playing. That window is an attempt of its own,
      // fenced by the presentation that is ready now; it never acknowledges an explicit request,
      // and only its own stop or restore (also 0) ends it. A later press is then its leaving
      // request, exactly as for an explicit window.
      const ready = this.host.snapshot().externalPresentationBinding;
      if (!ready || !sameBindingFact(raw.binding, ready) || state === 'started' && this.pictureBinding && !sameNativePresentation(this.pictureBinding, ready)) return false;
      if (state === 'started') {this.pictureBinding = ready; this.pictureAttempt = Object.freeze({request: 0, state: 'active'}); this.pictureLeave = undefined;}
    } else {
      // Started, restore and stopped must name this attempt (or the later request
      // that asks to come back from an active one), carry its own complete
      // binding, and only move in one direction.
      const ready = this.host.snapshot().externalPresentationBinding;
      if (!this.pictureBinding || !ready || !sameNativePresentation(this.pictureBinding, ready)) return false;
      if (!attempt || !sameBindingFact(raw.binding, this.pictureBinding)) return false;
      const owns = requested === attempt.request || (attempt.state === 'active' && requested !== 0 && requested === this.pictureLeave);
      if (!owns) return false;
      if (state === 'started' ? attempt.state !== 'pending' || requested !== attempt.request : attempt.state !== 'active') return false;
    }
    this.pictureState = state;
    if (state === 'started' && attempt && attempt.request !== 0) this.pictureAttempt = Object.freeze({request: attempt.request, state: 'active'});
    // Its own terminal only: the attempt and the request that may end it both retire.
    if (state === 'stopped' || state === 'unavailable') {this.pictureAttempt = undefined; this.pictureLeave = undefined;}
    this.host.publish();
    return true;
  };

  snapshotFields(playback: PlaybackSnapshot, fence: PlaybackFence | undefined, nativeBinding: NativeBinding | undefined): {
    externalPresentationBinding?: NativeBinding | null;
    pictureInPicture?: NativePlaybackRuntimeSnapshot['pictureInPicture'];
  } {
    const acknowledgement = this.binding;
    const retired = this.host.isRetired();
    const externalPresentationBinding = acknowledgement === undefined ? undefined : acknowledgement && fence && !retired
      && !playback.pendingSeek && !playback.failedSeek && playback.phase !== 'error' && nativeBinding
      && sameNativePresentation(acknowledgement, nativeBinding) && acknowledgement.intentId === playback.intentId
      && acknowledgement.sessionId === fence.sessionId && acknowledgement.generation === fence.generation
      && acknowledgement.itemId === playback.itemId ? acknowledgement : null;
    return {
      externalPresentationBinding,
      ...(this.host.pictureInPictureControls ? {pictureInPicture: Object.freeze({
        request: this.pictureRequests,
        active: this.pictureAttempt?.state === 'active',
        state: this.pictureState,
        automatic: !retired && this.host.autoPictureInPicture(),
        ...(this.pictureBinding && externalPresentationBinding && sameNativePresentation(this.pictureBinding, externalPresentationBinding) ? {binding: this.pictureBinding} : {}),
      })} : {}),
    };
  }

  private clearPicture(): void {
    this.pictureBinding = undefined;
    this.pictureAttempt = undefined;
    this.pictureLeave = undefined;
    this.pictureState = 'idle';
  }

  /** A pending attempt owns the request counter, and an active one already holds its
   * single leaving request; in both cases another press asks for nothing new. */
  private pictureRequestAllowed(): boolean {
    const attempt = this.pictureAttempt;
    return !attempt || attempt.state === 'active' && this.pictureLeave === undefined;
  }

  private waitForExternalPresentation(signal: AbortSignal, current: () => boolean, accepts: (binding: NativeBinding) => boolean): Promise<NativeBinding | undefined> {
    return new Promise(resolve => {
      let finished = false, timer: unknown, unsubscribe = () => {};
      const finish = (binding?: NativeBinding) => {
        if (finished) return; finished = true; unsubscribe(); signal.removeEventListener('abort', aborted);
        if (timer !== undefined) {if (this.host.timers?.clearTimer) this.host.timers.clearTimer(timer); else clearTimeout(timer as ReturnType<typeof setTimeout>);}
        resolve(binding);
      };
      const aborted = () => finish();
      const check = () => {
        if (signal.aborted || !current()) {finish(); return;}
        const binding = this.host.snapshot().externalPresentationBinding;
        if (binding && accepts(binding)) finish(binding);
      };
      unsubscribe = this.host.subscribe(check); signal.addEventListener('abort', aborted, {once: true});
      timer = this.host.timers?.setTimer ? this.host.timers.setTimer(() => finish(), 10000) : setTimeout(() => finish(), 10000);
      check();
    });
  }
}
