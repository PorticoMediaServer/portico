import {useLayoutEffect, useMemo} from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';
import {NativeScreenLifetime, screenScopeEnded} from './lifetime';

/** Stable transport/captured authority; subscriptions exist only for committed viewports. */
export function useSettingsRequestScope(context: NativeShellBootstrapContext) {
  const scope = useMemo(() => {
    const authority = Object.freeze({...context.authority()});
    let active: NativeScreenLifetime | undefined;
    const lifetime = {current: () => !!active?.current()};
    const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      const captured = active;
      if (!captured) return Promise.reject(screenScopeEnded());
      return captured.request(next => context.api.request<T>(path, method, body, next), signal);
    }};
    const mount = () => {
      if (!context.current() || !sameAuthority(authority, context.authority())) return;
      const captured = new NativeScreenLifetime(context);
      active = captured;
      return () => {captured.dispose(); if (active === captured) active = undefined;};
    };
    return {api, lifetime, authority, mount};
  }, [context]);
  useLayoutEffect(() => scope.mount(), [scope]);
  return scope;
}
