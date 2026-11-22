import './notification-destinations';
import {createNativeLibrarySettingsRenderer} from './library-settings-route';
import {createNativeEqualizerSettingsRenderer, createNativeTVServerSettingsRenderer, createNativeServerDashboardRenderer, createNativeSettingsRenderer} from './settings-route';
import {nativeShellRoutes, type NativeRouteFactory} from './route-registry';

/** One registration for the phone and TV entries. The Settings family owns the
 * composition, its real ports and its native panels; this module only mounts it. */
const settingsFactory: NativeRouteFactory = context => createNativeSettingsRenderer(context);
const dashboardFactory: NativeRouteFactory = context => createNativeServerDashboardRenderer(context);

nativeShellRoutes.registerDestination('settings', settingsFactory);
nativeShellRoutes.registerRoute('server-dashboard', dashboardFactory);

nativeShellRoutes.registerRoute('server-settings', context => createNativeTVServerSettingsRenderer(context));

nativeShellRoutes.registerRoute('settings-equalizer', context => createNativeEqualizerSettingsRenderer(context));
nativeShellRoutes.registerRoute('library-settings', context => createNativeLibrarySettingsRenderer(context));
