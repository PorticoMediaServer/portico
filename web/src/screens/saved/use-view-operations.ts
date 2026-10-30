import {useEffect, useMemo, useRef} from 'react';
import {SavedOperations} from './operations.ts';

/** Route-local UI work may not redirect, reopen a dialog, or report an old error
 * after navigation. The parent retains authority/transport lifetime ownership. */
export function useSavedViewOperations(parent: SavedOperations, routeKey: string): SavedOperations {
  const currentKey = useRef(routeKey);
  currentKey.current = routeKey;
  const binding = useMemo(() => {
    const lifetime = {active: true};
    return {lifetime, operations: new SavedOperations(() => lifetime.active && currentKey.current === routeKey && parent.current)};
  }, [parent, routeKey]);
  useEffect(() => {
    binding.lifetime.active = true;
    return () => { binding.lifetime.active = false; binding.operations.abortPending(); };
  }, [binding]);
  return binding.operations;
}
