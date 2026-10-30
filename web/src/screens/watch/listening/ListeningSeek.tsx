import {defaultI18n} from '@i18n';
import {watchClock} from '../model';
import {listeningSeekModel, type ListeningSeekBounds, type ListeningSeekChapter} from './listening-seek';
import styles from './listening-seek.module.css';
const t = defaultI18n.t;

/** Native keyboard/assistive input over the authored rail, fill, chapter ticks and thumb. */
export function ListeningSeek({position, duration, bounds, chapters, disabled, phone, className, onSeek}: Readonly<{
  position: number; duration: number; bounds?: ListeningSeekBounds | null; chapters?: readonly ListeningSeekChapter[];
  disabled?: boolean; phone?: boolean; className?: string; onSeek(seconds: number): void;
}>) {
  const model = listeningSeekModel(position, duration, bounds, chapters);
  return <div className={`${styles.seek}${className ? ` ${className}` : ''}`} data-phone={phone || undefined}>
    <span className={styles.elapsed}>{watchClock(model.at)}</span>
    <div className={styles.rail}>
      <div className={styles.track} aria-hidden="true">
        {model.chapter && <span className={styles.chapter} style={{left: `${model.chapter.start * 100}%`, width: `${(model.chapter.end - model.chapter.start) * 100}%`}}/>}
        <span className={styles.fill} style={{width: `${model.progress * 100}%`}}/>
        {model.ticks.map(tick => <span key={tick} className={styles.tick} style={{left: `${tick * 100}%`}}/>)}
        <span className={styles.thumb} style={{left: `${model.progress * 100}%`}}/>
      </div>
      <input type="range" min={model.start} max={model.end} step={.1} value={model.at} disabled={disabled || !model.valid}
        aria-label={t('player.position')} aria-valuetext={t('player.clockOf', {position: watchClock(model.at), duration: watchClock(duration)})}
        onChange={event => {const value = event.currentTarget.valueAsNumber;if (model.valid && !disabled && Number.isFinite(value)) onSeek(Math.max(model.start, Math.min(model.end, value)));}}/>
    </div>
    <span className={styles.remaining}>{model.remaining === undefined ? null : `−${watchClock(model.remaining)}`}</span>
  </div>;
}
