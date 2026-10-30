import React, {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {BackHandler, ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {PinnedResource} from '../../../../../packages/client-core/src/library-pins';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {SavedEntry, SavedListFilter, SavedRoute, SavedSnapshot} from '../../../../../packages/client-core/src/saved';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {PosterCard, SquareCard, type CardSelection} from '../../kit/cards';
import {CardFrame, Parts} from '../../kit/cards/primitives';
import {Artwork, StandardBackdrop} from '../../kit/artwork';
import {Chip, Focusable, Tabs} from '../../kit/controls';
import {Menu} from '../../kit/overlays';
import {SelectionCircle} from '../../kit/selection';
import {StateView} from '../../kit/states';
import {Icon, type IconName} from '../../kit/Icon';
import {browseType, alpha} from '../../kit/browse/presentation';
import {usePullRefresh} from '../../kit/states/usePullRefresh';
import {useTheme} from '../../kit/theme';
import {groupOfflineEntries, type NativeOfflineDownloadsDevice} from '../../platform/offline-downloads';
import type {NativeShellRuntime} from '../../shell/runtime';
import {homeCardText} from '../home/model';
import {NativeSavedDestination} from './model';
import {SavedLoading, SavedTail} from './SavedLoading';
import {savedNearEnd} from './paging';
import {SavedDownloadCard, type NativeSavedDownloadsCopy} from './SavedDownloadCard';
import {openNativeSavedDownload} from '../downloads/playback';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import type {NativeEntryMenuState} from '../destinations/entry-actions';

const t = defaultI18n.t;
const content = (entry: SavedEntry): entry is ContentEntry => entry.kind !== 'playlist' && entry.kind !== 'playlist_entry' && entry.kind !== 'reading_group';
export type NativeSavedActions = Readonly<{
  menuState?: NativeEntryMenuState;
  openView: (view: SavedRoute) => void; openEntry: (entry: ContentEntry) => void; menuEntry: (entry: ContentEntry, anchorRef?: RefObject<View | null>, select?: () => void) => void;
  isMenuOpen?: (entry: ContentEntry) => boolean;
  openDownloads: () => void; openPin: (pin: PinnedResource) => void; editPins: () => void;
  /** The wider My List grid re-reads the real watchlist by server sort/filter/shape. */
  selectMyList?: (route: SavedRoute) => Promise<void>;
  nextMyList?: (shape: 'posters' | 'squares', sectionId: string) => Promise<void>;
  /** Real shortcut into Customize Home for one loaded saved view. */
  showOnHome?: (resource: PersonalResource) => void;
  /** Exact saved definition opened in its real library. */
  openInLibrary?: (resource: PersonalResource) => void;
}>;
export type NativeSavedSelectionPort = Readonly<{
  /** Enter the page's real current selection context without selecting an item. */
  start?: (rendered?: unknown) => void;
  subscribe?: (listener: () => void) => () => void;
  getSnapshot?: () => number;
  register: (snapshot: SavedSnapshot) => () => void;
  item: (entry: ContentEntry) => CardSelection | undefined;
  render: (artwork?: (path?: string) => React.ReactNode) => React.ReactNode;
}>;
export type NativeSavedScreenProps = Readonly<{
  destination: NativeSavedDestination; runtime: NativeShellRuntime; device: NativeOfflineDownloadsDevice;
  actions: NativeSavedActions; selection?: NativeSavedSelectionPort; onThisPhone: MessageId;
  downloadsCopy: NativeSavedDownloadsCopy;
  activation: NativeDestinationActivationPort;
  /** Saved's own Downloads tab body. Present on handhelds that draw the wider Saved chrome. */
  downloads?: (runtime: NativeShellRuntime) => React.ReactNode;
  tabBody?: (tab: 'favorites' | 'playlists' | 'collections' | 'views' | 'history', runtime: NativeShellRuntime) => React.ReactNode;
}>;

/** phone-app9: Saved is the genuine hub, with My List and actual device downloads.
 * Every other row opens its existing SavedRoute; pins keep the producer's order. */
export function SavedScreen({destination, runtime, device, actions, selection, onThisPhone, downloadsCopy, activation, downloads, tabBody}: NativeSavedScreenProps) {
  useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  useSyncExternalStore(actions.menuState?.subscribe ?? (() => () => {}), actions.menuState?.getSnapshot ?? (() => 0));
  const theme = useTheme(), insets = useSafeAreaInsets(), scroll = useRef<ScrollView>(null);
  const snapshot = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const deviceState = useSyncExternalStore(device.subscribe, device.getSnapshot);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const report = useCallback((error: unknown) => runtime.reportError(error), [runtime]);
  useEffect(() => {void destination.load().catch(report); void device.open().catch(report);}, [destination, device, report]);
  const pull = usePullRefresh(() => destination.load({quiet: true}), report);
  useEffect(() => selection?.register(snapshot.myList), [selection, snapshot.myList]);
  useEffect(() => runtime.registerScrollToTop({kind: 'saved'}, () => scroll.current?.scrollTo({y: 0, animated: !theme.reduceMotion})), [runtime, theme.reduceMotion]);
  // tablet11/11b/12: the wider Saved page keeps its real title and seven tabs around
  // the same routes. Downloads is the last tab and draws inside this page, so the shell
  // sidebar and this chrome stay in place; phone keeps the pushed page.
  const tablet = !theme.isTV && theme.platform !== 'phone';
  const [savedTab, setSavedTab] = useState<'watchlist' | 'favorites' | 'playlists' | 'collections' | 'views' | 'history' | 'downloads'>('watchlist');
  const [gridWidth, setGridWidth] = useState(0), [sortOpen, setSortOpen] = useState(false), [filterOpen, setFilterOpen] = useState(false), [listMode, setListMode] = useState(false);
  const sortRef = useRef<View>(null), filterRef = useRef<View>(null), viewport = useRef({offset: 0, height: 0, extent: 0});
  // A retained but hidden Saved page must never change its local tab or open a
  // downloadable view; the same current activation gates the existing actions.
  const visible = () => activation.getSnapshot().visible;
  const showDownloads = useCallback(() => {
    if (!activation.getSnapshot().visible) return;
    if (tablet) {setSavedTab('downloads'); return;}
    actions.openDownloads();
  }, [activation, actions, tablet]);
  const chooseTab = (value: string) => {
    if (!visible()) return;
    setFilterOpen(false); setSortOpen(false);
    if (value === 'watchlist' || value === 'downloads' || value === 'favorites' || value === 'playlists' || value === 'collections' || value === 'views' || value === 'history') setSavedTab(value);
  };
  const downloadsOpen = tablet && !!downloads && savedTab === 'downloads';
  const otherTab = tablet && savedTab !== 'watchlist' && savedTab !== 'downloads' ? savedTab : undefined;
  const myListColumns = theme.platform === 'tablet' ? 4 : 5;
  useEffect(() => {
    if (!tablet || savedTab === 'watchlist' || !active.visible) return;
    const subscription = BackHandler.addEventListener('hardwareBackPress', () => {if (!activation.getSnapshot().visible) return false; setSavedTab('watchlist'); return true;});
    return () => subscription.remove();
  }, [active.visible, activation, savedTab, tablet]);
  const tabItems = [
    {value: 'watchlist', label: t('saved.tab.watchlist')}, {value: 'favorites', label: t('saved.tab.favorites')},
    {value: 'playlists', label: t('saved.tab.playlists')}, {value: 'collections', label: t('saved.tab.collections')},
    {value: 'views', label: t('saved.tab.savedViews')}, {value: 'history', label: t('saved.tab.history')},
    {value: 'downloads', label: t('downloads.title')},
  ] as const;
  // states 4b / a991: a page already read may stay on screen only for a positively known
  // transport failure. A refusal, permission, missing or invalid answer takes the ordinary
  // error state instead of stale documents. TV keeps its existing recovery unchanged.
  const documentError = theme.isTV ? snapshot.error : snapshot.error ?? snapshot.myList.error ?? snapshot.myListSquares.error;
  const documentFacts = documentError ? presentError(documentError, 'saved', {operation: 'load'}) : undefined;
  const transportFailure = documentFacts !== undefined && (documentFacts.category === 'unreachable' || documentFacts.category === 'offline'
    || documentFacts.category === 'timeout' || documentFacts.category === 'busy');
  const retainedDocument = !!snapshot.myList.projection || !!snapshot.myListSquares?.projection;
  const admitted = theme.isTV || documentFacts === undefined || (transportFailure && retainedDocument);
  // A withheld document retires its query menus too; reopening waits for an admitted page.
  useLayoutEffect(() => {if (!admitted) {setFilterOpen(false); setSortOpen(false);}}, [admitted]);
  // The actual server shape window supplies the drawn poster preview. Filtering
  // one mixed page locally could hide later posters and misstate its count.
  const posterProjection = admitted ? snapshot.myList.projection : undefined, squareProjection = admitted ? snapshot.myListSquares?.projection : undefined;
  const items = posterProjection?.sections.flatMap(section => section.entries.filter(content)) ?? [];
  const localGroups = groupOfflineEntries(deviceState.entries);
  const summary = snapshot.summary;
  const posterEntries = posterProjection?.sections.flatMap(section => section.entries.filter(content)) ?? [];
  const squareEntries = squareProjection?.sections.flatMap(section => section.entries.filter(content)) ?? [];
  const pageTotal = (projection: typeof posterProjection) => projection?.sections.reduce((sum, section) => sum + section.totalCount, 0) ?? 0;
  // Each drawn shape keeps its own server total; the grid's count is its poster query,
  // never a local union of the two windows.
  const myListCount = (snapshot.phase === 'ready' || snapshot.phase === 'loading') && posterProjection ? pageTotal(posterProjection) : undefined;
  const filterOptions = posterProjection?.filters.flatMap(filter => filter.options.map(option => ({...option, field: filter.id}))) ?? [];
  const applyMyList = (patch: {sort?: string; direction?: 'asc' | 'desc'; filter?: SavedListFilter}) => {
    if (!visible() || !actions.selectMyList || !posterProjection || !squareProjection) return;
    const now = destination.getSnapshot();
    // The open control belongs to the projection it was drawn from; a hidden page or a
    // successor query must not apply its old sort/filter to the new watchlist.
    if (now.phase !== 'ready' || now.myList.projection !== posterProjection || now.myListSquares.projection !== squareProjection) return;
    const query = posterProjection.query;
    const filter = patch.filter ?? (query.category && query.category !== 'all' ? query.category as SavedListFilter : undefined);
    const sort = patch.sort ?? query.sort, direction = patch.direction ?? (query.direction === 'asc' || query.direction === 'desc' ? query.direction : undefined);
    const base = {view: 'watchlist' as const, ...(filter ? {filter} : {}), ...(sort ? {sort} : {}), ...(direction ? {direction} : {})};
    void actions.selectMyList({...base, shape: 'posters'});
    void actions.selectMyList({...base, shape: 'squares'});
  };
  const sortItems = (posterProjection?.sorts ?? []).map(sort => ({id: sort.id, label: t(sort.labelKey as MessageId), selected: posterProjection?.query.sort === sort.id,
    onSelect: () => {setSortOpen(false); applyMyList({sort: sort.id, direction: sort.directions.includes(posterProjection?.query.direction as never) ? posterProjection!.query.direction as 'asc' | 'desc' : sort.directions[0] ?? 'asc'});}}));
  const filterItems = filterOptions.map(option => ({id: `${option.field}:${option.id}`, label: option.label, trailing: option.count ? defaultI18n.number(option.count) : undefined,
    selected: posterProjection?.query.category === option.id, onSelect: () => {setFilterOpen(false); applyMyList({filter: option.id as SavedListFilter});}}));
  const nextMyListPage = (shape: 'posters' | 'squares', projection: NonNullable<typeof posterProjection>, sectionId: string) => {
    if (!visible() || !actions.nextMyList) return;
    const now = destination.getSnapshot(), current = shape === 'posters' ? now.myList.projection : now.myListSquares.projection;
    if (now.phase !== 'ready' || current !== projection) return;
    void actions.nextMyList(shape, sectionId);
  };
  const nextPosters = () => {
    const section = posterProjection?.sections.find(value => value.nextCursor);
    const stride = listMode ? 88 : Math.max(118, gridWidth / myListColumns) * 1.5 + 58;
    if (tablet && savedTab === 'watchlist' && section && savedNearEnd(viewport.current.offset, viewport.current.height, viewport.current.extent, stride, listMode ? 1 : myListColumns)) {
      nextMyListPage('posters', posterProjection!, section.id);
    }
  };
  const rows: readonly {id: string; label: MessageId; icon: IconName; count?: number; view: SavedRoute}[] = [
    {id: 'favorites', label: 'saved.tab.favorites', icon: 'i-heart', count: summary && summary.favorites.posters + summary.favorites.squares, view: {view: 'favorites'}},
    {id: 'playlists', label: 'saved.tab.playlists', icon: 'i-queue', count: summary?.playlists, view: {view: 'playlists'}},
    {id: 'collections', label: 'saved.tab.collections', icon: 'i-stack', count: summary?.collections, view: {view: 'collections'}},
    {id: 'views', label: 'saved.tab.savedViews', icon: 'i-filter', count: summary?.savedViews, view: {view: 'views'}},
    {id: 'history', label: 'saved.tab.history', icon: 'i-clock', view: {view: 'history'}},
  ];
  // phone-app9 draws each shelf as the phone row: a 19 heading with its arrow,
  // the count beside it, and a 12-gap track inset by the 20 gutter.
  const track = {paddingHorizontal: theme.gutter, paddingTop: theme.spec.shelf.phonePaddingTop, paddingBottom: theme.spec.shelf.phonePaddingBottom, gap: theme.spec.shelf.gap[theme.platform]};
  const pinKind = (kind: PinnedResource['kind']) => kind === 'view' ? t('title.savedView') : t('title.kind', {kind});
  return <View testID="native-saved-screen" pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible}
    importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}}><StandardBackdrop/>
    {tablet && <View style={{paddingTop: Math.max(insets.top, 10)}}>
      <Text accessibilityRole="header" style={[browseType(theme, 32, 800, -.035, 36.8), {color: theme.colors.text, marginHorizontal: theme.gutter}]}>{t('saved.title')}</Text>
      <Tabs items={tabItems} value={savedTab} onValueChange={chooseTab} accessibilityLabel={t('saved.title')} testID="saved-tablet-tabs" style={{marginTop: 8}}/>
    </View>}
    {downloadsOpen ? <View style={{flex: 1}}>{downloads(runtime)}</View>
      : otherTab ? <View style={{flex: 1}}>{tabBody?.(otherTab, runtime)}</View>
      : <ScrollView ref={scroll} refreshControl={pull} onLayout={event => {viewport.current.height = event.nativeEvent.layout.height; nextPosters();}}
        onContentSizeChange={(_width, height) => {viewport.current.extent = height; nextPosters();}}
        onScroll={event => {viewport.current.offset = event.nativeEvent.contentOffset.y; nextPosters();}} scrollEventThrottle={64} contentContainerStyle={{paddingTop: tablet ? 8 : Math.max(58, insets.top), paddingBottom: 28}}>
      {!tablet && <View style={{paddingHorizontal: theme.gutter, paddingBottom: 12}}>
        <Text accessibilityRole="header" style={[browseType(theme, 32, 800, -.035, 36.8), {color: theme.colors.text}]}>{t('saved.title')}</Text>
      </View>}
      {documentFacts && (theme.isTV || !transportFailure || !retainedDocument) && <StateView variant="inline" title={t('saved.loadFailed')} body={documentFacts.body}
        primaryAction={{id: 'retry', label: t('saved.retry'), onPress: () => {void destination.load().catch(report);}}}/>}
      {tablet ? <View style={{marginTop: 12}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, marginHorizontal: theme.gutter}}>
          {admitted && <><Chip ref={filterRef} testID="saved-my-list-filters" icon="i-filter" onPress={() => setFilterOpen(true)} accessibilityLabel={t('next.browse.native.filters')}>{t('next.browse.native.filters')}</Chip>
          <Chip ref={sortRef} testID="saved-my-list-sort" icon="i-sort" trailingIcon="i-down" onPress={() => setSortOpen(true)} accessibilityLabel={t('next.browse.native.sort')}>{t('next.browse.native.sort')}</Chip></>}
          {admitted && selection?.start && <Chip testID="saved-my-list-select" icon="i-check" onPress={() => selection.start?.(snapshot.myList)} accessibilityLabel={t('next.selection.select')}>{t('next.selection.select')}</Chip>}
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 2, marginLeft: 'auto'}}>
            <Focusable testID="saved-my-list-grid" accessibilityLabel={t('next.browse.native.grid')} accessibilityState={{selected: !listMode}} onPress={() => setListMode(false)} focusRadius={8}
              style={{width: 34, height: 34, alignItems: 'center', justifyContent: 'center', borderRadius: 8, backgroundColor: !listMode ? theme.colors.raisedBright : undefined}}>
              <Icon name="i-grid" size={18} color={!listMode ? theme.colors.text : theme.colors.textTertiary}/>
            </Focusable>
            <Focusable testID="saved-my-list-list" accessibilityLabel={t('next.browse.native.list')} accessibilityState={{selected: listMode}} onPress={() => setListMode(true)} focusRadius={8}
              style={{width: 34, height: 34, alignItems: 'center', justifyContent: 'center', borderRadius: 8, backgroundColor: listMode ? theme.colors.raisedBright : undefined}}>
              <Icon name="i-list" size={18} color={listMode ? theme.colors.text : theme.colors.textTertiary}/>
            </Focusable>
            {myListCount !== undefined && <Text style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary, marginLeft: 8}]}>{t('count.titles', {count: myListCount})}</Text>}
          </View>
        </View>
        {!!filterOptions.length && <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: theme.gutter, paddingTop: 10, gap: 8}}>
          {filterOptions.map(option => <Chip key={`${option.field}:${option.id}`} variant="quick" selected={posterProjection?.query.category === option.id} count={option.count}
            onPress={() => applyMyList({filter: option.id as SavedListFilter})}>{option.label}</Chip>)}
        </ScrollView>}
        {!posterProjection || snapshot.phase !== 'ready' && snapshot.phase !== 'loading' ? ((snapshot.phase === 'idle' || snapshot.phase === 'loading') ? <SavedLoading columns={myListColumns}/> : null)
          : !posterEntries.length && !squareEntries.length ? <StateView variant="inline" title={t('web.saved.empty.watchlist')} body={t('web.saved.empty.watchlistBody')}/>
          : listMode ? <View style={{marginTop: 8}}>{posterEntries.map(entry => <SavedMyListRow key={entry.id} entry={entry} runtime={runtime} actions={actions} selection={admitted ? selection?.item(entry) : undefined}/>)}</View>
          : <View onLayout={event => setGridWidth(event.nativeEvent.layout.width)} style={{flexDirection: 'row', flexWrap: 'wrap', marginHorizontal: theme.gutter - 5, marginTop: 8}}>
            {posterEntries.map(entry => <View key={entry.id} style={{width: `${100 / myListColumns}%`, padding: 5}}>
              <SavedMyListCard entry={entry} runtime={runtime} actions={actions} selection={admitted ? selection?.item(entry) : undefined} width={gridWidth ? Math.floor(gridWidth / myListColumns - 10) : undefined}/>
            </View>)}
          </View>}
        {snapshot.myList.phase === 'loading' && !!posterEntries.length && <View style={{marginHorizontal: theme.gutter, marginTop: 12}}><SavedTail width={gridWidth ? gridWidth / myListColumns - 10 : 118}/></View>}
        {!!squareEntries.length && <ScrollView horizontal showsHorizontalScrollIndicator={false} scrollEventThrottle={64}
          onScroll={event => {const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
            const section = squareProjection?.sections.find(value => value.nextCursor);
            if (section && savedNearEnd(contentOffset.x, layoutMeasurement.width, contentSize.width, 144)) nextMyListPage('squares', squareProjection!, section.id);}}
          contentContainerStyle={{paddingHorizontal: theme.gutter, paddingTop: 12, gap: 12}}>
          {squareEntries.map(entry => <SavedMyListCard key={entry.id} entry={entry} runtime={runtime} actions={actions} width={132} selection={admitted ? selection?.item(entry) : undefined}/>)}
          {snapshot.myListSquares.phase === 'loading' && <SavedTail width={132} square/>}
        </ScrollView>}
      </View> : <SavedSection first>
        <SavedRowHeading testID="saved-my-list" title={t('saved.tab.watchlist')} detail={summary ? t('next.selection.titles', {count: summary.myList.posters + summary.myList.squares}) : undefined} onPress={() => actions.openView({view: 'watchlist'})}/>
        {(snapshot.phase === 'idle' || snapshot.phase === 'loading') && !items.length && <SavedLoading kind="shelf"/>}
        {!!items.length && <ScrollView horizontal showsHorizontalScrollIndicator={false} scrollEventThrottle={64}
          onScroll={event => {const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent; const section = posterProjection?.sections.find(value => value.nextCursor);
            if (section && savedNearEnd(contentOffset.x, layoutMeasurement.width, contentSize.width, theme.spec.shelf.gap[theme.platform] + 118)) nextMyListPage('posters', posterProjection!, section.id);}} contentContainerStyle={track}>
          {items.map(entry => <SavedMyListCard key={entry.id} entry={entry} runtime={runtime} actions={actions} selection={admitted ? selection?.item(entry) : undefined}/>)}
          {snapshot.myList.phase === 'loading' && <SavedTail width={118}/>}
        </ScrollView>}
      </SavedSection>}
      {!tablet && deviceState.supported && <SavedSection>
        <SavedRowHeading testID="saved-downloads" title={t('downloads.title')} detail={t(onThisPhone, {size: defaultI18n.bytes(deviceState.usedBytes)})} onPress={showDownloads}/>
        {!!localGroups.length && <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={track}>
          {localGroups.map(group => <SavedDownloadCard key={group.key} group={group} device={device} copy={downloadsCopy} onOpen={() => openNativeSavedDownload(device, group, showDownloads, report)}/>)}
        </ScrollView>}
      </SavedSection>}
      {!tablet && <View style={{marginHorizontal: 16, marginTop: 24, borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>
        {rows.map((row, index) => <SavedLink key={row.id} testID={`saved-view-${row.id}`} label={t(row.label)} icon={row.icon} value={row.count === undefined ? undefined : defaultI18n.number(row.count)} separator={index > 0} onPress={() => actions.openView(row.view)}/>)}
      </View>}
      <View style={{flexDirection: 'row', alignItems: 'baseline', justifyContent: 'space-between', marginTop: 24, marginHorizontal: theme.gutter, marginBottom: 6}}>
        <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{t('pins.heading')}</Text>
        <Focusable testID="saved-edit-pins" accessibilityLabel={t('pins.edit')} onPress={actions.editPins} hitSlop={12}><Text style={[browseType(theme, 14, 600), {color: theme.colors.textTertiary}]}>{t('pins.edit')}</Text></Focusable>
      </View>
      {!!snapshot.pins.length && <View style={{marginHorizontal: 16, borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>
        {snapshot.pins.map((pin, index) => <SavedLink key={JSON.stringify([pin.kind, pin.id])} testID={`saved-pin-${pin.kind}-${encodeURIComponent(pin.id)}`} label={pin.name} caption={pinKind(pin.kind)}
          icon={pin.kind === 'playlist' ? 'i-queue' : pin.kind === 'collection' ? 'i-stack' : 'i-filter'} separator={index > 0} onPress={() => actions.openPin(pin)}/>)}
      </View>}
    </ScrollView>}
    {!downloadsOpen && !otherTab && admitted && selection?.render()}
    <Menu testID="saved-my-list-filter-menu" open={admitted && filterOpen} onOpenChange={setFilterOpen} anchorRef={filterRef} returnFocusRef={filterRef} title={t('filter.title')} items={filterItems}/>
    <Menu testID="saved-my-list-sort-menu" open={admitted && sortOpen} onOpenChange={setSortOpen} anchorRef={sortRef} returnFocusRef={sortRef} title={t('next.browse.native.sort')} items={sortItems}/>
  </View>;
}

/** The phone row opens its full view and keeps the drawn 28 shelf rhythm; the
 * first row sits 2 below the large title's 12 bottom padding. */
function SavedSection({first = false, children}: {first?: boolean; children: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{marginTop: first ? 2 : theme.spec.shelf.marginTop[theme.platform]}}>{children}</View>;
}

function SavedMyListRow({entry, runtime, actions, selection}: {entry: ContentEntry; runtime: NativeShellRuntime; actions: NativeSavedActions; selection?: CardSelection}) {
  const theme = useTheme(), text = homeCardText(entry, false, runtime.text), menuAnchorRef = useRef<View>(null);
  return <View style={{marginHorizontal: 16, borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.lineStrong, .45)}}>
    <CardFrame title={entry.displayTitle || entry.title} menuAnchorRef={menuAnchorRef} moreExpanded={actions.isMenuOpen?.(entry)} testID={`saved-my-list-row-${encodeURIComponent(entry.id)}`}
      onNavigate={() => actions.openEntry(entry)} onMore={() => actions.menuEntry(entry, menuAnchorRef, selection?.onEnter)} selection={selection} pressableStyle={{flexDirection: 'row', alignItems: 'center', gap: 13, paddingVertical: 9}}>
      {() => <>
        {selection?.active && <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title: entry.displayTitle || entry.title})}/>}
        <Artwork source={runtime.artwork(entry.posterUrl)} width={46} height={69} style={{borderRadius: 6}}/>
        <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[browseType(theme, 15, 700), {color: theme.colors.text}]}>{entry.displayTitle || entry.title}</Text>
          <Parts parts={text.caption} gap={8} style={[browseType(theme, 12.5, 400), {color: theme.colors.textTertiary, marginTop: 2}]}/></View>
      </>}
    </CardFrame>
  </View>;
}

