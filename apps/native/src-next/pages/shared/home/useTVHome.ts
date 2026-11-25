import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, type ViewProps} from 'react-native';
import {runOnJS, useAnimatedScrollHandler, useSharedValue} from 'react-native-reanimated';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {HomeRow} from '../../../../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {homeRowPresentation, homeSpotlightModels, homeTitleFactParts} from '../../../../../../packages/client-core/src/presentation/home-spotlight';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {useFocusRegistry, type FocusRegionProps} from '../../../kit/focus';
import {createInitialRegionEntry} from '../../../kit/focus/initial-entry';
import {homeRowMetrics, type HomeHeroAction} from '../../../kit/home';
import {useFocusAnchor} from '../../../kit/layout/FocusAnchor';
import type {HeroCandidate} from '../../../kit/layout';
import {skeletonTiming} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {noteHomeRead} from '../../../shell/home-read-log';
import {logBootMilestone} from '../../../shell/boot-timing';
import {type ShellAuthority, sameAuthority} from '../../../shell/model';
import {NativeHomePaging} from '../../../shell/home-paging';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds, tvHomeRegionMap} from '../../../shell/tv-focus';
import {useTVPageBack} from '../../../screens/destinations/useTVPageBack';
import {homeMixesFocusRow as mixesFocusRow, homeSpotlightFocusActions, homeStartModel, resolveTVHomeStart} from '../../../screens/home/home-start';
import {homeMountedRows, homeVisibleRange, type HomeRowBox} from '../../../screens/home/home-window';
import {liveRereadPlan, type LiveRereadMemory} from '../../../screens/home/live-expiry';
import {authorizedHome, homeCardText, homePlayTarget, homeRowHasContent, pageAtVisibleEnd, platformRows} from '../../../screens/home/model';
import {tvHomeRows} from '../../../screens/home/tv-presentation';
import {useTvHomeRecovery} from '../../../screens/home/useTvHomeRecovery';

type HomeCandidate = HeroCandidate & {entry: ContentEntry; target?: ContentEntry; primaryLabel?: string};
type HomeCardModel = ReturnType<typeof homeRowPresentation>['cards'][number];
const noMixesSubscription = () => () => {};
const noMixesSnapshot = () => undefined;

export type TVHomeProps = {
  runtime: NativeShellRuntime; authority: ShellAuthority;
  active?: boolean; heroObscured?: boolean; railExpanded?: boolean; bottomInset?: number; onScrolledChange?: (scrolled: boolean) => void;
  holdRailDim?: (lease: Readonly<{retired: boolean}>) => void; releaseRailDim?: (lease: Readonly<{retired: boolean}>) => void;
};

