import React, {useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, type NativeScrollEvent, type NativeSyntheticEvent, type View as NativeView} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {cardText} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {Artwork, ArtistCircle, missingArt} from '../../../kit/artwork';
import {Button, Chip, Focusable} from '../../../kit/controls';
import {CircleCard, SquareCard} from '../../../kit/cards';
import {Icon} from '../../../kit/Icon';
import {alpha, browseType} from '../../../kit/browse/presentation';
import {TrackNumber} from '../../../kit/browse';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../entry-actions';
import type {NativeTVMusicController, NativeTVMusicSnapshot, MusicAlbumRow} from './model';
import {MusicSectionControls} from './MusicSectionControls';
import {artistReleaseGroups} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {MusicErrorView} from './MusicErrorView';
import {MusicFormatBadges} from './MusicFormatBadges';
import {usePlayingItem} from './use-playing-item';
import {MusicScrollView, MusicWindow, MusicShelf} from './MusicWindow';
import type {NativeSavedSelectionPort} from '../../saved/SavedScreen';
import {SelectionCircle} from '../../../kit/selection';
import {TVRow} from '../../../kit/layout/TVRow';
import {AboutTable, PanelHeading, TitleFrame, type TitleAction} from '../../../kit/frames';

const t = defaultI18n.t;
const noSubscribe = () => () => {};
const noRevision = () => 0;
type Actions = Pick<NativeEntryActions, 'menu'>;

/** phone-app#13 and #15b, in pt. The two drawn phone music pages: an album with
 * its own tracks, and an artist with Top songs and its release list. */
export const phoneMusicGeometry = Object.freeze({
  albumTop: 104, albumCover: 232, albumCoverRadius: 12, albumTitle: 30, albumArtist: 16,
  meta: 13, actionHeight: 46, actionRadius: 10, actionGap: 10,
  trackHeight: 54, trackPaddingLeft: 20, trackPaddingRight: 12, trackGap: 14, trackNumberWidth: 18,
  trackTitle: 15.5, trackNumber: 14, trackDuration: 13.5, trackMoreWidth: 34,
  artistTop: 98, artistCircle: 164, artistTitle: 30, sectionLabel: 13.5, albumCard: 132,
});


