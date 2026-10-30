import {AppState, NativeModules, type View} from 'react-native';
import type {RefObject} from 'react';
import {startFailureHttp, type NativePlaybackStartFailure} from './start-failure';
import {capacityRetryHttp, type NativePlaybackCapacityWait} from './capacity-retry';
import {createNativeListeningReadPort} from './listening-controls';
import {PlaybackService, type PlaybackSession} from '../../../../../packages/client-core/src/index';
import {DetailService, type DetailProjection} from '../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {WholeGroupQuery} from '../../../../../packages/client-core/src/presentation/browse-groups';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import {encodedRoutePath, routePath} from '../../../../../packages/contracts/src/routes';
import {SessionsClient} from '../../../../../packages/client-core/src/playback-v1/sessions';
import {localV1Http} from '../../../../../packages/client-core/src/playback-v1/local-http';
import {call, type V1Request, type V1Response} from '../../../../../packages/client-core/src/playback-v1/http';
import {PlaybackOptionsClient} from '../../../../../packages/client-core/src/playback-v1/options';
import {V1QueuePlayer, serverSupportsQueueTransitions, queueVideoKind, type V1QueuedEntrySelection, type V1QueueEditIntent, type V1QueuePolicyIntent} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {QueueClient} from '../../../../../packages/client-core/src/playback-v1/queue';
import {V1ChannelControl, serverSupportsV1Channels} from '../../../../../packages/client-core/src/playback-v1/channel-control';
import {channelSeconds, type ChannelReference} from '../../../../../packages/client-core/src/playback/linear-protocol';
import {fetchGuideSourceSummaries} from '../../../../../packages/client-core/src/guide/directory';
import {legacyChannel} from '../../../../../packages/client-core/src/guide/legacy-source';
import type {HomeChannel} from '../../../../../packages/client-core/src/home';
import type {HomeSelectionPlaybackPort, HomeSelectionPlaybackRequest} from '../../../../../packages/client-core/src/home-selection/types';
import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model/index';
import {obj, parseDiscTitleRef, parseQueue, parseSession, type DiscTitleRef, type Session, type PlaybackOptions, type QueueHeader} from '../../../../../packages/client-core/src/playback-v1/types';
import type {PlaybackChoices} from '../../../../../packages/client-core/src/playback-v1/choices';
import type {SegmentInput} from '../../../../../packages/client-core/src/playback-v1/queue';
import {qualityOptionRequest, type QualityOption} from '../../../../../packages/client-core/src/playback-v1/quality';
import {SubtitleService} from '../../../../../packages/client-core/src/subtitles';
import {forcedChoiceOf, parseSubtitleLines} from '../../../../../packages/client-core/src/subtitle-originals';
import {forcedImageLinesPath} from './forced-image';
import type {PlaybackSubject, CardSubject} from '../../../../../packages/client-core/src/presentation/kit-model';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {PlaybackDock, PlayerSessionChoices, parseQueueSnapshot, playbackFence, sameFence, sameScope, type ChannelPlaybackFence, type QueuePlaybackFence, type DockPlayback, type DockTransition, type PlaybackFence, type QueueScope, type RemoteDock} from '../../../../../packages/player-core/src';
import type {PolicyCommand} from '../../../../../packages/player-core/src/playback-policy';
import {NativePlaybackCoordinator, type NativeBinding} from './NativePlaybackCoordinator';
import {nativeExternalPresentationBinding, sameNativePresentation} from './external-presentation';
import {NativePlayback, automaticSteps} from './native-playback';
import {PlaybackScopeHttp} from './scope-http';
import {captureNativePreparingOwner, nativePreparingStarts} from './preparing-owner';
import type {NativeStartRegistration} from './preparing-start';
import {captureNativeStartPresentation, nativeFirstVideoFrame, type NativeStartPresentationOwner, type NativeFirstVideoFrame} from './start-presentation';
import {NativeOfflinePlayback, type NativeOfflineSource} from './offline-playback';
import type {OfflineEntry} from '../../../platform/offline-downloads';
import {captionStyle} from './caption-style';
import {NativeSubtitleStyleChoices} from './subtitle-style-choices';
import {nativeStartSeconds} from './start-intent';
import {NativeVideoPlaybackPolicy, nativeVideoPolicyMarkers, nativeVideoPolicyPostPlay, type NativeVideoPolicyFrame} from './video-policy';
import {boundSubtitleControls, type NativePlaybackSubtitleControls} from './subtitle-controls';
import {uploadPickedSubtitle} from './subtitle-file-upload';
import {deviceDrawnTrack, engineSubtitleRender, externalSubtitlePreparation, fontsUrlOf, linesUrlOf, parseSubtitleFonts, type SubtitleFonts} from './engine-media';
import {atNaturalEnd, classifyPlaybackFailure} from './playback-failure';
import {mediaRecovered, NativePlaybackRecovery} from './playback-recovery';
import {directAudioStreamIndex, NativeAudioTrackSelection} from './audio-track-selection';
import {NativeOfflineTracks} from './offline-tracks';
import {nativeListeningFor, serverPlaybackV1Kinds} from './native-listening-kinds';
import {selectionSegments, selectionStartSeconds} from './selection';
import type {VideoSnapshotCapture, VideoSnapshotSource} from '../video-snapshot';
import {retainNativeVideoExitImage} from './video-exit-permission';
import type {NativeExternalPresentationLifetime, NativePictureInPictureState, NativePlaybackReadLifetime, NativeListeningReadPort, NativeOfflinePlaybackLifetime, NativePlaybackStart, NativePlaybackChannelStart, NativePlaybackVersionSelection, NativePlaybackRuntimeOptions, NativePlaybackRuntimeSnapshot, NativePlaybackVideoSnapshot, NativePlaybackStarting, NativePlaybackStartTimedOut, NativeStartDisplay, PlaybackAuthority} from './types';

/** No credentials: exact admission principal generation for one original start. */
const startupPrincipal = (session: import('../../../../../packages/client-core/src/index').LocalSession | undefined) => session
  ? JSON.stringify([session.sessionFamilyId, session.tokenGeneration, session.authorizationHorizon, session.deviceId ?? null, session.installationId ?? null]) : undefined;
const authorityKey = (value: PlaybackAuthority) => JSON.stringify([value.serverId, value.accountId, value.profileId, value.epoch, value.privacyFence ?? value.viewerFence]);
/** The retained binding a native view reports, compared field by field. */
const sameBindingFact = (fact: unknown, binding: NativeBinding): boolean => {
  if (fact === null || typeof fact !== 'object' || Array.isArray(fact)) return false;
  const raw = fact as Record<string, unknown>;
  return raw.lease === binding.lease && raw.intentId === binding.intentId && raw.sessionId === binding.sessionId
    && raw.generation === binding.generation && raw.itemId === binding.itemId;
};
const wireId = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.-]{1,128}$/.test(value);
const audioKind = (kind: string | undefined) => kind === 'song' || kind === 'track' || kind === 'audiobook_file' || kind === 'audiobook';
const kindOf = (kind: string | undefined): 'video' | 'music' | 'audiobook' => kind === 'song' || kind === 'track' ? 'music' : kind === 'audiobook_file' || kind === 'audiobook' ? 'audiobook' : 'video';
const offlineContentKind = (kind: string | undefined) => kind === 'movie' || kind === 'episode' || kind === 'song' || kind === 'audiobook_file' ? kind : undefined;
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
  responseFence?: string;
};

/** Actual session, queue and native-engine wiring. Construct one per selected authority.
 * The production factory in native.tsx supplies the existing native modules and one view. */
const entryDisplay = (entry: ContentEntry): NativeStartDisplay => Object.freeze({title: entry.title, ...(entry.posterUrl ?? entry.backdropUrl ? {posterUrl: entry.posterUrl ?? entry.backdropUrl} : {})});
/** O-159: something (the starting dock or the real dock) is drawn within this time of Play. */
export const NATIVE_START_DRAWN_MS = 5000;
/** O-159: the start deadline, as web's START_DEADLINE_MS. From Play, the real dock or player is
 * drawn within this time, accepted or not; otherwise "Couldn't start playback". */
export const NATIVE_START_DEADLINE_MS = 20_000;

