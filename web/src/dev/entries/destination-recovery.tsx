import {StrictMode, useLayoutEffect, useSyncExternalStore} from 'react';
import {flushSync} from 'react-dom';
import {createRoot} from 'react-dom/client';
import {RouterProvider, createMemoryHistory} from '@tanstack/react-router';
import type {HttpLocalApi, Library, Viewer} from '@core/index.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import {router, rootRoute, searchRoute} from '../../router';
import {WebScreenProvider} from '../../route-registry-view';
import {ShellBindingsProvider} from '../../shell/bindings';
import {IconSprite} from '../../kit/Icon';
import {LastResortBoundary} from '../../shell/Errors';
import {DestinationErrorBoundary, destinationErrorActions, type DestinationErrorActions, type DestinationRecoveryWitness} from '../../shell/DestinationError';

/**
 * FX-156 specimen: the real router, real ShellRoot and real Shell chrome with finite fixture data.
 * The destination fault is installed through the actual route's public options update and the exact
 * original options are restored on teardown. No production session, network or credentials exist here.
 */
type Fault = 'none' | 'render' | 'effect';
let fault: Fault = 'none';
const faultListeners = new Set<() => void>();
const setFault = (value: Fault) => { fault = value; for (const listener of faultListeners) listener(); };
const subscribeFault = (listener: () => void) => { faultListeners.add(listener); return () => {faultListeners.delete(listener);}; };
const faultSnapshot = () => fault;
const original = Object.freeze({search: searchRoute.options.component, root: rootRoute.options.component, validateSearch: searchRoute.options.validateSearch, history: router.history});
/** The deterministic fixture destination is mounted from the start; only its fault state changes. */
function FixtureDestination() {
  const value = useSyncExternalStore(subscribeFault, faultSnapshot, faultSnapshot);
  useLayoutEffect(() => { if (value === 'effect') throw new Error('destination-effect-fault'); }, [value]);
  if (value === 'render') throw new Error('destination-render-fault');
  return <div data-destination-ok>{value}</div>;
}

/* Component-level witness case: the real outlet boundary with typed witnesses, separate from the
   production-shell proof above. It proves only the boundary's own identity rule. */
let witnessFault = true;
function WitnessThrower() { if (witnessFault) throw new Error('witness-fault'); return <div data-witness-ok>ok</div>; }
function mountWitness(element: HTMLElement, witness: DestinationRecoveryWitness, onHome: () => void) {
  const root = createRoot(element);
  const render = (value: DestinationRecoveryWitness) => root.render(<StrictMode><DestinationErrorBoundary witness={value} onHome={onHome}><WitnessThrower/></DestinationErrorBoundary></StrictMode>);
  render(witness);
  let disposed = false;
  return {render, dispose: () => { if (!disposed) { disposed = true; root.unmount(); } }};
}

declare global {
  interface Window { destinationRecovery?: {
    fault: (value: Fault) => void; scope: (value: string) => void; entry: () => void; navigate: (value: string) => Promise<void>;
    validate: (value: boolean) => void; rootFault: (value: boolean) => void; shellMarker: () => boolean; restore: () => void;
    witness: () => void;
    moveHistory: (href: string) => void;
    scopeNow: (value: string) => void; revalidate: () => Promise<void>; href: () => string; errorMessage: () => string;
  };
  destinationRecoveryWitness?: {
    same: () => void; freshEntry: () => void; scope: () => void; missingKey: () => void; sameHrefNewObject: () => void;
    keyless: () => void; keylessSameHref: () => void;
    release: () => void; rearm: () => void; homeCalls: () => number;
    actions: () => readonly DestinationErrorActions[]; unmount: () => void; remount: () => void; dispose: () => void;
  };
  destinationRecoveryActions?: {
    list: (source: DestinationErrorActions['source']) => readonly DestinationErrorActions[];
    latest: (source: DestinationErrorActions['source']) => DestinationErrorActions | undefined;
  };
  disposeDestinationRecovery?: () => void; }
}

const root = typeof document === 'undefined' ? null : document.getElementById('root');
if (root) mount(root);

