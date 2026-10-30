import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src';
import type {NativePlaybackAdapter, NativePlaybackReadLifetime} from '../platform/playback/types';
import type {NativeShellBootstrapContext} from './bootstrap';

type Request = Parameters<NonNullable<NativePlaybackAdapter['setQueuePolicy']>>[1];

/** The real queue command, with the expanded screen and route's lifetime. */
export function createNativeListeningQueuePolicy(context: NativeShellBootstrapContext) {
  let disposed = false;
  const pending = new Set<{abort: AbortController; current: () => boolean}>();
  const expanded = (fence: PlaybackFence) => {
    const state = context.playback.getSnapshot();
    return !disposed && context.current() && state.dock.local?.expanded === true && sameFence(state.fence, fence);
  };
  const check = () => {for (const operation of pending) if (!operation.current()) operation.abort.abort();};
  const off = [context.navigation.subscribe(check), context.subscribeAuthority(check), context.playback.subscribe(check)];
  return Object.freeze({
    canApply: (fence: PlaybackFence) => !!context.playback.setQueuePolicy && expanded(fence)
      && context.playback.getSnapshot().capabilities.queuePolicy === true,
    apply: async (fence: PlaybackFence, request: Request, caller: NativePlaybackReadLifetime): Promise<void> => {
      if (!context.playback.setQueuePolicy || !expanded(fence) || caller.signal.aborted || !caller.isCurrent()) return;
      const revision = context.navigation.getSnapshot().revision, abort = new AbortController();
      // The accepted queue revision may change. The original route/fence stays fixed.
      const current = () => !abort.signal.aborted && !caller.signal.aborted && caller.isCurrent() && expanded(fence)
        && context.navigation.getSnapshot().revision === revision;
      const operation = {abort, current}, cancel = () => abort.abort();
      pending.add(operation); caller.signal.addEventListener('abort', cancel, {once: true});
      try {if (current()) await context.playback.setQueuePolicy(fence, request, {signal: abort.signal, isCurrent: current});}
      finally {caller.signal.removeEventListener('abort', cancel); pending.delete(operation); abort.abort();}
    },
    dispose: () => {if (disposed) return; disposed = true; for (const operation of pending) operation.abort.abort(); pending.clear(); off.forEach(stop => stop());},
  });
}

export type NativeListeningQueuePolicy = ReturnType<typeof createNativeListeningQueuePolicy>;
