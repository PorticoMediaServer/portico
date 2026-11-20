import {useMemo, useSyncExternalStore} from 'react';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {artistReleaseGroups} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {StateRowProps} from '../../../kit/frames';
import type {PosterRowItem, PosterRowProps} from '../../../kit/layout';
import type {MusicSection} from '../../../kit/music/section';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {MusicAlbumRow, NativeTVMusicController} from '../../../screens/destinations/music/model';
import {usePlayingItem} from '../../../screens/destinations/music/use-playing-item';
import type {NativeSavedSelectionPort} from '../../../screens/saved/SavedScreen';
import {similarPaging, songList, type SongList} from './music-rows';

const t = defaultI18n.t;
const year = (value: number | undefined) => value === undefined ? undefined : defaultI18n.number(value, {useGrouping: false});

export type ArtistPageProps = Readonly<{
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Pick<NativeEntryActions, 'menu'>;
  selection?: NativeSavedSelectionPort; openEntry: (entry: ContentEntry) => void; onBack: () => void;
}>;

/** What an artist's page shows (phone-app frame 15b), from the music controller the old artist screen read: data only. */
export type ArtistView = Readonly<{
  title: string; initials: string;
  /** The artist's circle: their albums' covers (the old page's circle). */
  covers: readonly ArtworkSource[];
  /** The page wash: from their first album's cover, else the artist's own art. */
  glow?: AmbientArtSource;
  facts: readonly FactPart[];
  /** The longer line on TV (tv-more frame 8): the first genre, the country, the active years and "1 album and 4 songs on
   * your server", each only when the read has it. */
  factsTV: readonly FactPart[];
  play: () => void; shuffle: () => void; more: () => void;
  /** Whether this viewer favourites the artist, and the toggle (the old TV page's heart). */
  favorite: Readonly<{on: boolean; toggle: () => void}>;
  /** Country, active years and type, then what the server says about them and where that is from (the old TV page's Details). */
  details: Readonly<{rows: readonly Readonly<{label: string; values: readonly Readonly<{text: string}>[]}>[]; overview?: string; source?: string}>;
  /** The read failed: Try again reads the artist again (the old screen's error view). */
  state?: StateRowProps;
  /** Top songs, ranked (frame 15b: 1, 2, 3, the playing one with bars). */
  topSongs?: SongList & Readonly<{title: string}>;
  /** Their releases as rows of square covers, one per kind (Albums, Singles & EPs, Compilations, Appears on). */
  releases: readonly (PosterRowProps & Readonly<{group: string}>)[];
  /** Next, previous, Try again and Refresh for the server's pages of releases. */
  releasePages: Readonly<{section: MusicSection<MusicAlbumRow>; next: () => void; previous: () => void; retry: () => void; refresh: () => void}>;
  /** Similar artists as circles, with the server's reason. */
  similar?: PosterRowProps; similarState?: StateRowProps;
  /** Every song of theirs, read only when asked for (the old TV page's All songs) and paged by the server. */
  allSongs: Readonly<{list?: SongList; total?: string; loading: boolean; load: () => void; section: MusicSection<ContentEntry>;
    next: () => void; previous: () => void; retry: () => void; refresh: () => void; focus: (id: string) => void}>;
}>;

