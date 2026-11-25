import {readScrubPreviewsEnabled} from '../../../../../packages/client-core/src/query/playback';
import {entryFromItem, readItem} from '../../../../../packages/client-core/src/items';
import {downloadTrickplaySet, readScrubPreviews, type TrickplaySet} from '../../../../../packages/client-core/src/trickplay';
import {AppState, NativeModules, type View} from 'react-native';
import type {RefObject} from 'react';
import {startFailureHttp, type NativePlaybackStartFailure} from './start-failure';
import {capacityRetryHttp, type NativePlaybackCapacityWait} from './capacity-retry';
import {PlaybackService, type LocalSession, type PlaybackSession} from '../../../../../packages/client-core/src/index';
import {signInHolds} from './paint-authority';
import {DetailService, type DetailProjection} from '../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {playbackPath} from '../../../../../packages/client-core/src/playback-v1/routes';
import {SessionsClient} from '../../../../../packages/client-core/src/playback-v1/sessions';
import {localV1Http} from '../../../../../packages/client-core/src/playback-v1/local-http';
import {call, type V1Request, type V1Response} from '../../../../../packages/client-core/src/playback-v1/http';
import {PlaybackOptionsClient} from '../../../../../packages/client-core/src/playback-v1/options';
import {V1QueuePlayer, queueVideoKind, type V1QueuedEntrySelection, type V1QueueEditIntent, type V1QueuePolicyIntent} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {channelSeconds, type ChannelReference} from '../../../../../packages/client-core/src/playback/linear-protocol';
import {legacyChannel} from '../../../../../packages/client-core/src/guide/legacy-source';
import type {HomeChannel} from '../../../../../packages/client-core/src/home';
import type {HomeSelectionPlaybackPort, HomeSelectionPlaybackRequest} from '../../../../../packages/client-core/src/home-selection/types';
import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model/index';
import {parseSession, type Session, type PlaybackOptions, type QueueHeader, type Selector} from '../../../../../packages/client-core/src/playback-v1/types';
import type {PlaybackChoices} from '../../../../../packages/client-core/src/playback-v1/choices';
import type {SegmentInput} from '../../../../../packages/client-core/src/playback-v1/queue';
import {qualityOptionRequest, type QualityOption} from '../../../../../packages/client-core/src/playback-v1/quality';
import {SubtitleService} from '../../../../../packages/client-core/src/subtitles';
import {sessionSubtitlePlan} from '../../../../../packages/client-core/src/playback-v1/offers';
import type {PlaybackSubject, CardSubject} from '../../../../../packages/client-core/src/presentation/kit-model';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {PlaybackDock, PlayerSessionChoices, parseQueueSnapshot, playbackFence, sameFence, sameScope, type QueuePlaybackFence, type DockPlayback, type DockTransition, type PlaybackFence, type QueueScope, type RemoteDock} from '../../../../../packages/player-core/src';
import {NativePlaybackCoordinator, type NativeBinding} from './NativePlaybackCoordinator';
import {sameNativePresentation} from './external-presentation';
import {NativePlayback} from './native-playback';
import {PlaybackScopeHttp} from './scope-http';
import {captureNativePreparingOwner, nativePreparingStarts} from './preparing-owner';
import type {NativeStartRegistration} from './preparing-start';
import {nativeFirstVideoFrame, type NativeStartPresentationOwner, type NativeFirstVideoFrame} from './start-presentation';
import {NativeOfflinePlayback, type NativeOfflineSource} from './offline-playback';
import type {OfflineEntry} from '../offline-downloads';
import {NativeSubtitleStyleChoices} from './subtitle-style-choices';
import {NativeVideoPlaybackPolicy} from './video-policy';
import {boundSubtitleControls, type NativePlaybackSubtitleControls} from './subtitle-controls';
import type {SubtitleFonts} from './engine-media';
import {atNaturalEnd, classifyPlaybackFailure, playbackFailureReason} from './playback-failure';
import {mediaRecovered, NativePlaybackRecovery} from './playback-recovery';
import {NativeAudioTrackSelection} from './audio-track-selection';
import {NativeOfflineTracks} from './offline-tracks';
import {nativeListeningFor} from './native-listening-kinds';
import {NATIVE_START_DEADLINE_MS, NATIVE_START_DRAWN_MS, NativeStartWatch, entryDisplay} from './start-watch';
import {NativeChannelRunner} from './runtime-channel';
import {NativeExternalPresentation} from './runtime-external';
import {reopenOfflineBookmark, restartOfflinePlayback, startOfflinePlayback, type NativeOfflineHost} from './runtime-offline';
import {createRuntimeListeningRead, readPlaybackSession, type NativeListeningReadHost} from './runtime-listening';
import {enqueueCatalogueSegments, enqueueSelection, playCatalogueSegmentsNow, playSelectionNow, startGroupNow, type NativeSelectionHost} from './runtime-selection';
import {applyPlaybackOptions, clearForcedImage, forcedImageAvailable, type ForcedImageRead, type NativeOptionsHost} from './runtime-options';
import {startItemPlayback, startVersionPlayback, type NativeStartHost} from './runtime-start';
import {buildVideoPolicyFrame, editQueueAction, executeVideoPolicyCommand, retryFailedQueueAction, selectQueuedEntryAction, setQueuePolicyAction, videoPolicyCurrentFor, type NativeQueueActionsHost} from './runtime-queue-actions';
import type {NativeExternalPresentationLifetime, NativePlaybackReadLifetime, NativeListeningReadPort, NativeOfflinePlaybackLifetime, NativePlaybackStart, NativeSleepChoice, NativeSleepEnd, NativePlaybackChannelStart, NativePlaybackVersionSelection, NativePlaybackRuntimeOptions, NativePlaybackRuntimeSnapshot, PlaybackAuthority} from './types';
import {viewerQueriesFor} from '../../shell/freshness';

export {NATIVE_START_DRAWN_MS, NATIVE_START_DEADLINE_MS};

const authorityKey = (value: PlaybackAuthority) => JSON.stringify([value.serverId, value.accountId, value.profileId, value.epoch, value.privacyFence ?? value.viewerFence]);
const audioKind = (kind: string | undefined) => kind === 'song' || kind === 'track' || kind === 'audiobook_file' || kind === 'audiobook';
const kindOf = (kind: string | undefined): 'video' | 'music' | 'audiobook' => kind === 'song' || kind === 'track' ? 'music' : kind === 'audiobook_file' || kind === 'audiobook' ? 'audiobook' : 'video';
const subjectOf = (entry: ContentEntry, detail?: DetailProjection): PlaybackSubject => {
  const item = detail?.item, episode = item?.episode;
  const card: CardSubject = {
    id: entry.id, kind: entry.kind, title: entry.title,
    artist: item?.song?.artist ?? entry.artist?.name, album: item?.song?.albumTitle ?? entry.album?.name,
    author: entry.author, durationSeconds: entry.duration,
    ...(episode ? {show: {id: episode.showId, title: episode.showTitle}, episode: episode.numbering === 'date' ? {mode: 'date' as const, day: episode.airDate} : episode.numbering === 'absolute' ? {mode: 'absolute' as const, number: episode.number} : episode.numbering === 'special' ? {mode: 'special' as const} : {mode: 'seasonal' as const, season: episode.seasonNumber ?? undefined, episode: episode.number}} : entry.episodeNumber === undefined ? {} : {episode: {mode: 'seasonal' as const, season: entry.seasonNumber, episode: entry.episodeNumber}}),
  };
  const kind = kindOf(entry.kind);
  return {kind, item: card};
};
type Bookmark = Readonly<{playback: DockPlayback; entry?: ContentEntry; subject?: PlaybackSubject; queueId?: string; entryId?: string; channel?: ChannelReference; offline?: NativeOfflineSource}>;
type SourceDetail = {
  service: DetailService; sourceEpoch: number; sourceKey: string;
  intentId: number; sessionId: string; generation: number; itemId: string; libraryId: string;
};

/** Actual session, queue and native-engine wiring. Construct one per selected authority.
 * The production factory in native.tsx supplies the existing native modules and one view. */
export class NativePlaybackRuntime {
  readonly playback: PlaybackService;
  readonly path: NativePlayback;
  readonly queue: V1QueuePlayer;
  readonly coordinator: NativePlaybackCoordinator;
  readonly dock: PlaybackDock;
  readonly choices = new PlayerSessionChoices();
  private readonly subtitleStyles = new NativeSubtitleStyleChoices();
  private readonly videoPolicy: NativeVideoPlaybackPolicy;
  private readonly startWatch: NativeStartWatch;
  private readonly channels: NativeChannelRunner;
  private readonly external: NativeExternalPresentation;
  private readonly o: NativePlaybackRuntimeOptions;
  private readonly bound: PlaybackAuthority;
  private readonly sessionFamilyId: string;
  private readonly scope: QueueScope;
  private readonly http: PlaybackScopeHttp;
  private readonly preparingStarts: NativeStartRegistration;
  private readonly optionsClient: PlaybackOptionsClient;
  private readonly offline?: NativeOfflinePlayback;
  private offlineSource?: NativeOfflineSource;
  private localTracks?: NativeOfflineTracks;
  private snapshot: NativePlaybackRuntimeSnapshot;
  private listeners = new Set<() => void>();
  private offs: (() => void)[] = [];
  private connected = false;
  private retired = false;
  private startEpoch = 0;
  private startContext?: () => boolean;
  private startAdmission = false;
  /** The sign-in the current start was pressed under. A routine refresh keeps it; the epoch, not
   *  the token generation, keeps one press to one start. */
  private startSession?: LocalSession;
  private readonly startLife = new AbortController();
  private startFailure?: NativePlaybackStartFailure;
  private capacityWait?: NativePlaybackCapacityWait;
  private versionMenuEpoch = 0;
  private startController?: AbortController;
  private offlineLifetimeOff?: () => void;
  private selectionControllers = new Set<AbortController>();
  private enqueueTail: Promise<unknown> = Promise.resolve();
  private sourceEpoch = 0;
  private sourceController?: AbortController;
  private sourceKey = '';
  private subtitleService?: SubtitleService;
  private subtitleOff?: () => void;
  private fonts?: SubtitleFonts | 'pending';
  private fontsKey?: string;
  private forcedImage?: ForcedImageRead;
  private detail?: SourceDetail;
  private detailOff?: () => void;
  private entry?: ContentEntry;
  private subject?: PlaybackSubject;
  private options?: PlaybackOptions;
  private bookmarks = new Map<string, Bookmark>();
  private updating = false;
  private lastPlayState: 'paused' | 'playing' = 'paused';
  private routeWasReady = true;
  private factDepth = 0;
  private factPublication = false;
  private preparing = false;
  private transitionEpoch?: number;
  private expanded = false;
  private expandAtStart?: number;
  private displaySubtitles = true;
  private serverKinds: readonly string[] = [];
  private transitions = false;
  private effectiveRate = 0;
  /** The speed last asked of the engine: what Playback options shows while the engine reports none. */
  private chosenRate = 1;
  private supportedRates: readonly number[] = [];
  private surfaceOwner?: object;
  private readonly recovery: NativePlaybackRecovery;
  private readonly audioTrackSelection = new NativeAudioTrackSelection();
  private audioTrackInstance?: string;
  private cleanupSessions = new Set<string>();
  private initialPresentation?: NativeStartPresentationOwner;
  private initialPresentationTarget?: {owner: NativeStartPresentationOwner; epoch: number; itemId: string; session?: Session};
  private firstVideoFrame?: NativeFirstVideoFrame;
  /** N1-4: the engine could not take up a streamed subtitle (ENGINE-CONTRACT §7 `subtitleStatus`).
   * Kept with the exact subtitle plan object it was about: a new session, version or track is a new
   * plan, so a stale refusal can never mark a later track (no fence is compared). */
  private streamSubtitleFailure?: object;
  /** N1-5: a first frame drawn while the opening seek is still pending is the media's 0:00, not
   * where it opens. It waits here, and lifts the poster only once that seek has settled. */
  private heldFirstVideoFrame?: NativeFirstVideoFrame;
  private failure?: Readonly<{intentId: number; run: number; reason: import('./playback-failure').NativePlaybackFailureReason}>;
  private scrubPreviews?: Promise<boolean>;

