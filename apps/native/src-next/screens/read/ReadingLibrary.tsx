import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {generatedCoverTint} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ReadingItem} from '../../../../../packages/client-core/src';
import type {BrowseCapabilities} from '../../../../../packages/client-core/src/browse';
import {readLibrarySummaries, type LibrarySummary} from '../../../../../packages/client-core/src/library-summary';
import {formatDuration} from '../../../../../packages/client-core/src/presentation/content';
import {libraryTabs, readingTabModel, type LibraryTabId} from '../../../../../packages/client-core/src/presentation/library-tabs';
import {GeneratedCover, StandardBackdrop} from '../../kit/artwork';
import {ReadResumeCard} from '../../kit/cards';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {WindowedGrid} from '../../kit/layout';
import {ReadingLoading, ReadingGridSkeleton} from './ReadingLoading';
import {NativeReadingWindow} from './reading-window';
import {ReadingItemMenu, ReadingSelectionToolbar, ReadingMenuAnchor, useReadingActions} from './ReadingActions';
import {SelectionCircle} from '../../kit/selection';
import type {NativeReadingActionOffer} from './reading-actions';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {BrowseLabels} from '../../kit/browse';
import {NativeReadingBooks} from './ReadingBooks';
import {LibraryCompactHeader, LibraryTabs} from '../library/LibraryScreen';
import {isReadingLibraryKind} from './platform';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import {askNativeDownload} from '../downloads/request';
import {NativeReadingDownloads, nativeReadingPageOpening, nativeReadingDownloadOffer, type NativeReadingPageOpening, type NativeReadingDownloadOffer, type NativeReadingOfferLifetime} from './reading-page-opening';
import {NativeReadingDiscovery} from './reading-discovery';
import {NativeReadingLibrary} from './reading-library';

export type NativeReadingLibraryScreenProps = Readonly<{
  context: NativeShellBootstrapContext;
  runtime: NativeShellRuntime;
  libraryId: string;
  title: string;
  labels: BrowseLabels;
  onBack: () => void;
}>;

const initial = (title: string): string => title.trim().slice(0, 1).toUpperCase() || '·';
const tint = (title: string) => generatedCoverTint(title);
const cover = (item: ReadingItem, width: number, height: number) => <GeneratedCover title={item.title}
  author={item.author?.value} glyph={initial(item.title)} tint={tint(item.title)} width={width} height={height}/>;
const authorPart = (item: ReadingItem) => item.author ? [{id: 'author', text: item.author.value, role: 'identity' as const}] : [];

/** phone-read frames 1 and 2: Discover (real Continue reading and Recently
 * added rows) and Books/Volumes (the bounded Reading list). The other drawn
 * rows need producers that do not exist yet, so they are omitted rather than
 * faked; no generic content browse, library series row or word-count estimate
 * is used. An item opens its own book page, whose series link reaches the
 * signed series page; the library itself lists no synthetic series row. */