export function useArtist({controller, entry, runtime, actions, selection, openEntry}: ArtistPageProps): ArtistView {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playingId = usePlayingItem(runtime);
  // A row's or card's place in the selection redraws them.
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? zero);
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const groups = useMemo(() => artistReleaseGroups(snapshot.albums), [snapshot.albums]);
  const covers = useMemo(() => snapshot.albums.flatMap(album => {const source = runtime.artwork(album.posterUrl); return source ? [source] : [];}), [snapshot.albums, runtime]);
  const about = snapshot.artistDetails ?? entry, releases = snapshot.releases;
  // The albums, the songs and the first genre (frame 15b: "1 album  4 songs  R&B/Soul"), each only when the read has it.
  const facts: FactPart[] = [];
  if (about.albumCount !== undefined) facts.push({id: 'albums', text: t('title.albumCount', {count: about.albumCount}), role: 'context'});
  if (snapshot.songTotal) facts.push({id: 'songs', text: t('title.songCount', {count: snapshot.songTotal}), role: 'context'});
  if (entry.genres?.[0]) facts.push({id: 'genre', text: entry.genres[0], role: 'context'});
  const years = about.activeBeginYear === undefined ? undefined : about.activeEndYear !== undefined
    ? `${year(about.activeBeginYear)}–${year(about.activeEndYear)}` : t('title.activeSince', {year: year(about.activeBeginYear)});
  const genre = about.genres?.[0] ?? entry.genres?.[0];
  const factsTV: FactPart[] = [...(genre ? [{id: 'genre', text: genre, role: 'identity' as const}] : []),
    ...(about.country ? [{id: 'country', text: about.country, role: 'context' as const}] : []), ...(years ? [{id: 'years', text: years, role: 'context' as const}] : []),
    ...(about.albumCount !== undefined && snapshot.songTotal !== undefined ? [{id: 'count', text: t('music.onServer', {albums: about.albumCount, songs: snapshot.songTotal}), role: 'context' as const}] : [])];
  const all = snapshot.allSongs, byId = new Map(all.songs.map(song => [song.id, song]));
  const retryRead = {label: t('action.tryAgain'), onPress: () => run(controller.load())};
  const release = (album: MusicAlbumRow): PosterRowItem => {
    // A release opens and offers its menu only while it is still on this page (the old page's guard).
    const value = controller.releaseEntry(album), picked = value ? selection?.item(value) : undefined, offered = () => !!value && controller.contains(value);
    return {key: album.id, title: album.title, year: year(album.year), artwork: runtime.artwork(album.posterUrl), selection: picked,
      onNavigate: () => {if (value && offered()) openEntry(value);}, onMore: anchor => {if (value && offered()) actions.menu(value, anchor, picked?.onEnter);}};
  };
  const artists = snapshot.similar.filter(value => value.kind === 'artist' && value.libraryId);
  return {
    title: entry.displayTitle ?? entry.title, initials: [...entry.title.trim()][0] ?? '', covers,
    glow: runtime.ambient(snapshot.albums[0]?.posterUrl ?? entry.posterUrl ?? entry.backdropUrl), facts,
    play: () => run(controller.play()), shuffle: () => run(controller.play(undefined, true)), more: () => actions.menu(entry),
    factsTV, favorite: {on: snapshot.favorite, toggle: () => run(controller.toggleFavorite())},
    details: {rows: [...(about.country ? [{label: t('title.country'), values: [{text: about.country}]}] : []), ...(years ? [{label: t('title.activeYears'), values: [{text: years}]}] : []),
      {label: t('title.albumType'), values: [{text: about.artistType ?? t('title.kind', {kind: 'artist'})}]}],
      overview: about.overview || undefined, source: about.overview && about.overviewSource ? t('title.fromSource', {source: about.overviewSource}) : undefined},
    state: snapshot.phase === 'error' ? {kind: 'error', icon: 'i-warn', title: presentError(snapshot.error, 'detail').title, action: retryRead} : undefined,
    topSongs: snapshot.songs.length ? {title: t('music.topSongs'),
      ...songList({songs: snapshot.songs, number: (_value, index) => index + 1, playingId, controller, runtime, actions, selection})} : undefined,
    releases: groups.map(group => ({group: group.id, title: t(group.label), shape: 'square', testID: `music-releases-${group.id}`, items: group.entries.map(release)})),
    releasePages: {section: releases, next: () => run(controller.nextReleases(releases)), previous: () => run(controller.previousReleases(releases)),
      retry: () => run(controller.retryReleases(releases)), refresh: () => run(controller.refreshReleases(releases))},
    // Only artists (the similar feed for an artist), each opened only while it is still this page's row, paging on as it scrolls.
    similar: artists.length ? {title: t('music.similarArtists'), subtitle: t(snapshot.similarBySound ? 'music.moreLikeThisBySound' : 'music.moreLikeThisHint'), shape: 'circle',
      testID: `music-similar-${entry.id}`,
      items: artists.map(value => ({key: value.id, title: value.displayTitle ?? value.title, selection: selection?.item(value),
        covers: (value.artworkPaths ?? []).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];}),
        onNavigate: () => {if (controller.similarCurrent(snapshot, value)) openEntry(value);},
        onMore: anchor => {if (controller.similarCurrent(snapshot, value)) actions.menu(value, anchor, selection?.item(value)?.onEnter);}})),
      ...similarPaging(controller, snapshot)} : undefined,
    similarState: snapshot.similarError ? {kind: 'error', icon: 'i-warn', title: presentError(snapshot.similarError, 'detail').title, action: retryRead} : undefined,
    allSongs: {list: all.songs.length ? songList({songs: all.songs, number: (_value, index) => all.offset + index + 1, playingId, controller, runtime, actions, selection}) : undefined,
      total: all.total === undefined || all.totalIsLowerBound ? undefined : t('title.songCount', {count: all.total}), loading: all.phase === 'loading',
      load: () => run(controller.loadAllSongs()), section: all, next: () => run(controller.nextAllSongs(all)), previous: () => run(controller.previousAllSongs(all)),
      retry: () => run(controller.retryAllSongs(all)), refresh: () => run(controller.refreshAllSongs(all)),
      focus: id => {const value = byId.get(id); if (value) controller.focusAllSong(value, all);}},
  };
}

const noSubscribe = () => () => {};
const zero = () => 0;
