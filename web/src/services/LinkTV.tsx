import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {AuthLinks} from '../kit/accounts/AuthStage';
import {ServicesActions, ServicesAuthStage, ServicesHeading, ServicesLead, ServicesNotice, ServicesStateIcon} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {servicesI18n} from './i18n';
import {accountSession, personCall} from './account-session';
import {ServicesError} from './services-api';
import {problemMessage} from './account-problem';
import {AccountSignIn} from './AccountSignIn';
import {codeInput, useAction} from './AccountSignUp';
import styles from './services.module.css';

/** The code a TV's QR code carried (`/tv#code=…`). Read once and cleared from the address bar at
 * once; kept only in this page's memory, never in storage or in another address, so it survives
 * signing in on this page but not a reload. */
let fromAddress: string | undefined;
export function takeTVCodeFromAddress(page: Location = location, pageHistory: History = history): void {
  const code = new URLSearchParams(page.hash.slice(1)).get('code');
  if (code === null) return;
  pageHistory.replaceState(pageHistory.state, '', page.pathname);
  fromAddress = codeInput(code);
}

type Found = Readonly<{id: string; device: {name: string; kind: string}; code: string; fromLink: boolean}>;
/** The TV's own words, shown as text on one line: nothing unprintable, and not too long. */
const plain = (value: string, most: number) => { const kept = [...value.replace(/\p{C}/gu, '')]; return kept.length > most ? kept.slice(0, most - 1).join('') + '…' : kept.join(''); };

/** `/tv`: a person signed in to their Portico Account types (or scans) the code a TV shows, checks
 * it against the TV and approves or denies. Nothing is ever approved without that last step. */
export function LinkTV() {
  const {t} = servicesI18n();
  const signedIn = useSyncExternalStore(accountSession.subscribe, accountSession.signedIn);
  // Read during the first render, before anything below it looks for the code.
  useState(() => { takeTVCodeFromAddress(); return true; });
  if (!signedIn) return <AccountSignIn next="/tv" lead={t('next.account.signIn.forTV')} />;
  return <SignedInLinkTV />;
}

function SignedInLinkTV() {
  const {t} = servicesI18n();
  const [code, setCode] = useState(() => fromAddress ?? ''), [found, setFound] = useState<Found>();
  const {busy, error, setError, run} = useAction();
  const lookup = (value: string, fromLink: boolean) => run(async () => {
    const answer = await personCall<{id: string; device: {name: string; kind: string}}>('POST', '/v1/tv-codes/lookup', {code: value});
    setFound({...answer, code: value, fromLink});
  });
  // A code from the address is looked up at once: the person still sees it, with the warning
  // for links, and must choose Approve or Deny.
  const started = useRef(false);
  useEffect(() => {
    if (started.current || !fromAddress) return;
    started.current = true;
    const value = fromAddress; fromAddress = undefined;
    void lookup(value, true)();
  });
  if (found) return <TVDecision found={found} onAnother={() => { setFound(undefined); setCode(''); setError(undefined); }} />;
  return <ServicesAuthStage>
    <ServicesHeading>{t('next.account.tv.codeTitle')}</ServicesHeading>
    <ServicesLead>{t('next.account.tv.codeLead')}</ServicesLead>
    {error ? <ServicesNotice tone="error">{t(tvProblem(error))}</ServicesNotice> : null}
    <form className={styles.form} onSubmit={lookup(code, false)}>
      <TextField variant="auth" label={t('next.account.tv.code')} name="tv-code" autoComplete="off" autoCapitalize="characters" spellCheck={false} placeholder={t('next.account.codePlaceholder')} value={code} onChange={event => setCode(codeInput(event.currentTarget.value))} disabled={busy} required autoFocus />
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={code.length !== 9}>{t('next.account.tv.find')}</Button></ServicesActions>
    </form>
  </ServicesAuthStage>;
}

