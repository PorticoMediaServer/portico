import {useMemo, useSyncExternalStore} from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {playbackItemStore} from './playback-item';

export function usePlayingItem(runtime: NativeShellRuntime) {
  const store = useMemo(() => playbackItemStore(runtime.playback), [runtime.playback]);
  return useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
}