function SavedMyListCard({entry, runtime, actions, selection, width}: {entry: ContentEntry; runtime: NativeShellRuntime; actions: NativeSavedActions; selection?: CardSelection; width?: number}) {
  const menuAnchorRef = useRef<View>(null), text = homeCardText(entry, false, runtime.text);
  const props = {testID: `saved-my-list-${encodeURIComponent(entry.id)}`, title: entry.displayTitle || entry.title, caption: text.caption, ...(width === undefined ? {} : {width}),
    artwork: runtime.artwork(entry.posterUrl), onNavigate: () => actions.openEntry(entry), onMore: () => actions.menuEntry(entry, menuAnchorRef, selection?.onEnter), selection,
    menuAnchorRef, moreExpanded: actions.isMenuOpen?.(entry)};
  return entry.kind === 'song' || entry.kind === 'album' || entry.kind === 'book' || entry.kind === 'audiobook_file' ? <SquareCard {...props}/> : <PosterCard {...props}/>;
}

function SavedRowHeading({title, detail, onPress, testID}: {title: string; detail?: string; onPress: () => void; testID: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', gap: theme.spec.shelf.headingGap[theme.platform], marginHorizontal: theme.gutter,
    marginBottom: theme.spec.shelf.headingMargin[theme.platform]}}>
    <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={title} onPress={onPress} focusRadius={8}
      style={{flexDirection: 'row', alignItems: 'center', gap: 1, flexShrink: 1}}>
      <Text accessibilityRole="header" numberOfLines={1} style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
    </Focusable>
    {detail && <Text numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textTertiary}]}>{detail}</Text>}
  </View>;
}

function SavedLink({label, caption, icon, value, separator, onPress, testID}: {label: string; caption?: string; icon: IconName; value?: string; separator: boolean; onPress: () => void; testID: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityLabel={label} onPress={onPress} style={{position: 'relative', flexDirection: 'row', alignItems: 'center', minHeight: 48, paddingVertical: 8, paddingHorizontal: 14, gap: 12}}>
    {separator && <View style={{position: 'absolute', left: 56, right: 0, top: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>}
    <View style={{width: 30, height: 30, borderRadius: 8, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedBright}}><Icon name={icon} size={18} color={theme.colors.textSecondary}/></View>
    <View style={{flex: 1, minWidth: 0}}>
      <Text numberOfLines={1} style={[browseType(theme, 15.5, 600), {color: theme.colors.text}]}>{label}</Text>
      {caption && <Text numberOfLines={1} style={[browseType(theme, 12.5, 500, 0, 17.5), {color: theme.colors.textTertiary, marginTop: 2}]}>{caption}</Text>}
    </View>
    {value !== undefined && <Text style={[browseType(theme, 14.5, 600), {color: theme.colors.textTertiary}]}>{value}</Text>}
    <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
  </Focusable>;
}
