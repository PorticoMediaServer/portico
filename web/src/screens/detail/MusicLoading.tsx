import {Skeleton} from '../../kit/states';
import {loadingPhase} from '../../kit/states/model';
import {Banner} from '../../kit/overlays';
import {useLoadingClock} from './Loading';
import {t} from './text';
import styles from './music-loading.module.css';

export function MusicDetailLoading({kind}: {kind: 'album' | 'artist'}) {
  return <MusicDetailSkeleton kind={kind} elapsedMs={useLoadingClock()} />;
}

/** Reserve the real music layout immediately; still shapes arrive only after 300 ms. */
export function MusicDetailSkeleton({kind, elapsedMs}: {kind: 'album' | 'artist'; elapsedMs: number}) {
  const phase = loadingPhase(elapsedMs);
  return <div className={styles.loading} data-kind={kind} role="status" aria-label={t('next.states.loading')}>
    <div className={styles.hero}>{phase !== 'waiting' && <>
      <Skeleton className={styles.art} radius={kind === 'artist' ? 999 : 12} />
      <div className={styles.copy}><Skeleton className={styles.title} width="72%" /><Skeleton width="60%" height={20} />
        <div className={styles.actions}><Skeleton width="var(--music-loading-action-width)" height={52} /><Skeleton width="var(--music-loading-action-width)" height={52} /><Skeleton width={52} height={52} /></div>
      </div>
    </>}</div>
    <div className={styles.content}>{phase !== 'waiting' && <>
      <div className={styles.tracks}>{Array.from({length: 5}, (_, index) => <Skeleton key={index} height={44} />)}</div>
      <div className={styles.details}><Skeleton width="48%" height={26} /><Skeleton height={18} /><Skeleton height={18} /><Skeleton width="75%" height={18} /></div>
    </>}</div>
    {phase === 'slow' && <Banner variant="quiet">{t('next.states.stillLoading')}</Banner>}
  </div>;
}
