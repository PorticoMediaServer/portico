import {profileTargetKey, switchAuthorityKey, switchOfferKey} from './profile-offer';
import {enterContinuesProfile, profileContinueEnabled} from './profile-pin-enter';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ComponentType, type ReactNode} from 'react';
import {Link, useNavigate, useRouterState} from '@tanstack/react-router';
import {ownerSwitchContextAllowed} from '../platform/secure-pending';
import {defaultI18n} from '@i18n';
import type {Library, Viewer} from '@core/index.ts';
import {AccountMenu, ProfileAvatar, ProfileAvatarFace, type ProfileIdentity, type ProfileTone} from '../kit/shell';
import type {FactPart} from '@core/presentation/kit-model/index.ts';
import {Icon} from '../kit/Icon';
import {PhoneSelectionProvider, PhoneSelectionSlot, usePhoneSelectionPresent} from '../kit/selection';
import {MenuItem,Dialog} from '../kit/overlays';
import {TextField,Button} from '../kit/controls';
import {activeLibraryId, isLibraryLocation, rememberLibrary, rememberedLibrary, showOwnerActivity} from './model';
import {useShellScrolled} from './viewport';
import {useSizeClass, useCoarsePointer, useWindowScrollAnchor} from '../kit/theme';
import type {ManagementCapabilities} from '@core/profile-management.ts';
import type {SignInKind} from '@core/app-sessions.ts';
import styles from './shell.module.css';
import wordmark from './wordmark.svg';
import type {OwnerActivityProps} from './Activity';
import {LibraryNavigation} from './LibraryNavigation';
import {DesktopNavigation} from './DesktopNavigation';
import {usePageWindowChrome} from './window-chrome';
import {SwitchProfileServerHost, HostedServerOpening, type SwitchProfileServerOpening, type HostedSwitchSelection} from './SwitchProfileServerHost';
import {PendingChanges, type PendingChangesBinding} from './PendingChanges';
import {ConnectionAway} from './ConnectionAway';

export type ShellIdentity = Readonly<{
  viewer: Viewer; signIn?:SignInKind; capabilities?:ManagementCapabilities; username?: string; profileName: string; profileArt?: ProfileTone; serverName: string; serverVersion?: string; /** O-159: the server's `apiVersion` (major.minor), shown in About. */ serverApiVersion?: string;
  primaryProfile?: boolean; profiles: readonly ProfileIdentity[]; canManageProfiles: boolean; accountFacts: readonly FactPart[];
  libraries: readonly Library[]; liveAvailable: boolean; unread: number; showNotificationDot: boolean;
  /** The library directory did not answer (a timeout, a busy server). `libraries` is then not known
   * to be empty: no screen may treat it as "no libraries". Cleared when the directory answers. */
  librariesUnavailable?: true;
}>;
export type ShellProps = Readonly<{
  identity: ShellIdentity;
  /** Mount owns its reads. Compact and non-owner shells do not mount it. */
  OwnerActivity: ComponentType<OwnerActivityProps>;
  dockVisible: boolean;
  /** The verified connection reports the server away; the native desktop host draws one quiet line. */
  serverAway?: boolean;
  onSignOut: () => void; onSignOutPorticoAccount?: () => void; onSwitchProfile?:(profileId:string,pin:string,signal:AbortSignal)=>Promise<void>; onSwitchServer?:(serverId:string,expectedRevision?:number)=>Promise<void>; savedServers?:readonly {id:string;label:string}[];
  onFeedback?: () => void;
  pendingChanges?: PendingChangesBinding;
  overlays?: ReactNode;
  windowChrome?: ReactNode;
  phoneSelection?: ReactNode;
  children: ReactNode;
}>;
const t = defaultI18n.t;
const noSubscription = () => () => {};
function libraryMemory() { try { return window.localStorage; } catch { return undefined; } }
const collapseMemoryKey = 'portico.next.sidebar-collapsed';

/** One shell, one phone-selection store. Screens inside it publish their live
 * selection to this shell's store and never to another shell's. */
