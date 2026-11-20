import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt} from '../../../kit/artwork';
import {Button} from '../../../kit/controls';
import {AboutNote, AboutTable, ArtistCover, AsideSection, PanelBlock, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterColumn, PosterRow} from '../../../kit/layout';
import {MusicSectionControls} from '../../../kit/music/MusicSectionControls';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {useArtist, type ArtistPageProps} from '../../shared/music/useArtist';
import {useOpenedReturn} from '../../shared/music/useOpenedReturn';

const t = defaultI18n.t;

/** An artist's page on a TV (tv-more frame 8): `TitleFrame` with "Artist" over their name, the genre, country, active years
 * and what the server has of theirs, Play, Shuffle, the heart and ⋯, and the circle of their albums at the right with its
 * rings. Under it Top songs, with their Albums as a column beside the panel and Details under that; then their other
 * releases (paged by the server), similar artists, and All songs, read when asked for. Focus starts on Play, or back on
 * the album this page opened. */
export function ArtistPage(props: ArtistPageProps) {
  // W-12: Back from an album this page opened starts on its card (`useOpenedReturn`).
  const {returning, openEntry, pageKey} = useOpenedReturn(props.entry, props.openEntry);
  const view = useArtist({...props, openEntry}), songs = view.topSongs, pages = view.releasePages, all = view.allSongs;
  // That card is the start while the releases load and once drawn; if it is gone, Play is.
  const waiting = pages.section.phase === 'idle' || pages.section.phase === 'loading';
  const startFocus = returning && (waiting || view.releases.some(row => row.items.some(item => item.key === returning))) ? returning : undefined;
  const albums = view.releases.find(row => row.group === 'albums'), others = view.releases.filter(row => row !== albums);
  const {rows: details, overview, source} = view.details;
  // The music route draws a new page on Back: named regions let the layer stack find the card that opened the next page.
  const stable = (testID?: string) => testID && `${testID}.${pageKey}`;
  return <TitleFrame testID="native-tv-artist" startFocus={startFocus} back={{label: t('action.back'), onPress: props.onBack, testID: 'artist-back'}}
    art={<AmbientArt treatment="artist" source={view.glow}/>}
    cover={{shape: 'circle', large: true, node: size => <ArtistCover covers={view.covers} initials={view.initials} alt={view.title} size={size}/>}}
    kicker={{label: t('search.kind.artist')}} title={view.title} metadata={<HeroMetadata facts={view.factsTV}/>}
    primary={{icon: 'i-play', label: t('entry.play'), onPress: view.play, testID: 'artist-play'}}
    actions={[{icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, onPress: view.shuffle, testID: 'artist-shuffle'},
      {icon: view.favorite.on ? 'i-heart-f' : 'i-heart', label: t(view.favorite.on ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), active: view.favorite.on,
        onPress: view.favorite.toggle, testID: 'artist-favorite'},
      {icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'artist-menu'}]}
    aside={<>
      {albums && albums.items.length > 0 && <AsideSection title={albums.title}><PosterColumn items={albums.items} testID={albums.testID} focusKey={stable(albums.testID)}/></AsideSection>}
      <AsideSection title={t('title.details')}><AboutTable rows={details}/>{overview && <AboutNote text={overview} source={source}/>}</AsideSection>
    </>}>
    {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
    {songs && <TrackWindow heading={{title: songs.title}} rows={songs.rows} variant="top-songs" select={songs.select} onPlay={songs.onPlay} onMore={songs.onMore}/>}
    {others.map(row => <PosterRow key={row.testID} {...row} focusKey={stable(row.testID)}/>)}
    <PanelBlock><MusicSectionControls id="artist-releases" section={pages.section} next={pages.next} previous={pages.previous} retry={pages.retry} refresh={pages.refresh}/></PanelBlock>
    {view.similar && <PosterRow {...view.similar} focusKey={stable(view.similar.testID)}/>}
    {view.similarState && <PanelBlock><StateRow {...view.similarState}/></PanelBlock>}
    {all.list
      ? <TrackWindow heading={{title: t('music.allSongs'), trailing: all.total}} rows={all.list.rows} variant="top-songs" select={all.list.select} onPlay={all.list.onPlay} onMore={all.list.onMore} onFocusRow={all.focus}/>
      : <PanelBlock><Button testID="music-all-songs" variant="text" icon="i-music" disabled={all.loading} onPress={all.load}>{t('music.allSongs')}</Button></PanelBlock>}
    {all.list && <PanelBlock><MusicSectionControls id="artist-songs" section={all.section} next={all.next} previous={all.previous} retry={all.retry} refresh={all.refresh}/></PanelBlock>}
  </TitleFrame>;
}
