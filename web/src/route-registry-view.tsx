import {createContext, lazy, Suspense, useCallback, useContext, useLayoutEffect, useState, useSyncExternalStore, type ComponentType, type ReactNode} from 'react';
import {useMatch} from '@tanstack/react-router';
import {validReadingIndexedSearchQuery} from '@core/reading-indexed-search.ts';
import {createWebScreenRegistry, defineWebScreen, invokeWebAction, type ShellPopoverProps, type WebActionId, type WebScreenChrome, type WebScreenDefinition, type WebScreenId, type WebScreenModule, type WebScreenSearch, type WebScreenSearchValidator} from './route-registry';
import type {ShellBindings} from './shell/bindings';
import type {ShellAssemblyContext, ShellBootstrap} from './shell/bootstrap';
import type {ShellPlaybackBinding} from './shell/Playback';
import type {TakeAccountReturn} from './route-registry-return';
import type {TakeAccountMove} from './route-registry-arrival';
import {parseHomeRowScreenSearch, parseLibraryScreenSearch} from './screens/browse/route-search';
import {parseLiveScreenSearch} from './screens/live/route-search';
import {parseSavedScreenRoute} from './screens/saved/route';
import {savedDownloadsBinder, subscribeSavedDownloadsBinder} from './screens/saved/native-binding';
import {readSearch} from './screens/read/route-search';
import {settingsRoute} from './kit/settings/search';

type RouteScreenMeta = Readonly<{
  module: string;
  chrome?: WebScreenChrome;
  validateSearch?: WebScreenSearchValidator;
  playbackHost?: true;
  retainedHost?: 'read' | 'saved';
  shellPopover?: true;
}>;

const readingSearchQuery: WebScreenSearchValidator = (raw: WebScreenSearch) => ({
  q: validReadingIndexedSearchQuery(raw.q) ? raw.q : '',
});
const serversSearchQuery: WebScreenSearchValidator = raw => ({
  ...(raw.local === true || raw.local === 'true' ? {local: true} : {}),
  ...(typeof raw.link === 'string' && /^[A-Za-z0-9._~-]{1,128}$/.test(raw.link) ? {link: raw.link} : {}),
});
const settingsSearchQuery: WebScreenSearchValidator = raw =>
  raw.scope === undefined && raw.section === undefined && raw.row === undefined ? {} : settingsRoute(raw, () => true);

// Lightweight synchronous route metadata. Heavy screen modules stay in lazy
// chunks discovered by import.meta.glob below and load only when visited.
const screenMeta: Readonly<Record<WebScreenId, RouteScreenMeta>> = Object.freeze({
  home: {module: './screens/home/Home.route.tsx'},
  search: {module: './screens/search/Search.route.tsx'},
  readingSearch: {module: './screens/search/ReadingSearch.route.tsx', validateSearch: readingSearchQuery},
  libraries: {module: './screens/browse/Libraries.route.tsx'},
  library: {module: './screens/browse/Library.route.tsx', validateSearch: parseLibraryScreenSearch},
  live: {module: './screens/live/Live.route.tsx', validateSearch: parseLiveScreenSearch},
  saved: {module: './screens/saved/Saved.route.tsx', retainedHost: 'saved', validateSearch: parseSavedScreenRoute},
  item: {module: './screens/detail/Item.route.tsx'},
  homeRow: {module: './screens/browse/HomeRow.route.tsx', validateSearch: parseHomeRowScreenSearch},
  player: {module: './screens/watch/watch.route.tsx', chrome: 'player', playbackHost: true},
  settings: {module: './screens/settings/Settings.route.tsx', validateSearch: settingsSearchQuery},
  dashboard: {module: './screens/dashboard/Dashboard.route.tsx'},
  notifications: {module: './screens/account/Notifications.route.tsx', shellPopover: true},
  setup: {module: './screens/setup/Setup.route.tsx', chrome: 'standalone'},
  profiles: {module: './screens/accounts/Profiles.route.tsx', chrome: 'standalone'},
  servers: {module: './screens/accounts/Servers.route.tsx', chrome: 'standalone', validateSearch: serversSearchQuery},
  account: {module: './screens/account/Support.route.tsx', chrome: 'standalone'},
  signIn: {module: './screens/accounts/SignIn.route.tsx', chrome: 'standalone'},
  together: {module: './screens/together/Together.route.tsx', chrome: 'shell'},
  help: {module: './screens/account/Support.route.tsx', chrome: 'standalone'},
  about: {module: './screens/about/About.route.tsx'},
  person: {module: './screens/detail/Person.route.tsx'},
  read: {module: './screens/read/read.route.tsx', chrome: 'standalone', retainedHost: 'read', validateSearch: readSearch},
  device: {module: './screens/device/Device.route.tsx', chrome: 'standalone'},
});

