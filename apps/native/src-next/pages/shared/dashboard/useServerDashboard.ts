import {useEffect, useSyncExternalStore} from 'react';
import {recoverEventState} from '../../../platform/event-resync';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';

/** The server Dashboard's live read, for every form: it refreshes on open, on an admin.sessions hint and after a resync, and
 * a viewer the server turns away is sent on (inline: the parent picks the first permitted account page; standalone: Settings). */
export function useServerDashboard({controller, ports, runtime, onDenied}: {
  controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; onDenied?: () => void;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  useEffect(() => {
    void controller.refresh();
    // A resync means admin.sessions hints may have been missed: read the Dashboard as it is now.
    const refresh = () => {void controller.refresh();};
    const offs = [ports.context.events.on('admin.sessions', refresh), recoverEventState(ports.context.events, async ({signal}) => {
      if (signal.aborted || !controller.lifetime.current()) return;
      await controller.refresh();
      if (!signal.aborted && controller.lifetime.current() && controller.getSnapshot().error) throw controller.getSnapshot().error;
    })];
    return () => {for (const off of offs) off();};
  }, [controller, ports]);
  useEffect(() => {
    if (state.admission !== 'denied') return;
    // Inline keeps the outer Settings route and asks the parent to select its first
    // permitted account page; standalone retains the existing route transition.
    if (onDenied) {onDenied(); return;}
    runtime.onDestination({kind: 'settings'});
  }, [onDenied, state.admission, runtime]);
  return state;
}
