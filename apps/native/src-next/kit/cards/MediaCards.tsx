import React, {useState, type ReactNode} from 'react';
import {StyleSheet, Text, View} from 'react-native';
import Svg, {Text as SvgText} from 'react-native-svg';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Artwork, MediaArt, CircleArt, PersonCircle, GeneratedCoverClearance, missingArt, useWholeBox, type MissingArtShape, type ArtworkProps, type ArtworkSource, type CircleArtProps, type PersonCircleProps} from '../artwork/index';
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
export type CircleCardProps = CardProps & ({art: CircleArtProps; person?: false} | {art: PersonCircleProps; person: true}) & {
  /** The circle's diameter where the name and role under it need more room than the circle (a title page's cast:
   * phone-detail d1 frame 2, detail-tv-4). The circle is centred; the focus ring wraps only it; the card grows whole. */
  circle?: number};
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

function Marks({progress, watched, isNew, onNewBadge, insetProgress = false}: Media & {onNewBadge?: (bottom: number) => void; /** A poster's bar sits inside the art's edges (mock.html cards); a still's runs along its bottom edge. */ insetProgress?: boolean}) {
  const t = useTheme(), inset = t.spec.card.badge.inset[t.column];
  return <View pointerEvents="none" style={StyleSheet.absoluteFill}>
    {isNew && <View onLayout={event => onNewBadge?.(event.nativeEvent.layout.y + event.nativeEvent.layout.height)} style={{position: 'absolute', left: inset, top: inset}}><Badge kind="new">{defaultI18n.t('next.kit.new')}</Badge></View>}
    {watched && <View accessibilityLabel={defaultI18n.t('card.watched')} style={{position: 'absolute', right: inset, top: inset}}><Badge kind="watched" /></View>}
    <Progress value={progress} style={insetProgress ? {position: 'absolute', left: inset, right: inset, bottom: inset} : {position: 'absolute', left: 0, right: 0, bottom: 0}} />
  </View>;
}

/** Where a card's New badge ends, for a generated cover beneath it (W-1); a small gap below the badge. */
function useBadgeClearance(isNew: boolean | undefined) {
  const t = useTheme(), [bottom, setBottom] = useState(0);
  return [isNew && bottom > 0 ? bottom + t.spec.card.badge.inset[t.column] / 2 : 0, setBottom] as const;
}

/** A card with no picture served draws its generated cover (mock.html "No artwork"), unless the surface chose its own
 * fallback (null keeps the frame plain). A card that has a picture draws nothing under it. */
function fallbackFor(props: Media & {title: string}, shape: MissingArtShape, width: number, height: number): ReactNode {
  return props.artworkFallback !== undefined || props.artwork ? props.artworkFallback : missingArt({title: props.title, shape, width, height});
}

