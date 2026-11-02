import {useLayoutEffect, useRef, type CSSProperties} from 'react';
import {measureCoverTitleLines} from './cover-fit';
import {artClass, artSize} from './Artwork';
import type {ArtworkPalette, ArtStyleProps, GeneratedCoverTint} from './types';
import styles from './artwork.module.css';

export type GeneratedArtProps = ArtStyleProps & Readonly<{glyph: string; palette?: ArtworkPalette}>;
/** Missing-title art only. ColourBackdrop is used for auth, state and Server bands. */
export function GeneratedArt({glyph, palette, className, style, width, height}: GeneratedArtProps) {
  return <span aria-hidden="true" className={artClass(styles.generatedArt, className)} style={artSize({width, height, style: {...(palette ? {'--art-first': palette.first, '--art-second': palette.second} : {}), ...style} as CSSProperties})}>
    <span className={styles.generatedGlyph}>{glyph}</span>
  </span>;
}

export type GeneratedCoverProps = ArtStyleProps & Readonly<{
  title: string; author?: string; tint: GeneratedCoverTint; glyph: string;
  /** Authored CSS cqw percentage; defaults to 12.6 without title-based inference. */
  fontScale?: number;
  /** Compact list/selection previews may use the source frame's single-line title. */
  titleClassName?: string;
  /** Reserve the visible phone card selection circle above generated copy. */
  selectionClearance?: boolean;
}>;
export function GeneratedCover({title, author, tint, glyph, fontScale = 12.6, titleClassName, selectionClearance = false, className, style, width, height}: GeneratedCoverProps) {
  // The title is clamped to the whole lines this cover's own frame holds (a square audiobook cover
  // with an author holds fewer than a poster), re-measured whenever the frame changes size.
  const copy = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => {
    const box = copy.current, cover = box?.parentElement, view = box?.ownerDocument.defaultView;
    if (!box || !cover || !view) return;
    const fit = () => {cover.style.setProperty('--cover-lines', String(measureCoverTitleLines(box)));};
    fit();
    if (!view.ResizeObserver) return;
    const observer = new view.ResizeObserver(fit);
    observer.observe(cover);
    return () => observer.disconnect();
  }, [title, author, fontScale, titleClassName, selectionClearance]);
  return <span role="img" aria-label={title} data-generated-cover="" className={artClass(styles.generatedCover, selectionClearance && styles.selectionClearance, className)} style={artSize({width, height, style: {'--art-first': tint.first, '--art-second': tint.second, '--cover-font': `${fontScale}cqw`, ...style} as CSSProperties})}>
    <span className={styles.coverGlyph} aria-hidden="true">{glyph}</span>
    <span ref={copy} className={styles.coverCopy} data-cover-copy="" aria-hidden="true"><b className={titleClassName}>{title}</b>{author && <small>{author}</small>}</span>
  </span>;
}
