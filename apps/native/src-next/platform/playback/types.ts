import type {V1QueueEditIntent, V1QueuedEntrySelection, V1QueuePolicyIntent} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import type {ComponentType, RefObject} from 'react';
import type {View} from 'react-native';
import type {OfflineEntry} from '../../../platform/offline-downloads';
import type {NativeOfflinePlaybackDevice} from './offline-playback';
import type {HttpLocalApi, LocalSession, PlaybackSnapshot} from '../../../../../packages/client-core/src/index';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {EventsClient} from '../../../../../packages/client-core/src/playback-v1/events';
import type {PlaybackOptions, Session} from '../../../../../packages/client-core/src/playback-v1/types';
import type {QueuePlayerState} from '../../../../../packages/client-core/src/queue-controller';
import type {PlaybackSubject} from '../../../../../packages/client-core/src/presentation/kit-model';
import type {SubtitleSnapshot} from '../../../../../packages/client-core/src/subtitles';
import type {CapabilityOverrides} from '../../../../../packages/client-core/src/client-profile';
import type {DockState, DockTransition, PlaybackDock, PlaybackFence, RemoteDock} from '../../../../../packages/player-core/src';
import type {SkipMode} from '../../../../../packages/player-core/src';
import type {EngineFeatures, EngineKind, EngineSetting} from './engine-choice';
import type {EngineSelection, NativeBinding, NativePlaybackTransport} from './NativePlaybackCoordinator';
import type {NativeListeningV1Control} from '../../../../../packages/client-core/src/playback/native-listening';
import type {Timers} from '../../../../../packages/client-core/src/playback-v1/sessions';
import type {HomeSelectionPlaybackPort} from '../../../../../packages/client-core/src/home-selection/types';
import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model/index';
import type {PlaybackChoices} from '../../../../../packages/client-core/src/playback-v1/choices';
import type {V1QueuePlayer} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import type {SegmentInput} from '../../../../../packages/client-core/src/playback-v1/queue';
import type {QueueHeader} from '../../../../../packages/client-core/src/playback-v1/types';
import type {QualityOption} from '../../../../../packages/client-core/src/playback-v1/quality';
import type {NativePlaybackSubtitleControls} from './subtitle-controls';
import type {NativeSubtitleFilePicker} from './subtitle-file-upload';
import type {SubtitlePreferences} from '../../../../../packages/client-core/src/preference-effects';
import type {NativePlaybackStartFailure} from './start-failure';
import type {NativePlaybackCapacityWait} from './capacity-retry';
import type {ListeningSnapshot} from '../../../../../packages/client-core/src/listening';
import type {AudiobookPreferences} from '../../../../../packages/client-core/src/audiobook-preferences';
import type {PlayerChapter, PlayerChaptersData, PlayerChaptersSnapshot} from '../../../../../packages/client-core/src/player-chapters';
import type {NativeStartArtworkOffer, NativeStartPresentation, NativeFirstVideoFrame} from './start-presentation';
import type {PolicyView} from '../../../../../packages/player-core/src/playback-policy';