/** The drawn meta line: the strong first fact, then quiet ones, centred. */
function Facts({facts, strongFirst = true}: {facts: readonly string[]; strongFirst?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: theme.platform === 'phone' ? 'center' : 'flex-start', columnGap: 12, rowGap: 6, marginTop: 8, paddingHorizontal: theme.platform === 'phone' ? 20 : 0}}>
    {facts.map((fact, index) => <Text key={`${fact}-${index}`} style={[browseType(theme, theme.platform === 'phone' ? phoneMusicGeometry.meta : 14.5, strongFirst && index === 0 ? 700 : 400),
      {color: strongFirst && index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
  </View>;
}

/** A still three-bar mark, the same drawn playing state the kit rows use. */

/** How many digits a list's longest track number has, so every row's number column is the same width. */
const digitsBySongs = new WeakMap<readonly ContentEntry[], Map<number, number>>();
function trackDigits(songs: readonly ContentEntry[], offset = 0): number {
  const known = digitsBySongs.get(songs) ?? new Map<number, number>(); digitsBySongs.set(songs, known);
  let digits = known.get(offset);
  if (digits === undefined) {digits = String(songs.reduce((most, song) => Math.max(most, song.trackNumber ?? 0), offset + songs.length)).length; known.set(offset, digits);}
  return digits;
}

/** One drawn album track: number (or the playing mark), title, duration, More. */
function SongLine({song, index, digits, runtime, controller, actions, playingId, selection}: {
  song: ContentEntry; index: number; digits?: number; runtime: NativeShellRuntime; controller: NativeTVMusicController; actions: Actions; playingId?: string; selection?: NativeSavedSelectionPort;
}) {
  const theme = useTheme(), anchor = useRef<NativeView>(null), picked = selection?.item(song);
  const title = song.displayTitle ?? song.title;
  const model = useMemo(() => cardText({id: song.id, kind: 'song', title, artist: song.artist?.name, album: song.album?.name, durationSeconds: song.duration}, 'song-row', runtime.text), [song, title, runtime.text]);
  const duration = model.detail.find(part => part.id === 'duration')?.text;
  const playing = playingId === song.id;
  return <Focusable ref={anchor} accessibilityRole={picked?.active ? 'checkbox' : 'button'} accessibilityState={{checked: picked?.active ? picked.selected : undefined}} accessibilityLabel={t(picked?.active ? 'card.select' : 'next.browse.native.play', {title})} focusKind="row" focusRadius={10}
    onLongPressAction={() => actions.menu(song, anchor, picked?.onEnter)}
    onPress={() => {if (picked?.active) picked.onToggle(); else void controller.play(song.id).catch(runtime.reportError);}} style={{height: phoneMusicGeometry.trackHeight, paddingLeft: phoneMusicGeometry.trackPaddingLeft,
      paddingRight: phoneMusicGeometry.trackPaddingRight, flexDirection: 'row', alignItems: 'center', gap: phoneMusicGeometry.trackGap}}>
    {picked?.active ? <SelectionCircle selected={picked.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title})}/> : null}
    <TrackNumber number={song.trackNumber ?? index + 1} digits={digits} playing={playing} width={phoneMusicGeometry.trackNumberWidth} size={phoneMusicGeometry.trackNumber}/>
    {/* A track title wraps to a second line before it is cut; two lines fit the fixed 54-point row (W4 F7). */}
    <Text numberOfLines={2} style={[browseType(theme, phoneMusicGeometry.trackTitle, 600, 0), {flex: 1, color: playing ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
    {duration ? <Text style={[browseType(theme, phoneMusicGeometry.trackDuration, 400), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{duration}</Text> : null}
    <Focusable accessibilityRole="button" accessibilityLabel={t('next.browse.native.moreFor', {title})} onPress={() => actions.menu(song, anchor, picked?.onEnter)} focusRadius={17}
      style={{width: phoneMusicGeometry.trackMoreWidth, height: phoneMusicGeometry.trackMoreWidth, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-more" size={20} color={theme.colors.textTertiary}/></Focusable>
  </Focusable>;
}

function SongLines({songs, runtime, controller, actions, playingId, selection, offset=0, onFocusRow, onBlurRow, onTail}: {songs: readonly ContentEntry[]; runtime: NativeShellRuntime; controller: NativeTVMusicController; actions: Actions; playingId?: string; selection?: NativeSavedSelectionPort; offset?: number; onFocusRow?: (entry:ContentEntry)=>void; onBlurRow?: (entry:ContentEntry)=>void; onTail?:()=>void}) {
  return <MusicWindow items={songs} itemHeight={phoneMusicGeometry.trackHeight} renderItem={(song, index) => <View onFocus={()=>{onFocusRow?.(song);if(index>=songs.length-10)onTail?.();}} onBlur={()=>onBlurRow?.(song)}><SongLine song={song} index={offset+index} digits={trackDigits(songs, offset)} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/></View>}/>;
}

function AlbumCard({album,runtime,actions,openEntry,original,current,controller,onFocus,onBlur,selection}: {selection?:NativeSavedSelectionPort;controller?:NativeTVMusicController;onFocus?:()=>void;onBlur?:()=>void;album:MusicAlbumRow;runtime:NativeShellRuntime;actions:Actions;openEntry:(entry:ContentEntry)=>void;original?:ContentEntry;current?:()=>boolean}) {
  const entry=original??controller?.releaseEntry(album), anchor=useRef<NativeView>(null), picked=entry?selection?.item(entry):undefined;
  const offered=()=>!!entry&&(current?current():!!controller?.contains(entry));
  return <SquareCard selection={picked} menuAnchorRef={anchor} onFocus={onFocus} onBlur={onBlur} width={phoneMusicGeometry.albumCard} title={album.title} caption={Object.freeze([{id: 'caption', text: album.year === undefined ? '' : defaultI18n.number(album.year, {useGrouping: false}), role: 'value' as const}])}
    artwork={runtime.artwork(album.posterUrl)} onNavigate={()=>{if(entry&&offered())openEntry(entry);}} onMore={()=>{if(entry&&offered())actions.menu(entry,anchor,picked?.onEnter);}}/>;
}

/** music-plus16c: the existing album recommendation row, with the server's reason. */
function SimilarAlbums({snapshot,controller,runtime,actions,openEntry,selection}:{snapshot:NativeTVMusicSnapshot;controller:NativeTVMusicController;runtime:NativeShellRuntime;actions:Actions;openEntry:(entry:ContentEntry)=>void;selection?:NativeSavedSelectionPort}){
  const theme=useTheme();
  // Only album cards belong to this row; an artist page's similar artists are
  // drawn by its own page, so an empty row is not given a heading.
  const cards=snapshot.similar.filter(value=>value.kind==='album'&&value.libraryId);
  if(!cards.length&&!snapshot.similarError)return null;
  return <View style={{marginTop:30,paddingHorizontal:theme.platform==='phone'?20:32}}>
    <Text style={[browseType(theme,19,700),{color:theme.colors.text,marginBottom:10}]}>{t('next.controls.moreLikeThis')}</Text>
    {cards.length?<Text style={[browseType(theme,13,500),{color:theme.colors.textSecondary,marginBottom:10}]}>{t(snapshot.similarBySound?'music.moreLikeThisBySound':'music.moreLikeThisHint')}</Text>:null}
    {snapshot.similarError?<MusicErrorView testID={`music-similar-${snapshot.entry?.id??'page'}-error`} error={snapshot.similarError} onRetry={()=>{void controller.load();}}/>:null}
    {cards.length?<TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap:14}} scrollEventThrottle={100} onScroll={similarNearEnd(controller,snapshot)}>
      {cards.map(value=>{const id=value.id,libraryId=value.libraryId!;return <AlbumCard key={id} album={{id,libraryId,title:value.title,year:value.year,posterUrl:value.posterUrl}} original={value}
        current={()=>controller.similarCurrent(snapshot,value)} selection={selection} runtime={runtime} actions={actions} openEntry={openEntry}/>;})}
    </ScrollView></TVRow>:null}
  </View>;
}

/** music-plus#8's artist row: the artist's own recommendation row. The controller's
 * similar feed for an artist is already filtered to artist entries of the same kind,
 * so this only draws the title, the server's reason and the returned artwork. */
function SimilarArtistCard({entry, snapshot, controller, runtime, actions, openEntry, selection}: {
  entry: ContentEntry; snapshot: NativeTVMusicSnapshot; controller: NativeTVMusicController; runtime: NativeShellRuntime;
  actions: Actions; openEntry: (entry: ContentEntry) => void; selection?: NativeSavedSelectionPort;
}) {
  const anchor = useRef<NativeView>(null), picked = selection?.item(entry);
  const covers = useMemo(() => (entry.artworkPaths ?? []).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];}), [entry.artworkPaths, runtime]);
  return <CircleCard width={phoneMusicGeometry.albumCard} title={entry.displayTitle ?? entry.title} menuAnchorRef={anchor}
    art={{covers, alt: entry.title}} selection={picked}
    onNavigate={() => {if (controller.similarCurrent(snapshot, entry)) openEntry(entry);}}
    onMore={() => {if (controller.similarCurrent(snapshot, entry)) actions.menu(entry, anchor, picked?.onEnter);}}/>;
}

function SimilarArtists({snapshot,controller,runtime,actions,openEntry,inset,gap,selection}:{snapshot:NativeTVMusicSnapshot;controller:NativeTVMusicController;runtime:NativeShellRuntime;actions:Actions;openEntry:(entry:ContentEntry)=>void;inset:number;gap:number;selection?:NativeSavedSelectionPort}){
  const theme=useTheme();
  const cards=snapshot.similar.filter(value=>value.kind==='artist'&&value.libraryId);
  if(!cards.length&&!snapshot.similarError)return null;
  return <View style={{marginTop:theme.platform==='phone'?14:28}}>
    <View style={{flexDirection:'row',alignItems:'baseline',gap:10,paddingHorizontal:inset,marginBottom:10}}>
      <Text accessibilityRole="header" style={[browseType(theme,19,700),{color:theme.colors.text}]}>{t('music.similarArtists')}</Text>
      {cards.length?<Text numberOfLines={1} style={[browseType(theme,13,500),{color:theme.colors.textSecondary,flexShrink:1}]}>{t(snapshot.similarBySound?'music.moreLikeThisBySound':'music.moreLikeThisHint')}</Text>:null}
    </View>
    {snapshot.similarError?<MusicErrorView testID={`music-similar-${snapshot.entry?.id??'page'}-error`} error={snapshot.similarError} onRetry={()=>{void controller.load();}} style={{marginHorizontal:inset}}/>:null}
    {cards.length?<TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap,paddingHorizontal:inset}} scrollEventThrottle={100} onScroll={similarNearEnd(controller,snapshot)}>
      {cards.map(entry => <SimilarArtistCard key={entry.id} entry={entry} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry} selection={selection}/>)}
    </ScrollView></TVRow>:null}
  </View>;
}

/** phone-app#13, tablet-play 11: the album page on TitleFrame. The canonical controller owns the server lists; the page hands
 * the frame its cover, words and buttons, and keeps its own scroller (the song window and paging near the end). */
export function PhoneAlbumScreen({controller, entry, runtime, actions, openEntry, onBack, download, onPlaylist, selection}: {
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; selection?: NativeSavedSelectionPort; openEntry: (entry: ContentEntry) => void; onBack: () => void; download?: (entry: ContentEntry) => void; onPlaylist?: () => void;
}) {
  const theme = useTheme(), snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playingId = usePlayingItem(runtime);
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? noRevision);
  const art = runtime.artwork(entry.posterUrl);
  const [tableWidth, setTableWidth] = useState(0), columns = songTableColumns(tableWidth, theme.textScale * theme.fontScale);
  const minutes = snapshot.songDurationSeconds === undefined ? undefined : Math.max(1, Math.round(snapshot.songDurationSeconds / 60));
  const facts = [
    entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false}),
    entry.genres?.[0],
    snapshot.songTotal === undefined ? undefined : minutes === undefined ? t('title.songCount', {count: snapshot.songTotal})
      : t('music.songCountAndDuration', {songs: snapshot.songTotal, minutes}),
  ].filter((value): value is string => !!value);
  const title = entry.displayTitle ?? entry.title, tablet = theme.platform !== 'phone';
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const date = (value: string | undefined) => value ? new Intl.DateTimeFormat(runtime.text.locale ?? 'en-US', {dateStyle: 'medium'}).format(new Date(value)) : undefined;
  const details = tablet ? [[t('title.year'), entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false})],
    [t('title.releaseDate'), date(entry.releaseDate)], [t('title.label'), entry.label], [t('title.albumType'), entry.albumType],
    [t('title.genres'), entry.genres?.join(', ')], [t('title.added'), date(entry.addedAt ?? undefined)]].filter(([, value]) => !!value) as [string, string][] : [];
  const artist = entry.artist?.name ? tablet
    ? <Focusable accessibilityRole="link" onPress={() => {const value = controller.artistEntry(); if (value) openEntry(value);}}
      style={{minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: 10}}>
      <ArtistCircle covers={art ? [art] : []} initials={entry.artist.name.split(/\s+/).slice(0, 2).map(word => [...word][0]).join('')} alt={entry.artist.name} size={28} visible/>
      <Text numberOfLines={1} style={[browseType(theme, 16, 700), {color: theme.colors.text}]}>{entry.artist.name}</Text>
    </Focusable>
    : <Text numberOfLines={1} style={[browseType(theme, phoneMusicGeometry.albumArtist, 700), {color: theme.colors.accentStrong}]}>{entry.artist.name}</Text> : null;
  // Each form keeps its own buttons: a phone Shuffle, Download (where offered) and More; a tablet Shuffle, Favorite, Add to
  // playlist and More. The frame lays them out (labels never cut: it wraps the round ones).
  // More opens the album's menu anchored on the drawn button the frame hands over.
  const moreButton = useRef<NativeView | null>(null);
  const more = {icon: 'i-more' as const, label: t('card.more', {title}), anchorRef: (view: NativeView | null) => {moreButton.current = view;}, onPress: () => actions.menu(entry, moreButton)};
  const buttons: TitleAction[] = tablet ? [
    {icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, onPress: () => run(controller.play(undefined, true))},
    {icon: snapshot.favorite ? 'i-heart-f' : 'i-heart', label: t(snapshot.favorite ? 'next.browse.native.unfavorite' : 'next.browse.native.favorite'), active: !!snapshot.favorite, onPress: () => run(controller.toggleFavorite())},
    ...(onPlaylist ? [{icon: 'i-list-plus' as const, label: t('next.selection.addToPlaylist'), onPress: onPlaylist}] : []),
    more,
  ] : [
    {icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, onPress: () => run(controller.play(undefined, true))},
    ...(download ? [{icon: 'i-dl' as const, label: t('next.cards.download'), onPress: () => download(entry)}] : []),
    more,
  ];
  const songs = tablet ? <View>
    {selection?.start && controller.selectionProjection() ? <View style={{alignItems: 'flex-end', marginBottom: 10}}><Chip icon="i-check" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip></View> : null}
    <SongTableHeader columns={columns} onWidth={setTableWidth}/>
    <MusicWindow items={snapshot.songs} itemHeight={60} renderItem={(song,index)=><View onFocus={()=>{controller.focusSong(song,snapshot.songPage);if(index>=snapshot.songs.length-10)void controller.nextSongs(snapshot.songPage);}} onBlur={()=>controller.blurSong(song,snapshot.songPage)}><TabletSongLine song={song} columns={columns} index={snapshot.songPage.offset+index} digits={trackDigits(snapshot.songs, snapshot.songPage.offset)} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/></View>}/>
  </View> : snapshot.songs.length ? <SongLines songs={snapshot.songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection} offset={snapshot.songPage.offset} onFocusRow={value=>controller.focusSong(value,snapshot.songPage)} onBlurRow={value=>controller.blurSong(value,snapshot.songPage)} onTail={()=>{void controller.nextSongs(snapshot.songPage);}}/> : null;
  return <TitleFrame testID={tablet ? 'native-tablet-album' : 'native-phone-album'} back={{label: t('action.back'), onPress: onBack, testID: tablet ? 'native-tablet-album-back' : 'native-phone-album-back'}}
    scroller={(content, style, collapse) => <MusicScrollView onNearEnd={()=>{void controller.nextSongs(snapshot.songPage);}} style={{flex: 1}} contentContainerStyle={style} showsVerticalScrollIndicator={false}
      collapseAt={collapse.at} onCollapse={collapse.onChange}>{content}</MusicScrollView>}
    cover={{shape: 'square', node: (size: number) => <Artwork source={art} width={size} height={size} priority fallback={art ? undefined : missingArt({title, author: entry.artist?.name, shape: 'square', width: size, height: size})}/>}}
    kicker={{label: t('next.browse.native.album')}} title={title}
    metadata={<View style={{alignItems: tablet ? 'flex-start' : 'center', gap: 6}}>
      {artist}
      {facts.length ? <Facts facts={facts}/> : null}
      <MusicFormatBadges badges={snapshot.albumDetails?.badges} size={tablet ? 'tablet' : 'phone'}/>
    </View>}
    primary={{icon: 'i-play', label: t('entry.play'), onPress: () => run(controller.play())}} actions={buttons}
    aside={details.length ? <View><PanelHeading title={t('title.details')}/><AboutTable rows={details.map(([label, value]) => ({label, values: [{text: value}]}))}/></View> : undefined}>
    {snapshot.phase === 'error' ? <MusicErrorView testID={`music-page-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}}/> : null}
    {songs}
    <MusicSectionControls id="album-songs" section={snapshot.songPage} next={()=>{void controller.nextSongs(snapshot.songPage);}} previous={()=>{void controller.previousSongs(snapshot.songPage);}} retry={()=>{void controller.retrySongs(snapshot.songPage);}} refresh={()=>{void controller.refreshSongs(snapshot.songPage);}}/>
    <SimilarAlbums selection={selection} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry}/>
  </TitleFrame>;
}

/** The tablet song table's secondary columns. The title keeps a real share of the row (at least 40 % and 160 pt, at the
 * system text size); when the row is short Plays gives way first, then Duration; the favourite and More always stay. */
export type SongColumns = Readonly<{plays: boolean; duration: boolean}>;
export function songTableColumns(width: number, scale = 1): SongColumns {
  if (width <= 0) return {plays: true, duration: true};
  const fixed = 16 + 36 * scale + 88, title = Math.max(160 * scale, width * .4), plays = 62 * scale, duration = 70 * scale;
  if (width - fixed - plays - duration >= title) return {plays: true, duration: true};
  return {plays: false, duration: width - fixed - duration >= title};
}

/** The one header of the tablet song table (album and artist pages), with the same columns as its rows. */
function SongTableHeader({columns, onWidth}: {columns: SongColumns; onWidth: (width: number) => void}) {
  const theme = useTheme(), scale = theme.textScale * theme.fontScale;
  return <View onLayout={event => onWidth(event.nativeEvent.layout.width)} style={{flexDirection: 'row', alignItems: 'center', minHeight: 42, paddingHorizontal: 8, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
    <Text style={[browseType(theme, 13, 600), {width: 36 * scale, color: theme.colors.textTertiary}]}>#</Text>
    <Text numberOfLines={1} style={[browseType(theme, 13, 600), {flex: 1, color: theme.colors.textTertiary}]}>{t('next.browse.native.title')}</Text>
    {columns.plays ? <Text numberOfLines={1} style={[browseType(theme, 13, 600), {width: 62 * scale, textAlign: 'right', color: theme.colors.textTertiary}]}>{t('next.browse.native.plays')}</Text> : null}
    {columns.duration ? <Text numberOfLines={1} style={[browseType(theme, 13, 600), {width: 70 * scale, textAlign: 'right', color: theme.colors.textTertiary}]}>{t('next.browse.duration')}</Text> : null}
    <View style={{width: 88}}/>
  </View>;
}

function TabletSongLine({song, index, digits, columns = {plays: true, duration: true}, runtime, controller, actions, playingId, selection}: {
  song: ContentEntry; index: number; digits?: number; columns?: SongColumns; runtime: NativeShellRuntime; controller: NativeTVMusicController; actions: Actions; playingId?: string; selection?: NativeSavedSelectionPort;
}) {
  const theme = useTheme(), title = song.displayTitle ?? song.title, playing = playingId === song.id, anchor = useRef<NativeView>(null), picked = selection?.item(song);
  const model = useMemo(() => cardText({id: song.id, kind: 'song', title, artist: song.artist?.name, album: song.album?.name, durationSeconds: song.duration}, 'song-row', runtime.text), [song, title, runtime.text]);
  const duration = model.detail.find(part => part.id === 'duration')?.text, scale = theme.textScale * theme.fontScale;
  return <Focusable ref={anchor} accessibilityRole={picked?.active ? 'checkbox' : 'button'} accessibilityState={{checked: picked?.active ? picked.selected : undefined}} accessibilityLabel={t(picked?.active ? 'card.select' : 'next.browse.native.play', {title})} focusKind="row" focusRadius={10}
    onLongPressAction={() => actions.menu(song, anchor, picked?.onEnter)} onPress={() => {if (picked?.active) picked.onToggle(); else void controller.play(song.id).catch(runtime.reportError);}}
    style={{height: 60, paddingHorizontal: 8, flexDirection: 'row', alignItems: 'center', borderRadius: 10, backgroundColor: playing ? alpha(theme.colors.text, .08) : undefined}}>
    {picked?.active ? <SelectionCircle selected={picked.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title})}/> : null}
    <TrackNumber number={song.trackNumber ?? index + 1} digits={digits} playing={playing} width={36 * scale} size={14}/>
    <Text numberOfLines={2} style={[browseType(theme, 16, 600), {flex: 1, color: playing ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
    {columns.plays ? song.plays !== undefined ? <Text numberOfLines={1} style={[browseType(theme, 14, 400), {width: 62 * scale, textAlign: 'right', color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{defaultI18n.number(song.plays)}</Text> : <View style={{width: 62 * scale}}/> : null}
    {columns.duration ? duration ? <Text numberOfLines={1} style={[browseType(theme, 14, 400), {width: 70 * scale, textAlign: 'right', color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{duration}</Text> : <View style={{width: 70 * scale}}/> : null}
    <Button variant="text" iconOnly icon={controller.songFavorite(song) ? 'i-heart-f' : 'i-heart'} iconColor={controller.songFavorite(song) ? theme.colors.accentStrong : theme.colors.textTertiary} iconSize={20}
      style={{width: 44, height: 44, flexShrink: 0}} accessibilityLabel={t(controller.songFavorite(song) ? 'entry.removeFromFavorites' : 'entry.addToFavorites')} accessibilityState={{checked: controller.songFavorite(song)}}
      disabled={controller.songFavoritePending(song)} onPress={() => {void controller.toggleSongFavorite(song).catch(runtime.reportError);}}/>
    <Focusable accessibilityRole="button" accessibilityLabel={t('next.browse.native.moreFor', {title})} onPress={() => actions.menu(song, anchor, picked?.onEnter)} focusRadius={17}
      style={{width: 44, height: 44, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-more" size={20} color={theme.colors.textTertiary}/></Focusable>
  </Focusable>;
}

/** phone-app#15b, tablet-play 11: the artist page on TitleFrame: the circle, Play and its buttons, Top songs, the releases by
 * kind and Similar artists. The page keeps its own scroller (the song window and the shelves' paging). */
export function PhoneArtistScreen({controller, entry, runtime, actions, openEntry, onBack, selection}: {
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; selection?: NativeSavedSelectionPort; openEntry: (entry: ContentEntry) => void; onBack: () => void;
}) {
  const theme = useTheme(), snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playingId = usePlayingItem(runtime);
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? noRevision);
  const [tableWidth, setTableWidth] = useState(0), columns = songTableColumns(tableWidth, theme.textScale * theme.fontScale);
  const releases=useMemo(()=>artistReleaseGroups(snapshot.albums),[snapshot.albums]);
  const covers = useMemo(() => snapshot.albums.flatMap(album => {const source = runtime.artwork(album.posterUrl); return source ? [source] : [];}), [snapshot.albums, runtime]);
  const facts = [
    (snapshot.artistDetails??entry).albumCount!==undefined?t('title.albumCount',{count:(snapshot.artistDetails??entry).albumCount!}):undefined,
    snapshot.songTotal ? t('title.songCount', {count: snapshot.songTotal}) : undefined,
    entry.genres?.[0],
  ].filter((value): value is string => !!value);
  const tablet = theme.platform !== 'phone', inset = tablet ? 32 : 20;
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const years = entry.activeBeginYear || entry.activeEndYear ? `${entry.activeBeginYear ?? ''}–${entry.activeEndYear ?? ''}` : undefined;
  const details = tablet ? [[t('title.genres'), entry.genres?.join(', ')], [t('title.country'), entry.country], [t('title.year'), years]].filter(([, value]) => !!value) as [string, string][] : [];
  // Each form keeps its own buttons: a phone Shuffle and More; a tablet Shuffle, Favorite and More. More's menu anchors on
  // the drawn button the frame hands over.
  const moreButton = useRef<NativeView | null>(null);
  const buttons: TitleAction[] = [
    {icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, onPress: () => run(controller.play(undefined, true))},
    ...(tablet ? [{icon: snapshot.favorite ? 'i-heart-f' as const : 'i-heart' as const, label: t(snapshot.favorite ? 'next.browse.native.unfavorite' : 'next.browse.native.favorite'), active: !!snapshot.favorite, onPress: () => run(controller.toggleFavorite())}] : []),
    {icon: 'i-more', label: t('card.more', {title: entry.title}), anchorRef: view => {moreButton.current = view;}, onPress: () => actions.menu(entry, moreButton)},
  ];
  return <TitleFrame testID={tablet ? 'native-tablet-artist' : 'native-phone-artist'} back={{label: t('action.back'), onPress: onBack, testID: tablet ? 'native-tablet-artist-back' : 'native-phone-artist-back'}}
    scroller={(content, style, collapse) => <MusicScrollView style={{flex: 1}} contentContainerStyle={style} showsVerticalScrollIndicator={false} collapseAt={collapse.at} onCollapse={collapse.onChange}>{content}</MusicScrollView>}
    cover={{shape: 'circle', node: (size: number) => <ArtistCircle covers={covers} rings={[3]} initials={[...entry.title.trim()][0] ?? ''} alt={entry.title} size={size} visible/>}}
    title={entry.title} metadata={facts.length ? <Facts facts={facts} strongFirst={false}/> : undefined}
    primary={{icon: 'i-play', label: t('entry.play'), onPress: () => run(controller.play())}} actions={buttons}
    aside={details.length ? <View><PanelHeading title={t('title.details')}/><AboutTable rows={details.map(([label, value]) => ({label, values: [{text: value}]}))}/></View> : undefined}>
    {snapshot.phase === 'error' ? <MusicErrorView testID={`music-page-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}} style={{marginHorizontal: inset}}/> : null}
    {tablet && selection?.start && controller.selectionProjection() ? <View style={{alignItems: 'flex-end', paddingHorizontal: inset}}><Chip icon="i-check" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip></View> : null}
    {snapshot.songs.length ? tablet ? <View style={{paddingHorizontal: inset}}>
      <Text style={[browseType(theme, 19, 700), {color: theme.colors.text, marginBottom: 8}]}>{t('music.topSongs')}</Text>
      <SongTableHeader columns={columns} onWidth={setTableWidth}/>
      <MusicWindow items={snapshot.songs} itemHeight={60} renderItem={(song, index) => <TabletSongLine song={song} columns={columns} index={index} digits={trackDigits(snapshot.songs)} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/>}/>
    </View> : <View>
      <Text style={[browseType(theme, phoneMusicGeometry.sectionLabel, 700), {color: theme.colors.textTertiary, marginHorizontal: inset, marginBottom: 8}]}>{t('music.topSongs')}</Text>
      <SongLines songs={snapshot.songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/>
    </View> : null}
    {releases.map(group=><View key={group.id}>
      <Text style={[browseType(theme,19,700),{color:theme.colors.text,marginHorizontal:inset,marginBottom:10}]}>{t(group.label)}</Text>
      <MusicShelf items={group.entries} width={phoneMusicGeometry.albumCard} gap={tablet ? 14 : 12} inset={inset} onNearEnd={()=>{void controller.nextReleases(snapshot.releases);}}
        renderItem={album=><AlbumCard album={album} controller={controller} selection={selection} runtime={runtime} actions={actions} openEntry={openEntry} onFocus={()=>controller.focusRelease(album,snapshot.releases)} onBlur={()=>controller.blurRelease(album,snapshot.releases)}/>}/>
    </View>)}
    <View style={{paddingHorizontal:inset}}><MusicSectionControls id="artist-releases" section={snapshot.releases} next={()=>{void controller.nextReleases(snapshot.releases);}} previous={()=>{void controller.previousReleases(snapshot.releases);}} retry={()=>{void controller.retryReleases(snapshot.releases);}} refresh={()=>{void controller.refreshReleases(snapshot.releases);}}/></View>
    <SimilarArtists selection={selection} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry} inset={inset} gap={tablet ? 14 : 12}/>
  </TitleFrame>;
}

/** TV-005/WEB-050 parity: a similar row reads its next page within a screen of its end. */
function similarNearEnd(controller:NativeTVMusicController,snapshot:NativeTVMusicSnapshot){
  return ({nativeEvent:{contentOffset,layoutMeasurement,contentSize}}:NativeSyntheticEvent<NativeScrollEvent>)=>{
    if(contentOffset.x+layoutMeasurement.width>=contentSize.width-layoutMeasurement.width)controller.similarPage(snapshot)?.requestNext();
  };
}
