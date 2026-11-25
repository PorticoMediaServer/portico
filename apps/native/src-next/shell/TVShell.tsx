import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View, useWindowDimensions} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {layout as designLayout} from '@portico/design';
import {useTheme} from '../kit/theme';
import {useMotion} from '../kit/useMotion';
import {registerBack} from '../kit/navigation/back';
import {registerTVMenuFallback} from '../platform/menu-key';
import {useTVMenuCapture} from '../platform/tv-menu';
import {CoveredLayer, CoveredSignal, createTVLayerStack, provideTVPage, requestNativeFocus, tvLayerSpecs, tvPageModal, tvRouteLayerKeys, useFocusRegistry, type FocusSnapshot} from '../kit/focus';
import {tvFocusIds, sameDestination, tvRouteKeepsRail} from './tv-focus';
import {HomeScreen} from './HomeScreen';
import {canYieldTVHomeMenu, homeStartModel, resolveTVHomeStart} from '../screens/home/home-start';
import {authorityKey, canOpenLibrary, mediumRailMetrics, sameAuthority, shellLayout, type ShellDestination} from './model';
import {nativeScreenChromeHidden, subscribeNativeScreenChrome} from './chrome';
import type {NativeShellRuntime, NativeShellSnapshot} from './runtime';
import {TVRail} from './TVRail';
import {TVProfileMenu} from './TVProfileMenu';
import {NativePlaybackLayer} from './NativePlaybackLayer';
import {ShellUnreachableLine} from './UnreachableLine';

const noNavigation = () => () => {};

