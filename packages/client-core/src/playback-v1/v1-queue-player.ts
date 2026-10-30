import type {ResyncReadContext, ResyncRegistration} from '../resync-coordinator.ts';
/**
 * The device queue on Playback Protocol v1 (Plan — Client Playback Migration, core step): the
 * same `QueueController` surface as the /v2 `QueuePlayer`, over v1 queues (§8) and sessions (§5).
 * Playback runs through `PlaybackService`; sessions are started, adopted and ended through a
 * `QueueSessionTransport` (the web's session controller path, or the native apps' bridge).
 *
 * The server queue is the truth: the player holds its header and a window of at most 200
 * entries around the current one (Up Next), never the whole queue. Play and Shuffle are one
 * request when possible (`startPlayback`); a very large selection whose first entry the server
 * chooses later (`startPending`) is awaited. Next, Previous, a chosen entry and completion are
 * `:advance`, which starts the next session in the same request (`replacesSessionId`).
 *
 * `queue.updated` events (the device's `/v1/events` stream) refresh the view and wake a waiting
 * start; a command on an entry still being snapshotted retries (503 queue_building).
 *
 * Rendered audio (spec §18; gapless, crossfade): near the end of a track the player prepares the
 * next entry's audio (`:prepare-next`), the engine buffers its first window, and at the audio
 * boundary the player commits it (`:commit-next`), which moves the queue and starts the next
 * session. Any failure falls back to the ordinary end-and-advance. Only when the server offers
 * `features.queueTransitions` (`transitions`); music uses v1 only behind the per-kind switch.
 *
 * Native listening (plan §9.3 C3; Apple): with `native`, an audio session is handed to the native
 * owner, which reports its timeline, holds its lease and moves the queue (`:advance`, and the §18
 * edges) while JS sleeps. This player then neither reports nor prepares for it; it sends the
 * viewer's queue moves to the owner and adopts the sessions the owner moves to (its snapshots'
 * `transition`), so Up Next follows.
 *
 * Changes to what is playing (quality, version, audio track) re-plan the live session
 * (`changePlayback`): the same session on the same entry, so the queue is never rebuilt. Playing
 * the current entry's title again (Replay, a retry on another route) restarts that entry in place.
 * Queue moves run one after another, and a session whose answer arrives after the viewer has
 * moved on is stopped rather than left to its lease.
 *
 * Not here yet: live channels (phase 6).
 */
import type {PlaybackService, PlaybackSession} from '../index.ts';
import type {AudiobookPreferences} from '../audiobook-preferences.ts';
import {serviceText} from '../presentation/service-text.ts';
import {playbackUserMessage} from '../playback-messages.ts';
import {ListeningService, loadListeningSelection, type ListeningTarget} from '../listening.ts';
import type {QueueAdvanceMode, QueuePostPlay} from '../post-play.ts';
import type {PreparedChoice} from '../prepared-media.ts';
import type {CompletionPolicy, HeldCompletion, QueueController, QueuePlaybackView, QueuePlaybackWindow, QueuePlayerState, QueueWorkspaceService} from '../queue-controller.ts';
import type {QueueEntry as LegacyEntry, QueueIntent, QueueItemInput, QueueScope, QueueSnapshot, QueueState} from '../queues.ts';
import {PlaybackApiError, call, idempotencyKey, type V1Http} from './http.ts';
import {toPlaybackSession} from './session-shape.ts';
import {retireQueuedSelectionRegistration, type QueuedSelectionRetirer, type QueueSelectionRegistration} from './selection-registration.ts';
import {audioTrackId, type PlaybackChoices} from './choices.ts';
import {requestInForce, type QualityRequest} from './quality.ts';
import {SessionsClient, type SessionChange, type StartContext, type StartOptions, type StartPreparation, type StartTarget} from './sessions.ts';
import {requireSessionChangeCurrent, sessionChangeCurrent, type SessionChangeContext} from './change-context.ts';
export type {SessionChangeContext} from './change-context.ts';
import {QUEUE_WINDOW_MAX, QueueClient, saveQueueAsPlaylist, queueWindowScope, queueHasObservedCurrent, type CreateResult, type QueueAnchor, type QueuePlaylistSave, type SegmentInput} from './queue.ts';
import {audioTransitionsEnabled} from '../audio-effects.ts';
import type {NativeListeningV1Control, NativeListeningV1Event} from '../playback/native-listening.ts';
import {playableAudio} from './audio-render.ts';
import {ContractError, consecutiveOnAlbum, id, parseDiscTitleRef, queueEntryFacts, type DiscTitleRef, type QueueEntry, type QueueHeader, type Selector, type ServerEvent, type Session} from './types.ts';

/** Items per `items` selector segment, and segments per create (spec §8). */
const SEGMENT_ITEMS = 500, MAX_SEGMENTS = 16;
const WINDOW_BEFORE = 20, WINDOW_AFTER = 50;
const MUSIC_CONTAINERS = ['album', 'artist', 'disc'];

export type V1Viewer = Readonly<{serverId: string; authority: 'local' | 'hosted'; accountId: string; profileId: string}>;
/** Captured by the player while this exact output is still selected. */
export type V1QueuedEntrySelection = Readonly<{queueId: string; expectedRevision: string; entryId: string}>;
export type V1QueueEditIntent = Readonly<{action: 'remove'; entryId: string} | {action: 'reorder'; entryIds: readonly string[]}>;
/** A bounded presentation of a real issued positional queue page. */
export type V1QueueEditPage = Readonly<{start: number; total: number; revision: string; items: readonly QueueEntry[]; nextCursor?: string; canPrevious: boolean}>;
/** Host-supplied evidence belongs to its original output fence; local session role alone is insufficient. */
export type V1QueueEditWindowContext = Readonly<{
  signal: AbortSignal;
  isCurrent: () => boolean;
  /** Must verify its original host/output after reading any potentially reentrant getters. */
  readOutput: () => Readonly<{kind: 'local'; target: V1PlaybackTarget}> | Readonly<{kind: 'remote' | 'unknown'}>;
}>;
export type V1QueueEditReader = Readonly<{
  read: () => Promise<V1QueueEditPage>;
  next: (page: V1QueueEditPage) => Promise<V1QueueEditPage>;
  previous: (page: V1QueueEditPage) => Promise<V1QueueEditPage>;
  edit: (page: V1QueueEditPage, intent: V1QueueEditIntent) => Promise<V1QueueEditPage>;
  dispose: () => void;
}>;
export type V1QueuePolicyIntent = Readonly<{action: 'repeat'; repeat: 'off' | 'one' | 'all'} | {action: 'shuffle'; shuffled: boolean}>;
export type V1PlaybackTarget = Readonly<{sessionId: string; generation: number; playbackRun: number; itemId: string; versionId: string; queueId: string; entryId: string}>;
/** Captured caller lifetime; its predicate must remain current across this start's own output change. */
export type V1PlayItemContext = Readonly<{signal?: AbortSignal; isCurrent?: () => boolean; state?: 'playing' | 'paused'; propagateError?: boolean}>;

/**
 * How the queue player starts, adopts and ends sessions. The web gives it the session controller's
 * transport; the native apps the bridge that reports through `PlaybackService`.
 */
export interface QueueSessionTransport {
  start(target: StartTarget, options: StartOptions, key?: string, durationSeconds?: number, signal?: AbortSignal,
    context?: Pick<StartContext, 'isCurrent' | 'capturePreparing' | 'onPreparing'>): Promise<{session: Session; playback: PlaybackSession}>;
  /** Tracks a session another request started (a queue create with startPlayback, an advance). */
  adopt(session: Session, durationSeconds: number): PlaybackSession;
  /** A returned accepted title reference pins its own duration; never read the main title instead. */
  duration(itemId: string, versionId?: string, signal?: AbortSignal, discTitle?: DiscTitleRef): Promise<number>;
  stopPlayback(id: string, signal?: AbortSignal): Promise<void>;
  /** The server replaced or completed this session: nothing more is reported for it. */
  retire(id: string): void;
  forget(id: string): void;
  /** Bare id captures known absence only; adopted captures that returned object's original registration.
   * Omission or unknown ownership leaves selection cleanup closed. */
  selectionRegistration?(id: string, adopted?: PlaybackSession): QueueSelectionRegistration | undefined;
  /** Stop reporting a session a native owner takes over; the epoch it held, for the owner's claim. */
  handOverObserver(id: string): string | undefined;
  /** Read current authority/end after missed events, without issuing playback commands. */
  reconcileSession?(id: string, signal?: AbortSignal): Promise<void>;
  /** Re-plans a live session (quality, version, audio track): same session, next generation. */
  change?(id: string, change: SessionChange, durationSeconds: number, context?: SessionChangeContext): Promise<{session: Session; playback: PlaybackSession}>;
  /** Tells the queue player that a session it holds was re-planned behind it (a recovered player failure). */
  onSessionChanged?(listener: (session: Session) => void): () => void;
}

export type V1QueuePlayerOptions = Readonly<{
  /** Same viewer's current confirmed settings, owned by the caller lifetime. */
  audiobookPreferences?: () => AudiobookPreferences;
  http: V1Http;
  api: QueueSessionTransport;
  /** The quality a new playback starts with (the viewer's saved default for this network), when
   * the viewer has not chosen one in the player. Undefined leaves it to the server. */
  quality?: () => QualityRequest | undefined;
  /** Actual current/bookmarked queue IDs for this viewer, sampled once before a create intent awaits. */
  retainQueueIds?: () => readonly string[];
  playback: PlaybackService;
  viewer: V1Viewer;
  /** Original-principal cleanup; true means retired under the supplied ownership guard.
   * Refusal/throw/omission never falls back to another principal or raw forget. */
  retireQueuedSelection?: QueuedSelectionRetirer;
  key?: () => string;
  /** Poll interval while a start waits for its snapshot (tests shorten it). */
  pendingPollMs?: number;
  /** The device's `/v1/events` subscription: `queue.updated` refreshes the view (another
   * device's edit, a building segment growing) and wakes a start waiting for its snapshot. A
   * resync (updates were missed) does the same, since any of them may have been this queue's. */
  events?: Readonly<{on(typeOrPrefix: string, listener: (event: ServerEvent) => void): () => void; onResync?(listener: () => void): () => void; onResyncRead?(read: (context: ResyncReadContext) => Promise<void>, options?: {priority?: number}): Pick<ResyncRegistration, 'dispose'>}>;
  /** How long a command retries while its entry is still being snapshotted (queue_building). */
  buildingRetryMs?: number;
  /** The server offers queue transitions for rendered audio (spec §18,
   * `features.queueTransitions`): gapless and crossfade edges. */
  transitions?: boolean | (() => boolean);
  /** The viewer's `music.shuffleDefault` and `music.repeatDefault`, applied when a music queue
   * starts with Play (Shuffle is always a shuffle). Undefined: off and off. */
  musicDefaults?: () => Readonly<{shuffle: boolean; repeat: 'off' | 'one' | 'all'}> | undefined;
  /** The native listening owner (Apple): audio sessions are handed to it (plan §9.3 C3). */
  native?: NativeListeningV1Control;
  /** Which item kinds the native owner takes (`song`, `audiobook_file`…). Nothing by default: an
   * app turns a kind on only once the server offers it and the exit tests pass on a real server. */
  nativeFor?: (itemKind: string) => boolean;
}>;

const segments = (ids: readonly string[], shuffle: boolean): SegmentInput[] => {
  const out: SegmentInput[] = [];
  for (let i = 0; i < ids.length && out.length < MAX_SEGMENTS; i += SEGMENT_ITEMS) {
    out.push({source: {items: {ids: ids.slice(i, i + SEGMENT_ITEMS)}}, ...(i === 0 && shuffle ? {order: {mode: 'shuffle'}} : {})});
  }
  return out;
};

const message = (e: unknown, fallback: string): string => {
  if (e instanceof PlaybackApiError) {
    switch (e.code) {
      case 'conversion_capacity': return playbackUserMessage(e);
      case 'queue_too_large': return serviceText('error.queueTooLarge');
      case 'queue_building': return serviceText('queuePlayer.queueBuilding');
      case 'queue_ended': return serviceText('queuePlayer.queueEnded');
      case 'not_found': return serviceText('queuePlayer.notFound');
      case 'stream_limit_reached': return serviceText('queuePlayer.streamLimit');
      case 'transcode_not_allowed': return serviceText('queuePlayer.transcodeNotAllowed');
      case 'unsupported_media': return serviceText('queuePlayer.unsupported');
      case 'connection_too_slow': return serviceText('queuePlayer.connectionTooSlow');
    }
  }
  return fallback;
};

/** Entry kinds that are listening (music and books), in both vocabularies: the catalogue's
 * (song, audiobook_file) and the v1 queue window's (track, audiobook). */
const LISTENING_KINDS: ReadonlySet<string> = new Set(['song', 'track', 'audiobook_file', 'audiobook']);

