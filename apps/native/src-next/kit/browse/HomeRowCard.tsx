import React, {useRef, type RefObject} from 'react';
import type {View} from 'react-native';
import {generatedCoverTint} from '@portico/design';
import {homeEntryCapabilities} from '../../../../../packages/client-core/src/home-selection';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {PosterCard, SquareCard} from '../cards';
import {GeneratedArt, generatedPoster} from '../artwork';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {homeCardText} from '../../screens/home/model';

export function HomeRowCard({entry, runtime, width, onOpen, onPlay, onMenu, onFocus, onBlur, onCaptionLayout, square}: {square?: boolean; entry: ContentEntry; runtime: NativeShellRuntime; width: number; onOpen: () => void; onPlay: () => void;
  onMenu?: (anchor: RefObject<View | null>) => void; onFocus: () => void; onBlur: () => void; onCaptionLayout?: (height: number) => void}) {
  const anchor = useRef<View>(null), theme = useTheme(), text = homeCardText(entry, false, runtime.text);
  const allowed = homeEntryCapabilities(entry, {platform: theme.isTV ? 'tv' : 'phone-app', playbackAvailable: !!runtime.playback});
  const Card = square ? SquareCard : PosterCard;
  return <Card title={text.title} caption={text.caption} detail={text.detail} width={width} artwork={runtime.artwork(entry.posterUrl)} artworkFallback={square
      ? <GeneratedArt glyph={[...text.title.trim()][0] ?? ''} palette={generatedCoverTint(text.title)} width={width} height={width}/>
      : generatedPoster(text.title, width)} density="grid" titleLines={2} onCaptionLayout={onCaptionLayout} menuAnchorRef={anchor}
    onNavigate={onOpen} primaryAction={theme.isTV && allowed.playable && entry.playback ? 'play' : 'navigate'} onPlay={onPlay} onFocus={onFocus} onBlur={onBlur} onMore={onMenu ? () => onMenu(anchor) : undefined}/>;
}
