import {preferenceValue} from '../../../../../packages/client-core/src/preferences';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {NativeGenreBrowseDestination} from './genre-browse';
import type {NativeSearchGenre} from './types';

/** Only the confirmed effective preference or its published registry default. */
export function nativeSearchRememberHistory(context: Pick<NativeShellBootstrapContext, 'current' | 'preferences'>): boolean {
  return context.current() && preferenceValue<boolean>(context.preferences(), 'search.rememberHistory') === true;
}

export function openNativeSearchGenre(context: NativeShellBootstrapContext, genre: NativeSearchGenre): boolean {
  return context.current() && context.navigation.open(Object.freeze({kind: 'search-genre', value: genre.id, label: genre.name}), context.authority());
}

export function openNativeSearchGenres(context: NativeShellBootstrapContext): boolean {
  return context.current() && context.navigation.open(Object.freeze({kind: 'search-genres'}), context.authority());
}

/** Keep this exact source through Title/Back; dropping its actual history
 * entry or retiring the viewer aborts and clears the old private data. */
export function createNativeSearchGenreController(context: NativeShellBootstrapContext, target: Extract<NativeShellRoute, {kind: 'search-genre'}>) {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false;
  const retained = () => context.navigation.getSnapshot().route === target || context.navigation.retainedRoutes().includes(target);
  if (!retained()) throw new Error('native_genre_route_required');
  const source = new NativeGenreBrowseDestination({api: context.api, value: target.value, current: () => !disposed && lifetime.current() && retained()});
  let previous: ReturnType<typeof context.navigation.getSnapshot> | undefined, previousCurrent: boolean | undefined;
  let snapshot: ReturnType<NativeDestinationActivationPort['getSnapshot']>;
  const activation: NativeDestinationActivationPort = {
    subscribe: context.navigation.subscribe,
    getSnapshot() {
      const route = context.navigation.getSnapshot(), current = !disposed && lifetime.current();
      if (previous !== route || previousCurrent !== current) {
        previous = route; previousCurrent = current;
        snapshot = Object.freeze({visible: current && route.route === target, intent: context.navigation.getIntent(), revision: route.revision});
      }
      return snapshot;
    },
  };
  const cleanups: (() => void)[] = [];
  const dispose = () => {if (disposed) return; disposed = true; cleanups.splice(0).forEach(stop => stop()); source.dispose();};
  const check = () => {if (!lifetime.current() || !retained()) dispose();};
  cleanups.push(context.navigation.subscribe(check), context.subscribeAuthority(check));
  return Object.freeze({source, activation, dispose});
}
