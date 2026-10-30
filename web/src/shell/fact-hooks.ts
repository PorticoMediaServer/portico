import {useCallback, useSyncExternalStore} from 'react';
import type {ShellFacts} from './bootstrap';
import type {ShellFactsSource} from './fact-source';

type PresentationContext = Readonly<{factsSource?: ShellFactsSource; preferences?: Readonly<{subscribe(listener: () => void): () => void}>}>;
/** PreferencesReader remains primary; fallback facts and identity also notify. */
export function subscribePresentation(context: PresentationContext | undefined, listener: () => void): () => void {
  const offFacts = context?.factsSource?.subscribe(listener), offPreferences = context?.preferences?.subscribe(listener);
  return () => {offFacts?.();offPreferences?.();};
}
/** A projection subscription, never a new shell/session owner. Fixtures may omit the source. */
export function useShellFacts(context: Readonly<{facts?: ShellFacts; factsSource?: ShellFactsSource}> | undefined): ShellFacts | undefined {
  const subscribe = useCallback((listener: () => void) => context?.factsSource?.subscribe(listener) ?? (() => {}), [context]);
  const read = useCallback(() => context?.factsSource?.getSnapshot() ?? context?.facts, [context]);
  return useSyncExternalStore(subscribe, read, read);
}
