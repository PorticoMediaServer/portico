import type {ComponentProps, ReactNode} from 'react';
import type {MusicMixesRow} from '../../../screens/destinations/music/MusicMixesRow';
import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {BrowseNode} from '../../../../../../packages/client-core/src/browse';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {browseToolbarModel} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {libraryTabs, type LibraryTabId} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {shareLinkActions} from '../../../screens/share/share-actions';
import {nativeLibraryCanSave, nativeLibraryEntryKey, type NativeLibrarySnapshot} from '../../../screens/library/model';
import {libraryAppliedFilters} from '../../../screens/library/applied-filters';
import {libraryAlphabetRail, libraryJumpLetter, libraryJumpLetters} from '../../../screens/library/jump';
import {createDestinationEntryFocus, nativeDestinationFocusIds} from '../../../screens/destinations/focus';
import {useTVPageBack} from '../../../screens/destinations/useTVPageBack';
import {useDestinationEntryFocus} from '../../../kit/focus/useDestinationEntryFocus';
import {useFocusRegistry} from '../../../kit/focus';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLibraryFilterDraft} from '../../../screens/library/filters';
import type {HomeSeeAll} from '../../../../../../packages/client-core/src/home';
import {hiddenReadingLibraryOnTV} from '../../../screens/read/platform';
import type {NativeLibraryScreenProps} from '../../../screens/library/LibraryScreen';
import type {PageStateProps} from '../../../kit/frames';
import type {StateViewProps} from '../../../kit/states';
import type {WindowedGridHandle} from '../../../kit/layout';
import {usePullRefresh} from '../../../kit/states/usePullRefresh';
import {useFirstScanning} from './useFirstScanning';

export {nativeLibraryEntryKey};
export type LibraryProps = NativeLibraryScreenProps & {
  onSeeAll: (target: HomeSeeAll, title: string) => void;
  tvMusicDiscover?: ReactNode;
  mixes?: (events: Pick<ComponentProps<typeof MusicMixesRow>, 'onReady' | 'onLayout'>) => ReactNode;
};

const t = defaultI18n.t;
const subscribeEmpty = () => () => {};
const getEmpty = () => undefined;

export function browseFailure(view: StateViewProps): PageStateProps {
  return {icon: view.icon ?? 'i-warn', tone: view.tone, title: typeof view.title === 'string' ? view.title : t('next.states.pageFailed'),
    body: typeof view.body === 'string' ? view.body : undefined, primaryAction: view.primaryAction, secondaryAction: view.secondaryAction, help: view.help, testID: view.testID};
}

