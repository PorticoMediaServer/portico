import React, {useState, type ReactNode} from 'react';
import {RefreshControl, ScrollView, Text, View, useWindowDimensions, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent, type ViewStyle} from 'react-native';
import Animated, {type AnimatedRef} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {FocusRegion, StartFocusScope, type FocusRegionProps} from '../focus';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FiltersDock, FiltersOver, FiltersPane, LibraryExpandedHeader, LibraryHeader, LibraryMediumHeader, StickyBrowseHeader, libraryFiltersTabletPane} from '../browse';
import {Button, Tabs, type TabsProps} from '../controls';
import type {IconName} from '../Icon';
import {BottomSheet, SidePanel} from '../overlays';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {FrameRhythm, useFrameMetrics, type FrameMetrics} from './metrics';
import {PageHeader, type PageHeaderProps} from './PageHeader';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';
import {HeroWordsTop} from '../layout/Hero';

/** The header regions (`frameRegions.browse`): a library's title is its switcher; a pushed list has Back and a plain title. */
export type BrowseHeaderProps = PageHeaderProps & Readonly<{
  /** The kit `LibrarySwitcher`: the title switches library (a round chevron beside it wide, the centred name on a phone). */
  switcher?: ReactNode;
  /** The kit `LibraryMenu` (⋯): Save this view, Mark all as watched, Edit pins, and the owner's Library settings and Scan. Not on TV. */
  menu?: ReactNode;
  /** The library's counts under its title ("10 artists  10 albums  40 songs"): wide headers only, never the phone bar. */
  counts?: readonly FactPart[];
  /** The library's own wash behind its wide header (its latest artwork); the phone bar and TV draw none. */
  ambient?: ReactNode;
  /** The page's tabs: Discover / Movies / Collections (Music: Discover / Artists / Albums / Songs / Genres). The frame draws
   * the kit `Tabs` from the gutter, its rule across the page. */
  tabs?: Pick<TabsProps, 'items' | 'value' | 'onValueChange' | 'disabled' | 'itemProps'> &
    {region?: Pick<FocusRegionProps, 'id' | 'edges' | 'enabled'>};
  /** A pushed list's one line saying why these titles are here (desktop-browse seeall, collections-46). */
  intro?: ReactNode;
  /** Labelled buttons under the intro: Play and Shuffle on See all. */
  actions?: ReactNode;
}>;

/** The Filters region: what the fields say and do. The frame picks the surface for the form: a sheet on a phone, a pane over
 * the grid upright on a tablet, docked beside it on a landscape tablet, a side sheet on TV. */
export type BrowseFiltersProps = Readonly<{
  open: boolean; onClose: () => void; onClear: () => void;
  canApply?: boolean;
  /** "6 movies match" on a panel; the sheet's primary reads "Show 6 movies". */
  count?: string;
  /** Apply and close: Done on a panel, the count's button on a sheet. */
  onDone: () => void;
  /** A phone sheet's Save view beside its primary. */
  save?: Readonly<{canSave: boolean; onSave: () => void}>;
  /** The fields: the kit `FilterBuilder`. */
  children: ReactNode;
}>;