export function NativeReadingLibraryScreen(props: NativeReadingLibraryScreenProps) {
  const {context, runtime, libraryId, title, labels, onBack} = props, theme = useTheme(), insets = useSafeAreaInsets(), dimensions = useWindowDimensions();
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
  const [tab, setTab] = useState<LibraryTabId>('discover'), [switcherOpen, setSwitcherOpen] = useState(false), fellBack = useRef<NativeReadingPageOpening | undefined>(undefined);
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
  const [paneWidth, setPaneWidth] = useState<number>();
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
  const tablet = !theme.isTV && theme.platform !== 'phone'; // Medium, tablet and native web windows share the wide surface.
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
  const gridTab = useMemo(() => capability?.phase === 'ready' && capability.data
    ? readingTabModel(capability.data)?.find(value => value.id === 'browse') : undefined, [capability]);
  const tabs = useMemo(() => gridTab ? [authoredTabs[0]!, gridTab] : authoredTabs, [authoredTabs, gridTab]);
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
  const switcherItems: MenuEntry[] = readingLibraries.map(value => ({id: value.id, label: value.name, selected: value.id === libraryId,
    onSelect: () => {setSwitcherOpen(false); if (value.id !== libraryId) runtime.onDestination({kind: 'library', libraryId: value.id});}}));
  const switcher = <Focusable accessibilityRole="button" focusKind="row" accessibilityLabel={defaultI18n.t('next.browse.native.switchLibraryNamed', {name})}
    disabled={readingLibraries.length < 2} onPress={() => setSwitcherOpen(true)} style={{flexDirection: 'row', alignItems: 'center', gap: 4}}>
    <Text numberOfLines={1} style={[browseType(theme, 17, 700), {color: theme.colors.text, maxWidth: 170}]}>{name}</Text>
    {readingLibraries.length > 1 ? <Icon name="i-down" size={14} color={theme.colors.textSecondary}/> : null}
  </Focusable>;
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
    if (!visibleLife) return undefined;
    const isCurrent = () => visibleLife.isCurrent() && (source === 'books' ? books.getSnapshot().items.includes(item)
      : discovery.getSnapshot().recent.includes(item) || discovery.getSnapshot().shortReads.some(row => row.item === item) || discovery.getSnapshot().continueItems.some(row => row.item === item));
    return {item, lifetime: {signal: visibleLife.signal, isCurrent}, open: () => source === 'books' ? openBooks(item) : openDiscover(item),
      ...(nativePublicationReadsAvailable() ? {download: () => source === 'books' ? offerBooksDownload(item) : offerDiscoverDownload(item)} : {})};
  };
  const retryDiscover = () => {void discovery.retry().catch(runtime.reportError);};
  const retryBooks = () => {void books.retry().catch(runtime.reportError);};
  const retry = <StateView testID="reading-library-error" variant="inline" {...runtime.error(tab === 'discover' ? discoveryState.error : booksState.error, tab === 'discover' ? retryDiscover : retryBooks, 'library')}/>;
  const empty = <StateView testID="reading-library-empty" variant="inline" title={defaultI18n.t('library.emptyLibrary')} body={defaultI18n.t('library.emptyLibraryBody')}/>;
  const columns = tablet ? 4 : 3, gap = 16, width = Math.max(96, ((paneWidth ?? dimensions.width) - theme.gutter * 2 - gap * (columns - 1)) / columns);
  const loading = <ReadingLoading ready={false} style={{flex: 1, minHeight: 480}} skeleton={<ReadingGridSkeleton width={width} columns={columns} gap={gap} gutter={theme.gutter}/>}/>;
  const discover = <ScrollView testID="reading-discover" contentContainerStyle={{paddingBottom: insets.bottom + 40}}>
    {discoveryState.phase === 'error' ? retry : null}
    {discoveryState.phase === 'loading' && !discoveryState.continueItems.length && !discoveryState.recent.length ? loading : null}
    {discoveryState.continueItems.length ? <DiscoveryRow first title={defaultI18n.t('next.cards.read')}>
      {discoveryState.continueItems.map(row => <ReadingMenuAnchor key={row.item.id}>{anchorRef => {const offered = readingOffer(row.item, 'discover'); return <ReadResumeCard testID={`reading-continue-${row.item.id}`} width={tablet ? 392 : 296} height={tablet ? 176 : 150}
        title={row.item.title} caption={authorPart(row.item)} detail={row.progress.location.section ? [{id: 'section', text: row.progress.location.section, role: 'context'}] : []}
        progress={Math.min(0.999, Math.max(0, row.progress.location.progression))} palette={tint(row.item.title)}
        artworkFallback={<View accessible={false} importantForAccessibility="no-hide-descendants">{cover(row.item, tablet ? 99 : 81, tablet ? 148 : 122)}</View>}
        onNavigate={() => openDiscover(row.item)} menuAnchorRef={anchorRef} selection={offered ? readingActions.selection(offered) : undefined}
        onMore={offered ? () => readingActions.actions.show({...offered, anchorRef}) : undefined}/>;}}</ReadingMenuAnchor>)}
    </DiscoveryRow> : null}
    {discoveryState.recent.length ? <DiscoveryRow title={defaultI18n.t('home.row.recentlyAdded')} first={!discoveryState.continueItems.length} onSeeAll={() => setTab('browse')}
      onNearEnd={() => {void discovery.more('recent').catch(runtime.reportError);}}>
      {discoveryState.recent.map(item => <DiscoveryCoverCard key={item.id} item={item} width={tablet ? 142 : 118} onOpen={openDiscover} offer={readingOffer(item, 'discover')} actions={readingActions}/>)}
    </DiscoveryRow> : null}
    {discoveryState.shortReads.length ? <DiscoveryRow title={defaultI18n.t('next.reader.shortReads')} sub={defaultI18n.t('next.reader.underThreeHours')}
      onNearEnd={() => {void discovery.more('short-reads').catch(runtime.reportError);}}>
      {discoveryState.shortReads.map(row => <DiscoveryCoverCard key={row.item.id} item={row.item} width={tablet ? 142 : 118} onOpen={openDiscover} offer={readingOffer(row.item, 'discover')} actions={readingActions}
        detail={defaultI18n.t('next.reader.aboutDuration', {duration: formatDuration(row.estimatedSeconds)})}/>)}
    </DiscoveryRow> : null}
  </ScrollView>;
  const booksGrid = <View style={{flex: 1}}>
    {booksState.phase === 'error' ? retry : null}
    <ReadingLoading ready={booksState.items.length > 0 || booksState.phase === 'ready' || booksState.phase === 'error'} style={{flex: 1}}
      skeleton={<ReadingGridSkeleton width={width} columns={columns} gap={gap} gutter={theme.gutter}/>}>
      <WindowedGrid testID="reading-books" collection={booksWindow} getKey={item => `${item.id}:${item.sourceId}`} itemWidth={width} itemHeight={width * 1.5 + 44}
        gap={gap} rowGap={gap} gutter={theme.gutter} paddingTop={theme.gutter} paddingBottom={insets.bottom + 40} style={{flex: 1}}
        header={booksState.phase === 'ready' && !booksState.items.length ? empty : undefined}
        renderPlaceholder={() => <ReadingGridSkeleton width={width} columns={1} gutter={0} rows={1}/>}
        renderItem={item => <DiscoveryCoverCard item={item} width={width} onOpen={openBooks} offer={readingOffer(item, 'books')} actions={readingActions}/>}/>
    </ReadingLoading>
  </View>;
  const pivots: Readonly<Record<string, string>> = {book: 'books', ebook: 'books', comic: 'titles', magazine: 'titles', newspaper: 'titles'};
  const counts = (summary?.opening === opening ? summary?.value?.counts ?? [] : []).filter(part => pivots[part.kind]).map(part =>
    defaultI18n.t(part.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: part.count, pivot: pivots[part.kind]!}));
  return <View testID={`native-reading-library-${libraryId}`} style={{flex: 1}} onLayout={event => {
    // The authored cover grid fills the pane it is laid out in, not the window:
    // the shell rail is already outside this view.
    const next = Math.round(event.nativeEvent.layout.width);
    if (next > 0 && next !== paneWidth) setPaneWidth(next);
  }}><StandardBackdrop/>
    {tablet ? <View style={{paddingTop: insets.top + 6, paddingHorizontal: theme.gutter}}>
      <Focusable accessibilityRole="button" focusKind="row" accessibilityLabel={defaultI18n.t('next.browse.native.libraries')} onPress={onBack}
        style={{height: 44, flexDirection: 'row', alignItems: 'center', gap: 6, alignSelf: 'flex-start'}}>
        <Icon name="i-left" size={20} color={theme.colors.textSecondary}/>
        <Text style={[browseType(theme, 15, 600), {color: theme.colors.textSecondary}]}>{defaultI18n.t('next.browse.native.libraries')}</Text>
      </Focusable>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
        <Text accessibilityRole="header" numberOfLines={1} style={[browseType(theme, 44, 800, -.045), {color: theme.colors.text, flexShrink: 1}]}>{name}</Text>
        {readingLibraries.length > 1 ? <Focusable accessibilityRole="button" focusKind="row" accessibilityLabel={defaultI18n.t('next.browse.native.switchLibraryNamed', {name})}
          onPress={() => setSwitcherOpen(true)} style={{width: 48, height: 48, borderRadius: 24, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raised}}>
          <Icon name="i-down" size={22} color={theme.colors.text}/>
        </Focusable> : null}
      </View>
      {gridCount && gridCount.libraryId === libraryId && gridCount.opening === opening ? <View style={{flexDirection: 'row', gap: 16, marginTop: 10, flexWrap: 'wrap'}}>
        <Text accessibilityLiveRegion="polite" style={[browseType(theme, 15, 500), {color: theme.colors.textTertiary}]}>{gridCount.text}</Text></View>
        : counts.length ? <View style={{flexDirection: 'row', gap: 16, marginTop: 10, flexWrap: 'wrap'}}>
          {counts.map(text => <Text key={text} style={[browseType(theme, 15, 500), {color: theme.colors.textTertiary}]}>{text}</Text>)}</View> : null}
      <View style={{marginTop: 16}}><LibraryTabs items={tabs} selected={tab} enabled phone={false} regionId={`reading-tabs-${libraryId}`}
        leftEdge="reading-tabs-edge" onSelect={id => setTab(id)}/></View>
    </View> : <LibraryCompactHeader switcher={switcher} onLibraries={onBack} testID="native-reading-library-header">
      <LibraryTabs items={tabs} selected={tab} enabled phone={theme.platform === 'phone'} regionId={`reading-tabs-${libraryId}`}
        leftEdge="reading-tabs-edge" onSelect={id => setTab(id)}/>
    </LibraryCompactHeader>}
    {(!gridTab || tab === 'discover') ? <ReadingSelectionToolbar port={readingActions} runtime={runtime}/> : null}
    {!opening ? <View style={{flex: 1}}>{loading}</View> : tab === 'discover' ? discover
      : !theme.isTV && gridTab && library ? <NativeReadingBooks context={scopedContext} opening={opening} runtime={runtime} library={library} tab={gridTab} labels={labels}
        owner={context.session()?.viewer.role === 'owner'} onCount={tablet ? projectGridCount : undefined}
        onDownload={offerDownload}/>
      : !theme.isTV && !capability ? <View style={{flex: 1}}>{loading}</View>
      : booksGrid}
    <ReadingItemMenu port={readingActions} runtime={runtime}/>
    <Menu open={!!downloadMenu && downloadMenu.opening === opening && downloadMenu.lifetime.isCurrent()}
      onOpenChange={open => {if (!open) downloads.dismiss(downloadMenu);}} title={downloadMenu?.item.title}
      items={[{id: 'download', label: defaultI18n.t('action.download'), onSelect: () => downloads.select(downloadMenu)}]}/>
    <Menu open={switcherOpen} onOpenChange={setSwitcherOpen} title={defaultI18n.t('next.browse.native.switchLibrary')} items={switcherItems}/>
  </View>;
}

