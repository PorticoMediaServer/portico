import React, {type ReactNode} from 'react';
import {ScrollView, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FiltersDock, FiltersOver, FiltersPane, LibraryExpandedHeader, LibraryHeader, LibraryMediumHeader, StickyBrowseHeader, libraryFiltersTabletPane} from '../browse';
import {Button, Tabs, type TabItem} from '../controls';
import {BottomSheet, SidePanel} from '../overlays';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {PageHeader, type PageHeaderProps} from './PageHeader';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';

/** The header regions (`frameRegions.browse`): a library's title is its switcher; a pushed list has Back and a plain title. */
export type BrowseHeaderProps = PageHeaderProps & Readonly<{
  /** The kit `LibrarySwitcher`: the title switches library (a round chevron beside it wide, the centred name on a phone). */
  switcher?: ReactNode;
  /** The kit `LibraryMenu` (⋯): Save this view, Mark all as watched, Edit pins, and the owner's Library settings and Scan. Not on TV. */
  menu?: ReactNode;
  /** The page's tabs: Discover / Movies / Collections (Music: Discover / Artists / Albums / Songs / Genres). The frame draws
   * the kit `Tabs` from the gutter, its rule across the page. */
  tabs?: Readonly<{items: readonly TabItem[]; value: string; onValueChange: (value: string) => void}>;
}>;

/** The Filters region: what the fields say and do. The frame picks the surface for the form: a sheet on a phone, a pane over
 * the grid upright on a tablet, docked beside it on a landscape tablet, a side sheet on TV. */
export type BrowseFiltersProps = Readonly<{
  open: boolean; onClose: () => void; onClear: () => void;
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
  testID?: string;
}> & (
  | Readonly<{header: BrowseHeaderProps; hero?: never}>
  /** Home: the hero leads and the shell's wordmark bar sits over it. `null` while Home has nothing to feature. */
  | Readonly<{hero: ReactNode; header?: never}>);

/** Frame 1, Browse: Home, a library, See all, any page of rows of cards or a grid under one header. Its regions are
 * `frameRegions.browse` and `.home` (packages/design). The frame owns the header, the safe area, the space between regions
 * and rows and the clearance at the bottom; the shell owns the tab bar, sidebar or rail, and the window owns the backdrop. */
export function BrowseFrame({header, hero, toolbar, grid, alphabet, filters, state, children, testID}: BrowseFrameProps) {
  const frame = useFrameMetrics(), insets = useSafeAreaInsets(), form = frame.form;
  const failure = state && 'title' in state ? state : undefined, loading = !!state && !failure;
  // A library's header and toolbar stay put while its grid or rows scroll under them; a plain page's title scrolls away.
  const pinned = !!header && (!!header.switcher || !!toolbar || !!grid);
  const top = header ? <BrowseHeader {...header}/> : hero ?? <View style={{height: form === 'tv' ? frame.titleTop : insets.top + frameSpec.header.wordmarkBar}}/>;
  const bar = toolbar && <View style={{marginTop: frameSpec.browse.toolbarGap[form]}}>{toolbar}</View>;
  if (failure) return <View testID={testID} style={{flex: 1}}>{top}<PageStateFrame {...failure}/></View>;
  const rows = <View style={{gap: frame.sectionGap, marginTop: hero ? frameSpec.hero.home.rowOffset[form] : header ? frame.headerGap : 0}}>{loading ? <RowsSkeleton/> : children}</View>;
  const body = grid || (loading && toolbar)
    ? <View style={{flex: 1, marginTop: frameSpec.browse.gridGap[form]}}>
      {loading ? <GridSkeleton/> : grid}
      {alphabet && !loading && form !== 'tv' && <View pointerEvents="box-none" style={{position: 'absolute', top: 0, bottom: frame.bottomClear, right: 0, width: frame.rightGutter, alignItems: 'center', justifyContent: 'center'}}>{alphabet}</View>}
    </View>
    : <ScrollView style={{flex: 1}} scrollEnabled={!loading} contentContainerStyle={{paddingBottom: frame.bottomClear}}>{!pinned && top}{rows}</ScrollView>;
  const page = <View style={{flex: 1}}>{pinned && top}{bar}{body}</View>;
  return <View testID={testID} style={{flex: 1}}>
    <View style={{flex: 1, flexDirection: 'row'}}>{page}{filters?.open && form === 'expanded' && <FiltersDock testID={testID && `${testID}-filters`}><Pane filters={filters}/></FiltersDock>}</View>
    {filters && <FiltersSurface filters={filters} testID={testID && `${testID}-filters`}/>}
  </View>;
}

/** The header per form (frameRegions.browse `header.*`). A library: the phone's top bar with the switcher centred and ⋯, the
 * upright tablet's 24 pt name, the landscape tablet's large title, TV's title with its tabs beside it. A plain page: rule 3's header. */