export type BrowseFrameProps = Readonly<{
  /** The rows: one flexible region. Each child is a kit `Row` (it draws its own heading); the frame scrolls and spaces them.
   * A row with no items is left out whole. */
  children?: ReactNode;
  /** In place of rows, the other flexible region: a grid or list that scrolls itself (kit `WindowedGrid`), given the room under the toolbar. */
  grid?: ReactNode;
  /** The kit `AlphabetRail`, beside the grid in the right gutter. Phone, tablet. */
  alphabet?: ReactNode;
  /** The kit `BrowseToolbar` (Filters, quick chips, count, sort, view, Save view, applied filters), fixed under the header. */
  toolbar?: ReactNode;
  filters?: BrowseFiltersProps;
  /** The whole page is loading for the first time, or can't load. Loading keeps the header, tabs and toolbar and draws the
   * grid's or rows' boxes; a failure keeps the header. */
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
  /** A focused TV empty state can stay over the grid as newly scanned titles arrive. */
  gridState?: PageStateProps;
  testID?: string;
  /** The rows form's scroller, where the page keeps its own (Home's scroll handler drives its row window, scrolled header and
   * pull to refresh). The frame still places the hero, the rows and their gaps and the bottom clearance: it hands the
   * scroller what to scroll and the content style to put on it. Without it the frame scrolls the rows itself. */
  scroller?: (content: ReactNode, contentContainerStyle: ViewStyle) => ReactNode;
  /** One notice over the rows, inside the gutter (Home: "Some libraries didn't load" with Try again). */
  notice?: ReactNode;
  /** The page's foot under its rows: one action centred over a line of description (Home's Customize Home). */
  foot?: Readonly<{action: Readonly<{label: string; icon: IconName; onPress: () => void}>; description?: string}>;
  /** The rows form's own scroller, drawn by the frame (Home): the page's scroll handler and offset, which drive its row
   * window and scrolled header, pull to refresh, and the room for the tab bar under the frame's bottom clearance. */
  scroll?: Readonly<{
    /** The scroller's own id (Home's is `native-home`, which the app's tests and device scripts find it by). */
    testID?: string;
    /** The page's animated ref (`useAnimatedRef`), which its scroll anchoring reads. */
    ref?: AnimatedRef<Animated.ScrollView>; style?: React.ComponentProps<typeof Animated.ScrollView>['style'];
    onScroll?: ((event: NativeSyntheticEvent<NativeScrollEvent>) => void) | object; onScrollBeginDrag?: () => void;
    refresh?: Readonly<{refreshing: boolean; onRefresh: () => void}>; bottomInset?: number;
  }>;
  /** Where the rows start in the scrolled content, for a page that measures its rows against the scroll offset. */
  onRowsLayout?: (event: LayoutChangeEvent) => void;
  /** TV: the `focusId` or `testID` of the control the page starts on (R1): Home's first Continue Watching card, a
   * list's first card. Never in the rail. */
  startFocus?: string;
  focus?: Pick<FocusRegionProps, 'id' | 'entry' | 'edges' | 'enabled'>;
}> & (
  | Readonly<{header: BrowseHeaderProps; hero?: never}>
  /** Home: the hero leads and the shell's wordmark bar sits over it. `null` while Home has nothing to feature. */
  | Readonly<{hero: ReactNode; header?: never}>);

/** Frame 1, Browse: Home, a library, See all, any page of rows of cards or a grid under one header. Its regions are
 * `frameRegions.browse` and `.home` (packages/design). The frame owns the header, the safe area, the space between regions
 * and rows and the clearance at the bottom; the shell owns the tab bar, sidebar or rail, and the window owns the backdrop. */
