import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {ScrollView, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {titleBucketValue} from '../../../../../../packages/client-core/src/browse-buckets';
import {savedListFilters, type HistoryPeriod, type SavedListFilter, type SavedListSort, type SavedRoute} from '../../../../../../packages/client-core/src/saved';
import {focusTestID} from '../../../kit/focus';
import type {NativeSavedSelectionPort} from './useSavedPage';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import {nativeDestinationFocusIds} from '../../../screens/destinations/focus';
import type {NativeEntryActions} from '../../../screens/destinations/entry-actions';
import {useSavedTVTabs} from '../../../screens/saved/useSavedTVTabs';
import {useTVPageBack} from '../../../screens/destinations/useTVPageBack';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import {sameAuthority, type ShellAuthority} from '../../../shell/model';
import type {SavedTVDirectoryHandle} from './useTVSavedDirectory';
import type {NativeSavedViewDestination} from '../../../screens/saved/view-model';
import type {NativeTVSavedController, NativeTVSavedSnapshot, NativeTVSavedTab} from '../../../screens/destinations/saved/model';

export const t = defaultI18n.t;
export const itemID = (item: {id: string}) => item.id;
export const historyFocusable = (row: {media?: ContentEntry}) => !!row.media;
export const factLine = (text: string) => Object.freeze([{id: 'caption', text, role: 'value' as const}]);
export type Tab = NativeTVSavedTab;
export type Actions = Pick<NativeEntryActions, 'menu' | 'isMenuOpen' | 'play'>;
export const sorts: readonly Readonly<{id: Exclude<SavedListSort, 'updated'>; label: Parameters<typeof t>[0]}>[] = [
  {id: 'added', label: 'web.saved.sort.added'}, {id: 'title', label: 'web.saved.sort.title'},
  {id: 'year', label: 'web.saved.sort.year'}, {id: 'duration', label: 'web.saved.sort.duration'},
  {id: 'progress', label: 'web.saved.sort.progress'},
];
export const filters: readonly Readonly<{id: SavedListFilter; label: Parameters<typeof t>[0]}>[] = [
  {id: 'all', label: 'search.chip.all'}, {id: 'unwatched', label: 'filter.quick.unwatched'}, {id: 'inProgress', label: 'filter.quick.inProgress'},
];
export const periods: readonly HistoryPeriod[] = ['24h', '7d', '30d', '90d', 'all'];
export const periodLabel: Record<HistoryPeriod, Parameters<typeof t>[0]> = {['24h']: 'saved.today', '7d': 'web.saved.thisWeek', '30d': 'web.saved.days', '90d': 'web.saved.days', all: 'web.saved.allTime'};

export const emptyFor: Record<Tab, Readonly<{title: Parameters<typeof t>[0]; body: Parameters<typeof t>[0]}>> = {
  watchlist: {title: 'web.saved.empty.watchlist', body: 'web.saved.empty.watchlistBody'},
  favorites: {title: 'web.saved.empty.favorites', body: 'web.saved.empty.favoritesBody'},
  playlists: {title: 'web.saved.empty.playlists', body: 'web.saved.empty.playlistsBody'},
  collections: {title: 'web.saved.empty.collections', body: 'web.saved.empty.collectionsBody'},
  views: {title: 'web.saved.empty.views', body: 'web.saved.empty.viewsBody'},
  history: {title: 'web.saved.empty.history', body: 'web.saved.empty.historyBody'},
};

export function dayLabel(value: string | undefined, locale: string | undefined): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return undefined;
  const now = new Date(), at = new Date(date.getFullYear(), date.getMonth(), date.getDate()), today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const days = Math.round((today.getTime() - at.getTime()) / 86400000);
  if (days === 0) return t('saved.today');
  if (days === 1) return t('relative.yesterday');
  return new Intl.DateTimeFormat(locale ?? 'en-US', {weekday: 'long'}).format(date);
}

export function clock(value: string | undefined, locale: string | undefined): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return undefined;
  return new Intl.DateTimeFormat(locale ?? 'en-US', {hour: 'numeric', minute: '2-digit'}).format(date);
}

