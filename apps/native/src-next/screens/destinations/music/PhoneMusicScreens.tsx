import React, {useId, useMemo, useRef, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, type NativeScrollEvent, type NativeSyntheticEvent, type View as NativeView} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {cardText} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {AmbientArt, Artwork, ArtistCircle, StandardBackdrop} from '../../../kit/artwork';
import {Button, Chip, Focusable} from '../../../kit/controls';
import {CircleCard, SquareCard} from '../../../kit/cards';
import {Icon} from '../../../kit/Icon';
import {alpha, browseType} from '../../../kit/browse/presentation';
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


/** The floating back/More header the phone music frames draw over the wash. */
function PhoneHeader({title, onBack, onMore, moreRef}: {title: string; onBack: () => void; onMore: () => void; moreRef: React.RefObject<NativeView | null>}) {
  const insets = useSafeAreaInsets();
  return <View style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: 10, paddingTop: insets.top, height: 50 + insets.top, paddingHorizontal: 10, flexDirection: 'row', alignItems: 'center'}}>
    <Button variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={onBack} style={{height: 44, width: 44}}/>
    <View style={{flex: 1}}/>
    <Button ref={moreRef} variant="icon" icon="i-more" iconOnly accessibilityLabel={t('card.more', {title})} onPress={onMore} style={{height: 44, width: 44}}/>
  </View>;
}

/** The cover's colours spread behind the page and fade down (phone-app#13 .amb-l). */
function Wash({runtime, path}: {runtime: NativeShellRuntime; path: string | undefined}) {
  const theme = useTheme(), source = runtime.ambient(path), gradient = useId();
  return <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: theme.platform === 'phone' ? 760 : 700, overflow: 'hidden'}}>
    {source ? <AmbientArt source={source} treatment="title" style={{position: 'absolute', inset: 0}}/> : null}
    <View style={{position: 'absolute', inset: 0, backgroundColor: alpha(theme.colors.ink, .45)}}/>
    <Svg width="100%" height="100%" style={{position: 'absolute', inset: 0}} accessible={false}>
      <Defs><LinearGradient id={gradient} x1="0" y1="0" x2="0" y2="1"><Stop offset="0.25" stopColor={theme.colors.ink} stopOpacity={0}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={1}/></LinearGradient></Defs>
      <Rect width="100%" height="100%" fill={`url(#${gradient})`}/>
    </Svg>
  </View>;
}

