import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {useRouterState} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {RouteError} from '@core/route-identity.ts';
import {porticoAccountHolder} from '../../platform/portico-account-holder';
import type {PorticoServerAccess} from '../../platform/portico-server-access';
import type {Account} from '@core/portico-account.ts';
import {router} from '../../router';
import {goTo} from '../../platform/hosted-navigation';
import {createBrowserAccountEntry, type BrowserAccountEntry} from '../../platform/account-entry';
import {browserAppSessions} from '../../platform/server-connections';
import {browserAppSessionActions, signOutBrowserPorticoAccount} from '../../platform/app-sessions';
import {ApiError} from '@core/index.ts';
import {ServicesServerDirectory} from '../../kit/accounts/ServerDirectory';
import {ServicesDirectoryStatus} from '../../kit/accounts/DirectoryStatus';
import {HostedError, ServicesAuthStage, ServicesNotice} from '../../kit/accounts/ServicesAuth';
import {ServicesHeader} from '../../kit/accounts/ServicesWorkspace';
import {Dialog} from '../../kit/overlays/Dialog';
import {Button} from '../../kit/controls';
import {DocumentBootReady} from '../../shell/DocumentBootReady';
import {ServicesDirectoryFlow, type DirectoryItem} from '../../services/directory-flow';
import {fallbackFor} from './hosted-fallback';
import {directoryNotice} from './hosted-notice';
import {DirectoryAccountUnavailable} from '../../services/directory-errors';
import {presentError} from '@core/presentation/errors.ts';
import {servicesRelativeTime} from '../../services/time';
import {distributionPage} from '../../services/distribution';
import {ServerlessAccountMenu} from './ServerlessAccountMenu';
import {AccountSignInView} from './AccountSignInView';
import {AccountSignInForm} from './sign-in-form';
import {resumeAfterProfiles} from './chooser-resume';
import {ChooserJoin, ChooserLocalAddress, ChooserStart, chooserIntent, keepHandoffInvitation} from './ChooserStart';
import {ChooserMove} from './ChooserMove';
import {carryInvitation, takeCarriedInvitation, type CarriedInvitation} from '../../platform/invitation-carry';

const t = defaultI18n.t;
type Admitted = Readonly<{entry: BrowserAccountEntry; form: AccountSignInForm; dispose: () => void}>;
type Removal = Readonly<{server: DirectoryItem}>;
/** Hosted refused because the server's claim is no longer the one this row showed. */
const changed = (error: unknown) => error instanceof ApiError && (error.status === 409 || error.status === 412);

/** connect 1-31 at web.getportico.tv: the Portico Account's own servers, opened here. The
 * chosen server's sign-in, profile and PIN run in this page, and Home follows on this same
 * address. The browser is never sent to a server's address unless the person presses the one
 * fallback a state offers. */
export function HostedServersScreen() {
  // Before anything else, and before a sign-in that would lose it: a server page's invitation.
  useState(keepHandoffInvitation);
  const signedIn = useSyncExternalStore(porticoAccountHolder.subscribe, porticoAccountHolder.signedIn);
  const [who, setWho] = useState<Account>(), [error, setError] = useState<unknown>(), [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true; setWho(undefined); setError(undefined);
    if (signedIn) void porticoAccountHolder.who().then(value => {if (live) setWho(value);}, failure => {if (live) setError(failure);});
    return () => {live = false;};
  }, [signedIn, attempt]);
  if (!signedIn) return <Replace to={'/sign-in?next=' + encodeURIComponent('/servers' + location.search)} />;
  if (error) return <DocumentBootReady><ServicesAuthStage><HostedError error={error} /><Button variant="primary" onClick={() => setAttempt(value => value + 1)}>{t('next.states.tryAgain')}</Button></ServicesAuthStage></DocumentBootReady>;
  if (!who) return <ServicesAuthStage><></></ServicesAuthStage>;
  return <HostedChooser key={who.id} who={who} />;
}

