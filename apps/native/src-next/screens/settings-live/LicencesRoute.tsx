import React from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {LicencesPage} from '../../pages/touch/settings/LicencesPage';

/** Local generated notices belong to this restored shell's ordinary route lifetime. */
export function createNativeLicencesRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  let alive = true;
  const current = () => alive && context.current() && context.navigation.getSnapshot().route.kind === 'licenses';
  return {
    render: route => route.kind === 'licenses' && current()
      ? <LicencesPage current={current} onBack={() => {if (current()) context.navigation.back(context.authority());}}/> : null,
    dispose: () => {alive = false;},
  };
}

nativeShellRoutes.registerRoute('licenses', createNativeLicencesRenderer);
