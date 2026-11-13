import {useRef, useState} from 'react';
import {Artwork} from './Artwork';
import type {ArtworkProps} from './types';
import styles from './artwork.module.css';

/** A cover drawn whole inside its bound, the parent box: the picture keeps its own shape and is
 * never cropped, and what is behind the bound shows around it, with no letterbox bars (owner's
 * rule: a cover is never cropped). Until the picture is in, and for the fallback, it fills the
 * bound. A narrower cover sits where `--whole-align` puts it (centred unless the page says). */
export function WholeArtwork({onReady, style, ...props}: ArtworkProps) {
  const bound = useRef<HTMLSpanElement>(null);
  const [found, setFound] = useState<Readonly<{key: string; aspect: number}>>();
  const aspect = found && found.key === props.source?.key ? found.aspect : undefined;
  return <span ref={bound} className={styles.whole}>
    <Artwork {...props} style={aspect ? {...style, width: `${Math.min(1, aspect) * 100}%`, height: 'auto', aspectRatio: String(aspect)} : style}
      onReady={source => {
        const image = bound.current?.querySelector('img');
        if (image && image.naturalWidth > 0 && image.naturalHeight > 0) setFound({key: source.key, aspect: image.naturalWidth / image.naturalHeight});
        onReady?.(source);
      }}/>
  </span>;
}
