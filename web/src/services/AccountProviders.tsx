import {useEffect, useRef, useState, type FormEvent} from 'react';
import {AuthLinks} from '../kit/accounts/AuthStage';
import {ServicesActions, ServicesAuthStage, ServicesDivider, ServicesHeading, ServicesLabel, ServicesLead, ServicesNotice, ServicesProviderButton, ServicesProviderContext, ServicesQuiet, ServicesTerms} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {goTo} from '../platform/hosted-navigation';
import {servicesI18n} from './i18n';
import {account} from './account-session';
import {ServicesError, thisDevice} from './services-api';
import {problemField, problemMessage} from './account-problem';
import {AccountTwoStep} from './AccountTwoStep';
import {signInPath} from './AccountSignIn';
import styles from './services.module.css';

export type {Provider} from '@core/portico-account.ts';
import type {Provider} from '@core/portico-account.ts';
/** A browser sign-in out at Google or Apple: the secret only this tab holds (Services has its
 * digest), where to go afterwards, and whether it connects the provider to the signed-in
 * account rather than signing in. */
type Started = Readonly<{secret: string; next: string; provider: Provider; connect: boolean}>;
const startedKey = (id: string) => `portico.account.provider.${id}`;

function base64url(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** Starts Sign in with Google or Apple and leaves for the provider. `proof` (current password and
 * two-step code) is sent only to connect a provider to the signed-in account. */
export async function startProvider(provider: Provider, next: string, proof?: {currentPassword?: string; twoStepCode?: string}): Promise<void> {
  const secret = base64url(crypto.getRandomValues(new Uint8Array(32)));
  const secretHash = base64url(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(secret))));
  const answer = await account.startProvider(provider, secretHash, proof);
  sessionStorage.setItem(startedKey(answer.id), JSON.stringify({secret, next, provider, connect: Boolean(proof)} satisfies Started));
  location.assign(answer.authorizeUrl);
}

/** Which of Google and Apple this Portico Services offers (`GET /v1/system`), asked once a page. */
let offered: Promise<readonly string[]> | undefined;
function offeredProviders(): Promise<readonly string[]> {
  offered ??= account.capabilities().catch(() => { offered = undefined; return []; });
  return offered;
}

export function ProviderButtons({next, disabled, divider}: {next: string; disabled: boolean; divider: string}) {
  const {t} = servicesI18n();
  const [leaving, setLeaving] = useState(false), [error, setError] = useState<unknown>(), [providers, setProviders] = useState<readonly string[]>([]);
  useEffect(() => { let live = true; void offeredProviders().then(value => { if (live) setProviders(value); }); return () => { live = false; }; }, []);
  const start = (provider: Provider) => {
    setLeaving(true); setError(undefined);
    startProvider(provider, next).catch(caught => { setError(caught); setLeaving(false); });
  };
  if (!providers.includes('apple') && !providers.includes('google')) return null;
  return <>
    {providers.includes('apple') && <ServicesProviderButton provider="apple" disabled={disabled || leaving} onClick={() => start('apple')}>{t('next.account.signIn.apple')}</ServicesProviderButton>}
    {providers.includes('google') && <ServicesProviderButton provider="google" disabled={disabled || leaving} onClick={() => start('google')}>{t('next.account.signIn.google')}</ServicesProviderButton>}
    {error ? <ServicesNotice tone="error">{t(providerProblem(error))}</ServicesNotice> : null}
    <ServicesDivider>{divider}</ServicesDivider>
  </>;
}

function providerProblem(error: unknown) {
  if (error instanceof ServicesError) switch (error.code) {
    case 'provider_unavailable': return 'next.account.provider.unavailable' as const;
    case 'sign_in_gone': return 'next.account.provider.gone' as const;
    case 'email_required': return 'next.account.provider.emailRequired' as const;
    case 'email_unverified': return 'next.account.provider.emailUnverified' as const;
    case 'account_exists': return 'next.account.provider.accountExists' as const;
    case 'provider_in_use': return 'next.account.provider.inUse' as const;
    case 'password_wrong': return 'next.account.problem.passwordWrong' as const;
    case 'sign_in_again': return 'next.account.problem.signInAgain' as const;
  }
  return problemMessage(error, 'signIn');
}

/** `/app/account-return#tx=…&code=…`: where Services sends the browser after Google or Apple.
 * The address is read once and scrubbed before anything else, then finished with the secret
 * this tab kept: neither half alone finishes a sign-in. */
