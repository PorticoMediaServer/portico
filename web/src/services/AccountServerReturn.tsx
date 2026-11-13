import {useEffect, useState, useSyncExternalStore} from 'react';
import {openServerList, type Envelope} from '@core/portico-server-list.ts';
import {webRouteCrypto} from '@core/route-identity.ts';
import {AuthLinks} from '../kit/accounts/AuthStage';
import {ServicesActions, ServicesAuthStage, ServicesFacts, ServicesHeading, ServicesLead, ServicesNotice, ServicesStateIcon} from '../kit/accounts/ServicesAuth';
import {Button} from '../kit/controls';
import {goTo} from '../platform/hosted-navigation';
import {accountReturnFinish, httpsOrigin, returnChallengePattern, returnServerPattern, returnStatePattern} from '../platform/portico-account-return';
import {servicesI18n} from './i18n';
import {account as services, accountSession} from './account-session';
import {ServicesError} from './services-api';
import {problemMessage} from './account-problem';
import {AccountSignIn} from './AccountSignIn';
import {useAction} from './AccountSignUp';
import styles from './services.module.css';

/** What a server's own page asked for: read from this page's address, and trusted for nothing shown. */
export type ServerReturnAsk = Readonly<{serverId: string; challenge: string; to: string; state: string}>;
export function readServerReturnAsk(search: string): ServerReturnAsk | undefined {
  const values = new URLSearchParams(search), one = (name: string) => values.getAll(name).length === 1 ? values.get(name)! : undefined;
  const serverId = one('server'), challenge = one('challenge'), to = httpsOrigin(one('to') ?? ''), state = one('state');
  if (!serverId || !returnServerPattern.test(serverId) || !challenge || !returnChallengePattern.test(challenge) || !to || !state || !returnStatePattern.test(state)) return undefined;
  return Object.freeze({serverId, challenge, to, state});
}

/** The listed server this ask names, from the account's signed list, and the listed address that is
 * exactly `to`. The name and address shown come only from here, never from the link. */
export type ListedReturn = Readonly<{name: string; address: string}>;
export type RefusedReturn = Readonly<{refused: 'not_listed'} | {refused: 'address_unknown'; name: string}>;
export function listedReturn(servers: readonly Readonly<{serverId: string; name: string; addresses: readonly string[]}>[], ask: ServerReturnAsk): ListedReturn | RefusedReturn {
  const server = servers.find(entry => entry.serverId === ask.serverId);
  if (!server) return Object.freeze({refused: 'not_listed'});
  const address = server.addresses.map(listed => {try {return new URL(listed).origin;} catch {return '';}}).find(origin => origin === ask.to);
  return address ? Object.freeze({name: server.name, address: new URL(address).host}) : Object.freeze({refused: 'address_unknown', name: server.name});
}

/** `/servers/return`: a server's own HTTPS page sent the person here to sign in with their Portico
 * Account (option 2). After signing in they see which server, at which address, from their signed
 * list, and only Continue asks Portico Services for a note for that address, which goes back in the
 * fragment of the server's own return page. */
export function AccountServerReturn() {
  const {t} = servicesI18n();
  const signedIn = useSyncExternalStore(accountSession.subscribe, accountSession.signedIn);
  const [ask] = useState(() => readServerReturnAsk(location.search));
  if (!ask) return <ServicesAuthStage variant="state">
    <ServicesStateIcon tone="warning" icon="i-warn" />
    <ServicesNotice tone="error">{t('next.account.serverReturn.badLink')}</ServicesNotice>
    <ServicesActions><Button onClick={() => goTo('/servers')}>{t('next.account.serverReturn.chooseServer')}</Button></ServicesActions>
  </ServicesAuthStage>;
  if (!signedIn) return <AccountSignIn next={location.pathname + location.search} lead={t('next.account.signIn.forServerReturn')} />;
  return <ServerReturnConfirm ask={ask} />;
}

