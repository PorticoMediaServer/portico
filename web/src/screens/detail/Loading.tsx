import {useEffect, useState} from 'react';
import {Skeleton, TitleSkeleton} from '../../kit/states';
import {Banner} from '../../kit/overlays';
import {useShellWidth} from '../../shell/viewport';
import {t} from './text';
import styles from './detail.module.css';

/** Timers change loading phases only; no looping animation or invented progress. */
export function useLoadingClock() {
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    const started = performance.now(), timers = [300, 8000].map(at => setTimeout(() => setElapsed(performance.now() - started), at));
    return () => timers.forEach(clearTimeout);
  }, []);
  return elapsed;
}
export function DetailLoading({shape = 'title'}: {shape?: 'title' | 'panel' | 'episode'}) {
  const elapsed = useLoadingClock(), width = useShellWidth();
  if (shape === 'episode') return <div className={styles.episodeLoading} role="status" aria-label={t('next.states.loading')}>{elapsed >= 300 && <><Skeleton height={248}/><div><Skeleton width="75%" height={44}/><Skeleton height={20}/><Skeleton height={20}/><Skeleton height={54} width={160}/></div></>}{elapsed >= 8000 && <Banner variant="quiet">{t('next.states.stillLoading')}</Banner>}</div>;
  if (shape === 'panel') return <div className={styles.panelLoading} role="status" aria-label={t('next.states.loading')}>{elapsed >= 300 && <><Skeleton height={120}/><Skeleton height={20}/><Skeleton height={20} width="75%"/></>}{elapsed >= 8000 && <Banner variant="quiet">{t('next.states.stillLoading')}</Banner>}</div>;
  return <><TitleSkeleton elapsedMs={elapsed} window={{width, height: window.innerHeight, scrollTop: window.scrollY}} pressed={{title: elapsed >= 300 ? <Skeleton width="75%" height={width >= 1024 ? 66 : 44}/> : null}} />{elapsed >= 8000 && <Banner variant="quiet">{t('next.states.stillLoading')}</Banner>}</>;
}
