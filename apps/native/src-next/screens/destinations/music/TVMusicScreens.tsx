import React, {useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View, type View as NativeView} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {cardText} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {artistReleaseGroups} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {Artwork, ArtistCircle, missingArt} from '../../../kit/artwork';
import {Button, Chip, Focusable} from '../../../kit/controls';
import {SkeletonBlock} from '../../../kit/states';
import {SquareCard} from '../../../kit/cards';
import {TrackRow, type TrackRowData, type TrackSelection} from '../../../kit/browse';
import {SelectionCircle} from '../../../kit/selection';
import type {NativeSelection} from '../../selection/native-selection';
import {browseType} from '../../../kit/browse/presentation';
import {FocusRegion} from '../../../kit/focus';
import {useTheme} from '../../../kit/theme';
import {TVShelf} from '../../../tv/layout/TVShelf';
import {TVArtFocus} from '../../../tv/layout/TVArtFocus';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../entry-actions';
import type {NativeTVMusicController, NativeTVMusicSnapshot, MusicAlbumRow} from './model';
import {MusicErrorView} from './MusicErrorView';
import {MusicSectionControls} from './MusicSectionControls';
import {MusicFormatBadges} from './MusicFormatBadges';
import {MusicScrollView, MusicWindow} from './MusicWindow';
import {TitleFrame, type TitleAction, type TitlePrimary} from '../../../kit/frames';

type Actions = Pick<NativeEntryActions, 'menu' | 'isMenuOpen'>;
type DetailRow = Readonly<{id: string; label: string; value: string}>;
const factLine = (text: string) => Object.freeze([{id: 'caption', text, role: 'value' as const}]);
const t = defaultI18n.t;

function DetailRows({rows, testID}: {rows: readonly DetailRow[]; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{width: 900, marginTop: 16}}>
    {rows.map((row, index) => <View key={row.id} style={{flexDirection: 'row', alignItems: 'baseline', gap: 24, paddingVertical: 14,
      borderTopWidth: index ? 1 : 0, borderColor: theme.colors.line}}>
      <Text style={[browseType(theme, 22, 600), {width: 220, color: theme.colors.textTertiary}]}>{row.label}</Text>
      <Text style={[browseType(theme, 22, 400), {flex: 1, color: theme.colors.text}]}>{row.value}</Text>
    </View>)}
  </View>;
}

function usePlayback(runtime: NativeShellRuntime) {
  const adapter = runtime.playback;
  const snapshot = useSyncExternalStore(adapter?.subscribe ?? (() => () => {}), adapter?.getSnapshot ?? (() => undefined));
  return snapshot;
}


function SectionHeading({title, detail, left, top, width}: {title: string; detail?: string; left?: number; top?: number; width?: number}) {
  const theme = useTheme();
  return <View style={{position: top === undefined ? 'relative' : 'absolute', left: top === undefined ? undefined : left, top: top === undefined ? undefined : top, width,
    flexDirection: 'row', alignItems: 'baseline', gap: 16, zIndex: 2}}>
    <Text style={[browseType(theme, 32, 700, -.019), {color: theme.colors.text}]}>{title}</Text>
    {detail ? <Text style={[browseType(theme, 22, 600), {color: theme.colors.textTertiary}]}>{detail}</Text> : null}
  </View>;
}

/** The TV album and artist pages' buttons for TitleFrame: Play, then Shuffle (labelled), Favorite, Add to playlist (an album,
 * where offered) and More, which anchors its menu on the drawn button the frame hands over. Test ids as before. */
