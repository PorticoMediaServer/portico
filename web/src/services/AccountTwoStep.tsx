import {useState, type FormEvent} from 'react';
import {AuthLinks} from '../kit/accounts/AuthStage';
import {ServicesActions, ServicesAuthStage, ServicesHeading, ServicesLead, ServicesNotice} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {goTo} from '../platform/hosted-navigation';
import {servicesI18n} from './i18n';
import {accountSession} from './account-session';
import {servicesCall, ServicesError, type Tokens} from './services-api';
import {problemMessage} from './account-problem';
import styles from './services.module.css';

/** The second step of a sign-in whose password, Google or Apple was right: a code from the
 * authenticator app, or a recovery code. Five minutes and five tries, then sign in again. */
export function AccountTwoStep({challenge, next, onGone}: {challenge: string; next: string; onGone: () => void}) {
  const {t} = servicesI18n();
  const [recovery, setRecovery] = useState(false), [code, setCode] = useState('');
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError(undefined);
    try {
      const answer = await servicesCall<{tokens: Tokens}>('POST', '/v1/sessions/two-step', {challengeId: challenge, code: code.trim()}, {create: true});
      accountSession.keep(answer.tokens);
      goTo(next, {replace: true});
    } catch (caught) {
      if (caught instanceof ServicesError && caught.code === 'challenge_gone') { onGone(); return; }
      setError(caught);
    } finally { setBusy(false); }
  };
  return <ServicesAuthStage>
    <ServicesHeading>{t('next.account.twoStep.title')}</ServicesHeading>
    <ServicesLead>{t(recovery ? 'next.account.twoStep.recoveryLead' : 'next.account.twoStep.lead')}</ServicesLead>
    {error ? <ServicesNotice tone="error">{t(problemMessage(error, 'twoStep'))}</ServicesNotice> : null}
    <form className={styles.form} onSubmit={submit}>
      {recovery
        ? <TextField key="recovery" variant="auth" label={t('next.account.twoStep.recovery')} name="recovery-code" autoComplete="off" autoCapitalize="characters" spellCheck={false} value={code} onChange={event => setCode(event.currentTarget.value)} disabled={busy} required autoFocus />
        : <TextField key="app" variant="auth" label={t('next.account.twoStep.code')} name="one-time-code" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={event => setCode(event.currentTarget.value.replace(/\D/g, ''))} disabled={busy} required autoFocus />}
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={recovery ? !code.trim() : code.length !== 6}>{t('action.continue')}</Button></ServicesActions>
    </form>
    <AuthLinks><button type="button" disabled={busy} onClick={() => { setRecovery(value => !value); setCode(''); setError(undefined); }}>{t(recovery ? 'next.account.twoStep.useApp' : 'next.account.twoStep.useRecovery')}</button></AuthLinks>
  </ServicesAuthStage>;
}
