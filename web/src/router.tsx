import {createRootRoute, createRoute, createRouter, lazyRouteComponent, redirect, type AnyRoute} from '@tanstack/react-router';
import {WebScreenOutlet, webScreens} from './route-registry-view';
import type {WebScreenId, WebScreenSearchValidator} from './route-registry';
import {browserEntryDestination, profileSettingsAliasPath, profileSettingsDestination} from './route-registry-destination';
import {gallerySearch} from './platform/gallery-search';
import {accountReturnPath} from './route-registry-return';
import {ShellRoot} from './shell/Root';
import {ChildRouteError, RootErrorRethrow} from './shell/DestinationError';
import {NotFoundScreen} from './shell/NotFound';
import {useShellBindings} from './shell/bindings';
import {HomeScreen} from './screens/home/Home';
import {librarySearch, searchQuery} from './shell/navigation';
import {localDeviceHint} from './platform/local-device-entry';
import {createServerScope, scopedLinks} from './platform/server-scope';
import {currentWebEnvironment} from './platform/environment';
import {LinkUnavailableScreen, linkUnavailableSearch} from './shell/LinkUnavailable';

// Local approval links are hints, not grants. Scrub before Router parses/stores
// the initial history entry; account return/Security paths are never captured.
if (typeof location !== 'undefined' && typeof history !== 'undefined') localDeviceHint.capture(location, history);

/** O-161: Hosted paths name their server; the router reads and writes them through this scope. */
export const serverScope = createServerScope(typeof location !== 'undefined' && currentWebEnvironment().hostedWeb, link => scopedLinks.remember(link));

function BoundHome() { const binding = useShellBindings(); return binding ? <HomeScreen {...binding.home} /> : null; }

