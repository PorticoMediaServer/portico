import React, {type ReactNode} from 'react';
import {StyleSheet, Text, View} from 'react-native';
import Svg, {Text as SvgText} from 'react-native-svg';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {MediaArt, CircleArt, PersonCircle, type ArtworkProps, type ArtworkSource, type CircleArtProps, type PersonCircleProps} from '../artwork/index';
import {Focusable} from '../controls/Focusable';
import {SelectionCircle} from '../selection/index';
import {useTheme} from '../theme';
import {ArtFrame, Badge, Caption, CardAction, CardFrame, Parts, Progress, Scrim, titleStyle, type CardProps} from './primitives';

type Media = Readonly<{
  artwork?: ArtworkSource | null;
  artworkFallback?: ReactNode;
  artworkProps?: Omit<ArtworkProps, 'source' | 'fallback' | 'width' | 'height'>;
  progress?: number;
  watched?: boolean;
  isNew?: boolean;
}>;
export type PosterCardProps = CardProps & Media & {titleLines?: 1 | 2};
export type WideCardProps = CardProps & Media;
export type SquareCardProps = CardProps & Media;
export type CircleCardProps = CardProps & ({art: CircleArtProps; person?: false} | {art: PersonCircleProps; person: true});
export type CoverCardProps = PosterCardProps & {kind?: 'book' | 'periodical' | 'newspaper'; next?: boolean};

/** SelectionCircle belongs to the selection family; cards only position the actual control. */
function Overlays({props, hovered, height, holding = false, playSize = 40, centered = false, episode = false, persistentPlay = false, pause}: {props: CardProps; hovered: boolean; height: number; holding?: boolean; playSize?: number; centered?: boolean; episode?: boolean; /** tablet7: a touch tile always shows its Play on the picture. */ persistentPlay?: boolean; pause?: () => void}) {
  const t = useTheme(), {selection, onPlay, onMore, title} = props;
  if (props.disabled || holding) return null;
  const circleInset = t.isTV ? 12 : t.layoutMode === 'desktop' ? 10 : episode ? 6 : 7;
  return <View pointerEvents="box-none" style={[StyleSheet.absoluteFill, {height}]}>
    {selection && t.layoutMode === 'desktop' && (selection.active || hovered) ? <Focusable accessibilityRole="checkbox" accessibilityLabel={defaultI18n.t('card.select', {title})} accessibilityState={{checked: selection.selected}} focusRadius={13}
      onPress={() => selection.active ? selection.onToggle() : selection.onEnter()} style={{position: 'absolute', left: circleInset, top: circleInset}}>
      <SelectionCircle selected={selection.selected} selecting interactive={false} size={26} accessibilityLabel={defaultI18n.t('card.select', {title})} />
    </Focusable> : selection?.active ? <View style={{position: 'absolute', left: circleInset, top: circleInset}}>
      <SelectionCircle selected={selection.selected} selecting size={t.isTV ? 44 : 24} interactive={false} accessibilityLabel={defaultI18n.t('card.select', {title})} />
    </View> : null}
    {!t.isTV && !selection?.active && (hovered || persistentPlay) && (onPlay || pause) && <CardAction name={pause ? 'i-pause' : 'i-play'} label={pause ? defaultI18n.t('next.cards.pause') : defaultI18n.t('card.play', {title})} onPress={pause ?? onPlay!} size={playSize}
      style={centered ? {position: 'absolute', left: '50%', top: height / 2 - playSize / 2, marginLeft: -playSize / 2} : {position: 'absolute', bottom: 10, right: 10}} />}
    {!t.isTV && !selection?.active && (hovered || props.menuVisibility === 'always') && onMore && <CardAction name="i-more" label={defaultI18n.t('card.more', {title})} onPress={onMore} color={t.colors.text}
      style={{position: 'absolute', right: 4, top: 4, backgroundColor: t.colors.scrim}} />}
  </View>;
}

