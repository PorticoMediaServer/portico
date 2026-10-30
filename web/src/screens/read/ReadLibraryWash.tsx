import {Artwork, type ArtworkSource} from '../../kit/artwork';
import styles from './wash.module.css';

/**
 * desktop-read 1b/2/2c: the library's own recent covers, blurred behind its header.
 * The sources are the server's bounded cover URLs; this draws nothing when the
 * library has no artwork.
 */
export function ReadLibraryWash({covers}: {covers: readonly ArtworkSource[]}) {
  if (!covers.length) return null;
  return <div className={styles.wash} aria-hidden="true">{covers.slice(0, 3).map((cover, index) =>
    <span key={`${cover.key}-${index}`} className={styles.frame}><Artwork source={cover} alt="" visible/></span>)}</div>;
}
