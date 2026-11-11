import {useState} from 'react';
import {AuthLinks, AuthPasswordField} from '../kit/accounts/AuthStage';
import {ServicesActions, ServicesAuthStage, ServicesHeading, ServicesLabel, ServicesLead, ServicesNotice, ServicesQuiet, ServicesStateIcon, ServicesStrength} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {goTo} from '../platform/hosted-navigation';
import {servicesI18n} from './i18n';
import {account, accountSession} from './account-session';
import {problemField, problemMessage} from './account-problem';
import {codeInput, useAction} from './AccountSignUp';
import {signInPath} from './AccountSignIn';
import styles from './services.module.css';

/** Forgot password: the account's confirmed address gets a code; the code and a new password
 * set it. Every sign-in ends, this browser's too. */
export function AccountReset({next}: {next: string}) {
  const {t} = servicesI18n();
  const [email, setEmail] = useState(''), [sent, setSent] = useState(false), [code, setCode] = useState(''), [password, setPassword] = useState('');
  const [providers, setProviders] = useState<string[]>();
  const {busy, error, run} = useAction();
  const back = <AuthLinks><button type="button" disabled={busy} onClick={() => goTo(signInPath(next), {replace: true})}>{t('next.account.backToSignIn')}</button></AuthLinks>;
  const problem = error && !problemField(error) ? <ServicesNotice tone="error">{t(problemMessage(error, 'reset'))}</ServicesNotice> : null;
  if (providers) return <ServicesAuthStage variant="state">
    <ServicesStateIcon />
    <ServicesHeading>{t('next.account.reset.doneTitle')}</ServicesHeading>
    <ServicesLead>{t('next.account.reset.done')}</ServicesLead>
    {providers.length > 0 && <ServicesNotice tone="info">{t('next.account.reset.providers', {providers: providers.map(item => t(item === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')).join(' / ')})}</ServicesNotice>}
    <ServicesActions><Button variant="primary" onClick={() => goTo(signInPath(next), {replace: true})}>{t('next.account.signIn.submit')}</Button></ServicesActions>
  </ServicesAuthStage>;
  if (!sent) return <ServicesAuthStage variant="reset">
    <ServicesHeading>{t('next.account.reset.title')}</ServicesHeading>
    <ServicesLead>{t('next.account.reset.lead')}</ServicesLead>
    {problem}
    <form className={styles.form} onSubmit={run(async () => { await account.askPasswordReset(email); setSent(true); })}>
      <TextField variant="auth" label={t('next.account.reset.email')} name="email" type="email" autoComplete="email" value={email} onChange={event => setEmail(event.currentTarget.value)} error={problemField(error) ? t(problemMessage(error, 'reset')) : undefined} disabled={busy} required autoFocus />
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={!email.trim()}>{t('next.account.reset.send')}</Button></ServicesActions>
    </form>
    <ServicesQuiet>{t('next.account.reset.providerOnly')}</ServicesQuiet>
    {back}
  </ServicesAuthStage>;
  const finish = run(async () => {
    const answer = await account.finishPasswordReset(email, code, password);
    accountSession.forget();
    setProviders(answer.providers ?? []);
  });
  return <ServicesAuthStage variant="reset">
    <ServicesStateIcon tone="info" icon="i-mail" size={26} />
    <ServicesHeading>{t('next.account.reset.sentTitle')}</ServicesHeading>
    <ServicesLead>{t('next.account.reset.sent', {address: email.trim()})}</ServicesLead>
    {problem}
    <form className={styles.form} onSubmit={finish}>
      <TextField variant="auth" label={t('next.account.reset.code')} name="one-time-code" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} placeholder={t('next.account.codePlaceholder')} value={code} onChange={event => setCode(codeInput(event.currentTarget.value))} disabled={busy} required autoFocus />
      <div><AuthPasswordField label={<ServicesLabel detail={t('next.account.signUp.passwordRule')}>{t('next.account.reset.newPassword')}</ServicesLabel>} name="new-password" autoComplete="new-password" value={password} onChange={event => setPassword(event.currentTarget.value)} error={problemField(error) ? t(problemMessage(error, 'reset')) : undefined} disabled={busy} required /><ServicesStrength password={password} id="reset-strength" /></div>
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={code.length !== 9 || [...password].length < 8}>{t('next.account.reset.submit')}</Button></ServicesActions>
    </form>
    <AuthLinks><button type="button" disabled={busy} onClick={() => void run(async () => { await account.askPasswordReset(email); })()}>{t('next.account.confirm.resend')}</button></AuthLinks>
    {back}
  </ServicesAuthStage>;
}