export class V1QueuePlayer implements QueueController {
  readonly workspace: Readonly<{service: QueueWorkspaceService}>;
  readonly listening: ListeningService;
  private readonly client: QueueClient;
  private readonly api: QueueSessionTransport;
  private readonly retireQueuedSelection?: V1QueuePlayerOptions['retireQueuedSelection'];
  private readonly defaultQuality?: () => QualityRequest | undefined;
  private readonly retainQueueIds?: () => readonly string[];
  /** The quality chosen in the player for this viewing: it stays while the queue plays on. */
  private qualityOverride?: QualityRequest;
  /** Queue moves run one at a time: a second Next waits for the first and moves on from there. */
  private moves: Promise<unknown> = Promise.resolve();
  /** Rises when the viewer leaves or starts something else: a queue move still waiting its turn is then dropped. */
  private epoch = 0;
  private readonly playback: PlaybackService;
  private readonly http: V1Http;
  private readonly scope: QueueScope;
  private readonly key: () => string;
  private readonly pendingPollMs: number;
  private header?: QueueHeader;
  private window: readonly QueueEntry[] = [];
  private session?: Session;
  private state: QueuePlayerState = Object.freeze({phase: 'ready', view: null, error: null, completion: null});
  private queueState: QueueState = Object.freeze({queue: null, phase: 'idle', error: null});
  private readonly listeners = new Set<() => void>();
  private readonly queueListeners = new Set<() => void>();
  private completionPolicy: CompletionPolicy = () => 'advance';
  private endedId?: string;
  private completionRetry?: ReturnType<typeof setTimeout>;
  private completionFailures = 0;
  private generation = 0;
  private preparingStart?: {generation: number; intent: number; notice?: StartPreparation};
  private changeSequence = 0;
  private policySequence = 0;
  private refreshSequence = 0;
  private disposed = false;
  private detach?: () => void;
  private unsubscribe?: () => void;
  private readonly events?: V1QueuePlayerOptions['events'];
  private unlisten?: () => void;
  private unlistenSessions?: () => void;
  private unlistenResync?: () => void;
  private unlistenChanged?: () => void;
  private wakePending?: () => void;
  private refreshTimer?: ReturnType<typeof setTimeout>;
  private readonly buildingRetryMs: number;
  // Rendered-audio edges (§18): one try per track; the prepared token until its boundary.
  /** Read when used: a queue made before the server's capabilities arrived follows them once they do. */
  private readonly transitionsNow: () => boolean;
  private get transitions(): boolean { return this.transitionsNow(); }
  private readonly musicDefaults?: V1QueuePlayerOptions['musicDefaults'];
  private audioAttempt?: string;
  private prepared?: Readonly<{token: string; queueId: string; itemId: string; expiresAtMs: number; durationSeconds: number}>;
  private audioReadyToken?: string;
  private audioCommitting = false;
  // Native listening (C3): the owner, and which handover its snapshots belong to.
  private readonly native?: NativeListeningV1Control;
  private readonly nativeFor: (itemKind: string) => boolean;
  private nativeEpoch = 0;
  private nativeSession?: string;
  private sessionStartedAt = 0;

  constructor(options: V1QueuePlayerOptions) {
    const transitions = options.transitions;
    this.transitionsNow = typeof transitions === 'function' ? transitions : () => transitions === true;
    this.musicDefaults = options.musicDefaults;
    this.native = options.native;
    this.nativeFor = options.nativeFor ?? (() => false);
    this.http = options.http;
    this.api = options.api;
    this.retireQueuedSelection = options.retireQueuedSelection;
    // A session re-planned behind the queue (a player failure the service recovered from) is the
    // same session with a new presentation: keep the copy the queue commands read in step.
    this.unlistenChanged = this.api.onSessionChanged?.(session => { if (this.session?.id === session.id) this.session = session; });
    this.defaultQuality = options.quality;
    this.retainQueueIds = options.retainQueueIds;
    this.playback = options.playback;
    this.key = options.key ?? (() => idempotencyKey());
    this.client = new QueueClient(options.http, this.key);
    this.pendingPollMs = options.pendingPollMs ?? 1000;
    this.events = options.events;
    this.buildingRetryMs = options.buildingRetryMs ?? 30_000;
    // The listening service speaks plain v1 REST (preferences, selections); it has no controller lane.
    this.scope = Object.freeze({...options.viewer, controllerId: 'v1', controllerEpoch: 'v1', commandLaneId: 'v1'});
    // PERF-24: a video session never asks for listening details.
    this.listening = new ListeningService(this.scope, (path, body, method, signal) => this.request(path, body, method, signal), options.playback, undefined, (itemId, sessionId) => this.session?.id !== sessionId || this.session.itemId !== itemId || this.session.kind === 'audio', options.audiobookPreferences, (sessionId,generation) => this.session?.id===sessionId&&this.session.presentation.generation===generation&&this.session.queue?Object.freeze({...this.session.queue}):null);
    const self = this;
    this.workspace = Object.freeze({service: {
      getSnapshot: () => self.queueState,
      subscribe: (fn: () => void) => { self.queueListeners.add(fn); return () => { self.queueListeners.delete(fn); }; },
      mutate: (intent: QueueIntent) => self.mutate(intent),
    } satisfies QueueWorkspaceService});
  }