/** H-11: keyed on `privacyFence ?? viewerFence` (like the shell's `authorityKey`), never the library list. */
export type PlaybackAuthority = Readonly<{serverId: string; accountId: string; profileId: string; epoch: string; viewerFence: string; privacyFence?: string}>;
export type NativePlaybackStart = Readonly<{entry: ContentEntry; target: NonNullable<ContentEntry['playback']>; origin?: Readonly<{kind: 'home-row'; rowId: string}>;
  /** Ordinary Play/Resume asks the server for its current position. */
  startIntent?: 'resume' | 'start-over' | 'explicit';
  /** An exact edition to start, used only when the server still offers it. */
  versionId?: string;
  /** Optional pixels measured at this original touch, never playback authority. */
  presentation?: NativeStartArtworkOffer;
  /** A group prepares this title paused until its authoritative clock plays. */
  state?: 'playing' | 'paused';
  /** Real caller lifetime through options/create/adoption. It excludes an old output fence. */
  lifetime?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
}>;
export type NativeChannelGuideOrigin = Readonly<{
  row: import('../../../../../packages/client-core/src/guide/types').GuideChannelRow;
  options: import('../../../../../packages/client-core/src/guide/legacy-source').LegacyGuideOptions;
}>;
export type NativePlaybackChannelStart = Readonly<{sourceId: string; channelId: string; timezone: string;
  /** Once, only this operation's actually adopted playable channel fence. A cold
   * generation-zero session has no fence; it waits for its own actual update. */
  onAccepted?: (fence: import('../../../../../packages/player-core/src').ChannelPlaybackFence) => void;
  guide?: NativeChannelGuideOrigin;
  expectedFence?: import('../../../../../packages/player-core/src').ChannelPlaybackFence;
  expectedSourceGeneration?: string;
  lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
}>;
export type NativePlaybackVersionSelection = Readonly<{
  authority: PlaybackAuthority; options: PlaybackOptions; versionId: string;
  /** Exact ready title from this physical version's measured preview. */
  discTitle?: import('../../../../../packages/client-core/src/playback-v1/types').DiscTitleRef;
  /** The real chooser/route/entry lifetime. Output-fence checks remain runtime-owned. */
  lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
  /** Exact source when the menu opened, including an explicitly absent source. */
  expectedFence: PlaybackFence | undefined;
}>;
export type NativeExternalPresentationLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type NativePlaybackReadLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
/** Read facts only. No chapter seek action or playback mutation availability is projected. */
export type NativeListeningReadSnapshot = Readonly<{phase: 'retired'}> | Readonly<{
  phase: 'current';
  rate: Readonly<{selected: number; effective?: number; supported?: readonly number[]}>;
  preferences: Readonly<{phase: ListeningSnapshot['phase']; values: ListeningSnapshot['preferences']; audiobook?: AudiobookPreferences}>;
  chapters: Readonly<{phase: PlayerChaptersSnapshot['phase']; pagination: PlayerChaptersSnapshot['pagination'];
    data: (Pick<PlayerChaptersData, 'scope' | 'revision' | 'status' | 'reason' | 'duration' | 'totalCount' | 'nextCursor'>
      & Readonly<{chapters: readonly Pick<PlayerChapter, 'id' | 'index' | 'title' | 'startSeconds' | 'endSeconds' | 'thumbnailUrl'>[]}>) | null}>;
}>;
export type NativeListeningReadPort = Readonly<{
  current: () => boolean;
  getSnapshot: () => NativeListeningReadSnapshot;
  subscribe: (listener: () => void) => () => void;
  /** These operations only read pages of the captured source; they never select a chapter. */
  readChapters: () => Promise<void>;
  readNextChapterPage: () => Promise<void>;
  readPreviousChapterPage: () => Promise<void>;
  dispose: () => void;
}>;
export type NativeOfflinePlaybackLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type NativePlaybackBounds = Readonly<{x: number; y: number; width: number; height: number}>;
export type NativePlaybackSurfaceProps = Readonly<{
  dockBounds?: NativePlaybackBounds; expandedBounds?: NativePlaybackBounds; docked: boolean; subtitlesVisible: boolean;
  /** Composition only: a retained current-fence image may cover the live producer without unmounting it. */
  surfaceActive: boolean;
}>;
export type NativePlaybackVideoExitImage = Readonly<{
  fence: PlaybackFence; image: Readonly<{uri: string; width: number; height: number}>;
  signal: AbortSignal; paintCurrent(): boolean; subscribe(listener: () => void): () => void;
  release(): Promise<void>;
}>;
export type NativePlaybackVideoSnapshot = Readonly<{
  fence: PlaybackFence; image: Readonly<{uri: string; width: number; height: number}>; release: () => Promise<void>;
  /** Claim only while the original mini video and captured page are current. Never re-captures. */
  retainForExit?: () => Promise<NativePlaybackVideoExitImage | undefined>;
}>;
export type NativePlaybackSettings = Readonly<{
  passthroughFormats?: import('./passthrough-formats').PassthroughFormats;
  /** Android display matching; absent is the platform's existing Automatic behavior. */
  matchFrameRate?: 'auto' | 'off';
  matchDynamicRange?: 'auto' | 'off';
  audiobook?: import('../../../../../packages/client-core/src/audiobook-preferences').AudiobookPreferences;
  /** The profile's Music > Sound settings, as the listening engine renders them. */
  audioEffects?: import('../../../../../packages/client-core/src/audio-effects').AudioEffectsSettings;
  engine: EngineSetting; overrides?: CapabilityOverrides;
  videoRate: number; audiobookRate: number;
  skips: Readonly<Record<'intro' | 'recap' | 'credits', SkipMode>>;
  subtitles: SubtitlePreferences;
}>;
export type NativePlaybackCapabilities = Readonly<{play: boolean; pause: boolean; stop: boolean; retry?: boolean; queuePolicy?: boolean; queueEdit?: boolean; queueSelection?: boolean; seek: boolean; previous: boolean; next: boolean; cast: boolean; airPlayVideo?: boolean; quality: boolean; version: boolean; audioTrack: boolean; subtitleTrack: boolean; subtitlesCanHide: boolean}>;
export type NativePictureInPictureState = 'idle' | 'started' | 'restore' | 'stopped' | 'unavailable';

