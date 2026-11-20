import type {ComponentProps, ReactNode} from 'react';
import type {MusicMixesRow} from '../../../screens/destinations/music/MusicMixesRow';
import {useCallback, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {BrowseNode} from '../../../../../../packages/client-core/src/browse';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {appliedFilterParts, browseToolbarModel} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {libraryTabs, type LibraryTabId} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {shareLinkActions} from '../../../screens/share/share-actions';
import {nativeLibraryCanSave, nativeLibraryEntryKey, type NativeLibrarySnapshot} from '../../../screens/library/model';
import {libraryAlphabetRail} from '../../../screens/library/jump';
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
    menu: tv ? undefined : {...runtime.libraryMenu(state.library), canSaveView: !!state.sort && nativeLibraryCanSave(state, {predicates: state.predicates, sort: state.sort}),
      onSaveView: save, onMarkAll: actions.markAll && state.collection ? markAll : undefined,
      shareActions: shareLinkActions(runtime.shareScope?.(), 'library', {libraryId: state.library.id}, {notice: runtime.notice, report: runtime.reportError})},
    save,
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
  const appliedChips = applied.flatMap(node => 'field' in node ? [{id: JSON.stringify(node), parts: appliedFilterParts(node, id => {
    const field = source.capabilities?.fields.find(candidate => candidate.id === id);
    return field ? props.labels.field(field) : id;
  }, runtime.text)}] : []);
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
