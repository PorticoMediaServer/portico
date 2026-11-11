import {useEffect, useState, useSyncExternalStore} from 'react';
import {AuthLinks} from '../kit/accounts/AuthStage';
import {ServicesActions, ServicesAuthStage, ServicesFacts, ServicesHeading, ServicesLead, ServicesNotice, ServicesQuiet, ServicesStateIcon} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {servicesI18n} from './i18n';
import {account as services, accountSession} from './account-session';
import {ServicesError} from './services-api';
import {problemMessage} from './account-problem';
import {AccountSignIn} from './AccountSignIn';
import {codeInput, useAction} from './AccountSignUp';
import styles from './services.module.css';

type Purpose = 'setup' | 'switch' | 'change_owner';
type Approval = Readonly<{id: string; serverId: string; serverName: string; purpose: Purpose; state?: 'waiting' | 'approved' | 'declined'}>;
type Account = Readonly<{username: string; displayName: string; email: string}>;
type Decision = Readonly<{state: 'approved' | 'declined'; returnUrl: string; approval?: string}>;

/** `/approve/<id>`, the address a new server sends its owner to, and `/approve`, where the code
 * the server shows is typed (from a phone, say). Either way the person signs in first, sees the
 * server's name and id exactly as the server asked, and approves or declines. */
export function ApproveServer({id}: {id?: string}) {
  const {t} = servicesI18n();
  const signedIn = useSyncExternalStore(accountSession.subscribe, accountSession.signedIn);
  const here = id ? `/approve/${encodeURIComponent(id)}` : '/approve';
  if (!signedIn) return <AccountSignIn next={here} lead={t('next.account.signIn.forApproval')} />;
  return id ? <ApprovalById id={id} /> : <ApprovalByCode />;
}

function ApprovalByCode() {
  const {t} = servicesI18n();
  const [code, setCode] = useState(''), [found, setFound] = useState<Approval>();
  const {busy, error, run} = useAction();
  if (found) return <ApprovalCard approval={found} byCode />;
  const lookup = run(async () => { setFound(await services.lookupApproval(code)); });
  return <ServicesAuthStage>
    <ServicesHeading>{t('next.account.approve.codeTitle')}</ServicesHeading>
    <ServicesLead>{t('next.account.approve.codeLead')}</ServicesLead>
    {error ? <ServicesNotice tone="error">{t(problemMessage(error, 'approveCode'))}</ServicesNotice> : null}
    <form className={styles.form} onSubmit={lookup}>
      <TextField variant="auth" label={t('next.account.approve.code')} name="approval-code" autoComplete="off" autoCapitalize="characters" spellCheck={false} placeholder={t('next.account.codePlaceholder')} value={code} onChange={event => setCode(codeInput(event.currentTarget.value))} disabled={busy} required autoFocus />
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={code.length !== 9}>{t('next.account.approve.find')}</Button></ServicesActions>
    </form>
  </ServicesAuthStage>;
}

function ApprovalById({id}: {id: string}) {
  const {t} = servicesI18n();
  const [approval, setApproval] = useState<Approval>(), [error, setError] = useState<unknown>(), [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setError(undefined);
    services.approval(id, controller.signal).then(setApproval, caught => { if (!controller.signal.aborted) setError(caught); });
    return () => controller.abort();
  }, [id, attempt]);
  if (error) return <ServicesAuthStage variant="state">
    <ServicesStateIcon tone="warning" icon="i-warn" />
    <ServicesHeading>{t('next.account.approve.codeTitle')}</ServicesHeading>
    <ServicesNotice tone="error">{t(problemMessage(error, 'approve'))}</ServicesNotice>
    {error instanceof ServicesError && error.code !== 'approval_gone' && <ServicesActions><Button onClick={() => setAttempt(value => value + 1)}>{t('next.account.problem.tryAgain')}</Button></ServicesActions>}
  </ServicesAuthStage>;
  if (!approval) return <ServicesAuthStage><></></ServicesAuthStage>;
  return <ApprovalCard approval={approval} byCode={false} />;
}

