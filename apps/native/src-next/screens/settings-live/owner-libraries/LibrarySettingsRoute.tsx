import React from 'react';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeRouteRenderer} from '../../../shell/route-registry';
import {NativeScreenLifetime} from '../lifetime';
import {createNativeSettingsPorts} from '../settings-ports';
import {createNativeSettingsRenderer} from '../SettingsRoute';
import {Screen} from '../Screen';
import {NativeOwnerLibrariesPanel} from './OwnerLibrariesPanel';

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
      return <Screen><NativeOwnerLibrariesPanel key={route.libraryId} ports={ports} runtime={runtime} initialLibraryId={route.libraryId} lifetime={lifetime}/></Screen>;
    },
    dispose: () => {fallback.dispose?.(); lifetime.dispose();},
  };
}
