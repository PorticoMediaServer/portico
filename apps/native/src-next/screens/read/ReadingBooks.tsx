import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {ReadingError} from '../../../../../packages/client-core/src';
import type {BrowseNode, BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {Library} from '../../../../../packages/client-core/src';
import type {LibraryTab} from '../../../../../packages/client-core/src/presentation/library-tabs';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {BrowseLabels} from '../../kit/browse';
import {BrowseToolbar, FilteredEmpty} from '../../kit/browse';
import {Button, Chip, Focusable} from '../../kit/controls';
import {MediaArt} from '../../kit/artwork';
import {PosterCard} from '../../kit/cards';
import {Parts} from '../../kit/cards/primitives';
import {cardAccessibilityLabel} from '../../kit/cards/model';
import {AlphabetRail, WindowedGrid, type WindowedGridHandle} from '../../kit/layout';
import {StateView} from '../../kit/states';
import {ReadingLoading, ReadingGridSkeleton} from './ReadingLoading';
import {ReadingItemMenu, ReadingSelectionToolbar, ReadingMenuAnchor, useReadingActions} from './ReadingActions';
import {SelectionCircle} from '../../kit/selection';
import type {CardSelection} from '../../kit/cards/model';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {homeCardText} from '../home/model';
import {libraryAppliedFilters} from '../library/applied-filters';
import {NativeLibraryDestination, nativeLibraryEntryKey} from '../library/model';
import {LibraryFilters} from '../library/LibraryFilters';
import type {NativeLibraryFilterDraft} from '../library/filters';
import type {NativeReadOrigin} from './eligibility';
import {nativeReadingDownloadOffer, nativeReadingRowLifetime, type NativeReadingPageOpening, type NativeReadingDownloadOffer} from './reading-page-opening';
import {PhoneReadingBookCard, PhoneReadingBookCover} from './PhoneReadingBookCard';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import {openNativeReadingBookEntry} from './reading-books-open';

const noopSubscribe = () => () => {};
/** One committed layout of the grid: the route it was drawn under, and the
 * lifetime every action from that layout may use. */
type BooksLease = Readonly<{origin: NativeReadOrigin; family: string; life: AbortController}>;

/** tablet-read#1b: the Books pivot as the authored browsable grid — the shared
 * Browse toolbar over real capabilities and quick filters — with the real
 * Filters panel docked beside it in landscape. A card opens the item's own
 * Reading book page; this surface never opens the reader itself. */
export function NativeReadingBooks({context, runtime, library, tab, owner, labels, filterWidth = 360, onCount, opening, onDownload}: {
  context: NativeShellBootstrapContext; runtime: NativeShellRuntime; library: Library; /** The browsable tab the server's own reading pivots published. */ tab: LibraryTab;
  owner: boolean; labels: BrowseLabels; filterWidth?: number; opening: NativeReadingPageOpening;
  onDownload?: (offer: NativeReadingDownloadOffer) => void;
  /** tablet-read#1b: the wide header shows the count of this grid's view, tagged
   * with the owner of the mount that reported it. */
  onCount?: (owner: object, text: string | undefined) => void;
}) {
  const theme = useTheme(), phone = theme.platform === 'phone', dimensions = useWindowDimensions(), lifetime = useMemo(() => nativeDestinationLifetime(context), [context]);
  const current = useCallback(() => opening.isCurrent() && lifetime.current() && context.libraries().some(value => value.id === library.id && value.kind === library.kind),
    [opening, lifetime, context, library.id, library.kind]);
  const destination = useMemo(() => new NativeLibraryDestination({api: context.api, scope: lifetime.scope, library, current, tab}), [context, lifetime, library, tab, current]);
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const readingActions = useReadingActions(context, runtime, destination);
  const [filters, setFilters] = useState<NativeLibraryFilterDraft>();
  const [paneWidth, setPaneWidth] = useState<number>();
  const [view, setView] = useState<'grid' | 'list'>('grid');
  const [visible, setVisible] = useState({first: 0, last: 0});
  const grid = useRef<WindowedGridHandle>(null);
  const [lease, setLease] = useState<BooksLease>();
  const held = useRef<BooksLease | undefined>(undefined), command = useRef<object | undefined>(undefined);
  const navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const {route: renderedRoute, revision: renderedRevision} = navigation;
  // Install only the route/revision committed by this render. Navigation never
  // hands a retained handler a successor lease through a subscription callback.
  useLayoutEffect(() => {
    const selected = context.session();
    const admitted = opening.isCurrent() && renderedRoute === opening.origin.route && renderedRevision === opening.origin.revision;
    const next = admitted && selected ? Object.freeze({origin: opening.origin, family: selected.sessionFamilyId, life: new AbortController()}) : undefined;
    held.current = next; setLease(next);
    return () => {next?.life.abort(); if (held.current === next) held.current = undefined;};
  }, [context, library.id, opening, tab, renderedRoute, renderedRevision]);
  useEffect(() => {void destination.load(); return () => destination.dispose();}, [destination]);
  useEffect(() => () => filters?.dispose(), [filters]);
  const collection = state.collection, capabilities = state.capabilities, sort = state.sort, pivot = state.tab.pivot;
  const pageStore = useMemo(() => ({subscribe: (notify: () => void) => collection ? collection.subscribe(notify) : noopSubscribe(),
    getSnapshot: () => collection?.getSnapshot()}), [collection]);
  const page = useSyncExternalStore(pageStore.subscribe, pageStore.getSnapshot);
  const model = useMemo(() => capabilities && sort ? browseToolbarModel(capabilities, library.kind, state.predicates, sort,
    {...runtime.text, owner, textEntry: true}) : undefined, [capabilities, library.kind, owner, runtime.text, sort, state.predicates]);
  const applied = capabilities && model ? libraryAppliedFilters(model.applied, capabilities, labels) : [];
  const base = (state.tab.fixedFilter ?? []) as readonly BrowseNode[];
  const filtered = JSON.stringify(state.predicates) !== JSON.stringify(base);
  const total = page?.total, unfiltered = state.unfilteredTotal;
  const resultCount = total === undefined ? '' : filtered && unfiltered
    ? defaultI18n.t('title.shownOfTotal', {shown: `${defaultI18n.number(total)}${page?.totalIsLowerBound ? defaultI18n.t('next.browse.native.countPlus') : ''}`,
        total: defaultI18n.t(unfiltered.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: unfiltered.total, pivot: pivot ?? 'other'})})
    : defaultI18n.t(page?.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: total, pivot: pivot ?? 'other'});
  // tablet-read#1b puts a filtered view's count in the library header, not the
  // toolbar; an unfiltered grid keeps the library's own pivot counts there.
  // Report it under a per-mount owner so only this grid retires its own line.
  const countOwner = useMemo(() => ({}), []);
  useEffect(() => {onCount?.(countOwner, filtered ? resultCount || undefined : undefined);}, [countOwner, filtered, onCount, resultCount]);
  useEffect(() => () => {onCount?.(countOwner, undefined);}, [countOwner, onCount]);
  const applyQuery = (predicates: readonly BrowseNode[]) => {
    if (!filters) {destination.setQuery(predicates); return;}
    filters.setPredicates(predicates); // Debounced count/facet refresh of the same panel.
    filters.applyLive(); // The mounted grid takes the query; the panel stays open.
  };
  const closeFilters = () => {filters?.dispose(); setFilters(undefined);};
  const openFilters = () => {
    if (filters) {closeFilters(); return;}
    if (!current() || !capabilities || !sort || !state.collection) return;
    setFilters(destination.createFilters({live: true}));
  };
  const applySort = (next: BrowseSortSelection) => {
    destination.setQuery(state.predicates, next, filters);
    if (filters) {filters.dispose(); setFilters(destination.createFilters({live: true}));}
  };
  const quick = (id: string, checked: boolean) => {
    const preset = capabilities?.quickFilters.find(value => value.id === id);
    if (!preset) return;
    const same = (node: BrowseNode) => JSON.stringify(node) === JSON.stringify(preset.query);
    applyQuery([...state.predicates.filter(node => !same(node)), ...(checked ? [preset.query] : [])]);
  };
  // tablet-read#1: a card opens the item's own Reading book page, under the
  // exact lease, collection and leaf it was drawn with.
  const rowLifetime = (leased: BooksLease, entry: ContentEntry) => nativeReadingRowLifetime(opening, entry, state, destination.getSnapshot, {
    signal: leased.life.signal,
    isCurrent: () => held.current === leased && !leased.life.signal.aborted && current() && context.session()?.sessionFamilyId === leased.family,
  });
  const openBook = (leased: BooksLease, entry: ContentEntry) => {
    if (command.current) return;
    const ticket = {}; command.current = ticket;
    const offered = rowLifetime(leased, entry);
    if (!offered.isCurrent()) {if (command.current === ticket) command.current = undefined; return;}
    void openNativeReadingBookEntry(context, {libraryId: library.id, entry, origin: leased.origin, resident: offered.isCurrent, signal: offered.signal})
      .catch(error => {
        const cancelled = error instanceof ReadingError && error.code === 'cancelled' && error.status === undefined;
        if (offered.isCurrent() && command.current === ticket && !cancelled) runtime.reportError(error);
      })
      .finally(() => {if (command.current === ticket) command.current = undefined;});
  };
  const downloadBook = (leased: BooksLease, entry: ContentEntry) => {
    if (command.current) return;
    const ticket = {}; command.current = ticket;
    try {
      if (!onDownload || (entry.kind !== 'ebook' && entry.kind !== 'comic') || (entry.libraryId !== undefined && entry.libraryId !== library.id)) return;
      const offered = rowLifetime(leased, entry);
      const offer = nativeReadingDownloadOffer(opening, entry, offered.isCurrent, offered);
      if (offer) onDownload(offer);
    } finally {if (command.current === ticket) command.current = undefined;}
  };
  // The grid fills the actual pane: the shell rail and the docked Filters pane
  // are already outside it, so the window width would overshoot every row. The
  // drawn A-Z rail keeps its own 52 pt clear while it is shown.
  const docked = !phone && dimensions.width > dimensions.height, width = paneWidth ?? dimensions.width;
  const pane = filters && docked ? filterWidth : 0;
  // tablet-read#1b draws the A-Z rail beside the Books grid, beside the docked
  // Filters too; it is shown while the server publishes letters for this sort.
  const alphabet = !!page?.positionIndex && page.total !== undefined && sort?.field === 'title' && sort.direction === 'asc';
  // The drawn grid: seven across with 20/30 gaps; the docked pane's own four-across
  // drawing uses 14/22 and slightly narrower cards. Columns follow the real room and
  // never force a card wider than it, so a small landscape pane cannot overflow.
  // tablet-read#1b draws the docked GRID with 28 left and a 52 right band
  // (padding 20 52 0 28) against the page gutter of 64 at desktop width; the frame
  // proves nothing about the list's gutters, so only the grid takes them. The tight
  // gap/minimum rule is unchanged, and the tight card keeps its fractional width
  // (492 room -> 112.5) instead of flooring to 112 or rounding up and overflowing.
  const tight = pane > 0, tightGrid = tight && view === 'grid';
  // The phone preserves its existing gutter, 16 gap and bounded three-up
  // tracks. Width follows the actual pane; this is not a 108 pt fidelity claim.
  const gap = phone ? 16 : tight ? 14 : 20, rowGap = tight ? 22 : 30, minimum = phone ? 96 : tight ? 112 : 120;
  const left = tightGrid ? 28 : theme.gutter;
  const right = tightGrid ? 52 : alphabet ? 52 : theme.gutter;
  const room = Math.max(0, width - left - right - pane);
  const columns = Math.max(1, Math.min(phone ? 3 : 7, Math.floor((room + gap) / (minimum + gap))));
  const cardWidth = Math.max(1, tightGrid ? (room - gap * (columns - 1)) / columns : Math.floor((room - gap * (columns - 1)) / columns));
  const cardHeight = cardWidth * 1.5 + 48;
  // The drawn grid/list pair: the same bounded collection, the same query and
  // the same open action; only the row height and cover change.
  const list = view === 'list', rowWidth = Math.max(1, room);
  // Stable identity: the grid re-runs its range callback when this prop changes,
  // and an unchanged range must not publish a new object.
  const changedRange = useCallback((first: number, last: number) => {
    setVisible(current => current.first === first && current.last === last ? current : {first, last});
    destination.remember(first, 0);
  }, [destination]);
  return <View style={{flex: 1}} onLayout={event => {
    const next = Math.round(event.nativeEvent.layout.width);
    if (next > 0 && next !== paneWidth) setPaneWidth(next);
  }}>
    <ReadingSelectionToolbar port={readingActions} runtime={runtime}/>
    {model && !readingActions.state.selecting ? <BrowseToolbar testID="reading-books-toolbar" model={model} labels={labels} resultCount={phone ? resultCount : ''} filtersOpen={!!filters}
      view={{value: view, onValueChange: setView}}
      onFilters={openFilters} onQuickChange={quick} onSortChange={applySort}
      onReshuffle={() => {if (sort) applySort({...sort, seed: (sort.seed ?? 0) + 1});}}/> : null}
    {applied.length && !readingActions.state.selecting ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, flexWrap: 'wrap', paddingHorizontal: theme.gutter, paddingTop: 10}}>
      {applied.map(row => <Chip key={row.id} variant={phone ? 'applied' : undefined} trailingIcon={phone ? 'i-close' : undefined} testID={`reading-books-applied-${row.id}`} onPress={() => applyQuery(state.predicates.filter(value => JSON.stringify(value) !== row.id))}
        accessibilityLabel={defaultI18n.t('next.browse.native.removeFilter', {field: row.fieldName})}>{row.label}</Chip>)}
      <Button variant="text" size="small" onPress={() => applyQuery(base)}>{defaultI18n.t('next.browse.native.clearAll')}</Button>
    </View> : null}
    {state.phase === 'error' ? <StateView {...runtime.error(state.error, () => {void destination.load(state.tab.id).catch(runtime.reportError);})}/>
      : collection ? <View style={{flex: 1, flexDirection: 'row'}}>
        {total === 0 ? (filtered
          ? <View style={{flex: 1}}><FilteredEmpty testID="reading-books-filtered-empty" onClearFilters={() => applyQuery(base)}/></View>
          : <StateView testID="reading-books-empty" variant="inline" title={defaultI18n.t('library.emptyLibrary')} body={defaultI18n.t('library.emptyLibraryBody')} style={{flex: 1}}/>)
          : <View style={{flex: 1}}>
            <WindowedGrid ref={grid} testID={list ? 'reading-books-list' : 'reading-books-grid'} collection={collection} getKey={nativeLibraryEntryKey}
              itemWidth={list ? rowWidth : cardWidth} itemHeight={list ? 70 : cardHeight} captionHeight={list ? 70 : 48}
              gap={list ? 0 : gap} rowGap={list ? 0 : rowGap} gutter={left} rightGutter={right} style={{flex: 1}} initialIndex={state.memory.firstVisibleIndex}
              onVisibleRange={changedRange}
              renderPlaceholder={() => list ? <View style={{width: rowWidth, height: 70, backgroundColor: theme.colors.raised}}/>
                : <View style={{width: cardWidth, height: cardWidth * 1.5, borderRadius: 10, backgroundColor: theme.colors.raised}}/>}
              renderItem={(entry: ContentEntry) => {
                const open = () => {if (lease) openBook(lease, entry);};
                const download = onDownload && nativePublicationReadsAvailable() ? () => {if (lease) downloadBook(lease, entry);} : undefined;
                const offered = lease ? {item: entry, lifetime: rowLifetime(lease, entry), open, download} : undefined;
                const selection = offered ? readingActions.selection(offered) : undefined;
                const menu = offered ? (anchorRef: React.RefObject<View | null>) => readingActions.actions.show({...offered, anchorRef}) : undefined;
                if (list) return <ReadingListRow entry={entry} width={rowWidth} runtime={runtime} onOpen={open} onMenu={menu} selection={selection} phone={phone}/>;
                if (phone) return <PhoneReadingBookCard testID={`reading-book-${entry.id}`} title={entry.displayTitle ?? entry.title} author={entry.author}
                  width={cardWidth} artwork={runtime.artwork(entry.posterUrl)} periodical={entry.publicationType === 'magazine' || entry.publicationType === 'newspaper'}
                  onOpen={open} onMenu={menu} selection={selection}/>;
                const text = homeCardText(entry, false, runtime.text);
                return <ReadingMenuAnchor>{anchorRef => <PosterCard title={text.title} caption={text.caption} width={cardWidth} artwork={runtime.artwork(entry.posterUrl)} visible
                  testID={`reading-book-${entry.id}`} onNavigate={open} selection={selection} menuAnchorRef={anchorRef}
                  onMore={offered ? () => readingActions.actions.show({...offered, anchorRef}) : undefined}/>}</ReadingMenuAnchor>;
              }}/>
            {alphabet && page?.positionIndex && page.total !== undefined ? <AlphabetRail positionIndex={page.positionIndex}
              total={page.total} firstVisible={visible.first} lastVisible={visible.last} testID="reading-books-alphabet"
              onJump={index => grid.current?.scrollToIndex(index)} height={480} style={{position: 'absolute', right: 2, top: 16}}/> : null}
          </View>}
        {filters && docked ? <View style={{width: filterWidth, borderLeftWidth: 1, borderLeftColor: theme.colors.line, backgroundColor: theme.colors.ink}}>
          <LibraryFilters docked draft={filters} source={state} runtime={runtime} owner={owner} labels={labels} onClose={closeFilters}/>
        </View> : null}
      </View>
      : <ReadingLoading ready={false} style={{flex: 1}} skeleton={<ReadingGridSkeleton width={cardWidth} columns={columns} gap={gap} gutter={left}/>}/>}
    <ReadingItemMenu port={readingActions} runtime={runtime}/>
    {filters && !docked ? <LibraryFilters draft={filters} source={state} runtime={runtime} owner={owner} labels={labels} onClose={closeFilters}/> : null}
  </View>;
}

