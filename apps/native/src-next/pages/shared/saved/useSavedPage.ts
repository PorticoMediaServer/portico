import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {browseNameId} from '../../../../../../packages/client-core/src/presentation/browse-toolbar';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {PinnedResource} from '../../../../../../packages/client-core/src/library-pins';
import type {PersonalResource} from '../../../../../../packages/client-core/src/personal-saved';
import type {SavedEntry, SavedListFilter, SavedRoute, SavedSnapshot} from '../../../../../../packages/client-core/src/saved';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {CardSelection} from '../../../kit/cards';
import {usePullRefresh} from '../../../kit/states/usePullRefresh';
import {useTheme} from '../../../kit/theme';
import {groupOfflineEntries, type NativeOfflineDownloadsDevice} from '../../../platform/offline-downloads';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSavedDestination} from '../../../screens/saved/model';
import {savedNearEnd} from '../../../screens/saved/paging';
import type {NativeSavedDownloadsCopy} from '../../../kit/saved/SavedDownloadCard';
import type {NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import type {NativeEntryMenuState} from '../../../screens/destinations/entry-actions';
import {registerBack} from '../../../kit/navigation/back';
import type {ScrollView, View} from 'react-native';
import type {IconName} from '../../../kit/Icon';
import {cardTitleLine} from '../../../kit/cards/caption-metrics';

const t = defaultI18n.t;
const content = (entry: SavedEntry): entry is ContentEntry => entry.kind !== 'playlist' && entry.kind !== 'playlist_entry';
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
  render: (artwork?: (path?: string) => ReactNode) => ReactNode;
}>;
export type SavedPageProps = Readonly<{
  destination: NativeSavedDestination; runtime: NativeShellRuntime; device: NativeOfflineDownloadsDevice;
  actions: NativeSavedActions; selection?: NativeSavedSelectionPort; onThisPhone: MessageId;
  downloadsCopy: NativeSavedDownloadsCopy;
  activation: NativeDestinationActivationPort;
  /** Saved's own Downloads tab body. Present on handhelds that draw the wider Saved chrome. */
  downloads?: (runtime: NativeShellRuntime) => ReactNode;
  tabBody?: (tab: 'favorites' | 'playlists' | 'collections' | 'views' | 'history', runtime: NativeShellRuntime) => ReactNode;
}>;


export function useSavedPage({destination, runtime, device, actions, selection, activation, downloads}: SavedPageProps) {
  useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  useSyncExternalStore(actions.menuState?.subscribe ?? (() => () => {}), actions.menuState?.getSnapshot ?? (() => 0));
  const theme = useTheme(), scroll = useRef<ScrollView>(null);
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
    const subscription = registerBack(() => {if (!activation.getSnapshot().visible) return false; setSavedTab('watchlist'); return true;}, {scope: 'page', active: () => activation.getSnapshot().visible});
    return () => subscription();
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
  // A sort is named as the library names it ("Date added", "Title"); the server's `sort.<id>` keys are not catalogue words.
  const sortName = (sort: {id: string; labelKey: string}) => {const key = browseNameId(sort.id, sort.labelKey, defaultI18n.has); return key ? t(key as MessageId) : sort.id;};
  const sortItems = (posterProjection?.sorts ?? []).map(sort => ({id: sort.id, label: sortName(sort), selected: posterProjection?.query.sort === sort.id,
    onSelect: () => {setSortOpen(false); applyMyList({sort: sort.id, direction: sort.directions.includes(posterProjection?.query.direction as never) ? posterProjection!.query.direction as 'asc' | 'desc' : sort.directions[0] ?? 'asc'});}}));
  // The Sort control reads the current order, as the library's does ("Date added  Newest first").
  const sortChoice = posterProjection?.sorts.find(sort => sort.id === posterProjection.query.sort);
  const sortDirection = posterProjection?.query.direction === 'asc' || posterProjection?.query.direction === 'desc' ? posterProjection.query.direction : undefined;
  const sortLabel = sortChoice ? sortName(sortChoice) : undefined;
  const sortDetail = sortChoice && sortDirection && sortChoice.directions.length > 1 ? {field: sortChoice.id, direction: sortDirection as 'asc' | 'desc'} : undefined;
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
    const stride = listMode ? 88 : Math.max(118, gridWidth / myListColumns) * 1.5 + 58 + (theme.isTV ? 0 : cardTitleLine(theme));
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
  const nextShelf = (shape: 'posters' | 'squares', offset: number, width: number, extent: number) => {
    const projection = shape === 'posters' ? posterProjection : squareProjection;
    const section = projection?.sections.find(value => value.nextCursor);
    if (projection && section && savedNearEnd(offset, width, extent, shape === 'posters' ? theme.spec.shelf.gap[theme.column] + 118 : 144)) {
      nextMyListPage(shape, projection, section.id);
    }
  };
  return {active, tablet, snapshot, deviceState, admitted, documentFacts, transportFailure, retainedDocument, report,
    savedTab, chooseTab, tabItems, downloadsOpen, otherTab, scroll, pull, viewport, nextPosters, nextShelf,
    myListColumns, gridWidth, setGridWidth, listMode, setListMode, filterRef, sortRef, filterOpen, setFilterOpen, sortOpen, setSortOpen,
    posterProjection, posterEntries, squareEntries, summary, myListCount, filterOptions, sortItems, sortLabel, sortDetail, filterItems, applyMyList, localGroups, rows, showDownloads};
}
