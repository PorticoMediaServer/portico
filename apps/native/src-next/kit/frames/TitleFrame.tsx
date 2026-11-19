import React, {Children, isValidElement, useEffect, useRef, useState, type ReactNode} from 'react';
import {ScrollView, StyleSheet, TVFocusGuideView, Text, View, useWindowDimensions, type FocusGuideMethods, type ViewStyle} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {frameSpec} from '@portico/design';
import {Button} from '../controls/Button';
import {ActionRow, type RowAction, type RowPrimary} from '../controls/ActionRow';
import {Tabs, type TabItem} from '../controls/Tabs';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {Lockup} from '../layout/Lockup';
import {BarSurface, RoundBarButton, roundBarSize, type TopBarBack} from '../layout/TopBar';
import {alpha} from '../shell/visual';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {buttonGeometry} from '../controls/control-model';
import {useTheme} from '../theme';
import {FocusWithin, StartFocusScope} from '../focus';
import {useMotion} from '../useMotion';
import {useFrameMetrics} from './metrics';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {seasonColumn, type AboutValue} from './title-parts';

/** A title page's buttons: the kit `ActionRow`'s (round on a phone and an upright tablet, labelled where `labelled` says
 * so on a tablet on its side and TV). */
export type TitleAction = RowAction;
/** The one flexible button. With `progress` it is Resume. On a tablet on its side and TV the time left is beside its
 * label, the played part runs along its lower edge and Start over is the button after it (detail-desktop 2, detail-tv 2).
 * On a phone, an upright tablet and a cover page the played part, the time left and Start over are one line under the
 * action row (detail-phone 3, desktop-listen book). */
export type TitlePrimary = Omit<RowPrimary, 'focusId'>;
/** One part of the credits line: "Directed by" and the names; a name with `onPress` opens its page. */
export type TitleCredit = Readonly<{label: string; names: string | readonly AboutValue[]}>;
/** The line above the title (desktop-detail f1 "Dune collection · 2 films", f7 "Acting", desktop-listen "Album"). */
export type TitleKicker = Readonly<{label: string; icon?: IconName; detail?: string; onPress?: () => void}>;
/** A person's, an artist's, an album's or a book's own picture, beside the words in place of the art box. The frame
 * sizes it per form; a picture that draws to a size (a person's initials) takes it as a function of the width. */