export function BrowseFrame({startFocus, focus, ...props}: BrowseFrameProps) {
  // While the TV Filters sheet is open the page starts in it, on its primary (Show 6 movies), never on a card behind it.
  const page = <BrowsePage {...props}/>;
  return <StartFocusScope id={props.filters?.open ? 'apply' : startFocus}>
    {focus ? <FocusRegion {...focus} style={{flex: 1}}>{page}</FocusRegion> : page}
  </StartFocusScope>;
}
function BrowsePage({header, hero, toolbar, grid, alphabet, filters, state, gridState, children, testID, scroller, onRowsLayout, notice, foot, scroll}: Omit<BrowseFrameProps, 'startFocus' | 'focus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), form = frame.form;
  const failure = state && 'title' in state ? state : undefined, loading = !!state && !failure;
  // A library's header and toolbar stay put while its grid or rows scroll under them; a plain page's title scrolls away.
  const pinned = !!header && (!!header.switcher || !!toolbar || !!grid);
  // The hero's words start a header gap under the status bar and, on a phone and an upright tablet, the wordmark bar over it.
  const wordsTop = insets.top + (form === 'phone' || form === 'medium' ? frameSpec.header.wordmarkBar : 0) + frame.headerGap;
  const top = header ? <BrowseHeader {...header}/> : hero ? <HeroWordsTop.Provider value={wordsTop}>{hero}</HeroWordsTop.Provider>
    : <View style={{height: form === 'tv' ? frame.titleTop : insets.top + frameSpec.header.wordmarkBar}}/>;
  if (failure) return <View testID={testID} style={{flex: 1}}>{top}<PageStateFrame {...failure}/></View>;
  const rows = <FrameRhythm.Provider value><View onLayout={onRowsLayout} style={{gap: frame.rowSection, marginTop: hero ? frameSpec.hero.home.rowOffset[form] : header ? frame.headerGap : 0}}>{loading ? <RowsSkeleton/> : <>
    {notice ? <View style={{paddingHorizontal: frame.gutter}}>{notice}</View> : null}
    {children}
    {foot ? <BrowseFoot {...foot}/> : null}
  </>}</View></FrameRhythm.Provider>;
  const body = grid || (loading && toolbar)
    ? <View style={{flex: 1, marginTop: frameSpec.browse.gridGap[form]}}>
      {loading ? <GridSkeleton/> : grid}
      {gridState && <View style={{position: 'absolute', inset: 0, backgroundColor: theme.colors.ink}}><PageStateFrame {...gridState}/></View>}
      {alphabet && !loading && form !== 'tv' && <View pointerEvents="box-none" style={{position: 'absolute', top: 0, bottom: frame.bottomClear, right: 0, width: frame.rightGutter, alignItems: 'center', justifyContent: 'center'}}>{alphabet}</View>}
    </View>
    : scroll && !loading ? <Animated.ScrollView ref={scroll.ref} testID={scroll.testID ?? (testID ? `${testID}-scroll` : undefined)} style={scroll.style} scrollEventThrottle={16}
      onScroll={scroll.onScroll as never} onScrollBeginDrag={scroll.onScrollBeginDrag} showsVerticalScrollIndicator={false}
      refreshControl={scroll.refresh ? <RefreshControl refreshing={scroll.refresh.refreshing} tintColor={theme.colors.text} onRefresh={scroll.refresh.onRefresh}/> : undefined}
      contentContainerStyle={{paddingBottom: frame.bottomClear + (scroll.bottomInset ?? 0)}}>{!pinned && top}{rows}</Animated.ScrollView>
    : scroller && !loading ? scroller(<>{!pinned && top}{rows}</>, {paddingBottom: frame.bottomClear})
    : <ScrollView style={{flex: 1}} scrollEnabled={!loading} contentContainerStyle={{paddingBottom: frame.bottomClear}}>{!pinned && top}{rows}</ScrollView>;
  const page = <View style={{flex: 1}}>
    {header?.ambient && (form === 'phone' || !header.switcher) && <View pointerEvents="none" style={{position: 'absolute', inset: 0}}>{header.ambient}</View>}
    {pinned && top}{toolbar}{body}</View>;
  return <View testID={testID} style={{flex: 1}}>
    <View style={{flex: 1, flexDirection: 'row'}}>{page}{filters?.open && form === 'expanded' && <FiltersDock testID={testID && `${testID}-filters`}><Pane filters={filters}/></FiltersDock>}</View>
    {filters && <FiltersSurface filters={filters} testID={testID && `${testID}-filters`}/>}
  </View>;
}

/** The header per form (frameRegions.browse `header.*`). A library: the phone's top bar with the switcher centred and ⋯, the
 * upright tablet's 24 pt name, the landscape tablet's large title, TV's title with its tabs beside it. A plain page: rule 3's header. */
