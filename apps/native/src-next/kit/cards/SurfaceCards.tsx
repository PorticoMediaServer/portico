import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import type {ChannelMarkModel, FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {AmbientArt, Artwork, ChannelMark, CoverFan, CoverMosaic, GeneratedArt, MediaArt, type AmbientArtSource, type ArtworkPalette, type ArtworkSource, type CoverShape} from '../artwork/index';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {CardOverlays} from './MediaCards';
import {ArtFrame, Caption, CardFrame, Parts, Progress, Scrim, alpha, titleStyle, type CardProps} from './primitives';

export type LiveCardProps = CardProps & {
  channel: string;
  number?: string;
  sourceName?: string;
  mark: ChannelMarkModel;
  logo?: ArtworkSource;
  flags?: readonly FactPart[];
  range?: string;
  progress?: number;
};
export function LiveCard(props: LiveCardProps) {
  const t = useTheme(), width = props.width ?? (t.isTV ? 400 : 244), height = t.isTV ? 225 : 140, pad = t.isTV ? 22 : 14;
  return <CardFrame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={height} />}>
    {s => <ArtFrame height={height} radius={12} focused={s.focused} hovered={s.hovered} holding={s.holding} hoverScale={t.spec.card.hover.liveScale} selected={props.selection?.selected}>
      <AmbientArt palette={{first: t.colors.ambient, second: t.colors.raised}} treatment="cover" visible={props.visible} priority={props.priority} />
      <View style={{flex: 1, padding: pad}}>
        <View style={{flexDirection: 'row', gap: 10, alignItems: 'center'}}>
          <ChannelMark mark={props.mark} source={props.logo} size={t.isTV ? 48 : 34} visible={props.visible} />
          <View style={{flex: 1, minWidth: 0}}>
            <Text numberOfLines={1} style={[t.type.liveChannel, {color: t.colors.textSecondary}]}>{props.channel}</Text>
            {(props.number || props.sourceName) && <View style={{flexDirection: 'row', gap: 8}}>{props.number && <Text style={[t.type.cardSubtitle, {color: t.colors.textTertiary}]}>{props.number}</Text>}{props.sourceName && <Text numberOfLines={1} style={[t.type.cardSubtitle, {color: t.colors.textTertiary, flexShrink: 1}]}>{props.sourceName}</Text>}</View>}
          </View>
          {!!props.flags?.length && <View style={{gap: 3}}>{props.flags.map(flag => <View key={flag.id} style={{flexDirection: 'row', alignItems: 'center', gap: 6}}>
            {(flag.id === 'live' || flag.id === 'recording') && <View style={{width: 7, height: 7, borderRadius: 4, backgroundColor: t.colors.record}} />}
            <Text style={[t.type.badge, {color: flag.id === 'live' || flag.id === 'recording' ? t.colors.record : t.colors.accentStrong}]}>{flag.text}</Text>
          </View>)}</View>}
        </View>
        <View style={{marginTop: 'auto'}}>
          <Text numberOfLines={1} style={[t.type.liveTitle, {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
          <Parts parts={props.caption} />
          {props.range && <Text numberOfLines={1} style={[t.type.cardSubtitle, {color: t.colors.textTertiary, marginTop: 3}]}>{props.range}</Text>}
          <Progress value={props.progress} style={{marginTop: t.isTV ? 12 : 10}} />
        </View>
      </View>
    </ArtFrame>}
  </CardFrame>;
}

type ArtCollection = {
  covers: readonly ArtworkSource[];
  ambient?: AmbientArtSource;
  palette?: ArtworkPalette;
  renderCoverFallback?: (index: number) => ReactNode;
};
export type CollectionCardProps = CardProps & ArtCollection & {backdrop?: ArtworkSource; progress?: number; kindLabel?: string; body?: string; height?: number; layout?: 'tile' | 'link'};

function Collection({banner = false, ...props}: CollectionCardProps & {banner?: boolean}) {
  const t = useTheme(), width = props.width ?? (t.isTV ? 800 : 350), height = props.height ?? (banner ? t.isTV ? 360 : 236 : width * 9 / 16);
  const pad = t.isTV ? 32 : banner ? 24 : 22;
  return <CardFrame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={height} />}>
    {s => <ArtFrame height={height} radius={banner ? 16 : 14} focused={s.focused} hovered={s.hovered} holding={s.holding} hoverScale={1.025} selected={props.selection?.selected}>
      <AmbientArt source={props.ambient} foreground={props.backdrop} palette={props.palette} treatment="library" visible={props.visible} priority={props.priority} />
      <Scrim start={0} opacity={0.78} />
      <View style={{position: 'absolute', left: pad, right: pad, bottom: pad}}>
        {!!props.covers.length && <View style={{flexDirection: 'row', gap: 6, marginBottom: 14}}>{props.covers.slice(0, 4).map((source, index) => <Artwork key={source.cacheKey} source={source} width={banner ? 42 : 34} height={banner ? 63 : 51} visible={props.visible} priority={props.priority} style={{borderRadius: 5}} fallback={props.renderCoverFallback?.(index)} />)}</View>}
        {props.kindLabel && <Text style={[t.type.cardSubtitle, {color: t.colors.accentStrong, marginBottom: 8}]}>{props.kindLabel}</Text>}
        <Text numberOfLines={2} style={[titleStyle(t, t.isTV ? 44 : banner ? 36 : 32, 800, -0.035), {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
        <View style={{marginTop: 8}}><Parts parts={props.caption} style={{color: t.colors.textSecondary}} /></View>
        <Parts parts={props.detail} />
        {props.body && <Text numberOfLines={2} style={[t.type.body, {color: t.colors.textSecondary, marginTop: 10}]}>{props.body}</Text>}
      </View>
      <Progress value={props.progress} style={{position: 'absolute', left: 0, right: 0, bottom: 0}} />
    </ArtFrame>}
  </CardFrame>;
}
export function CollectionTile(props: CollectionCardProps) {return props.layout === 'link' ? <CollectionLink {...props} /> : <Collection {...props} />;}
export function CollectionBanner(props: CollectionCardProps) {return <Collection {...props} banner />;}

function CollectionLink(props: CollectionCardProps) {
  const t = useTheme(), width = props.width ?? 358, height = props.height ?? 72;
  return <CardFrame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={height} />}>
    {s => <ArtFrame height={height} radius={14} focused={s.focused} holding={s.holding} selected={props.selection?.selected}>
      <AmbientArt source={props.ambient} palette={props.palette} treatment="library" visible={props.visible} priority={props.priority} />
      <View style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: 14, paddingLeft: 12, paddingRight: 14}}>
        <View style={{width: 58, height: 54}}>{props.covers.slice(0, 2).map((source, index) => <Artwork key={source.cacheKey} source={source} width={36} height={54} visible={props.visible} priority={props.priority} fallback={props.renderCoverFallback?.(index)} style={{position: 'absolute', left: index * 22, borderRadius: 5, transform: [{rotate: index ? '6deg' : '-7deg'}]}} />)}</View>
        <View style={{flex: 1, minWidth: 0}}>
          {props.kindLabel && <Text style={[titleStyle(t, 12, 700), {color: t.colors.accentStrong, marginBottom: 1}]}>{props.kindLabel}</Text>}
          <Text numberOfLines={1} style={[titleStyle(t, 16, 800, -0.015), {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
          <View style={{marginTop: 2}}><Parts parts={props.caption} style={[titleStyle(t, 12.5, 600), {color: t.colors.textSecondary}]} /></View>
        </View>
        <Icon name="i-right" size={20} color={t.colors.lineStrong} />
      </View>
    </ArtFrame>}
  </CardFrame>;
}

export type GenreTileProps = CardProps & ArtCollection & {variant: 'video' | 'music'; backdrop?: ArtworkSource; height?: number};
export function GenreTile(props: GenreTileProps) {
  const t = useTheme(), width = props.width ?? (t.isTV ? 320 : 174), height = props.height ?? (t.isTV ? 180 : 84);
  const Frame = props.frame ?? CardFrame;
  return <Frame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={height} />}>
    {s => <ArtFrame height={height} radius={12} focused={s.focused} hovered={s.hovered} holding={s.holding} hoverScale={1.03} selected={props.selection?.selected}>
      <AmbientArt source={props.ambient} palette={props.palette} treatment="cover" visible={props.visible} priority={props.priority} />
      {props.variant === 'video' ? t.isTV ? <><MediaArt kind="wide" source={props.backdrop} width={width} height={height} visible={props.visible} priority={props.priority} /><Scrim start={0} opacity={0.75} /></>
        : props.covers.slice(0, 2).map((source, index) => <Artwork key={source.cacheKey} source={source} width={42} height={63} visible={props.visible} priority={props.priority} fallback={props.renderCoverFallback?.(index)} style={{position: 'absolute', right: index ? -2 : 30, top: index ? 30 : 38, borderRadius: 5, transform: [{rotate: index ? '10deg' : '-8deg'}]}} />)
        : <CoverFan covers={props.covers} shape="square" variant="music" width={t.isTV ? 180 : 112} height={t.isTV ? 140 : 84} visible={props.visible} renderFallback={props.renderCoverFallback} style={{position: 'absolute', right: -2, top: 18}} />}
      <View style={{position: 'absolute', left: t.isTV ? 22 : 14, top: t.isTV ? 22 : 12, right: props.variant === 'music' ? width * 0.42 : 14}}>
        <Text numberOfLines={2} style={[titleStyle(t, t.isTV ? 26 : 15, 800, -0.01), {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
        <Parts parts={props.caption} />
      </View>
    </ArtFrame>}
  </Frame>;
}

export type LibraryCardProps = CardProps & ArtCollection & {shape: CoverShape; height?: number; /** Deliberate fallback glyph when a personal library has no logo or covers. */ glyph?: string};
function Library({compact = false, ...props}: LibraryCardProps & {compact?: boolean}) {
  const t = useTheme(), width = props.width ?? (t.isTV ? 560 : 358), height = props.height ?? (compact ? t.isTV ? 120 : 108 : t.isTV ? 180 : 108);
  const fanWidth = t.isTV ? 190 : 112, fanHeight = t.isTV ? 140 : 80;
  return <CardFrame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={height} />}>
    {s => <ArtFrame height={height} radius={16} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected}>
      {props.ambient || props.covers.length ? <AmbientArt source={props.ambient} palette={props.palette} treatment="library" visible={props.visible} priority={props.priority} />
        : <GeneratedArt glyph={props.glyph} palette={props.palette} width={width} height={height}/>}
      <View style={{position: 'absolute', left: 18, right: fanWidth + 36, top: 0, bottom: 0, justifyContent: 'center', gap: 4}}>
        <Text numberOfLines={1} style={[titleStyle(t, t.isTV ? 30 : 21, 800, -0.025), {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
        <Parts parts={props.caption} style={{color: t.colors.textSecondary}} />
      </View>
      <CoverFan covers={props.covers} shape={props.shape} variant="library" width={fanWidth} height={fanHeight} visible={props.visible} renderFallback={props.renderCoverFallback} style={{position: 'absolute', right: 22, top: (height - fanHeight) / 2}} />
    </ArtFrame>}
  </CardFrame>;
}
export function LibraryCard(props: LibraryCardProps) {return <Library {...props} />;}
export function LibraryRow(props: LibraryCardProps) {return <Library {...props} compact />;}

export type SeriesTileProps = CardProps & ArtCollection & {segments: readonly Readonly<{id: string; progress?: number; finished?: boolean}>[]; read?: boolean; height?: number};
export function SeriesTile(props: SeriesTileProps) {
  const t = useTheme();
  if (t.isTV && props.read !== false) return null;
  const width = props.width ?? (t.isTV ? 340 : 236), height = props.height ?? (t.isTV ? 224 : 156);
  return <CardFrame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={height} />}>
    {s => <ArtFrame height={height} radius={14} focused={s.focused} hovered={s.hovered} holding={s.holding} hoverScale={1.03} selected={props.selection?.selected}>
      <AmbientArt source={props.ambient} palette={props.palette} treatment="cover" visible={props.visible} priority={props.priority} />
      <CoverFan covers={props.covers} shape="poster" variant="series" width={112} height={96} visible={props.visible} renderFallback={props.renderCoverFallback} style={{position: 'absolute', right: 12, top: 12}} />
      <View style={{position: 'absolute', left: 16, right: 16, bottom: 14}}>
        <Text numberOfLines={2} style={[titleStyle(t, t.isTV ? 26 : 17, 800, -0.02), {maxWidth: t.isTV ? 190 : 128, color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
        <View style={{marginTop: 4}}><Parts parts={props.caption} style={titleStyle(t, 12.5, 600)} /></View>
        <View style={{flexDirection: 'row', gap: 3, marginTop: 10}}>{props.segments.map(segment => <View key={segment.id} style={{flex: 1}}><Progress value={segment.finished ? 1 : segment.progress ?? 0} height={4} /></View>)}</View>
      </View>
    </ArtFrame>}
  </CardFrame>;
}

export type PlaylistMosaicProps = CardProps & {covers: readonly ArtworkSource[]; shape?: CoverShape; renderCoverFallback?: (index: number) => ReactNode; create?: boolean};
export function PlaylistMosaic(props: PlaylistMosaicProps) {
  const t = useTheme(), width = props.width ?? t.spec.card.width.square[t.platform];
  const Frame = props.frame ?? CardFrame;
  return <Frame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={width} />}>
    {s => <><ArtFrame height={width} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected} style={props.create ? {borderWidth: 2, borderStyle: 'dashed', borderColor: t.colors.lineStrong, backgroundColor: 'transparent'} : undefined}>
      {props.create ? <View style={{flex: 1, alignItems: 'center', justifyContent: 'center'}}><View style={{width: 64, height: 64, borderRadius: 32, backgroundColor: alpha(t.colors.text, 0.08), alignItems: 'center', justifyContent: 'center'}}><Icon name="i-plus" size={30} color={t.colors.textSecondary} /></View></View>
        : <CoverMosaic covers={props.covers} shape={props.shape ?? 'square'} width={width} height={width} visible={props.visible} renderFallback={props.renderCoverFallback} />}
    </ArtFrame><Caption {...props} selected={props.selection?.selected} /></>}
  </Frame>;
}

export function NotInterestedButton({onPress, title, testID}: {onPress: () => void; title: string; testID?: string}) {
  const t = useTheme();
  const size = t.isTV ? 64 : 44;
  return <CardFrame title={title} accessibilityLabel={defaultI18n.t('entry.notInterested')} onNavigate={onPress} width={size} testID={testID}>
    {s => <ArtFrame height={size} radius={size / 2} focused={s.focused}><View style={{flex: 1, alignItems: 'center', justifyContent: 'center', backgroundColor: t.colors.text}}><Icon name="i-not" size={t.isTV ? 28 : 20} color={t.colors.ink} /></View></ArtFrame>}
  </CardFrame>;
}