  private request(path: string, body?: unknown, method: 'GET' | 'POST' | 'PUT' = 'GET', signal?: AbortSignal): Promise<unknown> {
    return call(this.http, {method, path, body, signal}).then(r => r.body);
  }

  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };

  private publish(patch: Partial<QueuePlayerState>) {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch,
      preparation: this.preparingStart?.generation === this.generation ? this.preparingStart.notice : undefined});
    for (const fn of this.listeners) fn();
  }
  private publishQueue(patch: Partial<QueueState>) {
    if (this.disposed) return;
    this.queueState = Object.freeze({...this.queueState, ...patch});
    for (const fn of this.queueListeners) fn();
  }

  connect() {
    this.detach = this.playback.installQueue(this);
    this.listening.connect();
    this.unsubscribe = this.playback.subscribe(() => this.observed());
    this.unlisten = this.events?.on('queue.updated', event => this.queueEvent(event));
    // Rereads the queue as it is now: the header and the window around its actual current entry.
    // It never advances, commits or starts anything.
    if (this.events?.onResyncRead) {
      const recovery = this.events.onResyncRead(context => this.resyncCurrent(context), {priority: 0});
      this.unlistenResync = () => recovery.dispose();
    } else this.unlistenResync = this.events?.onResync?.(() => {
      // Compatibility ports still recover state through the same read-only path.
      void this.resyncCurrent({signal: new AbortController().signal, isCurrent: () => !this.disposed}).catch(() => {});
    });
    // An administrator's terminate reaches a native owner at once, not at its next report.
    this.unlistenSessions = this.events?.on('session.updated', event => {
      if (!this.nativeOwns() || event.resource?.id !== this.nativeSession) return;
      // The owner's own doing is not news to it: a track it reported finished ("ended"), or one a
      // gapless commit moved past ("completed"). Told about those, it would stop instead of advancing.
      const data = event.data as {state?: unknown; reason?: unknown} | undefined;
      if (data?.state === 'ended' && (data.reason === 'ended' || data.reason === 'completed')) return;
      void this.native!.sessionUpdated?.(this.nativeSession!).catch(() => {});
    });
    return () => this.dispose();
  }

  private async resyncCurrent(context: ResyncReadContext): Promise<void> {
    if (this.disposed || context.signal.aborted) return;
    const epoch = this.nativeEpoch, generation = this.generation;
    const current = () => !this.disposed && context.isCurrent() && !context.signal.aborted && epoch === this.nativeEpoch && generation === this.generation;
    this.wakePending?.();
    // Adopt a committed native successor first. Never ask the owner to reconcile
    // the predecessor JS last saw, or infer normal EOS from a missed hint.
    if (this.nativeOwns()) {
      await this.native!.refresh();
      if (!current()) return;
      if (this.nativeOwns()) {
        const sessionId = this.nativeSession!;
        // Prove current server state under this read's admission. The existing
        // native bridge then reconciles its own exact current/audible binding;
        // JS neither invents a native receipt nor classifies a missing row as EOS.
        try {await new SessionsClient(this.http).get(sessionId, context.signal);}
        catch (error) {if (!(error instanceof PlaybackApiError && (error.status === 404 || error.status === 410))) throw error;}
        if (current() && this.nativeOwns() && this.nativeSession === sessionId) await this.native!.sessionUpdated?.(sessionId);
      }
      if (!current()) return;
    } else if (this.session) {
      // Includes the existing SessionEnd reason. This is observation only: no
      // advance, commit, restart or new operation identity is produced.
      await this.api.reconcileSession?.(this.session.id, context.signal);
    }
    if (current()) await this.refresh(context.signal);
  }

  /** Another device's edit, a building segment growing, or the server choosing a waiting
   * start: refresh the view once (coalesced), and wake a start that waits. */
  private queueEvent(event: ServerEvent) {
    if (this.disposed || !this.header || event.resource?.id !== this.header.id) return;
    this.wakePending?.();
    if (event.revision !== undefined && event.revision === this.header.revision) return;
    if (this.refreshTimer) return;
    this.refreshTimer = setTimeout(() => { this.refreshTimer = undefined; void this.refresh().catch(() => {}); }, 100);
  }

  /** Runs a queue command, retrying while its entry is still being snapshotted (503
   * queue_building), for at most buildingRetryMs. */
  private async whileBuilding<T>(work: () => Promise<T>): Promise<T> {
    const until = Date.now() + this.buildingRetryMs;
    for (;;) {
      try {
        return await work();
      } catch (e) {
        if (!(e instanceof PlaybackApiError && e.code === 'queue_building') || Date.now() >= until || this.disposed) throw e;
        this.publish({phase: 'preparing'});
        await new Promise(r => setTimeout(r, Math.min(Math.max(250, e.retryAfterMs ?? 2000), 5000)));
      }
    }
  }

  // ── The view ────────────────────────────────────────────────────────

  /** Reads the header and the window around the current entry, and rebuilds the view. */
  async refresh(signal?: AbortSignal): Promise<void> {
    const id = this.header?.id, generation = this.generation, sequence = ++this.refreshSequence;
    const current = () => !this.disposed && !signal?.aborted && this.header?.id === id
      && generation === this.generation && sequence === this.refreshSequence;
    if (!id || !current()) return;
    const header = await this.client.header(id, signal);
    if (!current()) return;
    const window = await this.client.window(id, WINDOW_BEFORE, WINDOW_AFTER, signal);
    if (current()) this.accept(header, window);
  }

  private accept(header: QueueHeader, window?: readonly QueueEntry[]) {
    this.header = header;
    if (window) this.window = window;
    const current = header.current;
    const entries: LegacyEntry[] = this.window.map(e => e.available && e.itemId
      ? Object.freeze({id: e.entryId, removed: false, hidden: false as const, itemId: e.itemId, editionId: null, partId: null, sourceContext: Object.freeze({kind: 'item' as const, id: e.itemId, revision: null, entryId: null})})
      : Object.freeze({id: e.entryId, removed: false, hidden: true as const, ...(e.kind === 'pending' ? {pending: true as const} : {})}));
    const at = current ? this.window.findIndex(e => e.entryId === current.entryId) : -1;
    const following = at >= 0 ? this.window[at + 1] : undefined;
    const reason: 'ready' | 'end' | 'unavailable' = header.next?.reason ?? (following ? (following.available ? 'ready' : 'unavailable') : header.repeat === 'all' && header.total > 0 ? 'ready' : 'end');
    const next = Object.freeze({entryId: header.next?.entryId ?? following?.entryId ?? null, available: header.next?.available ?? reason === 'ready', reason});
    const postPlay: QueuePostPlay = Object.freeze({nextEntryId: next.entryId, available: next.available, reason, countdownSeconds: header.postPlay?.countdownSeconds as 0|5|10|15 ?? 10, autoplay: header.postPlay?.autoplay ?? true, passoutCheckDue: header.postPlay?.passoutCheckDue ?? false, automaticAdvances: header.postPlay?.automaticAdvances ?? 0, startDuringCredits: header.postPlay?.startDuringCredits ?? false});
    const queue: QueueSnapshot = Object.freeze({
      id: header.id, scope: this.scope, revision: header.revision, highestSeenSequence: '0', replayWindow: Object.freeze({results: 256, bodies: 32}),
      repeat: header.repeat, shuffled: !!header.shuffle, currentEntryId: current?.entryId ?? null, currentPlaybackId: this.session?.id ?? null, entries: Object.freeze(entries),
    });
    const projection = queueWindowProjection(header, this.window);
    const view: QueuePlaybackView = Object.freeze({
      ...(projection ? {window: projection} : {}),
      queue, highestSequence: '0', total:header.total, remainingCount:header.remainingCount, totalDurationMs:header.totalDurationMs, durationKnown:header.durationKnown,
      current: this.session ? Object.freeze({id: this.session.id, generation: this.session.presentation.generation, state: this.session.state}) : null,
      next,
      items: Object.freeze(this.window.filter(e => e.itemId).map(e => {
        const {libraryId, ...item} = e;
        // Only this exact accepted loaded occurrence can offer its own library.
        return Object.freeze({...item, ...(projection && queueEntryFacts(e).available === true && libraryId ? {libraryId} : {})});
      })),
      postPlay,
    });
    this.publish({view, error: null});
    this.publishQueue({queue, phase: 'ready', error: null});
  }

  // ── Playing ─────────────────────────────────────────────────────────

  /**
   * Plays one title with the viewer's choices. A prepared copy and the older quality names have
   * no selector of their own on this protocol: the planner picks a prepared copy when it fits the
   * requested quality, so `choices.quality` is what reaches the server.
   */
  playItem(itemId: string, startSeconds?: number, _prepared?: PreparedChoice, _quality?: string, audioStream?: number, choices?: PlaybackChoices, context?: V1PlayItemContext): Promise<void> {
    if (this.disposed || context?.signal?.aborted || context?.isCurrent?.() === false) return Promise.reject(Object.assign(new Error('cancelled'), {name: 'AbortError'}));
    const track = choices?.audioTrackId ?? audioTrackId(audioStream);
    const chosen: PlaybackChoices = {...choices, ...(track ? {audioTrackId: track} : {}),
      ...(choices?.discTitle === undefined ? {} : {discTitle: parseDiscTitleRef(choices.discTitle)})};
    // Replay, a retry on another route: the title is the queue's current entry, which starts again
    // in place. The rest of the queue is untouched.
    const current = this.currentEntry();
    if (current && current.itemId === itemId && this.session?.itemId === itemId && !this.nativeOwns()) return this.restartCurrent(startSeconds, chosen, context?.state ?? 'playing', false, context);
    return this.play(segments([itemId], false), itemId, {startSeconds, ...context}, chosen).then(() => {});
  }

  /** A fresh start outside the player captures the entire show on the server.
   * An in-player episode pick then jumps in this queue and retains this run. */
  playEpisode(showId: string, episodeId: string, startSeconds?: number): Promise<void> {
    return this.play([{source:{container:{kind:'show',id:showId}},anchor:{itemId:episodeId}}],episodeId,{startSeconds}).then(()=>{});
  }

  /** Resolve beyond the resident window before using the ordinary run-preserving jump. */
  async selectEpisode(episodeId: string): Promise<void> {
    const header=this.header;
    if(!header)throw new PlaybackApiError(404,'not_found','never');
    const found=await this.client.entryForItem(header.id,episodeId);
    if(this.disposed||this.header?.id!==header.id)return;
    await this.playEntry(header.id,found.entry.entryId,found.revision);
  }

  // ── Changing what is playing ────────────────────────────────────────

  /** The session being played and the quality, engine and tracks in force on it. */
  currentSession(): Session | undefined { return this.session; }
  /** Choose a quality for this session and the rest of this viewing. */
  setQuality(quality: QualityRequest, throughputKbps?: number) { return this.changePlayback({quality, ...(throughputKbps !== undefined ? {throughputKbps: Math.max(0, Math.round(throughputKbps))} : {})}); }
  /** Automatic quality: one step down or up from where the session is, chosen by the server. */
  stepQuality(direction: 'down' | 'up') { return this.changePlayback({quality: {mode: 'automatic', step: direction}}); }
  setVersion(versionId: string) { return this.changePlayback({versionId}); }
  setAudioTrack(trackId: string) { return this.changePlayback({audioTrackId: trackId}); }
  /** `null` turns subtitles off. A text track comes back as a sidecar file; a styled or image
   * track is drawn by the engine (`presentation.subtitle.delivery === 'device'`) or burned in. */
  setSubtitleTrack(trackId: string | null) { return this.changePlayback({subtitleTrackId: trackId}); }

  playEntries(entries: readonly QueueItemInput[], options: {shuffle?: boolean; startSeconds?: number} = {}): Promise<void> {
    if (!entries.length) return Promise.reject(new Error('No available items were selected.'));
    return this.playIds(entries.map(e => e.itemId), options);
  }

  private currentEntry(): QueueEntry | undefined {
    const id = this.header?.current?.entryId;
    return id ? this.window.find(e => e.entryId === id) : undefined;
  }

  /** What a start asks for: the choices for this title, the quality chosen in the player for this
   * viewing, else the viewer's saved default. */
  private startChoices(choices?: PlaybackChoices): Pick<StartOptions, 'quality' | 'versionId' | 'discTitle' | 'audio'> {
    const quality = choices?.quality ?? this.qualityOverride ?? this.defaultQuality?.();
    const discTitle = parseDiscTitleRef(choices?.discTitle);
    if (discTitle && !choices?.versionId) throw new ContractError('disc title version');
    return {...(quality ? {quality} : {}), ...(choices?.versionId ? {versionId: choices.versionId} : {}), ...(discTitle ? {discTitle} : {}), ...(choices?.audioTrackId ? {audio: {trackId: choices.audioTrackId}} : {})};
  }

  /** A session whose request was overtaken (the viewer moved on while it was in flight) is ended
   * now: nobody will play or report it, and it would hold a stream until its lease ran out. */
  private abandon(session: Session | undefined) {
    if (!session) return;
    if (this.session?.id === session.id) this.session = undefined;
    void this.api.stopPlayback(session.id).catch(() => {});
  }

  /** Starts the current entry again in place: a new session for the same entry, replacing the old one. */
  private async restartCurrent(startSeconds: number | undefined, choices: PlaybackChoices, state: 'playing' | 'paused' = 'playing', keepIntent = false, context?: V1PlayItemContext): Promise<void> {
    if (context?.signal?.aborted || context?.isCurrent?.() === false) throw Object.assign(new Error('cancelled'), {name: 'AbortError'});
    const header = this.header, entry = this.currentEntry();
    if (!header?.current) return;
    const generation = ++this.generation;
    ++this.epoch;
    const current = () => !this.disposed && generation === this.generation && !context?.signal?.aborted && context?.isCurrent?.() !== false;
    const cancel = () => { if (generation === this.generation && !this.disposed) this.playback.leave(); };
    context?.signal?.addEventListener('abort', cancel, {once: true});
    this.listening.interact();
    if (choices.quality) this.qualityOverride = choices.quality;
    const intent = keepIntent ? this.playback.getSnapshot().intentId : this.playback.beginQueueIntent(entry?.itemId);
    this.publish({phase: 'preparing', error: null, completion: null});
    try {
      if (!keepIntent && (!await this.playback.prepareQueueIntent(intent) || !current())) return;
      await this.startEntry(header.id, header.current.entryId, intent, startSeconds, generation, choices, state, context?.signal, context ? current : undefined);
      if (context?.propagateError && current() && this.playback.getSnapshot().phase === 'error') throw new ContractError('queue playback adoption');
      if (current()) this.publish({phase: 'ready'});
    } catch (e) {
      if (!current()) {if (context?.propagateError) throw new DOMException('Playback start retired', 'AbortError'); return;}
      this.fail(entry?.itemId ?? '', e, serviceText('queuePlayer.startFailed'));
      if (context?.propagateError) throw e;
    } finally { context?.signal?.removeEventListener('abort', cancel); }
  }

  /**
   * Changes the quality, version, audio track or subtitle track of what is playing: the live session is
   * re-planned at its position and stays the same session on the same entry. A session the
   * server no longer has (its lease lapsed) starts again in place with the choice instead.
   */
  changePlayback(choices: PlaybackChoices): Promise<void>;
  changePlayback(choices: PlaybackChoices, context: SessionChangeContext): Promise<Session | undefined>;
  async changePlayback(choices: PlaybackChoices, context?: SessionChangeContext): Promise<Session | void> {
    requireSessionChangeCurrent(context);
    if (choices.discTitle !== undefined) throw new ContractError('disc title requires a session start');
    const session = this.session;
    if (this.disposed || !session || this.nativeOwns()) {
      if (context) throw new PlaybackApiError(410, 'session_ended', 'never');
      return;
    }
    const sequence = ++this.changeSequence;
    if (!context && choices.quality && !('step' in choices.quality && choices.quality.step)) this.qualityOverride = choices.quality;
    const change: SessionChange = {...(choices.replan ? {replan: true} : {}), ...(choices.quality ? {quality: choices.quality} : {}), ...(choices.versionId ? {versionId: choices.versionId} : {}), ...(choices.audioTrackId ? {audio: {trackId: choices.audioTrackId}} : {}),
      ...(choices.subtitleTrackId !== undefined ? {subtitles: {trackId: choices.subtitleTrackId, ...(choices.subtitleTrackId !== null && choices.subtitleDelivery ? {delivery: choices.subtitleDelivery} : {})}} : {}),
      ...(choices.subtitleTrackId && choices.subtitleOffsetUs !== undefined ? {subtitleOffsetUs: choices.subtitleOffsetUs} : {}),
      ...(choices.subtitleTrackId && choices.subtitleOffsetUs === undefined && choices.subtitleOffsetMs !== undefined && Number.isFinite(choices.subtitleOffsetMs) ? {subtitleOffsetMs: Math.round(choices.subtitleOffsetMs)} : {}), ...(choices.throughputKbps !== undefined ? {throughputKbps: choices.throughputKbps} : {})};
    if (!Object.keys(change).length) return context ? session : undefined;
    const generation = this.generation;
    const live = () => !this.disposed && generation === this.generation && this.session?.id === session.id
      && (!context || sequence === this.changeSequence && sessionChangeCurrent(context)
        && this.session.presentation.generation === session.presentation.generation
        && !!session.queue && this.header?.id === session.queue.queueId && this.header.current?.entryId === session.queue.entryId);
    const check = () => {if (!live()) throw new DOMException('Playback change retired', 'AbortError');};
    if (context) check();
    const captured = context ? {...context, isCurrent: live} : undefined;
    // The server cancels a prepared next track when the session changes; so does this device.
    this.invalidateAudioPreparation();
    try {
      if (!this.api.change) throw new PlaybackApiError(410, 'session_ended', 'never');
      const r = await this.api.change(session.id, change, this.playback.getSnapshot().duration, captured);
      if (!live()) {if (context) check();return;}
      if (context && (r.session.id !== session.id || r.session.queue?.queueId !== session.queue?.queueId || r.session.queue?.entryId !== session.queue?.entryId)) throw new Error('Playback session changed during replan');
      if (context?.recoverEnded === false && r.session.state === 'ended') throw new PlaybackApiError(410, 'session_ended', 'never');
      this.session = r.session;
      // The quality the server put in force (the step Automatic is on) is what the next entry starts with.
      if (choices.quality && r.session.presentation.quality) this.qualityOverride = requestInForce(r.session.presentation.quality);
      if (this.header) this.accept(this.header);
      this.playback.installPresentation(r.playback);
      return context ? this.session : undefined;
    } catch (e) {
      if (!live()) {if (context) check();return;}
      if (context?.recoverEnded !== false && e instanceof PlaybackApiError && (e.status === 404 || e.status === 410 || e.code === 'session_ended') && this.header?.current) {
        const p = this.playback.getSnapshot();
        const retained = session.discTitle && choices.versionId === undefined ? {versionId: session.versionId, discTitle: session.discTitle} : {};
        await this.restartCurrent(p.pendingSeek?.positionSeconds ?? (p.phase === 'idle' ? undefined : p.positionSeconds), {...this.playback.playbackChoices(), ...choices, ...retained}, p.intent === 'paused' ? 'paused' : 'playing', true, context);
        requireSessionChangeCurrent(context);
        if (context && (this.disposed || sequence !== this.changeSequence || this.generation !== generation + 1
          || this.header?.id !== session.queue?.queueId || this.header?.current?.entryId !== session.queue?.entryId)) throw new DOMException('Playback change retired', 'AbortError');
        return context && this.session?.id !== session.id ? this.session : undefined;
      }
      throw e;
    }
  }

  /**
   * The lease of the playing session lapsed while this device could not report (a sleeping
   * laptop, a long outage): its entry starts again where it was, playing or paused as it was,
   * instead of ending in an error. False when there is nothing to recover here.
   */
  async recoverLease(sessionId: string, positionSeconds: number): Promise<boolean> {
    const p = this.playback.getSnapshot();
    if (this.disposed || this.session?.id !== sessionId || !this.header?.current || this.nativeOwns() || p.session?.id !== sessionId || p.channel || !['starting', 'ready'].includes(p.phase)) return false;
    const choices = {...this.playback.playbackChoices(), ...(this.session.discTitle ? {versionId: this.session.versionId, discTitle: this.session.discTitle} : {})};
    await this.restartCurrent(p.pendingSeek?.positionSeconds ?? positionSeconds, choices, p.intent === 'paused' ? 'paused' : 'playing', true);
    return this.session?.id !== sessionId && !!this.session;
  }

  async journey(target: ListeningTarget, action: 'play' | 'shuffle' | 'mix' | 'enqueue', resume = false, options: {signal?: AbortSignal; startItem?: string; startSeconds?: number} = {}) {
    this.listening.interact();
    const result = await loadListeningSelection((path, body, method, signal) => this.request(path, body, method, signal), this.scope, target, {mix: action === 'mix', resume, startItem: options.startItem, signal: options.signal});
    if (action === 'enqueue') await this.enqueueMany(result.entries);
    else await this.playEntries(result.entries, {shuffle: action === 'shuffle', startSeconds: options.startSeconds ?? (resume ? result.startSeconds : 0)});
    return result;
  }

  /** Play or Shuffle a container (a show, a season, a collection, an album…) as one queue
   * segment: the server snapshots it, however large, and the first entry plays at once. */
  playSelector(selector: Selector, options: {shuffle?: boolean; shuffleSeed?: string; anchor?: QueueAnchor; startSeconds?: number} = {}): Promise<void> {
    // A music container played (not shuffled) starts with the viewer's music defaults.
    const defaults = !options.shuffle && 'container' in selector && MUSIC_CONTAINERS.includes(selector.container.kind) ? this.musicDefaults?.() : undefined;
    const shuffle = !!options.shuffle || !!defaults?.shuffle;
    return this.play([{source: selector, ...(shuffle ? {order: {mode: 'shuffle', ...(options.shuffleSeed ? {seed: options.shuffleSeed} : {})}} : {}), ...(options.anchor ? {anchor: options.anchor} : {})}], '', {...options, repeat: defaults?.repeat}).then(() => {});
  }

  /** Whether the next track follows the current one on its album, so the audio engine joins them
   * gaplessly instead of crossfading (Plexamp's behavior; spec §18.5, Plan §9.6). */
  sameAlbum(currentItemId: string, nextItemId: string): boolean {
    const at = this.window.findIndex(e => e.itemId === currentItemId && e.entryId === this.header?.current?.entryId);
    const current = at >= 0 ? this.window[at] : this.window.find(e => e.itemId === currentItemId);
    const next = at >= 0 && this.window[at + 1]?.itemId === nextItemId ? this.window[at + 1] : this.window.find(e => e.itemId === nextItemId);
    return consecutiveOnAlbum(current, next);
  }

  /** Shows an existing queue (this device's, after a relaunch) without playing it. */
  async open(queueId: string, signal?: AbortSignal, isCurrent?: () => boolean): Promise<void> {
    const check = () => {if (signal?.aborted || isCurrent && !isCurrent()) throw Object.assign(new Error('Queue open retired'), {name: 'AbortError'});};
    check();
    const header = await this.client.header(queueId, signal); check();
    const window = await this.client.window(queueId, WINDOW_BEFORE, WINDOW_AFTER, signal); check();
    this.accept(header, window);
  }

  /** Restore a parked queue at its saved position without ever starting media playing.
   * The queue stays server-owned: a changed current entry or unavailable bookmark is refused,
   * never replaced by a one-title queue. This is a fresh playback with no client-held choices;
   * the server still owns its queue track-language carry policy. */
  async restorePaused(queueId: string, entryId: string, startSeconds: number): Promise<void> {
    id(queueId, 'queue id'); id(entryId, 'entry id');
    if (!Number.isFinite(startSeconds) || startSeconds < 0 || startSeconds > 1e9) throw new ContractError('restore position');
    if (this.disposed) return;
    this.publish({phase: 'preparing', view: null, error: null, completion: null});
    this.playback.leave();
    const generation = ++this.generation;
    ++this.epoch;
    this.qualityOverride = undefined;
    this.session = undefined;
    const intent = this.playback.beginQueueIntent(undefined, true, 'paused');
    const controller = new AbortController();
    const current = () => !this.disposed && generation === this.generation;
    const invalidate = () => { if (!current()) controller.abort(); };
    const offQueue = this.subscribe(invalidate), offPlayback = this.playback.subscribe(invalidate);
    this.publish({phase: 'preparing', view: null, error: null, completion: null});
    try {
      if (!await this.playback.prepareQueueIntent(intent) || !current()) return;
      const header = await this.client.header(queueId, controller.signal);
      if (!current() || this.playback.getSnapshot().intentId !== intent) return;
      const window = await this.client.window(queueId, WINDOW_BEFORE, WINDOW_AFTER, controller.signal);
      if (!current() || this.playback.getSnapshot().intentId !== intent) return;
      if (header.id !== queueId || header.current?.entryId !== entryId) throw new PlaybackApiError(409, 'queue_entry_changed', 'after_refresh');
      const entry = window.find(e => e.entryId === entryId);
      if (!entry?.available || !entry.itemId) throw new PlaybackApiError(404, 'not_found', 'never');
      this.header = header; this.window = window;
      await this.startEntry(queueId, entryId, intent, startSeconds, generation, undefined, 'paused', controller.signal);
      if (!current()) return;
      const snapshot = this.playback.getSnapshot(), session = this.currentSession();
      if (!session || snapshot.session?.id !== session.id || snapshot.intent !== 'paused' || !['starting', 'ready'].includes(snapshot.phase)) throw new ContractError('paused restore adoption');
      this.publish({phase: 'ready'});
    } catch (e) {
      if (!current()) return;
      const text = message(e, serviceText('queuePlayer.startFailed'));
      this.publish({phase: 'error', error: text});
      this.playback.queueFailed('', text);
      throw e;
    } finally {
      offQueue(); offPlayback(); controller.abort();
    }
  }

  /** Play an exact ordered mixture of existing server selectors and playable item segments.
   * The owner supplies its current document guard; no aggregate is flattened by this player. */
  async playSegments(input: readonly SegmentInput[], options: {startSeconds?: number; signal?: AbortSignal; isCurrent?: () => boolean} = {}): Promise<QueueHeader> {
    if (!input.length || options.startSeconds !== undefined && (!Number.isFinite(options.startSeconds) || options.startSeconds < 0 || options.startSeconds > 1e9)) throw new ContractError('queue playback selection');
    if (options.signal?.aborted || options.isCurrent?.() === false || this.disposed) throw Object.assign(new Error('cancelled'), {name: 'AbortError'});
    const queue = await this.play(input, '', {...options, propagateError: true});
    if (!queue) throw Object.assign(new Error('cancelled'), {name: 'AbortError'});
    return queue;
  }

  /** Play (or Shuffle): a new queue for this device, playing its first entry (or the shuffle's). */
  private playIds(ids: readonly string[], options: {shuffle?: boolean; startSeconds?: number}) {
    return this.play(segments(ids, !!options.shuffle), ids[0]!, options).then(() => {});
  }

  private async play(input: readonly SegmentInput[], first: string, options: {startSeconds?: number; repeat?: 'off' | 'one' | 'all'; signal?: AbortSignal; isCurrent?: () => boolean; propagateError?: boolean; state?: 'playing' | 'paused'}, choices: PlaybackChoices = {}): Promise<QueueHeader | undefined> {
    const state = options.state ?? 'playing';if (state !== 'playing' && state !== 'paused') throw new ContractError('queue start state');
    const generation = ++this.generation;
    ++this.epoch;
    const current = () => !this.disposed && generation === this.generation && !options.signal?.aborted && options.isCurrent?.() !== false;
    const cancel = () => { if (generation === this.generation && !this.disposed) this.playback.leave(); };
    options.signal?.addEventListener('abort', cancel, {once: true});
    // A new play starts from the viewer's saved default again, unless it names a quality itself.
    this.qualityOverride = choices.quality;
    const asked = this.startChoices(choices);
    this.listening.interact();
    // A new queue is a new playback: the player's own choices go back to the Settings.
    const intent = this.playback.beginQueueIntent(first || undefined, true, state);
    this.publish({phase: 'preparing', error: null, completion: null});
    try {
      const retainQueueIds = this.retainQueueIds ? [...this.retainQueueIds()] : undefined;
      if (!await this.playback.prepareQueueIntent(intent) || !current()) return;
      const at = options.startSeconds;
      // One request when the server may choose the start (resume or the beginning); an explicit
      // position, version or audio track starts the session separately, with those.
      const together = at === undefined && !asked.versionId && !asked.audio;
      const created: CreateResult = await this.client.create(input, {retainQueueIds, ...(together ? {startPlayback: {state, ...(asked.quality ? {quality: asked.quality} : {})}} : {}), ...(options.signal ? {signal: options.signal} : {})});
      if (!current()) { this.abandon(created.session); return; }
      this.accept(created.queue, created.window);
      let header = created.queue;
      if (options.repeat && options.repeat !== 'off') {
        if (!current()) { this.abandon(created.session); return; }
        header = (await this.client.update(header.id, header.revision, {repeat: options.repeat})).queue;
        if (!current()) { this.abandon(created.session); return; }
        this.accept(header);
      }
      if (QueueClient.startPending(created)) {
        if (!current()) return;
        const waiting = this.client.awaitCurrent(header.id, {intervalMs: this.pendingPollMs, signal: options.signal});
        this.wakePending = waiting.wake;
        try { header = await waiting.done; } finally { this.wakePending = undefined; }
        if (!current()) return;
        const window = await this.client.window(header.id, WINDOW_BEFORE, WINDOW_AFTER, options.signal);
        if (!current()) return;
        this.accept(header, window);
      }
      if (!current()) { this.abandon(created.session); return; }
      if (created.session && together) await this.adopt(created.session, intent, generation, current, options.signal);
      else if (header.current) await this.startEntry(header.id, header.current.entryId, intent, at, generation, choices, state, options.signal, current);
      if (!current()) return;
      if (options.propagateError && (!this.session || this.session.queue?.queueId !== header.id || this.playback.getSnapshot().phase === 'error')) throw new ContractError('queue playback adoption');
      this.publish({phase: 'ready'});
      return current() ? this.header : undefined;
    } catch (e) {
      if (!current()) { if (options.propagateError) throw e; return; }
      this.fail(first, e, serviceText('queuePlayer.startFailed'));
      if (options.propagateError) throw e;
    } finally { options.signal?.removeEventListener('abort', cancel); }
  }

  /** Starts the session for one entry (a start that wasn't made with the create or advance). */
  private async startEntry(queueId: string, entryId: string, intent: number, startSeconds?: number, generation: number = this.generation, choices?: PlaybackChoices, state: 'playing' | 'paused' = 'playing', signal?: AbortSignal, isCurrent?: () => boolean, subtitles?: StartOptions['subtitles'], automatic = false) {
    const current = () => !this.disposed && generation === this.generation && !signal?.aborted && isCurrent?.() !== false;
    const entry = this.window.find(e => e.entryId === entryId);
    const preparing: {generation: number; intent: number; notice?: StartPreparation} = {generation, intent};
    this.preparingStart = preparing;
    this.publish({});
    let r: Awaited<ReturnType<QueueSessionTransport['start']>>;
    try {r = await this.whileBuilding(() => {
      if (!current()) throw Object.assign(new Error('aborted'), {name: 'AbortError'});
      return this.api.start({queue: {queueId, entryId}}, {state, ...this.startChoices(choices), ...(subtitles ? {subtitles} : {}), ...(startSeconds === undefined ? {startFrom: 'resume'} : {startPositionMs: Math.round(startSeconds * 1000)}), ...(this.session ? {replacesSessionId: this.session.id} : {})}, this.key(), entry?.durationMs !== undefined ? entry.durationMs / 1000 : undefined, signal,
        {isCurrent: current, onPreparing: (_wait, notice) => {
          if (!current() || this.preparingStart !== preparing) return;
          preparing.notice = notice;
          this.publish({phase: 'preparing'});
          if (current() && this.preparingStart === preparing) this.playback.reportStartPreparation(intent, notice);
        }});
    });} finally {
      if (this.preparingStart === preparing) {
        this.preparingStart = undefined;
        this.playback.reportStartPreparation(intent, undefined);
        this.publish({});
      }
    }
    if (!current()) { this.api.forget(r.session.id); this.abandon(r.session); return; }
    if (isCurrent && (r.session.queue?.queueId !== queueId || r.session.queue.entryId !== entryId || !entry?.available || r.session.itemId !== entry.itemId)) {
      this.api.forget(r.session.id); this.abandon(r.session); throw new ContractError('queue playback session');
    }
    if (choices?.discTitle && (r.session.versionId !== choices.versionId || r.session.discTitle?.id !== choices.discTitle.id
      || r.session.discTitle.revision !== choices.discTitle.revision)) {
      this.api.forget(r.session.id); this.abandon(r.session); throw new ContractError('disc title playback selection');
    }
    if (subtitles && (r.session.versionId !== choices?.versionId || r.session.state !== state
      || r.session.presentation.startPositionMs !== Math.round(startSeconds! * 1000)
      || choices?.audioTrackId && r.session.presentation.audioTrackId !== choices.audioTrackId
      || (r.session.presentation.subtitle?.trackId ?? null) !== subtitles.trackId
      || subtitles.trackId && r.session.presentation.subtitle?.delivery !== (subtitles.delivery === 'embeddedClient' ? 'device' : subtitles.delivery))) {
      this.api.forget(r.session.id); this.abandon(r.session); throw new ContractError('retry playback selection');
    }
    const pending = this.playback.getSnapshot();
    if (signal && state === 'paused' && (pending.intentId !== intent || pending.intent !== 'paused' || this.header?.id !== queueId || this.header.current?.entryId !== entryId || r.session.state !== 'paused' || r.session.queue?.queueId !== queueId || r.session.queue.entryId !== entryId || r.session.itemId !== entry?.itemId || r.session.presentation.startPositionMs !== Math.round(startSeconds! * 1000))) {
      this.api.forget(r.session.id); this.abandon(r.session);
      throw new ContractError('paused restore session');
    }
    if(automatic)this.listening.acceptAutomaticTransition(r.session);
    // The presentation's timeline is the whole title: the engine starts at the chosen position.
    await this.adoptPlayback(r.session, startSeconds === undefined ? r.playback : {...r.playback, resumeSeconds: startSeconds}, intent, current);
  }

  /** Adopts a session a queue request started (create with startPlayback, or advance). */
  private async adopt(session: Session, intent: number, generation: number = this.generation, isCurrent?: () => boolean, signal?: AbortSignal) {
    const current = () => !this.disposed && generation === this.generation && !signal?.aborted && isCurrent?.() !== false;
    if (!current()) { this.abandon(session); return; }
    const entry = this.window.find(e => e.entryId === session.queue?.entryId);
    if (isCurrent && (session.queue?.queueId !== this.header?.id || session.queue?.entryId !== this.header?.current?.entryId || !entry?.available || session.itemId !== entry.itemId)) {
      this.abandon(session); throw new ContractError('queue playback session');
    }
    const duration = session.discTitle ? await this.api.duration(session.itemId ?? '', session.versionId, signal, session.discTitle)
      : entry?.durationMs !== undefined ? entry.durationMs / 1000 : await this.api.duration(session.itemId ?? '', session.versionId, signal);
    if (!current()) { this.abandon(session); return; }
    await this.adoptPlayback(session, this.api.adopt(session, duration), intent, current);
  }

  private async adoptPlayback(session: Session, playback: PlaybackSession, intent: number, isCurrent?: () => boolean, retireOnFailure = true) {
    if (isCurrent?.() === false) {if (retireOnFailure) {this.api.forget(session.id); this.abandon(session);} return;}
    const previous = this.session;
    this.session = session;
    this.sessionStartedAt = Date.now();
    // Replaced on the server: late reports from the engine for it are dropped, not sent (410).
    if (previous && previous.id !== session.id) this.api.retire(previous.id);
    this.endedId = undefined;
    this.nativeSession = undefined;
    if (this.header) this.accept(this.header);
    if (isCurrent?.() === false) {if (retireOnFailure) {this.api.forget(session.id); this.abandon(session);} return;}
    const itemKind = this.window.find(e => e.entryId === session.queue?.entryId)?.kind ?? '';
    if (this.native && session.kind === 'audio' && this.nativeFor(itemKind)) { await this.adoptNative(session, playback, intent); return; }
    if (!await this.playback.adoptQueueSession(session.itemId ?? '', playback, intent)) {
      // The viewer did something else while this session was being made. Unless that already put
      // another session here, this one has no player: it ends now instead of holding a stream.
      if (retireOnFailure && this.session?.id === session.id && this.playback.getSnapshot().session?.id !== session.id) { this.api.forget(session.id); this.abandon(session); }
    }
  }

  // ── Native listening (plan §9.3 C3) ─────────────────────────────────

  /** Hands an audio session to the native owner, with the timeline `seq` reached so far. */
  private async adoptNative(session: Session, playback: PlaybackSession, intent: number) {
    const native = this.native!, epoch = ++this.nativeEpoch, header = this.header;
    const durationSeconds = playback.duration;
    // INT T4: another playback began while this session's request was in flight. Nobody will own
    // it (the handover would be refused), so it ends now rather than lapsing with its lease.
    if (this.playback.getSnapshot().intentId !== intent) {
      await this.api.stopPlayback(session.id).catch(() => {});
      return;
    }
    // From here on the owner reports this session; JS never does. It claims with the epoch JS held.
    const observerEpoch = this.api.handOverObserver(session.id);
    this.api.forget(session.id);
    this.nativeSession = session.id;
    try {
      await this.playback.adoptNativeQueueSession(session.itemId ?? '', intent, native, () => native.adopt({
        session, playbackRun:this.playback.getSnapshot().playbackRun, queueId: header?.id ?? null, queueRevision: header?.revision ?? null, durationSeconds, lastSeq: 0, ...(observerEpoch ? {observerEpoch} : {}),
        leaseMs: Math.max(0, 120_000 - (Date.now() - this.sessionStartedAt)), transitions: this.transitions,
        intent: session.state === 'paused' ? 'paused' : 'playing',
      }, event => this.nativeEvent(epoch, event)));
    } catch {
      // The native owner refused the handover: the session isn't left without an owner (its lease
      // would run on with nobody reporting). JS plays it, as without a native owner.
      if (this.disposed || epoch !== this.nativeEpoch) return;
      this.nativeSession = undefined;
      ++this.nativeEpoch;
      await this.playback.adoptQueueSession(session.itemId ?? '', this.api.adopt(session, durationSeconds), this.playback.beginQueueIntent(session.itemId));
    }
  }

  /** Whether the native owner has the current session (queue moves then go through it). */
  private nativeOwns(): boolean {
    return !!this.native && !!this.nativeSession && this.nativeSession === this.session?.id && this.playback.hasNativeOwner();
  }

  /** A native snapshot: the owner may have moved the queue on (a completion, Next from the lock
   * screen, a committed gapless edge) while JS slept; its session becomes this player's. */
  private nativeEvent(epoch: number, event: NativeListeningV1Event) {
    if (this.disposed || epoch !== this.nativeEpoch) return;
    const session = event.session, previous = this.session;
    if (previous?.id !== session.id) {
      if (previous) this.api.forget(previous.id);
      this.endedId = undefined;
      this.nativeSession = session.id;
    }
    this.session = session;
    if (event.transition?.queue && event.transition.queue.id === this.header?.id) this.header = event.transition.queue;
    const moved = previous?.id !== session.id || (!!event.queue && !!this.header && event.queue.id === this.header.id && event.queue.revision !== this.header.revision);
    if (event.queue && this.header && event.queue.id === this.header.id && event.queue.revision !== this.header.revision) this.header = Object.freeze({...this.header, revision: event.queue.revision});
    this.playback.applyNativeQueueEvent(event, toPlaybackSession(session, event.durationSeconds));
    if (moved && this.header) {
      this.accept(this.header);
      if (!this.refreshTimer) this.refreshTimer = setTimeout(() => { this.refreshTimer = undefined; void this.refresh().catch(() => {}); }, 100);
    }
  }


  private fail(itemId: string, e: unknown, fallback: string) {
    const text = message(e, fallback);
    this.publish({phase: 'ready', error: text});
    this.playback.queueFailed(itemId, text);
  }

  /** Queue moves run one after another: two quick presses of Next move on twice, each from the
   * queue the one before it left, instead of racing with the same revision. */
  private advance(reason: 'completion' | 'next' | 'previous' | 'entry', entryId?: string): Promise<void> {
    const epoch = this.epoch;
    const run = () => (epoch === this.epoch ? this.advanceNow(reason, entryId) : Promise.resolve());
    const move = this.moves.then(run, run);
    this.moves = move.catch(() => {});
    return move;
  }

  /** `:advance` with the current revision; the next session starts in the same request. */
  private async advanceNow(reason: 'completion' | 'next' | 'previous' | 'entry', entryId?: string) {
    const header = this.header;
    if (!header) throw new Error('Load the queue first.');
    if (this.disposed) return;
    const releaseTimerEdge=this.listening.beginAutomaticTransition();
    if(reason!=='completion')this.listening.cancelBoundaryTimer();
    const generation = ++this.generation;
    const current = this.window.find(e => e.entryId === entryId);
    const intent = this.playback.beginQueueIntent(current?.itemId);
    try {
      const r = await this.whileBuilding(() => this.client.advance(header.id, header.revision, reason, entryId));
      // The queue moved on the server whether or not the viewer has since done something else.
      if (this.header?.id === r.queue.id) this.header = r.queue;
      if (generation !== this.generation) { this.abandon(r.session); return; }
      this.window = await this.client.window(r.queue.id, WINDOW_BEFORE, WINDOW_AFTER);
      if (generation !== this.generation) { this.abandon(r.session); return; }
      this.accept(r.queue);
      if (r.session) {if(reason==='completion')this.listening.acceptAutomaticTransition(r.session);await this.adopt(r.session, intent, generation);}
      else if (r.queue.current) await this.startEntry(r.queue.id, r.queue.current.entryId, intent, undefined, generation,undefined,'playing',undefined,undefined,undefined,reason==='completion');
    } catch (e) {
      if (generation !== this.generation) return;
      if (e instanceof PlaybackApiError && e.status === 412) { await this.refresh().catch(() => {}); }
      this.fail(current?.itemId ?? '', e, serviceText('queuePlayer.moveFailed'));
      throw e;
    } finally {releaseTimerEdge?.();}
  }

  /** A retained row chooses only its actual loaded VOD occurrence. The caller's
   * route lifetime must survive this operation's own accepted output replacement;
   * the core, not that predicate, binds the old output until the handoff. */
  async selectQueuedEntry(expected: V1PlaybackTarget, request: V1QueuedEntrySelection, context: SessionChangeContext): Promise<void> {
    requireSessionChangeCurrent(context);
    const target = Object.freeze({...expected});
    const lifetime: SessionChangeContext = Object.freeze({signal: context.signal, isCurrent: context.isCurrent});
    const command = Object.freeze({queueId: id(request.queueId, 'queue selection'), revision: request.expectedRevision,
      body: Object.freeze({reason: 'entry' as const, entryId: id(request.entryId, 'queue selection entry')})});
    const openingGeneration = this.generation, openingEpoch = this.epoch, changes = this.changeSequence;
    const openingPlayback = this.playback.getSnapshot(), opening = this.header;
    const retired = () => new DOMException('Queue selection retired', 'AbortError');
    const ownsOpeningOutput = () => {
      const p = this.playback.getSnapshot(), session = this.session;
      return p.playbackRun === target.playbackRun && p.itemId === target.itemId && p.intentId === openingPlayback.intentId
        && p.session?.id === target.sessionId && p.session.generation === target.generation
        && session?.id === target.sessionId && session.presentation.generation === target.generation
        && session.kind === 'vod' && session.role === 'local' && !p.nativeListening && !p.channel
        && session.itemId === target.itemId && session.versionId === target.versionId
        && session.queue?.queueId === target.queueId && session.queue.entryId === target.entryId;
    };
    const openingCurrent = () => !this.disposed && openingGeneration === this.generation && openingEpoch === this.epoch
      && changes === this.changeSequence && sessionChangeCurrent(lifetime) && ownsOpeningOutput()
      && ['starting', 'ready'].includes(this.playback.getSnapshot().phase)
      && this.header?.id === command.queueId && command.queueId === target.queueId
      && this.header.revision === command.revision && this.header.current?.entryId === target.entryId
      && this.header.current.position === opening?.current?.position && !this.nativeOwns() && !this.playback.hasNativeOwner();
    if (!openingCurrent()) throw retired();
    if (!opening || this.session?.kind !== 'vod' || this.session.role !== 'local' || openingPlayback.nativeListening
      || !['starting', 'ready'].includes(openingPlayback.phase) || command.body.entryId === target.entryId) throw new ContractError('queue selection output');
    const selected = queueEditWindow(opening, this.window, target).get(command.body.entryId);
    if (!selected || queueEntryFacts(selected).available !== true || !selected.itemId || !queueVideoKind(selected.kind)) throw new ContractError('queue selection entry');
    const selectedCurrent = (acceptedRevision?: string) => {
      const matches = this.window.filter(entry => entry.entryId === command.body.entryId);
      const scope = queueWindowScope(this.window);
      return scope?.queueId === command.queueId && (scope.revision === command.revision || acceptedRevision !== undefined && scope.revision === acceptedRevision)
        && matches.length === 1 && queueEntryFacts(matches[0]!).available === true && matches[0]!.itemId === selected.itemId
        && matches[0]!.position === selected.position && queueVideoKind(matches[0]!.kind);
    };
    const run = async () => {
      if (!selectedCurrent() || !openingCurrent() || this.playback.getSnapshot().intent !== openingPlayback.intent) throw retired();
      const generation = ++this.generation; ++this.epoch; ++this.policySequence; ++this.refreshSequence;
      const current = () => !this.disposed && generation === this.generation && changes === this.changeSequence
        && sessionChangeCurrent(lifetime) && !this.nativeOwns() && !this.playback.hasNativeOwner();
      let acceptedRevision: string | undefined, admitted: Session | undefined, installedIntent: number | undefined;
      let registration: QueueSelectionRegistration | undefined;
      const headerCurrent = () => this.header?.id === command.queueId
        && (this.header.revision === command.revision && this.header.current?.entryId === target.entryId && this.header.current.position === opening.current?.position
          || acceptedRevision !== undefined && this.header.revision === acceptedRevision && this.header.current?.entryId === command.body.entryId && this.header.current.position === selected.position);
      let handedOff = false, replacing = false;
      const oldCurrent = () => current() && ownsOpeningOutput() && headerCurrent() && selectedCurrent(acceptedRevision)
        && this.playback.getSnapshot().intent === (replacing ? 'playing' : openingPlayback.intent);
      const checkOld = () => {if (!oldCurrent()) throw retired();};
      try {
        this.listening.interact();
        checkOld();
        // These opening values are frozen before this command enters the serial lane.
        const result = await this.client.advance(command.queueId, command.revision, command.body.reason, command.body.entryId, lifetime.signal);
        if (result.session && result.session.id !== target.sessionId && result.session.role === 'local'
          && result.session.queue?.queueId === command.queueId && result.session.queue.entryId === command.body.entryId) admitted = result.session;
        if (!admitted || admitted.kind !== 'vod' || admitted.role !== 'local' || admitted.state !== 'playing'
          || admitted.queue?.queueId !== command.queueId || admitted.queue.entryId !== command.body.entryId
          || admitted.itemId !== selected.itemId || !admitted.versionId) throw new ContractError('queue selection session');
        registration = this.api.selectionRegistration?.(admitted.id);
        const session = admitted, versionId = admitted.versionId;
        acceptedRevision = result.queue.revision;
        const accepted = (header: QueueHeader) => header.id === command.queueId && header.revision === acceptedRevision
          && header.current?.entryId === command.body.entryId && header.current.position === selected.position && header.total === opening.total;
        checkOld();
        if (acceptedRevision === command.revision || !accepted(result.queue)) throw new ContractError('queue selection result');
        const window = await this.client.window(command.queueId, WINDOW_BEFORE, WINDOW_AFTER, lifetime.signal); checkOld();
        const header = await this.client.header(command.queueId, lifetime.signal); checkOld();
        if (!accepted(header)) throw retired();
        const nextTarget = Object.freeze({...target, sessionId: session.id, generation: session.presentation.generation,
          itemId: selected.itemId, versionId, entryId: command.body.entryId});
        queueEditWindow(header, window, nextTarget);
        const duration = await this.api.duration(selected.itemId, versionId, lifetime.signal, session.discTitle); checkOld();
        if (!Number.isFinite(duration) || duration < 0) throw new ContractError('queue selection duration');
        const acceptedSession = () => {
          const active = this.session;
          return active?.id === session.id && active.presentation.generation === session.presentation.generation
            && active.kind === 'vod' && active.role === 'local' && active.itemId === selected.itemId && active.versionId === session.versionId
            && active.queue?.queueId === command.queueId && active.queue.entryId === command.body.entryId;
        };
        const nextOutput = () => {
          const p = this.playback.getSnapshot();
          return current() && !p.nativeListening && !p.channel && p.playbackRun === target.playbackRun && p.session?.id === session.id
            && p.session.generation === session.presentation.generation && p.itemId === selected.itemId && acceptedSession()
            && this.header?.id === command.queueId && this.header.revision === acceptedRevision && this.header.current?.entryId === command.body.entryId;
        };
        // During synchronous installation only this admitted session may replace
        // the old metadata. After installation, only the new exact output owns it.
        const installing = () => {
          const p = this.playback.getSnapshot();
          return current() && headerCurrent() && (ownsOpeningOutput()
            || acceptedSession() && p.session?.id === target.sessionId && p.session.generation === target.generation
              && p.itemId === target.itemId && p.intentId === openingPlayback.intentId && p.intent === 'playing' && p.playbackRun === target.playbackRun
            || nextOutput());
        };
        const playback = this.api.adopt(session, duration);
        registration = this.api.selectionRegistration?.(session.id, playback); checkOld();
        this.invalidateAudioPreparation(); checkOld();
        replacing = true;
        const intent = this.playback.beginQueueIntent(selected.itemId); checkOld();
        this.header = header; this.window = window; ++this.refreshSequence;
        const adoption = this.adoptPlayback(session, playback, intent, installing, false);
        // adoptPlayback installs synchronously before its persistence await. Once
        // installed, PlaybackService owns cleanup; a late callback cannot stop its successor.
        if (nextOutput()) {installedIntent = this.playback.getSnapshot().intentId; handedOff = true; ++this.epoch;}
        await adoption;
        if (!handedOff || !nextOutput() || this.playback.getSnapshot().intentId !== installedIntent) throw retired();
        this.publish({phase: 'ready'});
      } catch (error) {
        if (admitted && !handedOff) {
          const unowned = () => {
            const live = this.playback.getSnapshot();
            // Any accepted same occurrence is protected, even with the same
            // presentation generation under a different run or intent.
            return !this.disposed && generation === this.generation && changes === this.changeSequence
              && live.playbackRun === target.playbackRun && live.intentId === openingPlayback.intentId
              && live.session?.id === target.sessionId && live.session.generation === target.generation
              && live.session.id !== admitted!.id
              && (this.session?.id !== admitted!.id || this.session === admitted);
          };
          if (await retireQueuedSelectionRegistration(admitted, registration, this.retireQueuedSelection, unowned)
            && unowned() && this.session === admitted) this.session = undefined;
        }
        if (oldCurrent()) this.publish({phase: 'ready', error: message(error, serviceText('queuePlayer.moveFailed'))});
        throw error;
      }
    };
    const pending = this.moves.then(run, run);
    this.moves = pending.catch(() => {});
    return pending;
  }

  async playEntry(queueId: string, entryId: string, expectedRevision: string): Promise<void> {
    this.listening.interact();
    if (queueId !== this.header?.id || expectedRevision !== this.header.revision) throw new Error('Refresh the queue before selecting an item.');
    this.listening.cancelBoundaryTimer();
    if (this.nativeOwns()) { await this.native!.advance('entry', entryId); return; }
    await this.advance('entry', entryId);
  }

  async next(queueId: string, expectedRevision: string): Promise<void> {
    this.listening.interact();
    if (queueId !== this.header?.id || expectedRevision !== this.header.revision) throw new Error('Refresh the queue before advancing.');
    this.listening.cancelBoundaryTimer();
    if (this.nativeOwns()) { await this.native!.advance('next'); return; }
    await this.advance('next');
  }

  /** The credits' countdown ran out: the server hears a completion, so it counts toward the
   * passout check like an advance at the end would. */
  async advanceFromCredits(queueId: string, expectedRevision: string): Promise<void> {
    if (queueId !== this.header?.id || expectedRevision !== this.header.revision) throw new Error('Refresh the queue before advancing.');
    this.listening.cancelBoundaryTimer();
    // A native listening owner plays music, which has no credits; it only knows a viewer's move.
    if (this.nativeOwns()) { await this.native!.advance('next'); return; }
    await this.advance('completion');
  }

  /** Previous restarts the item after three seconds, else moves back. */
  async previous(): Promise<void> {
    this.listening.interact();
    if (this.playback.getSnapshot().positionSeconds > 3) { this.playback.seek(0); return; }
    if (!this.header) throw new Error('Load the queue first.');
    if (this.nativeOwns()) { await this.native!.advance('previous'); return; }
    await this.advance('previous');
  }

  // ── Editing ─────────────────────────────────────────────────────────

  async enqueue(item: QueueItemInput, next = false): Promise<void> {
    await this.enqueueMany([item], next);
  }

  async enqueueMany(entries: readonly QueueItemInput[], next = false): Promise<void> {
    if (!entries.length) throw new Error('No available items were selected.');
    const ids = entries.map(e => e.itemId);
    if (!this.header) {
      const retainQueueIds = this.retainQueueIds ? [...this.retainQueueIds()] : undefined;
      const created = await this.client.create(segments(ids, false), {retainQueueIds});
      this.accept(created.queue, created.window);
      return;
    }
    const starts:number[]=[];for(let i=0;i<ids.length;i+=SEGMENT_ITEMS)starts.push(i);if(next)starts.reverse();
    for (const i of starts) {
      const r = await this.client.addSegment(this.header!.id, this.header!.revision, next ? 'next' : 'end', {source: {items: {ids: ids.slice(i, i + SEGMENT_ITEMS)}}});
      this.header = r.queue;
    }
    await this.refresh();
  }

  /** NEW-37: save the queue as a playlist; a long queue's copy is followed by replaying the key. */
  saveAsPlaylist(queueId: string, name: string, onProgress?: (save: QueuePlaylistSave) => void, signal?: AbortSignal): Promise<QueuePlaylistSave> {
    return saveQueueAsPlaylist(this.client, queueId, name, {onProgress, signal});
  }

  /** Policy edits from retained player controls use their opening output and queue revision. */
  async setQueuePolicy(expected: V1PlaybackTarget, expectedRevision: string, intent: V1QueuePolicyIntent, context: SessionChangeContext): Promise<void> {
    requireSessionChangeCurrent(context);
    if (intent.action === 'repeat' ? !['off', 'one', 'all'].includes(intent.repeat)
      : intent.action !== 'shuffle' || typeof intent.shuffled !== 'boolean') throw new ContractError('queue policy');
    const generation = this.generation, sequence = ++this.policySequence;
    let acceptedRevision: string | undefined;
    const current = () => {
      const p = this.playback.getSnapshot(), session = this.session, header = this.header;
      return !this.disposed && generation === this.generation && sequence === this.policySequence && sessionChangeCurrent(context)
        && p.playbackRun === expected.playbackRun && p.itemId === expected.itemId
        && p.session?.id === expected.sessionId && p.session.generation === expected.generation
        && session?.id === expected.sessionId && session.presentation.generation === expected.generation
        && session.versionId === expected.versionId && session.itemId === expected.itemId
        && session.queue?.queueId === expected.queueId && session.queue.entryId === expected.entryId
        && header?.id === expected.queueId && header.current?.entryId === expected.entryId
        && (header.revision === expectedRevision || acceptedRevision !== undefined && header.revision === acceptedRevision);
    };
    const check = () => {if (!current()) throw new DOMException('Queue policy retired', 'AbortError');};
    check(); ++this.refreshSequence;
    const change = intent.action === 'repeat' ? {repeat: intent.repeat} : {shuffle: {on: intent.shuffled}};
    const result = await this.client.update(expected.queueId, expectedRevision, change, context.signal);
    acceptedRevision = result.queue.revision; check();
    if (result.session || result.queue.id !== expected.queueId || result.queue.current?.entryId !== expected.entryId
      || (intent.action === 'repeat' ? result.queue.repeat !== intent.repeat : !!result.queue.shuffle !== intent.shuffled)) throw new ContractError('queue policy result');
    // Reordering changes the window. Read it under the accepted revision, then prove that revision still holds.
    const window = await this.client.window(expected.queueId, WINDOW_BEFORE, WINDOW_AFTER, context.signal); check();
    const header = await this.client.header(expected.queueId, context.signal); check();
    if (header.revision !== acceptedRevision || header.current?.entryId !== expected.entryId) throw new DOMException('Queue policy changed', 'AbortError');
    this.invalidateAudioPreparation();
    check(); ++this.refreshSequence; this.accept(header, window);
  }

  /** A private, bounded positional reader for one original playback and queue opening. */
  captureEditWindow(expected: V1PlaybackTarget, expectedRevision: string, context: V1QueueEditWindowContext): V1QueueEditReader {
    if (!context || !context.signal || typeof context.isCurrent !== 'function' || typeof context.readOutput !== 'function') throw new ContractError('queue edit output witness');
    const target = Object.freeze({...expected}), lifetime = Object.freeze({signal: context.signal, isCurrent: context.isCurrent}), readOutput = context.readOutput;
    const client = this.client, opening = this.header, initialPlayback = this.playback.getSnapshot();
    const readerLife = new AbortController(), signal = lifetime.signal ? AbortSignal.any([lifetime.signal, readerLife.signal]) : readerLife.signal;
    const generation = this.generation, epoch = this.epoch, changes = this.changeSequence;
    if (!opening?.current || !queueHasObservedCurrent(opening) || opening.id !== target.queueId || opening.revision !== expectedRevision
      || opening.current.entryId !== target.entryId || !Number.isSafeInteger(opening.current.position)
      || opening.current.position < 0 || opening.current.position >= opening.total) throw new ContractError('queue edit opening');
    const anchor = Object.freeze({...opening.current});
    let revision = expectedRevision, total = opening.total, sequence = this.policySequence, live = true, inFlight = false, started = false;
    let acceptedRevision: string | undefined, acceptedTotal: number | undefined;
    const owner = Object.freeze({});
    const issued = new WeakMap<V1QueueEditPage, Readonly<{owner: object; target: V1PlaybackTarget; anchor: typeof anchor; revision: string; start: number; total: number; rows: readonly QueueEntry[]}>>(), resident: V1QueueEditPage[] = [];
    let navigation: V1QueueEditPage | undefined; const history: number[] = [];
    const invalidate = () => {resident.length = 0; navigation = undefined; history.length = 0;};
    const retire = () => {live = false; invalidate(); readerLife.abort();};
    const retired = () => new DOMException('Queue edit reader retired', 'AbortError');
    const stable = () => {
      const p = this.playback.getSnapshot(), session = this.session, header = this.header;
      return live && !this.disposed && generation === this.generation && epoch === this.epoch && changes === this.changeSequence
        && sequence === this.policySequence && !lifetime.signal?.aborted && !this.nativeOwns() && !this.playback.hasNativeOwner()
        && ['starting', 'ready'].includes(p.phase) && !p.channel && !p.nativeListening && p.intentId === initialPlayback.intentId
        && p.playbackRun === target.playbackRun && p.itemId === target.itemId && p.session?.id === target.sessionId && p.session.generation === target.generation
        && session?.role === 'local' && session.id === target.sessionId && session.presentation.generation === target.generation
        && session.itemId === target.itemId && session.versionId === target.versionId && session.queue?.queueId === target.queueId && session.queue.entryId === target.entryId
        && header?.id === target.queueId && header.current?.entryId === anchor.entryId && header.current.position === anchor.position
        && (header.revision === revision && header.total === total || acceptedRevision !== undefined && header.revision === acceptedRevision && header.total === acceptedTotal);
    };
    const check = () => {
      try {
        if (!stable() || lifetime.signal.aborted || lifetime.isCurrent() !== true || !stable()) throw retired();
        const output = readOutput();
        if (!output || output.kind !== 'local' || !output.target || output.target.sessionId !== target.sessionId
          || output.target.generation !== target.generation || output.target.playbackRun !== target.playbackRun
          || output.target.itemId !== target.itemId || output.target.versionId !== target.versionId
          || output.target.queueId !== target.queueId || output.target.entryId !== target.entryId || !stable()
          || lifetime.signal.aborted || lifetime.isCurrent() !== true || !stable()) throw retired();
      } catch {retire(); throw retired();}
    };
    const headerMatches = (header: QueueHeader, rev: string, count: number) => queueHasObservedCurrent(header) && header.id === target.queueId && header.revision === rev
      && header.total === count && header.current?.entryId === anchor.entryId && header.current.position === anchor.position;
    const validate = (page: Awaited<ReturnType<QueueClient['entries']>>, from: number, rev: string, count: number, canPrevious: boolean): V1QueueEditPage => {
      if (page.revision !== rev || page.total !== count || page.start !== from || !Number.isSafeInteger(from) || from < 0 || from > count
        || page.items.length > 50 || from + page.items.length > count || new Set(page.items.map(row => row.entryId)).size !== page.items.length
        || page.items.some((row, i) => queueEntryFacts(row).position !== from + i || queueEntryFacts(row).available === undefined)) throw new ContractError('queue edit page');
      const end = from + page.items.length;
      if (end < count ? page.nextCursor !== `from:${end}` || end <= from : page.nextCursor !== undefined) throw new ContractError('queue edit cursor');
      return Object.freeze({start: from, total: count, revision: rev, items: page.items, canPrevious, ...(page.nextCursor ? {nextCursor: page.nextCursor} : {})});
    };
    const own = (page: V1QueueEditPage) => {const receipt = issued.get(page); if (!receipt || receipt.owner !== owner || receipt.target !== target || receipt.anchor !== anchor || receipt.revision !== revision || receipt.start !== page.start || receipt.total !== page.total || receipt.rows !== page.items || !resident.includes(page)) throw new ContractError('queue edit receipt');};
    const issue = (page: V1QueueEditPage) => {issued.set(page, Object.freeze({owner, target, anchor, revision: page.revision, start: page.start, total: page.total, rows: page.items})); resident.push(page); if (resident.length > 2) resident.shift(); return page;};
    const exclusive = async <T,>(work: () => Promise<T>): Promise<T> => {
      try {check(); if (inFlight) throw new ContractError('queue edit reader busy'); inFlight = true;
        try {return await work();} catch (error) {retire(); throw error;} finally {inFlight = false;}
      } catch (error) {retire(); throw error;}
    };
    const load = async (from: number, direction: 'first' | 'next' | 'previous') => {
      check(); const page = await client.entries(target.queueId, from, 50, signal); check();
      const nextHistory = direction === 'first' ? [from] : direction === 'next' ? [...history, from].slice(-2) : history.slice(0, -1);
      const parsed = validate(page, from, revision, total, nextHistory.length > 1);
      const header = await client.header(target.queueId, signal); check();
      if (!headerMatches(header, revision, total)) throw retired();
      check(); history.splice(0, history.length, ...nextHistory); navigation = issue(parsed); return navigation;
    };
    check();
    return Object.freeze({
      read: () => exclusive(async () => {if (started) throw new ContractError('queue edit already read'); started = true; return load(anchor.position, 'first');}),
      next: page => exclusive(async () => {own(page); if (navigation !== page) throw new ContractError('queue edit navigation'); const cursor = page.nextCursor; if (!cursor) throw new ContractError('queue edit end');
        const from = page.start + page.items.length; if (cursor !== `from:${from}` || !Number.isSafeInteger(from) || from <= page.start || from >= total) throw new ContractError('queue edit cursor'); return load(from, 'next');}),
      previous: page => exclusive(async () => {own(page); if (navigation !== page || !page.canPrevious || history.length !== 2) throw new ContractError('queue edit navigation'); return load(history[0]!, 'previous');}),
      edit: (page, intent) => exclusive(async () => {
        own(page); const plan = queueEditPlan(page.items, anchor.position, intent, true); check(); own(page);
        const beforeHeader = await client.header(target.queueId, signal); check(); own(page);
        if (!headerMatches(beforeHeader, revision, total)) throw retired();
        invalidate(); sequence = ++this.policySequence; ++this.refreshSequence; check();
        const result = plan.edit.action === 'remove'
          ? await client.remove(target.queueId, revision, plan.edit.entryId, signal, check)
          : await client.move(target.queueId, revision, plan.moved!.entryId, plan.moved!.after ? {after: plan.moved!.after} : {before: plan.moved!.before!}, signal, check);
        acceptedRevision = result.queue.revision; const nextTotal = total - (plan.edit.action === 'remove' ? 1 : 0); acceptedTotal = nextTotal; check();
        if (result.session || acceptedRevision === revision || !headerMatches(result.queue, acceptedRevision, nextTotal)) throw new ContractError('queue edit result');
        const raw = await client.entries(target.queueId, page.start, 50, signal); check();
        const nextPage = validate(raw, page.start, acceptedRevision, nextTotal, false);
        const header = await client.header(target.queueId, signal); check();
        if (!headerMatches(header, acceptedRevision, nextTotal)) throw retired();
        verifyQueueEdit(plan, nextPage.items, true);
        const window = await client.window(target.queueId, WINDOW_BEFORE, WINDOW_AFTER, signal); check();
        const finalHeader = await client.header(target.queueId, signal); check();
        if (!headerMatches(finalHeader, acceptedRevision, nextTotal)) throw retired();
        queueEditWindow(finalHeader, window, target);
        check(); this.invalidateAudioPreparation(); check(); ++this.refreshSequence; this.accept(finalHeader, window);
        revision = acceptedRevision; total = nextTotal; acceptedRevision = undefined; acceptedTotal = undefined; check(); history.push(nextPage.start); navigation = issue(nextPage); return navigation;
      }),
      dispose: retire,
    });
  }

  /** Retained Up Next controls edit only their drawn, loaded upcoming rows.
   * Reorder supplies one contiguous block in its new order, with one real move.
   * The command and readback keep the opening output, revision and caller lifetime. */
  async editQueue(expected: V1PlaybackTarget, expectedRevision: string, intent: V1QueueEditIntent, context: SessionChangeContext): Promise<void> {
    requireSessionChangeCurrent(context);
    const target = Object.freeze({...expected});
    const lifetime: SessionChangeContext = Object.freeze({signal: context.signal, isCurrent: context.isCurrent});
    let edit: V1QueueEditIntent;
    if (intent?.action === 'remove') edit = Object.freeze({action: 'remove', entryId: id(intent.entryId, 'queue edit entry')});
    else if (intent?.action === 'reorder' && Array.isArray(intent.entryIds) && intent.entryIds.length >= 2 && intent.entryIds.length <= QUEUE_WINDOW_MAX) {
      const entryIds = Object.freeze(intent.entryIds.map(value => id(value, 'queue edit entry')));
      if (new Set(entryIds).size !== entryIds.length) throw new ContractError('queue edit membership');
      edit = Object.freeze({action: 'reorder', entryIds});
    } else throw new ContractError('queue edit');
    const generation = this.generation, opening = this.header, drawn = this.window;
    let sequence = this.policySequence, acceptedRevision: string | undefined;
    const current = () => {
      const p = this.playback.getSnapshot(), session = this.session, header = this.header;
      return !this.disposed && generation === this.generation && sequence === this.policySequence && sessionChangeCurrent(lifetime)
        && p.playbackRun === target.playbackRun && p.itemId === target.itemId
        && p.session?.id === target.sessionId && p.session.generation === target.generation
        && session?.id === target.sessionId && session.presentation.generation === target.generation
        && session.versionId === target.versionId && session.itemId === target.itemId
        && session.queue?.queueId === target.queueId && session.queue.entryId === target.entryId
        && header?.id === target.queueId && header.current?.entryId === target.entryId
        && header.current.position === opening?.current?.position
        && (header.revision === expectedRevision || acceptedRevision !== undefined && header.revision === acceptedRevision);
    };
    const check = () => {if (!current()) throw new DOMException('Queue edit retired', 'AbortError');};
    check();
    if (!opening?.current || opening.revision !== expectedRevision) throw new ContractError('queue edit revision');
    queueEditWindow(opening, drawn, target);
    const before = opening.current.position - drawn[0]!.position, after = drawn[drawn.length - 1]!.position - opening.current.position;
    if (before < 0 || after < 0 || before + after + 1 > QUEUE_WINDOW_MAX) throw new ContractError('queue edit span');
    const plan = queueEditPlan(drawn, opening.current.position, edit);
    const {moved} = plan;
    check(); sequence = ++this.policySequence; ++this.refreshSequence;
    const result = edit.action === 'remove'
      ? await this.client.remove(target.queueId, expectedRevision, edit.entryId, lifetime.signal)
      : await this.client.move(target.queueId, expectedRevision, moved!.entryId, moved!.after ? {after: moved!.after} : {before: moved!.before!}, lifetime.signal);
    acceptedRevision = result.queue.revision; check();
    const total = opening.total - (edit.action === 'remove' ? 1 : 0);
    const accepted = (header: QueueHeader) => header.id === target.queueId && header.revision === acceptedRevision
      && header.current?.entryId === target.entryId && header.current.position === opening.current!.position && header.total === total;
    if (result.session || acceptedRevision === expectedRevision || !accepted(result.queue)) throw new ContractError('queue edit result');
    // Read exactly this loaded span, then prove the window belonged to the accepted revision.
    const window = await this.client.window(target.queueId, before, after, lifetime.signal); check();
    const header = await this.client.header(target.queueId, lifetime.signal); check();
    if (!accepted(header)) throw new DOMException('Queue edit changed', 'AbortError');
    queueEditWindow(header, window, target);
    verifyQueueEdit(plan, window);
    check(); this.invalidateAudioPreparation();
    check(); ++this.refreshSequence; this.accept(header, window);
  }

  /** The workspace's edits (Up Next): remove, reorder (one moved entry), repeat, shuffle, add. */
  private async mutate(intent: QueueIntent): Promise<void> {
    const h = this.header;
    if (!h) throw new Error('Load the queue first.');
    this.publishQueue({phase: 'sending', error: null});
    try {
      switch (intent.action) {
        case 'remove': this.header = (await this.client.remove(h.id, h.revision, intent.entryId)).queue; break;
        case 'repeat': this.header = (await this.client.update(h.id, h.revision, {repeat: intent.repeat})).queue; break;
        case 'shuffle': this.header = (await this.client.update(h.id, h.revision, {shuffle: {on: intent.shuffled}})).queue; break;
        case 'add': case 'play-next': await this.enqueueMany([intent.entry], intent.action === 'play-next'); return;
        case 'append': case 'insert-next': await this.enqueueMany(intent.entries, intent.action === 'insert-next'); return;
        case 'reorder': {
          const before = this.window.map(e => e.entryId).filter(id => intent.entryIds.includes(id));
          const moved = movedEntry(before, intent.entryIds);
          if (!moved) break;
          const to = moved.after ? {after: moved.after} : {before: moved.before!};
          this.header = (await this.client.move(h.id, h.revision, moved.entryId, to)).queue;
          break;
        }
        default: throw new Error('This change isn’t available for this queue.');
      }
      await this.refresh();
    } catch (e) {
      if (e instanceof PlaybackApiError && e.status === 412) await this.refresh().catch(() => {});
      this.publishQueue({phase: 'ready', error: message(e, serviceText('queuePlayer.changeFailed'))});
      throw e;
    }
  }

  // ── Completion ──────────────────────────────────────────────────────

  private observed() {
    if (this.nativeOwns()) return; // the native owner reports the end and advances
    if (this.maybePrepareAudio()) return;
    const p = this.playback.getSnapshot();
    const session = this.session;
    if (this.disposed || !session || p.channel || p.phase !== 'ended' || p.session?.id !== session.id) {this.clearCompletionRetry();return;}
    if (this.endedId === session.id) return;
    this.endedId = session.id;
    const intent = p.intentId, epoch = this.epoch;
    const current = () => {const held=this.playback.getSnapshot();return !this.disposed&&epoch===this.epoch&&this.session?.id===session.id&&held.intentId===intent&&held.phase==='ended'&&held.session?.id===session.id;};
    void (async () => {
      try {
        try {await this.playback.confirmQueueEnd(intent);}
        catch(error) {
          const problem=error as {status?:number;retry?:string;retryable?:boolean;retryAfterMs?:number;retryAfterSeconds?:number}|null;
          const retryable=problem?.retryable??(problem?.retry!==undefined?problem.retry==='same_request':!problem?.status||problem.status===429||problem.status>=500);
          if(current()&&retryable&&this.completionFailures<2){
            const directed=problem?.retryAfterMs??(problem?.retryAfterSeconds??0)*1000;
            const delay=Math.max(1000*2**this.completionFailures++,Number.isFinite(directed)&&directed>0?directed:0);
            const wait=(remaining:number)=>{this.completionRetry=setTimeout(()=>{
              this.completionRetry=undefined;
              if(!current())return;
              if(remaining>2147483647)wait(remaining-2147483647);else {this.endedId=undefined;this.observed();}
            },Math.min(remaining,2147483647));};
            wait(delay);
          }
          else if(current())this.publishQueue({phase:'ready',error:message(error,serviceText('queuePlayer.changeFailed'))});
          return;
        }
        if (!current()) return;
        this.clearCompletionRetry();
        await this.refresh();
        if (!current()) return;
        const view = this.state.view;
        const item = view?.items.find(x => x.entryId === view.queue.currentEntryId);
        const kind = item?.kind ?? '';
        // NEW-44: an audio session is music or a book, whatever the window calls the entry (queue
        // windows name songs "track" and book files "audiobook", and the entry may be outside the
        // window). Listening rules (autoplay, sleep timer, "are you still listening") decide whether
        // it goes on, at once; the post-play hold (a countdown the video player shows) never applies.
        const listening = session.kind === 'audio' || LISTENING_KINDS.has(kind);
        if (listening && !this.listening.mayAdvance()) return;
        if (!listening && (view?.postPlay.passoutCheckDue || view?.postPlay.autoplay === false || this.completionPolicy({kind, itemId: item?.itemId ?? p.itemId ?? ''}) === 'hold')) {
          const held: HeldCompletion = Object.freeze({sessionId: session.id, intentId: intent, nextEntryId: view!.next.entryId, nextAvailable: view!.next.available, reason: view!.next.reason, postPlay: view!.postPlay});
          this.publish({completion: held});
          return;
        }
        if (view?.next.available) await this.advance('completion');
        else this.playback.queueEnded(intent);
      } catch { /* the engine stays ended; the viewer can choose again */ }
    })();
  }

  private clearCompletionRetry(){if(this.completionRetry)clearTimeout(this.completionRetry);this.completionRetry=undefined;this.completionFailures=0;}

  setCompletionPolicy(policy: CompletionPolicy) { this.completionPolicy = policy; }

  async continueCompletion(mode: QueueAdvanceMode = 'automatic'): Promise<void> {
    const held = this.state.completion;
    if (!held || mode === 'automatic' && held.postPlay.passoutCheckDue) return;
    this.publish({completion: null});
    const p = this.playback.getSnapshot();
    if (p.phase !== 'ended' || p.session?.id !== held.sessionId || !held.nextAvailable) return;
    if (mode !== 'automatic') this.listening.interact();
    await this.advance(mode === 'automatic' ? 'completion' : 'next');
  }

  releaseCompletion() { if (this.state.completion) this.publish({completion: null}); }

  // ── Recovery and lifetime ───────────────────────────────────────────

  /** A retained error control may replace only the failed output it opened for. */
  async retryFailed(expected: V1PlaybackTarget, context: V1PlayItemContext): Promise<void> {
    requireSessionChangeCurrent(context);
    const p = this.playback.getSnapshot(), session = this.session, header = this.header;
    if (p.phase !== 'error' || p.playbackRun !== expected.playbackRun || p.itemId !== expected.itemId
      || p.session?.id !== expected.sessionId || p.session.generation !== expected.generation
      || session?.id !== expected.sessionId || session.presentation.generation !== expected.generation
      || session.versionId !== expected.versionId || session.itemId !== expected.itemId
      || session.queue?.queueId !== expected.queueId || session.queue.entryId !== expected.entryId
      || header?.id !== expected.queueId || header.current?.entryId !== expected.entryId
      || !this.window.some(e => e.entryId === expected.entryId && e.available && e.itemId === expected.itemId)) return;
    // The native listening owner already has its own bound retry command.
    if (this.nativeOwns() || session.presentation.subtitle?.delivery === 'other') return;
    const at = this.playback.recoveryTarget() ?? p.positionSeconds, state = this.playback.recoveryIntent();
    const choices: PlaybackChoices = {...this.playback.playbackChoices(), versionId: expected.versionId, discTitle: session.discTitle,
      quality: this.playback.playbackChoices().quality ?? requestInForce(session.presentation.quality),
      ...(session.presentation.audioTrackId ? {audioTrackId: session.presentation.audioTrackId} : {}),
    };
    const subtitle = session.presentation.subtitle;
    const subtitles: StartOptions['subtitles'] = {trackId: subtitle?.trackId ?? null,
      ...(subtitle ? {delivery: subtitle.delivery === 'device' ? 'embeddedClient' : subtitle.delivery === 'sidecar' ? 'sidecar' : 'burn'} : {})};
    const generation = ++this.generation, changeSequence = this.changeSequence; ++this.epoch;
    const intent = this.playback.beginQueueIntent(expected.itemId, false, state);
    const operationCurrent = () => !this.disposed && generation === this.generation && sessionChangeCurrent(context);
    const current = () => {
      const live = this.playback.getSnapshot();
      return operationCurrent() && changeSequence === this.changeSequence && live.intent === state && this.header?.id === expected.queueId && this.header.revision === header.revision
        && this.header.current?.entryId === expected.entryId && live.intentId === intent
        && live.playbackRun === expected.playbackRun && live.session?.id === expected.sessionId
        && live.session.generation === expected.generation;
    };
    const cancel = () => {if (generation === this.generation && this.playback.getSnapshot().intentId === intent
      && this.playback.getSnapshot().session?.id === expected.sessionId) this.playback.leave();};
    context.signal?.addEventListener('abort', cancel, {once: true});
    this.publish({phase: 'preparing', error: null, completion: null});
    try {
      if (!await this.playback.prepareQueueIntent(intent) || !current()) return;
      await this.startEntry(expected.queueId, expected.entryId, intent, at, generation, choices, state, context.signal, current, subtitles);
      // Adoption intentionally changes the output fence. The queue operation still owns its generation.
      if (operationCurrent() && this.session?.queue?.queueId === expected.queueId && this.session.queue.entryId === expected.entryId)
        this.publish({phase: 'ready'});
    } catch (error) {
      if (current()) this.fail(expected.itemId, error, serviceText('queuePlayer.startFailed'));
      throw error;
    } finally {context.signal?.removeEventListener('abort', cancel);}
  }

  /** Retry: start the current entry again, where the failed start or seek meant to be (a resume
   * point that never played is kept, not reset to 0:00). */
  async check(_retryUnknown = false): Promise<void> {
    const h = this.header;
    if (!h?.current) return;
    const item = this.window.find(e => e.entryId === h.current!.entryId)?.itemId, p = this.playback.getSnapshot();
    // The position and the choices belong to the title that failed. When the queue has since
    // moved to another entry, that entry starts where its own progress says.
    const same = !item || !p.itemId || p.itemId === item;
    const at = same ? this.playback.recoveryTarget() : undefined, choices = same ? {...this.playback.playbackChoices(),
      ...(this.session?.discTitle && this.session.itemId === item ? {versionId: this.session.versionId, discTitle: this.session.discTitle} : {})} : undefined;
    const generation = ++this.generation;
    const intent = this.playback.beginQueueIntent(item);
    try { await this.startEntry(h.id, h.current.entryId, intent, at, generation, choices); } catch (e) { if (generation === this.generation) this.fail('', e, serviceText('queuePlayer.startFailed')); }
  }

  private clearStartPreparation() {
    const original = this.preparingStart;
    this.preparingStart = undefined;
    if (original) this.playback.reportStartPreparation(original.intent, undefined);
  }

  async cancel(_invalidateSelection = true): Promise<void> { ++this.generation; ++this.epoch; this.clearCompletionRetry(); this.prepared = undefined; this.audioReadyToken = undefined; this.clearStartPreparation(); this.publish({phase: 'ready'}); }

  leave() { ++this.generation; ++this.epoch; this.clearCompletionRetry(); this.prepared = undefined; this.audioReadyToken = undefined; this.clearStartPreparation(); this.publish({completion: null}); }

  dispose() {
    if (this.disposed) return;
    this.leave();
    this.disposed = true;
    this.unlistenChanged?.();
    ++this.nativeEpoch;
    this.unsubscribe?.();
    this.unlisten?.();
    this.unlistenSessions?.();
    this.unlistenResync?.();
    if (this.refreshTimer) clearTimeout(this.refreshTimer);
    this.detach?.();
    this.listening.dispose();
    if (this.session) this.api.forget(this.session.id);
    this.clearStartPreparation();
    this.state = Object.freeze({...this.state, phase: 'disposed', view: null, preparation: undefined});
    this.listeners.clear();
    this.queueListeners.clear();
  }

  // ── Rendered-audio edges (spec §18) ─────────────────────────────────

  /** The server's music preferences are the effects (X-02); nothing is kept per device here. */
  async saveAudioEffects(): Promise<void> {}

  /** Near the end of a playing, rendered track (the /v2 rule: at most min(40, 30×rate) seconds
   * left), prepare the next entry's audio once. */
  private maybePrepareAudio(): boolean {
    const p = this.playback.getSnapshot(), e = p.audioEffects, session = this.session, header = this.header;
    if (!this.transitions || this.disposed || !session || !header || session.kind !== 'audio' || p.session?.id !== session.id || !e.rendering || !audioTransitionsEnabled(e.settings) || p.channel || p.phase !== 'ready' || p.intent !== 'playing' || p.pendingSeek || p.failedSeek || this.audioAttempt === session.id || this.prepared) return false;
    const remaining = p.duration - p.positionSeconds;
    if (remaining <= 0 || remaining > Math.min(40, 30 * e.rate) || !this.listening.mayTransition(remaining / Math.max(.25, e.rate))) return false;
    this.audioAttempt = session.id;
    const generation = this.generation;
    void (async () => {
      try {
        const next = await this.client.prepareNext(header.id, header.revision, {sessionId: session.id, sessionGeneration: session.presentation.generation});
        if (this.disposed || generation !== this.generation || this.session?.id !== session.id) return;
        // Version 2 (§18.1): the engine decodes the next file itself.
        const audio = playableAudio(next.presentation.audio) ? next.presentation.audio : undefined;
        if (!audio) return;
        const expiresAtMs = Date.parse(next.expiresAt);
        const durationSeconds = audio.durationFrames / audio.sampleRate;
        this.prepared = Object.freeze({token: next.token, queueId: header.id, itemId: next.itemId, expiresAtMs, durationSeconds});
        this.playback.prepareAudio({token: next.token, expiresAtMs, itemId: next.itemId, audio});
      } catch { /* no edge for this track: it ends and advances as usual */ }
    })();
    return true;
  }

  /** The engine has the next entry's first window. */
  audioPrepared(token: string) { if (this.prepared?.token === token) this.audioReadyToken = token; }

  /** At the audio boundary: commit, which moves the queue and starts the next session (§18.3). */
  async commitAudio(token: string): Promise<void> {
    const prepared = this.prepared, p = this.playback.getSnapshot();
    if (this.audioCommitting || this.disposed || !prepared || prepared.token !== token || this.audioReadyToken !== token || p.audioEffects.prepared?.token !== token) return;
    if (!this.listening.mayTransition(Math.max(0, p.duration - p.positionSeconds) / Math.max(.25, p.audioEffects.rate))) { this.discardAudio(); return; }
    if (prepared.expiresAtMs <= Date.now()) { this.discardAudio(); return; }
    this.audioCommitting = true;
    const releaseTimerEdge=this.listening.beginAutomaticTransition();
    const generation = this.generation, intent = p.intentId;
    try {
      let r: Awaited<ReturnType<QueueClient['commitNext']>>;
      try { r = await this.client.commitNext(prepared.queueId, token); }
      // A lost answer: the commit is idempotent by token, so asking again gets the same session.
      catch (e) { if (e instanceof PlaybackApiError) throw e; r = await this.client.commitNext(prepared.queueId, token); }
      if (this.disposed || generation !== this.generation) return;
      this.listening.acceptAutomaticTransition(r.session);
      const old = this.session;
      this.header = r.queue;
      this.session = r.session;
      this.endedId = undefined;
      if (old && old.id !== r.session.id) this.api.retire(old.id); // ended on the server as completed
      const playback = this.api.adopt(r.session, prepared.durationSeconds);
      this.accept(r.queue);
      if (!await this.playback.commitAudioAuthority(token, prepared.itemId, playback, intent)) {
        // The engine moved on meanwhile: play the committed session ordinarily.
        this.prepared = undefined; this.audioReadyToken = undefined;
        await this.adoptPlayback(r.session, playback, this.playback.beginQueueIntent(prepared.itemId));
      }
    } catch (e) {
      if (generation === this.generation && !this.disposed) this.discardAudio();
    } finally {
      this.audioCommitting = false;releaseTimerEdge();
    }
  }

  /** The engine crossed the boundary: the committed session is now the one playing. */
  async audioBoundary(token: string, position: number): Promise<void> {
    if (!this.playback.adoptAudioBoundary(token, position)) return;
    this.prepared = undefined; this.audioReadyToken = undefined; this.audioAttempt = undefined;
    await this.refresh().catch(() => {});
  }

  /** A preparation this device drops: the track ends and advances as usual (the server's token
   * expires or is canceled by the next command). */
  discardAudio() {
    this.prepared = undefined; this.audioReadyToken = undefined;
    this.playback.clearPreparedAudio();
    this.playback.audioDiagnostic('audio_transition_unavailable');
  }

  invalidateAudioPreparation() {
    if (this.playback.getSnapshot().audioEffects.committed || !this.prepared) return;
    this.prepared = undefined; this.audioReadyToken = undefined;
    this.playback.clearPreparedAudio();
  }

  /** Seeking in a track whose successor is committed isn't offered on v1: the seek applies normally. */
  seekRetiringAudio(): boolean { return false; }

  audioRenderFailed(message: string) {
    const p = this.playback.getSnapshot();
    this.prepared = undefined; this.audioReadyToken = undefined;
    if(!this.playback.fallbackAudioRender())this.playback.fail(p.intentId, message);
  }
}

