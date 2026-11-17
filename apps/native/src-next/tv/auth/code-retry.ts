import {useEffect, useRef} from 'react';
import {repeat} from '../../../../../packages/client-core/src/timer';

/** How often a TV without a code asks for one again (the code card says it keeps trying). */
export const CODE_RETRY_MS = 30_000;

/** While `active`, asks for a code every 30 seconds through the shared timer: one request per tick, through a caller
 * that ignores a request while one is already in flight. Nothing runs while inactive (busy, or a code is showing). */
export function useCodeRetry(active: boolean, retry: () => void) {
  const latest = useRef(retry); latest.current = retry;
  useEffect(() => {
    if (!active) return;
    const tick = repeat(async () => {latest.current();}, {everyMs: CODE_RETRY_MS, backoff: {baseMs: CODE_RETRY_MS, maxMs: CODE_RETRY_MS, jitter: 'none'}, immediate: false});
    return () => tick.stop();
  }, [active]);
}
