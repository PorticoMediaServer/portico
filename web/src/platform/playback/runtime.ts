import {playbackFailurePermanent} from '@core/playback-messages.ts';
import {captureAppLogRecorder, type AppLogCode, type AppLogRecorder} from '../app-log';
import {advancePendingChoice, takePendingTrackChoice, type PendingChoiceProgress} from '@core/pending-track-choice.ts';
import {createWebQueueListPort, type WebQueueListPort, type WebQueueListLifetime} from './queue-list';
import {capacityRetryHttp, type WebPlaybackCapacityWait} from './capacity-retry';
import {startFailureHttp, type WebPlaybackStartFailure} from './start-failure';
import {PlaybackService, type PlaybackSnapshot} from '@core/index.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import type {ItemResponse} from '@core/media-item.ts';
import {parseMediaItem} from '@core/media-item.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import {V1QueuePlayer} from '@core/playback-v1/v1-queue-player.ts';
import {SessionsClient} from '@core/playback-v1/sessions.ts';
import type {CatalogueSelectionQueuePort} from '@core/catalogue-selection.ts';
import {QueueClient, type SegmentInput} from '@core/playback-v1/queue.ts';
import {V1ChannelControl} from '@core/playback-v1/channel-control.ts';
import {ownedWebChannels} from './channel-start-binding.ts';
import type {WebStartRegistration} from './preparing-start.ts';
import type {HomeSelectionPlaybackPort, HomeSelectionPlaybackRequest} from '@core/home-selection/types.ts';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {HomeChannel} from '@core/home.ts';
import type {GuideChannel} from '@core/channel-guide.ts';
import type {GroupSessionService} from '@core/group-session.ts';
import type {GroupTransportCommand} from '@core/social-playback.ts';
import {bindRuntimeGroup, type RuntimeGroupBinding} from './group-binding.ts';
import {localPlaybackScope, sameLocalScope, type LocalPlaybackFence, type SkipMode} from '../../../../packages/player-core/src/index.ts';
import {WebLocalQueue, captureLocalQueue, type WebLocalQueuePreparation, type WebLocalQueueResource, type WebLocalQueueMember} from './local-queue.ts';
import {listeningRates, audiobookRates} from '@core/listening.ts';
import {idempotencyKey, type V1Http} from '@core/playback-v1/http.ts';
import {CapabilityPublisher, PlaybackOptionsClient} from '@core/playback-v1/options.ts';
import {audioStreamIndex, qualityChoiceOf, qualityRequest, offeredQualities, type QualityChoice} from '@core/playback-v1/choices.ts';
import {PlayerOffersService, type PlayerOffersData} from '@core/player-offers.ts';
import {SubtitleService, fetchSubtitleDocument, type SubtitleDocument, type SubtitlePlan, type SubtitleSnapshot} from '@core/subtitles.ts';
import {DrawnSubtitleRetry, revalidateTextSubtitles, subtitleFetch} from './subtitle-retry.ts';
import type {SubtitlePreferences} from '@core/preference-effects.ts';
import type {PlaybackOptions, QueueHeader, Session} from '@core/playback-v1/types.ts';
import type {PlaybackSubject} from '@core/presentation/kit-model/index.ts';
import {PlaybackDock, PlaybackPolicy, PlayerSessionChoices, playbackFence, queueScope, sameFence, type DockPlayback, type DockTransition, type PlaybackFence, type PolicyCommand, type PolicyView, type QueuePlaybackFence, type ChannelPlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {WebPlayback} from './web-playback.ts';
import {DecodeAudioRender} from './DecodeAudioRender.ts';
import {loadDeviceEqualizer, subscribeDeviceEqualizer} from './audio/equalizer-store.ts';
import type {EqualizerSettings} from './audio/equalizer.ts';
import type {EqualizerPort} from '../../kit/media/equalizer-port.ts';
import {ByteCache} from './audio/bytes.ts';
import {airPlaySourceReady, wirelessPlayback} from './airplay.ts';
import {browserNativeHlsPolicy} from './native-hls.ts';
import {documentVisibility} from './page-visibility.ts';
import {SubtitleSurface, type SubtitleHost} from './subtitle-surface.ts';
import {createDrawnSubtitleSurface, type DrawnSubtitleSurface} from './subtitles/drawn-surface.ts';
import {decodeSubtitleText, subtitleFileLimits, type DrawnSubtitleSource, type ReadSubtitleFile} from './subtitles/types.ts';
import {assPlainCues} from './subtitles/ass-text.ts';
import {SUBTITLE_CJK_FONT_PATH, forcedChoiceOf, parseSubtitleLines, readSubtitleFile, readSubtitleFontList, selectedSubtitleOriginal, type SubtitleLines, type SubtitleOriginal} from '@core/subtitle-originals.ts';
import {entryStartDisplay, entrySubject, itemSubject, queueEntrySubject} from './subject.ts';
import {playbackEnvironment} from './environment.ts';
import {browserClientProfile} from './client-profile.ts';
import {selectionSegments, selectionStartSeconds} from './selection.ts';
import {captureQueueSource, type WebPlaybackQueueSource} from './queue-source.ts';
import {createWebListeningPort, type WebListeningLifetime, type WebListeningPort} from './listening-port.ts';
import {admitCommand, createQueueEditOpening, localEditOutput} from './queue-edit-adapter.ts';
import type {WebQueueListSnapshot} from './queue-list.ts';
import {PlayerChaptersService} from '@core/player-chapters.ts';
import {LyricsService} from '@core/lyrics.ts';
import {currentPolicyQueue, type WebPlaybackPolicyAction} from './policy.ts';
import type {WebPlaybackVersionChoices, WebPlaybackVersionChoice} from './versions.ts';
import type {VideoLike} from './web-media-player.ts';
import type {WebPlaybackInfoRead, WebPlaybackAction, WebPlaybackAuthority, WebPlaybackEnvironment, WebPlaybackPresentation, WebPlaybackRuntimeSnapshot, WebPlaybackSource, WebPlaybackStart, WebPlaybackVersionStart, WebPlaybackScreenTarget, WebPlaybackScreenLifetime, WebPlaybackStartDisplay, WebPlaybackStarting, WebPlaybackStartTimedOut} from './types.ts';

/** How long a Play may wait for its playback to start before it is a reported failure. */
const START_DEADLINE_MS = 20_000;

type ChannelTarget = Readonly<{channel: GuideChannel}>;
type LocalQueueLease = {preparation: WebLocalQueuePreparation; release(): Promise<void>; retired: boolean; released: boolean};
type Bookmark = Readonly<{playback: DockPlayback; subject: PlaybackSubject; queueId?: string; libraryId?: string; channel?: ChannelTarget; posterUrl?: string; backdropUrl?: string; durationSeconds: number; rate: number;
  local?: Readonly<{lease: LocalQueueLease; index: number; positionMs: number}>}>;
type OfferBinding = {
  readonly api: WebPlaybackSource['api']; readonly authority: WebPlaybackAuthority;
  readonly sessionKey: string; readonly libraryId: string; readonly itemId: string;
  readonly sessionId: string; readonly generation: number; readonly playbackRun: number;
  detailFence?: string;
};
type Starting = {attach(stack: Stack): void; identify(next: WebPlaybackStartDisplay): void; stopWith(stop: () => void): void; accepted(): void; end(): void};
type StartingState = {key: string; display?: WebPlaybackStartDisplay; accepted: boolean; stack?: Stack; stop: () => void; retry: () => Promise<void>; timer: unknown; view?: WebPlaybackStarting};
type Stack = {
  airPlay?: {key: string; abort: AbortController; receipt?: Awaited<ReturnType<SessionsClient["prepareAirPlay"]>>; failed?: boolean};
  startFailure?: WebPlaybackStartFailure;
  capacityWait?: WebPlaybackCapacityWait;
  capacityCurrent?: () => boolean;
  preplay?: Readonly<{versionId: string; progress: PendingChoiceProgress}>;
  mode: 'server' | 'local'; local?: Readonly<{controller: WebLocalQueue; lease: LocalQueueLease}>;
  capabilityRejected?: boolean;
  web: WebPlayback; service: PlaybackService; queue: V1QueuePlayer; http: V1Http;
  life: AbortController; offers: PlayerOffersService; optionsClient: PlaybackOptionsClient;
  offs: (() => void)[]; unmount?: () => void; subtitles?: SubtitleService; offSubtitles?: () => void;
  subtitleKey?: string; documentKey?: string; documentAbort?: AbortController; documentTimer?: unknown;
  /** The overlay this stack draws a styled (ASS) or image (PGS/VobSub/DVB) track on. */
  drawnSurface?: DrawnSubtitleSurface;
  /** A styled track shown as plain lines on the text surface ("Styled subtitles" off). */
  drawnPlain?: boolean;
  /** Tracks that could not be drawn here in this playback run: they are asked for in the picture. */
  undrawable?: Readonly<{run: number; ids: ReadonlySet<string>}>;
  drawnKey?: string;
  drawnAbort?: AbortController;
  drawnRetry?: DrawnSubtitleRetry;
  /** This playback's temporary forced-only choice, gated by the offered lines of one drawn image load. */
  forcedOnly?: boolean;
  forcedLines?: Readonly<{lines: SubtitleLines; key: string; epoch: number}>;
  drawnEpoch?: number;
  drawnFailed?: boolean;
  /** The one track id this runtime told the player it is drawing, and which path wrote it. */
  subtitleRecorded?: Readonly<{id: string | null; path: 'text' | 'drawn'; key: string}>;
  /** A style change made in the options panel, for the one playback run it belongs to. */
  subtitleStyle?: Partial<Pick<SubtitlePreferences,'size'|'background'>> & Readonly<{run: number}>;
  /** The drawn sleep timer: one playback run's record, with the token that armed it. */
  sleepTimer?: Readonly<{minutes: number; endsAt: number; timer: unknown; run: number; sessionId: string; token: object}>;
  sessionKey?: string; metadataAbort?: AbortController; options: PlaybackOptions | null;
  offerBinding?: OfferBinding;
  itemId?: string; metadataItemId?: string; libraryId?: string; subject?: PlaybackSubject; showId?: string; seasonId?: string;
  posterUrl?: string; backdropUrl?: string; expanded: boolean; retired: boolean;
  channel?: ChannelTarget;
  channelStarts?: WebStartRegistration;
  confirmedEnd?: Readonly<{sessionId: string; generation: number; intentId: number; view: ReturnType<V1QueuePlayer['getSnapshot']>['view']}>;
};
const emptySubtitle: SubtitleSnapshot = {catalog: null, plan: null, busy: false, loading: false, suppressed: true, error: null, canRetry: false, candidates: []};
function problem(code: string): never { throw new Error(code); }
/** Stop on the starting dock is the viewer's own cancel: never reported as a failure. */
const startStopped = () => new DOMException('Playback start stopped', 'AbortError');
const validSeconds = (value: number) => Number.isFinite(value) && value >= 0 && value <= 1e9;
const compatibleVideo = (value: VideoLike): value is VideoLike & SubtitleHost => typeof (value as Partial<SubtitleHost>).addTextTrack === 'function' && !!(value as Partial<SubtitleHost>).textTracks;

/** A downloaded title's own stored facts, as the playback subject its dock names. */
function localMemberSubject(member: WebLocalQueueMember): Exclude<PlaybackSubject, {kind: 'channel'}> {
  const title = member.title, kind = title.kind as 'movie' | 'episode' | 'extra' | 'song' | 'audiobook_file';
  const item = {id: member.itemId, kind, title: title.title, durationSeconds: member.durationMs / 1000,
    ...(title.year > 0 ? {year: title.year} : {}), ...(title.artist ? {artist: title.artist} : {}),
    ...(kind === 'song' && title.parentTitle ? {album: title.parentTitle} : {}),
    ...(kind === 'episode' ? {show: {id: title.parentId, title: title.parentTitle}, episode: {mode: 'seasonal' as const,
      ...(title.groupNumber === null ? {} : {season: title.groupNumber}), ...(title.number === null ? {} : {episode: title.number})}} : {})};
  return {kind: kind === 'song' ? 'music' : kind === 'audiobook_file' ? 'audiobook' : 'video', item};
}
/** The first downloaded title is the one the start loads; a kind this client doesn't know isn't named. */
function localStartDisplay(member: WebLocalQueueMember | undefined): WebPlaybackStartDisplay | undefined {
  return member && member.title.title && ['movie', 'episode', 'extra', 'song', 'audiobook_file'].includes(member.title.kind) ? {subject: localMemberSubject(member)} : undefined;
}

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
    const inner = localV1Http(source.api);
    this.channelControl = new V1ChannelControl({events: source.events, http: {send: request => inner.send(request.method === 'DELETE' ? request : {...request, signal: request.signal ? AbortSignal.any([request.signal, this.life.signal]) : this.life.signal})}, key: environment.key, now: environment.timers?.now});
    // This is the exact device-queue scope V1QueuePlayer exposes; these are its existing v1
    // controller values, never a Cast/receiver identity or a newly invented server resource.
    this.dock = new PlaybackDock(queueScope({...source.authority, controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'}), localScope);
    this.stack = this.makeStack();
    this.activate(this.stack);
    this.sync();
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
  private current(stack = this.stack): boolean { return !this.disposed && !stack.retired && stack === this.stack && this.source.isCurrent(); }
  private key(fence: PlaybackFence): string { return JSON.stringify(playbackFence(fence)); }
  private assertCurrent(): void { if (!this.current()) problem('playback_authority_retired'); }
  private setTimer(fn: () => void, ms: number): unknown { return this.env.timers?.setTimer?.(fn, ms) ?? setTimeout(fn, ms); }
  private clearTimer(timer: unknown): void { if (timer === undefined) return; if (this.env.timers?.clearTimer) this.env.timers.clearTimer(timer); else clearTimeout(timer as ReturnType<typeof setTimeout>); }

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
      http, mediaUrl: path => this.source.api.mediaUrl(path),
      subscribeRoute: this.source.api.subscribeRoute, getRouteSnapshot: this.source.api.getRouteSnapshot,
      recoverRoute: () => this.source.api.getRouteConnection() ? this.source.api.recoverRoute() : undefined,
      farewell: (path, method, body) => this.source.api.farewell(path, method, body),
      loadHls: this.env.loadHls ?? (() => import('hls.js').then(module => module.default as unknown as import('./web-media-player.ts').HlsModule)),
      nativeHls: this.env.nativeHls ?? browserNativeHlsPolicy(), timers: this.env.timers, playerTimers: this.env.playerTimers, key: this.env.key,
      deviceMemory: typeof navigator === 'undefined' ? undefined : (navigator as Navigator & {deviceMemory?: number}).deviceMemory,
      createPlayer: this.env.createPlayer,
      createRender: this.env.createRender === null ? undefined : this.env.createRender ?? ((service, element) => new DecodeAudioRender(service, path => this.source.api.mediaUrl(path), element as HTMLVideoElement, (current, next) => !!current && (service.getQueueController()?.sameAlbum?.(current, next) ?? false), this.audioCache, this.recordDiagnostic)),
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
    const queue = new V1QueuePlayer({http, api: web.transport, playback: service, viewer: this.source.authority, events: this.source.events, key: this.env.key, musicDefaults: this.source.settings.musicDefaults, audiobookPreferences: this.source.settings.audiobookPreferences, quality: this.source.settings.defaultQuality, retainQueueIds: () => this.retainedQueueIds(stackReference),
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
    return stackReference;
  }

  private activate(stack: Stack): void {
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
    this.surface?.applyPreferences(this.subtitlePreferences(this.stack));
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
    this.clearSleepTimer(stack);
    this.clearDrawnSubtitles(stack);
    this.clearRecordedSidecar(stack, 'text');
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
      || !validSeconds(captured.startSeconds)) problem('invalid_playback_target');
    const serial = ++this.operation, previous = this.stack;
    const finishRetention = this.retainMediaStart(context.signal, () => serial === this.operation);
    const starting = this.beginStarting(captured.display, () => this.playFromScreen(captured, this.retryLifetime())), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    try {
      const reading = AbortSignal.any([context.signal, previous.life.signal, this.life.signal, stopped.signal]);
      const raw = await this.source.api.request<ItemResponse>(encodedRoutePath('get_items_id', {id: encodeURIComponent(captured.itemId)}), 'GET', undefined, reading);
      check();
      if (!this.current(previous) || serial !== this.operation) problem('stale_selection_context');
      const item = parseMediaItem(raw);
      if (item.id !== captured.itemId || item.libraryId !== captured.libraryId || !item.available || raw.missing) problem('invalid_playback_target');
      const subject = itemSubject(raw);
      starting.identify({subject, ...(item.posterUrl ?? item.backdropUrl ? {posterUrl: item.posterUrl ?? item.backdropUrl} : {})});
      if (captured.storedResume !== undefined && (typeof captured.storedResume !== 'boolean' || captured.storedResume && (subject.kind !== 'audiobook' || captured.versionId !== undefined))) problem('invalid_playback_target');
      const options = await previous.optionsClient.options(item.id, {}, reading);
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
      const deadline = this.startDeadline();
      const lifetime = AbortSignal.any([context.signal, stack.life.signal, this.life.signal, deadline.signal, stopped.signal]);
      const current = () => {
        if (lifetime.aborted || !this.current(stack) || serial !== this.operation) return false;
        try {check();return true;} catch {return false;}
      };
      const cancel = () => { if (this.current(stack) && serial === this.operation) {
        this.replacing = true; this.retire(stack); this.stack = this.makeStack(); this.activate(this.stack); this.replacing = false; this.sync();
      } };
      lifetime.addEventListener('abort', cancel, {once: true});
      try {
        stack.capacityCurrent = current;
        await stack.queue.playItem(item.id, startSeconds, undefined, undefined, undefined,
          {...(versionId ? {versionId} : {}), ...(audio ? {audioTrackId: audio.id} : {})}, {signal: lifetime, isCurrent: current});
        if (!current()) problem(deadline.expired() && !context.signal.aborted ? 'playback_start_timeout' : 'stale_selection_context');
        const session = stack.queue.currentSession();
        if (stack.service.getSnapshot().phase === 'error' || session?.itemId !== item.id
          || versionId !== undefined && session.versionId !== versionId) problem('playback_start_failed');
        // Accepted: the watch now runs until the real dock is drawn, and Stop ends this session.
        starting.accepted(); starting.stopWith(cancel);
        this.sync();
      } catch (error) {cancel(); if (deadline.expired() && !context.signal.aborted && !stopped.signal.aborted) problem('playback_start_timeout'); throw error;}
      finally {deadline.clear(); if (stack.capacityCurrent === current) stack.capacityCurrent = undefined; lifetime.removeEventListener('abort', cancel);}
    } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
    finally {finishRetention();}
  }

  /** Canonical room follower and local corrections share this runtime's one actual engine. */
  bindGroup(service: GroupSessionService, isCurrent: () => boolean, reportError: (error: unknown) => void): () => void {
    this.assertCurrent();if (this.group) problem('group_playback_already_bound');
    const currentGroup = () => {const value = service.getSnapshot();return this.current() && isCurrent() && (value.phase === 'live' || value.phase === 'reconnecting') ? value.group : undefined;};
    const owner = bindRuntimeGroup({service, isCurrent: () => this.current() && isCurrent(), reportError, changed: this.sync,
      player: {
        state: () => {const value = this.getSnapshot().playback;return {itemId: value.itemId ?? '', positionSeconds: value.positionSeconds,
          playing: value.phase === 'ready' && value.intent === 'playing' && !value.audioEffects.waiting, ready: value.phase === 'ready',
          buffering: value.phase === 'starting' || !!value.recovering || !!value.audioEffects.waiting, seeking: !!value.pendingSeek,
          ...(value.duration > 0 ? {durationSeconds: value.duration} : {})};},
        play: () => {if (currentGroup()?.timeline.itemId === this.stack.service.getSnapshot().itemId) this.stack.service.resume();},
        pause: () => {if (currentGroup()?.timeline.itemId === this.stack.service.getSnapshot().itemId) this.stack.service.pause();},
        seek: seconds => {if (currentGroup()?.timeline.itemId === this.stack.service.getSnapshot().itemId) this.stack.service.seek(seconds);},
        setRate: rate => {if (this.current() && Number.isFinite(rate) && rate >= .25 && rate <= 4) {this.groupRate = rate;this.stack.service.setPlaybackRate(rate);this.stack.web.setRate(rate);this.sync();}},
      },
      load: (itemId, seconds, playing, context) => this.playGroupItem(itemId, seconds, playing, context),
    });
    this.group = owner;this.groupServices.set(owner, service);const off = service.subscribe(this.sync);this.sync();
    const detach = () => {if (this.group !== owner) return;off();this.group = undefined;this.detachGroup = undefined;owner.dispose();this.groupRate = 1;if (this.current()) this.sync();};
    this.detachGroup = detach;return detach;
  }
  private async playGroupItem(itemId: string, seconds: number, playing: boolean, context: WebPlaybackScreenLifetime): Promise<void> {
    this.assertCurrent();if (!/^[A-Za-z0-9_.-]{1,128}$/.test(itemId) || !validSeconds(seconds) || context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
    const serial = ++this.operation, previous = this.stack;
    const finishRetention = this.retainMediaStart(context.signal, () => serial === this.operation);
    try {
      const raw = await this.source.api.request<ItemResponse>(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}), 'GET', undefined, AbortSignal.any([context.signal, previous.life.signal, this.life.signal]));
      if (!this.current(previous) || serial !== this.operation || context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
      const item = parseMediaItem(raw);if (item.id !== itemId || !item.available || raw.missing) problem('invalid_playback_target');
      const stack = this.replace();stack.itemId = item.id;stack.libraryId = item.libraryId;stack.subject = itemSubject(raw);stack.posterUrl = item.posterUrl;stack.backdropUrl = item.backdropUrl;stack.expanded = stack.subject.kind !== 'music';
      const signal = AbortSignal.any([context.signal, stack.life.signal, this.life.signal]);
      const current = () => !signal.aborted && this.current(stack) && serial === this.operation && context.isCurrent();
      const cancel = () => {if (this.current(stack) && serial === this.operation) stack.service.leave();};
      signal.addEventListener('abort', cancel, {once: true});
      try {
        stack.capacityCurrent = current;
        await stack.queue.playItem(itemId, seconds, undefined, undefined, undefined, undefined, {signal, isCurrent: current, state: playing ? 'playing' : 'paused'});
        if (!current()) problem('stale_selection_context');
        if (stack.queue.currentSession()?.itemId !== itemId || stack.service.getSnapshot().phase === 'error') problem('group_playback_failed');
        this.sync();
      } catch (error) {cancel();throw error;} finally {if (stack.capacityCurrent === current) stack.capacityCurrent = undefined;signal.removeEventListener('abort', cancel);}
    } finally {finishRetention();}
  }
  private async groupTransport(command: GroupTransportCommand, fence: PlaybackFence, seconds?: number): Promise<boolean> {
    if (!this.group?.matches(fence)) return false;
    this.require(fence);
    const group = this.group.active();if (!group?.permissions.canControl) problem('group_control_forbidden');
    // The screen's service owns its actual expected revision and refusal/readback path.
    const service = this.groupServices.get(this.group);if (!service) problem('group_playback_unavailable');
    if (!await service.transport(command, seconds === undefined ? {} : {positionUs: String(Math.round(seconds * 1e6))})) problem('group_transport_refused');return true;
  }
  /** Current room occurrence only; equal title IDs do not prove a room's source was adopted. */
  groupPlaybackMatches(fence: PlaybackFence, groupId: string, entryId: string): boolean {
    const active = this.group?.active();
    return this.matches(fence) && !!this.group?.matches(fence) && active?.id === groupId && active.timeline.currentEntryId === entryId;
  }
  private readonly groupServices = new WeakMap<RuntimeGroupBinding, GroupSessionService>();
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

  /** Info reads the current committed report afresh. It neither adopts a session nor
   * substitutes capabilities/options for source, delivery or renderer measurements. */
  async readPlaybackInfo(expected:QueuePlaybackFence,signal:AbortSignal):Promise<WebPlaybackInfoRead|null> {
    if(expected.kind!=='queue'||signal.aborted)return null;
    const fence=playbackFence(expected),stack=this.require(fence),captured=this.snapshot.session;
    if(stack.mode!=='server'||!captured||captured.kind!=='vod'||captured.state==='ended'
      ||captured.id!==fence.sessionId||captured.itemId!==fence.itemId
      ||captured.presentation.generation!==fence.generation||captured.queue?.entryId!==fence.entryId)return null;
    const isCurrent=()=>!signal.aborted&&this.current(stack)&&this.matches(fence)
      &&this.snapshot.session?.revision===captured.revision
      &&this.snapshot.session?.state!=='ended';
    if(!isCurrent())return null;
    const retired=new AbortController(),off=this.subscribe(()=>{if(!isCurrent())retired.abort();});
    try {
      const fresh=await new SessionsClient(stack.http).get(fence.sessionId,AbortSignal.any([signal,stack.life.signal,this.life.signal,retired.signal]));
      if(!isCurrent()||fresh.id!==captured.id||fresh.revision!==captured.revision
        ||fresh.kind!==captured.kind||fresh.role!==captured.role||fresh.state==='ended'
        ||fresh.itemId!==fence.itemId||fresh.versionId!==captured.versionId
        ||fresh.presentation.generation!==fence.generation
        ||fresh.queue?.queueId!==captured.queue?.queueId||fresh.queue?.entryId!==fence.entryId)return null;
      return Object.freeze({fence,sessionRevision:fresh.revision,
        ...(fresh.playbackInfo!==undefined?{playbackInfo:fresh.playbackInfo}:{}),
        ...(fresh.observedPlayback!==undefined?{observedPlayback:fresh.observedPlayback}:{}),isCurrent});
    } finally {off();}
  }

  /** A chooser reads real current options without stopping the playing source. Its owner keeps
   * this read alive until the chosen start settles, then closes/disposes the chooser. */
  async readVersions(expected: QueuePlaybackFence, signal: AbortSignal, {excludeFailed = false}: Readonly<{excludeFailed?: boolean}> = {}): Promise<WebPlaybackVersionChoices | null> {
    if (expected.kind !== 'queue') return null;
    const fence = playbackFence(expected), stack = this.require(fence), selectedVersionId = this.snapshot.session?.versionId;
    if (stack.subject?.kind !== 'video' || !selectedVersionId || signal.aborted) return null;
    const playback = stack.service.getSnapshot(), failed = playback.phase === 'error' && !playback.ended;
    if (excludeFailed && !failed) return null;
    const failedVersionId = failed ? selectedVersionId : undefined;
    const isCurrent = () => !signal.aborted && this.current(stack) && this.matches(fence)
      && this.snapshot.session?.versionId === selectedVersionId
      && (stack.service.getSnapshot().phase === 'error' && !stack.service.getSnapshot().ended) === failed;
    const options = await stack.optionsClient.options(fence.itemId, {}, AbortSignal.any([signal, stack.life.signal]));
    if (!isCurrent()) return null;
    if (options.itemId !== fence.itemId) problem('playback_version_unavailable');
    const versions = Object.freeze(options.versions.filter(version => !excludeFailed || version.id !== failedVersionId));
    const choices = Object.freeze({fence, options, versions, selectedVersionId, ...(failedVersionId ? {failedVersionId} : {}), isCurrent});
    this.versionChoices.set(choices, {isCurrent, signal, ...(excludeFailed ? {failedVersionId} : {})});
    return choices;
  }

  /** Both entry menus and the in-player chooser use this same actual fresh start. Section21
   * always resumes from the new options read; no caller position or version preference survives. */
  async playVersion(request: WebPlaybackVersionChoice): Promise<void>;
  async playVersion(request: WebPlaybackVersionStart, isCurrent: (context: SelectionContext) => boolean): Promise<void>;
  async playVersion(request: WebPlaybackVersionStart | WebPlaybackVersionChoice, isCurrent?: (context: SelectionContext) => boolean): Promise<void> {
    const {versionId, signal} = request;
    const selection = 'choices' in request ? this.versionChoices.get(request.choices) : undefined;
    const itemId = 'choices' in request ? request.choices.fence.itemId : request.itemId;
    const check = () => {
      if ('choices' in request) { if (!selection?.isCurrent() || signal.aborted) problem('stale_playback_command'); }
      else { if (!isCurrent) problem('stale_selection_context'); this.selectionCurrent(request.context, signal, isCurrent); }
    };
    check();
    await this.startVersion({itemId, versionId, signal, check, selection, failedVersionId: selection?.failedVersionId,
      // The accepted new output intentionally retires the old chooser fence. Only its original
      // read/action lifetime and genuine parent authority remain command prerequisites here.
      valid: lifetime => {
        if ('choices' in request) return true;
        try { this.selectionCurrent(request.context, lifetime, isCurrent!); return true; } catch { return false; }
      },
      accepted: lifetime => {
        if ('choices' in request) { if (lifetime.aborted) problem('stale_playback_command'); }
        else this.selectionCurrent(request.context, signal, isCurrent!);
      }});
  }
  private async startVersion(a: Readonly<{itemId: string; versionId: string; signal: AbortSignal; check: () => void; selection?: Readonly<{signal: AbortSignal}>; failedVersionId?: string;
    valid: (lifetime: AbortSignal) => boolean; accepted: (lifetime: AbortSignal) => void}>): Promise<void> {
    const {itemId, versionId, signal, check} = a;
    if (![itemId, versionId].every(id => /^[A-Za-z0-9_.-]{1,128}$/.test(id))) problem('invalid_playback_target');
    const serial = ++this.operation, previous = this.stack;
    const finishRetention = this.retainMediaStart(signal, () => serial === this.operation);
    // A different version of the title already playing is the same title: its facts are truthful.
    // Anything else is named by its own queue entry once one exists; no title is invented.
    const playing = previous.itemId === itemId && previous.subject && previous.subject.kind !== 'channel' ? previous.subject : undefined;
    const posterUrl = previous.posterUrl ?? previous.backdropUrl;
    const retry = async () => {
      const life = this.retryLifetime();
      await this.startVersion({itemId, versionId, signal: life.signal, check: () => {this.assertCurrent(); if (life.signal.aborted || !life.isCurrent()) problem('stale_playback_command');},
        valid: () => true, accepted: lifetime => {if (lifetime.aborted) problem('stale_playback_command');}});
    };
    const starting = this.beginStarting(playing && {subject: playing, ...(posterUrl ? {posterUrl} : {})}, retry), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    try {
      const options = await previous.optionsClient.options(itemId, {}, AbortSignal.any([signal, previous.life.signal, stopped.signal]));
      check();
      if (serial !== this.operation || !this.current(previous)) problem('stale_selection_context');
      // options.versions is readAssets(available=1) grouped by versionsOf on the server.
      if (options.itemId !== itemId || !options.versions.some(version => version.id === versionId) || versionId === a.failedVersionId) problem('playback_version_unavailable');
      const startSeconds = options.resume ? options.resume.positionMs / 1000 : 0;
      if (!validSeconds(startSeconds)) problem('invalid_playback_position');
      const stack = this.replace(); stack.expanded = true;
      starting.attach(stack);
      const lifetime = AbortSignal.any([signal, ...(a.selection ? [a.selection.signal] : []), stack.life.signal, stopped.signal]);
      const current = () => lifetime.aborted || !this.current(stack) || serial !== this.operation ? false : a.valid(lifetime);
      const cancel = () => { if (this.current(stack) && serial === this.operation) {
        this.replacing = true; this.retire(stack); this.stack = this.makeStack(); this.activate(this.stack); this.replacing = false; this.sync();
      } };
      lifetime.addEventListener('abort', cancel, {once: true});
      try {
        stack.capacityCurrent = current;
        await stack.queue.playItem(itemId, startSeconds, undefined, undefined, undefined, {versionId}, {signal: lifetime, isCurrent: current});
        a.accepted(lifetime);
        if (!this.current(stack) || serial !== this.operation) problem('stale_selection_context');
        const started = stack.service.getSnapshot(), session = stack.queue.currentSession();
        if (started.phase === 'error' || !session || session.itemId !== itemId || session.versionId !== versionId
          || started.session?.id !== session.id || started.session.generation !== session.presentation.generation) problem('playback_version_failed');
        starting.accepted(); starting.stopWith(cancel);
        this.sync();
      } catch (error) { cancel(); throw error; }
      finally {if (stack.capacityCurrent === current) stack.capacityCurrent = undefined; lifetime.removeEventListener('abort', cancel);}
    } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
    finally {finishRetention();}
  }

  /** Start the whole genuine browse/resource selection. Never expand a visible page locally.
   * The immutable captured selector and original screen lifetime reach server POST and adopt. */
  /** Canonical catalogue selections supply immutable, capability-checked segments.
   * This port remains owned by the same viewer runtime and real queue/media stack. */
  cataloguePlayback(): Pick<V1QueuePlayer, 'playSegments'> {
    this.assertCurrent();
    this.cataloguePort ??= Object.freeze<Pick<V1QueuePlayer, 'playSegments'>>({
      playSegments: async (segments, options = {}) => {
        if (!options.signal || typeof options.isCurrent !== 'function') problem('stale_selection_context');
        return this.playQueueSegments(segments, {signal: options.signal, isCurrent: options.isCurrent}, options.startSeconds);
      },
    });
    return this.cataloguePort;
  }

  /** Append checked catalogue segments without inventing a Home document context. */
  catalogueQueue(): CatalogueSelectionQueuePort | undefined {
    this.assertCurrent();
    if (this.stack.mode !== 'server' || this.stack.local || this.stack.channel || this.group || this.dock.getSnapshot().remote) return undefined;
    return {enqueueSegments: async (segments, placement, lifetime) => {
      if (this.catalogueEnqueuing) problem('queue_busy');
      this.catalogueEnqueuing = true;
      let finishRetention = () => {};
      try {
        const stack = this.stack, serial = this.operation;
        let expectedQueueId = stack.queue.getSnapshot().view?.queue.id;
        const check = () => {
          this.assertCurrent();
          if (stack.mode !== 'server' || stack.local || stack.channel || this.group || this.dock.getSnapshot().remote
            || serial !== this.operation || stack.queue.getSnapshot().view?.queue.id !== expectedQueueId) problem('queue_control_unavailable');
          if (lifetime.signal.aborted || !lifetime.isCurrent() || !this.current(stack)) problem('stale_selection_context');
        };
        check();
        if (!['next', 'end'].includes(placement) || !segments.length || segments.length > 256) problem('invalid_queue_placement');
        finishRetention = this.retainMediaStart(lifetime.signal, () => serial === this.operation);
        const signal = AbortSignal.any([lifetime.signal, stack.life.signal, this.life.signal]);
        const client = new QueueClient({send: value => {
          check();
          return stack.http.send({...value, signal: value.signal ? AbortSignal.any([value.signal, signal]) : signal});
        }}, this.env.key);
        const header = stack.queue.getSnapshot().view?.queue;
        if (!header) {
          if (stack.queue.getSnapshot().phase === 'preparing' || stack.service.getSnapshot().session) problem('queue_not_ready');
          const created = await client.create(segments, {signal, retainQueueIds: this.retainedQueueIds(stack)});
          check(); await stack.queue.open(created.queue.id, signal, () => {check(); return true;});
          expectedQueueId = created.queue.id; check(); this.sync(); return created.queue;
        }
        let queue = await client.header(header.id, signal); check();
        // Each next insertion is after current; reverse requests preserve pick order.
        for (const segment of placement === 'next' ? [...segments].reverse() : segments) {
          check(); queue = (await client.addSegment(queue.id, queue.revision, placement, segment)).queue; check();
        }
        await stack.queue.open(queue.id, signal, () => {check(); return true;}); check(); this.sync(); return queue;
      } finally {this.catalogueEnqueuing = false; finishRetention();}
    }};
  }

  async playFromQueue(source: WebPlaybackQueueSource, context: WebPlaybackScreenLifetime, options: Readonly<{shuffle?: boolean; display?: WebPlaybackStartDisplay}> = {}): Promise<void> {
    this.assertCurrent();
    if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
    const captured = captureQueueSource(source);
    if (options.shuffle !== undefined && typeof options.shuffle !== 'boolean') problem('invalid_queue_playback_source');
    await this.playQueueSegments([{source: captured, order: {mode: options.shuffle ? 'shuffle' : 'source'}}], context, undefined, options.display);
  }

  private async playQueueSegments(segments: readonly SegmentInput[], context: WebPlaybackScreenLifetime, startSeconds?: number, display?: WebPlaybackStartDisplay): Promise<QueueHeader> {
    const check = () => {
      this.assertCurrent();
      if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
    };
    check();
    if (!Array.isArray(segments) || !segments.length || startSeconds !== undefined && !validSeconds(startSeconds)) problem('invalid_queue_playback_source');
    const serial = ++this.operation;
    const finishRetention = this.retainMediaStart(context.signal, () => serial === this.operation);
    const starting = this.beginStarting(display, async () => {await this.playQueueSegments(segments, this.retryLifetime(), startSeconds, display);}), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    try {
      const stack = this.replace();
      starting.attach(stack);
      // Actual metadata collapses music to its real dock in selectSource. A queue that is only ever
      // music (an album, an artist, a radio) starts in the dock: it never waits on that read to show.
      stack.expanded = !segments.every(segment => 'container' in segment.source && ['album', 'artist', 'radio'].includes(segment.source.container.kind));
      const deadline = this.startDeadline();
      const lifetime = AbortSignal.any([context.signal, stack.life.signal, this.life.signal, deadline.signal, stopped.signal]);
      const current = () => {
        if (lifetime.aborted || !this.current(stack) || serial !== this.operation || stack.capabilityRejected) return false;
        try {check();return true;} catch {return false;}
      };
      const cancel = () => {if (this.current(stack) && serial === this.operation) {
        this.replacing = true;this.retire(stack);this.stack = this.makeStack();this.activate(this.stack);this.replacing = false;this.sync();
      }};
      lifetime.addEventListener('abort', cancel, {once: true});
      try {
        stack.capacityCurrent = current;
        const queue = await stack.queue.playSegments(segments, {startSeconds, signal: lifetime, isCurrent: current});
        if (!current()) problem(deadline.expired() && !context.signal.aborted ? 'playback_start_timeout' : 'stale_selection_context');
        const session = stack.queue.currentSession(), started = stack.service.getSnapshot();
        if (started.phase === 'error' || !session || session.queue?.queueId !== queue.id || started.session?.id !== session.id || started.session.generation !== session.presentation.generation) problem('playback_start_failed');
        starting.accepted(); starting.stopWith(cancel);
        this.sync();return queue;
      } catch (error) {
        let admissionFailed = false;
        if (stack.capabilityRejected && this.current(stack) && serial === this.operation) {
          try {check();admissionFailed = true;} catch { /* Original caller retired. */ }
        }
        cancel();
        if (admissionFailed) problem('playback_start_failed');
        if (deadline.expired() && !context.signal.aborted && !stopped.signal.aborted) problem('playback_start_timeout');
        throw error;
      }
      finally {deadline.clear(); if (stack.capacityCurrent === current) stack.capacityCurrent = undefined; lifetime.removeEventListener('abort', cancel);}
    } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
    finally {finishRetention();}
  }

  /** The shell supplies the same current Home context guard used by its selection controller.
   * No route/document revision is guessed from the playback authority. */
  selectionPlayback(isCurrent: (context: SelectionContext) => boolean): HomeSelectionPlaybackPort {
    return {
      play: request => this.playSelection(request, isCurrent),
      enqueue: request => this.enqueueSelection(request, isCurrent),
      playChannel: (channel, context, signal) => this.playChannel(channel, context, signal, isCurrent),
    };
  }
  private selectionCurrent(context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): void {
    this.assertCurrent();
    const scope = context.scope, authority = this.authority;
    if (signal.aborted) problem('cancelled');
    if (scope.serverId !== authority.serverId || scope.authority !== authority.authority || scope.accountId !== authority.accountId || scope.profileId !== authority.profileId || authority.viewerFence !== undefined && scope.privacyFence !== authority.viewerFence || !current(context)) problem('stale_selection_context');
  }
  private async playSelection(request: HomeSelectionPlaybackRequest, current: (context: SelectionContext) => boolean) {
    this.selectionCurrent(request.context, request.signal, current);
    const segments = selectionSegments(request.items, true), serial = ++this.operation;
    const finishRetention = this.retainMediaStart(request.signal, () => serial === this.operation);
    const first = request.items[0], pressed = first && 'entry' in first ? first.entry : undefined;
    const starting = this.beginStarting(pressed && entryStartDisplay(pressed), async () => {await this.playSelection({...request, signal: this.life.signal}, current);}), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    try {
      const stack = this.replace();
      starting.attach(stack);
      stack.expanded = true;
      const cancel = () => { if (this.current(stack) && serial === this.operation) {
        this.replacing = true; this.retire(stack); this.stack = this.makeStack(); this.activate(this.stack); this.replacing = false; this.sync();
      } };
      const deadline = this.startDeadline(), signal = AbortSignal.any([request.signal, deadline.signal, stopped.signal]);
      signal.addEventListener('abort', cancel, {once: true});
      const originalCurrent = () => this.current(stack) && serial === this.operation && !signal.aborted && current(request.context);
      try {
        stack.capacityCurrent = originalCurrent;
        const queue = await stack.queue.playSegments(segments, {startSeconds: selectionStartSeconds(request.items), signal, isCurrent: originalCurrent});
        if (deadline.expired() && !request.signal.aborted) problem('playback_start_timeout');
        this.selectionCurrent(request.context, request.signal, current);
        if (!this.current(stack) || serial !== this.operation) problem('stale_selection_context');
        starting.accepted(); starting.stopWith(cancel);
        this.sync();
        return {queue};
      } catch (error) {
        if (this.current(stack) && serial === this.operation) cancel();
        if (deadline.expired() && !request.signal.aborted && !stopped.signal.aborted) problem('playback_start_timeout');
        throw error;
      } finally {deadline.clear(); if (stack.capacityCurrent === originalCurrent) stack.capacityCurrent = undefined;signal.removeEventListener('abort', cancel);}
    } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
    finally {finishRetention();}
  }
  private async enqueueSelection(request: HomeSelectionPlaybackRequest & {placement: 'next' | 'end'}, current: (context: SelectionContext) => boolean) {
    this.selectionCurrent(request.context, request.signal, current);
    if (!['next', 'end'].includes(request.placement)) problem('invalid_queue_placement');
    const segments = selectionSegments(request.items, false), stack = this.stack;
    const finishRetention = this.retainMediaStart(request.signal, () => this.current(stack));
    try {
      const client = new QueueClient({send: value => stack.http.send({...value, signal: value.signal ? AbortSignal.any([value.signal, request.signal]) : request.signal})}, this.env.key);
      const check = () => { this.selectionCurrent(request.context, request.signal, current); if (!this.current(stack)) problem('stale_selection_context'); };
      const header = stack.queue.getSnapshot().view?.queue;
      if (!header) {
        if (stack.queue.getSnapshot().phase === 'preparing' || stack.service.getSnapshot().session) problem('queue_not_ready');
        const created = await client.create(segments, {signal: request.signal, retainQueueIds: this.retainedQueueIds(stack)});
        check();
        await stack.queue.open(created.queue.id, request.signal);
        check(); this.sync(); return {queue: created.queue};
      }
      let queue = await client.header(header.id, request.signal); check();
      // Each next insertion is immediately after current, so reverse requests retain pick order.
      const ordered = request.placement === 'next' ? [...segments].reverse() : segments;
      for (const segment of ordered) {
        check(); queue = (await client.addSegment(queue.id, queue.revision, request.placement, segment)).queue; check();
      }
      await stack.queue.open(queue.id, request.signal); check(); this.sync(); return {queue};
    } finally {finishRetention();}
  }
  async playChannel(channel: HomeChannel, context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): Promise<void> {
    this.selectionCurrent(context, signal, current);
    const value = channel.channel, source = channel.source;
    if (!value.tuneAvailable || value.tuneUnavailableReason || value.sourceId !== source.id || value.generation !== source.generation || value.provenance !== source.provenance) problem('invalid_channel_target');
    await this.playFromGuide(value, {signal, isCurrent: () => {this.selectionCurrent(context, signal, current);return true;}});
  }

  /** A genuine guide handle needs no Home entry or Home selection context. The channel
   * generation goes unchanged to the server's current-source/access admission check. */
  async playFromGuide(channel: GuideChannel, context: WebPlaybackScreenLifetime): Promise<void> {
    const check = () => {this.assertCurrent();if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');};
    check();
    if (!channel.tuneAvailable || channel.tuneUnavailableReason || !['live-source', 'library-channel'].includes(channel.provenance)
      || ![channel.id, channel.sourceId, channel.generation].every(value => typeof value === 'string' && value.length > 0 && value.length <= 256 && !/[\r\n]/.test(value))) problem('invalid_channel_target');
    const value = Object.freeze({...channel}), reference = Object.freeze({kind: value.provenance, sourceId: value.sourceId, channelId: value.id, generation: value.generation});
    const serial = ++this.operation;
    const finishRetention = this.retainMediaStart(context.signal, () => serial === this.operation);
    // The channel's own name is the only fact a guide start has; no art is invented.
    const subject = {kind: 'channel' as const, id: value.id, sourceId: value.sourceId, channelName: value.name};
    const starting = this.beginStarting({subject}, () => this.playFromGuide(value, this.retryLifetime()), {watchdog: false}), stopped = new AbortController();
    starting.stopWith(() => stopped.abort());
    try {
      const stack = this.replace();
      starting.attach(stack);
      stack.expanded = true;
      stack.channel = {channel: value};
      stack.subject = subject;
      const abandon = () => this.abandon(stack, serial);
      starting.stopWith(() => {stopped.abort(); abandon();});
      const lifetime = AbortSignal.any([context.signal, stack.life.signal, this.life.signal, stopped.signal]);
      const current = () => {if (lifetime.aborted || !this.current(stack) || serial !== this.operation) return false;try {check();return true;} catch {return false;}};
      const cancel = () => {if (this.current(stack) && serial === this.operation) stack.service.leave();};
      lifetime.addEventListener('abort', cancel, {once: true});
      try {
        await stack.service.playChannel(reference, {signal: lifetime, isCurrent: current});
        if (!current()) problem('stale_selection_context');
        const state = stack.service.getSnapshot();
        if (state.phase === 'error' || !state.channel) problem('channel_playback_failed');
        starting.accepted(); starting.stopWith(abandon);
        this.sync();
      } catch (error) { cancel(); throw error; }
      finally {lifetime.removeEventListener('abort', cancel);}
    } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
    finally {finishRetention();}
  }

  private offers(stack: Stack, playback: PlaybackSnapshot): PlayerOffersData | null {
    const binding = stack.offerBinding, response = stack.offers.getSnapshot(), data = response.data;
    if (!this.current(stack) || !binding || binding.api !== this.source.api || binding.authority !== this.authority || binding.sessionKey !== stack.sessionKey || binding.sessionKey !== `${playback.intentId}:${playback.session?.id}:${playback.session?.generation}` || binding.playbackRun !== playback.playbackRun) return null;
    if (response.phase !== 'ready' || response.error || response.scope.serverId !== this.source.contentScope.serverId || response.scope.viewerId !== this.source.contentScope.viewerId || !data) return null;
    if (response.query?.libraryId !== binding.libraryId || response.query.itemId !== binding.itemId || response.query.sessionId !== binding.sessionId || stack.libraryId !== binding.libraryId || stack.itemId !== binding.itemId) return null;
    if (data.scope.serverId !== this.authority.serverId || data.scope.libraryId !== binding.libraryId || data.scope.itemId !== binding.itemId || playback.itemId !== binding.itemId || data.current?.sessionId !== binding.sessionId || playback.session?.id !== binding.sessionId || data.current.generation !== binding.generation || playback.session.generation !== binding.generation) return null;
    // Home hashes its entitled library set; detail/offers hash a different namespace. Bind
    // the actual validated response under this captured API/authority and media source,
    // never substitute the Home fence. PlayerOffersService also retains its detail fence
    // across refreshes and source generations, and rejects a changed fence or denial.
    if (binding.detailFence && binding.detailFence !== data.scope.viewerFence) return null;
    binding.detailFence = data.scope.viewerFence;
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
      if (subject.kind === 'music') stack.expanded = false;
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
      if (playback.channel && playback.linear) stack.subject = {kind: 'channel', id: playback.channel.channelId, sourceId: playback.channel.sourceId, channelName: playback.linear.name, programmeTitle: playback.linear.programme?.title};
      const heldSession = stack.web.session(playback.session?.id) ?? stack.queue.currentSession();
      const session = heldSession?.id === playback.session?.id ? heldSession : undefined;
      const prior = this.snapshot?.fence;
      if (prior && playback.session && (prior.sessionId !== playback.session.id || prior.generation !== playback.session.generation)) {
        this.retiredFences.add(this.key(prior));
        this.trimRetiredFences();
      }
      this.prepareAirPlay(stack, session, playback);
      this.selectSource(stack, playback, session);
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
            queueMicrotask(() => { if (this.matches(command.fence)) void this.advancePolicy(stack, command).catch(() => { if (this.current(stack)) { this.runtimeProblem = 'playback_advance_failed'; this.sync(); } }); });
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
        if (fence.kind === 'queue' && queue?.next.available && !playback.ended) controls.push('next');
        if (fence.kind === 'queue' && queue && (queue.queue.entries.findIndex(entry => entry.id === fence.entryId) > 0 || playback.positionSeconds > 3) && !playback.ended) controls.push('previous');
        if (this.source.settings.webVolume() && this.video) controls.push('volume');
        if (this.group?.matches(fence)) {
          const group = this.group.active(), queue = this.groupServices.get(this.group)?.getSnapshot().queue ?? group?.queue;
          if (!group?.permissions.canControl) controls.splice(0, controls.length, ...controls.filter(value => value === 'volume'));
          else if (queue) {
            if (queue.entries.some(entry => entry.position > queue.position && !entry.unavailable && entry.itemId) && !controls.includes('next')) controls.push('next');
            if (queue.entries.some(entry => entry.position < queue.position && !entry.unavailable && entry.itemId) && !controls.includes('previous')) controls.push('previous');
          }
        }
        presentation = {fence, subject: stack.subject, kind: stack.subject.kind === 'channel' ? 'video' : stack.subject.kind, posterUrl: stack.posterUrl, backdropUrl: stack.backdropUrl, durationSeconds: playback.duration, positionSeconds: local.positionSeconds, playing: local.playing, expanded: stack.expanded, finished, controls, volume: this.video?.volume ?? 1, muted: this.video?.muted ?? false, rate: speed, policy: this.policyView, ...(playback.channel && playback.linear ? {seekWindow: {startSeconds: Number(playback.linear.media.windowStartUs) / 1e6, endSeconds: Number(playback.linear.media.windowEndUs) / 1e6}} : {})};
        this.renderSubtitles(stack);
      }
      this.snapshot = Object.freeze({nativeVideoSurfaceActive: !!fence && stack.subject?.kind === 'video' && stack.web.slot.nativeSurfaceActive, authority: this.authority, sequence: ++this.sequence, playback, dock: this.dock.getSnapshot(), fence, subject: stack.subject, presentation, presentations: this.presentations(presentation), queueView: stack.queue.getSnapshot().view, session, offers, options: stack.options, problem: this.runtimeProblem, disposed: false,
        ...(stack.capacityWait?.isCurrent() ? {capacityWait: stack.capacityWait} : {}),
        ...(stack.startFailure?.isCurrent() ? {startFailure: stack.startFailure} : {}),
        ...this.startingFields(stack, !!presentation)});
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
    const pending = stack.preplay;
    if (!pending || !this.current(stack) || !fence || fence.kind !== 'queue' || !playback.session) return;
    if (pending.progress.choice?.itemId !== playback.itemId || this.snapshot.session?.versionId !== pending.versionId) {stack.preplay = undefined;return;}
    const subtitles = stack.subtitles?.getSnapshot(), plan = subtitles?.plan;
    if (!plan || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation || plan.sourceId !== fence.sourceId) return;
    const progress = advancePendingChoice(pending.progress, {
      sessionId: fence.sessionId, audio: {availability: 'unavailable'},
      subtitles: {mode: plan.mode, selectedId: plan.selected?.id, busy: subtitles?.busy ?? true, resources: plan.resources},
    }, {
      // Audio is already the verified start track, never a second asynchronous restart.
      selectRendition: () => {problem('playback_audio_unavailable');},
      selectTrack: () => {problem('playback_audio_unavailable');},
      chooseSubtitle: trackId => {
        const original = fence;
        queueMicrotask(() => {
          if (!this.current(stack) || !this.matches(original)) return;
          void this.selectSubtitle(original, trackId).catch(() => {
            if (this.current(stack) && this.matches(original)) {this.runtimeProblem = 'playback_subtitles_failed';this.sync();}
          });
        });
      },
    });
    stack.preplay = progress.choice ? {versionId: pending.versionId, progress} : undefined;
  }

  private prepareAirPlay(stack: Stack, session: Session | undefined, playback: PlaybackSnapshot): void {
    if (!this.airPlayCanReplan() || !this.current(stack) || !session || session.kind !== 'vod' || session.role !== 'local' || playback.session?.id !== session.id || playback.session.generation !== session.presentation.generation) {
      stack.airPlay?.abort.abort();stack.airPlay = undefined;stack.web.slot.setAirPlaySource(null);return;
    }
    const key = `${session.id}:${session.presentation.generation}:${session.presentation.choiceRevision}`;
    if (stack.airPlay?.key === key) {
      if (stack.airPlay.receipt) stack.web.slot.setAirPlaySource({sessionId: session.id, generation: session.presentation.generation, url: stack.airPlay.receipt.presentation.url});
      return;
    }
    stack.airPlay?.abort.abort();stack.web.slot.setAirPlaySource(null);
    const pending = stack.airPlay = {key, abort: new AbortController()} as NonNullable<Stack['airPlay']>;
    const current = () => this.current(stack) && stack.airPlay === pending && !pending.abort.signal.aborted;
    const client = new SessionsClient(stack.http, this.env.timers);
    void client.prepareAirPlay(session, Math.round((playback.pendingSeek?.positionSeconds ?? playback.positionSeconds) * 1000), AbortSignal.any([pending.abort.signal, stack.life.signal])).then(receipt => {
      if (!current()) return;
      pending.receipt = receipt;
      stack.web.slot.setAirPlaySource({sessionId: session.id, generation: receipt.generation, url: receipt.presentation.url});
      if (this.runtimeProblem === 'playback_subtitles_failed') this.runtimeProblem = undefined;
      this.sync();
    }, () => {if (current()) {pending.failed = true;if (wirelessPlayback(this.video)) this.runtimeProblem = 'playback_subtitles_failed';this.sync();}});
  }

  private selectSource(stack: Stack, playback: PlaybackSnapshot, session: Session | undefined): void {
    if (playback.channel || !playback.session || !playback.itemId || !session || session.id !== playback.session.id || session.presentation.generation !== playback.session.generation || session.itemId !== playback.itemId) { if (stack.sessionKey) this.clearSourceResources(stack); return; }
    const key = `${playback.intentId}:${playback.session.id}:${playback.session.generation}`;
    if (stack.sessionKey === key) return;
    this.clearSourceResources(stack);
    stack.sessionKey = key;
    const itemId = playback.itemId, abort = new AbortController(); stack.metadataAbort = abort;
    const current = () => this.current(stack) && stack.sessionKey === key && !abort.signal.aborted;
    const read = async () => {
      if (stack.metadataItemId !== itemId || !stack.libraryId || !stack.subject) {
        const raw = await this.readItemBounded(itemId, AbortSignal.any([abort.signal, stack.life.signal]), current)
          .catch(error => this.queueFactsInstead(stack, itemId, current, error));
        if (raw) {
          const item = parseMediaItem(raw);
          if (!current()) return;
          if (item.id !== itemId || !item.available || raw.missing) problem('invalid_playback_item');
          stack.subject = itemSubject(raw); stack.itemId = itemId; stack.metadataItemId = itemId; stack.libraryId = item.libraryId;
          stack.posterUrl = item.posterUrl; stack.backdropUrl = item.backdropUrl;
          stack.showId = raw.episode?.showId; stack.seasonId = raw.episode?.seasonId;
          if (stack.subject.kind === 'music') stack.expanded = false;
        }
      }
      if (!current()) return;
      if (!stack.libraryId) {
        // Queue facts stood in and named no library: the item's offers name it.
        await stack.offers.select({itemId, sessionId: session.id});
        const learned = stack.offers.getSnapshot().data?.scope.libraryId;
        if (!current() || !learned) return;
        stack.libraryId = learned;
      }
      stack.offerBinding = {api: this.source.api, authority: this.authority, sessionKey: key, libraryId: stack.libraryId, itemId, sessionId: session.id, generation: session.presentation.generation, playbackRun: playback.playbackRun};
      await stack.offers.select({libraryId: stack.libraryId, itemId, sessionId: session.id});
      if (!current()) return;
      const data = this.offers(stack, stack.service.getSnapshot());
      if (data) this.bindSubtitles(stack, data, key);
      const options = await stack.optionsClient.options(itemId, {versionId: session.versionId}, abort.signal);
      if (!current()) return;
      if (options.itemId !== itemId) problem('invalid_playback_options');
      stack.options = options;
      this.sync();
    };
    void read().catch(() => { if (current()) { this.runtimeProblem = 'playback_metadata_failed'; this.sync(); } });
  }

  /** The item's own facts did not answer. An audio queue entry carries its real kind, title,
   * album, poster and library: play on with those, so the dock draws and Pause is there (P2-74
   * E-web-14); the next source change reads the item again. Anything else keeps the failure. */
  private queueFactsInstead(stack: Stack, itemId: string, current: () => boolean, error: unknown): undefined {
    if (!current()) return undefined;
    const entry = this.currentQueueEntry(stack, itemId), fallback = entry && queueEntrySubject(entry);
    if (!entry || !fallback) throw error;
    stack.subject = fallback; stack.itemId = itemId; stack.libraryId = entry.libraryId; stack.posterUrl = entry.posterUrl; stack.backdropUrl = undefined;
    if (fallback.kind === 'music') stack.expanded = false;
    return undefined;
  }
  /** A start the server never answers must not stall Play silently (P2-74 E-web-17): after
   * START_DEADLINE_MS the start is retired through its ordinary lifetime and reported. */
  private startDeadline() {
    const controller = new AbortController(), timer = this.setTimer(() => controller.abort(), START_DEADLINE_MS);
    return {signal: controller.signal, expired: () => controller.signal.aborted, clear: () => this.clearTimer(timer)};
  }
  /** O-159 watchdog. A start is drawn from its first frame from the pressed card's facts (the
   * starting dock, Stop only), and replaced in place once its real dock exists. An ACCEPTED start
   * that is still not drawable at START_DEADLINE_MS is stopped, recorded and offered again; slow
   * buffering never trips it, because a drawn dock ends the watch. Before acceptance the start's
   * own deadline (startDeadline) reports as it always has. */
  private beginStarting(display: WebPlaybackStartDisplay | undefined, retry: () => Promise<void>, options: Readonly<{watchdog?: boolean}> = {}): Starting {
    this.endStarting(); this.startTimedOut = undefined;
    const state: StartingState = {key: String(++this.startingKeys), display, accepted: false, stop: () => {}, retry, timer: undefined};
    // A Live tune is drawn by its starting dock from acceptance: a slow stream keeps that dock
    // (with the server's reason, if it has one) under the channel's own hard bound, and never
    // trips this watchdog.
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
    return {...(live?.display && live.stack === stack ? {starting: this.startingView(live)} : {}), ...(this.startTimedOut ? {startTimedOut: this.startTimedOut} : {})};
  }
  private startingView(state: StartingState): WebPlaybackStarting {
    // A starting channel whose session the server reports recoverable says so, plainly, while
    // it keeps trying: never a silent "Starting…" when the server already knows why.
    const service = state.stack?.service.getSnapshot();
    const waiting = service?.channel && service.linearStalled ? 'channel_source' as const : undefined;
    if (state.view?.display !== state.display || state.view?.waiting !== waiting)
      state.view = Object.freeze({key: state.key, display: state.display!, stop: () => this.stopStarting(state), ...(waiting ? {waiting} : {})});
    return state.view!;
  }
  /** The queue's own entry for the item now playing, when the loaded window holds it. */
  private currentQueueEntry(stack: Stack, itemId: string) {
    const view = stack.queue.getSnapshot().view;
    return view?.items.find(entry => entry.entryId === view.queue.currentEntryId && entry.itemId === itemId);
  }
  /** The playing item's facts draw its dock and its Now playing. A passing failure (a 503
   * burst) is read again twice, after 1 s and 3 s, before the start reports it: music must
   * never play on with nothing drawn (P2-74 E-web-14). */
  private async readItemBounded(itemId: string, signal: AbortSignal, current: () => boolean): Promise<ItemResponse> {
    const path = encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)});
    for (let attempt = 0; ; attempt++) {
      // A read that hangs is a failure too: each attempt gets 8 s.
      const attemptLife = new AbortController(), expiry = this.setTimer(() => attemptLife.abort(), 8000);
      try { return await this.source.api.request<ItemResponse>(path, 'GET', undefined, AbortSignal.any([signal, attemptLife.signal])); }
      catch (error) {
        const failure = error as {name?: string; status?: number; retryable?: boolean} | undefined;
        const timedOut = attemptLife.signal.aborted && !signal.aborted;
        const passing = timedOut || failure?.retryable === true || (failure?.status ?? 0) >= 500;
        if (attempt >= 2 || !passing || failure?.name === 'AbortError' && !timedOut || signal.aborted || !current()) throw error;
        await new Promise<void>((resolve, reject) => {
          const timer = this.setTimer(() => {signal.removeEventListener('abort', stop); resolve();}, attempt ? 3000 : 1000);
          const stop = () => {this.clearTimer(timer); reject(new DOMException('Playback metadata read stopped', 'AbortError'));};
          signal.addEventListener('abort', stop, {once: true});
        });
      } finally { this.clearTimer(expiry); }
    }
  }

  private clearSourceResources(stack: Stack): void {
    stack.metadataAbort?.abort(); stack.metadataAbort = undefined;
    stack.documentAbort?.abort(); stack.documentAbort = undefined;
    this.clearTimer(stack.documentTimer); stack.documentTimer = undefined;
    stack.offSubtitles?.(); stack.offSubtitles = undefined; stack.subtitles?.stop(); stack.subtitles = undefined;
    stack.sessionKey = undefined; stack.offerBinding = undefined; stack.subtitleKey = undefined; stack.documentKey = undefined; stack.options = null;
    if (!stack.retired) stack.offers.cancel();
    this.clearDrawnSubtitles(stack);
    this.surface?.guard(false); this.surface?.clear();
    this.clearRecordedSidecar(stack, 'text');
  }
  private bindSubtitles(stack: Stack, offers: PlayerOffersData, key: string): void {
    const current = offers.current, plan = offers.subtitlePlan;
    if (!current || !plan || !stack.itemId) return;
    const service = new SubtitleService({request: <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal) => this.source.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, stack.life.signal]) : stack.life.signal)}, {itemId: stack.itemId, sessionId: current.sessionId, generation: current.generation}, this.env.key ?? idempotencyKey, {install: presentation => this.current(stack) && stack.sessionKey === key && stack.service.installSubtitlePresentation(presentation)});
    stack.subtitles = service;
    stack.offSubtitles = service.subscribe(() => { if (this.current(stack) && stack.sessionKey === key) { this.renderSubtitles(stack); this.sync(); } });
    // A reused stack or a new playback must never keep another playback's injected caption CSS.
    if (stack.subtitleStyle && stack.subtitleStyle.run !== this.playbackRun()) stack.subtitleStyle = undefined;
    this.surface?.applyPreferences(this.subtitlePreferences(stack));
    service.setPolling(plan.mode === 'track'); service.start();
  }
  /** The chosen track's original, when the plan has this browser draw it (styled or image). */
  private drawnOriginal(plan: SubtitlePlan | null | undefined) {
    return plan?.mode === 'track' ? selectedSubtitleOriginal(plan) : null;
  }
  /** A media path as an address on this server; another origin is refused. */
  private subtitleUrl(path: string): string {
    const url = this.source.api.mediaUrl(path);
    if (new URL(url).origin !== new URL(this.source.api.baseUrl).origin) problem('unexpected_subtitle_origin');
    return url;
  }
  /** What the overlay is given for an original. The script and pictures are read through the
   * load's own reader; fonts are addresses the styled renderer's worker fetches itself. */
  private async drawnSource(original: SubtitleOriginal, signal: AbortSignal): Promise<DrawnSubtitleSource> {
    if (original.format === 'vobsub') return {kind: 'vobsub', index: original.url, data: original.companionUrl ?? ''};
    if (original.format !== 'ass') return {kind: original.format, data: original.url};
    let fonts: string[] = [];
    if (original.fontsUrl) {
      try { fonts = (await readSubtitleFontList(subtitleFetch(this.env.fetch ?? fetch), this.subtitleUrl(original.fontsUrl), original.fontsUrl, signal)).map(font => this.subtitleUrl(font.url)); }
      // A track whose font list cannot be read is still drawn, in the fallback fonts.
      catch { signal.throwIfAborted(); }
    }
    return {kind: 'ass', script: original.url, fonts, cjkFont: this.subtitleUrl(SUBTITLE_CJK_FONT_PATH)};
  }
  /** The viewer's subtitle size for a drawn track, as a scale of the authored size. */
  private drawnScale(stack: Stack): number {
    const preferences = this.subtitlePreferences(stack);
    if (!preferences || preferences.useSystemCaptionStyle) return 1;
    return ({small: 0.9, medium: 1, large: 1.12, 'extra-large': 1.25} as Record<string, number>)[preferences.size] ?? 1;
  }
  /** Whether this run already failed to draw the track, so it is asked for in the picture. */
  private undrawable(stack: Stack, trackId: string | null | undefined): boolean {
    return !!trackId && stack.undrawable?.run === this.playbackRun() && stack.undrawable.ids.has(trackId);
  }
  /** A drawn track that cannot be drawn here is asked for in the picture instead, once per track
   * in this playback run. A refusal (the owner turned conversions off) leaves the problem shown. */
  private burnUndrawable(stack: Stack, plan: SubtitlePlan): void {
    const id = plan.selected?.id, offsetMs = Number(plan.offsetUs) / 1000;
    if (!id || this.undrawable(stack, id)) return;
    const playback = stack.service.getSnapshot(), sessionId = playback.session?.id, intentId = playback.intentId;
    const run = this.playbackRun(), before = stack.undrawable?.run === run ? stack.undrawable.ids : [];
    stack.undrawable = Object.freeze({run, ids: new Set([...before, id])});
    const owns = () => this.current(stack) && this.playbackRun() === run && stack.service.getSnapshot().session?.id === sessionId && stack.service.getSnapshot().intentId === intentId;
    void stack.service.changePlayback({subtitleTrackId: id, subtitleDelivery: 'burn', ...(Number.isSafeInteger(offsetMs) ? {subtitleOffsetMs: offsetMs} : {subtitleOffsetUs: plan.offsetUs})})
      .then(() => { if (owns()) { if (this.runtimeProblem === 'playback_subtitles_failed') this.runtimeProblem = undefined; this.sync(); } },
        () => { if (owns() && stack.service.getSnapshot().session?.generation === plan.generation) { this.runtimeProblem = 'playback_subtitles_failed'; this.sync(); } });
  }
  /** The selected addresses and the original load lifetime are checked before every read. */
  private subtitleReader(stack: Stack, life: AbortSignal, addresses: ReadonlySet<string>, current: () => boolean): ReadSubtitleFile {
    return async (address, maxBytes, signal) => {
      const joined = AbortSignal.any([signal, life, stack.life.signal]);
      joined.throwIfAborted();
      if (!current() || !addresses.has(address)) problem('playback_subtitle_unavailable');
      const file = await readSubtitleFile(subtitleFetch(this.env.fetch ?? fetch), this.subtitleUrl(address), maxBytes, joined);
      joined.throwIfAborted();
      if (!current()) throw new DOMException('Playback changed', 'AbortError');
      return file;
    };
  }
  /** Reads the chosen revision's own forced-line offer under the image load that can use it. A
   * stale, failed, cleared or replaced load never arms the switch. */
  private async readForcedLines(stack: Stack, linesUrl: string, key: string, epoch: number, life: AbortSignal, read: ReadSubtitleFile, current: () => boolean): Promise<void> {
    try {
      const file = await read(linesUrl, 8192, life);
      const lines = parseSubtitleLines(JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(file.bytes)));
      if (!lines || !current() || !forcedChoiceOf(lines)) return;
      stack.forcedLines = Object.freeze({lines, key, epoch});
      this.sync();
    } catch { /* no offer, no switch; the track still draws every line */ }
  }
  private renderDrawnSubtitles(stack: Stack, plan: SubtitlePlan, original: SubtitleOriginal): void {
    const video = this.video;
    // An installed engine playing the picture draws its own subtitles; the page never draws over it.
    if (!stack.expanded || !video || plan.renderer !== 'client' || stack.web.slot.nativeSurfaceActive) { this.clearDrawnSubtitles(stack); return; }
    // "Styled subtitles" off: the script's lines in the viewer's caption style, on the text surface.
    const plain = original.format === 'ass' && this.subtitlePreferences(stack)?.keepStyling === false;
    const key = JSON.stringify([stack.sessionKey, plan.sourceId, plan.revision, original.url, original.companionUrl ?? '', plan.offsetUs, plain]);
    if (stack.drawnKey === key) { stack.drawnSurface?.setScale(this.drawnScale(stack)); return; }
    // A new load owns its own epoch, so a late answer from the previous one cannot arm anything.
    this.clearDrawnSubtitles(stack);
    stack.drawnKey = key;
    const epoch = stack.drawnEpoch = (stack.drawnEpoch ?? 0) + 1;
    const abort = new AbortController(); stack.drawnAbort = abort;
    const sessionKey = stack.sessionKey, run = this.playbackRun(), subtitles = stack.subtitles;
    const owns = () => {
      const active = subtitles?.getSnapshot(), selected = active?.plan?.selected;
      return this.current(stack) && !abort.signal.aborted && !stack.life.signal.aborted && stack.expanded
        && stack.sessionKey === sessionKey && this.playbackRun() === run && stack.subtitles === subtitles
        && stack.drawnKey === key && stack.drawnEpoch === epoch && !active?.suppressed
        && active?.plan?.sessionId === plan.sessionId && active.plan.generation === plan.generation
        && active.plan.sourceId === plan.sourceId && active.plan.revision === plan.revision && active.plan.offsetUs === plan.offsetUs
        && selected?.id === plan.selected?.id && selected?.revision === plan.selected?.revision;
    };
    const current = () => owns() && !stack.drawnFailed;
    const addresses = new Set([original.url, ...(original.companionUrl ? [original.companionUrl] : []), ...(original.linesUrl ? [original.linesUrl] : [])]);
    const read = this.subtitleReader(stack, abort.signal, addresses, current);
    const failed = (error: unknown) => {
      if (!current()) return;
      stack.drawnFailed = true; stack.forcedLines = undefined;
      if (stack.drawnPlain) { this.surface?.guard(false); this.surface?.clear(); }
      this.clearRecordedSidecar(stack, 'drawn', key);
      // A passing outage is tried again; anything else is shown the ordinary way, in the picture.
      if (!stack.drawnRetry?.schedule(error) && !stack.drawnRetry?.pending) this.burnUndrawable(stack, plan);
      this.runtimeProblem = 'playback_subtitles_failed'; this.sync();
    };
    let attempt = 0;
    const shown = (loading: number) => {
      if (!current() || loading !== attempt) return false;
      this.recordSidecar(stack, 'drawn', key, plan.selected?.id ?? null);
      if (this.runtimeProblem === 'playback_subtitles_failed') this.runtimeProblem = undefined;
      return true;
    };
    let present: () => void;
    const surface = plain ? undefined : stack.drawnSurface = createDrawnSubtitleSurface(video as HTMLVideoElement, read, failed);
    if (plain) {
      const text = this.surface;
      if (!text) { failed(new Error('This browser cannot show subtitles.')); return; }
      stack.drawnPlain = true;
      present = () => { const loading = ++attempt; void read(original.url, subtitleFileLimits.script, abort.signal).then(file => {
        if (!current() || loading !== attempt) return;
        text.show(assPlainCues(decodeSubtitleText(file.bytes, file.charset)), Number(plan.offsetUs) / 1e6);
        text.guard(true);
        if (shown(loading)) this.sync();
      }).catch(failed); };
    } else {
      if (!surface) { failed(new Error('This browser cannot draw subtitles.')); return; }
      surface.setDelay(Number(plan.offsetUs) / 1e6);
      surface.setForcedOnly(stack.forcedOnly === true);
      surface.setScale(this.drawnScale(stack));
      // The player's canonical offset may already be known (HLS presents a nonzero start).
      surface.timeline(this.subtitleTimeline);
      present = () => { const loading = ++attempt; void this.drawnSource(original, abort.signal).then(source => surface.present(source)).then(() => {
        if (!shown(loading)) return;
        if (original.linesUrl) void this.readForcedLines(stack, original.linesUrl, key, epoch, abort.signal, read, () => current() && loading === attempt);
        this.sync();
      }).catch(failed); };
    }
    stack.drawnRetry = new DrawnSubtitleRetry({current: owns, retry: () => {stack.drawnFailed = false;present();},
      setTimer: (fn, ms) => this.setTimer(fn, ms), clearTimer: timer => this.clearTimer(timer)});
    present();
  }
  /** The player's own observation of the track this runtime draws: never inferred from a choice. */
  private recordSidecar(stack: Stack, path: 'text' | 'drawn', key: string, id: string | null): void {
    stack.subtitleRecorded = Object.freeze({id, path, key});
    stack.web.slot.selectSidecarSubtitle(id);
  }
  /** Retiring one path clears the record only when that path still owns it, so a late drawn
   * teardown can never erase the successor text track (or the reverse). */
  private clearRecordedSidecar(stack: Stack, path: 'text' | 'drawn', key?: string): void {
    const recorded = stack.subtitleRecorded;
    if (!recorded || recorded.path !== path || (key !== undefined && recorded.key !== key)) return;
    stack.subtitleRecorded = undefined;
    stack.web.slot.selectSidecarSubtitle(null);
  }
  /** The drawn path's own retirement: its reads, canvas or plain lines, offer, identity and epoch all end here. */
  private clearDrawnSubtitles(stack: Stack): void {
    stack.drawnRetry?.dispose(); stack.drawnRetry = undefined;
    stack.drawnAbort?.abort(); stack.drawnAbort = undefined;
    stack.drawnSurface?.clear(); stack.drawnSurface?.dispose(); stack.drawnSurface = undefined;
    if (stack.drawnPlain) { this.surface?.guard(false); this.surface?.clear(); stack.drawnPlain = undefined; }
    stack.drawnKey = undefined; stack.drawnEpoch = (stack.drawnEpoch ?? 0) + 1;
    stack.forcedLines = undefined; stack.forcedOnly = undefined; stack.drawnFailed = undefined;
    this.clearRecordedSidecar(stack, 'drawn');
  }
  /** The text path's own teardown, so exactly one subtitle owner draws at a time. */
  private clearTextSubtitles(stack: Stack): void {
    stack.documentAbort?.abort(); stack.documentAbort = undefined;
    this.clearTimer(stack.documentTimer); stack.documentTimer = undefined;
    stack.documentKey = undefined;
    this.surface?.guard(false); this.surface?.clear();
    stack.subtitles?.setPolling(false);
    this.clearRecordedSidecar(stack, 'text');
  }
  private renderSubtitles(stack: Stack): void {
    const state = stack.subtitles?.getSnapshot() ?? emptySubtitle, plan = state.plan, surface = this.surface;
    // The route can be activated by system controls as well as our picker. Only an exact
    // current plan may request a replacement; off and native text never become burn requests.
    const playback = stack.service.getSnapshot(), session = playback.session;
    const currentPlan = plan && session && plan.sessionId === session.id && plan.generation === session.generation
      && plan.sourceId === this.offers(stack, playback)?.current?.sourceId;
    if (wirelessPlayback(this.video) && currentPlan && !state.suppressed) {
      // The prepared native source already carries this drawn track. Let its first
      // captioned frame arrive before the optional local burn replan rotates owner grants.
      const receiverFrame = !stack.web.slot.settling && ['playing','paused','buffering'].includes(stack.web.slot.observe().state);
      if (receiverFrame && stack.airPlay?.receipt && stack.airPlay.receipt.generation === plan.generation && stack.airPlay.receipt.presentation.subtitle?.trackId === plan.selected?.id && stack.airPlay.receipt.presentation.subtitle?.delivery === 'burn' && plan.mode === 'track' && plan.renderer === 'client' && plan.selected?.enabled && this.drawnOriginal(plan)) this.burnUndrawable(stack, plan);
      this.clearDrawnSubtitles(stack);this.clearTextSubtitles(stack);
      return;
    }
    // A styled or image track the plan has this browser draw comes from its own original file.
    const drawn = this.drawnOriginal(plan);
    if (drawn) {
      // One owner: the text document, its poll and any engine track stop before a drawn track shows
      // (a styled track shown as plain lines already owns the text surface).
      if (!stack.drawnPlain) this.clearTextSubtitles(stack);
      if (state.suppressed) { this.clearDrawnSubtitles(stack); return; }
      this.renderDrawnSubtitles(stack, plan!, drawn);
      return;
    }
    this.clearDrawnSubtitles(stack);
    // The dock is quiet: Portico's subtitle surface is never visible in a thumbnail.
    if (!surface || !stack.expanded || !plan || state.suppressed || plan.mode !== 'track' || plan.renderer !== 'external_text' || !plan.documentUrl) { this.clearTextSubtitles(stack); return; }
    stack.subtitles?.setPolling(plan.mode === 'track');
    const documentUrl = plan.documentUrl;
    const key = JSON.stringify([stack.sessionKey, plan.sourceId, plan.revision, plan.documentUrl, plan.offsetUs]);
    if (stack.documentKey === key) return;
    stack.documentKey = key;
    stack.documentAbort?.abort(); this.clearTimer(stack.documentTimer);
    const abort = new AbortController(); stack.documentAbort = abort;
    void revalidateTextSubtitles<SubtitleDocument>({signal: abort.signal,
      current: () => this.current(stack) && stack.documentKey === key,
      read: async (etag, signal) => {
        const url = this.source.api.mediaUrl(documentUrl);
        if (new URL(url).origin !== new URL(this.source.api.baseUrl).origin) problem('unexpected_subtitle_origin');
        return fetchSubtitleDocument(subtitleFetch(this.env.fetch ?? fetch), url, etag, signal);
      },
      show: document => {surface.show(document.cues, Number(plan.offsetUs) / 1e6);surface.guard(true);this.recordSidecar(stack, 'text', key, plan.selected?.id ?? null);},
      recovered: () => {
        if (this.runtimeProblem === 'playback_subtitles_failed') {this.runtimeProblem = undefined;this.sync();}
      },
      clear: () => {surface.guard(false);surface.clear();this.clearRecordedSidecar(stack, 'text', key);},
      failed: () => {this.runtimeProblem = 'playback_subtitles_failed';this.sync();},
      schedule: (load, ms) => {stack.documentTimer = this.setTimer(load, ms);},
    });
  }

  private schedulePolicy(): void {
    this.clearTimer(this.timer); this.timer = undefined;
    const post = this.snapshot?.queueView?.postPlay;
    // The dock deliberately hides the offer, but its explicit during-credits setting still
    // runs the same policy. Do not depend on a rendered card to advance that quiet queue.
    const inGroup = !!this.snapshot?.fence && !!this.group?.matches(this.snapshot.fence);
    const earlyCountdown = !inGroup && this.snapshot?.subject?.kind === 'video' && this.snapshot.presentation?.playing && post?.available && post.autoplay && post.startDuringCredits;
    // Undo expires even while paused. Early credits count the same actual policy clock as the
    // end card, including when a media element has not emitted another timeupdate yet.
    if (this.snapshot?.fence && (!inGroup && this.stack.queue.getSnapshot().completion || this.policyView?.undo || earlyCountdown) && !this.disposed) this.timer = this.setTimer(() => { this.timer = undefined; this.sync(); }, 250);
  }
  private async advancePolicy(stack: Stack, command: Extract<PolicyCommand, {kind: 'advance'}>): Promise<void> {
    const fence = command.fence;
    this.require(fence);
    // The canonical group readiness/end report advances the room. A member's private
    // queue must never advance independently of that authoritative occurrence.
    if (this.group?.matches(fence)) problem('group_transport_required');
    const state = stack.queue.getSnapshot(), view = currentPolicyQueue(this.snapshot, fence);
    if (!view || !view.next.available || view.next.entryId !== command.nextEntryId || view.postPlay.nextEntryId !== command.nextEntryId) problem('playback_policy_unavailable');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      const held = state.completion, playback = stack.service.getSnapshot();
      if (held && held.sessionId === fence.sessionId && held.intentId === playback.intentId && held.nextEntryId === command.nextEntryId && playback.phase === 'ended') {
        await stack.queue.continueCompletion(command.mode);
      } else {
        if (playback.phase !== 'ready' || command.mode === 'still-watching' || view.postPlay.passoutCheckDue || command.mode === 'automatic' && (!view.postPlay.autoplay || !view.postPlay.startDuringCredits)) problem('playback_policy_unavailable');
        if (command.mode === 'automatic') await stack.queue.advanceFromCredits(view.queue.id, view.queue.revision);
        else await stack.queue.next(view.queue.id, view.queue.revision);
      }
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }

  /** Execute only the prompt the caller actually rendered, through the existing policy and
   * queue controllers. No UI-supplied position, synthetic completion or queue ordering. */
  async policyAction(expected: QueuePlaybackFence, action: WebPlaybackPolicyAction): Promise<void> {
    if (this.group?.matches(expected) && !this.group.active()?.permissions.canControl) problem('group_control_forbidden');
    if (expected.kind !== 'queue') problem('playback_policy_unavailable');
    const fence = playbackFence(expected);
    this.require(fence); this.sync();
    const stack = this.require(fence), view = currentPolicyQueue(this.snapshot, fence), policy = this.policyView;
    const playback = stack.service.getSnapshot();
    if (!view || !stack.expanded || !policy || playback.ended || sameFence(this.policyAdvanceFence, fence)
      || playback.pendingSeek && action.kind !== 'undo') problem('playback_policy_unavailable');
    const prompt = policy.prompt;
    let command: PolicyCommand | null = null;
    switch (action.kind) {
      case 'skip': {
        const marker = prompt?.kind === 'skip' ? prompt.marker : prompt?.kind === 'up-next' ? prompt.offer.marker : undefined;
        if (!marker || marker.id !== action.markerId || prompt?.kind === 'up-next' && marker.kind !== 'credits' && marker.kind !== 'outro') problem('playback_policy_unavailable');
        command = this.policy.skip(fence, action.markerId);
        if (command?.kind !== 'seek' || command.markerId !== action.markerId) problem('playback_policy_unavailable');
        break;
      }
      case 'dismiss':
        if (prompt?.kind !== 'skip' || prompt.marker.id !== action.markerId || !this.policy.dismiss(fence)) problem('playback_policy_unavailable');
        break;
      case 'undo':
        if (policy.undo?.marker.id !== action.markerId) problem('playback_policy_unavailable');
        command = this.policy.undoSkip(fence, (this.env.timers?.now ?? Date.now)());
        break;
      case 'watch-credits':
        if (prompt?.kind !== 'up-next' || prompt.offer.id !== action.offerId || !this.policy.dismiss(fence)) problem('playback_policy_unavailable');
        break;
      case 'play-now':
      case 'continue': {
        const held = stack.queue.getSnapshot().completion;
        const finished = !!held && held.sessionId === fence.sessionId && held.intentId === stack.service.getSnapshot().intentId;
        if (!view.next.available || view.next.entryId !== action.nextEntryId || view.postPlay.nextEntryId !== action.nextEntryId
          || (action.kind === 'continue' ? !finished || policy.attention !== 'still-watching' : policy.attention || !finished && prompt?.kind !== 'up-next')) problem('playback_policy_unavailable');
        command = this.policy.advance(fence, action.kind === 'continue' ? 'still-watching' : 'manual');
        break;
      }
      case 'cancel-autoplay': {
        const held = stack.queue.getSnapshot().completion;
        if (!held || held.sessionId !== fence.sessionId || held.intentId !== stack.service.getSnapshot().intentId || policy.attention || policy.replay || !held.nextAvailable) problem('playback_policy_unavailable');
        this.policy.cancelAutoplay(fence);
        break;
      }
      default: problem('playback_policy_unavailable');
    }
    if (action.kind === 'skip' || action.kind === 'undo' || action.kind === 'play-now' || action.kind === 'continue') {
      if (!command) problem('playback_policy_unavailable');
      if (command.kind === 'seek') {
        const from = stack.service.getSnapshot().positionSeconds;
        if (!await this.groupTransport('seek', fence, command.positionSeconds)) stack.service.seek(command.positionSeconds);
        if (command.reason === 'manual-skip') stack.service.markerSkipped(command.markerId, 'manual', from);
      } else if (command.kind === 'advance') { this.policyAdvanceFence = fence; await this.advancePolicy(stack, command); }
    }
    if (this.current(stack)) this.sync();
  }
  /** Root34's general Up next list uses only this captured server queue. */
  /** One admitted listening record over the existing chapters, lyrics and queue services.
   * It captures the exact original target/source and re-proves it after every external predicate;
   * any observed retirement disposes the record synchronously. No player/session/queue/engine is created. */
  captureListening(fence: PlaybackFence, lifetime: WebListeningLifetime): WebListeningPort | undefined {
    if (fence.kind !== 'queue' || !this.matches(fence) || this.stack.mode !== 'server' || this.group?.matches(fence)) return undefined;
    const stack = this.stack, view = stack.queue.getSnapshot().view, session = stack.queue.currentSession(), playback = stack.service.getSnapshot();
    const libraryId = stack.libraryId, versionId = session?.versionId;
    if (!view?.queue.id || view.queue.currentEntryId !== fence.entryId || !session || !libraryId || !versionId) return undefined;
    if (session.role !== 'local' || (session.kind !== 'vod' && session.kind !== 'audio')) return undefined;
    if (session.id !== fence.sessionId || session.itemId !== fence.itemId || session.presentation.generation !== fence.generation
      || session.queue?.queueId !== view.queue.id || session.queue.entryId !== fence.entryId) return undefined;
    const target = Object.freeze({libraryId, itemId: fence.itemId, sessionId: session.id, sessionGeneration: fence.generation,
      sourceId: fence.sourceId, versionId, playbackRun: playback.playbackRun, queueId: view.queue.id, entryId: fence.entryId});
    // Original transports are captured once; no later source lookup supplies them.
    const source = this.source, request = source.api.request.bind(source.api), requestLyrics = source.api.requestLyrics.bind(source.api);
    const combined = (command?: AbortSignal) => AbortSignal.any([stack.life.signal, this.life.signal, lifetime.signal, ...(command ? [command] : [])]);
    /** Exact original record: runtime/stack/source plus the captured target, run and queue occurrence. */
    const internal = (): boolean => {
      if (!this.current(stack) || !this.matches(fence) || this.group?.matches(fence) || this.source !== source) return false;
      const current = stack.queue.currentSession(), currentView = stack.queue.getSnapshot().view, currentPlayback = stack.service.getSnapshot();
      return !!current && !!currentView && stack.libraryId === target.libraryId
        && current.id === target.sessionId && current.itemId === target.itemId && current.versionId === target.versionId
        && current.presentation.generation === target.sessionGeneration && current.role === 'local'
        && current.queue?.queueId === target.queueId && current.queue.entryId === target.entryId
        && currentView.queue.id === target.queueId && currentView.queue.currentEntryId === target.entryId
        && currentPlayback.playbackRun === target.playbackRun;
    };
    /** The host callback may retire runtime or source, so the exact owner is re-proved after it. */
    const reprove = (): boolean => {
      if (!internal()) return false;
      if (lifetime.signal.aborted || !lifetime.isCurrent()) return false;
      return internal();
    };
    const call = <T>(work: (signal: AbortSignal) => Promise<T>, command?: AbortSignal): Promise<T> => {
      if (!reprove()) return Promise.reject(new Error('stale_listening_port'));
      return work(combined(command)).then(value => {if (!reprove()) throw new Error('stale_listening_port'); return value;});
    };
    const bindCommand = (command: WebListeningLifetime) => ({signal: combined(command.signal),
      isCurrent: () => !command.signal.aborted && command.isCurrent() && reprove()});
    /** Caller, runtime, stack, source and queue retirement all retire this record. */
    const observe = (retired: () => void) => {
      const check = () => {if (!reprove()) retired();};
      const offs = [this.subscribe(check), stack.queue.subscribe(check)];
      const lifeOff = () => {stack.life.signal.removeEventListener('abort', check); this.life.signal.removeEventListener('abort', check);};
      stack.life.signal.addEventListener('abort', check, {once: true});
      this.life.signal.addEventListener('abort', check, {once: true});
      return () => {for (const off of offs) off(); lifeOff();};
    };
    const queue = this.queueList(fence, lifetime);
    return createWebListeningPort({fence, lifetime, target, current: reprove, request, requestLyrics,
      chaptersTarget: {libraryId, itemId: fence.itemId, sessionId: session.id, sessionGeneration: fence.generation, sourceId: fence.sourceId},
      makeChapters: api => new PlayerChaptersService({api, scope: source.contentScope, pageSize: 100}),
      makeLyrics: api => new LyricsService(api, source.contentScope, {libraryId, itemId: fence.itemId, sessionId: session.id,
        sessionGeneration: fence.generation, sourceId: fence.sourceId}),
      lyricsEnabled: () => source.settings.lyricsEnabled?.() === true, observe, ...(queue ? {queue} : {}),
      policy: (expected, revision, intent, command) => {
        if (!reprove()) return Promise.reject(new Error('stale_listening_port'));
        return stack.queue.setQueuePolicy(expected, revision, intent, bindCommand(command));
      },
      save: (queueId, name, command, onProgress) => {
        if (!reprove()) return Promise.reject(new Error('stale_listening_port'));
        const bound = bindCommand(command);
        return stack.queue.saveAsPlaylist(queueId, name, save => {if (bound.isCurrent()) onProgress?.(save);}, bound.signal);
      },
      seek: (seconds, command) => {
        const bound = bindCommand(command);
        if (!bound.isCurrent()) return Promise.reject(new Error('stale_listening_port'));
        return this.seek(fence, seconds);
      },
      openEdit: (shown: WebQueueListSnapshot, command: WebListeningLifetime) => {
        if (!reprove()) throw new Error('stale_listening_port');
        const v1Target = {sessionId: target.sessionId, generation: target.sessionGeneration, playbackRun: target.playbackRun,
          itemId: target.itemId, versionId: target.versionId, queueId: target.queueId, entryId: target.entryId};
        /** Local proof from the exact original server stack and dock. Every external getter —
         * including the group callbacks — runs before one final pure owner/output sample that is
         * the value returned, so a takeover inside a getter cannot ride a stale earlier sample. */
        const readOutput = () => localEditOutput({originalStack: stack, originalSource: source, fence, target, same: sameFence,
          read: () => {
            const owner = () => {
              const stackNow = this.stack, dock = this.dock.getSnapshot(), snap = this.snapshot;
              const session = stackNow.queue.currentSession(), view = stackNow.queue.getSnapshot().view, playback = stackNow.service.getSnapshot();
              return {stack: stackNow, source: this.source, fence: snap.fence, presentation: !!snap.presentation,
                dockLocalFence: dock.local?.fence, dockRemote: dock.remote !== null,
                session: session ? {id: session.id, role: session.role, kind: session.kind, itemId: session.itemId, versionId: session.versionId,
                  generation: session.presentation.generation, queueId: session.queue?.queueId, entryId: session.queue?.entryId} : undefined,
                viewQueueId: view?.queue.id, viewEntryId: view?.queue.currentEntryId ?? undefined, playbackRun: playback.playbackRun};
            };
            const before = owner();
            const groupTaken = !!this.group?.active() || !!this.group?.matches(fence);
            // Runtime currentness is evaluated after the group callbacks, then one pure sample follows.
            const currentAfter = this.current(before.stack);
            const after = owner();
            const stable = after.stack === before.stack && after.playbackRun === before.playbackRun
              && after.dockLocalFence === before.dockLocalFence && after.fence === before.fence
              && after.viewQueueId === before.viewQueueId && after.viewEntryId === before.viewEntryId;
            return {...after, group: groupTaken, current: currentAfter && !groupTaken && stable};
          }});
        const reader = stack.queue.captureEditWindow(v1Target, shown.revision ?? '', {signal: combined(command.signal),
          isCurrent: () => admitCommand(reprove, command), readOutput});
        return createQueueEditOpening({reader, current: reprove, lifetime: command, currentEntryId: target.entryId,
          hidden: entryId => shown.entries.find(entry => entry.entryId === entryId)?.hidden === true});
      },
    });
  }

  queueList(fence: PlaybackFence, selectionLife?: WebQueueListLifetime): WebQueueListPort | undefined {
    if (fence.kind !== 'queue' || !this.matches(fence) || this.stack.mode !== 'server' || this.group?.matches(fence)) return;
    const stack = this.stack, view = stack.queue.getSnapshot().view, queueId = view?.queue.id;
    const session = stack.queue.currentSession(), playback = stack.service.getSnapshot();
    if (!view || !queueId || view.queue.currentEntryId !== fence.entryId || !session?.versionId
      || session.id !== fence.sessionId || session.itemId !== fence.itemId || session.presentation.generation !== fence.generation
      || session.queue?.queueId !== queueId || session.queue.entryId !== fence.entryId) return;
    const target = Object.freeze({sessionId: session.id, generation: fence.generation, playbackRun: playback.playbackRun,
      itemId: session.itemId, versionId: session.versionId, queueId, entryId: fence.entryId});
    const current = () => this.current(stack) && this.matches(fence) && !this.group?.matches(fence)
      && stack.queue.currentSession()?.versionId === target.versionId && stack.queue.getSnapshot().view?.queue.id === queueId;
    const selectable = session.kind === 'vod' && session.role === 'local' && !this.group?.active() && !!selectionLife;
    return createWebQueueListPort({fence, queueId, selectable, isCurrent: current,
      read: () => ({view: stack.queue.getSnapshot().view, workspace: stack.queue.workspace.service.getSnapshot()}),
      subscribe: listener => {const off = this.subscribe(listener), offQueue = stack.queue.subscribe(listener), offWorkspace = stack.queue.workspace.service.subscribe(listener);return () => {off();offQueue();offWorkspace();};},
      play: (entryId, revision, gesture) => {
        if (!selectable || !selectionLife || !current() || gesture.signal.aborted || !gesture.isCurrent()) problem('stale_playback_command');
        // The drawer proves this gesture before dispatch. The original route
        // then owns the command across its intended output/drawer replacement;
        // core owns the old-to-accepted output fence and late admission cleanup.
        const isCurrent = () => this.current(stack) && !this.group?.active() && !selectionLife.signal.aborted && selectionLife.isCurrent();
        if (!isCurrent()) problem('stale_playback_command');
        return stack.queue.selectQueuedEntry(target, {queueId, expectedRevision: revision, entryId},
          {signal: AbortSignal.any([stack.life.signal, this.life.signal, selectionLife.signal]), isCurrent});
      },
      edit: (revision, intent, lifetime) => {
        const isCurrent = () => current() && !lifetime.signal.aborted && lifetime.isCurrent();
        if (!isCurrent()) problem('stale_playback_command');
        return stack.queue.editQueue(target, revision, intent,
          {signal: AbortSignal.any([stack.life.signal, this.life.signal, lifetime.signal]), isCurrent});
      },
    });
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
        // A native command ACK is not FILE_LOADED/paused observation. Keep the exact
        // preparation alive until its current media port reports the real paused load.
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
    else if (!await this.groupTransport('pause', fence)) stack.service.pause();
  }
  async resume(fence: PlaybackFence): Promise<void> {
    const stack = this.offered(fence, 'play');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      if (fence.kind === 'local') stack.local!.controller.resume(fence);
      else if (!await this.groupTransport('play', fence)) stack.service.resume();
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
  /** The existing channel service's Go Live intent. No seek-to-zero, no second controller and
   * no ACK: the next published snapshot is the only source of the live-edge state. */
  goLive(fence: PlaybackFence): void {
    const stack = this.require(fence);
    if (fence.kind !== 'channel') problem('playback_command_unavailable');
    const live = stack.service.getSnapshot();
    if (!live.channel || live.channel.kind !== fence.channelKind || live.channel.sourceId !== fence.sourceId
      || live.channel.channelId !== fence.channelId || live.channel.generation !== fence.channelGeneration) problem('playback_command_unavailable');
    stack.service.goLive();
    if (this.current(stack)) this.sync();
  }
  async seek(fence: PlaybackFence, seconds: number): Promise<void> {
    if (!validSeconds(seconds)) problem('invalid_playback_position');const stack = this.offered(fence, 'seek');
    if (fence.kind === 'local') stack.local!.controller.seek(fence, seconds);
    else if (!await this.groupTransport('seek', fence, seconds)) stack.service.seek(Math.min(stack.service.getSnapshot().duration || Infinity, seconds));
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
        const view = currentPolicyQueue(this.snapshot, fence), held = queue.completion, receipt = stack.confirmedEnd;
        if (this.group?.active() || !stack.expanded || state.phase !== 'ended' || state.ended || queue.phase !== 'ready' || queue.error
          || !held || held.sessionId !== fence.sessionId || held.intentId !== state.intentId
          || !receipt || receipt.sessionId !== fence.sessionId || receipt.generation !== fence.generation || receipt.intentId !== state.intentId
          || !view || view === receipt.view || session?.id !== fence.sessionId || session.presentation.generation !== fence.generation
          || session.kind !== 'vod' || session.role !== 'local' || !session.versionId || session.itemId !== fence.itemId
          || session.queue?.queueId !== view.queue.id || session.queue.entryId !== fence.entryId) problem('playback_command_unavailable');
        const choices = {...stack.service.playbackChoices(), versionId: session.versionId, ...(session.discTitle ? {discTitle: session.discTitle} : {})};
        // The queue captures the original occurrence synchronously. Its own
        // generation guards the old-to-restarted handoff; this caller lifetime
        // survives that intended replacement, never another stack or room.
        await stack.queue.playItem(fence.itemId, 0, undefined, undefined, undefined, choices,
          {signal: AbortSignal.any([this.life.signal, stack.life.signal]), isCurrent: () => this.current(stack) && !this.group?.active(), propagateError: true});
      } else if (fence.kind === 'local') await stack.local!.controller.replay(fence);
      else await stack.service.play(fence.itemId, 0);
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  async previous(fence: PlaybackFence): Promise<void> {
    if (fence.kind === 'channel') problem('playback_command_unavailable');const stack = this.offered(fence, 'previous');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      if (fence.kind === 'local') await stack.local!.controller.previous(fence);else if (!await this.groupTransport('previous', fence)) await stack.queue.previous();
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  async next(fence: PlaybackFence): Promise<void> {
    if (fence.kind === 'channel') problem('playback_command_unavailable');const stack = this.offered(fence, 'next');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      if (fence.kind === 'local') await stack.local!.controller.next(fence);else if (!await this.groupTransport('next', fence)) {
        const view = stack.queue.getSnapshot().view;if (!view) problem('playback_command_unavailable');await stack.queue.next(view.queue.id, view.queue.revision);
      }
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  /** An in-player episode pick (desktop-player f6): jump inside this run's queue and keep its
   * choices. The queue player resolves an entry beyond the resident window itself; a foreign
   * queue, a channel or a retired fence is refused rather than replacing the playback. */
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

  async onTransition(transition: DockTransition): Promise<void> {
    this.assertCurrent();
    if (transition.state !== this.dock.getSnapshot()) problem('stale_dock_transition');
    let retentionOperation = this.operation;
    const finishRetention = transition.effects.length ? this.retainMediaStart(this.life.signal, () => retentionOperation === this.operation) : () => {};
    try {
      this.replacing = true;
      try { for (const effect of transition.effects) {
        const key = this.key(effect.playback.fence);
        if (effect.kind === 'restore') {
          const bookmark = this.bookmarks.get(key);
          const fence = effect.playback.fence;
          if (!bookmark) problem('playback_bookmark_unavailable');
          if (fence.kind === 'local') {
            const saved = bookmark.local;
            if (!saved || saved.lease.released || saved.lease.preparation.items[saved.index]?.id !== fence.entryId) problem('playback_bookmark_unavailable');
            const serial = ++this.operation; retentionOperation = serial;
            const stack = this.replace('local'), controller = this.bindLocalQueue(stack, saved.lease);
            stack.expanded = false;
            await controller.start({signal: stack.life.signal, isCurrent: () => this.current(stack) && serial === this.operation}, {index: saved.index, startPositionMs: saved.positionMs, autoplay: false});
            if (!this.current(stack) || serial !== this.operation) return;
            const accepted = await this.awaitLocalPaused(stack, serial);
            if (accepted.sessionId === fence.sessionId) problem('playback_restore_failed');
            continue;
          }
          const queueId = bookmark.queueId, channel = bookmark.channel?.channel;
          if (fence.kind === 'queue' && !queueId || fence.kind === 'channel' && !channel) problem('playback_bookmark_unavailable');
          if (fence.kind === 'channel' && channel && (channel.id !== fence.channelId || channel.provenance !== fence.channelKind || channel.sourceId !== fence.sourceId || channel.generation !== fence.channelGeneration)) problem('stale_channel_bookmark');
          const serial = ++this.operation; retentionOperation = serial;
          const stack = this.replace();
          stack.subject = bookmark.subject; stack.itemId = fence.kind === 'queue' ? fence.itemId : undefined; stack.libraryId = bookmark.libraryId; stack.channel = bookmark.channel;
          stack.posterUrl = bookmark.posterUrl; stack.backdropUrl = bookmark.backdropUrl; stack.expanded = false;
          if (fence.kind === 'queue') {
            if (!queueId) problem('playback_bookmark_unavailable');
            await stack.queue.restorePaused(queueId, fence.entryId, effect.playback.positionSeconds);
          } else {
            if (!channel) problem('playback_bookmark_unavailable');
            await stack.service.playChannel({kind: channel.provenance, sourceId: channel.sourceId, channelId: channel.id, generation: channel.generation}, {state: 'paused'});
          }
          if (!this.current(stack) || serial !== this.operation) return;
          this.sync();
          const accepted = await this.awaitFence(stack, serial);
          if (stack.service.getSnapshot().intent !== 'paused') problem('playback_restore_failed');
          if (fence.kind === 'channel') {
            if (accepted.kind !== 'channel') problem('playback_restore_failed');
            const linear = stack.service.getSnapshot().linear, target = effect.playback.positionSeconds;
            if (!linear || target < Number(linear.media.windowStartUs) / 1e6 || target > Number(linear.media.windowEndUs) / 1e6) { this.runtimeProblem = 'channel_bookmark_expired'; this.sync(); problem('channel_bookmark_expired'); }
            // The attached real paused session owns this retained window. An expired bookmark
            // is reported above, never clamped or represented as a successful restoration.
            stack.service.seek(target);
          }
        } else if (this.snapshot.fence && sameFence(this.snapshot.fence, effect.playback.fence)) {
          if (this.stack.mode === 'local') this.stack.web.slot.pause();else this.stack.service.pause();this.rememberCurrent();
          if (effect.kind === 'stop') this.retire(this.stack);
        } else if (!this.retiredFences.has(key)) problem('stale_dock_effect');
      } } finally { this.replacing = false; }
      if (this.stack.retired) { this.stack = this.makeStack(); this.activate(this.stack); }
      this.sync();
    } finally {finishRetention();}
  }

  async restoreBookmark(expected: PlaybackFence, playExplicitly = false): Promise<PlaybackFence> {
    this.assertCurrent();
    const state = this.dock.getSnapshot();
    const transition = sameFence(state.parkedAudio?.fence, expected) ? this.dock.restoreAudio() : sameFence(state.resumeVideo?.fence, expected) ? this.dock.restoreVideo() : undefined;
    if (!transition) problem('stale_playback_command');
    const serial = this.operation + 1;
    await this.onTransition(transition);
    if (serial !== this.operation) problem('stale_playback_command');
    const stack = this.stack, fence = expected.kind === 'local' ? stack.local?.controller.getSnapshot().fence : await this.awaitFence(stack, serial);
    if (!fence) problem('playback_restore_failed');
    if (fence.kind !== expected.kind || fence.sessionId === expected.sessionId) problem('playback_restore_failed');
    if (playExplicitly) await this.resume(fence);
    return fence;
  }

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
  async selectSubtitle(fence: PlaybackFence, trackId: string | null): Promise<void> {
    if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
    const stack = this.require(fence), plan = stack.subtitles?.getSnapshot().plan ?? this.snapshot.offers?.subtitlePlan;
    const track = trackId === null ? null : plan?.resources.find(resource => resource.id === trackId && resource.sourceId === fence.sourceId && resource.enabled);
    if (!plan || trackId !== null && !track) problem('playback_subtitle_unavailable');
    const offsetMs = track ? Number(track.offsetUs) / 1000 : undefined;
    // The replan wire is integer milliseconds. Preserve the actual signed correction
    // exactly, including zero; a fractional correction cannot be rounded into another clock.
    if (track && !Number.isSafeInteger(offsetMs)) problem('playback_subtitle_unavailable');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      // A track this playback could not draw is asked for in the picture again, not handed over.
      await stack.service.changePlayback({subtitleTrackId: trackId, ...(track ? {subtitleOffsetMs: offsetMs} : {}), ...(this.undrawable(stack, trackId) ? {subtitleDelivery:'burn' as const} : track && this.airPlayCanReplan() ? {subtitleDelivery:track.renderer === 'external_text' ? 'sidecar' as const : 'embeddedClient' as const} : {})});
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  /** Shifts the drawn track's own timing without changing which track is on. The plan must be
   * the one this full fence names and the track must still be that source's selected,
   * deliverable resource; whole milliseconds only, inside the plan's own signed bound. */
  async setSubtitleOffset(fence: PlaybackFence, offsetMs: number): Promise<void> {
    if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
    const stack = this.require(fence), plan = stack.subtitles?.getSnapshot().plan ?? this.snapshot.offers?.subtitlePlan;
    if (!plan || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation || plan.sourceId !== fence.sourceId
      || plan.mode !== 'track' || !plan.selected) problem('playback_subtitle_unavailable');
    const track = plan.selected;
    if (track.sourceId !== fence.sourceId || track.retired === true || !track.enabled) problem('playback_subtitle_unavailable');
    // A correction the replan wire cannot carry is refused, never rounded into another clock.
    if (!Number.isSafeInteger(offsetMs) || Math.abs(offsetMs) > 600000) problem('playback_subtitle_unavailable');
    const finishRetention = this.retainMediaStart(stack.life.signal, () => this.current(stack));
    try {
      await stack.service.changePlayback({subtitleTrackId: track.id, subtitleOffsetMs: offsetMs, ...(this.undrawable(stack, track.id) ? {subtitleDelivery:'burn' as const} : this.airPlayCanReplan() ? {subtitleDelivery:track.renderer === 'external_text' ? 'sidecar' as const : 'embeddedClient' as const} : {})});
      if (this.current(stack)) this.sync();
    } finally {finishRetention();}
  }
  private playbackRun(): number { return this.snapshot?.playback.playbackRun ?? -1; }
  private subtitlePreferences(stack: Stack): SubtitlePreferences | undefined {
    const saved = this.source.settings.subtitlePreferences?.();
    if (!saved) return undefined;
    const override = stack.subtitleStyle && stack.subtitleStyle.run === this.playbackRun() ? stack.subtitleStyle : undefined;
    if (!override) return saved;
    // An explicit size or background chosen in the player is drawn by this page, even where the
    // saved preference delegates to the system caption style; the saved value is not written.
    return {...saved, ...(override.size ? {size: override.size} : {}), ...(override.background ? {background: override.background} : {}), useSystemCaptionStyle: false};
  }
  /** Only the page-drawn text renderer on this runtime's own surface can honour a style: a text
   * track, or a styled track shown as plain lines. */
  private canStyleSubtitles(stack: Stack): boolean {
    if (!this.surface || stack.retired || this.snapshot?.nativeVideoSurfaceActive === true) return false;
    const plan = stack.subtitles?.getSnapshot().plan ?? this.snapshot?.offers?.subtitlePlan;
    return !!plan && (plan.renderer === 'external_text' || plan.renderer === 'client' && stack.drawnPlain === true);
  }
  /** The style this playback actually draws with: the saved preferences plus any change made
   * here. Undefined where the host supplies no preferences at all. */
  subtitleStyle(fence: PlaybackFence): Readonly<Pick<SubtitlePreferences,'size'|'background'>> | undefined {
    if (fence.kind !== 'queue' || !this.matches(fence) || !this.canStyleSubtitles(this.stack)) return undefined;
    const preferences = this.subtitlePreferences(this.stack);
    return preferences ? Object.freeze({size: preferences.size, background: preferences.background}) : undefined;
  }
  /** desktop-player f3: size and background last until this playback stops. The change reaches
   * only this stack's own surface; the saved preferences are never written. */
  setSubtitleStyle(fence: PlaybackFence, patch: Readonly<{size?: SubtitlePreferences['size']; background?: SubtitlePreferences['background']}>): void {
    if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
    const stack = this.require(fence);
    if (!this.canStyleSubtitles(stack)) problem('playback_subtitle_unavailable');
    if (patch.size !== undefined && !['small','medium','large','extra-large'].includes(patch.size)) problem('playback_subtitle_unavailable');
    if (patch.background !== undefined && !['none','translucent','opaque'].includes(patch.background)) problem('playback_subtitle_unavailable');
    const run = this.playbackRun(), held = stack.subtitleStyle?.run === run ? stack.subtitleStyle : undefined;
    stack.subtitleStyle = Object.freeze({run, ...(held?.size ? {size: held.size} : {}), ...(held?.background ? {background: held.background} : {}),
      ...(patch.size ? {size: patch.size} : {}), ...(patch.background ? {background: patch.background} : {})});
    this.surface?.applyPreferences(this.subtitlePreferences(stack));
    this.sync();
  }
  /** The chosen image track's own forced-line offer. Only a mixed track (some forced, some not)
   * from the exact source and revision still on screen has a switch. */
  subtitleForced(fence: PlaybackFence): Readonly<{only: boolean; lines: SubtitleLines}> | undefined {
    if (fence.kind !== 'queue' || !this.matches(fence)) return undefined;
    const stack = this.stack, offer = stack.forcedLines;
    // The offer belongs to the one image load that produced it, on screen, in this stack.
    if (!stack.drawnSurface || !stack.expanded || stack.drawnFailed || !offer || !stack.drawnKey) return undefined;
    if (offer.key !== stack.drawnKey || offer.epoch !== stack.drawnEpoch) return undefined;
    return Object.freeze({only: stack.forcedOnly === true, lines: offer.lines});
  }
  setSubtitleForcedOnly(fence: PlaybackFence, on: boolean): void {
    if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
    const stack = this.require(fence), offer = stack.forcedLines;
    if (!stack.drawnSurface || !stack.expanded || stack.drawnFailed || !offer || offer.key !== stack.drawnKey || offer.epoch !== stack.drawnEpoch) problem('playback_subtitle_unavailable');
    stack.forcedOnly = on === true;
    stack.drawnSurface.setForcedOnly(stack.forcedOnly);
    this.sync();
  }
  /** desktop-player f3: the drawn sleep timer. Only this stack's own local playback is
   * paused when the time is up; a group is never paused on a member's behalf. */
  sleepTimer(fence: PlaybackFence): Readonly<{minutes: number; endsAt: number}> | undefined {
    if (fence.kind !== 'queue' || !this.matches(fence)) return undefined;
    const timer = this.stack.sleepTimer;
    if (!timer) return undefined;
    // A different run or session means its allowed lifetime is over: retire the armed record
    // rather than leaving a timer the panel still shows as set.
    if (timer.run !== this.playbackRun() || timer.sessionId !== fence.sessionId) { this.clearSleepTimer(this.stack); return undefined; }
    return Object.freeze({minutes: timer.minutes, endsAt: timer.endsAt});
  }
  setSleepTimer(fence: PlaybackFence, minutes: number): void {
    if (fence.kind !== 'queue') problem('playback_sleep_unavailable');
    const stack = this.require(fence);
    if (!Number.isSafeInteger(minutes) || ![0, 15, 30, 45, 60, 90].includes(minutes)) problem('playback_sleep_unavailable');
    this.clearSleepTimer(stack);
    if (!minutes) { this.sync(); return; }
    const token = {}, run = this.playbackRun(), sessionId = fence.sessionId;
    const timer = this.setTimer(() => {
      // Only the record that armed this callback may act; a successor timer owns the stack now.
      if (stack.sleepTimer?.token !== token) return;
      this.clearSleepTimer(stack);
      if (!this.current(stack) || this.playbackRun() !== run || this.snapshot?.session?.id !== sessionId) { this.sync(); return; }
      if (this.group?.matches(fence)) { this.sync(); return; }
      try { stack.service.pause(); } catch { /* a retired session has nothing to pause */ }
      this.sync();
    }, minutes * 60_000);
    stack.sleepTimer = Object.freeze({minutes, endsAt: (this.env.timers?.now ?? Date.now)() + minutes * 60_000, timer, run, sessionId, token});
    this.sync();
  }
  private clearSleepTimer(stack: Stack): void {
    const timer = stack.sleepTimer;
    if (!timer) return;
    stack.sleepTimer = undefined;
    this.clearTimer(timer.timer);
  }
  /** The server's own subtitle-provider facts for this fence's plan. Read-only: whether
   * subtitles found online can be searched at all, and the server's reason when they cannot. */
  subtitleProvider(fence: PlaybackFence): Readonly<{enabled: boolean; reason?: string}> | undefined {
    if (fence.kind !== 'queue' || !this.matches(fence)) return undefined;
    const state = this.stack.subtitles?.getSnapshot(), plan = state?.plan, catalog = state?.catalog;
    if (!plan || !catalog || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation
      || plan.sourceId !== fence.sourceId || catalog.itemId !== fence.itemId) return undefined;
    const provider = catalog.provider;
    return Object.freeze({enabled: provider.enabled, ...(provider.reason ? {reason: provider.reason} : {})});
  }
  /** Drawn temporary choices for this actual subject; music and live have no speed picker. */
  speedChoices(fence: PlaybackFence): readonly number[] {
    const stack = this.require(fence);
    if (this.group?.matches(fence)) return Object.freeze([]);
    return Object.freeze([...(fence.kind === 'channel' || stack.subject?.kind === 'music' ? [] : stack.subject?.kind === 'audiobook' ? audiobookRates : stack.subject?.kind === 'video' ? listeningRates : [])]);
  }
  setSpeed(fence: PlaybackFence, speed: number): void {
    const stack = this.require(fence);
    const fixed = stack.subject?.kind === 'music' || fence.kind === 'channel';
    if (!Number.isFinite(speed) || (fixed ? speed !== 1 : !this.speedChoices(fence).includes(speed))) problem('playback_speed_unavailable');
    this.choices.set('speed', fence.playback, speed); stack.service.setPlaybackRate(speed); stack.web.setRate(speed); this.sync();
  }
  stepSpeed(fence: PlaybackFence, direction: -1 | 1): void {
    this.require(fence);
    const presentation = this.snapshot.presentation;
    if ((direction !== -1 && direction !== 1) || !presentation?.expanded || presentation.finished || this.snapshot.dock.remote?.phase === 'playing') problem('playback_speed_unavailable');
    const offered = this.speedChoices(fence), rate = presentation.rate;
    if (!offered.length) problem('playback_speed_unavailable');
    const next = direction === 1 ? offered.find(value => value > rate) : [...offered].reverse().find(value => value < rate);
    if (next !== undefined) this.setSpeed(fence, next);
  }
  /** Browser transports belong only to this loaded, unheld HTML-media source.
   * A native factory or the separate music renderer cannot lend its idle video. */
  browserMediaReady(fence: PlaybackFence): boolean {
    if (!this.matches(fence) || fence.kind === 'local' || this.env.createPlayer || this.stack.mode !== 'server') return false;
    const slot = this.stack.web.slot, observed = slot.observe();
    return slot.loaded === fence.generation && !slot.settling && observed.generation === fence.generation
      && ['playing', 'paused', 'buffering'].includes(observed.state) && this.snapshot.dock.remote?.phase !== 'playing';
  }

  private airPlayCanReplan(): boolean {
    return !this.env.createPlayer && typeof this.video?.webkitShowPlaybackTargetPicker === 'function' && !!this.video.canPlayType('application/vnd.apple.mpegurl');
  }

  /** A callable picker is not evidence that a receiver can fetch this generation. */
  browserAirPlayReady(fence: PlaybackFence): boolean {
    const receipt = this.stack.airPlay?.receipt, session = this.snapshot.session;
    return this.browserMediaReady(fence) && !!this.video && airPlaySourceReady(this.video, fence.generation)
      && receipt?.sessionId === fence.sessionId && receipt.generation === fence.generation
      && receipt.choiceRevision === session?.presentation.choiceRevision;
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
    // The port's actual ordinary observations own the frame's position; no seek or clock guess.
  }
  audioEngineFacts(fence: PlaybackFence) { return this.require(fence).web.audioFacts(); }
  async setEqualizer(fence: PlaybackFence, settings: EqualizerSettings): Promise<void> {
    const stack = this.require(fence);
    if (stack.subject?.kind !== 'music') problem('equalizer_unavailable');
    if (this.equalizerPort) await this.equalizerPort.change(settings);
    else await stack.web.setEqualizer(settings);
    if (this.current(stack)) this.sync();
  }
  refreshSettings(): void { this.assertCurrent(); this.stack.queue.listening.refreshAudiobookPreferences(); this.surface?.applyPreferences(this.subtitlePreferences(this.stack)); this.stack.service.adoptAudioEffectsPreference(this.source.settings.audioEffects()); this.sync(); }

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