export class NativePlaybackRuntime {
  readonly playback: PlaybackService;
  readonly path: NativePlayback;
  readonly queue: V1QueuePlayer;
  readonly coordinator: NativePlaybackCoordinator;
  readonly dock: PlaybackDock;
  readonly choices = new PlayerSessionChoices();
  private readonly subtitleStyles = new NativeSubtitleStyleChoices();
  private readonly videoPolicy: NativeVideoPlaybackPolicy;
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
  private startPrincipal?: string;
  private readonly startLife = new AbortController();
  private startFailure?: NativePlaybackStartFailure;
  // O-159: the drawn start (the pressed card's facts, Stop only), its 5 s drawn check and its 20 s deadline.
  private startingState?: {key: string; display?: NativeStartDisplay; accepted: boolean; timer: unknown; drawnTimer: unknown; deadlineFrom: 'play' | 'none'; stop: () => void; retry: () => Promise<void>; view?: NativePlaybackStarting; fence?: PlaybackFence};
  private startTimedOut?: NativePlaybackStartTimedOut;
  private startingKeys = 0;
  private capacityWait?: NativePlaybackCapacityWait;
  private versionMenuEpoch = 0;
  private startController?: AbortController;
  private offlineLifetimeOff?: () => void;
  private selectionControllers = new Set<AbortController>();
  private enqueueTail: Promise<unknown> = Promise.resolve();
  private channelControl?: V1ChannelControl;
  private channelCleanup?: () => void;
  private channel?: HomeChannel;
  private channelGuide?: NativePlaybackChannelStart['guide'];
  private sourceEpoch = 0;
  private sourceController?: AbortController;
  private sourceKey = '';
  private subtitleService?: SubtitleService;
  private subtitleOff?: () => void;
  private subtitleFileController?: AbortController;
  private externalPresentationBinding?: NativeBinding | null;
  private externalController?: AbortController;
  private externalEpoch = 0;
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
  private fonts?: SubtitleFonts | 'pending';
  private fontsKey?: string;
  private forcedImage?: {fence: PlaybackFence; path: string; epoch: number; controller: AbortController; mixed?: boolean};
  private detail?: SourceDetail;
  private detailOff?: () => void;
  private entry?: ContentEntry;
  private subject?: PlaybackSubject;
  private options?: PlaybackOptions;
  private bookmarks = new Map<string, Bookmark>();
  private updating = false;
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
  private supportedRates: readonly number[] = [];
  private surfaceOwner?: object;
  private surfaceTag?: number;
  private surfaceTagOwner?: object;
  private snapshotCapture?: VideoSnapshotCapture;
  private captureFence?: PlaybackFence;
  private captureSource?: VideoSnapshotSource;
  private readonly videoExitImages = new Set<() => Promise<void>>();
  private captureEpoch = 0;
  private readonly recovery: NativePlaybackRecovery;
  private readonly audioTrackSelection = new NativeAudioTrackSelection();
  private audioTrackInstance?: string;
  private cleanupSessions = new Set<string>();
  private initialPresentation?: NativeStartPresentationOwner;
  private initialPresentationTarget?: {owner: NativeStartPresentationOwner; epoch: number; itemId: string; session?: Session};
  private firstVideoFrame?: NativeFirstVideoFrame;

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
      const off = AppState.addEventListener('change', state => {if (state === 'active') listener();});return () => off.remove();
    }, onChange(listener: () => void) {
      const off = AppState.addEventListener('change', () => listener());return () => off.remove();
    }};
    const captureCurrent = (request: V1Request) => {
      const epoch = this.startEpoch, context = this.startContext, guard = this.http.capture(request.signal);
      if (!context || !request.signal || !this.startAdmission) return;
      return () => this.currentAuthority() && this.startOperationCurrent(epoch) && !request.signal!.aborted && context() && guard?.() !== false;
    };
    const capacity = capacityRetryHttp({http: originalHttp, life: this.startLife.signal, captureCurrent,
      isCurrent: () => this.currentAuthority(), timers: options.timers, foreground: () => !visibility.hidden(), onVisible: visibility.onVisible,
      publish: wait => {this.capacityWait = wait;if (this.snapshot) this.changed();}});
    const startHttp = options.startFailurePresentation ? startFailureHttp({http: capacity, api: options.api, events: options.events,
      life: this.startLife.signal, visibility, captureCurrent, isCurrent: () => this.currentAuthority(), isInitial: () => this.startAdmission,
      subscribeCurrent: listener => {const offs = [options.subscribeAuthority(listener), options.subscribeStartContext?.(listener)];return () => offs.forEach(off => off?.());},
      publish: failure => {this.startFailure = failure;if (this.snapshot) this.changed();},
      cancelStart: () => {this.beginOperation();this.changed();},
    }) : originalHttp;
    this.optionsClient = new PlaybackOptionsClient(this.http);
    this.path = new NativePlayback({http: startHttp, key: options.requestId, timers: options.timers, preparingStarts: this.preparingStarts,
      offeredStart: session => {
        const original = this.initialPresentationTarget;
        if (original && original.owner === this.initialPresentation && this.startAdmission && this.startOperationCurrent(original.epoch)
          && session.kind === 'vod' && session.role === 'local' && session.itemId === original.itemId && session.queue) original.session = session;
      },
      captureStartCurrent: () => {
        const epoch = this.startEpoch, principal = startupPrincipal(this.o.session()), initial = this.startAdmission, original = this.startContext;
        // The queue's own later start has its current core context, not the
        // route that originally opened playback. Initial user Play retains it.
        const actor = () => this.currentAuthority() && epoch === this.startEpoch && principal !== undefined && startupPrincipal(this.o.session()) === principal;
        return () => actor() && (!initial || this.startContext === original && original?.() !== false) && actor()
          && (!initial || this.startContext === original);
      }});
    if (options.offlineDevice) this.offline = new NativeOfflinePlayback(options.session, options.offlineDevice);
    this.playback = new PlaybackService(this.offline?.wrap(this.path.transport) ?? this.path.transport, options.requestId, {queueRequired: true, deferRecovery: true, pageVisibility: visibility});
    this.queue = new V1QueuePlayer({http: startHttp, api: this.path.transport, playback: this.playback, viewer: this.scope, key: options.requestId, events: options.events,
      transitions: () => this.transitions, native: options.nativeListening, nativeFor: kind => nativeListeningFor(this.serverKinds)(kind),
      retireQueuedSelection: (session, isUnowned) => this.stopOwnedSession(session.id, undefined, () => isUnowned()
        && this.playback.getSnapshot().session?.id !== session.id && this.path.controller.getSnapshot().session?.id !== session.id),
      audiobookPreferences: () => this.o.settings().audiobook ?? {skipBackMs:15000,skipForwardMs:30000,resumeRewindMs:0},
      retainQueueIds: () => this.retainedQueueIds()});
    // The shared video policy owns the held end card. Listening keeps its owner.
    this.queue.setCompletionPolicy(({kind}) => audioKind(kind) ? 'advance' : 'hold');
    this.dock = new PlaybackDock(this.scope, this.offline?.owner());
    this.coordinator = new NativePlaybackCoordinator(this.playback, options.transport, options.requestId,
      state => {
        if (!state.session) return '';
        if (state.session.mode === 'local') {
          const url = this.offline?.mediaUrl(state.session);
          if (url) return url;
          if (state.phase !== 'error') this.playback.fail(state.intentId, serviceText('downloads.offline.notPlayableHere')); return '';
        }
        try { return options.api.mediaUrl(state.session.streamUrl); }
        catch { this.playback.fail(state.intentId, serviceText('queuePlayer.startFailed')); return ''; }
      }, fact => this.acceptFact(fact), options.engines);
    this.videoPolicy = new NativeVideoPlaybackPolicy({
      read: () => this.videoPolicyFrame(), current: fence => this.videoPolicyCurrent(fence), execute: command => this.executeVideoPolicy(command),
      changed: () => this.publish(), failed: (fence, _error) => {if (this.videoPolicyCurrent(fence)) this.playback.fail(this.playback.getSnapshot().intentId, serviceText('queuePlayer.startFailed'));},
      now: options.timers?.now ?? (() => Math.floor(performance.now())),
      setTimer: options.timers?.setTimer ?? ((run, ms) => setTimeout(run, ms)),
      clearTimer: options.timers?.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>)),
    });
    this.snapshot = this.makeSnapshot(0);
  }
  getSnapshot = () => this.snapshot;
  /** File format facts only for this exact admitted local session. */
  localContainer(session: PlaybackSession): string | undefined {
    return this.offline?.mediaUrl(session) ? this.offlineSource?.media.extras?.bundle?.media.container : undefined;
  }
  localSubtitleRendering(): string | undefined {return this.playback.getSnapshot().session?.mode === 'local' ? this.localTracks?.requiredRendering() : undefined;}
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private authorityMatches(): boolean {
    const session = this.o.session(), viewer = session?.viewer;
    return session?.sessionFamilyId === this.sessionFamilyId && authorityKey(this.o.authority()) === authorityKey(this.bound) && viewer?.serverId === this.scope?.serverId && viewer.accountId === this.scope.accountId && viewer.profileId === this.scope.profileId && viewer.authority === this.scope.authority;
  }
  private retiredStart(request: V1Request, response: V1Response): void {
    this.preparingStarts.retired(request, response);
    if (request.method !== 'POST' || ![200, 201].includes(response.status) || !this.authorityMatches()) return;
    let session: Session;
    if (request.path === routePath('create_queue')) {
      // A combined queue create can also return a real session after its caller
      // retires. Scope HTTP rejects that response before the core can abandon it.
      if (!obj(request.body) || request.body.owner !== 'device' || !obj(request.body.startPlayback) || !obj(response.body)) return;
      try {
        const queue = parseQueue(response.body.queue);
        session = parseSession(response.body.session);
        if (session.role !== 'local' || session.queue?.queueId !== queue.id || !queue.current || session.queue.entryId !== queue.current.entryId) return;
      } catch {return;}
    } else if (request.path === routePath('start_playback_session')) {
      try {session = parseSession(response.body);} catch {return;}
      const body = request.body as {itemId?: unknown; queue?: {queueId?: unknown; entryId?: unknown}} | undefined;
      if (!body || !body.queue && typeof body.itemId !== 'string') return;
      if (body.queue ? session.queue?.queueId !== body.queue.queueId || session.queue?.entryId !== body.queue.entryId : session.itemId !== body.itemId) return;
    } else if (obj(request.body) && request.body.reason === 'entry' && typeof request.body.entryId === 'string' && obj(response.body)) {
      // Scope HTTP owns a late advance response before core can observe its admitted session.
      try {
        const queue = parseQueue(response.body.queue);
        session = parseSession(response.body.session);
        const revision = Object.entries(request.headers ?? {}).find(([key]) => key.toLowerCase() === 'if-match')?.[1];
        if (!revision || queue.revision === revision || request.path !== encodedRoutePath('advance_queue', {id: encodeURIComponent(queue.id)})
          || session.role !== 'local' || session.queue?.queueId !== queue.id || session.queue.entryId !== request.body.entryId
          || queue.current?.entryId !== request.body.entryId) return;
      } catch {return;}
    } else return;
    if (this.playback.getSnapshot().session?.id === session.id) return;
    this.stopOwnedSession(session.id);
  }
  private async stopOwnedSession(id: string, positionMs?: number, isUnowned: () => boolean = () => true): Promise<boolean> {
    const current = () => {try {return this.authorityMatches() && isUnowned();} catch {return false;}};
    if (!current() || this.cleanupSessions.has(id)) return false;
    this.cleanupSessions.add(id);
    while (this.cleanupSessions.size > 32) this.cleanupSessions.delete(this.cleanupSessions.values().next().value!);
    const cleanup = new PlaybackScopeHttp(localV1Http(this.o.api), current);
    const off = this.o.subscribeAuthority(() => { if (!current()) cleanup.dispose(); });
    try {
      await new SessionsClient(cleanup, this.o.timers).stop(id, positionMs);
      return current();
    } catch {return false;}
    finally {off(); cleanup.dispose();}
  }
  private currentAuthority(): boolean { return !this.retired && this.authorityMatches(); }
  private active(): boolean { if (!this.currentAuthority()) { this.dispose(); return false; } return true; }
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
    this.offs.push(this.coordinator.subscribe(() => { if (this.captureSource && !this.isSnapshotSourceCurrent(this.captureSource)) this.cancelSnapshot(); this.applyOptions(); this.publish(); }));
    this.offs.push(this.o.subscribeAuthority(() => { if (!this.active()) return; void this.refreshAuthority(); }));
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
    this.offs.push(this.o.subscribeSettings(() => { if (this.active()) { adoptAudioEffects(); this.queue.listening.refreshAudiobookPreferences(); this.applyOptions(); this.coordinator.reconsiderEngine(); this.publish(); } }));
    this.offs.push(this.choices.subscribe(() => {
      // Source binding resets these choices while changed() is still selecting
      // the next source. That path applies once after the new source is ready.
      if (this.updating || this.transitionEpoch !== undefined || !this.active()) return;
      this.applyOptions(); this.publish();
    }));
    this.offs.push(this.o.api.subscribeRoute(() => this.routeChanged()));
    this.offs.push(this.o.events.on('session.updated', event => { if (this.active() && event.resource?.kind === 'session') this.path.transport.sessionUpdated(event.resource.id, event.revision, event.data); }));
    if (this.o.events.onResyncRead) {
      // The queue registers its own bounded recovery (including native terminal
      // authority). Keep this runtime's detail read in the same dispatcher.
      const recovery = this.o.events.onResyncRead(async context => {
        if (this.active() && !context.signal.aborted) await this.detail?.service.refresh();
      }, {priority: 1});
      this.offs.push(() => recovery.dispose());
    } else this.offs.push(this.o.events.onResync(() => { if (this.active()) { void this.queue.refresh().catch(() => {}); void this.refreshAuthority(); } }));
    this.routeChanged();
    this.coordinator.start();
    void this.playback.startRecovery();
    this.changed();
  }
  private routeChanged(): void {
    if (!this.active()) return;
    const route = this.o.api.getRouteSnapshot();
    if (route && route.phase !== 'ready') {this.cancelSnapshot(); this.externalPresentationBinding = null; this.pictureBinding = undefined; this.pictureAttempt = undefined; this.pictureLeave = undefined; this.pictureState = 'idle'; this.externalController?.abort();}
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
    this.startPrincipal = startupPrincipal(this.o.session());
    this.startFailure = undefined;this.capacityWait = undefined;this.startAdmission = false;
    this.externalController?.abort(); this.pictureBinding = undefined; this.pictureAttempt = undefined; this.pictureLeave = undefined; this.pictureState = 'idle';
    this.offlineLifetimeOff?.(); this.offlineLifetimeOff = undefined;
    this.startController?.abort();
    for (const controller of [...this.selectionControllers]) {
      if (controller.signal === retainScope) continue;
      this.selectionControllers.delete(controller);
      controller.abort();
    }
    this.channelControl?.close(); this.channelControl = undefined;
    this.channelCleanup?.(); this.channelCleanup = undefined;
    this.channel = undefined;
    this.channelGuide = undefined;
    this.transitionEpoch = undefined; this.preparing = false;
    this.expandAtStart = undefined;
    return epoch;
  }

  private startOperationCurrent(epoch: number): boolean {
    return epoch === this.startEpoch && this.startPrincipal !== undefined && startupPrincipal(this.o.session()) === this.startPrincipal;
  }

  /** The same exact Home document/route guard is supplied to its selection controller. */
  selectionPlayback = (isCurrent: (context: SelectionContext) => boolean): HomeSelectionPlaybackPort => ({
    play: request => this.playSelection(request, isCurrent),
    enqueue: request => {
      const epoch = this.startEpoch, source = this.sourceEpoch;
      const result = this.enqueueTail.then(() => this.enqueueSelection(request, isCurrent, epoch, source));
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
    return {signal: controller.signal, close: () => { unguard(); signal.removeEventListener('abort', abort); this.selectionControllers.delete(controller); controller.abort(); }};
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
    return this.watchedStart(pressed ? entryDisplay(pressed) : undefined, async () => {await this.playSelection({...request, signal: this.startLife.signal}, current);},
      () => this.playSelectionNow(request, current));
  }
  private async playSelectionNow(request: HomeSelectionPlaybackRequest, current: (context: SelectionContext) => boolean) {
    this.assertSelection(request.context, request.signal, current);
    const segments = selectionSegments(request.items, true), epoch = this.beginOperation();
    const valid = () => this.startOperationCurrent(epoch) && this.selectionCurrent(request.context, request.signal, current);
    const scoped = this.selectionScope(request.signal, valid);
    try {
      this.connect();
      await this.queue.cancel();
      this.assertSelection(request.context, scoped.signal, current); if (!valid()) throw new Error('stale_selection_context');
      await this.o.capabilitiesReady();
      this.assertSelection(request.context, scoped.signal, current); if (!valid()) throw new Error('stale_selection_context');
      const capabilities = await this.request<unknown>(routePath('get_capabilities'), 'GET', undefined, scoped.signal);
      if (!valid()) throw new Error('stale_selection_context');
      this.serverKinds = serverPlaybackV1Kinds(capabilities); this.transitions = serverSupportsQueueTransitions(capabilities);
      this.rememberCurrent();
      const first = request.items[0]!;
      this.entry = 'entry' in first && first.entry.id === first.target.itemId ? first.entry : undefined;
      this.subject = this.entry ? subjectOf(this.entry) : undefined; this.options = undefined;
      this.expanded = this.entry ? !audioKind(this.entry.kind) : true;
      this.expandAtStart = epoch;
      this.preparing = true;this.startAdmission = true;
      // Only the canonical core owns the create/adopt path and its pre-adoption context fence.
      const queue = await this.queue.playSegments(segments, {startSeconds: selectionStartSeconds(request.items), signal: scoped.signal, isCurrent: valid});
      if (!valid()) throw new Error('stale_selection_context');
      return {queue};
    } catch (error) {
      if (this.startOperationCurrent(epoch) && this.currentAuthority()) { await this.queue.cancel(); this.playback.leave(); this.changed(); }
      throw error;
    } finally {
      scoped.close();
      if (epoch === this.startEpoch) { this.preparing = false;this.startAdmission = false; this.changed(); }
    }
  }
  private async enqueueSelection(request: HomeSelectionPlaybackRequest & {placement: 'next' | 'end'}, current: (context: SelectionContext) => boolean, epoch: number, source: number) {
    this.assertSelection(request.context, request.signal, current);
    if (request.placement !== 'next' && request.placement !== 'end') throw new Error('invalid_queue_placement');
    const segments = selectionSegments(request.items, false);
    const valid = () => epoch === this.startEpoch && source === this.sourceEpoch && this.selectionCurrent(request.context, request.signal, current);
    if (!valid()) throw new Error('stale_selection_context');
    const scoped = this.selectionScope(request.signal, valid);
    const client = new QueueClient({send: request => this.http.send({...request, signal: scoped.signal})}, this.o.requestId);
    const check = () => { if (!valid()) throw new Error('stale_selection_context'); };
    try {
      this.connect(); check();
      const held = this.queue.getSnapshot().view?.queue;
      if (!held) {
        const created = await client.create(segments, {signal: scoped.signal, retainQueueIds: this.retainedQueueIds()}); check();
        await this.queue.open(created.queue.id, scoped.signal); check();
        this.changed(); return {queue: created.queue};
      }
      let queue = await client.header(held.id, scoped.signal); check();
      const ordered = request.placement === 'next' ? [...segments].reverse() : segments;
      for (const segment of ordered) {
        check(); queue = (await client.addSegment(queue.id, queue.revision, request.placement, segment)).queue; check();
      }
      await this.queue.refresh(scoped.signal); check();
      this.changed(); return {queue};
    } finally { scoped.close(); }
  }

  /** O-159: a Home channel row's Play is drawn at once as that channel. */
  private playChannel(channel: HomeChannel, context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): Promise<void> {
    const name = channel.channel.name, logo = (channel.channel as {logoPath?: string}).logoPath;
    return this.watchedStart(name ? {title: name, ...(logo ? {posterUrl: logo} : {})} : undefined,
      () => this.playChannel(channel, context, this.startLife.signal, current), () => this.playChannelNow(channel, context, signal, current), 'none');
  }
  private async playChannelNow(channel: HomeChannel, context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): Promise<void> {
    this.assertSelection(context, signal, current);
    const value = channel.channel, source = channel.source;
    if (!value.tuneAvailable || value.tuneUnavailableReason || value.hidden || value.sourceId !== source.id || value.generation !== source.generation || value.provenance !== source.provenance || !['live-source', 'library-channel'].includes(value.provenance) || !wireId(source.generation) || !wireId(source.id) || !wireId(value.id) || value.provenance === 'library-channel' && source.id !== value.id) throw new Error('invalid_channel_target');
    const epoch = this.beginOperation();
    const valid = () => epoch === this.startEpoch && this.selectionCurrent(context, signal, current);
    const scoped = this.selectionScope(signal, valid);
    try {
      this.connect(); await this.queue.cancel();
      if (!valid()) throw new Error('stale_selection_context');
      await this.o.capabilitiesReady();
      if (!valid()) throw new Error('stale_selection_context');
      const capabilities = await this.request<unknown>(routePath('get_capabilities'), 'GET', undefined, scoped.signal);
      if (!valid()) throw new Error('stale_selection_context');
      if (!serverSupportsV1Channels(capabilities)) throw new Error('channel_playback_unavailable');
      this.rememberCurrent(); this.channel = channel; this.expanded = true;
      await this.openChannel({kind: value.provenance, sourceId: source.id, channelId: value.id, generation: value.generation}, epoch, scoped.signal, valid, 'playing');
      this.changed();
    } catch (error) {
      if (epoch === this.startEpoch && this.currentAuthority()) { this.playback.leave(); this.changed(); }
      throw error;
    } finally { scoped.close(); }
  }

  /** Route/Guide playback uses the same installed channel owner. The source's
   * generation/provenance is re-read; only the real channel POST grants tuning. */
  /** O-159: drawn at once as the guide row's channel, then its real dock. */
  startChannel = (input: NativePlaybackChannelStart): Promise<void> => {
    const row = input.guide && legacyChannel(input.guide.row);
    return this.watchedStart(row?.name ? {title: row.name, ...(row.logoPath ? {posterUrl: row.logoPath} : {})} : undefined,
      () => this.startChannel({...input, lifetime: this.retryLifetime(), onAccepted: undefined, expectedFence: undefined}), () => this.startChannelNow(input), 'none');
  };
  private startChannelNow = async (input: NativePlaybackChannelStart): Promise<void> => {
    const accepted = input.onAccepted;
    // These are this invocation's values even if its caller later reuses a DTO.
    input = Object.freeze({...input, lifetime: Object.freeze({...input.lifetime})});
    if (!wireId(input.sourceId) || !wireId(input.channelId) || !input.timezone || input.lifetime.signal.aborted || !input.lifetime.isCurrent() || !this.currentAuthority()) return;
    const expected = input.expectedFence && playbackFence(input.expectedFence);
    if (expected && !sameFence(this.snapshot.fence, expected)) return;
    // A zap's preflight still borrows the old output. Taking the operation below
    // closes that output itself, so only the owned epoch guards actual admission.
    let dispatching = !expected, epoch = expected ? this.startEpoch : this.beginOperation();
    const principal = startupPrincipal(this.o.session());
    const current = () => this.currentAuthority() && principal !== undefined && startupPrincipal(this.o.session()) === principal
      && epoch === this.startEpoch && !input.lifetime.signal.aborted && input.lifetime.isCurrent()
      && (dispatching ? this.startOperationCurrent(epoch) : !!expected && sameFence(this.snapshot.fence, expected));
    const scoped = this.selectionScope(input.lifetime.signal, current);
    try {
      this.connect(); if (!expected) await this.queue.cancel(); if (!current()) return;
      await this.o.capabilitiesReady(); if (!current()) return;
      const [capabilities, directory] = await Promise.all([
        this.request<unknown>(routePath('get_capabilities'), 'GET', undefined, scoped.signal),
        fetchGuideSourceSummaries({request: (path, method, body, signal) => this.request(path, method, body, signal)}, this.bound.serverId, input.timezone, scoped.signal),
      ]);
      if (!current()) return;
      if (!serverSupportsV1Channels(capabilities)) throw new Error('channel_playback_unavailable');
      const sources = directory.sources.filter(source => source.id === input.sourceId);
      if (sources.length !== 1 || !wireId(sources[0]!.generation)) throw new Error('channel_source_unavailable');
      const source = sources[0]!;
      if (input.expectedSourceGeneration !== undefined && source.generation !== input.expectedSourceGeneration) throw new Error('channel_source_unavailable');
      const origin = input.guide, row = origin && legacyChannel(origin.row);
      if (origin && (!row || row.id !== input.channelId || row.sourceId !== source.id || row.provenance !== source.provenance
        || row.generation !== source.generation || !row.tuneAvailable || origin.options.timezone !== input.timezone)) throw new Error('channel_source_unavailable');
      if (source.provenance === 'library-channel' && input.sourceId !== input.channelId) throw new Error('invalid_channel_target');
      if (expected) {
        if (!current()) return;
        epoch = this.beginOperation(scoped.signal);dispatching = true;
        await this.queue.cancel();if (!current()) return;
      }
      this.rememberCurrent(); this.channel = undefined; this.expanded = true;
      await this.openChannel({kind: source.provenance, sourceId: source.id, channelId: input.channelId, generation: source.generation}, epoch, scoped.signal, current, 'playing', accepted);
      if (current()) {this.channelGuide = origin ? Object.freeze({row: origin.row, options: Object.freeze({...origin.options})}) : undefined;this.changed();}
    } catch (error) {
      if (current()) {if (dispatching) {this.playback.leave(); this.changed();} throw error;}
    } finally {scoped.close();}
  };

  private async openChannel(reference: ChannelReference, epoch: number, signal: AbortSignal, beforeAdoption: () => boolean, state: 'playing' | 'paused', onAccepted?: (fence: ChannelPlaybackFence) => void): Promise<void> {
    let adopted = false, opening = false, notified = false;
    const valid = () => epoch === this.startEpoch && this.currentAuthority() && (adopted || !signal.aborted && beforeAdoption());
    const notifyAccepted = () => {
      if (!onAccepted || notified || !adopted) return;
      // Observed retirement is terminal for this notification, independently of
      // the accepted channel owner's ordinary playback lifetime.
      if (!valid() || !beforeAdoption()) {notified = true;return;}
      const fence = this.fence(), session = this.playback.getSnapshot().session;
      if (fence?.kind !== 'channel' || fence.channelKind !== reference.kind || fence.channelId !== reference.channelId
        || fence.sourceId !== reference.sourceId || fence.channelGeneration !== reference.generation
        || session?.id !== fence.sessionId || session.generation !== fence.generation) return;
      // Predicates may synchronously retire or replace an owner. Check the same
      // actual fence again, then latch before invoking the captured callback.
      if (!valid() || !beforeAdoption() || !sameFence(this.fence(), fence)) {notified = true;return;}
      notified = true;
      onAccepted(fence);
      if (!valid() || !beforeAdoption() || !sameFence(this.fence(), fence)) return;
    };
    const scoped = this.selectionScope(signal, valid);
    let control: V1ChannelControl | undefined;
    let rejectStart: ((error: Error) => void) | undefined;
    const abort = () => { if (!adopted) { control?.close(); rejectStart?.(new Error('cancelled')); } };
    signal.addEventListener('abort', abort, {once: true});
    const close = () => { signal.removeEventListener('abort', abort); control?.close(); scoped.close(); rejectStart?.(new Error('stale_selection_context')); rejectStart = undefined; };
    try {
      // DELETE cleanup stays bound to its original authority after a Home context expires.
      control = new V1ChannelControl({events: this.o.events, key: this.o.requestId, http: {send: async request => {
        if (request.method === 'DELETE') return this.http.send(request);
        if (!valid()) { control?.close(); throw new Error('stale_selection_context'); }
        const response = await this.http.send(adopted ? request : {...request, signal: scoped.signal});
        if (!valid()) control?.close();
        return response;
      }}});
      const channelControl = control;
      this.playback.useChannelPlayback({
        open: async (reference, sink, options) => {
          opening = true;
          // open() can return before its ordered join seek. Await the service's real session.
          let resolveStart!: () => void;
          const joined = new Promise<void>((resolve, reject) => { resolveStart = resolve; rejectStart = reject; });
          void joined.catch(() => {});
          await channelControl.open(reference, {
            state: value => {
              if (!valid()) { close(); return; }
              sink.state(value);
              if (!adopted && this.playback.getSnapshot().session?.id === value.playbackId) {
                adopted = true; scoped.close(); signal.removeEventListener('abort', abort); rejectStart = undefined; resolveStart();
              }
              notifyAccepted();
            },
            error: (message, terminal) => { if (valid()) sink.error(message, terminal); if (!adopted) { rejectStart?.(new Error('channel_playback_failed')); rejectStart = undefined; } },
          }, options);
          await joined;
        },
        recover: () => channelControl.recover(), pause: () => channelControl.pause(), resume: () => channelControl.resume(),
        seek: seconds => channelControl.seek(seconds), goLive: () => channelControl.goLive(), retry: () => channelControl.retry(),
        observe: (seconds, seekId, errorCode) => channelControl.observe(seconds, seekId, errorCode),
        close: () => { if (opening) close(); else channelControl.close(); },
      });
      this.channelControl = control; this.channelCleanup = close;
      this.entry = undefined; this.subject = undefined; this.options = undefined;
      await this.playback.playChannel(reference, {state});
      if (!valid()) throw new Error('stale_selection_context');
      if (this.playback.getSnapshot().phase === 'error' || !this.playback.getSnapshot().session) throw new Error('channel_playback_failed');
    } catch (error) { close(); throw error; }
  }

  start = async (input: NativePlaybackStart): Promise<void> => {
    const caller = input.lifetime;
    const callerCurrent = () => !caller?.signal.aborted && caller?.isCurrent() !== false;
    if (!callerCurrent()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
    this.connect();
    if (!this.active()) return;
    if (!wireId(input.target.itemId)) throw new Error('Invalid playback target.');
    nativeStartSeconds(input);
    const epoch = this.beginOperation();
    const controller = new AbortController(); this.startController = controller;
    // O-159: drawn at once from the pressed card; Try again belongs to this runtime, not the page.
    let stopped = false;
    const starting = this.beginStarting(entryDisplay(input.entry), () => this.start({...input, lifetime: undefined, presentation: undefined}));
    starting.stopWith(() => {stopped = true; controller.abort();});
    const abort = () => controller.abort();
    caller?.signal.addEventListener('abort', abort, {once: true});
    if (caller?.signal.aborted) abort();
    const current = () => this.active() && this.startOperationCurrent(epoch) && !controller.signal.aborted && callerCurrent();
    const scoped = this.selectionScope(controller.signal, current);
    // Paint retains the original page/actor, not the command scope that closes
    // on successful completion. It has no ability to start/restart playback.
    const originalContext = this.startContext, api = this.o.api, route = api.getRouteSnapshot(), principal = this.o.session();
    const connection = api.getRouteConnection(), pin = connection?.pin.publicKey, certificate = api.getCertificateSessionEpoch();
    const paintUntil = principal ? Math.min(Date.parse(principal.expiresAt), Date.parse(principal.authorizationHorizon)) : NaN;
    const paintCurrent = () => this.currentAuthority() && this.startOperationCurrent(epoch) && callerCurrent()
      && !!principal && Number.isFinite(paintUntil) && Date.now() < paintUntil && this.o.session()?.accessToken === principal.accessToken
      && this.o.session()?.expiresAt === principal.expiresAt && this.o.session()?.viewer.role === principal.viewer.role
      && api.getRouteConnection() === connection && !!pin && connection?.pin.publicKey === pin && api.getCertificateSessionEpoch() === certificate
      && this.startContext === originalContext && originalContext?.() !== false
      && this.o.api === api && (route ? api.getRouteSnapshot()?.phase === 'ready'
        && api.getRouteSnapshot()?.revision === route.revision && api.getRouteSnapshot()?.origin === route.origin : api.getRouteSnapshot() === null);
    const presentation = captureNativeStartPresentation(caller && originalContext ? input.presentation : undefined, {current: paintCurrent,
      sourceCurrent: original => sameFence(this.fence(), original) && !['error', 'ended'].includes(this.playback.getSnapshot().phase),
      changed: () => {if (this.snapshot && !this.retired) this.publish();},
      subscribe: listener => {
        const offs = [this.subscribe(listener), this.o.subscribeAuthority(listener), this.o.subscribeStartContext?.(listener), api.subscribeRoute(listener)];
        caller?.signal.addEventListener('abort', listener, {once: true});
        return () => {offs.forEach(off => off?.());caller?.signal.removeEventListener('abort', listener);};
      }});
    if (paintCurrent()) {
      this.initialPresentation = presentation;
      this.initialPresentationTarget = presentation ? {owner: presentation, epoch, itemId: input.target.itemId} : undefined;
    } else presentation?.retire();
    try {
      await this.queue.cancel(); if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
      await this.o.capabilitiesReady(); if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
      const [offered, capabilities] = await Promise.all([
        this.optionsClient.options(input.target.itemId, {}, scoped.signal),
        this.request<unknown>(routePath('get_capabilities'), 'GET', undefined, scoped.signal),
      ]);
      if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
      // A returning Cast title keeps the edition the receiver reported, when it is still offered.
      const versionId = input.versionId && offered.versions.filter(value => value.id === input.versionId).length === 1 ? input.versionId : undefined;
      await this.startOffered(input, offered, capabilities, epoch, versionId, {signal: scoped.signal, isCurrent: current});
      const accepted = this.fence(), session = this.currentSession();
      // Accepted: the watch now runs until this fence's real dock is drawn; Stop ends the session.
      starting.accepted(accepted);
      starting.stopWith(() => {stopped = true; controller.abort(); if (accepted) void this.stop(accepted).catch(() => {});});
      if (presentation && this.initialPresentation === presentation && current() && !audioKind(offered.kind)
        && accepted?.kind === 'queue' && accepted.itemId === input.target.itemId && session?.id === accepted.sessionId
        && session.role === 'local' && session.presentation.generation === accepted.generation && session.versionId === accepted.sourceId
        && session.queue?.entryId === accepted.entryId) presentation.bind(accepted);
      else if (this.initialPresentation === presentation) {this.initialPresentation = undefined;this.initialPresentationTarget = undefined;presentation?.retire();}
    } catch (error) {
      starting.end();
      if (this.initialPresentation === presentation) {this.initialPresentation = undefined;this.initialPresentationTarget = undefined;presentation?.retire();}
      if (stopped || !current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});throw error;
    }
    finally {
      caller?.signal.removeEventListener('abort', abort); scoped.close();
      if (this.startController === controller) this.startController = undefined;
    }
  };

  /** Opens the actual verified device file through this same player. No server
   * options, queue creation or source URL is supplied by the screen. */
  /** O-159: a download is drawn at once from its own title and kept cover. */
  startOffline = (entry: OfflineEntry, lifetime: NativeOfflinePlaybackLifetime): Promise<void> =>
    this.watchedStart({title: entry.title, ...(entry.artworkUrl ? {localArtwork: entry.artworkUrl} : {})},
      () => this.startOffline(entry, this.retryLifetime()), () => this.startOfflineNow(entry, lifetime));
  private startOfflineNow = async (entry: OfflineEntry, lifetime: NativeOfflinePlaybackLifetime): Promise<void> => {
    const admitted = () => !lifetime.signal.aborted && this.currentAuthority() && lifetime.isCurrent();
    const retired = () => Object.assign(new Error('cancelled'), {name: 'AbortError'});
    if (!admitted()) throw retired();
    if (!this.offline) throw new Error(serviceText('downloads.offline.notPlayableHere'));
    const epoch = this.beginOperation(), controller = new AbortController(); this.startController = controller;
    const abort = () => controller.abort();
    lifetime.signal.addEventListener('abort', abort, {once: true});
    const unfollow = () => lifetime.signal.removeEventListener('abort', abort); this.offlineLifetimeOff = unfollow;
    const current = () => epoch === this.startEpoch && !controller.signal.aborted && admitted();
    const scoped = this.selectionScope(controller.signal, current);
    let accepted = false;
    try {
      const source = await this.offline.prepare(entry, current);
      if (!current()) throw retired();
      if (!offlineContentKind(source.metadata.kind)) throw new Error(serviceText('downloads.offline.notPlayableHere'));
      this.rememberCurrent(); await this.queue.cancel();
      if (!current() || !source.current()) throw retired();
      this.bindOffline(source);
      this.expanded = !audioKind(source.metadata.kind);
      this.preparing = true;
      await this.playback.playLocal(source.itemId, () => this.makeOfflineSession(source), undefined, {signal: controller.signal, isCurrent: current});
      const session = this.playback.getSnapshot().session;
      accepted = !!session && this.offlineSource === source && !!this.offline.mediaUrl(session);
      if (!accepted) {
        if (!current()) throw retired();
        throw new Error(this.playback.getSnapshot().error || serviceText('downloads.offline.notPlayableHere'));
      }
    } catch (error) {
      if (!current()) throw retired();
      throw error;
    } finally {
      scoped.close();
      if (!accepted) {unfollow(); controller.abort(); if (this.offlineLifetimeOff === unfollow) this.offlineLifetimeOff = undefined;}
      if (epoch === this.startEpoch) {this.preparing = false; this.changed();}
    }
  };

  private bindOffline(source: NativeOfflineSource): void {
    const contentKind = offlineContentKind(source.metadata.kind);
    if (!contentKind) throw new Error(serviceText('downloads.offline.notPlayableHere'));
    this.offlineSource = source; this.entry = undefined; this.options = undefined;
    this.subject = {kind: kindOf(contentKind), item: {id: source.itemId, title: source.metadata.title, kind: contentKind}};
  }

  private makeOfflineSession(source: NativeOfflineSource): PlaybackSession {
    const session = source.session();
    // The real service has already assigned this play's run. Validate and bind
    // the full local identity before its session can reach the native engine.
    if (!this.offline?.fence(session, this.playback.getSnapshot().playbackRun)) throw new Error(serviceText('downloads.offline.notPlayableHere'));
    return session;
  }

  private readonly cataloguePort = Object.freeze({
    playSegments: (segments: readonly SegmentInput[], options: NonNullable<Parameters<V1QueuePlayer['playSegments']>[1]> = {}) => {
      // P0-PHONE-ALBUM-PLAYBACK proof: temporary __DEV__-only, sanitized call arguments
      // (container kind/id and order only). Removed after the proof run.
      if (__DEV__) console.log('[P0-ALBUM-PROOF]', new Date().toISOString(), JSON.stringify(segments.map(segment => ({
        source: {container: 'container' in segment.source ? {kind: segment.source.container.kind, id: segment.source.container.id} : null},
        order: segment.order ? {mode: segment.order.mode, ...(segment.order.mode === 'shuffle' && segment.order.seed ? {seed: segment.order.seed} : {})} : null,
      }))));
      return this.playCatalogueSegments(segments, options);
    },
    enqueueSegments: (segments: readonly SegmentInput[], placement: 'next' | 'end', lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => {
      // Capture the queue/source epochs before waiting behind another append: a call queued
      // for queue A must never adopt a source B that replaced it while it waited.
      const epoch = this.startEpoch, source = this.sourceEpoch;
      const result = this.enqueueTail.then(() => this.enqueueCatalogueSegments(segments, placement, lifetime, epoch, source));
      this.enqueueTail = result.catch(() => {});
      return result;
    },
  });
  /** Borrow the genuine authority-local player. This port has no Home context,
   * selection membership, page expansion or independent queue/session store. */
  cataloguePlayback = () => this.cataloguePort;

  /** O-159: an album, artist, playlist or radio start is named by its first entry once known. */
  private playCatalogueSegments(segments: readonly SegmentInput[], options: NonNullable<Parameters<V1QueuePlayer['playSegments']>[1]>): Promise<QueueHeader> {
    return this.watchedStart({icon: 'i-music'}, async () => {await this.playCatalogueSegments(segments, {...options, ...this.retryLifetime()});}, () => this.playCatalogueSegmentsNow(segments, options));
  }
  private async playCatalogueSegmentsNow(segments: readonly SegmentInput[], options: NonNullable<Parameters<V1QueuePlayer['playSegments']>[1]>): Promise<QueueHeader> {
    const {signal, isCurrent} = options;
    if (!signal || !isCurrent) throw new Error('native_catalogue_lifetime_required');
    if (!segments.length || options.startSeconds !== undefined && (!Number.isFinite(options.startSeconds) || options.startSeconds < 0 || options.startSeconds > 1e9)) throw new Error('native_catalogue_selection_invalid');
    const admitted = () => !signal.aborted && this.currentAuthority() && isCurrent();
    const retired = () => Object.assign(new Error('native_catalogue_retired'), {name: 'AbortError'});
    if (!admitted()) throw retired();
    const epoch = this.beginOperation(), valid = () => this.startOperationCurrent(epoch) && admitted();
    const scoped = this.selectionScope(signal, valid);
    const assertCurrent = () => {if (!valid()) throw retired();};
    try {
      this.connect();
      await this.queue.cancel(); assertCurrent();
      await this.o.capabilitiesReady(); assertCurrent();
      const capabilities = await this.request<unknown>(routePath('get_capabilities'), 'GET', undefined, scoped.signal); assertCurrent();
      this.serverKinds = serverPlaybackV1Kinds(capabilities); this.transitions = serverSupportsQueueTransitions(capabilities);
      this.rememberCurrent(); assertCurrent();
      this.entry = undefined; this.subject = undefined; this.options = undefined;
      this.expanded = true; this.expandAtStart = epoch; this.preparing = true;this.startAdmission = true;
      const queue = await this.queue.playSegments(segments, {...options, signal: scoped.signal, isCurrent: valid});
      assertCurrent(); return queue;
    } catch (error) {
      if (this.startOperationCurrent(epoch) && this.currentAuthority()) {await this.queue.cancel(); this.playback.leave(); this.changed();}
      throw error;
    } finally {
      scoped.close();
      if (epoch === this.startEpoch) {this.preparing = false;this.startAdmission = false; this.changed();}
    }
  }

  /** Append segments to the same queue this runtime already plays. The caller's
   * group/follower authority decides whether the append is offered; the queue,
   * CAS and adoption stay here and in the one queue owner. */
  private async enqueueCatalogueSegments(segments: readonly SegmentInput[], placement: 'next' | 'end',
    lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>, epoch: number, source: number): Promise<QueueHeader> {
    const admitted = () => !lifetime.signal.aborted && this.currentAuthority() && lifetime.isCurrent();
    const retired = () => Object.assign(new Error('native_catalogue_retired'), {name: 'AbortError'});
    if (!admitted() || !segments.length) throw retired();
    if (placement !== 'next' && placement !== 'end') throw Object.assign(new Error('native_catalogue_placement_invalid'), {code: 'invalid_queue_placement'});
    const valid = () => epoch === this.startEpoch && source === this.sourceEpoch && admitted();
    if (!valid()) throw retired();
    const scoped = this.selectionScope(lifetime.signal, valid);
    const client = new QueueClient({send: request => this.http.send({...request, signal: scoped.signal})}, this.o.requestId);
    const check = () => {if (!valid()) throw retired();};
    try {
      this.connect(); check();
      const held = this.queue.getSnapshot().view?.queue;
      if (!held) {
        check();
        const created = await client.create(segments, {signal: scoped.signal, retainQueueIds: this.retainedQueueIds()}); check();
        await this.queue.open(created.queue.id, scoped.signal); check();
        this.changed(); return created.queue;
      }
      let queue = await client.header(held.id, scoped.signal); check();
      const ordered = placement === 'next' ? [...segments].reverse() : segments;
      for (const segment of ordered) {check(); queue = (await client.addSegment(queue.id, queue.revision, placement, segment)).queue; check();}
      await this.queue.refresh(scoped.signal); check(); this.changed(); return queue;
    } finally {scoped.close();}
  }

  /** Play the server-owned whole group, never the screen's loaded preview.
   * The caller supplies its genuine route/member lifetime through adoption. */
  /** O-159: a group or shuffle start is named by its first entry once the queue knows it. */
  startGroup = (input: Readonly<{query: WholeGroupQuery; shuffle: boolean; signal: AbortSignal; isCurrent: () => boolean}>): Promise<void> =>
    this.watchedStart({}, () => this.startGroup({...input, ...this.retryLifetime()}), () => this.startGroupNow(input));
  private startGroupNow = async (input: Readonly<{query: WholeGroupQuery; shuffle: boolean; signal: AbortSignal; isCurrent: () => boolean}>): Promise<void> => {
    const admitted = () => !input.signal.aborted && this.currentAuthority() && input.isCurrent();
    if (!admitted()) throw Object.assign(new Error('native_group_retired'), {name: 'AbortError'});
    const epoch = this.beginOperation(), valid = () => this.startOperationCurrent(epoch) && admitted();
    const scoped = this.selectionScope(input.signal, valid);
    const assertCurrent = () => {if (!valid()) throw Object.assign(new Error('native_group_retired'), {name: 'AbortError'});};
    try {
      this.connect();
      await this.queue.cancel(); assertCurrent();
      await this.o.capabilitiesReady(); assertCurrent();
      const capabilities = await this.request<unknown>(routePath('get_capabilities'), 'GET', undefined, scoped.signal); assertCurrent();
      this.serverKinds = serverPlaybackV1Kinds(capabilities); this.transitions = serverSupportsQueueTransitions(capabilities);
      this.rememberCurrent(); assertCurrent();
      this.entry = undefined; this.subject = undefined; this.options = undefined;
      this.expanded = true; this.expandAtStart = epoch; this.preparing = true;this.startAdmission = true;
      await this.queue.playSegments([{source: {query: input.query}, ...(input.shuffle ? {order: {mode: 'shuffle' as const}} : {})}], {signal: scoped.signal, isCurrent: valid});
      assertCurrent();
    } catch (error) {
      if (this.startOperationCurrent(epoch) && this.currentAuthority()) {await this.queue.cancel(); this.playback.leave(); this.changed();}
      throw error;
    } finally {
      scoped.close();
      if (epoch === this.startEpoch) {this.preparing = false;this.startAdmission = false; this.changed();}
    }
  };

  /** A menu cannot start a version from another authority or replace a newer source. Its
   * cached options prove what was drawn; a fresh scoped read proves it is still offered. */
  /** O-159: "Play a different version" is drawn at once from its title. */
  startVersion = (input: NativePlaybackStart, selection: NativePlaybackVersionSelection): Promise<void> =>
    this.watchedStart(entryDisplay(input.entry), () => this.startVersion({...input, lifetime: undefined, presentation: undefined}, {...selection, lifetime: this.retryLifetime()}),
      () => this.startVersionNow(input, selection));
  private startVersionNow = async (input: NativePlaybackStart, selection: NativePlaybackVersionSelection): Promise<void> => {
    this.connect();
    if (!this.active()) return;
    let epoch = this.startEpoch;
    const menu = ++this.versionMenuEpoch, lifetime = selection.lifetime;
    const authority = authorityKey(selection.authority), expected = selection.expectedFence ? playbackFence(selection.expectedFence) : undefined;
    const versionId = selection.versionId, discTitle = parseDiscTitleRef(selection.discTitle), principal = startupPrincipal(this.o.session());
    const sameSource = () => expected ? sameFence(this.snapshot.fence, expected) : this.snapshot.fence === undefined;
    // A captured output belongs to preflight. Once this command owns the queue intent,
    // the core's generation/adoption guard must allow its own newly accepted output.
    let dispatching = false;
    const current = () => !lifetime.signal.aborted && lifetime.isCurrent() && this.currentAuthority() && principal !== undefined && startupPrincipal(this.o.session()) === principal && epoch === this.startEpoch && menu === this.versionMenuEpoch && authority === authorityKey(this.bound) && (!dispatching || this.startOperationCurrent(epoch)) && (dispatching || sameSource());
    if (!current()) return;
    if (!wireId(input.target.itemId) || !wireId(versionId) || selection.options.itemId !== input.target.itemId || selection.options.versions.filter(value => value.id === versionId).length !== 1) throw new Error(serviceText('queuePlayer.unsupported'));
    const drawnTitle = selection.options.versions.find(value => value.id === versionId)?.discTitle;
    if (drawnTitle && (drawnTitle.id !== discTitle?.id || drawnTitle.revision !== discTitle?.revision)) throw new Error(serviceText('queuePlayer.unsupported'));
    if (discTitle && !selection.options.versions.some(value => value.id === versionId
      && value.discTitle?.id === discTitle.id && value.discTitle.revision === discTitle.revision)) throw new Error(serviceText('queuePlayer.unsupported'));
    const scoped = this.selectionScope(lifetime.signal, current);
    try {
      await this.o.capabilitiesReady();
      if (!current()) return;
      const [offered, capabilities] = await Promise.all([
        this.optionsClient.options(input.target.itemId, discTitle ? {versionId, discTitle} : {}, scoped.signal),
        this.request<unknown>(routePath('get_capabilities'), 'GET', undefined, scoped.signal),
      ]);
      if (!current()) return;
      if (offered.itemId !== input.target.itemId || offered.versions.filter(value => value.id === versionId).length !== 1) throw new Error(serviceText('queuePlayer.unsupported'));
      // This final read owns resume. An earlier card/menu position has no authority here.
      const positionMs = discTitle ? 0 : offered.resume?.positionMs ?? 0;
      if (!Number.isFinite(positionMs) || positionMs < 0) throw new Error(serviceText('queuePlayer.unsupported'));
      const fresh = {...input, startIntent: 'explicit' as const, target: {...input.target, startSeconds: positionMs / 1000}};
      epoch = this.beginOperation(scoped.signal);
      await this.queue.cancel();
      if (!current()) return;
      dispatching = true;
      await this.startOffered(fresh, offered, capabilities, epoch, versionId, {signal: scoped.signal, isCurrent: current}, discTitle);
    } catch (error) { if (!current() || scoped.signal.aborted) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'}); throw error; }
    finally { scoped.close(); }
  };

  private async startOffered(input: NativePlaybackStart, offered: PlaybackOptions, capabilities: unknown, epoch: number, versionId?: string, lifetime?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>, discTitle?: DiscTitleRef): Promise<void> {
    const current = () => this.active() && this.startOperationCurrent(epoch) && !lifetime?.signal.aborted && lifetime?.isCurrent() !== false;
    if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
    if (offered.itemId !== input.target.itemId || !offered.versions.length) throw new Error(serviceText('queuePlayer.unsupported'));
    this.serverKinds = serverPlaybackV1Kinds(capabilities);
    this.transitions = serverSupportsQueueTransitions(capabilities);
    const old = this.dock.getSnapshot().local;
    if (old) {
      this.remember(old);
      if (this.snapshot.fence && sameFence(old.fence, this.snapshot.fence)) this.playback.pause();
    }
    if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'}); // A synchronous pause subscriber may retire the chooser or start another item.
    this.preparing = true;this.startAdmission = true;
    this.entry = input.entry.id === input.target.itemId ? input.entry : undefined;
    this.subject = this.entry ? subjectOf(this.entry) : undefined;
    this.options = offered;
    this.expanded = !audioKind(offered.kind);
    try {
      if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
      await this.queue.playItem(input.target.itemId, nativeStartSeconds(input), undefined, undefined, undefined, versionId ? {versionId, ...(discTitle ? {discTitle} : {})} : undefined,
        {...lifetime, state: input.state ?? 'playing', isCurrent: current, propagateError: true});
    } catch (error) {
      // Keep the original typed admission refusal, including its closed reason,
      // retry policy and account detail. A retired operation cannot publish it.
      if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
      throw error;
    } finally { if (epoch === this.startEpoch) { this.preparing = false;this.startAdmission = false; this.changed(); } }
  }

  /** Info reads borrow the actual queue occurrence. Unlike a replan, a read must
   * remain on the captured generation/revision and must never adopt its reply. */
  readSession = async (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): Promise<Session | undefined> => {
    if (fence.kind !== 'queue' || !this.matches(fence) || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const original = this.currentSession(), queue = this.queue.getSnapshot().view?.queue, start = this.startEpoch, route = this.o.api.getRouteSnapshot();
    if (!original || original.kind !== 'vod' || original.role !== 'local' || original.state === 'ended' || original.end || !queue || original.queue?.queueId !== queue.id
      || original.queue.entryId !== fence.entryId || original.versionId !== fence.sourceId || queue.currentEntryId !== fence.entryId
      || queue.currentPlaybackId !== original.id || route && route.phase !== 'ready') return;
    const current = () => {
      const session = this.currentSession(), live = this.fence(), heldQueue = this.queue.getSnapshot().view?.queue, liveRoute = this.o.api.getRouteSnapshot();
      return this.currentAuthority() && this.startEpoch === start && !lifetime.signal.aborted && lifetime.isCurrent()
        && !!live && sameFence(live, fence) && session?.id === original.id && session.revision === original.revision
        && heldQueue?.id === queue.id && heldQueue.revision === queue.revision && heldQueue.currentEntryId === fence.entryId
        && heldQueue.currentPlaybackId === original.id && (route ? liveRoute?.phase === 'ready' && liveRoute.revision === route.revision && liveRoute.origin === route.origin : liveRoute === null);
    };
    const scoped = this.selectionScope(lifetime.signal, current);
    const unroute = this.o.api.subscribeRoute(() => {if (!current()) scoped.close();});
    try {
      if (!current()) return;
      const fresh = await new SessionsClient(this.http, this.o.timers).get(original.id, scoped.signal);
      if (!current() || fresh.id !== original.id || fresh.revision !== original.revision || fresh.kind !== 'vod'
        || fresh.role !== original.role || fresh.state === 'ended' || fresh.end
        || fresh.itemId !== original.itemId || fresh.versionId !== original.versionId
        || fresh.presentation.generation !== fence.generation || fresh.queue?.queueId !== queue.id
        || fresh.queue.entryId !== fence.entryId) return;
      return fresh;
    } catch (error) {
      if (scoped.signal.aborted || !current()) return;
      throw error;
    } finally { unroute(); scoped.close(); }
  };

  /** Read-only audiobook facts. Mutation signatures remain with Media's accepting owner API. */
  listeningRead = (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): NativeListeningReadPort | undefined => {
    if (fence.kind !== 'queue' || !this.matches(fence) || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const original = this.currentSession(), principal = this.o.session(), context = this.queue.listening.getSnapshot().context, occurrence = original?.queue;
    const native = this.playback.getSnapshot().nativeListening, connection = this.o.api.getRouteConnection(), route = this.o.api.getRouteSnapshot();
    if (!original || original.kind !== 'audio' || original.role !== 'local' || original.state === 'ended' || original.end
      || original.id !== fence.sessionId || original.itemId !== fence.itemId || original.versionId !== fence.sourceId
      || original.presentation.generation !== fence.generation || !occurrence || occurrence.entryId !== fence.entryId
      || !principal?.sessionFamilyId || !context || context.kind !== 'audiobook_file' || context.itemId !== fence.itemId
      || !native?.binding || native.terminal || native.binding.sessionId !== fence.sessionId || native.binding.generation !== fence.generation || native.binding.itemId !== fence.itemId
      || !connection || route?.phase !== 'ready') return;
    const captured = Object.freeze({...fence, playback: Object.freeze({...fence.playback, scope: Object.freeze({...fence.playback.scope})})});
    const binding = Object.freeze({...native.binding}), owner = native.owner, family = principal.sessionFamilyId;
    const viewerKey = (value: NonNullable<ReturnType<NativePlaybackRuntimeOptions['session']>>) => JSON.stringify([value.viewer.authority, value.viewer.serverId, value.viewer.accountId, value.viewer.profileId]);
    const originalViewer = viewerKey(principal);
    const authority = authorityKey(this.bound), start = this.startEpoch, libraryId = context.libraryId, queueId = occurrence.queueId;
    const current = () => {
      const live = this.fence(), session = this.currentSession(), viewer = this.o.session(), details = this.queue.listening.getSnapshot();
      const heard = this.playback.getSnapshot().nativeListening, liveRoute = this.o.api.getRouteSnapshot();
      return this.currentAuthority() && authorityKey(this.o.authority()) === authority && this.startEpoch === start
        && !lifetime.signal.aborted && lifetime.isCurrent() && !!viewer && viewer.sessionFamilyId === family && viewerKey(viewer) === originalViewer
        && !!live && sameFence(live, captured) && !!session && session.id === original.id && session.kind === 'audio' && session.role === original.role
        && session.state !== 'ended' && !session.end && session.itemId === captured.itemId && session.versionId === captured.sourceId
        && session.presentation.generation === captured.generation && session.queue?.queueId === queueId && session.queue.entryId === captured.entryId
        && details.phase !== 'disposed' && details.context?.kind === 'audiobook_file' && details.context.itemId === captured.itemId && details.context.libraryId === libraryId
        && !!heard && heard.owner === owner && !heard.terminal && sameBindingFact(heard.binding, binding)
        && this.o.api.getRouteConnection() === connection && liveRoute?.phase === 'ready' && liveRoute.revision === route.revision && liveRoute.origin === route.origin;
    };
    const port = createNativeListeningReadPort({
      target: {libraryId, itemId: captured.itemId, sessionId: captured.sessionId, sessionGeneration: captured.generation, sourceId: captured.sourceId},
      scope: {serverId: this.bound.serverId, viewerId: JSON.stringify([principal.viewer.authority, principal.viewer.accountId, principal.viewer.profileId, family, this.bound.epoch])},
      lifetime, current, request: this.request,
      facts: () => {
        if (!current()) throw new Error('native_listening_read_retired');
        const heard = this.playback.getSnapshot().nativeListening!, details = this.queue.listening.getSnapshot(), audiobook = this.o.settings().audiobook;
        return {rate: {selected: heard.rate, ...(heard.effectiveRate === undefined ? {} : {effective: heard.effectiveRate}), ...(heard.supportedRates === undefined ? {} : {supported: heard.supportedRates})},
          preferences: {phase: details.phase, values: details.preferences, ...(audiobook ? {audiobook} : {})}};
      },
      subscribe: [this.subscribe, listener => this.playback.subscribe(listener), listener => this.queue.listening.subscribe(listener),
        listener => this.o.api.subscribeRoute(listener), listener => this.o.subscribeAuthority(listener), listener => this.o.subscribeSettings(listener)],
    });
    if (!port.current()) {port.dispose(); return;}
    return port;
  };

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
      || data.item.id !== binding.itemId || data.item.libraryId !== binding.libraryId || data.scope.viewerFence !== binding.responseFence) return;
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
    if (queue) { try { readyQueue = parseQueueSnapshot(queue, this.scope); } catch { /* Keep the validated v1 window in snapshot.queue; never invent a stricter projection. */ } }
    const kind = this.entry?.id === fence.itemId ? this.entry.kind : this.options?.itemId === fence.itemId ? this.options.kind : this.queue.getSnapshot().view?.items.find(item => item.entryId === fence.entryId)?.kind;
    return {...common, fence, kind: kindOf(kind) as 'video' | 'music' | 'audiobook', queue: readyQueue};
  }
  private changed(): void {
    if (this.updating || this.transitionEpoch !== undefined || !this.active()) return;
    this.updating = true;
    try {
      const state = this.playback.getSnapshot(), fence = this.fence();
      if (state.channel) {
        const channel = state.channel, linear = state.linear;
        const name = linear?.name || this.channel?.channel.name;
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
    } finally { this.updating = false; }
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
    this.cancelSnapshot();
    this.clearForcedImage();
    this.externalPresentationBinding = undefined; this.pictureBinding = undefined; this.pictureAttempt = undefined; this.pictureLeave = undefined; this.pictureState = 'idle';
    this.airPlayController?.abort(); this.airPlayController = undefined;
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
    if (!key) { if (!this.preparing && this.playback.getSnapshot().phase === 'idle') { this.entry = undefined; this.subject = undefined; } return; }
    const controller = new AbortController(); this.sourceController = controller;
    const state = this.playback.getSnapshot(), itemId = state.itemId!, session = this.currentSession();
    if (state.session?.mode === 'local') {
      this.options = undefined;
      if (this.offlineSource?.current() && this.offline?.mediaUrl(state.session)) this.localTracks = new NativeOfflineTracks(this.offlineSource.media,
        {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation});
      return;
    }
    if (state.channel) return; // Live references are never catalogue/subtitle endpoint IDs.
    if (this.entry?.id !== itemId) { this.entry = undefined; this.subject = undefined; }
    else this.subject = subjectOf(this.entry);
    const valid = () => {
      const current = this.playback.getSnapshot();
      return this.active() && epoch === this.sourceEpoch && key === this.sourceKey && !controller.signal.aborted
        && !current.channel && current.intentId === state.intentId && current.itemId === itemId
        && current.session?.id === state.session?.id && current.session?.generation === state.session?.generation;
    };
    void this.optionsClient.options(itemId, session?.versionId ? {versionId: session.versionId, ...(session.discTitle ? {discTitle: session.discTitle} : {})} : {}, controller.signal).then(value => {
      if (!valid() || value.itemId !== itemId) return; this.options = value; this.applyOptions(); this.publish();
    }).catch(() => {});
    void this.request<unknown>(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}), 'GET', undefined, controller.signal).then(raw => {
      if (!valid()) return;
      const item = parseMediaItem(raw);
      if (item.id !== itemId) return;
      const kind = item.kind === 'track' ? 'song' : item.kind === 'audiobook' ? 'audiobook_file' : item.kind;
      if (!['movie', 'episode', 'extra', 'song', 'audiobook_file'].includes(kind)) return;
      this.entry = {...this.entry, ...item, kind: kind as ContentEntry['kind']}; this.subject = subjectOf(this.entry);
      const detail = new DetailService({api: {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
        if (!valid() || !signal) throw new Error('Playback detail source retired.');
        // DetailService owns its read signal; bind that actual request to this source too.
        const unguard = this.http.guard(signal, valid);
        try { return await this.request<T>(path, method, body, signal); }
        finally { unguard(); }
      }}, scope: {serverId: this.bound.serverId, viewerId: this.bound.profileId}, requestId: async () => this.o.requestId()});
      const binding: SourceDetail = {service: detail, sourceEpoch: epoch, sourceKey: key, intentId: state.intentId,
        sessionId: state.session!.id, generation: state.session!.generation, itemId, libraryId: item.libraryId};
      this.detail = binding;
      this.detailOff = detail.subscribe(() => {
        if (!valid() || this.detail !== binding) return;
        const snapshot = detail.getSnapshot();
        // Home and Detail issue different fence namespaces. Pin only the genuine decoded
        // response from this bound service; its parser checks server/library/item scope.
        if (binding.responseFence === undefined && snapshot.phase === 'ready' && snapshot.data) binding.responseFence = snapshot.data.scope.viewerFence;
        const data = this.detailProjection();
        this.subject = this.entry ? subjectOf(this.entry, data) : undefined;
        this.changed();
      });
      void detail.select({itemId, libraryId: item.libraryId});
    }).catch(() => {});
    if (state.session && session?.kind === 'vod') {
      const service = new SubtitleService({request: this.request, ...(typeof this.o.api.requestSubtitleUpload === 'function' ? {
        requestSubtitleUpload: async <T>(path: string, method: 'POST' | 'PUT', body: FormData, signal?: AbortSignal): Promise<T> => {
          // Use the same authority guard, abort registration and late-response fence as JSON,
          // while the API owns the single-attempt multipart transport and its deadline.
          const response = await this.http.send({path, method, body, signal}, {send: async request => ({
            status: 200, headers: {}, body: await this.o.api.requestSubtitleUpload(request.path, method, body, request.signal),
          })});
          return response.body as T;
        },
      } : {})}, {itemId, sessionId: state.session.id, generation: state.session.generation}, this.o.requestId,
        {install: presentation => valid() && this.playback.installSubtitlePresentation(presentation)});
      this.subtitleService = service;
      // RN-05: no catalogue polling. The same source retries once the app returns to the
      // foreground or its route recovers; replacing or retiring the source unsubscribes.
      let routeReady = (this.o.api.getRouteSnapshot()?.phase ?? 'ready') === 'ready';
      // A failed refresh is published as the service's own error state.
      const retry = () => {if (valid() && this.subtitleService === service) void service.refresh().catch(() => {});};
      const offs = [
        service.subscribe(() => { if (valid()) { this.applyOptions(); this.publish(); } }),
        (() => {const off = AppState.addEventListener('change', state => {if (state === 'active') retry();}); return () => off.remove();})(),
        this.o.api.subscribeRoute(() => {
          const ready = (this.o.api.getRouteSnapshot()?.phase ?? 'ready') === 'ready', recovered = ready && !routeReady;
          routeReady = ready; if (recovered) retry();
        }),
      ];
      this.subtitleOff = () => {for (const off of offs) off();};
      service.setPolling(false); service.start();
    }
  }

  private applyOptions(): void {
    if (this.retired) return;
    const state = this.playback.getSnapshot(), fence = this.fence(), settings = this.o.settings();
    this.coordinator.setPassthroughPolicy({disabled: settings.overrides?.disablePassthrough === true || settings.overrides?.forceStereo === true, formats: settings.passthroughFormats ?? {}});
    this.coordinator.setDisplayMatching(this.o.androidDisplayMatching ? settings.matchFrameRate ?? 'auto' : undefined);
    this.coordinator.setDynamicRangeMatching(this.o.nativeDynamicRangeMatching ? settings.matchDynamicRange ?? 'auto' : undefined);
    const instance = this.coordinator.getInstanceId();
    if (instance !== this.audioTrackInstance) {this.audioTrackInstance = instance; this.audioTrackSelection.clear();}
    if (!state.session || !fence) { this.clearForcedImage(); this.audioTrackSelection.clear(); this.coordinator.setAudioTrack(null); this.coordinator.setLocalAudioTrack(null); this.coordinator.setOptions(1, null, null); return; }
    if (fence.kind !== 'local') this.coordinator.setLocalAudioTrack(null);
    if (fence.kind === 'channel') {
      this.clearForcedImage();
      this.audioTrackSelection.clear(); this.coordinator.setAudioTrack(null);
      this.coordinator.setOptions(1, null, {binding: {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation}, mode: 'off'});
      return;
    }
    const baseRate = this.local(fence).kind === 'audiobook' ? settings.audiobookRate : this.local(fence).kind === 'music' ? 1 : settings.videoRate;
    const rate = this.choices.get('speed', fence.playback, baseRate);
    const style = captionStyle(this.subtitleStyles.read(fence, {
      ...settings.subtitles,
      size: this.choices.get('subtitleSize', fence.playback, settings.subtitles.size),
      // The existing player offers three box overrides. Outline/shadow remain
      // genuine profile choices; do not narrow them to that older choice set.
      background: this.choices.has('subtitleBackground', fence.playback)
        ? this.choices.get('subtitleBackground', fence.playback, 'none') : settings.subtitles.background,
    }));
    if (fence.kind === 'local') {
      this.clearForcedImage();
      this.audioTrackSelection.clear();
      const engine = this.coordinator.getEngine() === 'mpv' ? 'mpv' : 'native', tracks = this.localTracks;
      // The local renderer validates every original, companion and font against the download.
      const rendering = this.o.engineFeatures(engine).subtitleRendering;
      const offeredRendering = settings.engine !== 'native' && this.o.engines.transport('mpv')
        ? [...new Set([...rendering, ...this.o.engineFeatures('mpv').subtitleRendering])] : rendering;
      tracks?.configure(engine, this.coordinator.getInstanceId(), rendering, offeredRendering, engine === 'mpv' || this.o.nativeLocalSubtitleTiming === true);
      this.coordinator.setAudioTrack(tracks?.audioCommand() ?? null);
      this.coordinator.setLocalAudioTrack(tracks?.localAudioCommand() ?? null);
      this.coordinator.setOptions(rate, null, tracks?.subtitleCommand(style, this.displaySubtitles && this.expanded)
        ?? {binding: {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation}, mode: 'off'});
      return;
    }
    this.path.setRate(rate);
    const session = this.currentSession(), plan = this.subtitleService?.getSnapshot().plan;
    const binding = {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation};
    const audioIndex = directAudioStreamIndex(this.coordinator.getEngine(), session, this.options);
    this.coordinator.setAudioTrack(this.audioTrackSelection.select(binding, audioIndex));
    if (this.coordinator.getEngine() === 'mpv' && session?.kind === 'vod' && session.role === 'local' && session.presentation.mode === 'direct'
      && session.presentation.audioTrackId && audioIndex === undefined && (state.phase === 'starting' || state.phase === 'ready')) {
      this.playback.fail(state.intentId, serviceText('player.opt.unavailable')); return;
    }
    const audioTrack = this.audioTrackSelection.getSnapshot();
    this.path.setTracks(this.coordinator.getEngine() === 'mpv' && session?.presentation.mode === 'direct'
      ? audioTrack?.state === 'applied' ? session.presentation.audioTrackId : undefined : session?.presentation.audioTrackId,
    session?.presentation.subtitle?.trackId ?? null);
    let subtitles: Record<string, unknown> | null = null;
    let forcedPath: string | undefined;
    const visible = this.displaySubtitles && this.expanded;
    const burned = session?.presentation.subtitle?.delivery === 'burn' || plan?.mode === 'track' && plan.renderer === 'burn_in';
    const drawn = deviceDrawnTrack(plan, session), selected = plan?.mode === 'track' && !this.subtitleService?.getSnapshot().suppressed ? plan.selected : null;
    const listUrl = fontsUrlOf(plan, session);
    if (visible && listUrl && listUrl !== this.fontsKey) {
      this.fontsKey = listUrl; this.fonts = 'pending'; const epoch = this.sourceEpoch;
      void this.request<unknown>(listUrl, 'GET', undefined, this.sourceController?.signal).then(raw => {
        if (this.active() && epoch === this.sourceEpoch && this.fontsKey === listUrl) { this.fonts = parseSubtitleFonts(raw, listUrl); this.applyOptions(); }
      }).catch(() => { if (this.active() && epoch === this.sourceEpoch && this.fontsKey === listUrl) { this.fonts = parseSubtitleFonts(null, listUrl); this.applyOptions(); } });
    }
    if (plan) subtitles = {binding, mode: 'off', revision: plan.revision};
    if (visible && plan && !burned && (selected || drawn)) {
      // mediaUrl enforces the current selected origin/media grant. No raw URL concatenation.
      const engine = this.coordinator.getEngine() === 'mpv' ? 'mpv' : 'native';
      // A native engine may genuinely install the rich subtitle producer too.
      // Plain cue documents retain their existing platform selection path.
      const rendering = this.o.engineFeatures(engine).subtitleRendering.filter(kind => engine === 'mpv' || kind !== 'text');
      const render = engineSubtitleRender(plan, state.session, rendering, path => this.o.api.mediaUrl(path), this.fonts, session);
      if (selected && plan.documentUrl || render) {
        const resource = selected ?? drawn?.resource;
        if (engine === 'mpv') {
          // The service keeps a route-relative media path. Resolve it through
          // the same authority-local route used by the actual native producer.
          try {forcedPath = forcedImageLinesPath(this.o.api.mediaUrl(state.session.streamUrl), render, resource, linesUrlOf(plan, session));}
          catch { /* an unavailable route cannot grant an optional filter */ }
        }
        subtitles = {binding, mode: 'track', revision: plan.revision, resourceId: selected?.id ?? drawn?.id, resourceRevision: resource?.revision ?? 1,
          manifestName: resource?.manifestName, origin: resource?.origin ?? 'embedded', language: resource?.language ?? '', title: resource?.title ?? '', offsetUs: selected ? plan.offsetUs : '0',
          ...(selected && plan.documentUrl ? {documentUrl: this.o.api.mediaUrl(plan.documentUrl)} : {}), ...(render ? {render} : {}), refreshRevision: 0, style};
      }
    }
    this.readForcedImage(fence, forcedPath);
    if (subtitles && this.forcedImageAvailable(fence) && this.choices.get('forcedOnly', fence.playback, false)) subtitles = {...subtitles, forcedOnly: true};
    this.coordinator.setOptions(rate, null, subtitles);
    const version = this.options?.versions.find(value => value.id === session?.versionId);
    this.path.setAutomatic(session?.presentation.quality.mode === 'automatic' ? {steps: automaticSteps(this.options?.qualities ?? []), originalKbps: version?.bitrateKbps} : null);
  }

  private clearForcedImage(): void {
    this.forcedImage?.controller.abort(); this.forcedImage = undefined;
  }

  private forcedImageAvailable(fence: PlaybackFence | undefined): boolean {
    const read = this.forcedImage, phase = this.playback.getSnapshot().phase;
    return !!fence && fence.kind === 'queue' && !!read?.mixed && read.epoch === this.sourceEpoch
      && !read.controller.signal.aborted && sameFence(read.fence, fence) && this.expanded && this.displaySubtitles
      && this.coordinator.getEngine() === 'mpv' && (phase === 'ready' || phase === 'starting');
  }

  private readForcedImage(fence: PlaybackFence, path: string | undefined): void {
    const source = this.sourceController?.signal, previous = this.forcedImage;
    if (!path || fence.kind !== 'queue' || !source || source.aborted) { this.clearForcedImage(); return; }
    if (previous?.path === path && previous.epoch === this.sourceEpoch && sameFence(previous.fence, fence)) return;
    this.clearForcedImage();
    const read = {fence, path, epoch: this.sourceEpoch, controller: new AbortController(), mixed: undefined as boolean | undefined};
    this.forcedImage = read;
    const abort = () => read.controller.abort(); source.addEventListener('abort', abort, {once: true});
    const current = () => this.active() && this.forcedImage === read && read.epoch === this.sourceEpoch
      && !read.controller.signal.aborted && this.matches(fence);
    void this.request<unknown>(path, 'GET', undefined, read.controller.signal).then(raw => {
      if (!current()) return;
      read.mixed = forcedChoiceOf(parseSubtitleLines(raw)); this.applyOptions(); this.publish();
    }).catch(() => { if (current()) {read.mixed = false; this.publish();} })
      .finally(() => source.removeEventListener('abort', abort));
  }

  setForcedOnly = (fence: PlaybackFence, on: boolean): boolean => {
    if (typeof on !== 'boolean' || !this.matches(fence) || !this.forcedImageAvailable(fence)) return false;
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
    const ready = !!fence && !this.retired && (playback.phase === 'ready' || playback.phase === 'starting');
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
    const nativeBinding = this.coordinator.getBinding(), acknowledgement = this.externalPresentationBinding;
    const localPicture = this.dock.getSnapshot().local;
    const firstVideoFrame = fence && !this.retired && nativeBinding && this.firstVideoFrame
      && this.firstVideoFrame.engine === this.coordinator.getEngine() && this.firstVideoFrame.instanceId === this.coordinator.getInstanceId()
      && sameNativePresentation(this.firstVideoFrame, nativeBinding) && this.firstVideoFrame.intentId === playback.intentId
      && this.firstVideoFrame.sessionId === fence.sessionId && this.firstVideoFrame.generation === fence.generation
      && this.firstVideoFrame.itemId === playback.itemId && localPicture && sameFence(localPicture.fence, fence)
      && (localPicture.kind === 'video' || localPicture.kind === 'live')
      ? this.firstVideoFrame : undefined;
    const externalPresentationBinding = acknowledgement === undefined ? undefined : acknowledgement && fence && !this.retired
      && !playback.pendingSeek && !playback.failedSeek && playback.phase !== 'error' && nativeBinding
      && sameNativePresentation(acknowledgement, nativeBinding) && acknowledgement.intentId === playback.intentId
      && acknowledgement.sessionId === fence.sessionId && acknowledgement.generation === fence.generation
      && acknowledgement.itemId === playback.itemId ? acknowledgement : null;
    const seekWindow = playback.channel && playback.linear ? {startSeconds: channelSeconds(playback.linear.media.windowStartUs), endSeconds: channelSeconds(playback.linear.media.windowEndUs)} : undefined;
    return Object.freeze({authority: this.bound, sequence, dock: this.dock.getSnapshot(), playback, fence, subject: this.subject, entry: this.entry, queue, session, policy: this.videoPolicy.getSnapshot(),
      ...(fence?.kind === 'channel' && this.channelGuide && legacyChannel(this.channelGuide.row)?.id === fence.channelId
        && legacyChannel(this.channelGuide.row)?.sourceId === fence.sourceId && legacyChannel(this.channelGuide.row)?.provenance === fence.channelKind
        && legacyChannel(this.channelGuide.row)?.generation === fence.channelGeneration ? {channelGuide: this.channelGuide} : {}),
      startFailure: this.startFailure, capacityWait: this.capacityWait, firstVideoFrame, ...this.startingFields(fence),
      initialPresentation: this.initialPresentation?.read(fence),
      ...(fence && this.forcedImageAvailable(fence) ? {forcedOnly: Object.freeze({on: this.choices.get('forcedOnly', fence.playback, false)})} : {}),
      options: this.options, subtitles: this.subtitleService?.getSnapshot(), ...(fence?.kind === 'queue' ? {subtitleStyle: this.subtitleStyles.read(fence, this.o.settings().subtitles)} : {}), localTracks, audioTrackSelection: this.audioTrackSelection.getSnapshot(), seekWindow, engine: this.coordinator.getEngine() === 'mpv' ? 'mpv' : 'native', lease: this.coordinator.getSnapshot(), effectiveRate: playback.nativeListening?.effectiveRate ?? this.effectiveRate, supportedRates: playback.nativeListening?.supportedRates ?? this.supportedRates, externalPresentationBinding,
      ...(this.o.pictureInPictureControls ? {pictureInPicture: Object.freeze({request: this.pictureRequests, active: this.pictureAttempt?.state === 'active', state: this.pictureState,
        ...(this.pictureBinding && externalPresentationBinding && sameNativePresentation(this.pictureBinding, externalPresentationBinding) ? {binding: this.pictureBinding} : {})})} : {}), retired: this.retired,
      capabilities: Object.freeze({queueSelection: queueSelectionReady, queueEdit: queueEditReady, queuePolicy: ready && fence?.kind === 'queue' && !!view && session?.queue?.queueId === view.queue.id && view.queue.currentEntryId === fence.entryId, retry: playback.phase === 'error' && !this.preparing && fence?.kind === 'queue' && session?.kind === 'vod' && session.presentation.subtitle?.delivery !== 'other' && view?.queue.currentEntryId === fence.entryId && view.queue.entries.some(entry => entry.id === fence.entryId && !entry.hidden && !entry.removed), play: ready && playback.intent === 'paused', pause: ready && playback.intent === 'playing', stop: !!fence, seek: ready && (seekWindow ? seekWindow.endSeconds > seekWindow.startSeconds : playback.duration > 0),
        previous: ready && fence?.kind === 'queue' && (playback.positionSeconds > 3 || currentIndex > 0), next: ready && fence?.kind === 'queue' && !!view && view.next.reason !== 'end', cast: ready && fence?.kind === 'queue' && !!this.o.openDestinations,
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
    if (this.factDepth) { this.factPublication = true; return; }
    this.videoPolicy.update();
    this.snapshot = this.makeSnapshot(this.snapshot.sequence + 1);
    for (const listener of [...this.listeners]) listener();
  }

  private acceptFact(fact: any): void {
    if (!this.active()) return;
    this.factDepth++;
    try { this.applyFact(fact); }
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
      if (binding) this.firstVideoFrame = binding;
    }
    this.externalPresentationBinding = nativeExternalPresentationBinding(fact, this.coordinator.getBinding());
    // BUFFERING can emit an unchanged progress tick. Keep both the UI and the
    // server observation stalled until actual media recovery is observed.
    if (!(fact.type === 'progress' && before.recovering && !recovered && before.intent === 'playing')) this.path.engineFact(fact);
    if (recovered || fact.type === 'ended') {this.recovery.clear(); this.playback.recovering(before.intentId, false);}
    if (this.audioTrackSelection.accept(fact)) {
      if (fact.type === 'audioTrackFailed') this.playback.fail(before.intentId, serviceText('player.opt.unavailable'));
      else this.applyOptions();
    }
    if (before.session?.mode === 'local') {
      const result = this.localTracks?.accept(fact);
      if (result === 'failed') this.playback.fail(before.intentId, serviceText('player.opt.unavailable'));
      else if (result === 'changed') this.applyOptions();
    }
    if (typeof fact.effectiveRate === 'number') this.effectiveRate = fact.effectiveRate;
    if (Array.isArray(fact.supportedRates)) this.supportedRates = Object.freeze(fact.supportedRates.filter((rate: unknown): rate is number => typeof rate === 'number' && Number.isFinite(rate) && rate >= .5 && rate <= 3));
    if (fact.type === 'externalPlayback') this.followAirPlay(fact.active === true);
    else if (fact.type === 'progress' && this.airPlayActive) this.followAirPlay();
    if (fact.type === 'systemPaused') this.playback.pause();
    else if (fact.type === 'ready') this.playback.ready(before.intentId);
    else if (fact.type === 'stalled') this.playback.recovering(before.intentId, true);
    else if (fact.type === 'seekApplied') this.playback.seekApplied(before.intentId, fact.seekRevision, fact.position, fact.paused ? 'paused' : 'playing');
    else if (fact.type === 'seekFailed') this.playback.seekFailed(before.intentId, fact.seekRevision, serviceText('queuePlayer.startFailed'));
    else if (fact.type === 'progress' || fact.type === 'ended') this.playback.fact(before.intentId, fact.position, fact.type === 'ended' ? 'ended' : fact.paused ? 'paused' : 'playing');
    else if (fact.type === 'error' && before.session && atNaturalEnd(Math.max(typeof fact.position === 'number' ? fact.position : 0, before.positionSeconds), before.duration)) {
      this.recovery.clear(); this.playback.fact(before.intentId, before.duration, 'ended');
    }
    else if (fact.type === 'error') {
      const failure = classifyPlaybackFailure(fact);
      if (failure.network && this.o.api.getRouteConnection()) {
        const run = `${before.intentId}/${before.playbackRun}`;
        const current = () => {const state = this.playback.getSnapshot(); return this.active() && state.intentId === before.intentId
          && state.playbackRun === before.playbackRun && !!state.session && (state.phase === 'starting' || state.phase === 'ready');};
        if (!current()) return;
        this.playback.recovering(before.intentId, true);
        this.recovery.start(run, current, () => this.playback.fail(before.intentId, serviceText('queuePlayer.startFailed')),
          () => {void this.o.api.recoverRoute().catch(() => {});});
        return;
      }
      this.recovery.clear();
      if (failure.engine) this.playback.engineFailed(before.intentId, failure.engine, '', serviceText('queuePlayer.unsupported'));
      else this.playback.fail(before.intentId, serviceText('queuePlayer.startFailed'));
    }
    if (fact.rateError && before.intent === 'playing') this.playback.pause();
    this.publish();
  }

  private videoPolicyCurrent(fence: QueuePlaybackFence): boolean {
    const live = this.fence();
    return this.currentAuthority() && !!live && sameFence(live, fence);
  }
  private videoPolicyFrame(): NativeVideoPolicyFrame | undefined {
    const state = this.playback.getSnapshot(), fence = this.fence(), queue = this.queue.getSnapshot(), view = queue.view;
    const session = this.currentSession();
    if (this.retired || this.preparing || this.transitionEpoch !== undefined || !fence || fence.kind !== 'queue'
      || !session || session.kind !== 'vod' || session.role !== 'local' || !view || view.queue.currentEntryId !== fence.entryId
      || session.queue?.queueId !== view.queue.id || session.queue.entryId !== fence.entryId) return;
    const duration = Number.isFinite(state.duration) ? Math.max(0, state.duration) : 0;
    const position = Number.isFinite(state.positionSeconds) ? Math.max(0, state.positionSeconds) : 0;
    if (duration > 1e9 || position > 1e9) return;
    const markers = nativeVideoPolicyMarkers(this.options?.itemId === fence.itemId ? this.options.markers : [], duration);
    const item = view.items.find(entry => entry.entryId === fence.entryId);
    const rate = this.effectiveRate > 0 ? this.effectiveRate : this.choices.get('speed', fence.playback, this.o.settings().videoRate);
    return {markers, frame: {fence, nowMs: 0, positionSeconds: duration > 0 ? Math.min(position, duration) : position,
      durationSeconds: duration, rate: Math.max(.25, Math.min(4, rate)), kind: item?.kind ?? this.entry?.kind ?? 'movie',
      playing: state.phase === 'ready' && state.intent === 'playing' && !state.recovering && !state.pendingSeek && !state.failedSeek,
      expanded: this.expanded, ended: state.phase === 'ended' && queue.completion?.sessionId === fence.sessionId,
      modes: this.o.settings().skips, postPlay: nativeVideoPolicyPostPlay(view.postPlay),
      ...(state.phase === 'error' ? {attention: 'playback-error' as const} : {})}};
  }

  private async executeVideoPolicy(command: PolicyCommand): Promise<void> {
    const fence = command.fence;
    if (!this.videoPolicyCurrent(fence)) return;
    if (command.kind === 'expand') {
      // changed() already reveals each new failed source once. A later Back or
      // Minimize must not be undone by another policy tick for that same error.
      if (command.reason === 'playback-error') return;
      this.expanded = true; this.dock.expand(fence); this.applyOptions(); this.publish(); return;
    }
    if (command.kind === 'seek') {
      const state = this.playback.getSnapshot();
      if (state.phase !== 'ready' || state.recovering || state.pendingSeek || state.failedSeek) return;
      if (command.reason !== 'undo') this.playback.markerSkipped(command.markerId, command.reason === 'automatic-skip' ? 'automatic' : 'manual', state.positionSeconds);
      this.playback.seek(command.positionSeconds); return;
    }
    const queue = this.queue.getSnapshot(), view = queue.view;
    if (!view || view.queue.currentEntryId !== fence.entryId || view.next.entryId !== command.nextEntryId || !view.next.available) return;
    if (queue.completion?.sessionId === fence.sessionId) await this.queue.continueCompletion(command.mode);
    else if (command.mode === 'automatic') await this.queue.advanceFromCredits(view.queue.id, view.queue.revision);
    else await this.queue.next(view.queue.id, view.queue.revision);
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

  toggle = async (fence: PlaybackFence): Promise<void> => { if (this.matches(fence)) { const state = this.playback.getSnapshot(); if (state.intent === 'playing') this.playback.pause(); else this.playback.resume(); } };
  selectQueuedEntry = async (fence: PlaybackFence, request: V1QueuedEntrySelection, lifetime: NativePlaybackReadLifetime): Promise<void> => {
    if (!this.matches(fence) || fence.kind !== 'queue' || !this.snapshot.capabilities.queueSelection
      || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const session = this.currentSession(), view = this.queue.getSnapshot().view, offered = view?.window;
    const entry = offered?.entries.find(row => row.entryId === request.entryId);
    if (!offered || offered.queueId !== request.queueId || offered.revision !== request.expectedRevision
      || entry?.available !== true || entry.position === undefined || !session?.queue || session.queue.queueId !== request.queueId || session.queue.entryId !== fence.entryId
      || view?.queue.id !== request.queueId || view.queue.revision !== request.expectedRevision || request.entryId === fence.entryId
      || !queueVideoKind(view.items.find(item => item.entryId === request.entryId)?.kind ?? '')) return;
    const epoch = this.beginOperation();
    // The route/authority survive this intentional source change; core retains the old output binding.
    const current = () => this.currentAuthority() && this.startOperationCurrent(epoch) && !lifetime.signal.aborted && lifetime.isCurrent();
    const scoped = this.selectionScope(lifetime.signal, current);
    this.preparing = true; this.publish();
    try {
      await this.queue.selectQueuedEntry({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
        itemId: fence.itemId, versionId: fence.sourceId, queueId: request.queueId, entryId: fence.entryId}, request,
        {signal: scoped.signal, isCurrent: current});
    } finally {
      scoped.close();
      if (epoch === this.startEpoch) {this.preparing = false; this.changed();}
    }
  };
  editQueue = async (fence: PlaybackFence, request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueueEditIntent}>, lifetime: NativePlaybackReadLifetime): Promise<void> => {
    if (!this.matches(fence) || fence.kind !== 'queue' || !this.snapshot.capabilities.queueEdit
      || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const session = this.currentSession(), view = this.queue.getSnapshot().view, offered = view?.window;
    if (!offered || offered.queueId !== request.queueId || offered.revision !== request.expectedRevision || !session?.queue || session.queue.queueId !== request.queueId || session.queue.entryId !== fence.entryId
      || view?.queue.id !== request.queueId || view.queue.revision !== request.expectedRevision) return;
    const epoch = this.startEpoch;
    const current = () => epoch === this.startEpoch && this.matches(fence) && !lifetime.signal.aborted && lifetime.isCurrent();
    const scoped = this.selectionScope(lifetime.signal, current);
    try {
      await this.queue.editQueue({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
        itemId: fence.itemId, versionId: fence.sourceId, queueId: request.queueId, entryId: fence.entryId}, request.expectedRevision, request.intent,
        {signal: scoped.signal, isCurrent: current});
    } finally {scoped.close();}
  };
  setQueuePolicy = async (fence: PlaybackFence, request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueuePolicyIntent}>, lifetime: NativePlaybackReadLifetime): Promise<void> => {
    if (!this.matches(fence) || fence.kind !== 'queue' || !this.snapshot.capabilities.queuePolicy
      || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const session = this.currentSession(), view = this.queue.getSnapshot().view;
    if (!session?.queue || session.queue.queueId !== request.queueId || session.queue.entryId !== fence.entryId
      || view?.queue.id !== request.queueId || view.queue.revision !== request.expectedRevision) return;
    const epoch = this.startEpoch;
    const current = () => epoch === this.startEpoch && this.matches(fence) && !lifetime.signal.aborted && lifetime.isCurrent();
    const scoped = this.selectionScope(lifetime.signal, current);
    try {
      await this.queue.setQueuePolicy({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
        itemId: fence.itemId, versionId: fence.sourceId, queueId: request.queueId, entryId: fence.entryId}, request.expectedRevision, request.intent,
        {signal: scoped.signal, isCurrent: current});
    } finally {scoped.close();}
  };
  retry = async (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime): Promise<void> => {
    if (!this.matches(fence) || fence.kind !== 'queue' || this.preparing || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const session = this.currentSession(), state = this.playback.getSnapshot();
    if (state.phase !== 'error' || session?.kind !== 'vod' || session.queue?.entryId !== fence.entryId) return;
    const wantedIntent = this.playback.recoveryIntent();
    const epoch = this.beginOperation();
    // The core owns the intentional output replacement; the original screen and authority own this operation.
    const current = () => this.currentAuthority() && epoch === this.startEpoch && !lifetime.signal.aborted && lifetime.isCurrent();
    const scoped = this.selectionScope(lifetime.signal, () => {
      const live = this.playback.getSnapshot();
      return current() && live.intent === wantedIntent && live.playbackRun === fence.playback.run && live.session?.id === fence.sessionId
        && live.session.generation === fence.generation;
    });
    this.preparing = true; this.publish();
    try {
      await this.queue.retryFailed({sessionId: fence.sessionId, generation: fence.generation, playbackRun: fence.playback.run,
        itemId: fence.itemId, versionId: fence.sourceId, queueId: session.queue.queueId, entryId: fence.entryId},
        {signal: scoped.signal, isCurrent: current});
    } finally {
      scoped.close();
      if (epoch === this.startEpoch) {this.preparing = false; this.changed();}
    }
  };
  replay = async (fence: PlaybackFence): Promise<void> => {
    if (!this.matches(fence)) return;
    if (fence.kind === 'channel') { this.channelControl?.retry(); return; }
    if (fence.kind === 'local') {
      if (this.playback.getSnapshot().phase === 'error') await this.playback.retry();
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
  seek = async (fence: PlaybackFence, seconds: number): Promise<void> => {
    if (!this.matches(fence) || !Number.isFinite(seconds) || seconds < 0 || !this.snapshot.capabilities.seek) return;
    const window = this.snapshot.seekWindow;
    if (fence.kind === 'channel' && (!window || seconds < window.startSeconds || seconds > window.endSeconds)) return;
    this.playback.seek(seconds);
  };
  skip = async (fence: PlaybackFence, seconds: number): Promise<void> => { if (this.matches(fence) && Number.isFinite(seconds)) await this.seek(fence, Math.max(0, this.playback.getSnapshot().positionSeconds + seconds)); };
  previous = async (fence: PlaybackFence): Promise<void> => { if (this.matches(fence) && this.snapshot.capabilities.previous) await this.queue.previous(); };
  next = async (fence: PlaybackFence): Promise<void> => { const view = this.queue.getSnapshot().view; if (this.matches(fence) && this.snapshot.capabilities.next && view) await this.queue.next(view.queue.id, view.queue.revision); };
  goLive = async (fence: PlaybackFence): Promise<void> => { if (this.matches(fence) && fence.kind === 'channel') this.channelControl?.goLive(); };

  /** The remote dock's own transitions: this app's player is not the source of any of them. */
  beginRemote = async (value: RemoteDock): Promise<void> => {
    if (!this.active()) return;
    // The explicit connection start may only retire the playback it was captured
    // from; a replacement dock playback is never stopped by a late handoff.
    const local = this.dock.getSnapshot().local;
    if (local && !sameFence(local.fence, value.playback.fence)) return;
    await this.onTransition(this.dock.beginRemote(value, Date.now()));
  };
  updateRemote = async (value: RemoteDock): Promise<void> => { if (this.active()) await this.onTransition(this.dock.remote(value, Date.now())); };
  dismissRemote = async (id: string): Promise<void> => { if (this.active()) await this.onTransition(this.dock.dismissRemote(id)); };
  continueHere = async (id: string): Promise<void> => { if (this.active()) await this.onTransition(this.dock.continueHere(id)); };
  setSleepTimer = async (fence: PlaybackFence, minutes: number | null): Promise<void> => { if (this.matches(fence)) await this.playback.setListeningDeadline('sleep', minutes); };
  stop = async (fence: PlaybackFence): Promise<void> => { if (this.matches(fence)) await this.onTransition(this.dock.close(fence)); };
  open = (fence: PlaybackFence): void => { if (this.matches(fence)) { this.expanded = true; this.dock.expand(fence); this.applyOptions(); this.publish(); } };
  collapse = (fence: PlaybackFence): void => { if (this.matches(fence)) { this.expanded = false; this.dock.collapse(fence); this.applyOptions(); this.publish(); } };
  setSubtitleDisplay(visible: boolean): void { if (this.displaySubtitles !== visible) { this.displaySubtitles = visible; this.applyOptions(); } }
  subtitleControls = (fence: PlaybackFence): NativePlaybackSubtitleControls | undefined => {
    const service = this.subtitleService, epoch = this.sourceEpoch, key = this.sourceKey;
    const plan = service?.getSnapshot().plan;
    if (!service || !plan || fence.kind !== 'queue' || !this.matches(fence) || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation) return undefined;
    const current = () => this.matches(fence) && this.subtitleService === service && this.sourceEpoch === epoch && this.sourceKey === key
      && service.getSnapshot().plan?.sourceId === plan.sourceId;
    const picker = this.o.subtitleFilePicker, sourceSignal = this.sourceController?.signal;
    return boundSubtitleControls(service, current, picker && service.canUpload && sourceSignal ? async (metadata, caller) => {
      if (!current()) return false;
      this.subtitleFileController?.abort();
      const controller = new AbortController(); this.subtitleFileController = controller;
      const abort = () => controller.abort();
      sourceSignal.addEventListener('abort', abort, {once: true});
      if (sourceSignal.aborted) controller.abort();
      try {
        return await uploadPickedSubtitle(picker, async (input, lifetime) => {
          await service.upload(input, undefined, lifetime);
          if (service.getSnapshot().error) throw new Error(serviceText('player.subtitles.changeFailed'));
        }, {signal: controller.signal, isCurrent: current}, caller, metadata);
      } finally {
        sourceSignal.removeEventListener('abort', abort); controller.abort();
        if (this.subtitleFileController === controller) this.subtitleFileController = undefined;
      }
    } : undefined);
  };
  cast = async (fence: PlaybackFence, anchorRef?: RefObject<View | null>): Promise<void> => { if (this.matches(fence) && this.o.openDestinations) await this.o.openDestinations(fence, anchorRef); };

  /** Same queue occurrence/session, then an actual native acknowledgement. The
   * caller owns host/navigation lifetime; the accepted new generation is ours. */
  prepareExternalPresentation = async (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime): Promise<NativeBinding | undefined> => {
    if (fence.kind !== 'queue' || !this.matches(fence) || lifetime.signal.aborted || !lifetime.isCurrent()) return;
    const original = this.currentSession(), playback = this.playback.getSnapshot(), controllerState = this.path.controller.getSnapshot();
    if (!original || original.kind !== 'vod' || original.id !== fence.sessionId || !original.queue || original.queue.entryId !== fence.entryId || playback.nativeListening
      || playback.phase !== 'ready' || playback.pendingSeek || playback.failedSeek || controllerState.changing
      || !controllerState.observation || controllerState.observation.generation !== original.presentation.generation
      || !Number.isFinite(controllerState.observation.positionMs) || controllerState.observation.state === 'idle' || controllerState.observation.state === 'error') return;
    const subtitles = this.subtitleService?.getSnapshot();
    const preparation = externalSubtitlePreparation(subtitles?.suppressed ? null : subtitles?.plan, original);
    if (preparation.kind === 'unavailable') return;
    this.externalController?.abort();
    const controller = this.externalController = new AbortController(), epoch = ++this.externalEpoch, start = this.startEpoch;
    let generation: number | undefined;
    const current = () => {
      const state = this.playback.getSnapshot(), session = this.queue.currentSession(), queue = this.queue.getSnapshot().view?.queue;
      try {return this.active() && !controller.signal.aborted && !lifetime.signal.aborted && lifetime.isCurrent()
        && epoch === this.externalEpoch && start === this.startEpoch && state.intentId === playback.intentId
        && state.session?.id === original.id && session?.id === original.id
        && queue?.id === original.queue!.queueId && queue.currentEntryId === original.queue!.entryId && queue.currentPlaybackId === original.id
        && (generation === undefined || state.session.generation === generation);}
      catch {return false;}
    };
    const abort = () => controller.abort();
    lifetime.signal.addEventListener('abort', abort, {once: true});
    if (lifetime.signal.aborted) controller.abort();
    const retire = this.subscribe(() => {if (!current()) controller.abort();});
    try {
      const adopted = preparation.kind === 'burn'
        ? await this.queue.changePlayback(preparation.choices, {signal: controller.signal, isCurrent: current, recoverEnded: false}) : original;
      if (!current() || !adopted || adopted.id !== original.id || adopted.queue?.queueId !== original.queue.queueId || adopted.queue.entryId !== original.queue.entryId
        || adopted.versionId !== original.versionId || adopted.presentation.audioTrackId !== original.presentation.audioTrackId) return;
      if (preparation.kind === 'burn' && (adopted.presentation.generation <= original.presentation.generation || adopted.presentation.subtitle?.trackId !== preparation.trackId || adopted.presentation.subtitle.delivery !== 'burn')) return;
      generation = adopted.presentation.generation;
      return await this.waitForExternalPresentation(controller.signal, current, binding => {
        if (binding.sessionId !== adopted.id || binding.generation !== generation || binding.intentId !== playback.intentId || binding.itemId !== playback.itemId) return false;
        if (preparation.kind !== 'burn') return true;
        const plan = this.subtitleService?.getSnapshot().plan;
        return plan?.sessionId === adopted.id && plan.generation === generation && plan.sourceId === preparation.sourceId;
      });
    } catch (error) {
      if (!current()) return;
      throw error;
    } finally {
      retire(); lifetime.signal.removeEventListener('abort', abort); controller.abort();
      if (this.externalController === controller) this.externalController = undefined;
    }
  };

  /** The AirPlay route changed (`active`), or playback moved on while it lasts. */
  private followAirPlay(active?: boolean): void {
    if (active !== undefined && active !== this.airPlayActive) {
      this.airPlayActive = active; this.airPlaySession = undefined;
      this.airPlayController?.abort(); this.airPlayController = undefined;
    }
    const fence = this.snapshot.fence, sessionId = this.currentSession()?.id;
    if (!this.airPlayActive || this.airPlayController || fence?.kind !== 'queue' || !sessionId || sessionId === this.airPlaySession) return;
    const controller = this.airPlayController = new AbortController();
    void this.prepareExternalPresentation(fence, {signal: controller.signal, isCurrent: () => this.airPlayController === controller})
      .then(binding => {if (this.airPlayController === controller && binding) this.airPlaySession = sessionId;},
        // The captions stay drawn on this device; the next progress tick tries again.
        error => {if (__DEV__) console.warn('[airplay-captions]', error);})
      .finally(() => {if (this.airPlayController === controller) this.airPlayController = undefined;});
  }

  private waitForExternalPresentation(signal: AbortSignal, current: () => boolean, accepts: (binding: NativeBinding) => boolean): Promise<NativeBinding | undefined> {
    return new Promise(resolve => {
      let finished = false, timer: unknown, unsubscribe = () => {};
      const finish = (binding?: NativeBinding) => {
        if (finished) return; finished = true; unsubscribe(); signal.removeEventListener('abort', aborted);
        if (timer !== undefined) {if (this.o.timers?.clearTimer) this.o.timers.clearTimer(timer); else clearTimeout(timer as ReturnType<typeof setTimeout>);}
        resolve(binding);
      };
      const aborted = () => finish();
      const check = () => {
        if (signal.aborted || !current()) {finish(); return;}
        const binding = this.snapshot.externalPresentationBinding;
        if (binding && accepts(binding)) finish(binding);
      };
      unsubscribe = this.subscribe(check); signal.addEventListener('abort', aborted, {once: true});
      timer = this.o.timers?.setTimer ? this.o.timers.setTimer(() => finish(), 10000) : setTimeout(() => finish(), 10000);
      check();
    });
  }

  /** True acknowledges a fenced retained-view request, never that PiP started. */
  requestPictureInPicture = async (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime): Promise<boolean> => {
    if (!this.o.pictureInPictureControls) return false;
    // One attempt at a time, like the retained view, and a press the view could not
    // serve is a true no-op: nothing is issued, published or handed to native, so a
    // starting attempt is never retagged by its counter.
    if (!this.pictureRequestAllowed()) return false;
    const pending = this.prepareExternalPresentation(fence, lifetime), epoch = this.externalEpoch;
    const binding = await pending, ready = this.snapshot.externalPresentationBinding;
    if (!binding || !ready || !this.active() || epoch !== this.externalEpoch || lifetime.signal.aborted || !lifetime.isCurrent()
      || !sameNativePresentation(binding, ready) || this.pictureRequests >= Number.MAX_SAFE_INTEGER) return false;
    // A racing call may have issued while this one prepared.
    if (!this.pictureRequestAllowed()) return false;
    this.pictureRequests++;
    this.pictureBinding = binding;
    if (this.pictureAttempt?.state === 'active') this.pictureLeave = this.pictureRequests;
    else {this.pictureAttempt = Object.freeze({request: this.pictureRequests, state: 'pending'}); this.pictureState = 'idle';}
    this.publish(); return true;
  };

  /** A pending attempt owns the request counter, and an active one already holds its
   * single leaving request; in both cases another press asks for nothing new. */
  private pictureRequestAllowed(): boolean {
    const attempt = this.pictureAttempt;
    return !attempt || attempt.state === 'active' && this.pictureLeave === undefined;
  }

  /** The native view's own report about a request it was handed. Admitted only for
   * the current lease and that request, so a replaced surface or a stale answer
   * cannot claim the picture; `unavailable` may also arrive before any request. */
  admitPictureInPicture = (fact: unknown): boolean => {
    if (!this.o.pictureInPictureControls || this.retired) return false;
    if (fact === null || typeof fact !== 'object' || Array.isArray(fact)) return false;
    const raw = fact as {lease?: unknown; request?: unknown; requestId?: unknown; binding?: unknown; state?: unknown};
    const state = raw.state === 'started' || raw.state === 'restore' || raw.state === 'stopped' || raw.state === 'unavailable' ? raw.state : undefined;
    if (!state || typeof raw.lease !== 'string' || !raw.lease || raw.lease !== this.coordinator.getSnapshot()) return false;
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
    } else {
      // Started, restore and stopped must name this attempt (or the later request
      // that asks to come back from an active one), carry its own complete
      // binding, and only move in one direction.
      const ready = this.snapshot.externalPresentationBinding;
      if (!this.pictureBinding || !ready || !sameNativePresentation(this.pictureBinding, ready)) return false;
      if (!attempt || !sameBindingFact(raw.binding, this.pictureBinding)) return false;
      const owns = requested === attempt.request || (attempt.state === 'active' && requested !== 0 && requested === this.pictureLeave);
      if (!owns) return false;
      if (state === 'started' ? attempt.state !== 'pending' || requested !== attempt.request : attempt.state !== 'active') return false;
    }
    this.pictureState = state;
    if (state === 'started' && attempt) this.pictureAttempt = Object.freeze({request: attempt.request, state: 'active'});
    // Its own terminal only: the attempt and the request that may end it both retire.
    if (state === 'stopped' || state === 'unavailable') {this.pictureAttempt = undefined; this.pictureLeave = undefined;}
    this.publish();
    return true;
  };

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
    if (this.choices.set('speed', fence.playback, rate, fence)) { this.playback.setPlaybackRate(rate); this.applyOptions(); this.publish(); }
  };
  onTransition = async (transition: DockTransition): Promise<void> => { await this.applyTransition(transition); };

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
            await this.openChannel(saved.channel, epoch, controller.signal, valid, 'paused');
            if (!valid()) return;
            const current = this.fence(), linear = this.playback.getSnapshot().linear;
            const position = effect.playback.positionSeconds;
            if (!current || current.kind !== 'channel' || !linear || position < channelSeconds(linear.media.windowStartUs) || position > channelSeconds(linear.media.windowEndUs)) {
              this.playback.leave(); throw new Error('channel_position_unavailable');
            }
            try {
              await this.restoreChannelPosition(current, position, epoch, controller.signal);
            } catch (error) {
              // Retire only this restore's paused session, never a replacement that won.
              if (epoch === this.startEpoch && this.currentAuthority()) this.playback.leave();
              throw error;
            }
          } else if (effect.playback.fence.kind === 'local') {
            const valid = () => this.currentAuthority() && epoch === this.startEpoch && !controller.signal.aborted;
            if (!this.offline || !saved.offline || !saved.offline.current()) throw new Error(serviceText('downloads.offline.notPlayableHere'));
            const source = await this.offline.reopen(saved.offline, valid);
            if (!valid()) return;
            this.bindOffline(source);
            await this.playback.playLocal(source.itemId, () => this.makeOfflineSession(source), effect.playback.positionSeconds,
              {initialIntent: 'paused', signal: controller.signal, isCurrent: valid});
            if (!valid()) return;
            const local = this.fence();
            if (!local || local.kind !== 'local') throw new Error(this.playback.getSnapshot().error || serviceText('downloads.offline.notPlayableHere'));
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
          else { this.playback.leave(); this.coordinator.reconsiderEngine(); }
        }
      }
    } catch (error) {
      if (epoch !== this.startEpoch || !this.currentAuthority()) return;
      throw error;
    } finally {
      if (this.transitionEpoch === epoch) { this.transitionEpoch = undefined; this.changed(); }
    }
    return restored && epoch === this.startEpoch ? {fence: restored, epoch} : undefined;
  };

  private restoreChannelPosition(fence: ChannelPlaybackFence, seconds: number, epoch: number, signal: AbortSignal): Promise<void> {
    const before = this.playback.getSnapshot();
    const previousSeek = before.linear?.desired.seek?.seekCommandId;
    return new Promise((resolve, reject) => {
      let off = () => {}, done = false;
      let seekId: string | undefined;
      let cancelTimer = () => {};
      const finish = (error?: Error) => {
        if (done) return; done = true; off(); cancelTimer(); signal.removeEventListener('abort', abort); error ? reject(error) : resolve();
      };
      const check = () => {
        const state = this.playback.getSnapshot(), current = this.fence(), linear = state.linear;
        // A server-resolved seek can change media generation. Its session, service run,
        // source and channel reference must still be the ones this restore actually opened.
        if (signal.aborted || epoch !== this.startEpoch || !this.currentAuthority() || current?.kind !== 'channel' || current.generation < fence.generation || !sameFence(current, {...fence, generation: current.generation})) return finish(new Error('stale_selection_context'));
        if (state.phase === 'error' || state.failedSeek || state.seekError || state.intent !== 'paused') return finish(new Error('channel_position_unavailable'));
        if (!linear || seconds < channelSeconds(linear.media.windowStartUs) || seconds > channelSeconds(linear.media.windowEndUs)) return finish(new Error('channel_position_unavailable'));
        const seek = linear.desired.seek;
        if (!seek || seek.seekCommandId === previousSeek || state.seekRevision <= before.seekRevision) return;
        if (seek.positionUs === null || Math.abs(channelSeconds(seek.positionUs) - seconds) > .001 || seekId !== undefined && seekId !== seek.seekCommandId) return finish(new Error('channel_position_unavailable'));
        seekId = seek.seekCommandId;
        if (seek.bufferGeneration === linear.media.bufferGeneration && linear.acknowledgedSeekCommandId === seekId && !state.pendingSeek && Math.abs(state.positionSeconds - seconds) < .25) finish();
      };
      const abort = () => finish(new Error('cancelled'));
      const timeout = () => finish(new Error('channel_position_unavailable'));
      const timers = this.o.timers;
      if (timers?.setTimer && timers.clearTimer) {
        const clear = timers.clearTimer, timer = timers.setTimer(timeout, 15000);
        cancelTimer = () => clear(timer);
      } else {
        const timer = setTimeout(timeout, 15000);
        cancelTimer = () => clearTimeout(timer);
      }
      if (done) { cancelTimer(); return; }
      off = this.playback.subscribe(check); signal.addEventListener('abort', abort, {once: true});
      if (signal.aborted) { abort(); return; }
      this.playback.seek(seconds); check();
    });
  }

  /** The native surface is an exclusive attachment; a second mounted Surface is rejected. */
  installSnapshotCapture(capture: VideoSnapshotCapture): void {
    if (this.snapshotCapture) throw new Error('Snapshot capture is already bound.');
    if (this.retired) { capture.dispose(); return; }
    this.snapshotCapture = capture;
  }
  setSurfaceTag(owner: object, tag: number | null): void {
    if (this.surfaceOwner && this.surfaceOwner !== owner || this.surfaceTagOwner && this.surfaceTagOwner !== owner) throw new Error('Playback surface is already mounted.');
    const next = tag !== null && Number.isSafeInteger(tag) && tag > 0 ? tag : undefined;
    if (this.surfaceTag !== next) this.cancelSnapshot();
    this.surfaceTag = next; this.surfaceTagOwner = next === undefined ? undefined : owner;
  }
  private snapshotSource(fence: PlaybackFence): VideoSnapshotSource | undefined {
    if (!this.matches(fence) || !this.surfaceOwner || this.surfaceTagOwner !== this.surfaceOwner || !this.surfaceTag) return;
    const state = this.playback.getSnapshot(), binding = this.coordinator.getBinding(), engine = this.coordinator.getEngine();
    if (state.nativeListening || !binding || binding.lease !== this.coordinator.getSnapshot() || binding.intentId !== state.intentId || binding.sessionId !== fence.sessionId || binding.generation !== fence.generation || binding.itemId !== state.itemId || !['native', 'mpv'].includes(engine)) return;
    return Object.freeze({reactTag: this.surfaceTag, engine: engine as 'native' | 'mpv', lease: binding.lease,
      binding: Object.freeze({intentId: binding.intentId, sessionId: binding.sessionId, generation: binding.generation, itemId: binding.itemId})});
  }
  isSnapshotSourceCurrent = (source: VideoSnapshotSource): boolean => {
    if (!this.captureFence || !this.currentAuthority()) return false;
    const actual = this.snapshotSource(this.captureFence);
    return !!actual && actual.reactTag === source.reactTag && actual.engine === source.engine && actual.lease === source.lease && actual.binding.intentId === source.binding.intentId && actual.binding.sessionId === source.binding.sessionId && actual.binding.generation === source.binding.generation && actual.binding.itemId === source.binding.itemId;
  };
  private cancelSnapshot(): void {
    ++this.captureEpoch; this.captureFence = undefined; this.captureSource = undefined; this.snapshotCapture?.cancel();
  }
  captureVideoSnapshot = async (fence: PlaybackFence, options?: {signal?: AbortSignal}): Promise<NativePlaybackVideoSnapshot> => {
    const source = this.snapshotSource(fence), capture = this.snapshotCapture;
    if (!source || !capture) throw new Error('snapshot_unavailable');
    if (this.captureSource) throw new Error('snapshot_busy');
    const epoch = ++this.captureEpoch;
    // Capture the original page/route/actor before any native await. No startEpoch:
    // replacing playback is allowed to leave passive pixels, replacing the page is not.
    let context: (() => boolean) | undefined;
    try {if (this.o.subscribeStartContext) context = this.o.captureStartContext?.();} catch { /* Ordinary capture remains available without passive-page proof. */ }
    const api = this.o.api, route = api.getRouteSnapshot();
    const connection = api.getRouteConnection(), pin = connection?.pin.publicKey, certificate = api.getCertificateSessionEpoch();
    const session = this.o.session(), principal = startupPrincipal(session), token = session?.accessToken, expires = session?.expiresAt, role = session?.viewer.role;
    const until = session ? Math.min(Date.parse(session.expiresAt), Date.parse(session.authorizationHorizon)) : NaN;
    const paintCurrent = () => this.currentAuthority() && !!context && context() === true
      && !!session && Number.isFinite(until) && Date.now() < until && startupPrincipal(this.o.session()) === principal
      && this.o.session()?.accessToken === token && this.o.session()?.expiresAt === expires && this.o.session()?.viewer.role === role
      && this.o.api === api && api.getRouteConnection() === connection && !!pin && connection?.pin.publicKey === pin
      && api.getCertificateSessionEpoch() === certificate && (route ? api.getRouteSnapshot()?.phase === 'ready'
        && api.getRouteSnapshot()?.revision === route.revision && api.getRouteSnapshot()?.origin === route.origin : api.getRouteSnapshot() === null);
    this.captureFence = fence; this.captureSource = source;
    try {
      const result = await capture.capture(source, options);
      if (epoch !== this.captureEpoch || !this.isSnapshotSourceCurrent(result.source)) { await result.release(); throw new Error('snapshot_stale'); }
      let released: Promise<void> | undefined;
      return Object.freeze({fence, image: result.image,
        retainForExit: () => retainNativeVideoExitImage(fence, {
          sourceCurrent: () => epoch === this.captureEpoch && this.isSnapshotSourceCurrent(source)
            && !!this.snapshot.dock.local && !this.snapshot.dock.local.expanded
            && ['video', 'live'].includes(this.snapshot.dock.local.kind) && sameFence(this.snapshot.dock.local.fence, fence),
          paintCurrent,
          subscribe: listener => {
            const offs: (() => void)[] = [];
            try {
              offs.push(this.subscribe(listener)); offs.push(this.o.subscribeAuthority(listener));
              if (this.o.subscribeStartContext) offs.push(this.o.subscribeStartContext(listener));
              offs.push(api.subscribeRoute(listener));
            } catch (error) {for (const off of offs) off();throw error;}
            return () => {for (const off of offs.splice(0)) off();};
          },
          retain: () => result.retainForExit?.() ?? Promise.resolve(undefined),
        }, this.videoExitImages),
        release: () => released ??= result.release().finally(() => { if (epoch === this.captureEpoch) { this.captureFence = undefined; this.captureSource = undefined; } })});
    } catch (error) {
      if (epoch === this.captureEpoch) { this.captureFence = undefined; this.captureSource = undefined; }
      throw error;
    }
  };
  attachSurface(owner: object): () => void {
    if (this.surfaceOwner && this.surfaceOwner !== owner) throw new Error('Playback surface is already mounted.');
    this.surfaceOwner = owner; this.connect();
    return () => { if (this.surfaceOwner === owner) { this.cancelSnapshot(); this.surfaceTag = undefined; this.surfaceTagOwner = undefined; this.surfaceOwner = undefined; queueMicrotask(() => { if (!this.surfaceOwner) this.dispose(); }); } };
  }
  /** The runtime's own lifetime: a Try again belongs to it, not to a page that may be gone. */
  private retryLifetime(): Readonly<{signal: AbortSignal; isCurrent: () => boolean}> {
    return {signal: this.startLife.signal, isCurrent: () => !this.retired && this.currentAuthority()};
  }
  /** O-159 for every other start path: drawn at once (generic until its title is known), bounded
   * by NATIVE_START_DEADLINE_MS from Play, and cancelled for real by Stop. A Live tune, as on web,
   * has no start deadline: it is drawn at tune acceptance, and the channel's own 60 s bound
   * (CHANNEL_STREAM_WAIT_MS) ends a stream that never comes up. A start that ends without a
   * session or fence was not accepted and is never reported as a silent no-op. */
  private async watchedStart<T>(display: NativeStartDisplay | undefined, retry: () => Promise<void>, run: () => Promise<T>, deadlineFrom: 'play' | 'none' = 'play'): Promise<T> {
    let stopped = false;
    const starting = this.beginStarting(display, retry, deadlineFrom);
    starting.stopWith(() => {stopped = true; this.beginOperation();});
    try {
      const result = await run();
      if (stopped) throw Object.assign(new Error('Playback start stopped'), {name: 'AbortError'});
      const fence = this.fence();
      if (!fence && !this.currentSession()) {starting.end(); return result;}
      starting.accepted(fence);
      starting.stopWith(() => {stopped = true; this.beginOperation(); if (fence) void this.stop(fence).catch(() => {});});
      return result;
    } catch (error) {
      starting.end();
      if (stopped) throw Object.assign(new Error('Playback start stopped'), {name: 'AbortError'});
      throw error;
    }
  }
  /** O-159, two clocks as on web. (a) By NATIVE_START_DRAWN_MS something is drawn: the starting
   * dock drawn at Play from the pressed card satisfies it at once. (b) By NATIVE_START_DEADLINE_MS
   * from Play the real dock or player replaces it in place, accepted or not; otherwise the start
   * is stopped, recorded locally and offered again. Slow buffering never trips it, because a drawn
   * real dock ends the watch, and a Stop-only dock never stays forever. */
  private beginStarting(display: NativeStartDisplay | undefined, retry: () => Promise<void>, deadlineFrom: 'play' | 'none' = 'play') {
    this.endStarting(); this.startTimedOut = undefined;
    const state: NonNullable<NativePlaybackRuntime['startingState']> = {key: String(++this.startingKeys), display, accepted: false, timer: undefined, drawnTimer: undefined, deadlineFrom, stop: () => {}, retry};
    state.drawnTimer = this.setStartTimer(() => this.checkStartDrawn(state), NATIVE_START_DRAWN_MS);
    if (deadlineFrom === 'play') state.timer = this.setStartTimer(() => this.expireStarting(state), NATIVE_START_DEADLINE_MS);
    this.startingState = state; if (this.snapshot) this.publish();
    const mine = () => this.startingState === state;
    return {
      stopWith: (stop: () => void) => {if (mine()) state.stop = stop;},
      accepted: (fence: PlaybackFence | undefined) => {if (mine()) {state.accepted = true; state.fence = fence; if (this.snapshot) this.publish();}},
      end: () => {if (mine()) {this.endStarting(); if (this.snapshot) this.publish();}},
    };
  }
  private endStarting(): void {
    const state = this.startingState;
    if (!state) return;
    this.startingState = undefined;
    this.clearStartTimer(state.timer); this.clearStartTimer(state.drawnTimer);
  }
  private setStartTimer(run: () => void, ms: number): unknown {
    return this.o.timers?.setTimer ? this.o.timers.setTimer(run, ms) : setTimeout(run, ms);
  }
  private clearStartTimer(timer: unknown): void {
    if (timer === undefined) return;
    if (this.o.timers?.clearTimer) this.o.timers.clearTimer(timer); else clearTimeout(timer as ReturnType<typeof setTimeout>);
  }
  /** Clock (a): every start draws its starting dock at once (a generic one when untitled), so this
   * only notes in the journal a start that somehow drew nothing; the deadline still bounds it. */
  private checkStartDrawn(state: NonNullable<NativePlaybackRuntime['startingState']>): void {
    if (this.startingState !== state || this.retired) return;
    state.drawnTimer = undefined;
    if (state.view || this.dock.getSnapshot().local) return;
    try {console.warn(`portico_playback_not_drawn ${JSON.stringify({code: 'playback_not_drawn_dock', kind: this.entry?.kind ?? this.subject?.kind ?? 'unknown'})}`);} catch { /* passive */ }
  }
  private expireStarting(state: NonNullable<NativePlaybackRuntime['startingState']>): void {
    if (this.startingState !== state || this.retired) return;
    const code = !state.accepted ? 'playback_start_timeout' : !this.currentSession() ? 'playback_not_drawn_session' : !this.options ? 'playback_not_drawn_offers' : 'playback_not_drawn_presentation';
    this.endStarting(); state.stop();
    // The local diagnostic (ErrorJournal) names the read that never became current.
    try {console.warn(`portico_playback_not_drawn ${JSON.stringify({code, kind: this.entry?.kind ?? this.subject?.kind ?? 'unknown'})}`);} catch { /* passive */ }
    const timedOut: NativePlaybackStartTimedOut = Object.freeze({key: state.key,
      retry: async () => {if (this.startTimedOut !== timedOut) return; this.startTimedOut = undefined; if (this.snapshot) this.publish(); await state.retry();},
      close: () => {if (this.startTimedOut === timedOut) {this.startTimedOut = undefined; if (this.snapshot) this.publish();}}});
    this.startTimedOut = timedOut; if (this.snapshot) this.publish();
  }
  /** Snapshot fields; the start's own fence on the real dock ends the watch (drawn). */
  private startingFields(fence: PlaybackFence | undefined): Partial<Pick<NativePlaybackRuntimeSnapshot, 'starting' | 'startTimedOut'>> {
    const state = this.startingState;
    const local = this.dock.getSnapshot().local;
    if (state?.accepted && local && fence && (!state.fence || sameFence(state.fence, fence)) && sameFence(local.fence, fence)) this.endStarting();
    const live = this.startingState;
    // A start that came without a card is named by what the runtime now knows it is playing;
    // until then it is drawn as a generic "Starting…" (with its kind's icon, when known).
    if (live && !live.display?.title) {
      const known = this.entry ? entryDisplay(this.entry) : this.subject?.kind === 'channel' && this.subject.channelName ? {title: this.subject.channelName} : undefined;
      if (known) live.display = {...(live.display?.icon ? {icon: live.display.icon} : {}), ...known};
      else live.display ??= {};
    }
    // A Live start the server reports recoverable says so while it keeps trying (never a silent "Starting…").
    const playback = this.playback.getSnapshot(), reason = playback.channel && playback.linearStalled ? 'channel_source' as const : undefined;
    if (live?.display && (live.view?.display !== live.display || live.view?.reason !== reason)) live.view = Object.freeze({key: live.key, display: live.display, ...(reason ? {reason} : {}), stop: () => {if (this.startingState === live) {this.endStarting(); live.stop(); if (this.snapshot) this.publish();}}});
    return {...(live?.view ? {starting: live.view} : {}), ...(this.startTimedOut ? {startTimedOut: this.startTimedOut} : {})};
  }
  dispose = (): void => {
    this.startLife.abort(); this.endStarting(); this.startTimedOut = undefined;
    if (this.retired) return;
    const state = this.playback.getSnapshot();
    // An ordinary shell unmount stops its exact server session at the settled position. On an
    // authority change, no request is sent with the next viewer's credentials: the old lease
    // expires server-side, while the engine and every old request are torn down immediately.
    if (state.session && !this.playback.hasNativeOwner() && this.authorityMatches()) {
      this.stopOwnedSession(state.session.id, Math.max(0, Math.round(state.positionSeconds * 1000)));
    }
    this.beginOperation(); this.retired = true; ++this.sourceEpoch; this.externalPresentationBinding = null;
    this.cancelSnapshot(); for (const release of this.videoExitImages) void release().catch(() => {}); this.snapshotCapture?.dispose();
    this.startController?.abort(); this.sourceController?.abort(); this.clearForcedImage(); this.http.dispose(); this.videoPolicy.dispose();
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
