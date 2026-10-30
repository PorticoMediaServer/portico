import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useSyncExternalStore} from 'react';
import {AppState, Platform, View} from 'react-native';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellRoute} from '../../shell/routes';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {createNativeEntryActions} from '../destinations/entry-actions';
import {createNativeSearchController} from './controller';
import {createNativeSearchActions} from './actions';
import {createNativeSearchGenreController, openNativeSearchGenre} from './navigation';
import {NativeSearchExplore, NativeSearchGenreList} from './explore';
import {SearchScreen} from './SearchScreen';
import {GenreBrowseScreen} from './GenreBrowseScreen';
import {AllGenresScreen} from './AllGenresScreen';
import {createNativeSearchSelection} from './selection';
import {createNativeReadingSearchModule} from './reading-register';
import {authorityKey} from '../../shell/model';

type Root = Readonly<{route: NativeShellRoute; key: number; focusScope: string; search: ReturnType<typeof createNativeSearchController>;
  explore: NativeSearchExplore; selection: ReturnType<typeof createNativeSearchSelection>; actions: ReturnType<typeof createNativeSearchActions>}>;
type GenreRoute = Extract<NativeShellRoute, {kind: 'search-genre'}>;
type Genre = Readonly<{route: GenreRoute; key: number; focusScope: string; controller: ReturnType<typeof createNativeSearchGenreController>; actions: ReturnType<typeof createNativeEntryActions>}>;
type AllRoute = Extract<NativeShellRoute, {kind: 'search-genres'}>;
type All = Readonly<{route: AllRoute; key: number; focusScope: string; explore: NativeSearchGenreList; current: () => boolean}>;

/** Screen-owned registration; every controller is keyed by its actual retained
 * history descriptor. The shell's presentation descriptor never becomes data.
 * The same module serves the handset and TV entries; the drawn genre prefixes
 * follow the actual surface rather than the entry that imported it. */
