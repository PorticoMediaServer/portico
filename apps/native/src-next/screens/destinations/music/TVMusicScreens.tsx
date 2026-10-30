import React, {useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View, type View as NativeView} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {cardText} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {artistReleaseGroups} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {AmbientArt, Artwork, ArtistCircle, StandardBackdrop} from '../../../kit/artwork';
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

function Backdrop({runtime, path}: {runtime: NativeShellRuntime; path: string | undefined}) {
  const source = runtime.ambient(path);
  return <View pointerEvents="none" style={{position: 'absolute', inset: 0, backgroundColor: '#071018', overflow: 'hidden'}}>
    {/* No art: the one standard backdrop, never flat ink (Justin, 29 Oct). */}
    {source ? <AmbientArt source={source} treatment="music" style={{position: 'absolute', inset: 0}}/> : <StandardBackdrop/>}
  </View>;
}

function SectionHeading({title, detail, left, top, width}: {title: string; detail?: string; left?: number; top?: number; width?: number}) {
  const theme = useTheme();
  return <View style={{position: top === undefined ? 'relative' : 'absolute', left: top === undefined ? undefined : left, top: top === undefined ? undefined : top, width,
    flexDirection: 'row', alignItems: 'baseline', gap: 16, zIndex: 2}}>
    <Text style={[browseType(theme, 32, 700, -.019), {color: theme.colors.text}]}>{title}</Text>
    {detail ? <Text style={[browseType(theme, 22, 600), {color: theme.colors.textTertiary}]}>{detail}</Text> : null}
  </View>;
}