const routeLoaders = import.meta.glob<WebScreenModule>('./screens/**/*.route.tsx');
const actionModules = import.meta.glob<WebScreenModule>('./screens/**/*.action.tsx', {eager: true});

const loadedModules = new Map<string, Promise<readonly WebScreenDefinition[]>>();
function loadRouteDefinitions(modulePath: string): Promise<readonly WebScreenDefinition[]> {
  let pending = loadedModules.get(modulePath);
  if (!pending) {
    const loader = routeLoaders[modulePath];
    if (!loader) throw new Error(`missing-web-screen-loader:${modulePath}`);
    pending = loader().then(mod => {
      const exported = mod.default;
      const values = Array.isArray(exported) ? exported : [exported];
      return values.filter((value): value is WebScreenDefinition => !('kind' in value && value.kind === 'action')).map(value => defineWebScreen(value));
    });
    loadedModules.set(modulePath, pending);
  }
  return pending;
}

function loadScreenDefinition(id: WebScreenId, modulePath: string): Promise<WebScreenDefinition> {
  return loadRouteDefinitions(modulePath).then(definitions => {
    const found = definitions.find(definition => definition.id === id);
    if (!found) throw new Error(`missing-web-screen-definition:${id}`);
    return found;
  });
}

const routeLoadingFallback = <div data-route-loading="" aria-busy="true" />;

function createLazyRouteComponent(id: WebScreenId, modulePath: string, suspenseFallback: ReactNode): ComponentType {
  const LazyScreen = lazy(() => loadScreenDefinition(id, modulePath).then(definition => ({default: definition.component})));
  return function LazyRouteScreen() {
    return <Suspense fallback={suspenseFallback}><LazyScreen /></Suspense>;
  };
}

function createLazyPlaybackHost(modulePath: string): ComponentType<{binding: ShellPlaybackBinding | undefined}> {
  const LazyPlayback = lazy(() => loadScreenDefinition('player', modulePath).then(definition => {
    if (!definition.playbackHost) throw new Error('missing-player-playback-host');
    return {default: definition.playbackHost};
  }));
  return function LazyPlaybackHost(props: {binding: ShellPlaybackBinding | undefined}) {
    if (!props.binding) return null;
    return <Suspense fallback={null}><LazyPlayback {...props} /></Suspense>;
  };
}

function createLazyShellPopover(modulePath: string): ComponentType<ShellPopoverProps> {
  const LazyPopover = lazy(() => loadScreenDefinition('notifications', modulePath).then(definition => {
    if (!definition.shellPopover) throw new Error('missing-notifications-shell-popover');
    return {default: definition.shellPopover};
  }));
  return function LazyNotificationsPopover(props: ShellPopoverProps) {
    return <Suspense fallback={null}><LazyPopover {...props} /></Suspense>;
  };
}

function createLazyRetainedHost(id: 'read' | 'saved', modulePath: string): ComponentType {
  const LazyHost = lazy(() => loadScreenDefinition(id, modulePath).then(definition => {
    if (!definition.retainedHost) throw new Error(`missing-retained-host:${id}`);
    return {default: definition.retainedHost};
  }));
  if (id === 'read') {
    return function LazyReadRetainedHost() {
      const match = useMatch({from: '/read/$itemId', shouldThrow: false});
      const [activated, setActivated] = useState(() => !!match);
      useLayoutEffect(() => {if (match) setActivated(true);}, [match]);
      return activated ? <Suspense fallback={null}><LazyHost /></Suspense> : null;
    };
  }
  return function LazySavedRetainedHost() {
    const match = useMatch({from: '/saved', shouldThrow: false});
    const hasBinder = useSyncExternalStore(subscribeSavedDownloadsBinder, () => savedDownloadsBinder() !== undefined, () => false);
    const [activated, setActivated] = useState(() => !!match || hasBinder);
    useLayoutEffect(() => {if (match || hasBinder) setActivated(true);}, [match, hasBinder]);
    return activated ? <Suspense fallback={null}><LazyHost /></Suspense> : null;
  };
}