export function Shell(props: ShellProps) {
  return <PhoneSelectionProvider><ShellFrame {...props}/></PhoneSelectionProvider>;
}
function ShellFrame({identity, OwnerActivity, dockVisible, serverAway, onSignOut, onSignOutPorticoAccount,onSwitchProfile,onSwitchServer,savedServers,onFeedback,pendingChanges, phoneSelection, overlays, windowChrome, children}: ShellProps) {
  useWindowScrollAnchor();
  const sizeClass = useSizeClass(), coarsePointer = useCoarsePointer();
  // A live screen selection draws its own action bar here, in place of the tab bar.
  const screenSelectionPresent = usePhoneSelectionPresent();
  const desktop = sizeClass === 'expanded' && !coarsePointer, compact = sizeClass === 'compact', scrolled = useShellScrolled();
  const chrome = usePageWindowChrome(scrolled);
  // Only the native desktop host, whose window chrome reports an OS, draws the away line.
  const awayShown = serverAway === true && chrome.os !== undefined;
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try { return window.localStorage.getItem(collapseMemoryKey) === 'true'; } catch { return false; }
  });
  const toggleSidebar = () => setSidebarCollapsed(value => {
    try { window.localStorage.setItem(collapseMemoryKey, String(!value)); } catch { /* The live choice still works. */ }
    return !value;
  });
  const navigation = compact ? 'tabs' : desktop ? 'topbar' : sizeClass === 'medium' || sidebarCollapsed ? 'rail' : 'sidebar';
  const pathname = useRouterState({select: state => state.location.pathname});
  const query = useRouterState({select: state => state.location.search});
  const lastSearch = useRef('');
  if (pathname === '/search' && typeof query.q === 'string') lastSearch.current = query.q;
  const navigate = useNavigate();
  const [profileChoice,setProfileChoice]=useState<ProfileIdentity>(),[profilePin,setProfilePin]=useState(''),[profileError,setProfileError]=useState<unknown>(),[profileBusy,setProfileBusy]=useState(false);
  const profileRequest=useRef<AbortController|undefined>(undefined);
  const profileProof = useRef<string | undefined>(undefined);
  const profileKey = profileChoice ? JSON.stringify([switchAuthorityKey(identity), profileTargetKey(identity.profiles.find(profile => profile.id === profileChoice.id))]) : undefined;
  useLayoutEffect(() => {
    if (!profileChoice || profileProof.current === profileKey) return;
    profileProof.current = undefined;profileRequest.current?.abort();setProfileChoice(undefined);setProfilePin('');setProfileError(undefined);setProfileBusy(false);
  }, [profileKey, profileChoice]);
  const offerProfile = (profile: ProfileIdentity) => {
    profileProof.current = JSON.stringify([switchAuthorityKey(identity), profileTargetKey(profile)]);
    setProfilePin('');setProfileError(undefined);setProfileChoice(Object.freeze({...profile}));
  };
  const profileDisplay = profileChoice && identity.profiles.find(profile => profile.id === profileChoice.id) || profileChoice;
  useEffect(()=>()=>profileRequest.current?.abort(),[]);
  const chooseProfile=async(profile:ProfileIdentity,pin='')=>{if(!onSwitchProfile || profileProof.current !== JSON.stringify([switchAuthorityKey(identity), profileTargetKey(profile)]))return;profileRequest.current?.abort();const controller=profileRequest.current=new AbortController();setProfileBusy(true);setProfileError(undefined);try{await onSwitchProfile(profile.id,pin,controller.signal);if(!controller.signal.aborted)setProfileChoice(undefined);}catch(error){if(!controller.signal.aborted)setProfileError(error);}finally{if(profileRequest.current===controller)setProfileBusy(false);}};
  const home = pathname === '/', [accountOpen, setAccountOpen] = useState(false), [moreOpen, setMoreOpen] = useState(false);
  const [switchOpening, setSwitchOpening] = useState<SwitchProfileServerOpening>(), [hostedSelection, setHostedSelection] = useState<HostedSwitchSelection>();
  const switchOwner = useRef<SwitchProfileServerOpening | undefined>(undefined);
  const offerKey = switchOfferKey(identity);
  const committedSwitch = useRef<{key: string; pathname: string; selectServer: typeof onSwitchServer; selectProfile: typeof onSwitchProfile} | undefined>(undefined);
  useLayoutEffect(() => {
    committedSwitch.current = {key: offerKey, pathname, selectServer: onSwitchServer, selectProfile: onSwitchProfile};
    switchOwner.current = undefined; setSwitchOpening(undefined); setHostedSelection(undefined);
    return () => {committedSwitch.current = undefined; switchOwner.current = undefined;};
  }, [offerKey, pathname, onSwitchServer, onSwitchProfile]);
  const openSwitcher = () => {
    const original = committedSwitch.current;
    if (!desktop || !original || original.key !== offerKey || original.pathname !== pathname) return;
    setAccountOpen(false);
    const opening: SwitchProfileServerOpening = Object.freeze({
      current: () => switchOwner.current === opening && committedSwitch.current === original,
      serverId: identity.viewer.serverId, serverName: identity.serverName, portico: identity.signIn === 'portico',
      profileId: identity.viewer.profileId, profiles: Object.freeze(identity.profiles.map(profile => Object.freeze({...profile}))),
      selectProfile: (profile: ProfileIdentity) => {
        if (!opening.current()) return;
        offerProfile(profile);
        if (onSwitchProfile && !profile.pinRequired) void chooseProfile(profile);
        else if (!onSwitchProfile) void navigate({to: '/profiles', search: {profileId: profile.id}});
      },
      selectServer: onSwitchServer,
      selectHosted: (selection: HostedSwitchSelection) => {if (opening.current()) setHostedSelection(selection);},
      useLocal: () => {if (opening.current()) void navigate({to: '/servers', search: {local: true}});},
    });
    switchOwner.current = opening; setSwitchOpening(opening);
  };
  const {viewer} = identity;
  useEffect(() => {
    if (!pathname.startsWith('/library/')) return;
    let id: string;
    try { id = decodeURIComponent(pathname.slice('/library/'.length).split('/')[0]); } catch { return; }
    rememberLibrary(libraryMemory(), viewer, identity.libraries, id);
  }, [pathname, viewer, identity.libraries]);
  const activeLibrary = activeLibraryId(pathname, rememberedLibrary(libraryMemory(), viewer, identity.libraries));
  useEffect(() => { setAccountOpen(false); setMoreOpen(false); }, [pathname]);
  const openLibraries = () => {
    const id = rememberedLibrary(libraryMemory(), viewer, identity.libraries);
    if (id) void navigate({to: '/library/$libraryId', params: {libraryId: id}});
    else void navigate({to: '/libraries'});
  };
  const current = {id: viewer.profileId, username: identity.username, name: identity.profileName, initial: Array.from(identity.profileName.trim())[0] ?? '', art: identity.profileArt};
  const dashboard = () => { void navigate({to: '/dashboard', search: viewer.role === 'owner' ? {} : {section: 'streams'}}); };
  const serverAction = !desktop && identity.capabilities?.manageServer === true && (viewer.role === 'owner' || viewer.role === 'admin' || viewer.role === 'administrator')
    ? <MenuItem className={styles.dashboardCard} onSelect={dashboard}>
      <span className={styles.dashboardIcon}><Icon name="i-pulse" size={22} /></span>
      <span className={styles.dashboardText}><small>{t('next.shell.server')}</small><b>{t('next.shell.dashboard')}</b></span>
      <Icon name="i-right" size={18} />
    </MenuItem> : undefined;
  const account = <AccountMenu open={accountOpen} onOpenChange={setAccountOpen} current={current} primary={identity.primaryProfile === true}
    notificationCount={desktop ? undefined : identity.unread} notificationDot={!desktop && identity.showNotificationDot && identity.unread > 0}
    serverChoices={!desktop&&onSwitchServer?savedServers?.map(server=>({...server,onSelect:()=>{void onSwitchServer(server.id);}})):undefined} signOutLabel={identity.serverName?t('accounts.signOut.namedServer',{server:identity.serverName}):undefined} profiles={identity.profiles} accountFacts={identity.accountFacts} serverAction={serverAction} actions={{
      ...(identity.canManageProfiles ? {manageProfiles: () => { void navigate({to: '/settings'}); }} : {}),
      ...(identity.canManageProfiles ? {addProfile: () => { void navigate({to: '/settings'}); }} : {}),
      switchProfile: profile => {if(onSwitchProfile){offerProfile(profile);if(!profile.pinRequired)void chooseProfile(profile);}else{void navigate({to: '/profiles', search: {profileId: profile.id}});}},
      switchServer: () => {if (desktop) openSwitcher(); else void navigate({to: '/servers'});},
      ...(identity.signIn === 'portico' && viewer.role === 'owner' && identity.capabilities?.manageServer === true && ownerSwitchContextAllowed() ? {switchToLocalSignIn: () => { void navigate({to: '/settings', search: {section: 'signin'}}); }} : {}),
      settings: () => { void navigate({to: '/settings'}); },
      ...(!desktop ? {notifications: () => { void navigate({to: '/notifications'}); }} : {}),
      watchTogether: () => { void navigate({to: '/watch-together'}); }, feedback: onFeedback,
      help: () => { void navigate({to: '/help'}); }, signOut: onSignOut,signOutPorticoAccount:onSignOutPorticoAccount,
    }} trigger={navigation === 'sidebar' ? <button type="button" className={styles.profileRow} aria-label={[t('web.account.switchProfileOrServer'), identity.username || t('next.shell.usernameUnavailable'), identity.serverName].join(', ')}>
      <ProfileAvatarFace initial={current.initial} art={identity.profileArt} size={38} />
      <span className={styles.profileText}><b>{identity.username || t('next.shell.usernameUnavailable')}</b><small>{identity.serverName}</small></span><Icon name="i-updown" size={18} />
    </button> : <ProfileAvatar className={styles.avatar} name={identity.profileName} initial={current.initial} art={identity.profileArt} size={navigation === 'rail' ? 40 : desktop ? 34 : 30}
      notificationDot={compact && identity.showNotificationDot && identity.unread > 0} aria-label={t('next.home.shell.profileMenu', {name: identity.profileName})} />} />;
  const pendingCount = useSyncExternalStore(pendingChanges?.model.subscribe ?? noSubscription, () => pendingChanges?.model.snapshot().rows.length ?? 0, () => 0);
  const pending = pendingChanges && pendingCount > 0 ? <PendingChanges key={pendingChanges.scopeKey} {...pendingChanges} /> : undefined;
  if (hostedSelection) return <HostedServerOpening selection={hostedSelection} cancel={() => setHostedSelection(undefined)} />;
  const captionRenderer = windowChrome ?? chrome.renderer;
  return <div className={styles.shell} data-desktop={desktop} data-size-class={sizeClass} data-pointer={coarsePointer ? 'coarse' : 'fine'} data-navigation={navigation} data-has-dock={dockVisible} data-window-os={chrome.os} data-window-fullscreen={chrome.fullscreen}>
    <a className={styles.skip} href="#portico-content">{t('next.home.shell.skip')}</a>
    {/* desktop-app#5: over a scrolled rail page the Windows caption symbols need the
        authored shade at the page top. It is pointer-inert and sits under them. */}
    {chrome.os === 'windows' && !chrome.fullscreen && sizeClass === 'medium' && <div className={styles.captionShade} data-on={scrolled || undefined} aria-hidden="true" />}
    {captionRenderer && <div data-window-chrome-host data-window-no-drag>{captionRenderer}</div>}
    <div className={styles.navigation} data-shell-navigation>
      {desktop ? <nav className={styles.topnav} data-solid={scrolled} data-window-drag-region="page-topbar" aria-label={t('next.home.shell.main')}>
        <Link className={styles.brand} to="/" data-window-no-drag aria-label={t('next.platform.appName')}><img src={wordmark} alt="" /></Link>
        <DesktopNavigation libraries={identity.libraries} liveAvailable={identity.liveAvailable} activeLibraryId={activeLibrary} moreOpen={moreOpen} onMoreOpenChange={setMoreOpen} />
        <div className={styles.tools} data-window-no-drag>
          {pending}
          <Link className={styles.icon} to="/search" search={{q: lastSearch.current}} aria-label={t('next.home.shell.search')} activeOptions={{includeSearch: false}} activeProps={{'aria-current': 'page'}}><Icon name="i-search" size={22} /></Link>
          <Link className={styles.icon} to="/saved" aria-label={t('next.home.shell.saved')} activeProps={{'aria-current': 'page'}}><Icon name="i-bookmark" size={22} /></Link>
          <Link className={styles.icon} to="/notifications" aria-label={identity.unread > 0 ? t('next.home.shell.unread', {count: identity.unread}) : t('next.home.shell.notifications')} activeProps={{'aria-current': 'page'}}><Icon name="i-bell" size={22} />{identity.showNotificationDot && identity.unread > 0 && <span className={styles.dot} />}</Link>
          {identity.capabilities?.manageServer === true && showOwnerActivity(viewer.role, desktop) && !awayShown && <OwnerActivity />}
          {account}
        </div>
      </nav> : !compact ? <nav className={styles.rail} data-window-drag-region="rail-empty" aria-label={t('next.home.shell.main')}>
        {sizeClass === 'medium' && <div className={styles.nativeRailMark}><span><img src={wordmark} alt={t('next.platform.appName')} /></span></div>}
        {sizeClass === 'expanded' && <div className={styles.railBrand} data-window-no-drag>
          {navigation === 'sidebar' && <Link className={styles.brand} to="/" aria-label={t('next.platform.appName')}><img src={wordmark} alt="" /></Link>}
          <button type="button" className={styles.collapse} aria-label={t(sidebarCollapsed ? 'web.shell.expand' : 'web.shell.collapse')} aria-expanded={!sidebarCollapsed} onClick={toggleSidebar}><Icon name="i-panels" size={22} /></button>
        </div>}
        <div className={styles.railPrimary} data-window-no-drag>
          <Link className={styles.railItem} to="/search" search={{q: lastSearch.current}} activeOptions={{includeSearch: false}} activeProps={{'aria-current': 'page'}}><Icon name="i-search" size={22} /><span data-nav-label>{t('next.home.shell.search')}</span></Link>
          <Link className={styles.railItem} to="/" activeOptions={{exact: true}} activeProps={{'aria-current': 'page'}}><Icon name="i-home" size={22} /><span data-nav-label>{t('next.home.shell.home')}</span></Link>
        </div>
        <LibraryNavigation libraries={identity.libraries} sidebar={navigation === 'sidebar'} activeLibraryId={activeLibrary} />
        <div className={styles.railFixed} data-window-no-drag>
          <div className={styles.railDivider} />
          {identity.liveAvailable && <Link className={styles.railItem} to="/live" activeProps={{'aria-current': 'page'}}><Icon name="i-live" size={22} /><span data-nav-label>{t('next.home.shell.live')}</span></Link>}
          <Link className={styles.railItem} to="/saved" activeProps={{'aria-current': 'page'}}><Icon name="i-bookmark" size={22} /><span data-nav-label>{t('next.home.shell.saved')}</span></Link>
        </div>
        <div className={styles.railBottom} data-window-no-drag>
          {pending}
          <Link className={styles.railItem} to="/notifications" data-icon-only={navigation === 'rail' || undefined} aria-label={identity.unread > 0 ? t('next.home.shell.unread', {count: identity.unread}) : t('next.home.shell.notifications')} activeProps={{'aria-current': 'page'}}><Icon name="i-bell" size={22} />{navigation === 'sidebar' && <span data-nav-label>{t('next.home.shell.notifications')}</span>}{identity.showNotificationDot && identity.unread > 0 && <span data-nav-count aria-hidden="true">{identity.unread}</span>}</Link>
          {identity.capabilities?.manageServer === true && viewer.role === 'owner' && !awayShown && <OwnerActivity presentation={navigation === 'sidebar' ? 'sidebar' : 'rail'} className={styles.railItem} />}
          <Link className={styles.railItem} to="/settings" activeProps={{'aria-current': 'page'}}><Icon name="i-settings" size={22} /><span data-nav-label>{t('next.shell.settings')}</span></Link>
          {account}
        </div>
      </nav> : <>
        {(home || pending) && <header className={styles.phoneHeader}><Link className={styles.brand} to="/" aria-label={t('next.platform.appName')}><img src={wordmark} alt="" /></Link><div className={styles.phoneTools}>
          {pending}
          {account}
        </div></header>}
        {phoneSelection ?? (screenSelectionPresent ? <PhoneSelectionSlot/> : <nav className={styles.tabbar} aria-label={t('next.home.shell.main')}>
          <Link to="/" activeOptions={{exact: true}} activeProps={{'aria-current': 'page'}}><Icon name="i-home" size={22} /><span>{t('next.home.shell.home')}</span></Link>
          <Link to="/search" search={{q: lastSearch.current}} activeOptions={{includeSearch: false}} activeProps={{'aria-current': 'page'}}><Icon name="i-search" size={22} /><span>{t('next.home.shell.search')}</span></Link>
          <button type="button" aria-current={isLibraryLocation(pathname) || activeLibrary ? 'page' : undefined} onClick={openLibraries}><Icon name="i-grid" size={22} /><span>{t('next.home.shell.libraries')}</span></button>
          {identity.liveAvailable && <Link to="/live" activeProps={{'aria-current': 'page'}}><Icon name="i-tv" size={22} /><span>{t('next.home.shell.live')}</span></Link>}
          <Link to="/saved" activeProps={{'aria-current': 'page'}}><Icon name="i-bookmark" size={22} /><span>{t('next.home.shell.saved')}</span></Link>
        </nav>)}
      </>}
    </div>
    {awayShown && <ConnectionAway serverName={identity.serverName} />}
    {children}
    <Dialog open={!!profileChoice&&(profileChoice.pinRequired===true||!!profileError)} onOpenChange={open=>{if(!open){profileProof.current=undefined;profileRequest.current?.abort();setProfileChoice(undefined);setProfilePin('');setProfileError(undefined);}}} title={profileDisplay?.name??identity.profileName} footer={<><Button variant="glass" onClick={()=>{profileProof.current=undefined;profileRequest.current?.abort();setProfileChoice(undefined);setProfilePin('');}}>{t('accounts.profiles.cancel')}</Button><Button disabled={!profileContinueEnabled(profileChoice?.pinRequired,profilePin,profileBusy)} onClick={()=>{if(profileChoice)void chooseProfile(profileChoice,profilePin);}}>{t('accounts.profiles.continue')}</Button></>}>
      {profileChoice?.pinRequired&&<TextField type="password" inputMode="numeric" maxLength={4} autoComplete="off" label={t('accounts.profiles.pinForProfile',{profile:profileDisplay?.name ?? profileChoice.name})} value={profilePin} onChange={event=>setProfilePin(event.currentTarget.value)} onKeyDown={event=>{if(profileChoice&&enterContinuesProfile({key:event.key,isComposing:event.nativeEvent.isComposing},profileChoice.pinRequired,profilePin,profileBusy)){event.preventDefault();void chooseProfile(profileChoice,profilePin);}}} error={(profileError as {code?:unknown}|undefined)?.code==='profile_pin_invalid'?t('accounts.profiles.pinFailed'):undefined} />}
    </Dialog>
    {switchOpening && <SwitchProfileServerHost opening={switchOpening} presentation={identity} close={() => {if (switchOwner.current === switchOpening) switchOwner.current = undefined; setSwitchOpening(current => current === switchOpening ? undefined : current);}} />}
    {overlays}
  </div>;
}
