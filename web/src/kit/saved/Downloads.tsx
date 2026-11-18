import type {CSSProperties, ReactNode} from 'react';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button} from '../controls';
import styles from './saved.module.css';

export type StorageMeterProps = Readonly<{used: string; free?: string; value: number; other?: number; note?: string}>;
/** How much room is used (tablet 11b, phone-app 12): a raised card with the figures, the bar and a line. On the web the
 * recordings line beside Recordings' views is the kit `dvr` RecordingStorage. */
export function StorageMeter({used, free, value, other, note}: StorageMeterProps) {
  return <div className={styles.storage} style={{'--used': value, '--other': other ?? 0} as CSSProperties}>
    <span><b>{used}</b>{free && <> {free}</>}</span><span className={styles.meter} aria-hidden="true"><i/><i/></span>{note && <small>{note}</small>}
  </div>;
}

export type DownloadRowProps = Readonly<{
  title: string; detail?: string; artwork?: ArtworkSource;
  status?: Readonly<{text: string; tone?: 'quiet' | 'warning' | 'error' | 'ready'}>; progress?: number;
  action?: ReactNode;
  /** A failed one's Try again, under where it is (tablet 11b). */
  retry?: Readonly<{label: string; onPress: () => void}>;
  more?: Readonly<{label: string; onPress: () => void}>; onPress?: () => void;
}>;
/** One download (tablet 11b, collections-34, collections-36): the poster, the title and its facts, where the download is,
 * its bar, and the action that fits. The live Downloads page (`screens/saved/Downloads.tsx`) moves onto it in Stage 2. */
export function DownloadRow({title, detail, artwork, status, progress, action, retry, more, onPress}: DownloadRowProps) {
  return <div className={styles.download}><div className={styles.downloadMain}>
    <button type="button" className={styles.downloadOpen} onClick={onPress}>
      <span className={styles.downloadArt}>{artwork && <Artwork source={artwork} alt=""/>}</span>
      <span className={styles.downloadWords}><b>{title}</b>{detail && <small>{detail}</small>}{status && <span data-tone={status.tone}>{status.text}</span>}
        {progress !== undefined && <span className={styles.bar} aria-hidden="true"><i style={{width: `${Math.round(progress * 100)}%`}}/></span>}</span>
    </button>
    {retry && <Button size="small" icon="i-retry" className={styles.retry} onClick={retry.onPress}>{retry.label}</Button>}</div>
    {action}{more && <Button variant="icon" icon="i-more" iconOnly aria-label={more.label} onClick={more.onPress}/>}
  </div>;
}
