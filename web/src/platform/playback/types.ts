import type {HttpLocalApi, PlaybackOptions as ServiceOptions, PlaybackSnapshot} from '@core/index.ts';
import type {AudiobookPreferences} from '@core/audiobook-preferences.ts';
import type {ContentEntry, ContentScope} from '@core/library-content.ts';
import type {PlaybackSubject} from '@core/presentation/kit-model/index.ts';
import type {PlayerOffersData} from '@core/player-offers.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import type {V1Viewer} from '@core/playback-v1/v1-queue-player.ts';
import type {PlaybackOptions, Session} from '@core/playback-v1/types.ts';
import type {Timers} from '@core/playback-v1/sessions.ts';
import type {SubtitlePreferences} from '@core/preference-effects.ts';
import type {CapabilityDocument} from '@core/client-profile.ts';
import type {PlaybackMediaPort, RenderEngine} from './web-playback.ts';
import type {PlaybackService} from '@core/index.ts';
import type {WebMediaPlayerOptions} from './web-media-player.ts';
import type {AudioEffectsSettings} from '@core/audio-effects.ts';
import type {QualityRequest} from '@core/playback-v1/quality.ts';
import type {QueuePlaybackView} from '@core/queue-controller.ts';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {DockState, DockTransition, PlaybackFence, QueuePlaybackFence, PolicyView, SkipMode} from '../../../../packages/player-core/src/index.ts';
import type {HlsModule, VideoLike, WebPlayerTimers} from './web-media-player.ts';
import type {EqualizerPort} from '../../kit/media/equalizer-port.ts';
import type {LocalPlaybackScope} from '../../../../packages/player-core/src/index.ts';
import type {WebLocalQueueSnapshot, WebLocalQueueResource} from './local-queue.ts';
import type {WebPlaybackCapacityWait} from './capacity-retry.ts';
import type {WebPlaybackStartFailure} from './start-failure.ts';
import type {WebPreparingStarts} from './preparing-start';

/** The session that owns playback. A Home privacy fence is optional: the shell's player is built
 * from the session and never waits for Home; Home selections are then checked by their own guard. */
export type WebPlaybackAuthority = V1Viewer & Readonly<{epoch: string | number; viewerFence?: string}>;
export type WebPlaybackStart = Readonly<{entry: ContentEntry; target: NonNullable<ContentEntry['playback']>; origin?: Readonly<{kind: 'home-row'; rowId: string}>}>;
export type WebPlaybackVersionStart = Readonly<{itemId: string; versionId: string; context: SelectionContext; signal: AbortSignal}>;
/** O-159: the pressed card's own facts, so an accepted start is drawn at once (the starting dock). */
export type WebPlaybackStartDisplay = Readonly<{subject: PlaybackSubject; posterUrl?: string}>;
export type WebPlaybackScreenTarget = Readonly<{libraryId: string; itemId: string; startSeconds: number; versionId?: string; storedResume?: boolean; display?: WebPlaybackStartDisplay}>;
/** A start with no drawable dock yet: drawn from its display facts with Stop only, never "playing". */
export type WebPlaybackStarting = Readonly<{key: string; display: WebPlaybackStartDisplay; stop: () => void; /** The server reports why it isn't ready yet. */ waiting?: 'channel_source'}>;
/** An accepted start that never became drawable by its deadline: stopped, with Try again. */
export type WebPlaybackStartTimedOut = Readonly<{key: string; retry: () => Promise<void>; close: () => void}>;
export type WebPlaybackScreenLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
export type WebPlaybackSettings = Readonly<{
  audiobookPreferences?: () => AudiobookPreferences;
  subtitlePreferences?: () => SubtitlePreferences;
  videoSpeed: () => number;
  audiobookSpeed: () => number;
  /** Undefined leaves the saved delivery default to the server. Original is an explicit choice. */
  defaultQuality?: () => QualityRequest | undefined;
  skipModes: () => Readonly<Record<string, SkipMode>>;
  audioEffects: () => AudioEffectsSettings;
  /** Canonical playback.showSyncedLyrics; absent/unknown keeps the Lyrics opening withdrawn. */
  lyricsEnabled?: () => boolean;
  musicDefaults?: () => Readonly<{shuffle: boolean; repeat: 'off' | 'one' | 'all'}> | undefined;
  webVolume: () => boolean;
}>;
/** The shell supplies an actual viewer-bound transport/feed, and destroys this runtime before
 * replacing that authority. isCurrent also fences an async callback during shell teardown. */