function tvTitleButtons(controller: NativeTVMusicController, entry: ContentEntry, runtime: NativeShellRuntime, actions: Actions, more: React.MutableRefObject<NativeView | null>, favorite: boolean | undefined, onPlaylist?: () => void): {primary: TitlePrimary; buttons: TitleAction[]} {
  const run = (work: Promise<unknown>) => { void work.catch(runtime.reportError); };
  const id = encodeURIComponent(entry.id);
  return {primary: {icon: 'i-play', label: t('entry.play'), testID: `music-play-${id}`, onPress: () => run(controller.play())}, buttons: [
    {icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, testID: `music-shuffle-${id}`, onPress: () => run(controller.play(undefined, true))},
    {icon: favorite ? 'i-heart-f' : 'i-heart', label: t(favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), active: !!favorite, testID: `music-favorite-${id}`, onPress: () => run(controller.toggleFavorite())},
    ...(onPlaylist ? [{icon: 'i-listplus' as const, label: t('entry.addToPlaylist'), testID: `music-playlist-${id}`, onPress: onPlaylist}] : []),
    {icon: 'i-more', label: t('card.more', {title: entry.title}), testID: `music-more-${id}`, anchorRef: view => {more.current = view;}, onPress: () => actions.menu(entry, more)},
  ]};
}
/** TV leaves a page by the remote's Back; TitleFrame draws no Back disc on TV, so this is never pressed. */
const tvBack = {label: t('action.back'), onPress: () => {}};

function songRow(entry: ContentEntry, index: number, runtime: NativeShellRuntime, playingId: string | undefined): TrackRowData {
  const text = cardText({id: entry.id, kind: 'song', title: entry.displayTitle ?? entry.title, artist: entry.artist?.name, album: entry.album?.name, durationSeconds: entry.duration}, 'song-row', runtime.text);
  const artwork = runtime.artwork(entry.posterUrl);
  return Object.freeze({id: entry.id, text, number: entry.trackNumber ?? index + 1, playing: playingId === entry.id,
    // tv-more6 draws a filled heart on the row of a song this viewer favorited.
    ...(entry.favorite === true ? {favorite: true} : {}),
    artwork: artwork ? <Artwork source={artwork} width={56} height={56} style={{borderRadius: 8, overflow: 'hidden'}}/> : undefined});
}

/** `windowed` lists mount only the rows in view (LR03/LR04 TV): fixed 84-point rows inside the
 * page's MusicScrollView, the focused row pinned from its own focus event. A list inside a
 * region with a declared focus order (Top songs) stays whole, so that order never names an
 * unmounted row. */
function TrackList({songs, runtime, controller, actions, selection, group = 'songs', playingId, variant, style, focusPrefix, onTail, onFocusRow, onBlurRow, offset = 0, windowed = false}: {songs: readonly ContentEntry[]; runtime: NativeShellRuntime; controller: NativeTVMusicController; actions: Actions; selection?: NativeSelection; group?: string; playingId?: string; variant: 'album' | 'top-songs'; style?: React.ComponentProps<typeof View>['style']; focusPrefix?: string; onTail?: () => void; onFocusRow?: (entry: ContentEntry) => void; onBlurRow?: (entry: ContentEntry) => void; offset?: number; windowed?: boolean}) {
  useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  const [focused, setFocused] = useState<number>();
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const states = new Map(songs.map(song => [song.id, selection?.item(song, group)]));
  const select: TrackSelection = {active: [...states.values()].some(state => state?.active),
    isSelected: id => states.get(id)?.selected === true, onToggle: id => states.get(id)?.onToggle(), onEnter: id => states.get(id)?.onEnter(),
    renderControl: (row, selected) => <SelectionCircle selected={selected} selecting accessibilityLabel={row.text.title} variant="row" interactive={false}/>};
  const row = (song: ContentEntry, index: number) => <TrackRow key={song.id} row={songRow(song, offset + index, runtime, playingId)} variant={variant}
    focusId={focusPrefix ? `${focusPrefix}-${song.id}` : `music-${group}-${song.id}`} index={index} previousFocused={focused === index - 1} selection={select}
    onFocus={() => {setFocused(index); onFocusRow?.(song); if (index >= songs.length - 3) onTail?.();}}
    onBlur={() => {setFocused(current => current === index ? undefined : current); onBlurRow?.(song);}}
    onPlay={() => run(controller.play(song.id))} onMore={(_id, anchor) => actions.menu(song, anchor, states.get(song.id)?.onEnter)}/>;
  return <View style={style}>
    {windowed ? <MusicWindow items={songs} itemHeight={84} focusedId={focused === undefined ? undefined : songs[focused]?.id} renderItem={row}/> : songs.map(row)}
  </View>;
}

