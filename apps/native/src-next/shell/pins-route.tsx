import React from 'react';
import {Platform} from 'react-native';
import {nativeShellRoutes, type NativeRouteFactory} from './route-registry';
import type {NativeShellRoute} from './routes';
import {nativeScreenServices} from '../screens/native-screen-services';
import {nativeDestinationLifetime} from '../screens/destinations/lifetime';
import {NativeDestinationPins} from '../screens/destinations/pins';
import {PinEditorSheet} from '../kit/pins/PinEditor';

export const createNativeHandheldPinsModule: NativeRouteFactory = context => {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  let controller: NativeDestinationPins | undefined, target: NativeShellRoute | undefined, disposed = false;
  const retire = () => {controller?.dispose(); controller = undefined; target = undefined;};
  const off = context.navigation.subscribe(() => {if (target && context.navigation.getSnapshot().route !== target) retire();});
  return {
    render(route) {
      if (disposed || !lifetime.current() || route.kind !== 'edit-pins' && route.kind !== 'library-edit-pins'
        || context.navigation.getSnapshot().route !== route) return null;
      if (route.kind === 'library-edit-pins' && !context.libraries().some(library => library.id === route.libraryId)) return null;
      if (target !== route || !controller) {
        retire(); target = route;
        controller = new NativeDestinationPins({api: context.api, serverId: lifetime.scope.serverId, requestId: common.requestId,
          libraryIds: () => context.libraries().map(library => library.id),
          current: () => !disposed && lifetime.current() && context.navigation.getSnapshot().route === route});
      }
      return <PinEditorSheet controller={controller} libraries={context.libraries()} onClose={() => {
        if (!disposed && lifetime.current() && context.navigation.getSnapshot().route === route) context.navigation.back(context.authority());
      }}/>;
    },
    dispose() {if (disposed) return; disposed = true; off(); retire();},
  };
};

// One factory identity: all three drawn entries borrow the same authority owner.
if (!Platform?.isTV) {
  if (!nativeShellRoutes.hasRoute?.('edit-pins')) nativeShellRoutes.registerRoute('edit-pins', createNativeHandheldPinsModule);
  if (!nativeShellRoutes.hasRoute?.('library-edit-pins')) nativeShellRoutes.registerRoute('library-edit-pins', createNativeHandheldPinsModule);
}
