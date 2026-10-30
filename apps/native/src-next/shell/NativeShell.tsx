import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View, useWindowDimensions} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {OverlaysProvider} from '../kit/overlays';
import {PlayerMorphProvider, useInitialPlayerPagePaint} from '../kit/dock/PlayerMorphBridge';
import {DockClearanceProvider} from '../kit/dock/DockClearance';
import {ListeningCoverProvider, useListeningTabBarPaint} from '../kit/dock/ListeningCoverBridge';
import {SurfacePaintHost} from '../kit/overlays/SurfacePaintHost';
import {AccountMenu} from '../kit/shell';
import {useTheme} from '../kit/theme';
import {useMotion} from '../kit/useMotion';
import {registerTVMenuFallback} from '../platform/menu-key';
import {useTVMenuCapture} from '../platform/tv-menu';
import {useFocusRegistry, type FocusSnapshot} from '../kit/focus';
import {tvFocusIds, sameDestination} from './tv-focus';
import {HomeScreen} from '../screens/home/HomeScreen';
import {canYieldTVHomeMenu, homeStartModel, resolveTVHomeStart} from '../screens/home/home-start';
import {authorityKey, canOpenLibrary, librariesDestination, mediumRailMetrics, phoneBarHeight, sameAuthority, selectedTab, shellLayout, type ShellDestination, type ShellTab} from './model';
import {nativeScreenChromeHidden, subscribeNativeScreenChrome} from './chrome';
import type {NativeShellRuntime, NativeShellSnapshot} from './runtime';
import type {NativeShellRoute} from './routes';
import {PhoneTabBar} from './PhoneTabBar';
import {PhoneHomeHeader} from './PhoneHomeHeader';
import {MediumRail} from './MediumRail';
import {TVRail} from './TVRail';
import {TVProfileMenu} from './TVProfileMenu';
import {NativePlaybackLayer} from './NativePlaybackLayer';
import {canReturnToPage, holdsPageFocus} from './page-focus-return';
import {NativeStackSlots, NativeStackSlotsProvider} from '../kit/navigation/stack-slot';
import {TouchStacks, nativeStackMotionTier, type NativeStackPage} from './TouchStacks';

const noStacks = () => () => {};
const topRoute = (stacks: NativeShellRuntime['stacks']): NativeShellRoute | undefined => {
  const value = stacks?.getSnapshot();
  return value?.stacks.find(stack => stack.tab === value.active)?.presentations.at(-1)?.route;
};
/** Pages that take the whole screen, without the tab bar. */
const immersiveRoute = (route: NativeShellRoute | undefined) => route?.kind === 'read' || route?.kind === 'channel';

export function NativeShell({runtime}: {runtime: NativeShellRuntime}) {
  const snapshot = useSyncExternalStore(runtime.subscribe, runtime.getSnapshot);
  // Permission/profile/server changes cannot reuse component state, focus, selections or images.
  return <OverlaysProvider key={authorityKey(snapshot.authority)}><PlayerMorphProvider><ListeningCoverProvider><DockClearanceProvider><SurfacePaintHost><ShellSession key={authorityKey(snapshot.authority)} runtime={runtime} snapshot={snapshot}/></SurfacePaintHost></DockClearanceProvider></ListeningCoverProvider></PlayerMorphProvider></OverlaysProvider>;
}

