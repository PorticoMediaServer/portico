import {createContext, useContext, useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {goTo} from '../platform/hosted-navigation';
import {createRootRouteWithContext, createRoute, createRouter, Outlet, RouterProvider, type RouterHistory} from '@tanstack/react-router';
import {registerInPageNavigation} from '../platform/hosted-navigation';
import type {JoinLink} from '@core/join-link.ts';
import {IconSprite} from '../kit/Icon';
import {StateView} from '../kit/states';
import {DocumentBootReady} from '../shell/DocumentBootReady';
import {ServicesAuthStage, ServicesActions, ServicesHeading, ServicesLead, ServicesNotice, ServicesStateIcon} from '../kit/accounts/ServicesAuth';
import {Button} from '../kit/controls';
import {servicesAccount, type ServicesAccount} from './session';
import {ServicesBootstrap} from './bootstrap';
import {useAccountResource} from './use-account-resource';
import {servicesLinkStore, type LinkCapture} from './intake';
import {servicesI18n} from './i18n';
import {ServicesAuthForm} from './AuthForm';
import {DevicePage} from './DevicePage';
import {ResetPage, VerifyPage} from './LinkPages';
import {AccountPage} from './AccountPage';
import {SignedOutRecoveryCodes} from './SecurityPanel';
import {JoinPage, ShortJoinPage} from './JoinPage';
import {ServicesHomePage} from './HomePage';
import {RegisteredSignInPage} from './SignInPage';
import type {SignInCapture} from './sign-in';
import type {HostedOwnerClaimCapture} from './owner-claim';
import {hostedClaimExpired} from './owner-pending';
import {claimStartView} from './claim-start';
import {OwnerApprovalPage} from './OwnerApprovalPage';
import {SetupApprovalPage} from './SetupApprovalPage';
import {HostedSetupApproval} from '../screens/setup/HostedSetupApproval';

type Context = {providerReturn?: boolean; account: ServicesAccount; bootstrap: ServicesBootstrap; invalidContinuation: boolean; providerFailure?: 'google' | 'apple' | 'unknown'; join?: JoinLink; plainShortJoin?: boolean; identityTransaction?: string};
const ProviderFailureContext = createContext<Context['providerFailure']>(undefined);
const root = createRootRouteWithContext<Context>()({component: ServicesRoot, notFoundComponent: InvalidLink});
const home = createRoute({getParentRoute: () => root, path: '/', component: HomeRoute});
// The app's own sign-in address at web.getportico.tv is the Portico Account sign-in (P2-66).
const signIn = createRoute({getParentRoute: () => root, path: '/sign-in', component: HomeRoute});
// Join and Set up inside the app at web.getportico.tv, where `/` is a server's Home (H14).
const welcome = createRoute({getParentRoute: () => root, path: '/welcome', component: HomeRoute});
// Inside the app every other address is the app's own; these pages draw nothing there.
const elsewhere = createRoute({getParentRoute: () => root, path: '$', component: () => null});
/** The account pages' own addresses, opened in place by either router. */
export const servicesRoutePaths: ReadonlySet<string> = new Set(['/', '/sign-in', '/welcome', '/account', '/device', '/reset', '/reset-password', '/verify-email', '/claim', '/join']);
const workspace = createRoute({getParentRoute: () => root, path: '/account', component: AccountRoute,
  validateSearch: (value: Record<string, unknown>) => {
    const transaction = typeof value.identityTransaction === 'string' && /^[A-Za-z0-9_-]{1,160}$/.test(value.identityTransaction) ? value.identityTransaction : undefined;
    return {tab: transaction ? 'security' : value.tab === 'security' || value.tab === 'servers' || value.tab === 'notifications' ? value.tab : 'devices', ...(transaction ? {identityTransaction: transaction} : {})};
  }});
const device = createRoute({getParentRoute: () => root, path: '/device', component: DeviceRoute});
const reset = createRoute({getParentRoute: () => root, path: '/reset', component: ResetRoute});
const resetPassword = createRoute({getParentRoute: () => root, path: '/reset-password', component: ResetRoute});
const verify = createRoute({getParentRoute: () => root, path: '/verify-email', component: VerifyRoute});
const invitation = createRoute({getParentRoute: () => root, path: '/join', component: JoinRoute});
// Owner continuation stays separate from the ordinary signed sign-in root.
const claim = createRoute({getParentRoute: () => root, path: '/claim', component: ClaimStart});
type ServicesRouter = ReturnType<typeof makeRouter>;
function makeRouter(context: Context, history?: RouterHistory) {
  const routes = [home, signIn, welcome, workspace, device, reset, resetPassword, verify, claim, invitation];
  return createRouter({...(history ? {history} : {}), routeTree: root.addChildren(history ? [...routes, elsewhere] : routes), context});
}
// This router's types stay local. The server app remains the only global TanStack Register.
export function ServicesApp({capture, invalidContinuation, join, plainShortJoin, signIn, owner, identityTransaction, providerFailure, providerReturn, history}: {history?: RouterHistory; capture?: LinkCapture; invalidContinuation?: boolean; join?: JoinLink; plainShortJoin?: boolean; signIn?: SignInCapture; owner?: HostedOwnerClaimCapture; identityTransaction?: string; providerFailure?: 'google' | 'apple' | 'unknown'; providerReturn?: boolean} = {}) {
  // main.tsx captures and scrubs intake once, before React. A discarded render
  // must not reread a scrubbed URL or allocate a terminal bootstrap controller.
  const create = useCallback(() => {
    const account = servicesAccount(), bootstrap = new ServicesBootstrap(account, servicesLinkStore, capture, {origin: location.origin, capture: signIn},
      location.pathname === '/claim' ? {origin: location.origin, capture: owner} : undefined);
    return {account, bootstrap, embedded: Boolean(history), router: makeRouter({account, bootstrap, invalidContinuation: invalidContinuation === true, join, plainShortJoin, identityTransaction, providerFailure, providerReturn}, history), dispose: () => bootstrap.dispose()};
  }, [capture, invalidContinuation, join, plainShortJoin, signIn, owner, identityTransaction, providerFailure, providerReturn, history]);
  const bound = useAccountResource(create);
  return <><IconSprite />{bound ? <MountedServicesRouter key={bound.id} value={bound.value} /> : <ServicesAuthStage><></></ServicesAuthStage>}</>;
}
function MountedServicesRouter({value}: {value: {bootstrap: ServicesBootstrap; router: ServicesRouter; embedded: boolean}}) {
  useLayoutEffect(() => {void value.bootstrap.start();}, [value]);
  // Opened alone (a captured link), the account pages still change pages in place; the app's
  // own pages are a page load away. Inside the app, the app's router owns this.
  useLayoutEffect(() => value.embedded ? undefined : registerInPageNavigation((to, replace) => {
    if (servicesRoutePaths.has(new URL(to, location.origin).pathname)) {if (replace) value.router.history.replace(to); else value.router.history.push(to);}
    else if (replace) location.replace(to); else location.assign(to);
  }), [value]);
  return <RouterProvider router={value.router} />;
}
function ServicesRoot() {
  const {account, bootstrap, invalidContinuation, providerFailure: capturedFailure, providerReturn} = root.useRouteContext<ServicesRouter>();
  const session = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot).session;
  const state = useSyncExternalStore(bootstrap.subscribe, bootstrap.getSnapshot);
  const failureOpening = useRef<{familyId?: string; retired: boolean} | undefined>(undefined);
  if (state.phase === 'ready' && !failureOpening.current) failureOpening.current = {familyId: session?.familyId, retired: false};
  if (session && failureOpening.current && session.familyId !== failureOpening.current.familyId) failureOpening.current.retired = true;
  // The callback's display hint belongs to this opening; a successful sign-in
  // (including email or another tab) retires it without replacing that family.
  const providerFailure = failureOpening.current?.retired ? undefined : capturedFailure;
  const {t} = servicesI18n();
  if (state.phase === 'restoring') return <ServicesAuthStage><></></ServicesAuthStage>;
  // A signed-out account is a resolved opening too. Public email links and the
  // sign-in shell must retire the same static first paint as the server app.
  const waitingForAuth = providerReturn || !session && (location.pathname === '/' || location.pathname === '/sign-in' || location.pathname === '/account');
  return <ProviderFailureContext.Provider value={providerFailure}><DocumentBootReady ready={Boolean(!waitingForAuth || state.phase === 'error' || providerFailure && session || invalidContinuation || state.invalidSignIn || state.invalidOwner)}>{state.phase === 'error'
    ? <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
      primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => { void bootstrap.start(); }}} />
    : providerFailure && session ? <ServicesAuthStage variant="state"><ServicesHeading>{t('auth.welcomeHome')}</ServicesHeading><ServicesNotice tone="error">{providerFailure === 'unknown' ? t('web.link.invalid') : t('services.provider.stopped', {provider: t(providerFailure === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')})}</ServicesNotice><ServicesActions><Button variant="primary" onClick={() => location.assign(location.pathname === '/account' ? '/account?tab=security' : '/')}>{t('action.continue')}</Button></ServicesActions></ServicesAuthStage>
    : state.invalidOwner === 'expired' ? <SetupExpired />
    : invalidContinuation || state.invalidSignIn || state.invalidOwner ? <InvalidLink />
    : state.signIn && location.pathname === '/' ? <RegisteredSignInPage account={account} pending={state.signIn} providerFailure={providerFailure} />
    : <Outlet />}</DocumentBootReady></ProviderFailureContext.Provider>;
}
function useAccount() {
  const {account} = root.useRouteContext<ServicesRouter>();
  const state = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot);
  return {account, session: state.session, ended: state.phase === 'signedOut' && Boolean(state.error)};
}
function HomeRoute() {
  const providerFailure = useContext(ProviderFailureContext);
  const {account, session, ended} = useAccount();
  const {t} = servicesI18n();
  const [authenticating, setAuthenticating] = useState(() => !session);
  useEffect(() => { if (!session) setAuthenticating(true); }, [session]);
  // Explicit recovery/create links do not resume a different transaction from the cookie.
  const mode = new URLSearchParams(location.search).get('mode');
  if (!session || authenticating) return <ServicesAuthForm account={account} providerFailure={providerFailure} before={ended ? <ServicesNotice>{t('services.signin.ended')}</ServicesNotice> : undefined} initialMode={mode === 'forgot' || mode === 'create' ? mode : 'signin'} onSignedIn={() => setAuthenticating(false)} />;
  return <ServicesHomePage account={account} session={session} />;
}
function AccountRoute() {
  const {identityTransaction} = root.useRouteContext<ServicesRouter>();
  const providerFailure = useContext(ProviderFailureContext);
  const {account, session} = useAccount(), {tab} = workspace.useSearch<ServicesRouter>();
  const [authenticating, setAuthenticating] = useState(() => !session), [deleted, setDeleted] = useState(false);
  const {t} = servicesI18n();
  const [securityEnded, setSecurityEnded] = useState<{codes?: readonly string[]}>();
  useEffect(() => {if (session) {setDeleted(false);setSecurityEnded(undefined);}}, [session]);
  useEffect(() => { if (!session) setAuthenticating(true); }, [session]);
  if (securityEnded?.codes?.length && !session) return <SignedOutRecoveryCodes codes={securityEnded.codes} onDone={() => setSecurityEnded({})}/>;
  if (deleted && !session) return <ServicesAuthStage><ServicesStateIcon/><ServicesHeading>{t('web.deleteAccount.doneTitle')}</ServicesHeading>
    <ServicesLead>{t('web.deleteAccount.doneBody')}</ServicesLead><ServicesActions><Button variant="glass" onClick={() => {setDeleted(false);goTo('/');}}>{t('web.link.backToPortico')}</Button></ServicesActions></ServicesAuthStage>;
  if (!session || authenticating) return <ServicesAuthForm account={account} providerFailure={providerFailure} before={securityEnded ? <ServicesNotice>{t('services.signin.ended')}</ServicesNotice> : undefined} onSignedIn={() => {setSecurityEnded(undefined);setAuthenticating(false);}} />;
  return <AccountPage identityTransaction={identityTransaction} account={account} session={session} section={tab as 'devices' | 'security' | 'servers' | 'notifications'} onDeleted={() => setDeleted(true)} onSecurityEnded={codes => setSecurityEnded({...(codes ? {codes: Object.freeze([...codes])} : {})})} />;
}
function DeviceRoute() {
  const {account, bootstrap} = root.useRouteContext<ServicesRouter>(), link = bootstrap.getSnapshot().link;
  return <DevicePage account={account} initialCode={link?.kind === 'device' ? link.value : undefined} />;
}
function ResetRoute() {
  const {account, bootstrap} = root.useRouteContext<ServicesRouter>(), link = bootstrap.getSnapshot().link;
  return <ResetPage account={account} token={link?.kind === 'reset' ? link.value : undefined} />;
}
function VerifyRoute() {
  const {account, bootstrap} = root.useRouteContext<ServicesRouter>(), link = bootstrap.getSnapshot().link;
  return <VerifyPage account={account} token={link?.kind === 'verify-email' ? link.value : undefined} />;
}
function JoinRoute() {
  const {account, join, plainShortJoin} = root.useRouteContext<ServicesRouter>();
  return join ? <JoinPage account={account} link={join} onBack={() => goTo('/')} />
    : plainShortJoin ? <ShortJoinPage account={account} onBack={() => goTo('/')} /> : <InvalidLink />;
}
function InvalidLink() {
  const {t} = servicesI18n();
  return <DocumentBootReady><ServicesAuthStage variant="reset"><ServicesHeading>{t('auth.welcomeHome')}</ServicesHeading><ServicesNotice>{t('web.link.invalid')}</ServicesNotice>
    <ServicesActions><Button onClick={() => goTo('/')}>{t('web.link.backToPortico')}</Button></ServicesActions></ServicesAuthStage></DocumentBootReady>;
}
export {InvalidLink as ServicesInvalidLink};
function SetupExpired() {
  const {t} = servicesI18n();
  return <DocumentBootReady><ServicesAuthStage variant="reset"><ServicesHeading>{t('auth.welcomeHome')}</ServicesHeading><ServicesNotice>{t('web.claim.setupExpired')}</ServicesNotice>
    <ServicesActions><Button onClick={() => goTo('/')}>{t('web.link.backToPortico')}</Button></ServicesActions></ServicesAuthStage></DocumentBootReady>;
}
function ClaimStart() {
  const {account, bootstrap} = root.useRouteContext<ServicesRouter>(), {session} = useAccount();
  const {t} = servicesI18n();
  // Signing in from the verification email in another tab puts the session here too: carry on to
  // the approval page, which still shows the account and waits for an explicit Approve.
  const view = claimStartView({ownerPending: Boolean(bootstrap.getSnapshot().ownerPending), signedIn: Boolean(session)});
  if (view === 'form') return <ServicesAuthForm account={account} heading={t('auth.welcomeHome')} lead={t('web.claim.signInFirst')} onSignedIn={() => {}} />;
  if (view === 'approval') return <ClaimApprovalRoute account={account} bootstrap={bootstrap} />;
  return <ServicesAuthStage variant="reset"><ServicesHeading>{t('web.claim.titleGeneric')}</ServicesHeading><ServicesNotice tone="info">{t('web.claim.openFromServer')}</ServicesNotice>
    <ServicesActions><Button onClick={() => goTo('/')}>{t('web.claim.backToPortico')}</Button></ServicesActions></ServicesAuthStage>;
}

/** The scrubbed code is an intake locator only. An authenticated server lookup
 * chooses the drawn setup/switch view; its purpose-closed controller then
 * independently rechecks every decision and return against the current family. */
type ApprovalPurpose='setup'|'switch';
type ApprovalOpening={scope:string;account:ServicesAccount;bootstrap:ServicesBootstrap;identity:{accountId:string;familyId:string;accessToken:string};controller:AbortController;pending?:AbortController;renewing:boolean;renewed:boolean;alive:boolean;settled?:number};
type ApprovalShown=
 | Readonly<{state:'lookup';opening:ApprovalOpening;attempt:number}>
 | Readonly<{state:'purpose';opening:ApprovalOpening;attempt:number;purpose:ApprovalPurpose}>
 | Readonly<{state:'invalid';opening:ApprovalOpening;attempt:number;expired?:true}>
 | Readonly<{state:'error';opening:ApprovalOpening;attempt:number;error:unknown;retryAt:number}>;
/** Only a capability answer names a terminal link; transport/auth/other replies do not. */
const approvalTerminal=(error:unknown)=>{const code=(error as {code?:unknown})?.code;
 return typeof code==='string'&&['invalid_return','invalid','expired','cancelled','claim_expired'].includes(code);};
/** A well-formed setup request that ran out of time, never the generic incomplete link. */
const approvalExpired=(error:unknown)=>hostedClaimExpired(error);
const terminal=(target:ApprovalOpening,attempt:number,error:unknown):ApprovalShown=>({state:'invalid',opening:target,attempt,...(approvalExpired(error)?{expired:true as const}:{})});
/** The exact finite Retry-After deadline, never shortened; an unusable hint fails closed. */
const approvalDeadline=(error:unknown)=>{
 const value=(error as {retryAfterSeconds?:unknown})?.retryAfterSeconds;
 if(value===undefined)return 0;
 if(typeof value!=='number'||!Number.isFinite(value)||value<=0)return Number.POSITIVE_INFINITY;
 const deadline=Date.now()+Math.ceil(value*1000);
 return Number.isSafeInteger(deadline)?deadline:Number.POSITIVE_INFINITY;
};
const approvalScope=(value:{account:{id:string};familyId:string})=>JSON.stringify([value.account.id,value.familyId]);

function ClaimApprovalRoute({account,bootstrap}:{account:ServicesAccount;bootstrap:ServicesBootstrap}) {
 const {t}=servicesI18n();
 const session=useSyncExternalStore(account.service.subscribe,account.service.getSnapshot).session;
 const scope=session?.familyId?JSON.stringify([session.account.id,session.familyId]):'';
 const [shown,setShown]=useState<ApprovalShown>();
 const [clock,setClock]=useState(Date.now);
 const opening=useRef<ApprovalOpening|undefined>(undefined);
 const ticket=useRef(0);
 // One original opening. A retry claims the ticket synchronously, and every
 // publication is bound to that exact settled attempt, so a retained click can
 // neither start another lookup nor bypass a newer deadline.
 const launch=(target:ApprovalOpening)=>{
  if(!target.alive||target.pending||target.renewing)return;
  const own=++ticket.current;
  const controller=new AbortController();
  target.controller=controller;
  target.pending=controller;
  target.settled=undefined;
  setShown({state:'lookup',opening:target,attempt:own});
  const settle=(value:ApprovalShown)=>{
   if(!target.alive||own!==ticket.current||opening.current!==target||controller.signal.aborted)return;
   target.settled=own;
   setShown(value);
  };
  const finish=()=>{if(target.pending===controller)target.pending=undefined;};
  const reconcile=(error:unknown)=>{
   if(!target.alive||own!==ticket.current||opening.current!==target)return;
   const live=account.service.getSnapshot().session;
   // A changed scope retires this error rather than acting under its successor.
   if(!live||approvalScope(live)!==target.scope||target.renewed){
    settle({state:'error',opening:target,attempt:own,error,retryAt:0});return;
   }
   target.renewed=true;
   // BLK-HOST-011 through FX200's account boundary: a definitive 401 already forced
   // the canonical rotation for this exact family and did not replay the lookup.
   // A newer access token for the same family therefore means one bounded repeat.
   if(live.accessToken!==target.identity.accessToken){launch(target);return;}
   // Standalone seam: the rejected call never reached that reconciliation, so make
   // the same exact-original-family rotation once, then repeat the lookup once.
   target.renewing=true;
   const renew=++ticket.current;
   setShown({state:'lookup',opening:target,attempt:renew});
   const still=()=>target.alive&&renew===ticket.current&&opening.current===target;
   void account.service.accessSession(true,target.identity).then(
    ()=>{target.renewing=false;if(still())launch(target);},
    ()=>{target.renewing=false;if(!still())return;
     const current=account.service.getSnapshot().session;
     if(!current||approvalScope(current)!==target.scope)return;      // ended or replaced: parent takes over
     if(current.accessToken!==target.identity.accessToken){launch(target);return;}
     target.settled=renew;
     setShown({state:'error',opening:target,attempt:renew,error,retryAt:approvalDeadline(error)});});
  };
  let result:Promise<ApprovalPurpose>;
  try{result=target.bootstrap.approvalPurpose(target.account,controller.signal);}
  catch(error){
   finish();
   // The parent no longer owns this claim opening; it is being replaced, not failed.
   if((error as {code?:unknown})?.code==='claim_stale')return;
   settle(approvalTerminal(error)?terminal(target,own,error):{state:'error',opening:target,attempt:own,error,retryAt:0});
   return;
  }
  void result.then(purpose=>{finish();settle({state:'purpose',opening:target,attempt:own,purpose});},error=>{
   finish();
   if(controller.signal.aborted)return;
   if((error as {status?:unknown})?.status===401){reconcile(error);return;}
   settle(approvalTerminal(error)?terminal(target,own,error):{state:'error',opening:target,attempt:own,error,retryAt:approvalDeadline(error)});
  });
 };
  // The opening keeps this commit's session identity and launcher; a later re-render's
  // new closures must not replace the approval it already started.
  const capture=useRef({session,launch});
  useLayoutEffect(()=>{capture.current={session,launch};});
 useLayoutEffect(()=>{
  const {session,launch}=capture.current, ticketRef=ticket;
  if(!scope||!session)return;
  const next:ApprovalOpening={scope,account,bootstrap,identity:{accountId:session.account.id,familyId:session.familyId,accessToken:session.accessToken},controller:new AbortController(),renewing:false,renewed:false,alive:true};
  opening.current=next;
  // Monotonic original-family liveness: a foreign or absent family retires this
  // opening at publish time, even if React has not rendered (or the original
  // family is published back in the same batch).
  const release=account.service.subscribe(()=>{
   if(!next.alive)return;
   const current=account.service.getSnapshot().session;
   if(current&&approvalScope(current)===next.scope)return;
   next.alive=false;
   ++ticket.current;
   next.controller.abort();
  });
  launch(next);
  return()=>{
   release();
   ++ticketRef.current;
   next.alive=false;
   next.controller.abort();
   if(opening.current===next)opening.current=undefined;
  };
 },[account,bootstrap,scope]);
 const deadline=shown?.state==='error'?shown.retryAt:0;
 useEffect(()=>{
  if(!deadline||!Number.isFinite(deadline))return;
  setClock(Date.now());
  const timer=setInterval(()=>setClock(Date.now()),250);
  return()=>clearInterval(timer);
 },[deadline]);
 const live=shown&&shown.opening.alive&&opening.current===shown.opening&&shown.opening.account===account&&shown.opening.bootstrap===bootstrap&&shown.opening.scope===scope?shown:undefined;
 if(!live||live.state==='lookup')return <ServicesAuthStage><></></ServicesAuthStage>;
 if(live.state==='invalid')return live.expired?<SetupExpired/>:<InvalidLink/>;
 if(live.state==='purpose')return live.purpose==='setup'?<SetupApprovalPage account={account} bootstrap={bootstrap} Review={HostedSetupApproval}/>:<OwnerApprovalPage account={account} bootstrap={bootstrap}/>;
 const seconds=Number.isFinite(live.retryAt)?Math.max(0,Math.ceil((live.retryAt-clock)/1000)):Number.POSITIVE_INFINITY;
 const blocked=seconds>0;
 const label=blocked&&Number.isFinite(seconds)?t('services.signin.tryIn',{time:`${Math.floor(seconds/60)}:${String(seconds%60).padStart(2,'0')}`}):t('next.states.tryAgain');
 return <ServicesAuthStage><ServicesHeading>{t('next.states.pageFailed')}</ServicesHeading><ServicesLead>{t('next.states.pageFailedBody')}</ServicesLead>
  <ServicesActions><Button disabled={blocked} onClick={()=>{
   const target=opening.current;
   // The latest deadline belongs to this exact settled attempt, rechecked against
   // the real clock and the opening's original-family liveness.
   if(!target||!target.alive||live.opening!==target||target.settled!==live.attempt||Date.now()<live.retryAt)return;
   launch(target);
  }}>{label}</Button></ServicesActions></ServicesAuthStage>;
}