function Marks({progress, watched, isNew}: Media) {
  const t = useTheme(), inset = t.spec.card.badge.inset[t.platform];
  return <View pointerEvents="none" style={StyleSheet.absoluteFill}>
    {isNew && <View style={{position: 'absolute', left: inset, top: inset}}><Badge kind="new">{defaultI18n.t('next.kit.new')}</Badge></View>}
    {watched && <View accessibilityLabel={defaultI18n.t('card.watched')} style={{position: 'absolute', right: inset, top: inset}}><Badge kind="watched" /></View>}
    <Progress value={progress} style={{position: 'absolute', left: 0, right: 0, bottom: 0}} />
  </View>;
}

function Portrait({kind = 'poster', next, titleLines = 1, ...props}: PosterCardProps & {kind?: 'poster' | 'book' | 'periodical' | 'newspaper'; next?: boolean}) {
  const t = useTheme(), width = props.width ?? t.spec.card.width.poster[t.platform], height = width / (kind === 'periodical' || kind === 'newspaper' ? 3 / 4 : 2 / 3);
  const Frame = props.frame ?? CardFrame;
  return <Frame {...props} width={width} overlay={state => <Overlays props={props} hovered={state.hovered} holding={state.holding} height={height} />}>
    {state => <>
      <ArtFrame height={height} focused={state.focused} hovered={state.hovered} holding={state.holding} selected={props.selection?.selected || next}>
        <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} source={props.artwork} fallback={props.artworkFallback} kind={kind} width={width} height={height} />
        <Marks {...props} />
      </ArtFrame>
      <Caption {...props} selected={props.selection?.selected} focused={state.focused} tvRestingSecondary lines={titleLines} />
    </>}
  </Frame>;
}

export function PosterCard(props: PosterCardProps) {return <Portrait {...props} />;}
export function CoverCard(props: CoverCardProps) {
  const t = useTheme();
  if (t.isTV) return null;
  return <Portrait {...props} kind={props.kind ?? 'book'} />;
}

export function SquareCard(props: SquareCardProps) {
  const t = useTheme(), width = props.width ?? t.spec.card.width.square[t.platform];
  const Frame = props.frame ?? CardFrame;
  return <Frame {...props} width={width} overlay={s => <Overlays props={props} hovered={s.hovered} holding={s.holding} height={width} />}>
    {s => <><ArtFrame height={width} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected}>
      <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="square" source={props.artwork} fallback={props.artworkFallback} width={width} height={width} />
      <Scrim start={0.55} opacity={0.75} /><Marks watched={props.watched} isNew={props.isNew} />
      <Progress value={props.progress} height={t.isTV ? 6 : 3} style={{position: 'absolute', left: 8, right: 8, bottom: 8}} />
      {props.playing && <View pointerEvents="none" style={{position: 'absolute', left: 12, bottom: 12, flexDirection: 'row', alignItems: 'center', gap: 10,
        paddingVertical: 7, paddingHorizontal: 12, borderRadius: 9, backgroundColor: 'rgba(7,16,24,.82)'}}>
        <View accessible={false} importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', alignItems: 'flex-end', gap: 3, height: 16}}>
          {[.55, 1, .38].map((factor, index) => <View key={index} style={{width: 4, height: 16 * factor, borderRadius: 1.5, backgroundColor: t.colors.accentStrong}}/>)}
        </View>
        <Text style={{fontFamily: 'Manrope-ExtraBold', fontSize: 18 * t.textScale, lineHeight: 22 * t.textScale, fontWeight: '800', color: t.colors.text}}>{defaultI18n.t('player.miniPlaying')}</Text>
      </View>}
    </ArtFrame><Caption {...props} selected={props.selection?.selected} focused={s.focused} tvRestingSecondary titleTextStyle={props.playing ? {color: t.colors.accentStrong} : props.titleTextStyle} /></>}
  </Frame>;
}

