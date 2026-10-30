import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {useFocusRegistry, type FocusSnapshot} from '../../kit/focus';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {type NativeShellRoute} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import {authorityKey, sameAuthority} from '../../shell/model';
import {createNativeEntryActions} from '../destinations/entry-actions';
import {catalogueSelectionSource, createNativeSelection} from '../selection/native-selection';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {NativeCollectionController} from './controller';
import {NativeCollectionScreen} from './CollectionScreen';

type CollectionRoute = Extract<NativeShellRoute, {kind: 'collection'}>;

/** One real catalogue collection page. It reads the server's collection view and
 * uses the same authority-local queue selector for Play all and Shuffle. The page
 * stays mounted while a title opened from it is on top, so Back finds the same
 * window, scroll position and focused card (as See all does). */
export function createNativeCollectionRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  let alive = true, sequence = 0;
  const pages = new Map<CollectionRoute, Readonly<{key: number}>>(), listeners = new Set<() => void>();
  const reconcile = () => {
    const keep = new Set(alive && context.current() ? [...context.navigation.retainedRoutes(), context.navigation.getSnapshot().route] : []);
    let changed = false;
    for (const route of [...pages.keys()]) if (!keep.has(route)) {pages.delete(route); changed = true;}
    for (const route of keep) if (route.kind === 'collection' && !pages.has(route)) {pages.set(route, {key: ++sequence}); changed = true;}
    if (changed) for (const listener of [...listeners]) listener();
  };
  const unsubscribe = context.navigation.subscribe(reconcile);
  reconcile();
  return Object.freeze({
    render: (route: NativeShellRoute) => {
      if (route.kind === 'collection') reconcile();
      return null;
    },
    renderRetained: (runtime: NativeShellRuntime) => context.current() && context.session()?.viewer
      ? <RetainedCollections pages={pages} subscribe={listener => {listeners.add(listener); return () => {listeners.delete(listener);};}} context={context} runtime={runtime}/> : null,
    dispose: () => {alive = false; unsubscribe(); pages.clear(); for (const listener of [...listeners]) listener(); listeners.clear();},
  });
}

