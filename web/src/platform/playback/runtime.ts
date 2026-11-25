import {readWebPlaybackCapabilities, scrubPreviewsEnabled} from '@core/query/playback.ts';
import {readPlaybackItem, type PlaybackItem} from '@core/playback-v1/item.ts';
import {readPlaybackItemBounded} from '@core/query/playback-item.ts';
import {playbackFailurePermanent} from '@core/playback-messages.ts';
import {readPlaybackTrickplay, type PlaybackTrickplaySelection} from './trickplay.ts';
import {captureAppLogRecorder, type AppLogCode, type AppLogRecorder} from '../app-log';
import {type WebQueueListPort, type WebQueueListLifetime} from './queue-list';
import {capacityRetryHttp} from './capacity-retry';
import {startFailureHttp} from './start-failure';
import {markPresentedInPlayer} from './presented';
import {PlaybackService, type PlaybackSnapshot} from '@core/index.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import {V1QueuePlayer, serverSupportsQueueTransitions} from '@core/playback-v1/v1-queue-player.ts';
import {SessionsClient} from '@core/playback-v1/sessions.ts';
import type {CatalogueSelectionQueuePort} from '@core/catalogue-selection.ts';
import type {V1ChannelControl} from '@core/playback-v1/channel-control.ts';
import {ownedWebChannels} from './channel-start-binding.ts';
import type {HomeSelectionPlaybackPort} from '@core/home-selection/types.ts';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {HomeChannel} from '@core/home.ts';
import type {GuideChannel} from '@core/channel-guide.ts';
import type {RoomSessionService as GroupSessionService} from '@core/room-session.ts';
import {localPlaybackScope, sameLocalScope, type LocalPlaybackFence, type SkipMode} from '../../../../packages/player-core/src/index.ts';
import {WebLocalQueue, captureLocalQueue, type WebLocalQueueResource} from './local-queue.ts';
import type {V1Http} from '@core/playback-v1/http.ts';
import {CapabilityPublisher, PlaybackOptionsClient} from '@core/playback-v1/options.ts';
import {audioStreamIndex, qualityChoiceOf, qualityRequest, offeredQualities, type QualityChoice} from '@core/playback-v1/choices.ts';
import {PlayerOffersService, type PlayerOffersData} from '@core/player-offers.ts';
import type {SubtitlePreferences} from '@core/preference-effects.ts';
import type {Session} from '@core/playback-v1/types.ts';
import type {SubtitleLines} from '@core/subtitle-originals.ts';
import {PlaybackDock, PlaybackPolicy, PlayerSessionChoices, playbackFence, queueScope, sameFence, type DockPlayback, type DockTransition, type PlaybackFence, type PolicyView, type QueuePlaybackFence, type ChannelPlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {takePendingTrackChoice} from '@core/pending-track-choice.ts';
import {WebPlayback} from './web-playback.ts';
import {DecodeAudioRender} from './DecodeAudioRender.ts';
import {loadDeviceEqualizer, subscribeDeviceEqualizer} from './audio/equalizer-store.ts';
import type {EqualizerSettings} from './audio/equalizer.ts';
import type {EqualizerPort} from '../../kit/media/equalizer-port.ts';
import {ByteCache} from './audio/bytes.ts';
import {wirelessPlayback} from './airplay.ts';
import {browserNativeHlsPolicy} from './native-hls.ts';
import {documentVisibility} from './page-visibility.ts';
import {SubtitleSurface} from './subtitle-surface.ts';
import {entryStartDisplay, entrySubject, queueEntrySubject} from './subject.ts';
import {playbackEnvironment} from './environment.ts';
import {browserClientProfile} from './client-profile.ts';
import type {WebPlaybackQueueSource} from './queue-source.ts';
import type {WebListeningLifetime, WebListeningPort} from './listening-port.ts';
import type {WebPlaybackPolicyAction} from './policy.ts';
import type {WebPlaybackVersionChoices, WebPlaybackVersionChoice} from './versions.ts';
import type {VideoLike} from './web-media-player.ts';
import type {RuntimeGroupBinding} from './group-binding.ts';
import type {WebPlaybackInfoRead, WebPlaybackAction, WebPlaybackAuthority, WebPlaybackEnvironment, WebPlaybackPresentation, WebPlaybackRuntimeSnapshot, WebPlaybackSource, WebPlaybackStart, WebPlaybackVersionStart, WebPlaybackScreenTarget, WebPlaybackScreenLifetime, WebPlaybackStartDisplay, WebPlaybackStarting, WebPlaybackStartTimedOut} from './types.ts';
import {RuntimeMediaSession} from './media-session.ts';
import {START_DEADLINE_MS, compatibleVideo, localMemberSubject, localStartDisplay, problem, screenListSelector, startStopped, validSeconds, type Bookmark, type LocalQueueLease, type Stack, type Starting, type StartingState, type WebSleepEnd, type WebSubtitleSearch} from './runtime-types.ts';
import {advancePreplay as advancePreplayImpl, bindSubtitles as bindSubtitlesImpl, clearDrawnSubtitles as clearDrawnSubtitlesImpl, clearRecordedSidecar, downloadSubtitle as downloadSubtitleImpl, renderSubtitles as renderSubtitlesImpl, searchSubtitles as searchSubtitlesImpl, selectSubtitle as selectSubtitleImpl, setSubtitleForcedOnly as setSubtitleForcedOnlyImpl, setSubtitleOffset as setSubtitleOffsetImpl, setSubtitleStyle as setSubtitleStyleImpl, subtitleForced as subtitleForcedImpl, subtitlePreferences as subtitlePreferencesImpl, subtitleProvider as subtitleProviderImpl, subtitleSearch as subtitleSearchImpl, subtitleStyle as subtitleStyleImpl, type SubtitleRuntimeHost} from './runtime-subtitles.ts';
import {captureListening as captureListeningImpl, chapterMove as chapterMoveImpl, clearSleepTimer as clearSleepTimerImpl, playBookChapter as playBookChapterImpl, setSleepTimer as setSleepTimerImpl, setSpeed as setSpeedImpl, sleepTimer as sleepTimerImpl, speedChoices as speedChoicesImpl, stepSpeed as stepSpeedImpl, type ListeningRuntimeHost} from './runtime-listening.ts';
import {createChannelControl, goLive as goLiveImpl, playChannel as playChannelImpl, playFromGuide as playFromGuideImpl, type LiveRuntimeHost} from './runtime-live.ts';
import {bindRuntimeTogether, groupPlaybackMatches as groupPlaybackMatchesImpl, groupTransport as groupTransportImpl, handleExternalTransport, type TogetherRuntimeHost} from './runtime-together.ts';
import {advancePolicy as advancePolicyImpl, catalogueQueue as catalogueQueueImpl, createStartDeadline, enqueueSelection as enqueueSelectionImpl, onTransition as onTransitionImpl, playFromQueue as playFromQueueImpl, playQueueSegments as playQueueSegmentsImpl, playSelection as playSelectionImpl, playVersion as playVersionImpl, policyAction as policyActionImpl, queueList as queueListImpl, readPlaybackInfo as readPlaybackInfoImpl, readVersions as readVersionsImpl, restoreBookmark as restoreBookmarkImpl, selectSource as selectSourceImpl, type QueueRuntimeHost} from './runtime-queue.ts';

export type {WebSleepEnd, WebSubtitleSearch};

/** Prepared joins are web-only until native's own measured change lands. */
const readServerCapabilities = (api: WebPlaybackSource['api'], signal: AbortSignal): Promise<unknown> => readWebPlaybackCapabilities(api, signal);

/** Actual playback owner. The shell keeps the video node mounted once outside destinations;
 * this class binds the real port and applies the accepted dock's effects to real services. */
export class WebShellPlaybackRuntime {
  readonly dock: PlaybackDock;
  readonly authority: WebPlaybackAuthority;
  private readonly source: WebPlaybackSource;
  private readonly env: WebPlaybackEnvironment;
  readonly equalizerPort?: EqualizerPort;
  private readonly audioCache = new ByteCache();
  private readonly choices = new PlayerSessionChoices();
  private readonly policy = new PlaybackPolicy();
  private readonly listeners = new Set<() => void>();
  private readonly retentionListeners = new Set<() => void>();
  private readonly pendingMediaStarts = new Set<{current(): boolean; finish(): void}>();
  private publishedRetention = false;
  private remoteRetention?: () => boolean;
  private readonly bookmarks = new Map<string, Bookmark>();
  private readonly retiredFences = new Set<string>();
  private readonly versionChoices = new WeakMap<WebPlaybackVersionChoices, Readonly<{isCurrent(): boolean; signal: AbortSignal; failedVersionId?: string}>>();
  private stack: Stack;
  private video?: VideoLike;
  private surface?: SubtitleSurface;
  /** The player's one canonical subtitle offset, replayed to a surface created later. */
  private subtitleTimeline: number | null = 0;
  private snapshot!: WebPlaybackRuntimeSnapshot;
  private disposed = false;
  /** The server offers §18 queue transitions (gapless and crossfade joins), read once (MU-5). */
  private queueTransitions = false;
  /** Scrub previews are asked for only once the server says it makes them. */
  private scrubPreviews = false;
  private replacing = false;
  private syncing = false;
  private again = false;
  private sequence = 0;
  private operation = 0;
  private startingState?: StartingState;
  private startTimedOut?: WebPlaybackStartTimedOut;
  private startingKeys = 0;
  private group?: RuntimeGroupBinding;
  private detachGroup?: () => void;
  private groupRate = 1;
  private readonly groupServices = new WeakMap<RuntimeGroupBinding, GroupSessionService>();
  private localRun = 0;
  private catalogueEnqueuing = false;
  private cataloguePort?: Pick<V1QueuePlayer, 'playSegments'>;
  private readonly localLeases = new Set<LocalQueueLease>();
  private runtimeProblem?: string;
  private policyView: PolicyView | null = null;
  private policyAdvanceFence?: QueuePlaybackFence;
  private timer?: unknown;
  private lifecycle: (() => void)[] = [];
  private readonly capabilityPublisher: CapabilityPublisher;
  private readonly life = new AbortController();
  private readonly channelControl: V1ChannelControl;
  private readonly recordDiagnostic: AppLogRecorder;
  private readonly mediaSession: RuntimeMediaSession;

  constructor(source: WebPlaybackSource, environment: WebPlaybackEnvironment = playbackEnvironment()) {
    if (source.contentScope.serverId !== source.authority.serverId || source.authority.viewerFence === '' || !source.isCurrent()) problem('invalid_playback_authority');
    const localScope = source.localScope && localPlaybackScope(source.localScope);
    if (localScope && (['serverId', 'authority', 'accountId', 'profileId'] as const).some(key => localScope[key] !== source.authority[key])) problem('invalid_playback_authority');
    this.authority = Object.freeze({...source.authority});
    this.source = Object.freeze({...source, ...(localScope ? {localScope} : {}), authority: this.authority, contentScope: Object.freeze({...source.contentScope})});
    this.env = environment;
    const record = captureAppLogRecorder();
    this.recordDiagnostic = (code, level) => {if (!this.disposed && source.isCurrent()) record(code, level);};
    this.equalizerPort = environment.equalizerPort;
    this.capabilityPublisher = new CapabilityPublisher(localV1Http(source.api), () => JSON.stringify([this.authority.serverId, this.authority.accountId, this.authority.profileId, this.authority.epoch]));
    this.channelControl = createChannelControl(source, environment, this.life);
    this.mediaSession = new RuntimeMediaSession({
      resolveArtwork: this.mediaUrl,
      now: environment.timers?.now,
      actions: {
        play: fence => this.resume(fence),
        pause: fence => this.pause(fence),
        replay: fence => this.replay(fence),
        seek: (fence, seconds) => this.seek(fence, seconds),
        skip: (fence, delta) => this.skip(fence, delta),
        previous: fence => this.previous(fence),
        next: fence => this.next(fence),
        stop: fence => this.stop(fence),
      },
    });
    // This is the exact device-queue scope V1QueuePlayer exposes; these are its existing v1
    // controller values, never a Cast/receiver identity or a newly invented server resource.
    this.dock = new PlaybackDock(queueScope({...source.authority, controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'}), localScope);
    this.stack = this.makeStack();
    this.activate(this.stack);
    this.sync();
    // Read once for this runtime; a queue made before the answer follows it once it arrives.
    void readServerCapabilities(source.api, this.life.signal).then(capabilities => {if (!this.disposed) {this.queueTransitions = serverSupportsQueueTransitions(capabilities); this.scrubPreviews = scrubPreviewsEnabled(capabilities);}}).catch(() => {});
    const page = environment.page ?? (typeof document === 'undefined' ? undefined : document);
    const browser = environment.browser ?? (typeof window === 'undefined' ? undefined : window);
    if (page) {
      const hidden = () => { if (page.visibilityState === 'hidden') this.stack.web.pageHidden(false); };
      page.addEventListener('visibilitychange', hidden);
      this.lifecycle.push(() => page.removeEventListener('visibilitychange', hidden));
    }
    if (browser) {
      const leaving = (event: PageTransitionEvent) => { this.stack.web.pageHidden(!event.persisted); if (!event.persisted) this.dispose(); };
      browser.addEventListener('pagehide', leaving);
      this.lifecycle.push(() => browser.removeEventListener('pagehide', leaving));
    }
    if (localScope && environment.attachLocalQueue) {
      let attached = true;
      try {
        const detach = environment.attachLocalQueue(localScope, Object.freeze({
          canPlayLocalQueue: () => attached && this.canPlayLocalQueue(),
          playLocalQueue: async (resource: WebLocalQueueResource, lifetime: WebPlaybackScreenLifetime) => {
            if (!attached) {await resource.release();problem('playback_authority_retired');}
            await this.playLocalQueue(resource, lifetime);
          },
          subscribe: (listener: () => void) => attached ? this.subscribe(listener) : () => {},
        }));
        this.lifecycle.push(() => {attached = false;detach();});
      } catch (error) {attached = false;this.dispose();throw error;}
    }
  }

  getSnapshot = (): WebPlaybackRuntimeSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };

  /** Real owned resources, independent of playing intent and the snapshot ticker. */
  retainsMedia = (): boolean => {
    if (this.disposed || !this.source.isCurrent()) return false;
    if ([...this.pendingMediaStarts].some(start => start.current())) return true;
    if (this.remoteRetention?.()) return true;
    const stack = this.stack, dock = this.dock.getSnapshot(), snapshot = this.snapshot;
    if (dock.local || dock.parkedAudio || dock.resumeVideo || dock.remote || [...this.localLeases].some(lease => !lease.released)) return true;
    if (stack.retired) return false;
    const playback = stack.service.getSnapshot(), queue = stack.queue.getSnapshot();
    return !!(playback.session || stack.local || snapshot?.fence || snapshot?.presentation || snapshot?.presentations.length
      || queue.view || playback.phase === 'starting' || playback.preparation || queue.phase === 'preparing' || stack.capacityWait?.isCurrent());
  };
  onRetention = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.retentionListeners.add(listener);
    return () => {this.retentionListeners.delete(listener);};
  };
  /** The actual Cast controller owns remote continuation outside PlaybackDock.
   * Observe that existing owner directly; no copied remote state or start registry. */
  bindRemoteRetention(source: Readonly<{retainsMedia(): boolean; subscribe(listener: () => void): () => void}>): () => void {
    this.assertCurrent();
    const read = () => source.retainsMedia();
    this.remoteRetention = read;
    const off = source.subscribe(() => this.publishRetention());
    const release = () => {
      off(); this.life.signal.removeEventListener('abort', release);
      if (this.remoteRetention === read) {this.remoteRetention = undefined;this.publishRetention();}
    };
    this.life.signal.addEventListener('abort', release, {once: true});
    this.publishRetention();
    return release;
  }
  private publishRetention(): void {
    // Epoch replacement and caller cancellation release even a preflight whose
    // underlying I/O ignores abort. Its eventual finally is intentionally harmless.
    for (const start of [...this.pendingMediaStarts]) if (!start.current()) start.finish();
    const retained = this.retainsMedia();
    if (retained === this.publishedRetention) return;
    this.publishedRetention = retained;
    for (const listener of [...this.retentionListeners]) {
      try {listener();} catch { /* An observer cannot interrupt media ownership. */ }
    }
  }
  private retainMediaStart(signal?: AbortSignal, isCurrent: () => boolean = () => true): () => void {
    let finished = false;
    const current = () => {
      try {return !finished && !this.disposed && !signal?.aborted && this.source.isCurrent() && isCurrent();} catch {return false;}
    };
    const finish = () => {
      if (finished) return;
      finished = true; signal?.removeEventListener('abort', finish);
      this.pendingMediaStarts.delete(start); this.publishRetention();
    };
    const start = {current, finish};
    if (!current()) return () => {};
    this.pendingMediaStarts.add(start);
    signal?.addEventListener('abort', finish, {once: true});
    this.publishRetention();
    return finish;
  }
  private readonly mediaUrl = (path: string): string => this.source.api.mediaUrl(path);
  private current(stack = this.stack): boolean { return !this.disposed && !stack.retired && stack === this.stack && this.source.isCurrent(); }
  private key(fence: PlaybackFence): string { return JSON.stringify(playbackFence(fence)); }
  private assertCurrent(): void { if (!this.current()) problem('playback_authority_retired'); }
  private setTimer(fn: () => void, ms: number): unknown { return this.env?.timers?.setTimer?.(fn, ms) ?? setTimeout(fn, ms); }
  private clearTimer(timer: unknown): void { if (timer === undefined) return; if (this.env?.timers?.clearTimer) this.env.timers.clearTimer(timer); else clearTimeout(timer as ReturnType<typeof setTimeout>); }

  private makeStack(mode: Stack['mode'] = 'server'): Stack {
    const life = new AbortController(), inner = localV1Http(this.source.api);
    // A stale successful start is returned to the retired core owner so it can abandon the
    // actual server session. DELETE cleanup still uses its original viewer-bound API.
    const bound: V1Http = {send: request => inner.send(request.method === 'DELETE' ? request : {...request, signal: request.signal ? AbortSignal.any([request.signal, life.signal]) : life.signal})};
    const page = this.env.page ?? (typeof document === 'undefined' ? undefined : document);
    const visibility = page ? {hidden: () => page.visibilityState !== 'visible', onVisible(listener: () => void) {
      const changed = () => {if (page.visibilityState === 'visible') listener();};
      page.addEventListener('visibilitychange', changed);return () => page.removeEventListener('visibilitychange', changed);
    }} : undefined;
    const capacity = capacityRetryHttp({http: bound, life: life.signal, timers: this.env.timers, visibility,
      isCurrent: () => this.current(stackReference), captureCurrent: () => stackReference.capacityCurrent, publish: wait => {
        if (!this.current(stackReference)) return;
        stackReference.capacityWait = wait;
        this.sync();
      }});
    const http = startFailureHttp({http: capacity, api: this.source.api, events: this.source.events, visibility, life: life.signal, now: this.env.timers?.now,
      isCurrent: () => this.current(stackReference), captureCurrent: () => stackReference.capacityCurrent,
      isInitial: () => !stackReference.queue.currentSession() && !stackReference.service.getSnapshot().session,
      publish: failure => {if (this.current(stackReference)) {stackReference.startFailure = failure;this.sync();}},
      cancelStart: () => {if (this.current(stackReference)) {
        this.replacing = true;this.retire(stackReference);this.stack = this.makeStack();this.activate(this.stack);this.replacing = false;this.sync();
      }}});
    const web = new WebPlayback({
      preparingStarts: this.source.preparingStarts,
      captureStartCurrent: () => {
        const operation = this.operation, drawn = stackReference.service.getSnapshot(), queue = stackReference.queue.getSnapshot().view?.queue;
        const originalCurrent = stackReference.capacityCurrent;
        return () => this.current(stackReference) && !life.signal.aborted && operation === this.operation &&
          stackReference.service.getSnapshot().intentId === drawn.intentId && stackReference.service.getSnapshot().playbackRun === drawn.playbackRun &&
          stackReference.queue.getSnapshot().view?.queue === queue && (originalCurrent?.() ?? true);
      },
      recordDiagnostic: this.recordDiagnostic,
      http, mediaUrl: this.mediaUrl,
      subscribeRoute: this.source.api.subscribeRoute, getRouteSnapshot: this.source.api.getRouteSnapshot,
      recoverRoute: () => this.source.api.getRouteConnection() ? this.source.api.recoverRoute() : undefined,
      farewell: (path, method, body) => this.source.api.farewell(path, method, body),
      loadHls: this.env.loadHls ?? (() => import('hls.js').then(module => module.default as unknown as import('./web-media-player.ts').HlsModule)),
      nativeHls: this.env.nativeHls ?? browserNativeHlsPolicy(), timers: this.env.timers, playerTimers: this.env.playerTimers, key: this.env.key,
      deviceMemory: typeof navigator === 'undefined' ? undefined : (navigator as Navigator & {deviceMemory?: number}).deviceMemory,
      createPlayer: this.env.createPlayer, secondEngine: this.env.secondEngine,
      createRender: this.env.createRender === null ? undefined : this.env.createRender ?? ((service, element) => new DecodeAudioRender(service, this.mediaUrl, element as HTMLVideoElement, (current, next) => !!current && (service.getQueueController()?.sameAlbum?.(current, next) ?? false), this.audioCache, this.recordDiagnostic)),
    });
    const service = new PlaybackService({...web.transport, progressPlayback: async (id, payload) => {
      const before = service.getSnapshot();
      await web.transport.progressPlayback(id, payload);
      const after = service.getSnapshot();
      // A real ended report was acknowledged for this exact owner. Queue refresh/hold still
      // decides whether it finished or is about to advance; an old acknowledgement cannot.
      if (payload.state === 'ended' && this.current(stackReference) && before.phase === 'ended' && after.phase === 'ended' && !after.ended &&
          before.intentId === after.intentId && before.playbackRun === after.playbackRun && before.session?.id === id && before.session.generation === payload.generation && after.session?.id === id && after.session.generation === payload.generation) {
        const previous = stackReference.confirmedEnd;
        if (previous?.sessionId !== id || previous.generation !== payload.generation || previous.intentId !== after.intentId) {
          stackReference.confirmedEnd = {sessionId: id, generation: payload.generation, intentId: after.intentId, view: stackReference.queue.getSnapshot().view};
        }
      }
    }}, this.env.key, {...this.env.service, queueRequired: true, deferRecovery: true, pageVisibility: documentVisibility, beforeFreshPlayback: async () => {
      // The copied real browser probe is the capability source. Node's media-port tests have
      // no browser; they exercise the actual server's baseline planning instead.
      if (this.env.createRender !== null && !this.env.createPlayer && typeof indexedDB !== 'undefined') {
        try { const eq = await loadDeviceEqualizer(); if (this.current(stackReference)) stackReference.web.applyEqualizer(eq); }
        catch { if (this.current(stackReference)) { this.runtimeProblem = 'equalizer_unavailable'; this.sync(); } }
      }
      stackReference.capabilityRejected = false;
      if (!this.env.capabilities && (typeof document === 'undefined' || typeof window === 'undefined')) return;
      try {
        const profile = await (this.env.capabilities ?? browserClientProfile)();
        if (!this.current(stackReference)) return;
        // Only an accepted or already-current document admits the start. A failed or
        // superseded publication of this same document is retried once, then refused.
        let published = await this.capabilityPublisher.publish(profile, life.signal);
        if (!this.current(stackReference)) return;
        if (published === 'failed') published = await this.capabilityPublisher.publish(profile, life.signal);
        if (!this.current(stackReference)) return;
        if (published === 'failed') problem('capability_rejected');
      } catch (error) {stackReference.capabilityRejected = true;throw error;}
    }});
    const queue = new V1QueuePlayer({http, api: web.transport, playback: service, viewer: this.source.authority, events: this.source.events, transitions: () => this.queueTransitions, key: this.env.key, musicDefaults: this.source.settings.musicDefaults, audiobookPreferences: this.source.settings.audiobookPreferences, quality: this.source.settings.defaultQuality, retainQueueIds: () => this.retainedQueueIds(stackReference),
      retireQueuedSelection: async (session, isUnowned) => {
        // Use this stack's original viewer-bound API, never a latest transport or
        // raw stop fallback. The private registration guard survives every await.
        const current = () => this.current(stackReference) && !life.signal.aborted && isUnowned();
        if (!current()) return false;
        const cleanupHttp: V1Http = {send: async request => {
          if (!current()) throw new DOMException('Queue selection retired', 'AbortError');
          const result = await inner.send({...request, signal: life.signal});
          if (!current()) throw new DOMException('Queue selection retired', 'AbortError');
          return result;
        }};
        await new SessionsClient(cleanupHttp, this.env.timers).stop(session.id, undefined, life.signal);
        return current();
      }});
    // The actual controller retains its ordered stop barrier across consecutive channel starts.
    const channelStarts = this.source.preparingStarts?.attach(id => service.getSnapshot().session?.id === id);
    service.useChannelPlayback(ownedWebChannels(this.channelControl, channelStarts, channel => {
      const operation = this.operation, original = service.getSnapshot();
      return () => this.current(stackReference) && !life.signal.aborted && this.operation === operation &&
        service.getSnapshot().intentId === original.intentId && service.getSnapshot().playbackRun === original.playbackRun &&
        original.channel === channel && service.getSnapshot().channel === channel;
    }));
    queue.setCompletionPolicy(() => 'hold');
    const api = {request: <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal) => this.source.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, life.signal]) : life.signal)};
    const stackReference: Stack = {mode, web, service, queue, http, life, channelStarts, offers: new PlayerOffersService({api, scope: this.source.contentScope}), optionsClient: new PlaybackOptionsClient(http), options: null, offs: [], expanded: false, retired: false};
    web.external = state => handleExternalTransport(togetherHostOf(this), stackReference, state);
    return stackReference;
  }

  private activate(stack: Stack): void {
    stack.queue.followPostPlay(this.source.settings.postPlay);
    if (stack.mode === 'local') {stack.web.beginLocal();if (this.video) this.bindElement(stack);return;}
    stack.service.adoptAudioEffectsPreference(this.source.settings.audioEffects());
    stack.offs.push(stack.web.bind(stack.service, stack.queue), stack.queue.connect());
    stack.offs.push(stack.service.subscribe(() => this.sync()), stack.queue.subscribe(() => this.sync()), stack.offers.subscribe(() => this.sync()));
    if (this.env.createRender !== null && !this.env.createPlayer) stack.offs.push(subscribeDeviceEqualizer(settings => {
      if (!this.current(stack)) return;
      try { stack.web.applyEqualizer(settings); }
      catch (error) { this.runtimeProblem = 'equalizer_unavailable'; this.sync(); throw error; }
    }));
    stack.offs.push(this.source.events.on('session.updated', event => { if (this.current(stack) && event.resource?.kind === 'session' && event.resource.id) stack.web.transport.sessionUpdated(event.resource.id, event.revision, event.data); }));
    if (this.video) this.bindElement(stack);
    void stack.service.startRecovery();
  }

  /** Bind the shell's actual video node. Route changes, a dock morph and portal content never
   * call this again. Unmount is source disappearance, so it disposes the complete runtime. */
  mount(video: VideoLike): () => void {
    this.assertCurrent();
    if (this.video) problem('playback_surface_already_mounted');
    this.video = video;
    if (this.env.subtitleSurface !== false && compatibleVideo(video) && SubtitleSurface.supported(video)) this.surface = new SubtitleSurface(video);
    this.surface?.applyPreferences(subtitlePreferencesImpl(subtitleHostOf(this), this.stack));
    this.bindElement(this.stack);
    const volume = () => this.sync();
    video.addEventListener('volumechange', volume);
    this.lifecycle.push(() => video.removeEventListener('volumechange', volume));
    // A paused title still receives buffer progress. Publish its actual ranges to the seek rail.
    video.addEventListener('progress', volume);
    this.lifecycle.push(() => video.removeEventListener('progress', volume));
    // The optional native local port is only known after this same persistent host
    // mounts. Publish that actual capability change to the app attachment.
    this.sync();
    return () => this.dispose();
  }
  private bindElement(stack: Stack): void {
    if (this.video && !stack.unmount) stack.unmount = stack.web.mount(this.video, {
      // One timeline for both drawn paths: the page's text cues and the drawn overlay.
      timeline: offset => { this.subtitleTimeline = offset; this.surface?.timeline(offset); stack.drawnSurface?.timeline(offset); },
      reset: () => { this.subtitleTimeline = null; this.surface?.reset(); stack.drawnSurface?.timeline(null); },
    }, () => wirelessPlayback(this.video) ? 'airplay' : undefined);
  }

  private rememberCurrent(): void {
    const p = this.snapshot?.presentation, stack = this.stack;
    if (!p || p.fence.kind === 'queue' && !stack.libraryId) return;
    const local = this.dock.getSnapshot().local;
    if (!local || !sameFence(local.fence, p.fence)) return;
    const localState = stack.local?.controller.getSnapshot();
    this.bookmarks.set(this.key(p.fence), {playback: {...local, playing: false}, subject: p.subject, queueId: p.fence.kind === 'queue' ? stack.queue.currentSession()?.queue?.queueId : undefined, libraryId: stack.libraryId, channel: stack.channel, posterUrl: p.posterUrl, backdropUrl: p.backdropUrl, durationSeconds: p.durationSeconds, rate: p.rate,
      ...(p.fence.kind === 'local' && localState?.observation && stack.local ? {local: {lease: stack.local.lease, index: localState.index, positionMs: localState.observation.positionMs}} : {})});
    // Offered bookmarks stay restorable while newer sources fill the private history cache.
    const dock = this.dock.getSnapshot(), retained = new Set([dock.local, dock.parkedAudio, dock.resumeVideo].flatMap(value => value ? [this.key(value.fence)] : []));
    for (const key of this.bookmarks.keys()) {
      if (this.bookmarks.size <= 8) break;
      if (!retained.has(key)) this.bookmarks.delete(key);
    }
    this.trimLocalLeases();
  }
  private releaseLocalLease(lease: LocalQueueLease): void {
    if (lease.released) return;lease.released = true;this.localLeases.delete(lease);this.publishRetention();
    void Promise.resolve().then(lease.release).catch(() => {if (!this.disposed) {this.runtimeProblem = 'local_playback_release_failed';this.sync();}});
  }
  private trimLocalLeases(): void {
    const dock = this.dock.getSnapshot(), retained = [dock.local, dock.parkedAudio, dock.resumeVideo].flatMap(value => value ? [this.bookmarks.get(this.key(value.fence))?.local?.lease] : []);
    for (const lease of this.localLeases) if (lease.retired && !retained.includes(lease)) this.releaseLocalLease(lease);
  }
  /** The canonical create samples these actual authority-owned queues, including the current
   * source until the next subject is known. It owns retention and retirement on the server. */
  private retainedQueueIds(stack: Stack): readonly string[] {
    if (!this.current(stack)) problem('playback_authority_retired');
    const dock = this.dock.getSnapshot(), ids = new Set<string>();
    for (const value of [dock.local, dock.parkedAudio, dock.resumeVideo]) {
      if (!value || value.fence.kind !== 'queue') continue;
      const bookmark = this.bookmarks.get(this.key(value.fence));
      if (!bookmark?.queueId || !sameFence(bookmark.playback.fence, value.fence)) problem('playback_bookmark_unavailable');
      ids.add(bookmark.queueId);
    }
    if (ids.size > 3) problem('invalid_queue_bookmarks');
    return Object.freeze([...ids]);
  }
  private retire(stack: Stack): void {
    if (stack.retired) return;
    clearSleepTimerImpl({clearTimer: timer => this.clearTimer(timer)}, stack);
    clearDrawnSubtitlesImpl(subtitleHostOf(this), stack);
    clearRecordedSidecar(stack, 'text');
    this.mediaSession?.clear();
    // Pause before releasing the actual media and checkpoint its real position. The accepted
    // dock retains only the bookmark, never a second live or hidden media session.
    if (stack.mode === 'local') stack.web.slot.pause();else stack.service.pause();
    const local = this.dock.getSnapshot().local, playback = stack.service.getSnapshot();
    if (local && this.snapshot?.fence && sameFence(local.fence, this.snapshot.fence)) this.dock.update({...local, positionSeconds: stack.local?.controller.getSnapshot().observation?.positionMs !== undefined ? stack.local.controller.getSnapshot().observation!.positionMs / 1000 : playback.pendingSeek?.positionSeconds ?? playback.positionSeconds, playing: false});
    if (this.snapshot?.fence) this.retiredFences.add(this.key(this.snapshot.fence));
    this.trimRetiredFences();
    for (const off of stack.offs.splice(0)) off();
    stack.local?.controller.dispose();
    stack.retired = true;
    stack.confirmedEnd = undefined;
    this.clearSourceResources(stack);
    stack.service.leave(); stack.channelStarts?.dispose(); stack.queue.dispose(); stack.unmount?.(); stack.unmount = undefined;
    stack.web.dispose(); stack.offers.dispose(); stack.life.abort();
    this.surface?.clear();
  }
  private trimRetiredFences(): void {
    while (this.retiredFences.size > 16) {
      const oldest = this.retiredFences.values().next();
      if (oldest.done) break;
      this.retiredFences.delete(oldest.value);
    }
  }
  private replace(mode: Stack['mode'] = 'server'): Stack {
    this.replacing = true;
    this.stack.service.pause(); this.rememberCurrent();
    if (this.snapshot?.fence) { this.choices.stop(this.snapshot.fence.playback); if (this.snapshot.fence.kind === 'queue') this.policy.stop(this.snapshot.fence); }
    this.retire(this.stack);
    this.stack = this.makeStack(mode);
    this.activate(this.stack);
    this.replacing = false;
    this.runtimeProblem = undefined; this.policyView = null;
    this.sync();
    return this.stack;
  }

  async start(input: WebPlaybackStart): Promise<void> {
    this.assertCurrent();
    if (input.entry.available === false || input.entry.missing || !/^[A-Za-z0-9_.-]{1,128}$/.test(input.target.itemId) || input.target.startSeconds !== undefined && !validSeconds(input.target.startSeconds)) problem('invalid_playback_target');
    const aggregate = ['show', 'season', 'album', 'artist', 'book', 'collection'].includes(input.entry.kind);
    if (!aggregate && input.entry.id !== input.target.itemId) problem('invalid_playback_target');
    const serial = ++this.operation;
    const finishRetention = this.retainMediaStart(this.life.signal, () => serial === this.operation);
    const starting = this.beginStarting(entryStartDisplay(input.entry), () => this.start(input)), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    try {
      const stack = this.replace();
      starting.attach(stack);
      const cancel = () => this.abandon(stack, serial);
      starting.stopWith(() => {stopped.abort(); cancel();});
      if (input.entry.id === input.target.itemId) {
        stack.itemId = input.target.itemId; stack.libraryId = input.entry.libraryId;
        stack.subject = entrySubject(input.entry); stack.posterUrl = input.entry.posterUrl; stack.backdropUrl = input.entry.backdropUrl;
      }
      stack.expanded = input.entry.kind !== 'song';
      const current = () => this.current(stack) && serial === this.operation;
      stack.capacityCurrent = current;
      try {if (aggregate) {
        await stack.queue.playSegments([{source: {container: {kind: input.entry.kind, id: input.entry.id}}, anchor: {itemId: input.target.itemId}}], {startSeconds: input.target.startSeconds, isCurrent: () => this.current(stack) && serial === this.operation});
      } else {
        await stack.service.play(input.target.itemId, input.target.startSeconds);
      }} finally {if (stack.capacityCurrent === current) stack.capacityCurrent = undefined;}
      if (stopped.signal.aborted) throw startStopped();
      if (!this.current(stack) || serial !== this.operation) {starting.end(); return;}
      if (this.sessionAccepted(stack)) {starting.accepted(); starting.stopWith(cancel);} else starting.end();
      this.sync();
    } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
    finally {finishRetention();}
  }

  /** PlaybackService-shaped Home effect. The real item supplies display metadata; no fixture or
   * invented title is installed while that read is outstanding. */
  async play(itemId: string, startSeconds?: number, choices?: Readonly<{versionId?: string}>, display?: WebPlaybackStartDisplay): Promise<void> {
    this.assertCurrent();
    if (!/^[A-Za-z0-9_.-]{1,128}$/.test(itemId) || startSeconds !== undefined && !validSeconds(startSeconds)
      || choices?.versionId !== undefined && !/^[A-Za-z0-9._:-]{1,128}$/.test(choices.versionId)) problem('invalid_playback_target');
    const serial = ++this.operation;
    const finishRetention = this.retainMediaStart(this.life.signal, () => serial === this.operation);
    const starting = this.beginStarting(display, () => this.play(itemId, startSeconds, choices, display)), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    try {
      const stack = this.replace();
      starting.attach(stack);
      stack.expanded = true;
      const cancel = () => this.abandon(stack, serial);
      starting.stopWith(() => {stopped.abort(); cancel();});
      const current = () => this.current(stack) && serial === this.operation;
      stack.capacityCurrent = current;
      try {await stack.service.play(itemId, startSeconds, undefined, undefined, undefined, choices?.versionId ? {versionId: choices.versionId} : undefined);} finally {if (stack.capacityCurrent === current) stack.capacityCurrent = undefined;}
      if (stopped.signal.aborted) throw startStopped();
      if (!this.current(stack) || serial !== this.operation) {starting.end(); return;}
      if (this.sessionAccepted(stack)) {starting.accepted(); starting.stopWith(cancel);} else starting.end();
      this.sync();
    } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
    finally {finishRetention();}
  }

  /** A real screen action retains its captured authority and cancellation through POST/adopt.
   * Validate the actual library/item and fresh offers before retiring the playing source. */
  async playFromScreen(target: WebPlaybackScreenTarget, context: WebPlaybackScreenLifetime): Promise<void> {
    const captured = Object.freeze({...target});
    const check = () => {
      this.assertCurrent();
      if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
    };
    check();
    if (![captured.libraryId, captured.itemId, ...(captured.versionId === undefined ? [] : [captured.versionId])].every(id => /^[A-Za-z0-9_.-]{1,128}$/.test(id))
      || !validSeconds(captured.startSeconds) || captured.from !== undefined && !screenListSelector(captured.from, captured.itemId)) problem('invalid_playback_target');
    const serial = ++this.operation, previous = this.stack;
    const finishRetention = this.retainMediaStart(context.signal, () => serial === this.operation);
    const starting = this.beginStarting(captured.display, () => this.playFromScreen(captured, this.retryLifetime())), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    // The reads before a session (the item and its options) share the start's deadline, so a server
    // that never answers ends in the player's own timeout, not an endless ring.
    const readDeadline = createStartDeadline({setTimer: (fn, ms) => this.setTimer(fn, ms), clearTimer: timer => this.clearTimer(timer)});
    try {
      const reading = AbortSignal.any([context.signal, previous.life.signal, this.life.signal, stopped.signal, readDeadline.signal]);
      const item = await readDeadline.race(readPlaybackItem(this.source.api, captured.itemId, reading));
      check();
      if (!this.current(previous) || serial !== this.operation) problem('stale_selection_context');
      if (item.id !== captured.itemId || item.libraryId !== captured.libraryId || !item.available) problem('invalid_playback_target');
      const subject = item.subject;
      starting.identify({subject, ...(item.posterUrl ?? item.backdropUrl ? {posterUrl: item.posterUrl ?? item.backdropUrl} : {}), ...(item.backdropUrl ? {backdropUrl: item.backdropUrl} : {})});
      if (captured.storedResume !== undefined && (typeof captured.storedResume !== 'boolean' || captured.storedResume && (subject.kind !== 'audiobook' || captured.versionId !== undefined))) problem('invalid_playback_target');
      const options = await readDeadline.race(previous.optionsClient.options(item.id, {}, reading));
      readDeadline.clear();
      check();
      if (!this.current(previous) || serial !== this.operation) problem('stale_selection_context');
      if (options.itemId !== item.id || !options.versions.length || captured.versionId !== undefined && !options.versions.some(version => version.id === captured.versionId)) problem('playback_version_unavailable');
      // A title-page choice names its actual offered version. Consume it only
      // after this original screen has passed fresh item/options admission.
      const pending = takePendingTrackChoice(item.id);
      const versionId = captured.versionId ?? pending?.versionId ?? (pending ? options.preferred?.versionId : undefined);
      const version = options.versions.find(candidate => candidate.id === versionId);
      if (pending?.versionId && pending.versionId !== versionId || pending && !version) problem('playback_version_unavailable');
      const audio = pending?.audioStreamIndex === undefined ? undefined : version?.audio.find(track => audioStreamIndex(track.id) === pending.audioStreamIndex);
      if (pending?.audioStreamIndex !== undefined && !audio) problem('playback_audio_unavailable');
      const subtitle = pending?.subtitle;
      if (subtitle && subtitle !== 'off' && subtitle.resourceId
        && !version?.subtitles.some(track => track.id === subtitle.resourceId)) problem('playback_subtitle_unavailable');
      // Explicit version choice never remembers a caller's old resume position (§21).
      const startSeconds = captured.storedResume ? undefined : captured.versionId === undefined ? captured.startSeconds : (options.resume?.positionMs ?? 0) / 1000;
      if (startSeconds !== undefined && !validSeconds(startSeconds)) problem('invalid_playback_position');
      const stack = this.replace();
      if (pending?.subtitle !== undefined && version) stack.preplay = {versionId: version.id, progress: {choice: {...pending, audioStreamIndex: undefined}}};
      stack.itemId = item.id; stack.libraryId = item.libraryId; stack.subject = subject;
      stack.posterUrl = item.posterUrl; stack.backdropUrl = item.backdropUrl; stack.expanded = subject.kind !== 'music';
      starting.attach(stack);
      const deadline = createStartDeadline({setTimer: (fn, ms) => this.setTimer(fn, ms), clearTimer: timer => this.clearTimer(timer)}, () => !!stack.startFailure?.isCurrent());
      const lifetime = AbortSignal.any([context.signal, stack.life.signal, this.life.signal, deadline.signal, stopped.signal]);
      const current = () => {
        if (lifetime.aborted || !this.current(stack) || serial !== this.operation) return false;
        try {check();return true;} catch {return false;}
      };
      const cancel = () => this.abandon(stack, serial);
      lifetime.addEventListener('abort', cancel, {once: true});
      try {
        stack.capacityCurrent = current;
        // The queue plays on through what the item belongs to (PB-1, MU-1, MU-2): the list it was
        // pressed in, else its show, album or book anchored on it; a lone title is one entry.
        const from = captured.from ?? (options.container ? {container: options.container} : undefined);
        await deadline.race(stack.queue.playItem(item.id, startSeconds, undefined, undefined, undefined,
          {...(versionId ? {versionId} : {}), ...(audio ? {audioTrackId: audio.id} : {})}, {signal: lifetime, isCurrent: current, propagateError: true, ...(from ? {from} : {})}));
        if (!current()) problem(deadline.expired() && !context.signal.aborted ? 'playback_start_timeout' : 'stale_selection_context');
        const session = stack.queue.currentSession();
        if (stack.service.getSnapshot().phase === 'error' || session?.itemId !== item.id
          || versionId !== undefined && session.versionId !== versionId) problem('playback_start_failed');
        // Accepted: the watch now runs until the real dock is drawn, and Stop ends this session.
        starting.accepted(); starting.stopWith(cancel);
        this.sync();
      } catch (error) {cancel(); if (deadline.expired() && !context.signal.aborted && !stopped.signal.aborted) problem('playback_start_timeout'); throw error;}
      finally {deadline.clear(); if (stack.capacityCurrent === current) stack.capacityCurrent = undefined; lifetime.removeEventListener('abort', cancel);}
    } catch (error) {
      const display = starting.display();
      starting.end(); if (stopped.signal.aborted) throw startStopped();
      const reason = readDeadline.expired() && !context.signal.aborted ? new Error('playback_start_timeout') : error;
      const outcome = this.reportStartProblem(reason, display, () => this.playFromScreen(captured, this.retryLifetime()), serial);
      throw outcome === 'unhandled' ? reason : markPresentedInPlayer(reason);
    }
    finally {readDeadline.clear(); finishRetention();}
  }

  /** A start that failed before it could play is drawn inside the player, with its reason, the
   * title's picture, Try again and Back; never a bare toast (J-17). */
  private reportStartProblem(error: unknown, display: WebPlaybackStartDisplay | undefined, retry: () => Promise<void>, serial: number, sentence?: string): 'shown' | 'quiet' | 'unhandled' {
    if (this.disposed) return 'unhandled';
    const value = error as {name?: unknown; code?: unknown; message?: unknown} | null;
    const code = typeof value?.code === 'string' && value.code ? value.code
      : typeof value?.message === 'string' && /^[a-z][a-z0-9_]{2,63}$/.test(value.message) ? value.message : 'playback_start_failed';
    // A newer press, the page that pressed it going away, or a cancel: nothing to say, and no toast.
    if (serial !== this.operation || value?.name === 'AbortError' || code === 'stale_selection_context') return 'quiet';
    // A refused session already draws its own failure in the player.
    if (this.stack.startFailure?.isCurrent()) return 'quiet';
    const view: WebPlaybackStartTimedOut = Object.freeze({key: String(++this.startingKeys), ...(code === 'playback_start_timeout' ? {} : {code}), ...(display ? {display} : {}), ...(sentence ? {sentence} : {}),
      retry: async () => {if (this.startTimedOut !== view) return; this.startTimedOut = undefined; this.sync(); await retry();},
      close: () => {if (this.startTimedOut === view) {this.startTimedOut = undefined; this.sync();}}});
    this.startTimedOut = view; this.sync();
    return 'shown';
  }

  /** Canonical room follower and local corrections share this runtime's one actual engine. */
  bindGroup(service: GroupSessionService, isCurrent: () => boolean, reportError: (error: unknown) => void): () => void {
    return bindRuntimeTogether(togetherHostOf(this), service, isCurrent, reportError);
  }
  /** Current room occurrence only; equal title IDs do not prove a room's source was adopted. */
  groupPlaybackMatches(fence: PlaybackFence, groupId: string, entryId: string): boolean {
    return groupPlaybackMatchesImpl(togetherHostOf(this), fence, groupId, entryId);
  }

  /** Actual installed optional port and captured scope, not a platform/UA inference. */
  canPlayLocalQueue(): boolean {
    return this.current() && !!this.source.localScope && this.stack.web.slot.canLoadLocal && !this.group?.active();
  }

  /** Complete receipt-verified native preparation, not a server queue or a first-item alias.
   * The app transfers ownership of its exact preparation through this call, including failure. */
  async playLocalQueue(resource: WebLocalQueueResource, context: WebPlaybackScreenLifetime): Promise<void> {
    let lease: LocalQueueLease | undefined, stack: Stack | undefined, serial = this.operation, starting: Starting | undefined;
    const stopped = new AbortController();
    const finishRetention = this.retainMediaStart(context.signal, () => serial === this.operation);
    try {
      try {
        this.assertCurrent();
        if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
        const scope = this.source.localScope;
        if (!scope || !this.canPlayLocalQueue()) problem('local_playback_unavailable');
        const preparation = captureLocalQueue(resource.preparation, scope);
        lease = {preparation, release: () => resource.release(), retired: false, released: false};this.localLeases.add(lease);
        serial = ++this.operation;
        starting = this.beginStarting(localStartDisplay(preparation.items[0]), async () => {
          // The stopped start released this resource; Try again never replays it, only a fresh one.
          if (!resource.reacquire) problem('local_playback_unavailable');
          await this.playLocalQueue(await resource.reacquire(), this.retryLifetime());
        });
        starting.stopWith(() => stopped.abort());
        stack = this.replace('local');
        starting.attach(stack);
        const owned = stack, mine = lease, owner = serial;
        const cancel = () => {this.abandon(owned, owner); mine.retired = true; this.trimLocalLeases();};
        const controller = this.bindLocalQueue(stack, lease);
        await controller.start({signal: AbortSignal.any([context.signal, stopped.signal]), isCurrent: () => this.current(stack!) && serial === this.operation && context.isCurrent()});
        if (!this.current(stack) || serial !== this.operation || context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
        starting.accepted(); starting.stopWith(cancel);
        this.sync();
      } catch (error) {
        starting?.end();
        if (stack && this.current(stack) && serial === this.operation) {this.replacing = true;this.retire(stack);this.stack = this.makeStack();this.activate(this.stack);this.replacing = false;this.sync();}
        if (lease) {lease.retired = true;this.trimLocalLeases();}
        else await resource.release();
        throw stopped.signal.aborted ? startStopped() : error;
      }
    } finally {finishRetention();}
  }
  private bindLocalQueue(stack: Stack, lease: LocalQueueLease): WebLocalQueue {
    if (!this.current(stack) || stack.mode !== 'local' || lease.released || !this.source.localScope || !sameLocalScope(lease.preparation.scope, this.source.localScope)) problem('local_playback_unavailable');
    lease.retired = false;
    const controller = new WebLocalQueue({scope: this.source.localScope, run: ++this.localRun, port: stack.web.slot, preparation: lease.preparation,
      isCurrent: () => this.current(stack), release: async () => {lease.retired = true;this.trimLocalLeases();},
      reportError: () => {if (this.current(stack)) {this.runtimeProblem = 'local_playback_failed';this.sync();}}});
    stack.local = {controller, lease};stack.offs.push(controller.subscribe(this.sync));
    return controller;
  }

  readPlaybackInfo(expected: QueuePlaybackFence, signal: AbortSignal): Promise<WebPlaybackInfoRead | null> {
    return readPlaybackInfoImpl(queueHostOf(this), expected, signal);
  }
  readVersions(expected: QueuePlaybackFence, signal: AbortSignal, options?: Readonly<{excludeFailed?: boolean}>): Promise<WebPlaybackVersionChoices | null> {
    return readVersionsImpl(queueHostOf(this), expected, signal, options);
  }
  async playVersion(request: WebPlaybackVersionChoice): Promise<void>;
  async playVersion(request: WebPlaybackVersionStart, isCurrent: (context: SelectionContext) => boolean): Promise<void>;
  async playVersion(request: WebPlaybackVersionStart | WebPlaybackVersionChoice, isCurrent?: (context: SelectionContext) => boolean): Promise<void> {
    return playVersionImpl(queueHostOf(this), request, isCurrent);
  }

  /** Canonical catalogue selections supply immutable, capability-checked segments.
   * This port remains owned by the same viewer runtime and real queue/media stack. */
  cataloguePlayback(): Pick<V1QueuePlayer, 'playSegments'> {
    this.assertCurrent();
    this.cataloguePort ??= Object.freeze<Pick<V1QueuePlayer, 'playSegments'>>({
      playSegments: async (segments, options = {}) => {
        if (!options.signal || typeof options.isCurrent !== 'function') problem('stale_selection_context');
        return playQueueSegmentsImpl(queueHostOf(this), segments, {signal: options.signal, isCurrent: options.isCurrent}, options.startSeconds);
      },
    });
    return this.cataloguePort;
  }

  /** Append checked catalogue segments without inventing a Home document context. */
  catalogueQueue(): CatalogueSelectionQueuePort | undefined {
    return catalogueQueueImpl(queueHostOf(this));
  }

  playFromQueue(source: WebPlaybackQueueSource, context: WebPlaybackScreenLifetime, options?: Readonly<{shuffle?: boolean; display?: WebPlaybackStartDisplay}>): Promise<void> {
    return playFromQueueImpl(queueHostOf(this), source, context, options);
  }

  /** The shell supplies the same current Home context guard used by its selection controller.
   * No route/document revision is guessed from the playback authority. */
  selectionPlayback(isCurrent: (context: SelectionContext) => boolean): HomeSelectionPlaybackPort {
    return {
      play: request => playSelectionImpl(queueHostOf(this), request, isCurrent),
      enqueue: request => enqueueSelectionImpl(queueHostOf(this), request, isCurrent),
      playChannel: (channel, context, signal) => this.playChannel(channel, context, signal, isCurrent),
    };
  }
  private selectionCurrent(context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): void {
    this.assertCurrent();
    const scope = context.scope, authority = this.authority;
    if (signal.aborted) problem('cancelled');
    if (scope.serverId !== authority.serverId || scope.authority !== authority.authority || scope.accountId !== authority.accountId || scope.profileId !== authority.profileId || authority.viewerFence !== undefined && scope.privacyFence !== authority.viewerFence || !current(context)) problem('stale_selection_context');
  }
  playChannel(channel: HomeChannel, context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): Promise<void> {
    return playChannelImpl(liveHostOf(this), channel, context, signal, current);
  }
  playFromGuide(channel: GuideChannel, context: WebPlaybackScreenLifetime): Promise<void> {
    return playFromGuideImpl(liveHostOf(this), channel, context);
  }

  private offers(stack: Stack, playback: PlaybackSnapshot): PlayerOffersData | null {
    const binding = stack.offerBinding, response = stack.offers.getSnapshot(), data = response.data;
    if (!this.current(stack) || !binding || binding.api !== this.source.api || binding.authority !== this.authority || binding.sessionKey !== stack.sessionKey || binding.sessionKey !== `${playback.intentId}:${playback.session?.id}:${playback.session?.generation}` || binding.playbackRun !== playback.playbackRun) return null;
    if (response.phase !== 'ready' || response.error || response.scope.serverId !== this.source.contentScope.serverId || response.scope.viewerId !== this.source.contentScope.viewerId || !data) return null;
    if (response.query?.libraryId !== binding.libraryId || response.query.itemId !== binding.itemId || response.query.sessionId !== binding.sessionId || stack.libraryId !== binding.libraryId || stack.itemId !== binding.itemId) return null;
    if (data.scope.serverId !== this.authority.serverId || data.scope.libraryId !== binding.libraryId || data.scope.itemId !== binding.itemId || playback.itemId !== binding.itemId || data.current?.sessionId !== binding.sessionId || playback.session?.id !== binding.sessionId || data.current.generation !== binding.generation || playback.session.generation !== binding.generation) return null;
    // Bind the actual validated response under this captured API/authority and media source.
    // Step 7: a moved viewer fence on the offers read is a newer answer for the same session;
    // a real loss of access answers 401/403, which stops the playback (sync's offers check).
    return data;
  }
  private presentations(current?: WebPlaybackPresentation): readonly WebPlaybackPresentation[] {
    const out = current ? [current] : [], dock = this.dock.getSnapshot();
    for (const value of [dock.local, dock.parkedAudio, dock.resumeVideo]) {
      if (!value || out.some(p => sameFence(p.fence, value.fence))) continue;
      const saved = this.bookmarks.get(this.key(value.fence));
      if (!saved) continue;
      out.push({fence: value.fence, subject: saved.subject, kind: saved.subject.kind === 'channel' ? 'video' : saved.subject.kind, posterUrl: saved.posterUrl, backdropUrl: saved.backdropUrl, durationSeconds: saved.durationSeconds, positionSeconds: value.positionSeconds, playing: false, expanded: false, finished: false, controls: [], volume: this.video?.volume ?? 1, muted: this.video?.muted ?? false, rate: saved.rate, policy: null});
    }
    return Object.freeze(out);
  }
  private fence(stack: Stack, playback: PlaybackSnapshot, session: Session | undefined, offers: PlayerOffersData | null): QueuePlaybackFence | ChannelPlaybackFence | undefined {
    const channel = playback.channel;
    if (channel && playback.session && playback.session.generation > 0 && playback.playbackRun > 0) {
      const common = {kind: 'channel' as const, playback: {scope: queueScope({...this.authority, controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'}), run: playback.playbackRun}, sessionId: playback.session.id, generation: playback.session.generation, channelId: channel.channelId, channelGeneration: channel.generation, sourceId: channel.sourceId};
      return channel.kind === 'live-source' ? playbackFence({...common, channelKind: 'live-source'}) : playbackFence({...common, channelKind: 'library-channel'});
    }
    if (!session?.queue || !session.itemId || !offers?.current || !playback.session || !playback.playbackRun) return;
    return playbackFence({kind: 'queue', playback: {scope: queueScope({...this.authority, controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'}), run: playback.playbackRun}, sessionId: session.id, itemId: session.itemId, sourceId: offers.current.sourceId, entryId: session.queue.entryId, generation: playback.session.generation, ...(stack.showId ? {showId: stack.showId} : {}), ...(stack.seasonId ? {seasonId: stack.seasonId} : {})});
  }

  private syncLocal(stack: Stack): void {
    this.clearTimer(this.timer);this.timer = undefined;
    const state = stack.local?.controller.getSnapshot(), member = state?.preparation.items[state.index], title = member?.title, fence = state?.fence, observed = state?.observation;
    let presentation: WebPlaybackPresentation | undefined;
    if (title && member) {
      const subject = localMemberSubject(member);
      stack.subject = subject;
      if (subject.kind === 'music' && !stack.placed) stack.expanded = false;
      stack.placed = true;
      if (fence && observed && !['idle', 'loading'].includes(observed.state)) {
        this.choices.begin(fence.playback);this.choices.bindSource(fence);
        const positionSeconds = Math.min(member.durationMs, observed.positionMs) / 1000, finished = state.phase === 'ended', active = !finished && state.phase !== 'error';
        const controls: WebPlaybackPresentation['controls'][number][] = finished ? ['replay'] : active ? [observed.state === 'paused' ? 'play' : 'pause'] : [];
        if (active && member.durationMs > 0) controls.push('seek');
        if (active && state.index + 1 < state.preparation.items.length) controls.push('next');
        if (active && (state.index > 0 || positionSeconds > 3)) controls.push('previous');
        // The optional opaque native port has no volume command. Do not offer a
        // slider backed by the idle browser video's unrelated volume property.
        const local: DockPlayback = {kind: subject.kind, fence, queue: null, positionSeconds, playing: observed.state === 'playing', expanded: stack.expanded};
        if (sameFence(this.dock.getSnapshot().local?.fence, fence)) this.dock.update(local);else this.dock.start(local);
        presentation = {fence, subject, kind: subject.kind, durationSeconds: member.durationMs / 1000, positionSeconds,
          playing: local.playing, expanded: stack.expanded, finished, controls, volume: observed.volume ?? 1, muted: observed.muted ?? false, rate: observed.rate, policy: null};
      }
    }
    this.snapshot = Object.freeze({nativeVideoSurfaceActive: !!presentation && presentation.kind === 'video' && stack.web.slot.nativeSurfaceActive,
      authority: this.authority, sequence: ++this.sequence, playback: stack.service.getSnapshot(), dock: this.dock.getSnapshot(), fence, subject: stack.subject,
      presentation, presentations: this.presentations(presentation), local: state, queueView: null, offers: null, options: null, problem: state?.problem ?? this.runtimeProblem, disposed: false,
      ...this.startingFields(stack, !!presentation)});
    this.mediaSession?.sync(presentation);
    this.rememberCurrent();this.trimLocalLeases();this.publishRetention();for (const listener of [...this.listeners]) listener();
  }

  private sync = (): void => {
    if (this.replacing) return;
    if (this.syncing) { this.again = true; return; }
    if (this.disposed) return;
    if (!this.source.isCurrent()) { this.dispose(); return; }
    this.syncing = true;
    try { do {
      this.again = false;
      const stack = this.stack, playback = stack.service.getSnapshot();
      if (stack.mode === 'local') {this.syncLocal(stack);continue;}
      if (['unauthorized', 'forbidden', 'permission_changed'].includes(stack.offers.getSnapshot().error?.code ?? '')) { this.dispose(); return; }
      if (!this.group?.active() && this.groupRate !== 1) this.groupRate = 1;
      if (playback.channel && playback.linear) stack.subject = {kind: 'channel', id: playback.channel.channelId, sourceId: playback.channel.sourceId, channelName: playback.linear.name, programmeTitle: playback.linear.programme?.title};
      const heldSession = stack.web.session(playback.session?.id) ?? stack.queue.currentSession();
      const session = heldSession?.id === playback.session?.id ? heldSession : undefined;
      const prior = this.snapshot?.fence;
      if (prior && playback.session && (prior.sessionId !== playback.session.id || prior.generation !== playback.session.generation)) {
        this.retiredFences.add(this.key(prior));
        this.trimRetiredFences();
      }
      selectSourceImpl(queueHostOf(this), stack, playback, session);
      const offers = this.offers(stack, playback), fence = this.fence(stack, playback, session, offers);
      if (this.policyAdvanceFence && (!fence || !sameFence(this.policyAdvanceFence, fence))) this.policyAdvanceFence = undefined;
      let presentation: WebPlaybackPresentation | undefined;
      if (fence && stack.subject) {
        this.choices.begin(fence.playback); this.choices.bindSource(fence);
        const savedSpeed = stack.subject.kind === 'music' || stack.subject.kind === 'channel' ? 1 : stack.subject.kind === 'audiobook' ? this.source.settings.audiobookSpeed() : this.source.settings.videoSpeed();
        const speed = this.group?.matches(fence) ? this.groupRate : this.choices.get('speed', fence.playback, savedSpeed);
        stack.service.setPlaybackRate(speed); stack.web.setRate(speed);
        const timing = {positionSeconds: playback.pendingSeek?.positionSeconds ?? playback.positionSeconds, playing: playback.intent === 'playing' && playback.phase !== 'ended' && playback.phase !== 'error', expanded: stack.expanded};
        // v1 is not the strict player-core QueueSnapshot protocol. Keep its genuine queue
        // receipt/window separately; never rename occurrence IDs to pass that parser.
        let local: DockPlayback;
        if (fence.kind === 'channel') {
          if (stack.subject.kind !== 'channel') problem('invalid_playback_subject');
          local = {fence, kind: 'live', queue: null, ...timing};
        } else {
          if (stack.subject.kind === 'channel') problem('invalid_playback_subject');
          local = {fence, kind: stack.subject.kind, queue: null, ...timing};
        }
        const transition = sameFence(this.dock.getSnapshot().local?.fence, fence) ? this.dock.update(local) : this.dock.start(local);
        // Replacement pre-applied the retiring source's effects. No old fence can command the
        // new session; externally requested transitions use onTransition below.
        if (transition.effects.some(effect => !this.retiredFences.has(this.key(effect.playback.fence)))) this.runtimeProblem = 'unapplied_dock_effect';
        if (fence.kind === 'queue') this.policy.begin(fence, offers?.markers?.markers ?? [], playback.duration || undefined);
        const queueState = stack.queue.getSnapshot(), queue = queueState.view, receipt = stack.confirmedEnd;
        const ended = fence.kind === 'queue' && playback.phase === 'ended' && !playback.ended && queueState.phase === 'ready' && !queueState.error;
        const confirmed = ended && receipt?.sessionId === fence.sessionId && receipt.generation === fence.generation && receipt.intentId === playback.intentId &&
          queue && queue !== receipt.view && queue.current?.id === fence.sessionId && queue.current.generation === fence.generation && queue.queue.currentEntryId === fence.entryId;
        const completion = confirmed && queueState.completion?.sessionId === fence.sessionId && queueState.completion.intentId === playback.intentId ? queueState.completion : null;
        const listening = stack.queue.listening.getSnapshot();
        const listeningFinished = confirmed && stack.subject.kind !== 'video' &&
          (!queue.next.available || listening.stopped);
        // Start the end countdown only after the actual controller has published its hold.
        // With a zero countdown, consuming an earlier raw media end loses the advance.
        const savedModes = this.source.settings.skipModes();
        // A member's unattended device preference cannot independently move the room clock.
        // Manual offered skips use the genuine room transport and its permission/revision.
        const inGroup = !!this.group?.matches(fence);
        const modes: Record<string, SkipMode> = {...savedModes};
        if (inGroup) for (const kind of Object.keys(modes)) if (modes[kind] === 'auto') modes[kind] = 'ask';
        const result = fence.kind === 'queue' ? this.policy.update({fence, nowMs: (this.env.timers?.now ?? Date.now)(), positionSeconds: Math.min(playback.duration || Infinity, playback.positionSeconds), durationSeconds: playback.duration, rate: speed, playing: local.playing, expanded: stack.expanded, ended: !!completion && !inGroup, kind: stack.subject.kind === 'video' ? stack.subject.item.kind : stack.subject.kind, modes, postPlay: inGroup ? null : queue?.postPlay ?? null, ...(playback.phase === 'error' ? {attention: playback.ended?.reason === 'terminated' ? 'owner-stopped' as const : 'playback-error' as const} : {})}) : null;
        this.policyView = result?.view ?? null;
        for (const command of result?.commands ?? []) {
          if (command.kind === 'expand') { stack.expanded = true; this.dock.expand(fence); }
          else if (command.kind === 'seek') { stack.service.seek(command.positionSeconds); stack.service.markerSkipped(command.markerId, 'automatic', playback.positionSeconds); }
          else {
            this.policyAdvanceFence = command.fence;
            queueMicrotask(() => { if (this.matches(command.fence)) void advancePolicyImpl(queueHostOf(this), stack, command).catch(() => { if (this.current(stack)) { this.runtimeProblem = 'playback_advance_failed'; this.sync(); } }); });
          }
        }
        const finished = !inGroup && !!(completion && !this.policyView?.attention && (!completion.nextAvailable || !completion.postPlay.autoplay || this.policyView?.replay) || listeningFinished);
        // f6e Replay restarts this held occurrence; attention still owns the
        // modal and must not be relabelled as the ordinary finished card.
        const stillWatchingReplay = fence.kind === 'queue' && stack.subject.kind === 'video' && !!completion
          && this.policyView?.attention === 'still-watching' && !this.group?.active()
          && session?.kind === 'vod' && session.role === 'local' && !!session.versionId
          && session.itemId === fence.itemId && session.queue?.queueId === queue?.queue.id && session.queue?.entryId === fence.entryId;
        const controls: WebPlaybackPresentation['controls'][number][] = finished || stillWatchingReplay ? ['replay'] : playback.phase === 'ended' || playback.phase === 'error' ? [] : [playback.intent === 'playing' ? 'pause' : 'play'];
        if (!playback.ended && (finished || ['ready', 'starting'].includes(playback.phase))) controls.push('seek');
        // A book part with known chapters moves by chapter, across parts (MU-7).
        const chapterNext = fence.kind === 'queue' ? stack.queue.chapterTarget('next') : undefined, chapterPrevious = fence.kind === 'queue' ? stack.queue.chapterTarget('previous') : undefined;
        if (fence.kind === 'queue' && (chapterNext !== undefined ? !!chapterNext : queue?.next.available) && !playback.ended) controls.push('next');
        if (fence.kind === 'queue' && queue && (chapterPrevious !== undefined ? !!chapterPrevious : queue.queue.entries.findIndex(entry => entry.id === fence.entryId) > 0 || playback.positionSeconds > 3) && !playback.ended) controls.push('previous');
        if (this.source.settings.webVolume() && this.video) controls.push('volume');
        if (this.group?.matches(fence)) {
          const group = this.group.active();
          if (!group?.you.mayControl) controls.splice(0, controls.length, ...controls.filter(value => value === 'volume'));
          else {
            const index = group.queue.findIndex(entry => entry.entryId === group.clock.entryId);
            if (group.queue.slice(index + 1).some(entry => entry.available && entry.itemId) && !controls.includes('next')) controls.push('next');
            if (group.queue.slice(0, index).some(entry => entry.available && entry.itemId) && !controls.includes('previous')) controls.push('previous');
          }
        }
        presentation = {fence, subject: stack.subject, kind: stack.subject.kind === 'channel' ? 'video' : stack.subject.kind, posterUrl: stack.posterUrl, backdropUrl: stack.backdropUrl, durationSeconds: playback.duration, positionSeconds: local.positionSeconds, playing: local.playing, expanded: stack.expanded, finished, controls, volume: this.video?.volume ?? 1, muted: this.video?.muted ?? false, rate: speed, policy: this.policyView, ...(playback.channel && playback.linear ? {seekWindow: {startSeconds: Number(playback.linear.media.windowStartUs) / 1e6, endSeconds: Number(playback.linear.media.windowEndUs) / 1e6}} : {})};
        this.renderSubtitles(stack);
      }
      this.snapshot = Object.freeze({nativeVideoSurfaceActive: !!fence && stack.subject?.kind === 'video' && stack.web.slot.nativeSurfaceActive, authority: this.authority, sequence: ++this.sequence, playback, dock: this.dock.getSnapshot(), fence, subject: stack.subject, presentation, presentations: this.presentations(presentation), queueView: stack.queue.getSnapshot().view, bookChapters: fence?.kind === 'queue' ? stack.queue.bookChaptersView() : null, session, offers, options: stack.options, problem: this.runtimeProblem, disposed: false,
        ...(stack.capacityWait?.isCurrent() ? {capacityWait: stack.capacityWait} : {}),
        ...(stack.startFailure?.isCurrent() ? {startFailure: stack.startFailure} : {}),
        ...this.startingFields(stack, !!presentation)});
      this.mediaSession?.sync(presentation);
      this.advancePreplay(stack, playback, fence);
      this.rememberCurrent();
      this.publishRetention();
      for (const listener of [...this.listeners]) listener();
      this.schedulePolicy();
      if (fence && presentation?.finished && this.dock.getSnapshot().parkedAudio && (!stack.queue.getSnapshot().view?.next.available || !stack.queue.getSnapshot().view?.postPlay.autoplay)) {
        const captured = fence;
        queueMicrotask(() => { if (this.matches(captured)) void this.stop(captured).catch(() => { if (this.current(stack)) { this.runtimeProblem = 'playback_restore_failed'; this.sync(); } }); });
      }
    } while (this.again && !this.disposed); } finally { this.syncing = false; }
  };

  /** Audio was part of the initial start. Advance only this stack's exact
   * subtitle choice once its standing session has published a real plan. */
  private advancePreplay(stack: Stack, playback: PlaybackSnapshot, fence: PlaybackFence | undefined): void {
    advancePreplayImpl(subtitleHostOf(this), stack, playback, fence);
  }

  /** Try again after the player's options and offers couldn't be read (PB-5). */
  retryMetadata(fence: PlaybackFence): void {
    const stack = this.stack;
    if (!this.current(stack) || !this.matches(fence) || this.runtimeProblem !== 'playback_metadata_failed' || !stack.metadataRetry) return;
    this.runtimeProblem = undefined; this.sync();
    stack.metadataRetry();
  }

  /** O-159 watchdog. A start is drawn from its first frame from the pressed card's facts (the
   * starting dock, Stop only), and replaced in place once its real dock exists. */
  private beginStarting(display: WebPlaybackStartDisplay | undefined, retry: () => Promise<void>, options: Readonly<{watchdog?: boolean}> = {}): Starting {
    this.endStarting(); this.startTimedOut = undefined;
    const state: StartingState = {key: String(++this.startingKeys), display, accepted: false, stop: () => {}, retry, timer: undefined};
    if (options.watchdog !== false) state.timer = this.setTimer(() => this.expireStarting(state), START_DEADLINE_MS);
    this.startingState = state; this.sync();
    const mine = () => this.startingState === state;
    return {
      /** The stack whose own presentation means this start is drawn. */
      attach: (stack: Stack) => {if (mine()) {state.stack = stack; this.sync();}},
      identify: (next: WebPlaybackStartDisplay) => {if (mine() && !state.display) {state.display = next; this.sync();}},
      stopWith: (stop: () => void) => {if (mine()) state.stop = stop;},
      accepted: () => {if (mine()) state.accepted = true;},
      end: () => {if (mine()) {this.endStarting(); this.sync();}},
      display: () => state.display,
    };
  }
  /** A Try again after the watchdog belongs to this runtime, not to a page that may be gone. */
  private retryLifetime(): WebPlaybackScreenLifetime {return {signal: this.life.signal, isCurrent: () => !this.disposed && this.source.isCurrent()};}
  /** The idle stack a Stop (or the watchdog) leaves behind: this start's stack retired, nothing half-started. */
  private abandon(stack: Stack, serial: number): void {
    if (!this.current(stack) || serial !== this.operation) return;
    this.replacing = true; this.retire(stack); this.stack = this.makeStack(); this.activate(this.stack); this.replacing = false; this.sync();
  }
  /** A start whose service call has no accept hook of its own is accepted once its session exists. */
  private sessionAccepted(stack: Stack): boolean {
    const state = stack.service.getSnapshot();
    return this.current(stack) && state.phase !== 'error' && !!state.session;
  }
  private endStarting(): void {
    const state = this.startingState;
    if (!state) return;
    this.startingState = undefined; this.clearTimer(state.timer);
  }
  private stopStarting(state: StartingState): void {
    if (this.startingState !== state) return;
    this.endStarting(); state.stop(); this.sync();
  }
  private expireStarting(state: StartingState): void {
    if (this.startingState !== state || !state.accepted || this.disposed) return;
    const snapshot = this.snapshot, code: AppLogCode = !snapshot?.session ? 'playback_not_drawn_session' : !snapshot.offers?.current ? 'playback_not_drawn_offers' : 'playback_not_drawn_presentation';
    this.endStarting(); state.stop();
    this.recordDiagnostic(code, 'warn');
    const timedOut: WebPlaybackStartTimedOut = Object.freeze({key: state.key,
      retry: async () => {if (this.startTimedOut !== timedOut) return; this.startTimedOut = undefined; this.sync(); await state.retry();},
      close: () => {if (this.startTimedOut === timedOut) {this.startTimedOut = undefined; this.sync();}}});
    this.startTimedOut = timedOut; this.sync();
  }
  /** Snapshot fields; the start's own presentation on its own stack ends the watch (drawn). */
  private startingFields(stack: Stack, presented: boolean): Partial<Pick<WebPlaybackRuntimeSnapshot, 'starting' | 'startTimedOut'>> {
    const state = this.startingState;
    if (state && presented && state.stack === stack) this.endStarting();
    // A start that came without the card's facts is named by its queue's own current entry.
    if (state && this.startingState === state && !state.display && state.stack === stack) {
      const view = stack.queue.getSnapshot().view, entry = view?.items.find(value => value.entryId === view.queue.currentEntryId);
      const subject = entry && queueEntrySubject(entry);
      if (subject) state.display = {subject};
    }
    const live = this.startingState;
    return {...(live?.display && (live.stack === stack || live.stack === undefined) ? {starting: this.startingView(live)} : {}), ...(this.startTimedOut ? {startTimedOut: this.startTimedOut} : {})};
  }
  private startingView(state: StartingState): WebPlaybackStarting {
    const service = state.stack?.service.getSnapshot();
    const waiting = service?.channel && service.linearStalled ? 'channel_source' as const : undefined;
    if (state.view?.display !== state.display || state.view?.waiting !== waiting)
      state.view = Object.freeze({key: state.key, display: state.display!, stop: () => this.stopStarting(state), ...(waiting ? {waiting} : {})});
    return state.view!;
  }
  /** The playing item's facts draw its dock and its Now playing (P2-74 E-web-14). */
  private readItemBounded(itemId: string, signal: AbortSignal, current: () => boolean): Promise<PlaybackItem> {
    return readPlaybackItemBounded(this.source.api, itemId, signal, current, {setTimer: (run, ms) => this.setTimer(run, ms), clearTimer: timer => this.clearTimer(timer)});
  }

  private clearSourceResources(stack: Stack): void {
    stack.metadataAbort?.abort(); stack.metadataAbort = undefined;
    stack.documentAbort?.abort(); stack.documentAbort = undefined;
    this.clearTimer(stack.documentTimer); stack.documentTimer = undefined;
    stack.offSubtitles?.(); stack.offSubtitles = undefined; stack.subtitles?.stop(); stack.subtitles = undefined;
    stack.sessionKey = undefined; stack.metadataRetry = undefined; stack.offerBinding = undefined; stack.subtitleKey = undefined; stack.documentKey = undefined; stack.options = null;
    if (!stack.retired) stack.offers.cancel();
    clearDrawnSubtitlesImpl(subtitleHostOf(this), stack);
    this.surface?.guard(false); this.surface?.clear();
    clearRecordedSidecar(stack, 'text');
  }
  private renderSubtitles(stack: Stack): void {
    renderSubtitlesImpl(subtitleHostOf(this), stack);
  }

  private schedulePolicy(): void {
    this.clearTimer(this.timer); this.timer = undefined;
    const post = this.snapshot?.queueView?.postPlay;
    const inGroup = !!this.snapshot?.fence && !!this.group?.matches(this.snapshot.fence);
    const earlyCountdown = !inGroup && this.snapshot?.subject?.kind === 'video' && this.snapshot.presentation?.playing && post?.available && post.autoplay && post.startDuringCredits;
    if (this.snapshot?.fence && (!inGroup && this.stack.queue.getSnapshot().completion || this.policyView?.undo || earlyCountdown) && !this.disposed) this.timer = this.setTimer(() => { this.timer = undefined; this.sync(); }, 250);
  }

  policyAction(expected: QueuePlaybackFence, action: WebPlaybackPolicyAction): Promise<void> {
    return policyActionImpl(queueHostOf(this), expected, action);
  }
  captureListening(fence: PlaybackFence, lifetime: WebListeningLifetime): WebListeningPort | undefined {
    return captureListeningImpl(listeningHostOf(this), fence, lifetime);
  }
  /** The playing video's preview set (W1-A2, as PB-2 on TV): read when a scrub first asks, never at start. */
  trickplay(fence: PlaybackFence, signal: AbortSignal): Promise<PlaybackTrickplaySelection | null> {
    const scope = this.snapshot.offers?.scope;
    if (!this.scrubPreviews || fence.kind !== 'queue' || !this.matches(fence) || this.stack.mode !== 'server' || !scope) return Promise.resolve(null);
    return readPlaybackTrickplay({api: this.source.api, authority: this.authority, contentScope: this.source.contentScope,
      isCurrent: () => !this.disposed && this.matches(fence), getSnapshot: this.getSnapshot}, fence, {libraryId: scope.libraryId, itemId: scope.itemId}, signal);
  }
  queueList(fence: PlaybackFence, selectionLife?: WebQueueListLifetime): WebQueueListPort | undefined {
    return queueListImpl(queueHostOf(this), fence, selectionLife);
  }

  private matches(fence: PlaybackFence): boolean { return this.current() && sameFence(this.snapshot?.fence, fence) && sameFence(this.dock.getSnapshot().local?.fence, fence); }
  private require(fence: PlaybackFence): Stack { if (!this.matches(fence)) problem('stale_playback_command'); return this.stack; }
  private offered(fence: PlaybackFence, command: WebPlaybackPresentation['controls'][number]): Stack { const stack = this.require(fence); if (!this.snapshot.presentation?.controls.includes(command)) problem('playback_command_unavailable'); return stack; }
  private awaitLocalPaused(stack: Stack, serial: number): Promise<LocalPlaybackFence> {
    return new Promise((resolve, reject) => {
      const controller = stack.local?.controller;if (!controller) {reject(new Error('playback_restore_failed'));return;}
      let off = () => {};
      const timer = this.setTimer(() => {off();reject(new Error('playback_source_timeout'));}, this.env.service?.operationTimeoutMs ?? 5000);
      const check = () => {
        const state = controller.getSnapshot();
        if (!this.current(stack) || serial !== this.operation || state.phase === 'error' || state.phase === 'disposed') {
          off();this.clearTimer(timer);reject(new Error('playback_restore_failed'));return;
        }
        if (!state.fence || state.observation?.state !== 'paused') return;
        off();this.clearTimer(timer);resolve(state.fence);
      };
      off = controller.subscribe(check);check();
    });
  }
  private awaitFence(stack: Stack, serial: number): Promise<PlaybackFence> {
    return new Promise((resolve, reject) => {
      let off = () => {};
      const timer = this.setTimer(() => { off(); reject(new Error('playback_source_timeout')); }, this.env.service?.operationTimeoutMs ?? 5000);
      const check = () => {
        let error: string | undefined;
        if (!this.current(stack) || serial !== this.operation) error = 'stale_playback_command';
        else if (stack.service.getSnapshot().phase === 'error' || this.snapshot.problem === 'playback_metadata_failed') error = 'playback_restore_failed';
        if (error) { off(); this.clearTimer(timer); reject(new Error(error)); return; }
        const fence = this.snapshot.fence, session = stack.service.getSnapshot().session;
        if (!fence || !session || fence.sessionId !== session.id || fence.generation !== session.generation || !this.matches(fence)) return;
        off(); this.clearTimer(timer); resolve(fence);
      };
      off = this.subscribe(check); check();
    });
  }

  async pause(fence: PlaybackFence): Promise<void> {
    const stack = this.offered(fence, 'pause');
    if (fence.kind === 'local') stack.local!.controller.pause(fence);
    else if (!await groupTransportImpl(togetherHostOf(this), 'pause', fence)) stack.service.pause();
  }
  async resume(fence: PlaybackFence): Promise<void> {
    const stack = this.offered(fence, 'play');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      if (fence.kind === 'local') stack.local!.controller.resume(fence);
      else if (!await groupTransportImpl(togetherHostOf(this), 'play', fence)) stack.service.resume();
    } finally {finishRetention();}
  }
  /** Explicit UI recovery only; the existing service retains position and intent. */
  canRetryFailure(fence: PlaybackFence): boolean {
    const playback = this.snapshot.playback;
    return this.current() && this.matches(fence) && fence.kind === 'queue' && this.stack.mode === 'server' && !this.group
      && playback.phase === 'error' && !playback.ended && !playbackFailurePermanent(playback.error);
  }
  async retryFailure(fence: PlaybackFence): Promise<void> {
    if (!this.canRetryFailure(fence)) problem('playback_command_unavailable');
    const stack = this.require(fence);
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      await stack.service.retry();
    } finally {finishRetention();}
  }
  async toggle(fence: PlaybackFence): Promise<void> {this.require(fence);if (this.snapshot.presentation?.playing) await this.pause(fence);else await this.resume(fence);}
  goLive(fence: PlaybackFence): void { goLiveImpl(liveHostOf(this), fence); }
  async seek(fence: PlaybackFence, seconds: number): Promise<void> {
    if (!validSeconds(seconds)) problem('invalid_playback_position');const stack = this.offered(fence, 'seek');
    if (fence.kind === 'local') stack.local!.controller.seek(fence, seconds);
    else if (!await groupTransportImpl(togetherHostOf(this), 'seek', fence, seconds)) stack.service.seek(Math.min(stack.service.getSnapshot().duration || Infinity, seconds));
  }
  async skip(fence: PlaybackFence, seconds: number): Promise<void> {
    if (!Number.isFinite(seconds)) problem('invalid_playback_position');const stack = this.require(fence);
    const position = fence.kind === 'local' ? stack.local!.controller.getSnapshot().observation!.positionMs / 1000 : stack.service.getSnapshot().pendingSeek?.positionSeconds ?? stack.service.getSnapshot().positionSeconds;
    await this.seek(fence, Math.max(0, position + seconds));
  }
  async replay(fence: PlaybackFence): Promise<void> {
    if (fence.kind === 'channel') problem('playback_command_unavailable');
    this.require(fence);this.sync();
    const stack = this.offered(fence, 'replay');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      if (fence.kind === 'queue' && this.snapshot.presentation?.policy?.attention === 'still-watching') {
        const state = stack.service.getSnapshot(), queue = stack.queue.getSnapshot(), session = stack.queue.currentSession();
        const view = stack.queue.getSnapshot().view, held = queue.completion, receipt = stack.confirmedEnd;
        if (this.group?.active() || !stack.expanded || state.phase !== 'ended' || state.ended || queue.phase !== 'ready' || queue.error
          || !held || held.sessionId !== fence.sessionId || held.intentId !== state.intentId
          || !receipt || receipt.sessionId !== fence.sessionId || receipt.generation !== fence.generation || receipt.intentId !== state.intentId
          || !view || view === receipt.view || session?.id !== fence.sessionId || session.presentation.generation !== fence.generation
          || session.kind !== 'vod' || session.role !== 'local' || !session.versionId || session.itemId !== fence.itemId
          || session.queue?.queueId !== view.queue.id || session.queue.entryId !== fence.entryId) problem('playback_command_unavailable');
        const choices = {...stack.service.playbackChoices(), versionId: session.versionId, ...(session.discTitle ? {discTitle: session.discTitle} : {})};
        await stack.queue.playItem(fence.itemId, 0, undefined, undefined, undefined, choices,
          {signal: AbortSignal.any([this.life.signal, stack.life.signal]), isCurrent: () => this.current(stack) && !this.group?.active(), propagateError: true});
      } else if (fence.kind === 'local') await stack.local!.controller.replay(fence);
      else await stack.service.play(fence.itemId, 0);
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  playBookChapter(fence: PlaybackFence, chapterId: string): Promise<void> {
    return playBookChapterImpl(listeningHostOf(this), fence, chapterId);
  }
  async previous(fence: PlaybackFence): Promise<void> {
    if (fence.kind === 'channel') problem('playback_command_unavailable');const stack = this.offered(fence, 'previous');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      if (fence.kind === 'local') await stack.local!.controller.previous(fence);
      else if (!await groupTransportImpl(togetherHostOf(this), 'previous', fence) && !await chapterMoveImpl(listeningHostOf(this), stack, fence, 'previous')) await stack.queue.previous();
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  async next(fence: PlaybackFence): Promise<void> {
    if (fence.kind === 'channel') problem('playback_command_unavailable');const stack = this.offered(fence, 'next');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      if (fence.kind === 'local') await stack.local!.controller.next(fence);
      else if (!await groupTransportImpl(togetherHostOf(this), 'next', fence) && !await chapterMoveImpl(listeningHostOf(this), stack, fence, 'next')) {
        const view = stack.queue.getSnapshot().view;if (!view) problem('playback_command_unavailable');await stack.queue.next(view.queue.id, view.queue.revision);
      }
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  /** An in-player episode pick (desktop-player f6): jump inside this run's queue and keep its choices. */
  async selectEpisode(fence: PlaybackFence, episodeId: string): Promise<void> {
    if (fence.kind !== 'queue') problem('playback_command_unavailable');
    if (!/^[A-Za-z0-9_.-]{1,128}$/.test(episodeId)) problem('invalid_playback_target');
    const stack = this.require(fence);
    if (!stack.queue.getSnapshot().view) problem('playback_command_unavailable');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      await stack.queue.selectEpisode(episodeId);
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  open(fence: PlaybackFence): void { const stack = this.require(fence); stack.expanded = true; this.dock.expand(fence); this.sync(); }
  collapse(fence: PlaybackFence): void { const stack = this.require(fence); stack.expanded = false; this.dock.collapse(fence); this.surface?.guard(false); this.surface?.clear(); this.sync(); }
  async stop(fence: PlaybackFence): Promise<void> { this.require(fence); this.rememberCurrent(); await this.onTransition(this.dock.close(fence)); }
  onTransition(transition: DockTransition): Promise<void> { return onTransitionImpl(queueHostOf(this), transition); }
  restoreBookmark(expected: PlaybackFence, playExplicitly = false): Promise<PlaybackFence> { return restoreBookmarkImpl(queueHostOf(this), expected, playExplicitly); }

  async setQuality(fence: PlaybackFence, choice: QualityChoice): Promise<void> {
    if (fence.kind !== 'queue') problem('playback_quality_unavailable');
    const stack = this.require(fence), options = stack.options, session = this.snapshot.session;
    const source = this.snapshot.offers?.sources.find(value => value.id === fence.sourceId);
    const enabled = source?.qualities.some(rung => rung.enabled && (choice === 'automatic' ? rung.kind === 'automatic' : choice === 'original' ? rung.kind === 'original' : rung.kind === 'fixed' && rung.id === choice));
    if (!enabled || !options || !session || !options.qualities.some(offer => offer.applicable !== false && qualityChoiceOf(offer) === choice)) problem('playback_quality_unavailable');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      await stack.service.changePlayback({quality: qualityRequest(choice)});
      if (!this.current(stack) || stack.service.getSnapshot().session?.id !== fence.sessionId) return;
      this.choices.set('quality', fence.playback, choice);
      const version = options.versions.find(v => v.id === session.versionId);
      stack.web.forgetAutomaticStep(); stack.web.setAutomatic(choice === 'automatic' ? {steps: offeredQualities({height: version?.video[0]?.height, bitrateKbps: version?.bitrateKbps}), originalKbps: version?.bitrateKbps} : null);
      this.sync();
    } finally {finishRetention();}
  }
  async selectAudio(fence: PlaybackFence, trackId: string): Promise<void> {
    if (fence.kind !== 'queue') problem('playback_audio_unavailable');
    const stack = this.require(fence), version = stack.options?.versions.find(v => v.id === this.snapshot.session?.versionId);
    const offered = this.snapshot.offers?.sources.find(source => source.id === fence.sourceId)?.streams.some(stream => stream.type === 'audio' && stream.enabled && stream.index === audioStreamIndex(trackId));
    if (!offered || !version?.audio.some(track => track.id === trackId)) problem('playback_audio_unavailable');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      await stack.service.changePlayback({audioTrackId: trackId});
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  selectSubtitle(fence: PlaybackFence, trackId: string | null): Promise<void> { return selectSubtitleImpl(subtitleHostOf(this), fence, trackId); }
  setSubtitleOffset(fence: PlaybackFence, offsetMs: number): Promise<void> { return setSubtitleOffsetImpl(subtitleHostOf(this), fence, offsetMs); }
  private playbackRun(): number { return this.snapshot?.playback.playbackRun ?? -1; }
  subtitleStyle(fence: PlaybackFence): Readonly<Pick<SubtitlePreferences, 'size' | 'background'>> | undefined { return subtitleStyleImpl(subtitleHostOf(this), fence); }
  setSubtitleStyle(fence: PlaybackFence, patch: Readonly<{size?: SubtitlePreferences['size']; background?: SubtitlePreferences['background']}>): void { setSubtitleStyleImpl(subtitleHostOf(this), fence, patch); }
  subtitleForced(fence: PlaybackFence): Readonly<{only: boolean; lines: SubtitleLines}> | undefined { return subtitleForcedImpl(subtitleHostOf(this), fence); }
  setSubtitleForcedOnly(fence: PlaybackFence, on: boolean): void { setSubtitleForcedOnlyImpl(subtitleHostOf(this), fence, on); }
  sleepTimer(fence: PlaybackFence): Readonly<{minutes: number; endsAt: number}> | Readonly<{end: WebSleepEnd}> | undefined { return sleepTimerImpl(listeningHostOf(this), fence); }
  setSleepTimer(fence: PlaybackFence, choice: number | WebSleepEnd): Promise<void> { return setSleepTimerImpl(listeningHostOf(this), fence, choice); }
  subtitleProvider(fence: PlaybackFence): Readonly<{enabled: boolean; reason?: string}> | undefined { return subtitleProviderImpl(subtitleHostOf(this), fence); }
  subtitleSearch(fence: PlaybackFence): WebSubtitleSearch | undefined { return subtitleSearchImpl(subtitleHostOf(this), fence); }
  searchSubtitles(fence: PlaybackFence, language: string): Promise<void> { return searchSubtitlesImpl(subtitleHostOf(this), fence, language); }
  downloadSubtitle(fence: PlaybackFence, candidateId: string): Promise<void> { return downloadSubtitleImpl(subtitleHostOf(this), fence, candidateId); }
  speedChoices(fence: PlaybackFence): readonly number[] { return speedChoicesImpl(listeningHostOf(this), fence); }
  setSpeed(fence: PlaybackFence, speed: number): void { setSpeedImpl(listeningHostOf(this), fence, speed); }
  stepSpeed(fence: PlaybackFence, direction: -1 | 1): void { stepSpeedImpl(listeningHostOf(this), fence, direction); }

  /** Browser transports belong only to this loaded, unheld HTML-media source.
   * A native factory or the separate music renderer cannot lend its idle video. */
  browserMediaReady(fence: PlaybackFence): boolean {
    if (!this.matches(fence) || fence.kind === 'local' || this.env.createPlayer || this.stack.mode !== 'server') return false;
    const slot = this.stack.web.slot, observed = slot.observe();
    return slot.loaded === fence.generation && !slot.settling && observed.generation === fence.generation
      && ['playing', 'paused', 'buffering'].includes(observed.state) && this.snapshot.dock.remote?.phase !== 'playing';
  }

  /** AirPlay requires server-prepared receiver streams, which are not offered yet. */
  browserAirPlayReady(_fence: PlaybackFence): boolean {
    return false;
  }

  /** Only VOD uses the element's title clock directly. Native and live clocks are not inferred. */
  bufferedRanges(fence: PlaybackFence): readonly Readonly<{start: number; end: number}>[] {
    if (fence.kind !== 'queue' || !this.browserMediaReady(fence) || !this.video || this.stack.subject?.kind !== 'video') return [];
    const ranges = this.video.buffered;
    return Array.from({length: ranges.length}, (_, index) => ({start: ranges.start(index), end: ranges.end(index)}));
  }

  /** The ordinary browser has no qualified frame primitive. A platform command still owns
   * its actual decoder/entry readiness and may refuse without publishing a guessed clock. */
  canStepFrame(fence: PlaybackFence): boolean {
    if (!this.matches(fence)) return false;
    if (this.group?.matches(fence)) return false;
    const presentation = this.snapshot.presentation, playback = this.stack.service.getSnapshot();
    return fence.kind === 'queue' && presentation?.kind === 'video' && presentation.expanded && !presentation.playing && !presentation.finished && !playback.pendingSeek && this.snapshot.dock.remote?.phase !== 'playing' && this.stack.web.slot.loaded === fence.generation && this.stack.web.slot.canStepFrame;
  }
  async stepFrame(fence: PlaybackFence, direction: -1 | 1): Promise<void> {
    const stack = this.require(fence);
    if ((direction !== -1 && direction !== 1) || !this.canStepFrame(fence)) problem('playback_frame_step_unavailable');
    await stack.web.slot.stepFrame(direction, fence.generation);
    this.require(fence);
  }
  audioEngineFacts(fence: PlaybackFence) { return this.require(fence).web.audioFacts(); }
  async setEqualizer(fence: PlaybackFence, settings: EqualizerSettings): Promise<void> {
    const stack = this.require(fence);
    if (stack.subject?.kind !== 'music') problem('equalizer_unavailable');
    if (this.equalizerPort) await this.equalizerPort.change(settings);
    else await stack.web.setEqualizer(settings);
    if (this.current(stack)) this.sync();
  }
  refreshSettings(): void { this.assertCurrent(); this.stack.queue.listening.refreshAudiobookPreferences(); this.surface?.applyPreferences(subtitlePreferencesImpl(subtitleHostOf(this), this.stack)); this.stack.service.adoptAudioEffectsPreference(this.source.settings.audioEffects()); this.sync(); }

  onAction = async (action: WebPlaybackAction): Promise<void> => {
    if (action.remote) problem('remote_playback_binding_unavailable');
    if (action.kind === 'restore-audio' || action.kind === 'restore-video') {
      this.assertCurrent();
      const state = this.dock.getSnapshot(), bookmark = action.kind === 'restore-audio' ? state.parkedAudio : state.resumeVideo;
      if (!sameFence(bookmark?.fence, action.fence)) problem('stale_playback_command');
      await this.restoreBookmark(action.fence, true); return;
    }
    switch (action.kind) {
      case 'play': await this.resume(action.fence); return;
      case 'pause': await this.pause(action.fence); return;
      case 'replay': await this.replay(action.fence); return;
      case 'previous': await this.previous(action.fence); return;
      case 'next': await this.next(action.fence); return;
      case 'book-chapter': if (!action.chapterId) problem('playback_command_unavailable'); await this.playBookChapter(action.fence, action.chapterId); return;
      case 'seek': if (action.seconds === undefined) problem('invalid_playback_position'); await this.seek(action.fence, action.seconds); return;
      case 'step-speed': if (action.direction === undefined) problem('playback_speed_unavailable'); this.stepSpeed(action.fence, action.direction); return;
      case 'step-frame': if (action.direction === undefined) problem('playback_frame_step_unavailable'); await this.stepFrame(action.fence, action.direction); return;
      case 'expand': this.open(action.fence); return;
      case 'collapse': this.collapse(action.fence); return;
      case 'stop': await this.stop(action.fence); return;
      case 'volume': {
        this.offered(action.fence, 'volume');
        const video = this.video, volume = action.volume;
        if (volume === undefined || !Number.isFinite(volume) || volume < 0 || volume > 1 || !video) problem('invalid_playback_volume');
        video.volume = volume; this.sync(); return;
      }
      case 'mute': {
        this.offered(action.fence, 'volume');
        const video = this.video;
        if (!video) problem('invalid_playback_volume');
        video.muted = !video.muted; this.sync(); return;
      }
      default: problem('playback_command_unavailable');
    }
  };

  dispose(): void {
    this.endStarting(); this.startTimedOut = undefined;
    if (this.disposed) return;
    this.replacing = true; ++this.operation;
    this.detachGroup?.();
    this.retire(this.stack); this.disposed = true;
    this.mediaSession?.clear();
    this.clearTimer(this.timer); this.timer = undefined;
    for (const off of this.lifecycle.splice(0)) off();
    this.surface?.dispose(); this.surface = undefined; this.video = undefined;
    this.life.abort(); this.audioCache.clear(); this.bookmarks.clear(); this.retiredFences.clear();
    for (const lease of [...this.localLeases]) this.releaseLocalLease(lease);
    this.dock.reset(queueScope({...this.authority, controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'}));
    this.snapshot = Object.freeze({nativeVideoSurfaceActive: false, authority: this.authority, sequence: ++this.sequence, playback: this.stack.service.getSnapshot(), dock: this.dock.getSnapshot(), presentations: [], queueView: null, offers: null, options: null, disposed: true});
    for (const start of [...this.pendingMediaStarts]) start.finish();
    this.publishRetention(); this.retentionListeners.clear();
    for (const listener of [...this.listeners]) listener(); this.listeners.clear();
  }
}

/* eslint-disable @typescript-eslint/no-explicit-any */
function subtitleHostOf(runtime: WebShellPlaybackRuntime): SubtitleRuntimeHost {
  const self = runtime as any;
  return {
    stack: () => self.stack,
    video: () => self.video,
    surface: () => self.surface,
    subtitleTimeline: () => self.subtitleTimeline,
    source: () => self.source,
    env: () => self.env,
    snapshot: () => self.snapshot,
    runtimeProblem: () => self.runtimeProblem,
    setRuntimeProblem: code => { self.runtimeProblem = code; },
    current: stack => self.current(stack),
    matches: fence => self.matches(fence),
    require: fence => self.require(fence),
    offers: (stack, playback) => self.offers(stack, playback),
    playbackRun: () => self.playbackRun(),
    retainMediaStart: (signal, isCurrent) => self.retainMediaStart(signal, isCurrent),
    setTimer: (fn, ms) => self.setTimer(fn, ms),
    clearTimer: timer => self.clearTimer(timer),
    sync: () => self.sync(),
    selectSubtitle: (fence, trackId) => self.selectSubtitle(fence, trackId),
    renderSubtitles: stack => self.renderSubtitles(stack),
  };
}

function listeningHostOf(runtime: WebShellPlaybackRuntime): ListeningRuntimeHost {
  const self = runtime as any;
  return {
    stack: () => self.stack,
    source: () => self.source,
    env: () => self.env,
    dock: () => self.dock,
    choices: () => self.choices,
    life: () => self.life,
    group: () => self.group,
    snapshot: () => self.snapshot,
    current: stack => self.current(stack),
    matches: fence => self.matches(fence),
    require: fence => self.require(fence),
    offered: (fence, command) => self.offered(fence, command),
    playbackRun: () => self.playbackRun(),
    subscribe: listener => self.subscribe(listener),
    queueList: (fence, selectionLife) => self.queueList(fence, selectionLife),
    seek: (fence, seconds) => self.seek(fence, seconds),
    retainMediaStart: (signal, isCurrent) => self.retainMediaStart(signal, isCurrent),
    setTimer: (fn, ms) => self.setTimer(fn, ms),
    clearTimer: timer => self.clearTimer(timer),
    sync: () => self.sync(),
  };
}

function liveHostOf(runtime: WebShellPlaybackRuntime): LiveRuntimeHost {
  const self = runtime as any;
  return {
    life: () => self.life,
    nextOperation: () => ++self.operation,
    operation: () => self.operation,
    assertCurrent: () => self.assertCurrent(),
    current: stack => self.current(stack),
    require: fence => self.require(fence),
    selectionCurrent: (context, signal, current) => self.selectionCurrent(context, signal, current),
    retainMediaStart: (signal, isCurrent) => self.retainMediaStart(signal, isCurrent),
    beginStarting: (display, retry, options) => self.beginStarting(display, retry, options),
    retryLifetime: () => self.retryLifetime(),
    replace: mode => self.replace(mode),
    abandon: (stack, serial) => self.abandon(stack, serial),
    reportStartProblem: (error, display, retry, serial, sentence) => self.reportStartProblem(error, display, retry, serial, sentence),
    sync: () => self.sync(),
  };
}

function togetherHostOf(runtime: WebShellPlaybackRuntime): TogetherRuntimeHost {
  const self = runtime as any;
  return {
    stack: () => self.stack,
    source: () => self.source,
    life: () => self.life,
    snapshot: () => self.snapshot,
    group: () => self.group,
    setGroup: binding => { self.group = binding; },
    setDetachGroup: detach => { self.detachGroup = detach; },
    groupServices: () => self.groupServices,
    groupRate: () => self.groupRate,
    setGroupRate: rate => { self.groupRate = rate; },
    nextOperation: () => ++self.operation,
    operation: () => self.operation,
    assertCurrent: () => self.assertCurrent(),
    current: stack => self.current(stack),
    matches: fence => self.matches(fence),
    require: fence => self.require(fence),
    retainMediaStart: (signal, isCurrent) => self.retainMediaStart(signal, isCurrent),
    replace: mode => self.replace(mode),
    sync: () => self.sync(),
  };
}

function queueHostOf(runtime: WebShellPlaybackRuntime): QueueRuntimeHost {
  const self = runtime as any;
  return {
    authority: () => self.authority,
    source: () => self.source,
    env: () => self.env,
    dock: () => self.dock,
    policy: () => self.policy,
    life: () => self.life,
    stack: () => self.stack,
    setStack: stack => { self.stack = stack; },
    snapshot: () => self.snapshot,
    group: () => self.group,
    bookmarks: () => self.bookmarks,
    retiredFences: () => self.retiredFences,
    versionChoices: () => self.versionChoices,
    catalogueEnqueuing: () => self.catalogueEnqueuing,
    setCatalogueEnqueuing: value => { self.catalogueEnqueuing = value; },
    policyView: () => self.policyView,
    policyAdvanceFence: () => self.policyAdvanceFence,
    setPolicyAdvanceFence: fence => { self.policyAdvanceFence = fence; },
    runtimeProblem: () => self.runtimeProblem,
    setRuntimeProblem: code => { self.runtimeProblem = code; },
    setReplacing: value => { self.replacing = value; },
    nextOperation: () => ++self.operation,
    operation: () => self.operation,
    assertCurrent: () => self.assertCurrent(),
    current: stack => self.current(stack),
    matches: fence => self.matches(fence),
    require: fence => self.require(fence),
    key: fence => self.key(fence),
    selectionCurrent: (context, signal, current) => self.selectionCurrent(context, signal, current),
    retainMediaStart: (signal, isCurrent) => self.retainMediaStart(signal, isCurrent),
    retainedQueueIds: stack => self.retainedQueueIds(stack),
    makeStack: mode => self.makeStack(mode),
    activate: stack => self.activate(stack),
    retire: stack => self.retire(stack),
    replace: mode => self.replace(mode),
    rememberCurrent: () => self.rememberCurrent(),
    bindLocalQueue: (stack, lease) => self.bindLocalQueue(stack, lease),
    awaitLocalPaused: (stack, serial) => self.awaitLocalPaused(stack, serial),
    awaitFence: (stack, serial) => self.awaitFence(stack, serial),
    beginStarting: (display, retry, options) => self.beginStarting(display, retry, options),
    retryLifetime: () => self.retryLifetime(),
    reportStartProblem: (error, display, retry, serial, sentence) => self.reportStartProblem(error, display, retry, serial, sentence),
    clearSourceResources: stack => self.clearSourceResources(stack),
    readItemBounded: (itemId, signal, current) => self.readItemBounded(itemId, signal, current),
    offers: (stack, playback) => self.offers(stack, playback),
    bindSubtitles: (stack, offers, key) => bindSubtitlesImpl(subtitleHostOf(runtime), stack, offers, key),
    groupTransport: (command, fence, seconds) => groupTransportImpl(togetherHostOf(runtime), command, fence, seconds),
    resume: fence => self.resume(fence),
    subscribe: listener => self.subscribe(listener),
    setTimer: (fn, ms) => self.setTimer(fn, ms),
    clearTimer: timer => self.clearTimer(timer),
    sync: () => self.sync(),
  };
}
/* eslint-enable @typescript-eslint/no-explicit-any */
