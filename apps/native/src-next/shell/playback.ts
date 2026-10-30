import type {ComponentType, RefObject} from 'react';
import type {View} from 'react-native';
import type {HomeSelectionPlaybackPort} from '../../../../packages/client-core/src/home-selection/types';
import type {SelectionContext} from '../../../../packages/client-core/src/selection-model/index';
import type {PlaybackSnapshot} from '../../../../packages/client-core/src/index';
import type {V1QueuePlayer, V1QueueEditIntent} from '../../../../packages/client-core/src/playback-v1/v1-queue-player';
import type {SegmentInput} from '../../../../packages/client-core/src/playback-v1/queue';
import type {QueueHeader} from '../../../../packages/client-core/src/playback-v1/types';
import type {QueuePlayerState} from '../../../../packages/client-core/src/queue-controller';
import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import type {PlaybackSubject} from '../../../../packages/client-core/src/presentation/kit-model';
import type {NativePlaybackAdapter, NativePlaybackRuntimeSnapshot, NativePlaybackVideoExitImage} from '../platform/playback/types';
import {sameFence, type DockState, type DockTransition, type PlaybackDock, type PlaybackFence, type RemoteDock} from '../../../../packages/player-core/src';
import {sameAuthority, type ShellAuthority} from './model';
import type {NativePlaybackStartFailure} from '../platform/playback/start-failure';
import type {NativePlaybackCapacityWait} from '../platform/playback/capacity-retry';
import type {NativeStartArtworkOffer} from '../platform/playback/start-presentation';

export type NativeShellPlaybackStart = Readonly<{
  /** The real display record and the server-selected target stay separate for show continuations. */
  entry: ContentEntry;
  target: NonNullable<ContentEntry['playback']>;
  origin?: Readonly<{kind: 'home-row'; rowId: string}>;
  /** Ordinary Play/Resume asks the server for its current position. */
  startIntent?: 'resume' | 'start-over' | 'explicit';
  /** An exact edition to start, used only when the server still offers it. */
  versionId?: string;
  presentation?: NativeStartArtworkOffer;
}>;
export type NativeShellBounds = Readonly<{x: number; y: number; width: number; height: number}>;
export type NativeShellVideoSnapshot = Readonly<{
  retainForExit?: () => Promise<NativePlaybackVideoExitImage | undefined>;
  fence: PlaybackFence;
  image: Readonly<{uri: string; width: number; height: number}>;
  release: () => Promise<void>;
}>;
export type NativeShellPlaybackSnapshot = Readonly<{
  /** Actual runtime retirement, never inferred from a missing session/fence. */
  retired: NativePlaybackRuntimeSnapshot['retired'];
  authority: ShellAuthority;
  dock: DockState;
  playback: PlaybackSnapshot;
  startFailure?: NativePlaybackStartFailure;
  capacityWait?: NativePlaybackCapacityWait;
  /** O-159: the drawn start and its 5 s watch (platform/playback/runtime.ts). */
  starting?: NativePlaybackRuntimeSnapshot['starting'];
  startTimedOut?: NativePlaybackRuntimeSnapshot['startTimedOut'];
  initialPresentation?: NativePlaybackRuntimeSnapshot['initialPresentation'];
  firstVideoFrame?: NativePlaybackRuntimeSnapshot['firstVideoFrame'];
  fence?: PlaybackFence;
  subject?: PlaybackSubject;
  entry?: ContentEntry;
  /** The listening owner's own queue projection for a queue-backed occupant (album,
   * artist, audiobook): the same items, order and durations the engine plays, read
   * here only to draw them. Absent for a local file or while no queue is ready. */
  queue?: QueuePlayerState;
  /** The runtime's admitted retained-view fact, passed through without inference. */
  pictureInPicture?: NativePlaybackRuntimeSnapshot['pictureInPicture'];
  seekWindow?: Readonly<{startSeconds: number; endSeconds: number}>;
  capabilities: Readonly<{play: boolean; pause: boolean; stop: boolean; retry?: boolean; queueEdit?: boolean; seek: boolean; previous: boolean; next: boolean; cast: boolean; airPlayVideo?: boolean}>;
}>;

