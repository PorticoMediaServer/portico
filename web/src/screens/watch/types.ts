import type {ReactNode, Ref, RefObject} from 'react';
import type {TextEnvironment} from '@core/presentation/kit-model/index.ts';
import type {WebPlaybackRuntimeSnapshot, WebQueueListPort, WebShellPlaybackRuntime} from '../../platform/playback';
import type {ShellArtwork} from '../../shell/artwork';
import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {TrickplayFrame} from '@core/trickplay.ts';
import type {ArtworkSource} from '../../kit/artwork/types';
import type {LiveChromePort} from '../live/live-chrome-types';
import type {WatchEndCard} from './end-card';
import type {PlaybackLossNotice} from '../../kit/media';
import type {VideoGesturePort} from '../../kit/dock/video-gesture-port';
import type {WatchDockPanelOffer} from './dock-panel-opening';

/** Structurally matches the parent's binding. The screen never creates or mounts media. */
export type WatchBindings = Readonly<{
  runtime: WebShellPlaybackRuntime;
  artwork: ShellArtwork;
  environment: TextEnvironment;
  skips: Readonly<{back: number; forward: number}>;
  reportError: (error: unknown) => void;
}>;
export type WatchPanel = 'options' | 'subtitles-audio' | 'subtitles' | 'audio' | 'quality' | 'speed' | 'episodes' | 'queue' | 'play-on' | 'info' | 'file-info' | 'versions' | 'equalizer';
export type WatchPanelContext = Readonly<{
  panel: WatchPanel | null;
  snapshot: WebPlaybackRuntimeSnapshot;
  layout: 'desktop' | 'phone';
  /** A compact video window wider than it is tall uses the authored side sheet. */
  landscape?: boolean;
  phoneTop: number;
  /** Current parent-offered title panels and the existing guarded feedback action. */
  available?: readonly WatchPanel[];
  reportProblem?: Readonly<{label: string; action: () => void | Promise<void>}>;
  close: () => void;
  /** Navigate the parent's open panel, e.g. a phone row to its own sub-panel. */
  show: (panel: WatchPanel) => void;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
}>;
/** The parent validates/reads one set and owns authenticated tile delivery and retirement. */
export type WatchTrickplay = Readonly<{
  fence: QueuePlaybackFence;
  frameAt: (seconds: number) => Readonly<{frame: TrickplayFrame; source: ArtworkSource}> | null;
  isCurrent: () => boolean;
}>;
/** The parent composes the media owner's checked panels. A missing port is no capability. */
export type WatchSurfaces = Readonly<{
  available: readonly WatchPanel[];
  render: (context: WatchPanelContext) => ReactNode;
  playOn?: Readonly<{active: boolean; open(anchor?: HTMLButtonElement): void; close(): void}>;
  together?: ReactNode;
  prompts?: ReactNode;
  notices?: ReactNode;
  /** Frames 6e: a modal prompt that really owns the screen. Only the admitted
   * validator sets it, never raw attention; the stage and the portal stay. */
  modal?: 'still-watching';
  /** One real loss note for this playback, drawn once and only when the device's
   * "Picture and sound notices" setting allows it. */
  loss?: Readonly<{notice: PlaybackLossNotice; enabled: boolean}>;
  /** Actual delivered-quality copy from the media adapter, bound to this source. */
  quality?: Readonly<{fence: PlaybackFence; automatic: boolean; label: string; accessibleLabel: string}>;
  tracks?: Readonly<{fence: PlaybackFence; audio?: string; subtitles?: string}>;
  trickplay?: WatchTrickplay;
  /** One retained title body owns its tab, season, paging and scroll, also when hidden. */
  body?: ReactNode;
  /** Actual title/authority identity; layout and source generation never enter this key. */
  bodyIdentity?: string;
  myList?: Readonly<{selected: boolean; pending?: boolean; toggle: () => void | Promise<void>}>;
  /** states f9: the account's own other playbacks, only for the stream-limit failure. */
  streamLimit?: ReactNode;
  /** states f9: the owner's real way to turn converting back on, when this card is that failure. */
  convertingOwnerAction?: () => void;
  /** f1q: the live queue's own captured run and rows, only while this fence is current. */
  queue?: WebQueueListPort;
  /** desktop-player f4b: a real film-end card from the retained title read. */
  end?: WatchEndCard;
  more?: readonly Readonly<{id: string; label: string; action: () => void | Promise<void>}>[];
}>;
export type WatchWindowPort = Readonly<{
  pictureInPicture?: Readonly<{active: boolean; toggle: () => void | Promise<void>}>;
  fullscreen?: Readonly<{active: boolean; toggle: () => void | Promise<void>}>;
  fit?: Readonly<{value: 'contain' | 'cover'; toggle: () => void | Promise<void>}>;
}>;
export type WatchRect = Readonly<{left: number; top: number; width: number; height: number}>;
/** Geometry is supplied by Webmedia's presentation controller, never by another engine. */
export type WatchGeometry = Readonly<{
  mode: 'full' | 'portrait' | 'book' | 'tabletop';
  picture: WatchRect;
  controls: WatchRect;
  body?: WatchRect;
  revision: string | number;
}>;
export type WatchScreenProps = Readonly<{
  binding: WatchBindings;
  snapshot: WebPlaybackRuntimeSnapshot;
  expanded: boolean;
  onCollapse: () => void;
  onControlsVisibleChange?: (visible: boolean) => void;
  /** The existing panel owner publishes one offer per committed collapsed view. */
  publishDockPanels?: (offer: WatchDockPanelOffer) => () => void;
  dockPanelsCurrent?: () => boolean;
  subscribeDockPanels?: (changed: () => void) => () => void;
  /** Empty bounds target for the SAME MediaSurface the parent already owns. */
  surfaceSlotRef: Ref<HTMLDivElement>;
  videoGesture?: Readonly<{port?: VideoGesturePort; screen: RefObject<HTMLDivElement | null>; preview: boolean; active: boolean;
    admit: (gate: () => boolean) => () => void}>;
  surfaces?: WatchSurfaces;
  /** Genuine live channel facts and actions; absent for VOD or an unavailable guide. */
  live?: LiveChromePort;
  windowPort?: WatchWindowPort;
  geometry?: WatchGeometry;
}>;
