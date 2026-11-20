import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, ArtistCircle} from '../../../kit/artwork';
import {AboutTable, musicScroller, PanelBlock, PanelHeading, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {MusicSectionControls} from '../../../kit/music/MusicSectionControls';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {useArtist, type ArtistPageProps} from '../../shared/music/useArtist';

const t = defaultI18n.t;

/** An artist's page on a phone and a tablet (phone-app frame 15b; a tablet adds its song table, Details and the heart): `TitleFrame` with the circle of their albums over the page wash from
 * them, the counts and genre, Play and Shuffle sharing the row with ⋯, Top songs, their releases as rows of square covers
 * (paged by the server), and similar artists as circles. */
export function ArtistPage(props: ArtistPageProps) {
  const view = useArtist(props), songs = view.topSongs, pages = view.releasePages;
  return <TitleFrame testID="native-phone-artist" back={{label: t('action.back'), onPress: props.onBack, testID: 'artist-back'}}
    topAction={{icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'artist-more'}}
    art={<AmbientArt treatment="page" source={view.glow}/>}
    cover={{shape: 'circle', large: true, node: size => <ArtistCircle covers={view.covers} rings={[3]} initials={view.initials} alt={view.title} size={size} visible/>}}
    title={view.title} metadata={<HeroMetadata facts={view.facts}/>}
    primary={{icon: 'i-play', label: t('entry.play'), onPress: view.play, testID: 'artist-play'}}
    actions={[{icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, split: true, onPress: view.shuffle, testID: 'artist-shuffle'},
      // A tablet on its side: the heart beside them (the old tablet artist page); elsewhere it is in ⋯.
      {icon: view.favorite.on ? 'i-heart-f' : 'i-heart', label: t(view.favorite.on ? 'next.browse.native.unfavorite' : 'next.browse.native.favorite'), active: view.favorite.on, wide: true, onPress: view.favorite.toggle, testID: 'artist-favorite'},
      {icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'artist-menu'}]}
    aside={view.about?.length ? <PanelBlock inset={false}><PanelHeading title={t('title.details')}/><AboutTable rows={view.about}/></PanelBlock> : undefined}
    scroller={musicScroller({testID: 'artist-scroll'})}>
    {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
    {songs && <TrackWindow heading={{title: songs.title, quiet: true}} rows={songs.rows} variant="top-songs" select={songs.select} onPlay={songs.onPlay} onMore={songs.onMore} onFavorite={songs.onFavorite}/>}
    {view.releases.map(row => <PosterRow key={row.testID} {...row}/>)}
    <PanelBlock><MusicSectionControls id="artist-releases" section={pages.section} next={pages.next} previous={pages.previous} retry={pages.retry} refresh={pages.refresh}/></PanelBlock>
    {view.similar && <PosterRow {...view.similar}/>}
    {view.similarState && <PanelBlock><StateRow {...view.similarState}/></PanelBlock>}
  </TitleFrame>;
}
