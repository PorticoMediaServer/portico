import React from 'react';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {NativeRouteRenderer} from './route-registry';
import {NativeScreenLifetime} from '../screens/settings-live/lifetime';
import {createNativeSettingsPorts} from '../screens/settings-live/settings-ports';
import {createNativeSettingsRenderer} from '../screens/settings-live/SettingsRoute';
import {Screen} from '../screens/settings-live/Screen';
import {ServerLibrariesPage} from '../pages/touch/settings/ServerLibrariesPage';

/** Reuse the actual owner library editor; no alternate page or administration API. */
export function createNativeLibrarySettingsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = new NativeScreenLifetime(context);
  const fallback = createNativeSettingsRenderer(context);
  const ports = createNativeSettingsPorts(context, lifetime, route => {
    lifetime.assert(); context.navigation.open(route, lifetime.authority);
  });
  return {
    render: (route, runtime) => {
      lifetime.assert();
      if (route.kind !== 'library-settings') throw new Error('native_library_settings_route_mismatch');
      const account = context.account;
      if (!account.canManage || account.account.role !== 'owner' || account.account.disabled || account.account.primaryProfileId !== lifetime.authority.profileId)
        return fallback.render({kind: 'shell', destination: {kind: 'settings'}}, runtime);
      return <Screen><ServerLibrariesPage key={route.libraryId} ports={ports} runtime={runtime} initialLibraryId={route.libraryId} lifetime={lifetime}/></Screen>;
    },
    dispose: () => {fallback.dispose?.(); lifetime.dispose();},
  };
}