/** TV video has no collapsed state. Audio folds away; phones retain their video dock. */
export function nativeShellBackTransition(dock: PlaybackDock, snapshot: Pick<NativeShellPlaybackSnapshot, 'authority' | 'fence' | 'dock'>, authority: ShellAuthority, isTV: boolean): DockTransition | undefined {
  if (!sameAuthority(snapshot.authority, authority) || !snapshot.fence || !snapshot.dock.local?.expanded || !sameFence(snapshot.dock.local.fence, snapshot.fence)) return undefined;
  const video = snapshot.dock.local.kind === 'video' || snapshot.dock.local.kind === 'live';
  return isTV && video ? dock.close(snapshot.fence) : dock.collapse(snapshot.fence);
}
export type NativeShellSurfaceProps = Readonly<{
  /** Window coordinates measured from the actual art slot; undefined until mounted/measured. */
  dockBounds?: NativeShellBounds;
  /** Absent while the genuine watch outlet has no current measured picture. */
  expandedBounds?: NativeShellBounds;
  docked: boolean;
  subtitlesVisible: boolean;
  /** Producer composition only; false only while a current-fence real snapshot covers it. */
  surfaceActive: boolean;
}>;

/** Shell contract for the genuine platform runtime. No engine/session state is synthesized here.
 * Mount Surface exactly once above destinations. The dock positions that same surface; it never
 * receives a second video view or clones/reparents a mounted native player. */
export interface NativeShellPlaybackAdapter {
  dock: PlaybackDock;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeShellPlaybackSnapshot;
  start: (input: NativeShellPlaybackStart) => Promise<void>;
  /** The same authority-local queue owner, for screens that play a server-named group
   * (an album or an artist) rather than one item. Absent only where the platform has no queue player. */
  cataloguePlayback?: () => Pick<V1QueuePlayer, 'playSegments'> & Readonly<{
    /** Append to the single authority-local queue, never a second store. */
    enqueueSegments: (segments: readonly SegmentInput[], placement: 'next' | 'end',
      lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => Promise<QueueHeader>;
  }>;
  selectionPlayback: (isCurrent: (context: SelectionContext) => boolean) => HomeSelectionPlaybackPort;
  onTransition: (transition: DockTransition) => Promise<void>;
  restoreBookmark: (expectedBookmarkFence: PlaybackFence, playExplicitly: boolean) => Promise<PlaybackFence | undefined>;
  Surface: ComponentType<NativeShellSurfaceProps>;
  captureVideoSnapshot: (fence: PlaybackFence, options?: {signal?: AbortSignal}) => Promise<NativeShellVideoSnapshot>;
  toggle: (fence: PlaybackFence) => Promise<void>;
  replay: (fence: PlaybackFence) => Promise<void>;
  /** Retry the exact failed queue output at its saved target; the core fences the output it replaced. */
  retry?: (fence: PlaybackFence, lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => Promise<void>;
  /** Edit only loaded upcoming rows under the drawn output and revision. */
  editQueue?: (fence: PlaybackFence, request: Readonly<{queueId: string; expectedRevision: string; intent: V1QueueEditIntent}>,
    lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => Promise<void>;
  seek: (fence: PlaybackFence, seconds: number) => Promise<void>;
  skip: (fence: PlaybackFence, seconds: number) => Promise<void>;
  previous: (fence: PlaybackFence) => Promise<void>;
  next: (fence: PlaybackFence) => Promise<void>;
  /** Play one queue entry by identity. The existing queue owner performs it; the
   * revision is the one the drawn row was read from, so a changed queue refuses. */
  queuePlay?: (queueId: string, entryId: string, expectedRevision: string) => Promise<void>;
  /** Channels only: return to the live edge of the retained window. */
  goLive?: (fence: PlaybackFence) => Promise<void>;
  /** Original-source audiobook facts from the existing platform runtime. */
  listeningRead?: NativePlaybackAdapter['listeningRead'];
  /** Session-scoped rate choice, fenced by the platform runtime. */
  setRate: NativePlaybackAdapter['setRate'];
  /** Sleep timer for this playback: minutes, or null to clear. */
  setSleepTimer?: (fence: PlaybackFence, minutes: number | null) => Promise<void>;
  /** The remote dock (Portico play-on handoff or a cast session). */
  beginRemote?: (value: RemoteDock) => Promise<void>;
  updateRemote?: (value: RemoteDock) => Promise<void>;
  dismissRemote?: (id: string) => Promise<void>;
  continueHere?: (id: string) => Promise<void>;
  cast?: (fence: PlaybackFence, anchorRef?: RefObject<View | null>) => Promise<void>;
  stop: (fence: PlaybackFence) => Promise<void>;
  open: (fence: PlaybackFence) => void;
}
