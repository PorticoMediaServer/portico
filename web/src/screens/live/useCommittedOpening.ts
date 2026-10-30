import {useCallback, useLayoutEffect, useRef, useState} from 'react';

export type Opening<T> = {id: string; target: T; live: boolean};

/** One committed confirmation opening. The exact published object is retired
 * only by its own layout cleanup or by consuming that same object; a retained
 * action from an older opening cannot consume the current one. */
export function useCommittedOpening<T>(requested: T | undefined) {
  const [published, publish] = useState<Opening<T>>();
  const held = useRef<Opening<T> | undefined>(undefined);
  useLayoutEffect(() => {
    if (requested === undefined) return;
    const opening: Opening<T> = {id: crypto.randomUUID(), target: requested, live: true};
    held.current = opening;
    publish(opening);
    return () => {opening.live = false; if (held.current === opening) held.current = undefined;};
  }, [requested]);
  const consume = useCallback((opening: Opening<T>) => {
    if (!opening.live || held.current !== opening) return false;
    opening.live = false;held.current = undefined;
    return true;
  }, []);
  return {opening: published?.live ? published : undefined, consume};
}
