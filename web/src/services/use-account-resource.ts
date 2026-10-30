import {useLayoutEffect, useRef, useState} from 'react';

/** A disposed account service is terminal. Every commit/replay owns a fresh one. */
export function useAccountResource<T extends {dispose(): void}>(create: () => T) {
  const sequence = useRef(0);
  const [bound, setBound] = useState<{id: number; create: () => T; value: T; active: boolean}>();
  useLayoutEffect(() => {
    const next = {id: ++sequence.current, create, value: create(), active: true};
    setBound(next);
    return () => {next.active = false; next.value.dispose();};
  }, [create]);
  return bound?.active && bound.create === create ? bound : undefined;
}
