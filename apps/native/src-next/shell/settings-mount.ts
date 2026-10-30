import '../screens/settings-live/server/NotificationDestinations';
import {createNativeLibrarySettingsRenderer} from '../screens/settings-live/owner-libraries/LibrarySettingsRoute';
import {createNativeEqualizerSettingsRenderer, createNativeTVServerSettingsRenderer, createNativeServerDashboardRenderer, createNativeSettingsRenderer} from '../screens/settings-live/SettingsRoute';
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
