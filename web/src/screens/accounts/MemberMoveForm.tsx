import {useId, useState} from 'react';
import {defaultI18n} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import {ApiError} from '@core/index.ts';
import {AuthError, AuthLinks, AuthPasswordField, AuthStage} from '../../kit/accounts';
import {ServicesStrength} from '../../kit/accounts/ServicesAuth';
import {Button, TextField} from '../../kit/controls';
import styles from '../../kit/accounts/accounts.module.css';
import moveStyles from './member-move.module.css';

const t = defaultI18n.t;
type Props = Readonly<{
  mode: 'old-password' | 'code' | 'local' | 'portico'; serverName: string; initialUsername?: string;
  busy: boolean; error?: unknown; onBack: () => void;
  onOldPassword: (username: string, password: string) => void;
  onCode: (code: string) => void; onLocal: (username: string, password: string) => void; onPortico: () => void;
}>;

/** Connect43/46 inputs only. The inspected entry owns every proof and admission. */
export function MemberMoveForm({mode, serverName, initialUsername = '', busy, error, onBack, onOldPassword, onCode, onLocal, onPortico}: Props) {
  const strengthId = useId();
  const [username, setUsername] = useState(initialUsername), [password, setPassword] = useState(''), [confirm, setConfirm] = useState(''), [code, setCode] = useState('');
  const local = mode === 'local', old = mode === 'old-password';
  const title = t(mode === 'portico' ? 'accounts.move.toPortico' : mode === 'code' ? 'accounts.move.enterCode' : local ? 'accounts.move.chooseSignIn' : 'accounts.move.oldSignIn', {server: serverName});
  const body = t(mode === 'portico' ? 'accounts.move.linkBody' : mode === 'code' ? 'accounts.move.codeHelp' : local ? 'accounts.move.profilesKept' : 'accounts.move.oldSignInBody', {server: serverName});
  const ready = mode === 'code' ? !!code.trim() : !!username.trim() && !!password && (!local || password === confirm && passwordStrength(password).acceptable);
  return <AuthStage className={moveStyles.stage}><h1>{title}</h1><p className={styles.lede}>{body}</p>
    {mode === 'code' && error instanceof ApiError && error.code === 'move_code_invalid'
      ? <p role="alert">{t('accounts.move.codeInvalid')}</p> : <AuthError error={error}/>}
    {mode === 'portico' ? <Button variant="primary" loading={busy} onClick={onPortico}>{t('accounts.signIn.handoff')}</Button>
      : <form className={`${styles.form} ${moveStyles.form}`} onSubmit={event => {
        event.preventDefault(); if (busy || !ready) return;
        if (mode === 'code') {const value = code; setCode(''); onCode(value);}
        else {const value = password; setPassword(''); setConfirm(''); if (old) onOldPassword(username, value); else onLocal(username, value);}
      }}>
        {mode === 'code' ? <TextField variant="auth" containerClassName={moveStyles.code} label={t('accounts.move.code')} name="move-code" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} value={code} onChange={event => setCode(event.currentTarget.value)} disabled={busy} autoFocus required/>
          : <><TextField variant="auth" label={t('accounts.signIn.username')} name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} value={username} onChange={event => setUsername(event.currentTarget.value)} disabled={busy} required/>
            <AuthPasswordField label={t('accounts.signIn.password')} name="password" autoComplete={local ? 'new-password' : 'current-password'} value={password} aria-describedby={local && password ? strengthId : undefined} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} required/>
            {local && <><ServicesStrength password={password} id={strengthId}/><AuthPasswordField label={t('web.reset.confirm')} name="confirm-password" autoComplete="new-password" value={confirm} error={confirm && password !== confirm ? t('accounts.signIn.passwordsDiffer') : undefined} onChange={event => setConfirm(event.currentTarget.value)} disabled={busy} required/></>}</>}
        <Button variant="primary" type="submit" loading={busy} disabled={!ready}>{t('action.continue')}</Button>
      </form>}
    {old && <p className={styles.lede}>{t('accounts.move.oldPasswordHelp')}</p>}
    <AuthLinks><button type="button" disabled={busy} onClick={onBack}>{t('action.back')}</button></AuthLinks>
  </AuthStage>;
}