export type NativePlaybackRuntimeSnapshot = Readonly<{
  authority: PlaybackAuthority; sequence: number; dock: DockState; playback: PlaybackSnapshot;
  fence?: PlaybackFence; subject?: PlaybackSubject; entry?: ContentEntry;
  queue: QueuePlayerState; session?: Session; options?: PlaybackOptions; subtitles?: SubtitleSnapshot;
  /** Effective Settings plus appearance overrides for this playback run only. */
  subtitleStyle?: SubtitlePreferences;
  /** Shared skip, credits and end-card policy for this exact video source. */
  policy?: PolicyView;
  /** Direct-file audio is selected only after the current engine acknowledges it. */
  audioTrackSelection?: import('./audio-track-selection').NativeAudioTrackStatus;
  localTracks?: import('./offline-tracks').NativeLocalTracksSnapshot;
  channelGuide?: NativeChannelGuideOrigin;
  seekWindow?: Readonly<{startSeconds: number; endSeconds: number}>;
  capabilities: NativePlaybackCapabilities; engine: EngineKind; lease: string;
  effectiveRate: number; supportedRates: readonly number[];
  /** Present only for a current mpv image track with known mixed forced lines. */
  forcedOnly?: Readonly<{on: boolean}>;
  /** Actual native external source readiness. Absent=unknown; null=unready. Not PiP support. */
  externalPresentationBinding?: NativeBinding | null;
  /** Installed retained-view request port, and the admitted native state of that
   * same view: only the view's own fenced report sets `active` or `state`. */
  pictureInPicture?: Readonly<{request: number; binding?: NativeBinding; active: boolean; state: NativePictureInPictureState}>;
  retired: boolean;
  startFailure?: NativePlaybackStartFailure;
  capacityWait?: NativePlaybackCapacityWait;
  initialPresentation?: NativeStartPresentation;
  /** Only an admitted renderer firstFrame fact for this exact current binding. */
  firstVideoFrame?: NativeFirstVideoFrame;
  /** O-159: a Play drawn at once from the pressed card (Stop only) until its real dock exists. */
  starting?: NativePlaybackStarting;
  /** O-159: a Play whose real dock or player was not drawn within NATIVE_START_DEADLINE_MS of Play. */
  startTimedOut?: NativePlaybackStartTimedOut;
}>;
/** What a start is drawn as before its real dock: the pressed card's title and art (a local file
 * for a download). Absent until known for a start that came without a card (a group or radio). */
/** What a starting dock draws. A start whose title is not known yet (a catalogue or group start)
 * draws a generic "Starting…" with its kind's icon when known, and is named once the runtime learns it. */