function ApprovalCard({approval, byCode}: {approval: Approval; byCode: boolean}) {
  const {t} = servicesI18n();
  const [account, setAccount] = useState<Account>();
  const [decided, setDecided] = useState<Decision | undefined>(() => approval.state === 'declined' ? {state: 'declined', returnUrl: ''} : undefined);
  const {busy, error, run} = useAction();
  useEffect(() => { void services.account().then(setAccount, () => undefined); }, []);
  const server = approval.serverName;
  const decide = (approve: boolean) => run(async () => {
    const answer = await services.decideApproval(approval.id, approve);
    setDecided(answer);
    // By link the browser carries the approval back to the server that asked; by code the server
    // asks Services itself, so this device stays here.
    if (answer.state === 'approved' && answer.approval && !byCode) location.assign(`${answer.returnUrl}#approval=${answer.approval}`);
  });
  if (decided?.state === 'approved' || approval.state === 'approved') return <ServicesAuthStage variant="state">
    <ServicesStateIcon />
    <ServicesHeading>{t('next.account.approve.approvedTitle')}</ServicesHeading>
    <ServicesLead>{t(byCode ? 'next.account.approve.approvedCode' : 'next.account.approve.approvedBody', {server})}</ServicesLead>
    {!byCode && decided?.approval && <AuthLinks><a href={`${decided.returnUrl}#approval=${decided.approval}`}>{t('next.account.approve.back', {server})}</a></AuthLinks>}
  </ServicesAuthStage>;
  if (decided) return <ServicesAuthStage variant="state">
    <ServicesStateIcon tone="info" icon="i-info" />
    <ServicesHeading>{t('next.account.approve.declinedTitle')}</ServicesHeading>
    <ServicesLead>{t('next.account.approve.declinedBody', {server})}</ServicesLead>
    {!byCode && decided.returnUrl && <AuthLinks><a href={decided.returnUrl}>{t('next.account.approve.back', {server})}</a></AuthLinks>}
  </ServicesAuthStage>;
  // Refusals that pressing Approve again cannot change.
  const final = error instanceof ServicesError && ['server_owned', 'too_many_servers', 'already_answered', 'approval_gone', 'server_not_linked'].includes(error.code);
  const who = account ? account.displayName || account.username : '';
  const title = approval.purpose === 'switch' ? 'next.account.approve.titleSwitch' : approval.purpose === 'change_owner' ? 'next.account.approve.titleOwner' : 'next.account.approve.titleSetup';
  const lead = approval.purpose === 'switch' ? 'next.account.approve.leadSwitch' : approval.purpose === 'change_owner' ? 'next.account.approve.leadOwner' : 'next.account.approve.leadSetup';
  return <ServicesAuthStage variant="ownerReview">
    <ServicesHeading>{t(title, {server})}</ServicesHeading>
    <ServicesLead>{t(lead)}</ServicesLead>
    <ServicesFacts items={[
      {id: 'server', label: t('next.account.approve.server'), value: server},
      {id: 'serverId', label: t('next.account.approve.serverId'), value: <code className={styles.serverId}>{approval.serverId}</code>},
      {id: 'account', label: t('next.account.approve.account'), value: account ? `${account.username} (${account.email})` : ''},
    ]} />
    <ServicesNotice tone="info">{t('next.account.approve.grants', {account: who || t('next.account.approve.account'), server})}</ServicesNotice>
    <ServicesQuiet>{t('next.account.approve.match')}</ServicesQuiet>
    {error ? <ServicesNotice tone="error">{t(problemMessage(error, 'approve'))}</ServicesNotice> : null}
    <ServicesActions>
      {/* Approve waits until the page shows which account will own the server. */}
      {!final && <Button variant="primary" loading={busy} disabled={!account} onClick={() => void decide(true)()}>{t('next.account.approve.approve')}</Button>}
      <Button disabled={busy} onClick={() => void decide(false)()}>{t('next.account.approve.decline')}</Button>
    </ServicesActions>
    <AuthLinks><button type="button" disabled={busy} onClick={() => void services.signOut()}>{t('next.account.approve.notYou')}</button></AuthLinks>
  </ServicesAuthStage>;
}
