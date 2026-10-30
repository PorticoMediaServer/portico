import {useEffect, useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {playbackFailure} from '@core/presentation/playback-failure.ts';
import {Button} from '../controls';
import styles from './playback-failure.module.css';
const t = defaultI18n.t;
/** Safe existing failure vocabulary; no raw engine diagnostics or transport ownership. */
export function PlaybackFailure({error, onRetry, onVersions, onReport, onClose, onError, closeLabel, children, heading, sentence, retryLabel}: Readonly<{
  error?: string; onRetry?: () => Promise<void>; onVersions?: () => void; onReport?: () => void; onClose: () => void; onError: (error: unknown) => void;
  /** The drawn owner variant names the setting this card closes to; absent keeps "Close". */ closeLabel?: string;
  /** The drawn action's own words, e.g. the busy-converting card's Try again now; absent keeps Try again. */ retryLabel?: string;
  /** The drawn state's own body content, e.g. the account's other playbacks; no layout changes. */ children?: ReactNode;
  /** A own heading/sentence for a card that is not the mapped playback failure, e.g. the
   * server's directed conversion wait; absent keeps the existing vocabulary. */
  heading?: ReactNode; sentence?: ReactNode;
}>) {
  const failure = playbackFailure(error), [busy, setBusy] = useState(false);
  const running = useRef(false), alive = useRef(true);
  useEffect(() => {alive.current = true;return () => {alive.current = false;};}, []);
  const retry = async () => {if (running.current || !onRetry) return;running.current = true;setBusy(true);try {await onRetry();} catch (error) {if (alive.current) onError(error);} finally {running.current = false;if (alive.current) setBusy(false);}};
  return <section className={styles.failure} role="alert">
    <h2>{heading ?? t(failure.title)}</h2><p>{sentence ?? (failure.body ? t(failure.body) : failure.text)}</p>
    {children}
    <div>{!failure.permanent && onRetry && <Button variant="primary" disabled={busy} loading={busy} onClick={() => {void retry();}}>{retryLabel ?? t('next.states.tryAgain')}</Button>}
      {onVersions && <Button variant="glass" onClick={onVersions}>{t('browse.field.versions')}</Button>}
      {onReport && <Button variant="glass" onClick={onReport}>{t('web.player.error.report')}</Button>}
      <Button variant="glass" onClick={onClose}>{closeLabel ?? t('action.close')}</Button></div>
  </section>;
}