type Loaded = Readonly<{username: string; listed: ListedReturn | RefusedReturn}>;
function ServerReturnConfirm({ask}: {ask: ServerReturnAsk}) {
  const {t} = servicesI18n();
  const [loaded, setLoaded] = useState<Loaded>(), [failure, setFailure] = useState<unknown>(), [attempt, setAttempt] = useState(0);
  const {busy, error, run} = useAction();
  useEffect(() => {
    const controller = new AbortController();
    setFailure(undefined); setLoaded(undefined);
    void (async () => {
      const [me, answer] = await Promise.all([services.account(controller.signal), services.serverList(controller.signal)]);
      const list = await openServerList(answer.list as Envelope, me.id, 0, webRouteCrypto);
      if (!controller.signal.aborted) setLoaded({username: me.username, listed: listedReturn(list.servers, ask)});
    })().catch(caught => {if (!controller.signal.aborted) setFailure(caught);});
    return () => controller.abort();
  }, [ask, attempt]);
  if (failure) return <ServicesAuthStage variant="state">
    <ServicesStateIcon tone="warning" icon="i-warn" />
    <ServicesNotice tone="error">{t(problemMessage(failure, 'account'))}</ServicesNotice>
    <ServicesActions><Button onClick={() => setAttempt(value => value + 1)}>{t('next.account.problem.tryAgain')}</Button></ServicesActions>
  </ServicesAuthStage>;
  if (!loaded) return <ServicesAuthStage><></></ServicesAuthStage>;
  const {listed} = loaded;
  // Services refuses an unregistered address too (`return_address_unknown`); this page doesn't ask.
  const name = 'name' in listed ? listed.name : undefined;
  const refused = 'refused' in listed ? listed.refused : error instanceof ServicesError && error.code === 'return_address_unknown' ? 'address_unknown'
    : error instanceof ServicesError && error.code === 'server_unknown' ? 'not_listed' : undefined;
  if (refused) return <ServicesAuthStage variant="state">
    <ServicesStateIcon tone="warning" icon="i-warn" />
    <ServicesNotice tone="error">{refused === 'not_listed' || !name ? t('next.account.serverReturn.notListed') : t('next.account.serverReturn.addressUnknown', {server: name})}</ServicesNotice>
    <ServicesActions>
      {refused === 'address_unknown' && name && <Button variant="primary" onClick={() => goTo(`/servers?open=${encodeURIComponent(ask.serverId)}`)}>{t('next.account.serverReturn.openHere', {server: name})}</Button>}
      <Button onClick={() => goTo('/servers')}>{t('next.account.serverReturn.chooseServer')}</Button>
    </ServicesActions>
  </ServicesAuthStage>;
  const shown = listed as ListedReturn;
  const proceed = run(async () => {
    const answer = await services.serverReturnIdentity(ask.serverId, ask.challenge, ask.to);
    location.replace(accountReturnFinish(ask.to, {identity: answer.identity, state: ask.state}));
  });
  return <ServerReturnCard server={shown.name} address={shown.address} username={loaded.username} busy={busy} error={error ? t(problemMessage(error, 'account')) : undefined}
    onContinue={() => void proceed()} onCancel={() => goTo('/servers')} onNotYou={() => void services.signOut()} />;
}

/** The confirm itself (frame 5's way back): every name on it comes from the signed list or the account. */
export function ServerReturnCard({server, address, username, busy = false, error, onContinue, onCancel, onNotYou}: {
  server: string; address: string; username: string; busy?: boolean; error?: string; onContinue: () => void; onCancel: () => void; onNotYou: () => void;
}) {
  const {t} = servicesI18n(), mark = '\u2063address\u2063';
  const [before = '', after = ''] = t('next.account.serverReturn.title', {server, address: mark}).split(mark);
  return <ServicesAuthStage variant="ownerReview">
    {/* The address is long (a Portico address with its port): it sits on its own smaller line of the heading, unbroken where it fits. */}
    <ServicesHeading>{before}<span className={styles.returnAddress}>{address}{after}</span></ServicesHeading>
    <ServicesLead>{t('next.account.serverReturn.lead')}</ServicesLead>
    <ServicesFacts items={[
      {id: 'server', label: t('next.account.approve.server'), value: server},
      {id: 'account', label: t('next.account.approve.account'), value: username},
    ]} />
    {error ? <ServicesNotice tone="error">{error}</ServicesNotice> : null}
    <ServicesActions>
      <Button variant="primary" loading={busy} onClick={onContinue}>{t('next.account.serverReturn.continue')}</Button>
      <Button disabled={busy} onClick={onCancel}>{t('next.account.serverReturn.cancel')}</Button>
    </ServicesActions>
    <AuthLinks><button type="button" disabled={busy} onClick={onNotYou}>{t('next.account.approve.notYou')}</button></AuthLinks>
  </ServicesAuthStage>;
}
