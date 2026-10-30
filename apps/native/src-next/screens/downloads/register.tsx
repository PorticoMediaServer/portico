import React from 'react';
import {View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import {phoneBarHeight, shellLayout} from '../../shell/model';
import {createNativeDownloadsView} from './view';
import {StandardBackdrop} from '../../kit/artwork';

function DownloadsRoute({children}: {children: React.ReactNode}) {
  const insets = useSafeAreaInsets(), dimensions = useWindowDimensions();
  const layout = shellLayout(dimensions.width, dimensions.height, false);
  const landscapePhone = layout.kind === 'phone' && dimensions.width > dimensions.height;
  // This route covers the Saved page while its genuine bottom navigation remains mounted.
  // Its own absolute box (and ink) means a natural-height screen can never let the
  // previous route show through below the list.
  return <View style={{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0,
    paddingBottom: layout.tabs ? phoneBarHeight(insets.bottom, landscapePhone) : 0}}><StandardBackdrop/>{children}</View>;
}

export const createNativeDownloadsModule: NativeRouteFactory = context => {
  const view = createNativeDownloadsView(context);
  return {
    render: (route, runtime) => route.kind === 'downloads' ? <DownloadsRoute>{view.render(runtime)}</DownloadsRoute> : null,
    dispose: view.dispose,
  };
};

nativeShellRoutes.registerRoute('downloads', createNativeDownloadsModule);