/** The drawn meta line: the strong first fact, then quiet ones, centred. */
function Facts({facts, strongFirst = true}: {facts: readonly string[]; strongFirst?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: theme.platform === 'phone' ? 'center' : 'flex-start', columnGap: 12, rowGap: 6, marginTop: 8, paddingHorizontal: theme.platform === 'phone' ? 20 : 0}}>
    {facts.map((fact, index) => <Text key={`${fact}-${index}`} style={[browseType(theme, theme.platform === 'phone' ? phoneMusicGeometry.meta : 14.5, strongFirst && index === 0 ? 700 : 400),
      {color: strongFirst && index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
  </View>;
}

/** Play and Shuffle are the drawn wide pair; the round slot carries the drawn More on an artist. */
function MusicActions({controller, entry, runtime, actions, moreRef, roundMore, download}: {
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; moreRef: React.RefObject<NativeView | null>; roundMore: boolean; download?: (entry: ContentEntry) => void;
}) {
  const theme = useTheme();
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const label = entry.kind === 'album' ? t('music.playAlbum', {album: entry.title}) : t('music.playArtist', {artist: entry.title});
  const shuffle = entry.kind === 'album' ? t('title.shuffle') : t('music.shuffleArtist', {artist: entry.title});
  const action = {height: phoneMusicGeometry.actionHeight, borderRadius: phoneMusicGeometry.actionRadius, paddingHorizontal: 20, gap: 8};
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: phoneMusicGeometry.actionGap}}>
    <Button style={[action, {flex: 1}]} icon="i-play" textStyle={browseType(theme, 15, 700)} accessibilityLabel={label} onPress={() => run(controller.play())}>{t('entry.play')}</Button>
    <Button variant="glass" style={[action, {flex: 1}]} icon="i-shuffle" textStyle={browseType(theme, 15, 700)} accessibilityLabel={shuffle} onPress={() => run(controller.play(undefined, true))}>{t('title.shuffle')}</Button>
    {download ? <Button variant="glass" iconOnly icon="i-dl" style={{height: phoneMusicGeometry.actionHeight, width: phoneMusicGeometry.actionHeight, borderRadius: phoneMusicGeometry.actionRadius}}
      accessibilityLabel={t('next.cards.download')} onPress={() => download(entry)}/> : roundMore ? <Button ref={moreRef} variant="glass" iconOnly icon="i-more" style={{height: phoneMusicGeometry.actionHeight, width: phoneMusicGeometry.actionHeight, borderRadius: phoneMusicGeometry.actionRadius}}
      accessibilityLabel={t('card.more', {title: entry.title})} onPress={() => actions.menu(entry, moreRef)}/> : null}
  </View>;
}

/** A still three-bar mark, the same drawn playing state the kit rows use. */
function LevelBars() {
  const theme = useTheme();
  return <View accessible={false} style={{height: 14, width: 18, flexDirection: 'row', alignItems: 'flex-end', justifyContent: 'center', gap: 2}}>
    {[8, 14, 10].map((height, index) => <View key={index} style={{height, width: 3, borderRadius: 1, backgroundColor: theme.colors.accentStrong}}/>)}
  </View>;
}

/** One drawn album track: number (or the playing mark), title, duration, More. */
function SongLine({song, index, runtime, controller, actions, playingId, selection}: {
  song: ContentEntry; index: number; runtime: NativeShellRuntime; controller: NativeTVMusicController; actions: Actions; playingId?: string; selection?: NativeSavedSelectionPort;
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
    <View style={{width: phoneMusicGeometry.trackNumberWidth, alignItems: 'center'}}>{playing ? <LevelBars/> : <Text style={[browseType(theme, phoneMusicGeometry.trackNumber, 600), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{song.trackNumber ?? index + 1}</Text>}</View>
    <Text numberOfLines={1} style={[browseType(theme, phoneMusicGeometry.trackTitle, 600, 0), {flex: 1, color: playing ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
    {duration ? <Text style={[browseType(theme, phoneMusicGeometry.trackDuration, 400), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{duration}</Text> : null}
    <Focusable accessibilityRole="button" accessibilityLabel={t('next.browse.native.moreFor', {title})} onPress={() => actions.menu(song, anchor, picked?.onEnter)} focusRadius={17}
      style={{width: phoneMusicGeometry.trackMoreWidth, height: phoneMusicGeometry.trackMoreWidth, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-more" size={20} color={theme.colors.textTertiary}/></Focusable>
  </Focusable>;
}

function SongLines({songs, runtime, controller, actions, playingId, selection, offset=0, onFocusRow, onBlurRow, onTail}: {songs: readonly ContentEntry[]; runtime: NativeShellRuntime; controller: NativeTVMusicController; actions: Actions; playingId?: string; selection?: NativeSavedSelectionPort; offset?: number; onFocusRow?: (entry:ContentEntry)=>void; onBlurRow?: (entry:ContentEntry)=>void; onTail?:()=>void}) {
  return <MusicWindow items={songs} itemHeight={phoneMusicGeometry.trackHeight} renderItem={(song, index) => <View onFocus={()=>{onFocusRow?.(song);if(index>=songs.length-10)onTail?.();}} onBlur={()=>onBlurRow?.(song)}><SongLine song={song} index={offset+index} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/></View>}/>;
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
    {cards.length?<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap:14}} scrollEventThrottle={100} onScroll={similarNearEnd(controller,snapshot)}>
      {cards.map(value=>{const id=value.id,libraryId=value.libraryId!;return <AlbumCard key={id} album={{id,libraryId,title:value.title,year:value.year,posterUrl:value.posterUrl}} original={value}
        current={()=>controller.similarCurrent(snapshot,value)} selection={selection} runtime={runtime} actions={actions} openEntry={openEntry}/>;})}
    </ScrollView>:null}
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
    art={{covers, initials: [...entry.title.trim()][0] ?? '', alt: entry.title}} selection={picked}
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
    {cards.length?<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap,paddingHorizontal:inset}} scrollEventThrottle={100} onScroll={similarNearEnd(controller,snapshot)}>
      {cards.map(entry => <SimilarArtistCard key={entry.id} entry={entry} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry} selection={selection}/>)}
    </ScrollView>:null}
  </View>;
}

/** phone-app#13: the album body. The canonical controller owns the server lists;
 * this file only draws them at phone size. */
export function PhoneAlbumScreen({controller, entry, runtime, actions, openEntry, onBack, download, onPlaylist, selection}: {
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; selection?: NativeSavedSelectionPort; openEntry: (entry: ContentEntry) => void; onBack: () => void; download?: (entry: ContentEntry) => void; onPlaylist?: () => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playingId = usePlayingItem(runtime);
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? noRevision);
  const art = runtime.artwork(entry.posterUrl), more = useRef<NativeView>(null);
  const minutes = snapshot.songDurationSeconds === undefined ? undefined : Math.max(1, Math.round(snapshot.songDurationSeconds / 60));
  const facts = [
    entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false}),
    entry.genres?.[0],
    snapshot.songTotal === undefined ? undefined : minutes === undefined ? t('title.songCount', {count: snapshot.songTotal})
      : t('music.songCountAndDuration', {songs: snapshot.songTotal, minutes}),
  ].filter((value): value is string => !!value);
  const title = entry.displayTitle ?? entry.title, tablet = theme.platform !== 'phone';
  if (tablet) {
    const date = (value: string | undefined) => value ? new Intl.DateTimeFormat(runtime.text.locale ?? 'en-US', {dateStyle: 'medium'}).format(new Date(value)) : undefined;
    const details = [[t('title.year'), entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false})],
      [t('title.releaseDate'), date(entry.releaseDate)], [t('title.label'), entry.label], [t('title.albumType'), entry.albumType],
      [t('title.genres'), entry.genres?.join(', ')], [t('title.added'), date(entry.addedAt ?? undefined)]].filter(([, value]) => !!value) as [string, string][];
    const round = {height: 52, width: 52, borderRadius: phoneMusicGeometry.actionRadius};
    return <View testID="native-tablet-album" style={{flex: 1}}><StandardBackdrop/>
      <Wash runtime={runtime} path={entry.posterUrl ?? entry.backdropUrl}/>
      <MusicScrollView onNearEnd={()=>{void controller.nextSongs(snapshot.songPage);}} contentContainerStyle={{paddingTop: 70, paddingBottom: 100}} showsVerticalScrollIndicator={false}>
        <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: 36, paddingHorizontal: 32}}>
          <View style={{width: 260, height: 260, borderRadius: 12, overflow: 'hidden', backgroundColor: theme.colors.raised, shadowColor: theme.colors.shadow, shadowOffset: {width: 0, height: 30}, shadowOpacity: .9, shadowRadius: 30}}>
            {art ? <Artwork source={art} width={260} height={260} priority/> : null}
          </View>
          <View style={{flex: 1, minWidth: 0}}>
            <Text style={[browseType(theme, 14.5, 600), {color: theme.colors.textSecondary, marginBottom: 8}]}>{t('next.browse.native.album')}</Text>
            <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, 80, 800, -.045, 78.4), {color: theme.colors.text, marginBottom: 16}]}>{title}</Text>
            {entry.artist?.name ? <Focusable accessibilityRole="link" onPress={() => {const artist = controller.artistEntry(); if (artist) openEntry(artist);}}
              style={{minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: 10, alignSelf: 'flex-start'}}>
              <ArtistCircle covers={art ? [art] : []} initials={entry.artist.name.split(/\s+/).slice(0, 2).map(word => [...word][0]).join('')} alt={entry.artist.name} size={28} visible/>
              <Text numberOfLines={1} style={[browseType(theme, 16, 700), {color: theme.colors.text}]}>{entry.artist.name}</Text>
            </Focusable> : null}
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, flexWrap: 'wrap'}}>
              {facts.length ? <Facts facts={facts}/> : null}
              <MusicFormatBadges badges={snapshot.albumDetails?.badges} size="tablet"/>
            </View>
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginTop: 20}}>
              <Button style={{height: 52, borderRadius: phoneMusicGeometry.actionRadius, paddingHorizontal: 24}} icon="i-play" textStyle={browseType(theme, 16, 700)} onPress={() => {void controller.play().catch(runtime.reportError);}}>{t('entry.play')}</Button>
              <Button variant="glass" style={{height: 52, borderRadius: phoneMusicGeometry.actionRadius, paddingHorizontal: 24}} icon="i-shuffle" textStyle={browseType(theme, 16, 700)} onPress={() => {void controller.play(undefined, true).catch(runtime.reportError);}}>{t('title.shuffle')}</Button>
              <Button variant="glass" iconOnly icon={snapshot.favorite ? 'i-heart-f' : 'i-heart'} style={round} accessibilityLabel={t(snapshot.favorite ? 'next.browse.native.unfavorite' : 'next.browse.native.favorite')} onPress={() => {void controller.toggleFavorite().catch(runtime.reportError);}}/>
              {onPlaylist ? <Button variant="glass" iconOnly icon="i-list-plus" style={round} accessibilityLabel={t('next.selection.addToPlaylist')} onPress={onPlaylist}/> : null}
              <Button ref={more} variant="glass" iconOnly icon="i-more" style={round} accessibilityLabel={t('card.more', {title})} onPress={() => actions.menu(entry, more)}/>
            </View>
          </View>
        </View>
        {snapshot.phase === 'error' ? <MusicErrorView testID={`music-page-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}} style={{marginHorizontal: 32, marginTop: 24}}/> : null}
        <View style={{flexDirection: 'row', gap: 40, paddingHorizontal: 32, marginTop: 34}}>
          <View style={{flex: 1, minWidth: 0}}>
            {selection?.start && controller.selectionProjection() ? <View style={{alignItems: 'flex-end', marginBottom: 10}}><Chip icon="i-check" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip></View> : null}
            <View style={{flexDirection: 'row', alignItems: 'center', height: 42, paddingHorizontal: 8, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
              <Text style={[browseType(theme, 13, 600), {width: 36, color: theme.colors.textTertiary}]}>#</Text>
              <Text style={[browseType(theme, 13, 600), {flex: 1, color: theme.colors.textTertiary}]}>{t('next.browse.native.title')}</Text>
              <Text style={[browseType(theme, 13, 600), {width: 62, textAlign: 'right', color: theme.colors.textTertiary}]}>{t('next.browse.native.plays')}</Text>
              <Text style={[browseType(theme, 13, 600), {width: 70, textAlign: 'right', color: theme.colors.textTertiary}]}>{t('next.browse.duration')}</Text>
              <View style={{width: 88}}/>
            </View>
            <MusicWindow items={snapshot.songs} itemHeight={60} renderItem={(song,index)=><View onFocus={()=>{controller.focusSong(song,snapshot.songPage);if(index>=snapshot.songs.length-10)void controller.nextSongs(snapshot.songPage);}} onBlur={()=>controller.blurSong(song,snapshot.songPage)}><TabletSongLine song={song} index={snapshot.songPage.offset+index} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/></View>}/>
            <MusicSectionControls id="album-songs" section={snapshot.songPage} next={()=>{void controller.nextSongs(snapshot.songPage);}} previous={()=>{void controller.previousSongs(snapshot.songPage);}} retry={()=>{void controller.retrySongs(snapshot.songPage);}} refresh={()=>{void controller.refreshSongs(snapshot.songPage);}}/>
          </View>
          {details.length ? <View style={{width: 250}}>
            <Text style={[browseType(theme, 19, 700), {color: theme.colors.text, marginBottom: 8}]}>{t('title.details')}</Text>
            {details.map(([label, value]) => <View key={label} style={{flexDirection: 'row', gap: 12, paddingVertical: 8, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
              <Text style={[browseType(theme, 14.5, 400), {width: 88, color: theme.colors.textTertiary}]}>{label}</Text><Text style={[browseType(theme, 14.5, 500), {flex: 1, color: theme.colors.text}]}>{value}</Text></View>)}
          </View> : null}
        </View>
        <SimilarAlbums selection={selection} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry}/>
      </MusicScrollView>
      <View pointerEvents="box-none" style={{position: 'absolute', top: insets.top + 8, left: 16, zIndex: 15}}>
        <Button testID="native-tablet-album-back" variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={onBack} style={{height: 44, width: 44}}/>
      </View>
    </View>;
  }
  return <View testID="native-phone-album" style={{flex: 1}}><StandardBackdrop/>
    <Wash runtime={runtime} path={entry.posterUrl ?? entry.backdropUrl}/>
    <MusicScrollView onNearEnd={()=>{void controller.nextSongs(snapshot.songPage);}} contentContainerStyle={{paddingTop: Math.max(phoneMusicGeometry.albumTop, insets.top + 56), paddingBottom: 40}} showsVerticalScrollIndicator={false}>
      <View style={{alignItems: 'center', paddingHorizontal: 20}}>
        <View style={{width: phoneMusicGeometry.albumCover, height: phoneMusicGeometry.albumCover, borderRadius: phoneMusicGeometry.albumCoverRadius, overflow: 'hidden', backgroundColor: theme.colors.raised,
          shadowColor: theme.colors.shadow, shadowOffset: {width: 0, height: 30}, shadowOpacity: .9, shadowRadius: 30}}>
          {art ? <Artwork source={art} width={phoneMusicGeometry.albumCover} height={phoneMusicGeometry.albumCover} priority/> : null}
        </View>
        <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, phoneMusicGeometry.albumTitle, 800, -.035), {color: theme.colors.text, textAlign: 'center', marginTop: 20}]}>{title}</Text>
        {entry.artist?.name ? <Text numberOfLines={1} style={[browseType(theme, phoneMusicGeometry.albumArtist, 700), {color: theme.colors.accentStrong, marginTop: 4}]}>{entry.artist.name}</Text> : null}
        {facts.length ? <Facts facts={facts}/> : null}
        <MusicFormatBadges badges={snapshot.albumDetails?.badges} size="phone"/>
      </View>
      <View style={{paddingHorizontal: 20, marginTop: 18}}><MusicActions controller={controller} entry={entry} runtime={runtime} actions={actions} moreRef={more} roundMore={false} download={download}/></View>
      {snapshot.phase === 'error' ? <MusicErrorView testID={`music-page-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}} style={{margin: 20}}/> : null}
      {snapshot.songs.length ? <View style={{marginTop: 16}}><SongLines songs={snapshot.songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection} offset={snapshot.songPage.offset} onFocusRow={entry=>controller.focusSong(entry,snapshot.songPage)} onBlurRow={entry=>controller.blurSong(entry,snapshot.songPage)} onTail={()=>{void controller.nextSongs(snapshot.songPage);}}/></View> : null}
      <View style={{paddingHorizontal:20}}><MusicSectionControls id="album-songs" section={snapshot.songPage} next={()=>{void controller.nextSongs(snapshot.songPage);}} previous={()=>{void controller.previousSongs(snapshot.songPage);}} retry={()=>{void controller.retrySongs(snapshot.songPage);}} refresh={()=>{void controller.refreshSongs(snapshot.songPage);}}/></View>
      <SimilarAlbums selection={selection} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry}/>
    </MusicScrollView>
    <PhoneHeader title={title} onBack={onBack} onMore={() => actions.menu(entry, more)} moreRef={more}/>
  </View>;
}