function BrowseHeader({title, back, trailing, switcher, menu, counts, ambient, tabs: tabItems, intro, actions}: BrowseHeaderProps) {
  const frame = useFrameMetrics(), form = frame.form;
  // TV sets the tabs beside the title, in the header's own row; elsewhere they run from the gutter under it.
  const strip = tabItems && <Tabs items={tabItems.items} value={tabItems.value} onValueChange={tabItems.onValueChange}
    disabled={tabItems.disabled} itemProps={tabItems.itemProps} style={form === 'tv' ? undefined : {marginTop: form === 'phone' ? 0 : frame.headerGap}}
    contentContainerStyle={form === 'tv' ? {paddingHorizontal: 0} : {paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}/>;
  const tabs = tabItems?.region ? <FocusRegion {...tabItems.region} orientation="horizontal" entry={tabItems.value}>{strip}</FocusRegion> : strip;
  const theme = useTheme();
  if (!switcher) return <>
    <PageHeader title={title} back={back} trailing={trailing} showTVBack/>
    {intro && <Text style={[theme.type.intro, {color: theme.colors.textSecondary, marginHorizontal: frame.gutter, maxWidth: frameSpec.settings.paragraphMax}]}>{intro}</Text>}
    {/* On a phone a pushed list's actions share their row in equal halves (collections 46a: Play and Shuffle). */}
    {actions && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.controlGap, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, marginTop: frame.headerGap}}>
      {form === 'phone' ? halves(actions) : actions}</View>}
    {tabs}</>;
  if (form === 'phone') return <StickyBrowseHeader name={title} switcher={switcher} menu={menu} back={back}>{tabs}</StickyBrowseHeader>;
  if (form === 'medium') return <LibraryMediumHeader name={title} counts={counts} ambient={ambient} switcher={switcher} menu={menu} selection={trailing}>{tabs}</LibraryMediumHeader>;
  if (form === 'expanded') return <LibraryExpandedHeader name={title} counts={counts} ambient={ambient} switcher={switcher} menu={menu} selection={trailing}>{tabs}</LibraryExpandedHeader>;
  return <LibraryHeader name={title} counts={counts} switcher={switcher} menu={trailing}>{tabs}</LibraryHeader>;
}

/** A phone's pushed-list actions as equal halves: each kit Button (a fragment's too) in its own flexible cell, stretched to it. */
function halves(actions: ReactNode) {
  const items = React.Children.toArray(actions).flatMap(child => React.isValidElement<{children?: ReactNode}>(child) && child.type === React.Fragment ? React.Children.toArray(child.props.children) : [child]);
  return items.map((child, index) => <View key={index} style={{flex: 1}}>{React.isValidElement(child) ? React.cloneElement(child as React.ReactElement<{align?: string}>, {align: 'stretch'}) : child}</View>);
}

function Pane({filters}: {filters: BrowseFiltersProps}) {
  return <FiltersPane summary={filters.count && defaultI18n.t('next.browse.native.resultsMatch', {results: filters.count})} onClose={filters.onClose} onClear={filters.onClear} onDone={filters.onDone} geometry={libraryFiltersTabletPane}>{filters.children}</FiltersPane>;
}

/** The surfaces a frame opens over the page: the phone's sheet, the upright tablet's pane over the grid, TV's side sheet. The
 * landscape tablet's pane is docked in the page instead. */
function FiltersSurface({filters, testID}: {filters: BrowseFiltersProps; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), {height} = useWindowDimensions(), t = defaultI18n.t;
  // The upright pane stays mounted after it closes until its exit has played (FiltersOver calls onExited).
  const [over, setOver] = useState(filters.open);
  if (filters.open && !over) setOver(true);
  const close = (open: boolean) => {if (!open) filters.onClose();};
  const show = <Button focusId="apply" variant="primary" disabled={filters.canApply === false} onPress={filters.onDone} style={{flex: 1}}>{filters.count ? t('next.browse.native.showResults', {results: filters.count}) : t('next.controls.done')}</Button>;
  if (form === 'medium') return over ? <FiltersOver open={filters.open} onDismiss={filters.onClose} onExited={() => setOver(false)}><Pane filters={filters}/></FiltersOver> : null;
  if (form === 'expanded') return null;
  if (form === 'tv') return <SidePanel testID={testID} open={filters.open} onOpenChange={close} title={t('next.browse.native.filters')} description={filters.count && t('next.browse.native.resultsMatch', {results: filters.count})}
    width={frameSpec.browse.filtersWidth.tv} topInset={0} showClose={false}
    footer={<View style={{flexDirection: 'row', gap: theme.space.sm}}>{show}<Button focusId="clear" variant="glass" onPress={filters.onClear}>{t('next.browse.native.clearAll')}</Button></View>}>{filters.children}</SidePanel>;
  return <BottomSheet testID={testID} open={filters.open} onOpenChange={close} title={t('next.browse.native.filters')} showClose={false} scrollable={false} detents={frameSpec.browse.phoneFilterDetents.map(detent => detent * height)} defaultDetent={1}
    headerAction={<Button focusId="clear" variant="text" textStyle={{color: theme.colors.textSecondary}} onPress={filters.onClear}>{t('next.browse.native.clearAll')}</Button>}
    footer={<View style={{flexDirection: 'row', gap: theme.space.controlGap}}>{filters.save && <Button focusId="save-view" variant="glass" disabled={!filters.save.canSave} onPress={filters.save.onSave}>{t('next.browse.native.saveView')}</Button>}{show}</View>}>{filters.children}</BottomSheet>;
}

