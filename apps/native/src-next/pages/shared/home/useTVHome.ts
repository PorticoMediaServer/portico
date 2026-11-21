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
  const ownershipSnapshot = useCallback(() => JSON.stringify(ownershipIds.filter(id => registry.isRegionActive(id))), [ownershipIds, registry]);
  // Home needs ownership transitions for row ordering. A child-to-child move is
  // already delivered by the focused card's preview callback, not every registry revision.
  useSyncExternalStore(registry.subscribe, ownershipSnapshot, ownershipSnapshot);
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
  // TV: a focused row's top sits under the 588 preview hero, whichever way focus came. A row (492) never fits between that
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
  const guarded = (run: () => void) => {if (current()) run();};
  const readLibraries = new Set(runtime.getSnapshot().libraries.filter(library => library.kind === 'books').map(library => library.id));
  const incomingRows = document ? platformRows(document, 'tv', readLibraries).filter(homeRowHasContent).map(row => pages.present(row)) : [];
  // The server publishes every Home row as a deferred descriptor with its real title and
  // shape, then the row previews hydrate one at a time. Draw only the descriptors whose
  // preview has not arrived yet as bounded skeletons; no card, count or focus target.
  const pendingRows = document ? platformRows(document, 'tv', readLibraries).filter(row => row.deferred === true && !homeRowHasContent(row)).slice(0, 3) : [];
  const previousRows = useRef<readonly HomeRow[]>([]);
  const occupied = (registry.isRegionActive(ids.spotlight) || previousRows.current.some(row => registry.isRegionActive(ids.row(row.id))));
  const rows = tvHomeRows(previousRows.current, incomingRows, rowId => registry.isRegionActive(ids.row(rowId)), occupied);
  previousRows.current = rows;
  const rowLayoutKey = `${theme.textScale}:${cardScale}`;
  const rowModels = rows.map(row => {
    const presentation = homeRowPresentation(row);
    const metrics = homeRowMetrics(theme, row, {ranked: presentation.cards.some(card => card.rank !== undefined), cardScale});
    return {row, presentation, ...metrics, slotHeight: measuredRows.layout === rowLayoutKey ? measuredRows.boxes.get(row.id)?.height ?? metrics.estimate : metrics.estimate};
  });
  // Owner, 18 Nov: no rotating spotlight. Until a card reports focus (at launch, and on coming back to Home) the hero shows
  // the start card, the first card of the first row of titles, so the spotlight never flashes up before the focused card.
  const focusShown = focus && rows.some(row => row.id === focus.rowId && row.entries.some(entry => entry.id === focus.entryId)) ? focus : undefined;
  const startRow = rows.find(row => !row.live && !row.collection && row.entries.length > 0);
  const heroFocus = focusShown ?? (startRow && {rowId: startRow.id, entryId: startRow.entries[0]!.id});
  const heroBlock = heroFocus ? spec.preview : spec.spotlight;
  let rowY = rowsTop ?? heroBlock;
  const rowBoxes: readonly HomeRowBox[] = rowModels.map(({row, slotHeight}) => {
    const measured = measuredRows.layout === rowLayoutKey ? measuredRows.boxes.get(row.id) : undefined;
    const box = {id: row.id, y: measured && rowsTop !== undefined ? measured.y + rowsTop : rowY, height: slotHeight};
    rowY = box.y + slotHeight + frameSpec.rhythm.rowSection.tv;
    return box;
  });
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
  const regionMap = tvHomeRegionMap(authority, [...rows.map(row => row.id), ...(hasMixes ? [mixesFocusRow] : [])], {spotlightFocusable: false});
  const mixesRegion = regionMap.rows.get(mixesFocusRow);
  const startModel = homeStartModel(document, authority, {rows: rows.map(row => row.id), readLibraryIds: readLibraries, mixesReady: hasMixes, playbackAvailable: !!runtime.playback, spotlight: false});
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
  const focusedRow = rows.find(row => row.id === heroFocus?.rowId), focusedEntry = focusedRow?.entries.find(entry => entry.id === heroFocus?.entryId);
  const preview = focusedEntry ? {
    id: focusedEntry.id, title: homeCardText(focusedEntry, true, runtime.text).title, entry: focusedEntry,
    facts: homeTitleFactParts(focusedEntry, runtime.text), overview: focusedEntry.overview,
    target: focusedRow && homePlayTarget(focusedRow, focusedEntry),
  } satisfies HomeCandidate : undefined;
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
  const anchorRow = (box: {y: number; height: number}) => vertical.anchor(box.y + (rowsTopRef.current ?? heroBlock), box.height, viewportHeight, contentHeight.current, spec.preview, spec.bottomClear);
  const onRowFocus = (row: HomeRow, entryId: string) => {
    initialEntry.finish();
    setFocus({rowId: row.id, entryId}); setHeroFocused(false);
    if (focus?.rowId === row.id && focus.entryId === entryId) return;
    const target = focusTarget.current;
    requestAnimationFrame(() => {
      if (!current() || !canAnchor.current || focusTarget.current !== target) return;
      const box = rowPositions.current.get(row.id);
      if (box) anchorRow(box);
    });
  };
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
    const model = rowModels[index]!, {row, presentation} = model;
    const source = document?.rows.find(value => value.id === row.id);
    const shown = active && foreground && (visibleRows.has(row.id) || !rowPositions.current.has(row.id) && index === 0);
    const physicalIds = row.live ? row.live.channels.map(item => JSON.stringify([item.channel.sourceId, item.channel.id])) : row.entries.map(entry => entry.id);
    const retainedIds = registry.isRegionActive(ids.row(row.id)) && focus?.rowId === row.id ? [focus.entryId] : undefined;
    const pending = pages.state(row.id), waitingItem = presentation.cards.at(-1);
    const waitingId = pending?.loading ? pending.waitingId : ids.page(row.id, row.nextCursor);
    const paging = waitingItem && !row.live && !row.collection && source ? {hasMore: !!row.nextCursor || !!pending?.loading,
      loading: !!pending?.loading || !!snapshot.paging?.has(row.id) || !shown, failed: !!pending?.failed && pending.cursor === row.nextCursor,
      waitingId, waitingItem, fulfilled: pending?.fulfilled, requestNext: () => {if (shown && current()) void pages.request(source, waitingId);}} : undefined;
    const title = serverTextLabel(labels, presentation.title), subtitle = serverTextLabel(labels, presentation.subtitle) || undefined;
    const onRange = (first: number, last: number) => {
      if (!source || !current()) return;
      const keys = ranges.current.get(row.id)?.keys;
      ranges.current.set(row.id, {first, last, keys}); runtime.setHomeWindow(source, index, first, last, shown, physicalIds, retainedIds, keys);
      if (shown && row.live && pageAtVisibleEnd(row, last, 10)) void runtime.home.more(row.id);
    };
    const onVisibleKeys = (keys: readonly string[]) => {
      if (!source || !current()) return;
      const before = ranges.current.get(row.id) ?? {first: 0, last: -1};
      ranges.current.set(row.id, {...before, keys}); runtime.setHomeWindow(source, index, before.first, before.last, shown, physicalIds, retainedIds, keys);
    };
    const onLayout: ViewProps['onLayout'] = event => {
      const relative = event.nativeEvent.layout;
      rowPositions.current.set(row.id, relative);
      setMeasuredRows(before => {
        const measured = before.layout === rowLayoutKey ? before.boxes.get(row.id) : undefined;
        if (measured?.y === relative.y && measured.height === relative.height) return before;
        const boxes = new Map(before.layout === rowLayoutKey ? before.boxes : []); boxes.set(row.id, relative);
        return {layout: rowLayoutKey, boxes};
      });
      if (canAnchor.current && focus?.rowId === row.id && focusTarget.current?.rowId === row.id && current()) anchorRow(relative);
    };
    return {...model, title, subtitle, source, shown, physicalIds, retainedIds, range: ranges.current.get(row.id), paging, onRange,
      onVisibleKeys, onLayout, mounted: mountedRows.has(index), focus: regionMap.rows.get(row.id), onItemFocus: (id: string) => onRowFocus(row, id),
      seeAll: row.seeAll ? () => guarded(() => runtime.actions.seeAll(row.seeAll!, title)) : undefined};
  }
  const retry = () => guarded(() => {void runtime.home.retry();});
  return {t, labels, ids, snapshot, cachedAtMount: cachedAtMount.current, document, recovery, foreground, visible: active && foreground,
    librariesUnavailable, loadingElapsed, heroHeight, heroActive, heroVisible, heroFocused: heroFocused && !railExpanded,
    preview, candidates, activeIndex, setActiveIndex, actions, showBackdrops, repeating: repeating || vertical.repeating, startModel,
    rows, rowModel, pendingRows, width: viewportWidth, mixesRegion, customizeRegion: regionMap.customize, holdCustomize, current,
    cardFocusId: homeCardFocusId, setRepeating, retry, customize: () => guarded(runtime.actions.customize),
    setRowsTop, scroll: {testID: 'native-home', ref: vertical.scrollRef, style: vertical.scrollStyle, onScroll, onScrollBeginDrag: vertical.stop,
      bottomInset, onContentHeight: (height: number) => {contentHeight.current = height;}}};
}
