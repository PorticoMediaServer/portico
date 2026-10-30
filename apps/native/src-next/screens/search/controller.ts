import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {nativeScreenServices} from '../native-screen-services';
import {nativeSearchRememberHistory} from './navigation';
import {NativeSearchDestination} from './model';

/** One actual Search root history object. No renderer, eager request or URL
 * state: the screen owns its draft and canonical arrived query/page. */
export function createNativeSearchController(context: NativeShellBootstrapContext, target: NativeShellRoute) {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  const retained = () => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  if (target.kind !== 'shell' || target.destination.kind !== 'search' || !retained()) throw new Error('native_search_route_required');
  let disposed = false, remember = nativeSearchRememberHistory(context);
  const current = () => !disposed && lifetime.current() && retained();
  const visible = () => current() && context.navigation.getSnapshot().route === target;
  // The route's own query is copied once into the draft; later edits never write back to it.
  const source = new NativeSearchDestination({api: context.api, scope: lifetime.scope, current,
    initialQuery: target.destination.query,
    rememberHistory: () => nativeSearchRememberHistory(context), reportError: error => {if (current()) common.reportError(error, 'search');}});
  let seeded = false;
  const seed = () => {
    if (seeded || !visible()) return;
    const state = source.getSnapshot();
    seeded = true;
    if (!state.text.trim() || state.problem || state.search.phase !== 'idle') return;
    void source.commit().catch(error => {if (current()) common.reportError(error, 'search');});
  };
  const listeners = new Set<() => void>(), cleanups: (() => void)[] = [];
  let previous: ReturnType<typeof context.navigation.getSnapshot> | undefined, previousCurrent: boolean | undefined;
  let snapshot: ReturnType<NativeDestinationActivationPort['getSnapshot']>;
  const activation: NativeDestinationActivationPort = {
    subscribe: listener => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => {
      const state = context.navigation.getSnapshot(), live = current();
      if (state !== previous || previousCurrent !== live) {
        previous = state; previousCurrent = live;
        snapshot = Object.freeze({visible: live && state.route === target, intent: context.navigation.getIntent(), revision: state.revision});
      }
      return snapshot;
    },
  };
  const emit = () => {for (const listener of [...listeners]) listener();};
  const dispose = () => {
    if (disposed) return;
    disposed = true; cleanups.splice(0).forEach(stop => stop()); source.dispose(); emit(); listeners.clear();
  };
  const changed = () => {
    if (!current()) {dispose(); return;}
    if (visible()) source.resume(); else source.pause();
    emit();
  };
  cleanups.push(context.navigation.subscribe(changed), context.subscribeAuthority(changed), context.subscribePreferences(() => {
    if (!current()) {dispose(); return;}
    const next = nativeSearchRememberHistory(context);
    if (next === remember) return;
    remember = next;
    void source.refreshHistory();
  }));
  if (!visible()) source.pause();
  return Object.freeze({source, activation, load: () => visible() ? source.refreshHistory() : Promise.resolve(), seed, dispose});
}
