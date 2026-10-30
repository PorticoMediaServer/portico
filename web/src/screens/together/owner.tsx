import {useEffect, useSyncExternalStore} from 'react';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import type {WatchBindings} from '../watch';
import {createTogetherBindings, type TogetherBindings} from './bindings';

const owners = new WeakMap<ShellAssemblyContext, TogetherBindings>(), listeners = new Set<() => void>();
const changed = () => {for (const listener of [...listeners]) listener();};
const subscribe = (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};};
/** Screen-owned rendezvous, keyed by the actual parent context. No import-time service/I/O. */
export function TogetherOwner({context, playback}: {context: ShellAssemblyContext; playback: WatchBindings}) {
  useEffect(() => {
    if (!context.isCurrent() || playback.runtime.getSnapshot().disposed) return;
    const binding = createTogetherBindings(context, playback);owners.set(context, binding);changed();binding.start();
    return () => {if (owners.get(context) === binding) owners.delete(context);binding.dispose();changed();};
  }, [context, playback]);
  return null;
}
export function useTogetherBindings(context?: ShellAssemblyContext): TogetherBindings | undefined {
  return useSyncExternalStore(subscribe, () => {const value = context && owners.get(context);return value?.isCurrent() ? value : undefined;}, () => undefined);
}