export function useTVHome({runtime, authority, active = true, heroObscured = false, railExpanded = false, bottomInset = 0, onScrolledChange, holdRailDim, releaseRailDim}: TVHomeProps) {
  const registry = useFocusRegistry();
  const ids = useMemo(() => tvFocusIds(authority), [authority]);
  const homeCardFocusId = useCallback((item: HomeCardModel) => ids.entry(item.entry), [ids]);
  const theme = useTheme(), t = runtime.text.t;
  const labels = useMemo(() => ({t, has: defaultI18n.has}), [t]);
  const spec = theme.spec.hero.tvHome;
  const cardScale = (runtime.appearanceCardSizePercent ?? 100) / 100, showBackdrops = runtime.appearanceShowBackdrops !== false;
  const snapshot = useSyncExternalStore(runtime.home.subscribe, runtime.home.getSnapshot);
  const librariesUnavailable = useSyncExternalStore(runtime.subscribe ?? noMixesSubscription, () => {
    try {return runtime.getSnapshot?.().librariesUnavailable === true;}
    catch {return false;}
  });
  const heroHeight = spec.spotlight;
  const heroActive = active && !heroObscured && !runtime.getSnapshot().selectionActive;
  const document = authorizedHome(snapshot.document, authority);
  const ownershipIds = useMemo(() => [ids.spotlight, ...(document?.rows ?? []).map(row => ids.row(row.id))], [document, ids]);
  const subscribeOwnership = useCallback((listener: () => void) => {
    if (typeof registry.subscribeRegion !== 'function') return registry.subscribe(listener);
    const releases = ownershipIds.map(id => registry.subscribeRegion(id, listener));
    return () => {for (const release of releases) release();};
  }, [ownershipIds, registry]);
  const ownershipSnapshot = useCallback(() => {
    let activeIds = '';
    for (const id of ownershipIds) if (registry.isRegionActive(id)) activeIds += `${id}\u0000`;
    return activeIds;
  }, [ownershipIds, registry]);
  // Home needs ownership transitions for row ordering. A child-to-child move is
  // already delivered by the focused card's preview callback, not every registry revision.
  const ownership = useSyncExternalStore(subscribeOwnership, ownershipSnapshot, ownershipSnapshot);
  const cachedAtMount = useRef(!!document);
  const [foreground, setForeground] = useState(AppState.currentState === 'active'), [loadingElapsed, setLoadingElapsed] = useState(0);
  // The recovery session lives here, at the screen's stable scope, so the service clearing
  // its error for the length of a retry cannot unmount it or restart its schedule.
  const recovery = useTvHomeRecovery({runtime, authority, error: snapshot.error, active: active && foreground, resolved: !!document, enabled: true});
  // states.html 5 dims the rail while the mounted recovery is the only thing to read. The lease
  // is that recovery's own ticket: it ends with the opening, and a stale release compares
  // against the current lease, so it can never clear a successor's.
  const railLease = recovery.visible ? recovery.session?.ticket : undefined;
  // Committed-layout acquisition and release: the lease is taken only once the recovery is on
  // screen and dropped in the same layout phase as its removal.
  useLayoutEffect(() => {
    if (!railLease) return;
    holdRailDim?.(railLease);
    return () => {releaseRailDim?.(railLease);};
  }, [railLease, holdRailDim, releaseRailDim]);
  const [heroVisible, setHeroVisible] = useState(true), [visibleRows, setVisibleRows] = useState<ReadonlySet<string>>(new Set());
  const [visibleRange, setVisibleRange] = useState<readonly [number, number]>([0, 0]);
  const [measuredRows, setMeasuredRows] = useState<Readonly<{layout: string; boxes: ReadonlyMap<string, {y: number; height: number}>}>>({layout: '', boxes: new Map()});
  // Phone and tablet: where the frame starts the rows in the scrolled content (a row measures itself from there).
  const [rowsTop, publishRowsTop] = useState<number>(), rowsTopRef = useRef<number | undefined>(undefined);
  const [focus, setFocusState] = useState<{rowId: string; entryId: string}>(), [heroFocused, setHeroFocused] = useState(false), [repeating, setRepeating] = useState(false);
  const focusTarget = useRef<typeof focus>(undefined), canAnchor = useRef(false);
  canAnchor.current = active && !railExpanded;
  const setFocus = useCallback((next: typeof focus) => {
    if (focusTarget.current?.rowId === next?.rowId && focusTarget.current?.entryId === next?.entryId) return;
    focusTarget.current = next; setFocusState(next);
  }, []);
  const [activeIndex, setActiveIndex] = useState(0);
  const scrolled = useRef(false);
  const initialEntry = useMemo(() => createInitialRegionEntry(registry, ids.spotlight), [registry, ids.spotlight]);
  const rowPositions = useRef(new Map<string, {y: number; height: number}>());
  // TV: a focused row's top sits under the 520 preview hero, whichever way focus came. A row (492) never fits between that
  // and a bottom margin, so keeping it merely visible put it 96 px higher when focus arrived from below, as at the first landing.
  const vertical = useFocusAnchor(false, active, 'start');
  const moveToTop = vertical.move;
  const current = useCallback(() => {
    try {return sameAuthority(authority, runtime.getSnapshot().authority);}
    catch (error) {if (error instanceof Error && error.name === 'AbortError') return false; throw error;}
  }, [authority, runtime]);
  const pages = useMemo(() => new NativeHomePaging(runtime.home, () => sameAuthority(authority, runtime.getSnapshot().authority), error => runtime.reportError(error)), [authority, runtime]);
  const pagingRevision = useSyncExternalStore(pages.subscribe, pages.getSnapshot, pages.getSnapshot);
  useEffect(() => pages.activate(), [pages]);
  useEffect(() => {
    const saved = registry.capture();
    const row = document?.rows.find(value => ids.row(value.id) === saved?.regionId);
    const page = row && pages.state(row.id), fulfilled = page?.fulfilled;
    const entryId = fulfilled?.firstItemId;
    if (row && entryId && saved?.childId === page?.hostId) {
      setFocus({rowId: row.id, entryId});
      setHeroFocused(false);
    }
  }, [document, ids, pages, pagingRevision, registry, setFocus]);
  const guarded = useCallback((run: () => void) => {if (current()) run();}, [current]);
  const libraries = runtime.getSnapshot().libraries;
  const readLibraryKey = JSON.stringify(libraries.filter(library => library.kind === 'books').map(library => library.id));
  const readLibraries = useMemo(() => new Set(JSON.parse(readLibraryKey) as string[]), [readLibraryKey]);
  const platformHomeRows = useMemo(() => document ? platformRows(document, 'tv', readLibraries) : [], [document, readLibraries]);
  const incomingRows = useMemo(() => {
    void pagingRevision;
    return platformHomeRows.filter(homeRowHasContent).map(row => pages.present(row));
  }, [pages, pagingRevision, platformHomeRows]);
  // The server publishes every Home row as a deferred descriptor with its real title and
  // shape, then the row previews hydrate one at a time. Draw only the descriptors whose
  // preview has not arrived yet as bounded skeletons; no card, count or focus target.
  const pendingRows = useMemo(() => platformHomeRows.filter(row => row.deferred === true && !homeRowHasContent(row)).slice(0, 3), [platformHomeRows]);
  const previousRows = useRef<readonly HomeRow[]>([]), previousIncoming = useRef<readonly HomeRow[]>(incomingRows);
  const rows = useMemo(() => {
    void ownership;
    const before = previousRows.current;
    const occupied = registry.isRegionActive(ids.spotlight) || before.some(row => registry.isRegionActive(ids.row(row.id)));
    const next = tvHomeRows(before, incomingRows, rowId => registry.isRegionActive(ids.row(rowId)), occupied);
    const sameInput = previousIncoming.current === incomingRows;
    previousIncoming.current = incomingRows;
    if (sameInput && before.length === next.length && before.every((row, index) => sameTVRowOrder(row, next[index]!))) return before;
    previousRows.current = next;
    return next;
  }, [ids, incomingRows, ownership, registry]);
  const rowIdsKey = useMemo(() => JSON.stringify(rows.map(row => row.id)), [rows]);
  const rowIds = useMemo(() => JSON.parse(rowIdsKey) as readonly string[], [rowIdsKey]);
  const rowLayoutKey = `${theme.textScale}:${cardScale}`;
  const rowLayoutKeyRef = useRef(rowLayoutKey);
  rowLayoutKeyRef.current = rowLayoutKey;
  const baseRowModels = useMemo(() => rows.map(row => {
    const presentation = homeRowPresentation(row);
    const metrics = homeRowMetrics(theme, row, {ranked: presentation.cards.some(card => card.rank !== undefined), cardScale});
    const title = serverTextLabel(labels, presentation.title);
    const subtitle = serverTextLabel(labels, presentation.subtitle) || undefined;
    const physicalIds = row.live ? row.live.channels.map(item => JSON.stringify([item.channel.sourceId, item.channel.id])) : row.entries.map(entry => entry.id);
    return {row, presentation, title, subtitle, physicalIds, ...metrics};
  }), [cardScale, labels, rows, theme]);
  const rowModels = useMemo(() => baseRowModels.map(model => ({
    ...model,
    slotHeight: measuredRows.layout === rowLayoutKey ? measuredRows.boxes.get(model.row.id)?.height ?? model.estimate : model.estimate,
  })), [baseRowModels, measuredRows, rowLayoutKey]);
  const sourceById = useMemo(() => new Map((document?.rows ?? []).map(row => [row.id, row])), [document]);
  // Owner, 18 Nov: no rotating spotlight. Until a card reports focus (at launch, and on coming back to Home) the hero shows
  // the start card, the first card of the first row of titles, so the spotlight never flashes up before the focused card.
  const focusShown = focus && rows.some(row => row.id === focus.rowId && row.entries.some(entry => entry.id === focus.entryId)) ? focus : undefined;
  const startRow = rows.find(row => !row.live && !row.collection && row.entries.length > 0);
  const heroFocus = focusShown ?? (startRow && {rowId: startRow.id, entryId: startRow.entries[0]!.id});
  const heroBlock = heroFocus ? spec.preview : spec.spotlight;
  const rowBoxes: readonly HomeRowBox[] = useMemo(() => {
    let rowY = rowsTop ?? heroBlock;
    return rowModels.map(({row, slotHeight}) => {
      const measured = measuredRows.layout === rowLayoutKey ? measuredRows.boxes.get(row.id) : undefined;
      const box = {id: row.id, y: measured && rowsTop !== undefined ? measured.y + rowsTop : rowY, height: slotHeight};
      rowY = box.y + slotHeight + frameSpec.rhythm.rowSection.tv;
      return box;
    });
  }, [heroBlock, measuredRows, rowLayoutKey, rowModels, rowsTop]);
  const boxesRef = useRef(rowBoxes), scrollBoxes = useSharedValue<readonly HomeRowBox[]>([]), reportedRange = useSharedValue('');
  boxesRef.current = rowBoxes;
  const capturedRegion = registry.capture()?.regionId;
  const mountedRows = homeMountedRows(rows.length, visibleRange[0], visibleRange[1], [rows.findIndex(row => row.id === focus?.rowId), rows.findIndex(row => ids.row(row.id) === capturedRegion)]);
  // F04: only rows that are mounted (visible, overscan or focus-pinned) ask the server for their shelf.
  // The drawn skeleton rows are demanded too: they are not in `rows` until they load, so leaving them out
  // parked every deferred row after a restore forever (E-tv-02, E-mobile-08).
  const rowDemand = JSON.stringify([...rows.filter((_row, index) => mountedRows.has(index)).map(row => row.id), ...pendingRows.map(row => row.id)]);
  // TV: skeleton rows have no focus target, so Down from the hero and the first native focus would otherwise skip
  // them to Customize and scroll Home to the bottom. Customize waits until no row is still loading.
  const holdCustomize = pendingRows.length > 0;
  const mixes = useSyncExternalStore(runtime.mixes?.subscribe ?? noMixesSubscription, runtime.mixes?.getSnapshot ?? noMixesSnapshot);
  const hasMixes = mixes?.phase === 'ready' && mixes.rows.length > 0;
  // Owner, 18 Nov (TV Home): Home opens on the first Continue Watching card under a moderate hero that follows focus; there is
  // no rotating spotlight with buttons. So the spotlight offers no focus stop: the start is the first row's first card, Up
  // from that row stops there, and the hero shows the focused card (FocusPreviewHero).
  const regionMap = useMemo(
    () => tvHomeRegionMap(authority, [...rowIds, ...(hasMixes ? [mixesFocusRow] : [])], {spotlightFocusable: false}),
    [authority, hasMixes, rowIds],
  );
  const mixesRegion = regionMap.rows.get(mixesFocusRow);
  const hasPlayback = !!runtime.playback;
  const startModel = useMemo(
    () => homeStartModel(document, authority, {rows: rowIds, readLibraryIds: readLibraries, mixesReady: hasMixes, playbackAvailable: hasPlayback, spotlight: false}),
    [authority, document, hasMixes, hasPlayback, readLibraries, rowIds],
  );
  const resolveStart = useCallback(() => resolveTVHomeStart(registry, startModel), [registry, startModel]);
  useTVPageBack(runtime, {kind: 'home'}, true, {
    // A pending jump from removed content cannot survive an actionless page.
    scope: startModel.actionless ? 'empty' : 'content', registerTop: true,
    current: () => active && foreground && sameAuthority(authority, runtime.getSnapshot().authority),
    // Back to the top lands on the card last held in the first row: the row's first card may be scrolled under the side menu,
    // where tvOS will not focus it.
    start: () => {
      const start = resolveStart().address, kept = start && registry.remembered(start.regionId);
      return start && kept && registry.child({regionId: start.regionId, childId: kept}) ? {regionId: start.regionId, childId: kept} : start;
    },
    atTop: () => {const start = resolveStart(); return start.kind === 'empty' || vertical.offset.value <= 0 && start.kind === 'ready';},
    // The hero keeps showing the focused card while Home scrolls back up, so the first row stays where focus can reach it.
    scrollTop: () => moveToTop(0, true),
  });
  useLayoutEffect(() => {
    if (startModel.actionless && scrolled.current && !runtime.retirementSignal?.aborted && current()) {
      scrolled.current = false; onScrolledChange?.(false);
    }
  }, [current, onScrolledChange, runtime, startModel.actionless]);
  const ranges = useRef(new Map<string, {first: number; last: number; keys?: readonly string[]}>());
  const viewportHeight = spec.canvasHeight, viewportWidth = spec.canvasWidth;

  // Cold-launch timing (P2-74 4b): the first commit with Home's document on screen, once per process.
  useEffect(() => {if (document && current()) logBootMilestone('home-painted', {rows: document.rows.length});}, [document, current]);
  // The restore has already read Home: a mount reads only when nothing has been read yet.
  useEffect(() => {if (current() && !runtime.home.getSnapshot().document) {noteHomeRead('mount'); void runtime.home.load();}}, [current, runtime]);
  useEffect(() => {const sub = AppState.addEventListener('change', state => setForeground(state === 'active')); return () => sub.remove();}, []);
  useEffect(() => {
    if (snapshot.phase !== 'loading' && snapshot.phase !== 'idle') return;
    // Only the 300 ms and slow thresholds matter: two timers, not a 120 ms re-render clock.
    setLoadingElapsed(0);
    const timers = [skeletonTiming.delayMs, skeletonTiming.slowMs].map(at => setTimeout(() => setLoadingElapsed(at), at));
    return () => timers.forEach(clearTimeout);
  }, [snapshot.phase]);
  // E-mobile-29: one timer per live expiry (live-expiry.ts). Unrelated document updates (row
  // hydration) keep it; only a re-read that brings back the same or an expired expiry backs off.
  const liveTimer = useRef<{expiry: string; timer: ReturnType<typeof setTimeout>} | undefined>(undefined);
  const liveMemory = useRef<LiveRereadMemory>({stalls: 0});
  const clearLiveTimer = useCallback(() => {if (liveTimer.current) {clearTimeout(liveTimer.current.timer); liveTimer.current = undefined;}}, []);
  useEffect(() => clearLiveTimer, [clearLiveTimer]);
  useEffect(() => {
    if (!active || !foreground || !document) {clearLiveTimer(); return;}
    // Only live rows on screen or about to be: an off-screen deferred row keeps its old answer
    // until hydration reads it again, so its stale expiry must not drive Home re-reads.
    const demanded = new Set(JSON.parse(rowDemand) as string[]);
    const plan = liveRereadPlan(document.rows.filter(row => demanded.has(row.id)), liveMemory.current);
    if (!plan) {clearLiveTimer(); liveMemory.current = {stalls: 0}; return;}
    if (liveTimer.current?.expiry === plan.expiry) return;
    clearLiveTimer();
    liveMemory.current = {expiry: plan.expiry, stalls: plan.stalls};
    const timer = setTimeout(() => {
      if (liveTimer.current?.timer === timer) liveTimer.current = undefined;
      if (current()) {noteHomeRead('live-expiry'); void runtime.home.revalidate().catch(runtime.reportError);}
    }, plan.delayMs);
    liveTimer.current = {expiry: plan.expiry, timer};
  }, [active, clearLiveTimer, current, document, foreground, rowDemand, runtime]);
  // Saved views on Home past the first few arrive deferred: the demanded rows (mounted, plus the drawn pending ones) are
  // read by client-core's HomeService, one read per row, stopped when a row stops being demanded.
  useEffect(() => {runtime.home.demand(active && foreground && current() ? JSON.parse(rowDemand) as string[] : []);}, [active, current, foreground, rowDemand, runtime]);
  const updateViewport = useCallback((y: number) => {
    if (startModel.actionless) return;
    setHeroVisible(y < heroHeight);
    if (scrolled.current !== (y > 0)) {scrolled.current = y > 0; onScrolledChange?.(scrolled.current);}
    const boxes = boxesRef.current, range = homeVisibleRange(boxes, y, viewportHeight - bottomInset);
    const visible = new Set(boxes.slice(range[0], range[1] + 1).map(box => box.id));
    setVisibleRange(previous => previous[0] === range[0] && previous[1] === range[1] ? previous : range);
    setVisibleRows(previous => previous.size === visible.size && [...visible].every(id => previous.has(id)) ? previous : visible);
  }, [bottomInset, heroHeight, onScrolledChange, startModel.actionless, viewportHeight]);
  useLayoutEffect(() => {scrollBoxes.value = rowBoxes; reportedRange.value = ''; updateViewport(vertical.offset.value);}, [rowBoxes, scrollBoxes, reportedRange, updateViewport, vertical.offset]);
  const onScroll = useAnimatedScrollHandler(event => {
    const y = event.contentOffset.y;
    vertical.offset.value = y;
    const range = homeVisibleRange(scrollBoxes.value, y, viewportHeight - bottomInset);
    const next = `${range[0]}:${range[1]}:${y < heroHeight}:${y > 0}`;
    if (reportedRange.value !== next) {reportedRange.value = next; runOnJS(updateViewport)(y);}
  });
  const focusedRow = useMemo(() => rows.find(row => row.id === heroFocus?.rowId), [heroFocus?.rowId, rows]);
  const focusedEntry = useMemo(() => focusedRow?.entries.find(entry => entry.id === heroFocus?.entryId), [focusedRow, heroFocus?.entryId]);
  const preview = useMemo(() => {
    void t;
    return focusedEntry ? {
      id: focusedEntry.id, title: homeCardText(focusedEntry, true, runtime.text).title, entry: focusedEntry,
      facts: homeTitleFactParts(focusedEntry, runtime.text), overview: focusedEntry.overview,
      target: focusedRow && homePlayTarget(focusedRow, focusedEntry),
    } satisfies HomeCandidate : undefined;
  }, [focusedEntry, focusedRow, runtime.text, t]);
  const candidates: readonly HomeCandidate[] = useMemo(() => document ? homeSpotlightModels(document).map(model => ({
    id: model.entry.id, title: model.entry.displayTitle || model.entry.title, entry: model.entry, overview: model.overview,
    reason: serverTextLabel(labels, model.reason.text), facts: homeTitleFactParts(model.entry, runtime.text),
    target: model.primary?.entry, primaryLabel: model.primary ? t(model.primary.label.key, model.primary.label.params) : undefined,
  })) : [], [document, runtime.text, labels, t]);
  useEffect(() => {
    const request = () => {
      const start = resolveStart();
      return initialEntry.request({active: heroActive && foreground && !railExpanded && !recovery.visible && current() && start.kind === 'ready',
        primary: start.address?.regionId ?? ids.spotlight});
    };
    if (request() !== 'waiting') return;
    let live = true, pending = false;
    const off = registry.subscribe(() => {
      if (pending) return;
      pending = true;
      // Registration may run during a layout commit; land only after it settles.
      queueMicrotask(() => {pending = false; if (live && request() !== 'waiting') off();});
    });
    return () => {live = false; off();};
  }, [initialEntry, heroActive, foreground, railExpanded, recovery.visible, current, ids.spotlight, registry, resolveStart]);


  const contentHeight = useRef<number>(viewportHeight);
  const anchorRow = useCallback((box: {y: number; height: number}) => vertical.anchor(box.y + (rowsTopRef.current ?? heroBlock), box.height, viewportHeight, contentHeight.current, spec.preview, spec.bottomClear), [heroBlock, spec.bottomClear, spec.preview, vertical, viewportHeight]);
  const anchorRowRef = useRef(anchorRow);
  anchorRowRef.current = anchorRow;
  const currentRef = useRef(current);
  currentRef.current = current;
  const onRowFocus = useCallback((row: HomeRow, entryId: string) => {
    initialEntry.finish();
    const alreadyFocused = focusTarget.current?.rowId === row.id && focusTarget.current?.entryId === entryId;
    setFocus({rowId: row.id, entryId}); setHeroFocused(false);
    if (alreadyFocused) return;
    const target = focusTarget.current;
    requestAnimationFrame(() => {
      if (!currentRef.current() || !canAnchor.current || focusTarget.current !== target) return;
      const box = rowPositions.current.get(row.id);
      if (box) anchorRowRef.current(box);
    });
  }, [initialEntry, setFocus]);
  const onRowFocusRef = useRef(onRowFocus);
  onRowFocusRef.current = onRowFocus;
  const rowHandlers = useRef(new Map<string, {
    row: HomeRow;
    onLayout: ViewProps['onLayout'];
    onItemFocus: (id: string) => void;
    onCardFocus: (item: HomeCardModel) => void;
    rangeDeps?: readonly [HomeRow, HomeRow | undefined, number, boolean, readonly string[], string | undefined, typeof current, NativeShellRuntime];
    onRange?: (first: number, last: number) => void;
    onVisibleKeys?: (keys: readonly string[]) => void;
    seeAllDeps?: readonly [HomeRow['seeAll'], string, typeof guarded, NativeShellRuntime];
    seeAll?: () => void;
  }>());
  const setRowsTop = (top: number) => {
    rowsTopRef.current = top;
    publishRowsTop(top);
    const target = focusTarget.current, box = target && rowPositions.current.get(target.rowId);
    if (box && canAnchor.current && current()) anchorRow(box);
  };
  const focusSpotlight = () => {initialEntry.finish(); setHeroFocused(true); moveToTop(0, true); setFocus(undefined);};
  const blurSpotlight = () => setHeroFocused(false);
  const actions = (candidate: HomeCandidate): {region: FocusRegionProps; buttons: readonly HomeHeroAction[]} => {
    const {entry, target} = candidate;
    const {offers, order} = homeSpotlightFocusActions(entry, target, {platform: 'tv', playbackAvailable: !!runtime.playback});
    const focusProps = {onFocus: focusSpotlight, onBlur: blurSpotlight};
    const buttons: HomeHeroAction[] = [];
    if (target?.playback && offers.playable) {
      const playback = target.playback;
      buttons.push({id: 'play', label: candidate.primaryLabel ?? t('next.home.play'), icon: 'i-play', variant: 'primary',
        onPress: () => guarded(() => runtime.actions.play(entry, playback)), onLongPress: () => guarded(() => runtime.actions.more(entry)), ...focusProps});
    }
    if (offers.watchlisted !== undefined) buttons.push({id: 'my-list', label: t('next.home.myList'), icon: offers.watchlisted ? 'i-check' : 'i-plus', variant: 'glass', ...focusProps,
      onPress: () => guarded(() => {void runtime.actions.myList(entry, !offers.watchlisted).then(() => {if (current()) {noteHomeRead('my-list'); return runtime.home.revalidate();}}).catch(runtime.reportError);})});
    if (offers.details) buttons.push({id: 'details', label: t('action.details'), variant: 'glass', onPress: () => guarded(() => runtime.actions.navigate(entry)), ...focusProps});
    return {region: {...regionMap.spotlight, entry: order[0], orientation: 'horizontal', order}, buttons};
  };
  function rowModel(index: number) {
    const model = rowModels[index]!, {row, presentation, title, subtitle, physicalIds} = model;
    const source = sourceById.get(row.id);
    const shown = active && foreground && (visibleRows.has(row.id) || !rowPositions.current.has(row.id) && index === 0);
    const retainedEntryId = registry.isRegionActive(ids.row(row.id)) && focus?.rowId === row.id ? focus.entryId : undefined;
    const retainedIds = retainedEntryId !== undefined ? [retainedEntryId] : undefined;
    const pending = pages.state(row.id), waitingItem = presentation.cards.at(-1);
    const waitingId = pending?.loading ? pending.waitingId : ids.page(row.id, row.nextCursor);
    const paging = waitingItem && !row.live && !row.collection && source ? {hasMore: !!row.nextCursor || !!pending?.loading,
      loading: !!pending?.loading || !!snapshot.paging?.has(row.id) || !shown, failed: !!pending?.failed && pending.cursor === row.nextCursor,
      waitingId, waitingItem, fulfilled: pending?.fulfilled, requestNext: () => {if (shown && current()) void pages.request(source, waitingId);}} : undefined;
    let handlers = rowHandlers.current.get(row.id);
    if (!handlers) {
      const rowId = row.id;
      const entry = {
        row,
        onItemFocus: (id: string) => onRowFocusRef.current(entry.row, id),
        onCardFocus: (item: HomeCardModel) => onRowFocusRef.current(entry.row, item.entry.id),
        onLayout: ((event: Parameters<NonNullable<ViewProps['onLayout']>>[0]) => {
          const relative = event.nativeEvent.layout;
          rowPositions.current.set(rowId, relative);
          const activeLayoutKey = rowLayoutKeyRef.current;
          setMeasuredRows(before => {
            const measured = before.layout === activeLayoutKey ? before.boxes.get(rowId) : undefined;
            if (measured?.y === relative.y && measured.height === relative.height) return before;
            const boxes = new Map(before.layout === activeLayoutKey ? before.boxes : []); boxes.set(rowId, relative);
            return {layout: activeLayoutKey, boxes};
          });
          if (canAnchor.current && focusTarget.current?.rowId === rowId && currentRef.current()) anchorRowRef.current(relative);
        }) as ViewProps['onLayout'],
      };
      handlers = entry;
      rowHandlers.current.set(row.id, handlers);
    } else {
      handlers.row = row;
    }
    const rangeDeps = [row, source, index, shown, physicalIds, retainedEntryId, current, runtime] as const;
    if (!handlers.rangeDeps || rangeDeps.some((value, at) => !Object.is(value, handlers!.rangeDeps![at]))) {
      handlers.rangeDeps = rangeDeps;
      handlers.onRange = (first: number, last: number) => {
        if (!source || !current()) return;
        const keys = ranges.current.get(row.id)?.keys;
        ranges.current.set(row.id, {first, last, keys}); runtime.setHomeWindow(source, index, first, last, shown, physicalIds, retainedIds, keys);
        if (shown && row.live && pageAtVisibleEnd(row, last, 10)) void runtime.home.more(row.id);
      };
      handlers.onVisibleKeys = (keys: readonly string[]) => {
        if (!source || !current()) return;
        const before = ranges.current.get(row.id) ?? {first: 0, last: -1};
        ranges.current.set(row.id, {...before, keys}); runtime.setHomeWindow(source, index, before.first, before.last, shown, physicalIds, retainedIds, keys);
      };
    }
    const seeAllDeps = [row.seeAll, title, guarded, runtime] as const;
    if (!handlers.seeAllDeps || seeAllDeps.some((value, at) => !Object.is(value, handlers!.seeAllDeps![at]))) {
      handlers.seeAllDeps = seeAllDeps;
      handlers.seeAll = row.seeAll ? () => guarded(() => runtime.actions.seeAll(row.seeAll!, title)) : undefined;
    }
    return {...model, title, subtitle, source, shown, physicalIds, retainedIds, range: ranges.current.get(row.id), paging, onRange: handlers.onRange!,
      onVisibleKeys: handlers.onVisibleKeys!, onLayout: handlers.onLayout, mounted: mountedRows.has(index), focus: regionMap.rows.get(row.id),
      onItemFocus: handlers.onItemFocus, onCardFocus: handlers.onCardFocus, seeAll: handlers.seeAll};
  }
  const retry = () => guarded(() => {void runtime.home.retry();});
  return {t, labels, ids, snapshot, cachedAtMount: cachedAtMount.current, document, recovery, foreground, visible: active && foreground,
    librariesUnavailable, loadingElapsed, heroHeight, heroActive, heroVisible, heroFocused: heroFocused && !railExpanded,
    preview, candidates, activeIndex, setActiveIndex, actions, showBackdrops, repeating: repeating || vertical.repeating, startModel,
    rows, rowModels, rowBoxes, rowModel, pendingRows, width: viewportWidth, mixesRegion, customizeRegion: regionMap.customize, holdCustomize, current,
    cardFocusId: homeCardFocusId, setRepeating, retry, customize: () => guarded(runtime.actions.customize),
    setRowsTop, scroll: {testID: 'native-home', ref: vertical.scrollRef, style: vertical.scrollStyle, onScroll, onScrollBeginDrag: vertical.stop,
      bottomInset, onContentHeight: (height: number) => {contentHeight.current = height;}}};
}

function sameTVRowOrder(a: HomeRow, b: HomeRow): boolean {
  if (a === b) return true;
  if (a.id !== b.id || a.artworkShape !== b.artworkShape || a.entries.length !== b.entries.length) return false;
  for (let index = 0; index < a.entries.length; index++) if (a.entries[index] !== b.entries[index]) return false;
  if (a.live === b.live) return true;
  if (!a.live || !b.live || a.live.channels.length !== b.live.channels.length) return false;
  for (let index = 0; index < a.live.channels.length; index++) if (a.live.channels[index] !== b.live.channels[index]) return false;
  return true;
}
