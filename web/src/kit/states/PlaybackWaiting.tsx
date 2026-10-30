import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import styles from './playback-waiting.module.css';
/** states.html f23: the drawn ring and its one plain line, once it has waited three seconds.
 * It observes a real media recovery and owns no retry, clock or transport. */
export function PlaybackWaiting({waiting}: {waiting: boolean}) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    setShown(false);
    if (!waiting) return;
    const timer = setTimeout(() => setShown(true), 3000);
    return () => clearTimeout(timer);
  }, [waiting]);
  if (!waiting || !shown) return null;
  return <div className={styles.waiting} role="status">
    <svg className={styles.ring} width="64" height="64" viewBox="0 0 24 24" aria-hidden="true">
      <circle className={styles.track} cx="12" cy="12" r="9" />
      <circle className={styles.arc} cx="12" cy="12" r="9" />
    </svg>
    <p>{defaultI18n.t('playback.message.network_stall')}</p>
  </div>;
}