export function useLibrary(props: LibraryProps, tv = false) {
  const {activation, destination, directory, runtime, libraries, actions, shelfSelection, owner, firstScan} = props;
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const summaries = useSyncExternalStore(directory.subscribe, directory.getSnapshot);
  useSyncExternalStore(actions.menuState?.subscribe ?? subscribeEmpty, actions.menuState?.getSnapshot ?? getEmpty);
  useSyncExternalStore(shelfSelection?.subscribe ?? subscribeEmpty, shelfSelection?.getSnapshot ?? getEmpty);
  useEffect(() => {void destination.load().catch(runtime.reportError); void directory.load().catch(runtime.reportError);}, [destination, directory, runtime]);

  const [filters, setFilters] = useState<NativeLibraryFilterDraft>();
  const draft = useRef<NativeLibraryFilterDraft | undefined>(undefined);
  const [generation, setGeneration] = useState(0);
  const closeFilters = useCallback(() => {draft.current?.dispose();}, []);
  useEffect(() => closeFilters, [closeFilters, destination, state.tab.id]);
  useEffect(() => {if (!active.visible) closeFilters();}, [active.visible, closeFilters]);
  const openFilters = () => {
    closeFilters();
    const next = destination.createFilters();
    draft.current = next;
    setGeneration(value => value + 1);
    setFilters(next);
  };

  const choices = libraries.filter(library => !tv || !hiddenReadingLibraryOnTV(library.kind)).map(library => {
    const summary = summaries.libraries.find(value => value.id === library.id)?.summary;
    return {id: library.id, name: library.name, count: summary && libraryCounts(summary.counts).map(part => part.text).join(' · ')};
  });
  const artwork = summaries.libraries.find(library => library.id === state.library.id)?.summary.latestArtwork;
  const canSave = !!state.sort && nativeLibraryCanSave(state, {predicates: state.predicates, sort: state.sort});
  const save = () => {
    if (state.sort && nativeLibraryCanSave(state, {predicates: state.predicates, sort: state.sort})) actions.saveView(destination, {predicates: state.predicates, sort: state.sort});
  };
  const markAll = () => {
    const data = state.collection?.getSnapshot();
    if (data?.total === undefined || !actions.markAll) return;
    const {predicates, sort} = state;
    const filter = predicates.length === 0 ? undefined : predicates.length === 1 ? predicates[0] : {all: predicates};
    actions.markAll({selector: {query: {libraryId: state.library.id, pivot: state.tab.pivot ?? '',
      ...(filter ? {filter} : {}), ...(sort ? {sort: [{field: sort.field, direction: sort.direction}]} : {})}},
      pivot: state.tab.pivot ?? 'other', viewTotal: data.total, viewAtLeast: data.totalIsLowerBound === true, read: false,
      applied: () => {
        const now = destination.getSnapshot();
        if (now.predicates === predicates && now.sort === sort) destination.setQuery(predicates, sort);
      }});
  };
  const empty = state.phase === 'ready' && state.unfilteredTotal?.total === 0;
  const scanning = useFirstScanning(firstScan, state.library.id, empty && state.tab.shows !== 'genres' && state.tab.shows !== 'playlists');
  const emptyState: PageStateProps | undefined = empty ? scanning && firstScan ? {
    icon: 'i-library', title: t('next.states.firstScan'), body: t('next.states.firstScanBody', {library: state.library.name}),
    primaryAction: {id: 'view-activity', label: t('next.states.viewActivity'), onPress: firstScan.openActivity},
  } : {icon: 'i-library', title: t(state.tab.shows === 'genres' ? 'library.genres.empty' : state.tab.shows === 'playlists' ? 'next.selection.native.empty.playlist' : 'library.emptyLibrary'),
    body: t(state.tab.shows === 'genres' ? 'library.genres.emptyBody' : state.tab.shows === 'playlists' ? 'web.saved.playlistEmptyBody' : 'library.emptyLibraryBody'),
    primaryAction: {id: 'browse-home', label: t('saved.browseHome'), onPress: () => runtime.onDestination({kind: 'home'})}} : undefined;

  return {t, state, active, choices, ambient: artwork && ['music', 'audiobook'].includes(state.library.kind) ? runtime.ambient(artwork) : undefined,
    filters, generation, openFilters, emptyState,
    failure: state.phase === 'error' ? browseFailure(runtime.error(state.error, destination.retry, 'library')) : undefined,
    summaryError: summaries.phase === 'error',
    retrySummaries: () => {void directory.load().catch(runtime.reportError);},
    tabs: {items: libraryTabs(state.library.kind).map(tab => ({value: tab.id, label: t(tab.label)})), value: state.tab.id,
      onValueChange: (id: string) => {if (activation.getSnapshot().visible) void destination.load(id as LibraryTabId).catch(runtime.reportError);}},
    menu: tv ? undefined : {...runtime.libraryMenu(state.library), canSaveView: canSave,
      onSaveView: save, onMarkAll: actions.markAll && state.collection ? markAll : undefined,
      shareActions: shareLinkActions(runtime.shareScope?.(), 'library', {libraryId: state.library.id}, {notice: runtime.notice, report: runtime.reportError})},
    save, canSave,
    rows: state.discover.appended.filter(section => section.entries.length).map(section => {
      const title = serverTextLabel(defaultI18n, section.heading);
      const target: HomeSeeAll | undefined = section.seeAll && ('rowId' in section.seeAll
        ? {type: 'row', rowId: section.seeAll.rowId, libraryId: state.library.id}
        : {type: 'library', libraryId: state.library.id, ...section.seeAll});
      return {...section, title, onSeeAll: target && (() => {
        if (activation.getSnapshot().visible) props.onSeeAll(target, title);
      }), onVisibleRange: (_first: number, last: number) => {
        if (activation.getSnapshot().visible && section.nextCursor && last >= section.entries.length - 10) void destination.nextDiscover(section.id).catch(runtime.reportError);
      }};
    }),
  };
}

