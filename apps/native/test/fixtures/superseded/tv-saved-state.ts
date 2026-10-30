// Test-only historical model; no production consumer mounts this module.
import type {NativeShellBootstrapContext} from '../../../src-next/shell/bootstrap';
import type {NativeShellRoute} from '../../../src-next/shell/routes';
import type {NativeDestinationActivation, NativeDestinationActivationPort} from '../../../src-next/screens/destinations/focus';
import {nativeDestinationLifetime} from '../../../src-next/screens/destinations/lifetime';
import {createNativeEntryActions} from '../../../src-next/screens/destinations/entry-actions';
import {nativeScreenServices} from '../../../src-next/screens/native-screen-services';
import {createNativeSavedState} from '../../../src-next/screens/saved/state';
import {NativeSavedViewDestination} from '../../../src-next/screens/saved/view-model';
import type {NativeSavedTVTab} from '../../../src-next/screens/saved/SavedTVTabs';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {PlaylistCard} from '../../../../../packages/client-core/src/saved';
import {openNativeSavedView} from '../../../src-next/screens/saved/navigation';

type RootRoute = Extract<NativeShellRoute, {kind: 'shell'}>;

/** TV's existing inline tabs belong to the actual Saved root history object.
 * Resource routes and the one device lease remain with the shared Saved state.
 * This installs no renderer, registration or default navigation side effect. */
export function createNativeTVSavedState(context: NativeShellBootstrapContext) {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context), shared = createNativeSavedState(context);
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const retained = (target: NativeShellRoute) => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  const roots = new Map<RootRoute, ReturnType<typeof createRoot>>(), cleanups: (() => void)[] = [];
  function createRoot(target: RootRoute) {
    let retired = false;
    const live = () => !retired && current() && retained(target);
    const source = new NativeSavedViewDestination({api: context.api, scope: lifetime.scope, current: live, requestId: common.requestId});
    const listeners = new Set<() => void>();
    let previous: NativeDestinationActivation | undefined, snapshot: NativeDestinationActivation | undefined;
    const activation: NativeDestinationActivationPort = {
      subscribe: listener => {if (retired) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
      getSnapshot: () => {
        const page = shared.activation.getSnapshot(), visible = live() && page.visible && context.navigation.getSnapshot().route === target;
        if (!snapshot || previous !== page || snapshot.visible !== visible) {previous = page; snapshot = Object.freeze({...page, visible});}
        return snapshot;
      },
    };
    const emit = () => {for (const listener of [...listeners]) listener();};
    const stopActivation = shared.activation.subscribe(emit);
    const visible = () => activation.getSnapshot().visible;
    const actions = createNativeEntryActions(context, {current: visible, contains: source.contains, subscribe: source.subscribe});
    return Object.freeze({source, activation, actions,
      load: () => visible() && !source.getSnapshot().route ? source.select({view: 'watchlist'}) : Promise.resolve(),
      selectTab: (view: NativeSavedTVTab) => visible() ? source.select({view}) : Promise.resolve(),
      openPlaylist: (entry: PlaylistCard) => visible() && entry.kind === 'playlist' && source.getSnapshot().saved?.projection?.sections.some(section => section.entries.includes(entry)) === true
        && openNativeSavedView(context, {view: 'playlist', playlistId: entry.id}),
      openResource: (resource: PersonalResource) => visible() && source.getSnapshot().personal?.resources.includes(resource) === true
        && openNativeSavedView(context, {view: 'resource', resourceId: resource.id}),
      dispose: () => {if (retired) return; retired = true; stopActivation(); actions.dispose(); source.dispose(); emit(); listeners.clear();},
    });
  }
  const dispose = () => {
    if (disposed) return;
    disposed = true; cleanups.splice(0).forEach(stop => stop());
    for (const root of roots.values()) root.dispose();
    roots.clear(); shared.dispose();
  };
  const check = () => {
    if (!current()) {dispose(); return;}
    for (const [target, root] of roots) if (!retained(target)) {roots.delete(target); root.dispose();}
  };
  cleanups.push(context.subscribeAuthority(check), context.navigation.subscribe(check));
  return Object.freeze({
    offline: shared.offline,
    view: shared.view,
    root: (target: NativeShellRoute) => {
      check();
      if (!current() || target.kind !== 'shell' || target.destination.kind !== 'saved' || !retained(target)) return;
      let root = roots.get(target);
      if (!root) {root = createRoot(target); roots.set(target, root);}
      return root;
    },
    dispose,
  });
}
