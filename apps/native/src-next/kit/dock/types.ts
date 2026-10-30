import type React from 'react';
import type {CastRemoteIdentity} from '../../../../../packages/client-core/src/cast';
import type {StyleProp, View, ViewStyle} from 'react-native';
import type {SharedValue} from 'react-native-reanimated';
import type {PanGesture} from 'react-native-gesture-handler';
import type {DockPlayback, DockState, PlaybackDock, PlaybackFence, RemoteDock} from '../../../../../packages/player-core/src/index';
import type {FactPart, PlaybackIdentityModel} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import type {ArtworkSource, AmbientArtSource, ArtworkPalette} from '../artwork';

export type DockRect = Readonly<{x: number; y: number; width: number; height: number}>;
/** A settled ancestor transform, valid only for its current presence lifetime. */
export type DockSlotAdmission = Readonly<{isCurrent: () => boolean}>;
export type DockAction = 'expand' | 'collapse' | 'close' | 'stop' | 'play' | 'pause' | 'replay' | 'seek' | 'previous' | 'next' | 'cast' | 'open-remote' | 'continue-here' | 'dismiss-remote' | 'restore-audio' | 'restore-video';
export type DockCommand = Readonly<{
  type: DockAction;
  fence: PlaybackFence | CastDockIdentity;
  remote?: Readonly<{id: string; sequence: string}>;
  positionSeconds?: number;
  /** Measured opener for the tablet Play on popover; never playback authority. */
  anchorRef?: React.RefObject<View | null>;
}>;
/** Capabilities are the adapter's current allowed controls, including queue/chapter boundaries. */
export type DockCapabilities = Readonly<{
  play: boolean; pause: boolean; stop: boolean; seek: boolean;
  previous: boolean; next: boolean; cast: boolean;
}>;
export type DockPresentation = Readonly<{
  fence: PlaybackFence;
  identity: PlaybackIdentityModel;
  album?: readonly FactPart[];
  artwork?: ArtworkSource;
  ambient?: AmbientArtSource;
  palette?: ArtworkPalette;
  durationSeconds?: number;
  /** Receiver observation only; false hides a placeholder numeric position. */
  positionKnown?: boolean;
  /** Actual seekable window; a live dock uses the rewind window, not a fabricated programme range. */
  seekWindow?: Readonly<{start: number; end: number}>;
  finished?: boolean;
  previousLabel?: string;
  nextLabel?: string;
  skipBackSeconds: number;
  skipForwardSeconds?: number;
  capabilities: DockCapabilities;
  device?: string;
}>;
export type DockOccupant = Readonly<{
  mode: 'video' | 'music' | 'audiobook' | 'cast' | 'play-on';
  playback: DockPlayback;
  remote?: RemoteDock;
  presentation: DockPresentation;
}>;
/** Presentation custody only: never accepted as a player-core/runtime fence. */
export type CastDockIdentity=Readonly<{kind:'cast-view';attachmentId:string;remote:CastRemoteIdentity}>;
export type CastDockPlayback=Readonly<{kind:'cast';fence:CastDockIdentity;positionSeconds:number;playing:boolean;expanded:false}>;
export type CastDockOccupant=Readonly<{
 mode:'cast';playback:CastDockPlayback;
 remote:Readonly<{id:string;kind:'cast';phase:RemoteDock['phase'];sequence:string;positionSeconds:number;playback:CastDockPlayback}>;
 presentation:Omit<DockPresentation,'fence'>&Readonly<{fence:CastDockIdentity}>;
}>;
export type DockRenderOccupant=DockOccupant|CastDockOccupant;
export type CastDockBinding=Readonly<{occupant:CastDockOccupant;isCurrent:(identity:CastDockIdentity)=>boolean}>;
export type DockRenderProps = {
  occupant: DockRenderOccupant;
  onAction: (command: DockCommand) => void;
  visible?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
};
export type PlayerMorphController = {
  progress: SharedValue<number>;
  velocity: SharedValue<number>;
  dragging: SharedValue<boolean>;
  releaseVelocity: SharedValue<number>;
  /** UI-only release goal, including before its guarded JS command settles. */
  presentationGoal?: SharedValue<number>;
  request: (docked: boolean, velocity?: number) => void;
  /** Shared ancestor motion for video chrome AND its retained native picture. */
  dockMotionHosted: boolean;
  dockX: SharedValue<number>;
  dockFade: SharedValue<number>;
  /** Distance-linked paint shared by dock chrome and the persistent picture. */
  dockTravelFade: SharedValue<number>;
  /** Each physical detector owns a distinct RNGH instance. */
  gesture: PanGesture;
  dockGesture: PanGesture;
  /** Distinct physical handler, same progress/velocity and original release guard. */
  chromeGesture: (enabled: boolean, current?: SharedValue<boolean>, paintCurrent?: () => boolean) => PanGesture;
};
export type DockHostProps = {
  /** Consume only an original pre-retirement captured image and its measured slot. */
  takeVideoExit?: import('./video-exit-paint').TakeDockVideoExit;
  testID?: string;
  dock: PlaybackDock;
  snapshot: DockState;
  castView?:CastDockBinding;
  presentation: Readonly<{local?: DockPresentation; remote?: DockPresentation}>;
  onAction: (command: DockCommand) => void;
  bottomInset?: number;
  /** The shell's navigation width: the dock sits at the foot of the content beside it. */
  contentInset?: number;
  visible?: boolean;
  tvExpanded?: boolean;
  onTVExpandedChange?: (expanded: boolean) => void;
  returnFocusRef?: React.RefObject<View | null>;
  onVideoSlot?: (rect: DockRect, sourceCurrent?: () => boolean) => void;
  morph?: PlayerMorphController;
  notice?: DockNoticeModel;
  onNoticeDismiss?: () => void;
  style?: StyleProp<ViewStyle>;
};
export type DockNoticeModel = Readonly<{
  id: string;
  kind: 'paused-audio' | 'stopped-video';
  fence: PlaybackFence;
  identity: PlaybackIdentityModel;
  positionSeconds: number;
}>;
