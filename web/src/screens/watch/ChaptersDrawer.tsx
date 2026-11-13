import {useLayoutEffect, useRef} from 'react';
import {defaultI18n} from '@i18n';
import type {VideoChapter} from '@core/presentation/video-chapters.ts';
import {Icon} from '../../kit/Icon';
import {watchClock} from './model';
import styles from './episodes.module.css';

const t = defaultI18n.t;

/** The Chapters drawer: a title with chapters but no episodes (INTEGRATION.md §7: the Chapters box goes; chapter ticks
 * stay on the seek bar). It is the Episodes drawer's own surface and cards: each chapter's number, title and start,
 * the one playing marked; choosing one moves playback to its start. */
export function WatchChaptersDrawer({chapters, onSeek, onClose}: {chapters: readonly VideoChapter[]; onSeek: (seconds: number) => void; onClose: () => void}) {
  const root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => { root.current?.focus({preventScroll: true}); }, []);
  return <div ref={root} tabIndex={-1} className={styles.drawer} role="dialog" aria-label={t('player.opt.chapters')} data-watch-chapters onKeyDown={event => {
    if (event.key !== 'Escape') return;
    event.preventDefault(); event.stopPropagation(); onClose();
  }}>
    <div className={styles.head}>
      <h3>{t('player.opt.chapters')}</h3>
      <span className={styles.count}>{t('next.watch.chapterCount', {count: chapters.length})}</span>
      <button type="button" className={styles.close} aria-label={t('next.overlays.close')} onClick={onClose}><Icon name="i-close" size={22} /></button>
    </div>
    <div className={styles.track}>
      {chapters.map(chapter => <button key={chapter.id} type="button" className={styles.ep} data-playing={chapter.current || undefined} aria-current={chapter.current ? 'true' : undefined}
        onClick={() => { onSeek(chapter.startSeconds); onClose(); }}>
        <span className={styles.still}>
          <span className={styles.blank} aria-hidden="true" />
          {chapter.current && <span className={styles.playing}><span className={styles.eq} aria-hidden="true"><i /><i /><i /></span>{t('next.dock.playing')}</span>}
          <span className={styles.duration}>{watchClock(chapter.startSeconds)}</span>
        </span>
        <span className={styles.title}>{chapter.title}</span>
        <span className={styles.when}><span>{t('next.watch.chapterNumber', {number: chapter.index})}</span></span>
      </button>)}
    </div>
  </div>;
}