  constructor(options: NativePlaybackRuntimeOptions) {
    this.o = options;
    this.recovery = new NativePlaybackRecovery(options.timers);
    this.bound = Object.freeze({...options.authority()});
    const session = options.session(), viewer = session?.viewer;
    if (!session || !viewer || !['local', 'hosted'].includes(viewer.authority) || viewer.serverId !== this.bound.serverId || viewer.accountId !== this.bound.accountId || viewer.profileId !== this.bound.profileId) throw new Error('Invalid playback authority.');
    this.sessionFamilyId = session.sessionFamilyId;
    // These are the actual controller identifiers V1QueuePlayer publishes (its constructor).
    this.scope = Object.freeze({serverId: viewer.serverId, authority: viewer.authority as 'local' | 'hosted', accountId: viewer.accountId, profileId: viewer.profileId, controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'});
    this.preparingStarts = nativePreparingStarts(options.api).attach(() => captureNativePreparingOwner(options.api, options.session),
      id => !!this.path?.session(id) || this.path?.controller.getSnapshot().session?.id === id || this.playback?.getSnapshot().session?.id === id);
    this.http = new PlaybackScopeHttp(localV1Http(options.api), () => this.currentAuthority(), (request, response) => this.retiredStart(request, response));
    const originalHttp = {send: (request: V1Request) => {this.preparingStarts.observe(request); return this.http.send(request);}};
    // onChange lets startup and seek deadlines pause while the app is hidden (V14).
    const visibility = {hidden: () => AppState.currentState !== 'active', onVisible(listener: () => void) {
      const off = AppState.addEventListener('change', state => {if (state === 'active') listener();}); return () => off.remove();
    }, onChange(listener: () => void) {
      const off = AppState.addEventListener('change', () => listener()); return () => off.remove();
    }};
    const captureCurrent = (request: V1Request) => {
      const epoch = this.startEpoch, context = this.startContext, guard = this.http.capture(request.signal);
      if (!context || !request.signal || !this.startAdmission) return;
      return () => this.currentAuthority() && this.startOperationCurrent(epoch) && !request.signal!.aborted && context() && guard?.() !== false;
    };
    const capacity = capacityRetryHttp({http: originalHttp, life: this.startLife.signal, captureCurrent,
      isCurrent: () => this.currentAuthority(), timers: options.timers, foreground: () => !visibility.hidden(), onVisible: visibility.onVisible,
      publish: wait => {this.capacityWait = wait; if (this.snapshot) this.changed();}});
    const startHttp = options.startFailurePresentation ? startFailureHttp({http: capacity, api: options.api, events: options.events,
      life: this.startLife.signal, visibility, captureCurrent, isCurrent: () => this.currentAuthority(), isInitial: () => this.startAdmission,
      subscribeCurrent: listener => {const offs = [options.subscribeAuthority(listener), options.subscribeStartContext?.(listener)]; return () => offs.forEach(off => off?.());},
      publish: failure => {this.startFailure = failure; if (this.snapshot) this.changed();},
      cancelStart: () => {this.beginOperation(); this.changed();},
    }) : originalHttp;
    this.optionsClient = new PlaybackOptionsClient(this.http);
    this.path = new NativePlayback({http: startHttp, key: options.requestId, timers: options.timers, preparingStarts: this.preparingStarts,
      offeredStart: session => {
        const original = this.initialPresentationTarget;
        if (original && original.owner === this.initialPresentation && this.startAdmission && this.startOperationCurrent(original.epoch)
          && session.kind === 'vod' && session.role === 'local' && session.itemId === original.itemId && session.queue) original.session = session;
      },
      captureStartCurrent: () => {
        const epoch = this.startEpoch, principal = this.o.session(), initial = this.startAdmission, original = this.startContext;
        // The queue's own later start has its current core context, not the
        // route that originally opened playback. Initial user Play retains it.
        const actor = () => this.currentAuthority() && epoch === this.startEpoch && signInHolds(principal, this.o.session());
        return () => actor() && (!initial || this.startContext === original && original?.() !== false) && actor()
          && (!initial || this.startContext === original);
      }});
    if (options.offlineDevice) this.offline = new NativeOfflinePlayback(options.session, options.offlineDevice);
    this.playback = new PlaybackService(this.offline?.wrap(this.path.transport) ?? this.path.transport, options.requestId, {queueRequired: true, deferRecovery: true, pageVisibility: visibility});
    this.lastPlayState = this.playback.getSnapshot().intent;
    this.queue = new V1QueuePlayer({http: startHttp, api: this.path.transport, playback: this.playback, viewer: this.scope, key: options.requestId, events: options.events,
      transitions: () => this.transitions, native: options.nativeListening, nativeFor: kind => nativeListeningFor(this.serverKinds)(kind),
      retireQueuedSelection: (session, isUnowned) => this.stopOwnedSession(session.id, undefined, () => isUnowned()
        && this.playback.getSnapshot().session?.id !== session.id && this.path.controller.getSnapshot().session?.id !== session.id),
      audiobookPreferences: () => this.o.settings().audiobook ?? {skipBackMs: 15000, skipForwardMs: 30000, resumeRewindMs: 0},
      postPlay: () => this.o.settings().postPlay,
      retainQueueIds: () => this.retainedQueueIds()});
    // The shared video policy owns the held end card. Listening keeps its owner.
    this.queue.setCompletionPolicy(({kind}) => audioKind(kind) ? 'advance' : 'hold');
    this.dock = new PlaybackDock(this.scope, this.offline?.owner());
    this.coordinator = new NativePlaybackCoordinator(this.playback, options.transport, options.requestId,
      state => {
        if (!state.session) return '';
        if (state.session.mode === 'local') {
          const url = this.offline?.mediaUrl(state.session);
          if (url) {const parts = this.offline?.mediaParts(state.session); return parts?.length ? {url, localParts: parts} : url;}
          if (state.phase !== 'error') this.playback.fail(state.intentId, serviceText('downloads.offline.notPlayableHere')); return '';
        }
        try {return options.api.mediaUrl(state.session.streamUrl);}
        catch {this.playback.fail(state.intentId, serviceText('queuePlayer.startFailed')); return '';}
      }, fact => this.acceptFact(fact), options.engines);
    this.videoPolicy = new NativeVideoPlaybackPolicy({
      read: () => buildVideoPolicyFrame(this.queueActionsHost()),
      current: fence => this.videoPolicyCurrent(fence),
      execute: command => executeVideoPolicyCommand(this.queueActionsHost(), command, fence => this.videoPolicyCurrent(fence)),
      changed: () => this.publish(), failed: (fence, _error) => {if (this.videoPolicyCurrent(fence)) this.playback.fail(this.playback.getSnapshot().intentId, serviceText('queuePlayer.startFailed'));},
      now: options.timers?.now ?? (() => Math.floor(performance.now())),
      setTimer: options.timers?.setTimer ?? ((run, ms) => setTimeout(run, ms)),
      clearTimer: options.timers?.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>)),
    });
    this.startWatch = new NativeStartWatch({
      timers: options.timers,
      isRetired: () => this.retired,
      hasSnapshot: () => !!this.snapshot,
      publish: () => this.publish(),
      beginOperation: () => this.beginOperation(),
      fence: () => this.fence(),
      currentSession: () => this.currentSession(),
      hasOptions: () => !!this.options,
      localDock: () => this.dock.getSnapshot().local,
      fallbackDisplay: () => this.entry ? entryDisplay(this.entry) : this.subject?.kind === 'channel' && this.subject.channelName ? {title: this.subject.channelName} : undefined,
      diagnosticKind: () => this.entry?.kind ?? this.subject?.kind ?? 'unknown',
      isChannelStalled: () => {const playback = this.playback.getSnapshot(); return !!playback.channel && !!playback.linearStalled;},
      stop: fence => this.stop(fence),
    });
    this.channels = new NativeChannelRunner({
      playback: this.playback, events: options.events, requestId: options.requestId, timers: options.timers,
      sendHttp: request => this.http.send(request), request: this.request, session: options.session,
      capabilitiesReady: options.capabilitiesReady, connect: () => this.connect(), cancelQueue: () => this.queue.cancel(),
      currentAuthority: () => this.currentAuthority(), startEpoch: () => this.startEpoch,
      startOperationCurrent: epoch => this.startOperationCurrent(epoch), beginOperation: retain => this.beginOperation(retain),
      assertSelection: (context, signal, current) => this.assertSelection(context, signal, current),
      selectionCurrent: (context, signal, current) => this.selectionCurrent(context, signal, current),
      selectionScope: (signal, current) => this.selectionScope(signal, current),
      rememberCurrent: () => this.rememberCurrent(), setExpanded: value => {this.expanded = value;},
      clearCatalogState: () => {this.entry = undefined; this.subject = undefined; this.options = undefined;},
      snapshotFence: () => this.snapshot.fence, fence: () => this.fence(), changed: () => this.changed(),
    });
    this.external = new NativeExternalPresentation({
      playback: this.playback, path: this.path, queue: this.queue, timers: options.timers,
      pictureInPictureControls: options.pictureInPictureControls,
      autoPictureInPicture: () => this.o.settings().autoPictureInPicture !== false,
      isRetired: () => this.retired, active: () => this.active(), matches: fence => this.matches(fence),
      startEpoch: () => this.startEpoch, lease: () => this.coordinator.getSnapshot(),
      currentSession: () => this.currentSession(), subtitles: () => this.subtitleService?.getSnapshot(),
      snapshot: () => this.snapshot, subscribe: this.subscribe, publish: () => this.publish(),
    });
    this.snapshot = this.makeSnapshot(0);
  }

