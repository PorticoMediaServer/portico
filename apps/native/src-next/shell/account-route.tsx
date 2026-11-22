import React from 'react';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {NativeShellRoute} from './routes';
import {nativeShellRoutes, type NativeRouteFactory} from './route-registry';
import {ProfilesRoutePage} from '../pages/shared/accounts/ProfilesRoutePage';
import {ServersRoutePage} from '../pages/shared/accounts/ServersRoutePage';

/** Genuine account destinations for the current authority. Shell media routes and Home
 * remain with their existing owners. Runtime disposal retires every private view below. */
export function AccountRoute({route, context}: {route: NativeShellRoute; context: NativeShellBootstrapContext}) {
  const back = () => {context.navigation.back(context.authority());};
  return route.kind === 'profiles' ? <ProfilesRoutePage context={context} route={route} onBack={back}/>
    : <ServersRoutePage context={context} initialPanel={route.kind === 'servers' ? route.initialPanel : undefined} onBack={back}/>;
}

const createAccountRoute: NativeRouteFactory = context => ({render: route => {
  if (route.kind !== 'profiles' && route.kind !== 'servers') throw new Error('native_account_route_mismatch');
  return <AccountRoute key={context.navigation.getSnapshot().revision} route={route} context={context}/>;
}});
nativeShellRoutes.registerRoute('profiles', createAccountRoute);
nativeShellRoutes.registerRoute('servers', createAccountRoute);