function Portrait({kind = 'poster', next, titleLines: suppliedLines, ...props}: PosterCardProps & {kind?: 'poster' | 'book' | 'periodical' | 'newspaper'; next?: boolean}) {
  const t = useTheme(), width = props.width ?? t.spec.card.width.poster[t.column], height = width / (kind === 'periodical' || kind === 'newspaper' ? 3 / 4 : 2 / 3);
  // A phone poster's title wraps to a second line before it is cut; a TV poster keeps its one line unless asked.
  const titleLines = suppliedLines ?? 2; // Rule 10 and the owner's no-cut rule: a poster title takes two lines on every form, TV too.
  const [clearance, setBadgeBottom] = useBadgeClearance(props.isNew);
  // Real art is never cut (owner): a poster slot's art of another shape (an audiobook's square cover in a mixed row) is drawn
  // whole and centred over its own blurred wash. Art within 8% of 2:3 fills the slot as before. Drawn whole, the art's own wash
  // is its only backing: the generated cover (the title in words) is not drawn under it, or its words show around the art.
  const art = props.artwork, [natural, setNatural] = useState<Readonly<{key: string; aspect: number}>>();
  const aspect = art && natural?.key === art.cacheKey ? natural.aspect : undefined;
  const whole = kind === 'poster' && aspect !== undefined && Math.abs(aspect / (2 / 3) - 1) > .08;
  const Frame = props.frame ?? CardFrame;
  return <Frame {...props} width={width} overlay={state => <Overlays props={props} hovered={state.hovered} holding={state.holding} height={height} />}>
    {state => <>
      <ArtFrame height={height} focused={state.focused} hovered={state.hovered} holding={state.holding} selected={props.selection?.selected || next}>
        {whole && <View pointerEvents="none" style={[StyleSheet.absoluteFillObject, {borderRadius: t.radii.artwork, overflow: 'hidden'}]}><Artwork source={art} alt="" blurRadius={18} brightness={.72} style={StyleSheet.absoluteFillObject}/></View>}
        <GeneratedCoverClearance.Provider value={clearance}>
          <MediaArt {...props.artworkProps} fit={whole ? 'contain' : props.artworkProps?.fit} style={whole ? {backgroundColor: 'transparent'} : undefined} onSize={art ? size => setNatural({key: art.cacheKey, aspect: size.width / size.height}) : undefined} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} source={props.artwork} fallback={whole ? undefined : fallbackFor(props, kind, width, height)} kind={kind} width={width} height={height} />
        </GeneratedCoverClearance.Provider>
        <Marks {...props} onNewBadge={setBadgeBottom} insetProgress />
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
  const t = useTheme(), width = props.width ?? t.spec.card.width.square[t.column];
  const Frame = props.frame ?? CardFrame;
  // A cover is never cropped (owner's rule): a 2:3 book cover keeps its shape, whole and centred in the square slot, and its
  // frame, ring and marks take that shape too, so no bars are drawn. TV keeps its square tiles.
  const {box, onSize} = useWholeBox(t.isTV ? undefined : props.artwork, width, width);
  return <Frame {...props} width={width} overlay={s => <Overlays props={props} hovered={s.hovered} holding={s.holding} height={width} />}>
    {s => <><View style={{width, height: width, alignItems: 'center', justifyContent: 'center'}}><ArtFrame width={box.width} height={box.height} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected}>
      <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="square" source={props.artwork} fallback={fallbackFor(props, 'square', width, width)} width={box.width} height={box.height} onSize={onSize} />
      <Scrim start={0.55} opacity={0.75} /><Marks watched={props.watched} isNew={props.isNew} />
      <Progress value={props.progress} height={t.isTV ? 6 : 3} style={{position: 'absolute', left: 8, right: 8, bottom: 8}} />
      {props.playing && <View pointerEvents="none" style={{position: 'absolute', left: 12, bottom: 12, flexDirection: 'row', alignItems: 'center', gap: 10,
        paddingVertical: 7, paddingHorizontal: 12, borderRadius: 9, backgroundColor: 'rgba(7,16,24,.82)'}}>
        <View accessible={false} importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', alignItems: 'flex-end', gap: 3, height: 16}}>
          {[.55, 1, .38].map((factor, index) => <View key={index} style={{width: 4, height: 16 * factor, borderRadius: 1.5, backgroundColor: t.colors.accentStrong}}/>)}
        </View>
        <Text style={{fontFamily: 'Manrope-ExtraBold', fontSize: 18 * t.textScale, lineHeight: 22 * t.textScale, fontWeight: '800', color: t.colors.text}}>{defaultI18n.t('player.miniPlaying')}</Text>
      </View>}
    </ArtFrame></View><Caption {...props} selected={props.selection?.selected} focused={s.focused} tvRestingSecondary titleTextStyle={props.playing ? {color: t.colors.accentStrong} : props.titleTextStyle} /></>}
  </Frame>;
}

/** Off TV a 16:9 card's title wraps rather than cut (owner, 13 Nov): three lines of 16 pt, with its facts and progress,
 * still sit inside the card's art (a 60-character title takes three on a phone's 262 pt card). */
export const wideTitleLines = 3;