export type TitleCover = Readonly<{node: ReactNode | ((size: number) => ReactNode); shape: 'poster' | 'square' | 'circle'}>;
/** Where the collapsed bar shows, for a page's own scroller (`MusicScrollView`'s `collapseAt` and `onCollapse`). */
export type TitleCollapse = Readonly<{at: number; onChange: (collapsed: boolean) => void}>;
export type TitleFrameProps = Readonly<{
  /** The picture behind the words, as the kit's `AmbientArt` with `treatment="title"`. The frame gives it the box. */
  art?: ReactNode;
  /** In place of `art`: the cover beside the words (a poster or square at the left, a circle at the right). */
  cover?: TitleCover;
  /** A glass disc over the art. Not drawn on TV, where the remote's Back leaves the page. */
  back: TopBarBack;
  /** One glass disc opposite Back (Play on). */
  topAction?: TitleAction;
  /** The shell's own control in the top-action place, drawn in place of `topAction` on the art and on the collapsed bar:
   * Play on (`PlayOnButton variant="round"`), which names and marks the device it will play on. */
  topControl?: ReactNode;
  /** The art box, measured by the page when Play opens the player from it (the start motion grows the art into the player). */
  artRef?: React.RefObject<View | null>;
  /** Where the page was scrolled: the frame scrolls back there once the page is long enough, and reports each new place. */
  scrollMemory?: Readonly<{offset: number; onOffset: (y: number) => void}>;
  /** The page's selection bar while titles in its panel are selected (Select in a card's menu), at the top of the panel. */
  selectionBar?: ReactNode;
  kicker?: TitleKicker;
  title: string;
  /** The metadata line (`HeroMetadata`). Its line is kept when there is nothing to show. */
  metadata?: ReactNode;
  /** The audio and subtitles for the next play (`TrackPicker`), under the synopsis on a phone and an upright tablet. */
  picker?: ReactNode;
  /** The one flexible button: it takes the room the round buttons leave. A person has none. */
  primary?: TitlePrimary;
  /** The buttons after it, in fixed slots. One that the title doesn't support stays in its slot with `disabled`. */
  actions?: readonly TitleAction[];
  /** One bold line over the synopsis, on a tablet on its side and TV. */
  tagline?: string;
  synopsis?: string;
  /** A biography's Read more and where it is from ("Biography from TMDB"). */
  readMore?: Readonly<{label: string; onPress: () => void; note?: string}>;
  /** "Directed by" and "Starring", on a tablet on its side and TV. */
  credits?: readonly TitleCredit[];
  /** The cast circles on the left page when an open foldable lies on its side (foldable f2). Elsewhere the cast is in Details. */
  cast?: ReactNode;
  tabs?: Readonly<{items: readonly TabItem[]; value: string; onValueChange: (value: string) => void}>;
  /** TV, a show's or a season's Episodes tab (detail-tv-5a, 5b): the season column (`SeasonListRow`s and the Hold OK
   * hint) at the left, and beside it the episodes and the band that describes what holds focus. The frame places it
   * at the top of the panel; the rest of the panel (More like this) runs under both at full width. Touch forms draw the
   * season in the panel's `SeasonBar` instead. */
  seasons?: Readonly<{column: ReactNode; episodes: ReactNode; tab?: string}>;
  /** The tab panel: the one flexible region. On TV every section is drawn and focus walks down them; mark each with
   * `TitleSection` so the tabs follow the one that holds focus. */
  children?: ReactNode;
  /** Beside the panel on a tablet on its side and TV (a collection's About, an album's Details); under it otherwise. */
  aside?: ReactNode;
  /** The bar that replaces the hero (desktop-detail f3, phone-detail d1 frame 2, detail-tv-4). The frame sets it itself:
   * on touch from the scroll, on TV when focus moves into the panel. A screen sets it only to hold it (a tab opened directly). */
  collapsed?: boolean;
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
  /** The page's own scroller, in place of the frame's on every form (an album's and artist's song list windows its rows and
   * pages the server near the end: `MusicScrollView`). The frame hands it what to scroll, the content style to put on it
   * and where the collapsed bar shows: past `collapse.at` the scroller calls `collapse.onChange(true)`, back above it
   * `false`. On an open foldable it scrolls the right page (the tabs and their panel). */
  scroller?: (content: ReactNode, contentContainerStyle: ViewStyle, collapse: TitleCollapse) => ReactNode;
  testID?: string;
  /** TV: the control the page starts on (R1), by `focusId` or `testID`. Left out, it is the primary (Play or Resume). */
  startFocus?: string;
}>;

/** Frame 2, a title page: a film, show, season, episode, person, collection, album, artist or book. Its regions are
 * `frameRegions.title` (packages/design). Fixed: the art or cover, Back, the kicker, the title slot (the title sits on
 * its bottom edge and grows upward over the art), the metadata line, the action row, the tabs and the collapsed bar.
 * Flexible: the words and the tab panel. Forms: a phone and an upright tablet are one centred column (ruling 6); an open
 * foldable on its side is two pages; a tablet on its side and TV put the words over the art at the left. The frame
 * draws no page colour: the layer's backdrop shows through it (R10). Loading draws the same boxes. */
export function TitleFrame({startFocus, ...props}: TitleFrameProps) {
  return <StartFocusScope id={startFocus ?? props.primary?.testID ?? titlePrimaryId}><TitlePage {...props}/></StartFocusScope>;
}

/** One section of a title page's panel (More like this, Details, a related row), named by the tab it belongs to. On TV,
 * where every section is drawn, the tabs mark the section that holds focus; elsewhere it draws its children as they are. */
export function TitleSection({children}: Readonly<{tab: string; children?: ReactNode}>) {return <>{children}</>;}