/** One Books list row: the item's own cover and the same real title and caption
 * the card draws. A format, page count or reading position appears only when the
 * browse page itself carries it, so nothing here is inferred from the file. */
function ReadingListRow({entry, width, runtime, onOpen, onMenu, selection, phone}: {entry: ContentEntry; width: number; runtime: NativeShellRuntime; onOpen: () => void; onMenu?: (anchor: React.RefObject<View | null>) => void; selection?: CardSelection; phone?: boolean}) {
  const anchor = useRef<View>(null);
  const theme = useTheme(), text = homeCardText(entry, false, runtime.text), title = entry.displayTitle || text.title;
  return <Focusable accessibilityRole="button" accessibilityLabel={cardAccessibilityLabel(title, text.caption, text.detail)} focusKind="row" ref={anchor} onPress={selection?.active ? selection.onToggle : onOpen} onLongPressAction={onMenu ? () => onMenu(anchor) : undefined} testID={`reading-list-${entry.id}`}
    style={{width, height: 70 * (theme.fontScale ?? 1), flexDirection: 'row', alignItems: 'center', gap: 14, paddingHorizontal: 12}}>
    {selection?.active ? <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row" accessibilityLabel={title}/> : null}
    {phone ? <View style={{borderRadius: 5, overflow: 'hidden'}}><PhoneReadingBookCover title={title} author={entry.author} artwork={runtime.artwork(entry.posterUrl)} width={36} height={54}/></View>
      : <MediaArt kind="poster" source={runtime.artwork(entry.posterUrl)} width={36} height={54} style={{borderRadius: 5}}/>}
    <View style={{flex: 1, minWidth: 0, gap: phone ? 2 : 3}}>
      <Text numberOfLines={1} style={[browseType(theme, 15, 600), {color: theme.colors.text}]}>{title}</Text>
      <Parts parts={text.caption} style={phone ? [browseType(theme, 12.5, 500), {color: theme.colors.textTertiary}] : [browseType(theme, 12.5, 400), {color: theme.colors.textSecondary}]}/>
    </View>
    <View pointerEvents="none" style={{position: 'absolute', left: 12, right: 12, bottom: 0, height: 1, backgroundColor: theme.colors.line}}/>
  </Focusable>;
}
