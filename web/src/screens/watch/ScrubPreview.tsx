import {useState} from 'react';
import {Artwork} from '../../kit/artwork/Artwork';
import type {ArtworkSource} from '../../kit/artwork/types';
import type {TrickplayFrame} from '@core/trickplay.ts';
import {watchClock} from './model';
import {scrubImageStyle} from './preview';
import styles from './watch.module.css';

export function ScrubPreview({frame, source, seconds, percent, phone}: {
  frame: TrickplayFrame; source: ArtworkSource; seconds: number; percent: number; phone: boolean;
}) {
  const [loaded, setLoaded] = useState<{key: string; scheduler: ArtworkSource['scheduler']; width?: number; height?: number} | null>(null);
  const image = source.scheduler.peek(source.key);
  const sheet = image ?? (loaded?.key === source.key && loaded.scheduler === source.scheduler ? loaded : {});
  const crop = scrubImageStyle(frame, sheet, phone);
  const ready = !!crop && loaded?.key === source.key && loaded.scheduler === source.scheduler;
  const arrived = (value: ArtworkSource) => {
    const actual = value.scheduler.peek(value.key);
    setLoaded(actual ? {key: value.key, scheduler: value.scheduler, width: actual.width, height: actual.height} : null);
  };
  return <span className={styles.scrubPreview} style={{left: `clamp(var(--scrub-half), ${percent}%, calc(100% - var(--scrub-half)))`}} data-phone={phone} data-ready={ready} aria-hidden="true">
    <span className={styles.scrubFrame}>
      <Artwork source={source} alt="" className={styles.scrubSheet} style={crop ?? undefined} fit="contain" arrivalDurationMs={0}
        onReady={arrived} onError={() => setLoaded(null)} onStatusChange={status => { if (status !== 'ready') setLoaded(null); }} />
      <b>{watchClock(seconds)}</b>
    </span>
  </span>;
}
