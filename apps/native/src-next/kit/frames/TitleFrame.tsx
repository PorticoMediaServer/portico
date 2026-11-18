import React, {useState, type ReactNode} from 'react';
import {ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {frameSpec} from '@portico/design';
import {Button, ResumeButton} from '../controls/Button';
import {Tabs, type TabItem} from '../controls/Tabs';
import {Icon, type IconName} from '../Icon';
import {Lockup} from '../layout/Lockup';
import {RoundBarButton, roundBarSize, type TopBarBack} from '../layout/TopBar';
import {alpha} from '../shell/visual';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {buttonGeometry} from '../controls/control-model';
import {useTheme} from '../theme';
import {StartFocusScope} from '../focus';
import {useFrameMetrics} from './metrics';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {seasonColumn, type AboutValue} from './title-parts';

export type TitleAction = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; testID?: string;
  /** Shown with its label where there is room (a tablet on its side and TV): Start over, My List, Shuffle. Round elsewhere. */
  labelled?: boolean;
  /** On (in My List, watched, a favourite): tinted in the accent (phone-detail d1 frame 3). */
  active?: boolean;
  /** Also on the collapsed bar, after the primary (desktop-detail f3: Play and +). */
  pinned?: boolean;
  /** Drawn only on a tablet on its side and TV. On a phone and an upright tablet it is in More, so the primary keeps the
   * room for its label (phone-detail d1: Play, My List, Download, More; Watched is in More). */
  wide?: boolean}>;
/** The one flexible button. With `progress` it is Resume. On a tablet on its side and TV the time left is beside its
 * label, the played part runs along its lower edge and Start over is the button after it (detail-desktop 2, detail-tv 2).
 * On a phone, an upright tablet and a cover page the played part, the time left and Start over are one line under the
 * action row (detail-phone 3, desktop-listen book). */
export type TitlePrimary = Omit<TitleAction, 'labelled' | 'pinned' | 'wide'> & Readonly<{progress?: number; remaining?: string; startOver?: Readonly<{label: string; onPress: () => void}>}>;
/** One part of the credits line: "Directed by" and the names; a name with `onPress` opens its page. */
export type TitleCredit = Readonly<{label: string; names: string | readonly AboutValue[]}>;
/** The line above the title (desktop-detail f1 "Dune collection · 2 films", f7 "Acting", desktop-listen "Album"). */
export type TitleKicker = Readonly<{label: string; icon?: IconName; detail?: string; onPress?: () => void}>;
/** A person's, an artist's, an album's or a book's own picture, beside the words in place of the art box. The frame
 * sizes it per form; a picture that draws to a size (a person's initials) takes it as a function of the width. */
