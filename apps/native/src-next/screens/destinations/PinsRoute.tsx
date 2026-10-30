import React from 'react';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import type {NativeShellRoute} from '../../shell/routes';
import {nativeScreenServices} from '../native-screen-services';
import {nativeDestinationLifetime} from './lifetime';
import {NativeDestinationPins} from './pins';
import {NativePinEditorSheet} from './PinEditorSheet';

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
      return <NativePinEditorSheet controller={controller} libraries={context.libraries()} onClose={() => {
        if (!disposed && lifetime.current() && context.navigation.getSnapshot().route === route) context.navigation.back(context.authority());
      }}/>;
    },
    dispose() {if (disposed) return; disposed = true; off(); retire();},
  };
};

// One factory identity: all three drawn entries borrow the same authority owner.
nativeShellRoutes.registerRoute('edit-pins', createNativeHandheldPinsModule);
nativeShellRoutes.registerRoute('library-edit-pins', createNativeHandheldPinsModule);
