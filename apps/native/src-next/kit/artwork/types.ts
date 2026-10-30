import type {ReactNode} from 'react';
import type {ImageResizeMode, ImageStyle, StyleProp, ViewStyle} from 'react-native';
import type {AnimatedStyle} from 'react-native-reanimated';
import type {AmbientArtworkSource} from '../../../../../packages/client-core/src/artwork/index';
import type {ChannelMarkModel} from '../../../../../packages/client-core/src/presentation/kit-model/index';

/** Supplied by the authenticated platform adapter. Change cacheKey at every authority fence. */
export type ArtworkSource = Readonly<{
  uri: string;
  cacheKey: string;
  headers?: Readonly<Record<string, string>>;
}>;

/** The canonical representation plus its transport-resolved URI; never a foreground substitute. */
export type AmbientArtSource = AmbientArtworkSource & Readonly<{
  uri: string;
  headers?: Readonly<Record<string, string>>;
}>;
export type ArtworkPalette = Readonly<{first: string; second: string}>;
export type GeneratedCoverTint = ArtworkPalette;
export type ArtworkStatus = 'queued' | 'loading' | 'ready' | 'error';
export type ArtworkPosition = Readonly<{x: number; y: number}>;
export type ArtworkProps = Readonly<{
  source?: ArtworkSource | null;
  alt?: string;
  width?: number;
  height?: number;
  fit?: 'cover' | 'contain';
  resizeMode?: ImageResizeMode;
  position?: ArtworkPosition;
  zoom?: number;
  saturation?: number;
  brightness?: number;
  blurRadius?: number;
  filter?: ViewStyle['filter'];
  visible?: boolean;
  priority?: boolean;
  /** Picture arrival only; reduction stays160ms and a cached image stays immediate. */
  arrivalDurationMs?: number;
  /** Ambient-v1 and already-sized profile pictures bypass sharp-art sizing. */
  sized?: boolean;
  style?: StyleProp<ViewStyle>;
  imageStyle?: StyleProp<ImageStyle>;
  imageOverlay?: ReactNode;
  fallback?: ReactNode;
  onStatusChange?: (status: ArtworkStatus) => void;
  onReady?: (source: ArtworkSource) => void;
  onLoad?: (source: ArtworkSource) => void;
  onError?: (source: ArtworkSource) => void;
  testID?: string;
}>;

export type AmbientTreatment = 'hero' | 'title' | 'phone' | 'cover' | 'wash' | 'library' | 'artist' | 'music' | 'person';
export type AmbientArtProps = Readonly<{
  source?: AmbientArtSource | null;
  sources?: readonly AmbientArtSource[];
  foreground?: ArtworkSource | null;
  palette?: ArtworkPalette;
  treatment?: AmbientTreatment;
  width?: number;
  height?: number;
  visible?: boolean;
  priority?: boolean;
  arrivalDurationMs?: number;
  position?: ArtworkPosition;
  style?: StyleProp<ViewStyle>;
  /** Fixed foreground bounds and stationary masks; transforms belong to foregroundStyle. */
  foregroundFrameStyle?: StyleProp<ViewStyle>;
  foregroundStyle?: StyleProp<AnimatedStyle<ViewStyle>>;
  /** The drawn foreground width as a fraction of the surface; absent keeps the role's own column. */
  foregroundFraction?: number;
  /** The drawn fade into ink on one edge of the foreground only; absent keeps the existing scrims. */
  foregroundFade?: 'left';
  /** Fires after the foreground decoded, or after ambient decode when there is no foreground. */
  onReady?: (source: ArtworkSource) => void;
  /** The visible palette committed with no foreground or ambient image offered; no image-decode claim. */
  onFallbackReady?: () => void;
  /** Actual foreground Image failure or scheduler exhaustion; ambient failure only without foreground. */
  onError?: (source: ArtworkSource) => void;
  onStatusChange?: (status: ArtworkStatus) => void;
  testID?: string;
}>;
export type ColourBackdropProps = Readonly<{
  shape?: 'side' | 'round' | 'top';
  fill?: boolean;
  width?: number;
  height?: number;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;
export type CircleArtProps = Readonly<{
  covers: readonly ArtworkSource[];
  initials?: string;
  alt?: string;
  size?: number;
  visible?: boolean;
  style?: StyleProp<ViewStyle>;
}>;
export type PersonCircleProps = Omit<CircleArtProps, 'covers' | 'initials'> & Readonly<{
  source?: ArtworkSource | null;
  covers?: readonly ArtworkSource[];
  ambientCovers?: readonly AmbientArtSource[];
  palette?: ArtworkPalette;
  initials: string;
}>;
export type GeneratedCoverProps = Readonly<{
  title: string;
  author?: string;
  tint: GeneratedCoverTint;
  /** Exact authored --fs in percent of cover width; CSS default12.6cqw. */
  fontScale?: number;
  glyph: string;
  width?: number;
  height?: number;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;
export type CoverShape = 'poster' | 'square' | 'periodical';
export type CoverArrangementProps = Readonly<{
  covers: readonly ArtworkSource[];
  shape: CoverShape;
  width?: number;
  height?: number;
  alt?: string;
  visible?: boolean;
  style?: StyleProp<ViewStyle>;
  /** Allows an actual generated cover in a missing-art slot; no invented sample art. */
  renderFallback?: (index: number) => ReactNode;
  variant?: 'library' | 'series' | 'music';
  spine?: boolean;
}>;
export type ChannelMarkProps = Readonly<{
  mark: ChannelMarkModel;
  source?: ArtworkSource | null;
  size?: number;
  /** Fixed logo representations bypass media resize query parameters. */
  sized?: boolean;
  alt?: string;
  visible?: boolean;
  style?: StyleProp<ViewStyle>;
  onStatusChange?: (status: ArtworkStatus) => void;
}>;