/** The page's foot: its one action centred over a line of description (Home's Customize Home). */
function BrowseFoot({action, description}: NonNullable<BrowseFrameProps['foot']>) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{marginHorizontal: frame.gutter, alignItems: 'center', gap: frameSpec.browse.foot.gap}}>
    {/* 48 tall over 22 a side: the button size Customize Home always drew. */}
    <Button variant="glass" size="episode" icon={action.icon} style={{alignSelf: 'center'}} onPress={action.onPress}>{action.label}</Button>
    {description ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, textAlign: 'center'}]}>{description}</Text> : null}
  </View>;
}

/** Loading holds the loaded page's boxes (rule 12): two rows, each a heading and a line of cards at their real size. */
function RowsSkeleton() {
  const theme = useTheme(), frame = useFrameMetrics();
  const card = theme.spec.card, gap = theme.spec.shelf.gap[theme.column];
  const row = (kind: 'wide' | 'poster', shine: boolean) => {
    const width = card.width[kind][theme.column], height = width / card.aspect[kind], count = Math.ceil(frame.width / (width + gap));
    return <View style={{paddingLeft: frame.gutter}}>
      <SkeletonBlock width={width} height={theme.type.rowHeading.fontSize} style={{marginBottom: frame.rowHeadingGap}}/>
      <View style={{flexDirection: 'row', gap, overflow: 'hidden'}}>{Array.from({length: count}, (_, index) => <SkeletonBlock key={index} width={width} height={height} radius={theme.radii.artwork} shine={shine && index === 0}/>)}</View>
    </View>;
  };
  return <>{row('wide', true)}{row('poster', false)}</>;
}

/** The poster grid's card width: as many columns of at least `frameSpec.browse.grid.card` as fit between the gutters, stretched
 * to fill them (phone-app 5: three across; desktop-browse: seven). Beside a docked Filters pane the grid has that much less room.
 * `frame` must be read inside the page the grid is drawn in (under the shell's page inset), or the columns are the window's. */
export function browseGridCard(frame: FrameMetrics, filtersDocked = false, minCard = frameSpec.browse.grid.card[frame.form]) {
  const grid = frameSpec.browse.grid, gap = grid.columnGap[frame.form];
  const room = frame.width - frame.gutter - frame.rightGutter - (filtersDocked ? frameSpec.browse.filtersWidth[frame.form] : 0);
  const columns = Math.max(1, Math.floor((room + gap) / (minCard + gap)));
  return {width: Math.floor((room - gap * (columns - 1)) / columns), columns};
}

/** A library grid loading: posters at their real size with their two caption lines, in the grid's own columns. */
function GridSkeleton() {
  const theme = useTheme(), frame = useFrameMetrics();
  const {width, columns} = browseGridCard(frame), grid = frameSpec.browse.grid, gap = grid.columnGap[frame.form], line = theme.type.cardTitle.fontSize ?? 13;
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: gap, rowGap: grid.rowGap[frame.form], paddingLeft: frame.gutter, paddingRight: frame.rightGutter, overflow: 'hidden'}}>
    {Array.from({length: columns * 3}, (_, index) => <View key={index} style={{width, gap: theme.space.tight}}>
      <SkeletonBlock width={width} height={width * 1.5} radius={theme.radii.artwork} shine={index === 0}/>
      <SkeletonBlock width={width * .8} height={line}/><SkeletonBlock width={width * .4} height={line * .8}/>
    </View>)}
  </View>;
}
