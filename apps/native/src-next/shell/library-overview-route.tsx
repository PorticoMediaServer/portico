import React from 'react';
import {Platform, useWindowDimensions} from 'react-native';
import {frameForm} from '@portico/design';
import {CoveredLayer} from '../kit/focus';
import {LibrariesPage} from '../pages/touch/library/LibrariesPage';
import type {LibraryOverviewProps} from '../pages/shared/library/useLibrary';
import {nativeShellRoutes, type NativeRouteFactory} from './route-registry';
import {nativeDestinationActivation} from './routes';
import {nativeDestinationLifetime} from '../screens/destinations/lifetime';
import {NativeLibraryDirectory} from '../screens/library/directory';
import {NativeLibraryOverview} from '../kit/browse/LibraryOverview';

export const createNativeLibraryOverviewModule: NativeRouteFactory = context => {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const directory = new NativeLibraryDirectory({api: context.api, current});
  const activation = nativeDestinationActivation(context.navigation, {kind: 'libraries'});
  return {render: (route, runtime) => current() && route.kind === 'shell' && route.destination.kind === 'libraries'
    ? <LibrariesSurface directory={directory} runtime={runtime} activation={activation}
      openLibrary={id => {
        if (current() && activation.getSnapshot().visible && directory.getSnapshot().libraries.some(value => value.id === id)
          && context.libraries().some(value => value.id === id)) runtime.onDestination({kind: 'library', libraryId: id});
      }} editPins={() => {if (current() && activation.getSnapshot().visible) context.navigation.open({kind: 'edit-pins'}, context.authority());}}/> : null,
    dispose() {if (disposed) return; disposed = true; directory.dispose();}};
};

nativeShellRoutes.registerDestination('libraries', createNativeLibraryOverviewModule);

function LibrariesSurface(props: LibraryOverviewProps) {
  const {width, height} = useWindowDimensions();
  const active = React.useSyncExternalStore(props.activation.subscribe, props.activation.getSnapshot);
  // Tablets and TV still draw the old overview (tablet work); phones are upright only.
  if (Platform.isTV || frameForm(width, false, height) !== 'phone') return <NativeLibraryOverview {...props}/>;
  return <CoveredLayer covered={!active.visible} style={{flex: 1}}><LibrariesPage {...props}/></CoveredLayer>;
}