export function AccountProviderReturn({arrival}: {arrival: {id?: string; code?: string; failed: boolean}}) {
  const {t} = servicesI18n();
  const started = useRef<Started | undefined>(undefined);
  if (!started.current && arrival.id) {
    try { started.current = JSON.parse(sessionStorage.getItem(startedKey(arrival.id)) ?? 'null') ?? undefined; } catch { started.current = undefined; }
  }
  const flow = started.current;
  const [state, setState] = useState<{phase: 'working' | 'username' | 'twoStep' | 'failed'; error?: unknown; challenge?: string}>(() => arrival.failed || !flow || !arrival.code ? {phase: 'failed'} : {phase: 'working'});
  const [username, setUsername] = useState(''), [name, setName] = useState(''), [busy, setBusy] = useState(false);
  const finish = async (extra: {username?: string; displayName?: string} = {}) => {
    if (!flow || !arrival.id || !arrival.code) return;
    try {
      const answer = await account.completeProvider(arrival.id, arrival.code, flow.secret, thisDevice(), extra);
      sessionStorage.removeItem(startedKey(arrival.id));
      if (answer.twoStep) { setState({phase: 'twoStep', challenge: answer.twoStep.challengeId}); return; }
      goTo(answer.connected ? `/account?tab=security&connected=${answer.connected}` : flow.next, {replace: true});
    } catch (caught) {
      if (caught instanceof ServicesError && (caught.code === 'username_needed' || caught.code === 'username_taken' || caught.code === 'invalid')) {
        setState({phase: 'username', error: caught.code === 'username_needed' ? undefined : caught});
        return;
      }
      sessionStorage.removeItem(startedKey(arrival.id));
      setState({phase: 'failed', error: caught});
    }
  };
  const once = useRef(false);
  useEffect(() => { if (!once.current && state.phase === 'working') { once.current = true; void finish(); } });
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    try { await finish({username, displayName: name}); } finally { setBusy(false); }
  };
  if (state.phase === 'twoStep' && state.challenge) return <AccountTwoStep challenge={state.challenge} next={flow?.next ?? '/account'} onGone={() => setState({phase: 'failed', error: new ServicesError(410, 'challenge_gone')})} />;
  if (state.phase === 'username' && flow) {
    const field = problemField(state.error);
    return <ServicesAuthStage variant="providerOnboarding">
      <ServicesProviderContext provider={flow.provider} />
      <ServicesHeading>{t('next.account.provider.usernameTitle')}</ServicesHeading>
      <ServicesLead>{t('next.account.provider.usernameLead')}</ServicesLead>
      <form className={styles.form} onSubmit={submit}>
        <TextField variant="auth" label={t('next.account.signUp.username')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} description={t('next.account.signUp.usernameHelp')}
          error={field === 'username' ? t(problemMessage(state.error, 'signUp')) : undefined} value={username} onChange={event => setUsername(event.currentTarget.value)} minLength={3} maxLength={64} required disabled={busy} autoFocus />
        <TextField variant="auth" label={<ServicesLabel detail={t('next.account.signUp.optional')}>{t('next.account.signUp.name')}</ServicesLabel>} name="name" autoComplete="name"
          error={field === 'displayName' ? t(problemMessage(state.error, 'signUp')) : undefined} value={name} onChange={event => setName(event.currentTarget.value)} maxLength={120} disabled={busy} />
        <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={username.trim().length < 3}>{t('action.continue')}</Button></ServicesActions>
      </form>
      <ServicesQuiet>{t('next.account.provider.usernameTime')}</ServicesQuiet>
      <ServicesTerms provider={flow.provider} />
    </ServicesAuthStage>;
  }
  if (state.phase === 'failed') return <ServicesAuthStage variant="state">
    <ServicesHeading>{t('next.account.provider.failedTitle')}</ServicesHeading>
    <ServicesNotice tone="error">{t(state.error ? providerProblem(state.error) : 'next.account.provider.failed')}</ServicesNotice>
    <ServicesActions><Button variant="primary" onClick={() => goTo(signInPath(flow?.next ?? '/account'), {replace: true})}>{t('next.account.backToSignIn')}</Button></ServicesActions>
    {flow?.connect && <AuthLinks><button type="button" onClick={() => goTo('/account?tab=security', {replace: true})}>{t('next.account.provider.backToAccount')}</button></AuthLinks>}
  </ServicesAuthStage>;
  return <ServicesAuthStage variant="state"><ServicesHeading>{t('next.account.provider.finishing')}</ServicesHeading></ServicesAuthStage>;
}

/** Reads and scrubs the return address before anything renders, so the one-use code never
 * stays in the address bar or history. */
export function captureProviderReturn(page: Location = location, pageHistory: History = history): {id?: string; code?: string; failed: boolean} {
  const values = new URLSearchParams(page.hash.slice(1));
  pageHistory.replaceState(pageHistory.state, '', page.pathname);
  return {id: values.get('tx') ?? undefined, code: values.get('code') ?? undefined, failed: values.has('error')};
}