function RetainedCollections({pages, subscribe, context, runtime}: {pages: ReadonlyMap<CollectionRoute, Readonly<{key: number}>>;
  subscribe: (listener: () => void) => () => void; context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const [, setRevision] = useState(0);
  useEffect(() => subscribe(() => setRevision(value => value + 1)), [subscribe]);
  const route = useSyncExternalStore(context.navigation.subscribe, () => context.navigation.getSnapshot().route);
  return <>{[...pages].map(([page, {key}]) => <RetainedCollection key={key} route={page} active={route === page} context={context} runtime={runtime}/>)}</>;
}

/** Hidden pages keep their state but take no focus, touches or screen-reader attention;
 * the opener's focus is held while covered and restored on return. */
function RetainedCollection({route, active, context, runtime}: {route: CollectionRoute; active: boolean; context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const registry = useFocusRegistry(), opener = useRef<FocusSnapshot | undefined>(undefined), release = useRef<(() => void) | undefined>(undefined), shown = useRef(active);
  const place = useNativeStackPlacement(route), stacked = useNativeStackHosted();
  useEffect(() => {if (!active) return; const remember = () => {const value = registry.capture(); if (value) opener.current = value;};
    remember(); return registry.subscribe(remember);
  }, [active, registry]);
  useLayoutEffect(() => {
    if (shown.current && !active && opener.current && !release.current) release.current = registry.retain(opener.current);
    const restored = !shown.current && active; shown.current = active;
    if (!restored) return;
    release.current?.(); release.current = undefined;
    const frame = requestAnimationFrame(() => registry.restore(opener.current));
    return () => cancelAnimationFrame(frame);
  }, [active, registry]);
  useEffect(() => () => release.current?.(), []);
  return place(<View pointerEvents={active ? 'auto' : 'none'} accessibilityElementsHidden={!active} importantForAccessibility={active ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, opacity: active || stacked ? 1 : 0}}>
    <CollectionHost route={route} active={active} context={context} runtime={runtime}/>
  </View>);
}

function CollectionHost({route, active, context, runtime}: {route: CollectionRoute; active: boolean; context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const viewer = context.session()!.viewer, authority = context.authority();
  const controller = useMemo(() => new NativeCollectionController({
    api: context.api, scope: {serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])},
    libraryId: route.libraryId, collectionId: route.collectionId, title: route.title, current: context.current,
  }), [context, route.libraryId, route.collectionId, route.title, viewer.accountId, viewer.authority, viewer.profileId, viewer.serverId]);
  useEffect(() => {void controller.open().catch(runtime.reportError); return () => controller.dispose();}, [controller, runtime]);
  const actions = useMemo(() => createNativeEntryActions(context, {
    current: () => context.navigation.getSnapshot().route === route,
    contains: controller.contains, subscribe: controller.subscribe,
  }), [context, controller, route]);
  useEffect(() => () => actions.dispose(), [actions]);
  const selection = useMemo(() => {
    let previous: ReturnType<typeof context.navigation.getSnapshot> | undefined;
    let activation: ReturnType<NativeDestinationActivationPort['getSnapshot']>;
    const port: NativeDestinationActivationPort = {subscribe: context.navigation.subscribe, getSnapshot: () => {
      const now = context.navigation.getSnapshot();
      if (previous !== now) {previous = now; activation = Object.freeze({visible: context.current() && now.route === route,
        revision: now.revision, intent: context.navigation.getIntent()});}
      return activation;
    }};
    return createNativeSelection(context, catalogueSelectionSource({subscribe: controller.subscribe, getSnapshot: () => {
      const state = controller.getSnapshot(), projection = state.projection, viewer = context.session()?.viewer, resident = controller.resident();
      return {key: JSON.stringify(['collection', route.libraryId, route.collectionId, projection?.query]), witness: state,
        context: projection && !state.error && viewer && (viewer.authority === 'local' || viewer.authority === 'hosted') ? {
          scope: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId,
            viewId: JSON.stringify(['collection', route.libraryId, route.collectionId, projection.query]), privacyFence: projection.scope.viewerFence},
          revision: {catalogRevision: String(projection.revision.catalog), viewerRevision: String(projection.revision.viewer)},
        } : null, sections: [{id: 'items', entries: resident.entries, positions: resident.positions}]};
    }}, port), {area: 'saved', aggregates: true, preserveEvicted: true});
  }, [context, controller, route]);
  useEffect(() => () => selection.dispose(), [selection]);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const current = () => context.current() && sameAuthority(context.navigation.getSnapshot().authority, authority);
  const play = (shuffle: boolean) => {
    const port = runtime.playback?.cataloguePlayback?.();
    if (!port) return;
    const operation = new AbortController(), revision = context.navigation.getSnapshot().revision;
    const isCurrent = () => !operation.signal.aborted && current() && context.navigation.getSnapshot().revision === revision;
    void port.playSegments([{source: {container: {kind: 'collection', id: route.collectionId}}, ...(shuffle ? {order: {mode: 'shuffle' as const}} : {})}],
      {signal: operation.signal, isCurrent}).catch(runtime.reportError);
  };
  return <NativeCollectionScreen focusScope={`collection:${authorityKey(authority)}:${route.libraryId}:${route.collectionId}`} state={state} runtime={runtime} actions={actions} selection={selection}
    active={active} onPlay={() => play(false)} onShuffle={() => play(true)}
    onSort={(sort, direction) => {void controller.sort(sort, direction).catch(runtime.reportError);}}
    onOpen={actions.open}
    onBack={() => {if (current() && context.navigation.getSnapshot().route === route) context.navigation.back(authority);}}
    onRetry={() => {void controller.retry().catch(runtime.reportError);}}/>;
}

nativeShellRoutes.registerRoute('collection', createNativeCollectionRenderer);
