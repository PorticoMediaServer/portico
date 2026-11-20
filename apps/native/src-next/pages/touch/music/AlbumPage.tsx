import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork, missingArt} from '../../../kit/artwork';
import {AboutTable, musicScroller, PanelBlock, PanelHeading, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {MusicFormatBadges} from '../../../kit/music/MusicFormatBadges';
import {MusicSectionControls} from '../../../kit/music/MusicSectionControls';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {useAlbum, type AlbumPageProps} from '../../shared/music/useAlbum';

const t = defaultI18n.t;

/** An album's page on a phone and a tablet (phone-app frame 13, tablet-play 11): `TitleFrame` with its cover over a glow from it, the artist as a link
 * under the title, Play and Shuffle sharing the row with Download, ⋯ at the top, its numbered songs a window at a time
 * (paging the server near the end), and More like this. */
export function AlbumPage(props: AlbumPageProps) {
  const view = useAlbum(props), songs = view.songs;
  return <TitleFrame testID="native-phone-album" back={{label: t('action.back'), onPress: props.onBack, testID: 'album-back'}}
    topAction={{icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'album-more'}}
    art={<AmbientArt treatment="page" source={view.glow}/>}
    cover={{shape: 'square', node: size => <Artwork source={view.cover} width={size} height={size} priority
      fallback={view.cover ? undefined : missingArt({title: view.title, author: view.artistName, shape: 'square', width: size, height: size})}/>}}
    kicker={view.kicker} title={view.title} byline={view.byline}
    metadata={<HeroMetadata facts={view.facts}><MusicFormatBadges badges={view.badges} size="phone"/></HeroMetadata>}
    primary={{icon: 'i-play', label: t('entry.play'), onPress: view.play, testID: 'album-play'}}
    actions={[{icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, split: true, onPress: view.shuffle, testID: 'album-shuffle'},
      // A tablet on its side: the heart and Add to playlist beside them (tablet-play 11); elsewhere they are in ⋯.
      {icon: view.favorite.on ? 'i-heart-f' : 'i-heart', label: t(view.favorite.on ? 'next.browse.native.unfavorite' : 'next.browse.native.favorite'), active: view.favorite.on, wide: true, onPress: view.favorite.toggle, testID: 'album-favorite'},
      ...(view.playlist ? [{icon: 'i-list-plus' as const, label: t('next.selection.addToPlaylist'), wide: true, onPress: view.playlist, testID: 'album-playlist'}] : []),
      ...(view.download ? [{icon: 'i-dl' as const, label: t('next.cards.download'), onPress: view.download, testID: 'album-download'}] : [])]}
    aside={view.details && <PanelBlock inset={false}><PanelHeading title={t('title.details')}/><AboutTable rows={view.details}/></PanelBlock>}
    scroller={musicScroller({onNearEnd: songs.onNearEnd, testID: 'album-scroll'})}>
    {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
    <TrackWindow rows={songs.rows} variant="album" select={songs.select} onPlay={songs.onPlay} onMore={songs.onMore} onFavorite={songs.onFavorite} onFocusRow={songs.onFocusRow} onBlurRow={songs.onBlurRow}/>
    <PanelBlock><MusicSectionControls id="album-songs" section={songs.section} next={songs.next} previous={songs.previous} retry={songs.retry} refresh={songs.refresh}/></PanelBlock>
    {view.similar && <PosterRow {...view.similar}/>}
    {view.similarState && <PanelBlock><StateRow {...view.similarState}/></PanelBlock>}
  </TitleFrame>;
}
