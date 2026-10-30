import React from 'react';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import {nativeDestinationActivation} from '../../shell/routes';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {NativeLibraryDirectory} from './directory';
import {NativeLibraryOverview} from './LibraryOverview';

export const createNativeLibraryOverviewModule: NativeRouteFactory = context => {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const directory = new NativeLibraryDirectory({api: context.api, current});
  const activation = nativeDestinationActivation(context.navigation, {kind: 'libraries'});
  return {render: (route, runtime) => current() && route.kind === 'shell' && route.destination.kind === 'libraries'
    ? <NativeLibraryOverview directory={directory} runtime={runtime} activation={activation}
      openLibrary={id => {
        if (current() && activation.getSnapshot().visible && directory.getSnapshot().libraries.some(value => value.id === id)
          && context.libraries().some(value => value.id === id)) runtime.onDestination({kind: 'library', libraryId: id});
      }} editPins={() => {if (current() && activation.getSnapshot().visible) context.navigation.open({kind: 'edit-pins'}, context.authority());}}/> : null,
    dispose() {if (disposed) return; disposed = true; directory.dispose();}};
};

nativeShellRoutes.registerDestination('libraries', createNativeLibraryOverviewModule);
