import React, {useLayoutEffect, useMemo, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import type {Library} from '../../../../packages/client-core/src';
import {parseLibraryList} from '../../../../packages/client-core/src/library-list';
import {routePath} from '../../../../packages/contracts/src/routes';
import {LibrarySwitcher} from '../kit/browse';
import {useOptionalFocusRegistry, type FocusRegistry, type FocusSnapshot} from '../kit/focus';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {NativeShellRuntime} from './runtime';
import type {NativeRouteSnapshot} from './routes';

type Choice = Readonly<{opening: NativeRouteSnapshot; libraries: readonly Library[]}>;
type OriginFocus = Readonly<{opener: FocusSnapshot; release: () => void}>;
const noSubscription = () => () => {};
const zeroRevision = () => 0;

/** Root37: choosing the surrounding library never changes profile mix membership.
 * The chooser stays over its original route; cancellation adds no Back entry. */
export function createNativeMusicDiscoverNavigation(context: NativeShellBootstrapContext, runtime: () => NativeShellRuntime | undefined) {
  const authority = context.authority(), listeners = new Set<() => void>();
  let alive = true, operation: AbortController | undefined, openingOperation: NativeRouteSnapshot | undefined, choice: Choice | undefined;
  let focusRegistry: FocusRegistry | null = null, openingFocus: OriginFocus | undefined;
  let returning: Readonly<{route: NativeRouteSnapshot['route']; focus: OriginFocus}> | undefined;
  const current = (opening: NativeRouteSnapshot) => alive && context.current() && context.navigation.isCurrent()
    && context.navigation.getSnapshot() === opening;
  const emit = () => {for (const listener of [...listeners]) listener();};
  const cancel = () => {operation?.abort(); operation = undefined; openingOperation = undefined; openingFocus?.release(); openingFocus = undefined; if (choice) {choice = undefined; emit();}};
  const read = async (signal: AbortSignal) => context.readLibraries
    ? context.readLibraries(signal)
    : parseLibraryList(await context.api.request(routePath('get_libraries'), 'GET', undefined, signal)).items;
  const remember = (id: string) => {void runtime()?.memory.rememberMusic(authority, id).catch(() => {});};
  const openSurface = (route: NativeRouteSnapshot['route'], opening: NativeRouteSnapshot) => {
    if (!current(opening)) return false;
    const origin = openingFocus; openingFocus = undefined;
    cancel();
    returning?.focus.release(); returning = origin ? {route: opening.route, focus: origin} : undefined;
    if (context.navigation.open(route, authority)) return true;
    returning?.focus.release(); returning = undefined; return false;
  };
  const enter = (id: string, opening: NativeRouteSnapshot) => {
    if (openSurface({kind: 'shell', destination: {kind: 'library', libraryId: id, tab: 'discover', anchor: 'mixes'}}, opening)) remember(id);
  };
  const choose = (id: string) => {
    const captured = choice;
    if (!captured || !current(captured.opening) || !captured.libraries.some(value => value.id === id)) return;
    choice = undefined; emit();
    operation?.abort(); const controller = new AbortController(); operation = controller;
    void read(controller.signal).then(libraries => {
      if (controller.signal.aborted || operation !== controller || !current(captured.opening)) return;
      const music = libraries.filter(value => value.kind === 'music');
      if (music.some(value => value.id === id)) enter(id, captured.opening);
      else {choice = music.length ? {opening: captured.opening, libraries: music} : undefined; emit();
        if (!music.length) openSurface({kind: 'shell', destination: {kind: 'libraries'}}, captured.opening);}
    }).catch(error => {if (!controller.signal.aborted && current(captured.opening)) runtime()?.reportError(error);});
  };
  const off = context.navigation.subscribe(() => {
    if (openingOperation && !current(openingOperation)) cancel();
    const state = context.navigation.getSnapshot(), destination = state.destination;
    if (state.route.kind === 'shell' && destination.kind === 'library'
      && context.libraries().some(value => value.id === destination.libraryId && value.kind === 'music')) remember(destination.libraryId);
  });
  const store = {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => choice};
  function Chooser() {
    const value = useSyncExternalStore(store.subscribe, store.getSnapshot);
    const registry = useOptionalFocusRegistry();
    const navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
    const focusRevision = useSyncExternalStore(registry?.subscribe ?? noSubscription, registry?.getSnapshot ?? zeroRevision);
    useLayoutEffect(() => {focusRegistry = registry; return () => {if (focusRegistry === registry) focusRegistry = null;};}, [registry]);
    useLayoutEffect(() => {
      const ticket = returning;
      if (!ticket || !alive || !context.current()) return;
      if (navigation.route === ticket.route) {
        if (registry?.restore(ticket.focus.opener)) {ticket.focus.release(); returning = undefined;}
      } else if (!context.navigation.retainedRoutes().includes(ticket.route)) {
        ticket.focus.release(); returning = undefined;
      }
    }, [registry, navigation, focusRevision]);
    const returnFocus = useMemo(() => {
      const opener = registry?.capture();
      return {current: {focus: () => {if (value && current(value.opening)) registry?.restore(opener);}}};
    }, [value, registry]);
    return value && current(value.opening) ? <View pointerEvents="box-none" style={{position: 'absolute', top: 56, left: 96}}>
      <LibrarySwitcher testID="music-discover-library-chooser" value="" libraries={value.libraries} open returnFocusRef={returnFocus}
        onOpenChange={open => {if (!open && choice) cancel();}} onValueChange={choose} trigger="chevron"/>
    </View> : null;
  }
  return {
    open: () => {
      cancel(); const opening = context.navigation.getSnapshot();
      if (!current(opening)) return;
      openingOperation = opening;
      const opener = focusRegistry?.capture();
      if (opener && focusRegistry) openingFocus = {opener, release: focusRegistry.retain(opener)};
      const controller = new AbortController(); operation = controller;
      void Promise.all([read(controller.signal), runtime()?.memory.readMusic(authority).catch(() => undefined),
        runtime()?.memory.read(authority).catch(() => undefined)]).then(([libraries, musicPrior, lastLibrary]) => {
        if (controller.signal.aborted || operation !== controller || !current(opening)) return;
        const music = libraries.filter(value => value.kind === 'music'), destination = opening.destination;
        const prior = musicPrior && music.some(value => value.id === musicPrior) ? musicPrior : lastLibrary;
        const selected = destination.kind === 'library' && music.some(value => value.id === destination.libraryId) ? destination.libraryId
          : prior && music.some(value => value.id === prior) ? prior : music.length === 1 ? music[0]!.id : undefined;
        if (selected) enter(selected, opening);
        else if (music.length) {choice = {opening, libraries: music}; emit();}
        else openSurface({kind: 'shell', destination: {kind: 'libraries'}}, opening);
      }).catch(error => {if (!controller.signal.aborted && current(opening)) runtime()?.reportError(error);});
    },
    render: () => <Chooser/>,
    dispose: () => {if (!alive) return; alive = false; cancel(); returning?.focus.release(); returning = undefined; off(); listeners.clear();},
  };
}
