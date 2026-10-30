import {useLayoutEffect, useCallback, useMemo, useRef, useState} from 'react';
import {goTo} from '../platform/hosted-navigation';
import type {HostedSession} from '@core/index.ts';
import {parseJoinLink, type JoinLink} from '@core/join-link.ts';
import {DocumentBootReady} from '../shell/DocumentBootReady';
import {StateView} from '../kit/states';
import {ServicesAuthStage, HostedError, ServicesNotice} from '../kit/accounts/ServicesAuth';
import {ServicesHeader, ServicesStart} from '../kit/accounts/ServicesWorkspace';
import {ServerlessAccountMenu} from '../screens/accounts/ServerlessAccountMenu';
import {ServicesLocalServerPage} from './DirectoryPage';
import {ServicesInvitationDialog, ShortJoinPage} from './JoinPage';
import {parseShortInvitationCode} from '@core/short-invitation-code.ts';
import {distributionPage} from './distribution';
import {servicesI18n} from './i18n';
import {useMembershipEnding} from './use-membership-ending';
import {accountIdentity, type ServicesAccount} from './session';
import {workspaceApi, type WorkspaceServers} from './workspace-api';

/** The only section anchors Home accepts as intent; every other fragment stays inert. */
export function sectionIntent(hash: string): 'join' | 'setup' | 'local' | undefined {
  return hash === '#join' ? 'join' : hash === '#setup' ? 'setup' : hash === '#local' ? 'local' : undefined;
}

/** Consume a supported anchor once, preserving the query, history state and any
 * unrelated fragment handling. Refuses once the original Home path or account is
 * replaced, so a retired page can never consume or navigate. */
function takeSectionIntent(original: Readonly<{pathname: string; current: () => boolean}>): 'join' | 'setup' | 'local' | undefined {
  if (typeof location === 'undefined' || !original.current() || location.pathname !== original.pathname) return;
  const intent = sectionIntent(location.hash);
  if (intent) history.replaceState(history.state, '', location.pathname + location.search);
  return intent;
}

/** An empty directory is the drawn welcome, with no Server or library bootstrap. */
export function ServicesHomePage({account, session}: {account: ServicesAccount; session: Omit<HostedSession, 'refreshToken'>}) {
  const identity = accountIdentity(session);
  return <BoundHome key={identity.accountId + '/' + identity.familyId} account={account} session={session} />;
}
function BoundHome({account, session}: {account: ServicesAccount; session: Omit<HostedSession, 'refreshToken'>}) {
  const {t} = useMemo(servicesI18n, []);
  const accountId = session.account.id, familyId = session.familyId!;
  const api = useMemo(() => workspaceApi(account, {accountId, familyId}), [account, accountId, familyId]);
  type Read = {api: typeof api; attempt: number; active: boolean; controller: AbortController; value?: WorkspaceServers};
  const [loaded, setLoaded] = useState<Read>(), [error, setError] = useState<unknown>(), [attempt, retry] = useState(0);
  const live = useRef<Read | null>(null);
  const [intent,setIntent]=useState<'join'|'setup'|'local'>();
  const [reveal,setReveal]=useState(0);
  // Same-document anchor navigation keeps working without remounting Home; only the
  // two supported sections become intent, and each is consumed once.
  useLayoutEffect(() => {
    // This exact home instance only: a replayed or retired page never consumes the
    // anchor, and the check is rebuilt from the captured original scalars.
    let alive = true;
    const pathname = location.pathname;
    const original = {pathname, current: () => {
      if (!alive) return false;
      if (location.pathname !== pathname) return false;
      const current = account.service.getSnapshot().session;
      return Boolean(current?.familyId && current.account.id === accountId && current.familyId === familyId);
    }};
    const consume = () => {
      const next = takeSectionIntent(original);
      // The URL may have been replaced while scrubbing; do not publish intent from a
      // page or family that is no longer the original one.
      if (!next || !original.current()) return;
      setIntent(next); setReveal(value => value + 1);
    };
    consume(); // cold load: only after the original family/path is confirmed
    addEventListener('hashchange', consume);
    return () => {alive = false; removeEventListener('hashchange', consume);};
  }, [account, accountId, familyId]);
  useLayoutEffect(() => {
    const read: Read = {api, attempt, active: true, controller: new AbortController()}; live.current = read; setError(undefined);
    void api.servers(read.controller.signal).then(value => {
      if (read.active && live.current === read && !read.controller.signal.aborted) {read.value = value; setLoaded(read);}
    }, value => {if (read.active && live.current === read && !read.controller.signal.aborted) setError(value);});
    return () => {read.active = false; if (live.current === read) live.current = null; read.controller.abort();};
  }, [api, attempt]);
  const sourceCurrent = useCallback(() => Boolean(loaded && loaded.api === api && loaded.attempt === attempt && loaded.active && live.current === loaded && !loaded.controller.signal.aborted), [loaded, api, attempt]);
  const servers = sourceCurrent() ? loaded?.value : undefined;
  const localCurrent=useCallback(()=>{const current=account.service.getSnapshot().session;return Boolean(sourceCurrent()&&current?.familyId&&current.account.id===accountId&&current.familyId===familyId);},[account,accountId,familyId,sourceCurrent]);
  if (error) return <DocumentBootReady><StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => retry(value => value + 1)}} /></DocumentBootReady>;
  if (!servers) return <ServicesAuthStage><></></ServicesAuthStage>;
  if(intent==='local')return <ServicesLocalServerPage current={localCurrent} onBack={()=>{if(servers.items.length)goTo('/servers',{replace:true});else setIntent(undefined);}} />;
  if(!servers.items.length||intent==='join'||intent==='setup')return <DocumentBootReady><Start account={account} session={session} servers={servers} sourceCurrent={sourceCurrent} initialSection={intent} reveal={reveal} /></DocumentBootReady>;
  return <OpenChooser />;
}
/** The account has servers: the chooser is the app's own (connect 1), on this same address,
 * so opening a server lands on its Home here and never sends the browser to the server. */