export function createNativeSearchModule(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context), roots = new Map<NativeShellRoute, Root>(), genres = new Map<GenreRoute, Genre>(), all = new Map<AllRoute, All>();
  const surface = Platform.isTV ? 'tv' as const : 'phone' as const;
  let disposed = false, sequence = 0, foreground = !AppState.currentState || AppState.currentState === 'active';
  const current = () => !disposed && lifetime.current();
  const retained = (route: NativeShellRoute) => current() && (context.navigation.getSnapshot().route === route || context.navigation.retainedRoutes().includes(route));
  const visible = (route: NativeShellRoute) => retained(route) && context.navigation.getSnapshot().route === route;
  const closeRoot = (entry: Root) => {entry.selection.dispose(); entry.actions.dispose(); entry.search.dispose(); entry.explore.dispose();};
  const closeGenre = (entry: Genre) => {entry.actions.dispose(); entry.controller.dispose();};
  const reconcile = () => {
    const routes = current() ? [...context.navigation.retainedRoutes(), context.navigation.getSnapshot().route] : [];
    const keep = new Set(routes);
    for (const [route, entry] of roots) if (!keep.has(route)) {roots.delete(route); closeRoot(entry);}
    for (const [route, entry] of genres) if (!keep.has(route)) {genres.delete(route); closeGenre(entry);}
    for (const [route, entry] of all) if (!keep.has(route)) {all.delete(route); entry.explore.dispose();}
    for (const route of routes) {
      if (route.kind !== 'shell' || route.destination.kind !== 'search' || roots.has(route)) continue;
      const search = createNativeSearchController(context, route);
      const explore = new NativeSearchExplore({api: context.api, surface, current: () => retained(route), openGenre: genre => {
        if (visible(route)) openNativeSearchGenre(context, genre);
      }});
      const selection = createNativeSearchSelection(context, search.source, search.activation);
      const key = ++sequence;
      roots.set(route, {route, key, focusScope: `search:${key}`, search, explore, selection, actions: createNativeSearchActions(context, search.source, search.activation, selection)});
    }
    for (const route of routes) {
      if (route.kind === 'search-genre' && !genres.has(route)) {
        const controller = createNativeSearchGenreController(context, route);
        const actions = createNativeEntryActions(context, {current: () => visible(route), contains: controller.source.contains,
          subscribe: listener => {
            let collection = controller.source.getSnapshot().collection, offCollection = collection?.subscribe(listener);
            const off = controller.source.subscribe(() => {
              const next = controller.source.getSnapshot().collection;
              if (next !== collection) {offCollection?.(); collection = next; offCollection = next?.subscribe(listener);}
              listener();
            });
            return () => {off(); offCollection?.();};
          }});
        const key = ++sequence;
        genres.set(route, {route, key, focusScope: `genre:${authorityKey(context.authority())}:${JSON.stringify(route.value)}:${key}`, controller, actions});
      }
      if (route.kind === 'search-genres' && !all.has(route)) {
        const explore = new NativeSearchGenreList({api: context.api, current: () => retained(route),
          openGenre: genre => {if (visible(route)) openNativeSearchGenre(context, genre);}});
        const key = ++sequence;
        all.set(route, {route, key, focusScope: `search-all:${authorityKey(context.authority())}:${key}`, explore, current: () => visible(route)});
      }
    }
    roots.forEach(entry => entry.explore.setActive(foreground && visible(entry.route)));
    all.forEach(entry => entry.explore.setActive(foreground && visible(entry.route), !foreground));
  };
  const appState = AppState.addEventListener('change', state => {foreground = state === 'active'; reconcile();});
  const stops = [context.navigation.subscribe(reconcile), context.subscribeAuthority(reconcile), () => appState.remove()];
  const back = (route: NativeShellRoute) => {if (visible(route)) context.navigation.back(context.authority());};
  return {
    render: () => {reconcile(); return null;},
    renderRetained: runtime => {
      reconcile();
      return <>
        {[...roots.values()].map(entry => <SearchHost key={entry.key} entry={entry} runtime={runtime}/>)}
        {[...genres.values()].map(entry => <GenreBrowseScreen key={entry.key} focusScope={entry.focusScope} name={entry.route.label} value={entry.route.value} destination={entry.controller.source}
          activation={entry.controller.activation} runtime={runtime} actions={entry.actions} owner={context.session()?.viewer.role === 'owner'} onBack={() => back(entry.route)} stackRoute={entry.route}/>)}
        {[...all.values()].map(entry => <AllHost key={entry.key} entry={entry} runtime={runtime} navigation={context.navigation} back={() => back(entry.route)}/>)}
      </>;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true; stops.forEach(stop => stop()); roots.forEach(closeRoot); genres.forEach(closeGenre);
      all.forEach(entry => {entry.explore.dispose();}); roots.clear(); genres.clear(); all.clear();
    },
  };
}
function SearchHost({entry, runtime}: {entry: Root; runtime: NativeShellRuntime}) {
  const active = useSyncExternalStore(entry.search.activation.subscribe, entry.search.activation.getSnapshot);
  const place = useNativeStackPlacement(entry.route), stacked = useNativeStackHosted();
  useSyncExternalStore(entry.selection.subscribe, entry.selection.getSnapshot);
  // The captured-route query starts the ordinary search only after the screen is actually
  // visible; the controller seeds the draft, never a render-created request.
  useEffect(() => {if (active.visible) entry.search.seed();}, [active.visible, entry]);
  return place(<View pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible} importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, display: active.visible || stacked ? 'flex' : 'none'}}>
    <SearchScreen focusScope={entry.focusScope} destination={entry.search.source} activation={entry.search.activation} explore={entry.explore} actions={entry.actions} selection={entry.selection} runtime={runtime}
      tvCopy={{preferToTalk: 'next.search.preferToTalk', voiceHint: 'next.search.remoteMicrophoneHelp'}}/>
  </View>);
}
function AllHost({entry, runtime, navigation, back}: {entry: All; runtime: NativeShellRuntime; navigation: NativeShellBootstrapContext['navigation']; back: () => void}) {
  useSyncExternalStore(navigation.subscribe, navigation.getSnapshot);
  const live = entry.current(), placeAll = useNativeStackPlacement(entry.route), stackedAll = useNativeStackHosted();
  return placeAll(<View pointerEvents={live ? 'auto' : 'none'} accessibilityElementsHidden={!live} importantForAccessibility={live ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, display: live || stackedAll ? 'flex' : 'none'}}>
    <AllGenresScreen focusScope={entry.focusScope} explore={entry.explore} runtime={runtime} visible={live} current={entry.current} onBack={back}/>
  </View>);
}

nativeShellRoutes.registerDestination('search', createNativeSearchModule);
nativeShellRoutes.registerRoute('search-genre', createNativeSearchModule);
nativeShellRoutes.registerRoute('search-genres', createNativeSearchModule);
nativeShellRoutes.registerRoute('search-reading', createNativeReadingSearchModule);
