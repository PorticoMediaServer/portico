import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {WebPlaybackCapacityWait} from '../../platform/playback';
import {PlaybackFailure} from '../../kit/states/PlaybackFailure';
import styles from './capacity-wait.module.css';

const t = defaultI18n.t;

/** states.html f9's busy-converting card, over the server's own directed wait on the original
 * start. The wait object that was rendered is the only authority: Cancel asks exactly that wait
 * to cancel and nothing here reads the latest runtime, starts another play or invents a retry. */
export function WatchCapacityWait({wait}: {wait: WebPlaybackCapacityWait}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  if (!wait.isCurrent()) return null;
  const seconds = Math.max(0, Math.ceil((wait.retryAt - now) / 1000));
  return <div className={styles.cover}>
    <PlaybackFailure error={undefined} onRetry={async () => { if (wait.isCurrent()) wait.retryNow(); }} onError={() => undefined}
      retryLabel={t('web.player.error.tryAgainNow')}
      onClose={() => { if (wait.isCurrent()) wait.cancel(); }} closeLabel={t('next.states.back')}
      heading={t('web.player.error.conversionBusyTitle')} sentence={t('web.player.error.conversionBusy')}>
      {seconds > 0 && <p className={styles.alive} role="status">
        <svg className={styles.ring} width="20" height="20" viewBox="0 0 24 24" aria-hidden="true">
          <circle className={styles.track} cx="12" cy="12" r="9" /><circle className={styles.arc} cx="12" cy="12" r="9" />
        </svg>
        {t('savedServer.retryIn', {seconds})}
      </p>}
    </PlaybackFailure>
  </div>;
}
