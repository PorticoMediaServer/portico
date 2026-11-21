import React, {useRef, useSyncExternalStore} from 'react';
import type {View} from 'react-native';
import {generatedCoverTint} from '@portico/design';
import {GeneratedArt} from '../artwork';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {shapeFor, type ShapeFamily} from '../../../../../packages/client-core/src/presentation/card-shape';
import {CircleCard, GenreTile, PlaylistMosaic, PosterCard, SquareCard, WideCard, type CardSelection} from '../cards';
import {TVSparseCardFrame, TVCardPaintFrame, TVPendingCardPaint} from '../cards/TVSparseCardFrame';
import type {CardProps} from '../cards/primitives';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {homeCardText} from '../../screens/home/model';
import type {NativeLibraryActions} from '../../screens/library/LibraryScreen';

export function LibraryCard({entry, family, width, wide = false, placeholderHeight, runtime, actions, selected, disabled, focusId, onFocus, onBlur, onSelect, onCaptionLayout}: {entry?: ContentEntry; /** The grid's own library kind (the server does not put it on each entry); the shared shape rule's family. */ family?: string; width: number; wide?: boolean; placeholderHeight?: number; runtime: NativeShellRuntime; actions: NativeLibraryActions; selected?: CardSelection; disabled?: boolean; focusId?: string; onFocus?: () => void; onBlur?: () => void; onSelect?: () => void; onCaptionLayout?: CardProps['onCaptionLayout']}) {
  const menuAnchorRef = useRef<View>(null);
  const theme = useTheme();
  const adapter = entry ? runtime.playback : undefined;
  const playing = useSyncExternalStore(adapter?.subscribe ?? (() => () => {}), adapter?.getSnapshot ?? (() => undefined));
  const renderCard = <P extends CardProps & React.Attributes,>(Component: React.ComponentType<P>, props: P) => theme.isTV
    ? <TVSparseCardFrame {...props}><Component {...props} frame={TVCardPaintFrame}/></TVSparseCardFrame> : <Component {...props}/>;
  if (!entry) return <TVSparseCardFrame title="" width={width} disabled={disabled} focusId={focusId} onFocus={onFocus} onBlur={onBlur} menuAnchorRef={menuAnchorRef}>
    <TVPendingCardPaint height={placeholderHeight ?? width * 1.5}/>
  </TVSparseCardFrame>;
  // All live variants delegate only paint; the one outer native CardFrame survives a fill/kind change.
  if (entry.kind === 'radio' && entry.radioSeed) {
    const artwork = runtime.artwork(entry.posterUrl);
    return renderCard(SquareCard, {focusId, disabled,
      title: defaultI18n.t('music.radioNamed', {name: entry.title}), width, titleLines: 2, onCaptionLayout,
      caption: [{id: 'radio', role: 'value' as const, text: defaultI18n.t('music.radio')}], artwork,
      artworkFallback: artwork ? undefined : <GeneratedArt glyph={Array.from(entry.title.trim())[0] ?? ''}
        palette={generatedCoverTint(entry.title)} width={width} height={width}/>,
      onNavigate: () => actions.play(entry), onFocus, onBlur});
  }
  if (entry.kind === 'playlist' || entry.kind === 'category' && entry.navigation?.browse?.pivot === 'albums') {
    const covers = (entry.artworkPaths ?? []).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];});
    // A playlist counts its songs; a genre the albums that carry it (the tile opens Albums filtered to it).
    const caption = entry.count === undefined ? [] : [{id: 'count', role: 'value' as const,
      text: defaultI18n.t(entry.countIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: entry.count, pivot: entry.kind === 'playlist' ? 'songs' : 'albums'})}];
    const resource = {focusId, disabled, width, title: entry.title, caption, covers, onNavigate: () => actions.open(entry), onFocus, onBlur};
    return entry.kind === 'playlist' ? renderCard(PlaylistMosaic, {...resource, titleLines: 2, onCaptionLayout})
      : renderCard(GenreTile, {...resource, variant: 'music' as const, height: theme.isTV ? 180 : 84});
  }
  const text = homeCardText(entry, false, runtime.text);
  const playingAlbum = entry.kind === 'album' && playing?.entry?.album?.id === entry.id;
  const listening = entry.kind === 'album' || entry.kind === 'song';
  const artworkFallback = listening ? <GeneratedArt glyph={Array.from(entry.title.trim())[0] ?? ''}
    palette={generatedCoverTint(entry.title)} width={width} height={width}/> : undefined;
  const props = {focusId, disabled, title: entry.displayTitle || entry.title, width, titleLines: 2 as const, onCaptionLayout, caption: text.caption, artwork: runtime.artwork(entry.posterUrl), artworkFallback, watched: entry.watched, playing: playingAlbum,
    onNavigate: () => actions.open(entry), onPlay: entry.playback ? () => actions.play(entry) : undefined, onMore: () => actions.menu(entry, menuAnchorRef, onSelect), selection: selected,
    menuAnchorRef, moreExpanded: actions.isMenuOpen?.(entry), onFocus, onBlur};
  // A landscape shelf keeps the 16:9 art and puts its title and time left in the caption.
  if (wide) {const resume = homeCardText(entry, true, runtime.text);
    return renderCard(WideCard, {...props, captionBelow: true, caption: resume.caption, detail: resume.detail, artwork: runtime.artwork(entry.backdropUrl ?? entry.posterUrl), progress: resume.progress});}
  const artistCovers = (entry.artworkPaths ?? []).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];});
  // One shape rule for every client (client-core shapeFor): music and audiobooks square, an audiobook series too; reading
  // series 2:3; people (an audiobook's authors) circles, drawn as an artist is (collections 39, 44: initials over their covers).
  const shape = shapeFor(entry.kind, 'grid', family as ShapeFamily | undefined);
  return entry.kind === 'artist' || shape === 'circle' ? renderCard(CircleCard, {...props, art: {covers: artistCovers.length ? artistCovers : props.artwork ? [props.artwork] : [], alt: entry.title}})
    : shape === 'square' ? renderCard(SquareCard, props) : renderCard(PosterCard, props);
}
