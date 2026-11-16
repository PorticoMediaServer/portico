import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import {playbackWaitingDelayMs} from '@core/presentation/playback-waiting.ts';
import styles from './playback-waiting.module.css';
/** states.html f23: the drawn ring and its one plain line, once it has waited three seconds.
 * It observes a real media recovery and owns no retry, clock or transport. While the server is
 * converting the stream (`preparing`) the line is "Getting ready" (definition 07, rule 3). */
export function PlaybackWaiting({waiting, preparing = false}: {waiting: boolean; preparing?: boolean}) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    setShown(false);
    if (!waiting) return;
    const timer = setTimeout(() => setShown(true), playbackWaitingDelayMs);
    return () => clearTimeout(timer);
  }, [waiting]);
  if (!waiting || !shown) return null;
  return <div className={styles.waiting} role="status">
    <svg className={styles.ring} width="64" height="64" viewBox="0 0 24 24" aria-hidden="true">
      <circle className={styles.track} cx="12" cy="12" r="9" />
      <circle className={styles.arc} cx="12" cy="12" r="9" />
    </svg>
    <p>{defaultI18n.t(preparing ? 'player.gettingReady' : 'playback.message.network_stall')}</p>
  </div>;
}

export {playbackWaiting} from '@core/presentation/playback-waiting.ts';
