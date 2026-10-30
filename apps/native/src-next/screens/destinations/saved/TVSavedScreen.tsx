import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {titleBucketValue} from '../../../../../../packages/client-core/src/browse-buckets';
import {savedListFilters, type HistoryPeriod, type SavedListFilter, type SavedListSort, type SavedRoute} from '../../../../../../packages/client-core/src/saved';
import {Artwork, StandardBackdrop} from '../../../kit/artwork';
import {Button, Chip, Focusable} from '../../../kit/controls';
import {PosterCard, SquareCard} from '../../../kit/cards';
import {Menu} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {FocusRegion, focusTestID} from '../../../kit/focus';
import type {NativeSavedSelectionPort} from '../../saved/SavedScreen';
import {browseType} from '../../../kit/browse/presentation';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDestinationActivationPort} from '../focus';
import {nativeDestinationFocusIds} from '../focus';
import type {NativeEntryActions} from '../entry-actions';
import {TVFocusGrid} from '../../../tv/layout/TVFocusGrid';
import {SavedTVTabs} from '../../saved/SavedTVTabs';
import {SavedConfirmSheet} from '../../saved/SavedForms';
import {LibraryJumpSheet} from '../../library/LibraryJumpSheet';
import {useTVPageBack} from '../useTVPageBack';
import {useDestinationEntryFocus} from '../useEntryFocus';
import {sameAuthority, type ShellAuthority} from '../../../shell/model';
import {SavedTVLoading} from '../../saved/SavedTVLoading';
import {SavedTVDirectory, type SavedTVDirectoryHandle} from '../../saved/SavedTVDirectory';
import type {NativeSavedViewDestination} from '../../saved/view-model';
import type {NativeTVSavedController, NativeTVSavedSnapshot, NativeTVSavedTab} from './model';

const t = defaultI18n.t;
const itemID = (item: {id: string}) => item.id;
const historyFocusable = (row: {media?: ContentEntry}) => !!row.media;
const factLine = (text: string) => Object.freeze([{id: 'caption', text, role: 'value' as const}]);
type Tab = NativeTVSavedTab;
type Actions = Pick<NativeEntryActions, 'menu' | 'isMenuOpen' | 'play'>;
const sorts: readonly Readonly<{id: Exclude<SavedListSort, 'updated'>; label: Parameters<typeof t>[0]}>[] = [
  {id: 'added', label: 'web.saved.sort.added'}, {id: 'title', label: 'web.saved.sort.title'},
  {id: 'year', label: 'web.saved.sort.year'}, {id: 'duration', label: 'web.saved.sort.duration'},
  {id: 'progress', label: 'web.saved.sort.progress'},
];
const filters: readonly Readonly<{id: SavedListFilter; label: Parameters<typeof t>[0]}>[] = [
  {id: 'all', label: 'search.chip.all'}, {id: 'unwatched', label: 'filter.quick.unwatched'}, {id: 'inProgress', label: 'filter.quick.inProgress'},
];
const periods: readonly HistoryPeriod[] = ['24h', '7d', '30d', '90d', 'all'];
const periodLabel: Record<HistoryPeriod, Parameters<typeof t>[0]> = {['24h']: 'saved.today', '7d': 'web.saved.thisWeek', '30d': 'web.saved.days', '90d': 'web.saved.days', all: 'web.saved.allTime'};

const emptyFor: Record<Tab, Readonly<{title: Parameters<typeof t>[0]; body: Parameters<typeof t>[0]}>> = {
  watchlist: {title: 'web.saved.empty.watchlist', body: 'web.saved.empty.watchlistBody'},
  favorites: {title: 'web.saved.empty.favorites', body: 'web.saved.empty.favoritesBody'},
  playlists: {title: 'web.saved.empty.playlists', body: 'web.saved.empty.playlistsBody'},
  collections: {title: 'web.saved.empty.collections', body: 'web.saved.empty.collectionsBody'},
  views: {title: 'web.saved.empty.views', body: 'web.saved.empty.viewsBody'},
  history: {title: 'web.saved.empty.history', body: 'web.saved.empty.historyBody'},
};