export function useLibraryGrid(props: NativeLibraryScreenProps, source: NativeLibrarySnapshot, tv = false) {
  const {activation, destination, runtime, owner} = props;
  const selection = source.tab.shows === 'genres' || source.tab.shows === 'playlists' ? undefined : props.selection;
  const collection = source.collection!;
  const data = useSyncExternalStore(collection.subscribe, collection.getSnapshot);
  const selected = useSyncExternalStore(selection?.subscribe ?? subscribeEmpty, selection?.getSnapshot ?? getEmpty);
  const grid = useRef<WindowedGridHandle>(null);
  const refresh = usePullRefresh(() => collection.revalidate(), runtime.reportError);
  const initialIndex = useRef(source.memory.firstVisibleIndex).current;
  const [range, setRange] = useState({first: initialIndex, last: initialIndex});
  const lastRange = useRef(range), offset = useRef(source.memory.offset);
  const [view, setView] = useState({value: 'grid' as 'grid' | 'list', anchor: initialIndex, revision: 0});
  const revision = useRef(0);
  const unregister = useRef<(() => void) | undefined>(undefined);
  useEffect(() => () => unregister.current?.(), [collection]);
  const top = useCallback(() => grid.current?.scrollToIndex(0), []);
  useEffect(() => {if (source.queryRevision > 0) top();}, [source.queryRevision, top]);
  useEffect(() => tv ? undefined : runtime.registerScrollToTop({kind: 'library', libraryId: source.library.id}, top), [runtime, source.library.id, top, tv]);
  const onVisibleRange = useCallback((first: number, last: number) => {
    if (revision.current !== view.revision) return;
    lastRange.current = {first, last};
    setRange(lastRange.current);
    destination.remember(first, offset.current);
    if (!selection) return;
    const entries: ContentEntry[] = [];
    for (let index = first; index <= last; index++) {
      const entry = collection.itemAt(index);
      if (entry) entries.push(entry);
    }
    unregister.current?.();
    unregister.current = selection.register(destination.getSnapshot(), entries, first, last);
  }, [collection, destination, selection, view.revision]);
  const onScrollOffset = useCallback((value: number) => {
    if (revision.current !== view.revision) return;
    offset.current = value;
    destination.remember(lastRange.current.first, value);
  }, [destination, view.revision]);
  const onValueChange = (value: 'grid' | 'list') => {
    if (!activation.getSnapshot().visible || view.value === value) return;
    offset.current = 0;
    setView({value, anchor: lastRange.current.first, revision: ++revision.current});
  };
  const model = browseToolbarModel(source.capabilities!, source.library.kind, source.predicates, source.sort!, {...runtime.text, owner, textEntry: !tv});
  const base = source.tab.fixedFilter ?? [];
  const filters = source.predicates.filter(node => !base.some(fixed => JSON.stringify(fixed) === JSON.stringify(node)));
  const applied = model.applied.filter(node => filters.some(filter => JSON.stringify(node) === JSON.stringify(filter)));
  const appliedChips = libraryAppliedFilters(applied, source.capabilities!, props.labels);
  const shape = source.tab.shows === 'genres' ? 'genre' : ['music', 'audiobook'].includes(source.library.kind) ? 'square' : 'poster';
  const clear = () => destination.setQuery(base as readonly BrowseNode[]);
  const setSort = (sort: NonNullable<NativeLibrarySnapshot['sort']>) => destination.setQuery(source.predicates, sort);
  return {grid, data, collection, selected, selection, refresh, range, view, onValueChange, onVisibleRange, onScrollOffset, top, offset,
    initialIndex: view.revision ? view.anchor : initialIndex, hasAlphabet: libraryAlphabetRail(data.positionIndex, data.total, source.sort),
    model: {...model, applied: filters}, appliedChips, shape: shape as 'genre' | 'square' | 'poster', clear, setSort,
    remove: (id: string) => destination.setQuery(source.predicates.filter(node => JSON.stringify(node) !== id)),
    reshuffle: () => setSort({...source.sort!, seed: Math.floor(Math.random() * 4294967296)}),
    filteredEmpty: data.status === 'ready' && data.total === 0 && filters.length > 0,
  };
}

const rememberedTVResource = new WeakMap<NativeShellRuntime, Map<string, Readonly<{key: string; index: number}>>>();