/** The drawn Read row heading and its horizontal track (phone-read frames 1-3). */
function DiscoveryRow({title, sub, first = false, onSeeAll, onNearEnd, children}: {title: string; sub?: string; first?: boolean; onSeeAll?: () => void; onNearEnd?: () => void; children?: ReactNode}) {
  const theme = useTheme();
  return <View style={{marginTop: first ? 14 : 44}}>
    <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 14, paddingHorizontal: theme.gutter, marginBottom: 14}}>
      <Text accessibilityRole="header" style={[browseType(theme, 22, 700, -.015), {color: theme.colors.text}]}>{title}</Text>
      {sub ? <Text numberOfLines={1} style={[browseType(theme, theme.platform === 'phone' ? 13 : 14, 500), {color: theme.colors.textTertiary, flexShrink: 1}]}>{sub}</Text> : null}
      {onSeeAll ? <Focusable accessibilityRole="button" accessibilityLabel={title} focusKind="row" onPress={onSeeAll}
        style={{width: 44, height: 44, marginLeft: -8, alignItems: 'flex-start', justifyContent: 'center'}}>
        <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
      </Focusable> : null}
    </View>
    {/* Within about two screens of the end, the row asks for its next page (WEB-053). */}
    <ScrollView horizontal showsHorizontalScrollIndicator={false} scrollEventThrottle={200}
      onScroll={onNearEnd ? ({nativeEvent: {contentOffset, layoutMeasurement, contentSize}}) => {
        if (contentOffset.x + layoutMeasurement.width * 3 >= contentSize.width) onNearEnd();
      } : undefined}
      contentContainerStyle={{gap: theme.platform === 'phone' ? 16 : 14, paddingHorizontal: theme.gutter, paddingTop: 10, paddingBottom: 14}}>{children}</ScrollView>
  </View>;
}