/** Known VOD row kinds; the selection port separately proves local output ownership. */
export function queueVideoKind(kind: string): boolean {
  return ['movie', 'episode', 'video', 'musicVideo', 'extra', 'recording'].includes(kind);
}

/** Publish only the same accepted header/window scope. Header-only changes
 * deliberately leave this offer absent until an actual matching window arrives. */
function queueWindowProjection(header: QueueHeader, window: readonly QueueEntry[]): QueuePlaybackWindow | undefined {
  const scope = queueWindowScope(window);
  if (!scope || scope.queueId !== header.id || scope.revision !== header.revision || window.length > QUEUE_WINDOW_MAX
    || new Set(window.map(entry => entry.entryId)).size !== window.length) return;
  const segments = header.segments.slice(0, QUEUE_WINDOW_MAX);
  if (new Set(segments.map(segment => segment.id)).size !== segments.length) return;
  return Object.freeze({queueId: scope.queueId, revision: scope.revision,
    segments: Object.freeze(segments.map(segment => Object.freeze({id: segment.id, ...(segment.label ? {label: segment.label} : {})}))),
    entries: Object.freeze(window.map(entry => {
      const facts = queueEntryFacts(entry);
      return Object.freeze({entryId: entry.entryId,
        ...(facts.position !== undefined && facts.position < header.total ? {position: facts.position} : {}),
        ...(facts.available !== undefined ? {available: facts.available} : {}),
        ...(facts.available === true && entry.segmentId ? {segmentId: entry.segmentId} : {}),
      });
    })),
  });
}