function BrowseHeader({title, back, trailing, switcher, menu, tabs: tabItems}: BrowseHeaderProps) {
  const frame = useFrameMetrics(), form = frame.form;
  // TV sets the tabs beside the title, in the header's own row; elsewhere they run from the gutter under it.
  const tabs = tabItems && <Tabs items={tabItems.items} value={tabItems.value} onValueChange={tabItems.onValueChange} style={form === 'tv' ? undefined : {marginTop: form === 'phone' ? 0 : frame.headerGap}}
    contentContainerStyle={form === 'tv' ? undefined : {paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}/>;
  if (!switcher) return <><PageHeader title={title} back={back} trailing={trailing}/>{tabs}</>;
  if (form === 'phone') return <StickyBrowseHeader name={title} switcher={switcher} menu={menu} back={back}>{tabs}</StickyBrowseHeader>;
  if (form === 'medium') return <LibraryMediumHeader name={title} switcher={switcher} menu={menu} selection={trailing}>{tabs}</LibraryMediumHeader>;
  if (form === 'expanded') return <LibraryExpandedHeader name={title} switcher={switcher} menu={menu} selection={trailing}>{tabs}</LibraryExpandedHeader>;
  return <LibraryHeader name={title} switcher={switcher}>{tabs}</LibraryHeader>;
}

function Pane({filters}: {filters: BrowseFiltersProps}) {
  return <FiltersPane summary={filters.count && defaultI18n.t('next.browse.native.resultsMatch', {results: filters.count})} onClose={filters.onClose} onClear={filters.onClear} onDone={filters.onDone} geometry={libraryFiltersTabletPane}>{filters.children}</FiltersPane>;
}

/** The surfaces a frame opens over the page: the phone's sheet, the upright tablet's pane over the grid, TV's side sheet. The
 * landscape tablet's pane is docked in the page instead. */
function FiltersSurface({filters, testID}: {filters: BrowseFiltersProps; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), t = defaultI18n.t;
  const close = (open: boolean) => {if (!open) filters.onClose();};
  const show = <Button focusId="apply" variant="primary" onPress={filters.onDone} style={{flex: 1}}>{filters.count ? t('next.browse.native.showResults', {results: filters.count}) : t('next.controls.done')}</Button>;
  if (form === 'medium') return filters.open ? <FiltersOver open onDismiss={filters.onClose} onExited={filters.onClose}><Pane filters={filters}/></FiltersOver> : null;
  if (form === 'expanded') return null;
  if (form === 'tv') return <SidePanel testID={testID} open={filters.open} onOpenChange={close} title={t('next.browse.native.filters')} description={filters.count && t('next.browse.native.resultsMatch', {results: filters.count})}
    width={frameSpec.browse.filtersWidth.tv} topInset={0} showClose={false}
    footer={<View style={{flexDirection: 'row', gap: theme.space.sm}}>{show}<Button focusId="clear" variant="glass" onPress={filters.onClear}>{t('next.browse.native.clearAll')}</Button></View>}>{filters.children}</SidePanel>;
  return <BottomSheet testID={testID} open={filters.open} onOpenChange={close} title={t('next.browse.native.filters')} showClose={false} scrollable={false} detents={[580, 698]} defaultDetent={1}
    headerAction={<Button focusId="clear" variant="text" textStyle={{color: theme.colors.textSecondary}} onPress={filters.onClear}>{t('next.browse.native.clearAll')}</Button>}
    footer={<View style={{flexDirection: 'row', gap: theme.space.controlGap}}>{filters.save && <Button focusId="save-view" variant="glass" disabled={!filters.save.canSave} onPress={filters.save.onSave}>{t('next.browse.native.saveView')}</Button>}{show}</View>}>{filters.children}</BottomSheet>;
}

/** Loading holds the loaded page's boxes (rule 12): two rows, each a heading and a line of cards at their real size. */
function RowsSkeleton() {
  const theme = useTheme(), frame = useFrameMetrics();
  const card = theme.spec.card, gap = theme.spec.shelf.gap[theme.platform];
  const row = (kind: 'wide' | 'poster', shine: boolean) => {
    const width = card.width[kind][theme.platform], height = width / card.aspect[kind], count = Math.ceil(frame.width / (width + gap));
    return <View style={{paddingLeft: frame.gutter}}>
      <SkeletonBlock width={width} height={theme.type.rowHeading.fontSize} style={{marginBottom: frame.headingGap}}/>
      <View style={{flexDirection: 'row', gap, overflow: 'hidden'}}>{Array.from({length: count}, (_, index) => <SkeletonBlock key={index} width={width} height={height} radius={theme.radii.artwork} shine={shine && index === 0}/>)}</View>
    </View>;
  };
  return <>{row('wide', true)}{row('poster', false)}</>;
}

/** A library grid loading: posters at their real size with their two caption lines, in the grid's own columns. */
function GridSkeleton() {
  const theme = useTheme(), frame = useFrameMetrics();
  const width = theme.spec.card.width.poster[theme.platform], gap = theme.spec.shelf.gap[theme.platform], line = theme.type.cardTitle.fontSize ?? 13;
  const columns = Math.max(1, Math.floor((frame.width - frame.gutter - frame.rightGutter + gap) / (width + gap)));
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, overflow: 'hidden'}}>
    {Array.from({length: columns * 3}, (_, index) => <View key={index} style={{width, gap: theme.space.tight}}>
      <SkeletonBlock width={width} height={width * 1.5} radius={theme.radii.artwork} shine={index === 0}/>
      <SkeletonBlock width={width * .8} height={line}/><SkeletonBlock width={width * .4} height={line * .8}/>
    </View>)}
  </View>;
}