function AlbumCard({album, controller, runtime, actions, openEntry, selection, width = 200, caption, focusId, preferred = false, onFocus, onBlur}: {album: MusicAlbumRow; controller: NativeTVMusicController; runtime: NativeShellRuntime; actions: Actions; openEntry: (entry: ContentEntry) => void; selection?: NativeSelection; width?: number; caption: string; focusId?: string; preferred?: boolean; onFocus?: () => void; onBlur?: () => void}) {
  const anchor = useRef<NativeView>(null);
  const artwork = runtime.artwork(album.posterUrl);
  const entry=useMemo(()=>controller.releaseEntry(album),[album,controller]);
  // DESIGN: Select is in every card's hold-OK menu; a release card starts this page's selection.
  const card = entry ? selection?.item(entry) : undefined;
  return <SquareCard onFocus={onFocus} onBlur={onBlur} width={width} focusId={focusId} preferredFocus={preferred} title={album.title} caption={factLine(caption)} artwork={artwork} menuAnchorRef={anchor} selection={card}
    onNavigate={() => {if(entry&&controller.contains(entry))openEntry(entry);}} onMore={() => {if(entry)actions.menu(entry, anchor, card?.onEnter);}} moreExpanded={!!entry&&actions.isMenuOpen(entry)}/>;
}

