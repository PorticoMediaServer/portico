import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, useWindowDimensions, type ViewProps} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {runOnJS, useAnimatedScrollHandler, useSharedValue} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {HomeRow} from '../../../../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {homeRowPresentation, homeSpotlightModels, homeTitleFactParts} from '../../../../../../packages/client-core/src/presentation/home-spotlight';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import type {HeroCandidate} from '../../../kit/layout';
import {useFocusAnchor} from '../../../kit/layout/FocusAnchor';
import {homeRowMetrics, type HomeHeroAction} from '../../../kit/home';
import type {PageStateProps} from '../../../kit/frames';
import {skeletonTiming} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {noteHomeRead} from '../../../shell/home-read-log';
import {logBootMilestone} from '../../../shell/boot-timing';
import {phoneHeaderGeometry, sameAuthority, type ShellAuthority} from '../../../shell/model';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import {homeSpotlightFocusActions} from '../../../screens/home/home-start';
import {homeMountedRows, homeVisibleRange, type HomeRowBox} from '../../../screens/home/home-window';
import {liveRereadPlan, type LiveRereadMemory} from '../../../screens/home/live-expiry';
import {authorizedHome, homeRowHasContent, pageAtVisibleEnd, platformRows} from '../../../screens/home/model';

export type HomeCandidate = HeroCandidate & {entry: ContentEntry; target?: ContentEntry; primaryLabel?: string};
export type HomeProps = Readonly<{
  runtime: NativeShellRuntime; authority: ShellAuthority; active?: boolean; heroObscured?: boolean; bottomInset?: number;
  onScrolledChange?: (scrolled: boolean) => void;
}>;
type HomeCardModel = ReturnType<typeof homeRowPresentation>['cards'][number];

/**
 * Home on a phone (phone-app.html 1, 2), as data: the server-authored Home document and nothing else (no whole-library
 * reads, local ranking or metadata inference). It reads Home on mount, keeps the rows on screen demanded and windowed,
 * re-reads when a live row expires, refreshes on pull, and offers the spotlight's candidates and actions. A page already
 * read stays on screen only through a known transport failure (states 4b); any other failure is the full error state.
 */