/** Keep observed positions and placeholders, including gaps. A selected move
 * block must prove its own consecutive positions before it can cross no gaps. */
function queueEditWindow(header: QueueHeader, window: readonly QueueEntry[], target: V1PlaybackTarget): ReadonlyMap<string, QueueEntry> {
  const scope = queueWindowScope(window);
  if (!scope || scope.queueId !== header.id || scope.revision !== header.revision || !window.length || window.length > QUEUE_WINDOW_MAX || !header.current) throw new ContractError('queue edit window');
  const rows = new Map<string, QueueEntry>();
  let previous: number | undefined;
  for (const entry of window) {
    if (queueEntryFacts(entry).position !== entry.position || rows.has(entry.entryId) || !Number.isSafeInteger(entry.position) || entry.position < 0 || entry.position >= header.total
      || previous !== undefined && entry.position <= previous) throw new ContractError('queue edit window');
    rows.set(entry.entryId, entry); previous = entry.position;
  }
  const current = rows.get(target.entryId);
  if (!current || queueEntryFacts(current).available !== true || current.itemId !== target.itemId || current.position !== header.current.position) throw new ContractError('queue edit current');
  return rows;
}

type QueueEditPlan = Readonly<{edit: V1QueueEditIntent; rows: ReadonlyMap<string, QueueEntry>; block: readonly QueueEntry[]; moved?: ReturnType<typeof movedEntry>}>;
/** Both admission modes validate the same original, consecutive upcoming occurrences. */
function queueEditPlan(window: readonly QueueEntry[], anchor: number, intent: V1QueueEditIntent, positional = false): QueueEditPlan {
  let edit: V1QueueEditIntent;
  if (intent?.action === 'remove') edit = Object.freeze({action: 'remove', entryId: id(intent.entryId, 'queue edit entry')});
  else if (intent?.action === 'reorder' && Array.isArray(intent.entryIds) && intent.entryIds.length >= 2 && intent.entryIds.length <= QUEUE_WINDOW_MAX) {
    const entryIds = Object.freeze(intent.entryIds.map(value => id(value, 'queue edit entry')));
    if (new Set(entryIds).size !== entryIds.length) throw new ContractError('queue edit membership');
    edit = Object.freeze({action: 'reorder', entryIds});
  } else throw new ContractError('queue edit');
  const rows = new Map(window.map(row => [row.entryId, row]));
  const upcoming = (entry: QueueEntry | undefined): entry is QueueEntry => !!entry && queueEntryFacts(entry).available === true && !!entry.itemId && entry.position > anchor && (!positional || !['pending', 'hidden', 'other'].includes(entry.kind));
  let moved: ReturnType<typeof movedEntry>, block: readonly QueueEntry[] = [];
  if (edit.action === 'remove') {if (!upcoming(rows.get(edit.entryId))) throw new ContractError('queue edit entry');}
  else {
    const membership = new Set(edit.entryIds); block = window.filter(entry => membership.has(entry.entryId));
    if (block.length !== edit.entryIds.length || block.some((entry, index) => !upcoming(entry) || entry.position !== block[0]!.position + index)) throw new ContractError('queue edit block');
    moved = movedEntry(block.map(entry => entry.entryId), edit.entryIds);
    if (!moved) throw new ContractError('queue edit move');
  }
  return {edit, rows, block, moved};
}
function verifyQueueEdit(plan: QueueEditPlan, window: readonly QueueEntry[], positional = false): void {
  const {edit, rows, block, moved} = plan;
  if (edit.action === 'remove') {
    if (window.some(entry => entry.entryId === edit.entryId)) throw new ContractError('queue edit removal');
    if (positional) {const remaining = [...rows.values()].filter(entry => entry.entryId !== edit.entryId);
      if (remaining.some((entry, index) => window[index]?.entryId !== entry.entryId)) throw new ContractError('queue edit removal order');}
  }
  else {
    const after = window.filter(entry => entry.position >= block[0]!.position && entry.position <= block[block.length - 1]!.position);
    if (positional && window.some(entry => (entry.position < block[0]!.position || entry.position > block[block.length - 1]!.position)
      && [...rows.values()].some(original => original.position === entry.position && original.entryId !== entry.entryId))) throw new ContractError('queue edit surrounding rows');
    if (after.length !== edit.entryIds.length || after.some((entry, index) => {
      const wanted = edit.entryIds[index]!;
      if (wanted !== moved!.entryId) return entry.entryId !== wanted;
      return queueEntryFacts(entry).available !== true || entry.itemId !== rows.get(wanted)!.itemId || (entry.entryId !== wanted && rows.has(entry.entryId));
    })) throw new ContractError('queue edit order');
  }
}

