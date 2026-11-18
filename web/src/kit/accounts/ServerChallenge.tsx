import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {AuthCodeBoxes, AuthError, AuthLinks, AuthStage} from './AuthStage';
import {Button, TextField} from '../controls';
import styles from './accounts.module.css';

const t = defaultI18n.t;
/** The two-step code (account-10 frames 3 and 4): six boxes, or the recovery field, with the line that swaps them.
 * One look wherever a code is asked for: the sign-in step below and the box a signed-in person meets before a guarded
 * action. `error` is a refused code ("That code is not correct."), said under the boxes or the field. */
export function TwoStepCode({value, onChange, recovery, onRecoveryChange, error, disabled, autoFocus}: {
  value: string; onChange: (value: string) => void; recovery: boolean; onRecoveryChange: (recovery: boolean) => void;
  error?: string; disabled?: boolean; autoFocus?: boolean;
}) {
  const swap = () => {onChange(''); onRecoveryChange(!recovery);};
  return <>
    {recovery ? <TextField variant="auth" label={t('accounts.signIn.recoveryCode')} placeholder={t('services.recovery.codePlaceholder')} autoComplete="one-time-code" value={value} onChange={event => onChange(event.currentTarget.value)} error={error} disabled={disabled} autoFocus={autoFocus} />
      : <AuthCodeBoxes length={6} grouped value={value} onChange={onChange} onComplete={onChange} label={t('web.twoStep.code')} error={error} disabled={disabled} autoFocus={autoFocus} />}
    <p className={styles.challengeHelp}>{!recovery && <>{t('services.signin.lostPhone')} </>}<button type="button" disabled={disabled} onClick={swap}>{t(recovery ? 'services.signin.authenticator' : 'services.signin.recovery')}</button></p>
  </>;
}
/** Whether the code can be sent: six digits, or any recovery code. */
export const twoStepReady = (value: string, recovery: boolean) => recovery ? Boolean(value.trim()) : /^\d{6}$/.test(value);

/** Auth6's six real code boxes and account7's drawn recovery alternative.
 * Both submit to the original account-entry challenge, never another sign-in. */
export function ServerChallenge({busy, error, onCode, onBack}: {busy?: boolean; error?: unknown; onCode: (code: string) => void; onBack: () => void}) {
  const [code, setCode] = useState(''), [recovery, setRecovery] = useState(false), ready = twoStepReady(code, recovery);
  return <AuthStage legal={false}><h1>{t(recovery ? 'services.signin.recovery' : 'web.direct.codeTitle')}</h1>
    <p className={styles.lede}>{t(recovery ? 'services.signin.recoveryBody' : 'services.signin.twoStep')}</p><AuthError error={error} />
    <form className={styles.form} onSubmit={event => {event.preventDefault(); if (!busy && ready) {onCode(code); setCode('');}}}>
      <TwoStepCode value={code} onChange={setCode} recovery={recovery} onRecoveryChange={setRecovery} disabled={busy} autoFocus />
      <Button className={styles.submit} variant="primary" type="submit" loading={busy} disabled={!ready}>{t('action.continue')}</Button>
    </form>
    <AuthLinks><button type="button" disabled={busy} onClick={() => {setCode(''); onBack();}}>{t('auth.backToSignIn')}</button></AuthLinks>
  </AuthStage>;
}
