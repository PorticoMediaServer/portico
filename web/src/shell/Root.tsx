import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {Outlet, useRouter, useRouterState} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {useI18n} from './i18n';
import {useRouteFade, type RouteFadeCommits} from '../platform/route-motion';
import {Shell} from './Shell';
import {GlobalShortcuts} from './GlobalShortcuts';
import {useShellBindings} from './bindings';
import {finishRouteMotion, routeMotionOwnedByBrowser} from './route-motion';
import {webScreens, WebRetainedScreenHosts, useWebAction} from '../route-registry-view';
import {webScreenIdForRoute} from '../route-registry';
import {BootstrapContent, useShellBootstrapState} from './BootstrapState';
import {BootShell} from './Boot';
import {DestinationErrorBoundary, recoveryEntryOf, type DestinationRecoveryWitness} from './DestinationError';
import {useShellWidth} from './viewport';
import {useServerAway, useInsecureRouteRefused} from './ConnectionAway';
import {announceRoute, canAnnounceRoute, observeRouteHeading, routeDocumentTitle, routeHeading, routeLabelKey, routeTitleUpdates} from './route-accessibility';
import bootStyles from './boot.module.css';
import {currentWebEnvironment} from '../platform/environment';
import {HostedServicesHost, isHostedServicesPath, useHostedInPageNavigation} from '../screens/accounts/HostedServicesHost';