export function WideCard(props: WideCardProps) {
  const t = useTheme(), width = props.width ?? t.spec.card.width.wide[t.platform], height = width * 9 / 16;
  const handheld = t.layoutMode === 'phone';
  // kit.css draws the on-art fact separator (.gap) at 12 px, 16 px on TV.
  const factsGap = t.isTV ? 16 : 12;
  const compactFacts = handheld ? [...(props.caption ?? []), ...(props.detail ?? [])].filter(part => part.id === 'episode-label' || part.id === 'remaining' || part.id === 'picture-in-picture') : undefined;
  return <CardFrame {...props} width={width} overlay={s => <Overlays props={props} hovered={s.hovered} holding={s.holding} height={height} playSize={52} centered />}>
    {s => <ArtFrame height={height} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected}>
      <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="wide" source={props.artwork} fallback={props.artworkFallback} width={width} height={height} />
      <Scrim />
      <View style={{position: 'absolute', left: t.spec.card.wide.insetX[t.platform], right: t.spec.card.wide.insetX[t.platform], bottom: t.spec.card.wide.insetBottom[t.platform]}}>
        <Text numberOfLines={1} style={[t.type.wideTitle, {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
        <View style={{marginTop: t.isTV ? 4 : 2}}><Parts parts={handheld ? compactFacts : props.caption} gap={factsGap} style={{color: t.colors.textSecondary}} /></View>
        {!handheld && !t.isTV && <Parts parts={props.detail} gap={factsGap} style={{color: t.colors.textSecondary}} />}
        <Progress value={props.progress} style={{marginTop: t.spec.card.progress.marginTop[t.platform]}} />
      </View>
    </ArtFrame>}
  </CardFrame>;
}

export function CircleCard(props: CircleCardProps) {
  const t = useTheme(), width = props.width ?? (t.isTV ? t.spec.card.width.artist.tv : 104);
  const Frame = props.frame ?? CardFrame;
  return <Frame {...props} width={width} overlay={s => <Overlays props={props} hovered={s.hovered} holding={s.holding} height={width} />}>
    {s => <><ArtFrame height={width} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected} circle>
      {props.person ? <PersonCircle {...props.art} visible={props.visible ?? props.art.visible} size={width} /> : <CircleArt {...props.art} visible={props.visible ?? props.art.visible} size={width} />}
    </ArtFrame><View style={{marginTop: t.isTV ? 0 : 1}}><Caption {...props} titleTextStyle={[titleStyle(t, t.isTV ? 22 : 14, 700), props.titleTextStyle]} captionTextStyle={[titleStyle(t, t.isTV ? 20 : 12.5, 600), props.captionTextStyle]} centered selected={props.selection?.selected} /></View></>}
  </Frame>;
}

export type EpisodeTileProps = CardProps & Media & {
  backdropFallback?: boolean;
  /** Canonical episodeLabel/cardText parts, including show context only where shows mix. */
  label: readonly FactPart[];
  length?: string;
  status?: string;
  synopsis?: string;
  layout?: 'tile' | 'row';
  /** The drawn still width of a row tile; the phone's own row is 136 pt, and a
   * page that draws a larger still (the tablet's watch page) passes its size. */
  artWidth?: number;
  onDownload?: () => void;
  /** tablet7: the episode this panel belongs to is open. */
  open?: boolean;
};

export function EpisodeTile(props: EpisodeTileProps) {
  const t = useTheme(), row = props.layout === 'row' || (!t.isTV && props.layout !== 'tile');
  const width = props.width ?? (row ? 350 : t.spec.episode.width[t.platform]), artWidth = row ? props.artWidth ?? 136 : width, height = artWidth * 9 / 16;
  const art = (focused: boolean, hovered: boolean, holding: boolean) => <ArtFrame width={artWidth} height={height} radius={row ? 8 : undefined} focused={focused} hovered={hovered} holding={holding} hoverScale={t.spec.card.hover.episodeScale} selected={props.selection?.selected || props.open}>
    <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="still" backdropFallback={props.backdropFallback} source={props.artwork} fallback={props.artworkFallback} width={artWidth} height={height} />
    {props.length && !row && <View style={{position: 'absolute', right: t.spec.episode.duration.inset[t.platform], top: t.spec.episode.duration.inset[t.platform]}}><Badge>{props.length}</Badge></View>}
    {props.status && !row && <View style={{position: 'absolute', left: t.isTV ? 16 : 8, top: t.isTV ? 14 : 8}}><Badge kind="new">{props.status}</Badge></View>}
    <Marks watched={props.watched} progress={props.progress} />
  </ArtFrame>;
  const text = <View style={{flex: row ? 1 : undefined, minWidth: 0, marginTop: row ? 0 : t.spec.episode.titleMargin[t.platform], gap: 3, paddingRight: row && (props.onDownload || props.onMore) ? 40 : 0}}>
    <Text numberOfLines={1} style={[row ? titleStyle(t, 15) : t.type.episodeTitle, {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
    <Parts parts={props.label} style={row ? titleStyle(t, 12.5, 600) : undefined} />
    {row && (props.length || props.status) && <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 12}}>
      {props.status && <Text style={[titleStyle(t, 12.5, 700), {color: t.colors.accentStrong}]}>{props.status}</Text>}
      {props.length && <Text style={[titleStyle(t, 12.5, 600), {color: t.colors.textTertiary}]}>{props.length}</Text>}
    </View>}
    <Parts parts={props.caption} /><Parts parts={props.detail} />
  </View>;
  return <CardFrame {...props} width={width}
    accessibilityDetails={[...(props.accessibilityDetails ?? []), ...props.label.map(part => part.text), props.status, props.length]}
    accessibilityActions={[...(props.accessibilityActions ?? []), ...(props.onDownload ? [{name: 'download', label: defaultI18n.t('next.cards.download')}] : [])]}
    onAccessibilityAction={event => event.nativeEvent.actionName === 'download' ? props.onDownload?.() : props.onAccessibilityAction?.(event)}
    overlay={s => row ? <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, top: 0, height}}>
    <View pointerEvents="box-none" style={{width: artWidth, height}}><Overlays props={{...props, onMore: undefined}} hovered={s.hovered} holding={s.holding} height={height} playSize={48} centered episode /></View>
    {!props.disabled && !props.selection?.active && (props.onDownload || props.onMore) && <CardAction name={props.onDownload ? 'i-dl' : 'i-more'} label={props.onDownload ? defaultI18n.t('next.cards.download') : defaultI18n.t('card.more', {title: props.title})} onPress={props.onDownload ?? props.onMore!} size={t.isTV ? 38 : 44} color={t.colors.textTertiary} style={{position: 'absolute', right: 0, top: (height - (t.isTV ? 38 : 44)) / 2, backgroundColor: 'transparent'}} />}
  </View> : <Overlays props={props} hovered={s.hovered} holding={s.holding} height={height} playSize={48} persistentPlay={!t.isTV} episode />}>
    {s => <><View style={{flexDirection: row ? 'row' : 'column', gap: row ? 14 : 0, alignItems: row ? 'center' : undefined}}>{art(s.focused, s.hovered, s.holding)}{text}</View>
      {props.synopsis && <Text numberOfLines={row ? 2 : 3} style={[row ? titleStyle(t, 13, 600) : t.type.episodeBody, {color: t.colors.textSecondary, marginTop: row ? 9 : 8, lineHeight: row ? 18.85 * t.textScale : undefined}]}>{props.synopsis}</Text>}
    </>}
  </CardFrame>;
}

