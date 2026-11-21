import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ReadingItem} from '../../../../../../packages/client-core/src';
import type {BrowseCapabilities} from '../../../../../../packages/client-core/src/browse';
import {readLibrarySummaries, type LibrarySummary} from '../../../../../../packages/client-core/src/library-summary';
import {authorPredicate, libraryTabs, readingTabModel, type LibraryTabId} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import type {BrowseLabels} from '../../../kit/browse';
import {useTheme} from '../../../kit/theme';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useReadingActions} from '../read/useReadingActions';
import type {NativeReadingActionOffer} from '../../../screens/read/reading-actions';
import {NativeReadingWindow} from '../../../screens/read/reading-window';
import {isReadingLibraryKind} from '../../../screens/read/platform';
import {nativePublicationReadsAvailable} from '../../../../platform/offline-reading';
import {askNativeDownload} from '../../../screens/downloads/request';
import {NativeReadingDownloads, nativeReadingPageOpening, nativeReadingDownloadOffer, type NativeReadingPageOpening, type NativeReadingDownloadOffer, type NativeReadingOfferLifetime} from '../../../screens/read/reading-page-opening';
import {NativeReadingDiscovery} from '../../../screens/read/reading-discovery';
import {NativeReadingLibrary} from '../../../screens/read/reading-library';

export type BooksPageProps = Readonly<{
  context: NativeShellBootstrapContext;
  runtime: NativeShellRuntime;
  libraryId: string;
  title: string;
  labels: BrowseLabels;
  onBack: () => void;
}>;