/** The primary's own focus id when the screen gives it no test id. */
const titlePrimaryId = 'title-primary';
/** How far the folded head's words fade: almost away, never to zero (tvOS will not focus a transparent view). */
const tvFoldedCopy = .03;
function TitlePage({art, cover, back, topAction, kicker, title, metadata, picker, primary, actions = [], tagline, synopsis, readMore, credits, cast, tabs, seasons, children, aside, collapsed, state, scroller, topControl, artRef, scrollMemory, selectionBar, testID}: Omit<TitleFrameProps, 'startFocus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), spec = frameSpec.hero.title;
  const {form} = frame, tv = form === 'tv', split = form === 'medium' && window.width > window.height, stacked = form === 'phone' || form === 'medium';
  const [passed, setPassed] = useState(false), [tabsPinned, setTabsPinned] = useState(false), [tabsY, setTabsY] = useState(Number.POSITIVE_INFINITY), [heroEnd, setHeroEnd] = useState(Number.POSITIVE_INFINITY);
  // TV (detail-tv-3): focus moving into the panel folds the hero to the title and the tabs at the top, the art dims, and
  // the section that holds focus rises to just under them; focus back on the buttons unfolds it. The head stays laid out
  // (it scrolls under the band, its words faded), so Up from the first row finds the buttons again. `where` is the head or a section's index; focus
  // leaving the page (the rail, an overlay) keeps it. A page with its own scroller (an album's songs) scrolls itself and
  // does not fold.
  const motion = useMotion(), [where, setWhere] = useState<'head' | number>('head');
  const folded = tv && !scroller && (collapsed ?? where !== 'head');
  const restored = useRef(false), scroll = useRef<ScrollView>(null), ys = useRef({content: 0, column: 0, sections: [] as number[]}), [bandHeight, setBandHeight] = useState(0);
  const fold = useSharedValue(0);
  // tvOS moves focus only to what is on screen, and folded, the buttons are scrolled away above: a guide over the first
  // section sends Up to Play (or, with no Play, into the head). It is live only while folded, so Down from Play is untouched.
  const primaryView = useRef<View>(null), headView = useRef<View>(null), upGuide = useRef<View & FocusGuideMethods>(null);
  useEffect(() => {
    const target = primaryView.current ?? headView.current;
    upGuide.current?.setDestinations(folded && target ? [target] : []);
  }, [folded]);
  useEffect(() => {fold.value = withTiming(folded ? 1 : 0, motion.timing('standard'));}, [fold, folded, motion]);
  // The words fade as the page folds, so they never show through the band; never to nothing, since tvOS will not move
  // focus to a fully transparent view and Up must still reach Play.
  const copyStyle = useAnimatedStyle(() => ({opacity: 1 - fold.value * (1 - tvFoldedCopy)}));
  const bandStyle = useAnimatedStyle(() => ({opacity: fold.value}));
  // Folded, a scrim of ink deepens over the whole page (the art's own opacity is left alone: on Android it would drop the
  // art's faded edge and leave a hard line where the art box ends).
  const scrimStyle = useAnimatedStyle(() => ({opacity: fold.value * (1 - frameSpec.hero.title.tvCollapsedArt)}));
  const align = () => {
    if (!tv || scroller) return;
    const y = where === 'head' ? 0 : ys.current.content + ys.current.column + (ys.current.sections[where] ?? 0) - bandHeight;
    scroll.current?.scrollTo({y: Math.max(0, y), animated: !motion.reduce});
  };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(align, [where, bandHeight]);
  // The tabs follow the section that holds focus.
  const sectionTabs = useRef<readonly (string | undefined)[]>([]);
  useEffect(() => {
    const tab = typeof where === 'number' ? sectionTabs.current[where] : undefined;
    if (tabs && tab !== undefined && tab !== tabs.value) tabs.onValueChange(tab);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [where]);
  const failure = state && 'title' in state ? state : undefined, loading = !!state && !failure;
  if (failure) return <View testID={testID} style={{flex: 1}}>
    {!tv && <View style={{position: 'absolute', zIndex: theme.layers.sticky, left: theme.space.sm, top: insets.top + theme.space.tight}}><RoundBarButton icon="i-left" label={back.label} onPress={back.onPress} testID={back.testID}/></View>}
    <PageStateFrame {...failure}/>
  </View>;
  const height = split ? spec.height.phone : spec.height[form], lockup = split ? spec.lockup.phone : spec.lockup[form];
  const discTop = insets.top + theme.space.tight, discRow = discTop + roundBarSize + theme.space.sm;
  const centre = stacked ? 'center' as const : 'flex-start' as const;

  const kickerWords = kicker && <>
    <Text accessibilityRole={kicker.onPress && !tv ? 'link' : undefined} onPress={tv ? undefined : kicker.onPress} style={[theme.type.rowHelp, {color: kicker.onPress || kicker.icon ? theme.colors.accentStrong : theme.colors.textSecondary, fontFamily: theme.type.rowLabel.fontFamily}]}>
      {kicker.icon ? <><Icon name={kicker.icon} size={tv ? theme.scale.icon.tertiary.size : 16} color={theme.colors.accentStrong}/>{' '}</> : null}{kicker.label}</Text>
    {kicker.detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.rowLabel.fontFamily}]}>{kicker.detail}</Text>}
  </>;
  const kickerRow = {flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: centre, gap: theme.space.controlGap} as const;
  // On TV a kicker that opens something (the collection, tv-detail frame 1) is a focus stop of its own, above the buttons:
  // text alone takes no D-pad focus.
  const kickerLine = kicker && (tv && kicker.onPress
    ? <Focusable focusId="title-kicker" testID="title-kicker" accessibilityRole="link" accessibilityLabel={[kicker.label, kicker.detail].filter(Boolean).join(', ')} onPress={kicker.onPress}
      // The ring is drawn outside the line: its room comes on top of the gap, so it never touches the title (R1).
      style={[kickerRow, {alignSelf: 'flex-start', marginBottom: theme.space.controlGap + theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv}]}>{kickerWords}</Focusable>
    : <View style={[kickerRow, {marginBottom: theme.space.controlGap}]}>{kickerWords}</View>);
  const identity = <>
    {kickerLine}
    {loading && !title ? <SkeletonBlock width="70%" height={lockup}/> : <Lockup title={title} size={lockup} lineRatio={frameSpec.hero.lockupLine} tracking={frameSpec.hero.lockupTracking} lines={tv ? 3 : undefined} style={{textAlign: stacked ? 'center' : 'left'}}/>}
    <View style={{minHeight: theme.type.rowLabel.lineHeight, marginTop: theme.space.controlGap, alignItems: centre, justifyContent: 'center'}}>{loading ? <SkeletonBlock width="40%" height={theme.type.meta.fontSize}/> : metadata}</View>
  </>;
  const names = (value: TitleCredit['names']) => typeof value === 'string' ? value : value.map((name, index) => <Text key={index}>{index > 0 ? ', ' : ''}
    {name.onPress && !theme.isTV ? <Text accessibilityRole="link" onPress={name.onPress} style={{textDecorationLine: 'underline', textDecorationColor: alpha(theme.colors.text, .35)}}>{name.text}</Text> : name.text}</Text>);
  // The synopsis reads from the left on every form, under a centred title on a phone too (detail-phone d1 frames 1 and 3,
  // as the web page at phone width). Loading keeps the loaded page's boxes (rule 12): the synopsis's lines, so the tabs and the panel do not move when the words arrive.
  const words = loading ? <View style={{gap: spec.creditsGap}}>
    {Array.from({length: spec.synopsisLines[form]}, (_, index) => <SkeletonBlock key={index} width={index === 0 ? '96%' : '70%'} height={theme.type.intro.lineHeight ?? theme.type.intro.fontSize}/>)}
  </View> : (tagline !== undefined || synopsis !== undefined || credits?.length) ? <View style={{gap: spec.creditsGap, maxWidth: stacked ? undefined : theme.spec.hero.copy.titleWidth[tv ? 'tv' : 'web']}}>
    {!stacked && tagline !== undefined && <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{tagline}</Text>}
    {synopsis !== undefined && <Text numberOfLines={readMore ? spec.readMoreLines : spec.synopsisLines[form]} style={[theme.type.intro, {color: theme.colors.textSecondary, maxWidth: frameSpec.settings.paragraphMax}]}>{synopsis}</Text>}
    {readMore && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.md}}>
      <Button variant="text" size="xs" trailingIcon="i-chdown" onPress={readMore.onPress}>{readMore.label}</Button>
      {readMore.note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{readMore.note}</Text>}
    </View>}
    {!stacked && credits && credits.length > 0 && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>
      {credits.map((credit, index) => <Text key={credit.label}>{index > 0 ? '    ' : ''}{credit.label} <Text style={{color: theme.colors.textSecondary}}>{names(credit.names)}</Text></Text>)}</Text>}
  </View> : null;

  // The played part and the time left are in the button on a tablet on its side and TV; under the row elsewhere, with Start over.
  const resume = primary?.progress !== undefined && !loading, lineUnder = resume && (stacked || !!cover);
  const row = (primary || actions.length > 0) && <ActionRow loading={loading} actions={actions} primaryRef={primaryView} primary={primary && {...primary, focusId: primary.testID ?? titlePrimaryId,
    progress: lineUnder ? undefined : primary.progress, startOver: stacked ? undefined : primary.startOver}}/>;
  const track = theme.spec.card.progress.height[theme.column];
  const resumeLine = lineUnder ? <View style={{gap: theme.space.tight, width: '100%', maxWidth: cover && !stacked ? spec.coverResumeWidth : undefined}}>
    <View accessibilityRole="progressbar" accessibilityValue={{min: 0, max: 1, now: primary!.progress}} style={{height: track, borderRadius: track, overflow: 'hidden', backgroundColor: alpha(theme.colors.text, theme.spec.card.progress.trackAlpha)}}>
      <View style={{width: `${primary!.progress! * 100}%`, height: track, backgroundColor: theme.colors.accentStrong}}/></View>
    {/* phone-detail d1 frame 3 `.rsm .rl`: the time left in the secondary caption, Start over a link whose right edge is the bar's. */}
    <View style={{minHeight: buttonGeometry('xs', theme.column, 'link').height, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}><Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{primary!.remaining}</Text>
      {primary!.startOver && stacked && <Button variant="link" size="xs" icon="i-restart" onPress={primary!.startOver.onPress}>{primary!.startOver.label}</Button>}</View>
  </View> : null;
  const pickerRow = picker && stacked && !loading ? <View style={{alignSelf: 'stretch'}}>{picker}</View> : null;

  // The head: the art box with the words hanging from its bottom edge, or the cover beside them.
  const discs = !tv && <View pointerEvents="box-none" style={{position: 'absolute', zIndex: theme.layers.sticky, left: theme.space.sm, right: theme.space.sm, top: discTop, flexDirection: 'row', justifyContent: 'space-between'}}>
    <RoundBarButton icon="i-left" label={back.label} onPress={back.onPress} testID={back.testID}/>
    {topControl ?? (topAction && <RoundBarButton icon={topAction.icon} label={topAction.label} onPress={topAction.onPress} disabled={topAction.disabled} testID={topAction.testID}/>)}
  </View>;
  const body = <View style={{gap: theme.space.sm, alignItems: stacked ? 'center' : 'flex-start', alignSelf: 'stretch'}}>{stacked ? <>{row}{resumeLine}{words}{pickerRow}</> : <>{words}<View style={{marginTop: spec.actionGap[form] - theme.space.sm, gap: theme.space.sm}}>{row}{resumeLine}</View></>}</View>;
  const coverSize = spec.cover[cover?.shape ?? 'square'][split ? 'phone' : form];
  const coverBox = cover && <View style={{width: coverSize, height: cover.shape === 'poster' ? coverSize * 1.5 : coverSize, borderRadius: cover.shape === 'circle' ? coverSize / 2 : frame.cardRadius, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{typeof cover.node === 'function' ? cover.node(coverSize) : cover.node}</View>;
  const head = cover ? stacked
    ? <View style={{paddingTop: discRow, paddingHorizontal: frame.gutter, alignItems: 'center', gap: theme.space.sm}}>{split && discs}{coverBox}<View style={{alignSelf: 'stretch'}}>{identity}</View><View style={{alignSelf: 'stretch', marginTop: spec.actionGap.phone - theme.space.sm}}>{body}</View></View>
    : <View style={{paddingTop: tv ? frame.titleTop : discRow, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, flexDirection: cover.shape === 'circle' ? 'row-reverse' : 'row', alignItems: 'center', gap: spec.coverGap}}>
      {split && discs}{coverBox}<View style={{flex: 1, minWidth: 0}}>{identity}<View style={{marginTop: theme.space.sm}}>{body}</View></View></View>
    // A tablet on its side and TV: the art box is at least its drawn height and grows with its words, so a long title or a
    // second line of buttons pushes the panel down rather than rising under Back (the words never overlap the discs).
    : <><View onLayout={event => setHeroEnd(event.nativeEvent.layout.y + event.nativeEvent.layout.height)} style={stacked ? {height} : {minHeight: height, justifyContent: 'flex-end', paddingTop: tv ? frame.titleTop : discRow, paddingBottom: spec.copyBottom[form]}}>
      <View ref={artRef} collapsable={false} pointerEvents="box-none" style={StyleSheet.absoluteFill}>{!tv && art}
        {/* A tablet's words are over the picture, so the picture is shaded behind them (a phone's words are under its picture and a TV's art has its own). */}
        {!stacked && !tv && <Svg pointerEvents="none" accessible={false} width="100%" height="100%" style={StyleSheet.absoluteFill}>
          <Defs>
            <LinearGradient id="title-shade-side" x1="0" y1="0" x2="1" y2="0">{spec.shade.side.map(([opacity, at]) => <Stop key={at} offset={at} stopColor={theme.colors.ink} stopOpacity={opacity}/>)}</LinearGradient>
            <LinearGradient id="title-shade-foot" x1="0" y1="1" x2="0" y2="0">{spec.shade.foot.map(([opacity, at]) => <Stop key={at} offset={at} stopColor={theme.colors.ink} stopOpacity={opacity}/>)}</LinearGradient>
          </Defs>
          <Rect width="100%" height="100%" fill="url(#title-shade-side)"/><Rect width="100%" height="100%" fill="url(#title-shade-foot)"/>
        </Svg>}
      </View>
      {split && discs}
      {/* Everything in the art box hangs from its bottom edge, so a longer title grows upward and the row below never moves. */}
      <View pointerEvents="box-none" style={stacked ? {position: 'absolute', left: frame.gutter, right: frame.gutter, bottom: split ? 0 : spec.copyBottom[form]} : {paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
        <View style={stacked || tv ? undefined : {maxWidth: theme.spec.hero.copy.titleWidth.web}}>{identity}</View>
        {/* The words keep the copy width; the action row takes the page's width, so its buttons stay on one line (tablet f6, detail-tv-2). */}
        {!stacked && <View style={{marginTop: theme.space.sm}}>{body}</View>}
      </View>
    </View>
    {stacked && <View style={{paddingHorizontal: frame.gutter, marginTop: spec.actionGap[form]}}>{body}</View>}</>;

  const posterWidth = theme.spec.card.width.poster[theme.column];
  const panel = loading ? <View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', gap: theme.space.sm, paddingLeft: frame.gutter, overflow: 'hidden'}}>
    {Array.from({length: 6}, (_, index) => <SkeletonBlock key={index} width={posterWidth} height={posterWidth * 1.5} shine={index === 0}/>)}</View> : children;
  const besideAside = !!aside && !stacked && !loading;
  // TV: the seasons and each of the panel's sections, in the order focus walks them.
  const sections = !tv ? [] : [...(seasons ? [{tab: seasons.tab, node: <View style={{flexDirection: 'row', gap: seasonColumn.gap, paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
    <View style={{width: seasonColumn.width}}>{seasons.column}</View><View style={{flex: 1, minWidth: 0}}>{seasons.episodes}</View></View>}] : []),
    ...Children.toArray(children).map(node => ({tab: isValidElement<{tab?: string}>(node) && node.type === TitleSection ? node.props.tab : undefined, node}))];
  sectionTabs.current = sections.map(section => section.tab);
  const content = <View onLayout={event => {ys.current.content = event.nativeEvent.layout.y;}} style={{marginTop: tabs ? frame.headerGap + frame.headingGap : frame.sectionGap, flexDirection: besideAside ? 'row' : 'column', alignItems: 'flex-start', gap: besideAside ? theme.space.xl : frame.sectionGap}}>
    <View onLayout={event => {ys.current.column = event.nativeEvent.layout.y;}} style={{flex: besideAside ? 1 : undefined, alignSelf: besideAside ? undefined : 'stretch', minWidth: 0, gap: frame.sectionGap}}>
      {selectionBar}
      {tv && !scroller && !loading && <TVFocusGuideView ref={upGuide} focusable={folded} style={{position: 'absolute', left: 0, right: 0, top: 0, height: 1}}/>}
      {tv && !scroller && !loading ? sections.map((section, index) => <FocusWithin key={index} onChange={focused => {if (focused) setWhere(index);}}>
        <View style={{gap: frame.sectionGap}} onLayout={event => {ys.current.sections[index] = event.nativeEvent.layout.y; if (where === index) align();}}>{section.node}</View></FocusWithin>)
        : panel}</View>
    {aside && !loading && <View style={{width: besideAside ? spec.side : undefined, alignSelf: besideAside ? undefined : 'stretch', marginRight: besideAside ? frame.rightGutter : 0, paddingHorizontal: besideAside ? 0 : frame.gutter, gap: frame.sectionGap}}>{aside}</View>}
  </View>;
  // On TV the strip marks the section that holds focus and takes no focus itself: Down from the buttons goes straight into
  // the first row and Up from it back to the buttons (detail-tv-3).
  const tabBar = (style?: object) => tabs && <Tabs items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} passive={tv} disabled={loading} style={style} contentContainerStyle={{paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}/>;

  // The collapsed bar (touch): it shows once the hero (the art and the title on it) has scrolled away under it, so Back
  // and the title are never lost (phone-detail d1 frame 2); the tabs join it once the page's own tabs reach it. A phone
  // and an upright tablet: Back, the title centred and Play on, with the tabs under it; a tablet on its side: the title,
  // the tabs, the primary and the pinned actions. A cover page (no art box) shows it at its tabs.
  const barHeight = insets.top + 56; // R5 interim: rule 3's 56 bar under the status bar
  const barAt = Math.min(heroEnd, tabsY) - barHeight;
  const shown = !tv && !split && (collapsed ?? passed);
  // Held collapsed by the screen (a tab's content opened directly): the bar stands in place of the hero and the page's
  // tabs, and the panel starts under it (phone-detail d1 frame 2, desktop-detail f3).
  const held = shown && collapsed === true;
  const pinned = actions.filter(action => action.pinned);
  // The shared bar surface every pushed page's bar wears (ink over a blur, one hairline; phone-detail `.ph-top.solid`),
  // so what scrolls under it is not legible through it.
  const bar = shown && <View style={{position: held ? 'relative' : 'absolute', left: 0, right: 0, top: 0, zIndex: theme.layers.sticky}}>
    <BarSurface/>
    {stacked ? <>
      <View style={{height: barHeight, paddingTop: insets.top, paddingHorizontal: theme.space.sm, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
        <RoundBarButton icon="i-left" label={back.label} onPress={back.onPress}/>
        <Text accessibilityRole="header" style={[theme.type.barTitle, {flex: 1, textAlign: 'center', color: theme.colors.text}]}>{title}</Text>
        {topControl ?? (topAction ? <RoundBarButton icon={topAction.icon} label={topAction.label} onPress={topAction.onPress}/> : <View style={{width: roundBarSize}}/>)}
      </View>
      {(held || tabsPinned) && tabBar()}
    </> : <View style={{minHeight: barHeight, paddingTop: insets.top, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, flexDirection: 'row', alignItems: 'center', gap: theme.space.md}}>
      <RoundBarButton icon="i-left" label={back.label} onPress={back.onPress}/>
      <Text accessibilityRole="header" style={[theme.type.barTitle, {color: theme.colors.text, paddingRight: theme.space.md, borderRightWidth: 1, borderRightColor: theme.colors.line}]}>{title}</Text>
      <View style={{flex: 1, minWidth: 0}}>{tabs && <Tabs items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange}/>}</View>
      {primary && <Button variant="primary" size="small" icon={primary.icon} disabled={primary.disabled || loading} onPress={primary.onPress}>{primary.label}</Button>}
      {pinned.map(action => <Button key={action.label} variant="glass" size="small" iconOnly icon={action.icon} accessibilityLabel={action.label} accessibilityState={{selected: action.active}} disabled={action.disabled || loading} onPress={action.onPress}/>)}
    </View>}
  </View>;

  if (split) return <View testID={testID} style={{flex: 1, flexDirection: 'row'}}>
    {/* An open foldable on its side (foldable f2): the title on the left page, the tabs and their panel on the right; each scrolls on its own, and the crease is the divider. */}
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: frame.bottomClear}} showsVerticalScrollIndicator={false}>
      {head}
      {cast && <View style={{paddingHorizontal: frame.gutter, marginTop: frame.sectionGap}}>{cast}</View>}
    </ScrollView>
    <View style={{width: 1, backgroundColor: theme.colors.line}}/>
    {scroller ? <View style={{flex: 1}}>{scroller(<>{tabBar()}{content}</>, {paddingTop: insets.top, paddingBottom: frame.bottomClear}, noBar)}</View>
      : <ScrollView style={{flex: 1}} contentContainerStyle={{paddingTop: insets.top, paddingBottom: frame.bottomClear}}>{tabBar()}{content}</ScrollView>}
  </View>;
  const strip = tabs && !held && <View onLayout={event => setTabsY(event.nativeEvent.layout.y)}>{tabBar({marginTop: stacked ? theme.space.sm : tv ? spec.tabsGap.tv : 0})}</View>;
  const page = tv ? <>
    <FocusWithin onChange={focused => {if (focused) setWhere('head');}}><Animated.View ref={headView} style={copyStyle}>{head}{strip}</Animated.View></FocusWithin>
    {content}
  </> : <>
    {!held && head}
    {strip}
    {content}
  </>;
  // TV: the art stays where it is while the page moves under it (the title page's backdrop never follows focus), and the
  // folded band (the title at display size and the tabs over a wash of ink) sits over the top of the page.
  const band = tv && !scroller && <Animated.View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" onLayout={event => setBandHeight(event.nativeEvent.layout.height)}
    style={[{position: 'absolute', left: 0, right: 0, top: 0, paddingTop: frame.titleTop, paddingBottom: frame.headingGap}, bandStyle]}>
    {/* Sized and placed by number: tvOS draws nothing for this wash when it is laid out by absoluteFill. */}
    {bandHeight > 0 && <Svg pointerEvents="none" accessible={false} width="100%" height={bandHeight} style={{position: 'absolute', left: 0, top: 0}}>
      <Defs><LinearGradient id="title-band" x1="0" y1="0" x2="0" y2="1">{spec.tvBand.map(([opacity, at]) => <Stop key={at} offset={at} stopColor={theme.colors.ink} stopOpacity={opacity}/>)}</LinearGradient></Defs>
      <Rect width="100%" height="100%" fill="url(#title-band)"/>
    </Svg>}
    <View style={{paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}><Lockup title={title} size={spec.tvFoldedLockup} lineRatio={frameSpec.hero.lockupLine} tracking={frameSpec.hero.lockupTracking}/></View>
    {tabs && tabBar({marginTop: frame.headerGap})}
  </Animated.View>;
  return <View testID={testID} style={{flex: 1}}>
    {held && bar}
    {tv && art && !cover && <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: 0, height}}>{art}</View>}
    {tv && !scroller && <Animated.View pointerEvents="none" style={[StyleSheet.absoluteFill, {backgroundColor: theme.colors.ink}, scrimStyle]}/>}
    {scroller ? <View style={{flex: 1}}>{scroller(page, {paddingBottom: frame.bottomClear}, {at: barAt, onChange: value => {setPassed(value); setTabsPinned(value);}})}</View>
      : <ScrollView ref={scroll} style={{flex: 1}} contentContainerStyle={{paddingBottom: tv ? Math.max(frame.bottomClear, window.height - bandHeight) : frame.bottomClear}} scrollEventThrottle={32}
        onContentSizeChange={tv ? undefined : (_, contentHeight) => {
          // Back to where the page was, once it is long enough to hold that place (its panel arrives after the head).
          const offset = scrollMemory?.offset ?? 0;
          if (restored.current || offset <= 0 || contentHeight < offset + window.height) return;
          restored.current = true; scroll.current?.scrollTo({y: offset, animated: false});
        }}
        onScroll={tv ? undefined : event => {const y = event.nativeEvent.contentOffset.y; setPassed(y > barAt); setTabsPinned(y > tabsY - barHeight); scrollMemory?.onOffset(y);}}>{page}</ScrollView>}
    {band}
    {/* Back and the top action stay put over the page while the hero scrolls (phone-detail d1); the bar takes their place once it has gone. */}
    {!shown && !split && discs}
    {!held && bar}
  </View>;
}

/** An open foldable's pages have no collapsed bar. */
const noBar: TitleCollapse = {at: Number.POSITIVE_INFINITY, onChange: () => {}};
