import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View, useWindowDimensions} from 'react-native';
import Animated, {useAnimatedStyle} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {useInitialPlayerPagePaint} from '../kit/dock/PlayerMorphBridge';
import {useListeningTabBarPaint} from '../kit/dock/ListeningCoverBridge';
import {AccountMenu} from '../kit/shell';
import {Layer} from '../kit/shell/Layer';
import {nativeShellBackTransition} from './playback';
import {useTheme} from '../kit/theme';
import {useMotion} from '../kit/useMotion';
import {sameDestination} from './tv-focus';
import {HomeScreen} from './HomeScreen';
import {canOpenLibrary, librariesDestination, mediumRailMetrics, phoneBarHeight, sameAuthority, selectedTab, shellLayout, type ShellDestination, type ShellTab} from './model';
import {nativeScreenChromeHidden, subscribeNativeScreenChrome} from './chrome';
import type {NativeShellRuntime, NativeShellSnapshot} from './runtime';
import type {NativeShellRoute} from './routes';
import {PhoneTabBar} from './PhoneTabBar';
import {PhoneHomeHeader} from './PhoneHomeHeader';
import {PlayOnButton} from './PlayOnButton';
import {MediumRail} from './MediumRail';
import {pageInsetElement} from '../kit/layout/page-inset';
import {NativePlaybackLayer} from './NativePlaybackLayer';
import {NativeStackSlots, NativeStackSlotsProvider} from '../kit/navigation/stack-slot';
import {useRailHeld} from '../kit/navigation/rail-hold';
import {TouchStacks, nativeStackMotionTier, type NativeStackPage} from './TouchStacks';
import {ShellUnreachableLine} from './UnreachableLine';

const noStacks = () => () => {};
const topRoute = (stacks: NativeShellRuntime['stacks']): NativeShellRoute | undefined => {
  const value = stacks?.getSnapshot();
  return value?.stacks.find(stack => stack.tab === value.active)?.presentations.at(-1)?.route;
};
/** Pages that take the whole screen, without the tab bar. */
const immersiveRoute = (route: NativeShellRoute | undefined) => route?.kind === 'read' || route?.kind === 'channel';

