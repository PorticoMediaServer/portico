import {subscribePresentation} from '../../shell/fact-hooks';
import {useCallback, useSyncExternalStore} from 'react';
import {appearancePreferences} from '@core/preference-effects.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';

const systemMotion = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
const motionSubscribe = (listener: () => void) => {
  if (typeof matchMedia !== 'function') return () => {};
  const query = matchMedia('(prefers-reduced-motion: reduce)');
  query.addEventListener('change', listener);
  return () => query.removeEventListener('change', listener);
};

/** This is the captured shell's confirmed reader, never a second preference store. */
export function useScreenPreferences(context: ShellAssemblyContext | undefined) {
  const subscribe = useCallback((listener: () => void) => subscribePresentation(context, listener), [context]);
  const read = useCallback(() => context?.preferences?.getSnapshot() ?? context?.facts.preferences, [context]);
  return useSyncExternalStore(subscribe, read, read);
}

export function useScreenAppearance(context: ShellAssemblyContext | undefined) {
  const snapshot = useScreenPreferences(context);
  const reducedMotion = useSyncExternalStore(motionSubscribe, systemMotion, () => false);
  return snapshot ? appearancePreferences(snapshot, reducedMotion) : undefined;
}
