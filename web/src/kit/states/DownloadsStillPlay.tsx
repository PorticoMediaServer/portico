import type {CardCover} from '../cards/MediaCards';
import {BoundedCovers} from '../cards/MediaCards';
import {Icon} from '../Icon';
import styles from './states.module.css';

/** What still works while the server can't be reached (states 4a): "Your downloads still play" with a fan of their
 * covers and the first title. A page state places it between the retry line and its buttons. The native kit's part of
 * the same name draws the same row. */
export function DownloadsStillPlay({covers, title, detail, onOpen}: {covers: readonly CardCover[]; title: string; detail: string; onOpen: () => void}) {
  return <button type="button" className={styles.downloads} onClick={onOpen}>
    <span className={styles.downloadsFan}><BoundedCovers covers={covers} /></span>
    <span className={styles.downloadsText}><b>{title}</b><small>{detail}</small></span><Icon name="i-right" size={20} />
  </button>;
}
