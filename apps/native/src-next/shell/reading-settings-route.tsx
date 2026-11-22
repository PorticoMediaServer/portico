import React from 'react';
import {Platform} from 'react-native';
import type {NativeShellBootstrapContext} from './bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from './route-registry';
import {ReadingSettingsPage} from '../pages/touch/settings/ReadingSettingsPage';
import {nativeReadingSettingsPreferences} from '../screens/settings/reading-preferences';

export {nativeReadingSettingsPreferences} from '../screens/settings/reading-preferences';
export type {NativeReadingSettingsPreferences} from '../screens/settings/reading-preferences';
export {openNativeReadingSettings} from '../screens/settings/reading-entry';

export function createNativeReadingSettingsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const source = nativeReadingSettingsPreferences(context);
  return {render: (route, runtime) => {
    if (Platform.isTV || route.kind !== 'settings-page' || route.scope !== 'account' || route.page !== 'reading') throw new Error('native_reading_settings_route_mismatch');
    const captured = context.navigation.getSnapshot();
    const current = () => source.current() && context.navigation.getSnapshot() === captured;
    return <ReadingSettingsPage source={source} runtime={runtime} current={current}
      onBack={() => {if (current()) context.navigation.back(context.authority());}}/>;
  }};
}

nativeShellRoutes.registerRoute('settings-page', createNativeReadingSettingsRenderer);
