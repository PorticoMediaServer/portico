import {useLayoutEffect, useRef} from 'react';
import {useFocusRegistry} from '../../kit/focus';
import type {FocusAddress} from '../../kit/focus/registry';
import type {ShellDestination} from '../../shell/model';
import type {NativeShellRuntime} from '../../shell/runtime';
import {createTVPageBack} from './tv-page-back';

export function useTVPageBack(runtime: NativeShellRuntime, destination: ShellDestination, enabled: boolean,
  page: Readonly<{scope?: string; registerTop?: boolean; current: () => boolean; start: () => FocusAddress | undefined; atTop: () => boolean; scrollTop: () => void}>) {
  const registry = useFocusRegistry(), latest = useRef(page); latest.current = page;
  const key = JSON.stringify(destination), scope = page.scope, registerTop = page.registerTop;
  useLayoutEffect(() => {
    if (!enabled) return;
    const original = runtime.retirementSignal;
    if (original?.aborted) return;
    let alive = true, remove: (() => void) | undefined, removeTop: (() => void) | undefined;
    const owner = createTVPageBack(registry, {current: () => {
      if (!alive || original?.aborted) return false;
      const now = runtime.getSnapshot();
      return alive && !original?.aborted && now.contentVisible !== false && !now.mediaExpanded && !now.homeObscured
        && JSON.stringify(now.destination) === key && latest.current.current() && alive && !original?.aborted;
    }, start: () => latest.current.start(),
      atTop: () => latest.current.atTop(), scrollTop: () => latest.current.scrollTop()});
    const retire = () => {
      if (!alive) return;
      alive = false; owner.dispose(); removeTop?.(); remove?.();
    };
    const cleanup = () => {original?.removeEventListener('abort', retire); retire();};
    original?.addEventListener('abort', retire, {once: true});
    if (original?.aborted) {cleanup(); return;}
    remove = runtime.registerTVPageBack?.(owner.back, JSON.parse(key));
    // Registration may synchronously invoke a pending jump and retire this runtime.
    if (!alive) {remove?.(); return cleanup;}
    removeTop = registerTop ? runtime.registerScrollToTop(JSON.parse(key), owner.top) : undefined;
    if (!alive) removeTop?.();
    return cleanup;
  }, [enabled, key, registry, runtime, scope, registerTop]);
}
