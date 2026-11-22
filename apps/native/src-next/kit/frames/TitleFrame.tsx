import {PreviewText} from '../layout/PreviewText';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
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
import {Chip} from '../controls/Chip';
import {PersonCircle} from '../artwork/Circles';
import {Icon, type IconName} from '../Icon';
import {Lockup} from '../layout/Lockup';
import {HeroFactsCentred} from '../layout/Hero';
import {BarSurface, RoundBarButton, roundBarSize, type TopBarBack} from '../layout/TopBar';
import {alpha} from '../shell/visual';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {buttonGeometry} from '../controls/control-model';
import {useTheme} from '../theme';
import {FocusRegion, FocusWithin, StartFocusScope} from '../focus';
import {useMotion} from '../useMotion';
import {FrameReveal, useFrameMetrics} from './metrics';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {QuoteBlock, seasonColumn, type AboutValue} from './title-parts';

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
export type TitleKicker = Readonly<{label: string; icon?: IconName; detail?: string; onPress?: () => void;
  /** Plain words before the label, which is then the link: a magazine's "Magazine" before its library (phone-read 24). */
  kind?: string;
  /** In the accent though it is not a link: an author's or a series' kind over their name (collections 41b, 41c). */
  accent?: boolean}>;
/** A person's, an artist's, an album's or a book's own picture, beside the words in place of the art box. The frame
 * sizes it per form; a picture that draws to a size (a person's initials) takes it as a function of the width. */
export type TitleCover = Readonly<{node: ReactNode | ((size: number) => ReactNode); shape: 'poster' | 'square' | 'circle';
  /** An artist's circle: drawn large on a phone (phone-app frame 15b), where a person's is small (phone-detail d3 frame 7);
   * on TV at the right, as on touch, with room for its rings (tv-more frame 8), where a person's is at the left. */
  large?: boolean;
  /** A saved list's cover mosaic (a playlist, collection or saved view, collections 18 to 21): on TV smaller than an
   * album's, at the head's top, with a smaller name, so the list starts higher. Other forms draw it as any square. */
  mosaic?: boolean}>;
/** Where the collapsed bar shows, for a page's own scroller (`MusicScrollView`'s `collapseAt` and `onCollapse`). */
export type TitleCollapse = Readonly<{at: number; onChange: (collapsed: boolean) => void;
  /** Where the page's tabs reach the bar and pin under it (a page with tabs): past `pinAt`, `onPin(true)`. */
  pinAt?: number; onPin?: (pinned: boolean) => void}>;