export type NativeStartDisplay = Readonly<{title?: string; icon?: 'i-music'; posterUrl?: string; localArtwork?: string}>;
/** `reason`: a Live start the server reports recoverable (its source isn't responding) while it keeps trying. */
export type NativePlaybackStarting = Readonly<{key: string; display: NativeStartDisplay; reason?: 'channel_source'; stop: () => void}>;
export type NativePlaybackStartTimedOut = Readonly<{key: string; retry: () => Promise<void>; close: () => void}>;

/** One authority lifetime. Supply the shell's existing feed; the runtime never opens another. */
export type NativePlaybackRuntimeOptions = Readonly<{
  /** Opt in only with an installed refusal presenter. Captured before options/read/create. */
  startFailurePresentation?: boolean;
  captureStartContext?: () => (() => boolean) | undefined;
  subscribeStartContext?: (listener: () => void) => () => void;
  androidDisplayMatching?: true;
  nativeDynamicRangeMatching?: true;
  /** The native downloaded-WebVTT renderer applies a session offset separately from baked timing. */
  nativeLocalSubtitleTiming?: true;
  api: HttpLocalApi;
  authority: () => PlaybackAuthority;
  session: () => LocalSession | undefined;
  subscribeAuthority: (listener: () => void) => () => void;
  events: Pick<EventsClient, 'on' | 'onResync'> & {onResyncRead?(read: (context: {signal: AbortSignal; isCurrent(): boolean}) => Promise<void>, options?: {priority?: number}): {dispose(): void}};
  settings: () => NativePlaybackSettings;
  subscribeSettings: (listener: () => void) => () => void;
  requestId: () => string;
  transport: NativePlaybackTransport;
  engines: EngineSelection;
  engineFeatures: (engine: EngineKind) => EngineFeatures;
  nativeListening?: NativeListeningV1Control;
  capabilitiesReady: () => Promise<void>;
  disposeCapabilities: () => void;
  /** Borrow the existing authority-owned device connection; never connect/adopt a second one. */
  offlineDevice?: NativeOfflinePlaybackDevice;
  timers?: Timers;
  /** Optional actual destination binding. Its absence removes Cast from capabilities and API. */
  openDestinations?: (fence: PlaybackFence, anchorRef?: RefObject<View | null>) => Promise<void>;
  /** Platform-owned picker only; upload stays in the one current-source SubtitleService. */
  subtitleFilePicker?: NativeSubtitleFilePicker;
  /** The platform factory actually wires the retained view's existing request props. */
  pictureInPictureControls?: true;
}>;