function ShellSession({runtime, snapshot}: {runtime: NativeShellRuntime; snapshot: NativeShellSnapshot}) {
  const initialPlayer = useInitialPlayerPagePaint();
  const listeningTabBar = useListeningTabBarPaint();
  const initialTitle = initialPlayer?.title;
  const theme = useTheme(), motion = useMotion(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  const chromeHidden = useSyncExternalStore(subscribeNativeScreenChrome, nativeScreenChromeHidden);
  const layout = shellLayout(dimensions.width, dimensions.height, theme.isTV);
  /** phone-guide 2 draws the compact row bar on a landscape phone. */
  const landscapePhone = layout.kind === 'phone' && dimensions.width > dimensions.height;
  /** The sidebar's own breakpoint, captured so the hook reads a plain value. */
  const wideSidebar = dimensions.width >= 1024;
  // Touch hosts each tab's pages in a native stack (motion.html 2.9-2.11); television keeps its own layer.
  const stacks = theme.isTV ? undefined : runtime.stacks, slots = useMemo(() => new NativeStackSlots(), []);
  const stackTop = useSyncExternalStore(stacks?.subscribe ?? noStacks, () => topRoute(stacks), () => topRoute(stacks));
  const destination = snapshot.destination, registry = useFocusRegistry(), ids = useMemo(() => tvFocusIds(snapshot.authority), [snapshot.authority]);
  // Compact Settings is a modal over the last committed destination. Keep that
  // native subtree mounted so its scroll position and painted content survive.
  const previousPage = useRef<ShellDestination>({kind: 'home'});
  const settingsPage = !theme.isTV && destination.kind === 'settings';
  const settingsSheet = settingsPage && theme.width < 600;
  const paintedDestination = settingsPage ? previousPage.current : destination;
  useLayoutEffect(() => {if (destination.kind !== 'settings') previousPage.current = destination;}, [destination]);
  const [tvListeningPresent, setTVListeningPresent] = useState(false);
  const pageFocus = useRef<FocusSnapshot | undefined>(undefined), pageFocusDestination = useRef<string | undefined>(undefined);
  const railRelease = useRef<(() => void) | undefined>(undefined);
  useEffect(() => {
    const original = runtime.retirementSignal;
    const remember = () => {
      // Native region cleanup may publish before React's passive unsubscribe.
      // Refuse that original observer before touching any retired business accessor.
      if (original?.aborted) return;
      const now = runtime.getSnapshot(), saved = registry.capture();
      if (now.contentVisible === false || now.mediaExpanded || tvListeningPresent || now.homeObscured
        || now.destination.kind === 'home' && !now.homeVisible) return;
      if (saved && saved.regionId !== ids.rail && registry.config(saved.regionId)?.hold !== true) {
        pageFocus.current = saved; pageFocusDestination.current = JSON.stringify(now.destination);
      }
    };
    remember();
    if (original?.aborted) return;
    const off = registry.subscribe(remember);
    original?.addEventListener('abort', off, {once: true});
    if (original?.aborted) off();
    return () => {original?.removeEventListener('abort', off);off();};
  }, [ids, registry, runtime, tvListeningPresent]);
  const currentPageFocus = pageFocusDestination.current === JSON.stringify(destination) ? pageFocus.current : undefined;
  const pageHold = holdsPageFocus(snapshot, theme.isTV && tvListeningPresent);
  const heldPage = useRef<{opener: FocusSnapshot; destination: string; release: () => void} | undefined>(undefined);
  const listeningPresent = useRef(tvListeningPresent); listeningPresent.current = tvListeningPresent;
  useEffect(() => () => {railRelease.current?.(); heldPage.current?.release();}, []);
  const [remembered, setRemembered] = useState<string>(), [memoryReady, setMemoryReady] = useState(false);
  const lastOpenedLibrary = useRef<string | undefined>(undefined);
  const [accountOpen, setAccountOpen] = useState(false), [railExpanded, setRailExpanded] = useState(false);
  // A mounted page state may dim the rail while its own copy is the only thing to read. The
  // lease is the caller's own object, so a stale release can never clear a successor's lease.
  const [railDim, setRailDim] = useState<Readonly<{retired: boolean}>>();
  const holdRailDim = useCallback((lease: Readonly<{retired: boolean}>) => setRailDim(lease), []);
  const releaseRailDim = useCallback((lease: Readonly<{retired: boolean}>) => setRailDim(current => current === lease ? undefined : current), []);
  const [homeScrolled, setHomeScrolled] = useState(false);
  const getFocusSnapshot = useMemo(() => {
    let previous: Readonly<{regionId?: string; childId?: string; homeStart: boolean}> | undefined;
    return () => {
      if (runtime.retirementSignal?.aborted) return previous ??= {homeStart: false};
      const focused = theme.isTV ? registry.capture() : undefined;
      let homeStart = false;
      if (theme.isTV && snapshot.destination.kind === 'home' && snapshot.homeVisible && !snapshot.mediaExpanded && !tvListeningPresent
        && snapshot.contentVisible !== false) {
        const document = runtime.home.getSnapshot().document, mixes = runtime.mixes?.getSnapshot();
        const start = resolveTVHomeStart(registry, homeStartModel(document, snapshot.authority, {
          readLibraryIds: new Set(snapshot.libraries.filter(library => library.kind === 'books').map(library => library.id)),
          mixesReady: mixes?.phase === 'ready' && mixes.rows.length > 0, playbackAvailable: !!runtime.playback,
        }));
        homeStart = canYieldTVHomeMenu(start, focused, {home: true, visible: snapshot.homeVisible, obscured: snapshot.homeObscured,
          expanded: snapshot.mediaExpanded, listening: tvListeningPresent, scrolled: homeScrolled, account: accountOpen,
          rail: railExpanded || focused?.regionId === ids.rail, chromeHidden, contentVisible: true,
          // The existing return lease is reactive and lasts through actual overlay exit.
          // Keyboard/overlay Menu-capture leases retain their independent precedence.
          overlay: !!snapshot.selectionActive || registry.hasRetainedFocus()});
      }
      if (previous && previous.regionId === focused?.regionId && previous.childId === focused?.childId && previous.homeStart === homeStart) return previous;
      return previous = {regionId: focused?.regionId, childId: focused?.childId, homeStart};
    };
  }, [accountOpen, chromeHidden, homeScrolled, ids.rail, railExpanded, registry, runtime, snapshot, theme.isTV, tvListeningPresent]);
  const subscribeFocus = useCallback((listener: () => void) => {
    let previous = getFocusSnapshot();
    return registry.subscribe(() => {
      const next = getFocusSnapshot();
      if (next === previous) return;
      previous = next; listener();
    });
  }, [getFocusSnapshot, registry]);
  // Exact page focus still updates the rail opener; unrelated host/config changes do not render
  // the entire shell. Menu yield remains reactive to actual start readiness and retained overlays.
  const {homeStart} = useSyncExternalStore(subscribeFocus, getFocusSnapshot, getFocusSnapshot);
  useTVMenuCapture(theme.isTV && !homeStart);
  const [sidebarExpanded, setSidebarExpanded] = useState(true);
  const sidebarRevision = useRef(0);
  const sidebarLifetime = useRef<object | undefined>(undefined);
  const avatarRef = useRef<View>(null), opening = useRef(0), alive = useRef<{runtime: NativeShellRuntime} | undefined>(undefined);
  const shift = useSharedValue(0), dim = useSharedValue(0);
  const current = useCallback(() => alive.current?.runtime === runtime && !runtime.retirementSignal?.aborted && sameAuthority(snapshot.authority, runtime.getSnapshot().authority), [runtime, snapshot.authority]);
  useLayoutEffect(() => {
    if (!theme.isTV || runtime.retirementSignal?.aborted) return;
    const opener = pageFocusDestination.current === JSON.stringify(runtime.getSnapshot().destination) ? pageFocus.current : undefined;
    if (pageHold && !heldPage.current && opener && pageFocusDestination.current) heldPage.current = {
      opener, destination: pageFocusDestination.current, release: registry.retain(opener),
    };
    if (pageHold || !heldPage.current) return;
    const held = heldPage.current; heldPage.current = undefined; held.release();
    const frame = requestAnimationFrame(() => {
      if (!current()) return;
      const now = runtime.getSnapshot();
      if (canReturnToPage(held.destination, now, listeningPresent.current, registry.hasRetainedFocus())) registry.restore(held.opener);
    });
    return () => cancelAnimationFrame(frame);
  }, [current, pageHold, registry, runtime, theme.isTV]);
  useEffect(() => {
    const lifetime = alive, pending = opening, session = {runtime};
    lifetime.current = session;
    return () => {
      if (lifetime.current !== session) return;
      lifetime.current = undefined;
      pending.current++;
    };
  }, [runtime]);
  useEffect(() => {
    if (theme.isTV || !wideSidebar) return;
    const lifetime = {}, authority = {...snapshot.authority}, revision = sidebarRevision.current;
    sidebarLifetime.current = lifetime;
    const valid = () => sidebarLifetime.current === lifetime && current() && sameAuthority(authority, runtime.getSnapshot().authority);
    void runtime.memory.readSidebar(authority, valid).then(value => {
      if (valid() && revision === sidebarRevision.current) setSidebarExpanded(value);
    }, error => {if (valid()) runtime.reportError(error);});
    return () => {if (sidebarLifetime.current === lifetime) sidebarLifetime.current = undefined;};
  }, [current, runtime, snapshot.authority, theme.isTV, wideSidebar]);
  const toggleSidebar = () => {
    const lifetime = sidebarLifetime.current, authority = {...snapshot.authority};
    const valid = () => !!lifetime && sidebarLifetime.current === lifetime && current() && sameAuthority(authority, runtime.getSnapshot().authority);
    if (!valid()) return;
    sidebarRevision.current++;
    const value = !sidebarExpanded; setSidebarExpanded(value);
    void runtime.memory.rememberSidebar(authority, value, valid).catch(error => {if (valid()) runtime.reportError(error);});
  };
  useEffect(() => {
    let pending = true;
    void runtime.memory.read(snapshot.authority).then(id => {if (pending && current()) {setRemembered(lastOpenedLibrary.current ?? id); setMemoryReady(true);}}, error => {if (pending && current()) {setMemoryReady(true); runtime.reportError(error);}});
    return () => {pending = false;};
  }, [current, runtime, snapshot.authority]);
  useEffect(() => {
    if (!current() || snapshot.contentVisible === false || destination.kind !== 'library'
      || !canOpenLibrary(snapshot.libraries, destination.libraryId, theme.isTV) || lastOpenedLibrary.current === destination.libraryId) return;
    const id = destination.libraryId;
    lastOpenedLibrary.current = id; opening.current++; setRemembered(id); setMemoryReady(true);
    void runtime.memory.remember(snapshot.authority, id).catch(runtime.reportError);
  }, [current, destination, runtime, snapshot.authority, snapshot.contentVisible, snapshot.libraries, theme.isTV]);
  useEffect(() => {
    const retained = snapshot.destination;
    if (destination.kind === 'library' && !canOpenLibrary(snapshot.libraries, destination.libraryId, theme.isTV)
      || theme.isTV && retained.kind === 'library' && !canOpenLibrary(snapshot.libraries, retained.libraryId, true)) runtime.onDestination({kind: 'libraries'});
    if (destination.kind === 'live' && !snapshot.hasLiveTV) runtime.onDestination({kind: 'home'});
  }, [destination, runtime, snapshot.destination, snapshot.hasLiveTV, snapshot.libraries, theme.isTV]);
  const navigate = (next: ShellDestination, anyRoot = false) => {
    if (!current()) return;
    setAccountOpen(false); opening.current++;
    // H14: the chrome and the page stay painted; only the route's content changes, and its own
    // motion owns that (as web's page rule). The page area is never reset to 0, which blanked a frame.
    if (stacks) stacks.selectTab(next, anyRoot); else runtime.onDestination(next);
  };
  const openLibrary = (id: string) => {
    if (!current() || !canOpenLibrary(snapshot.libraries, id, theme.isTV)) return;
    navigate({kind: 'library', libraryId: id});
  };
  const selectTab = async (tab: ShellTab['id']) => {
    if (!current()) return;
    if (selectedTab(destination) === tab) {if (stacks) stacks.reselectTab(destination); else runtime.scrollToTop(destination); return;}
    if (tab !== 'libraries') {
      if (tab === 'search') navigate({kind: 'search'}, true); else navigate({kind: tab}, true);
      return;
    }
    // A visited Libraries tab comes back exactly as it was left.
    if (stacks?.getSnapshot().stacks.some(stack => stack.tab === 'libraries')) {navigate({kind: 'libraries'}, true); return;}
    const mine = ++opening.current, from = runtime.getSnapshot().destination;
    const id = memoryReady ? remembered : await runtime.memory.read(snapshot.authority).catch(error => {runtime.reportError(error); return undefined;});
    if (!current() || mine !== opening.current || runtime.getSnapshot().contentVisible === false || !sameDestination(from, runtime.getSnapshot().destination)) return;
    navigate(librariesDestination(id, runtime.getSnapshot().libraries, theme.isTV));
  };
  const expanded = useCallback((value: boolean) => {
    setRailExpanded(value);
    const saved = pageFocusDestination.current === JSON.stringify(runtime.getSnapshot().destination) ? pageFocus.current : undefined;
    if (value && !railRelease.current && saved) railRelease.current = registry.retain(saved);
    if (!value) {railRelease.current?.(); railRelease.current = undefined;}
    // tv.html f2 moves the copy's left edge from200 to470. The frame wins the stale220 token.
    shift.value = motion.transforms ? withTiming(value ? 270 : 0, motion.timing('standard')) : value ? 270 : 0;
    dim.value = withTiming(value ? .42 : 0, motion.timing('standard'));
  }, [dim, motion, registry, runtime, shift]);
  useEffect(() => {
    if (!theme.isTV) return;
    return runtime.registerTVRailBack?.(() => {
      if (registry.capture()?.regionId !== ids.rail) return false;
      const saved = pageFocusDestination.current === JSON.stringify(runtime.getSnapshot().destination) ? pageFocus.current : undefined;
      expanded(false);
      if (!registry.restore(saved)) runtime.scrollToTop(runtime.getSnapshot().destination);
      return true;
    });
  }, [runtime, registry, ids, theme.isTV, expanded]);
  useEffect(() => {
    const original = runtime.retirementSignal;
    if (!theme.isTV || original?.aborted) return;
    const off = registerTVMenuFallback(() => {
      // An original retirement can precede React's passive effect cleanup.
      if (original?.aborted || !current()) return;
      const now = runtime.getSnapshot();
      if (now.contentVisible === false || now.mediaExpanded || tvListeningPresent
        || now.homeObscured || now.destination.kind === 'home' && !now.homeVisible || accountOpen || chromeHidden) return;
      // Context Menu on an open rail is consumed without closing or navigating it.
      if (registry.capture()?.regionId === ids.rail) return;
      if (registry.focusRegion(ids.rail)) expanded(true);
    });
    original?.addEventListener('abort', off, {once: true});
    if (original?.aborted) off();
    return () => {original?.removeEventListener('abort', off); off();};
  }, [accountOpen, chromeHidden, current, expanded, ids.rail, registry, runtime, theme.isTV, tvListeningPresent]);
  useEffect(() => {if (!motion.transforms) shift.value = railExpanded ? 270 : 0;}, [motion.transforms, railExpanded, shift]);
  const pageStyle = useAnimatedStyle(() => ({transform: [{translateX: shift.value}]}));
  const initialTitleStyle = useAnimatedStyle(() => ({opacity: initialTitle?.value ?? 1}));
  const dimStyle = useAnimatedStyle(() => ({opacity: dim.value}));
  const barHeight = layout.tabs && !chromeHidden ? phoneBarHeight(insets.bottom, landscapePhone) : 0;
  // foldable3e/3f: the phone bar leaves only on medium; the dock floats at the foot.
  const sideNavigation = layout.rail || layout.kind === 'desktop';
  const sideWidth = layout.kind === 'desktop' && sidebarExpanded ? 248 : mediumRailMetrics.width;
  const homeBottom = sideNavigation ? 92 : barHeight, pageBottom = sideNavigation ? 16 : barHeight;
  const railDestination = snapshot.railDestination ?? destination;
  const notifications = snapshot.account.actions.find(action => action.id === 'notifications');
  // Only the covered destination leaves the accessibility tree. The watch
  // host's visible title/episode panes remain siblings in their own outlet.
  const baseHidden = snapshot.contentVisible === false || snapshot.mediaExpanded || theme.isTV && tvListeningPresent;
  const homePage = (active: boolean, obscured: boolean) => <HomeScreen runtime={runtime} authority={snapshot.authority} active={active} heroObscured={obscured} railExpanded={railExpanded} bottomInset={homeBottom} onScrolledChange={setHomeScrolled} holdRailDim={holdRailDim} releaseRailDim={releaseRailDim}/>;
  const phoneHomeHeader = <PhoneHomeHeader profile={snapshot.profile} unread={snapshot.unreadNotifications} avatarRef={avatarRef} onProfile={() => navigate({kind: 'settings'})} viewerRole={snapshot.account.viewerRole} serverAction={snapshot.account.serverAction} castControl={runtime.castControl} scrolled={homeScrolled}/>;
  /** One shell destination inside its stack screen: a tab's first screen, a library opened
   * from Libraries, or Settings (the modal stack's transparent root over the live page). */
  const renderShellPage = ({presentation, top, tabActive, modal}: NativeStackPage) => {
    const page = presentation.route.kind === 'shell' ? presentation.route.destination : presentation.destination;
    if (modal) return <View style={{flex: 1, paddingLeft: sideNavigation ? sideWidth : 0, paddingBottom: settingsSheet ? 0 : pageBottom, backgroundColor: settingsSheet ? 'transparent' : theme.colors.ink}}>
      {runtime.renderDestination(page, openLibrary)}
    </View>;
    return <View style={{flex: 1, paddingLeft: sideNavigation ? sideWidth : 0}}>
      {page.kind === 'home' ? <>
        {homePage(top && !snapshot.mediaExpanded && snapshot.homeVisible, !!snapshot.homeObscured || settingsPage)}
        {layout.kind === 'phone' && tabActive && !snapshot.selectionActive && !chromeHidden && phoneHomeHeader}
      </> : <View style={{flex: 1, paddingBottom: pageBottom}}>{runtime.renderDestination(page, openLibrary)}</View>}
    </View>;
  };
  // phone-detail and motion 2.9: the tab bar stays still under pushed pages, which end above it.
  // The reader and live TV are full-screen.
  const pushedPage = (route: NativeShellRoute, node: React.ReactNode) => <Animated.View pointerEvents={initialPlayer ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer}
    importantForAccessibility={initialPlayer ? 'no-hide-descendants' : 'auto'} style={[{position: 'absolute', top: 0, left: 0, right: 0, bottom: immersiveRoute(route) ? 0 : barHeight}, initialTitleStyle]}>{node}</Animated.View>;
  const content = <>
    {stacks ? <Animated.View pointerEvents="box-none" accessibilityElementsHidden={!!snapshot.mediaExpanded} importantForAccessibility={snapshot.mediaExpanded ? 'no-hide-descendants' : 'auto'} style={[{position: 'absolute', inset: 0}, pageStyle]}>
      <TouchStacks port={stacks} slots={slots} tier={nativeStackMotionTier(motion)} background={theme.colors.ink} renderShell={renderShellPage} routeStyle={pushedPage}/>
    </Animated.View> : <>
    <Animated.View pointerEvents={settingsPage ? 'none' : 'auto'} accessibilityElementsHidden={baseHidden || settingsPage} importantForAccessibility={baseHidden || settingsPage ? 'no-hide-descendants' : 'auto'} style={[{flex: 1}, settingsPage && !settingsSheet ? {display: 'none'} : null, sideNavigation ? {paddingLeft: sideWidth} : null, settingsPage ? undefined : pageStyle]}>
      {paintedDestination.kind === 'home' ? <HomeScreen runtime={runtime} authority={snapshot.authority} active={!settingsPage && !snapshot.mediaExpanded && snapshot.homeVisible} heroObscured={settingsPage || snapshot.homeObscured} railExpanded={railExpanded} bottomInset={homeBottom} onScrolledChange={setHomeScrolled} holdRailDim={holdRailDim} releaseRailDim={releaseRailDim}/>
        : <View style={{flex: 1, paddingBottom: pageBottom}}>
          {runtime.renderDestination(paintedDestination, openLibrary)}
        </View>}
    </Animated.View>
    {settingsPage && <Animated.View style={[{position: 'absolute', inset: 0, paddingLeft: sideNavigation ? sideWidth : 0, paddingBottom: settingsSheet ? 0 : pageBottom}, pageStyle]}>{runtime.renderDestination(destination, openLibrary)}</Animated.View>}
    </>}
    {theme.isTV && <Animated.View pointerEvents="none" style={[{position: 'absolute', inset: 0, backgroundColor: theme.colors.ink, zIndex: theme.layers.navigation - 1}, dimStyle]}/>}
    {!snapshot.selectionActive && (stacks ? !immersiveRoute(stackTop) : snapshot.contentVisible !== false) && !chromeHidden && (theme.isTV ? <TVRail authority={snapshot.authority} returnTo={currentPageFocus} libraries={snapshot.libraries} hasLiveTV={snapshot.hasLiveTV} destination={snapshot.railDestination ?? destination} profile={snapshot.profile} unread={snapshot.unreadNotifications} dimmed={destination.kind === 'home' && snapshot.homeVisible && !snapshot.mediaExpanded && !!railDim && !railDim.retired} onNavigate={next => {if (snapshot.contentVisible !== false && sameDestination(next, destination)) {expanded(false); runtime.scrollToTop(destination);} else if (next.kind === 'library') openLibrary(next.libraryId); else navigate(next);}}
      onProfile={() => setAccountOpen(true)} profileRef={avatarRef} onExpandedChange={expanded}/>
      : <>
        {sideNavigation && snapshot.contentVisible !== false && <MediumRail expanded={layout.kind === 'desktop' && sidebarExpanded} onToggle={layout.kind === 'desktop' ? toggleSidebar : undefined} serverName={snapshot.serverName} libraries={snapshot.libraries} hasLiveTV={snapshot.hasLiveTV} destination={railDestination} profile={snapshot.profile} unread={snapshot.unreadNotifications}
          serverAction={snapshot.account.serverAction && {id: snapshot.account.serverAction.id, label: snapshot.account.serverAction.label, onPress: snapshot.account.serverAction.onPress}}
          notificationAction={notifications && {label: notifications.label, onPress: notifications.onPress}}
          profileRef={avatarRef} short={dimensions.height - insets.top - insets.bottom < 800}
          onNavigate={next => {
            if (stacks && sameDestination(next, destination)) stacks.reselectTab(destination);
            else if (!stacks && snapshot.contentVisible !== false && sameDestination(next, destination)) runtime.scrollToTop(destination);
            else if (next.kind === 'library') openLibrary(next.libraryId); else navigate(next, next.kind !== 'libraries');
          }}
          onProfile={() => setAccountOpen(true)}/>}
        {!stacks && destination.kind === 'home' && layout.kind === 'phone' && <PhoneHomeHeader profile={snapshot.profile} unread={snapshot.unreadNotifications} avatarRef={avatarRef} onProfile={() => navigate({kind: 'settings'})} viewerRole={snapshot.account.viewerRole} serverAction={snapshot.account.serverAction} castControl={runtime.castControl} scrolled={homeScrolled}/>}
        {layout.tabs && <Animated.View pointerEvents={snapshot.mediaExpanded ? 'none' : 'box-none'} accessibilityElementsHidden={snapshot.mediaExpanded}
          importantForAccessibility={snapshot.mediaExpanded ? 'no-hide-descendants' : 'auto'} onLayout={event => listeningTabBar.measure(event.nativeEvent.layout.height)}
          style={[{position: 'absolute', left: 0, right: 0, bottom: 0, height: barHeight, zIndex: theme.layers.sticky}, listeningTabBar.style]}>
          <PhoneTabBar landscape={landscapePhone} destination={destination} hasLiveTV={snapshot.hasLiveTV} onSelect={tab => {void selectTab(tab).catch(runtime.reportError);}}/>
        </Animated.View>}
      </>)}
    {theme.isTV ? <TVProfileMenu snapshot={snapshot} open={accountOpen} onOpenChange={setAccountOpen} returnFocusRef={avatarRef}/>
      : <AccountMenu {...snapshot.account} profile={snapshot.profile} open={accountOpen} onOpenChange={setAccountOpen} anchorRef={avatarRef} returnFocusRef={avatarRef}/>}
  </>;
  return <NativeStackSlotsProvider value={stacks ? slots : undefined}><View testID="native-shell" style={{flex: 1, backgroundColor: theme.colors.ink, overflow: 'hidden'}}>
    {theme.isTV ? <View style={{position: 'absolute', left: layout.left, top: layout.top, width: layout.width, height: layout.height, transformOrigin: 'top left', transform: [{scale: layout.scale}]}}>{content}</View> : content}
    {runtime.playback && <NativePlaybackLayer adapter={runtime.playback} runtime={runtime} authority={snapshot.authority} contentInset={sideWidth} onTVListeningPresenceChange={theme.isTV ? setTVListeningPresent : undefined}/>}
    {runtime.renderWatch && <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1}}>{runtime.renderWatch()}</View>}
    {theme.isTV ? runtime.renderRoutes() : <Animated.View pointerEvents={initialPlayer ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer}
      importantForAccessibility={initialPlayer ? 'no-hide-descendants' : 'auto'} style={[{position: 'absolute', inset: 0}, initialTitleStyle]}>{runtime.renderRoutes()}</Animated.View>}
    {runtime.renderSelection()}
    {runtime.renderDownloads?.()}
    {runtime.renderPendingChanges?.()}
  </View></NativeStackSlotsProvider>;
}