export type WebPlaybackSource = Readonly<{
  /** Original shell principal, independent of a Home/player view's lifetime. */
  preparingStarts?: WebPreparingStarts;
  /** Same actual session as authority, including its independently captured family id. */
  localScope?: LocalPlaybackScope;
  api: HttpLocalApi; authority: WebPlaybackAuthority; contentScope: ContentScope;
  events: Pick<EventsClient, 'on'> & Partial<Pick<EventsClient, 'onResync' | 'getStatus' | 'subscribeStatus' | 'onResyncRead' | 'resyncReads'>>; isCurrent: () => boolean;
  settings: WebPlaybackSettings;
}>;
/** Bound to one runtime and its captured five-field local authority. The app registers
 * this controller without starting playback; actual native support may mount later. */
export type WebLocalQueueController = Readonly<{
  canPlayLocalQueue(): boolean;
  playLocalQueue(resource: WebLocalQueueResource, lifetime: WebPlaybackScreenLifetime): Promise<void>;
  subscribe(listener: () => void): () => void;
}>;
export type WebPlaybackEnvironment = Readonly<{
  /** App-private preparation/Saved bridge. Cleanup retires only this attachment. */
  attachLocalQueue?: (scope: LocalPlaybackScope, controller: WebLocalQueueController) => () => void;
  /** The app's one device store, shared by Settings and the player shortcut. */
  equalizerPort?: EqualizerPort;
  /** Build-time platform injection. null disables the browser music engine. */
  createPlayer?: (video: VideoLike, options: WebMediaPlayerOptions) => PlaybackMediaPort;
  createRender?: ((service: PlaybackService, video: VideoLike) => RenderEngine) | null;
  capabilities?: () => Promise<CapabilityDocument>;
  subtitleSurface?: boolean;
  page?: Document; browser?: Window; fetch?: typeof fetch;
  loadHls?: () => Promise<HlsModule>; nativeHls?: 'first' | 'fallback' | 'never';
  timers?: Timers; playerTimers?: WebPlayerTimers; key?: () => string;
  service?: Pick<ServiceOptions, 'startupTimeoutMs' | 'seekTimeoutMs' | 'operationTimeoutMs'>;
}>;
export type WebPlaybackControl = 'play' | 'pause' | 'replay' | 'seek' | 'previous' | 'next' | 'volume';
export type WebPlaybackPresentation = Readonly<{
  fence: PlaybackFence; subject: PlaybackSubject; kind: 'video' | 'music' | 'audiobook';
  posterUrl?: string; backdropUrl?: string; durationSeconds: number; positionSeconds: number;
  playing: boolean; expanded: boolean; finished: boolean; controls: readonly WebPlaybackControl[];
  seekWindow?: Readonly<{startSeconds: number; endSeconds: number}>;
  volume: number; muted: boolean; rate: number; policy: PolicyView | null;
}>;
/** A fresh authorized GET, separate from queue adoption and renderer observations. */
export type WebPlaybackInfoRead = Readonly<{
  fence:QueuePlaybackFence;sessionRevision:string;
  playbackInfo?:Session['playbackInfo'];observedPlayback?:Session['observedPlayback'];
  isCurrent():boolean;
}>;
export type WebPlaybackRuntimeSnapshot = Readonly<{
  /** A captured first start refused before any playback fence was admitted. */
  startFailure?: WebPlaybackStartFailure;
  /** The server refused conversion capacity and directed an exact idempotent retry. */
  capacityWait?: WebPlaybackCapacityWait;
  /** O-159 watchdog: a start drawn before its dock exists, and one stopped at its deadline. */
  starting?: WebPlaybackStarting; startTimedOut?: WebPlaybackStartTimedOut;
  /** Actual app-local receipt/queue state, never projected as a V1 session or queueView. */
  local?: WebLocalQueueSnapshot;
  nativeVideoSurfaceActive: boolean;
  authority: WebPlaybackAuthority; sequence: number; playback: PlaybackSnapshot; dock: DockState;
  fence?: PlaybackFence; subject?: PlaybackSubject; presentation?: WebPlaybackPresentation;
  presentations: readonly WebPlaybackPresentation[];
  queueView: QueuePlaybackView | null;
  session?: Session; offers: PlayerOffersData | null; options: PlaybackOptions | null;
  problem?: string; disposed: boolean;
}>;
/** Structural input accepted directly from DockBinding. Unsupported presentation controls are
 * rejected, never listed as a capability. Remote ids are rejected without a real remote binding. */
export type WebPlaybackAction = Readonly<{
  kind: string; fence: PlaybackFence; remote?: Readonly<{id: string; sequence: string}>;
  seconds?: number; volume?: number; shuffle?: boolean; repeat?: 'off' | 'one' | 'all';
  direction?: -1 | 1;
}>;
export type WebPlaybackTransition = DockTransition;
export type WebPlaybackElement = VideoLike;