function ActionBar({controller, entry, runtime, actions, focusId, style, onPlaylist, entryFocus = true, down}: {controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; focusId: string; style: React.ComponentProps<typeof View>['style']; onPlaylist?: () => void; entryFocus?: boolean; down?: string}) {
  const play = useRef<NativeView>(null), more = useRef<NativeView>(null);
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const run = (work: Promise<unknown>) => { void work.catch(runtime.reportError); };
  const label = entry.kind === 'album' ? t('music.playAlbum', {album: entry.title}) : t('music.playArtist', {artist: entry.title});
  const shuffleLabel = entry.kind === 'album' ? t('title.shuffle') : t('music.shuffleArtist', {artist: entry.title});
  const controls = <>
    <Button ref={play} testID={`music-play-${encodeURIComponent(entry.id)}`} focusId={focusId} hasTVPreferredFocus={entryFocus} variant="primary" icon="i-play" onPress={() => run(controller.play())} accessibilityLabel={label}>{t('entry.play')}</Button>
    <Button testID={`music-shuffle-${encodeURIComponent(entry.id)}`} variant="glass" icon="i-shuffle" onPress={() => run(controller.play(undefined, true))} accessibilityLabel={shuffleLabel}>{t('title.shuffle')}</Button>
    <Button testID={`music-favorite-${encodeURIComponent(entry.id)}`} variant="glass" iconOnly icon={snapshot.favorite ? 'i-heart-f' : 'i-heart'} accessibilityLabel={t(snapshot.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites')}
      accessibilityState={{checked: snapshot.favorite}} onPress={() => run(controller.toggleFavorite())}/>
    {onPlaylist && <Button testID={`music-playlist-${encodeURIComponent(entry.id)}`} variant="glass" iconOnly icon="i-listplus" accessibilityLabel={t('entry.addToPlaylist')} onPress={onPlaylist}/>}
    <Button ref={more} testID={`music-more-${encodeURIComponent(entry.id)}`} variant="glass" iconOnly icon="i-more" accessibilityLabel={t('card.more', {title: entry.title})}
      accessibilityState={{expanded: actions.isMenuOpen(entry)}} onPress={() => actions.menu(entry, more)}/>
  </>;
  const layout = [{flexDirection: 'row' as const, alignItems: 'center' as const, gap: 18, zIndex: 3}, style];
  return down ? <FocusRegion id={`${focusId}.actions`} orientation="horizontal" entry={focusId} edges={{down}} style={layout}>{controls}</FocusRegion>
    : <View style={layout}>{controls}</View>;
}

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

function AlbumCard({album, controller, runtime, actions, openEntry, width = 200, caption, focusId, preferred = false, onFocus, onBlur}: {album: MusicAlbumRow; controller: NativeTVMusicController; runtime: NativeShellRuntime; actions: Actions; openEntry: (entry: ContentEntry) => void; width?: number; caption: string; focusId?: string; preferred?: boolean; onFocus?: () => void; onBlur?: () => void}) {
  const anchor = useRef<NativeView>(null);
  const artwork = runtime.artwork(album.posterUrl);
  const entry=useMemo(()=>controller.releaseEntry(album),[album,controller]);
  return <SquareCard onFocus={onFocus} onBlur={onBlur} width={width} focusId={focusId} hasTVPreferredFocus={preferred} title={album.title} caption={factLine(caption)} artwork={artwork} menuAnchorRef={anchor}
    onNavigate={() => {if(entry&&controller.contains(entry))openEntry(entry);}} onMore={() => {if(entry)actions.menu(entry, anchor);}} moreExpanded={!!entry&&actions.isMenuOpen(entry)}/>;
}

/** tv-more6: the album page at ten feet. Header facts and track order are the server's; the queue is the album's own. */
/** tv-more6's album title line height (120 pt, 0.98). */
const albumTitleLine = 117.6;
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
  // tv-more6 draws a one-line title. A title that wraps pushes the facts, the actions and the list
  // down by the second line, so no line box ever lies over the row below it.
  const [titleLines, setTitleLines] = useState(1);
  const drop = (titleLines - 1) * albumTitleLine;
  const moreTop=Math.max(896,520+drop+songs.length*84+(discs.length>1?76:0)+100);
  return <View testID="native-tv-album" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <Backdrop runtime={runtime} path={entry.posterUrl ?? entry.backdropUrl}/>
    <MusicScrollView onNearEnd={()=>{void controller.nextSongs(snapshot.songPage);}} contentContainerStyle={{paddingBottom: 64}} showsVerticalScrollIndicator={false}>
      <View style={{width: 1920, height: Math.max(1320,moreTop+424)}}>
        <View style={{position: 'absolute', left: 200, top: 150, width: 480, height: 480, borderRadius: 18, backgroundColor: theme.colors.raised, zIndex: 2, boxShadow: [{offsetX: 0, offsetY: 60, blurRadius: 120, spreadDistance: -40, color: 'rgba(0,0,0,.9)'}, {offsetX: 0, offsetY: 0, blurRadius: 0, spreadDistance: 1, color: 'rgba(237,245,250,.08)'}]}}><View style={{width: 480, height: 480, borderRadius: 18, overflow: 'hidden'}}>
          {art ? <Artwork source={art} width={480} height={480} priority/> : null}
        </View></View>
        <Text style={[browseType(theme, 22, 700), {position: 'absolute', left: 760, top: 150, color: theme.colors.textSecondary, zIndex: 2}]}>{t('search.kind.album')}</Text>
        <Text numberOfLines={2} onTextLayout={event => setTitleLines(Math.min(2, Math.max(1, event.nativeEvent.lines.length)))} style={[browseType(theme, 120, 800, -.045, albumTitleLine), {position: 'absolute', left: 760, top: 186, width: 1000, color: theme.colors.text, zIndex: 2}]}>{entry.displayTitle ?? entry.title}</Text>
        <View style={{position: 'absolute', left: 760, top: 336 + drop, maxWidth: 1000, flexDirection: 'row', alignItems: 'center', gap: 26, zIndex: 2}}>
          {entry.artist && <View style={{marginRight: -8}}><ArtistCircle covers={art ? [art] : []} initials={[...entry.artist.name.trim()][0] ?? ''} alt={entry.artist.name} size={44} visible/></View>}
          {facts.map((fact, index) => <Text key={`${fact}-${index}`} numberOfLines={1} style={[browseType(theme, 24, index === 0 ? 700 : 400), {flexShrink: index === 0 ? 1 : 0, color: index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
          <MusicFormatBadges badges={snapshot.albumDetails?.badges} size="tv"/>
        </View>
        <ActionBar controller={controller} entry={entry} runtime={runtime} actions={actions} focusId={`music-play-${entry.id}`} onPlaylist={onPlaylist} style={{position: 'absolute', left: 760, top: 410 + drop}}/>
        {snapshot.phase === 'error' && <MusicErrorView testID={`music-album-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}} style={{position: 'absolute', left: 760, top: 520 + drop, width: 1000, zIndex: 3}}/>}
        <View style={{position: 'absolute', left: 742, top: 520 + drop, width: 1082, zIndex: 2}}>
          {discs.length > 1 && <View style={{flexDirection: 'row', gap: 14, marginBottom: 14}}>{discs.map(value => <Chip key={value} focusId={`music-disc-${value}`} selected={chosen === value}
            accessibilityRole="radio" accessibilityState={{checked: chosen === value}} onPress={() => setDisc(value)}>{t('title.disc', {number: defaultI18n.number(value, {useGrouping: false})})}</Chip>)}</View>}
          <TrackList windowed selection={selection} songs={songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} variant="album" offset={snapshot.songPage.offset} onFocusRow={entry=>controller.focusSong(entry,snapshot.songPage)} onBlurRow={entry=>controller.blurSong(entry,snapshot.songPage)} onTail={()=>{void controller.nextSongs(snapshot.songPage);}}/>
          {/* One failure state: the page error above already offers Try again for the whole album. */}
          {snapshot.phase !== 'error' && <MusicSectionControls id="album-songs" section={snapshot.songPage} next={()=>{void controller.nextSongs(snapshot.songPage);}} previous={()=>{void controller.previousSongs(snapshot.songPage);}} retry={()=>{void controller.retrySongs(snapshot.songPage);}} refresh={()=>{void controller.refreshSongs(snapshot.songPage);}}/>}
        </View>
        <SectionHeading title={t('music.moreLikeThis')} detail={t(snapshot.similarBySound?'music.moreLikeThisBySound':'music.moreLikeThisHint')} left={200} top={moreTop}/>
        {snapshot.similarError?<MusicErrorView testID={`music-similar-${entry.id}-error`} error={snapshot.similarError} onRetry={()=>{void controller.load();}} style={{position:'absolute',left:200,top:moreTop+58,width:1000,zIndex:3}}/>:null}
        <View style={{position: 'absolute', left: 200, right: 96, top: moreTop+58, zIndex: 2}}>
          <TVShelf items={snapshot.similar.filter(value => value.kind === 'album')} getKey={value => value.id} itemWidth={200} itemHeight={300} gap={32} paging={controller.similarPage(snapshot)}
            renderItem={(value, slot) => slot.waiting ? <SimilarPending focusId={slot.focusId} onFocus={slot.onFocus} onBlur={slot.onBlur} snapshot={snapshot} controller={controller} size={200}/> : <SquareCard width={200} focusId={slot.focusId} onFocus={slot.onFocus} onBlur={slot.onBlur} title={value.displayTitle ?? value.title}
              caption={factLine(value.artist?.name ?? '')} artwork={runtime.artwork(value.posterUrl)} onNavigate={()=>{if(controller.similarCurrent(snapshot,value))openEntry(value);}} onMore={()=>{if(controller.similarCurrent(snapshot,value))actions.menu(value);}} moreExpanded={controller.similarCurrent(snapshot,value)&&actions.isMenuOpen(value)}/>}/>
        </View>
      </View>
    </MusicScrollView>
  </View>;
}

/** tv-more8/9: the artist page, its circle cut from up to four covers, and the server's release lists. */
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
  const allSongs = snapshot.allSongs;
  const detailFacts: readonly DetailRow[] = [
    ...(artist.country ? [{id: 'country', label: t('title.country'), value: artist.country}] : []),
    ...(years ? [{id: 'activeYears', label: t('title.activeYears'), value: years}] : []),
    {id: 'type', label: t('title.albumType'), value: artist.artistType ?? t('title.kind', {kind: 'artist'})},
  ];
  return <View testID="native-tv-artist" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <Backdrop runtime={runtime} path={snapshot.albums[0]?.posterUrl ?? entry.posterUrl ?? entry.backdropUrl}/>
    <MusicScrollView onNearEnd={()=>{void controller.nextAllSongs(allSongs);}} contentContainerStyle={{paddingBottom: 64}} showsVerticalScrollIndicator={false}>
      <View style={{width: 1920, height: Math.max(1040,672+Math.ceil(albums.length/2)*330)}}>
        <View style={{position: 'absolute', left: 1280, top: 150, zIndex: 2}}>
          <ArtistCircle covers={covers} rings={[40, 80]} initials={[...entry.title.trim()][0] ?? ''} alt={entry.title} size={360} visible/>
        </View>
        <Text style={[browseType(theme, 22, 700), {position: 'absolute', left: 200, top: 170, color: theme.colors.textSecondary, zIndex: 2}]}>{t('search.kind.artist')}</Text>
        <Text numberOfLines={2} style={[browseType(theme, 120, 800, -.045, 117.6), {position: 'absolute', left: 200, top: 206, width: 1000, color: theme.colors.text, zIndex: 2}]}>{entry.title}</Text>
        <View style={{position: 'absolute', left: 200, top: 352, flexDirection: 'row', alignItems: 'center', gap: 26, flexWrap: 'wrap', width: 940, zIndex: 2}}>
          {facts.map((fact, index) => <Text key={`${fact}-${index}`} style={[browseType(theme, 24, index === 0 ? 700 : 400), {color: index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
        </View>
        {/* The TV artist page opens on Play (Justin's audit ruling 31). */}
        <ActionBar controller={controller} entry={entry} runtime={runtime} actions={actions} focusId={`music-play-${entry.id}`} style={{position: 'absolute', left: 200, top: 420}}
          down={`music-artist-${entry.id}.${snapshot.songs.length ? 'songs' : albums.length ? 'albums' : 'rest'}`}/>
        <SectionHeading title={t('music.topSongs')} left={200} top={612}/>
        {/* Top songs keeps explicit exits to Play, the available albums and the next section. */}
        <FocusRegion id={`music-artist-${entry.id}.songs`} entry={snapshot.songs[0] ? `music-artist-song-${snapshot.songs[0].id}` : undefined}
          order={snapshot.songs.map(song => `music-artist-song-${song.id}`)} orientation="vertical" edges={{up: `music-play-${entry.id}.actions`, down: `music-artist-${entry.id}.rest`, ...(albums.length ? {right: `music-artist-${entry.id}.albums`} : {})}}
          style={{position: 'absolute', left: 182, top: 666, width: 900, zIndex: 2}}>
          <TrackList selection={selection} songs={snapshot.songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} variant="top-songs" focusPrefix="music-artist-song"/>
        </FocusRegion>
        <SectionHeading title={t('music.albums')} left={1240} top={612}/>
        {/* The albums column wraps two across, so Up, Down, Left and Right follow the drawn
            geometry; only Left is pinned, into the songs. */}
        <FocusRegion id={`music-artist-${entry.id}.albums`} entry={albums[0] ? `music-artist-album-${albums[0].id}` : undefined}
          order={albums.map(album => `music-artist-album-${album.id}`)} edges={{left: snapshot.songs.length ? `music-artist-${entry.id}.songs` : `music-play-${entry.id}.actions`, up: `music-play-${entry.id}.actions`, down: `music-artist-${entry.id}.rest`}}
          style={{position: 'absolute', left: 1240, top: 672, width: 584, flexDirection: 'row', flexWrap: 'wrap', gap: 32, zIndex: 2}}>
          {albums.map(album => <AlbumCard key={album.id} album={album} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry} width={240} focusId={`music-artist-album-${album.id}`}
            onFocus={()=>{controller.focusRelease(album,snapshot.releases);if(album===albums.at(-1)||album===albums.at(-2))void controller.nextReleases(snapshot.releases);}} onBlur={()=>controller.blurRelease(album,snapshot.releases)} caption={album.year === undefined ? '' : defaultI18n.number(album.year, {useGrouping: false})}/>)}
        </FocusRegion>
      </View>
      <FocusRegion id={`music-artist-${entry.id}.rest`} edges={{up: snapshot.songs.length ? `music-artist-${entry.id}.songs` : `music-play-${entry.id}.actions`}} style={{width: 1624, marginLeft: 200, paddingTop: 40, gap: 34}}>
        <MusicSectionControls id="artist-releases" section={snapshot.releases} next={()=>{void controller.nextReleases(snapshot.releases);}} previous={()=>{void controller.previousReleases(snapshot.releases);}} retry={()=>{void controller.retryReleases(snapshot.releases);}} refresh={()=>{void controller.refreshReleases(snapshot.releases);}}/>
        {otherReleases.map(group => <View key={group.id}>
          <SectionHeading title={t(group.label)}/>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 32, marginTop: 18}}>
            {group.entries.map(album => <AlbumCard key={album.id} album={album} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry} width={240} focusId={`music-release-${album.id}`} onFocus={()=>{controller.focusRelease(album,snapshot.releases);if(album===group.entries.at(-1))void controller.nextReleases(snapshot.releases);}} onBlur={()=>controller.blurRelease(album,snapshot.releases)} caption={album.year === undefined ? '' : defaultI18n.number(album.year, {useGrouping: false})}/>)}
          </View>
        </View>)}
        <View>
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
        </View>
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
    </MusicScrollView>
  </View>;
}

/** The similar row's next-page host (TV-005): reading it is asked for when it takes focus. */
function SimilarPending({focusId, onFocus, onBlur, snapshot, controller, size, round = false}: {focusId?: string; onFocus: () => void; onBlur: () => void; snapshot: NativeTVMusicSnapshot; controller: NativeTVMusicController; size: number; round?: boolean}) {
  const failed = !!snapshot.similarPaging?.failed;
  return <Focusable focusId={focusId} testID="music-similar-pending" focusTreatment="none" accessibilityLabel={defaultI18n.t('next.states.loading')}
    onFocus={() => {onFocus(); controller.similarPage(snapshot)?.requestNext();}} onBlur={onBlur} style={{width: size}}>
    <SkeletonBlock width={size} height={size} radius={round ? size / 2 : 12} shine={!failed}/>
  </Focusable>;
}