function RouteContent() {
  const i18n = useI18n() ?? defaultI18n;
  const content = useRef<HTMLDivElement>(null);
  const router = useRouter();
  const owner = useShellBindings();
  const routeKey = useRouterState({select: state => {
    const entry = state.location.state.__TSR_key;
    return typeof entry === 'string' && entry.length ? JSON.stringify([state.location.href, entry]) : undefined;
  }});
  const scopeKey = owner?.scopeKey ?? null;
  const commits = useMemo<RouteFadeCommits>(() => { /* Preserve the original ownership/invalidation keys. */ void scopeKey; return ({subscribe: listener => router.subscribe('onRendered', event => {
    // Same-page (query-only) changes keep the page still, as with View Transitions.
    if (!event.pathChanged) return;
    const entry = event.toLocation.state.__TSR_key;
    if (typeof entry === 'string' && entry.length) listener(JSON.stringify([event.toLocation.href, entry]));
  })}); }, [router, scopeKey]);
  // The recovery witness is selected from the actual location object, independently of the
  // optional motion route key above: a missing history entry must not collapse every href, and a
  // new location object with the same href is still a new recovery lifetime.
  const location = useRouterState({select: state => state.location});
  const bootstrap = useShellBootstrapState();
  const phase = bootstrap?.snapshot.phase;
  const [announcement, setAnnouncement] = useState<{text: string; seq: number}>({text: '', seq: 0});
  const acceptedPath = useRef<string | undefined>(undefined);
  const lastRouteHeading = useRef<string | undefined>(undefined);
  const recovery = useMemo<DestinationRecoveryWitness>(() => Object.freeze({
    location, href: location.href, entry: recoveryEntryOf(location), scopeKey,
  }), [location, scopeKey]);
  const target = useMemo(() => ({get current() { return routeMotionOwnedByBrowser() ? null : content.current; }}), []);
  useRouteFade(target, routeKey, commits);
  useLayoutEffect(() => { finishRouteMotion(); }, [routeKey]);
  useLayoutEffect(() => {
    const main = content.current;
    if (!main) return;
    let live = true, accepted = false, announcedLabel: string | undefined;
    const previousHeading = acceptedPath.current !== undefined && acceptedPath.current !== location.pathname ? lastRouteHeading.current : undefined;
    const current = () => live && content.current === main && main.isConnected && router.state.location === location;
    const fallbackLabel = () => i18n.t(routeLabelKey(location.pathname));
    const pushAnnouncement = (text: string) => {
      announcedLabel = text;
      setAnnouncement(prev => ({text, seq: prev.seq + 1}));
    };
    const updateTitle = () => {
      if (!current()) return;
      observer.reconcile();
      const heading = routeHeading(main, location.pathname);
      if (heading) lastRouteHeading.current = heading;
      const next = heading || fallbackLabel();
      document.title = routeDocumentTitle(next, i18n.t('next.platform.appName'));
      // If a sibling or async route committed before its real heading painted,
      // re-announce once the resolved heading arrives without stealing focus again.
      if (accepted && heading && heading !== announcedLabel && heading !== previousHeading && canAnnounceRoute(main)) {
        pushAnnouncement(heading);
      }
    };
    const titles = routeTitleUpdates(main, updateTitle);
    const title = () => titles.schedule();
    const observer = observeRouteHeading(main, location.pathname, title, {previousHeading});
    const commit = () => {
      if (!current()) return;
      title();
      if (accepted || phase === 'restoring' || !main.childElementCount) return;
      accepted = true;
      // Search typing, sorting, filters and hash changes retain their own focus.
      // Back/Forward between pages uses preventScroll after router restoration.
      if (acceptedPath.current === location.pathname) return;
      acceptedPath.current = location.pathname;
      const heading = routeHeading(main, location.pathname);
      if (heading) lastRouteHeading.current = heading;
      else if (location.pathname === '/') lastRouteHeading.current = undefined;
      const initialLabel = heading && heading !== previousHeading ? heading : fallbackLabel();
      announceRoute(main, initialLabel, pushAnnouncement);
    };
    const off = router.subscribe('onRendered', event => {
      if (event.toLocation === location || event.toLocation.href === location.href && event.toLocation.state.__TSR_key === location.state.__TSR_key) commit();
    });
    title();
    // A session may finish restoring after this route's original render event.
    if (router.state.resolvedLocation?.href === location.href && !router.state.isLoading) commit();
    return () => { live = false; off(); observer.dispose(); titles.dispose(); };
  }, [router, location, phase, i18n]);
  return <><main id="portico-content" ref={content} data-route-content data-shell-page tabIndex={-1}><BootstrapContent><DestinationErrorBoundary witness={recovery}><Outlet /></DestinationErrorBoundary></BootstrapContent></main>
    <span className={bootStyles.srOnly} role="status" aria-live="polite" aria-atomic="true">{announcement.text ? <span key={announcement.seq}>{announcement.text}</span> : null}</span></>;
}
export function ShellRoot() {
  const binding = useShellBindings();
  const bootstrap = useShellBootstrapState();
  const onFeedback = useWebAction('feedback');
  const routeId = useRouterState({select: state => state.matches.at(-1)?.routeId ?? ''});
  const screenId = webScreenIdForRoute(routeId);
  const viewer = binding?.shell.identity.viewer, phone = useShellWidth() < 600;
  const serverAway = useServerAway(binding?.api);
  const insecureRefused = useInsecureRouteRefused(binding?.api);
  const hosted = currentWebEnvironment().hostedWeb;
  const path = useRouterState({select: state => state.location.pathname});
  const accountPages = hosted && isHostedServicesPath(path);
  useHostedInPageNavigation(hosted);
  const content = accountPages ? null : bootstrap?.snapshot.phase === 'restoring' && bootstrap.snapshot.waiting ? <BootShell><RouteContent /></BootShell>
    : !binding || !viewer || screenId && (webScreens.chrome(screenId) !== 'shell' || phone && webScreens.phoneChrome(screenId) === 'standalone') ? <RouteContent />
    : <Shell {...binding.shell} serverAway={serverAway} insecureRefused={insecureRefused} onFeedback={onFeedback}><RouteContent /></Shell>;
  return <>{content}{hosted && <HostedServicesHost active={accountPages} />}{binding?.playback && <GlobalShortcuts key={binding.scopeKey} playback={binding.playback}/>}<WebRetainedScreenHosts /></>;
}