export type TitleFrameProps = Readonly<{
  /** The picture behind the words, as the kit's `AmbientArt` with `treatment="title"`. The frame gives it the box. */
  art?: ReactNode;
  /** In place of `art`: the cover beside the words (a poster or square at the left, a circle at the right; on TV a person's
   * circle is at the left too, level with the top of the words, as tv-detail frame 10 draws a person). Given both,
   * `art` is the glow behind a cover's head on touch (phone-detail d3 frame 7: a person's films behind the circle). */
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
  /** Called while the page is scrolled within one screen of its end: the page loads its panel's next page (a long
   * season's further episodes), as the old title screen did. It may be called again before that page arrives. */
  onEnd?: () => void;
  /** The page's selection bar while titles in its panel are selected (Select in a card's menu), at the top of the panel. */
  selectionBar?: ReactNode;
  kicker?: TitleKicker;
  /** Who made it, under the title in the accent: an album's artist (phone-app frame 13), a link to their page. */
  byline?: Readonly<{text: string; onPress?: () => void;
    /** A person's initials in a small circle before their name (a reading book's author, phone-read 7 "FH Frank Herbert"). */
    initials?: string}>;
  /** Where a reader is in a book, one line under the buttons and over the synopsis (phone-read 7): the version they read
   * (a pill, when there is more than one: it opens the versions), then the place, the first part strong ("Page 248 of
   * 535", "About 7 h left"). */
  place?: Readonly<{version?: Readonly<{label: string; value: string; onPress: () => void}>; facts?: readonly string[]}>;
  title: string;
  /** The metadata line (`HeroMetadata`). Its line is kept when there is nothing to show. */
  metadata?: ReactNode;
  /** The audio and subtitles for the next play (`TrackPicker`), under the synopsis on a phone and an upright tablet. */
  picker?: ReactNode;
  /** The one flexible button: it takes the room the round buttons leave. A person has none. */
  primary?: TitlePrimary;
  /** The buttons after it, in fixed slots. One that the title doesn't support stays in its slot with `disabled`. */
  actions?: readonly TitleAction[];
  /** One bold line over the synopsis, on a tablet on its side (TV: in About, rule 14). */
  tagline?: string;
  synopsis?: string;
  /** A quiet note under the buttons with ⓘ (phone-detail d3 frame 8: how Portico made an automatic collection). */
  note?: string;
  /** A passage set apart under the description, over the tabs (a book's first lines, drawn as `QuoteBlock`). */
  quote?: Readonly<{label: string; text: string}>;
  /** A biography's Read more and where it is from ("Biography from TMDB"). */
  /** `expanded`: the whole paragraph shows and the link reads Show less (the old person screen's biography). */
  readMore?: Readonly<{label: string; onPress: () => void; note?: string; expanded?: boolean}>;
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
  /** On TV, beside a cover at the left: under its words and buttons (tv-more frame 6: an album's songs). Elsewhere it opens the
   * panel. */
  beside?: ReactNode;
  /** The bar that replaces the hero (desktop-detail f3, phone-detail d1 frame 2, detail-tv-4). The frame sets it itself:
   * on touch from the scroll, on TV when focus moves into the panel. A screen sets it only to hold it (a tab opened directly). */
  collapsed?: boolean;
  /** TV: a shorter head, for a page whose first row must show whole on arrival (a collection, tv-detail frame 11). */
  shortHead?: boolean;
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
function TitlePage({shortHead, art, cover, back, topAction, kicker, byline, place, title, metadata, picker, primary, actions = [], tagline, synopsis, note, quote, readMore, credits, cast, beside, tabs, seasons, children, aside, collapsed, state, scroller, topControl, artRef, scrollMemory, onEnd, selectionBar, testID}: Omit<TitleFrameProps, 'startFocus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), spec = frameSpec.hero.title;
  const [synopsisOpen, setSynopsisOpen] = useState(false);
  const {form} = frame, tv = form === 'tv', split = form === 'medium' && window.width > window.height, stacked = form === 'phone' || form === 'medium';
  const [passed, setPassed] = useState(false), [tabsPinned, setTabsPinned] = useState(false), [tabsY, setTabsY] = useState(Number.POSITIVE_INFINITY), [heroEnd, setHeroEnd] = useState(Number.POSITIVE_INFINITY), [wordsTop, setWordsTop] = useState(Number.POSITIVE_INFINITY);
  // TV (detail-tv-3): focus moving into the panel folds the hero to the title and the tabs at the top, the art dims, and
  // the section that holds focus rises to just under them; focus back on the buttons unfolds it. The head stays laid out
  // (it scrolls under the band, its words faded), so Up from the first row finds the buttons again. `where` is the head or a section's index; focus
  // leaving the page (the rail, an overlay) keeps it. A page with its own scroller (an album's songs) scrolls itself and
  // does not fold. Nor does a cover page (a person, tv-detail frame 10): its head is short, so the page moves only as far as
  // the control in focus needs and the head stays above the first row.
  const motion = useMotion(), [where, setWhere] = useState<'head' | number>('head');
  const folded = tv && !scroller && !cover && (collapsed ?? where !== 'head');
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
    if (!tv || scroller || cover) return;
    const y = where === 'head' ? 0 : ys.current.content + ys.current.column + (ys.current.sections[where] ?? 0) - bandHeight;
    scroll.current?.scrollTo({y: Math.max(0, y), animated: !motion.reduce});
  };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(align, [where, bandHeight]);
  // Within one section (Details: About, the cast, the collection) focus can move to a control below the screen or up
  // under the band: the page moves just enough to show it whole, with room for its ring. The page has no other mover on
  // TV (its ScrollView does not scroll by itself).
  const pageView = useRef<View>(null), scrollY = useRef(0), whereNow = useRef(where), tabsHeight = useRef(0);
  // Touch: bring a part to just under the compact bar and the pinned tabs (FrameReveal), from where it is on screen now.
  const revealTouch = (host: unknown) => {
    const node = host as View | null;
    if (!node || tv || scroller) return;
    requestAnimationFrame(() => node.measureInWindow((_x, y) => {
      scroll.current?.scrollTo({y: Math.max(0, scrollY.current + y - (insets.top + 56) - tabsHeight.current - theme.space.sm), animated: !motion.reduce});
    }));
  };
  whereNow.current = where;
  const reveal = (host: unknown) => {
    const node = host as View | null, page = pageView.current;
    if (!node || !page || scroller) return;
    requestAnimationFrame(() => node.measureLayout(page, (_x, y, _w, h) => {
      const room = theme.space.xl, top = y - room, bottom = y + h + room, band = folded ? bandHeight : 0;
      const target = bottom > scrollY.current + window.height ? bottom - window.height : top < scrollY.current + band ? top - band : undefined;
      if (target !== undefined) scroll.current?.scrollTo({y: Math.max(0, target), animated: !motion.reduce});
    }, () => {}));
  };
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
  const mosaicTV = tv && !!cover?.mosaic, saved = spec.parts.savedCoverTV;
  const height = split ? spec.height.phone : tv && shortHead ? spec.tvShortHeight : spec.height[form], lockup = mosaicTV ? saved.lockup : cover ? spec.coverLockup[split ? 'phone' : form] : split ? spec.lockup.phone : spec.lockup[form];
  const discTop = insets.top + theme.space.tight, discRow = discTop + roundBarSize + theme.space.sm;
  const centre = stacked ? 'center' as const : 'flex-start' as const;

  const kickerWords = kicker && <>
    {kicker.kind !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, fontFamily: theme.type.rowLabel.fontFamily}]}>{kicker.kind}</Text>}
    <Text accessibilityRole={kicker.onPress && !tv ? 'link' : undefined} onPress={tv ? undefined : kicker.onPress} style={[theme.type.rowHelp, {color: kicker.onPress || kicker.icon || kicker.accent ? theme.colors.accentStrong : theme.colors.textSecondary, fontFamily: theme.type.rowLabel.fontFamily}]}>
      {kicker.icon ? <><Icon name={kicker.icon} size={tv ? theme.scale.icon.tertiary.size : 16} color={theme.colors.accentStrong}/>{' '}</> : null}{kicker.label}</Text>
    {kicker.detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.rowLabel.fontFamily}]}>{kicker.detail}</Text>}
  </>;
  const kickerRow = {flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: centre, gap: theme.space.controlGap} as const;
  // On TV each head link sits in a focus region of this page's own, so a page it opens gives focus back to it on Back
  // (the layer stack keeps the control it left only when a region holds it). The page is drawn again on Back, so the
  // region is named by the page itself, not by this mount.
  const headLink = (id: string, link: ReactNode) => tv ? <FocusRegion id={`title.head.${testID ?? 'page'}.${title}.${id}`}>{link}</FocusRegion> : link;
  // On TV a kicker that opens something (the collection, tv-detail frame 1) is a focus stop of its own, above the buttons:
  // text alone takes no D-pad focus.
  const kickerLine = kicker && (tv && kicker.onPress
    ? headLink('kicker', <Focusable focusId="title-kicker" testID="title-kicker" accessibilityRole="link" accessibilityLabel={[kicker.kind, kicker.label, kicker.detail].filter(Boolean).join(', ')} onPress={kicker.onPress}
      // The ring is drawn outside the line: its room comes on top of the gap, so it never touches the title (R1).
      style={[kickerRow, {alignSelf: 'flex-start', marginBottom: theme.space.controlGap + theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv}]}>{kickerWords}</Focusable>)
    : <View style={[kickerRow, {marginBottom: theme.space.controlGap}]}>{kickerWords}</View>);
  const tvCover = tv && !!cover;
  const tvByline = (value: NonNullable<typeof byline>) => <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
    {value.initials && <PersonCircle size={spec.parts.bylineRowTV.circle} initials={value.initials}/>}
    <Text style={[theme.type.meta, {fontFamily: theme.type.listLabel.fontFamily, color: theme.colors.text}]}>{value.text}</Text></View>;
  const identity = <>
    {kickerLine}
    {loading && !title ? <SkeletonBlock width="70%" height={lockup}/> : <Lockup title={title} size={lockup} lineRatio={frameSpec.hero.lockupLine} tracking={frameSpec.hero.lockupTracking} style={{textAlign: stacked ? 'center' : 'left'}}/>}
    {byline && tvCover ? <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: spec.parts.bylineRowTV.gap, rowGap: theme.space.controlGap, marginTop: theme.space.lg}}>
      {/* tv-more 6 `.mrow`: the artist (a 44 circle and the name in bold) and the facts on one line, in the line's colours. */}
      {byline.onPress ? headLink('byline', <Focusable focusId="title-byline" accessibilityRole="link" accessibilityLabel={byline.text} onPress={byline.onPress} focusRadius={spec.parts.bylineRowTV.circle / 2}>{tvByline(byline)}</Focusable>) : tvByline(byline)}
      {!loading && metadata}
    </View> : <>
      {byline && (byline.onPress
        ? headLink('byline', <Focusable focusId="title-byline" accessibilityRole="link" accessibilityLabel={byline.text} onPress={byline.onPress} style={{alignSelf: stacked ? 'center' : 'flex-start', marginTop: theme.space.hairline * 2}}>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>{byline.initials && <PersonCircle size={spec.parts.bylineCircle} initials={byline.initials}/>}
            <Text style={[theme.type.wideTitle, {color: theme.colors.accentStrong, textAlign: stacked ? 'center' : 'left'}]}>{byline.text}</Text></View></Focusable>)
        : <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, alignSelf: stacked ? 'center' : 'flex-start', marginTop: theme.space.hairline * 2}}>
          {byline.initials && <PersonCircle size={spec.parts.bylineCircle} initials={byline.initials}/>}
          <Text style={[theme.type.wideTitle, {color: theme.colors.accentStrong, textAlign: stacked ? 'center' : 'left'}]}>{byline.text}</Text></View>)}
      <View style={{minHeight: theme.type.rowLabel.lineHeight, marginTop: theme.space.controlGap, alignItems: centre, justifyContent: 'center'}}>{loading ? <SkeletonBlock width="40%" height={theme.type.meta.fontSize}/>
        // A stacked head is centred, so its facts are too, line by line when they wrap: the page passes facts, never alignment.
        : <HeroFactsCentred.Provider value={stacked}>{metadata}</HeroFactsCentred.Provider>}</View>
    </>}
  </>;
  const names = (value: TitleCredit['names']) => typeof value === 'string' ? value : value.map((name, index) => <Text key={index}>{index > 0 ? ', ' : ''}
    {name.onPress && !theme.isTV ? <Text accessibilityRole="link" onPress={name.onPress} style={{textDecorationLine: 'underline', textDecorationColor: alpha(theme.colors.text, .35)}}>{name.text}</Text> : name.text}</Text>);
  // The synopsis reads from the left on every form, under a centred title on a phone too (detail-phone d1 frames 1 and 3,
  // as the web page at phone width). Loading keeps the loaded page's boxes (rule 12): the synopsis's lines, so the tabs and the panel do not move when the words arrive.
  // The synopsis is the type scale's synopsis role (14/21 regular on a phone, phone-app `.tblurb`), not the settings intro (14.5/500),
  // held to rule 14's two lines (owner, 17 Nov). A page with no Read more of its own opens the rest by a press on the paragraph,
  // so nothing is lost and the page keeps the frame's two lines.
  const synopsisStyle = [theme.type.synopsis, {color: theme.colors.textSecondary, maxWidth: frameSpec.settings.paragraphMax}];
  const words = loading ? <View style={{gap: tv ? spec.tvCreditsGap : spec.creditsGap}}>
    {Array.from({length: spec.synopsisLines[form]}, (_, index) => <SkeletonBlock key={index} width={index === 0 ? '96%' : '70%'} height={theme.type.intro.lineHeight ?? theme.type.intro.fontSize}/>)}
  </View> : (note !== undefined || tagline !== undefined || synopsis !== undefined || quote !== undefined || credits?.length) ? <View style={{alignSelf: 'stretch', gap: tv ? spec.tvCreditsGap : spec.creditsGap, maxWidth: stacked ? undefined : tv ? theme.spec.hero.copy.titleWidth.tv : spec.copyWidth}}>
    {note !== undefined && <View style={{alignSelf: 'stretch', flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.controlGap}}>
      <Icon name="i-info" size={frameSpec.hero.title.parts.listRow.check} color={theme.colors.textTertiary}/>
      <Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textTertiary}]}>{note}</Text></View>}
    {/* TV (rule 14, tv.html 3): no tagline in the hero; About carries it with the whole synopsis. */}
    {!stacked && !tv && tagline !== undefined && <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{tagline}</Text>}
    {synopsis !== undefined && (readMore && !readMore.expanded
      ? <PreviewText text={synopsis} lines={spec.readMoreLines[form]} style={synopsisStyle}/>
      // TV (rule 14, Justin 17 Nov: "the synopsis keeps two lines"): two lines here, the whole synopsis in About.
      : tv ? <PreviewText text={synopsis} lines={spec.synopsisLines.tv} style={synopsisStyle}/>
      : readMore || synopsisOpen ? <Text style={synopsisStyle}>{synopsis}</Text>
      : <Focusable accessibilityRole="button" accessibilityLabel={synopsis} accessibilityHint={defaultI18n.t('web.person.readMore')} onPress={() => setSynopsisOpen(true)} testID="title-synopsis">
        <PreviewText text={synopsis} lines={spec.synopsisLines[form]} style={synopsisStyle}/></Focusable>)}
    {readMore && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.md}}>
      {/* A phone's is a plain bold link under the paragraph (frame 7); wider forms keep the chevron. */}
      <Button variant={form === 'phone' ? 'link' : 'text'} size="xs" trailingIcon={form === 'phone' ? undefined : readMore.expanded ? 'i-chup' : 'i-chdown'} textStyle={form === 'phone' ? {color: theme.colors.text} : undefined}
        accessibilityState={readMore.expanded !== undefined ? {expanded: readMore.expanded} : undefined} testID="title-read-more" onPress={readMore.onPress}>{readMore.label}</Button>
      {readMore.note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{readMore.note}</Text>}
    </View>}
    {quote && <QuoteBlock label={quote.label} text={quote.text}/>}
    {!stacked && credits && credits.length > 0 && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>
      {credits.map((credit, index) => <Text key={credit.label}>{index > 0 ? '    ' : ''}{credit.label} <Text style={{color: theme.colors.textSecondary}}>{names(credit.names)}</Text></Text>)}</Text>}
  </View> : null;

  // The played part and the time left are in the button on a tablet on its side and TV; under the row elsewhere, with Start over.
  // A reading book's place line (phone-read 7) carries where the reader is, so its progress stays in the button.
  const resume = primary?.progress !== undefined && !loading, lineUnder = resume && (stacked || !!cover) && !place;
  const actionRow = (primary || actions.length > 0) && <ActionRow loading={loading} actions={actions} primaryRef={primaryView}
    primary={primary && {...primary, focusId: primary.testID ?? titlePrimaryId,
      progress: lineUnder ? undefined : primary.progress, startOver: stacked ? undefined : primary.startOver}}/>;
  // TV: the buttons are one focus region, so the shell remembers the one in use (Play or Resume at first) and Right from the
  // side menu returns to it; without a region nothing on a pushed title page was remembered and Right went nowhere. Named
  // by page and title like the head links: pages kept mounted in the back stack (two collections) must not share it.
  const actionsId = `title.actions.${testID ?? 'page'}.${title}`;
  const row = actionRow && tv ? <FocusRegion id={actionsId} orientation="horizontal">{actionRow}</FocusRegion> : actionRow;
  const track = theme.spec.card.progress.height[theme.column];
  const resumeBar = lineUnder ? <View accessibilityRole="progressbar" accessibilityValue={{min: 0, max: 1, now: primary!.progress}} style={{flex: 1, height: track, borderRadius: track, overflow: 'hidden', backgroundColor: alpha(theme.colors.text, theme.spec.card.progress.trackAlpha)}}>
    <View style={{width: `${primary!.progress! * 100}%`, height: track, backgroundColor: theme.colors.accentStrong}}/></View> : null;
  // A phone page with no Start over under its buttons (an audiobook: Start over is in its ⋯, collections 41a) draws the bar
  // and the time left on one line.
  const resumeLine = lineUnder && stacked && !primary!.startOver ? <View style={{width: '100%', minHeight: buttonGeometry('xs', theme.column, 'link').height, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
    {resumeBar}<Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{primary!.remaining}</Text></View>
    : lineUnder ? <View style={{gap: theme.space.tight, width: '100%', maxWidth: cover && !stacked ? spec.coverResumeWidth : undefined}}>
    <View style={{flexDirection: 'row'}}>{resumeBar}</View>
    {/* phone-detail d1 frame 3 `.rsm .rl`: the time left in the secondary caption, Start over a link whose right edge is the bar's. */}
    <View style={{minHeight: buttonGeometry('xs', theme.column, 'link').height, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}><Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{primary!.remaining}</Text>
      {primary!.startOver && stacked && <Button variant="link" size="xs" icon="i-restart" onPress={primary!.startOver.onPress}>{primary!.startOver.label}</Button>}</View>
  </View> : null;
  const pickerRow = picker && stacked && !loading ? <View style={{alignSelf: 'stretch'}}>{picker}</View> : null;
  // phone-read 7: the version pill, then where the reader is, on one line under the buttons.
  const placeRow = place && !loading && (place.version || place.facts?.length) ? <View style={{alignSelf: stacked ? 'stretch' : 'flex-start', flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.sm, rowGap: theme.space.tight}}>
    {place.version && <Chip testID="title-version" variant="pill" prefix={place.version.label} trailingIcon="i-chdown" onPress={place.version.onPress}
      accessibilityLabel={`${place.version.label} ${place.version.value}`}>{place.version.value}</Chip>}
    {place.facts?.map((fact, index) => <Text key={fact} style={[index ? theme.type.meta : theme.type.captionStrong, {color: index ? theme.colors.textSecondary : theme.colors.text}]}>{fact}</Text>)}</View> : null;

  // The head: the art box with the words hanging from its bottom edge, or the cover beside them.
  const discs = !tv && <View pointerEvents="box-none" style={{position: 'absolute', zIndex: theme.layers.sticky, left: theme.space.sm, right: theme.space.sm, top: discTop, flexDirection: 'row', justifyContent: 'space-between'}}>
    <RoundBarButton icon="i-left" label={back.label} onPress={back.onPress} testID={back.testID}/>
    {topControl ?? (topAction && <RoundBarButton icon={topAction.icon} label={topAction.label} onPress={topAction.onPress} disabled={topAction.disabled} testID={topAction.testID}/>)}
  </View>;
  const body = <View style={{gap: theme.space.sm, alignItems: stacked ? 'center' : 'flex-start', alignSelf: 'stretch'}}>{stacked ? <>{row}{resumeLine}{placeRow}{words}{pickerRow}</> : <>{words}<View style={{marginTop: spec.actionGap[form] - theme.space.sm, gap: theme.space.sm}}>{row}{resumeLine}{placeRow}</View></>}</View>;
  // An artist's circle on TV is at the right with its rings drawn around it (tv-more frame 8).
  const artistTV = tv && cover?.shape === 'circle' && !!cover.large, ring = spec.parts.artistCircleTV;
  // A square cover on TV (an album, tv-more frame 6) sits level with the words, its head starting where an artist's does,
  // and what the page sets `beside` it (the songs) runs under its words.
  const besideCoverTV = tv && (cover?.shape === 'square' || cover?.shape === 'circle' && !cover.large && !!beside);
  const coverSize = mosaicTV ? saved.size : artistTV || besideCoverTV && cover?.shape === 'circle' ? ring.size : cover?.shape === 'circle' && (split || form === 'phone') ? cover.large ? spec.parts.coverCircleLargePhone : spec.parts.coverCirclePhone
    : spec.cover[cover?.shape ?? 'square'][split ? 'phone' : form];
  // Its box holds the rings' room around the circle (a parent clips what is drawn outside it): the circle stays 280 from the
  // screen's edge and level with the words.
  const coverBox = cover && <View style={artistTV ? {width: coverSize + 2 * ring.ring, height: coverSize + 2 * ring.ring, padding: ring.ring, marginVertical: -ring.ring, marginRight: ring.right - ring.ring - frame.rightGutter}
    : {width: coverSize, height: cover.shape === 'poster' ? coverSize * 1.5 : coverSize, borderRadius: cover.shape === 'circle' ? coverSize / 2 : frame.cardRadius, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{typeof cover.node === 'function' ? cover.node(coverSize) : cover.node}</View>;
  const head = cover ? stacked
    // A cover page reports where its name starts (the head is the page's first part, so that is a page offset): the compact
    // bar, which carries the name, takes over as the name reaches it, so the name never runs under Back and the top action.
    ? <View style={{paddingTop: discRow, paddingHorizontal: frame.gutter, alignItems: 'center', gap: theme.space.sm}}>{split && discs}{coverBox}<View onLayout={event => setHeroEnd(event.nativeEvent.layout.y)} style={{alignSelf: 'stretch'}}>{identity}</View>
      {/* A cover page with no buttons and no words (a person without a biography) ends at its name: no empty gap. */}
      {(primary || actions.length > 0 || words || picker) && <View style={{alignSelf: 'stretch', marginTop: spec.actionGap.phone - theme.space.sm}}>{body}</View>}</View>
    : <View onLayout={event => setHeroEnd(event.nativeEvent.layout.y + event.nativeEvent.layout.height)} style={{paddingTop: mosaicTV ? saved.top : artistTV || besideCoverTV ? ring.top : tv ? frame.titleTop : discRow, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, flexDirection: cover.shape === 'circle' && (!tv || artistTV) ? 'row-reverse' : 'row', alignItems: (cover.shape === 'circle' || besideCoverTV) && tv ? 'flex-start' : 'center', gap: spec.coverGap}}>
      {/* Where the name starts (a page offset: the head is the page's first part): the bar takes over as it reaches the bar. */}
      {split && discs}{coverBox}<View onLayout={event => setWordsTop(event.nativeEvent.layout.y)} style={{flex: 1, minWidth: 0}}>{identity}<View style={{marginTop: theme.space.sm}}>{body}</View>
        {besideCoverTV && beside && <View style={{marginTop: frame.sectionGap}}>{beside}</View>}</View></View>
    // A tablet on its side and TV: the art box is at least its drawn height and grows with its words, so a long title or a
    // second line of buttons pushes the panel down rather than rising under Back (the words never overlap the discs).
    : <><View onLayout={event => setHeroEnd(event.nativeEvent.layout.y + event.nativeEvent.layout.height)} style={stacked ? {height} : {minHeight: height, justifyContent: 'flex-end', paddingTop: tv ? frame.titleTop : discRow, paddingBottom: spec.copyBottom[form]}}>
      <View ref={artRef} collapsable={false} pointerEvents="box-none" style={StyleSheet.absoluteFill}>{!tv && art}
      </View>
      {split && discs}
      {/* Everything in the art box hangs from its bottom edge, so a longer title grows upward and the row below never moves. */}
      {/* Where the kicker and title start (the art box is the page's first part, so a page offset): the bar takes over as they reach it. */}
      <View pointerEvents="box-none" onLayout={event => setWordsTop(event.nativeEvent.layout.y)} style={stacked ? {position: 'absolute', left: frame.gutter, right: frame.gutter, bottom: split ? 0 : spec.copyBottom[form]} : {paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
        <View style={stacked || tv ? undefined : {maxWidth: theme.spec.hero.copy.titleWidth.web}}>{identity}</View>
        {/* The words keep the copy width; the action row takes the page's width, so its buttons stay on one line (tablet f6, detail-tv-2). */}
        {!stacked && <View style={{marginTop: theme.space.sm}}>{body}</View>}
      </View>
    </View>
    {stacked && <View style={{paddingHorizontal: frame.gutter, marginTop: spec.actionGap[form]}}>{body}</View>}</>;

  const posterWidth = theme.spec.card.width.poster[theme.column];
  const panel = loading ? <View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', gap: theme.space.sm, paddingLeft: frame.gutter, overflow: 'hidden'}}>
    {Array.from({length: 6}, (_, index) => <SkeletonBlock key={index} width={posterWidth} height={posterWidth * 1.5} shine={index === 0}/>)}</View> : !besideCoverTV && beside ? <>{beside}{children}</> : children;
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
      {tv && !scroller && !loading ? sections.map((section, index) => <FocusWithin key={index} onChange={(focused, host) => {
        if (!focused) return;
        // A move within the section keeps the control on screen; entering a section aligns the section itself. A cover page
        // only keeps things on screen going down; going back up it shows the section from its heading, and the first one
        // with the whole head above it, as on arrival (f9).
        const entering = whereNow.current !== index, up = typeof whereNow.current === 'number' && index < whereNow.current;
        if (entering && cover && tv && up) {
          const top = index === 0 ? 0 : ys.current.content + ys.current.column + (ys.current.sections[index] ?? 0) - frame.titleTop;
          scroll.current?.scrollTo({y: Math.max(0, top), animated: !motion.reduce});
        } else if (!entering || cover) reveal(host);
        if (entering) setWhere(index);
      }}>
        <View style={{gap: frame.sectionGap}} onLayout={event => {ys.current.sections[index] = event.nativeEvent.layout.y; if (where === index) align();}}>{section.node}</View></FocusWithin>)
        : panel}</View>
    {aside && !loading && <View style={{width: besideAside ? form === 'expanded' ? spec.tabletSide : spec.side : undefined, alignSelf: besideAside ? undefined : 'stretch', marginRight: besideAside ? frame.rightGutter : 0, paddingHorizontal: besideAside ? 0 : frame.gutter, gap: frame.sectionGap}}>{aside}</View>}
  </View>;
  // On TV the strip marks the section that holds focus and takes no focus itself: Down from the buttons goes straight into
  // the first row and Up from it back to the buttons (detail-tv-3).
  // TV: the rule under the tabs runs from the gutter to the safe edge (detail-tv 1 to 5), so the strip's box is set in, not its row.
  const tabBar = (style?: object) => tabs && <Tabs items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} passive={tv} disabled={loading}
    style={[tv ? {marginLeft: frame.gutter, marginRight: frame.rightGutter, borderBottomWidth: spec.tvTabRule, borderBottomColor: theme.colors.line} : null, style]}
    contentContainerStyle={tv ? {paddingLeft: 0, paddingRight: 0} : {paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}/>;

  // The collapsed bar (touch): it shows once the hero (the art and the title on it) has scrolled away under it, so Back
  // and the title are never lost (phone-detail d1 frame 2); the tabs join it once the page's own tabs reach it. A phone
  // and an upright tablet: Back, the title centred and Play on, with the tabs under it; a tablet on its side: the title,
  // the tabs, the primary and the pinned actions. A cover page (no art box) shows it at its tabs.
  const barHeight = insets.top + 56; // R5 interim: rule 3's 56 bar under the status bar
  // The bar takes over as the title reaches it, never after it has run under Back and the top action (looker, 20 Nov).
  const barAt = Math.min(heroEnd, tabsY, wordsTop) - barHeight;
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
      {/* The rule after the title parts it from the tabs; a page with no tabs (an artist) has none. */}
      <Text accessibilityRole="header" style={[theme.type.barTitle, {color: theme.colors.text}, tabs ? {paddingRight: theme.space.md, borderRightWidth: 1, borderRightColor: theme.colors.line} : undefined]}>{title}</Text>
      {/* The tabs stand on the bar's lower edge, so their rule is the bar's own and the selected underline meets it (desktop-detail f3). */}
      <View style={{flex: 1, minWidth: 0, alignSelf: 'flex-end'}}>{tabs && <Tabs items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} contentContainerStyle={{paddingHorizontal: 0}}/>}</View>
      {primary && <Button variant="primary" size="small" align="center" icon={primary.icon} disabled={primary.disabled || loading} onPress={primary.onPress}>{primary.label}</Button>}
      {pinned.map(action => <Button key={action.label} variant="glass" size="small" align="center" iconOnly icon={action.icon} accessibilityLabel={action.label} accessibilityState={{selected: action.active}} disabled={action.disabled || loading} onPress={action.onPress}/>)}
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
  const strip = tabs && !held && <View onLayout={event => {setTabsY(event.nativeEvent.layout.y); tabsHeight.current = event.nativeEvent.layout.height;}}>{tabBar({marginTop: stacked ? theme.space.sm : tv ? spec.tabsGap.tv : 0})}</View>;
  const page = tv ? <View ref={pageView} collapsable={false}>
    <FocusWithin onChange={focused => {if (!focused) return; setWhere('head'); if (tv && cover) scroll.current?.scrollTo({y: 0, animated: !motion.reduce});}}><Animated.View ref={headView} style={copyStyle}>{head}{strip}</Animated.View></FocusWithin>
    {content}
  </View> : <>
    {!held && (cover && art ? <View>
      {/* The glow's box is not a clip: a longer wash (an album's, phone-app frame 13's page wash) runs past it and fades out
          by itself, where a clip drew a hard edge across the songs. */}
      <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: spec.coverGlow}}>{art}</View>{head}</View> : head)}
    {strip}
    {content}
  </>;
  // TV: the art stays where it is while the page moves under it (the title page's backdrop never follows focus), and the
  // folded band (the title at display size and the tabs over a wash of ink) sits over the top of the page.
  const band = tv && !scroller && <Animated.View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" onLayout={event => setBandHeight(event.nativeEvent.layout.height)}
    style={[{position: 'absolute', left: 0, right: 0, top: 0, height: spec.tvFoldedSectionTop, paddingTop: frame.titleTop}, bandStyle]}>
    {/* Sized and placed by number: tvOS draws nothing for this wash when it is laid out by absoluteFill. */}
    {bandHeight > 0 && <Svg pointerEvents="none" accessible={false} width="100%" height={bandHeight} style={{position: 'absolute', left: 0, top: 0}}>
      <Defs><LinearGradient id="title-band" x1="0" y1="0" x2="0" y2="1">{spec.tvBand.map(([opacity, at]) => <Stop key={at} offset={at} stopColor={theme.colors.ink} stopOpacity={opacity}/>)}</LinearGradient></Defs>
      <Rect width="100%" height="100%" fill="url(#title-band)"/>
    </Svg>}
    <View style={{paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}><Lockup title={title} size={spec.tvFoldedLockup} lineRatio={frameSpec.hero.lockupLine} tracking={frameSpec.hero.lockupTracking}/></View>
    {tabs && <View style={{position: 'absolute', left: 0, right: 0, top: spec.tvFoldedTabsTop}}>{tabBar()}</View>}
  </Animated.View>;
  return <View testID={testID} style={{flex: 1}}>
    {held && bar}
    {tv && art && !cover && <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: 0, height}}>{art}</View>}
    {tv && !scroller && <Animated.View pointerEvents="none" style={[StyleSheet.absoluteFill, {backgroundColor: theme.colors.ink}, scrimStyle]}/>}
    {scroller ? <View style={{flex: 1}}>{scroller(page, {paddingBottom: frame.bottomClear}, {at: barAt, onChange: value => {setPassed(value); if (!tabs) setTabsPinned(value);}, ...(tabs ? {pinAt: tabsY - barHeight, onPin: setTabsPinned} : {})})}</View>
      : <ScrollView ref={scroll} style={{flex: 1}} scrollEnabled={!tv} contentContainerStyle={{paddingBottom: tv ? Math.max(frame.bottomClear, window.height - bandHeight) : frame.bottomClear}} scrollEventThrottle={32}
        onContentSizeChange={tv ? undefined : (_, contentHeight) => {
          // Back to where the page was, once it is long enough to hold that place (its panel arrives after the head).
          const offset = scrollMemory?.offset ?? 0;
          if (restored.current || offset <= 0 || contentHeight < offset + window.height) return;
          restored.current = true; scroll.current?.scrollTo({y: offset, animated: false});
        }}
        onScroll={event => {
          const {contentOffset: {y}, contentSize, layoutMeasurement} = event.nativeEvent;
          scrollY.current = y;
          // An overscroll past the top (an iOS bounce, a fling as the window turns) reports a negative offset: the page remembers 0.
          if (!tv) {setPassed(y > barAt); setTabsPinned(y > tabsY - barHeight); scrollMemory?.onOffset(Math.max(0, y));}
          if (onEnd && contentSize.height - y - layoutMeasurement.height < layoutMeasurement.height) onEnd();
        }}><FrameReveal.Provider value={revealTouch}>{page}</FrameReveal.Provider></ScrollView>}
    {band}
    {/* Back and the top action stay put over the page while the hero scrolls (phone-detail d1); the bar takes their place once it has gone. */}
    {!shown && !split && discs}
    {!held && bar}
  </View>;
}

/** An open foldable's pages have no collapsed bar. */
const noBar: TitleCollapse = {at: Number.POSITIVE_INFINITY, onChange: () => {}};