function OpenChooser() {
  useLayoutEffect(() => {goTo('/servers', {replace: true});}, []);
  return <ServicesAuthStage><></></ServicesAuthStage>;
}
function Start({account, session, servers, sourceCurrent, initialSection, reveal = 0}: {account: ServicesAccount; session: Omit<HostedSession, 'refreshToken'>; servers: WorkspaceServers; sourceCurrent: () => boolean; initialSection?: 'join'|'setup'; reveal?: number}) {
  const {t} = useMemo(servicesI18n, []);
  const showEnding = useMembershipEnding(account, accountIdentity(session), servers.membershipEnding, sourceCurrent);
  type InvitationOpening = Readonly<{link: JoinLink; pasted: string}>;
  const [invitation, setInvitation] = useState(''), [joinState, setJoinState] = useState<{opening?: InvitationOpening; expired?: boolean}>({}), [shortJoin, setShortJoin] = useState<string>();
  const opening = joinState.opening;
  const [error, setError] = useState<unknown>(), [invalid, setInvalid] = useState(false);
  const inspect = (input = invitation) => {
    const parsed = parseJoinLink(input.trim()), short = parseShortInvitationCode(input);
    setInvalid(!parsed && !short); setError(undefined);
    setShortJoin(short ? input : undefined);
    setJoinState(parsed ? {opening: Object.freeze({link: parsed, pasted: input.trim()})} : {});
  };
  const run = (work: () => Promise<void>) => { void Promise.resolve().then(work).catch(setError); };
  const download = () => location.assign(distributionPage);
  const scrollToCard = (id: 'join' | 'setup' | 'apps') => {
    const target = document.getElementById(id);
    const visible = id === 'setup' && !target?.getClientRects().length ? document.getElementById('setup-details') : target;
    visible?.scrollIntoView({block: 'start', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth'});
  };
  // One effect: a new supported anchor and a repeated same anchor both scroll the
  // authored card; the bounded witness never remounts Start, so its drafts survive.
  useLayoutEffect(()=>{if(initialSection)scrollToCard(initialSection);},[initialSection, reveal]);
  if (shortJoin !== undefined) return <ShortJoinPage account={account} initialCode={shortJoin} onBack={() => setShortJoin(undefined)} />;
  const name = session.account.displayName || session.account.username;
  const ending = servers.membershipEnding;
  return <><ServicesStart returning={Boolean(ending)||Boolean(initialSection)} notice={showEnding.visible && ending ? <div ref={showEnding.mount}><ServicesNotice tone="info"><b>{t('services.start.endedTitle', {server: ending.serverName ?? t('services.start.thisServer')})}</b> {t('web.chooser.refusedBody')}</ServicesNotice></div> : undefined} header={<ServicesHeader notifications={false} avatar={<ServerlessAccountMenu username={session.account.username} name={name} t={t}
    onAccount={() => goTo('/account')} onJoin={() => scrollToCard('join')} onSetup={() => scrollToCard('setup')} onApps={() => scrollToCard('apps')}
    onHelp={() => window.open('https://getportico.tv/docs/', '_blank', 'noopener,noreferrer')}
    onSignOut={() => run(async () => {await account.service.logoutIfCurrent(accountIdentity(session));})} />} />}
    invitation={invitation} onInvitationChange={value => { setInvitation(value); setInvalid(false); setJoinState(previous => previous.expired ? {} : previous); }} onJoin={() => inspect()}
    onInvitationPaste={value => { setInvitation(value); inspect(value); }}
    onPaste={() => run(async () => { const value = await navigator.clipboard.readText(); setInvitation(value); inspect(value); })}
    onDownload={download} onApps={download} onCopyDownload={() => run(() => navigator.clipboard.writeText(distributionPage))}
    onShareDownload={() => run(async () => { if (navigator.share) await navigator.share({url: distributionPage}); else await navigator.clipboard.writeText(distributionPage); })}
    error={invalid || joinState.expired ? <ServicesNotice tone="error">{t(joinState.expired ? 'accounts.invitation.expiredBody' : 'web.link.invalid')}</ServicesNotice> : <HostedError error={error} />} />
    {opening ? <ServicesInvitationDialog account={account} link={opening.link} pasted={opening.pasted}
      onBack={() => setJoinState(previous => previous.opening === opening ? {} : previous)}
      onUnavailable={() => {
        const current = account.service.getSnapshot().session;
        if (!sourceCurrent() || !current || current.account.id !== session.account.id || current.familyId !== session.familyId) return;
        setJoinState(previous => previous.opening === opening ? {expired: true} : previous);
      }} /> : null}</>;
}
