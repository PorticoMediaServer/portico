import type {RefObject} from 'react';
import type {View} from 'react-native';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {cardText} from '../../../../../../packages/client-core/src/presentation/kit-model';
import type {TrackRowData} from '../../../kit/browse';
import type {CardSelection, PosterRowProps} from '../../../kit/layout';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {NativeTVMusicController, NativeTVMusicSnapshot} from '../../../screens/destinations/music/model';
import type {NativeSavedSelectionPort} from '../../../screens/saved/SavedScreen';

/** A music page's songs as `TrackWindow` takes them: the rows (numbered by `number`, the playing one marked) and what each
 * control does: a song plays, its ⋯ opens its menu with Select, and Select marks it. Shared by the album and artist pages. */
export type SongList = Readonly<{rows: readonly TrackRowData[]; select: (id: string) => CardSelection | undefined;
  onPlay: (id: string) => void; onMore: (id: string, anchor?: RefObject<View | null>) => void}>;

export function songList({songs, number, playingId, controller, runtime, actions, selection}: Readonly<{
  songs: readonly ContentEntry[]; number: (song: ContentEntry, index: number) => number; playingId?: string;
  controller: NativeTVMusicController; runtime: NativeShellRuntime; actions: Pick<NativeEntryActions, 'menu'>; selection?: NativeSavedSelectionPort;
}>): SongList {
  const byId = new Map(songs.map(song => [song.id, song]));
  return {
    rows: songs.map((value, index) => ({id: value.id, number: number(value, index), playing: playingId === value.id,
      text: cardText({id: value.id, kind: 'song', title: value.displayTitle ?? value.title, artist: value.artist?.name, album: value.album?.name, durationSeconds: value.duration}, 'song-row', runtime.text)})),
    select: id => {const value = byId.get(id); return value ? selection?.item(value) : undefined;},
    onPlay: id => {void controller.play(id).catch(runtime.reportError);},
    onMore: (id, anchor) => {const value = byId.get(id); if (value) actions.menu(value, anchor, selection?.item(value)?.onEnter);},
  };
}

/** The similar row's pages (the controller's `similarPage`) as the kit row's paging contract; the waiting host is a cursor. */
export function similarPaging(controller: NativeTVMusicController, snapshot: NativeTVMusicSnapshot): Pick<PosterRowProps, 'paging'> {
  const paging = controller.similarPage(snapshot);
  return paging ? {paging: {hasMore: paging.hasMore, loading: paging.loading, failed: paging.failed, waitingId: paging.waitingId, fulfilled: paging.fulfilled,
    waitingItem: {key: paging.waitingId, title: '', onNavigate: () => {}}, requestNext: paging.requestNext}} : {};
}
