import type {CSSProperties, ReactNode} from 'react';
import {Artwork, artClass, artSize} from './Artwork';
import type {ArtworkSource, ArtStyleProps} from './types';
import styles from './artwork.module.css';

export type CircleArtProps = ArtStyleProps & Readonly<{
  covers: readonly ArtworkSource[]; initials?: string; alt: string; size?: number;
  visible?: boolean; treatment?: 'artist' | 'search' | 'tv'; coverZoom?: number; coverTransformOrigin?: CSSProperties['transformOrigin']; initialsSize?: number;
}>;
export function CircleArt({covers, initials, alt, size, treatment = 'artist', coverZoom, coverTransformOrigin, initialsSize, visible = true, className, style, width = size, height = size}: CircleArtProps) {
  const items = covers.slice(0, 4);
  return <span role={alt ? 'img' : undefined} aria-label={alt || undefined} aria-hidden={alt ? undefined : true} className={artClass(styles.circle, styles[treatment === 'search' ? 'searchCircle' : treatment === 'tv' ? 'tvCircle' : 'artistCircle'], styles[`circle${items.length}`], className)} style={artSize({width, height, style: {...(coverZoom === undefined ? {} : {'--circle-cover-zoom': coverZoom}), ...(coverTransformOrigin === undefined ? {} : {'--circle-cover-origin': coverTransformOrigin}), ...(initialsSize === undefined ? {} : {'--circle-initials-size': `${initialsSize}px`}), ...style} as CSSProperties})}>
    {items.length ? items.map((source, index) => <span key={`${source.key}:${index}`} className={styles.circleCell}><Artwork source={source} alt="" visible={visible} /></span>) : initials && <b className={styles.initials}>{initials}</b>}
  </span>;
}
export const ArtistCircle = CircleArt;

export type PersonCircleProps = ArtStyleProps & Readonly<{
  covers?: readonly ArtworkSource[]; initials: string; alt: string; size?: number; visible?: boolean; initialsSize?: number;
}>;
export function PersonCircle({covers = [], initials, alt, size, initialsSize, visible = true, className, style, width = size, height = size}: PersonCircleProps) {
  return <span role={alt ? 'img' : undefined} aria-label={alt || undefined} aria-hidden={alt ? undefined : true} className={artClass(styles.circle, styles.personCircle, covers.length > 0 && styles.personCovers, className)} style={artSize({width, height, style: {...(initialsSize === undefined ? {} : {'--circle-initials-size': `${initialsSize}px`}), ...style} as CSSProperties})}>
    {covers.length > 0 && <span className={styles.personBlend}>{covers.slice(0, 4).map((source, index) => <Artwork key={`${source.key}:${index}`} source={source} visible={visible} alt="" />)}</span>}
    <b className={styles.initials}>{initials}</b>
  </span>;
}

export type CoverMosaicProps = ArtStyleProps & Readonly<{covers: readonly ArtworkSource[]; shape?: 'poster' | 'square' | 'periodical'; alt: string; visible?: boolean; renderFallback?: (index: number) => ReactNode}>;
export function CoverMosaic({covers, shape = 'square', alt, visible = true, renderFallback, className, style, width, height}: CoverMosaicProps) {
  const items = covers.slice(0, 4);
  return <span role={alt ? 'img' : undefined} aria-label={alt || undefined} aria-hidden={alt ? undefined : true} data-cover-shape={shape} className={artClass(styles.mosaic, styles[`mosaic${items.length}`], className)} style={artSize({width, height, style})}>
    {Array.from({length: items.length || 1}, (_, index) => <Artwork key={items[index]?.key ?? index} source={items[index]} visible={visible} alt="" fallback={renderFallback?.(index)} />)}
  </span>;
}