export function historyLine(entry: ContentEntry): string {
  if (entry.kind === 'episode') return [entry.seasonNumber !== undefined && entry.episodeNumber !== undefined ? t('title.episodeCode', {season: entry.seasonNumber, episode: entry.episodeNumber}) : undefined, entry.title].filter(Boolean).join(' · ');
  if (['song', 'album', 'artist'].includes(entry.kind)) return [entry.artist?.name, entry.album?.name].filter(Boolean).join(' · ') || entry.subtitle || '';
  if (entry.kind === 'book' || entry.kind === 'audiobook_file') return entry.author ?? '';
  // Saved mixes kinds, so a card says its kind (tv-more 1, 2): "2024 · Movie", "Show · 1 season".
  if (entry.kind === 'movie') return [entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false}), t('search.kind.movie')].filter(Boolean).join(' · ');
  if (entry.kind === 'show') return [t('search.kind.show'), entry.seasonCount ? t('title.seasonCount', {count: entry.seasonCount}) : undefined].filter(Boolean).join(' · ');
  return entry.subtitle ?? '';
}

export type TVSavedPageProps = {
  controller: NativeTVSavedController; runtime: NativeShellRuntime; actions: Actions; activation: NativeDestinationActivationPort;
  authority: Parameters<typeof nativeDestinationFocusIds>[0]; initialTab: Tab; selection?: NativeSavedSelectionPort;
  openSaved: (route: SavedRoute) => void; openEntry: (entry: ContentEntry) => void; browseHome: () => void;
};

