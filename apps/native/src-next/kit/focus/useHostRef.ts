import {useCallback, useLayoutEffect, useRef, type ForwardedRef} from 'react';

/** A stable native callback also forwards React 19 ref cleanups and live ref replacements. */
export function useHostRef<T, U extends T = T>(forwarded: ForwardedRef<T>, onHost: (host: U | null) => void) {
  const native = useRef<U | null>(null), committed = useRef({forwarded, onHost});
  const attached = useRef<{ref: ForwardedRef<T>; host: U; cleanup?: () => void} | undefined>(undefined);
  const detach = useCallback(() => {
    const old = attached.current;
    attached.current = undefined;
    if (!old) return;
    if (old.cleanup) old.cleanup();
    else if (typeof old.ref === 'function') old.ref(null);
    else if (old.ref) old.ref.current = null;
  }, []);
  const forward = useCallback((host: U | null, ref: ForwardedRef<T>) => {
    if (attached.current?.host === host && attached.current.ref === ref) return;
    detach();
    if (!host || !ref) return;
    if (typeof ref === 'function') {
      const cleanup = ref(host);
      attached.current = {ref, host, cleanup: typeof cleanup === 'function' ? cleanup : undefined};
    } else {ref.current = host; attached.current = {ref, host};}
  }, [detach]);
  const setRef = useCallback((host: U | null) => {
    native.current = host;
    committed.current.onHost(host);
    forward(host, committed.current.forwarded);
    if (host) return () => {native.current = null; committed.current.onHost(null); detach();};
  }, [detach, forward]);
  useLayoutEffect(() => {
    committed.current = {forwarded, onHost};
    forward(native.current, forwarded);
  });
  useLayoutEffect(() => () => {committed.current.onHost(null); detach();}, [detach]);
  return {native, ref: setRef};
}