export function useHome({runtime, authority, active = true, heroObscured = false, bottomInset = 0, onScrolledChange}: HomeProps) {
  const theme = useTheme(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets(), t = runtime.text.t;
  const labels = useMemo(() => ({t, has: defaultI18n.has}), [t]);
  const ids = useMemo(() => tvFocusIds(authority), [authority]);
  const cardFocusId = useCallback((item: HomeCardModel) => ids.entry(item.entry), [ids]);
  // Confirmed Appearance effects: the drawn card is 100 percent, and the picture is drawn unless the viewer turned backdrops off.
  const cardScale = (runtime.appearanceCardSizePercent ?? 100) / 100, showBackdrops = runtime.appearanceShowBackdrops !== false;
  const width = dimensions.width, height = dimensions.height;
  const snapshot = useSyncExternalStore(runtime.home.subscribe, runtime.home.getSnapshot);
  // P2-61: a library directory that did not answer is unknown, never "no libraries".
  const librariesUnavailable = useSyncExternalStore(runtime.subscribe ?? noSubscription, () => {try {return runtime.getSnapshot?.().librariesUnavailable === true;} catch {return false;}});
  const heroHeight = theme.spec.hero.homeHeight[theme.column];
  const heroActive = active && !heroObscured && !runtime.getSnapshot().selectionActive;
  const errorFacts = snapshot.error ? presentError(snapshot.error, 'home') : undefined;
  const transportFailure = errorFacts !== undefined && ['unreachable', 'offline', 'timeout', 'busy'].includes(errorFacts.category);
  const document = errorFacts === undefined || transportFailure ? authorizedHome(snapshot.document, authority) : undefined;
  const cachedAtMount = useRef(!!document);
  const [foreground, setForeground] = useState(AppState.currentState === 'active'), [loadingElapsed, setLoadingElapsed] = useState(0);
  const [heroVisible, setHeroVisible] = useState(true), [visibleRows, setVisibleRows] = useState<ReadonlySet<string>>(new Set());
  const [visibleRange, setVisibleRange] = useState<readonly [number, number]>([0, 0]);
  const [measured, setMeasured] = useState<Readonly<{layout: string; boxes: ReadonlyMap<string, {y: number; height: number}>}>>({layout: '', boxes: new Map()});
  // Where the frame starts the rows in the scrolled content (a row measures itself from there).
  const [rowsTop, setRowsTop] = useState<number>();
  // eslint-disable-next-line react-hooks/exhaustive-deps -- Shelf offsets belong to this authority.
  const shelfOffsets = useMemo(() => new Map<string, {offset: number}>(), [authority]);
  const [activeIndex, setActiveIndex] = useState(0), [refreshing, setRefreshing] = useState(false);
  const scrolled = useRef(false), rowPositions = useRef(new Map<string, {y: number; height: number}>());
  const ranges = useRef(new Map<string, {first: number; last: number; keys?: readonly string[]}>());
  const vertical = useFocusAnchor(false, false, 'start'), moveToTop = vertical.move;
  useEffect(() => runtime.registerScrollToTop({kind: 'home'}, () => moveToTop(0, true)), [runtime, moveToTop]);
  const current = useCallback(() => {
    try {return sameAuthority(authority, runtime.getSnapshot().authority);}
    catch (error) {if (error instanceof Error && error.name === 'AbortError') return false; throw error;}
  }, [authority, runtime]);
  const readLibraries = new Set(runtime.getSnapshot().libraries.filter(library => library.kind === 'books').map(library => library.id));
  const rows = document ? platformRows(document, 'phone', readLibraries).filter(homeRowHasContent) : [];
  // The server publishes every row as a deferred descriptor with its real title and shape; a row whose preview has not
  // arrived yet is drawn as a bounded skeleton (no card, count or focus target).
  const pendingRows = document ? platformRows(document, 'phone', readLibraries).filter(row => row.deferred === true && !homeRowHasContent(row)).slice(0, 2) : [];
  const layoutKey = `${width}:${height}:${dimensions.fontScale}:${theme.textScale}:${cardScale}`;
  const models = rows.map(row => {
    const presentation = homeRowPresentation(row);
    const metrics = homeRowMetrics(theme, row, {ranked: presentation.cards.some(card => card.rank !== undefined), cardScale});
    return {row, presentation, ...metrics, slotHeight: measured.layout === layoutKey ? measured.boxes.get(row.id)?.height ?? metrics.estimate : metrics.estimate};
  });
  const noHero = !document?.spotlight.length;
  const heroBlock = noHero ? phoneHeaderGeometry(insets.top, false).height : heroHeight;
  let rowY = rowsTop ?? heroBlock;
  const boxes: readonly HomeRowBox[] = models.map(({row, slotHeight, gap}) => {
    rowY += gap;
    const box = measured.layout === layoutKey ? measured.boxes.get(row.id) : undefined;
    const placed = {id: row.id, y: box ? box.y + (rowsTop ?? 0) : rowY, height: slotHeight};
    rowY = placed.y + slotHeight;
    return placed;
  });
  const boxesRef = useRef(boxes), scrollBoxes = useSharedValue<readonly HomeRowBox[]>([]), reportedRange = useSharedValue('');
  boxesRef.current = boxes;
  const mounted = homeMountedRows(rows.length, visibleRange[0], visibleRange[1], []);
  // F04: only rows that are mounted (visible or overscan), and the drawn skeleton rows, ask the server for their shelf.
  const rowDemand = JSON.stringify([...rows.filter((_row, index) => mounted.has(index)).map(row => row.id), ...pendingRows.map(row => row.id)]);
  const mixes = useSyncExternalStore(runtime.mixes?.subscribe ?? noSubscription, runtime.mixes?.getSnapshot ?? noSnapshot);

  // Cold-launch timing (P2-74 4b): the first commit with Home's document on screen, once per process.
  useEffect(() => {if (document && current()) logBootMilestone('home-painted', {rows: document.rows.length});}, [document, current]);
  // The restore has already read Home: a mount reads only when nothing has been read yet.
  useEffect(() => {if (current() && !runtime.home.getSnapshot().document) {noteHomeRead('mount'); void runtime.home.load();}}, [current, runtime]);
  useEffect(() => {const sub = AppState.addEventListener('change', state => setForeground(state === 'active')); return () => sub.remove();}, []);
  useEffect(() => {
    if (snapshot.phase !== 'loading' && snapshot.phase !== 'idle') return;
    // Only the 300 ms and slow thresholds matter: two timers, not a re-render clock.
    setLoadingElapsed(0);
    const timers = [skeletonTiming.delayMs, skeletonTiming.slowMs].map(at => setTimeout(() => setLoadingElapsed(at), at));
    return () => timers.forEach(clearTimeout);
  }, [snapshot.phase]);
  // E-mobile-29: one timer per live expiry among the demanded rows; only a re-read bringing back the same or an expired
  // expiry backs off.
  const liveTimer = useRef<{expiry: string; timer: ReturnType<typeof setTimeout>} | undefined>(undefined), liveMemory = useRef<LiveRereadMemory>({stalls: 0});
  const clearLiveTimer = useCallback(() => {if (liveTimer.current) {clearTimeout(liveTimer.current.timer); liveTimer.current = undefined;}}, []);
  useEffect(() => clearLiveTimer, [clearLiveTimer]);
  useEffect(() => {
    if (!active || !foreground || !document) {clearLiveTimer(); return;}
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
  useEffect(() => {runtime.home.demand(active && foreground && current() ? JSON.parse(rowDemand) as string[] : []);}, [active, current, foreground, rowDemand, runtime]);
  const updateViewport = useCallback((y: number) => {
    setHeroVisible(y < heroHeight);
    if (scrolled.current !== (y > 0)) {scrolled.current = y > 0; onScrolledChange?.(scrolled.current);}
    const all = boxesRef.current, range = homeVisibleRange(all, y, height - bottomInset);
    const visible = new Set(all.slice(range[0], range[1] + 1).map(box => box.id));
    setVisibleRange(previous => previous[0] === range[0] && previous[1] === range[1] ? previous : range);
    setVisibleRows(previous => previous.size === visible.size && [...visible].every(id => previous.has(id)) ? previous : visible);
  }, [bottomInset, heroHeight, onScrolledChange, height]);
  useLayoutEffect(() => {scrollBoxes.value = boxes; reportedRange.value = ''; updateViewport(vertical.offset.value);}, [boxes, scrollBoxes, reportedRange, updateViewport, vertical.offset]);
  const onScroll = useAnimatedScrollHandler(event => {
    const y = event.contentOffset.y;
    vertical.offset.value = y;
    const range = homeVisibleRange(scrollBoxes.value, y, height - bottomInset);
    const next = `${range[0]}:${range[1]}:${y < heroHeight}:${y > 0}`;
    if (reportedRange.value !== next) {reportedRange.value = next; runOnJS(updateViewport)(y);}
  });
  const candidates: readonly HomeCandidate[] = useMemo(() => document ? homeSpotlightModels(document).map(model => ({
    id: model.entry.id, title: model.entry.displayTitle || model.entry.title, entry: model.entry, overview: model.overview,
    reason: serverTextLabel(labels, model.reason.text), facts: homeTitleFactParts(model.entry, runtime.text),
    target: model.primary?.entry, primaryLabel: model.primary ? t(model.primary.label.key, model.primary.label.params) : undefined,
  })) : [], [document, runtime.text, labels, t]);
  const guarded = (run: () => void) => {if (current()) run();};

  return {
    theme, t, labels, ids, cardFocusId, width, current, document, rows, pendingRows, librariesUnavailable, loadingElapsed, cachedAtMount: cachedAtMount.current,
    visible: active && foreground, showBackdrops, heroHeight, heroBlock, noHero, heroActive, heroVisible: active && foreground && heroVisible,
    candidates, activeIndex, setActiveIndex, mixesReady: mixes?.phase === 'ready' && mixes.rows.length > 0,
    error: snapshot.error,
    /** Home's failure as the frame's page state: runtime's words for this error with Try again. */
    failure: (): PageStateProps | undefined => {
      if (!snapshot.error) return undefined;
      const state = runtime.error(snapshot.error, () => guarded(() => {void runtime.home.retry();}), 'home');
      return {icon: state.icon ?? 'i-alert', tone: state.tone, title: typeof state.title === 'string' ? state.title : '', body: typeof state.body === 'string' ? state.body : undefined,
        primaryAction: state.primaryAction, secondaryAction: state.secondaryAction, help: state.help, testID: state.testID};
    },
    /** The spotlight's buttons on a phone: Play (its server-authored label) when the title can play here, and My List
     * (added or not), which reads Home again once the list changed. */
    heroActions: (candidate: HomeCandidate): readonly HomeHeroAction[] => {
      const {offers} = homeSpotlightFocusActions(candidate.entry, candidate.target, {platform: 'phone-app', playbackAvailable: !!runtime.playback});
      const playback = candidate.target?.playback;
      return [
        ...(offers.playable && playback ? [{id: 'play', label: candidate.primaryLabel ?? t('next.home.play'), icon: 'i-play', variant: 'primary',
          onPress: () => guarded(() => runtime.actions.play(candidate.entry, playback))} as const] : []),
        ...(offers.watchlisted !== undefined ? [{id: 'my-list', label: t('next.home.myList'), icon: offers.watchlisted ? 'i-check' : 'i-plus', variant: 'glass',
          onPress: () => {
            if (current()) void runtime.actions.myList(candidate.entry, !offers.watchlisted).then(() => {if (current()) {noteHomeRead('my-list'); return runtime.home.revalidate();}}).catch(runtime.reportError);
          }} as const] : []),
      ];
    },
    liveCardWidth: theme.spec.card.live.width.phone,
    customize: () => guarded(runtime.actions.customize),
    seeAll: (row: HomeRow, title: string) => row.seeAll ? () => guarded(() => runtime.actions.seeAll(row.seeAll!, title)) : undefined,
    rowTitle: (row: HomeRow) => serverTextLabel(labels, homeRowPresentation(row).title),
    rowSubtitle: (row: HomeRow) => serverTextLabel(labels, homeRowPresentation(row).subtitle),
    /** One drawn row: whether it is mounted and on screen, its card sizes, the frame's measured box and its window. */
    row: (index: number) => {
      const model = models[index]!, {row} = model, source = document?.rows.find(value => value.id === row.id);
      const shown = active && foreground && (visibleRows.has(row.id) || !rowPositions.current.has(row.id) && index === 0);
      let scrollMemory = shelfOffsets.get(row.id);
      if (!scrollMemory) {scrollMemory = {offset: 0}; shelfOffsets.set(row.id, scrollMemory);}
      const onRange = (first: number, last: number) => {
        if (!current() || !source) return;
        const keys = ranges.current.get(row.id)?.keys; ranges.current.set(row.id, {first, last, keys});
        runtime.setHomeWindow(source, index, first, last, shown, undefined, undefined, keys);
        if (shown && pageAtVisibleEnd(row, last, 3)) void runtime.home.more(row.id);
      };
      const onVisibleKeys = (keys: readonly string[]) => {
        if (!current() || !source) return;
        const before = ranges.current.get(row.id) ?? {first: 0, last: -1};
        ranges.current.set(row.id, {...before, keys}); runtime.setHomeWindow(source, index, before.first, before.last, shown, undefined, undefined, keys);
      };
      const onLayout: ViewProps['onLayout'] = event => {
        const box = event.nativeEvent.layout;
        rowPositions.current.set(row.id, box);
        setMeasured(before => {
          const at = before.layout === layoutKey ? before.boxes.get(row.id) : undefined;
          if (at?.y === box.y && at.height === box.height) return before;
          const next = new Map(before.layout === layoutKey ? before.boxes : []); next.set(row.id, {y: box.y, height: box.height});
          return {layout: layoutKey, boxes: next};
        });
      };
      return {...model, source, mounted: mounted.has(index), shown, scrollMemory, onRange, onVisibleKeys, onLayout};
    },
    setRowsTop,
    /** The frame's scroller: the row window's scroll handler and offset, pull to refresh, and room for the tab bar. */
    scroll: {
      testID: 'native-home', ref: vertical.scrollRef, style: vertical.scrollStyle, onScroll, onScrollBeginDrag: vertical.stop, bottomInset,
      refresh: {refreshing, onRefresh: () => {
        if (!current() || refreshing) return;
        setRefreshing(true);
        noteHomeRead('pull'); void runtime.home.revalidate().catch(runtime.reportError).finally(() => {if (current()) setRefreshing(false);});
      }},
    },
  };
}

const noSubscription = () => () => {};
const noSnapshot = () => undefined;
