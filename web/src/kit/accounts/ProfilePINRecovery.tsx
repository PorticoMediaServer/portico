import {useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../overlays/Dialog';
import {Button} from '../controls/Button';
import {AuthCodeBoxes, AuthError, AuthPasswordField} from './AuthStage';
import styles from './accounts.module.css';

const t = defaultI18n.t;
/** Auth18 uses the shared modal/sheet focus and dismissal lifecycle. */
export function ProfilePINRecovery({profileName, username, phase, busy, error, onPassword, onPortico, onCode, onPIN, onClose, onOpen}: {
  profileName: string; username: string; phase: 'password' | 'portico' | 'challenge' | 'pin' | 'done'; busy?: boolean; error?: unknown;
  onPassword: (password: string) => void; onPortico: () => void; onCode: (code: string) => void; onPIN: (pin: string) => void; onClose: () => void; onOpen: (pin: string) => void;
}) {
  const [password, setPassword] = useState(''), [pin, setPIN] = useState(''), [code, setCode] = useState('');
  const passwordInput = useRef<HTMLInputElement>(null);
  const title = phase === 'done' ? t('profile.pinChanged') : phase === 'pin' ? t('profile.pinRecoveryTitle', {name: profileName}) : t('accounts.profiles.forgotTitle', {profile: profileName});
  let body: ReactNode;
  if (phase === 'done') body = <><p>{t('profile.pinSavedBody', {name: profileName})}</p><Button variant="primary" onClick={() => {onOpen(pin); setPIN('');}}>{t('accounts.profiles.open', {profile: profileName})}</Button><Button variant="ghost" onClick={onClose}>{t('action.done')}</Button></>;
  else if (phase === 'pin') body = <><AuthError error={error} /><label>{t('profile.pinChoose')}</label><AuthCodeBoxes length={4} value={pin} label={t('profile.pinChoose')} onChange={setPIN} onComplete={setPIN} autoFocus disabled={busy} />
    <Button variant="primary" disabled={pin.length !== 4} loading={busy} onClick={() => onPIN(pin)}>{t('profile.pinRecoveryAction')}</Button></>;
  else if (phase === 'challenge') body = <><AuthError error={error} /><AuthCodeBoxes length={6} value={code} label={t('profile.pinRecoveryCode')} onChange={setCode} onComplete={value => {onCode(value); setCode('');}} autoFocus disabled={busy} /></>;
  else body = <><p>{t('accounts.profiles.forgotBody')}</p><AuthError error={error} />{phase === 'password' ? <form onSubmit={event => {event.preventDefault(); if (password && !busy) {onPassword(password); setPassword('');}}}>
    <AuthPasswordField ref={passwordInput} label={t('accounts.profiles.accountPassword', {username})} autoComplete="current-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} autoFocus />
    <Button variant="primary" type="submit" disabled={!password} loading={busy}>{t('accounts.signIn.continue')}</Button></form> : <Button variant="primary" loading={busy} onClick={onPortico}>{t('accounts.signIn.handoff')}</Button>}
    <p className={styles.recoveryQuiet}>{t('accounts.profiles.porticoInstead')}</p></>;
  return <Dialog open title={title} onOpenChange={open => {if (!open) onClose();}} initialFocusRef={phase === 'password' ? passwordInput : undefined} showCloseButton={false}
    headerAction={phase === 'done' ? undefined : <Button size="small" variant="text" onClick={onClose}>{t('action.cancel')}</Button>}>
    <div className={styles.recovery}>{body}</div>
  </Dialog>;
}