/**
 * The one entry a drag moved, from the order before and after (Up Next's reorder sends the whole
 * new order): where it now sits, as after its new predecessor, or before the first entry.
 */
export function movedEntry(before: readonly string[], after: readonly string[]): {entryId: string; after?: string; before?: string} | undefined {
  if (before.length !== after.length || before.length < 2) return undefined;
  for (const candidate of before) {
    const rest = before.filter(id => id !== candidate), restAfter = after.filter(id => id !== candidate);
    if (rest.every((id, i) => restAfter[i] === id) && before.indexOf(candidate) !== after.indexOf(candidate)) {
      const at = after.indexOf(candidate);
      return at > 0 ? {entryId: candidate, after: after[at - 1]} : {entryId: candidate, before: after[1]};
    }
  }
  return undefined;
}

/** Whether the server speaks Playback Protocol v1 (`GET /v1/capabilities` → `features.playback_v1`). */
/** Whether the server offers queue transitions for rendered audio (spec §18). */
export function serverSupportsQueueTransitions(capabilities: unknown): boolean {
  const features = capabilities && typeof capabilities === 'object' ? (capabilities as {features?: unknown}).features : undefined;
  return !!features && typeof features === 'object' && (features as Record<string, unknown>).queueTransitions === 'enabled';
}

export function serverSupportsPlaybackV1(capabilities: unknown): boolean {
  const features = capabilities && typeof capabilities === 'object' ? (capabilities as {features?: unknown}).features : undefined;
  return !!features && typeof features === 'object' && (features as Record<string, unknown>).playback_v1 === 'enabled';
}

/** Whether a media kind plays on Playback Protocol v1 when the server supports it: video,
 * audiobooks and music (B7, 23 Sep: music moved once both engines decode on the device; `music:
 * false` keeps it on /v2 for a client that must). Live channels switch on their own capability. */
export function playbackV1ForKind(serverSupportsV1: boolean, kind: string, options: Readonly<{music?: boolean}> = {}): boolean {
  if (!serverSupportsV1) return false;
  if (['movie', 'episode', 'video', 'audiobook', 'audiobook_file', 'book'].includes(kind)) return true;
  return options.music !== false && ['song', 'track', 'album', 'artist', 'disc'].includes(kind);
}

export {QUEUE_WINDOW_MAX};
