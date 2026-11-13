import {useCallback, useRef, useState, type RefObject} from 'react';
import {Artwork} from './Artwork';
import type {ArtworkProps, ArtworkSource} from './types';
import styles from './artwork.module.css';

/** A cover's own shape (width over height), read from its picture once it is in; undefined until
 * then, and while a fallback is drawn. `bound` is the box the picture is drawn in. */
export function useCoverAspect(source: ArtworkSource | null | undefined, bound: RefObject<HTMLElement | null>) {
  const [found, setFound] = useState<Readonly<{key: string; aspect: number}>>();
  const onReady = useCallback((ready: ArtworkSource) => {
    const image = bound.current?.querySelector('img');
    if (image && image.naturalWidth > 0 && image.naturalHeight > 0) setFound({key: ready.key, aspect: image.naturalWidth / image.naturalHeight});
  }, [bound]);
  return {aspect: found && found.key === source?.key ? found.aspect : undefined, onReady};
}

/** A cover drawn whole inside its bound, the parent box: the picture keeps its own shape and is
 * never cropped, and what is behind the bound shows around it, with no letterbox bars (owner's
 * rule: a cover is never cropped). Until the picture is in, and for the fallback, it fills the
 * bound. `slot` is the bound's shape (square unless the page says). A narrower cover sits where
 * `--whole-align` puts it, a shorter one where `--whole-valign` does (both centred by default). */
export function WholeArtwork({onReady, style, slot = 1, ...props}: ArtworkProps & Readonly<{slot?: number}>) {
  const bound = useRef<HTMLSpanElement>(null);
  const cover = useCoverAspect(props.source, bound), aspect = cover.aspect;
  return <span ref={bound} className={styles.whole}>
    <Artwork {...props} style={aspect ? {...style, width: `${Math.min(1, aspect / slot) * 100}%`, height: 'auto', aspectRatio: String(aspect)} : style}
      onReady={source => {cover.onReady(source); onReady?.(source);}}/>
  </span>;
}
