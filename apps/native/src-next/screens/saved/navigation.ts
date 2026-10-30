import type {PinnedResource} from '../../../../../packages/client-core/src/library-pins';
import type {SavedRoute} from '../../../../../packages/client-core/src/saved';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeDestinationActivation, type NativeShellRoute} from '../../shell/routes';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {NativeSavedDestination} from './model';
import {NativeSavedViewDestination} from './view-model';

export type NativeSavedViewRoute = Extract<NativeShellRoute, {kind: 'saved-view'}>;
/** The two shell descriptors that point at a Saved page: a renamed Saved view
 * (the hub's directory rows) and a Home destination (playlist or collection). */
export type NativeSavedRouteTarget = NativeSavedViewRoute | Extract<NativeShellRoute, {kind: 'saved-destination'}>;
export function nativeSavedRouteTarget(target: NativeSavedRouteTarget): SavedRoute {
  return target.kind === 'saved-view' ? target.view
    : target.destination.kind === 'playlist' ? {view: 'playlist', playlistId: target.destination.id}
      : {view: 'resource', resourceId: target.destination.id};
}

/** Capture the actual Saved query/resource identity. Its canonical service
 * validates the query before dispatch; this does not expand a resource locally. */
export function openNativeSavedView(context: NativeShellBootstrapContext, view: SavedRoute): boolean {
  return context.current() && context.navigation.open(Object.freeze({kind: 'saved-view', view: Object.freeze({...view})}), context.authority());
}

/** Headless inputs for Saved's existing hub. No render registration or eager
 * I/O; the caller loads it and borrows the genuine device lease separately. */
export function createNativeSavedHubController(context: NativeShellBootstrapContext, requestId: () => Promise<string>) {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const source = new NativeSavedDestination({api: context.api, scope: lifetime.scope, current, requestId});
  const pageActivation = nativeDestinationActivation(context.navigation, {kind: 'saved'});
  const listeners = new Set<() => void>();
  let previous: ReturnType<NativeDestinationActivationPort['getSnapshot']> | undefined;
  let snapshot: ReturnType<NativeDestinationActivationPort['getSnapshot']>;
  const activation: NativeDestinationActivationPort = {
    subscribe: listener => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => {
      const page = pageActivation.getSnapshot(), visible = current() && page.visible;
      if (page !== previous || !snapshot || snapshot.visible !== visible) {
        previous = page; snapshot = Object.freeze({...page, visible});
      }
      return snapshot;
    },
  };
  const emit = () => {for (const listener of [...listeners]) listener();};
  const cleanups: (() => void)[] = [];
  const dispose = () => {if (disposed) return; disposed = true; cleanups.splice(0).forEach(stop => stop()); source.dispose(); emit(); listeners.clear();};
  const visible = () => activation.getSnapshot().visible;
  cleanups.push(context.subscribeAuthority(() => {if (!current()) dispose(); else emit();}), context.navigation.subscribe(emit));
  return Object.freeze({source, activation,
    openView: (view: SavedRoute) => visible() && openNativeSavedView(context, view),
    openPin: (pin: PinnedResource) => {
      if (!visible() || !source.getSnapshot().pins.includes(pin)) return false;
      return openNativeSavedView(context, pin.kind === 'playlist' ? {view: 'playlist', playlistId: pin.id} : {view: 'resource', resourceId: pin.id});
    },
    openDownloads: () => visible() && context.navigation.open({kind: 'downloads'}, context.authority()),
    editPins: () => visible() && context.navigation.open({kind: 'edit-pins'}, context.authority()),
    dispose,
  });
}

/** A Saved source belongs to one exact history object. Title/Back retains it;
 * removing that object or changing principal retires it permanently. */
export function createNativeSavedViewController(context: NativeShellBootstrapContext, target: NativeSavedRouteTarget, requestId: () => Promise<string>) {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false;
  const retained = () => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  if (!retained()) throw new Error('native_saved_route_required');
  const current = () => !disposed && lifetime.current() && retained();
  const source = new NativeSavedViewDestination({api: context.api, scope: lifetime.scope, current, requestId});
  let previous: ReturnType<typeof context.navigation.getSnapshot> | undefined, previousCurrent: boolean | undefined;
  let snapshot: ReturnType<NativeDestinationActivationPort['getSnapshot']>;
  const listeners = new Set<() => void>(), cleanups: (() => void)[] = [];
  const activation: NativeDestinationActivationPort = {
    subscribe: listener => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => {
      const route = context.navigation.getSnapshot(), live = current();
      if (previous !== route || previousCurrent !== live) {
        previous = route; previousCurrent = live;
        snapshot = Object.freeze({visible: live && route.route === target, intent: context.navigation.getIntent(), revision: route.revision});
      }
      return snapshot;
    },
  };
  const emit = () => {for (const listener of [...listeners]) listener();};
  const dispose = () => {
    if (disposed) return;
    disposed = true; cleanups.splice(0).forEach(stop => stop()); source.dispose(); emit(); listeners.clear();
  };
  const check = () => {if (!current()) dispose(); else emit();};
  cleanups.push(context.navigation.subscribe(check), context.subscribeAuthority(check));
  return Object.freeze({source, activation, load: () => current() ? source.select(nativeSavedRouteTarget(target)) : Promise.resolve(),
    refresh: () => current() ? source.refresh() : Promise.resolve(), dispose});
}
