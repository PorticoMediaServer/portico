import {useState, type FormEvent, type ReactNode} from 'react';
import {AuthLinks, AuthPasswordField} from '../kit/accounts';
import {ServicesActions, ServicesAuthStage, ServicesHeading, ServicesLead, ServicesNotice} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {goTo} from '../platform/hosted-navigation';
import {servicesI18n} from './i18n';
import {accountSession} from './account-session';
import {servicesCall, ServicesError, thisDevice, type Tokens} from './services-api';
import {problemMessage} from './account-problem';
import {rememberSignUp} from './account-sign-up-memory';
import {ProviderButtons} from './AccountProviders';
import {AccountTwoStep} from './AccountTwoStep';
import styles from './services.module.css';

/** Only a path on this address may be returned to after signing in. */
export function returnPath(search = location.search, fallback = '/account'): string {
  const next = new URLSearchParams(search).get('next');
  return next && next.startsWith('/') && !next.startsWith('//') ? next : fallback;
}
export function signInPath(next: string): string { return `/sign-in?next=${encodeURIComponent(next)}`; }

/** Sign in with an email address or username and a password, then the two-step code when the
 * account has it on. `lead` replaces the usual line when sign-in is a step of something else
 * (approving a server, linking a TV); `next` is where that something else is. */
export function AccountSignIn({next, lead, notice}: {next: string; lead?: ReactNode; notice?: ReactNode}) {
  const {t} = servicesI18n();
  const [login, setLogin] = useState(''), [password, setPassword] = useState('');
  const [challenge, setChallenge] = useState<string>(), [gone, setGone] = useState(false);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const signIn = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError(undefined); setGone(false);
    try {
      const answer = await servicesCall<{tokens?: Tokens; twoStep?: {challengeId: string}}>('POST', '/v1/sessions', {login, password, device: thisDevice()}, {create: true});
      if (answer.twoStep) setChallenge(answer.twoStep.challengeId);
      else if (answer.tokens) { accountSession.keep(answer.tokens); goTo(next, {replace: true}); }
    } catch (caught) {
      // The password was right but the address was never confirmed: a fresh code is on its way,
      // and this sign-in's own answer names the account to confirm (never a link's).
      if (caught instanceof ServicesError && caught.code === 'confirm_email' && caught.fields.accountId) {
        rememberSignUp({accountId: caught.fields.accountId, email: login.includes('@') ? login.trim() : undefined, next});
        goTo(`/sign-up/confirm?next=${encodeURIComponent(next)}`);
        return;
      }
      setError(caught);
    } finally { setBusy(false); }
  };
  if (challenge) return <AccountTwoStep challenge={challenge} next={next} onGone={() => { setChallenge(undefined); setPassword(''); setGone(true); }} />;
  return <ServicesAuthStage>
    <ServicesHeading>{t('next.account.signIn.title')}</ServicesHeading>
    <ServicesLead>{lead ?? t('next.account.signIn.lead')}</ServicesLead>
    {notice}
    <ProviderButtons next={next} disabled={busy} divider={t('next.account.signIn.orEmail')} />
    {error || gone ? <ServicesNotice tone="error">{t(gone ? 'next.account.problem.challengeGone' : problemMessage(error, 'signIn'))}</ServicesNotice> : null}
    <form className={styles.form} onSubmit={signIn}>
      <TextField variant="auth" label={t('next.account.signIn.login')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} value={login} onChange={event => setLogin(event.currentTarget.value)} disabled={busy} required />
      <AuthPasswordField label={t('next.account.signUp.password')} name="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} required />
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={!login.trim() || !password}>{t('next.account.signIn.submit')}</Button></ServicesActions>
    </form>
    <AuthLinks>
      <button type="button" disabled={busy} onClick={() => goTo(`/reset?next=${encodeURIComponent(next)}`)}>{t('next.account.signIn.forgot')}</button>
      <button type="button" disabled={busy} onClick={() => goTo(`/sign-up?next=${encodeURIComponent(next)}`)}>{t('next.account.signIn.create')}</button>
    </AuthLinks>
  </ServicesAuthStage>;
}
