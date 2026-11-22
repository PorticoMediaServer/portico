import React from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import {OwnerDashboardPage} from './OwnerDashboardPage';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useServerDashboard} from '../../../pages/shared/dashboard/useServerDashboard';

/** The Dashboard on a phone or tablet (owner-dashboard.html). TV draws its own in Server settings (pages/tv/dashboard). */
export function ServerDashboardPage({controller, ports, runtime, deniedFallback, inline = false, onDenied}: {controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; deniedFallback: React.ReactNode; inline?: boolean; onDenied?: () => void}) {
  const state = useServerDashboard({controller, ports, runtime, onDenied});
  // Ruling17.3: a forbidden direct destination falls back to the first permitted account
  // Settings page. Render that genuine surface while Accounts performs the route transition.
  if (state.admission === 'denied') return <>{deniedFallback}</>;
  return <OwnerDashboardPage controller={controller} ports={ports} runtime={runtime} inline={inline}/>;
}