export function useTVLibraryGrid(props: LibraryProps, view: ReturnType<typeof useLibrary>, regionId: string, toolbarId: string) {
  const {state: source, active} = view, {runtime, activation} = props;
  const grid = useLibraryGrid(props, source, true);
  const registry = useFocusRegistry();
  const [jumpOpen, setJumpOpen] = useState(false);
  useEffect(() => {if (!active.visible) setJumpOpen(false);}, [active.visible]);
  const resource = source.tab.shows === 'genres' || source.tab.shows === 'playlists';
  const restore = useRef(resource && active.intent === 'restore' ? rememberedTVResource.get(runtime)?.get(regionId) : undefined).current;
  const startingIndex = restore?.index ?? 0;
  const page = Math.floor(startingIndex / grid.collection.pageSize);
  const subscribePage = useCallback((listener: () => void) => grid.collection.subscribePage(page, listener), [grid.collection, page]);
  const pageVersion = useCallback(() => grid.collection.getPageVersion(page), [grid.collection, page]);
  useSyncExternalStore(subscribePage, pageVersion);
  const entry = restore ? grid.collection.residentItems().find(item => nativeLibraryEntryKey(item) === restore.key) ?? grid.collection.itemAt(startingIndex) : grid.collection.itemAt(0);
  const key = entry && nativeLibraryEntryKey(entry);
  const address = key && (registry.entityAddress(regionId, key) ?? {regionId, childId: key});
  const requested = useCallback(() => {
    if (restore) grid.grid.current?.scrollToIndex(startingIndex);
    else grid.top();
  }, [grid.grid, grid.top, restore, startingIndex]);
  useDestinationEntryFocus({...active, intent: resource && active.intent === 'restore' ? 'enter' : active.intent}, address || undefined, requested);
  useTVPageBack(runtime, {kind: 'library', libraryId: source.library.id}, true, {
    registerTop: true, scope: JSON.stringify([active.revision, source.queryRevision, source.tab.id]),
    current: () => activation.getSnapshot().visible && !jumpOpen && !view.filters?.getSnapshot().open,
    start: () => {
      const first = grid.collection.itemAt(0);
      return first ? registry.entityAddress(regionId, nativeLibraryEntryKey(first)) ?? {regionId, childId: nativeLibraryEntryKey(first)} : undefined;
    },
    atTop: () => grid.offset.current <= 0, scrollTop: grid.top,
  });
  const letters = libraryJumpLetters(grid.data.positionIndex);
  const selectedLetter = libraryJumpLetter(letters, grid.range.first);
  const selected = grid.selected?.active;
  const ids = nativeDestinationFocusIds(runtime.getSnapshot().authority), tabs = ids.libraryTabs(source.library.id);
  const appliedId = `${toolbarId}.applied`;
  const applied = grid.appliedChips.length > 0;
  const count = grid.data.total === undefined ? undefined : view.t(grid.data.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: grid.data.total, pivot: source.tab.pivot ?? 'other'});
  const unfiltered = applied && source.unfilteredTotal;
  return {...grid,
    count: count && unfiltered ? view.t('title.shownOfTotal', {shown: defaultI18n.number(grid.data.total!), total: view.t(unfiltered.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: unfiltered.total, pivot: source.tab.pivot ?? 'other'})}) : count,
    initialIndex: restore ? startingIndex : grid.initialIndex,
    toolbar: {id: toolbarId, enabled: active.visible && !selected, edges: {left: ids.rail, up: tabs, down: applied ? appliedId : regionId}},
    applied: {id: appliedId, enabled: active.visible, edges: {left: ids.rail, up: toolbarId, down: regionId}},
    region: {id: regionId, entry: address ? address.childId : undefined, enabled: active.visible,
      edges: {left: ids.rail, up: applied ? appliedId : toolbarId}, screenStart: {regionId: toolbarId, childId: 'sort'}, count: grid.data.total},
    onItemFocus: (item: ContentEntry, index: number) => {
      if (!resource || !activation.getSnapshot().visible) return;
      let memory = rememberedTVResource.get(runtime);
      if (!memory) {memory = new Map(); rememberedTVResource.set(runtime, memory);}
      memory.delete(regionId);
      memory.set(regionId, {key: nativeLibraryEntryKey(item), index});
      if (memory.size > 64) memory.delete(memory.keys().next().value!);
    },
    jump: {
      open: jumpOpen, panelId: `${toolbarId}.jump`, letters, selected: selectedLetter, onClose: () => setJumpOpen(false),
      onSelect: (letter: string) => {setJumpOpen(false); grid.grid.current?.scrollToLetter(letter);},
    },
    openJump: () => setJumpOpen(true),
  };
}

export type LibraryOverviewProps = {
  directory: LibraryProps['directory']; runtime: LibraryProps['runtime']; activation: LibraryProps['activation'];
  openLibrary: (id: string) => void; editPins: () => void;
};

