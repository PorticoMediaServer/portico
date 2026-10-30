import type {CSSProperties} from 'react';
import {Artwork, artClass, artSize} from './Artwork';
import type {AmbientArtSource, ArtworkPalette, ArtworkSource, ArtStyleProps, ArtworkStatus} from './types';
import styles from './artwork.module.css';

/** The one standard backdrop (bootstrap.css), drawn over the whole window. `shape`, `fill` and
 * `viewport` are retired: every shape draws the same backdrop, which picks the phone band or the
 * full glows from the window width alone. They stay accepted until their remaining callers drop them. */
export type ColourBackdropProps = Readonly<{className?: string; /** @deprecated */ shape?: 'side' | 'round' | 'top' | 'workspace'; /** @deprecated */ fill?: boolean; /** @deprecated */ viewport?: boolean}>;
export function ColourBackdrop({className}: ColourBackdropProps) {
  return <div aria-hidden="true" className={artClass('portico-backdrop', styles.colour, className)} />;
}

/** Bounded card washes contain no sharp art, scrims, controls or readiness overrides. */
export type AmbientArtTreatment = 'hero' | 'title' | 'phone' | 'cover' | 'wash' | 'library' | 'library-phone' | 'artist' | 'poster'
  | 'resume' | 'resume-phone' | 'library-row' | 'collection-tile' | 'session-audio';

export type AmbientArtProps = ArtStyleProps & Readonly<{
  source?: AmbientArtSource | null; sources?: readonly AmbientArtSource[];
  foreground?: ArtworkSource | null; palette?: ArtworkPalette;
  treatment?: AmbientArtTreatment;
  visible?: boolean; arrivalDurationMs?: number; position?: string;
  /** Static dimensions and position of the fixed masked foreground frame. */
  foregroundFrameStyle?: CSSProperties;
  /** Sharp-only transform; does not move the mask, frame or ambient wash. */
  foregroundStyle?: CSSProperties;
  onReady?: (source: ArtworkSource) => void; onError?: (source: ArtworkSource) => void; onStatusChange?: (status: ArtworkStatus) => void;
}>;

/** One continuous masked surface; sharp art and tiny preblur remain independent requests. */
export function AmbientArt({source, sources, foreground, palette, treatment = 'hero', visible = true, arrivalDurationMs, foregroundFrameStyle, foregroundStyle, position, onReady, onError, onStatusChange, className, style, width, height}: AmbientArtProps) {
  const ambient = (sources ?? (source ? [source] : [])).slice(0, 3).filter(item => item.variant === 'ambient-v1' && item.maxEdge === 64 && /^[a-f0-9]{64}$/.test(item.version));
  const colourStyle = palette ? {'--art-first': palette.first, '--art-second': palette.second} as CSSProperties : undefined;
  return <div aria-hidden="true" className={artClass(styles.ambient, styles[treatment], className)} style={artSize({width, height, style: {...colourStyle, ...style}})}>
    <div className={styles.washLayer}>
      {palette && <span className={styles.palette} />}
      {ambient.map((item, index) => <Artwork key={item.key} source={item} visible={visible} arrivalDurationMs={arrivalDurationMs} alt="" className={styles.ambientImage} style={{'--art-order': index} as CSSProperties} onStatusChange={!foreground && index === 0 ? onStatusChange : undefined} onReady={!foreground && index === 0 ? onReady : undefined} onError={!foreground && index === 0 ? onError : undefined} />)}
    </div>
    {foreground && <span className={styles.foreground} style={foregroundFrameStyle}><span data-foreground-art className={styles.foregroundArt} style={foregroundStyle}><Artwork source={foreground} visible={visible} arrivalDurationMs={arrivalDurationMs} alt="" position={position} onReady={onReady} onError={onError} onStatusChange={onStatusChange} /></span></span>}
  </div>;
}
