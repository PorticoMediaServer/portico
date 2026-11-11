import {useState, type FormEvent} from 'react';
import {AuthLinks, AuthPasswordField} from '../kit/accounts';
import {ServicesActions, ServicesAuthStage, ServicesHeading, ServicesLabel, ServicesLead, ServicesNotice, ServicesHint, ServicesPair, ServicesQuiet, ServicesStateIcon, ServicesStrength, ServicesTerms} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {goTo} from '../platform/hosted-navigation';
import {servicesI18n} from './i18n';
import {accountSession} from './account-session';
import {servicesCall, thisDevice, type Tokens} from './services-api';
import {problemField, problemMessage} from './account-problem';
import {forgetSignUp, rememberSignUp, signUpInProgress} from './account-sign-up-memory';
import {ProviderButtons} from './AccountProviders';
import {signInPath} from './AccountSignIn';
import styles from './services.module.css';

/** A code as it was emailed, XXXX-XXXX, whatever the person typed or pasted. */
export function codeInput(value: string): string {
  const plain = value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8);
  return plain.length > 4 ? `${plain.slice(0, 4)}-${plain.slice(4)}` : plain;
}

function useAction() {
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const run = (work: () => Promise<void>) => async (event?: FormEvent) => {
    event?.preventDefault();
    if (busy) return;
    setBusy(true); setError(undefined);
    try { await work(); } catch (caught) { setError(caught); } finally { setBusy(false); }
  };
  return {busy, error, setError, run};
}
export {useAction};

export function AccountSignUp({next}: {next: string}) {
  const {t} = servicesI18n();
  const [email, setEmail] = useState(''), [username, setUsername] = useState(''), [name, setName] = useState(''), [password, setPassword] = useState('');
  const {busy, error, run} = useAction();
  const field = problemField(error);
  const fieldError = (name: string) => field === name ? t(problemMessage(error, 'signUp')) : undefined;
  const submit = run(async () => {
    const answer = await servicesCall<{accountId: string; email: string}>('POST', '/v1/accounts', {email, username, password, displayName: name}, {create: true});
    rememberSignUp({accountId: answer.accountId, email: answer.email, next});
    goTo(`/sign-up/confirm?next=${encodeURIComponent(next)}`);
  });
  return <ServicesAuthStage variant="signup">
    <ServicesHeading singleLine>{t('next.account.signUp.title')}</ServicesHeading>
    <ServicesLead>{t('next.account.signIn.lead')}</ServicesLead>
    <ProviderButtons next={next} disabled={busy} divider={t('next.account.signIn.orEmail')} />
    {error && !field ? <ServicesNotice tone="error">{t(problemMessage(error, 'signUp'))}</ServicesNotice> : null}
    <form className={styles.form} onSubmit={submit}>
      <TextField variant="auth" label={t('next.account.signUp.email')} name="email" type="email" autoComplete="email" value={email} onChange={event => setEmail(event.currentTarget.value)} description={t('next.account.signUp.emailHelp')} error={fieldError('email')} required disabled={busy} />
      <ServicesPair>
        <TextField variant="auth" label={t('next.account.signUp.username')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} value={username} onChange={event => setUsername(event.currentTarget.value)} minLength={3} maxLength={64} error={fieldError('username')} required disabled={busy} />
        <TextField variant="auth" label={<ServicesLabel detail={t('next.account.signUp.optional')}>{t('next.account.signUp.name')}</ServicesLabel>} name="name" autoComplete="name" value={name} onChange={event => setName(event.currentTarget.value)} maxLength={120} error={fieldError('displayName')} disabled={busy} />
      </ServicesPair>
      <ServicesHint>{t('next.account.signUp.usernameHelp')}</ServicesHint>
      <div><AuthPasswordField label={<ServicesLabel detail={t('next.account.signUp.passwordRule')}>{t('next.account.signUp.password')}</ServicesLabel>} name="new-password" autoComplete="new-password" value={password} onChange={event => setPassword(event.currentTarget.value)} error={fieldError('password')} disabled={busy} required aria-describedby={password ? 'signup-strength' : undefined} /><ServicesStrength password={password} id="signup-strength" /></div>
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={!email.trim() || username.trim().length < 3 || [...password].length < 8}>{t('next.account.signUp.submit')}</Button></ServicesActions>
    </form>
    <AuthLinks><button type="button" disabled={busy} onClick={() => goTo(signInPath(next))}>{t('next.account.signUp.haveAccount')}</button></AuthLinks>
    <ServicesTerms />
  </ServicesAuthStage>;
}

/** "Check your email": the code finishes the sign-up this tab started. A person who closed the
 * tab signs in with the password they chose; that sign-in sends a fresh code and brings them back
 * here with the account it names. */
export function AccountConfirm({next}: {next: string}) {
  const {t} = servicesI18n();
  const signUp = signUpInProgress();
  const [code, setCode] = useState(''), [resent, setResent] = useState(false);
  const {busy, error, run} = useAction();
  if (!signUp) return <ServicesAuthStage variant="reset">
    <ServicesStateIcon tone="info" icon="i-mail" size={32} />
    <ServicesHeading>{t('next.account.confirm.title')}</ServicesHeading>
    <ServicesLead>{t('next.account.confirm.lost')}</ServicesLead>
    <ServicesActions><Button variant="primary" onClick={() => goTo(signInPath(next), {replace: true})}>{t('next.account.signIn.submit')}</Button></ServicesActions>
  </ServicesAuthStage>;
  const confirm = run(async () => {
    const answer = await servicesCall<{tokens: Tokens}>('POST', '/v1/accounts/confirm', {accountId: signUp.accountId, code, device: thisDevice()});
    forgetSignUp();
    accountSession.keep(answer.tokens);
    goTo(signUp.next ?? next, {replace: true});
  });
  const resend = run(async () => {
    await servicesCall('POST', '/v1/accounts/confirm/resend', {accountId: signUp.accountId});
    setResent(true);
  });
  return <ServicesAuthStage variant="reset">
    <ServicesStateIcon tone="info" icon="i-mail" size={32} />
    <ServicesHeading>{t('next.account.confirm.title')}</ServicesHeading>
    <ServicesLead>{signUp.email ? t('next.account.confirm.lead', {address: signUp.email}) : t('next.account.confirm.leadNoAddress')}</ServicesLead>
    {error ? <ServicesNotice tone="error">{t(problemMessage(error, 'confirm'))}</ServicesNotice> : resent ? <ServicesNotice tone="info">{t('next.account.confirm.resent')}</ServicesNotice> : null}
    <form className={styles.form} onSubmit={confirm}>
      <TextField variant="auth" label={t('next.account.confirm.code')} name="one-time-code" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} placeholder={t('next.account.codePlaceholder')} value={code} onChange={event => setCode(codeInput(event.currentTarget.value))} disabled={busy} required autoFocus />
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={code.length !== 9}>{t('next.account.confirm.submit')}</Button></ServicesActions>
    </form>
    <AuthLinks>
      <button type="button" disabled={busy} onClick={() => void resend()}>{t('next.account.confirm.resend')}</button>
      <button type="button" disabled={busy} onClick={() => { forgetSignUp(); goTo(`/sign-up?next=${encodeURIComponent(next)}`, {replace: true}); }}>{t('next.account.confirm.wrongAddress')}</button>
    </AuthLinks>
    <ServicesQuiet>{t('next.account.confirm.help')}</ServicesQuiet>
  </ServicesAuthStage>;
}