  getSnapshot = () => this.snapshot;
  /** File format facts only for this exact admitted local session. */
  localContainer(session: PlaybackSession): string | undefined {
    return this.offline?.mediaUrl(session) ? this.offlineSource?.media.extras?.bundle?.media.container : undefined;
  }
  localSubtitleRendering(): string | undefined {return this.playback.getSnapshot().session?.mode === 'local' ? this.localTracks?.requiredRendering() : undefined;}
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};

  private authorityMatches(): boolean {
    const session = this.o.session(), viewer = session?.viewer;
    return session?.sessionFamilyId === this.sessionFamilyId && authorityKey(this.o.authority()) === authorityKey(this.bound) && viewer?.serverId === this.scope?.serverId && viewer.accountId === this.scope.accountId && viewer.profileId === this.scope.profileId && viewer.authority === this.scope.authority;
  }
  private retiredStart(request: V1Request, response: V1Response): void {
    this.preparingStarts.retired(request, response);
    if (request.method !== 'POST' || ![200, 201].includes(response.status) || !this.authorityMatches()) return;
    // A start's answer can arrive after its caller retired. On the rebuilt server only `POST /v1/playback/sessions`
    // starts one: a queue's create and advance answer the queue alone.
    if (request.path !== playbackPath('POST /v1/playback/sessions')) return;
    let session: Session;
    try {session = parseSession(response.body);} catch {return;}
    const body = request.body as {itemId?: unknown; queue?: {queueId?: unknown; entryId?: unknown}} | undefined;
    if (!body || !body.queue && typeof body.itemId !== 'string') return;
    if (body.queue ? session.queue?.queueId !== body.queue.queueId || session.queue?.entryId !== body.queue.entryId : session.itemId !== body.itemId) return;
    if (this.playback.getSnapshot().session?.id === session.id) return;
    this.stopOwnedSession(session.id);
  }
  private async stopOwnedSession(id: string, positionMs?: number, isUnowned: () => boolean = () => true): Promise<boolean> {
    const current = () => {try {return this.authorityMatches() && isUnowned();} catch {return false;}};
    if (!current() || this.cleanupSessions.has(id)) return false;
    this.cleanupSessions.add(id);
    while (this.cleanupSessions.size > 32) this.cleanupSessions.delete(this.cleanupSessions.values().next().value!);
    const cleanup = new PlaybackScopeHttp(localV1Http(this.o.api), current);
    const off = this.o.subscribeAuthority(() => {if (!current()) cleanup.dispose();});
    try {
      await new SessionsClient(cleanup, this.o.timers).stop(id, positionMs);
      return current();
    } catch {return false;}
    finally {off(); cleanup.dispose();}
  }
  private currentAuthority(): boolean {return !this.retired && this.authorityMatches();}
  private active(): boolean {if (!this.currentAuthority()) {this.dispose(); return false;} return true;}
  private matches(fence: PlaybackFence): boolean {
    if (!this.active() || !this.snapshot.fence || !sameFence(this.snapshot.fence, fence)) return false;
    const live = fence.kind === 'local' ? this.fence() : fence;
    return !!live && sameFence(live, fence);
  }
  private request = async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) throw new Error('Invalid playback request method.');
    return (await call(this.http, {path, method: method as V1Request['method'], body, signal})).body as T;
  };

  connect(): void {
    if (this.connected || !this.active()) return;
    this.connected = true;
    this.offs.push(this.path.bind(this.playback, () => this.queue));
    this.offs.push(this.playback.subscribe(() => this.changed()));
    if (this.o.offlineDevice) this.offs.push(this.o.offlineDevice.subscribe(() => {if (this.active()) this.changed();}));
    this.offs.push(this.queue.subscribe(() => this.changed()));
    this.offs.push(this.path.subscribeSessions(() => this.changed()));
    this.offs.push(this.queue.connect());
    this.offs.push(this.coordinator.subscribe(() => {this.applyOptions(); this.publish();}));
    this.offs.push(this.o.subscribeAuthority(() => {if (!this.active()) return; void this.refreshAuthority();}));
    // X-02: the profile's Sound settings are what the listening engine renders with, so the
    // same adopt path the web player uses runs here on start and on every settings change.
    const adoptAudioEffects = () => {
      const effects = this.o.settings().audioEffects;
      if (!effects) return;
      this.playback.adoptAudioEffectsPreference(effects);
      // The engine's own rate matcher, when this build has one. A rejected mode stays
      // unapplied: the signal path reports the observed output rate, never the setting.
      const bridge = (NativeModules as unknown as {PorticoPlaybackModule?: {setAudioSampleRateMatching?: (mode: string) => Promise<unknown>}}).PorticoPlaybackModule?.setAudioSampleRateMatching;
      if (effects.sampleRateMatching && typeof bridge === 'function')
        void Promise.resolve(bridge(effects.sampleRateMatching)).catch(error => {if (__DEV__) console.warn('[audio-sample-rate]', error);});
    };
    adoptAudioEffects();
    this.offs.push(this.o.subscribeSettings(() => {if (this.active()) {adoptAudioEffects(); this.queue.listening.refreshAudiobookPreferences(); this.applyOptions(); this.coordinator.reconsiderEngine(); this.publish();}}));
    this.offs.push(this.choices.subscribe(() => {
      // Source binding resets these choices while changed() is still selecting
      // the next source. That path applies once after the new source is ready.
      if (this.updating || this.transitionEpoch !== undefined || !this.active()) return;
      this.applyOptions(); this.publish();
    }));
    this.offs.push(this.o.api.subscribeRoute(() => this.routeChanged()));
    this.offs.push(this.o.events.on('session.updated', event => {if (this.active() && event.resource?.kind === 'session') this.path.transport.sessionUpdated(event.resource.id, event.revision, event.data);}));
    if (this.o.events.onResyncRead) {
      // The queue registers its own bounded recovery (including native terminal
      // authority). Keep this runtime's detail read in the same dispatcher.
      const recovery = this.o.events.onResyncRead(async context => {
        if (this.active() && !context.signal.aborted) await this.detail?.service.refresh();
      }, {priority: 1});
      this.offs.push(() => recovery.dispose());
    } else this.offs.push(this.o.events.onResync(() => {if (this.active()) {void this.queue.refresh().catch(() => {}); void this.refreshAuthority();}}));
    this.routeChanged();
    this.coordinator.start();
    void this.playback.startRecovery();
    this.changed();
  }
  private routeChanged(): void {
    if (!this.active()) return;
    const route = this.o.api.getRouteSnapshot();
    const ready = !route || route.phase === 'ready', returned = ready && !this.routeWasReady;
    this.routeWasReady = ready;
    if (route && route.phase !== 'ready') this.external?.resetForRouteUnready();
    if (route && route.phase !== 'ready' && this.playback.getSnapshot().session?.mode !== 'local') this.recoverConnection();
    if (returned && this.playback.getSnapshot().session?.mode !== 'local') {
      if (this.playback.getSnapshot().intent === 'paused') this.recovery.clear();
      this.playback.recovering(this.playback.getSnapshot().intentId, false);
    }
    this.coordinator.setConnection(!route || route.phase === 'ready');
    if (route && route.phase !== 'ready') this.publish();
  }
  refreshAuthority = async (): Promise<void> => {
    if (!this.active()) return;
    await this.playback.refreshPlaybackAuthority();
    if (this.active()) await this.o.nativeListening?.refresh();
    if (this.active()) await this.detail?.service.refresh();
  };
  flush = () => this.path.controller.flush();

  private beginOperation(retainScope?: AbortSignal): number {
    const epoch = ++this.startEpoch;
    this.initialPresentationTarget = undefined;
    const previousPresentation = this.initialPresentation; this.initialPresentation = undefined;
    // The epoch invalidates paint synchronously. Deliver its passive abort/off
    // after this command's synchronous setup, so a subscriber cannot reenter it.
    if (previousPresentation) queueMicrotask(previousPresentation.retire);
    this.startContext = this.o.captureStartContext?.();
    this.startSession = this.o.session();
    this.startFailure = undefined; this.capacityWait = undefined; this.startAdmission = false;
    this.external.resetForOperation();
    this.offlineLifetimeOff?.(); this.offlineLifetimeOff = undefined;
    this.startController?.abort();
    for (const controller of [...this.selectionControllers]) {
      if (controller.signal === retainScope) continue;
      this.selectionControllers.delete(controller);
      controller.abort();
    }
    this.channels.clear();
    this.transitionEpoch = undefined; this.preparing = false;
    this.expandAtStart = undefined;
    return epoch;
  }

  private startOperationCurrent(epoch: number): boolean {
    return epoch === this.startEpoch && signInHolds(this.startSession, this.o.session());
  }

  private selectionHost(): NativeSelectionHost {
    return {
      playback: this.playback, queue: this.queue, requestId: this.o.requestId,
      sendHttp: request => this.http.send(request), request: this.request,
      capabilitiesReady: this.o.capabilitiesReady, connect: () => this.connect(),
      currentAuthority: () => this.currentAuthority(), startEpoch: () => this.startEpoch,
      sourceEpoch: () => this.sourceEpoch, startOperationCurrent: epoch => this.startOperationCurrent(epoch),
      beginOperation: () => this.beginOperation(),
      assertSelection: (context, signal, current) => this.assertSelection(context, signal, current),
      selectionCurrent: (context, signal, current) => this.selectionCurrent(context, signal, current),
      selectionScope: (signal, current) => this.selectionScope(signal, current),
      setServerCapabilities: (kinds, transitions) => {this.serverKinds = kinds; this.transitions = transitions;},
      rememberCurrent: () => this.rememberCurrent(),
      prepareStartState: (entry, epoch) => {
        this.entry = entry; this.subject = entry ? subjectOf(entry) : undefined; this.options = undefined;
        this.expanded = entry ? !audioKind(entry.kind) : true;
        this.expandAtStart = epoch; this.preparing = true; this.startAdmission = true;
      },
      finishStartState: epoch => {if (epoch === this.startEpoch) {this.preparing = false; this.startAdmission = false; this.changed();}},
      retainedQueueIds: () => this.retainedQueueIds(), changed: () => this.changed(),
    };
  }

  private offlineHost(): NativeOfflineHost {
    return {
      playback: this.playback, offline: this.offline, offlineDevice: this.o.offlineDevice,
      currentAuthority: () => this.currentAuthority(), startEpoch: () => this.startEpoch,
      beginOperation: () => this.beginOperation(),
      setStartController: controller => {this.startController = controller;},
      setOfflineLifetimeOff: off => {this.offlineLifetimeOff = off;},
      clearOfflineLifetimeOffIf: off => {if (this.offlineLifetimeOff === off) this.offlineLifetimeOff = undefined;},
      selectionScope: (signal, current) => this.selectionScope(signal, current),
      rememberCurrent: () => this.rememberCurrent(), cancelQueue: () => this.queue.cancel(),
      getOfflineSource: () => this.offlineSource,
      bindOffline: (source, subject) => {this.offlineSource = source; this.entry = undefined; this.options = undefined; this.subject = subject;},
      setExpanded: value => {this.expanded = value;}, setPreparing: value => {this.preparing = value;},
      changed: () => this.changed(), fence: () => this.fence(), retryLifetime: () => this.retryLifetime(),
    };
  }

  private listeningReadHost(): NativeListeningReadHost {
    return {
      api: this.o.api, http: this.http, timers: this.o.timers, bound: this.bound,
      playback: this.playback, queue: this.queue, authority: this.o.authority,
      session: this.o.session, settings: this.o.settings, subscribe: this.subscribe,
      subscribeAuthority: this.o.subscribeAuthority, subscribeSettings: this.o.subscribeSettings,
      currentAuthority: () => this.currentAuthority(), startEpoch: () => this.startEpoch,
      matches: fence => this.matches(fence), fence: () => this.fence(),
      currentSession: () => this.currentSession(), offlineSource: () => this.offlineSource,
      chosenRate: () => this.chosenRate, supportedRates: () => this.supportedRates,
      selectionScope: (signal, current) => this.selectionScope(signal, current), request: this.request,
    };
  }

  private optionsHost(): NativeOptionsHost {
    return {
      retired: this.retired, playback: this.playback, coordinator: this.coordinator, path: this.path,
      choices: this.choices, subtitleStyles: this.subtitleStyles, audioTrackSelection: this.audioTrackSelection,
      localTracks: this.localTracks, options: this.options, subtitles: this.subtitleService?.getSnapshot(),
      api: this.o.api, settings: this.o.settings, engineFeatures: this.o.engineFeatures,
      hasMpvTransport: () => !!this.o.engines.transport('mpv'),
      androidDisplayMatching: this.o.androidDisplayMatching, nativeDynamicRangeMatching: this.o.nativeDynamicRangeMatching,
      nativeLocalSubtitleTiming: this.o.nativeLocalSubtitleTiming,
      expanded: this.expanded, displaySubtitles: this.displaySubtitles,
      getSourceEpoch: () => this.sourceEpoch, sourceSignal: this.sourceController?.signal,
      audioTrackInstance: this.audioTrackInstance,
      setAudioTrackInstance: instance => {this.audioTrackInstance = instance;},
      setChosenRate: rate => {this.chosenRate = rate;},
      getFonts: () => this.fonts, getFontsKey: () => this.fontsKey,
      setFonts: (key, fonts) => {this.fontsKey = key; this.fonts = fonts;},
      getForcedImage: () => this.forcedImage, setForcedImage: read => {this.forcedImage = read;},
      fence: () => this.fence(), local: fence => this.local(fence), currentSession: () => this.currentSession(),
      active: () => this.active(), matches: fence => this.matches(fence), request: this.request,
      reapply: () => this.applyOptions(), publish: () => this.publish(),
    };
  }

  private startHost(): NativeStartHost {
    return {
      api: this.o.api, bound: this.bound, playback: this.playback, queue: this.queue, dock: this.dock, optionsClient: this.optionsClient,
      session: this.o.session, capabilitiesReady: this.o.capabilitiesReady, subscribe: this.subscribe,
      subscribeAuthority: this.o.subscribeAuthority, subscribeStartContext: this.o.subscribeStartContext,
      connect: () => this.connect(), active: () => this.active(), currentAuthority: () => this.currentAuthority(),
      isRetired: () => this.retired, hasSnapshot: () => !!this.snapshot, startEpoch: () => this.startEpoch,
      startContext: () => this.startContext, startOperationCurrent: epoch => this.startOperationCurrent(epoch),
      beginOperation: retain => this.beginOperation(retain),
      nextVersionMenuEpoch: () => ++this.versionMenuEpoch, versionMenuEpoch: () => this.versionMenuEpoch,
      setStartController: (controller, expected) => {if (!expected || this.startController === expected) this.startController = controller;},
      selectionScope: (signal, current) => this.selectionScope(signal, current), request: this.request,
      beginStarting: (display, retry) => this.startWatch.begin(display, retry), retryStart: input => this.start(input),
      setInitialPresentation: (owner, target) => {this.initialPresentation = owner; this.initialPresentationTarget = target;},
      getInitialPresentation: () => this.initialPresentation,
      setServerCapabilities: (kinds, transitions) => {this.serverKinds = kinds; this.transitions = transitions;},
      remember: playback => this.remember(playback),
      prepareOfferedStart: (entry, offered, expanded) => {
        this.preparing = true; this.startAdmission = true; this.entry = entry;
        this.subject = entry ? subjectOf(entry) : undefined; this.options = offered; this.expanded = expanded;
      },
      finishOfferedStart: epoch => {if (epoch === this.startEpoch) {this.preparing = false; this.startAdmission = false; this.changed();}},
      snapshotFence: () => this.snapshot.fence, fence: () => this.fence(), currentSession: () => this.currentSession(),
      stop: fence => this.stop(fence), publish: () => this.publish(),
    };
  }

  private queueActionsHost(): NativeQueueActionsHost {
    return {
      playback: this.playback, queue: this.queue, dock: this.dock, choices: this.choices,
      retired: () => this.retired, preparing: () => this.preparing, transitionEpoch: () => this.transitionEpoch,
      expanded: () => this.expanded, effectiveRate: () => this.effectiveRate, entry: () => this.entry,
      options: () => this.options, settings: this.o.settings, capabilities: () => this.snapshot.capabilities,
      currentAuthority: () => this.currentAuthority(), startEpoch: () => this.startEpoch,
      startOperationCurrent: epoch => this.startOperationCurrent(epoch), beginOperation: () => this.beginOperation(),
      matches: fence => this.matches(fence), fence: () => this.fence(), currentSession: () => this.currentSession(),
      selectionScope: (signal, current) => this.selectionScope(signal, current),
      setExpanded: value => {this.expanded = value;}, setPreparing: value => {this.preparing = value;},
      applyOptions: () => this.applyOptions(), publish: () => this.publish(), changed: () => this.changed(),
    };
  }

  /** The same exact Home document/route guard is supplied to its selection controller. */
  selectionPlayback = (isCurrent: (context: SelectionContext) => boolean): HomeSelectionPlaybackPort => ({
    play: request => this.playSelection(request, isCurrent),
    enqueue: request => {
      const epoch = this.startEpoch, source = this.sourceEpoch;
      const result = this.enqueueTail.then(() => enqueueSelection(this.selectionHost(), request, isCurrent, epoch, source));
      this.enqueueTail = result.catch(() => {});
      return result;
    },
    playChannel: (channel, context, signal) => this.playChannel(channel, context, signal, isCurrent),
  });
  private selectionCurrent(context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): boolean {
    const scope = context.scope;
    return !signal.aborted && this.currentAuthority() && scope.serverId === this.scope.serverId && scope.authority === this.scope.authority && scope.accountId === this.scope.accountId && scope.profileId === this.scope.profileId && scope.privacyFence === this.o.authority().viewerFence && current(context);
  }
  private assertSelection(context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): void {
    if (!this.selectionCurrent(context, signal, current)) throw new Error(signal.aborted ? 'cancelled' : 'stale_selection_context');
  }
  private selectionScope(signal: AbortSignal, current: () => boolean) {
    const controller = new AbortController();
    const abort = () => controller.abort();
    signal.addEventListener('abort', abort, {once: true}); if (signal.aborted) abort();
    this.selectionControllers.add(controller);
    const unguard = this.http.guard(controller.signal, current);
    return {signal: controller.signal, close: () => {unguard(); signal.removeEventListener('abort', abort); this.selectionControllers.delete(controller); controller.abort();}};
  }
  private rememberCurrent(): void {
    const old = this.dock.getSnapshot().local;
    if (!old) return;
    this.remember(old);
    if (this.snapshot.fence && sameFence(old.fence, this.snapshot.fence)) this.playback.pause();
  }
  /** O-159: a Home selection's Play is drawn at once from its first pressed entry. */
  private playSelection(request: HomeSelectionPlaybackRequest, current: (context: SelectionContext) => boolean) {
    const first = request.items[0], pressed = first && 'entry' in first ? first.entry : undefined;
    return this.startWatch.watched(pressed ? entryDisplay(pressed) : undefined,
      async () => {await this.playSelection({...request, signal: this.startLife.signal}, current);},
      () => playSelectionNow(this.selectionHost(), request, current));
  }

  /** O-159: a Home channel row's Play is drawn at once as that channel. */
  private playChannel(channel: HomeChannel, context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): Promise<void> {
    const name = channel.channel.name, logo = (channel.channel as {logoPath?: string}).logoPath;
    return this.startWatch.watched(name ? {title: name, ...(logo ? {posterUrl: logo} : {})} : undefined,
      () => this.playChannel(channel, context, this.startLife.signal, current),
      () => this.channels.playHomeChannel(channel, context, signal, current), 'none');
  }

  /** Route/Guide playback re-reads the viewer's channel; only the real channel POST grants tuning. */
  /** O-159: drawn at once as the guide row's channel, then its real dock. */
  startChannel = (input: NativePlaybackChannelStart): Promise<void> => {
    const row = input.guide && legacyChannel(input.guide.row);
    return this.startWatch.watched(row?.name ? {title: row.name, ...(row.logoPath ? {posterUrl: row.logoPath} : {})} : undefined,
      () => this.startChannel({...input, lifetime: this.retryLifetime(), onAccepted: undefined, expectedFence: undefined}),
      () => this.channels.startChannel(input), 'none');
  };

  start = (input: NativePlaybackStart): Promise<void> => startItemPlayback(this.startHost(), input);

  /** Opens the actual verified device file through this same player. No server
   * options, queue creation or source URL is supplied by the screen. */
  /** O-159: a download is drawn at once from its own title and kept cover. */
  startOffline = (entry: OfflineEntry, lifetime: NativeOfflinePlaybackLifetime): Promise<void> =>
    this.startWatch.watched({title: entry.title, ...(entry.artworkUrl ? {localArtwork: entry.artworkUrl} : {})},
      () => this.startOffline(entry, this.retryLifetime()), () => startOfflinePlayback(this.offlineHost(), entry, lifetime));

  private readonly cataloguePort = Object.freeze({
    playSegments: (segments: readonly SegmentInput[], options: NonNullable<Parameters<V1QueuePlayer['playSegments']>[1]> = {}) =>
      this.playCatalogueSegments(segments, options),
    enqueueSegments: (segments: readonly SegmentInput[], placement: 'next' | 'end', lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => {
      // Capture the queue/source epochs before waiting behind another append: a call queued
      // for queue A must never adopt a source B that replaced it while it waited.
      const epoch = this.startEpoch, source = this.sourceEpoch;
      const result = this.enqueueTail.then(() => enqueueCatalogueSegments(this.selectionHost(), segments, placement, lifetime, epoch, source));
      this.enqueueTail = result.catch(() => {});
      return result;
    },
  });
  /** Borrow the genuine authority-local player. This port has no Home context,
   * selection membership, page expansion or independent queue/session store. */
  cataloguePlayback = () => this.cataloguePort;

  /** O-159: an album, artist, playlist or radio start is named by its first entry once known. */
  private playCatalogueSegments(segments: readonly SegmentInput[], options: NonNullable<Parameters<V1QueuePlayer['playSegments']>[1]>): Promise<QueueHeader> {
    return this.startWatch.watched({icon: 'i-music'},
      async () => {await this.playCatalogueSegments(segments, {...options, ...this.retryLifetime()});},
      () => playCatalogueSegmentsNow(this.selectionHost(), segments, options));
  }

  /** Play the server-owned whole group, never the screen's loaded preview.
   * The caller supplies its genuine route/member lifetime through adoption. */
  /** O-159: a group or shuffle start is named by its first entry once the queue knows it. */
  startGroup = (input: Readonly<{source: Selector; shuffle: boolean; signal: AbortSignal; isCurrent: () => boolean}>): Promise<void> =>
    this.startWatch.watched({}, () => this.startGroup({...input, ...this.retryLifetime()}), () => startGroupNow(this.selectionHost(), input));

  /** A menu cannot start a version from another authority or replace a newer source. Its
   * cached options prove what was drawn; a fresh scoped read proves it is still offered. */
  /** O-159: "Play a different version" is drawn at once from its title. */
  startVersion = (input: NativePlaybackStart, selection: NativePlaybackVersionSelection): Promise<void> =>
    this.startWatch.watched(entryDisplay(input.entry), () => this.startVersion({...input, lifetime: undefined, presentation: undefined}, {...selection, lifetime: this.retryLifetime()}),
      () => startVersionPlayback(this.startHost(), input, selection));

  /** Info reads borrow the actual queue occurrence. Unlike a replan, a read must
   * remain on the captured generation/revision and must never adopt its reply. */
  readSession = (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): Promise<Session | undefined> =>
    readPlaybackSession(this.listeningReadHost(), fence, lifetime);

  /** Read-only audiobook facts. Mutation signatures remain with Media's accepting owner API. */
  listeningRead = (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): NativeListeningReadPort | undefined =>
    createRuntimeListeningRead(this.listeningReadHost(), fence, lifetime);

  private currentSession(): Session | undefined {
    const held = this.playback.getSnapshot().session;
    const session = held ? this.path.session(held.id) ?? this.queue.currentSession() : undefined;
    return held && session?.id === held.id && session.presentation.generation === held.generation ? session : undefined;
  }
  private detailProjection(): DetailProjection | undefined {
    const binding = this.detail, state = this.playback.getSnapshot();
    if (!binding || !this.currentAuthority() || binding.sourceEpoch !== this.sourceEpoch || binding.sourceKey !== this.sourceKey
      || state.channel || state.intentId !== binding.intentId || state.itemId !== binding.itemId
      || state.session?.id !== binding.sessionId || state.session.generation !== binding.generation
      || this.entry?.id !== binding.itemId || this.entry.libraryId !== binding.libraryId) return;
    const snapshot = binding.service.getSnapshot(), data = snapshot.data;
    if (snapshot.scope.serverId !== this.bound.serverId || snapshot.scope.viewerId !== this.bound.profileId
      || snapshot.target?.itemId !== binding.itemId || snapshot.target.libraryId !== binding.libraryId
      || data?.scope.serverId !== this.bound.serverId || data.scope.itemId !== binding.itemId || data.scope.libraryId !== binding.libraryId
      || data.item.id !== binding.itemId || data.item.libraryId !== binding.libraryId) return;
    return data;
  }
  private fence(): PlaybackFence | undefined {
    const state = this.playback.getSnapshot(), session = this.currentSession(), view = this.queue.getSnapshot().view;
    if (state.session?.mode === 'local') return this.offline?.fence(state.session, state.playbackRun);
    if (state.channel && state.session && state.session.generation > 0 && state.playbackRun > 0) {
      const reference = state.channel, common = {kind: 'channel' as const, playback: {scope: this.scope, run: state.playbackRun}, sessionId: state.session.id, generation: state.session.generation, channelId: reference.channelId, channelGeneration: reference.generation};
      return reference.kind === 'live-source'
        ? playbackFence({...common, channelKind: 'live-source', sourceId: reference.sourceId})
        : playbackFence({...common, channelKind: 'library-channel', sourceId: reference.sourceId});
    }
    if (!state.session || !state.itemId || !session?.versionId || !session.queue?.entryId || state.playbackRun < 1 || this.preparing && this.options?.itemId !== state.itemId) return undefined;
    if (view && !sameScope(view.queue.scope, this.scope)) return undefined;
    const episode = this.detailProjection()?.item.episode;
    return playbackFence({kind: 'queue', playback: {scope: this.scope, run: state.playbackRun}, sessionId: state.session.id, itemId: state.itemId,
      sourceId: session.versionId, entryId: session.queue.entryId, generation: state.session.generation,
      ...(episode?.showId ? {showId: episode.showId} : {}), ...(episode?.seasonId ? {seasonId: episode.seasonId} : {})});
  }
  private local(fence: PlaybackFence): DockPlayback {
    const state = this.playback.getSnapshot(), queue = this.queue.getSnapshot().view?.queue;
    const common = {positionSeconds: state.positionSeconds, playing: state.intent === 'playing' && state.phase !== 'error' && state.phase !== 'ended', expanded: this.expanded};
    if (fence.kind === 'channel') return {...common, fence, kind: 'live', queue: null};
    if (fence.kind === 'local') return {...common, fence, kind: kindOf(this.offlineSource?.metadata.kind), queue: null};
    // A building window is not a completed server-authoritative queue projection.
    let readyQueue: DockPlayback['queue'] = null;
    if (queue) {try {readyQueue = parseQueueSnapshot(queue, this.scope);} catch { /* Keep the validated v1 window in snapshot.queue; never invent a stricter projection. */ }}
    const kind = this.entry?.id === fence.itemId ? this.entry.kind : this.options?.itemId === fence.itemId ? this.options.kind : this.queue.getSnapshot().view?.items.find(item => item.entryId === fence.entryId)?.kind;
    return {...common, fence, kind: kindOf(kind) as 'video' | 'music' | 'audiobook', queue: readyQueue};
  }
  private changed(): void {
    if (!this.active()) return;
    const state = this.playback.getSnapshot(), previousPlayState = this.lastPlayState;
    this.lastPlayState = state.intent;
    if (state.intent === 'paused') this.recovery.clear();
    else if (previousPlayState === 'paused' && state.recovering) this.recoverConnection(false);
    if (this.updating || this.transitionEpoch !== undefined) return;
    this.updating = true;
    try {
      const fence = this.fence();
      if (state.channel) {
        const channel = state.channel, linear = state.linear;
        const name = linear?.name || this.channels?.channelName();
        if (name) this.subject = {kind: 'channel', id: channel.channelId, sourceId: channel.sourceId, channelName: name,
          ...(linear?.programme?.title ? {programmeTitle: linear.programme.title} : {}), live: true};
      }
      if (fence) {
        const opening = this.initialPresentationTarget, accepted = opening?.session;
        if (opening && opening.owner === this.initialPresentation && this.startOperationCurrent(opening.epoch)
          && accepted && accepted === this.currentSession() && fence.kind === 'queue' && fence.itemId === opening.itemId
          && fence.sessionId === accepted.id && fence.generation === accepted.presentation.generation
          && fence.sourceId === accepted.versionId && fence.entryId === accepted.queue?.entryId) opening.owner.bind(fence, false);
        if (fence.kind === 'queue' && this.expandAtStart === this.startEpoch) {
          this.expanded = !audioKind(this.queue.getSnapshot().view?.items.find(item => item.entryId === fence.entryId)?.kind);
          this.expandAtStart = undefined;
        }
        this.choices.begin(fence.playback); this.choices.bindSource(fence); this.subtitleStyles.bind(fence);
        const local = this.local(fence), before = this.dock.getSnapshot().local;
        if (before && sameFence(before.fence, fence)) this.dock.update(local);
        else {
          if (before) this.remember(before);
          const transition = this.dock.start(local);
          // Old sources are already fenced/retired by PlaybackService + coordinator. Never stop
          // the new generation in response to an effect naming the previous generation.
          for (const effect of transition.effects) if (effect.kind === 'pause') this.remember(effect.playback);
        }
        const previous = this.snapshot.playback;
        // A new error needs attention once for its actual intent/source. Repeated error
        // notifications must respect Back without clearing the error or retiring the Surface.
        if (state.phase === 'error' && !this.expanded && (previous.phase !== 'error' || previous.intentId !== state.intentId
          || previous.session?.id !== state.session?.id || previous.session?.generation !== state.session?.generation)) {
          this.expanded = true; this.dock.expand(fence);
        }
      } else if (state.phase === 'idle') this.subtitleStyles.clear();
      const key = state.session && state.itemId && (state.phase === 'starting' || state.phase === 'ready') ? `${state.intentId}/${state.session.id}/${state.session.generation}/${state.itemId}` : '';
      if (key !== this.sourceKey) this.selectSource(key);
      this.applyOptions();
      this.publish();
    } finally {this.updating = false;}
  }
  private remember(playback: DockPlayback): void {
    const session = this.currentSession();
    const previous = this.bookmarks.get(playback.fence.sessionId);
    const state = this.playback.getSnapshot(), fence = playback.fence;
    const current = state.session?.id === fence.sessionId && (fence.kind === 'channel' || fence.kind === 'local' && this.offlineSource?.entryId === fence.entryId || this.entry?.id === fence.itemId);
    this.bookmarks.set(playback.fence.sessionId, {playback, entry: current ? this.entry : previous?.entry, subject: current ? this.subject : previous?.subject,
      queueId: playback.queue?.id ?? (session?.id === playback.fence.sessionId ? session.queue?.queueId : previous?.queueId),
      ...(fence.kind === 'queue' ? {entryId: fence.entryId} : fence.kind === 'local' ? {offline: current ? this.offlineSource : previous?.offline} : {channel: current ? state.channel : previous?.channel})});
    while (this.bookmarks.size > 8) this.bookmarks.delete(this.bookmarks.keys().next().value!);
  }
  private retainedQueueIds(): readonly string[] {
    if (!this.currentAuthority()) throw new Error('Playback authority retired.');
    const state = this.dock.getSnapshot(), session = this.currentSession();
    const queueId = (value: DockPlayback | null): string | undefined => {
      if (!value || value.fence.kind !== 'queue') return;
      if (!sameScope(value.fence.playback.scope, this.scope)) throw new Error('Invalid playback bookmark scope.');
      const saved = this.bookmarks.get(value.fence.sessionId);
      if (session?.id === value.fence.sessionId && session.queue?.entryId === value.fence.entryId) return session.queue.queueId;
      if (saved && sameFence(saved.playback.fence, value.fence)) return saved.queueId;
      return value.queue?.id;
    };
    // Only normalized current/parked/resume state grants candidates. The older cache may
    // contain superseded sessions, and a new subject's kind is not known at create time.
    const candidates = [session?.queue?.queueId ?? queueId(state.local), queueId(state.parkedAudio), queueId(state.resumeVideo)];
    const ids = [...new Set(candidates.filter((id): id is string => id !== undefined))];
    if (ids.length > 3) throw new Error('Too many playback queue bookmarks.');
    return Object.freeze(ids);
  }
  private selectSource(key: string): void {
    clearForcedImage({getForcedImage: () => this.forcedImage, setForcedImage: read => {this.forcedImage = read;}});
    this.external?.resetForSource();
    this.sourceKey = key; const epoch = ++this.sourceEpoch;
    const playback = this.playback.getSnapshot();
    this.recovery.retain(playback.session && (playback.phase === 'starting' || playback.phase === 'ready') ? `${playback.intentId}/${playback.playbackRun}` : undefined);
    this.audioTrackSelection.clear(); this.coordinator.setAudioTrack(null); this.coordinator.setLocalAudioTrack(null); this.localTracks = undefined;
    this.sourceController?.abort();
    this.subtitleOff?.(); this.subtitleOff = undefined;
    const subtitles = this.subtitleService; this.subtitleService = undefined; subtitles?.stop();
    this.detailOff?.(); this.detailOff = undefined;
    this.detail?.service.dispose(); this.detail = undefined; this.fonts = undefined; this.fontsKey = undefined;
    const sourceSession = this.currentSession(), selected = this.options?.versions.find(value => value.id === sourceSession?.versionId);
    this.options = this.options?.itemId === this.playback.getSnapshot().itemId
      && (!sourceSession?.versionId || !!selected && selected.discTitle?.id === sourceSession.discTitle?.id
        && selected.discTitle?.revision === sourceSession.discTitle?.revision) ? this.options : undefined;
    this.effectiveRate = 0; this.supportedRates = [];
    if (!key) {if (!this.preparing && this.playback.getSnapshot().phase === 'idle') {this.entry = undefined; this.subject = undefined;} return;}
    const controller = new AbortController(); this.sourceController = controller;
    const state = this.playback.getSnapshot(), itemId = state.itemId!, session = this.currentSession();
    if (state.session?.mode === 'local') {
      this.options = undefined;
      if (this.offlineSource?.current() && this.offline?.mediaUrl(state.session)) this.localTracks = new NativeOfflineTracks(this.offlineSource.media,
        {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation});
      return;
    }
    if (state.channel) return; // Live references are never catalogue/subtitle endpoint IDs.
    if (this.entry?.id !== itemId) {this.entry = undefined; this.subject = undefined;}
    else this.subject = subjectOf(this.entry);
    const valid = () => {
      const current = this.playback.getSnapshot();
      return this.active() && epoch === this.sourceEpoch && key === this.sourceKey && !controller.signal.aborted
        && !current.channel && current.intentId === state.intentId && current.itemId === itemId
        && current.session?.id === state.session?.id && current.session?.generation === state.session?.generation;
    };
    // PB-5: a failed read keeps the options already shown and is tried once more after a moment.
    const readOptions = (retried: boolean): void => {
      void this.optionsClient.options(itemId, session?.versionId ? {versionId: session.versionId, ...(session.discTitle ? {discTitle: session.discTitle} : {})} : {}, controller.signal).then(value => {
        if (!valid() || value.itemId !== itemId) return; this.options = value; void this.subtitleService?.refresh(); this.applyOptions(); this.publish();
      }, () => {
        if (retried || !valid()) return;
        const setTimer = this.o.timers?.setTimer ?? ((run: () => void, ms: number) => setTimeout(run, ms));
        const timer = setTimer(() => {controller.signal.removeEventListener('abort', cancel); if (valid()) readOptions(true);}, 2000);
        const cancel = () => {if (this.o.timers?.clearTimer) this.o.timers.clearTimer(timer); else clearTimeout(timer as ReturnType<typeof setTimeout>);};
        controller.signal.addEventListener('abort', cancel, {once: true});
      });
    };
    readOptions(false);
    void readItem({request: this.request}, itemId, controller.signal).then(raw => {
      if (!valid()) return;
      // The title's card: a song is a "song" and an audiobook plays as its file.
      const card = entryFromItem(raw), item = {libraryId: raw.libraryId};
      if (!card || card.id !== itemId) return;
      const kind = raw.kind === 'audiobook' ? 'audiobook_file' : card.kind;
      if (!['movie', 'episode', 'extra', 'song', 'audiobook_file'].includes(kind)) return;
      this.entry = {...this.entry, ...card, kind: kind as ContentEntry['kind']}; this.subject = subjectOf(this.entry);
      const detailApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
        if (!valid() || !signal) throw new Error('Playback detail source retired.');
        // DetailService owns its read signal; bind that actual request to this source too.
        const unguard = this.http.guard(signal, valid);
        try {return await this.request<T>(path, method, body, signal);}
        finally {unguard();}
      }};
      // This source's own guarded reads: its page is cached with them, not with the shell's screens.
      const detailScope = {serverId: this.bound.serverId, viewerId: this.bound.profileId};
      const detail = new DetailService({queries: viewerQueriesFor(detailApi, detailScope), api: detailApi, scope: detailScope, requestId: async () => this.o.requestId()});
      const binding: SourceDetail = {service: detail, sourceEpoch: epoch, sourceKey: key, intentId: state.intentId,
        sessionId: state.session!.id, generation: state.session!.generation, itemId, libraryId: item.libraryId};
      this.detail = binding;
      this.detailOff = detail.subscribe(() => {
        if (!valid() || this.detail !== binding) return;
        // Step 7: a moved Detail fence is a newer answer for this item; the service's parser checks
        // server/library/item scope, and a 401/403 clears its data (and so this projection).
        const data = this.detailProjection();
        this.subject = this.entry ? subjectOf(this.entry, data) : undefined;
        this.changed();
      });
      void detail.select({itemId, libraryId: item.libraryId});
    }).catch(() => {});
    if (state.session && session?.kind === 'vod') {
      // The plan is the session's own, from it and this title's playback options (as web makes it); read when either changes.
      const service = new SubtitleService({request: this.request}, itemId, () => {
        const playing = this.currentSession(), version = this.options?.versions.find(value => value.id === playing?.versionId);
        return playing && version && playing.id === state.session!.id ? sessionSubtitlePlan(playing, version) : null;
      });
      this.subtitleService = service;
      const offs = [service.subscribe(() => {if (valid()) {this.applyOptions(); this.publish();}})];
      this.subtitleOff = () => {for (const off of offs) off();};
      service.start();
    }
  }

  private applyOptions(): void {
    applyPlaybackOptions(this.optionsHost());
  }

  setForcedOnly = (fence: PlaybackFence, on: boolean): boolean => {
    if (typeof on !== 'boolean' || !this.matches(fence) || !forcedImageAvailable(this.optionsHost(), fence)) return false;
    return this.choices.set('forcedOnly', fence.playback, on, fence);
  };

  setSubtitleStyle = (fence: PlaybackFence, key: string, value: string): boolean => {
    if (!this.matches(fence) || fence.kind !== 'queue' || !this.subtitleStyles.set(fence, key, value)) return false;
    this.applyOptions(); this.publish(); return true;
  };

  private makeSnapshot(sequence: number): NativePlaybackRuntimeSnapshot {
    const playback = this.playback.getSnapshot(), nextFence = this.retired ? undefined : this.fence(), queue = this.queue.getSnapshot(), view = queue.view;
    const previousFence = this.snapshot?.fence;
    // Progress is a new observation, not a new Surface/source lifetime. Metadata
    // can change within the same playback fence and must still reach consumers.
    const fence = nextFence && sameFence(previousFence, nextFence)
      && (nextFence.kind !== 'queue' || previousFence?.kind === 'queue' && previousFence.showId === nextFence.showId && previousFence.seasonId === nextFence.seasonId)
      ? previousFence : nextFence;
    const currentIndex = fence?.kind === 'queue' ? view?.items.findIndex(item => item.entryId === fence.entryId) ?? -1 : -1;
    // A book part with known chapters moves by chapter, across parts (MU-7).
    const chapterPrevious = fence?.kind === 'queue' ? this.queue.chapterTarget('previous') : undefined, chapterNext = fence?.kind === 'queue' ? this.queue.chapterTarget('next') : undefined;
    const ready = !!fence && !this.retired && (playback.phase === 'ready' || playback.phase === 'starting');
    const ended = !!fence && !this.retired && fence.kind !== 'channel' && playback.phase === 'ended';
    const session = this.currentSession();
    const localTracks = fence?.kind === 'local' ? this.localTracks?.getSnapshot() : undefined;
    const offered = view?.window;
    const rowFacts = view && offered && offered.queueId === view.queue.id && offered.revision === view.queue.revision ? offered.entries : undefined;
    const currentFact = fence?.kind === 'queue' ? rowFacts?.find(entry => entry.entryId === fence.entryId) : undefined;
    const currentPosition = currentFact?.position;
    const queueEditReady = ready && !this.preparing && fence?.kind === 'queue' && !!view
      && session?.queue?.queueId === view.queue.id && session.queue.entryId === fence.entryId && view.queue.currentEntryId === fence.entryId
      && currentFact?.available === true && currentPosition !== undefined
      && rowFacts?.some(entry => entry.available === true && entry.position !== undefined && entry.position > currentPosition) === true;
    const videoRows = new Set((view?.items ?? []).filter(item => queueVideoKind(item.kind)).map(item => item.entryId));
    const queueSelectionReady = ready && !this.preparing && fence?.kind === 'queue' && !playback.nativeListening
      && !this.playback.hasNativeOwner() && session?.kind === 'vod' && session.role === 'local' && !!view
      && session.queue?.queueId === view.queue.id && session.queue.entryId === fence.entryId && view.queue.currentEntryId === fence.entryId
      && currentFact?.available === true && currentPosition !== undefined
      && rowFacts?.some(entry => entry.entryId !== fence.entryId && entry.available === true && entry.position !== undefined && videoRows.has(entry.entryId)) === true;
    const nativeBinding = this.coordinator.getBinding(), localPicture = this.dock.getSnapshot().local;
    const firstVideoFrame = fence && !this.retired && nativeBinding && this.firstVideoFrame
      && this.firstVideoFrame.engine === this.coordinator.getEngine() && this.firstVideoFrame.instanceId === this.coordinator.getInstanceId()
      && sameNativePresentation(this.firstVideoFrame, nativeBinding) && this.firstVideoFrame.intentId === playback.intentId
      && this.firstVideoFrame.sessionId === fence.sessionId && this.firstVideoFrame.generation === fence.generation
      && this.firstVideoFrame.itemId === playback.itemId && localPicture && sameFence(localPicture.fence, fence)
      && (localPicture.kind === 'video' || localPicture.kind === 'live')
      ? this.firstVideoFrame : undefined;
    const externalFields = this.external?.snapshotFields(playback, fence, nativeBinding) ?? {};
    const seekWindow = playback.channel && playback.linear ? {startSeconds: channelSeconds(playback.linear.media.windowStartUs), endSeconds: channelSeconds(playback.linear.media.windowEndUs)} : undefined;
    const channelGuide = this.channels?.guideFor(fence);
    return Object.freeze({authority: this.bound, sequence, dock: this.dock.getSnapshot(), playback, fence, subject: this.subject, entry: this.entry, queue, session, policy: this.videoPolicy.getSnapshot(),
      ...(channelGuide ? {channelGuide} : {}),
      ...(this.failure && playback.phase === 'error' && this.failure.intentId === playback.intentId && this.failure.run === playback.playbackRun ? {failureReason: this.failure.reason} : {}),
      ...(this.streamSubtitleFailure && this.streamSubtitleFailure === this.subtitleService?.getSnapshot().plan ? {subtitleUnavailable: true as const} : {}),
      startFailure: this.startFailure, capacityWait: this.capacityWait, firstVideoFrame, ...(this.startWatch?.fields(fence) ?? {}),
      initialPresentation: this.initialPresentation?.read(fence),
      ...(fence && forcedImageAvailable(this.optionsHost(), fence) ? {forcedOnly: Object.freeze({on: this.choices.get('forcedOnly', fence.playback, false)})} : {}),
      options: this.options, subtitles: this.subtitleService?.getSnapshot(), ...(fence?.kind === 'queue' ? {subtitleStyle: this.subtitleStyles.read(fence, this.o.settings().subtitles)} : {}), localTracks, audioTrackSelection: this.audioTrackSelection.getSnapshot(), seekWindow, engine: this.coordinator.getEngine() === 'mpv' ? 'mpv' : 'native', lease: this.coordinator.getSnapshot(), effectiveRate: playback.nativeListening?.effectiveRate ?? (this.effectiveRate > 0 ? this.effectiveRate : this.chosenRate), supportedRates: playback.nativeListening?.supportedRates ?? this.supportedRates, ...externalFields, retired: this.retired,
      bookChapters: fence?.kind === 'queue' ? this.queue.bookChaptersView() : null,
      capabilities: Object.freeze({queueSelection: queueSelectionReady, queueEdit: queueEditReady, queuePolicy: ready && fence?.kind === 'queue' && !!view && session?.queue?.queueId === view.queue.id && view.queue.currentEntryId === fence.entryId, retry: playback.phase === 'error' && !this.preparing && fence?.kind === 'queue' && session?.kind === 'vod' && session.presentation.subtitle?.delivery !== 'other' && view?.queue.currentEntryId === fence.entryId && view.queue.entries.some(entry => entry.id === fence.entryId && !entry.hidden && !entry.removed), play: ready && playback.intent === 'paused' || ended, pause: ready && playback.intent === 'playing', stop: !!fence, seek: (ready || ended) && (seekWindow ? seekWindow.endSeconds > seekWindow.startSeconds : playback.duration > 0),
        previous: ready && fence?.kind === 'queue' && (chapterPrevious !== undefined ? !!chapterPrevious : playback.positionSeconds > 3 || currentIndex > 0), next: ready && fence?.kind === 'queue' && (chapterNext !== undefined ? !!chapterNext : !!view && view.next.reason !== 'end'), cast: ready && fence?.kind === 'queue' && !!this.o.openDestinations,
        // The system route picker moves the retained player; Portico adds no second transport.
        airPlayVideo: ready && fence?.kind === 'queue' && localPicture?.kind === 'video' && !this.path.controller.getSnapshot().changing
          && this.o.engineFeatures(this.coordinator.getEngine() === 'mpv' ? 'mpv' : 'native').airPlayVideo,
        quality: ready && !playback.nativeListening && !!this.options?.qualities.length, version: ready && !playback.nativeListening && (this.options?.versions.length ?? 0) > 1,
        audioTrack: ready && !playback.nativeListening && (localTracks ? localTracks.audio.length > 1 : !!this.options?.versions.find(value => value.id === session?.versionId)?.audio.length),
        subtitleTrack: ready && !playback.nativeListening && (localTracks ? localTracks.subtitles.length > 0 : !!this.subtitleService?.getSnapshot().plan),
        subtitlesCanHide: !!localTracks?.subtitles.length || fence?.kind === 'queue' && session?.presentation.subtitle?.delivery !== 'burn' && session?.presentation.decision.subtitles?.action !== 'burn' && this.subtitleService?.getSnapshot().plan?.renderer !== 'burn_in'})});
  }
  private publish(): void {
    // A native fact can synchronously publish through the path, service and
    // queue before its observation is settled. React must see that transaction
    // once, after all those listeners have run, rather than re-enter their fanout.
    if (this.factDepth) {this.factPublication = true; return;}
    this.videoPolicy.update();
    this.snapshot = this.makeSnapshot(this.snapshot.sequence + 1);
    for (const listener of [...this.listeners]) listener();
  }

  private acceptFact(fact: any): void {
    if (!this.active()) return;
    this.factDepth++;
    try {this.applyFact(fact);}
    finally {
      if (--this.factDepth === 0 && this.factPublication) {
        this.factPublication = false;
        this.publish();
      }
    }
  }
  private applyFact(fact: any): void {
    const before = this.playback.getSnapshot();
    const recovered = mediaRecovered(fact, before.positionSeconds);
    // Called only after the coordinator admits the current instance, command
    // barrier, sequence and binding. ready/playing/buffering are not this proof.
    if (fact.type === 'firstFrame') {
      const binding = nativeFirstVideoFrame(fact, this.coordinator.getBinding(), this.coordinator.getEngine(), this.coordinator.getInstanceId());
      if (binding && before.pendingSeek && !(this.firstVideoFrame && sameNativePresentation(this.firstVideoFrame, binding))) this.heldFirstVideoFrame = binding;
      else if (binding) {this.firstVideoFrame = binding; this.heldFirstVideoFrame = undefined;}
    }
    this.external?.acceptFact(fact, this.coordinator.getBinding());
    // BUFFERING can emit an unchanged progress tick. Keep both the UI and the
    // server observation stalled until actual media recovery is observed.
    if (!(fact.type === 'progress' && before.recovering && !recovered && before.intent === 'playing')) this.path.engineFact(fact);
    // The opening seek landed (or failed, which shows the picture where it is): the held frame is now true.
    if (this.heldFirstVideoFrame && !this.playback.getSnapshot().pendingSeek) {this.firstVideoFrame = this.heldFirstVideoFrame; this.heldFirstVideoFrame = undefined;}
    if (recovered || fact.type === 'ended') {
      this.recovery.clear();
      const route = this.o.api.getRouteSnapshot();
      if (!route || route.phase === 'ready' || fact.type === 'ended') this.playback.recovering(before.intentId, false);
    }
    if (this.audioTrackSelection.accept(fact)) {
      if (fact.type === 'audioTrackFailed') this.playback.fail(before.intentId, serviceText('player.opt.unavailable'));
      else this.applyOptions();
    }
    // A downloaded copy's tracks answer through localTracks below; a streamed session's here.
    if (fact.type === 'subtitleStatus' && before.session && before.session.mode !== 'local' && typeof fact.error === 'string') {
      const plan = this.subtitleService?.getSnapshot().plan;
      this.streamSubtitleFailure = fact.error && plan ? plan : undefined;
    }
    if (before.session?.mode === 'local') {
      const result = this.localTracks?.accept(fact);
      if (result === 'failed') this.playback.fail(before.intentId, serviceText('player.opt.unavailable'));
      else if (result === 'changed') this.applyOptions();
    }
    // A paused player reports rate 0: the speed shown and chosen stays the one it plays at.
    if (typeof fact.effectiveRate === 'number' && fact.effectiveRate > 0) this.effectiveRate = fact.effectiveRate;
    if (Array.isArray(fact.supportedRates)) this.supportedRates = Object.freeze(fact.supportedRates.filter((rate: unknown): rate is number => typeof rate === 'number' && Number.isFinite(rate) && rate >= .5 && rate <= 3));
    if (fact.type === 'externalPlayback') this.external?.followAirPlay(fact.active === true);
    else if (fact.type === 'progress') this.external?.followAirPlay();
    if (fact.type === 'systemPaused') this.playback.pause();
    else if (fact.type === 'ready') this.playback.ready(before.intentId);
    else if (fact.type === 'stalled') {
      if (before.session?.mode === 'local') this.playback.recovering(before.intentId, true);
      else if (before.intent === 'playing') this.recoverConnection(false);
    }
    else if (fact.type === 'seekApplied') this.playback.seekApplied(before.intentId, fact.seekRevision, fact.position, fact.paused ? 'paused' : 'playing');
    else if (fact.type === 'seekFailed') this.playback.seekFailed(before.intentId, fact.seekRevision, serviceText('queuePlayer.startFailed'));
    else if (fact.type === 'progress' || fact.type === 'ended') this.playback.fact(before.intentId, fact.position, fact.type === 'ended' ? 'ended' : fact.paused ? 'paused' : 'playing');
    else if (fact.type === 'error' && before.session && atNaturalEnd(Math.max(typeof fact.position === 'number' ? fact.position : 0, before.positionSeconds), before.duration)) {
      this.recovery.clear(); this.playback.fact(before.intentId, before.duration, 'ended');
    }
    else if (fact.type === 'error') {
      const failure = classifyPlaybackFailure(fact), reason = playbackFailureReason(fact);
      const remember = (value = reason) => {this.failure = value ? Object.freeze({intentId: before.intentId, run: before.playbackRun, reason: value}) : undefined;};
      if (failure.network && this.o.api.getRouteConnection()) {
        this.recoverConnection();
        return;
      }
      this.recovery.clear();
      remember();
      if (failure.engine) this.playback.engineFailed(before.intentId, failure.engine, '', serviceText('queuePlayer.unsupported'));
      else this.playback.fail(before.intentId, serviceText('queuePlayer.startFailed'));
    }
    if (fact.rateError && before.intent === 'playing') this.playback.pause();
    this.publish();
  }

  /** Losing the route can suspend the engine without an error fact. It has the same
   * finite recovery as a failed media request, including the reason and Retry. */
  private recoverConnection(recheckReadyRoute = true): void {
    const before = this.playback.getSnapshot();
    const current = () => {const state = this.playback.getSnapshot(); return this.active() && state.intentId === before.intentId
      && state.playbackRun === before.playbackRun && state.intent === 'playing' && !!state.session && (state.phase === 'starting' || state.phase === 'ready');};
    if (!this.active() || !before.session || before.phase !== 'starting' && before.phase !== 'ready') return;
    this.playback.recovering(before.intentId, true);
    if (!current()) return;
    this.recovery.start(`${before.intentId}/${before.playbackRun}`, current, () => {
      this.failure = recheckReadyRoute || this.o.api.getRouteSnapshot()?.phase !== 'ready'
        ? Object.freeze({intentId: before.intentId, run: before.playbackRun, reason: 'connection'}) : undefined;
      this.playback.fail(before.intentId, serviceText('queuePlayer.startFailed'));
    }, () => {if (recheckReadyRoute || this.o.api.getRouteSnapshot()?.phase !== 'ready') void this.o.api.recoverRoute().catch(() => {});});
  }

  private videoPolicyCurrent(fence: QueuePlaybackFence): boolean {
    return videoPolicyCurrentFor({currentAuthority: () => this.currentAuthority(), fence: () => this.fence()}, fence);
  }

  private async applyPolicySeek(fence: PlaybackFence, action: Readonly<{kind: 'skip'; markerId: string}> | Readonly<{kind: 'undo'}>): Promise<void> {
    const state = this.playback.getSnapshot();
    // Do not consume a prompt or Undo before the current output can accept its
    // seek. The executor rechecks the same conditions before touching playback.
    if (fence.kind !== 'queue' || !this.videoPolicyCurrent(fence) || state.phase !== 'ready'
      || state.recovering || state.pendingSeek || state.failedSeek) return;
    if (action.kind === 'skip') await this.videoPolicy.skip(fence, action.markerId);
    else await this.videoPolicy.undo(fence);
  }

  policyDismiss = (fence: PlaybackFence): boolean => fence.kind === 'queue' && this.videoPolicy.dismiss(fence);
  policySkip = (fence: PlaybackFence, markerId: string): Promise<void> => this.applyPolicySeek(fence, {kind: 'skip', markerId});
  policyUndo = (fence: PlaybackFence): Promise<void> => this.applyPolicySeek(fence, {kind: 'undo'});
  policyAdvance = async (fence: PlaybackFence, mode: 'manual' | 'still-watching'): Promise<void> => {if (fence.kind === 'queue') await this.videoPolicy.advance(fence, mode);};
  policyCancelAutoplay = (fence: PlaybackFence): void => {if (fence.kind === 'queue') this.videoPolicy.cancelAutoplay(fence);};

  toggle = async (fence: PlaybackFence): Promise<void> => {
    if (!this.matches(fence)) return;
    const state = this.playback.getSnapshot();
    // At the end, Play plays the title again from its start, as the end card's Play again does.
    if (state.phase === 'ended' && fence.kind !== 'channel') { await this.replay(fence); return; }
    if (state.intent === 'playing') this.playback.pause(); else this.playback.resume();
  };
  selectQueuedEntry = (fence: PlaybackFence, request: V1QueuedEntrySelection, lifetime: NativePlaybackReadLifetime): Promise<void> =>
    selectQueuedEntryAction(this.queueActionsHost(), fence, request, lifetime);
  editQueue = (fence: PlaybackFence, request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueueEditIntent}>, lifetime: NativePlaybackReadLifetime): Promise<void> =>
    editQueueAction(this.queueActionsHost(), fence, request, lifetime);
  setQueuePolicy = (fence: PlaybackFence, request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueuePolicyIntent}>, lifetime: NativePlaybackReadLifetime): Promise<void> =>
    setQueuePolicyAction(this.queueActionsHost(), fence, request, lifetime);
  retry = (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): Promise<void> =>
    retryFailedQueueAction(this.queueActionsHost(), fence, lifetime);
  replay = async (fence: PlaybackFence): Promise<void> => {
    if (!this.matches(fence)) return;
    if (fence.kind === 'channel') {this.channels.retry(); return;}
    if (fence.kind === 'local') {
      if (this.playback.getSnapshot().phase === 'ended') await restartOfflinePlayback(this.offlineHost(), fence, 0);
      else if (this.playback.getSnapshot().phase === 'error') await this.playback.retry();
      else {this.playback.seek(0); this.playback.resume();}
      return;
    }
    const session = this.currentSession(), listening = this.playback.getSnapshot().nativeListening;
    // The listening bridge retries only the audio session it actually owns.
    // Ordinary native video always restarts the current queue entry from zero.
    if (session?.kind === 'audio' && session.id === fence.sessionId && listening?.binding?.sessionId === session.id
      && listening.binding.generation === session.presentation.generation
      && this.playback.hasNativeOwner() && this.o.nativeListening) await this.o.nativeListening.action('retry', 0);
    else await this.queue.playItem(fence.itemId, 0, undefined, undefined, undefined,
      session?.discTitle ? {versionId: session.versionId, discTitle: session.discTitle} : undefined);
  };
  trickplay = async (fence: PlaybackFence, signal: AbortSignal): Promise<TrickplaySet | null> => {
    // A download plays from its own kept sheets, with no server in reach (queue 6).
    if (fence.kind === 'local') {
      const source = this.offlineSource;
      // `matches` proves this fence is the one playing now, which is the current offline source's.
      return source && source.current() && this.matches(fence)
        ? downloadTrickplaySet(source.media.extras?.bundle?.trickplay, source.media.extras?.trickplayTiles) : null;
    }
    // `matches` proves this is the playing occurrence; its loaded queue entry names the library.
    if (fence.kind !== 'queue' || !this.matches(fence)) return null;
    // Asked only from a server that says it makes scrub previews (read once), so a server without
    // them costs no failing read.
    if (!await (this.scrubPreviews ??= readScrubPreviewsEnabled({request: this.request})) || signal.aborted || !this.matches(fence)) return null;
    const set = await readScrubPreviews(this.request, fence.itemId, fence.sourceId, signal);
    return signal.aborted || !this.matches(fence) ? null : set;
  };
  playBookChapter = async (fence: PlaybackFence, chapterId: string): Promise<void> => {
    if (fence.kind !== 'queue' || !this.matches(fence) || !this.snapshot.capabilities.seek) return;
    const chapter = this.queue.bookChaptersView()?.chapters.find(row => row.id === chapterId);
    if (chapter) await this.queue.playChapter(chapter, seconds => this.seek(fence, seconds));
  };
  seek = async (fence: PlaybackFence, seconds: number): Promise<void> => {
    if (!this.matches(fence) || !Number.isFinite(seconds) || seconds < 0 || !this.snapshot.capabilities.seek) return;
    const window = this.snapshot.seekWindow;
    if (fence.kind === 'channel' && (!window || seconds < window.startSeconds || seconds > window.endSeconds)) return;
    const ended = this.playback.getSnapshot().phase === 'ended';
    if (ended && fence.kind === 'local') {await restartOfflinePlayback(this.offlineHost(), fence, seconds); return;}
    if (ended && fence.kind === 'queue') {
      const session = this.currentSession();
      await this.queue.playItem(fence.itemId, seconds, undefined, undefined, undefined,
        session?.discTitle ? {versionId: session.versionId, discTitle: session.discTitle} : undefined);
      return;
    }
    this.playback.seek(seconds);
  };
  skip = async (fence: PlaybackFence, seconds: number): Promise<void> => {if (this.matches(fence) && Number.isFinite(seconds)) await this.seek(fence, Math.max(0, this.playback.getSnapshot().positionSeconds + seconds));};
  previous = async (fence: PlaybackFence): Promise<void> => {if (this.matches(fence) && this.snapshot.capabilities.previous && !await this.chapterMove(fence, 'previous')) await this.queue.previous();};
  next = async (fence: PlaybackFence): Promise<void> => {const view = this.queue.getSnapshot().view; if (this.matches(fence) && this.snapshot.capabilities.next && !await this.chapterMove(fence, 'next') && view) await this.queue.next(view.queue.id, view.queue.revision);};
  /** On a book part with known chapters, Previous and Next move by chapter: a seek within the
   * part, else a jump to the chapter's part at the chapter's start (MU-7). False: the queue moves. */
  private async chapterMove(fence: PlaybackFence, direction: 'previous' | 'next'): Promise<boolean> {
    if (fence.kind !== 'queue') return false;
    const target = this.queue.chapterTarget(direction);
    if (target === undefined) return false;
    if (target === null) return true;
    await this.queue.playChapter(target, seconds => this.seek(fence, seconds));
    return true;
  }
  goLive = async (fence: PlaybackFence): Promise<void> => {if (this.matches(fence) && fence.kind === 'channel') this.channels.goLive();};

  /** The remote dock's own transitions: this app's player is not the source of any of them. */
  beginRemote = async (value: RemoteDock): Promise<void> => {
    if (!this.active()) return;
    // The explicit connection start may only retire the playback it was captured
    // from; a replacement dock playback is never stopped by a late handoff.
    const local = this.dock.getSnapshot().local;
    if (local && !sameFence(local.fence, value.playback.fence)) return;
    await this.onTransition(this.dock.beginRemote(value, Date.now()));
  };
  updateRemote = async (value: RemoteDock): Promise<void> => {if (this.active()) await this.onTransition(this.dock.remote(value, Date.now()));};
  dismissRemote = async (id: string): Promise<void> => {if (this.active()) await this.onTransition(this.dock.dismissRemote(id));};
  continueHere = async (id: string): Promise<void> => {if (this.active()) await this.onTransition(this.dock.continueHere(id));};
  setSleepTimer = async (fence: PlaybackFence, choice: NativeSleepChoice): Promise<void> => {
    if (!this.matches(fence)) return;
    if (typeof choice === 'string') {
      // The end of this title, chapter or book: the listening service verifies chapter timing and
      // the book's order before it arms (MU-6). A minutes deadline gives way to it.
      if (!['item', 'chapter', 'book'].includes(choice)) throw new Error(serviceText('queuePlayer.unsupported'));
      await this.playback.setListeningDeadline('sleep', null);
      await this.queue.listening.setTimer(choice);
    } else {
      if (this.queue.listening.getSnapshot().timer) this.queue.listening.clearTimer();
      await this.playback.setListeningDeadline('sleep', choice);
    }
    this.changed();
  };
  sleepEnd = (fence: PlaybackFence): NativeSleepEnd | undefined => {
    if (!this.matches(fence)) return undefined;
    const choice = this.queue.listening.getSnapshot().timer?.choice;
    return choice === 'item' || choice === 'chapter' || choice === 'book' ? choice : undefined;
  };
  stop = async (fence: PlaybackFence): Promise<void> => {if (this.matches(fence)) await this.onTransition(this.dock.close(fence));};
  open = (fence: PlaybackFence): void => {if (this.matches(fence)) {this.expanded = true; this.dock.expand(fence); this.applyOptions(); this.publish();}};
  collapse = (fence: PlaybackFence): void => {if (this.matches(fence)) {this.expanded = false; this.dock.collapse(fence); this.applyOptions(); this.publish();}};
  setSubtitleDisplay(visible: boolean): void {if (this.displaySubtitles !== visible) {this.displaySubtitles = visible; this.applyOptions();}}
  subtitleControls = (fence: PlaybackFence): NativePlaybackSubtitleControls | undefined => {
    const service = this.subtitleService, epoch = this.sourceEpoch, key = this.sourceKey;
    const plan = service?.getSnapshot().plan;
    if (!service || !plan || fence.kind !== 'queue' || !this.matches(fence) || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation) return undefined;
    const current = () => this.matches(fence) && this.subtitleService === service && this.sourceEpoch === epoch && this.sourceKey === key
      && service.getSnapshot().plan?.sourceId === plan.sourceId;
    // A downloaded subtitle is a new track of this version: read the options again so it is offered, then turn it on.
    return boundSubtitleControls(service, current, async trackId => {
      const playing = this.currentSession();
      if (!current() || !playing) return;
      const options = await this.optionsClient.options(fence.itemId, playing.versionId ? {versionId: playing.versionId, ...(playing.discTitle ? {discTitle: playing.discTitle} : {})} : {}, this.sourceController?.signal);
      if (!current() || options.itemId !== fence.itemId) return;
      this.options = options; void service.refresh(); this.publish();
      await this.change(fence, {subtitleTrackId: trackId});
    });
  };
  cast = async (fence: PlaybackFence, anchorRef?: RefObject<View | null>): Promise<void> => {if (this.matches(fence) && this.o.openDestinations) await this.o.openDestinations(fence, anchorRef);};

  prepareExternalPresentation = (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime): Promise<NativeBinding | undefined> =>
    this.external.prepare(fence, lifetime);
  requestPictureInPicture = (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime): Promise<boolean> =>
    this.external.requestPictureInPicture(fence, lifetime);
  admitPictureInPicture = (fact: unknown): boolean =>
    this.external.admitPictureInPicture(fact);

  change = async (fence: PlaybackFence, choice: PlaybackChoices): Promise<void> => {
    if (!this.matches(fence)) return;
    if (fence.kind === 'local') {
      if (!this.localTracks) throw new Error(serviceText('player.opt.unavailable'));
      this.localTracks.choose(choice); this.applyOptions(); this.coordinator.reconsiderEngine(); this.publish(); return;
    }
    if (this.playback.hasNativeOwner()) throw new Error(serviceText('player.opt.unavailable'));
    const options = this.options, version = options?.versions.find(value => value.id === this.currentSession()?.versionId);
    if (choice.quality && !options?.qualities.some(value => value.mode === choice.quality!.mode && (value.mode !== 'fixed' || value.id === ('id' in choice.quality! ? choice.quality!.id : undefined)))) throw new Error('Invalid playback quality.');
    if (!options || choice.versionId && !options.versions.some(value => value.id === choice.versionId) || choice.audioTrackId && !version?.audio.some(value => value.id === choice.audioTrackId) || choice.subtitleTrackId && !version?.subtitles.some(value => value.id === choice.subtitleTrackId) && !this.subtitleService?.getSnapshot().plan?.resources.some(value => value.id === choice.subtitleTrackId && value.enabled)) throw new Error('Invalid playback choice.');
    await this.playback.changePlayback(choice);
  };
  chooseQuality = async (fence: PlaybackFence, choice: QualityOption): Promise<void> => {
    if (!this.matches(fence)) return;
    if (this.playback.hasNativeOwner()) throw new Error(serviceText('player.opt.unavailable'));
    if (!this.options?.qualities.some(value => value.mode === choice.mode && value.id === choice.id)) return;
    this.path.forgetAutomaticStep(); await this.playback.changePlayback({quality: qualityOptionRequest(choice)});
  };
  setRate = (fence: PlaybackFence, rate: number): void => {
    if (!this.matches(fence) || !this.snapshot.supportedRates.includes(rate)) return;
    if (this.choices.set('speed', fence.playback, rate, fence)) {this.playback.setPlaybackRate(rate); this.applyOptions(); this.publish();}
  };
  onTransition = async (transition: DockTransition): Promise<void> => {await this.applyTransition(transition);};

  /** Resume actions name the offered bookmark. They never toggle whatever became current. */
  restoreBookmark = async (expected: PlaybackFence, playExplicitly: boolean): Promise<PlaybackFence | undefined> => {
    if (!this.active()) return;
    const state = this.dock.getSnapshot();
    const offered = [state.parkedAudio, state.resumeVideo, state.local].find(value => value && sameFence(value.fence, expected));
    const saved = this.bookmarks.get(expected.sessionId);
    if (!offered || !saved || !sameFence(saved.playback.fence, expected)) return;
    let transition: DockTransition;
    if (state.parkedAudio && sameFence(state.parkedAudio.fence, expected)) transition = this.dock.restoreAudio();
    else if (state.resumeVideo && sameFence(state.resumeVideo.fence, expected)) transition = this.dock.restoreVideo();
    else transition = {state, effects: [{kind: 'restore', playback: offered, playing: false}]};
    const result = await this.applyTransition(transition);
    if (!result || result.epoch !== this.startEpoch || !this.matches(result.fence)) return;
    if (playExplicitly) this.playback.resume();
    return result.fence;
  };

  private applyTransition = async (transition: DockTransition): Promise<{fence: PlaybackFence; epoch: number} | undefined> => {
    if (!this.active() || transition.state !== this.dock.getSnapshot()) return;
    if (!transition.effects.length) {
      this.expanded = transition.state.local?.expanded ?? false;
      this.applyOptions(); this.publish(); return;
    }
    const epoch = this.beginOperation();
    const controller = new AbortController(); this.startController = controller;
    this.transitionEpoch = epoch;
    let restored: PlaybackFence | undefined;
    try {
      await this.queue.cancel();
      if (!this.active() || epoch !== this.startEpoch) return;
      this.expanded = transition.state.local?.expanded ?? false;
      for (const effect of transition.effects) {
        if (!this.active() || epoch !== this.startEpoch) return;
        const fence = this.fence();
        if (effect.kind === 'restore') {
          const saved = this.bookmarks.get(effect.playback.fence.sessionId);
          if (!saved) throw new Error('Playback bookmark is unavailable.');
          this.entry = saved.entry; this.subject = saved.subject; this.expanded = false;
          if (effect.playback.fence.kind === 'channel') {
            if (!saved.channel) throw new Error('Playback bookmark is unavailable.');
            const valid = () => this.currentAuthority() && epoch === this.startEpoch && !controller.signal.aborted;
            await this.channels.open(saved.channel, epoch, controller.signal, valid, 'paused');
            if (!valid()) return;
            const current = this.fence(), linear = this.playback.getSnapshot().linear;
            const position = effect.playback.positionSeconds;
            if (!current || current.kind !== 'channel' || !linear || position < channelSeconds(linear.media.windowStartUs) || position > channelSeconds(linear.media.windowEndUs)) {
              this.playback.leave(); throw new Error('channel_position_unavailable');
            }
            try {
              await this.channels.restorePosition(current, position, epoch, controller.signal);
            } catch (error) {
              // Retire only this restore's paused session, never a replacement that won.
              if (epoch === this.startEpoch && this.currentAuthority()) this.playback.leave();
              throw error;
            }
          } else if (effect.playback.fence.kind === 'local') {
            await reopenOfflineBookmark(this.offlineHost(), saved.offline, effect.playback.positionSeconds, epoch, controller.signal);
          } else {
            if (!saved.queueId || !saved.entryId) throw new Error('Playback bookmark is unavailable.');
            // Canonical restore starts the actual queue entry paused, with no transient play.
            await this.queue.restorePaused(saved.queueId, saved.entryId, effect.playback.positionSeconds);
          }
          if (this.active() && epoch === this.startEpoch) {
            if (this.playback.getSnapshot().intent !== 'paused') throw new Error('Playback bookmark did not restore paused.');
            restored = this.fence();
          }
        } else if (fence && sameFence(fence, effect.playback.fence)) {
          this.remember(effect.playback);
          if (effect.kind === 'pause') this.playback.pause();
          else {this.playback.leave(); this.coordinator.reconsiderEngine();}
        }
      }
    } catch (error) {
      if (epoch !== this.startEpoch || !this.currentAuthority()) return;
      throw error;
    } finally {
      if (this.transitionEpoch === epoch) {this.transitionEpoch = undefined; this.changed();}
    }
    return restored && epoch === this.startEpoch ? {fence: restored, epoch} : undefined;
  };

  /** The native surface is an exclusive attachment; a second mounted Surface is rejected. */
  attachSurface(owner: object): () => void {
    if (this.surfaceOwner && this.surfaceOwner !== owner) throw new Error('Playback surface is already mounted.');
    this.surfaceOwner = owner; this.connect();
    return () => {if (this.surfaceOwner === owner) {this.surfaceOwner = undefined; queueMicrotask(() => {if (!this.surfaceOwner) this.dispose();});}};
  }
  /** The runtime's own lifetime: a Try again belongs to it, not to a page that may be gone. */
  private retryLifetime(): Readonly<{signal: AbortSignal; isCurrent: () => boolean}> {
    return {signal: this.startLife.signal, isCurrent: () => !this.retired && this.currentAuthority()};
  }

  dispose = (): void => {
    this.startLife.abort(); this.startWatch.dispose();
    if (this.retired) return;
    const state = this.playback.getSnapshot();
    // An ordinary shell unmount stops its exact server session at the settled position. On an
    // authority change, no request is sent with the next viewer's credentials: the old lease
    // expires server-side, while the engine and every old request are torn down immediately.
    if (state.session && !this.playback.hasNativeOwner() && this.authorityMatches()) {
      this.stopOwnedSession(state.session.id, Math.max(0, Math.round(state.positionSeconds * 1000)));
    }
    this.beginOperation(); this.retired = true; ++this.sourceEpoch; this.external.retire();
    this.startController?.abort(); this.sourceController?.abort();
    clearForcedImage({getForcedImage: () => this.forcedImage, setForcedImage: read => {this.forcedImage = read;}});
    this.http.dispose(); this.videoPolicy.dispose();
    const identity = this.snapshot.fence?.playback; if (identity) this.choices.stop(identity); this.subtitleStyles.clear();
    this.recovery.clear(); this.audioTrackSelection.clear(); this.localTracks = undefined;
    this.subtitleOff?.(); this.subtitleService?.stop(); this.subtitleService = undefined;
    this.detailOff?.(); this.detailOff = undefined;
    this.detail?.service.dispose(); this.detail = undefined;
    for (const off of this.offs.splice(0).reverse()) off();
    this.coordinator.stop(); this.queue.dispose(); this.playback.leave(); this.path.dispose(); this.offline?.dispose();
    void this.o.nativeListening?.detach(true).catch(() => {});
    this.o.disposeCapabilities();
    this.dock.reset(this.scope); this.bookmarks.clear(); this.entry = undefined; this.subject = undefined; this.options = undefined;
    this.publish(); this.listeners.clear();
  };
}