export type RankedCardProps = PosterCardProps & {rank: number};
export function RankedCard({rank, width, ...props}: RankedCardProps) {
  const t = useTheme(), desktop = t.layoutMode === 'desktop', rankSpec = t.spec.shelf.rank;
  const numberWidth = desktop ? rankSpec.numberWidth : t.isTV ? 120 : 70, posterWidth = desktop ? rankSpec.posterWidth : t.isTV ? 214 : 116, numberHeight = desktop ? rankSpec.numberHeight : t.isTV ? 321 : 172;
  const fontSize = desktop ? t.type.rank.fontSize : t.isTV ? 190 : 120;
  return <View style={{width: width ?? (desktop ? rankSpec.width : t.isTV ? 318 : 186), flexDirection: 'row', alignItems: 'flex-start'}}>
    <Svg accessible={false} width={numberWidth} height={numberHeight} style={{marginRight: desktop ? rankSpec.overlap : -16}}><SvgText x={numberWidth} y={numberHeight - 2} textAnchor="end" fontFamily={t.type.rank.fontFamily} fontSize={fontSize} letterSpacing={desktop ? t.type.rank.letterSpacing : (t.isTV ? 190 : 120) * -0.08} fill={t.colors.ink} stroke={t.colors.lineStrong} strokeWidth={rankSpec.strokeWidth}>{rank}</SvgText></Svg>
    <PosterCard {...props} width={posterWidth} />
  </View>;
}

export {Overlays as CardOverlays};