function mount(element: HTMLElement) {
  const viewer: Viewer = {serverId: 'fixture-server', authority: 'local', accountId: 'fixture-account', profileId: 'fixture-profile', role: 'owner'};
  const libraries: readonly Library[] = [{id: 'movies', name: 'Movies', kind: 'movie'}];
  const identity = {viewer, username: 'fixture', profileName: 'Fixture', serverName: 'Fixture server', primaryProfile: true,
    profiles: [], canManageProfiles: false, accountFacts: [], libraries, liveAvailable: false, unread: 0, showNotificationDot: false};
  const api = {
    getRouteSnapshot: () => ({phase: 'ready'}), subscribeRoute: () => () => {},
  } as unknown as HttpLocalApi;
  const events = {on: () => () => {}, onResync: () => () => {}, onAny: () => () => {}, getStatus: () => ({connected: true}), subscribeStatus: () => () => {}} as unknown as EventsClient;
  const bindingFor = (scopeKey: string) => ({api, events, scopeKey,
    shell: {identity, OwnerActivity: () => null, dockVisible: false, onSignOut: () => {}}, home: {} as never});
  let scopeKey = 'fixture-scope';
  let validationSeq = 0;
  const render = (value = scopeKey) => {
    scopeKey = value;
    const binding = bindingFor(value);
    reactRoot.render(<StrictMode><LastResortBoundary>
      <WebScreenProvider value={{binding} as never}>
        <IconSprite />
        <ShellBindingsProvider value={binding as never}><RouterProvider router={router} /></ShellBindingsProvider>
      </WebScreenProvider>
    </LastResortBoundary></StrictMode>);
  };
  // The specimen-only capture port records the exact committed action closures for the child page
  // and the outlet backstop. It only records; production never installs it.
  const actions: DestinationErrorActions[] = [];
  destinationErrorActions.capture = value => { actions.push(value); };
  window.destinationRecoveryActions = {
    list: source => actions.filter(action => action.source === source),
    latest: source => actions.filter(action => action.source === source).at(-1),
  };
  const reactRoot = createRoot(element);
  const originalHistory = original.history;
  searchRoute.update({component: FixtureDestination});
  router.update({history: createMemoryHistory({initialEntries: ['/search']})});
  render();
  window.destinationRecovery = {
    fault: value => setFault(value),
    scope: value => render(value),
    entry: () => { router.history.push('/search'); },
    navigate: async value => {
      const [path, query = ''] = value.split('?');
      const search = Object.fromEntries(new URLSearchParams(query));
      await router.navigate({to: path, search} as never);
    },
    // Test-only instrumentation: validateSearch is not part of the supported updatable options.
    validate: value => { searchRoute.update({validateSearch: (value ? () => { throw new Error(`validation-fault-${++validationSeq}`); } : original.validateSearch)} as never); },
    rootFault: value => { rootRoute.update({component: (value ? () => { throw new Error('shell-fault'); } : original.root)} as never); },
    shellMarker: () => Boolean(document.querySelector('header')),
    restore: () => { setFault('none'); searchRoute.update({component: original.search}); searchRoute.update({validateSearch: original.validateSearch} as never); rootRoute.update({component: original.root}); router.update({history: originalHistory}); },
    moveHistory: href => { router.history.push(href); },
    scopeNow: value => { flushSync(() => render(value)); },
    revalidate: async () => { await router.invalidate().catch(() => { /* The successor error is the specimen. */ }); },
    href: () => router.state.location.href,
    errorMessage: () => { const match = router.state.matches.filter(candidate => candidate.status === 'error').at(-1); return match?.error instanceof Error ? match.error.message : ''; },
    witness: () => {
      actions.length = 0;
      witnessFault = true;
      const host = document.createElement('div'); document.body.append(host);
      const base = {location: {}, href: '/witness', entry: 'entry-1', scopeKey: 'scope-1'};
      let homeCalls = 0;
      let mount = mountWitness(host, base, () => { homeCalls += 1; });
      window.destinationRecoveryWitness = {
        same: () => mount.render(base),
        freshEntry: () => mount.render({location: {}, href: '/witness', entry: 'entry-2', scopeKey: 'scope-1'}),
        scope: () => mount.render({location: {}, href: '/witness', entry: 'entry-2', scopeKey: 'scope-2'}),
        missingKey: () => mount.render({location: {}, href: '/witness?x=1', entry: null, scopeKey: 'scope-2'}),
        sameHrefNewObject: () => mount.render({location: {}, href: '/witness', entry: 'entry-1', scopeKey: 'scope-1'}),
        // Keyless destinations keep a truthful null entry in the identity; a same-href new location
        // object is still a new recovery lifetime.
        keyless: () => { witnessFault = true; mount.render({location: {}, href: '/witness-keyless', entry: null, scopeKey: 'scope-1'}); },
        keylessSameHref: () => { witnessFault = false; mount.render({location: {}, href: '/witness-keyless', entry: null, scopeKey: 'scope-1'}); },
        release: () => { witnessFault = false; },
        rearm: () => { witnessFault = true; mount.render({location: {}, href: '/witness', entry: 'entry-1', scopeKey: 'scope-1'}); },
        homeCalls: () => homeCalls,
        actions: () => actions.filter(action => action.source === 'backstop'),
        // A real unmount retires the mounted opening; a remount is a new instance with its own
        // admission, so the old saved closures stay dead while the new page acts.
        unmount: () => { mount.dispose(); },
        remount: () => { mount = mountWitness(host, base, () => { homeCalls += 1; }); },
        dispose: () => {mount.dispose(); host.remove(); delete destinationErrorActions.capture; delete window.destinationRecoveryWitness;},
      };
    },
  };
  (window as Window & {disposeDestinationRecovery?: () => void}).disposeDestinationRecovery = () => {
    window.destinationRecovery?.restore();
    reactRoot.unmount();
  };
}