export function TouchShell({runtime, snapshot}: {runtime: NativeShellRuntime; snapshot: NativeShellSnapshot}) {
  const initialPlayer = useInitialPlayerPagePaint();
  const listeningTabBar = useListeningTabBarPaint();
  const initialTitle = initialPlayer?.title;
  const theme = useTheme(), motion = useMotion(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  const chromeHidden = useSyncExternalStore(subscribeNativeScreenChrome, nativeScreenChromeHidden);
  const layout = shellLayout(dimensions.width, dimensions.height, false);
  /** The sidebar's own breakpoint, captured so the hook reads a plain value. */
  const wideSidebar = dimensions.width >= 1024;
  // Touch hosts each tab's pages in a native stack (motion.html 2.9-2.11).
  const stacks = runtime.stacks, slots = useMemo(() => new NativeStackSlots(), []);
  const stackTop = useSyncExternalStore(stacks?.subscribe ?? noStacks, () => topRoute(stacks), () => topRoute(stacks));
  const destination = snapshot.destination;
  // Compact Settings is a modal over the last committed destination. Keep that
  // native subtree mounted so its scroll position and painted content survive.
  const previousPage = useRef<ShellDestination>({kind: 'home'});
  const settingsPage = destination.kind === 'settings';
  const settingsSheet = settingsPage && theme.platform === 'phone';
  const paintedDestination = settingsPage ? previousPage.current : destination;
  useLayoutEffect(() => {if (destination.kind !== 'settings') previousPage.current = destination;}, [destination]);
  const [remembered, setRemembered] = useState<string>(), [memoryReady, setMemoryReady] = useState(false);
  const lastOpenedLibrary = useRef<string | undefined>(undefined);
  const [accountOpen, setAccountOpen] = useState(false);
  const [homeScrolled, setHomeScrolled] = useState(false);
  const [sidebarExpanded, setSidebarExpanded] = useState(true);
  // tablet-play6: a page's open side panel folds the sidebar to the rail; the remembered choice stays.
  const railHeld = useRailHeld();
  const sidebarRevision = useRef(0);
  const sidebarLifetime = useRef<object | undefined>(undefined);
  const avatarRef = useRef<View>(null), opening = useRef(0), alive = useRef<{runtime: NativeShellRuntime} | undefined>(undefined);
  const current = useCallback(() => alive.current?.runtime === runtime && !runtime.retirementSignal?.aborted && sameAuthority(snapshot.authority, runtime.getSnapshot().authority), [runtime, snapshot.authority]);
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
    if (!wideSidebar) return;
    const lifetime = {}, authority = {...snapshot.authority}, revision = sidebarRevision.current;
    sidebarLifetime.current = lifetime;
    const valid = () => sidebarLifetime.current === lifetime && current() && sameAuthority(authority, runtime.getSnapshot().authority);
    void runtime.memory.readSidebar(authority, valid).then(value => {
      if (valid() && revision === sidebarRevision.current) setSidebarExpanded(value);
    }, error => {if (valid()) runtime.reportError(error);});
    return () => {if (sidebarLifetime.current === lifetime) sidebarLifetime.current = undefined;};
  }, [current, runtime, snapshot.authority, wideSidebar]);
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
      || !canOpenLibrary(snapshot.libraries, destination.libraryId, false) || lastOpenedLibrary.current === destination.libraryId) return;
    const id = destination.libraryId;
    lastOpenedLibrary.current = id; opening.current++; setRemembered(id); setMemoryReady(true);
    void runtime.memory.remember(snapshot.authority, id).catch(runtime.reportError);
  }, [current, destination, runtime, snapshot.authority, snapshot.contentVisible, snapshot.libraries]);
  useEffect(() => {
    if (destination.kind === 'library' && !canOpenLibrary(snapshot.libraries, destination.libraryId, false)) runtime.onDestination({kind: 'libraries'});
    if (destination.kind === 'live' && !snapshot.hasLiveTV) runtime.onDestination({kind: 'home'});
  }, [destination, runtime, snapshot.hasLiveTV, snapshot.libraries]);
  const navigate = (next: ShellDestination, anyRoot = false) => {
    if (!current()) return;
    setAccountOpen(false); opening.current++;
    // H14: the chrome and the page stay painted; only the route's content changes, and its own
    // motion owns that (as web's page rule). The page area is never reset to 0, which blanked a frame.
    if (stacks) stacks.selectTab(next, anyRoot); else runtime.onDestination(next);
  };
  const openLibrary = (id: string) => {
    if (!current() || !canOpenLibrary(snapshot.libraries, id, false)) return;
    navigate({kind: 'library', libraryId: id});
  };
  const selectTab = async (tab: ShellTab['id']) => {
    if (!current()) return;
    // A tab or rail destination chosen while the watch page is open goes there: the player folds into its mini player, as
    // Back does, instead of the watch page staying drawn over the new screen (W3 F7).
    const playback = runtime.playback, fold = playback && nativeShellBackTransition(playback.dock, playback.getSnapshot(), snapshot.authority, false);
    if (playback && fold) {void playback.onTransition(fold).catch(runtime.reportError); if (selectedTab(destination) === tab) return;}
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
    navigate(librariesDestination(id, runtime.getSnapshot().libraries, false));
  };
  const initialTitleStyle = useAnimatedStyle(() => ({opacity: initialTitle?.value ?? 1}));
  const barHeight = layout.tabs && !chromeHidden ? phoneBarHeight(insets.bottom) : 0;
  // foldable3e/3f: the phone bar leaves only on medium; the dock floats at the foot.
  const sideNavigation = layout.rail || layout.kind === 'desktop';
  const sidebarOpen = sidebarExpanded && !railHeld;
  const sideWidth = layout.kind === 'desktop' && sidebarOpen ? 248 : mediumRailMetrics.width;
  const homeBottom = sideNavigation ? 92 : barHeight, pageBottom = sideNavigation ? 16 : barHeight;
  const railDestination = snapshot.railDestination ?? destination;
  const notifications = snapshot.account.actions.find(action => action.id === 'notifications');
  // Only the covered destination leaves the accessibility tree. The watch
  // host's visible title/episode panes remain siblings in their own outlet.
  const baseHidden = snapshot.contentVisible === false || !!snapshot.mediaExpanded;
  const homePage = (active: boolean, obscured: boolean) => <HomeScreen runtime={runtime} authority={snapshot.authority} playOnHidden={!!snapshot.selectionActive || chromeHidden} active={active} heroObscured={obscured} railExpanded={false} bottomInset={homeBottom} onScrolledChange={setHomeScrolled}/>;
  const phoneHomeHeader = <PhoneHomeHeader profile={snapshot.profile} unread={snapshot.unreadNotifications} avatarRef={avatarRef} onProfile={() => navigate({kind: 'settings'})} viewerRole={snapshot.account.viewerRole} serverAction={snapshot.account.serverAction} castControl={<PlayOnButton playOn={runtime.playOn} variant="plain"/>} activity={runtime.ownerActivity} scrolled={homeScrolled}/>;
  /** One shell destination inside its stack screen: a tab's first screen, a library opened
   * from Libraries, or Settings (the modal stack's transparent root over the live page). */
  // A shell page sits beside the sidebar or rail, as a pushed page does, so it is told that width (usePageWidth): without
  // it a tablet's library grid sized six columns for the whole window and drew four, leaving a gap at the right (iPad).
  const renderShellPage = (page: NativeStackPage) => pageInsetElement(sideNavigation ? sideWidth : 0, renderShellPageBody(page));
  const renderShellPageBody = ({presentation, top, tabActive, modal}: NativeStackPage) => {
    const page = presentation.route.kind === 'shell' ? presentation.route.destination : presentation.destination;
    if (modal) return <View style={{flex: 1, paddingLeft: sideNavigation ? sideWidth : 0, paddingBottom: settingsSheet ? 0 : pageBottom}}>
      {!settingsSheet && <Layer style={{position: 'absolute', inset: 0}}/>}
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
  // N2-3, tablet 6 and 8: on a tablet a pushed page sits in the width beside the rail, which stays.
  // The reader and live TV are full-screen.
  const pushedInset = (route: NativeShellRoute) => sideNavigation && !immersiveRoute(route) ? sideWidth : 0;
  const pushedPage = (route: NativeShellRoute, node: React.ReactNode) => <Animated.View pointerEvents={initialPlayer ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer}
    importantForAccessibility={initialPlayer ? 'no-hide-descendants' : 'auto'} style={[{position: 'absolute', top: 0, left: pushedInset(route), right: 0, bottom: immersiveRoute(route) ? 0 : barHeight}, initialTitleStyle]}>{pageInsetElement(pushedInset(route), node)}</Animated.View>;
  const basePage = <>
    {paintedDestination.kind === 'home' ? <HomeScreen runtime={runtime} authority={snapshot.authority} playOnHidden={!!snapshot.selectionActive || chromeHidden} active={!settingsPage && !snapshot.mediaExpanded && snapshot.homeVisible} heroObscured={settingsPage || snapshot.homeObscured} railExpanded={false} bottomInset={homeBottom} onScrolledChange={setHomeScrolled}/>
      : <View style={{flex: 1, paddingBottom: pageBottom}}>
        {runtime.renderDestination(paintedDestination, openLibrary)}
      </View>}
  </>;
  const content = <>
    {stacks ? <View pointerEvents="box-none" accessibilityElementsHidden={!!snapshot.mediaExpanded} importantForAccessibility={snapshot.mediaExpanded ? 'no-hide-descendants' : 'auto'} style={{position: 'absolute', inset: 0}}>
      <TouchStacks port={stacks} slots={slots} tier={nativeStackMotionTier(motion)} background={theme.colors.ink} renderShell={renderShellPage} routeStyle={pushedPage} shellInset={sideNavigation ? sideWidth : 0}/>
    </View> : <>
      <Animated.View pointerEvents={settingsPage ? 'none' : 'auto'} accessibilityElementsHidden={baseHidden || settingsPage} importantForAccessibility={baseHidden || settingsPage ? 'no-hide-descendants' : 'auto'} style={[{flex: 1}, settingsPage && !settingsSheet ? {display: 'none'} : null, sideNavigation ? {paddingLeft: sideWidth} : null]}>
        {basePage}
      </Animated.View>
      {settingsPage && <View style={{position: 'absolute', inset: 0, paddingLeft: sideNavigation ? sideWidth : 0, paddingBottom: settingsSheet ? 0 : pageBottom}}>{runtime.renderDestination(destination, openLibrary)}</View>}
    </>}
    {!snapshot.selectionActive && (stacks ? !immersiveRoute(stackTop) : snapshot.contentVisible !== false) && !chromeHidden && (<>
      {sideNavigation && (!!stacks || snapshot.contentVisible !== false) && <MediumRail expanded={layout.kind === 'desktop' && sidebarOpen} onToggle={layout.kind === 'desktop' && !railHeld ? toggleSidebar : undefined} serverName={snapshot.serverName} libraries={snapshot.libraries} hasLiveTV={snapshot.hasLiveTV} destination={railDestination} profile={snapshot.profile} unread={snapshot.unreadNotifications}
        serverAction={snapshot.account.serverAction && {id: snapshot.account.serverAction.id, label: snapshot.account.serverAction.label, onPress: snapshot.account.serverAction.onPress}} activity={runtime.ownerActivity}
        notificationAction={notifications && {label: notifications.label, onPress: notifications.onPress}}
        profileRef={avatarRef} short={dimensions.height - insets.top - insets.bottom < 800}
        onNavigate={next => {
          if (stacks && sameDestination(next, destination)) stacks.reselectTab(destination);
          else if (!stacks && snapshot.contentVisible !== false && sameDestination(next, destination)) runtime.scrollToTop(destination);
          else if (next.kind === 'library') openLibrary(next.libraryId); else navigate(next, next.kind !== 'libraries');
        }}
        onProfile={() => setAccountOpen(true)}/>}
      {!stacks && destination.kind === 'home' && layout.kind === 'phone' && <PhoneHomeHeader profile={snapshot.profile} unread={snapshot.unreadNotifications} avatarRef={avatarRef} onProfile={() => navigate({kind: 'settings'})} viewerRole={snapshot.account.viewerRole} serverAction={snapshot.account.serverAction} castControl={<PlayOnButton playOn={runtime.playOn} variant="plain"/>} activity={runtime.ownerActivity} scrolled={homeScrolled}/>}
      {layout.tabs && <Animated.View pointerEvents={snapshot.mediaExpanded ? 'none' : 'box-none'} accessibilityElementsHidden={snapshot.mediaExpanded}
        importantForAccessibility={snapshot.mediaExpanded ? 'no-hide-descendants' : 'auto'} onLayout={event => listeningTabBar.measure(event.nativeEvent.layout.height)}
        style={[{position: 'absolute', left: 0, right: 0, bottom: 0, height: barHeight, zIndex: theme.layers.sticky}, listeningTabBar.style]}>
        <PhoneTabBar destination={destination} hasLiveTV={snapshot.hasLiveTV} onSelect={tab => {void selectTab(tab).catch(runtime.reportError);}}/>
      </Animated.View>}
    </>)}
    <AccountMenu {...snapshot.account} profile={snapshot.profile} open={accountOpen} onOpenChange={setAccountOpen} anchorRef={avatarRef} returnFocusRef={avatarRef}/>
  </>;
  return <NativeStackSlotsProvider value={stacks ? slots : undefined}><View testID="native-shell" style={{flex: 1, overflow: 'hidden'}}>
    {content}
    {runtime.playback && <NativePlaybackLayer adapter={runtime.playback} runtime={runtime} authority={snapshot.authority} contentInset={sideWidth}/>}
    {runtime.renderWatch && <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1}}>
      {runtime.renderWatch()}</View>}
    <Animated.View pointerEvents={initialPlayer ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer}
      importantForAccessibility={initialPlayer ? 'no-hide-descendants' : 'auto'} style={[{position: 'absolute', inset: 0}, initialTitleStyle]}>{runtime.renderRoutes()}</Animated.View>
    <ShellUnreachableLine reachability={runtime.reachability} serverName={snapshot.serverName} layout={layout}
      hidden={!!snapshot.mediaExpanded || (stacks ? immersiveRoute(stackTop) : false)}/>
    {runtime.renderSelection()}
    {runtime.renderDownloads?.()}
  </View></NativeStackSlotsProvider>;
}