// A root/shell failure is the app's, not a destination's: rethrow it to the App last resort.
export const rootRoute = createRootRoute({component: ShellRoot, notFoundComponent: NotFoundScreen, errorComponent: RootErrorRethrow});
// Each screen owns its component/registration; the shared router owns only stable URLs.
const screenSearch = (id: WebScreenId, fallback?: WebScreenSearchValidator) => (raw: Record<string, unknown>) => webScreens.validateSearch(id, raw, fallback);
const screen = (id: WebScreenId) => function RegisteredScreen() {return <WebScreenOutlet id={id} />;};
export const homeRoute = createRoute({getParentRoute: () => rootRoute, path: '/', component: () => <WebScreenOutlet id="home" fallback={<BoundHome />} />, validateSearch: screenSearch('home')});
export const searchRoute = createRoute({getParentRoute: () => rootRoute, component: screen('search'), path: '/search', validateSearch: screenSearch('search', searchQuery)});
export const readingSearchRoute = createRoute({getParentRoute: () => rootRoute, component: screen('readingSearch'), path: '/search/reading', validateSearch: screenSearch('readingSearch')});
export const librariesRoute = createRoute({getParentRoute: () => rootRoute, component: screen('libraries'), path: '/libraries', validateSearch: screenSearch('libraries')});
export const libraryRoute = createRoute({getParentRoute: () => rootRoute, component: screen('library'), path: '/library/$libraryId', validateSearch: screenSearch('library', librarySearch)});
export const liveRoute = createRoute({getParentRoute: () => rootRoute, component: screen('live'), path: '/live', validateSearch: screenSearch('live', (raw: Record<string, unknown>): {sourceId?: string; group?: string} => ({...(typeof raw.sourceId === 'string' && raw.sourceId.length <= 256 ? {sourceId: raw.sourceId} : {}), ...(typeof raw.group === 'string' && raw.group.length <= 128 ? {group: raw.group} : {})}))});
export const savedRoute = createRoute({getParentRoute: () => rootRoute, component: screen('saved'), path: '/saved', validateSearch: screenSearch('saved', (raw: Record<string, unknown>): {resourceId?: string} => typeof raw.resourceId === 'string' && raw.resourceId.length <= 256 ? {resourceId: raw.resourceId} : {})});
export const itemRoute = createRoute({getParentRoute: () => rootRoute, component: screen('item'), path: '/item/$itemId', validateSearch: screenSearch('item')});
export const homeRowRoute = createRoute({getParentRoute: () => rootRoute, component: screen('homeRow'), path: '/home/rows/$rowId', validateSearch: screenSearch('homeRow')});
export const playerRoute = createRoute({getParentRoute: () => rootRoute, component: screen('player'), path: '/player', validateSearch: screenSearch('player')});
export const settingsRoute = createRoute({getParentRoute: () => rootRoute, component: screen('settings'), path: '/settings', validateSearch: screenSearch('settings')});
export const dashboardRoute = createRoute({getParentRoute: () => rootRoute, component: screen('dashboard'), path: '/dashboard', validateSearch: screenSearch('dashboard', (raw: Record<string, unknown>): {section?: 'streams' | 'activity' | 'updates'} => ['streams', 'activity', 'updates'].includes(String(raw.section)) ? {section: raw.section as 'streams' | 'activity' | 'updates'} : {})});
export const notificationsRoute = createRoute({getParentRoute: () => rootRoute, component: screen('notifications'), path: '/notifications', validateSearch: screenSearch('notifications')});
export const profilesRoute = createRoute({getParentRoute: () => rootRoute, component: screen('profiles'), path: '/profiles', validateSearch: screenSearch('profiles', (raw: Record<string, unknown>): {profileId?: string; add?: boolean} => ({...(typeof raw.profileId === 'string' && raw.profileId.length <= 128 ? {profileId: raw.profileId} : {}), ...(raw.add === true ? {add: true} : {})}))});
export const serversRoute = createRoute({getParentRoute: () => rootRoute, component: screen('servers'), path: '/servers', validateSearch: screenSearch('servers')});
export const accountRoute = createRoute({getParentRoute: () => rootRoute, component: screen('account'), path: '/account', validateSearch: screenSearch('account')});
export const setupRoute = createRoute({getParentRoute: () => rootRoute, component: screen('setup'), path: '/setup', validateSearch: screenSearch('setup')});
export const signInRoute = createRoute({getParentRoute: () => rootRoute, component: screen('signIn'), path: '/sign-in', validateSearch: screenSearch('signIn')});
export const accountReturnRoute = createRoute({getParentRoute: () => rootRoute, component: screen('signIn'), path: accountReturnPath, validateSearch: screenSearch('signIn')});
export const togetherRoute = createRoute({getParentRoute: () => rootRoute, component: screen('together'), path: '/watch-together', validateSearch: screenSearch('together')});
export const aboutRoute = createRoute({getParentRoute: () => rootRoute, component: screen('about'), path: '/about', validateSearch: screenSearch('about')});
export const helpRoute = createRoute({getParentRoute: () => rootRoute, component: screen('help'), path: '/help', validateSearch: screenSearch('help')});
export const personRoute = createRoute({getParentRoute: () => rootRoute, component: screen('person'), path: '/person/$personId', validateSearch: screenSearch('person')});
export const readRoute = createRoute({getParentRoute: () => rootRoute, component: screen('read'), path: '/read/$itemId', validateSearch: screenSearch('read')});
export const linkUnavailableRoute = createRoute({getParentRoute: () => rootRoute, component: LinkUnavailableScreen, path: '/link-unavailable', validateSearch: linkUnavailableSearch});
export const deviceRoute = createRoute({getParentRoute: () => rootRoute, component: screen('device'), path: '/device', validateSearch: screenSearch('device'), beforeLoad: ({location: entry, preload}) => {
  if (preload || !entry.hash) return;
  const url = new URL(entry.href, window.location.origin);
  if (url.origin !== window.location.origin) throw new Error('Invalid local device entry');
  localDeviceHint.capture(url, history);
  throw redirect({to: '/device', search: {}, hash: '', replace: true});
}});
// §31 uses this existing public web address. It is navigation, never proof.
export const profileSettingsAliasRoute = createRoute({getParentRoute: () => rootRoute, path: profileSettingsAliasPath, beforeLoad: () => {
  browserEntryDestination.rememberProfileSettings();
  throw redirect({...profileSettingsDestination, replace: true});
}});
const destinations = [homeRoute, searchRoute, readingSearchRoute, librariesRoute, libraryRoute, liveRoute, savedRoute, itemRoute, homeRowRoute, playerRoute, settingsRoute, dashboardRoute, notificationsRoute, profilesRoute, serversRoute, accountRoute, setupRoute, signInRoute, accountReturnRoute, togetherRoute, helpRoute, aboutRoute, personRoute, readRoute, deviceRoute, linkUnavailableRoute, profileSettingsAliasRoute];
const devRoutes = import.meta.env.DEV ? [createRoute({
  getParentRoute: () => rootRoute, path: '/dev/kit', validateSearch: gallerySearch,
  component: lazyRouteComponent(() => import('./dev/KitGallery'), 'KitGallery'),
})] : [];
// Each actual child destination owns the approved page failure, installed through its supported
// options update before the tree is built. There is no global page-failure default.
const installChildError = (route: AnyRoute) => route.update({errorComponent: ChildRouteError});
for (const route of [...destinations, ...devRoutes]) installChildError(route);
export const routeTree = rootRoute.addChildren([...destinations, ...devRoutes]);
// The initial route must paint before there is a document snapshot to animate.
// The shell's real-render listener enables native transitions for later routes.
// The global catch boundary would consume a root rethrow before App's last resort; per-child
// errorComponents still catch their own destinations.
export const router = createRouter({routeTree, rewrite: serverScope.rewrite, defaultViewTransition: false, scrollRestorationBehavior: 'instant', disableGlobalCatchBoundary: true});
declare module '@tanstack/react-router' { interface Register { router: typeof router } }
