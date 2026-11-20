import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {noteHomeRead} from '../../shell/home-read-log';
import {liveRereadPlan, type LiveRereadMemory} from './live-expiry';
import {logBootMilestone} from '../../shell/boot-timing';
import {AppState, RefreshControl, ScrollView, Text, View, useWindowDimensions, type ViewProps, type ViewStyle} from 'react-native';
import {BrowseFrame} from '../../kit/frames';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Animated, {runOnJS, useAnimatedScrollHandler, useSharedValue} from 'react-native-reanimated';
import {heroSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {HomeRow} from '../../../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {homeRowPresentation, homeSpotlightModels, homeTitleFactParts} from '../../../../../packages/client-core/src/presentation/home-spotlight';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {serverTextLabel} from '../../../../../packages/client-core/src/presentation/server-text';
import {usePlatformPosture} from '../../platform/platformPosture';
import {AmbientArt, Artwork} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {FocusItem, useFocusRegistry} from '../../kit/focus';
import {authorityKey} from '../../shell/model';
import {PlayOnButton} from '../../shell/PlayOnButton';
import {roundBarSize} from '../../kit/layout/TopBar';
import {tvFocusIds} from '../../shell/tv-focus';
import {homeSpotlightFocusActions} from './home-start';
import {wideDetailHeight} from '../../kit/cards';
import {cardTitleLine} from '../../kit/cards/caption-metrics';
import {Hero, Row, type HeroCandidate} from '../../kit/layout';
import {useFocusAnchor} from '../../kit/layout/FocusAnchor';
import type {ArtworkRenderContext} from '../../kit/layout/types';
import {LoadingPaint, StateView, HomeSkeleton, SkeletonBlock, skeletonTiming} from '../../kit/states';
import {Banner} from '../../kit/overlays';
import {nativeType, useTheme, type Theme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {MusicMixesRow} from '../destinations/music/MusicMixesRow';
import {phoneHeaderGeometry, sameAuthority, shellLayout, type ShellAuthority, type ShellLayout} from '../../shell/model';
import {authorizedHome, homeRowHasContent, pageAtVisibleEnd, platformRows} from './model';
import {homeMountedRows, homeVisibleRange, type HomeRowBox} from './home-window';
import {HomeCard, HomeCollectionCard, HomeHeroFacts, HomeLiveRow, HomePendingRow, HomeWindow, booksCardSize} from '../../kit/home';
import {HomePage} from '../../pages/touch/home/HomePage';
import {TVHomePage} from '../../pages/tv/home/HomePage';

type HomeCandidate = HeroCandidate & {entry: ContentEntry; target?: ContentEntry; primaryLabel?: string};
const noMixesSubscription = () => () => {};
type HomeCardModel = ReturnType<typeof homeRowPresentation>['cards'][number];
const homeCardKey = (item: HomeCardModel) => item.entry.id;

/**
 * Phone Home measurements the drawn frame states outside the shared kit:
 * phone-app.html `.phone .row{margin-top:28px}` and `.phone .row-head .sub{font-size:13px}`
 * (kit.css's shared row-subtitle role is 14). TV and desktop keep their shipped values.
 */
export function homeRowSpacing(theme: Theme) {
  return theme.isTV ? {marginBottom: 30} : theme.platform === 'tablet' ? {marginTop: 26} : theme.layoutMode === 'phone' && theme.platform === 'phone' ? {marginTop: 28} : {marginBottom: 18};
}

/** The hero reserves only its drawn height; the first row's 28 px gap comes from its own margin. */
export function homeHeroBlock(theme: Theme, heroHeight: number, preview: boolean): number {
  return theme.isTV ? (preview ? 588 : 820) : theme.platform === 'tablet' ? heroHeight : theme.layoutMode === 'phone' && theme.platform === 'phone' ? heroHeight : theme.layoutMode === 'desktop' ? heroHeight : 588;
}

/** With no spotlight Home draws no hero: rows start under the phone header where the shell draws one (its own
 * `shellLayout` kind, so a sideways phone agrees with the header), or under the safe area. */
const tabletPlayOnTop = (topInset: number) => Math.max(topInset, 24) + 20;
export function homeTopWithoutHero(layout: ShellLayout['kind'], topInset: number): number {
  return layout === 'phone' ? phoneHeaderGeometry(topInset, false).height : Math.max(topInset, 20);
}

/**
 * The medium window draws two hero compositions in foldable.html: upright the hero is
 * half the window (440 of 880), and sideways it is the 392 px frame 3f authors. The
 * 392 is drawn, never derived.
 */
export const MEDIUM_SIDEWAYS_HERO = 392;
export const mediumHeroHeight = (width: number, height: number): number => width > height ? MEDIUM_SIDEWAYS_HERO : Math.round(height / 2);
/** foldable.html `.md .ph-hero.sbs .copy`: inset 28, sideways width 320 and upright 300, bottom 30. */
export const mediumCopySide = (width: number, height: number) => ({inset: 28, width: width > height ? 320 : 300, bottom: 30});
/** foldable.html frame 7 draws `--bw:60%` upright and frame 3f the default 66% sideways. */
export const mediumBackdropFraction = (width: number, height: number): number => width > height ? 0.66 : 0.6;
/** foldable.html draws the sideways lockup at 50 px and frame 7 the upright one at 42 px. */
export const mediumLockupSize = (width: number, height: number): number => width > height ? 50 : 42;
/**
 * The drawn size keeps the handheld lockup's own line box and tracking ratios, so the
 * three values stay one coherent column rather than a bare number.
 */
export function mediumLockupStyle(theme: Theme, width: number, height: number) {
  const role = theme.type.lockup, size = mediumLockupSize(width, height), roleSize = role.fontSize ?? size, ratio = size / (roleSize / theme.textScale);
  return {fontSize: roleSize * ratio, lineHeight: (role.lineHeight ?? roleSize) * ratio, letterSpacing: (role.letterSpacing ?? 0) * ratio, marginBottom: 12};
}
/** foldable.html `.phone.md .card{width:140px}` and `.card.wide{width:292px}`;
 * tablet.html#2 draws the 820 pt upright tablet at 152/296. */
export const mediumCardWidth = (wide: boolean, tablet = false): number => wide ? tablet ? 296 : 292 : tablet ? 152 : 140;
/** tablet.html#2 draws a 28 pt gutter upright; foldable.html#7 keeps 24. */
export const mediumUprightTablet = (width: number, height: number): boolean => width < height && width >= 768;
export const mediumHomeGutter = (width: number, height: number): number => mediumUprightTablet(width, height) ? 28 : 24;

/** foldable.html `.phone.md .card .t{font-size:14px}` and `.card .s{font-size:12.5px}`, 9 under the art; the tablet's
 * Home rebuild models these as type roles. */
export const mediumCardType = {title: {fontSize: 14}, caption: {fontSize: 12.5}, titleMargin: 9} as const;
/** foldable.html `.phone.md .row-head h2{font-size:21px}`; the row heading role carries 19. */
export const mediumHeadingStyle = {fontSize: 21} as const;
/** tablet.html 2 and the desktop window draw the hero's facts at their own size over 1.4. */
const heroFactsSize = (size: number, theme: Theme) => ({text: {fontSize: size * theme.textScale, lineHeight: size * 1.4 * theme.textScale}, columnGap: 14, rowGap: 6});

/** kit.css draws the phone scroll 96 px deep over an 84 px bar; other platforms keep their padding. */
export function homeContentBottom(theme: Theme, bottomInset: number): number {
  return bottomInset + (theme.isTV ? 96 : theme.platform === 'tablet' ? 40 : theme.layoutMode === 'phone' && theme.platform === 'phone' ? 12 : 24);
}

type HomeScreenProps = Parameters<typeof HomeScreenForms>[0] & Pick<React.ComponentProps<typeof TVHomePage>, 'railExpanded' | 'holdRailDim' | 'releaseRailDim'>;

/** Home uses shared pages on TV and touch. Rotated phones and folds keep their own layout. */
export function HomeScreen(props: HomeScreenProps) {
  const theme = useTheme(), {runtime, authority, active} = props, posture = usePlatformPosture();
  if (theme.isTV) return <TVHomePage {...props}/>;
  const folded = posture?.status === 'available' && posture.folds.some(fold => fold.orientation === 'vertical');
  if (folded || (theme.form ?? 'phone') === 'phone' && (theme.layoutMode !== 'phone' || theme.platform !== 'phone'))
    return <HomeScreenForms {...props}/>;
  const mixes = runtime.mixes ? <MusicMixesRow focusScope={`home:${authorityKey(authority)}.mixes`} port={runtime.mixes} artwork={runtime.artwork} reportError={runtime.reportError}
    title={runtime.text.t('music.mixesForYou')} onOpen={runtime.musicDiscover} active={active ?? true}/> : undefined;
  return <HomePage runtime={runtime} authority={authority} active={props.active} heroObscured={props.heroObscured} bottomInset={props.bottomInset} onScrolledChange={props.onScrolledChange} mixes={mixes}
    toolbar={theme.form !== 'phone' && !props.playOnHidden ? <PlayOnButton playOn={runtime.playOn} variant="round"/> : undefined}/>;
}

/** Server-authored Home only. No whole-library reads, local ranking or metadata inference. */
function HomeScreenForms({runtime, authority, playOnHidden = false, active = true, heroObscured = false, bottomInset = 0, onScrolledChange}: {
  runtime: NativeShellRuntime; authority: ShellAuthority;
  /** The shell hides Play on while Home is selecting or its chrome is hidden. */
  playOnHidden?: boolean; active?: boolean; heroObscured?: boolean; bottomInset?: number; onScrolledChange?: (scrolled: boolean) => void;
}) {
  const registry = useFocusRegistry();
  const ids = useMemo(() => tvFocusIds(authority), [authority]);
  const homeCardFocusId = useCallback((item: HomeCardModel) => ids.entry(item.entry), [ids]);
  const theme = useTheme(), dimensions = useWindowDimensions(), t = runtime.text.t, insets = useSafeAreaInsets();
  const labels = useMemo(() => ({t, has: defaultI18n.has}), [t]);
  const desktop = theme.layoutMode === 'desktop', medium = theme.platform === 'tablet';
  const expandedTouch = desktop;
  // Confirmed Appearance effects: the drawn card is 100 percent, and the picture is drawn unless the viewer turned backdrops off.
  const cardScale = (runtime.appearanceCardSizePercent ?? 100) / 100, showBackdrops = runtime.appearanceShowBackdrops !== false;
  const windowWidth = dimensions.width, windowHeight = dimensions.height;
  const snapshot = useSyncExternalStore(runtime.home.subscribe, runtime.home.getSnapshot);
  // P2-61 HOME-RESILIENCE: a library directory that did not answer is unknown, never "no libraries".
  const librariesUnavailable = useSyncExternalStore(runtime.subscribe ?? noMixesSubscription, () => {try {return runtime.getSnapshot?.().librariesUnavailable === true;} catch {return false;}});
  const librariesNotice = librariesUnavailable ? <View testID="home-libraries-unavailable" style={{paddingHorizontal: theme.gutter, paddingTop: 16, paddingBottom: 4}}>
    <Banner placement="inline" variant="notice" tone="warning" icon="i-alert" message={t('next.home.librariesUnavailable')}
      action={runtime.retryLibraries ? {label: t('next.states.tryAgain'), onPress: () => runtime.retryLibraries?.()} : undefined}/></View> : null;
  // foldable7: on a real vertical crease the medium hero keeps its words on the left
  // page and its picture on the right, so the title never sits across the fold.
  const posture = usePlatformPosture();
  const crease = medium && posture.status === 'available' && posture.window?.width === windowWidth && posture.window.height === windowHeight
    ? posture.folds.find(value => value.orientation === 'vertical' && value.bounds.left > 0 && value.bounds.right > value.bounds.left && value.bounds.right <= windowWidth) : undefined;
  const tabletMedium = medium && !crease && mediumUprightTablet(windowWidth, windowHeight), mediumGutter = tabletMedium ? 28 : 24;
  const heroHeight = expandedTouch ? 440 : tabletMedium ? 560 : medium ? mediumHeroHeight(windowWidth, windowHeight) : theme.spec.hero.homeHeight[theme.column];
  const heroActive = active && !heroObscured && !runtime.getSnapshot().selectionActive;
  // states 4b: a page already read may stay on screen only for a positively known
  // transport failure. A refusal, permission, missing or invalid answer takes the
  // ordinary full error state instead of stale rows. TV keeps its own recovery.
  const errorFacts = snapshot.error ? presentError(snapshot.error, 'home') : undefined;
  const transportFailure = errorFacts !== undefined && (errorFacts.category === 'unreachable' || errorFacts.category === 'offline'
    || errorFacts.category === 'timeout' || errorFacts.category === 'busy');
  const retainable = errorFacts === undefined || transportFailure;
  const document = retainable ? authorizedHome(snapshot.document, authority) : undefined;
  const cachedAtMount = useRef(!!document);
  const [foreground, setForeground] = useState(AppState.currentState === 'active'), [loadingElapsed, setLoadingElapsed] = useState(0);
  const [heroVisible, setHeroVisible] = useState(true), [visibleRows, setVisibleRows] = useState<ReadonlySet<string>>(new Set());
  const [visibleRange, setVisibleRange] = useState<readonly [number, number]>([0, 0]);
  const [measuredRows, setMeasuredRows] = useState<Readonly<{layout: string; boxes: ReadonlyMap<string, {y: number; height: number}>}>>({layout: '', boxes: new Map()});
  // Phone and tablet: where the frame starts the rows in the scrolled content (a row measures itself from there).
  const [rowsTop, setRowsTop] = useState<number>();
  // eslint-disable-next-line react-hooks/exhaustive-deps -- Shelf offsets belong to this authority.
  const shelfOffsets = useMemo(() => new Map<string, {offset: number}>(), [authority]);
  const [focus, setFocus] = useState<{rowId: string; entryId: string}>();
  const [activeIndex, setActiveIndex] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const scrolled = useRef(false);
  const rowPositions = useRef(new Map<string, {y: number; height: number}>());
  const vertical = useFocusAnchor(false, false, 'start');
  const moveToTop = vertical.move;
  useEffect(() => runtime.registerScrollToTop({kind: 'home'}, () => moveToTop(0, true)), [runtime, moveToTop]);
  const current = useCallback(() => {
    try {return sameAuthority(authority, runtime.getSnapshot().authority);}
    catch (error) {if (error instanceof Error && error.name === 'AbortError') return false; throw error;}
  }, [authority, runtime]);
  const guarded = (run: () => void) => {if (current()) run();};
  const readLibraries = new Set(runtime.getSnapshot().libraries.filter(library => library.kind === 'books').map(library => library.id));
  const rows = document ? platformRows(document, 'phone', readLibraries).filter(homeRowHasContent) : [];
  // The server publishes every Home row as a deferred descriptor with its real title and
  // shape, then the row previews hydrate one at a time. Draw only the descriptors whose
  // preview has not arrived yet as bounded skeletons; no card, count or focus target.
  const pendingRows = document ? platformRows(document, 'phone', readLibraries).filter(row => row.deferred === true && !homeRowHasContent(row)).slice(0, 2) : [];
  const rowLayoutKey = `${windowWidth}:${windowHeight}:${dimensions.fontScale}:${theme.textScale}:${cardScale}:${theme.platform}:${theme.layoutMode}:${tabletMedium}`;
  const rowModels = rows.map(row => {
    const presentation = homeRowPresentation(row), wide = row.artworkShape === 'landscape', square = row.artworkShape === 'square';
    const ranked = presentation.cards.some(card => card.rank !== undefined);
    // Continue your books draws the Read lane's resume card (tablet-read.html frame 11) at the
    // sizes the Reading library uses for its own Continue row.
    const books = row.id === 'continue_books';
    const drawnWidth = books ? booksCardSize(theme).width : ranked ? desktop ? theme.spec.shelf.rank.width : 186
      : wide ? medium ? mediumCardWidth(true, tabletMedium) : desktop ? 262 : theme.spec.card.width.wide[theme.column]
      : square ? desktop ? 150 : theme.spec.card.width.square[theme.column] : medium ? mediumCardWidth(false, tabletMedium) : desktop ? 150 : theme.spec.card.width.poster[theme.column];
    const width = cardScale === 1 ? drawnWidth : Math.round(drawnWidth * cardScale);
    const posterWidth = ranked ? Math.round((desktop ? theme.spec.shelf.rank.posterWidth : 116) * cardScale) : width;
    const height = books ? Math.round(booksCardSize(theme).height * cardScale) : wide ? width * 9 / 16 + wideDetailHeight(theme) : square ? width + 54 : posterWidth * 1.5 + (48 + cardTitleLine(theme));
    const cardHeight = row.live ? desktop ? theme.spec.card.live.height.web : theme.spec.card.live.height.phone : height;
    const top = theme.spec.shelf.phonePaddingTop;
    const estimatedHeight = row.collection ? 236 : (theme.type.rowHeading.lineHeight ?? (theme.type.rowHeading.fontSize ?? 24) * 1.4)
      + theme.spec.shelf.headingMargin[theme.column] + cardHeight + (top + theme.spec.shelf.phonePaddingBottom);
    return {row, presentation, width, height, slotHeight: measuredRows.layout === rowLayoutKey ? measuredRows.boxes.get(row.id)?.height ?? estimatedHeight : estimatedHeight};
  });
  const noHero = !document?.spotlight.length;
  const heroBlock = (preview: boolean) => noHero ? homeTopWithoutHero(shellLayout(windowWidth, windowHeight, false).kind, insets.top) : homeHeroBlock(theme, heroHeight, preview);
  // A row reports its position relative to the rows container.
  const framedTop = rowsTop ?? 0;
  let rowY = rowsTop !== undefined ? rowsTop : heroBlock(rows.some(row => row.id === focus?.rowId && row.entries.some(entry => entry.id === focus?.entryId)));
  const spacing = homeRowSpacing(theme);
  const rowBoxes: readonly HomeRowBox[] = rowModels.map(({row, slotHeight}) => {
    rowY += spacing.marginTop ?? 0;
    const measured = measuredRows.layout === rowLayoutKey ? measuredRows.boxes.get(row.id) : undefined;
    const box = {id: row.id, y: measured ? measured.y + framedTop : rowY, height: slotHeight};
    rowY = box.y + slotHeight + (spacing.marginBottom ?? 0);
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
  const ranges = useRef(new Map<string, {first: number; last: number; keys?: readonly string[]}>());
  const viewportHeight = windowHeight, viewportWidth = windowWidth;

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
    setHeroVisible(y < heroHeight);
    if (scrolled.current !== (y > 0)) {scrolled.current = y > 0; onScrolledChange?.(scrolled.current);}
    const boxes = boxesRef.current, range = homeVisibleRange(boxes, y, viewportHeight - bottomInset);
    const visible = new Set(boxes.slice(range[0], range[1] + 1).map(box => box.id));
    setVisibleRange(previous => previous[0] === range[0] && previous[1] === range[1] ? previous : range);
    setVisibleRows(previous => previous.size === visible.size && [...visible].every(id => previous.has(id)) ? previous : visible);
  }, [bottomInset, heroHeight, onScrolledChange, viewportHeight]);
  useLayoutEffect(() => {scrollBoxes.value = rowBoxes; reportedRange.value = ''; updateViewport(vertical.offset.value);}, [rowBoxes, scrollBoxes, reportedRange, updateViewport, vertical.offset]);
  const onScroll = useAnimatedScrollHandler(event => {
    const y = event.contentOffset.y;
    vertical.offset.value = y;
    const range = homeVisibleRange(scrollBoxes.value, y, viewportHeight - bottomInset);
    const next = `${range[0]}:${range[1]}:${y < heroHeight}:${y > 0}`;
    if (reportedRange.value !== next) {reportedRange.value = next; runOnJS(updateViewport)(y);}
  });
  const candidates: readonly HomeCandidate[] = useMemo(() => document ? homeSpotlightModels(document).map(model => ({
    id: model.entry.id, title: model.entry.displayTitle || model.entry.title, long: expandedTouch && (model.entry.displayTitle || model.entry.title).length > 20, entry: model.entry, overview: model.overview,
    reason: serverTextLabel(labels, model.reason.text), facts: homeTitleFactParts(model.entry, runtime.text),
    target: model.primary?.entry, primaryLabel: model.primary ? t(model.primary.label.key, model.primary.label.params) : undefined,
  })) : [], [document, expandedTouch, runtime.text, labels, t]);
  const renderArtwork = (candidate: HomeCandidate, context: ArtworkRenderContext) => {
    const art = <AmbientArt source={runtime.ambient(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)}
      foreground={showBackdrops ? runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl) : undefined} treatment={theme.layoutMode === 'phone' ? 'phone' : 'hero'} visible={active && foreground && context.visible} priority
      onReady={context.onReady} onFallbackReady={context.onReady} onError={context.onError} foregroundStyle={context.foregroundStyle} width={crease ? Math.max(0, windowWidth - crease.bounds.right) : viewportWidth} height={heroHeight}
      foregroundFraction={expandedTouch ? .74 : medium && !tabletMedium ? mediumBackdropFraction(windowWidth, windowHeight) : undefined} foregroundFade={medium && !tabletMedium || expandedTouch ? 'left' : undefined}
      position={tabletMedium ? {x: .36, y: .28} : medium ? {x: 0.38, y: 0.22} : undefined}/>;
    return crease ? <View style={{position: 'absolute', left: crease.bounds.right, right: 0, top: 0, height: heroHeight}}>{art}</View> : art;
  };
  const activate = (entry: ContentEntry, target: ContentEntry | undefined) => guarded(() => {
    if (target?.playback) runtime.actions.play(entry, target.playback); else runtime.actions.navigate(entry);
  });
  const heroActions = (candidate: HomeCandidate) => {
    const {offers} = homeSpotlightFocusActions(candidate.entry, candidate.target, {platform: 'phone-app', playbackAvailable: !!runtime.playback});
    const style = tabletMedium || expandedTouch ? {height: undefined, minHeight: tabletMedium ? 52 : 48, paddingHorizontal: tabletMedium ? 26 : 22, borderRadius: 12} : undefined;
    const textStyle = tabletMedium || expandedTouch ? {fontSize: 16 * theme.textScale} : undefined;
    return <>
      {offers.playable && candidate.target?.playback && <Button focusId="play" icon="i-play" variant="primary" size="hero" style={style} textStyle={textStyle}
        onPress={() => activate(candidate.entry, candidate.target)}>{candidate.primaryLabel ?? t('next.home.play')}</Button>}
      {offers.watchlisted !== undefined && <Button focusId="my-list" icon={offers.watchlisted ? 'i-check' : 'i-plus'} variant="glass" size="hero" style={style} textStyle={textStyle} onPress={() => {
        if (current()) void runtime.actions.myList(candidate.entry, !offers.watchlisted).then(() => {
          if (current()) {noteHomeRead('my-list'); return runtime.home.revalidate();}
        }).catch(runtime.reportError);
      }}>{t('next.home.myList')}</Button>}
    </>;
  };
  const onRowFocus = (row: HomeRow, entryId: string) => setFocus({rowId: row.id, entryId});
  const spotlight = <Hero candidates={candidates} activeIndex={activeIndex} onIndexChange={setActiveIndex} active={heroActive} visible={active && foreground && heroVisible} variant={desktop ? 'desktop' : 'phone-app'} height={heroHeight}
    copySide={expandedTouch ? {inset: 32, width: 440, bottom: 118} : medium && !tabletMedium ? mediumCopySide(windowWidth, windowHeight) : undefined}
    copyStyle={tabletMedium ? {left: 56, right: 56, bottom: 44} : undefined}
    lockupStyle={expandedTouch || tabletMedium ? {...nativeType({size: tabletMedium ? 62 : 58, weight: 800, tracking: -.04, lineHeight: (tabletMedium ? 62 : 58) * 1.02}, theme.textScale), marginBottom: 12} : medium ? mediumLockupStyle(theme, windowWidth, windowHeight) : undefined}
    longLockupStyle={expandedTouch ? nativeType({size: 46, weight: 800, tracking: -.04, lineHeight: 46.92}, theme.textScale) : undefined}
    reasonTextStyle={expandedTouch || tabletMedium ? {...nativeType({size: tabletMedium ? 15 : 14, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), marginBottom: 10} : undefined}
    metadataStyle={expandedTouch || tabletMedium ? {marginBottom: tabletMedium ? 20 : 12} : undefined}
    overviewTextStyle={expandedTouch ? {...nativeType({size: 15, weight: 400, tracking: 0, lineHeight: 22.5}, theme.textScale), marginBottom: 18, maxWidth: 372} : undefined}
    dotsStyle={tabletMedium ? {marginTop: 20} : undefined} thumbnailSize={expandedTouch ? {width: 104, height: 58, labelSize: 11.5, bottom: 20, gap: 10, right: 32} : undefined}
    renderArtwork={renderArtwork} renderMetadata={candidate => <HomeHeroFacts facts={candidate.facts ?? []} centered={tabletMedium ? true : undefined} size={expandedTouch ? heroFactsSize(14, theme) : tabletMedium ? heroFactsSize(15, theme) : undefined}/>} renderActions={heroActions}
    renderThumbnail={candidate => <Artwork source={runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)} width={expandedTouch ? 104 : heroSpec.thumbnail.width} height={expandedTouch ? 58 : heroSpec.thumbnail.height}/>}
    onArtworkError={(error, candidate) => runtime.onArtworkError(error, candidate.entry)}/>;
  const mixesContent = runtime.mixes ? <MusicMixesRow focusScope={`home:${authorityKey(authority)}.mixes`} port={runtime.mixes} artwork={runtime.artwork} reportError={runtime.reportError} title={t('music.mixesForYou')} onOpen={runtime.musicDiscover} active={active}/> : null;
  if (!document) return snapshot.error
    ? <ScrollView testID="home-error-page" contentContainerStyle={{flexGrow: 1, justifyContent: 'center', paddingTop: phoneHeaderGeometry(insets.top, false).height, paddingBottom: homeContentBottom(theme, bottomInset)}}>
      <StateView variant="centered" {...runtime.error(snapshot.error, () => guarded(() => {void runtime.home.retry();}), 'home')}/></ScrollView>
    : <LoadingPaint ready={false} quietMs={0} skeleton={<HomeSkeleton elapsedMs={loadingElapsed} visible={active && foreground}/>}/>;
  if (!document.spotlight.length && !rows.length) return <LoadingPaint ready immediate={cachedAtMount.current} skeleton={null}>
    {librariesUnavailable ? <View style={{paddingTop: 100}}>{librariesNotice}</View> : runtime.renderHomeEmpty()}</LoadingPaint>;

  const scroll = (content: React.ReactNode, contentContainerStyle: ViewStyle) => <Animated.ScrollView ref={vertical.scrollRef} testID="native-home" style={vertical.scrollStyle} scrollEventThrottle={16} onScroll={onScroll}
    refreshControl={<RefreshControl refreshing={refreshing} tintColor={theme.colors.text} onRefresh={() => {
      if (!current() || refreshing) return;
      setRefreshing(true);
      noteHomeRead('pull'); void runtime.home.revalidate().catch(runtime.reportError).finally(() => {if (current()) setRefreshing(false);});
    }}/>}
    onScrollBeginDrag={vertical.stop}
    showsVerticalScrollIndicator={false}
    contentContainerStyle={contentContainerStyle}>{content}</Animated.ScrollView>;
  const rowsContent = <>
    {librariesNotice}
    {rowModels.map(({row, presentation, width, height, slotHeight}, rowIndex) => {
      const sourceRow = document.rows.find(value => value.id === row.id);
      if (!sourceRow) return null;
      if (!mountedRows.has(rowIndex)) return <View key={row.id}><View style={{height: slotHeight}}/></View>;
      let scrollMemory = shelfOffsets.get(row.id);
      if (!scrollMemory) {scrollMemory = {offset: 0}; shelfOffsets.set(row.id, scrollMemory);}
      const shown = active && foreground && (visibleRows.has(row.id) || !rowPositions.current.has(row.id) && rowIndex === 0);
      const title = serverTextLabel(labels, presentation.title), subtitle = serverTextLabel(labels, presentation.subtitle);
      // The kit's Row draws a string sub-line in the phone's 13 px role (phone-app.html .row-head .sub).
      const rowSubtitle = subtitle || undefined;
      const range = (first: number, last: number) => {if (!current()) return; const keys = ranges.current.get(row.id)?.keys; ranges.current.set(row.id, {first, last, keys}); runtime.setHomeWindow(sourceRow, rowIndex, first, last, shown, undefined, undefined, keys); if (shown && pageAtVisibleEnd(row, last, medium || desktop ? 10 : 3)) void runtime.home.more(row.id);};
      const visibleKeys = (keys: readonly string[]) => {if (!current()) return; const before = ranges.current.get(row.id) ?? {first: 0, last: -1};
        ranges.current.set(row.id, {...before, keys}); runtime.setHomeWindow(sourceRow, rowIndex, before.first, before.last, shown, undefined, undefined, keys);};
      const rowLayout: ViewProps['onLayout'] = event => {const box = event.nativeEvent.layout;
        rowPositions.current.set(row.id, box);
        setMeasuredRows(before => {
          const measured = before.layout === rowLayoutKey ? before.boxes.get(row.id) : undefined;
          if (measured?.y === box.y && measured.height === box.height) return before;
          const boxes = new Map(before.layout === rowLayoutKey ? before.boxes : []); boxes.set(row.id, {y: box.y, height: box.height});
          return {layout: rowLayoutKey, boxes};
        });
      };
      const rowBody = <>
        {row.live ? <HomeLiveRow row={row} runtime={runtime} visible={shown} itemWidth={desktop ? theme.spec.card.live.width.web : theme.spec.card.live.width.phone} onRange={range} onVisibleKeys={visibleKeys} onItemFocus={item => onRowFocus(row, JSON.stringify([item.channel.sourceId, item.channel.id]))} current={current} viewportWidth={viewportWidth} tabletMedium={tabletMedium} scrollMemory={scrollMemory} headingStyle={mediumHeadingStyle}/>
          : row.collection ? <FocusItem focusId={ids.collection(row.collection)}><View style={{paddingHorizontal: theme.gutter}}><HomeCollectionCard row={row} runtime={runtime}
            width={viewportWidth - theme.gutter - (theme.gutter)} visible={shown} current={current} onFocus={() => onRowFocus(row, row.collection!.collection.id)}/></View></FocusItem>
          : <Row title={title} subtitle={rowSubtitle} items={presentation.cards} getKey={homeCardKey} getFocusId={homeCardFocusId} initialViewportWidth={viewportWidth} scrollMemory={scrollMemory} itemWidth={width} itemHeight={height} growItems={row.id === 'continue_books'} captionHeight={row.id === 'continue_books' ? 0 : row.artworkShape === 'landscape' ? wideDetailHeight(theme) : row.artworkShape === 'square' ? 54 : 48 + cardTitleLine(theme)}
            gutter={medium ? mediumGutter : theme.gutter} rightGutter={medium ? mediumGutter : theme.gutter} gap={medium ? 14 : undefined} headingStyle={medium ? mediumHeadingStyle : undefined} selection={runtime.selection(row.id)} onVisibleRange={range} onVisibleKeys={visibleKeys}
            onSeeAll={row.seeAll ? () => guarded(() => runtime.actions.seeAll(row.seeAll!, title)) : undefined}
            onItemFocus={item => onRowFocus(row, item.entry.id)} renderItem={(item, context) => <View style={row.id === 'continue_books' || theme.fontScale > 1 || theme.textScale > 1 ? {width, minHeight: height} : {width, height}}><LoadingPaint ready={!context.waiting} immediate={!context.waiting} skeleton={<View style={{backgroundColor: theme.colors.ink}}><SkeletonBlock width={width} height={height}/></View>}><HomeCard runtime={runtime} row={row} entry={item.entry} rank={item.rank} context={context}
              width={width} rowIndex={rowIndex} visible={shown} preferred={false} cardType={medium ? mediumCardType : undefined}
              current={current}/></LoadingPaint></View>}/>}
</>;
      return <HomeWindow key={row.id} runtime={runtime} row={sourceRow} sectionOrder={rowIndex} visible={shown} range={ranges.current.get(row.id)}>
        <View onLayout={rowLayout}>{rowBody}</View></HomeWindow>;
    })}
    {pendingRows.map(row => <HomePendingRow key={`pending:${row.id}`} row={row} labels={labels} viewportWidth={viewportWidth}/>)}
    {mixesContent}
    <View style={{marginHorizontal: 24, alignItems: 'center', gap: 12}}>
      <Button focusId="customize" variant="glass" icon="i-sliders" style={{height: 48, paddingHorizontal: 22}} onPress={() => guarded(runtime.actions.customize)}>{t('home.customize.action')}</Button>
      <Text style={[nativeType({size: 13, weight: 400, tracking: 0, lineHeight: 19.5}, theme.textScale), {color: theme.colors.textTertiary, textAlign: 'center'}]}>{t('next.home.customizeDescription')}</Text>
    </View>
  </>;
  // tablet 2: Play on is the round glass button at the hero's top right, drawn in the page so it scrolls away with the hero
  // and never covers a row; with nothing featured it is the hero, a bar the rows start under. Hidden while the shell hides it.
  const tablet = shellLayout(windowWidth, windowHeight, false).kind === 'tablet';
  const tabletPlayOn = !playOnHidden && <View pointerEvents="box-none" style={{position: 'absolute', top: tabletPlayOnTop(insets.top), right: 20}}><PlayOnButton playOn={runtime.playOn} variant="round"/></View>;
  // Phone and tablet: the library frame (`BrowseFrame`) draws Home with its hero. The frame places the hero, the rows, the
  // gaps between them and the bottom clearance; Home keeps its own scroller, whose handler drives the row window, the
  // scrolled header and pull to refresh, and measures its rows from where the frame starts them.
  return <LoadingPaint ready immediate={cachedAtMount.current} skeleton={null}>
    <BrowseFrame testID="home-frame" hero={tablet ? <View style={{height: noHero ? tabletPlayOnTop(insets.top) + roundBarSize : heroBlock(false)}}>{noHero ? null : spotlight}{tabletPlayOn}</View>
      : noHero ? null : <View style={{height: heroBlock(false)}}>{spotlight}</View>}
      onRowsLayout={event => setRowsTop(event.nativeEvent.layout.y)}
      scroller={(content, style) => scroll(content, {paddingBottom: Number(style.paddingBottom ?? 0) + bottomInset})}>{rowsContent}</BrowseFrame>
  </LoadingPaint>;
}