/** tv-more6: the album page at ten feet. Header facts and track order are the server's; the queue is the album's own. */
export function TVAlbumScreen({controller, entry, runtime, actions, openEntry, onPlaylist, selection}: {selection?: NativeSelection; controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; openEntry: (entry: ContentEntry) => void; onPlaylist?: () => void}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playback = usePlayback(runtime), theme = useTheme();
  const playingId = playback?.playback.itemId;
  const art = runtime.artwork(entry.posterUrl);
  const minutes = snapshot.songDurationSeconds === undefined ? undefined : Math.max(1, Math.round(snapshot.songDurationSeconds / 60));
  const facts = [entry.artist?.name, entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false}),
    snapshot.songTotal === undefined ? undefined : minutes === undefined ? t('title.songCount', {count: snapshot.songTotal})
      : t('music.songCountAndDuration', {songs: snapshot.songTotal, minutes})].filter((value): value is string => !!value);
  // tv-more6: a multi-disc album draws a Disc chip row above the list; the chips are the album's
  // own discs, and the list shows the chosen one. A single-disc album draws no row.
  const discs = useMemo(() => [...new Set(snapshot.songs.flatMap(song => song.discNumber === undefined ? [] : [song.discNumber]))].sort((left, right) => left - right), [snapshot.songs]);
  const [disc, setDisc] = useState<number>();
  const chosen = disc !== undefined && discs.includes(disc) ? disc : discs[0];
  const songs = discs.length > 1 ? snapshot.songs.filter(song => (song.discNumber ?? discs[0]) === chosen) : snapshot.songs;
  const more = useRef<NativeView | null>(null), edge = {paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight};
  const {primary, buttons} = tvTitleButtons(controller, entry, runtime, actions, more, snapshot.favorite, onPlaylist);
  // tv-more6 on TitleFrame's TV form: the cover at the left and its words, Play and the round buttons; under them the disc
  // chips, the songs (windowed in the page's own MusicScrollView) and More like this.
  return <TitleFrame testID="native-tv-album" back={tvBack}
    scroller={(content, style, collapse) => <MusicScrollView onNearEnd={()=>{void controller.nextSongs(snapshot.songPage);}} contentContainerStyle={style} showsVerticalScrollIndicator={false} collapseAt={collapse.at} onCollapse={collapse.onChange}>{content}</MusicScrollView>}
    cover={{shape: 'square', node: (size: number) => <Artwork source={art} width={size} height={size} priority fallback={art ? undefined : missingArt({title: entry.displayTitle ?? entry.title, author: entry.artist?.name, shape: 'square', width: size, height: size})}/>}}
    kicker={{label: t('search.kind.album')}} title={entry.displayTitle ?? entry.title}
    metadata={<View style={{flexDirection: 'row', alignItems: 'center', gap: 26, flexWrap: 'wrap'}}>
      {entry.artist && <View style={{marginRight: -8}}><ArtistCircle covers={art ? [art] : []} initials={[...entry.artist.name.trim()][0] ?? ''} alt={entry.artist.name} size={44} visible/></View>}
      {facts.map((fact, index) => <Text key={`${fact}-${index}`} numberOfLines={1} style={[browseType(theme, 24, index === 0 ? 700 : 400), {flexShrink: index === 0 ? 1 : 0, color: index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
      <MusicFormatBadges badges={snapshot.albumDetails?.badges} size="tv"/>
    </View>}
    primary={primary} actions={buttons}>
    {snapshot.phase === 'error' && <View style={edge}><MusicErrorView testID={`music-album-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}}/></View>}
    <View style={edge}>
      {discs.length > 1 && <View style={{flexDirection: 'row', gap: 14, marginBottom: 14}}>{discs.map(value => <Chip key={value} focusId={`music-disc-${value}`} selected={chosen === value}
        accessibilityRole="radio" accessibilityState={{checked: chosen === value}} onPress={() => setDisc(value)}>{t('title.disc', {number: defaultI18n.number(value, {useGrouping: false})})}</Chip>)}</View>}
      <TrackList windowed selection={selection} songs={songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} variant="album" offset={snapshot.songPage.offset} onFocusRow={entry=>controller.focusSong(entry,snapshot.songPage)} onBlurRow={entry=>controller.blurSong(entry,snapshot.songPage)} onTail={()=>{void controller.nextSongs(snapshot.songPage);}}/>
      {/* One failure state: the page error above already offers Try again for the whole album. */}
      {snapshot.phase !== 'error' && <MusicSectionControls id="album-songs" section={snapshot.songPage} next={()=>{void controller.nextSongs(snapshot.songPage);}} previous={()=>{void controller.previousSongs(snapshot.songPage);}} retry={()=>{void controller.retrySongs(snapshot.songPage);}} refresh={()=>{void controller.refreshSongs(snapshot.songPage);}}/>}
    </View>
    <View style={[edge, {gap: 24}]}>
      <SectionHeading title={t('music.moreLikeThis')} detail={t(snapshot.similarBySound?'music.moreLikeThisBySound':'music.moreLikeThisHint')}/>
      {snapshot.similarError?<MusicErrorView testID={`music-similar-${entry.id}-error`} error={snapshot.similarError} onRetry={()=>{void controller.load();}}/>:null}
      <TVShelf items={snapshot.similar.filter(value => value.kind === 'album')} getKey={value => value.id} itemWidth={200} itemHeight={300} gap={32} paging={controller.similarPage(snapshot)}
            renderItem={(value, slot) => slot.waiting ? <SimilarPending focusId={slot.focusId} onFocus={slot.onFocus} onBlur={slot.onBlur} snapshot={snapshot} controller={controller} size={200}/> : <SquareCard width={200} focusId={slot.focusId} onFocus={slot.onFocus} onBlur={slot.onBlur} title={value.displayTitle ?? value.title}
              caption={factLine(value.artist?.name ?? '')} artwork={runtime.artwork(value.posterUrl)} selection={selection?.item(value)} onNavigate={()=>{if(controller.similarCurrent(snapshot,value))openEntry(value);}} onMore={()=>{if(controller.similarCurrent(snapshot,value))actions.menu(value, undefined, selection?.item(value)?.onEnter);}} moreExpanded={controller.similarCurrent(snapshot,value)&&actions.isMenuOpen(value)}/>}/>
    </View>
  </TitleFrame>;
}

/** tv-more8/9: the artist page, its circle cut from up to four covers, and the server's release lists. */
/** W-12: the artist page an album was opened from returns to that album's card on Back, not Play.
 * Keyed by the artist itself (library and id): Back opens a new navigation revision, so the
 * returning page has a new controller, and a key on the old one was never found. One entry per
 * artist, read once by the next page drawn for that artist. */
const artistReturn = new Map<string, string>();
const artistReturnKey = (entry: ContentEntry) => JSON.stringify([entry.libraryId ?? '', entry.id]);
export function TVArtistScreen({controller, entry, runtime, actions, openEntry, selection}: {selection?: NativeSelection; controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; openEntry: (entry: ContentEntry) => void}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playback = usePlayback(runtime), theme = useTheme();
  const playingId = playback?.playback.itemId;
  const covers = snapshot.albums.flatMap(album => {const source = runtime.artwork(album.posterUrl); return source ? [source] : [];});
  const artist = snapshot.artistDetails ?? entry;
  const years = artist.activeBeginYear === undefined ? undefined : artist.activeEndYear !== undefined ? `${artist.activeBeginYear}–${artist.activeEndYear}` : t('title.activeSince', {year: artist.activeBeginYear});
  const facts = [artist.genres?.[0] ?? entry.genres?.[0], artist.country, years,
    artist.albumCount!==undefined&&snapshot.songTotal!==undefined?t('music.onServer',{albums:artist.albumCount,songs:snapshot.songTotal}):undefined].filter((value): value is string => !!value);
  const releases = artistReleaseGroups(snapshot.albums);
  const albums = releases.find(group => group.id === 'albums')?.entries ?? [];
  const otherReleases = releases.filter(group => group.id !== 'albums');
  const [returning] = useState(() => {const key = artistReturnKey(entry), id = artistReturn.get(key); artistReturn.delete(key); return id;});
  const drawn = [...albums.map(album => `music-artist-album-${album.id}`), ...otherReleases.flatMap(group => group.entries.map(album => `music-release-${album.id}`))];
  const returnTo = returning && drawn.includes(returning) ? returning : undefined;
  const openFrom = (focusId: string) => (value: ContentEntry) => {artistReturn.set(artistReturnKey(entry), focusId); openEntry(value);};
  const allSongs = snapshot.allSongs;
  const detailFacts: readonly DetailRow[] = [
    ...(artist.country ? [{id: 'country', label: t('title.country'), value: artist.country}] : []),
    ...(years ? [{id: 'activeYears', label: t('title.activeYears'), value: years}] : []),
    {id: 'type', label: t('title.albumType'), value: artist.artistType ?? t('title.kind', {kind: 'artist'})},
  ];
  const more = useRef<NativeView | null>(null);
  const {primary, buttons} = tvTitleButtons(controller, entry, runtime, actions, more, snapshot.favorite);
  // tv-more8/9 on TitleFrame's TV form: the circle at the right and its words, Play and the round buttons (one focus region
  // whose Down is the first section; the page opens on Play, or on the album card it was left from: W-12); Top songs under
  // them with the albums column beside it, then the releases, Similar artists, Details and All songs.
  return <TitleFrame testID="native-tv-artist" back={tvBack} startFocus={returnTo}
    actionsRegion={{id: `music-play-${entry.id}.actions`, edges: {down: `music-artist-${entry.id}.${snapshot.songs.length ? 'songs' : albums.length ? 'albums' : 'rest'}`}}}
    scroller={(content, style, collapse) => <MusicScrollView onNearEnd={()=>{void controller.nextAllSongs(allSongs);}} contentContainerStyle={style} showsVerticalScrollIndicator={false} collapseAt={collapse.at} onCollapse={collapse.onChange}>{content}</MusicScrollView>}
    cover={{shape: 'circle', node: (size: number) => <ArtistCircle covers={covers} rings={[40, 80]} initials={[...entry.title.trim()][0] ?? ''} alt={entry.title} size={size} visible/>}}
    kicker={{label: t('search.kind.artist')}} title={entry.title}
    metadata={<View style={{flexDirection: 'row', alignItems: 'center', gap: 26, flexWrap: 'wrap'}}>
      {facts.map((fact, index) => <Text key={`${fact}-${index}`} style={[browseType(theme, 24, index === 0 ? 700 : 400), {color: index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
    </View>}
    primary={primary} actions={buttons}
    aside={<View style={{gap: 18}}>
      <SectionHeading title={t('music.albums')}/>
      {/* The albums column wraps two across, so Up, Down, Left and Right follow the drawn
          geometry; only Left is pinned, into the songs. */}
        <FocusRegion id={`music-artist-${entry.id}.albums`} entry={albums[0] ? `music-artist-album-${albums[0].id}` : undefined}
        order={albums.map(album => `music-artist-album-${album.id}`)} edges={{left: snapshot.songs.length ? `music-artist-${entry.id}.songs` : `music-play-${entry.id}.actions`, up: `music-play-${entry.id}.actions`, down: `music-artist-${entry.id}.rest`}}
        style={{flexDirection: 'row', flexWrap: 'wrap', gap: 32}}>
        {albums.map(album => <AlbumCard key={album.id} album={album} controller={controller} runtime={runtime} actions={actions} selection={selection} openEntry={openFrom(`music-artist-album-${album.id}`)} preferred={returnTo === `music-artist-album-${album.id}`} width={240} focusId={`music-artist-album-${album.id}`}
          onFocus={()=>{controller.focusRelease(album,snapshot.releases);if(album===albums.at(-1)||album===albums.at(-2))void controller.nextReleases(snapshot.releases);}} onBlur={()=>controller.blurRelease(album,snapshot.releases)} caption={album.year === undefined ? '' : defaultI18n.number(album.year, {useGrouping: false})}/>)}
      </FocusRegion>
    </View>}>
    <View style={{paddingLeft: theme.gutter, gap: 18}}>
      <SectionHeading title={t('music.topSongs')}/>
      {/* Top songs keeps explicit exits to Play, the available albums and the next section. */}
        <FocusRegion id={`music-artist-${entry.id}.songs`} entry={snapshot.songs[0] ? `music-artist-song-${snapshot.songs[0].id}` : undefined}
        order={snapshot.songs.map(song => `music-artist-song-${song.id}`)} orientation="vertical" edges={{up: `music-play-${entry.id}.actions`, down: `music-artist-${entry.id}.rest`, ...(albums.length ? {right: `music-artist-${entry.id}.albums`} : {})}}
        style={{marginLeft: -18}}>
        <TrackList selection={selection} songs={snapshot.songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} variant="top-songs" focusPrefix="music-artist-song"/>
      </FocusRegion>
    </View>
      <FocusRegion id={`music-artist-${entry.id}.rest`} edges={{up: snapshot.songs.length ? `music-artist-${entry.id}.songs` : `music-play-${entry.id}.actions`}} style={{paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, gap: 34}}>
      <MusicSectionControls id="artist-releases" section={snapshot.releases} next={()=>{void controller.nextReleases(snapshot.releases);}} previous={()=>{void controller.previousReleases(snapshot.releases);}} retry={()=>{void controller.retryReleases(snapshot.releases);}} refresh={()=>{void controller.refreshReleases(snapshot.releases);}}/>
      {otherReleases.map(group => <View key={group.id}>
        <SectionHeading title={t(group.label)}/>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 32, marginTop: 18}}>
          {group.entries.map(album => <AlbumCard key={album.id} album={album} controller={controller} runtime={runtime} actions={actions} selection={selection} openEntry={openFrom(`music-release-${album.id}`)} preferred={returnTo === `music-release-${album.id}`} width={240} focusId={`music-release-${album.id}`} onFocus={()=>{controller.focusRelease(album,snapshot.releases);if(album===group.entries.at(-1))void controller.nextReleases(snapshot.releases);}} onBlur={()=>controller.blurRelease(album,snapshot.releases)} caption={album.year === undefined ? '' : defaultI18n.number(album.year, {useGrouping: false})}/>)}
        </View>
      </View>)}
      {(snapshot.similar.length > 0 || !!snapshot.similarError) && <View>
        <SectionHeading title={t('music.similarArtists')} detail={snapshot.similarBySound?t('music.moreLikeThisBySound'):undefined}/>
        {snapshot.similarError?<MusicErrorView testID={`music-similar-${entry.id}-error`} error={snapshot.similarError} onRetry={()=>{void controller.load();}} style={{marginTop:24}}/>:null}
        <View style={{marginTop: 24}}>
          <TVShelf items={snapshot.similar.filter(value => value.kind === 'artist')} getKey={value => value.id} itemWidth={200} itemHeight={280} gap={40} paging={controller.similarPage(snapshot)} renderItem={(value, slot) => {
            if (slot.waiting) return <SimilarPending focusId={slot.focusId} onFocus={slot.onFocus} onBlur={slot.onBlur} snapshot={snapshot} controller={controller} size={200} round/>;
            const albumCovers = (value.artworkPaths ?? []).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];});
            return <Focusable focusId={slot.focusId} onFocus={slot.onFocus} onBlur={slot.onBlur} accessibilityRole="button" accessibilityLabel={value.displayTitle??value.title} focusKind="card" focusTreatment="none"
              onPress={()=>{if(controller.similarCurrent(snapshot,value))openEntry(value);}} style={{width:200}}>
              {({focused}) => <TVArtFocus focused={focused} width={200} faceSize={200}
                face={<ArtistCircle covers={albumCovers} initials={[...value.title.trim()][0] ?? ''} alt={value.title} size={200} visible/>}>
                <Text numberOfLines={1} style={[browseType(theme, 22, 700), {color: theme.colors.text, textAlign: 'center', marginTop: 12}]}>{value.displayTitle ?? value.title}</Text>
              </TVArtFocus>}
            </Focusable>;
          }}/>
        </View>
      </View>}
      <View>
        <SectionHeading title={t('title.details')}/>
        <DetailRows rows={detailFacts} testID={`music-artist-details-${encodeURIComponent(entry.id)}`}/>
        {artist.overview ? <View style={{marginTop: 22, width: 900}}>
          <Text style={[browseType(theme, 22, 400, 0), {color: theme.colors.textSecondary, lineHeight: 34}]}>{artist.overview}</Text>
          {artist.overviewSource ? <Text style={[browseType(theme, 19, 500), {color: theme.colors.textTertiary, marginTop: 8}]}>{t('title.fromSource', {source: artist.overviewSource})}</Text> : null}
        </View> : null}
        <View style={{marginTop: 28, flexDirection: 'row', alignItems: 'center', gap: 20}}>
          {allSongs.songs.length>0
            ? <SectionHeading title={t('music.allSongs')} detail={allSongs.total === undefined || allSongs.totalIsLowerBound ? undefined : t('title.songCount', {count: allSongs.total})}/>
            : <Button focusId="music-all-songs" variant="text" icon="i-music" disabled={allSongs.phase === 'loading'} onPress={() => {void controller.loadAllSongs();}}
              accessibilityLabel={t('music.allSongs')}>{t('music.allSongs')}</Button>}
        </View>
        {allSongs.phase === 'loading' && <Text style={[browseType(theme, 22, 600), {color: theme.colors.textSecondary, marginTop: 14}]}>{t('music.loading')}</Text>}
        {allSongs.songs.length>0 && <TrackList windowed selection={selection} group="all-songs" songs={allSongs.songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} variant="top-songs" offset={allSongs.offset} onFocusRow={entry=>controller.focusAllSong(entry,allSongs)} onBlurRow={entry=>controller.blurAllSong(entry,allSongs)} onTail={()=>{void controller.nextAllSongs(allSongs);}} style={{marginTop: 12, width: 1118}}/>}
        <MusicSectionControls id="artist-songs" section={allSongs} next={()=>{void controller.nextAllSongs(allSongs);}} previous={()=>{void controller.previousAllSongs(allSongs);}} retry={()=>{void controller.retryAllSongs(allSongs);}} refresh={()=>{void controller.refreshAllSongs(allSongs);}}/>
      </View>
    </FocusRegion>
  </TitleFrame>;
}

/** The similar row's next-page host (TV-005): reading it is asked for when it takes focus. */
function SimilarPending({focusId, onFocus, onBlur, snapshot, controller, size, round = false}: {focusId?: string; onFocus: () => void; onBlur: () => void; snapshot: NativeTVMusicSnapshot; controller: NativeTVMusicController; size: number; round?: boolean}) {
  const failed = !!snapshot.similarPaging?.failed;
  return <Focusable focusId={focusId} testID="music-similar-pending" focusTreatment="none" accessibilityLabel={defaultI18n.t('next.states.loading')}
    onFocus={() => {onFocus(); controller.similarPage(snapshot)?.requestNext();}} onBlur={onBlur} style={{width: size}}>
    <SkeletonBlock width={size} height={size} radius={round ? size / 2 : 12} shine={!failed}/>
  </Focusable>;
}
