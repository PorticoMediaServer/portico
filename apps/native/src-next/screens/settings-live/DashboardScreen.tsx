import React from 'react';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeSettingsLivePorts} from './ports';
import type {NativeDashboardController} from './dashboard-controller';
import {OwnerDashboardPage as NativeOwnerDashboardPhone} from '../../pages/touch/dashboard/OwnerDashboardPage';
import {useServerDashboard} from '../../pages/shared/dashboard/useServerDashboard';

/** The Dashboard on a phone or tablet (owner-dashboard.html). TV draws its own in Server settings (pages/tv/dashboard). */
export function NativeDashboardScreen({controller, ports, runtime, deniedFallback, inline = false, onDenied}: {controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; deniedFallback: React.ReactNode; inline?: boolean; onDenied?: () => void}) {
  const state = useServerDashboard({controller, ports, runtime, onDenied});
  // Ruling17.3: a forbidden direct destination falls back to the first permitted account
  // Settings page. Render that genuine surface while Accounts performs the route transition.
  if (state.admission === 'denied') return <>{deniedFallback}</>;
  return <NativeOwnerDashboardPhone controller={controller} ports={ports} runtime={runtime} inline={inline}/>;
}