function dayLabel(value: string | undefined, locale: string | undefined): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return undefined;
  const now = new Date(), at = new Date(date.getFullYear(), date.getMonth(), date.getDate()), today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const days = Math.round((today.getTime() - at.getTime()) / 86400000);
  if (days === 0) return t('saved.today');
  if (days === 1) return t('relative.yesterday');
  return new Intl.DateTimeFormat(locale ?? 'en-US', {weekday: 'long'}).format(date);
}

function clock(value: string | undefined, locale: string | undefined): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return undefined;
  return new Intl.DateTimeFormat(locale ?? 'en-US', {hour: 'numeric', minute: '2-digit'}).format(date);
}

function historyLine(entry: ContentEntry): string {
  if (entry.kind === 'episode') return [entry.seasonNumber !== undefined && entry.episodeNumber !== undefined ? t('title.episodeCode', {season: entry.seasonNumber, episode: entry.episodeNumber}) : undefined, entry.title].filter(Boolean).join(' · ');
  if (['song', 'album', 'artist'].includes(entry.kind)) return [entry.artist?.name, entry.album?.name].filter(Boolean).join(' · ') || entry.subtitle || '';
  if (entry.kind === 'book' || entry.kind === 'audiobook_file') return entry.author ?? '';
  if (entry.kind === 'movie') return [t('search.kind.movie'), entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false})].filter(Boolean).join(' · ');
  return entry.subtitle ?? '';
}

function Empty({tab, browseHome}: {tab: Tab; browseHome: () => void}) {
  const copy = emptyFor[tab];
  const body = [t(copy.body), tab === 'watchlist' ? t('saved.holdHint') : ''].filter(Boolean).join(' ');
  return <StateView testID="saved-empty" variant="centered" title={t(copy.title)} body={body} primaryAction={{id: 'browse-home', label: t('saved.browseHome'), onPress: browseHome}}/>;
}

function HistoryRow({entry, updatedAt, positionSeconds, completed, runtime, actions, openEntry, onFocus, disabled}: {
  entry: ContentEntry; updatedAt?: string; positionSeconds?: number; completed?: boolean; runtime: NativeShellRuntime; actions: Actions; openEntry: (entry: ContentEntry) => void; onFocus?: () => void; disabled?: boolean;
}) {
  const theme = useTheme(), progress = entry.duration && positionSeconds !== undefined ? Math.max(0, Math.min(1, positionSeconds / entry.duration)) : undefined;
  const remaining = entry.duration && positionSeconds !== undefined ? durationText(Math.max(0, entry.duration - positionSeconds), runtime.text.t) : undefined;
  const video = ['movie', 'episode', 'show', 'season', 'extra'].includes(entry.kind), art = runtime.artwork(video ? entry.backdropUrl ?? entry.stillUrl : entry.posterUrl);
  return <Focusable disabled={disabled} onFocus={onFocus} testID={`saved-history-${encodeURIComponent(entry.id)}`} accessibilityRole="button" accessibilityLabel={entry.show?.name ?? entry.displayTitle ?? entry.title}
    onPress={() => entry.playback ? actions.play(entry) : openEntry(entry)} onLongPressAction={() => actions.menu(entry)} focusKind="row" focusRadius={16} style={{height: 104}}>
    {({focused}) => <View style={{height: 104, marginBottom: 8, flexDirection: 'row', alignItems: 'center', gap: 26, paddingLeft: 14, paddingRight: 28, borderRadius: 16, backgroundColor: focused ? theme.colors.raisedHigh : undefined}}>
      <View style={{width: 156, height: 88, borderRadius: 10, overflow: 'hidden', backgroundColor: theme.colors.raised, flexShrink: 0}}>
        {art ? <Artwork source={art} width={156} height={88}/> : null}
        {!video && art ? <View style={{position: 'absolute', left: 42, top: 8, width: 72, height: 72, borderRadius: 6, overflow: 'hidden', borderWidth: 1, borderColor: theme.colors.line}}><Artwork source={art} width={72} height={72}/></View> : null}
      </View>
      <View style={{flex: 1, minWidth: 0}}>
        <Text numberOfLines={1} style={[browseType(theme, 28, 700), {color: theme.colors.text}]}>{entry.show?.name ?? entry.displayTitle ?? entry.title}</Text>
        <Text numberOfLines={1} style={[browseType(theme, 22, 400), {color: theme.colors.textTertiary, marginTop: 3}]}>{historyLine(entry)}</Text>
      </View>
      <View style={{width: 280, flexShrink: 0}}>
        {completed ? <Text style={[browseType(theme, 21), {color: theme.colors.textTertiary}]}>{t('saved.watched')}</Text> : <>
          {progress !== undefined ? <View style={{height: 6, borderRadius: 6, backgroundColor: 'rgba(237,245,250,.18)', marginBottom: 8}}><View style={{height: 6, width: `${progress * 100}%`, borderRadius: 6, backgroundColor: theme.colors.accentStrong}}/></View> : null}
          {remaining ? <Text style={[browseType(theme, 21), {color: theme.colors.textTertiary}]}>{t('next.kit.remaining', {time: remaining})}</Text> : null}
        </>}
      </View>
      <Text style={[browseType(theme, 23), {width: 140, textAlign: 'right', color: theme.colors.textSecondary, fontVariant: ['tabular-nums'], flexShrink: 0}]}>{clock(updatedAt, runtime.text.locale) ?? ''}</Text>
    </View>}
  </Focusable>;
}

