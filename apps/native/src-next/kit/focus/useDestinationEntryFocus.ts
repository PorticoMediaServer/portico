import {useLayoutEffect, useMemo, useSyncExternalStore} from 'react';
import {useFocusRegistry} from './FocusRegion';
import type {FocusAddress} from './registry';
import {createDestinationEntryFocus, type NativeDestinationActivation} from './destination-entry';

export function useDestinationEntryFocus(activation: NativeDestinationActivation, address: FocusAddress | undefined, onRequested?: () => void, retainedRequest?: ReturnType<typeof createDestinationEntryFocus>) {
  const registry = useFocusRegistry();
  const revision = useSyncExternalStore(registry.subscribe, registry.getSnapshot, registry.getSnapshot);
  const ownRequest = useMemo(() => createDestinationEntryFocus(registry), [registry]);
  const request = retainedRequest ?? ownRequest;
  const regionId = address?.regionId, childId = address?.childId;
  useLayoutEffect(() => {
    if (!activation.visible || activation.intent === 'restore' || !regionId || !childId || !registry.child({regionId, childId})) {
      request(activation, undefined); return;
    }
    if (request(activation, {regionId, childId})) onRequested?.();
  }, [activation, onRequested, childId, regionId, registry, request, revision]);
}
