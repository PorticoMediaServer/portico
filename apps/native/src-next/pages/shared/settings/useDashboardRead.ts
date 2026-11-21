import {useEffect, useSyncExternalStore} from 'react';
import {recoverEventState} from '../../../platform/event-resync';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';

export function useDashboardRead(controller: NativeDashboardController, ports: NativeSettingsLivePorts) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  useEffect(() => {
    const refresh = () => {void controller.refresh();};
    refresh();
    const off = ports.context.events.on('admin.sessions', refresh);
    const offResync = recoverEventState(ports.context.events, async ({signal}) => {
      if (signal.aborted || !controller.lifetime.current()) return;
      await controller.refresh();
      const {error} = controller.getSnapshot();
      if (!signal.aborted && controller.lifetime.current() && error) throw error;
    });
    return () => {off(); offResync();};
  }, [controller, ports]);
  return state;
}
