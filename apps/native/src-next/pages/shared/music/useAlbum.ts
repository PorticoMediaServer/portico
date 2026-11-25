import {useMemo, useState, useSyncExternalStore} from 'react';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {AboutValue, StateRowProps, TitleAction} from '../../../kit/frames';
import {useTheme} from '../../../kit/theme';
import {isTabletForm} from '@portico/design';
import type {PosterRowProps} from '../../../kit/layout';
import type {MusicSection} from '../../../kit/music/section';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {NativeTVMusicController} from '../../../screens/destinations/music/model';
import {usePlayingItem} from '../../../screens/destinations/music/use-playing-item';
import type {NativeSavedSelectionPort} from '../saved/useSavedPage';
import {favoriteAction, similarPaging, songList, type SongList} from './music-rows';

const t = defaultI18n.t;
const year = (value: number | undefined) => value === undefined ? undefined : defaultI18n.number(value, {useGrouping: false});

export type AlbumPageProps = Readonly<{
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Pick<NativeEntryActions, 'menu'>;
  selection?: NativeSavedSelectionPort; openEntry: (entry: ContentEntry) => void; onBack: () => void; download?: (entry: ContentEntry) => void;
  /** Add the album to a playlist (the old tablet page's button; the route's playlist picker). */
  onPlaylist?: () => void;
}>;

/** What an album's page shows (phone-app frame 13), from the music controller the old album screen read: data only. */
export type AlbumView = Readonly<{
  title: string; artistName?: string; cover?: ArtworkSource; glow?: AmbientArtSource;
  /** "Album" over the title on a tablet (tablet-play 11, the old tablet page); a phone's frame 13 has none. */
  kicker?: Readonly<{label: string}>;
  /** The album's artist under the title, a link to their page (the old tablet page's link; frame 13). */
  byline?: Readonly<{text: string; onPress: () => void}>;
  facts: readonly FactPart[]; badges: ContentEntry['badges'];
  play: () => void; shuffle: () => void; download?: () => void; more: () => void;
  /** Whether this viewer favourites the album, and the toggle (the old TV album page's heart). */
  favorite: Readonly<{on: boolean; toggle: () => void}>;
  /** The same heart as a page action (`favoriteAction`). */
  favoriteAction: TitleAction;
  playlist?: () => void;
  /** A tablet's Details beside the songs (tablet-play 11, the old tablet album page): year, release date, label, type,
   * genres, added. None on a phone (frame 13 has none). */
  details?: readonly Readonly<{label: string; values: readonly AboutValue[]}>[];
  /** A multi-disc album's discs, the chosen one's songs listed (the old TV album page's Disc chips, tv-more frame 6). A
   * single-disc album has none. */
  discs?: readonly Readonly<{id: string; label: string; selected: boolean; onPress: () => void}>[];
  /** The read failed: Try again reads the album again (the old screen's error view). */
  state?: StateRowProps;
  songs: SongList & Readonly<{
    /** The focused song (a keyboard or switch on a phone): the server's pages keep it while it holds focus. */
    onFocusRow: (id: string) => void; onBlurRow: (id: string) => void;
    /** Next, previous, Try again and Refresh for the server's pages of songs. */
    section: MusicSection<ContentEntry>; next: () => void; previous: () => void; retry: () => void; refresh: () => void;
    /** The scroller nears the end: the next page of songs. */
    onNearEnd: () => void}>;
  /** More like this: albums close to this one, with the server's reason. */
  similar?: PosterRowProps; similarState?: StateRowProps;
}>;

export function useAlbum({controller, entry, runtime, actions, selection, openEntry, download, onPlaylist}: AlbumPageProps): AlbumView {
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
  // The album's own discs, in order; the list shows the chosen one (the first until one is chosen).
  const discNumbers = useMemo(() => [...new Set(snapshot.songs.flatMap(song => song.discNumber === undefined ? [] : [song.discNumber]))].sort((left, right) => left - right), [snapshot.songs]);
  const [disc, setDisc] = useState<number>();
  const chosen = disc !== undefined && discNumbers.includes(disc) ? disc : discNumbers[0];
  const listed = discNumbers.length > 1 ? snapshot.songs.filter(song => (song.discNumber ?? discNumbers[0]) === chosen) : snapshot.songs;
  const byId = new Map(snapshot.songs.map(song => [song.id, song]));
  const albums = snapshot.similar.filter(value => value.kind === 'album' && value.libraryId);
  const retryRead = {label: t('action.tryAgain'), onPress: () => run(controller.load())};
  const tablet = isTabletForm(useTheme().form);
  const date = (value: string | null | undefined) => {
    if (!value) return undefined;
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return undefined;
    return new Intl.DateTimeFormat(runtime.text.locale ?? 'en-US', {dateStyle: 'medium'}).format(parsed);
  };
  const details = tablet ? ([[t('title.year'), released], [t('title.releaseDate'), date(entry.releaseDate)], [t('title.label'), entry.label],
    [t('title.albumType'), entry.albumType], [t('title.genres'), entry.genres?.join(', ')], [t('title.added'), date(entry.addedAt)]] as const)
    .flatMap(([label, value]) => value ? [{label, values: [{text: value}]}] : []) : [];
  return {
    title, kicker: tablet ? {label: t('next.browse.native.album')} : undefined, cover: runtime.artwork(entry.posterUrl), glow: runtime.ambient(entry.posterUrl ?? entry.backdropUrl), artistName,
    byline: artistName ? {text: artistName, onPress: () => {const artist = controller.artistEntry(); if (artist) openEntry(artist);}} : undefined,
    facts, badges: snapshot.albumDetails?.badges,
    play: () => run(controller.play()), shuffle: () => run(controller.play(undefined, true)),
    download: download ? () => download(entry) : undefined, more: () => actions.menu(entry),
    favorite: {on: snapshot.favorite, toggle: () => run(controller.toggleFavorite())},
    favoriteAction: favoriteAction(snapshot.favorite, () => run(controller.toggleFavorite()), 'album-favorite', t), playlist: onPlaylist,
    details: details.length ? details : undefined,
    discs: discNumbers.length > 1 ? discNumbers.map(value => ({id: String(value), label: t('title.disc', {number: defaultI18n.number(value, {useGrouping: false})}),
      selected: value === chosen, onPress: () => setDisc(value)})) : undefined,
    state: snapshot.phase === 'error' ? {kind: 'error', icon: 'i-warn', title: presentError(snapshot.error, 'detail').title, action: retryRead} : undefined,
    songs: {
      // Numbered by the album's own track numbers, else their place in the album.
      ...songList({songs: listed, number: (value, index) => value.trackNumber ?? page.offset + index + 1, playingId, controller, runtime, actions, selection, table: tablet}),
      onFocusRow: id => {const value = byId.get(id); if (value) controller.focusSong(value, page);},
      onBlurRow: id => {const value = byId.get(id); if (value) controller.blurSong(value, page);},
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
      ...similarPaging(controller, snapshot)} : undefined,
    similarState: snapshot.similarError ? {kind: 'error', icon: 'i-warn', title: presentError(snapshot.similarError, 'detail').title, action: retryRead} : undefined,
  };
}

const noSubscribe = () => () => {};
const zero = () => 0;
