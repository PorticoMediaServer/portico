import {useLayoutEffect, useRef, useSyncExternalStore} from 'react';
import {useOptionalFocusRegistry} from './FocusRegion';
import {createStableFocusOrder} from './stable-order';
const noSubscribe = () => () => {}, zero = () => 0;

/** Includes a live overlay's original-return lease until actual paint removal. */
export function useStableFocusOrder<T>(getKey: (item: T) => string, input: Readonly<{regionId?: string; scope: unknown;
  items: readonly T[]; allItems: readonly T[]; memberIds: ReadonlySet<string>}>) {
  const registry = useOptionalFocusRegistry();
  useSyncExternalStore(registry?.subscribe ?? noSubscribe, registry?.getSnapshot ?? zero, registry?.getSnapshot ?? zero);
  const held = !!input.regionId && !!registry?.isRegionActive(input.regionId);
  const owner = useRef<ReturnType<typeof createStableFocusOrder<T>> | undefined>(undefined);
  if (!owner.current) owner.current = createStableFocusOrder(getKey);
  const items = owner.current.project({...input, held}), scope = input.scope;
  useLayoutEffect(() => {owner.current!.commit(scope, items);}, [scope, items]);
  return items;
}
