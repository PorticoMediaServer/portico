import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {AuthCodeBoxes, AuthError, AuthLinks, AuthStage} from './AuthStage';
import {Button, TextField} from '../controls';
import styles from './accounts.module.css';

const t = defaultI18n.t;
/** Auth6's six real code boxes and account7's drawn recovery alternative.
 * Both submit to the original account-entry challenge, never another sign-in. */
export function ServerChallenge({busy, error, onCode, onBack}: {busy?: boolean; error?: unknown; onCode: (code: string) => void; onBack: () => void}) {
  const [code, setCode] = useState(''), [recovery, setRecovery] = useState(false);
  return <AuthStage legal={false}><h1>{t(recovery ? 'services.signin.recovery' : 'web.direct.codeTitle')}</h1>
    <p className={styles.lede}>{t(recovery ? 'services.signin.recoveryBody' : 'services.signin.twoStep')}</p><AuthError error={error} />
    <form className={styles.form} onSubmit={event => {event.preventDefault(); if (!busy && (recovery ? Boolean(code.trim()) : /^\d{6}$/.test(code))) {onCode(code); setCode('');}}}>
      {recovery ? <TextField variant="auth" label={t('accounts.signIn.recoveryCode')} placeholder={t('services.recovery.codePlaceholder')} autoComplete="one-time-code" value={code} onChange={event => setCode(event.currentTarget.value)} disabled={busy} autoFocus />
        : <AuthCodeBoxes length={6} grouped value={code} onChange={setCode} onComplete={setCode} label={t('web.twoStep.code')} disabled={busy} autoFocus />}
      {!recovery && <p className={styles.challengeHelp}>{t('services.signin.lostPhone')} <button type="button" disabled={busy} onClick={() => {setCode(''); setRecovery(true);}}>{t('services.signin.recovery')}</button></p>}
      <Button className={styles.submit} variant="primary" type="submit" loading={busy} disabled={recovery ? !code.trim() : !/^\d{6}$/.test(code)}>{t('action.continue')}</Button>
    </form>
    <AuthLinks>{recovery ? <button type="button" disabled={busy} onClick={() => {setCode(''); setRecovery(false);}}>{t('services.signin.authenticator')}</button>
      : <button type="button" disabled={busy} onClick={() => {setCode(''); onBack();}}>{t('auth.backToSignIn')}</button>}</AuthLinks>
  </AuthStage>;
}
