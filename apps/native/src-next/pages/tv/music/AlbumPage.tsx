import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork} from '../../../kit/artwork';
import {ChoiceChips, PanelBlock, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {MusicFormatBadges} from '../../../kit/music/MusicFormatBadges';
import {MusicSectionControls} from '../../../kit/music/MusicSectionControls';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {useAlbum, type AlbumPageProps} from '../../shared/music/useAlbum';
import {useOpenedReturn} from '../../shared/music/useOpenedReturn';

const t = defaultI18n.t;

export type TVAlbumPageProps = AlbumPageProps & Readonly<{
  /** Add to playlist (the old TV album page's button), when the server has playlists. */
  onPlaylist?: () => void}>;

/** An album's page on a TV (tv-more frame 6): `TitleFrame` with its cover at the left, "Album" over its title, the artist
 * (a link to their page), the year, genre, songs and length and its format badges, Play, Shuffle, the heart, Add to
 * playlist and ⋯; its songs under the words beside the cover (a multi-disc album's Disc choices over them), paged by the
 * server; then More like this. Focus starts on Play, or back on the album this page opened. */
export function AlbumPage(props: TVAlbumPageProps) {
  // W-12: Back from an album this page opened (More like this) starts on its card (`useOpenedReturn`).
  const {returning, openEntry, pageKey} = useOpenedReturn(props.entry, props.openEntry);
  const view = useAlbum({...props, openEntry}), songs = view.songs;
  // That card is the start while More like this is still to come and once it is drawn; if it is gone, Play is.
  const startFocus = returning && (!view.similar || view.similar.items.some(item => item.key === returning)) ? returning : undefined;
  return <TitleFrame testID="native-tv-album" startFocus={startFocus} back={{label: t('action.back'), onPress: props.onBack, testID: 'album-back'}}
    art={<AmbientArt treatment="cover" source={view.glow}/>}
    cover={{shape: 'square', node: size => <Artwork source={view.cover} width={size} height={size}/>}}
    kicker={{label: t('search.kind.album')}} title={view.title}
    byline={view.byline && {...view.byline, initials: [...view.byline.text.trim()][0]}}
    metadata={<HeroMetadata facts={view.facts}><MusicFormatBadges badges={view.badges} size="tv"/></HeroMetadata>}
    primary={{icon: 'i-play', label: t('entry.play'), onPress: view.play, testID: 'album-play'}}
    actions={[{icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, onPress: view.shuffle, testID: 'album-shuffle'},
      {icon: view.favorite.on ? 'i-heart-f' : 'i-heart', label: t(view.favorite.on ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), active: view.favorite.on,
        onPress: view.favorite.toggle, testID: 'album-favorite'},
      ...(props.onPlaylist ? [{icon: 'i-listplus' as const, label: t('entry.addToPlaylist'), onPress: props.onPlaylist, testID: 'album-playlist'}] : []),
      {icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'album-menu'}]}
    beside={<>
      {view.discs && <ChoiceChips items={view.discs}/>}
      <TrackWindow inset={false} rows={songs.rows} variant="album" select={songs.select} onPlay={songs.onPlay} onMore={songs.onMore} onFocusRow={songs.onFocusRow} onBlurRow={songs.onBlurRow}/>
      {!view.state && <MusicSectionControls id="album-songs" section={songs.section} next={songs.next} previous={songs.previous} retry={songs.retry} refresh={songs.refresh}/>}
    </>}>
    {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
    {view.similar && <PosterRow {...view.similar} focusKey={`${view.similar.testID}.${pageKey}`}/>}
    {view.similarState && <PanelBlock><StateRow {...view.similarState}/></PanelBlock>}
  </TitleFrame>;
}
