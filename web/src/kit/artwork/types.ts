import type {CSSProperties, ReactNode} from 'react';
import type {ArtworkScheduler, ArtworkRequestOptions} from '@core/artwork/index.ts';
import type {AmbientArtworkSource} from '@core/artwork/ambient.ts';

export type ArtworkImage = Readonly<{src: string; width?: number; height?: number; bytes?: number; retain?: () => () => void}>;
export type ArtworkSource = Readonly<{scheduler: ArtworkScheduler<ArtworkImage>; key: string; request?: Omit<ArtworkRequestOptions, 'signal'>; retain?: () => () => void;
  /** Factory-owned lifetime: equal keys in this scope have the same retention semantics. */
  retainScope?: object;
}>;
export type AmbientArtSource = ArtworkSource & Pick<AmbientArtworkSource, 'variant' | 'maxEdge' | 'version'>;
export type ArtworkStatus = 'empty' | 'loading' | 'ready' | 'error';
export type ArtworkPalette = Readonly<{first: string; second: string}>;
export type {GeneratedCoverTint} from '@design';
export type ArtStyleProps = Readonly<{className?: string; style?: CSSProperties; width?: number | string; height?: number | string}>;
export type ArtworkProps = ArtStyleProps & Readonly<{
  source?: ArtworkSource | null; alt: string; fit?: 'cover' | 'contain'; position?: string;
  zoom?: number; saturation?: number; brightness?: number; visible?: boolean;
  arrivalDurationMs?: number;
  fallback?: ReactNode; onStatusChange?: (status: ArtworkStatus) => void;
  onReady?: (source: ArtworkSource) => void;
  onError?: (source: ArtworkSource) => void;
}>;