/** tv-more1–5: the TV Saved landing with the six drawn tabs and server-owned bodies. */
export function TVSavedScreen({controller, runtime, actions, activation, authority, openSaved, openEntry, browseHome, initialTab, selection}: {
  controller: NativeTVSavedController; runtime: NativeShellRuntime; actions: Actions; activation: NativeDestinationActivationPort;
  authority: Parameters<typeof nativeDestinationFocusIds>[0]; initialTab: Tab; selection?: NativeSavedSelectionPort;
  openSaved: (route: SavedRoute) => void; openEntry: (entry: ContentEntry) => void; browseHome: () => void;
}) {
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
  type JumpOpening = Readonly<{token: number; scope: string}>;
  const jump = useRef<JumpOpening | undefined>(undefined), jumpToken = useRef(0), jumpAlive = useRef(true);
  const [jumpOpening, setJumpOpening] = useState<JumpOpening>();
  useLayoutEffect(() => {
    jumpAlive.current = true;
    return () => {jumpAlive.current = false; jump.current = undefined;};
  }, []);
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
  if (jump.current && (replaced || jump.current.scope !== scope || !active.visible)) jump.current = undefined;
  const opening = jumpOpening && jump.current === jumpOpening && jumpOpening.scope === scope && active.visible ? jumpOpening : undefined;
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
  const openJump = () => {const next = {token: ++jumpToken.current, scope}; jump.current = next; setJumpOpening(next);};
  const closeJump = (target: JumpOpening) => {
    if (jump.current !== target) return;
    jump.current = undefined;
    if (jumpAlive.current) setJumpOpening(current => current === target ? undefined : current);
  };
  const jumpCurrent = (target: JumpOpening) => jumpAlive.current && jump.current === target && target.scope === scope && active.visible;
  const cardTop = (id: string): number | undefined => {
    const at = measurement.cards.get(id), grid = at ? measurement.grids.get(at.section) : undefined;
    return at && grid !== undefined ? measurement.region + measurement.body + measurement.section + grid + at.y : undefined;
  };
  const selectedLetter = (() => {
    if (!jumpLetters.length) return undefined;
    const edge = scrollY + 140; let found: string | undefined;
    for (const letter of jumpLetters) {const top = cardTop(letter.id); if (top !== undefined && top <= edge) found = letter.key;}
    return found ?? jumpLetters[0]!.key;
  })();
  // The scope is part of the key: a successor query remounts its cards, so they lay their own
  // coordinates out instead of inheriting an old scope's. An appended page keeps every retained
  // card mounted.
  const measure = (section: 'posters' | 'squares', entry: ContentEntry, child: React.ReactNode, top?: number) => <View key={`${scope}:${entry.id}`} style={{width: 200}}
    onLayout={event => {measurement.cards.set(entry.id, {section, y: top ?? event.nativeEvent.layout.y});}}>{child}</View>;
  const card = (entry: ContentEntry, onFocus?: () => void, top?: number) => measure('posters', entry, <PosterCard disabled={!active.visible} onFocus={onFocus} testID={`saved-poster-${encodeURIComponent(entry.id)}`} width={200} title={entry.displayTitle ?? entry.title} caption={factLine(historyLine(entry))} artwork={runtime.artwork(entry.posterUrl)}
    onNavigate={() => openEntry(entry)} onMore={() => actions.menu(entry, undefined, select?.(entry))} moreExpanded={actions.isMenuOpen(entry)} selection={selection?.item(entry)}/>, top);
  // tv-more3: on a saved album or audiobook, OK plays it; hold OK still opens its menu. A square
  // that has nothing to play keeps opening its own page.
  const square = (entry: ContentEntry, onFocus?: () => void, top?: number) => measure('squares', entry, <SquareCard disabled={!active.visible} onFocus={onFocus} testID={`saved-square-${encodeURIComponent(entry.id)}`} width={200} title={entry.displayTitle ?? entry.title} caption={factLine(entry.artist?.name ?? entry.author ?? '')} artwork={runtime.artwork(entry.posterUrl)}
    {...(entry.playback ? {primaryAction: 'play' as const, onPlay: () => actions.play(entry)} : {onNavigate: () => openEntry(entry)})}
    onMore={() => actions.menu(entry, undefined, select?.(entry))} moreExpanded={actions.isMenuOpen(entry)} selection={selection?.item(entry)}/>, top);
  /** The menu's Select starts this screen's own canonical Saved selection on that title. */
  const select = selection ? (entry: ContentEntry) => () => {void selection.item(entry)?.onEnter();} : undefined;
  const heading = (title: string, detail: number) => <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 18, marginTop: 44, marginBottom: 22}}>
    <Text style={[browseType(theme, 32, 700, -.019), {color: theme.colors.text}]}>{title}</Text><Text style={[browseType(theme, 22, 600), {color: theme.colors.textTertiary}]}>{defaultI18n.number(detail)}</Text>
  </View>;
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
    edges: {left: ids.rail, up: id === squareRegion && posters.length ? posterRegion : ids.savedToolbar,
      ...(id === posterRegion && squares.length ? {down: squareRegion} : {})},
    neighbors: {previous: id === squareRegion && posters.length ? posterRegion : ids.savedToolbar,
      ...(id === posterRegion && squares.length ? {next: squareRegion} : {})},
    screenStart: {regionId: ids.savedToolbar, childId: tab === 'history' ? 'saved-period-7d' : 'saved-sort'}});
  const body = snapshot.phase === 'error' ? <StateView testID="saved-error" variant="centered" {...runtime.error(snapshot.error, () => {run(controller.select(tab, {sort, filter, period}));})}/>
    : snapshot.phase !== 'ready' ? <SavedTVLoading key={`${scope}:${period}`} shape={tab === 'history' ? 'history' : 'posters'}/>
      : tab === 'history' ? (snapshot.entries.length ? <TVFocusGrid focus={{...gridFocus(historyRegion, entry => entry.id), getFocusId: row => `saved-history-${encodeURIComponent(row.id)}`, isFocusable: historyFocusable}} key={`${scope}:history:${period}`} items={snapshot.entries} getKey={itemID} itemWidth={1} itemHeight={104} gap={0} rowGap={0}
        scrollY={scrollY} viewportHeight={viewportHeight} coordinateRoot={contentRoot} initialWidth={1920 - theme.gutter - theme.layout.gutter.tvRight} layoutRevision={contentHeight} getRowHeight={historyHeight}
        renderItem={(row, cell) => {const index = cell.index;const day = dayLabel(row.updatedAt, runtime.text.locale), previous = index ? dayLabel(snapshot.entries[index - 1]?.updatedAt, runtime.text.locale) : undefined;
          return <React.Fragment key={row.id}>{day && day !== previous ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 16, marginTop: index ? 30 : 6, marginBottom: 10}}><Text style={[browseType(theme, 22, 700), {color: theme.colors.textTertiary}]}>{day}</Text></View> : null}
            {row.media ? <HistoryRow disabled={!active.visible} entry={row.media} updatedAt={row.updatedAt} positionSeconds={row.positionSeconds} completed={row.completed} runtime={runtime} actions={actions} openEntry={openEntry} onFocus={cell.onFocus}/> : null}</React.Fragment>;}}/> : <Empty tab="history" browseHome={browseHome}/>)
        : tab === 'watchlist' || tab === 'favorites' ? ((posters.length || squares.length)
          ? <View onLayout={event => {measurement.section = event.nativeEvent.layout.y;}}>
            {posters.length ? <>{squareCount ? heading(t('saved.postersHeading'), posterCount) : null}<TVFocusGrid focus={gridFocus(posterRegion, entry => `saved-poster-${encodeURIComponent(entry.id)}`)} key={`${scope}:posters`} items={posters} getKey={itemID} itemWidth={200} itemHeight={380} gap={32} rowGap={40} scrollY={scrollY} viewportHeight={viewportHeight} coordinateRoot={contentRoot} initialWidth={1920 - theme.gutter - theme.layout.gutter.tvRight} layoutRevision={contentHeight} onLayout={event => {measurement.grids.set('posters', event.nativeEvent.layout.y);}} onGeometry={posterGeometry} renderItem={(entry, cell) => card(entry, cell.onFocus, cell.top)}/></> : null}
            {squares.length ? <>{posterCount ? heading(t('saved.squaresHeading'), squareCount) : null}<TVFocusGrid focus={gridFocus(squareRegion, entry => `saved-square-${encodeURIComponent(entry.id)}`)} key={`${scope}:squares`} items={squares} getKey={itemID} itemWidth={200} itemHeight={280} gap={32} rowGap={40} scrollY={scrollY} viewportHeight={viewportHeight} coordinateRoot={contentRoot} initialWidth={1920 - theme.gutter - theme.layout.gutter.tvRight} layoutRevision={contentHeight} onLayout={event => {measurement.grids.set('squares', event.nativeEvent.layout.y);}} onGeometry={squareGeometry} renderItem={(entry, cell) => square(entry, cell.onFocus, cell.top)}/></> : null}
          </View> : <Empty tab={tab} browseHome={browseHome}/>)
        : null;
  // tv-more1/3 draw the server's own quick filters, with counts in the Filters menu; a server
  // that publishes none keeps the two the page has always offered.
  const publishedFilters = (snapshot.posters?.filters ?? snapshot.squares?.filters ?? []).flatMap(filter => filter.options)
    .filter(option => (savedListFilters as readonly string[]).includes(option.id));
  const quickFilters = publishedFilters.filter(option => option.id !== 'all');
  // tv-more1/3 and collections 15: the page opens on its first title or tile; an empty page
  // keeps the tab as the entry (SavedTVTabs), so focus never lands nowhere.
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
  useDestinationEntryFocus(active, entryChild ? {regionId: firstRegion, childId: entryChild} : undefined);
  useTVPageBack(runtime, {kind: 'saved'}, theme.isTV, {
    registerTop: true, scope: JSON.stringify([active.revision, tab, sort, filter, period]),
    current: () => activation.getSnapshot().visible,
    start: () => entryChild ? {regionId: firstRegion, childId: entryChild} : undefined,
    atTop: () => scrollY <= 0 && (!directoryView || directoryPage.current?.atTop() !== false),
    scrollTop: () => {directoryPage.current?.scrollTop(); page.current?.scrollTo({y: 0, animated: false});},
  });
  const sortLabel = sorts.find(value => value.id === sort)?.label ?? 'web.saved.sort.added';
  return <View testID="native-tv-saved" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <StandardBackdrop/>
    <ScrollView ref={page} onContentSizeChange={(_width, height) => setContentHeight(height)} onLayout={event => setViewportHeight(event.nativeEvent.layout.height)} onScroll={event => setScrollY(event.nativeEvent.contentOffset.y)} scrollEventThrottle={16} contentContainerStyle={{paddingBottom: 64}} showsVerticalScrollIndicator={false}><View ref={contentRoot} collapsable={false}>
      {/* LibraryHeader already carries the page's 200 inset; collections.html 15-17 draw the
          tabs row from there, with the six tab labels 34 apart. */}
      <SavedTVTabs destination={tabsDestination} reportError={runtime.reportError} authority={authority} activation={activation} contentRegionId={ids.savedGrid}/>
      {directoryView ? <View style={{height: 860, paddingTop: 12}}>
        <SavedTVDirectory view={directoryView} destination={controller.source(directoryView)} runtime={runtime} libraries={runtime.getSnapshot().libraries}
          pageRef={directoryPage} enabled={active.visible} activation={activation} regionId={ids.savedGrid} edges={{up: ids.savedTabs, left: ids.rail}} select={route => {if (route.view === 'playlists') run(controller.select('playlists', {sort: route.sort, direction: route.direction, filter: route.filter}));}}
          openPlaylist={card => openSaved({view: 'playlist', playlistId: card.id})} openResource={value => openSaved({view: 'resource', resourceId: value.id})}/>
      </View> : <View onLayout={event => {measurement.region = event.nativeEvent.layout.y;}}>
      <FocusRegion id={ids.savedToolbar} enabled={active.visible} orientation="horizontal" entry={tab === 'history' ? 'saved-period-7d' : 'saved-sort'}
        edges={{up: ids.savedTabs, left: ids.rail, down: ids.savedGrid}}
        style={{flexDirection: 'row', alignItems: 'center', gap: 14, paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, paddingTop: 24}}>
        {tab !== 'history' ? <>
          <Button focusId="saved-sort" ref={sortAnchor} variant="glass" icon="i-sort" size="compact" onPress={() => setSortOpen(true)} accessibilityLabel={t('saved.sort')}>{t(sortLabel)}</Button>
          <Button focusId="saved-filter" ref={filterAnchor} variant="glass" icon="i-filter" size="compact" onPress={() => setFilterOpen(true)} accessibilityLabel={t('filter.title')}>{t('filter.title')}</Button>
          {showJump && <Chip focusId="saved-jump" disabled={!active.visible} onPress={openJump}>{t('next.browse.native.jumpTo')}</Chip>}
          <View style={{width: 1.5, height: 40, backgroundColor: theme.colors.line, marginHorizontal: 6}}/>
          {quickFilters.length
            ? quickFilters.map(option => <Chip focusId={`saved-filter-${option.id}`} key={option.id} selected={filter === option.id}
              onPress={() => {const next = filter === option.id ? 'all' as const : option.id as SavedListFilter; setFilter(next); run(controller.select(tab, {sort, filter: next, period}));}}>{option.label}</Chip>)
            : filters.filter(value => value.id !== 'all').map(value => <Chip focusId={`saved-filter-${value.id}`} key={value.id} selected={filter === value.id}
              onPress={() => {const next = filter === value.id ? 'all' as const : value.id; setFilter(next); run(controller.select(tab, {sort, filter: next, period}));}}>{t(value.label)}</Chip>)}
        </> : <>
          <Button variant="glass" icon="i-sort" size="compact" disabled accessibilityLabel={t('saved.sort')}>{t('web.saved.sort.recent')}</Button>
          {periods.map(value => <Chip focusId={`saved-period-${value}`} key={value} selected={period === value} onPress={() => {setPeriod(value); run(controller.select(tab, {sort, filter, period: value}));}}>
            {value === '30d' || value === '90d' ? t('web.saved.days', {count: value === '30d' ? 30 : 90}) : t(periodLabel[value])}
          </Chip>)}
          <Button focusId="saved-clear-history" variant="glass" icon="i-trash" size="compact" style={{marginLeft: 'auto'}} onPress={openClear}>{t('web.saved.clearHistory')}</Button>
        </>}
        <Text style={[browseType(theme, 22), {marginLeft: tab === 'history' ? 0 : 'auto', color: theme.colors.textTertiary}]}>{t('count.titles', {count})}</Text>
      </FocusRegion>
      <FocusRegion id={ids.savedGrid} enabled={active.visible} entry={emptyEntry} entryRegion={firstSection}
        edges={{up: ids.savedToolbar, left: ids.rail}} onLayout={event => {measurement.body = event.nativeEvent.layout.y;}}
        style={{paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, paddingTop: 38, minHeight: 520}}>{body}</FocusRegion>
      </View>}
    </View></ScrollView>
    <Menu testID={`${ids.savedGrid}.sort-menu`} open={sortOpen} onOpenChange={setSortOpen} anchorRef={sortAnchor} title={t('saved.sort')} placement="bottom-start" items={sorts.map(value => ({id: value.id, label: t(value.label), selected: value.id === sort,
      onSelect: () => {setSort(value.id); run(controller.select(tab, {sort: value.id, filter, period}));}}))}/>
    <Menu testID={`${ids.savedGrid}.filter-menu`} open={filterOpen} onOpenChange={setFilterOpen} anchorRef={filterAnchor} title={t('filter.title')} placement="bottom-start" items={publishedFilters.length
      ? publishedFilters.map(option => ({id: option.id, label: option.label, trailing: defaultI18n.number(option.count), selected: option.id === filter,
        onSelect: () => {setFilter(option.id as SavedListFilter); run(controller.select(tab, {sort, filter: option.id as SavedListFilter, period}));}}))
      : filters.map(value => ({id: value.id, label: t(value.label), selected: value.id === filter,
        onSelect: () => {setFilter(value.id); run(controller.select(tab, {sort, filter: value.id, period}));}}))}/>
    {/* tv-more3 draws History's Clear history confirm as the saved pages' own destructive
        confirm: Cancel first, then the red verb, and the sentence naming what stays. */}
    <SavedConfirmSheet open={clearView !== undefined} title={t('web.saved.clearHistoryTitle')} body={t('web.saved.clearHistoryBody')} action={t('web.saved.clearHistoryAction')}
      busy={clearing} destructive testID="saved-clear-history" onClose={() => {if (clearView) closeClear(clearView);}}
      onConfirm={() => {if (clearView) confirmClear(clearView);}}/>
    {opening && <LibraryJumpSheet open panelId={`${ids.savedGrid}.jump`} letters={jumpLetters.map(letter => ({key: letter.key, index: letter.index}))} selected={selectedLetter}
      onSelect={key => {
        if (!jumpCurrent(opening)) return;
        const target = jumpLetters.find(letter => letter.key === key), top = target ? cardTop(target.id) : undefined;
        if (top !== undefined) page.current?.scrollTo({y: Math.max(0, top - 24), animated: true});
        closeJump(opening);
      }} onClose={() => closeJump(opening)}/>}
    {selection?.render()}
  </View>;
}