function TabletSongLine({song, index, runtime, controller, actions, playingId, selection}: {
  song: ContentEntry; index: number; runtime: NativeShellRuntime; controller: NativeTVMusicController; actions: Actions; playingId?: string; selection?: NativeSavedSelectionPort;
}) {
  const theme = useTheme(), title = song.displayTitle ?? song.title, playing = playingId === song.id, anchor = useRef<NativeView>(null), picked = selection?.item(song);
  const model = useMemo(() => cardText({id: song.id, kind: 'song', title, artist: song.artist?.name, album: song.album?.name, durationSeconds: song.duration}, 'song-row', runtime.text), [song, title, runtime.text]);
  const duration = model.detail.find(part => part.id === 'duration')?.text;
  return <Focusable ref={anchor} accessibilityRole={picked?.active ? 'checkbox' : 'button'} accessibilityState={{checked: picked?.active ? picked.selected : undefined}} accessibilityLabel={t(picked?.active ? 'card.select' : 'next.browse.native.play', {title})} focusKind="row" focusRadius={10}
    onLongPressAction={() => actions.menu(song, anchor, picked?.onEnter)} onPress={() => {if (picked?.active) picked.onToggle(); else void controller.play(song.id).catch(runtime.reportError);}}
    style={{height: 60, paddingHorizontal: 8, flexDirection: 'row', alignItems: 'center', borderRadius: 10, backgroundColor: playing ? alpha(theme.colors.text, .08) : undefined}}>
    {picked?.active ? <SelectionCircle selected={picked.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title})}/> : null}
    <View style={{width: 36, alignItems: 'center'}}>{playing ? <LevelBars/> : <Text style={[browseType(theme, 14, 600), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{song.trackNumber ?? index + 1}</Text>}</View>
    <Text numberOfLines={1} style={[browseType(theme, 16, 600), {flex: 1, color: playing ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
    {song.plays !== undefined ? <Text style={[browseType(theme, 14, 400), {width: 62, textAlign: 'right', color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{defaultI18n.number(song.plays)}</Text> : <View style={{width: 62}}/>}
    {duration ? <Text style={[browseType(theme, 14, 400), {width: 70, textAlign: 'right', color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{duration}</Text> : <View style={{width: 70}}/>}
    <Button variant="text" iconOnly icon={controller.songFavorite(song) ? 'i-heart-f' : 'i-heart'} iconColor={controller.songFavorite(song) ? theme.colors.accentStrong : theme.colors.textTertiary} iconSize={20}
      style={{width: 44, height: 44, flexShrink: 0}} accessibilityLabel={t(controller.songFavorite(song) ? 'entry.removeFromFavorites' : 'entry.addToFavorites')} accessibilityState={{checked: controller.songFavorite(song)}}
      disabled={controller.songFavoritePending(song)} onPress={() => {void controller.toggleSongFavorite(song).catch(runtime.reportError);}}/>
    <Focusable accessibilityRole="button" accessibilityLabel={t('next.browse.native.moreFor', {title})} onPress={() => actions.menu(song, anchor, picked?.onEnter)} focusRadius={17}
      style={{width: 44, height: 44, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-more" size={20} color={theme.colors.textTertiary}/></Focusable>
  </Focusable>;
}

/** phone-app#15b: the artist body, with the drawn circle, Top songs and Albums. */
export function PhoneArtistScreen({controller, entry, runtime, actions, openEntry, onBack, selection}: {
  controller: NativeTVMusicController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions; selection?: NativeSavedSelectionPort; openEntry: (entry: ContentEntry) => void; onBack: () => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), playingId = usePlayingItem(runtime);
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? noRevision);
  const more = useRef<NativeView>(null);
  const releases=useMemo(()=>artistReleaseGroups(snapshot.albums),[snapshot.albums]);
  const covers = useMemo(() => snapshot.albums.flatMap(album => {const source = runtime.artwork(album.posterUrl); return source ? [source] : [];}), [snapshot.albums, runtime]);
  const facts = [
    (snapshot.artistDetails??entry).albumCount!==undefined?t('title.albumCount',{count:(snapshot.artistDetails??entry).albumCount!}):undefined,
    snapshot.songTotal ? t('title.songCount', {count: snapshot.songTotal}) : undefined,
    entry.genres?.[0],
  ].filter((value): value is string => !!value);
  if (theme.platform !== 'phone') {
    const round = {height: phoneMusicGeometry.actionHeight, width: phoneMusicGeometry.actionHeight, borderRadius: phoneMusicGeometry.actionRadius};
    const years = entry.activeBeginYear || entry.activeEndYear ? `${entry.activeBeginYear ?? ''}–${entry.activeEndYear ?? ''}` : undefined;
    const details = [[t('title.genres'), entry.genres?.join(', ')], [t('title.country'), entry.country], [t('title.year'), years]].filter(([, value]) => !!value) as [string, string][];
    return <View testID="native-tablet-artist" style={{flex: 1}}><StandardBackdrop/>
      <Wash runtime={runtime} path={snapshot.albums[0]?.posterUrl ?? entry.posterUrl ?? entry.backdropUrl}/>
      <MusicScrollView contentContainerStyle={{paddingTop: 56, paddingBottom: 100}} showsVerticalScrollIndicator={false}>
        <View style={{alignItems: 'center', paddingHorizontal: 32}}>
          <ArtistCircle covers={covers} rings={[3]} initials={[...entry.title.trim()][0] ?? ''} alt={entry.title} size={220} visible/>
          <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, 58, 800, -.04, 61), {color: theme.colors.text, textAlign: 'center', marginTop: 20}]}>{entry.title}</Text>
          {facts.length ? <Facts facts={facts} strongFirst={false}/> : null}
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginTop: 20}}>
            <Button style={{height: phoneMusicGeometry.actionHeight, borderRadius: phoneMusicGeometry.actionRadius, paddingHorizontal: 24}} icon="i-play" textStyle={browseType(theme, 15, 700)} onPress={() => {void controller.play().catch(runtime.reportError);}}>{t('entry.play')}</Button>
            <Button variant="glass" style={{height: phoneMusicGeometry.actionHeight, borderRadius: phoneMusicGeometry.actionRadius, paddingHorizontal: 24}} icon="i-shuffle" textStyle={browseType(theme, 15, 700)} onPress={() => {void controller.play(undefined, true).catch(runtime.reportError);}}>{t('title.shuffle')}</Button>
            <Button variant="glass" iconOnly icon={snapshot.favorite ? 'i-heart-f' : 'i-heart'} style={round} accessibilityLabel={t(snapshot.favorite ? 'next.browse.native.unfavorite' : 'next.browse.native.favorite')} onPress={() => {void controller.toggleFavorite().catch(runtime.reportError);}}/>
            <Button ref={more} variant="glass" iconOnly icon="i-more" style={round} accessibilityLabel={t('card.more', {title: entry.title})} onPress={() => actions.menu(entry, more)}/>
          </View>
        </View>
        {snapshot.phase === 'error' ? <MusicErrorView testID={`music-page-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}} style={{marginHorizontal: 32, marginTop: 24}}/> : null}
        {selection?.start && controller.selectionProjection() ? <View style={{alignItems: 'flex-end', paddingHorizontal: 32, marginTop: 24}}><Chip icon="i-check" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip></View> : null}
        {snapshot.songs.length ? <View style={{marginTop: 32, paddingHorizontal: 32}}>
          <Text style={[browseType(theme, 19, 700), {color: theme.colors.text, marginBottom: 8}]}>{t('music.topSongs')}</Text>
          <View style={{flexDirection: 'row', alignItems: 'center', height: 42, paddingHorizontal: 8, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
            <Text style={[browseType(theme, 13, 600), {width: 36, color: theme.colors.textTertiary}]}>#</Text>
            <Text style={[browseType(theme, 13, 600), {flex: 1, color: theme.colors.textTertiary}]}>{t('next.browse.native.title')}</Text>
            <Text style={[browseType(theme, 13, 600), {width: 62, textAlign: 'right', color: theme.colors.textTertiary}]}>{t('next.browse.native.plays')}</Text>
            <Text style={[browseType(theme, 13, 600), {width: 70, textAlign: 'right', color: theme.colors.textTertiary}]}>{t('next.browse.duration')}</Text>
            <View style={{width: 88}}/>
          </View>
          <MusicWindow items={snapshot.songs} itemHeight={60} renderItem={(song, index) => <TabletSongLine song={song} index={index} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/>}/>
        </View> : null}
        {releases.map(group=><View key={group.id} style={{marginTop:28,paddingHorizontal:32}}>
          <Text style={[browseType(theme,19,700),{color:theme.colors.text,marginBottom:10}]}>{t(group.label)}</Text>
          <MusicShelf items={group.entries} width={phoneMusicGeometry.albumCard} gap={14} inset={0} onNearEnd={()=>{void controller.nextReleases(snapshot.releases);}}
            renderItem={album=><AlbumCard album={album} controller={controller} selection={selection} runtime={runtime} actions={actions} openEntry={openEntry} onFocus={()=>controller.focusRelease(album,snapshot.releases)} onBlur={()=>controller.blurRelease(album,snapshot.releases)}/>}/>
        </View>)}
        <View style={{paddingHorizontal:32}}><MusicSectionControls id="artist-releases" section={snapshot.releases} next={()=>{void controller.nextReleases(snapshot.releases);}} previous={()=>{void controller.previousReleases(snapshot.releases);}} retry={()=>{void controller.retryReleases(snapshot.releases);}} refresh={()=>{void controller.refreshReleases(snapshot.releases);}}/></View>
        {details.length ? <View style={{paddingHorizontal: 32, marginTop: 28, width: 340}}>
          <Text style={[browseType(theme, 19, 700), {color: theme.colors.text, marginBottom: 8}]}>{t('title.details')}</Text>
          {details.map(([label, value]) => <View key={label} style={{flexDirection: 'row', gap: 12, paddingVertical: 8, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
            <Text style={[browseType(theme, 13, 400), {width: 96, color: theme.colors.textTertiary}]}>{label}</Text><Text style={[browseType(theme, 13, 500), {flex: 1, color: theme.colors.text}]}>{value}</Text></View>)}
        </View> : null}
        <SimilarArtists selection={selection} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry} inset={32} gap={14}/>
      </MusicScrollView>
      <View pointerEvents="box-none" style={{position: 'absolute', top: insets.top + 8, left: 16, zIndex: 15}}>
        <Button testID="native-tablet-artist-back" variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={onBack} style={{height: 44, width: 44}}/>
      </View>
    </View>;
  }
  return <View testID="native-phone-artist" style={{flex: 1}}><StandardBackdrop/>
    <Wash runtime={runtime} path={snapshot.albums[0]?.posterUrl ?? entry.posterUrl ?? entry.backdropUrl}/>
    <MusicScrollView contentContainerStyle={{paddingTop: Math.max(phoneMusicGeometry.artistTop, insets.top + 56), paddingBottom: 40}} showsVerticalScrollIndicator={false}>
      <View style={{alignItems: 'center', paddingHorizontal: 20}}>
        <ArtistCircle covers={covers} rings={[3]} initials={[...entry.title.trim()][0] ?? ''} alt={entry.title} size={phoneMusicGeometry.artistCircle} visible/>
        <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, phoneMusicGeometry.artistTitle, 800, -.035), {color: theme.colors.text, textAlign: 'center', marginTop: 18}]}>{entry.title}</Text>
        {facts.length ? <Facts facts={facts} strongFirst={false}/> : null}
      </View>
      <View style={{paddingHorizontal: 20, marginTop: 18}}><MusicActions controller={controller} entry={entry} runtime={runtime} actions={actions} moreRef={more} roundMore/></View>
      {snapshot.phase === 'error' ? <MusicErrorView testID={`music-page-${entry.id}-error`} error={snapshot.error} onRetry={() => {void controller.load();}} style={{margin: 20}}/> : null}
      {snapshot.songs.length ? <View style={{marginTop: 18}}>
        <Text style={[browseType(theme, phoneMusicGeometry.sectionLabel, 700), {color: theme.colors.textTertiary, marginHorizontal: 20, marginBottom: 8}]}>{t('music.topSongs')}</Text>
        <SongLines songs={snapshot.songs} runtime={runtime} controller={controller} actions={actions} playingId={playingId} selection={selection}/>
      </View> : null}
      {releases.map(group=><View key={group.id} style={{marginTop:14}}>
        <Text style={[browseType(theme,19,700),{color:theme.colors.text,marginHorizontal:20,marginBottom:10}]}>{t(group.label)}</Text>
        <MusicShelf items={group.entries} width={phoneMusicGeometry.albumCard} gap={12} inset={20} onNearEnd={()=>{void controller.nextReleases(snapshot.releases);}}
            renderItem={album=><AlbumCard album={album} controller={controller} selection={selection} runtime={runtime} actions={actions} openEntry={openEntry} onFocus={()=>controller.focusRelease(album,snapshot.releases)} onBlur={()=>controller.blurRelease(album,snapshot.releases)}/>}/>
      </View>)}
      <View style={{paddingHorizontal:20}}><MusicSectionControls id="artist-releases" section={snapshot.releases} next={()=>{void controller.nextReleases(snapshot.releases);}} previous={()=>{void controller.previousReleases(snapshot.releases);}} retry={()=>{void controller.retryReleases(snapshot.releases);}} refresh={()=>{void controller.refreshReleases(snapshot.releases);}}/></View>
      <SimilarArtists selection={selection} snapshot={snapshot} controller={controller} runtime={runtime} actions={actions} openEntry={openEntry} inset={20} gap={12}/>
    </MusicScrollView>
    <PhoneHeader title={entry.title} onBack={onBack} onMore={() => actions.menu(entry, more)} moreRef={more}/>
  </View>;
}

/** TV-005/WEB-050 parity: a similar row reads its next page within a screen of its end. */
function similarNearEnd(controller:NativeTVMusicController,snapshot:NativeTVMusicSnapshot){
  return ({nativeEvent:{contentOffset,layoutMeasurement,contentSize}}:NativeSyntheticEvent<NativeScrollEvent>)=>{
    if(contentOffset.x+layoutMeasurement.width>=contentSize.width-layoutMeasurement.width)controller.similarPage(snapshot)?.requestNext();
  };
}