export function TVShell({runtime, snapshot}: {runtime: NativeShellRuntime; snapshot: NativeShellSnapshot}) {
  const theme = useTheme(), motion = useMotion(), dimensions = useWindowDimensions();
  const chromeHidden = useSyncExternalStore(subscribeNativeScreenChrome, nativeScreenChromeHidden);
  const layout = shellLayout(dimensions.width, dimensions.height, true);
  const destination = snapshot.destination, registry = useFocusRegistry(), ids = useMemo(() => tvFocusIds(snapshot.authority), [snapshot.authority]);
  const [tvListeningPresent, setTVListeningPresent] = useState(false);
  const tvListeningPresentRef = useRef(tvListeningPresent); tvListeningPresentRef.current = tvListeningPresent;
  /** The element the rail returns to, per page (a rail page, or a pushed page that keeps the rail). */
  const pageFocus = useRef(new Map<string, FocusSnapshot>());
  const railRelease = useRef<(() => void) | undefined>(undefined);
  /** Whose element the rail returns to: the rail page, or a pushed page that keeps the rail. Read from navigation when
   * asked, because a pushed page's first element is focused before this shell renders again. */
  const pageOwner = useRef((): string | undefined => undefined);
  pageOwner.current = () => {
    const nav = runtime.navigation?.getSnapshot();
    if (nav && nav.route.kind !== 'shell') {
      const top = runtime.navigation?.presentationStack?.().at(-1);
      return tvRouteKeepsRail(nav.route) ? `route:${top?.occurrence ?? nav.revision}` : undefined;
    }
    return JSON.stringify(runtime.getSnapshot().destination);
  };
  useEffect(() => {
    const original = runtime.retirementSignal;
    let lastSeen: FocusSnapshot | undefined;
    const remember = () => {
      // Native region cleanup may publish before React's passive unsubscribe.
      // Refuse that original observer before touching any retired business accessor.
      if (original?.aborted) return;
      const now = runtime.getSnapshot(), saved = registry.capture();
      // Only a focus move counts: when a pushed page closes, navigation already names the page under it while focus
      // is still on the closing page's element, which must not become that page's.
      const moved = saved?.regionId !== lastSeen?.regionId || saved?.childId !== lastSeen?.childId;
      lastSeen = saved;
      if (!moved) return;
      // A pushed page that keeps the rail is the rail's page: its element is the one to return to.
      const owner = pageOwner.current();
      if (!owner || now.mediaExpanded || tvListeningPresent || !owner.startsWith('route:') && (now.contentVisible === false || now.homeObscured
        || now.destination.kind === 'home' && !now.homeVisible)) return;
      if (saved && saved.regionId !== ids.rail && registry.config(saved.regionId)?.hold !== true) {
        const pages = pageFocus.current;
        pages.delete(owner); pages.set(owner, saved);
        if (pages.size > 16) pages.delete(pages.keys().next().value!);
      }
    };
    remember();
    if (original?.aborted) return;
    const off = registry.subscribe(remember);
    original?.addEventListener('abort', off, {once: true});
    if (original?.aborted) off();
    return () => {original?.removeEventListener('abort', off); off();};
  }, [ids, registry, runtime, tvListeningPresent]);
  /** The page's and the routes' own layers (CoveredLayer hosts): a layer that takes focus asks its host, which
   * lands on the element last focused there or its first focusable. */
  const baseLayer = useRef<View>(null), routeLayer = useRef<View>(null);
  /** Search keeps its page alive in the route layer (shell/search-route.tsx), so on Search that layer is the page's. */
  const pageInRouteLayer = useRef(false);
  pageInRouteLayer.current = destination.kind === 'search';
  /** TV-FOCUS-REVIEW §5: one layer stack owns which full-screen layer is on top, what is covered, and where focus
   * goes when a layer opens over another or closes (W-9, W-12). */
  const railRegion = useRef(ids.rail); railRegion.current = ids.rail;
  const layers = useMemo(() => createTVLayerStack(registry, undefined, undefined, focus => focus.regionId === railRegion.current), [registry]);
  useEffect(() => () => layers.dispose(), [layers]);
  const navigationPort = runtime.navigation;
  const routeLayerKeys = useSyncExternalStore(navigationPort?.subscribe ?? noNavigation, () => tvRouteLayerKeys(navigationPort), () => '[]');
  // A TV page `Modal` over the shell (a refused content link): every layer under it is covered (context only).
  const pageModalOpen = useSyncExternalStore(tvPageModal.subscribe, tvPageModal.getSnapshot, () => false);
  const layerSpecs = useMemo(() => {
    const routes = JSON.parse(routeLayerKeys) as string[];
    // A runtime without the Back stack still says whether a route covers the page.
    if (!routes.length && snapshot.contentVisible === false) routes.push('route:current');
    return tvLayerSpecs(routes, snapshot.mediaExpanded === true, tvListeningPresent, () => pageInRouteLayer.current ? routeLayer.current : baseLayer.current, () => routeLayer.current);
  }, [routeLayerKeys, snapshot.contentVisible, snapshot.mediaExpanded, tvListeningPresent]);
  const topLayer = layerSpecs.at(-1)?.key;
  /** Choice 16: a title, person, collection or See all page pushed over a rail page keeps the closed rail. */
  const routeRail = !!topLayer?.startsWith('route:') && !!navigationPort && tvRouteKeepsRail(navigationPort.getSnapshot().route);
  const focusOwner = pageOwner.current();
  const currentPageFocus = focusOwner === undefined ? undefined : pageFocus.current.get(focusOwner);
  useLayoutEffect(() => {if (!runtime.retirementSignal?.aborted) layers.sync(layerSpecs);}, [layers, layerSpecs, runtime]);
  useEffect(() => () => {railRelease.current?.();}, []);
  const [accountOpen, setAccountOpen] = useState(false), [railExpanded, setRailExpanded] = useState(false);
  // A mounted page state may dim the rail while its own copy is the only thing to read. The
  // lease is the caller's own object, so a stale release can never clear a successor's lease.
  const [railDim, setRailDim] = useState<Readonly<{retired: boolean}>>();
  const holdRailDim = useCallback((lease: Readonly<{retired: boolean}>) => setRailDim(lease), []);
  const releaseRailDim = useCallback((lease: Readonly<{retired: boolean}>) => setRailDim(current => current === lease ? undefined : current), []);
  const [homeScrolled, setHomeScrolled] = useState(false);
  const homeStartNow = useCallback(() => {
    const document = runtime.home.getSnapshot().document, mixes = runtime.mixes?.getSnapshot();
    return resolveTVHomeStart(registry, homeStartModel(document, snapshot.authority, {
      readLibraryIds: new Set(snapshot.libraries.filter(library => library.kind === 'books').map(library => library.id)),
      mixesReady: mixes?.phase === 'ready' && mixes.rows.length > 0, playbackAvailable: !!runtime.playback,
    }));
  }, [registry, runtime, snapshot.authority, snapshot.libraries]);
  const getFocusSnapshot = useMemo(() => {
    let previous: Readonly<{regionId?: string; childId?: string; homeStart: boolean}> | undefined;
    return () => {
      if (runtime.retirementSignal?.aborted) return previous ??= {homeStart: false};
      const focused = registry.capture();
      let homeStart = false;
      if (snapshot.destination.kind === 'home' && snapshot.homeVisible && !snapshot.mediaExpanded && !tvListeningPresent
        && snapshot.contentVisible !== false) {
        const start = homeStartNow();
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
  }, [accountOpen, chromeHidden, homeScrolled, homeStartNow, ids.rail, railExpanded, registry, runtime, snapshot, tvListeningPresent]);
  // DS-427 (4 Nov amendment): a page that changed while a TV page `Modal` covered it (a profile switch, a route or
  // the player opened under it) opens on its own entry once the Modal goes, not the old page's element. Asked when
  // the Modal's hold ends, before this shell uncovers its layers: Home's declared start (#636), else the top layer's
  // host (its first control). A control that declares its start asks for itself once uncovered, so it lands last
  // and wins. The host is never asked when Home declares a start: that request would land first, and Home's own
  // entry would then count it as already owned. The player and the listening screen claim themselves.
  const pageIdentity = useRef(''), enterPage = useRef(() => {});
  pageIdentity.current = JSON.stringify([authorityKey(snapshot.authority), snapshot.profile.id, destination, topLayer]);
  enterPage.current = () => {
    if (topLayer === 'page' && destination.kind === 'home') {
      const start = homeStartNow();
      // Not ready yet: Home's own entry takes it when it is.
      if (start.kind === 'ready') registry.focus(start.address);
    } else if (topLayer === 'page') requestNativeFocus(pageInRouteLayer.current ? routeLayer.current : baseLayer.current);
    else if (topLayer?.startsWith('route:')) requestNativeFocus(routeLayer.current);
  };
  useEffect(() => provideTVPage({identity: () => pageIdentity.current, enter: () => enterPage.current()}), []);
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
  useTVMenuCapture(!homeStart);
  const avatarRef = useRef<View>(null), alive = useRef<{runtime: NativeShellRuntime} | undefined>(undefined);
  const shift = useSharedValue(0), opened = useSharedValue(0);
  const current = useCallback(() => alive.current?.runtime === runtime && !runtime.retirementSignal?.aborted && sameAuthority(snapshot.authority, runtime.getSnapshot().authority), [runtime, snapshot.authority]);
  useEffect(() => {
    const lifetime = alive, session = {runtime};
    lifetime.current = session;
    return () => {
      if (lifetime.current !== session) return;
      lifetime.current = undefined;
    };
  }, [runtime]);
  useEffect(() => {
    if (destination.kind === 'library' && !canOpenLibrary(snapshot.libraries, destination.libraryId, true)) runtime.onDestination({kind: 'libraries'});
    if (destination.kind === 'live' && !snapshot.hasLiveTV) runtime.onDestination({kind: 'home'});
  }, [destination, runtime, snapshot.hasLiveTV, snapshot.libraries]);
  const navigate = (next: ShellDestination) => {
    if (!current()) return;
    setAccountOpen(false);
    // H14: the chrome and the page stay painted; only the route's content changes, and its own
    // motion owns that (as web's page rule). The page area is never reset to 0, which blanked a frame.
    runtime.onDestination(next);
  };
  const openLibrary = (id: string) => {
    if (!current() || !canOpenLibrary(snapshot.libraries, id, true)) return;
    navigate({kind: 'library', libraryId: id});
  };
  const expanded = useCallback((value: boolean) => {
    setRailExpanded(value);
    const owner = pageOwner.current(), saved = owner === undefined ? undefined : pageFocus.current.get(owner);
    if (value && !railRelease.current && saved) railRelease.current = registry.retain(saved);
    if (!value) {railRelease.current?.(); railRelease.current = undefined;}
    // tv.html f2 moves the copy's left edge from200 to470. The frame wins the stale220 token.
    shift.value = motion.transforms ? withTiming(value ? 270 : 0, motion.timing('standard')) : value ? 270 : 0;
    // motion.html 4.2: the scrim's darkness follows the same timing as the slide, so closed and open cross-fade.
    opened.value = withTiming(value ? 1 : 0, motion.timing('standard'));
  }, [motion, opened, registry, shift]);
  useEffect(() => {
    const original = runtime.retirementSignal;
    if (original?.aborted) return;
    // TV rail Back runs in the `surface` scope ONLY when the rail holds focus and no player/overlay is active,
    // so overlays and expanded media take precedence and the rail still closes before any page Back handler.
    const off = registerBack(() => {
      if (original?.aborted || registry.capture()?.regionId !== ids.rail) return false;
      const owner = pageOwner.current(), saved = owner === undefined ? undefined : pageFocus.current.get(owner);
      expanded(false);
      // A pushed page whose element wasn't recorded gets focus back through its layer, which remembers it natively.
      if (!registry.restore(saved)) {if (owner?.startsWith('route:')) requestNativeFocus(routeLayer.current); else runtime.scrollToTop(runtime.getSnapshot().destination);}
      return true;
    }, {
      scope: 'surface',
      active: () => {
        if (original?.aborted) return false;
        const now = runtime.getSnapshot();
        return !now.mediaExpanded && !tvListeningPresentRef.current && !now.selectionActive && !now.homeObscured
          && registry.capture()?.regionId === ids.rail;
      },
    });
    original?.addEventListener('abort', off, {once: true});
    if (original?.aborted) off();
    return () => {original?.removeEventListener('abort', off); off();};
  }, [runtime, registry, ids, expanded]);
  useEffect(() => {
    const original = runtime.retirementSignal;
    if (original?.aborted) return;
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
  }, [accountOpen, chromeHidden, current, expanded, ids.rail, registry, runtime, tvListeningPresent]);
  useEffect(() => {if (!motion.transforms) shift.value = railExpanded ? 270 : 0;}, [motion.transforms, railExpanded, shift]);
  const pageStyle = useAnimatedStyle(() => ({transform: [{translateX: shift.value}]}));
  const openScrim = useAnimatedStyle(() => ({opacity: opened.value}));
  const closedScrim = useAnimatedStyle(() => ({opacity: 1 - opened.value}));
  // On TV the page is covered whenever another layer is on top (the stack's one answer).
  const baseHidden = topLayer !== 'page';
  const basePage = <>
    {destination.kind === 'home' ? <HomeScreen runtime={runtime} authority={snapshot.authority} playOnHidden={!!snapshot.selectionActive || chromeHidden} active={!snapshot.mediaExpanded && snapshot.homeVisible} heroObscured={snapshot.homeObscured} railExpanded={railExpanded} bottomInset={0} onScrolledChange={setHomeScrolled} holdRailDim={holdRailDim} releaseRailDim={releaseRailDim}/>
      : <View style={{flex: 1}}>
        {runtime.renderDestination(destination, openLibrary)}
      </View>}
  </>;
  const tvRail = <TVRail authority={snapshot.authority} returnTo={currentPageFocus} libraries={snapshot.libraries} hasLiveTV={snapshot.hasLiveTV} destination={snapshot.railDestination ?? destination} profile={snapshot.profile} unread={snapshot.unreadNotifications} dimmed={destination.kind === 'home' && snapshot.homeVisible && !snapshot.mediaExpanded && !!railDim && !railDim.retired} onNavigate={next => {
    if (snapshot.contentVisible !== false && sameDestination(next, destination)) {expanded(false); runtime.scrollToTop(destination); return;}
    // F-RAIL: OK opens that page and the rail closes; the page's own entry takes focus (its starting focus).
    expanded(false);
    if (next.kind === 'library') openLibrary(next.libraryId); else navigate(next);
  }}
  onProfile={() => setAccountOpen(true)} profileRef={avatarRef} onExpandedChange={expanded}/>;
  /** The rail and its dim sit in the route layer, above the page under them and under that page's own sheets: a rail
   * page (Search is kept alive there) or a pushed page that keeps the rail. */
  const railShown = topLayer === 'page' ? snapshot.contentVisible !== false : routeRail;
  const routeRailLayer = railShown && !snapshot.selectionActive && !chromeHidden ? <View pointerEvents="box-none" style={{position: 'absolute', left: layout.left, top: layout.top,
    width: layout.width, height: layout.height, transformOrigin: 'top left', transform: [{scale: layout.scale}]}}>
    {/* The rail's one scrim: the closed rail's own fade, cross-fading to the open panel running into one even dim over the page
        (tv.html f2). Plain views with a gradient and no input: tvOS's focus engine looks through them. */}
    <Animated.View pointerEvents="none" style={[{position: 'absolute', inset: 0, experimental_backgroundImage: tvRailScrim(theme.colors.ink, false)}, closedScrim]}/>
    <Animated.View pointerEvents="none" style={[{position: 'absolute', inset: 0, experimental_backgroundImage: tvRailScrim(theme.colors.ink, true)}, openScrim]}/>
    {tvRail}
  </View> : undefined;
  const content = <>
    <Animated.View pointerEvents="auto" accessibilityElementsHidden={baseHidden} importantForAccessibility={baseHidden ? 'no-hide-descendants' : 'auto'} style={[{flex: 1}, pageStyle]}>
      {/* A pushed TV route covers this page: nothing under it may take D-pad focus (CoveredLayer). */}
      <CoveredSignal covered={pageModalOpen}><CoveredLayer covered={baseHidden} hostRef={baseLayer} style={{flex: 1}} testID="native-shell-base">{basePage}</CoveredLayer></CoveredSignal>
    </Animated.View>
    <TVProfileMenu snapshot={snapshot} open={accountOpen} onOpenChange={setAccountOpen} returnFocusRef={avatarRef}/>
  </>;
  return <View testID="native-shell" style={{flex: 1, overflow: 'hidden'}}>
    <View style={{position: 'absolute', left: layout.left, top: layout.top, width: layout.width, height: layout.height, transformOrigin: 'top left', transform: [{scale: layout.scale}]}}>{content}</View>
    {runtime.playback && <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.player}}>
      {/* TV: under a page `Modal` (a refused link) the player and the listening screen are covered (context only), so
          nothing in them (the chrome's preferred picture as it hides) asks for focus in its own window. No native view. */}
      <CoveredSignal covered={pageModalOpen}><NativePlaybackLayer adapter={runtime.playback} runtime={runtime} authority={snapshot.authority} contentInset={mediumRailMetrics.width} onTVListeningPresenceChange={setTVListeningPresent}/></CoveredSignal>
    </View>}
    {runtime.renderWatch && <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1}}>
      <CoveredSignal covered={pageModalOpen}>{runtime.renderWatch()}</CoveredSignal>
    </View>}
    <CoveredSignal covered={pageModalOpen}>{runtime.renderRoutes({covered: !topLayer?.startsWith('route:') && !pageInRouteLayer.current, hostRef: routeLayer, rail: routeRailLayer, wrapPages: routeRail ? pages => <Animated.View pointerEvents="box-none" style={[{position: 'absolute', inset: 0}, pageStyle]}>{pages}</Animated.View> : undefined})}</CoveredSignal>
    <ShellUnreachableLine reachability={runtime.reachability} serverName={snapshot.serverName} layout={layout}
      hidden={!!snapshot.mediaExpanded || tvListeningPresent}/>
    {runtime.renderSelection()}
    {runtime.renderDownloads?.()}
  </View>;
}

/** The rail's one scrim (tv.html f1/f2). Closed: the rail's own fade, gone before the page's first column (the gutter, 200),
 * less the R1 room a focused card grows into (20), so a first-column card and its caption are never dimmed. tv.html runs it
 * to the rail's 260 edge, over the column. Open: the 560 panel, then the page dimmed evenly to the right edge, so nothing
 * under or beside it reads as cut. */
export function tvRailScrim(ink: string, open: boolean): string {
  const at = (alpha: number) => `${ink.slice(0, 7)}${Math.round(alpha * 255).toString(16).padStart(2, '0')}`;
  const clear = designLayout.gutter.tv - railFocusRoom;
  return open
    ? `linear-gradient(to right, ${at(.99)} 0px, ${at(.96)} 370px, ${at(.42)} 560px, ${at(.42)} 100%)`
    : `linear-gradient(to right, ${at(.78)} 0px, ${at(.42)} ${Math.round(clear * .45)}px, ${at(0)} ${clear}px)`;
}
/** FRAMES "TV rules": a free-standing control's ring plus its growth needs 20 each side. */
const railFocusRoom = 20;