function buildLazyRouteModules(): Record<string, WebScreenModule> {
  const referenced = new Set<string>();
  const modules: Record<string, WebScreenModule> = {};
  for (const [id, meta] of Object.entries(screenMeta) as [WebScreenId, RouteScreenMeta][]) {
    if (!Object.hasOwn(routeLoaders, meta.module)) throw new Error(`missing-web-screen-module:${meta.module}`);
    referenced.add(meta.module);
    const fallback = id === 'player' || id === 'read' ? null : routeLoadingFallback;
    modules[`lazy:${id}`] = {
      default: defineWebScreen({
        id,
        component: createLazyRouteComponent(id, meta.module, fallback),
        ...(meta.chrome ? {chrome: meta.chrome} : {}),
        ...(meta.validateSearch ? {validateSearch: meta.validateSearch} : {}),
        ...(meta.playbackHost ? {playbackHost: createLazyPlaybackHost(meta.module)} : {}),
        ...(meta.retainedHost ? {retainedHost: createLazyRetainedHost(meta.retainedHost, meta.module)} : {}),
        ...(meta.shellPopover ? {shellPopover: createLazyShellPopover(meta.module)} : {}),
      }),
    };
  }
  for (const filename of Object.keys(routeLoaders)) {
    if (!referenced.has(filename)) throw new Error(`unregistered-web-screen-module:${filename}`);
  }
  return modules;
}

export const webScreens = createWebScreenRegistry({
  ...buildLazyRouteModules(),
  ...actionModules,
});
export type WebScreenContext = Readonly<{
  binding?: ShellBindings;
  /** The actual captured server/profile epoch, with its original live PreferencesReader. */
  context?: ShellAssemblyContext;
  bootstrap?: ShellBootstrap;
  /** One captured callback, scrubbed before startup. The protected journal decides KIND. */
  takeAccountReturn?: TakeAccountReturn;
  /** Original scrubbed move link, delivered once; entry proves its expected ID. */
  takeAccountMove?: TakeAccountMove;
}>;
const Context = createContext<WebScreenContext>({});
export function WebScreenProvider({value, children}: {value: WebScreenContext; children: ReactNode}) {
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useWebScreenContext(): WebScreenContext {return useContext(Context);}
export function useProtectedWebScreenContext() {
  const value = useWebScreenContext();
  return value.binding && value.context?.isCurrent() ? {...value, binding: value.binding, context: value.context} : undefined;
}
const noActionSubscribe = () => () => {};
const noActionSnapshot = () => false;
/** The action owner supplies canSubmit; the shared boundary only fences scope. */
export function useWebAction(id: WebActionId): (() => void) | undefined {
  const current = useProtectedWebScreenContext(), context = current?.context, action = webScreens.getAction(id);
  const available = useSyncExternalStore(action?.subscribe ?? noActionSubscribe, action?.getSnapshot ?? noActionSnapshot, noActionSnapshot);
  const invoke = useCallback(() => {invokeWebAction(action, () => context?.isCurrent() === true);}, [action, context]);
  return current && available ? invoke : undefined;
}
/** Route components use the current captured epoch as their lifetime key. Token
 * rotation and the first Home response keep the same component and services. */
export function WebScreenOutlet({id, fallback}: {id: WebScreenId; fallback?: ReactNode}) {
  const {binding} = useWebScreenContext(), Screen = webScreens.get(id)?.component;
  return Screen ? <Screen key={binding?.scopeKey ?? 'signed-out'} /> : fallback;
}

/** Inside the router but outside changing shell/standalone chrome. A real
 * authority replacement unmounts every retained lifetime with its captured scope. */
export function WebRetainedScreenHosts() {
  const current = useProtectedWebScreenContext();
  if (!current) return null;
  return <>
    {webScreens.retainedHosts().map(({id, component: Host}) => <Host key={JSON.stringify([id, current.binding.scopeKey])} />)}
    {webScreens.actions().map(({id, host: Host}) => <Host key={JSON.stringify(['action', id, current.binding.scopeKey])} />)}
  </>;
}
