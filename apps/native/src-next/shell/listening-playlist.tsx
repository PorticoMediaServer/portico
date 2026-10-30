import React from 'react';
import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src';
import {createMusicPlaylistPicker} from '../screens/destinations/music/MusicPlaylistPicker';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {NativeShellRuntime} from './runtime';

/** The existing authority-local picker, borrowed by the current listening song.
 * A retained button never substitutes whatever happens to be playing now. */
export function createNativeListeningPlaylist(context: NativeShellBootstrapContext) {
  const picker = createMusicPlaylistPicker(context);
  let disposed = false;
  let opening: Readonly<{fence: PlaybackFence; revision: number; controller: AbortController}> | undefined;
  const song = (fence: PlaybackFence) => {
    if (disposed || !context.current() || fence.kind !== 'queue') return;
    const state = context.playback.getSnapshot(), view = state.queue?.view;
    if (!sameFence(state.fence, fence) || !state.dock.local?.expanded || state.dock.local.kind !== 'music'
      || state.playback.session?.id !== fence.sessionId || view?.queue.currentEntryId !== fence.entryId) return;
    const item = view.items.find(value => value.entryId === fence.entryId);
    return item?.kind === 'song' && item.itemId === fence.itemId ? item : undefined;
  };
  const retire = () => {
    const previous = opening;
    opening = undefined;
    previous?.controller.abort();
  };
  const check = () => {
    if (opening && (!song(opening.fence) || context.navigation.getSnapshot().revision !== opening.revision)) retire();
  };
  const off = [context.playback.subscribe(check), context.subscribeAuthority(check), context.navigation.subscribe(check)];
  return Object.freeze({
    canOpen: (fence: PlaybackFence) => !!song(fence),
    open: (fence: PlaybackFence) => {
      const item = song(fence);
      if (!item) return;
      retire();
      const at = Object.freeze({fence, revision: context.navigation.getSnapshot().revision, controller: new AbortController()});
      opening = at;
      picker.open(item.itemId, {signal: at.controller.signal, isCurrent: () => opening === at && !at.controller.signal.aborted
        && !!song(at.fence) && context.navigation.getSnapshot().revision === at.revision});
    },
    render: (runtime: NativeShellRuntime) => !disposed && context.current() ? picker.render(runtime) : null,
    dispose: () => {if (disposed) return; disposed = true; retire(); off.forEach(stop => stop()); picker.dispose();},
  });
}

export type NativeListeningPlaylist = ReturnType<typeof createNativeListeningPlaylist>;
