import type {ReactNode} from 'react';
import {createI18n, deviceRegion, resolveRegion} from '@i18n';
import {Icon, type IconName} from '../Icon';
import {Button} from '../controls';
import {ColourBackdrop} from '../artwork';
import styles from './directory-status.module.css';

const t = createI18n(resolveRegion(undefined, deviceRegion())).t;

/** connect 2: the six connection states between the directory and Home, on the
 * same state layout. Presentation only — the caller owns the phase, the retry
 * clock and every code that decides it. */
export type DirectoryStatusPhase = 'opening' | 'waiting' | 'unreachable' | 'account-waiting' | 'identity' | 'clock' | 'update';
/** The one way to reach the server's own page, worded for the case: a local address for a
 * server only this network can reach, or its direct address when this app can't open it. */
export type DirectoryFallback = Readonly<{kind: 'local' | 'direct'; onPress: () => void}>;

export function ServicesDirectoryStatus({header, phase, server, lastSeen, seconds, owner = false, onCheck, onChoose, fallback, busy = false, error}: {
  header: ReactNode;
  busy?: boolean;
  error?: ReactNode;
  phase: DirectoryStatusPhase;
  /** The server's own captured name. */
  server: string;
  /** The already formatted last-online time; absent means it was never seen. */
  lastSeen?: string;
  /** The caller's real retry countdown, in seconds. */
  seconds: number;
  owner?: boolean;
  onCheck: () => void;
  onChoose: () => void;
  /** Never followed by itself: the person presses it. */
  fallback?: DirectoryFallback;
}) {
  const opening = phase === 'opening';
  const spinning = opening;
  const icon: IconName | undefined = phase === 'waiting' || phase === 'unreachable' ? 'i-srvoff'
    : phase === 'account-waiting' ? 'i-cloudoff' : phase === 'identity' ? 'i-shield' : phase === 'clock' ? 'i-clock' : phase === 'update' ? 'i-server' : undefined;
  const title = phase === 'opening' ? t('web.chooser.connecting', {server})
    : phase === 'waiting' ? t('web.chooser.waitingTitle', {server})
      : phase === 'unreachable' ? t('web.chooser.unreachableTitle', {server})
        : phase === 'account-waiting' ? t('web.chooser.accountWaitingTitle')
          : phase === 'identity' ? t('web.chooser.unconfirmedTitle', {server})
            : phase === 'update' ? t('web.chooser.updateTitle', {server})
              : t('web.chooser.clockTitle', {server});
  const body = phase === 'opening' ? t('web.chooser.openingBody')
    : phase === 'waiting' ? t('web.chooser.waitingBody')
      : phase === 'unreachable' ? (lastSeen === undefined ? t('web.chooser.unreachableBodyNever') : t('web.chooser.unreachableBody', {date: lastSeen}))
        : phase === 'account-waiting' ? t('web.chooser.accountWaitingBody', {server})
          : phase === 'identity' ? t('web.chooser.unconfirmedBody')
            : phase === 'update' ? t(owner ? 'web.chooser.updateOwnerBody' : 'web.chooser.updateBody')
              : t('web.chooser.clockBody');
  const counting = phase === 'waiting' || phase === 'account-waiting';

  return <div className={styles.page}>
    {header}<ColourBackdrop />
    <main className={styles.main}>
      <section className={styles.state}>
        <span className={styles.icon} data-tone={opening ? 'accent' : 'warning'} aria-hidden="true">
          {spinning ? <span className={styles.spinner} /> : icon && <Icon name={icon} size={28} />}
        </span>
        <h1>{title}</h1>
        <p className={styles.body}>{body}</p>
        {counting && <div className={styles.alive}>
          <svg className={styles.ring} viewBox="0 0 24 24" aria-hidden="true"><circle className={styles.ringTrack} cx="12" cy="12" r="9" /><circle className={styles.ringArc} cx="12" cy="12" r="9" /></svg>
          {t('web.chooser.checkingAgain', {seconds})}
        </div>}
        {error}
        <div className={styles.actions}>
          {!opening && <Button variant="primary" disabled={busy} onClick={onCheck}>{t(counting ? 'web.chooser.checkNow' : 'web.chooser.tryAgain')}</Button>}
          {opening ? <button type="button" className={styles.quiet} onClick={onChoose}>{t('web.chooser.otherServer')}</button>
            : <Button onClick={onChoose}>{t('web.chooser.otherServer')}</Button>}
          {!opening && fallback && <button type="button" className={styles.quiet} disabled={busy} onClick={fallback.onPress}>{t(fallback.kind === 'local' ? 'web.chooser.openLocal' : 'web.chooser.openDirect')}</button>}
        </div>
        {!opening && fallback?.kind === 'local' && <p className={styles.help}>{t('web.chooser.localAccess')}</p>}
        {phase === 'unreachable' && owner && <p className={styles.help}>{t('web.hostedAccount.offlineOwnerHint')}</p>}
      </section>
    </main>
  </div>;
}
