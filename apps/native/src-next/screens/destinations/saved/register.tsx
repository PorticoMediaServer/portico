import React from 'react';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {SavedRoute} from '../../../../../../packages/client-core/src/saved';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../../shell/route-registry';
import {type NativeShellRoute} from '../../../shell/routes';
import {createNativeEntryActions} from '../entry-actions';
import {createNativeSavedSelection} from '../../saved/selection';
import {createNativeTVSavedDestination} from './model';
import {nativeTVSavedSelectionSource} from './tv-source';
import {TVSavedScreen} from './TVSavedScreen';

/** tv-more1–5: TV owns the Saved destination and its landing. Pushed playlist,
 * collection and saved-view pages stay with Saved's own route registration. */
export function createNativeTVSavedRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const destination = createNativeTVSavedDestination(context);
  let disposed = false, selection: ReturnType<typeof createNativeSavedSelection> | undefined;
  // One canonical Saved selection owner, over the TV landing's own authorized projections.
  const savedSelection = () => selection ??= createNativeSavedSelection(context, nativeTVSavedSelectionSource(destination.controller), destination.activation);
  const current = () => !disposed && context.current();
  const savedRoute = (): NativeShellRoute | undefined => {
    const route = context.navigation.getSnapshot().route;
    return route.kind === 'shell' && route.destination.kind === 'saved' ? route : undefined;
  };
  const actions = createNativeEntryActions(context, {
    current: () => current() && !!savedRoute(),
    contains: entry => destination.controller.contains(entry),
    subscribe: destination.controller.subscribe,
  });
  const openEntry = (entry: ContentEntry) => {
    if (!current()) return;
    actions.open(entry);
  };
  const openSaved = (view: SavedRoute) => {
    if (!current()) return;
    context.navigation.open({kind: 'saved-view', view: Object.freeze({...view})}, context.authority());
  };
  const renderer: NativeRouteRenderer = {
    render: (route, runtime) => {
      if (route.kind !== 'shell' || route.destination.kind !== 'saved' || !current()) return null;
      return <TVSavedScreen controller={destination.controller} runtime={runtime} actions={actions} activation={destination.activation}
        authority={runtime.getSnapshot().authority} initialTab="watchlist" selection={savedSelection()} openSaved={openSaved} openEntry={openEntry}
        browseHome={() => {if (current()) context.navigation.open({kind: 'shell', destination: {kind: 'home'}}, context.authority());}}/>;
    },
    dispose: () => {if (disposed) return; disposed = true; actions.dispose(); selection?.dispose(); destination.dispose();},
  };
  return Object.freeze(renderer);
}

nativeShellRoutes.registerDestination('saved', createNativeTVSavedRenderer);
