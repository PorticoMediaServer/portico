import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../controls/Button';
import {TextField} from '../controls/Fields';
import {AuthError, AuthLinks, AuthPasswordField, AuthStage} from './AuthStage';
import {Icon} from '../Icon';
import {signInRetryClock, signInRetrySeconds} from './sign-in-retry';
import styles from './accounts.module.css';

const t = defaultI18n.t;
export type LocalServerSignInProps = Readonly<{
  serverName: string; initialUsername?: string; remembered?: readonly Readonly<{accountId: string; username: string; displayName?: string}>[]; busy?: boolean; error?: unknown; retryAt?: number;
  onSignIn: (username: string, password: string) => void; onForgotPassword?: () => void; onJoin?: () => void; onMoveCode?: () => void;
}>;
export function LocalServerSignIn({serverName, initialUsername = '', remembered = [], busy, error, retryAt, onSignIn, onForgotPassword, onJoin, onMoveCode}: LocalServerSignInProps) {
  const [username, setUsername] = useState(initialUsername), [password, setPassword] = useState('');
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (retryAt === undefined) return;
    const tick = () => setNow(Date.now());
    tick();
    const timer = setInterval(() => {tick(); if (signInRetrySeconds(retryAt) === 0) clearInterval(timer);}, 1000);
    window.addEventListener('focus', tick); document.addEventListener('visibilitychange', tick);
    return () => {clearInterval(timer); window.removeEventListener('focus', tick); document.removeEventListener('visibilitychange', tick);};
  }, [retryAt]);
  const seconds = signInRetrySeconds(retryAt, now), waiting = seconds > 0;
  return <AuthStage phoneShape="top">
    <p className={styles.eyebrow}>{t('accounts.signIn.to')}</p><h1>{serverName}</h1><p className={styles.lede}>{t('accounts.signIn.localBody')}</p>
    {remembered.length > 1 && <div className={styles.remembered}>{remembered.map(account => <button key={account.accountId} type="button" className={styles.accountChip}
      data-selected={username === account.username || undefined} disabled={busy} onClick={() => {setUsername(account.username); setPassword('');}}>
      <span>{Array.from(account.displayName || account.username)[0]}</span>{account.displayName || account.username}
    </button>)}</div>}
    {retryAt === undefined ? <AuthError error={error} /> : waiting && <p className={`${styles.error} ${styles.wait}`} role="alert"><Icon name="i-clock" size={20} /><span><b>{t('accounts.signIn.rateLimitedTitle')}</b> {t('accounts.signIn.rateLimitedBody')}</span></p>}
    <form className={styles.form} onSubmit={event => {event.preventDefault(); if (username && password && !busy && signInRetrySeconds(retryAt) === 0) onSignIn(username, password);}}>
      <TextField variant="auth" label={t('accounts.signIn.username')} name="username" autoComplete="username" data-filled={remembered.some(account => account.username === username) || undefined} value={username} onChange={event => setUsername(event.currentTarget.value)} disabled={busy} required />
      <AuthPasswordField label={t('accounts.signIn.password')} name="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} aria-invalid={waiting || undefined} required />
      <Button className={styles.submit} variant="primary" type="submit" loading={busy} disabled={!username || !password || waiting} data-retry-waiting={waiting || undefined}>
        <span className={styles.signInLabel} data-waiting={waiting || undefined}><span aria-hidden={!waiting}>{t('services.signin.tryIn', {time: signInRetryClock(seconds, defaultI18n.locale)})}</span><span aria-hidden={waiting}>{t('accounts.signIn.submit')}</span></span>
      </Button>
    </form>
    <AuthLinks><button type="button" disabled={!onForgotPassword || busy} onClick={onForgotPassword}>{t('auth.forgotPassword')}</button><button type="button" data-quiet disabled={!onJoin || busy} onClick={onJoin}>{t('web.directJoin.have')}</button>
      {onMoveCode && <button type="button" data-quiet disabled={busy} onClick={onMoveCode}>{t('accounts.move.haveCode')}</button>}
    </AuthLinks>
  </AuthStage>;
}
/** `moveDays`: whole days left to use the old sign-in during a move to Portico Accounts (0 is
 * today). Absent means no move is on, and nothing about the old sign-in is shown. */
export function PorticoServerSignIn({serverName, busy, error, moveDays, onContinue, onRecovery, onOldPassword}: {
  serverName: string; busy?: boolean; error?: unknown; moveDays?: number; onContinue: () => void; onRecovery?: () => void; onOldPassword?: () => void;
}) {
  const moving = moveDays !== undefined;
  return <AuthStage phoneShape="top"><p className={styles.eyebrow}>{t('accounts.signIn.to')}</p><h1>{serverName}</h1>
    <p className={styles.lede}>{t('accounts.signIn.handoffBody')}</p><AuthError error={error} />
    <Button className={styles.handoffSubmit} variant="primary" loading={busy} onClick={onContinue}>{t('accounts.signIn.handoff')}</Button>
    {moving && <p className={styles.lede}>{t('accounts.move.toPortico', {server: serverName})}</p>}
    <AuthLinks><button type="button" data-quiet disabled={!onRecovery || busy} onClick={onRecovery}>{t('accounts.signIn.cannotUsePortico')}</button>
      {moving && <button type="button" data-quiet disabled={!onOldPassword || busy} onClick={onOldPassword}>{t('accounts.move.oldPasswordAction', {days: moveDays})}</button>}
    </AuthLinks>
  </AuthStage>;
}
