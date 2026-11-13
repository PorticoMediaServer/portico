import React from 'react';
import {View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import {phoneBarHeight, shellLayout} from '../../shell/model';
import {createNativeDownloadsView} from './view';

function DownloadsRoute({children}: {children: React.ReactNode}) {
  const insets = useSafeAreaInsets(), dimensions = useWindowDimensions();
  const layout = shellLayout(dimensions.width, dimensions.height, false);
  const landscapePhone = layout.kind === 'phone' && dimensions.width > dimensions.height;
  // This route covers the Saved page while its genuine bottom navigation remains mounted.
  // Its stack page draws the standard backdrop (TouchStacks), so the previous route never shows through.
  return <View style={{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0,
    paddingBottom: layout.tabs ? phoneBarHeight(insets.bottom, landscapePhone) : 0}}>{children}</View>;
}

export const createNativeDownloadsModule: NativeRouteFactory = context => {
  const view = createNativeDownloadsView(context);
  return {
    render: (route, runtime) => route.kind === 'downloads' ? <DownloadsRoute>{view.render(runtime)}</DownloadsRoute> : null,
    dispose: view.dispose,
  };
};

nativeShellRoutes.registerRoute('downloads', createNativeDownloadsModule);
