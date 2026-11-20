import {useSyncExternalStore, type RefObject} from 'react';
import type {View} from 'react-native';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {cardText, type FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {TrackRowData} from '../../../kit/browse';
import type {StateRowProps} from '../../../kit/frames';
import type {CardSelection, PosterRowProps} from '../../../kit/layout';
import type {MusicSection} from '../../../kit/music/section';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {NativeTVMusicController} from '../../../screens/destinations/music/model';
import {usePlayingItem} from '../../../screens/destinations/music/use-playing-item';
import type {NativeSavedSelectionPort} from '../../../screens/saved/SavedScreen';

const t = defaultI18n.t;
const year = (value: number | undefined) => value === undefined ? undefined : defaultI18n.number(value, {useGrouping: false});

export type AlbumPageProps = Readonly<{
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Pick<NativeEntryActions, 'menu'>;
  selection?: NativeSavedSelectionPort; openEntry: (entry: ContentEntry) => void; onBack: () => void; download?: (entry: ContentEntry) => void;
}>;

/** What an album's page shows (phone-app frame 13), from the music controller the old album screen read: data only. */
export type AlbumView = Readonly<{
  title: string; artistName?: string; cover?: ArtworkSource; glow?: AmbientArtSource;
  /** The album's artist under the title, a link to their page (the old tablet page's link; frame 13). */
  byline?: Readonly<{text: string; onPress: () => void}>;
  facts: readonly FactPart[]; badges: ContentEntry['badges'];
  play: () => void; shuffle: () => void; download?: () => void; more: () => void;
  /** The read failed: Try again reads the album again (the old screen's error view). */
  state?: StateRowProps;
  songs: Readonly<{rows: readonly TrackRowData[]; select: (id: string) => CardSelection | undefined;
    onPlay: (id: string) => void; onMore: (id: string, anchor?: RefObject<View | null>) => void;
    /** Next, previous, Try again and Refresh for the server's pages of songs. */
    section: MusicSection<ContentEntry>; next: () => void; previous: () => void; retry: () => void; refresh: () => void;
    /** The scroller nears the end: the next page of songs. */
    onNearEnd: () => void}>;
  /** More like this: albums close to this one, with the server's reason. */
  similar?: PosterRowProps; similarState?: StateRowProps;
}>;

export function useAlbum({controller, entry, runtime, actions, selection, openEntry, download}: AlbumPageProps): AlbumView {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playingId = usePlayingItem(runtime);
  // A row's place in the selection redraws the rows.
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? zero);
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const title = entry.displayTitle ?? entry.title, page = snapshot.songPage;
  // The album's artist, from its details read when the list entry lacks it (the controller links them from either).
  const artistName = snapshot.albumDetails?.artist?.name ?? entry.artist?.name;
  const minutes = snapshot.songDurationSeconds === undefined ? undefined : Math.max(1, Math.round(snapshot.songDurationSeconds / 60));
  // The year, the first genre, and the songs with their length (frame 13: "2020  R&B/Soul  4 songs, 15 min").
  const facts: FactPart[] = [];
  const released = year(entry.year), genre = entry.genres?.[0];
  if (released) facts.push({id: 'year', text: released, role: 'identity'});
  if (genre) facts.push({id: 'genre', text: genre, role: 'context'});
  if (snapshot.songTotal !== undefined) facts.push({id: 'songs', role: 'context', text: minutes === undefined ? t('title.songCount', {count: snapshot.songTotal})
    : t('music.songCountAndDuration', {songs: snapshot.songTotal, minutes})});
  const byId = new Map(snapshot.songs.map(song => [song.id, song]));
  const song = (id: string) => byId.get(id);
  const albums = snapshot.similar.filter(value => value.kind === 'album' && value.libraryId), paging = controller.similarPage(snapshot);
  const retryRead = {label: t('action.tryAgain'), onPress: () => run(controller.load())};
  return {
    title, cover: runtime.artwork(entry.posterUrl), glow: runtime.ambient(entry.posterUrl ?? entry.backdropUrl), artistName,
    byline: artistName ? {text: artistName, onPress: () => {const artist = controller.artistEntry(); if (artist) openEntry(artist);}} : undefined,
    facts, badges: snapshot.albumDetails?.badges,
    play: () => run(controller.play()), shuffle: () => run(controller.play(undefined, true)),
    download: download ? () => download(entry) : undefined, more: () => actions.menu(entry),
    state: snapshot.phase === 'error' ? {kind: 'error', icon: 'i-warn', title: presentError(snapshot.error, 'detail').title, action: retryRead} : undefined,
    songs: {
      rows: snapshot.songs.map((value, index) => ({id: value.id, number: value.trackNumber ?? page.offset + index + 1, playing: playingId === value.id,
        text: cardText({id: value.id, kind: 'song', title: value.displayTitle ?? value.title, artist: value.artist?.name, album: value.album?.name, durationSeconds: value.duration}, 'song-row', runtime.text)})),
      select: id => {const value = song(id); return value ? selection?.item(value) : undefined;},
      onPlay: id => run(controller.play(id)),
      onMore: (id, anchor) => {const value = song(id); if (value) actions.menu(value, anchor, selection?.item(value)?.onEnter);},
      section: page, next: () => run(controller.nextSongs(page)), previous: () => run(controller.previousSongs(page)),
      retry: () => run(controller.retrySongs(page)), refresh: () => run(controller.refreshSongs(page)), onNearEnd: () => run(controller.nextSongs(page)),
    },
    // The old screen's row: only albums (an artist's similar artists are its own page's), each opened only while it is still
    // this page's row, paging on as it scrolls.
    similar: albums.length ? {title: t('next.controls.moreLikeThis'), subtitle: t(snapshot.similarBySound ? 'music.moreLikeThisBySound' : 'music.moreLikeThisHint'), shape: 'square',
      testID: `music-similar-${entry.id}`,
      items: albums.map(value => ({key: value.id, title: value.displayTitle ?? value.title, year: year(value.year), artwork: runtime.artwork(value.posterUrl), selection: selection?.item(value),
        onNavigate: () => {if (controller.similarCurrent(snapshot, value)) openEntry(value);},
        onMore: anchor => {if (controller.similarCurrent(snapshot, value)) actions.menu(value, anchor, selection?.item(value)?.onEnter);}})),
      ...(paging ? {paging: {hasMore: paging.hasMore, loading: paging.loading, failed: paging.failed, waitingId: paging.waitingId, fulfilled: paging.fulfilled,
        waitingItem: {key: paging.waitingId, title: '', onNavigate: () => {}}, requestNext: paging.requestNext}} : {})} : undefined,
    similarState: snapshot.similarError ? {kind: 'error', icon: 'i-warn', title: presentError(snapshot.similarError, 'detail').title, action: retryRead} : undefined,
  };
}

const noSubscribe = () => () => {};
const zero = () => 0;
