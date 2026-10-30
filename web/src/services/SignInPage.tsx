import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ServicesAuthStage, ServicesHeading, ServicesLead, ServicesActions, ServicesSignedInAccount, HostedError} from '../kit/accounts/ServicesAuth';
import {DocumentBootReady} from '../shell/DocumentBootReady';
import {Icon} from '../kit/Icon';
import {Button} from '../kit/controls';
import {ServicesAuthForm} from './AuthForm';
import {ServicesInvalidLink} from './ServicesApp';
import {accountIdentity, type ServicesAccount} from './session';
import {continueHostedSignIn, hostedSignInStore, type HostedSignIn} from './sign-in';
import {servicesI18n} from './i18n';
import styles from './services-signin-r46.module.css';

/** auth5's quiet return link: drawn chevron, server name from the signed handoff only. */
function ReturnLink({href, server}: {href: string; server: string}) {
  const {t} = servicesI18n();
  return <div className={styles.returnLink}><a href={href}><span><Icon name="i-left" size={18} />{t('services.return.back', {server})}</span></a></div>;
}

/** Auth5 + connect19b, composed by ruling29. Only Continue issues an answer. */
export function RegisteredSignInPage({account, pending, providerFailure}: {account: ServicesAccount; pending: HostedSignIn; providerFailure?: 'google' | 'apple' | 'unknown'}) {
  const {t} = servicesI18n(), session = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot).session;
  const [authenticating, setAuthenticating] = useState(() => !session), [username, setUsername] = useState(session?.account.username ?? '');
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [expired, setExpired] = useState(false);
  const active = useRef<AbortController | undefined>(undefined), lastUsername = useRef(session?.account.username ?? '');
  useEffect(() => {
    if (session) lastUsername.current = session.account.username;
    else {setUsername(lastUsername.current); setAuthenticating(true);}
  }, [session]);
  useEffect(() => {
    const timeout = setTimeout(() => {active.current?.abort(); setExpired(true);}, Math.max(0, Date.parse(pending.handoff.expiresAt) - Date.now()));
    return () => {clearTimeout(timeout); active.current?.abort();};
  }, [pending]);
  const act = (work: (signal: AbortSignal) => Promise<void>) => {
    if (busy || active.current && !active.current.signal.aborted) return;
    const controller = new AbortController(); active.current = controller; setBusy(true); setError(undefined);
    void work(controller.signal).catch(value => {if (!controller.signal.aborted) setError(value);}).finally(() => {
      if (active.current === controller) active.current = undefined;
      if (!controller.signal.aborted) setBusy(false);
    });
  };
  if (expired || Date.parse(pending.handoff.expiresAt) <= Date.now()) return <DocumentBootReady><ServicesInvalidLink /></DocumentBootReady>;
  const back = <ReturnLink href={pending.handoff.registration.origin} server={pending.serverName} />;
  const lead = t('services.return.lead', {server: pending.serverName});
  if (!session || authenticating) return <ServicesAuthForm account={account} providerFailure={providerFailure} heading={t('auth.welcomeHome')} lead={lead}
    initialUsername={username} before={<HostedError error={error} />} after={back} onSignedIn={gesture => {
      setAuthenticating(false);
      const current = account.service.getSnapshot().session;
      if (gesture && current) act(signal => continueHostedSignIn(account, pending, accountIdentity(current), hostedSignInStore(), signal, value => location.assign(value)));
    }} />;
  return <DocumentBootReady><ServicesAuthStage><ServicesHeading singleLine>{t('auth.welcomeHome')}</ServicesHeading><ServicesLead>{lead}</ServicesLead>
    <HostedError error={error} /><ServicesSignedInAccount username={session.account.username} disabled={busy}
      onDifferent={() => act(async () => {await account.service.logoutIfCurrent(accountIdentity(session));})} />
    <ServicesActions><Button variant="primary" loading={busy} disabled={busy} onClick={() => act(signal =>
      continueHostedSignIn(account, pending, accountIdentity(session), hostedSignInStore(), signal, value => location.assign(value)))}>{t('action.continue')}</Button></ServicesActions>{back}
  </ServicesAuthStage></DocumentBootReady>;
}
