import {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {DetailSnapshot} from '@core/detail.ts';
import {fenceKey} from '../../../shell/playback-model';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../../platform/playback';
import type {WatchPlaybackContext} from '../play-on-binding';
import {createMusicFavorite, reportMusicFavoriteLoadFailure, type MusicFavoriteBinding, type MusicFavoriteSource} from './favorite-binding';
import {musicQueueItem} from './queue-facts';
const noSubscribe = () => () => {};
const noSnapshot = (): DetailSnapshot | null => null;

export function useMusicFavorite(context: WatchPlaybackContext | undefined, runtime: WebShellPlaybackRuntime, snapshot: WebPlaybackRuntimeSnapshot, open: boolean, reportError: (error: unknown) => void, isCurrent?: () => boolean) {
  const target = musicQueueItem(snapshot), itemId = target?.itemId, libraryId = target?.libraryId;
  const fence = snapshot.fence, at = fence ? fenceKey(fence) : '', authority = snapshot.authority, run = snapshot.playback.playbackRun;
  const source = useMemo<MusicFavoriteSource | null>(() => {
    if (!context || !open || !itemId || !libraryId || !at) return null;
    const current = runtime.getSnapshot();
    if (!current.fence || fenceKey(current.fence) !== at || current.authority !== authority || current.playback.playbackRun !== run) return null;
    return {context, runtime, authority, fence: current.fence, run, itemId, libraryId, isCurrent};
  }, [context, open, runtime, authority, at, run, itemId, libraryId, isCurrent]);
  const [owner, setOwner] = useState<MusicFavoriteBinding | null>(null);
  useLayoutEffect(() => {
    if (!source) {setOwner(null);return;}
    const next = createMusicFavorite(source);setOwner(next);
    // DetailService owns read/mutation errors in its snapshot; an aborted opening is silent.
    void next.load().catch(error => reportMusicFavoriteLoadFailure(next, error, reportError));
    return () => next.dispose();
  }, [source, reportError]);
  const state = useSyncExternalStore(owner?.subscribe ?? noSubscribe, owner?.getSnapshot ?? noSnapshot, noSnapshot);
  if (!owner || owner.source !== source || !state || !owner.current()) return;
  return {state, owner, offered: state.phase === 'ready' && state.data?.actions.some(action => action.id === 'favorite' && action.enabled) === true,
    selected: state.data?.personal.favorite === true, busy: state.pending.length > 0,
    failed: state.phase === 'error' || !!state.mutationError};
}
