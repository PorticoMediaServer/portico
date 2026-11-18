import type {CSSProperties, ReactNode} from 'react';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button} from '../controls';
import {useFrameForm} from '../overlays/shared';
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
  /** Where the download is ("Ready to save", "Waiting for Wi-Fi") and a quieter line under it ("kept until Oct 29"). */
  status?: Readonly<{text: string; tone?: 'quiet' | 'warning' | 'error' | 'ready'; note?: string}>; progress?: number;
  /** The row's own buttons: Play, Save file, Pause and Stop preparing. */
  action?: ReactNode;
  /** A failed one's Try again, under where it is (tablet 11b). */
  retry?: Readonly<{label: string; onPress: () => void}>;
  more?: Readonly<{label: string; onPress: () => void}>; onPress?: () => void;
}>;
/** One download (tablet 11b, collections-34, collections-36): the poster, the title and its facts, where the download is,
 * its bar, and the buttons that fit. At phone width the buttons sit under the words (collections-36b); from tablet width they
 * trail the row and Try again stays under the words (tablet 11b); from 1024 where it is takes a column of its own and Try again
 * joins the buttons (collections-34). The live Downloads page (`screens/saved/Downloads.tsx`) moves onto it in Stage 2. */
export function DownloadRow({title, detail, artwork, status, progress, action, retry, more, onPress}: DownloadRowProps) {
  const form = useFrameForm(), wide = form === 'expanded';
  const where = (status || progress !== undefined) && <span className={styles.downloadStatus}>{status && <span data-tone={status.tone}>{status.text}</span>}{status?.note && <small>{status.note}</small>}
    {progress !== undefined && <span className={styles.bar} aria-hidden="true"><i style={{width: `${Math.round(progress * 100)}%`}}/></span>}</span>;
  const again = retry && <Button size="small" icon="i-retry" onClick={retry.onPress}>{retry.label}</Button>;
  const under = form === 'phone' ? <>{again}{action}</> : !wide && again;
  return <div className={styles.download} data-wide={wide || undefined}><div className={styles.downloadMain}>
    <button type="button" className={styles.downloadOpen} onClick={onPress}>
      <span className={styles.downloadArt}>{artwork && <Artwork source={artwork} alt=""/>}</span>
      <span className={styles.downloadWords}><b>{title}</b>{detail && <small>{detail}</small>}{!wide && where}</span>
    </button>
    {under && <span className={styles.downloadButtons}>{under}</span>}</div>
    {wide && where}
    <span className={styles.downloadButtons}>{wide && again}{form !== 'phone' && action}{more && <Button variant="icon" icon="i-more" iconOnly aria-label={more.label} onClick={more.onPress}/>}</span>
  </div>;
}