export interface NativePlaybackAdapter {
  dock: PlaybackDock;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativePlaybackRuntimeSnapshot;
  start: (input: NativePlaybackStart) => Promise<void>;
  /** The installed queue owner's existing explicit entry command. */
  queuePlay?: (queueId: string, entryId: string, expectedRevision: string) => Promise<void>;
  /** The existing queue owner, for a server-named container (album, artist, show). */
  cataloguePlayback?: () => Pick<V1QueuePlayer, 'playSegments'> & Readonly<{
    /** Append to the one authority-local queue. The caller's lifetime and the active group's
     * authority are checked before every write and adoption; this is never a second store. */
    enqueueSegments: (segments: readonly SegmentInput[], placement: 'next' | 'end',
      lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => Promise<QueueHeader>;
  }>;
  startVersion: (input: NativePlaybackStart, selection: NativePlaybackVersionSelection) => Promise<void>;
  startOffline?: (entry: OfflineEntry, lifetime: NativeOfflinePlaybackLifetime) => Promise<void>;
  change: (fence: PlaybackFence, choices: PlaybackChoices) => Promise<void>;
  chooseQuality: (fence: PlaybackFence, quality: QualityOption) => Promise<void>;
  setRate: (fence: PlaybackFence, rate: number) => void;
  setForcedOnly?: (fence: PlaybackFence, on: boolean) => boolean;
  setSubtitleStyle?: (fence: PlaybackFence, key: string, value: string) => boolean;
  subtitleControls: (fence: PlaybackFence) => NativePlaybackSubtitleControls | undefined;
  /** Fresh read only; never adopts/replans the session or fabricates report facts. */
  readSession?: (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime) => Promise<Session | undefined>;
  /** Original-source audiobook facts only, pending Media's occurrence-bound accepting API. */
  listeningRead?: (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime) => NativeListeningReadPort | undefined;
  selectionPlayback: (isCurrent: (context: SelectionContext) => boolean) => HomeSelectionPlaybackPort;
  onTransition: (transition: DockTransition) => Promise<void>;
  Surface: ComponentType<NativePlaybackSurfaceProps>;
  captureVideoSnapshot: (fence: PlaybackFence, options?: {signal?: AbortSignal}) => Promise<NativePlaybackVideoSnapshot>;
  toggle: (fence: PlaybackFence) => Promise<void>;
  replay: (fence: PlaybackFence) => Promise<void>;
  /** Retry the exact failed VOD output at its saved target, preserving choices and intent. */
  retry?: (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime) => Promise<void>;
  /** Caller lifetime survives this port's own accepted output replacement. */
  selectQueuedEntry?: (fence: PlaybackFence, request: V1QueuedEntrySelection, lifetime: NativePlaybackReadLifetime) => Promise<void>;
  /** Edit only loaded upcoming rows under the drawn output and revision. */
  editQueue?: (fence: PlaybackFence, request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueueEditIntent}>, lifetime: NativePlaybackReadLifetime) => Promise<void>;
  setQueuePolicy?: (fence: PlaybackFence, request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueuePolicyIntent}>, lifetime: NativePlaybackReadLifetime) => Promise<void>;
  seek: (fence: PlaybackFence, seconds: number) => Promise<void>;
  skip: (fence: PlaybackFence, seconds: number) => Promise<void>;
  previous: (fence: PlaybackFence) => Promise<void>;
  next: (fence: PlaybackFence) => Promise<void>;
  policyDismiss?: (fence: PlaybackFence) => boolean;
  policySkip?: (fence: PlaybackFence, markerId: string) => Promise<void>;
  policyUndo?: (fence: PlaybackFence) => Promise<void>;
  policyAdvance?: (fence: PlaybackFence, mode: 'manual' | 'still-watching') => Promise<void>;
  policyCancelAutoplay?: (fence: PlaybackFence) => void;
  cast?: (fence: PlaybackFence, anchorRef?: RefObject<View | null>) => Promise<void>;
  prepareExternalPresentation?: (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime) => Promise<NativeBinding | undefined>;
  requestPictureInPicture?: (fence: PlaybackFence, lifetime: NativeExternalPresentationLifetime) => Promise<boolean>;
  /** Channels only: return to the live edge of the retained window. */
  goLive?: (fence: PlaybackFence) => Promise<void>;
  /** Another device takes the dock: the explicit connection start (Portico play-on handoff). */
  beginRemote?: (value: RemoteDock) => Promise<void>;
  /** A later update of that same occupant, in its own sequence. */
  updateRemote?: (value: RemoteDock) => Promise<void>;
  /** Dismisses the stopped/ended remote bar; a late update cannot resurrect it. */
  dismissRemote?: (id: string) => Promise<void>;
  /** Brings a stopped handoff's title back to this phone at the receiver's position. */
  continueHere?: (id: string) => Promise<void>;
  /** The viewer's sleep timer for this playback: minutes, or null to clear it. */
  setSleepTimer?: (fence: PlaybackFence, minutes: number | null) => Promise<void>;
  stop: (fence: PlaybackFence) => Promise<void>;
  restoreBookmark: (expectedBookmarkFence: PlaybackFence, playExplicitly: boolean) => Promise<PlaybackFence | undefined>;
  open: (fence: PlaybackFence) => void;
  dispose: () => void;
}
