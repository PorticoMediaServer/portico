import React from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import {nativeShellRoutes, type NativeRouteFactory} from '../../shell/route-registry';
import {ProfilesRoutePage} from '../../pages/shared/accounts/ProfilesRoutePage';
import {ServersRoutePage} from '../../pages/shared/accounts/ServersRoutePage';

/** Genuine account destinations for the current authority. Shell media routes and Home
 * remain with their existing owners. Runtime disposal retires every private view below. */
export function AccountRoute({route, context}: {route: NativeShellRoute; context: NativeShellBootstrapContext}) {
  const back = () => {context.navigation.back(context.authority());};
  return route.kind === 'profiles' ? <ProfileRoute context={context} route={route} onBack={back}/> : <ServersRoute context={context} initialPanel={route.kind === 'servers' ? route.initialPanel : undefined} onBack={back}/>;
}

/** Moved to a page; kept under this name for Settings until the rest of this file moves. */
export const ProfileRoute = ProfilesRoutePage;

/** Moved to a page; kept under this name for Settings and the shell until the rest of this file moves. */
export const ServersRoute = ServersRoutePage;

const createAccountRoute: NativeRouteFactory = context => ({render: route => {
  if (route.kind !== 'profiles' && route.kind !== 'servers') throw new Error('native_account_route_mismatch');
  return <AccountRoute key={context.navigation.getSnapshot().revision} route={route} context={context}/>;
}});
nativeShellRoutes.registerRoute('profiles', createAccountRoute);
nativeShellRoutes.registerRoute('servers', createAccountRoute);