function tvProblem(error: unknown, deciding = false) {
  if (error instanceof ServicesError) switch (error.code) {
    case 'tv_code_gone': return 'next.account.tv.gone' as const;
    case 'already_answered': return 'next.account.tv.alreadyAnswered' as const;
    case 'wait': return 'next.account.tv.wait' as const;
    case 'slow_down': return deciding ? 'next.account.tv.approveLimit' as const : 'next.account.problem.slowDown' as const;
  }
  return problemMessage(error, 'account');
}

function TVDecision({found, onAnother}: {found: Found; onAnother: () => void}) {
  const {t} = servicesI18n();
  const [account, setAccount] = useState<string>(), [answer, setAnswer] = useState<'approved' | 'denied'>(), [canDeny, setCanDeny] = useState(true);
  const {busy, error, run} = useAction();
  useEffect(() => { void personCall<{username: string}>('GET', '/v1/account').then(value => setAccount(value.username), () => undefined); }, []); // query-read: kept (Portico Services account pages: the person's Portico Account, not a media server document)
  // The person who approved may still deny until the TV collects its sign-in, which it does
  // within seconds; after that a denial would only be refused.
  useEffect(() => {
    if (answer !== 'approved') return;
    const timer = setTimeout(() => setCanDeny(false), 15_000);
    return () => clearTimeout(timer);
  }, [answer]);
  const decide = (approve: boolean) => run(async () => {
    await personCall('POST', `/v1/tv-codes/${encodeURIComponent(found.id)}/decision`, {approve});
    setAnswer(approve ? 'approved' : 'denied');
  });
  const name = plain(found.device.name, 40), kind = plain(found.device.kind, 20);
  // A denial after approving that finds the code used up means the TV has already signed in.
  const signedInAlready = answer === 'approved' && error instanceof ServicesError && error.code === 'tv_code_gone';
  const problem = error ? <ServicesNotice tone="error">{t(signedInAlready ? 'next.account.tv.alreadyIn' : tvProblem(error, true))}</ServicesNotice> : null;
  if (answer) return <ServicesAuthStage variant="state">
    <ServicesStateIcon tone={answer === 'approved' ? 'success' : 'info'} icon={answer === 'approved' ? 'i-check' : 'i-info'} />
    <ServicesHeading>{t('next.account.tv.title')}</ServicesHeading>
    <ServicesLead>{t(answer === 'approved' ? 'next.account.tv.approved' : 'next.account.tv.denied')}</ServicesLead>
    {problem}
    {answer === 'approved' && canDeny && !error && <ServicesActions><Button loading={busy} onClick={() => void decide(false)()}>{t('next.account.tv.deny')}</Button></ServicesActions>}
    <AuthLinks><button type="button" disabled={busy} onClick={onAnother}>{t('next.account.tv.another')}</button></AuthLinks>
  </ServicesAuthStage>;
  return <ServicesAuthStage variant="deviceReview">
    <ServicesHeading>{t('next.account.tv.title')}</ServicesHeading>
    <p className={styles.tvCode} aria-label={found.code.split('').join(' ')}>{found.code}</p>
    <ServicesNotice tone="warning">{t('next.account.tv.checkCode')}</ServicesNotice>
    {found.fromLink && <ServicesNotice tone="error">{t('next.account.tv.fromLink')}</ServicesNotice>}
    <p className={styles.tvDevice}><b>{t('next.account.tv.deviceName', {name})}</b>{kind && <span>{kind}</span>}</p>
    <ServicesLead>{t('next.account.tv.grants', {account: account ?? ''})}</ServicesLead>
    {problem}
    <ServicesActions>
      <Button loading={busy} disabled={!account} onClick={() => void decide(true)()}>{t('next.account.tv.approve')}</Button>
      <Button disabled={busy} onClick={() => void decide(false)()}>{t('next.account.tv.deny')}</Button>
    </ServicesActions>
  </ServicesAuthStage>;
}
