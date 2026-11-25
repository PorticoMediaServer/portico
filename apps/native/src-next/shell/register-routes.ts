import type {ShellDestination} from './model';
import type {NativeShellRoute} from './routes';
import {nativeShellRoutes, type NativeRouteFactory, type NativeShellRouteRegistry} from './route-registry';
import {createAccountRoute} from './account-route';
import {createNativeDownloadsModule} from './downloads-route';
import {createNativeCollectionRenderer} from './collections-route';
import {createNativeHandheldLibraryModule} from './library-route';
import {createNativeLibraryOverviewModule} from './library-overview-route';
import {createNativeHandheldPinsModule} from './pins-route';
import {createNativeTVMusicRenderer} from './music-route';
import {createNativeBookRenderer} from './audiobooks-route';
import {createNativeReadModule} from './read-route';
import {createNativeReadBookRenderer, createNativeReadPeriodicalRenderer, createNativeReadSeriesRenderer} from './read-destinations';
import {createNativeReadingSettingsRenderer} from './reading-settings-route';
import {createNativePersonRenderer} from './people-route';
import {createNativeNotificationsRenderer} from './notifications-route';
import {createNativeRecordingsRenderer, createNativeServerFeedbackRenderer} from './notification-destinations';
import {createNativeSearchModule} from './search-route';
import {createNativeReadingSearchModule} from './reading-search-route';
import {createNativeEqualizerSettingsRenderer, createNativeServerDashboardRenderer, createNativeSettingsRenderer, createNativeTVServerSettingsRenderer} from './settings-route';
import {createNativeLibrarySettingsRenderer} from './library-settings-route';
import {createNativeLicencesRenderer} from './licences-route';
import {createNativeHomeCustomizeRenderer} from './customize-route';
import {createNativeHomeSeeAllRenderer} from './see-all-route';
import {createNativeLiveRenderer} from './live-route';
import {createNativeTogetherRenderer} from './together-route';
import {createNativeSavedModule} from './saved-route';
import {createNativeTVSavedRenderer} from './saved-destination-route';
import {createNativeWatchBindings, registerNativeWatch} from '../screens/watch';

export type NativeDestinationRegistration = Readonly<{
  kind: ShellDestination['kind'];
  factory: NativeRouteFactory;
}>;

export type NativeRouteRegistration = Readonly<{
  kind: NativeShellRoute['kind'];
  factory: NativeRouteFactory;
}>;

/** Shell destinations registered on both Touch and TV. */
export const NATIVE_SHARED_DESTINATIONS: readonly NativeDestinationRegistration[] = [
  {kind: 'library', factory: createNativeHandheldLibraryModule},
  {kind: 'libraries', factory: createNativeLibraryOverviewModule},
  {kind: 'search', factory: createNativeSearchModule},
  {kind: 'settings', factory: createNativeSettingsRenderer},
  {kind: 'live', factory: createNativeLiveRenderer},
];

/** Shell destinations registered only on Touch (Phone, Tablet, Foldable, Desktop). */
export const NATIVE_TOUCH_DESTINATIONS: readonly NativeDestinationRegistration[] = [
  {kind: 'saved', factory: createNativeSavedModule},
];

/** Shell destinations registered only on TV. */
export const NATIVE_TV_DESTINATIONS: readonly NativeDestinationRegistration[] = [
  {kind: 'saved', factory: createNativeTVSavedRenderer},
];

/** Routes registered on both Touch and TV. */
export const NATIVE_SHARED_ROUTES: readonly NativeRouteRegistration[] = [
  {kind: 'profiles', factory: createAccountRoute},
  {kind: 'servers', factory: createAccountRoute},
  {kind: 'collection', factory: createNativeCollectionRenderer},
  {kind: 'music', factory: createNativeTVMusicRenderer},
  {kind: 'book', factory: createNativeBookRenderer},
  {kind: 'audiobook', factory: createNativeBookRenderer},
  {kind: 'person', factory: createNativePersonRenderer},
  {kind: 'search-genre', factory: createNativeSearchModule},
  {kind: 'search-genres', factory: createNativeSearchModule},
  {kind: 'search-reading', factory: createNativeReadingSearchModule},
  {kind: 'server-dashboard', factory: createNativeServerDashboardRenderer},
  {kind: 'server-settings', factory: createNativeTVServerSettingsRenderer},
  {kind: 'settings-equalizer', factory: createNativeEqualizerSettingsRenderer},
  {kind: 'library-settings', factory: createNativeLibrarySettingsRenderer},
  {kind: 'licenses', factory: createNativeLicencesRenderer},
  {kind: 'home-customize', factory: createNativeHomeCustomizeRenderer},
  {kind: 'home-see-all', factory: createNativeHomeSeeAllRenderer},
  {kind: 'together', factory: createNativeTogetherRenderer},
  {kind: 'saved-view', factory: createNativeSavedModule},
  {kind: 'saved-destination', factory: createNativeSavedModule},
];

/** Routes registered only on Touch (Phone, Tablet, Foldable, Desktop). */
export const NATIVE_TOUCH_ROUTES: readonly NativeRouteRegistration[] = [
  {kind: 'downloads', factory: createNativeDownloadsModule},
  {kind: 'edit-pins', factory: createNativeHandheldPinsModule},
  {kind: 'library-edit-pins', factory: createNativeHandheldPinsModule},
  {kind: 'read', factory: createNativeReadModule},
  {kind: 'read-book', factory: createNativeReadBookRenderer},
  {kind: 'read-series', factory: createNativeReadSeriesRenderer},
  {kind: 'read-periodical', factory: createNativeReadPeriodicalRenderer},
  {kind: 'settings-page', factory: createNativeReadingSettingsRenderer},
  {kind: 'notifications', factory: createNativeNotificationsRenderer},
  {kind: 'recordings', factory: createNativeRecordingsRenderer},
  {kind: 'server-feedback', factory: createNativeServerFeedbackRenderer},
];

/** Explicitly and idempotently registers all shell destinations, routes, and watch outlets for Touch or TV. */
export function registerNativeRoutes(isTV: boolean, registry: NativeShellRouteRegistry = nativeShellRoutes): void {
  const destinations = [...NATIVE_SHARED_DESTINATIONS, ...(isTV ? NATIVE_TV_DESTINATIONS : NATIVE_TOUCH_DESTINATIONS)];
  for (const entry of destinations) {
    if (!registry.hasDestination(entry.kind)) registry.registerDestination(entry.kind, entry.factory);
  }
  const routes = [...NATIVE_SHARED_ROUTES, ...(isTV ? [] : NATIVE_TOUCH_ROUTES)];
  for (const entry of routes) {
    if (!registry.hasRoute(entry.kind)) registry.registerRoute(entry.kind, entry.factory);
  }
  if (!registry.hasWatch() && !registry.hasRoute('title') && !registry.hasRoute('channel')) {
    registerNativeWatch(createNativeWatchBindings, registry);
  }
}