export function useTVSavedPage({controller, runtime, activation, authority, initialTab}: TVSavedPageProps) {
  const theme = useTheme(), live = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  // H14: a new sort, filter or period (or a background reload) never blanks the loaded grid. The
  // tab's last ready content stays painted until its replacement is ready; only a tab with
  // nothing loaded yet shows the loading outline.
  const lastReady = useRef<NativeTVSavedSnapshot | undefined>(undefined);
  if (live.phase === 'ready') lastReady.current = live;
  const snapshot = live.phase === 'loading' && lastReady.current?.tab === live.tab ? lastReady.current : live;
  const [tab, setTab] = useState<Tab>(initialTab), [sort, setSort] = useState<Exclude<SavedListSort, 'updated'>>('added');
  const [filter, setFilter] = useState<SavedListFilter>('all'), [period, setPeriod] = useState<HistoryPeriod>('7d');
  const [sortOpen, setSortOpen] = useState(false), [filterOpen, setFilterOpen] = useState(false);
  // The confirm belongs to the exact opening it was drawn for. The ref is the synchronous
  // truth (React state is only what this render draws), so a retained close or confirm can
  // never act on a successor, and one flight is held by a ref, not by a rendered flag.
  type ClearScope = Readonly<{scope: string; controller: NativeTVSavedController; authority: ShellAuthority; runtime: NativeShellRuntime; activation: NativeDestinationActivationPort}>;
  type ClearOpening = ClearScope & Readonly<{period: HistoryPeriod; routeRevision: number; historyRevision: number; abort: AbortController}>;
  const clear = useRef<ClearOpening | undefined>(undefined), clearAlive = useRef(true);
  // The dialog may close while its command is still running: the pending operation stays tracked
  // here so it can be aborted on a real scope change and so its completion owns only itself.
  const clearFlight = useRef<ClearOpening | undefined>(undefined);
  const clearScopeLive = useRef<ClearScope | undefined>(undefined);
  const [clearOpening, setClearOpening] = useState<ClearOpening>();
  const sortAnchor = useRef<View>(null), filterAnchor = useRef<View>(null);
  // tv-more1: Jump to is drawn only under a title sort, over the titles this page has actually
  // loaded; nothing claims an index for a page it has not read. Every measurement is kept
  // relative to its own parent, so a parent's layout callback arriving later cannot bake a stale
  // offset into a card, and a sheet is retired with the scope that drew it.
  const page = useRef<ScrollView>(null), contentRoot = useRef<View>(null), [scrollY, setScrollY] = useState(0), [viewportHeight, setViewportHeight] = useState(1080), [contentHeight, setContentHeight] = useState(0);
  const directoryPage = useRef<SavedTVDirectoryHandle | null>(null);
  const measured = useRef({scope: '', region: 0, body: 0, section: 0, grids: new Map<'posters' | 'squares', number>(),
    cards: new Map<string, Readonly<{section: 'posters' | 'squares'; y: number}>>()});
  // The confirm and its flight die with the screen; a late completion cannot touch a successor.
  useLayoutEffect(() => {clearAlive.current = true; return () => {
    clearAlive.current = false;
    const sheet = clear.current; if (sheet) {sheet.abort.abort(); clear.current = undefined;}
    const flight = clearFlight.current; if (flight) {flight.abort.abort(); clearFlight.current = undefined;}
  };}, []);
  const ids = useMemo(() => nativeDestinationFocusIds(authority), [authority]);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const run = useCallback((work: Promise<unknown>) => {void work.catch(runtime.reportError);}, [runtime]);
  const started = useRef(false);
  useEffect(() => {if (started.current) return; started.current = true; run(controller.select(tab, {sort, filter, period})); run(controller.summary());}, [controller, filter, period, run, sort, tab]);
  // Coming back from a page opened from here (a playlist that was deleted or renamed there) reads the tab again.
  const shown = useRef(active.visible);
  useEffect(() => {
    const returned = active.visible && !shown.current && started.current; shown.current = active.visible;
    if (returned) {run(controller.reload()); run(controller.summary());}
  }, [active.visible, controller, run]);
  const choose = useCallback((next: Tab) => {if (next === tab) return; setTab(next); run(controller.select(next, {sort, filter, period}));}, [controller, filter, period, run, sort, tab]);
  const tabsState = useMemo(() => ({phase: snapshot.phase, route: {view: tab} as const, error: snapshot.error}), [snapshot.phase, snapshot.error, tab]);
  const tabsDestination = useMemo(() => ({
    subscribe: controller.subscribe, getSnapshot: () => tabsState,
    select: (route: {view?: string}) => {const next = route.view as Tab; if (next && next !== tab) choose(next); return Promise.resolve();},
  }) as unknown as NativeSavedViewDestination, [choose, controller, tabsState, tab]);
  const count = tab === 'watchlist' || tab === 'favorites'
    ? (snapshot.counts?.posters ?? 0) + (snapshot.counts?.squares ?? 0)
    : tab === 'playlists' ? snapshot.playlists?.sections.reduce((sum, section) => sum + section.totalCount, 0) ?? 0
      : tab === 'history' ? snapshot.entries.length : snapshot.resources.length;
  const posters = useMemo(() => (tab === 'watchlist' || tab === 'favorites' ? snapshot.posters?.sections.flatMap(section => section.entries) ?? [] : [])
    .filter((entry): entry is ContentEntry => entry.kind !== 'playlist' && entry.kind !== 'playlist_entry' && !['album', 'song', 'book', 'audiobook_file', 'artist'].includes(entry.kind)), [tab, snapshot.posters]);
  const squares = useMemo(() => (tab === 'watchlist' || tab === 'favorites' ? snapshot.squares?.sections.flatMap(section => section.entries) ?? [] : [])
    .filter((entry): entry is ContentEntry => entry.kind !== 'playlist' && entry.kind !== 'playlist_entry'), [tab, snapshot.squares]);
  const posterCount = snapshot.counts?.posters ?? posters.length, squareCount = snapshot.counts?.squares ?? squares.length;
  const list = useMemo(() => [...posters.map(entry => ({entry, section: 'posters' as const})), ...squares.map(entry => ({entry, section: 'squares' as const}))], [posters, squares]);
  // The key is the server's own bucket for this title, kept whole (#, a non-Latin letter, a
  // multi-character bucket); only a title the server gives no bucket keeps its first character.
  const letterOf = (entry: ContentEntry): string | undefined => {
    const sortLetter = typeof entry.sortLetter === 'string' ? entry.sortLetter.trim() : '';
    if (sortLetter) return sortLetter;
    const value = titleBucketValue(entry), chars = typeof value === 'string' ? [...value.trim()] : [];
    return chars[0];
  };
  const jumpLetters = useMemo(() => {
    const seen = new Map<string, Readonly<{key: string; index: number; id: string}>>();
    list.forEach(({entry}, index) => {const key = letterOf(entry); if (key && !seen.has(key)) seen.set(key, {key, index, id: entry.id});});
    return [...seen.values()].sort((left, right) => left.index - right.index);
  }, [list]);
  const showJump = (tab === 'watchlist' || tab === 'favorites') && sort === 'title' && jumpLetters.length > 1;
  // One drawn query owns the measurements and the sheet. A successor scope resets them here, before
  // any of its cards lay out, and retires an opening before any of its callbacks can run; a page
  // that merely appends keeps every retained card's measurement.
  const scope = `${tab}:${sort}:${filter}`;
  // A reused component can be handed a different controller or authority without the query
  // changing, so that replacement retires the measurements and the sheet too.
  const owner = useRef<Readonly<{controller: unknown; authority: ShellAuthority}> | undefined>(undefined);
  const replaced = owner.current === undefined || owner.current.controller !== controller || !sameAuthority(owner.current.authority, authority);
  if (replaced) owner.current = {controller, authority};
  if (replaced || measured.current.scope !== scope) measured.current = {scope, region: 0, body: 0, section: 0, grids: new Map(), cards: new Map()};
  // The confirm retires with the query, the period, the destination revision, the authority or
  // a replaced controller, before any of its callbacks can run. Leaving Saved is a navigation
  // change: the activation revision is the route proof here, because the destination lifetime
  // only covers the session's authority.
  const clearScope = `${scope}:${period}`;
  clearScopeLive.current = {scope: clearScope, controller, authority, runtime, activation};
  const clearCurrent = (target: ClearOpening) => {
    const live = clearScopeLive.current, route = target.activation.getSnapshot();
    return clearAlive.current && !target.abort.signal.aborted && target.controller.historyRevision() !== undefined && live?.scope === target.scope
      && live.controller === target.controller && live.runtime === target.runtime && live.activation === target.activation
      && sameAuthority(live.authority, target.authority) && sameAuthority(target.runtime.getSnapshot().authority, target.authority)
      && route.visible && route.revision === target.routeRevision;
  };
  const retireClear = () => {
    const sheet = clear.current; if (sheet) {sheet.abort.abort(); clear.current = undefined;}
    const flight = clearFlight.current; if (flight) {flight.abort.abort(); clearFlight.current = undefined;}
  };
  const clearOwner = clear.current ?? clearFlight.current;
  if (clearOwner && !clearCurrent(clearOwner)) retireClear();
  const clearing = clearFlight.current !== undefined;
  const clearView = clearOpening && clear.current === clearOpening && clearOpening.scope === clearScope && active.visible
    && clearOpening.routeRevision === active.revision && sameAuthority(clearOpening.authority, authority) ? clearOpening : undefined;
  const openClear = () => {
    const live = clearScopeLive.current, route = activation.getSnapshot();
    if (clearFlight.current || !clearAlive.current || live?.scope !== clearScope || live.controller !== controller
      || live.runtime !== runtime || live.activation !== activation || !sameAuthority(live.authority, authority)
      || !route.visible || route.revision !== active.revision) return;
    const historyRevision = controller.historyRevision();
    // Nothing to clear, or already replaced: no confirmation is drawn at all.
    if (historyRevision === undefined || !active.visible) return;
    const next: ClearOpening = {scope: clearScope, period, controller, authority, runtime, activation, routeRevision: route.revision, historyRevision, abort: new AbortController()};
    clear.current?.abort.abort();
    clear.current = next; setClearOpening(next);
  };
  const closeClear = (target: ClearOpening) => {if (clear.current !== target) return; target.abort.abort(); clear.current = undefined; setClearOpening(current => current === target ? undefined : current);};
  const confirmClear = (target: ClearOpening) => {
    if (clear.current !== target || clearFlight.current || !clearCurrent(target)) return;
    // Closing the dialog must not lose the command: the pending operation stays tracked so a real
    // scope change or unmount can still abort it, and its completion owns only itself.
    clear.current = undefined; clearFlight.current = target;
    setClearOpening(current => current === target ? undefined : current);
    const own = () => clearFlight.current === target && clearCurrent(target);
    void target.controller.clearHistory({revision: target.historyRevision, period: target.period, signal: target.abort.signal, isCurrent: own})
      .catch(error => {if (own()) runtime.reportError(error);})
      .finally(() => {
        // Only the operation this screen still tracks may release its own pending state.
        if (clearFlight.current !== target) return;
        clearFlight.current = undefined;
        target.abort.abort();
      });
  };
  /** The exact object this render draws into; a late callback from another scope writes only its
   * own retired object, never this one. */
  const measurement = measured.current;
  const cardTop = (id: string): number | undefined => {
    const at = measurement.cards.get(id), grid = at ? measurement.grids.get(at.section) : undefined;
    return at && grid !== undefined ? measurement.region + measurement.body + measurement.section + grid + at.y : undefined;
  };
  // Owner, 20 Nov 19:45: on TV Jump to is the letter rail at the right, as on phones. It draws the letters of the titles
  // this page has loaded, the band over the cards in view, and scrolls to a letter's first card.
  const inView = (() => {
    let first: number | undefined, last: number | undefined;
    list.forEach(({entry}, index) => {
      const top = cardTop(entry.id);
      if (top !== undefined && top >= scrollY - 1 && top < scrollY + viewportHeight) {first ??= index; last = index;}
    });
    return {first: first ?? 0, last: last ?? first ?? 0};
  })();
  const directoryView = tab === 'playlists' || tab === 'collections' || tab === 'views' ? tab : undefined;
  const posterGeometry = useCallback((columns: number, stride: number) => posters.forEach((entry, index) => measurement.cards.set(entry.id, {section: 'posters', y: Math.floor(index / columns) * stride})), [posters, measurement]);
  const squareGeometry = useCallback((columns: number, stride: number) => squares.forEach((entry, index) => measurement.cards.set(entry.id, {section: 'squares', y: Math.floor(index / columns) * stride})), [squares, measurement]);
  const historyHeight = useCallback((index: number) => {
    const row = snapshot.entries[index]!, day = dayLabel(row.updatedAt, runtime.text.locale), previous = index ? dayLabel(snapshot.entries[index - 1]?.updatedAt, runtime.text.locale) : undefined;
    // An initial list estimate only; TVLoadedGrid adopts each native measured row.
    return 104 + (day && day !== previous ? (index ? 30 : 6) + 10 + 22 * theme.textScale : 0);
  }, [snapshot.entries, runtime.text.locale, theme.textScale]);
  const posterRegion = `${ids.savedGrid}.posters`, squareRegion = `${ids.savedGrid}.squares`, historyRegion = `${ids.savedGrid}.history`;
  const firstSection = tab === 'history' ? snapshot.entries.some(historyFocusable) ? historyRegion : undefined : posters.length ? posterRegion : squares.length ? squareRegion : undefined;
  const gridFocus = (id: string, getFocusId: (entry: ContentEntry) => string) => ({id, getFocusId, enabled: active.visible,
    edges: {left: ids.rail, up: id === squareRegion && posters.length ? posterRegion : ids.savedToolbar, ...showJump ? {right: `${ids.savedGrid}.alphabet`} : {},
      ...(id === posterRegion && squares.length ? {down: squareRegion} : {})},
    neighbors: {previous: id === squareRegion && posters.length ? posterRegion : ids.savedToolbar,
      ...(id === posterRegion && squares.length ? {next: squareRegion} : {})},
    screenStart: {regionId: ids.savedToolbar, childId: tab === 'history' ? 'saved-period-7d' : 'saved-sort'}});
  // tv-more1/3 draw the server's own quick filters, with counts in the Filters menu; a server
  // that publishes none keeps the two the page has always offered.
  const publishedFilters = (snapshot.posters?.filters ?? snapshot.squares?.filters ?? []).flatMap(filter => filter.options)
    .filter(option => (savedListFilters as readonly string[]).includes(option.id));
  const quickFilters = publishedFilters.filter(option => option.id !== 'all');
  // tv-more1/3 and collections 15: the page opens on its first title or tile; an empty page
  // keeps the tab as the entry, so focus never lands nowhere.
  const firstPlaylist = (snapshot.playlists?.sections ?? []).flatMap(section => section.entries).find(entry => entry.kind === 'playlist');
  // A card's focus child is its testID, whose id is URI-encoded; a tile's focus id is not.
  const firstChild = tab === 'watchlist' || tab === 'favorites'
    ? (posters[0] ? `saved-poster-${encodeURIComponent(posters[0].id)}` : squares[0] ? `saved-square-${encodeURIComponent(squares[0].id)}` : undefined)
    : tab === 'history' ? (snapshot.entries.find(historyFocusable) ? `saved-history-${encodeURIComponent(snapshot.entries.find(historyFocusable)!.id)}` : undefined)
      : tab === 'playlists' ? (snapshot.playlists?.actions?.includes('create_playlist') ? 'saved-tv-new-playlists' : firstPlaylist ? `saved-tv-playlists-${firstPlaylist.id}` : undefined)
        : tab === 'collections' && snapshot.phase === 'ready' ? 'saved-tv-new-collections'
          : snapshot.resources[0] ? `saved-tv-${tab}-${snapshot.resources[0].id}` : undefined;
  // An empty page draws its own Browse Home action, and that is where entry focus belongs when
  // there is no card to land on: the same child the empty state gives its primary action.
  const emptyEntry = !firstChild && snapshot.phase === 'ready'
    && (tab === 'history' ? snapshot.entries.length === 0
      : tab === 'watchlist' || tab === 'favorites' ? !posters.length && !squares.length : false)
    ? focusTestID('saved-empty', JSON.stringify(['action', 'browse-home'])) : undefined;
  const entryChild = firstChild ?? emptyEntry;
  const firstRegion = directoryView ? `${ids.savedGrid}.cards` : firstSection ?? ids.savedGrid;
  const tabs = useSavedTVTabs({destination: tabsDestination, reportError: runtime.reportError, authority, activation, contentRegionId: ids.savedGrid});
  useDestinationEntryFocus(active, entryChild ? {regionId: firstRegion, childId: entryChild} : undefined);
  useTVPageBack(runtime, {kind: 'saved'}, theme.isTV, {
    registerTop: true, scope: JSON.stringify([active.revision, tab, sort, filter, period]),
    current: () => activation.getSnapshot().visible,
    start: () => entryChild ? {regionId: firstRegion, childId: entryChild} : undefined,
    atTop: () => scrollY <= 0 && (!directoryView || directoryPage.current?.atTop() !== false),
    scrollTop: () => {directoryPage.current?.scrollTop(); page.current?.scrollTo({y: 0, animated: false});},
  });
  const sortLabel = sorts.find(value => value.id === sort)?.label ?? 'web.saved.sort.added';
  const alphabetId = `${ids.savedGrid}.alphabet`;
  const alphabet = showJump && active.visible ? {
    positionIndex: Object.fromEntries(jumpLetters.map(letter => [letter.key, letter.index])), total: list.length, firstVisible: inView.first, lastVisible: inView.last,
    // Left goes back into the cards (the rail runs beside the toolbar too, and tvOS would pick a chip by position).
    focus: {id: alphabetId, enabled: active.visible, edges: {left: firstSection ?? ids.savedGrid}},
    onJump: (_index: number, key: string) => {
      const target = jumpLetters.find(letter => letter.key === key), top = target ? cardTop(target.id) : undefined;
      if (top !== undefined) page.current?.scrollTo({y: Math.max(0, top - 24), animated: true});
    },
  } : undefined;
  return {snapshot, ids, active, tab, sort, setSort, filter, setFilter, period, setPeriod, sortOpen, setSortOpen, filterOpen, setFilterOpen,
    sortAnchor, filterAnchor, tabs, posterRegion, squareRegion, historyRegion, posters, squares, posterCount, squareCount, gridFocus,
    page, contentRoot, directoryPage, scrollY, setScrollY, viewportHeight, setViewportHeight, contentHeight, setContentHeight, count, theme,
    measurement, scope, posterGeometry, squareGeometry, firstSection, directoryView, historyHeight, publishedFilters, quickFilters, emptyEntry,
    alphabet, run, openClear, clearView, closeClear, confirmClear, clearing, sortLabel};
}
