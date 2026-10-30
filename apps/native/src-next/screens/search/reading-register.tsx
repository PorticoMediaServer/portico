import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {Platform, View} from 'react-native';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeRouteRenderer} from '../../shell/route-registry';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {createNativeReadingSearch, type NativeReadingSearchPort, type ReadingSearchOpening} from './reading-search';
import {ReadingSearchScreen} from './ReadingSearchScreen';

type SearchRoute = Extract<NativeShellRoute, {kind: 'search-reading'}>;
type Entry = Readonly<{route: SearchRoute; key: number; port: NativeReadingSearchPort}>;

/** Root54 native surface. One controller per retained route, fenced by its own
 * authority lifetime and the route still being current or retained; a popped route
 * disposes it in the same pass. `render` owns nothing so the page can stay mounted
 * under a reader it opened, exactly as the read route stays under Settings. */
export function createNativeReadingSearchModule(context: NativeShellBootstrapContext): NativeRouteRenderer {
  if (Platform.isTV) throw new Error('native_reading_search_unavailable');
  const lifetime = nativeDestinationLifetime(context), entries = new Map<SearchRoute, Entry>();
  let disposed = false, sequence = 0;
  const current = () => !disposed && lifetime.current();
  const reconcile = () => {
    const routes = current() ? [...context.navigation.retainedRoutes(), context.navigation.getSnapshot().route] : [];
    const keep = new Set(routes);
    for (const [route, entry] of entries) if (!keep.has(route)) {entries.delete(route); entry.port.dispose();}
    for (const route of routes) {
      if (route.kind !== 'search-reading' || entries.has(route)) continue;
      entries.set(route, {route, key: ++sequence, port: createNativeReadingSearch(context, route)});
    }
  };
  const stops = [context.navigation.subscribe(reconcile), context.subscribeAuthority(reconcile)];
  const back = (route: SearchRoute) => {
    if (current() && context.navigation.getSnapshot().route === route) context.navigation.back(context.authority());
  };
  return {
    render: () => {reconcile(); return null;},
    renderRetained: runtime => {
      reconcile();
      return <>{[...entries.values()].map(entry => <ReadingSearchHost key={entry.key} entry={entry} runtime={runtime}
        navigation={context.navigation} onBack={() => back(entry.route)}/>)}</>;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true; stops.forEach(stop => stop());
      for (const entry of entries.values()) entry.port.dispose();
      entries.clear();
    },
  };
}

function ReadingSearchHost({entry, runtime, navigation, onBack}: Readonly<{
  entry: Entry; runtime: NativeShellRuntime; navigation: NativeShellBootstrapContext['navigation']; onBack: () => void;
}>) {
  // The committed snapshot this render belongs to, never read again later: the opening
  // handed to this render's handlers is captured from it and retired synchronously when
  // the commit changes or the surface unmounts.
  const snapshot = useSyncExternalStore(navigation.subscribe, navigation.getSnapshot);
  const visible = snapshot.route === entry.route;
  const place = useNativeStackPlacement(entry.route), stacked = useNativeStackHosted();
  const [opening, setOpening] = useState<ReadingSearchOpening | undefined>(undefined);
  useEffect(() => {entry.port.attachReporter(runtime.reportError);}, [entry, runtime]);
  useLayoutEffect(() => {
    if (!visible || snapshot.route !== entry.route) {setOpening(undefined); return;}
    const admitted = entry.port.admit(snapshot);
    setOpening(admitted);
    return () => {if (admitted) entry.port.retire(admitted);};
  }, [entry, snapshot, visible]);
  return place(<View pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, display: visible || stacked ? 'flex' : 'none'}}>
    <ReadingSearchScreen port={entry.port} opening={opening} runtime={runtime} query={entry.route.query} onBack={onBack}/>
  </View>);
}
