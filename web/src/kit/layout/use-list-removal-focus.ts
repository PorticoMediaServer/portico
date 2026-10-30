import {useLayoutEffect, useMemo, useRef} from 'react';
import {RemovalFocusOwner} from './removal-focus-owner';

/** Call before useLayoutPresence, so its beforeLeave sees this commit's original live DOM. */
export function useListRemovalFocus(scope?: object) {
  const owner = useMemo(() => { /* Preserve the original ownership/invalidation keys. */ void scope; return new RemovalFocusOwner(); }, [scope]);
  const root = useRef<HTMLDivElement | null>(null);
  useLayoutEffect(() => {owner.activate();return () => owner.dispose();}, [owner]);
  useLayoutEffect(() => {owner.commit(root.current);});
  return {root, beforeLeave: owner.beforeLeave, scope: owner, lifetime: owner.lifetime};
}
