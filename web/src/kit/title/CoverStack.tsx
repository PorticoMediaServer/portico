import {WholeArtwork, type ArtworkSource} from '../artwork';
import styles from './cover-stack.module.css';

/** A series' first volumes fanned as one picture (desktop-read 4, phone-read 4): up to three real covers, each drawn whole
 * in its own 2:3 place. It fills the box it is given; the title frame's `stack` cover sizes that box. */
export function CoverStack({covers}: Readonly<{covers: readonly ArtworkSource[]}>) {
  const shown = covers.slice(0, 3);
  return <span className={styles.stack} data-count={shown.length}>
    {shown.map((source, index) => <span key={`${source.key}:${index}`} className={styles.volume}><WholeArtwork source={source} alt="" fit="cover" slot={2 / 3} className={styles.image}/></span>)}
  </span>;
}