export type TitleCover = Readonly<{node: ReactNode | ((size: number) => ReactNode); shape: 'poster' | 'square' | 'circle'}>;
export type TitleFrameProps = Readonly<{
  /** The picture behind the words, as the kit's `AmbientArt` with `treatment="title"`. The frame gives it the box. */
  art?: ReactNode;
  /** In place of `art`: the cover beside the words (a poster or square at the left, a circle at the right). */
  cover?: TitleCover;
  /** A glass disc over the art. Not drawn on TV, where the remote's Back leaves the page. */
  back: TopBarBack;
  /** One glass disc opposite Back (Play on). */
  topAction?: TitleAction;
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
  seasons?: Readonly<{column: ReactNode; episodes: ReactNode}>;
  /** The tab panel: the one flexible region. */
  children?: ReactNode;
  /** Beside the panel on a tablet on its side and TV (a collection's About, an album's Details); under it otherwise. */
  aside?: ReactNode;
  /** The bar that replaces the hero (desktop-detail f3, phone-detail d1 frame 2, detail-tv-4). On touch the frame sets
   * it from the scroll; on TV the screen sets it when focus moves into the panel. */
  collapsed?: boolean;
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
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
/** The primary's own focus id when the screen gives it no test id. */
const titlePrimaryId = 'title-primary';
function TitlePage({art, cover, back, topAction, kicker, title, metadata, picker, primary, actions = [], tagline, synopsis, readMore, credits, cast, tabs, seasons, children, aside, collapsed, state, testID}: Omit<TitleFrameProps, 'startFocus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), spec = frameSpec.hero.title;
  const {form} = frame, tv = form === 'tv', split = form === 'medium' && window.width > window.height, stacked = form === 'phone' || form === 'medium';
  const [passed, setPassed] = useState(false), [tabsY, setTabsY] = useState(Number.POSITIVE_INFINITY);
  const failure = state && 'title' in state ? state : undefined, loading = !!state && !failure;
  if (failure) return <View testID={testID} style={{flex: 1}}>
    {!tv && <View style={{position: 'absolute', zIndex: theme.layers.sticky, left: theme.space.sm, top: insets.top + theme.space.tight}}><RoundBarButton icon="i-left" label={back.label} onPress={back.onPress} testID={back.testID}/></View>}
    <PageStateFrame {...failure}/>
  </View>;
  const height = split ? spec.height.phone : spec.height[form], lockup = split ? spec.lockup.phone : spec.lockup[form], actionHeight = theme.spec.button.hero.height[theme.column];
  const discTop = insets.top + theme.space.tight, discRow = discTop + roundBarSize + theme.space.sm;
  const centre = stacked ? 'center' as const : 'flex-start' as const;
  const tvCollapsed = tv && !!collapsed;

  const kickerLine = kicker && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: centre, gap: theme.space.controlGap, marginBottom: theme.space.controlGap}}>
    <Text accessibilityRole={kicker.onPress ? 'link' : undefined} onPress={kicker.onPress} style={[theme.type.rowHelp, {color: kicker.onPress || kicker.icon ? theme.colors.accentStrong : theme.colors.textSecondary, fontFamily: theme.type.rowLabel.fontFamily}]}>
      {kicker.icon ? <><Icon name={kicker.icon} size={16} color={theme.colors.accentStrong}/>{' '}</> : null}{kicker.label}</Text>
    {kicker.detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.rowLabel.fontFamily}]}>{kicker.detail}</Text>}
  </View>;
  const identity = <>
    {kickerLine}
    {loading && !title ? <SkeletonBlock width="70%" height={lockup}/> : <Lockup title={title} size={tvCollapsed ? spec.lockup.expanded : lockup} lineRatio={frameSpec.hero.lockupLine} tracking={frameSpec.hero.lockupTracking} lines={tv ? 3 : undefined} style={{textAlign: stacked ? 'center' : 'left'}}/>}
    {!tvCollapsed && <View style={{minHeight: theme.type.rowLabel.lineHeight, marginTop: theme.space.controlGap, alignItems: centre, justifyContent: 'center'}}>{loading ? <SkeletonBlock width="40%" height={theme.type.meta.fontSize}/> : metadata}</View>}
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

  // Round on a phone and an upright tablet; labelled on a tablet on its side and TV (tablet f6, detail-tv-2).
  const side = (action: TitleAction) => action.labelled && !stacked
    ? <Button key={action.label} variant="glass" size="hero" icon={action.icon} iconColor={action.active ? theme.colors.accentStrong : undefined} disabled={action.disabled || loading} onPress={action.onPress} testID={action.testID}>{action.label}</Button>
    : <Button key={action.label} variant="glass" size="hero" iconOnly icon={action.icon} accessibilityLabel={action.label} accessibilityState={{selected: action.active}} iconColor={action.active ? theme.colors.accentStrong : undefined} disabled={action.disabled || loading} onPress={action.onPress} testID={action.testID}
      style={[{width: actionHeight, height: actionHeight}, action.active && {backgroundColor: alpha(theme.colors.accentStrong, theme.spec.card.progress.trackAlpha), borderWidth: 1, borderColor: alpha(theme.colors.accentStrong, .55)}]}/>;
  // While loading the row keeps its buttons but says nothing it does not know yet (the time left).
  const resume = primary?.progress !== undefined && !loading, lineUnder = resume && (stacked || !!cover);
  const lead = primary && {icon: primary.icon, disabled: primary.disabled || loading, onPress: primary.onPress, testID: primary.testID, focusId: primary.testID ?? titlePrimaryId, focusableWhenDisabled: loading, align: stacked ? 'stretch' as const : 'start' as const, style: stacked ? {flex: 1, maxWidth: form === 'medium' ? 300 : undefined} : undefined};
  const row = (primary || actions.length > 0) && <View style={{minHeight: actionHeight, alignSelf: stacked ? 'stretch' : undefined, flexDirection: 'row', flexWrap: stacked ? 'nowrap' : 'wrap', alignItems: 'center', justifyContent: centre, gap: theme.spec.hero.actions.gap[theme.column]}}>
    {lead && (resume && !lineUnder ? <ResumeButton {...lead} size="hero" progress={primary!.progress!} remaining={primary!.remaining}>{primary!.label}</ResumeButton> : <Button {...lead} variant="primary" size="hero">{primary!.label}</Button>)}
    {primary?.startOver && !stacked && !loading && <Button variant="glass" size="hero" icon="i-restart" onPress={primary.startOver.onPress}>{primary.startOver.label}</Button>}
    {actions.filter(action => !action.wide || !stacked).map(side)}
  </View>;
  const track = theme.spec.card.progress.height[theme.column];
  const resumeLine = lineUnder ? <View style={{gap: theme.space.tight, width: '100%', maxWidth: cover && !stacked ? spec.coverResumeWidth : undefined}}>
    <View accessibilityRole="progressbar" accessibilityValue={{min: 0, max: 1, now: primary!.progress}} style={{height: track, borderRadius: track, overflow: 'hidden', backgroundColor: alpha(theme.colors.text, theme.spec.card.progress.trackAlpha)}}>
      <View style={{width: `${primary!.progress! * 100}%`, height: track, backgroundColor: theme.colors.accentStrong}}/></View>
    <View style={{minHeight: buttonGeometry('xs', theme.column, 'text').height, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}><Text style={[theme.type.rowHelp, {color: theme.colors.text}]}>{primary!.remaining}</Text>
      {primary!.startOver && stacked && <Button variant="text" size="xs" icon="i-restart" onPress={primary!.startOver.onPress}>{primary!.startOver.label}</Button>}</View>
  </View> : null;
  const pickerRow = picker && stacked && !loading ? <View style={{alignSelf: 'stretch'}}>{picker}</View> : null;

  // The head: the art box with the words hanging from its bottom edge, or the cover beside them.
  const discs = !tv && <View pointerEvents="box-none" style={{position: 'absolute', zIndex: theme.layers.sticky, left: theme.space.sm, right: theme.space.sm, top: discTop, flexDirection: 'row', justifyContent: 'space-between'}}>
    <RoundBarButton icon="i-left" label={back.label} onPress={back.onPress} testID={back.testID}/>
    {topAction && <RoundBarButton icon={topAction.icon} label={topAction.label} onPress={topAction.onPress} disabled={topAction.disabled} testID={topAction.testID}/>}
  </View>;
  const body = <View style={{gap: theme.space.sm, alignItems: stacked ? 'center' : 'flex-start', alignSelf: 'stretch'}}>{stacked ? <>{row}{resumeLine}{words}{pickerRow}</> : <>{words}<View style={{marginTop: spec.actionGap[form] - theme.space.sm, gap: theme.space.sm}}>{row}{resumeLine}</View></>}</View>;
  const coverSize = spec.cover[cover?.shape ?? 'square'][split ? 'phone' : form];
  const coverBox = cover && <View style={{width: coverSize, height: cover.shape === 'poster' ? coverSize * 1.5 : coverSize, borderRadius: cover.shape === 'circle' ? coverSize / 2 : frame.cardRadius, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{typeof cover.node === 'function' ? cover.node(coverSize) : cover.node}</View>;
  const head = cover ? stacked
    ? <View style={{paddingTop: discRow, paddingHorizontal: frame.gutter, alignItems: 'center', gap: theme.space.sm}}>{discs}{coverBox}<View style={{alignSelf: 'stretch'}}>{identity}</View><View style={{alignSelf: 'stretch', marginTop: spec.actionGap.phone - theme.space.sm}}>{body}</View></View>
    : <View style={{paddingTop: tv ? frame.titleTop : discRow, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, flexDirection: cover.shape === 'circle' ? 'row-reverse' : 'row', alignItems: 'center', gap: spec.coverGap}}>
      {discs}{coverBox}<View style={{flex: 1, minWidth: 0}}>{identity}<View style={{marginTop: theme.space.sm}}>{body}</View></View></View>
    // TV with focus in the panel (detail-tv-4, 5a): the title and tabs at the top, the art box still behind them at its full height.
    : tvCollapsed ? <View style={{paddingTop: frame.titleTop, paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
      <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: 0, height, opacity: spec.tvCollapsedArt}}>{art}</View>{identity}</View>
    // A tablet on its side and TV: the art box is at least its drawn height and grows with its words, so a long title or a
    // second line of buttons pushes the panel down rather than rising under Back (the words never overlap the discs).
    : <><View style={stacked ? {height} : {minHeight: height, justifyContent: 'flex-end', paddingTop: tv ? frame.titleTop : discRow, paddingBottom: spec.copyBottom[form]}}>
      <View pointerEvents="box-none" style={StyleSheet.absoluteFill}>{art}
        {/* A tablet's words are over the picture, so the picture is shaded behind them (a phone's words are under its picture and a TV's art has its own). */}
        {!stacked && !tv && <Svg pointerEvents="none" accessible={false} width="100%" height="100%" style={StyleSheet.absoluteFill}>
          <Defs>
            <LinearGradient id="title-shade-side" x1="0" y1="0" x2="1" y2="0">{spec.shade.side.map(([opacity, at]) => <Stop key={at} offset={at} stopColor={theme.colors.ink} stopOpacity={opacity}/>)}</LinearGradient>
            <LinearGradient id="title-shade-foot" x1="0" y1="1" x2="0" y2="0">{spec.shade.foot.map(([opacity, at]) => <Stop key={at} offset={at} stopColor={theme.colors.ink} stopOpacity={opacity}/>)}</LinearGradient>
          </Defs>
          <Rect width="100%" height="100%" fill="url(#title-shade-side)"/><Rect width="100%" height="100%" fill="url(#title-shade-foot)"/>
        </Svg>}
      </View>
      {discs}
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
  const content = <View style={{marginTop: tabs ? frame.headerGap + frame.headingGap : frame.sectionGap, flexDirection: besideAside ? 'row' : 'column', alignItems: 'flex-start', gap: besideAside ? theme.space.xl : frame.sectionGap}}>
    <View style={{flex: besideAside ? 1 : undefined, alignSelf: besideAside ? undefined : 'stretch', minWidth: 0, gap: frame.sectionGap}}>
      {tv && seasons && !loading && <View style={{flexDirection: 'row', gap: seasonColumn.gap, paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
        <View style={{width: seasonColumn.width}}>{seasons.column}</View><View style={{flex: 1, minWidth: 0}}>{seasons.episodes}</View></View>}
      {panel}</View>
    {aside && !loading && <View style={{width: besideAside ? spec.side : undefined, alignSelf: besideAside ? undefined : 'stretch', marginRight: besideAside ? frame.rightGutter : 0, paddingHorizontal: besideAside ? 0 : frame.gutter, gap: frame.sectionGap}}>{aside}</View>}
  </View>;
  const tabBar = (style?: object) => tabs && <Tabs items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} disabled={loading} style={style} contentContainerStyle={{paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}/>;

  // The collapsed bar (touch): it shows once the tabs pass under it. A phone and an upright tablet: Back, the title
  // centred and Play on, with the tabs under it; a tablet on its side: the title, the tabs, the primary and the pinned actions.
  const barHeight = insets.top + 56; // R5 interim: rule 3's 56 bar under the status bar
  const shown = !tv && !split && (collapsed ?? passed);
  // Held collapsed by the screen (a tab's content opened directly): the bar stands in place of the hero and the page's
  // tabs, and the panel starts under it (phone-detail d1 frame 2, desktop-detail f3).
  const held = shown && collapsed === true;
  const pinned = actions.filter(action => action.pinned);
  const bar = shown && <View style={{position: held ? 'relative' : 'absolute', left: 0, right: 0, top: 0, zIndex: theme.layers.sticky, backgroundColor: alpha(theme.colors.ink, .92), borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
    {stacked ? <>
      <View style={{height: barHeight, paddingTop: insets.top, paddingHorizontal: theme.space.sm, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
        <RoundBarButton icon="i-left" label={back.label} onPress={back.onPress}/>
        <Text accessibilityRole="header" style={[theme.type.barTitle, {flex: 1, textAlign: 'center', color: theme.colors.text}]}>{title}</Text>
        {topAction ? <RoundBarButton icon={topAction.icon} label={topAction.label} onPress={topAction.onPress}/> : <View style={{width: roundBarSize}}/>}
      </View>
      {tabBar()}
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
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingTop: insets.top, paddingBottom: frame.bottomClear}}>{tabBar()}{content}</ScrollView>
  </View>;
  return <View testID={testID} style={{flex: 1}}>
    {held && bar}
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: frame.bottomClear}} scrollEventThrottle={32} onScroll={event => setPassed(event.nativeEvent.contentOffset.y > tabsY - barHeight)}>
      {!held && head}
      {tabs && !held && <View onLayout={event => setTabsY(event.nativeEvent.layout.y)}>{tabBar({marginTop: stacked ? theme.space.sm : tv ? spec.tabsGap.tv : 0})}</View>}
      {content}
    </ScrollView>
    {!held && bar}
  </View>;
}

