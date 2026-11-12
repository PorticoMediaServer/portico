import {useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../overlays/Dialog';
import {Button} from '../controls/Button';
import {AuthCodeBoxes, AuthError, AuthPasswordField} from './AuthStage';
import styles from './accounts.module.css';

const t = defaultI18n.t;
/** On a Portico Accounts server, where the Portico step stands: the Portico Account's facts once read,
 * that this page holds none (a server's own address), or the read's failure. */
export type PorticoRecoveryView = Readonly<{phase: 'loading'} | {phase: 'ready'; account: Readonly<{username: string; hasPassword: boolean; twoStep: boolean}>} | {phase: 'elsewhere'} | {phase: 'failed'; error: unknown}>;

/** Auth18 uses the shared modal/sheet focus and dismissal lifecycle. On a Portico Accounts server the
 * password is the Portico Account's, typed here and checked by Portico Services (owner's rule, 12 Nov). */
export function ProfilePINRecovery({profileName, username, phase, busy, error, portico, onPassword, onPorticoPassword, onPorticoRetry, onPorticoElsewhere, onSetPassword, onCode, onPIN, onClose, onOpen}: {
  profileName: string; username: string; phase: 'password' | 'portico' | 'challenge' | 'pin' | 'done'; busy?: boolean; error?: unknown;
  portico?: PorticoRecoveryView;
  onPassword: (password: string) => void; onPorticoPassword: (password: string, twoStepCode: string) => void; onPorticoRetry: () => void;
  onPorticoElsewhere: () => void; onSetPassword: () => void;
  onCode: (code: string) => void; onPIN: (pin: string) => void; onClose: () => void; onOpen: (pin: string) => void;
}) {
  const [password, setPassword] = useState(''), [pin, setPIN] = useState(''), [code, setCode] = useState(''), [twoStep, setTwoStep] = useState('');
  const passwordInput = useRef<HTMLInputElement>(null);
  const title = phase === 'done' ? t('profile.pinChanged') : phase === 'pin' ? t('profile.pinRecoveryTitle', {name: profileName}) : t('accounts.profiles.forgotTitle', {profile: profileName});
  let body: ReactNode;
  if (phase === 'done') body = <><p>{t('profile.pinSavedBody', {name: profileName})}</p><Button variant="primary" onClick={() => {onOpen(pin); setPIN('');}}>{t('accounts.profiles.open', {profile: profileName})}</Button><Button variant="ghost" onClick={onClose}>{t('action.done')}</Button></>;
  else if (phase === 'pin') body = <><AuthError error={error} /><label>{t('profile.pinChoose')}</label><AuthCodeBoxes length={4} value={pin} label={t('profile.pinChoose')} onChange={setPIN} onComplete={setPIN} autoFocus disabled={busy} />
    <Button variant="primary" disabled={pin.length !== 4} loading={busy} onClick={() => onPIN(pin)}>{t('profile.pinRecoveryAction')}</Button></>;
  else if (phase === 'challenge') body = <><AuthError error={error} /><AuthCodeBoxes length={6} value={code} label={t('profile.pinRecoveryCode')} onChange={setCode} onComplete={value => {onCode(value); setCode('');}} autoFocus disabled={busy} /></>;
  else if (phase === 'password') body = <><p>{t('accounts.profiles.forgotBody')}</p><AuthError error={error} /><form onSubmit={event => {event.preventDefault(); if (password && !busy) {onPassword(password); setPassword('');}}}>
    <AuthPasswordField ref={passwordInput} label={t('accounts.profiles.accountPassword', {username})} autoComplete="current-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} autoFocus />
    <Button variant="primary" type="submit" disabled={!password} loading={busy}>{t('accounts.signIn.continue')}</Button></form></>;
  else if (!portico || portico.phase === 'loading') body = <p role="status">{t('status.loading')}</p>;
  // A server's own address holds no Portico Account: the reset is done at web.getportico.tv.
  else if (portico.phase === 'elsewhere') body = <><p>{t('accounts.profiles.porticoElsewhere')}</p>
    <Button variant="primary" onClick={onPorticoElsewhere}>{t('accounts.signIn.handoff')}</Button></>;
  else if (portico.phase === 'failed') body = <><p>{t('accounts.profiles.forgotBody')}</p><AuthError error={portico.error} />
    <Button variant="primary" onClick={onPorticoRetry}>{t('action.tryAgain')}</Button></>;
  // Services checks a typed password; an account that signs in only with Google or Apple sets one first.
  else if (!portico.account.hasPassword || refusedFor(error, 'account_password_required')) body = <><p role="alert">{t('accounts.profiles.porticoNeedsPassword')}</p>
    <Button variant="primary" onClick={onSetPassword}>{t('accounts.profiles.porticoSetPassword')}</Button></>;
  else {
    const ready = !!password && (!portico.account.twoStep || twoStep.length === 6);
    body = <><p>{t('accounts.profiles.forgotBody')}</p><AuthError error={error} /><form onSubmit={event => {
      event.preventDefault(); if (!ready || busy) return; onPorticoPassword(password, twoStep); setPassword(''); setTwoStep('');
    }}>
      <AuthPasswordField ref={passwordInput} label={t('accounts.profiles.porticoPassword', {username: portico.account.username})} autoComplete="current-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} autoFocus />
      {portico.account.twoStep && <><label>{t('accounts.profiles.porticoTwoStep')}</label><AuthCodeBoxes length={6} value={twoStep} label={t('accounts.profiles.porticoTwoStep')} onChange={setTwoStep} onComplete={setTwoStep} disabled={busy} /></>}
      <Button variant="primary" type="submit" disabled={!ready} loading={busy}>{t('accounts.signIn.continue')}</Button></form>
      <p className={styles.recoveryQuiet}>{t('accounts.profiles.porticoChecked')}</p></>;
  }
  return <Dialog open title={title} onOpenChange={open => {if (!open) onClose();}} initialFocusRef={phase === 'password' || phase === 'portico' && portico?.phase === 'ready' ? passwordInput : undefined} showCloseButton={false}
    headerAction={phase === 'done' ? undefined : <Button size="small" variant="text" onClick={onClose}>{t('action.cancel')}</Button>}>
    <div className={styles.recovery}>{body}</div>
  </Dialog>;
}

const refusedFor = (error: unknown, code: string) => !!error && typeof error === 'object' && (error as {code?: unknown}).code === code;