/** The account's header, the same on the list and on its start pages. */
function accountHeader(who: Account, signOut: () => void): ReactNode {
  return <ServicesHeader notifications={false} avatar={<ServerlessAccountMenu username={who.username} name={who.displayName || who.username} t={t}
    onAccount={() => goTo('/account')} onJoin={() => goTo('/servers#join')} onSetup={() => goTo('/servers#setup')} onApps={() => location.assign(distributionPage)}
    onHelp={() => window.open('https://getportico.tv/docs/', '_blank', 'noopener,noreferrer')} onSignOut={signOut} />} />;
}
/** `/servers#join`, `#setup` and `#local` open their page in place of the list; an account with
 * no servers yet sees the start (join, set up, or use an address). */
function HostedChooser({who}: {who: Account}) {
  const hash = useRouterState({select: state => state.location.hash});
  const intent = chooserIntent(hash ? '#' + hash.replace(/^#/, '') : '');
  // An invitation carried here (a shared link opened at /join, or pasted on the start) opens Join filled in.
  const [carried, setCarried] = useState<CarriedInvitation | undefined>(takeCarriedInvitation), [empty, setEmpty] = useState(false);
  const back = () => {setCarried(undefined); goTo('/servers', {replace: true});};
  if (carried?.kind === 'move' || carried?.kind === 'ticket') return <ChooserMove who={who} held={carried.kind === 'ticket' ? {ticket: carried.code} : {code: carried.code}}
    initialAddress={carried.address || undefined} onBack={back}
    onNotYou={() => void signOutBrowserPorticoAccount().then(() => {
      carryInvitation(carried.code, carried.address, Date.now(), carried.kind);
      goTo('/sign-in?next=' + encodeURIComponent('/servers'), {replace: true});
    }, () => {})} />;
  if (carried || intent === 'join') return <ChooserJoin who={who} initialCode={carried?.code} initialAddress={carried?.address || undefined} onBack={back} />;
  if (intent === 'local') return <ChooserLocalAddress onBack={back} />;
  if (intent === 'setup' || empty) {
    const signOut = () => void signOutBrowserPorticoAccount().then(() => goTo('/sign-in', {replace: true}), () => {});
    return <ChooserStart header={accountHeader(who, signOut)} intent={intent === 'setup' ? 'setup' : undefined}
      onJoin={(code, address) => {setCarried(Object.freeze({code, address: address?.startsWith('https://') ? address : '', kind: 'invitation' as const})); goTo('/servers#join');}} onLocal={() => goTo('/servers#local')} />;
  }
  return <BoundServers who={who} onEmpty={() => setEmpty(true)} />;
}

function Replace({to}: {to: string}) {
  useLayoutEffect(() => {goTo(to, {replace: true});}, [to]);
  return <ServicesAuthStage><></></ServicesAuthStage>;
}

type Loaded = Readonly<{flow: ServicesDirectoryFlow<Admitted>; saved: ReadonlyMap<string, string>}>;
function BoundServers({who, onEmpty}: {who: Account; onEmpty: () => void}) {
  // The list's own read decides it is empty; the page shown then is the parent's.
  const emptied = useRef(onEmpty); emptied.current = onEmpty;
  // One scope per mounted account (the parent keys on it): the account id binds the list and every
  // server sign-in opened from it.
  const [scope] = useState(() => Object.freeze({accountId: who.id}));
  const [loaded, setLoaded] = useState<Loaded>(), [error, setError] = useState<unknown>(), [attempt, setAttempt] = useState(0);
  useLayoutEffect(() => {
    let live = true;
    const controller = new AbortController(), access = porticoAccountHolder.access(scope.accountId);
    const directory = {read: async (signal: AbortSignal) => {
      const list = await porticoAccountHolder.servers(signal);
      if (list.accountId !== scope.accountId) throw new RouteError('cancelled', 'The Portico Account changed.');
      return list;
    }};
    const current = () => live && access.isCurrent();
    let flow: ServicesDirectoryFlow<Admitted> | undefined;
    // A server just joined opens straight away (the person asked for it), here, in place.
    const opening = new URLSearchParams(location.search).get('open');
    // O-161: a shared link for a server this browser has not joined opens it here, if this account can.
    const linked = new URLSearchParams(location.search).get('link');

    setError(undefined);
    void (async () => {
      try {
        const [initial, record] = await Promise.all([directory.read(controller.signal), browserAppSessions().read()]);
        if (!current()) return;
        if (linked) {
          const item = initial.items.find(value => value.id === linked);
          if (!item) {void router.navigate({to: '/link-unavailable', search: {reason: 'access'}, replace: true}); return;}
        }
        if (!initial.items.length) {emptied.current(); return;}
        // Where each saved sign-in last worked, for the one fallback a state may offer.
        const saved = new Map<string, string>();
        for (const row of record.servers) if (row.signIn === 'portico' && row.connection.hosted?.accountId === scope.accountId && row.connection.routes.lastGood) saved.set(row.connection.pin.serverId, row.connection.routes.lastGood);
        flow = new ServicesDirectoryFlow<Admitted>(initial, {current, read: directory.read, empty: () => {if (current()) emptied.current();},
          admit: (server, signal) => admitServer(server, scope.accountId, access, signal)}, !record.selectedServerId);
        setLoaded({flow, saved});
        const asked = opening || linked ? initial.items.find(item => item.id === (opening ?? linked)) : undefined;
        // A server's own page handed over a server this account can't use: said, never an empty open.
        if (opening && !asked) {void router.navigate({to: '/link-unavailable', search: {reason: 'access'}, replace: true}); return;}
        if (opening || linked) goTo('/servers', {replace: true});
        if (asked) void flow.open(asked, flow.getSnapshot());
      } catch (failure) {if (current()) setError(failure);}
    })();
    return () => {live = false; controller.abort(); flow?.getSnapshot().admitted?.dispose(); flow?.dispose();};
  }, [scope, attempt]);
  if (error) return <DocumentBootReady><ServicesAuthStage><HostedError error={error} /><Button variant="primary" onClick={() => setAttempt(value => value + 1)}>{t('next.states.tryAgain')}</Button></ServicesAuthStage></DocumentBootReady>;
  if (!loaded) return <ServicesAuthStage><></></ServicesAuthStage>;
  return <DocumentBootReady><Chooser who={who} flow={loaded.flow} saved={loaded.saved} /></DocumentBootReady>;
}

/** The existing sign-in state machine opens the server. A server this browser already holds a
 * sign-in for is selected again (no new sign-in or device); any other is signed in to here with
 * the Portico Account, its routes and key proved first. */
async function admitServer(server: DirectoryItem, accountId: string, access: PorticoServerAccess, signal: AbortSignal): Promise<Admitted> {
  const entry = createBrowserAccountEntry(), form = new AccountSignInForm(entry);
  let disposed = false;
  const dispose = () => {if (disposed) return; disposed = true; try {form.dispose();} finally {entry.dispose();}};
  const abort = () => dispose();
  signal.addEventListener('abort', abort, {once: true});
  try {
    const record = await browserAppSessions().read(); signal.throwIfAborted();
    const saved = record.servers.find(row => row.signIn === 'portico' && row.connection.pin.serverId === server.id && row.connection.pin.publicKey === server.publicKey && row.connection.hosted?.accountId === accountId);
    if (saved) {
      await browserAppSessionActions().switchServer(server.id, () => !signal.aborted && access.isCurrent(), record.revision); signal.throwIfAborted();
      await entry.loadProfiles(); signal.throwIfAborted();
      const plan = await resumeAfterProfiles(entry, () => browserAppSessions().selected(), server.id, saved.connection.logicalOrigin, () => !signal.aborted);
      signal.throwIfAborted();
      const result = entry.getSnapshot();
      if (plan === 'entry' || plan === 'inspected' && (result.snapshot || result.server)) return {entry, form, dispose};
      throw result.error ?? new RouteError('route_unavailable', 'The server did not answer.');
    }
    const immutable = Object.freeze({id: server.id, name: server.name, publicKey: server.publicKey, baseUrl: server.baseUrl});
    const opened = await entry.openHostedServer(immutable, access, record.revision); signal.throwIfAborted();
    if (!opened) throw entry.getSnapshot().error ?? new RouteError('cancelled', 'The server opening was replaced.');
    return {entry, form, dispose};
  } catch (failure) {dispose(); throw failure;}
  finally {signal.removeEventListener('abort', abort);}
}

function Chooser({who, flow, saved}: {who: Account; flow: ServicesDirectoryFlow<Admitted>; saved: ReadonlyMap<string, string>}) {
  const state = useSyncExternalStore(flow.subscribe, flow.getSnapshot);
  const [clock, setClock] = useState(Date.now), [removing, setRemoving] = useState<Removal>(), [removeFailure, setRemoveFailure] = useState<unknown>(), [working, setWorking] = useState(false), [failure, setFailure] = useState<unknown>();
  useLayoutEffect(() => {
    const update = () => {if (document.visibilityState === 'visible') setClock(Date.now());};
    const timer = setInterval(update, 15000), grace = setTimeout(update, 40000);
    document.addEventListener('visibilitychange', update);
    return () => {clearInterval(timer); clearTimeout(grace); document.removeEventListener('visibilitychange', update);};
  }, [flow]);
  const blocked = state.status === 'identity' || state.status === 'clock' || state.status === 'update';
  // The signed list says nothing about whether a server is up, so the list is never "all offline".
  const allOffline = false;
  // Retry only the server being waited for, while this page is visible (H01/H11).
  useLayoutEffect(() => {
    let live = true, timer: ReturnType<typeof setTimeout> | undefined;
    const reconcile = () => {
      clearTimeout(timer);
      if (!live) return;
      if (document.visibilityState !== 'visible') {flow.cancelAutomatic(); return;}
      flow.automaticInitial();
      const snapshot = flow.getSnapshot();
      // Every server quiet and none chosen: read the account's list again so a server that comes
      // back shows as online ("Checking again in…"), without opening anything by itself.
      const recheck = !snapshot.waitingId && allOffline;
      if (snapshot.phase !== 'idle' || blocked || !snapshot.waitingId && !recheck) return;
      const deadline = Date.now() + (recheck ? 20 : flow.retryDelay()) * 1000;
      const tick = () => {
        const now = flow.getSnapshot();
        if (!live || document.visibilityState !== 'visible' || now.phase !== 'idle' || now.waitingId !== snapshot.waitingId || now.items !== snapshot.items) return;
        const remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000)); flow.countdown(remaining);
        if (!remaining) {if (recheck) void flow.refresh(now); else flow.automatic();} else timer = setTimeout(tick, Math.min(1000, deadline - Date.now()));
      };
      tick();
    };
    document.addEventListener('visibilitychange', reconcile); reconcile();
    return () => {live = false; clearTimeout(timer); document.removeEventListener('visibilitychange', reconcile);};
  }, [flow, state.phase, state.waitingId, state.items, blocked, allOffline]);
  const name = who.displayName || who.username;
  const signOut = () => {setWorking(true); void signOutBrowserPorticoAccount().then(() => goTo('/sign-in', {replace: true}), failure => {setWorking(false); setFailure(failure);});};
  const header = accountHeader(who, signOut);
  const admitted = state.phase === 'opened' ? state.admitted : undefined;
  if (admitted) return <AccountSignInView entry={admitted.entry} form={admitted.form} initialize={false} onCancel={() => flow.cancelOpening(flow.getSnapshot())} />;
  const target = state.target;
  const connecting = flow.inConnectingGrace(clock);
  const transient = state.status === 'waiting' || state.status === 'unreachable';
  if (target && state.showTarget && (state.phase === 'opening' || state.status)) {
    const lastSeen = target.presence.lastSeenAt ? defaultI18n.date(target.presence.lastSeenAt) : undefined;
    return <ServicesDirectoryStatus header={header} phase={connecting && transient || state.phase === 'opening' ? 'opening' : state.status!}
      server={target.name} lastSeen={lastSeen} owner={target.ownedByMe} seconds={state.seconds} fallback={fallbackFor(state, target, saved, address => location.assign(address))}
      error={<Notice notice={directoryNotice(state, 'frame', target.name)} />}
      onCheck={() => {void flow.open(target, flow.getSnapshot());}} onChoose={() => flow.cancelOpening(flow.getSnapshot())} />;
  }
  const checked = clock - state.checkedAt < 45000 ? t('services.directory.justNow') : servicesRelativeTime(defaultI18n, new Date(state.checkedAt).toISOString(), clock);
  // The exact claim this row showed and one operation id, captured when the dialog opens; a retry
  // of this dialog sends both again, unchanged (P2-66 D-3).
  const startRemoval = (item: DirectoryItem) => {setRemoveFailure(undefined); setRemoving(Object.freeze({server: item}));};
  const remove = async (removal: Removal) => {
    const {server} = removal;
    setWorking(true); setRemoveFailure(undefined);
    try {
      if (server.ownedByMe) await porticoAccountHolder.remove(server.id);
      else await porticoAccountHolder.leave(server.id);
      await forgetSaved(server.id);
      setRemoving(undefined); flow.forget(server.id, flow.getSnapshot());
    } catch (error) {setRemoveFailure(error);}
    finally {setWorking(false);}
  };
  return <>
    <ServicesServerDirectory header={header} signedInAs={name} checked={checked}
      servers={state.items.map(item => ({id: item.id, name: item.name, ownedByMe: item.ownedByMe}))}
      offline={allOffline} connecting={allOffline && connecting} seconds={state.seconds} pendingId={state.pendingId} busy={state.phase === 'reading' || working}
      error={failure ? <HostedError error={failure} /> : <Notice notice={connecting && transient ? undefined : directoryNotice(state, 'list', target?.name)} />}
      onOpen={id => {const item = state.items.find(value => value.id === id); if (item) void flow.open(item, state);}} onRefresh={() => void flow.refresh(state)}
      onAccount={() => goTo('/account')} onJoin={() => goTo('/servers#join')} onSetup={() => goTo('/servers#setup')} onLocal={() => goTo('/servers#local')}
      onDifferentAccount={signOut} onRemove={id => {const item = state.items.find(value => value.id === id); if (item) startRemoval(item);}} />
    {removing && <Dialog open onOpenChange={open => {if (!open && !working) setRemoving(undefined);}} size="sm" role="alertdialog"
      title={t(removing.server.ownedByMe ? 'web.chooser.removeTitle' : 'web.chooser.leaveTitle', {server: removing.server.name})}
      footer={<><Button onClick={() => setRemoving(undefined)} disabled={working}>{t('accounts.profiles.cancel')}</Button>
        <Button variant="primary" loading={working} onClick={() => void remove(removing)}>{t(removing.server.ownedByMe ? 'web.chooser.removeServer' : 'web.chooser.leaveServer')}</Button></>}>
      <p>{t(removing.server.ownedByMe ? 'web.chooser.removeBody' : 'web.chooser.leaveBody')}</p>
      {changed(removeFailure) ? <ServicesNotice tone="error">{t('web.chooser.removeChanged')}</ServicesNotice> : <HostedError error={removeFailure} />}
    </Dialog>}
  </>;
}

/** Account notices use the Portico Account presenter; a server's own failure is worded as that
 * server's, never as the account's (H12). */
function Notice({notice}: {notice: ReturnType<typeof directoryNotice>}) {
  if (!notice) return null;
  if (notice.source === 'account') return <HostedError error={notice.error instanceof DirectoryAccountUnavailable ? notice.error.cause ?? notice.error : notice.error} />;
  const result = presentError(notice.error, 'sign-in', {operation: 'load', service: 'server'});
  if (result.silent) return null;
  return <ServicesNotice tone="error">{notice.server && result.messageId === 'error.unreachable' ? t('web.signIn.error.unreachable', {server: notice.server}) : result.body}</ServicesNotice>;
}
/** The server no longer lists this account: this browser drops its sign-in there too. */
async function forgetSaved(serverId: string) {
  const record = await browserAppSessions().read();
  for (const row of record.servers) if (row.connection.pin.serverId === serverId) await browserAppSessionActions().forgetServer(row.connection.session);
}