/** One sparse-metadata cover: the drawn portrait art with the title and author
 * under it, using the catalogue's canonical generated cover. Books and comics
 * are drawn 2:3; a magazine or newspaper issue is drawn 3:4. */
function DiscoveryCoverCard({item, width, detail, onOpen, offer, actions}: {item: ReadingItem; width: number; detail?: string; offer?: NativeReadingActionOffer; actions: ReturnType<typeof useReadingActions>; onOpen: (item: ReadingItem) => void}) {
  const anchor = useRef<View>(null), selection = offer ? actions.selection(offer) : undefined;
  const theme = useTheme(), periodical = item.publicationType?.value === 'magazine' || item.publicationType?.value === 'newspaper';
  const height = periodical ? width * 4 / 3 : width * 3 / 2;
  return <Focusable testID={`reading-item-${item.id}`} accessibilityRole="button" accessibilityLabel={detail ? `${item.title}. ${detail}` : item.title} focusKind="row"
    ref={anchor} onPress={selection?.active ? selection.onToggle : () => onOpen(item)}
    onLongPressAction={offer ? () => actions.actions.show({...offer, anchorRef: anchor}) : undefined} style={{width}}>
    <View accessible={false} importantForAccessibility="no-hide-descendants" style={{width, height, borderRadius: 10, overflow: 'hidden', borderWidth: selection?.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>{cover(item, width, height)}
      {selection?.active ? <SelectionCircle selected={selection.selected} selecting interactive={false} accessibilityLabel={item.title}
        style={{position: 'absolute', right: 7, bottom: 7}}/> : null}
    </View>
    <Text numberOfLines={1} style={[browseType(theme, 12.5, 600), {color: theme.colors.text, marginTop: 7}]}>{item.title}</Text>
    {item.author ? <Text numberOfLines={1} style={[browseType(theme, 11.5, 500), {color: theme.colors.textTertiary, marginTop: 2}]}>{item.author.value}</Text> : null}
    {detail ? <Text numberOfLines={1} style={[browseType(theme, 11.5, 500), {color: theme.colors.textTertiary, marginTop: 2}]}>{detail}</Text> : null}
  </Focusable>;
}