export function WideCard(props: WideCardProps) {
  const t = useTheme(), width = props.width ?? t.spec.card.width.wide[t.column], height = width * 9 / 16;
  const handheld = t.layoutMode === 'phone', under = !handheld && !t.isTV && !!props.detail?.length;
  // kit.css draws the on-art fact separator (.gap) at 12 px, 16 px on TV.
  const factsGap = t.isTV ? 16 : 12;
  const compactFacts = handheld ? [...(props.caption ?? []), ...(props.detail ?? [])].filter(part => part.id === 'episode-label' || part.id === 'remaining' || part.id === 'picture-in-picture') : undefined;
  // The scrim grows with the words on it: when the title takes two lines (or text is larger), the shade starts 20 pt above
  // them rather than at the card's middle, so the first line is not left on bright art. Never shorter than the drawn .45.
  const [copyTop, setCopyTop] = useState<number>();
  const scrimStart = t.isTV || copyTop === undefined ? undefined : Math.max(0, Math.min(.45, (copyTop - 20) / height));
  return <CardFrame {...props} width={width} overlay={s => <Overlays props={props} hovered={s.hovered} holding={s.holding} height={height} playSize={52} centered />}>
    {s => <><ArtFrame height={height} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected}>
      <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="wide" source={props.artwork} fallback={fallbackFor(props, 'wide', width, height)} width={width} height={height} />
      <Scrim start={scrimStart} />
      <View onLayout={t.isTV ? undefined : event => {const top = event.nativeEvent.layout.y; if (top !== copyTop) setCopyTop(top);}} style={{position: 'absolute', left: t.spec.card.wide.insetX[t.column], right: t.spec.card.wide.insetX[t.column], bottom: t.spec.card.wide.insetBottom[t.column]}}>
        <Text numberOfLines={t.isTV ? 1 : wideTitleLines} style={[t.type.wideTitle, {color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
        <View style={{marginTop: t.isTV ? 4 : 2}}><Parts parts={handheld ? compactFacts : props.caption} gap={factsGap} style={{color: t.colors.textSecondary}} /></View>
        <Progress value={props.progress} style={{marginTop: t.spec.card.progress.marginTop[t.column]}} />
      </View>
    </ArtFrame>{under && <View style={{marginTop: wideDetailGap}}><Parts parts={props.detail} gap={factsGap} /></View>}</>}
  </CardFrame>;
}
/** A wide card's detail (Continue Watching's time left) sits under the picture on a tablet, in the caption grey, as on the web
 * (tablet 1, desktop-browse f1); a phone keeps it inside with the number (phone-app 4b), TV leaves it to the hero. A row of
 * wide cards adds this to its item height when its cards carry a detail. */
export function wideDetailHeight(t: ReturnType<typeof useTheme>) {
  return t.layoutMode === 'phone' || t.isTV ? 0 : wideDetailGap + Math.ceil(t.type.cardSubtitle.lineHeight ?? (t.type.cardSubtitle.fontSize ?? 13) * 1.4);
}
const wideDetailGap = 8;

export function CircleCard(props: CircleCardProps) {
  const t = useTheme(), width = props.width ?? (t.isTV ? t.spec.card.width.artist.tv : 104), art = props.circle ?? width;
  const Frame = props.frame ?? CardFrame;
  return <Frame {...props} width={width} overlay={s => <Overlays props={props} hovered={s.hovered} holding={s.holding} height={art} />}>
    {s => <><ArtFrame width={props.circle} height={art} focused={s.focused} hovered={s.hovered} holding={s.holding} selected={props.selection?.selected} circle style={props.circle ? {alignSelf: 'center'} : undefined}>
      {props.person ? <PersonCircle {...props.art} visible={props.visible ?? props.art.visible} size={art} /> : <CircleArt {...props.art} visible={props.visible ?? props.art.visible} size={art} />}
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
  // Rule R7: a row on a tablet is its own layout, not the phone's widened: the synopsis beside the picture under the
  // title, the length on the picture (tablet f8, tablet-play 3; tablets.md SC-5).
  const wide = row && t.column === 'tablet';
  const width = props.width ?? (row ? 350 : t.spec.episode.width[t.column]), artWidth = row ? props.artWidth ?? 136 : width, height = artWidth * 9 / 16;
  // Where the length sits on the picture (a tile, a tablet row), Playing or Watched is a labelled chip at the picture's
  // top left (desktop-player f6, tablet-play 2a), so nothing shares the length's corner. A tile's own status keeps its badge.
  // A row's facts are the form's `meta` role at the row's weight (R2: by form, so a TV row is TV-sized).
  const meta = (weight: 600 | 700) => {const {fontFamily, fontWeight} = titleStyle(t, 1, weight); return [t.type.meta, {fontFamily, fontWeight}];};
  const pictured = !row || wide, playingBars = (color: string) => [.55, 1, .38].map((factor, index) => <View key={index} style={{width: 3, height: 12 * factor, borderRadius: 1.5, backgroundColor: color}}/>);
  const chip = !pictured ? undefined : props.status && !row ? <Badge kind="new">{props.status}</Badge>
    : props.playing ? <Badge kind="playing">{defaultI18n.t('next.dock.playing')}</Badge> : props.watched ? <Badge kind="watched">{defaultI18n.t('next.cards.watched')}</Badge> : undefined;
  const art = (focused: boolean, hovered: boolean, holding: boolean) => <ArtFrame width={artWidth} height={height} radius={row ? 8 : undefined} focused={focused} hovered={hovered} holding={holding} hoverScale={t.spec.card.hover.episodeScale} selected={props.selection?.selected || props.open}>
    <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="still" backdropFallback={props.backdropFallback} source={props.artwork} fallback={fallbackFor(props, 'still', artWidth, height)} width={artWidth} height={height} />
    {/* The length's corner is the top right; on a row's small picture a status chip takes the top, so the length sits at the bottom right (tablet-play 2a). */}
    {props.length && pictured && <View style={[{position: 'absolute', right: t.spec.episode.duration.inset[t.column]}, row && chip
      ? {bottom: t.spec.episode.duration.inset[t.column] + t.spec.card.progress.height[t.column]} : {top: t.spec.episode.duration.inset[t.column]}]}><Badge>{props.length}</Badge></View>}
    {chip && <View style={{position: 'absolute', left: t.isTV ? 16 : 8, top: t.isTV ? 14 : 8}}>{chip}</View>}
    <Marks watched={props.watched && !pictured} progress={props.progress} />
    {/* A phone's row: the episode that is playing carries the playing mark on its picture (phone-player 4). */}
    {props.playing && row && !wide && <View pointerEvents="none" style={{position: 'absolute', right: t.spec.card.badge.inset[t.column], top: t.spec.card.badge.inset[t.column], flexDirection: 'row', alignItems: 'flex-end', gap: 2, padding: 5, borderRadius: 6, backgroundColor: 'rgba(7,16,24,.82)'}}>
      {playingBars(t.colors.accentStrong)}</View>}
  </ArtFrame>;
  const text = <View style={{flex: row ? 1 : undefined, minWidth: 0, marginTop: row ? 0 : t.spec.episode.titleMargin[t.column], gap: 3, paddingRight: row && (props.onDownload || props.onMore) ? 40 : 0}}>
    <Text numberOfLines={row && !t.isTV ? 2 : 1} style={[t.type.episodeTitle, {color: props.selection?.selected || props.playing ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
    {/* A tablet row's length is on its picture, so its status leads the number's line ("Up next  Episode 3", tablet-play 2a);
        a phone's row keeps the status beside the length under the number (phone-player 4). */}
    {wide && props.status && !(props.playing || props.watched)
      ? <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 12, minWidth: 0}}><Text style={[meta(700), {color: t.colors.accentStrong}]}>{props.status}</Text>
        <View style={{flex: 1, minWidth: 0}}><Parts parts={props.label} fit="cut" style={meta(600)} /></View></View>
      : <Parts parts={props.label} fit={row && !t.isTV ? 'wrap' : 'cut'} style={row ? meta(600) : undefined} />}
    {row && !wide && (props.length || props.status) && <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 12}}>
      {props.status && <Text style={[meta(700), {color: t.colors.accentStrong}]}>{props.status}</Text>}
      {props.length && <Text style={[meta(600), {color: t.colors.textTertiary}]}>{props.length}</Text>}
    </View>}
    <Parts parts={props.caption} /><Parts parts={props.detail} />
    {wide && props.synopsis && <Text numberOfLines={3} style={[t.type.episodeBody, {color: t.colors.textSecondary, marginTop: 4}]}>{props.synopsis}</Text>}
  </View>;
  return <CardFrame {...props} width={width}
    accessibilityDetails={[...(props.accessibilityDetails ?? []), ...props.label.map(part => part.text), props.status, props.length]}
    accessibilityActions={[...(props.accessibilityActions ?? []), ...(props.onDownload ? [{name: 'download', label: defaultI18n.t('next.cards.download')}] : [])]}
    onAccessibilityAction={event => event.nativeEvent.actionName === 'download' ? props.onDownload?.() : props.onAccessibilityAction?.(event)}
    overlay={s => row ? <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, top: 0, height}}>
    <View pointerEvents="box-none" style={{width: artWidth, height}}><Overlays props={{...props, onMore: undefined}} hovered={s.hovered} holding={s.holding} height={height} playSize={48} centered episode /></View>
    {!props.disabled && !props.selection?.active && (props.onDownload || props.onMore) && <CardAction name={props.onDownload ? 'i-dl' : 'i-more'} label={props.onDownload ? defaultI18n.t('next.cards.download') : defaultI18n.t('card.more', {title: props.title})} onPress={props.onDownload ?? props.onMore!} size={t.isTV ? 38 : 44} color={t.colors.textTertiary} style={{position: 'absolute', right: 0, top: (height - (t.isTV ? 38 : 44)) / 2, backgroundColor: 'transparent'}} />}
  </View> : <Overlays props={props} hovered={s.hovered} holding={s.holding} height={height} playSize={48} persistentPlay={!t.isTV} episode />}>
    {s => <><View style={[{flexDirection: row ? 'row' : 'column', gap: row ? wide ? 18 : 14 : 0, alignItems: wide ? 'flex-start' : row ? 'center' : undefined},
      /* A tablet row that is playing is raised (tablet-play 2a). */ wide && props.playing && {padding: t.space.xs, margin: -t.space.xs, borderRadius: t.spec.card.badge.radius[t.column] * 2, backgroundColor: t.colors.raisedHigh}]}>{art(s.focused, s.hovered, s.holding)}{text}</View>
      {props.synopsis && !wide && <Text numberOfLines={row ? 2 : 3} style={[t.type.episodeBody, {color: t.colors.textSecondary, marginTop: row ? 9 : 8}]}>{props.synopsis}</Text>}
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