export function useBooksLibrary(props: BooksPageProps) {
  const {context, runtime, libraryId, title} = props, theme = useTheme();
  const library = context.libraries().find(value => value.id === libraryId);
  const navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const {route: renderedRoute, revision: renderedRevision} = navigation;
  const [pageOpening, setPageOpening] = useState<NativeReadingPageOpening>();
  const libraryKind = library?.kind;
  useLayoutEffect(() => {
    const opening = libraryKind ? nativeReadingPageOpening(context, {libraryId, kind: libraryKind}, {route: renderedRoute, revision: renderedRevision}) : undefined;
    setPageOpening(opening);
    return () => opening?.dispose();
  }, [context, libraryId, libraryKind, renderedRoute, renderedRevision]);
  const opening = pageOpening?.context === context && pageOpening.libraryId === libraryId && pageOpening.kind === libraryKind
    && pageOpening.origin.route === navigation.route && pageOpening.origin.revision === navigation.revision && pageOpening.isCurrent() ? pageOpening : undefined;
  const scopedContext = useMemo<NativeShellBootstrapContext>(() => ({...context, current: () => opening?.isCurrent() === true}), [context, opening]);
  const kind = library?.kind === 'comic' ? 'comic' : 'book';
  const name = library?.name ?? title, authoredTabs = useMemo(() => libraryTabs(kind), [kind]);
  const [tab, setTab] = useState<LibraryTabId>('discover'), fellBack = useRef<NativeReadingPageOpening | undefined>(undefined);
  const [viewLife, setViewLife] = useState<NativeReadingOfferLifetime & Readonly<{opening: NativeReadingPageOpening; tab: LibraryTabId}>>();
  useLayoutEffect(() => {
    if (!opening) return;
    const life = new AbortController();
    setViewLife(Object.freeze({opening, tab, signal: life.signal, isCurrent: () => !life.signal.aborted && opening.isCurrent()}));
    return () => life.abort();
  }, [opening, tab]);
  const visibleLife = viewLife && viewLife.opening === opening && viewLife.tab === tab && viewLife.isCurrent() ? viewLife : undefined;
  const downloads = useMemo(() => new NativeReadingDownloads(opening, () => visibleLife?.isCurrent() === true && nativePublicationReadsAvailable(), askNativeDownload), [opening, visibleLife]);
  useEffect(() => () => downloads.dispose(), [downloads]);
  const downloadMenu = useSyncExternalStore(downloads.subscribe, downloads.getSnapshot);
  const offerDownload = (offer: NativeReadingDownloadOffer) => {downloads.offer(() => offer); downloads.select(downloads.getSnapshot());};
  // tablet-read#1b: the wide header carries the count of the view the grid is
  // showing, tagged with the library and the mount that published it. A line is
  // drawn only for this library, and only its own owner can retire it, so no
  // passive clear can erase a fresh line and no old library's count can show here.
  const [gridCount, setGridCount] = useState<Readonly<{owner: object; opening: NativeReadingPageOpening; libraryId: string; text: string}>>();
  const projectGridCount = useCallback((owner: object, text: string | undefined) => {
    setGridCount(current => {
      if (!text) return current?.owner === owner ? undefined : current;
      if (!opening || !opening.isCurrent()) return current;
      return Object.freeze({owner, opening, libraryId, text});
    });
  }, [libraryId, opening]);
  const [summary, setSummary] = useState<Readonly<{opening: NativeReadingPageOpening; value?: LibrarySummary}>>();
  /** Root §3: the server's own reading pivots decide the Books grid; undefined means this server
   * publishes none, and the authored pair keeps the bounded Reading list on every width. */
  const [reading, setReading] = useState<Readonly<{opening: NativeReadingPageOpening; phase: 'ready' | 'error'; data?: BrowseCapabilities}>>();
  useEffect(() => {
    if (!opening || theme.isTV) return;
    let active = true;
    void opening.capabilities().then(data => {
      if (active && opening.isCurrent()) setReading({opening, phase: 'ready', data});
    }, () => {if (active && opening.isCurrent()) setReading({opening, phase: 'error'});});
    return () => {active = false;};
  }, [opening, theme.isTV]);
  const capability = reading && opening && reading.opening === opening && opening.isCurrent() ? reading : undefined;
  // The server's own reading pivots, the same tab model the web's reading library draws (#3278): Books, and
  // Authors and Series when it publishes them, each on the shared library grid.
  const pivotTabs = useMemo(() => capability?.phase === 'ready' && capability.data
    ? readingTabModel(capability.data)?.filter(value => value.id !== 'discover') : undefined, [capability]);
  const gridTab = useMemo(() => pivotTabs?.find(value => value.id === 'browse'), [pivotTabs]);
  const tabs = useMemo(() => gridTab && pivotTabs ? [authoredTabs[0]!, ...pivotTabs] : authoredTabs, [authoredTabs, gridTab, pivotTabs]);
  const gridSelected = pivotTabs?.find(value => value.id === tab);
  /** An author's card opens the Books grid filtered to them, as their name on a book does. */
  const [authorOpen, setAuthorOpen] = useState<Readonly<{opening: NativeReadingPageOpening; personId: string}>>();
  const selectTab = (id: LibraryTabId) => {setAuthorOpen(undefined); setTab(id);};
  useEffect(() => {
    if (!opening) return;
    const ab = new AbortController();
    void readLibrarySummaries(context.api, ab.signal).then(rows => {const row = rows.find(value => value.id === libraryId); if (!ab.signal.aborted && opening.isCurrent()) setSummary({opening, value: row?.summary});}, () => {});
    return () => ab.abort();
  }, [context, libraryId, opening]);
  const discovery = useMemo(() => new NativeReadingDiscovery(scopedContext, libraryId), [scopedContext, libraryId]);
  const books = useMemo(() => new NativeReadingLibrary(scopedContext, libraryId), [scopedContext, libraryId]);
  useEffect(() => () => {discovery.dispose(); books.dispose();}, [discovery, books]);
  const discoveryState = useSyncExternalStore(discovery.subscribe, discovery.getSnapshot);
  const booksState = useSyncExternalStore(books.subscribe, books.getSnapshot);
  const readingActions = useReadingActions(scopedContext, runtime, visibleLife ?? discovery);
  const booksWindow = useMemo(() => new NativeReadingWindow(() => {const value = books.getSnapshot();
    return {items: value.items, cursor: value.nextCursor, loading: value.phase === 'loading', error: value.phase === 'error' ? value.error : undefined};
  }, books.subscribe, books.more, item => `${item.id}:${item.sourceId}`), [books]);
  useEffect(() => {
    if (!opening) return;
    if (tab === 'discover') void discovery.load().catch(runtime.reportError);
    else if (theme.isTV || (capability && !gridTab)) void books.load().catch(runtime.reportError);
  }, [tab, discovery, books, runtime, opening, theme.isTV, capability, gridTab]);
  // A ready Discover with neither real row has nothing to show; the library's
  // own Books/Volumes grid is the honest destination, never a false empty copy.
  useEffect(() => {
    if (opening?.isCurrent() && tab === 'discover' && fellBack.current !== opening && discoveryState.phase === 'ready' && !discoveryState.continueItems.length
      && !discoveryState.recent.length && !discoveryState.shortReads.length) {fellBack.current = opening; setTab('browse');}
  }, [tab, discoveryState, opening]);
  const readingLibraries = context.libraries().filter(value => isReadingLibraryKind(value.kind));
  const openDiscover = (item: ReadingItem) => {void discovery.open(item).catch(runtime.reportError);};
  const openBooks = (item: ReadingItem) => {void books.open(item).catch(runtime.reportError);};
  const offerDiscoverDownload = (item: ReadingItem) => {downloads.offer(() => opening && visibleLife ? nativeReadingDownloadOffer(opening, item, () => {
    const live = discovery.getSnapshot();
    return live === discoveryState && item.libraryId === libraryId && (live.recent.some(candidate => candidate === item)
      || live.shortReads.some(row => row.item === item) || live.continueItems.some(row => row.item === item));
  }, visibleLife) : undefined); downloads.select(downloads.getSnapshot());};
  const offerBooksDownload = (item: ReadingItem) => {downloads.offer(() => opening && visibleLife ? nativeReadingDownloadOffer(opening, item,
    () => books.getSnapshot() === booksState && item.libraryId === libraryId && booksState.items.some(candidate => candidate === item), visibleLife) : undefined); downloads.select(downloads.getSnapshot());};
  const readingOffer = (item: ReadingItem, source: 'discover' | 'books'): NativeReadingActionOffer | undefined => {
    // N5-4: a title the server can't serve now takes no menu or selection (as web).
    if (!visibleLife || item.inventoryAvailable !== true) return undefined;
    const isCurrent = () => visibleLife.isCurrent() && (source === 'books' ? books.getSnapshot().items.includes(item)
      : discovery.getSnapshot().recent.includes(item) || discovery.getSnapshot().shortReads.some(row => row.item === item) || discovery.getSnapshot().continueItems.some(row => row.item === item));
    return {item, lifetime: {signal: visibleLife.signal, isCurrent}, open: () => source === 'books' ? openBooks(item) : openDiscover(item),
      ...(nativePublicationReadsAvailable() ? {download: () => source === 'books' ? offerBooksDownload(item) : offerDiscoverDownload(item)} : {})};
  };
  const retryDiscover = () => {void discovery.retry().catch(runtime.reportError);};
  const retryBooks = () => {void books.retry().catch(runtime.reportError);};
  const pivots: Readonly<Record<string, string>> = {book: 'books', ebook: 'books', comic: 'titles', magazine: 'titles', newspaper: 'titles'};
  const counts = (summary?.opening === opening ? summary?.value?.counts ?? [] : []).filter(part => pivots[part.kind]).map(part =>
    defaultI18n.t(part.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: part.count, pivot: pivots[part.kind]!}));
  const openLibrary = (id: string) => {if (id !== libraryId) runtime.onDestination({kind: 'library', libraryId: id});};
  const selectAuthor = gridSelected?.id === 'authors' && opening ? (personId: string) => {
    if (opening.isCurrent()) {setAuthorOpen({opening, personId}); setTab('browse');}
  } : undefined;
  const currentAuthor = authorOpen?.opening === opening ? authorOpen?.personId : undefined;
  const grid = !theme.isTV && opening && gridTab && library ? {
    key: `${(gridSelected ?? gridTab).id}:${currentAuthor ?? ''}`,
    context: scopedContext, opening, runtime, library, tab: gridSelected ?? gridTab, labels: props.labels,
    owner: context.session()?.viewer.role === 'owner', onCount: projectGridCount,
    onDownload: offerDownload, onAuthor: selectAuthor,
    startQuery: (gridSelected ?? gridTab).id === 'browse' && currentAuthor ? [authorPredicate(currentAuthor)] : undefined,
  } : undefined;
  return {name, tab, tabs, selectTab, openLibrary, readingLibraries, grid,
    counts: gridCount && gridCount.libraryId === libraryId && gridCount.opening === opening ? [gridCount.text] : counts,
    waiting: !opening || tab !== 'discover' && !theme.isTV && !capability, showSelection: !gridTab || tab === 'discover',
    discoveryState, booksState, readingActions, booksWindow, openDiscover, openBooks, readingOffer,
    retry: tab === 'discover' ? retryDiscover : retryBooks, pageMore: (row: 'recent' | 'short-reads') => {void discovery.more(row).catch(runtime.reportError);},
    showBooks: () => setTab('browse'),
    downloadMenu, downloadOpen: !!downloadMenu && downloadMenu.opening === opening && downloadMenu.lifetime.isCurrent(),
    dismissDownload: () => downloads.dismiss(downloadMenu), startDownload: () => downloads.select(downloadMenu)};
}