export function useLibraryOverview({directory, runtime, activation, openLibrary}: LibraryOverviewProps) {
  const state = useSyncExternalStore(directory.subscribe, directory.getSnapshot);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const reload = () => {void directory.load(true).catch(runtime.reportError);};
  useEffect(() => {if (active.visible) void directory.load(true).catch(runtime.reportError);}, [active.visible, active.revision, directory, runtime]);
  return {active: active.visible, t, loading: state.phase === 'loading' && !state.libraries.length,
    failure: state.phase === 'error' ? browseFailure(runtime.error(state.error, reload)) : undefined,
    libraries: state.libraries.map(({id, name, kind, summary}) => ({
      id, title: name, shape: kind === 'music' || kind === 'audiobook' ? 'square' as const : 'poster' as const,
      covers: summary.coverUrls.flatMap(path => runtime.artwork(path) ?? []),
      glyph: [...name][0],
      ambient: summary.latestArtwork ? runtime.ambient(summary.latestArtwork) : undefined,
      caption: libraryCounts(summary.counts),
      onNavigate: () => openLibrary(id),
    })),
  };
}

const countNames: Record<string, string> = {movie: 'movies', show: 'shows', episode: 'episodes', artist: 'artists', album: 'albums', song: 'songs', book: 'books', author: 'authors', series: 'series', ebook: 'books', comic: 'titles'};
function libraryCounts(counts: readonly Readonly<{kind: string; count: number; atLeast?: boolean}>[]) {
  return counts.flatMap(part => countNames[part.kind] ? [{id: part.kind, role: 'value' as const,
    text: t(part.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: part.count, pivot: countNames[part.kind]})}] : []);
}

export function useTVLibrary(props: LibraryProps) {
  const view = useLibrary(props, true), {state, active} = view;
  const ids = nativeDestinationFocusIds(props.runtime.getSnapshot().authority);
  const id = state.library.id, tabsId = ids.libraryTabs(id), toolbarId = ids.libraryToolbar(id);
  const gridId = ids.libraryGrid(id, state.tab.id), switcherId = `${tabsId}.switcher`, stateId = `${tabsId}.state`;
  const registry = useFocusRegistry(), [retryEpoch, setRetryEpoch] = useState(0), retryPressed = useRef(false);
  useEffect(() => {
    if (!active.visible) retryPressed.current = false;
    else if (state.phase === 'error' && retryPressed.current) {
      retryPressed.current = false;
      setRetryEpoch(epoch => epoch + 1);
    }
  }, [active.visible, state.phase]);
  const request = useMemo(() => {
    // A failed retry is a new request to focus Try again.
    void retryEpoch;
    return createDestinationEntryFocus(registry);
  }, [registry, retryEpoch]);
  const retry = view.failure?.primaryAction;
  const failure = view.failure && retry?.id === 'retry' ? {...view.failure, primaryAction: {...retry, onPress: (event: Parameters<typeof retry.onPress>[0]) => {
    retryPressed.current = true;
    retry.onPress(event);
  }}} : view.failure;
  const firstAction = (failure ?? view.emptyState)?.primaryAction?.id;
  useDestinationEntryFocus(active, firstAction ? {regionId: stateId, childId: firstAction} : undefined, undefined, request);
  const rowId = (index: number) => `${gridId}.row.${index}`;
  return {...view, failure, gridId, toolbarId, tabsId, stateId,
    switcher: {id: switcherId, enabled: active.visible, edges: {left: ids.rail, right: tabsId, down: tabsId}},
    tabs: {...view.tabs, region: {id: tabsId, enabled: active.visible, edges: {left: switcherId, up: switcherId,
      down: firstAction ? stateId : state.collection ? toolbarId : view.rows.length && !props.tvMusicDiscover ? rowId(0) : undefined}},
      itemProps: (item: {value: string}) => ({focusId: item.value, onFocus: () => {if (state.tab.id !== item.value) view.tabs.onValueChange(item.value);}})},
    shelves: view.rows.map((row, index) => ({...row, focus: {id: rowId(index), enabled: active.visible,
      edges: {left: ids.rail, up: index ? rowId(index - 1) : tabsId, down: index + 1 < view.rows.length ? rowId(index + 1) : undefined}}})),
    pageState: firstAction ? {id: stateId, entry: firstAction, edges: {left: ids.rail, up: tabsId}} : undefined,
  };
}
