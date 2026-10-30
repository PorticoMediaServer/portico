import {TVSparseCardFrame, TVCardPaintFrame, TVPendingCardPaint} from '../../kit/cards/TVSparseCardFrame';
import type {CardProps} from '../../kit/cards/primitives';
import React, {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject} from 'react';
import {BackHandler, Pressable, ScrollView, StyleSheet, Text, View, useWindowDimensions, type LayoutChangeEvent, type StyleProp, type TextStyle} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {Library} from '../../../../../packages/client-core/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {BrowseNode} from '../../../../../packages/client-core/src/browse';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {libraryTabs, type LibraryTabId} from '../../../../../packages/client-core/src/presentation/library-tabs';
import {MusicMixesRow} from '../destinations/music/MusicMixesRow';
import {TVMusicDiscover} from '../destinations/music/TVMusicDiscover';
import {serverTextLabel} from '../../../../../packages/client-core/src/presentation/server-text';
import {CircleCard, GenreTile, PlaylistMosaic, PosterCard, SquareCard, type CardSelection} from '../../kit/cards';
import {AmbientArt, Artwork, StandardBackdrop} from '../../kit/artwork';
import {Button, Focusable} from '../../kit/controls';
import {FocusItem, FocusRegion, useFocusRegistry, type FocusRegionProps} from '../../kit/focus';
import {Banner} from '../../kit/overlays';
import {shareLinkActions} from '../share/share-actions';
import {LibraryHeader, LibraryMenu, LibrarySwitcher, QuickToggle, ViewToggle, type BrowseLabels} from '../../kit/browse';
import {browseType, alpha} from '../../kit/browse/presentation';
import {Icon} from '../../kit/Icon';
import {usePullRefresh} from '../../kit/states/usePullRefresh';
import {WindowedGrid, AlphabetRail, Row, type LayoutSelection, type WindowedGridHandle} from '../../kit/layout';
import {discoverCell} from './discover-cells';
import {GridSkeleton, SkeletonBlock, StateView, showSkeleton, skeletonTiming} from '../../kit/states';
import {useTheme, type Theme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import type {NativeShellRuntime} from '../../shell/runtime';
import {homeActionDiagnostics} from '../../platform/home-action-diagnostics';
import {homeCardText} from '../home/model';
import {createDestinationEntryFocus, nativeDestinationFocusIds, type NativeDestinationActivationPort} from '../destinations/focus';
import {useTVPageBack} from '../destinations/useTVPageBack';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import {NativeLibraryDestination, nativeLibraryEntryKey, nativeLibraryCanSave, type NativeLibrarySnapshot, type NativeLibraryViewDraft} from './model';
import {LibraryToolbar} from './LibraryToolbar';
import {LibraryListRow} from './LibraryListRow';
import {NativeLibraryDirectory} from './directory';
import {LibraryFilters, libraryFiltersDockWidth, libraryFiltersOverWidth, libraryFiltersTabletPane} from './LibraryFilters';
import {LibraryJumpSheet} from './LibraryJumpSheet';
import {libraryJumpLetter, libraryJumpLetters} from './jump';
import type {NativeLibraryFilterDraft, NativeLibraryFilterSnapshot} from './filters';
import {libraryAppliedFilters} from './applied-filters';
import {libraryTabletGridGeometry} from './library-grid';
import {hiddenReadingLibraryOnTV, isReadingLibraryKind} from '../read/platform';
import type {NativeEntryMenuState} from '../destinations/entry-actions';
import type {NativeMarkAllRequest} from './mark-all';

export type NativeLibraryActions = Readonly<{
  dispose?: () => void;
  menuState?: NativeEntryMenuState;
  open: (entry: ContentEntry) => void;
  play: (entry: ContentEntry) => void;
  menu: (entry: ContentEntry, anchorRef?: RefObject<View | null>, /** The real selection entry point the menu's Select row runs. */ select?: () => void) => void;
  isMenuOpen?: (entry: ContentEntry) => boolean;
  saveView: (destination: NativeLibraryDestination, draft: NativeLibraryViewDraft) => void;
  /** The host's count-aware bulk command: one durable job over the view the viewer sees. */
  markAll?: (request: NativeMarkAllRequest) => void;
}>;
export type NativeLibrarySelectionPort = Readonly<{
  /** Enter this library's actual current selection context without selecting an item. */
  start: () => void;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => LayoutSelection;
  register: (source: NativeLibrarySnapshot, entries: readonly ContentEntry[], first: number, last: number) => () => void;
  item: (entry: ContentEntry) => CardSelection;
  /** The picker's rows take the screen's own real artwork resolver; nothing is drawn without one. */
  render: (artwork?: (path?: string) => ReactNode) => ReactNode;
}>;
export type NativeLibraryScreenProps = Readonly<{
  destination: NativeLibraryDestination;
  runtime: NativeShellRuntime;
  activation: NativeDestinationActivationPort;
  libraries: readonly Library[];
  directory: NativeLibraryDirectory;
  labels: BrowseLabels;
  actions: NativeLibraryActions;
  selection?: NativeLibrarySelectionPort;
  owner: boolean;
  /** Home's Mixes More opens this library's Discover and lands on the Mixes row. */
  mixAnchor?: boolean;
}>;
const noSubscribe = () => () => {};
const noSelection = () => undefined;
const noFilterSnapshot = (): NativeLibraryFilterSnapshot | undefined => undefined;
/** Discover shelves have no multi-select of their own. */
const noRowSelection: LayoutSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};
const resourceFocusMemory = new WeakMap<NativeShellRuntime, Map<string, Readonly<{key: string; index: number}>>>();

/** Screen-local geometry named for this lane. Numbers come from the phone frame's
 * own CSS: the compact row is 44 high, the back control 38 with its optical -4
 * offset, the pivot strip 46 with a 3px accent underline, and the count row
 * `padding: 10px 20px`. */
const nativeLibraryR46 = StyleSheet.create({
  header: {borderBottomWidth: 1},
  navRow: {height: 44, paddingLeft: 12, paddingRight: 10, flexDirection: 'row', alignItems: 'center'},
  back: {height: 38, marginLeft: -4, paddingRight: 6, justifyContent: 'center'},
  backContent: {flexDirection: 'row', alignItems: 'center'},
  /** The switcher centres on the screen, not on the space the row leaves; the
   * side bounds keep a long library name clear of both controls. */
  centre: {position: 'absolute', left: 118, right: 118, top: 0, height: 44, alignItems: 'center', justifyContent: 'center'},
  centreInner: {maxWidth: '100%'},
  trailing: {marginLeft: 'auto'},
  tabStrip: {paddingHorizontal: 20, gap: 24},
  tab: {height: 46, justifyContent: 'center'},
  tabUnderline: {position: 'absolute', left: 0, right: 0, bottom: 0, height: 3, borderRadius: 3},
  wash: {position: 'absolute', top: 0, left: 0, right: 0, height: 470, overflow: 'hidden'},
  count: {paddingHorizontal: 20, paddingTop: 10, paddingBottom: 10},
});

/** The wash behind the compact row belongs to the Listen and Read libraries
 * (phone-app 15a and the Books row). Movies, TV Shows and Anime are flat. */
export function libraryWashKind(kind: string): boolean {
  return kind === 'music' || kind === 'audiobook' || isReadingLibraryKind(kind);
}

/** Phone Library's compact header (phone-app #r2a 4): one 44 row with
 * "‹ Libraries", the centred switcher and ⋯, then the pivot strip. The
 * switcher is centred on the screen itself, not on the space the row leaves. */
export function LibraryCompactHeader({switcher, menu, wash, children, onLibraries, testID}: {
  switcher?: ReactNode; menu?: ReactNode; wash?: boolean; children?: ReactNode; onLibraries: () => void; testID?: string;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), recipe = glassRecipe();
  return <View testID={testID} style={[nativeLibraryR46.header, {paddingTop: insets.top, backgroundColor: alpha(theme.colors.ink, wash ? .62 : .84),
    borderBottomColor: alpha(theme.colors.text, .07), zIndex: theme.layers.sticky}]}>
    {recipe.nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>}
    <View style={nativeLibraryR46.navRow}>
      <Focusable accessibilityRole="button" focusKind="row" onPress={onLibraries} accessibilityLabel={defaultI18n.t('next.browse.native.libraries')} style={nativeLibraryR46.back}>
        <View style={nativeLibraryR46.backContent}>
          <Icon name="i-left" size={22} color={theme.colors.textSecondary}/>
          <Text style={[browseType(theme, 16, 600), {color: theme.colors.textSecondary}]}>{defaultI18n.t('next.browse.native.libraries')}</Text>
        </View>
      </Focusable>
      <View pointerEvents="box-none" style={nativeLibraryR46.centre}><View style={nativeLibraryR46.centreInner}>{switcher}</View></View>
      <View style={nativeLibraryR46.trailing}>{menu}</View>
    </View>
    {children}
  </View>;
}

/** tablet4: the expanded library header is the desktop one at touch size: the
 * 44 pt title with its switcher beside it, then Select and the library menu at
 * the right, and the pivot strip on the drawn hairline. The sidebar already says
 * where you are, so there is no back control. */
export function LibraryExpandedHeader({name, switcher, menu, ambient, selection, children, testID}: {
  name: string; switcher?: ReactNode; menu?: ReactNode; ambient?: ReactNode; selection?: ReactNode; children?: ReactNode; testID?: string;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), recipe = glassRecipe();
  return <View testID={testID} style={{paddingTop: insets.top, backgroundColor: alpha(theme.colors.ink, ambient ? .62 : .84),
    borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.text, .07), zIndex: theme.layers.sticky}}>
    {ambient && <View pointerEvents="none" style={StyleSheet.absoluteFill}>{ambient}</View>}
    {recipe.nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: theme.gutter, paddingTop: 22}}>
      <Text accessibilityRole="header" numberOfLines={1} style={[browseType(theme, 44, 800, -.045, 44), {color: theme.colors.text, flexShrink: 1}]}>{name}</Text>
      <View style={{width: 44, height: 44, borderRadius: 22, marginTop: 4, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .12)}}>{switcher}</View>
      <View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 8}}>{selection}{menu}</View>
    </View>
    {children}
  </View>;
}

/** tablet5: upright, the rail already says where you are, so the compact header
 * keeps the phone's 24 pt name and switcher but drops its back control; Select
 * and the library menu sit at the right, and the pivots scroll as on a phone. */
export function LibraryMediumHeader({name, switcher, menu, selection, ambient, wash, children, testID}: {
  name: string; switcher?: ReactNode; menu?: ReactNode; selection?: ReactNode; ambient?: ReactNode; wash?: boolean; children?: ReactNode; testID?: string;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), recipe = glassRecipe();
  return <View testID={testID} style={{paddingTop: insets.top, backgroundColor: alpha(theme.colors.ink, wash ? .62 : .84),
    borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.text, .07), zIndex: theme.layers.sticky}}>
    {ambient && <View pointerEvents="none" style={StyleSheet.absoluteFill}>{ambient}</View>}
    {recipe.nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>}
    <View style={{height: 46, flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: 28}}>
      <Text accessibilityRole="header" numberOfLines={1} style={[browseType(theme, 24, 800, -.03), {color: theme.colors.text, flexShrink: 1}]}>{name}</Text>
      <View style={{width: 44, height: 44, borderRadius: 22, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .12)}}>{switcher}</View>
      <View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 8}}>{selection}{menu}</View>
    </View>
    {children}
  </View>;
}

/** tablet5: the 420 pt panel slides in from the right over the dimmed grid and
 * leaves the same way. It keeps the draft alive until the exit lands, then the
 * route disposes exactly that draft. Timings are the kit's side-panel pair
 * (320 in, 240 out); reduced motion keeps only the fade. */
function LibraryFiltersOverPanel({open, width, onDismiss, onExited, children}: {
  open: boolean; width: number; onDismiss: () => void; onExited: () => void; children?: ReactNode;
}) {
  const theme = useTheme(), motion = useMotion(), progress = useSharedValue(0);
  const exited = useRef(onExited); exited.current = onExited;
  useEffect(() => {progress.value = withTiming(open ? 1 : 0, {...motion.timing('slow', open ? 'out' : 'in'), duration: motion.reduce ? motion.duration('standard') : open ? 320 : 240});}, [open, motion, progress]);
  useEffect(() => {if (open) return; const timer = setTimeout(() => exited.current(), motion.reduce ? motion.duration('standard') : 240); return () => clearTimeout(timer);}, [open, motion]);
  const pane = useAnimatedStyle(() => ({transform: motion.transforms ? [{translateX: (1 - progress.value) * width}] : [], opacity: motion.reduce ? progress.value : 1}));
  const scrim = useAnimatedStyle(() => ({opacity: progress.value * .62}));
  return <View style={{position: 'absolute', inset: 0, zIndex: theme.layers.navigation}}>
    <Animated.View style={[StyleSheet.absoluteFill, {backgroundColor: '#03080c'}, scrim]}>
      <Pressable accessible={false} onPress={onDismiss} style={StyleSheet.absoluteFill}/>
    </Animated.View>
    <Animated.View testID="library-filters-over" accessibilityViewIsModal importantForAccessibility="yes"
      style={[{position: 'absolute', top: 0, bottom: 0, right: 0, width, maxWidth: '100%', backgroundColor: theme.colors.raised,
        shadowColor: '#000', shadowOffset: {width: -20, height: 0}, shadowOpacity: .6, shadowRadius: 40}, pane]}>{children}</Animated.View>
  </View>;
}

/** The pivot strip: it scrolls on a phone because Music and Audiobooks have more
 * pivots than fit (phone-app 15a), and stays one drawn row on TV. */
export function LibraryTabs({items, selected, enabled, disabled, regionId, leftEdge, downEdge, phone, gutter, onSelect, onFocusTab}: {
  items: readonly Readonly<{id: LibraryTabId; label: MessageId}>[]; selected: string; enabled: boolean; disabled?: boolean;
  regionId: string; leftEdge: string; downEdge?: string; phone: boolean; /** The drawn tablet gutter, so the pivots line up with the header's title. */ gutter?: number; onSelect: (id: LibraryTabId) => void; onFocusTab?: (id: LibraryTabId) => void;
}) {
  const theme = useTheme();
  const strip = items.map(tab => <Focusable key={tab.id} focusId={tab.id} accessibilityRole="tab" accessibilityLabel={defaultI18n.t(tab.label)}
    accessibilityState={{selected: selected === tab.id}} disabled={!enabled || !!disabled} onPress={() => onSelect(tab.id)}
    onFocus={theme.isTV ? () => onFocusTab?.(tab.id) : undefined}
    style={theme.isTV ? {height: 64, justifyContent: 'center', borderBottomWidth: selected === tab.id ? 4 : 0, borderBottomColor: theme.colors.accentStrong}
      : phone ? nativeLibraryR46.tab
      : {height: 46, justifyContent: 'center', borderBottomWidth: selected === tab.id ? 3 : 0, borderBottomColor: theme.colors.accentStrong}}>
    <Text style={[browseType(theme, theme.isTV ? 24 : 15.5, 700), {color: selected === tab.id ? theme.colors.text : theme.colors.textTertiary}]}>{defaultI18n.t(tab.label)}</Text>
    {phone && selected === tab.id && <View style={[nativeLibraryR46.tabUnderline, {backgroundColor: theme.colors.accent}]}/>}
  </Focusable>);
  return <FocusRegion id={regionId} entry="browse" orientation="horizontal" enabled={enabled} edges={{left: leftEdge, ...(downEdge ? {down: downEdge} : {})}}
    style={phone ? undefined : {flexDirection: 'row', gap: theme.isTV ? 32 : 28, paddingHorizontal: theme.isTV ? 0 : gutter ?? theme.gutter}}>
    {phone ? <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flexGrow: 0, flexShrink: 0}} contentContainerStyle={nativeLibraryR46.tabStrip}>{strip}</ScrollView> : strip}
  </FocusRegion>;
}

/** The drawn result count: its own left-aligned row under the toolbar on a
 * phone (`.cnt`), and the toolbar's trailing text on TV. */
export function LibraryResultCount({phone, style, children, controls}: {phone: boolean; style?: StyleProp<TextStyle>; children: ReactNode; controls?: ReactNode}) {
  const theme = useTheme();
  const count = <Text accessibilityLiveRegion="polite" style={[browseType(theme, phone ? 13 : 22, 600), {color: theme.colors.textTertiary}, !controls && phone ? nativeLibraryR46.count : undefined, style]}>{children}</Text>;
  return controls ? <View style={[nativeLibraryR46.count, {flexDirection: 'row', alignItems: 'center', gap: 10}]}>{count}<View style={{marginLeft: 'auto'}}>{controls}</View></View> : count;
}

/** The actual library route, independently retained below a title page. The
 * initial TV card is a real producer entity, never an index or placeholder. */
export function LibraryScreen(props: NativeLibraryScreenProps) {
  const {destination, runtime, activation, directory, libraries, actions} = props, theme = useTheme(), registry = useFocusRegistry(), dimensions = useWindowDimensions();
  useSyncExternalStore(actions.menuState?.subscribe ?? noSubscribe, actions.menuState?.getSnapshot ?? (() => 0));
  const entryRequest = useMemo(() => createDestinationEntryFocus(registry), [registry]);
  const [entered, setEntered] = useState(false), onEntered = useCallback(() => setEntered(true), []);
  // This route's own laid-out width. It is read on every layout, so a collapsed
  // sidebar, a fold or a docked pane is already excluded before the grid sizes.
  const [areaWidth, setAreaWidth] = useState(0);
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const selection = state.tab.shows === 'genres' || state.tab.shows === 'playlists' ? undefined : props.selection;
  const summaries = useSyncExternalStore(directory.subscribe, directory.getSnapshot);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  // One classification for the header, the grid and its skeleton: the drawn
  // tablet layout is medium below 1024 and expanded from 1024, whatever the
  // theme calls the platform at that width.
  const shellPhone = dimensions.width < 600 || dimensions.width < 1024 && dimensions.height < 600;
  const tabletLayout = !theme.isTV && !shellPhone;
  const expanded = dimensions.width >= 1024;
  const shellWidth = shellPhone || theme.isTV ? 0 : expanded ? 248 : 84;
  const contentWidth = areaWidth || Math.max(0, dimensions.width - shellWidth);
  // tablet4/5: the filter panel is a split view on expanded and a panel over the
  // grid on medium, so the route owns the draft that both the pane and the grid read.
  const [filterDraft, setFilterDraft] = useState<NativeLibraryFilterDraft>();
  const discoverScroll = useRef<ScrollView>(null), mixAnchored = useRef(false), mixAnchorY = useRef<number | undefined>(undefined);
  const filterOpening = useRef(0), filtersRef = useRef<NativeLibraryFilterDraft | undefined>(undefined), [overClosing, setOverClosing] = useState(false);
  const filterState = useSyncExternalStore(filterDraft?.subscribe ?? noSubscribe, filterDraft?.getSnapshot ?? noFilterSnapshot);
  useEffect(() => {if (active.intent !== 'restore') mixAnchored.current = false;}, [state.library.id, active.revision, active.intent]);
  const scrollToMixes = () => {
    const mixes = runtime.mixes?.getSnapshot();
    if (!props.mixAnchor || mixAnchored.current || !active.visible || active.intent === 'restore'
      || mixAnchorY.current === undefined || mixes?.phase !== 'ready' || !mixes.rows.length) return;
    mixAnchored.current = true;
    discoverScroll.current?.scrollTo({y: Math.max(0, mixAnchorY.current - 8), animated: false});
  };
  const anchorMixes = (event: LayoutChangeEvent) => {
    mixAnchorY.current = event.nativeEvent.layout.y; scrollToMixes();
  };
  const openFilters = useCallback(() => {
    filtersRef.current?.dispose(); filterOpening.current++;
    const next = destination.createFilters();
    filtersRef.current = next; setFilterDraft(next); setOverClosing(false);
  }, [destination]);
  // A retained close can arrive after a newer draft replaced this one. Only the
  // draft the caller actually owns is disposed and cleared, so an old panel never
  // erases its successor.
  // Leaving the route drops the panel at once; the drawn over-panel gets its exit
  // motion first, and only the draft it actually owns is disposed when it lands.
  const dropFilters = useCallback((subject?: NativeLibraryFilterDraft) => {
    const current = filtersRef.current;
    if (!current || subject && current !== subject) return;
    filtersRef.current = undefined; current.dispose(); setFilterDraft(undefined); setOverClosing(false);
  }, []);
  const closeFilters = useCallback((subject?: NativeLibraryFilterDraft) => {
    const current = filtersRef.current;
    if (!current || subject && current !== subject) return;
    if (tabletLayout && !expanded) {setOverClosing(true); return;}
    dropFilters(current);
  }, [dropFilters, expanded, tabletLayout]);
  useEffect(() => {if (!active.visible) dropFilters(filtersRef.current);}, [active.visible, dropFilters]);
  useEffect(() => () => {filtersRef.current?.dispose(); filtersRef.current = undefined;}, []);
  const ids = nativeDestinationFocusIds(runtime.getSnapshot().authority);
  useEffect(() => {void destination.load().catch(runtime.reportError); void directory.load().catch(runtime.reportError);}, [destination, directory, runtime]);
  // The drawn loading page waits the kit's own skeleton delay before drawing shapes; the
  // header and tabs are already known, the count and posters are the only missing facts.
  const loading = (state.phase === 'loading' || state.phase === 'idle') && !state.collection;
  const [loadingElapsed, setLoadingElapsed] = useState(0);
  useEffect(() => {
    if (!loading) {setLoadingElapsed(0); return;}
    // Only the 300 ms and slow thresholds matter: two timers, not a 120 ms re-render clock.
    const timers = [skeletonTiming.delayMs, skeletonTiming.slowMs].map(at => setTimeout(() => setLoadingElapsed(at), at));
    return () => timers.forEach(clearTimeout);
  }, [loading, state.library.id, state.tab.id]);
  // tv-focus 46: a library with no titles yet draws one action, never an empty grid.
  const empty = state.phase === 'ready' && state.unfilteredTotal?.total === 0;
  const emptyRegion = `${ids.libraryTabs(state.library.id)}.empty`;
  // E-tv-06/14: one error, named for this library, and reachable from the rail and the tabs.
  const errorRegion = `${ids.libraryTabs(state.library.id)}.error`, pageFailed = state.phase === 'error';
  // tv-focus 46/F-STATE-17: once the empty page holds focus, the first titles
  // arriving behind it never move focus; the page stays until the person goes
  // Down or Right into the new grid (or leaves it towards the tabs/rail).
  const scope = `${state.library.id}|${state.tab.id}`, scopeRef = useRef(scope), [emptyHeld, setEmptyHeld] = useState(false);
  useEffect(() => {
    if (scopeRef.current !== scope) {scopeRef.current = scope; setEmptyHeld(empty);}
    else if (empty) setEmptyHeld(true);
  }, [empty, scope]);
  const emptyShown = empty || emptyHeld;
  const firstCard = state.collection?.itemAt(0), firstCardRegion = firstCard ? theme.isTV ? ids.libraryGrid(state.library.id, state.tab.id) : ids.libraryCard(state.library.id, nativeLibraryEntryKey(firstCard)) : undefined;
  // E-tv-27: Try again unmounts the error page while the load runs, so focus falls to the header, and the
  // entry request was spent on the first failure. A failure that follows a Try again press is a fresh entry.
  const retryPressed = useRef(false), [retryEpoch, setRetryEpoch] = useState(0);
  useEffect(() => {
    if (!active.visible) retryPressed.current = false;
    else if (pageFailed && retryPressed.current) {retryPressed.current = false; setRetryEpoch(value => value + 1);}
  }, [pageFailed, active.visible]);
  const retryPage = useCallback(() => {retryPressed.current = true; destination.retry();}, [destination]);
  // eslint-disable-next-line react-hooks/exhaustive-deps -- a new epoch is a new request, by design
  const stateEntry = useMemo(() => createDestinationEntryFocus(registry), [registry, retryEpoch]);
  useDestinationEntryFocus(active, !theme.isTV ? undefined : emptyShown ? {regionId: emptyRegion, childId: 'browse-home'} : pageFailed ? {regionId: errorRegion, childId: 'retry'} : undefined, undefined, stateEntry);
  const pivots: Readonly<Record<string, string>> = {movie: 'movies', show: 'shows', episode: 'episodes', artist: 'artists', album: 'albums', song: 'songs', book: 'books', author: 'authors', series: 'series', ebook: 'books', comic: 'titles'};
  const choices = libraries.filter(library => !theme.isTV || !hiddenReadingLibraryOnTV(library.kind)).map(library => {
    const summary = summaries.libraries.find(value => value.id === library.id)?.summary;
    return {id: library.id, name: library.name, count: summary && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 12}}>{summary.counts.filter(part => pivots[part.kind]).map(part => <Text key={part.kind}
      style={[browseType(theme, theme.isTV ? 22 : 14), {color: theme.colors.textTertiary}]}>{defaultI18n.t(part.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: part.count, pivot: pivots[part.kind]})}</Text>)}</View>};
  });
  const tabs = <LibraryTabs items={libraryTabs(state.library.kind)} selected={state.tab.id} enabled={active.visible} phone={theme.platform === 'phone'} gutter={theme.isTV || theme.platform === 'phone' ? undefined : dimensions.width >= 1024 ? 32 : 28}
    disabled={theme.isTV && active.intent === 'enter' && !entered} regionId={ids.libraryTabs(state.library.id)} leftEdge={ids.rail} downEdge={emptyShown ? emptyRegion : pageFailed ? errorRegion : undefined}
    onSelect={id => {void destination.load(id).catch(runtime.reportError);}} onFocusTab={id => {void destination.load(id).catch(runtime.reportError);}}/>;
  const switcher = <LibrarySwitcher value={state.library.id} libraries={choices} onValueChange={id => runtime.onDestination({kind: 'library', libraryId: id})} trigger={theme.isTV || expanded ? 'chevron' : 'name'}/>;
  const latestArtwork = summaries.libraries.find(library => library.id === state.library.id)?.summary.latestArtwork;
  const source = latestArtwork ? runtime.ambient(latestArtwork) : undefined;
  // The phone's wash sits behind the pinned header and the first rows (`.lwash`).
  const washed = !!source && theme.platform === 'phone' && libraryWashKind(state.library.kind);
  const wash = washed ? <View pointerEvents="none" style={nativeLibraryR46.wash}><AmbientArt source={source} treatment="library"/></View> : undefined;
  const ambient = source && !washed ? <AmbientArt source={source} treatment="library"/> : undefined;
  // The menu's Mark all covers exactly this view: the pivot and filters the viewer sees, with
  // the view's own count. The job captures membership itself, so nothing here is a client tally.
  const markAll = (): void => {
    const view = state.collection?.getSnapshot();
    if (!view || view.total === undefined || !actions.markAll) return;
    const predicates = state.predicates;
    const filter = predicates.length === 0 ? undefined : predicates.length === 1 ? predicates[0] : {all: predicates};
    const sort = state.sort ? [{field: state.sort.field, direction: state.sort.direction}] : undefined;
    actions.markAll({selector: {query: {libraryId: state.library.id, pivot: state.tab.pivot ?? '',
      ...(filter ? {filter} : {}), ...(sort ? {sort} : {})}},
      pivot: state.tab.pivot ?? 'other', viewTotal: view.total, viewAtLeast: view.totalIsLowerBound === true,
      read: isReadingLibraryKind(state.library.kind),
      // The cards' watched marks come from the fetched page, so the view that submitted the job
      // reloads itself when the job is applied — and only while it is still the same view.
      applied: () => {
        const now = destination.getSnapshot();
        if (now.predicates === predicates && now.sort === state.sort) destination.setQuery(predicates, state.sort);
      }});
  };
  homeActionDiagnostics.libraryMenu('screen', runtime);
  // Preserve JSX's original component-reference-before-props evaluation order.
  const Menu = LibraryMenu;
  const menuProps = runtime.libraryMenu(state.library);
  homeActionDiagnostics.libraryMenu('produced', runtime);
  const canSaveView = !!state.sort && nativeLibraryCanSave(state, {predicates: state.predicates, sort: state.sort});
  const menu = <Menu {...menuProps}
    canSaveView={canSaveView} onSaveView={() => {
      if (state.sort && nativeLibraryCanSave(state, {predicates: state.predicates, sort: state.sort})) actions.saveView(destination, {predicates: state.predicates, sort: state.sort});
    }} onMarkAll={actions.markAll && state.collection ? markAll : undefined}
    shareActions={theme.isTV ? undefined : shareLinkActions(runtime.getSnapshot().authority.serverId, 'library', {libraryId: state.library.id}, {notice: runtime.notice, report: runtime.reportError})}/>;
  homeActionDiagnostics.libraryMenu('element', runtime);
  const select = selection && <Button testID="library-select-header" variant="text" size="compact" icon="i-check" style={{height: 44}} onPress={selection.start}>{defaultI18n.t('next.selection.select')}</Button>;
  const screenGutter = tabletLayout ? expanded ? 32 : 28 : theme.gutter;
  const songCount = state.library.kind === 'music' ? summaries.libraries.find(library => library.id === state.library.id)?.summary.counts.find(part => part.kind === 'song') : undefined;
  const musicCount = songCount && <Text style={[browseType(theme, 22, 400), {marginLeft: 'auto', color: theme.colors.textTertiary}]}>
    {defaultI18n.t(songCount.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: songCount.count, pivot: 'songs'})}</Text>;
  const header = theme.isTV ? <LibraryHeader name={state.library.name} switcher={switcher} ambient={ambient} menu={musicCount}>{tabs}</LibraryHeader>
    : theme.platform === 'phone' ? <LibraryCompactHeader switcher={switcher} menu={menu} wash={washed}
      onLibraries={() => runtime.onDestination({kind: 'libraries'})}>{tabs}</LibraryCompactHeader>
    : expanded ? <LibraryExpandedHeader name={state.library.name} switcher={switcher} menu={menu} ambient={ambient} selection={select}>{tabs}</LibraryExpandedHeader>
    : <LibraryMediumHeader name={state.library.name} switcher={switcher} menu={menu} ambient={ambient} wash={washed} selection={select}>{tabs}</LibraryMediumHeader>;
  // tablet4/5: Filters docks at 380 on expanded and covers the grid at 420 on
  // medium. The grid only ever sees the column it is actually given.
  const docked = tabletLayout && expanded && !!filterDraft;
  const over = tabletLayout && !expanded && !!filterDraft;
  const gridWidth = Math.max(0, contentWidth - (docked ? libraryFiltersDockWidth : 0));
  // Back closes the panel before it leaves the library, as the phone sheet does.
  useEffect(() => {
    if (!docked && !over) return;
    const subscription = BackHandler.addEventListener('hardwareBackPress', () => {closeFilters(filtersRef.current); return true;});
    return () => subscription.remove();
  }, [docked, over, closeFilters]);
  const panel = filterDraft && <LibraryFilters key={filterOpening.current} draft={filterDraft} source={state} runtime={runtime} labels={props.labels} owner={props.owner}
    onClose={() => closeFilters(filterDraft)} docked geometry={libraryFiltersTabletPane}/>;
  // The phone sheet and the TV panel own their exit motion, so their draft stays
  // mounted while it closes; the tablet panes have no exit motion to finish.
  const sheet = filterDraft && !tabletLayout && <LibraryFilters key={filterOpening.current} draft={filterDraft} source={state} runtime={runtime} labels={props.labels} owner={props.owner}
    onClose={() => filterDraft?.dispose()} onSave={view => actions.saveView(destination, view)}/>;
  return <View testID={`native-library-${encodeURIComponent(state.library.id)}`} pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible}
    importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'} onLayout={event => setAreaWidth(event.nativeEvent.layout.width)}
    style={{flex: 1}}><StandardBackdrop/>
    {wash}
    <View style={{flex: 1, flexDirection: docked ? 'row' : 'column'}}>
      <View style={{flex: 1}}>
        {header}
        {/* Counts and covers that did not load are a quiet note, never a second full error. */}
        {summaries.phase === 'error' && !pageFailed && <View style={{paddingHorizontal: theme.isTV ? 0 : theme.gutter, paddingVertical: 8}}><Banner placement="inline" variant="notice" tone="warning" icon="i-alert"
          message={defaultI18n.t('next.browse.librarySummariesUnavailable')}
          action={theme.isTV ? undefined : {label: defaultI18n.t('next.states.tryAgain'), onPress: () => {void directory.load().catch(runtime.reportError);}}}/></View>}
        {state.phase === 'error' ? <FocusRegion id={errorRegion} entry="retry" order={['retry']} edges={{up: ids.libraryTabs(state.library.id), left: ids.rail}} style={{flex: 1, justifyContent: 'center'}}>
          <StateView variant="centered" {...runtime.error(state.error, retryPage, 'library')}/></FocusRegion>
          : state.collection ? <View style={{flex: 1}}>
            <LibraryGrid {...props} selection={selection} source={state} paneWidth={gridWidth} tablet={tabletLayout} expanded={expanded} filtersOpen={filterState?.open ?? false} onFilters={openFilters} dockedFilters={docked}
              entryRequest={entryRequest} onEntered={onEntered} entryCovered={emptyShown}/>
            {emptyShown && <View testID="library-empty-page" style={{position: 'absolute', inset: 0}}><StandardBackdrop/>
              <LibraryEmpty runtime={runtime} regionId={emptyRegion} upRegionId={ids.libraryTabs(state.library.id)} firstCardRegion={firstCardRegion}
                surface={state.tab.shows}
                onLeave={() => {if (!empty) setEmptyHeld(false);}}/>
            </View>}
          </View>
          : loading ? <LibraryLoading theme={theme} width={theme.isTV ? dimensions.width : contentWidth} height={dimensions.height} tablet={tabletLayout} expanded={expanded} elapsedMs={loadingElapsed}/>
          : emptyShown ? <LibraryEmpty runtime={runtime} regionId={emptyRegion} upRegionId={ids.libraryTabs(state.library.id)} surface={state.tab.shows}/>
          : theme.isTV && state.library.kind === 'music' && state.tab.id === 'discover' ? <TVMusicDiscover
            destination={destination} state={state} runtime={runtime} active={active} anchor={props.mixAnchor} onEntered={onEntered}
            renderEntry={(entry, width, context) => <LibraryCard entry={entry} width={width} runtime={runtime} actions={actions} focusId={context.focusId} disabled={!active.visible} onFocus={context.onFocus} onBlur={context.onBlur}/>}/>
          : state.tab.shows === 'rows' ? <ScrollView ref={discoverScroll}>
            {state.discover.error && <StateView {...runtime.error(state.discover.error, destination.retry, 'library')}/>}
            {state.discover.appended.map((section, index) => {const cardWidth = theme.isTV ? 200 : 112, cell = discoverCell(theme, cardWidth, section.entries);
            return <View key={section.id}>
            <Text accessibilityRole="header" style={[browseType(theme, theme.isTV ? 32 : 19, 700), {color: theme.colors.text, marginHorizontal: screenGutter, marginTop: 28}]}>{serverTextLabel(defaultI18n, section.heading)}</Text>
            {/* LR05: the shelf mounts only the cards in view and asks for its next page ten cards early. */}
            <Row title={serverTextLabel(defaultI18n, section.heading)} showHeading={false} items={section.entries} getKey={nativeLibraryEntryKey}
              itemWidth={cardWidth} itemHeight={cell.art} captionHeight={cell.caption} gap={theme.isTV ? 28 : 10} gutter={screenGutter} rightGutter={screenGutter} selection={noRowSelection}
              onVisibleRange={(_first, last) => {
                if (active.visible && section.nextCursor && last >= section.entries.length - 10) void destination.nextDiscover(section.id).catch(runtime.reportError);
              }}
              renderItem={(entry, context) => <LibraryCard entry={entry} width={cardWidth} runtime={runtime} actions={actions} focusId={context.focusId} onFocus={context.onFocus} onBlur={context.onBlur}/>}/>
            {!theme.isTV && state.library.kind === 'music' && index === 0 && runtime.mixes ? <View onLayout={anchorMixes}>
              <MusicMixesRow focusScope={`${ids.libraryToolbar(state.library.id)}.${state.tab.id}.mixes`} port={runtime.mixes} artwork={runtime.artwork} reportError={runtime.reportError} title={defaultI18n.t('music.mixesForYou')} active={active.visible} onReady={scrollToMixes}/>
            </View> : null}
          </View>;})}
            {!theme.isTV && state.library.kind === 'music' && !state.discover.appended.length && runtime.mixes ? <View onLayout={anchorMixes}>
              <MusicMixesRow focusScope={`${ids.libraryToolbar(state.library.id)}.${state.tab.id}.mixes`} port={runtime.mixes} artwork={runtime.artwork} reportError={runtime.reportError} title={defaultI18n.t('music.mixesForYou')} active={active.visible} onReady={scrollToMixes}/>
            </View> : null}
          </ScrollView> : null}
      </View>
      {docked && <View testID="library-filters-dock" style={{width: libraryFiltersDockWidth, backgroundColor: theme.colors.raised,
        borderLeftWidth: 1, borderLeftColor: alpha(theme.colors.text, .07)}}>{panel}</View>}
    </View>
    {over && filterDraft && <LibraryFiltersOverPanel open={!overClosing} width={libraryFiltersOverWidth} onDismiss={() => closeFilters(filterDraft)}
      onExited={() => dropFilters(filterDraft)}>{panel}</LibraryFiltersOverPanel>}
    {sheet}
  </View>;
}

/** The drawn loading page: the real header and tabs are already above it, and the
 * count and posters are the only unknown shapes. Skeleton positions use the same
 * grid geometry as the loaded page, so nothing jumps when the first page lands. */
function LibraryLoading({theme, width, height, tablet, expanded, elapsedMs}: {theme: Theme; width: number; height: number; tablet: boolean; expanded: boolean; elapsedMs: number}) {
  const tv = theme.isTV;
  const tabletGeometry = tablet ? libraryTabletGridGeometry(width, {expanded, rail: false}) : undefined;
  const columns = tv ? 7 : tabletGeometry ? tabletGeometry.columns : 3, gap = tv ? 32 : tabletGeometry ? tabletGeometry.gap : 10;
  const cardWidth = tv ? 200 : tabletGeometry ? tabletGeometry.itemWidth : Math.floor((width - 62) / 3);
  const gutter = tv ? theme.gutter : tabletGeometry ? tabletGeometry.gutter : 16, right = tv ? theme.gutter : tabletGeometry ? tabletGeometry.rightGutter : 16;
  const contentWidth = tv ? Math.max(0, width - theme.gutter - 72) : tabletGeometry ? Math.max(0, width - gutter - right) : Math.max(0, width - 32);
  return <View testID="library-loading" style={{flex: 1, overflow: 'hidden'}}>
    {showSkeleton(elapsedMs) && <View style={{paddingLeft: gutter, paddingRight: right, marginTop: tv ? 24 : 10, marginBottom: tv ? 22 : 12}}>
      <SkeletonBlock width={tv ? 160 : 80} height={tv ? 22 : 11} radius={tv ? 11 : 6}/>
    </View>}
    <View style={{paddingLeft: gutter, paddingRight: right}}>
      <GridSkeleton testID="library-loading-grid" elapsedMs={elapsedMs} scrollOffset={0} viewportHeight={Math.max(0, height - (tv ? 300 : 240))}
        width={contentWidth} columns={columns} cardWidth={cardWidth} gap={gap} rowGap={tv ? 40 : tabletGeometry ? tabletGeometry.rowGap : 22} totalCount={columns * 3}/>
    </View>
  </View>;
}

/** tv-focus 46: the composed empty-library page. Header and pivot strip stay
 * above it; the one action enters first and leads Home. */
function LibraryEmpty({runtime, regionId, upRegionId, firstCardRegion, onLeave, surface}: {runtime: NativeShellRuntime; regionId: string; upRegionId: string; firstCardRegion?: string; onLeave?: () => void; surface?: NativeLibrarySnapshot['tab']['shows']}) {
  const ids = nativeDestinationFocusIds(runtime.getSnapshot().authority);
  return <FocusRegion id={regionId} entry="browse-home" order={['browse-home']} edges={{up: upRegionId, left: ids.rail,
    ...(firstCardRegion ? {down: firstCardRegion, right: firstCardRegion} : {})}} style={{flex: 1, justifyContent: 'center'}}>
    <StateView testID="library-empty" variant="centered"
      title={defaultI18n.t(surface === 'genres' ? 'library.genres.empty' : surface === 'playlists' ? 'next.selection.native.empty.playlist' : 'library.emptyLibrary')}
      body={defaultI18n.t(surface === 'genres' ? 'library.genres.emptyBody' : surface === 'playlists' ? 'web.saved.playlistEmptyBody' : 'library.emptyLibraryBody')}>
      <Button focusId="browse-home" testID="library-empty-browse-home" variant="primary" size="hero" onBlur={onLeave}
        onPress={() => runtime.onDestination({kind: 'home'})}>{defaultI18n.t('saved.browseHome')}</Button>
    </StateView>
  </FocusRegion>;
}

function LibraryGridRegion(props: FocusRegionProps) {
  return useTheme().isTV ? <FocusRegion {...props}/> : <>{props.children}</>;
}

export function LibraryGrid({destination, runtime, activation, actions, labels, selection, owner, source, paneWidth, tablet, expanded, filtersOpen, onFilters, dockedFilters, entryRequest, onEntered, entryCovered = false}: NativeLibraryScreenProps & {source: NativeLibrarySnapshot; /** The live width of this route's column, after the rail or sidebar and any docked pane. */ paneWidth: number; /** True on the medium and expanded tablet layouts, where the drawn grid is not the phone's. */ tablet: boolean; /** The one expanded classification the skeleton uses too: 1024 and wider. */ expanded: boolean; /** The route owns the filter draft, so the panel is drawn beside or over this grid. */ filtersOpen: boolean; onFilters: () => void; /** True while that panel is docked beside the grid. */ dockedFilters: boolean; entryRequest: ReturnType<typeof createDestinationEntryFocus>; onEntered: () => void; entryCovered?: boolean}) {
  const theme = useTheme(), registry = useFocusRegistry(), grid = useRef<WindowedGridHandle>(null);
  const collection = source.collection!, state = useSyncExternalStore(collection.subscribe, collection.getSnapshot);
  const pull = usePullRefresh(() => collection.revalidate(), runtime.reportError);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const resourceGrid = source.tab.shows === 'genres' || source.tab.shows === 'playlists';
  const memoryKey = JSON.stringify([source.library.id, source.tab.id]);
  const restoring = useRef(theme.isTV && resourceGrid && active.intent === 'restore' ? resourceFocusMemory.get(runtime)?.get(memoryKey) : undefined);
  const initialIndex = useRef(restoring.current?.index ?? source.memory.firstVisibleIndex).current;
  const openingIndex = state.total === undefined ? initialIndex : Math.min(initialIndex, Math.max(0, state.total - 1));
  const openingPage = Math.floor(openingIndex / collection.pageSize);
  const pageSubscribe = useCallback((listener: () => void) => collection.subscribePage(openingPage, listener), [collection, openingPage]);
  const pageVersion = useCallback(() => collection.getPageVersion(openingPage), [collection, openingPage]);
  useSyncExternalStore(pageSubscribe, pageVersion);
  const selected = useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? noSelection);
  const authority = runtime.getSnapshot().authority;
  const ids = useMemo(() => nativeDestinationFocusIds(authority), [authority]);
  const first = restoring.current ? collection.residentItems().find(entry => nativeLibraryEntryKey(entry) === restoring.current!.key) ?? collection.itemAt(openingIndex) : collection.itemAt(0);
  const gridRegion = ids.libraryGrid(source.library.id, source.tab.id);
  const firstKey = first && nativeLibraryEntryKey(first);
  const attachedStart = firstKey && theme.isTV ? registry.entityAddress(gridRegion, firstKey) : undefined;
  const firstAddress = firstKey ? attachedStart ?? {regionId: theme.isTV ? gridRegion : ids.libraryCard(source.library.id, firstKey), childId: firstKey} : undefined;
  const top = useCallback(() => grid.current?.scrollToIndex(0), []);
  useEffect(() => {if (source.queryRevision > 0) top();}, [source.queryRevision, top]);
  const entered = useCallback(() => {if (restoring.current) grid.current?.scrollToIndex(openingIndex); else top(); onEntered();}, [onEntered, top, openingIndex]);
  const tvActivation = useMemo(() => ({...active, intent: resourceGrid && active.intent === 'restore' ? 'enter' as const : active.intent,
    visible: active.visible && theme.isTV && !entryCovered}), [active, entryCovered, resourceGrid, theme.isTV]);
  useDestinationEntryFocus(tvActivation, firstAddress, entered, entryRequest);
  useTVPageBack(runtime, {kind: 'library', libraryId: source.library.id}, theme.isTV, {
    registerTop: true, scope: JSON.stringify([active.revision, source.queryRevision, source.tab.id]),
    current: () => activation.getSnapshot().visible && !entryCovered,
    start: () => {const entry = collection.itemAt(0), key = entry && nativeLibraryEntryKey(entry);
      return key ? registry.entityAddress(gridRegion, key) ?? {regionId: gridRegion, childId: key} : undefined;},
    atTop: () => offset.current <= 0, scrollTop: top,
  });
  useEffect(() => !theme.isTV ? runtime.registerScrollToTop({kind: 'library', libraryId: source.library.id}, () => {
    top(); const entry = collection.itemAt(0), key = entry && nativeLibraryEntryKey(entry);
    if (key) registry.focus({regionId: ids.libraryCard(source.library.id, key), childId: key});
  }) : undefined, [collection, ids, registry, runtime, source.library.id, top, theme.isTV]);
  const toolbar = useMemo(() => browseToolbarModel(source.capabilities!, source.library.kind, source.predicates, source.sort!, {...runtime.text, owner, textEntry: !theme.isTV}),
    [owner, runtime.text, source.capabilities, source.library.kind, source.predicates, source.sort, theme.isTV]);
  const [visible, setVisible] = useState({first: source.memory.firstVisibleIndex, last: source.memory.firstVisibleIndex});
  const geometry = useRef(0), [view, setView] = useState<{mode: 'grid' | 'list'; anchor: number; generation: number}>({mode: 'grid', anchor: source.memory.firstVisibleIndex, generation: 0});
  const list = !theme.isTV && view.mode === 'list';
  const [jumpOpen, setJumpOpen] = useState(false), jumpAnchor = useRef<View>(null);
  const jumpLetters = useMemo(() => libraryJumpLetters(state.positionIndex), [state.positionIndex]);
  const jumpLetter = useMemo(() => libraryJumpLetter(jumpLetters, visible.first), [jumpLetters, visible.first]);
  const offset = useRef(destination.getMemory().offset), range = useRef(visible), registered = useRef<(() => void) | undefined>(undefined);
  useEffect(() => {const held = registered; return () => {held.current?.(); held.current = undefined;};}, [collection]);
  const changedRange = useCallback((firstIndex: number, lastIndex: number) => {
    if (geometry.current !== view.generation) return;
    range.current = {first: firstIndex, last: lastIndex}; setVisible(range.current); destination.remember(firstIndex, offset.current);
    const entries: ContentEntry[] = [];
    for (let index = firstIndex; index <= lastIndex; index++) {const entry = collection.itemAt(index); if (entry) entries.push(entry);}
    registered.current?.(); registered.current = selection?.register(destination.getSnapshot(), entries, firstIndex, lastIndex);
  }, [collection, destination, selection, view.generation]);
  const changedOffset = useCallback((measured: number) => {if (geometry.current !== view.generation) return; offset.current = measured; destination.remember(range.current.first, measured);}, [destination, view.generation]);
  const changeView = (mode: 'grid' | 'list') => {
    if (!active.visible || theme.isTV || mode === view.mode) return;
    geometry.current++; offset.current = 0;
    destination.remember(range.current.first, 0);
    setView({mode, anchor: range.current.first, generation: geometry.current});
  };
  const base = source.tab.fixedFilter ?? [], filtered = JSON.stringify(source.predicates) !== JSON.stringify(base);
  const applied = libraryAppliedFilters(toolbar.applied.filter(node => !base.some(fixed => JSON.stringify(fixed) === JSON.stringify(node))), source.capabilities!, labels);
  const appliedRegion = `${ids.libraryToolbar(source.library.id)}.applied`;
  const alphabet = !!state.positionIndex && state.total !== undefined && source.sort?.field === 'title' && source.sort.direction === 'asc' && !source.predicates.length;
  const tabletGeometry = tablet ? libraryTabletGridGeometry(paneWidth, {expanded, rail: alphabet}) : undefined;
  const total = source.unfilteredTotal;
  const count = state.total === undefined ? undefined : filtered && total
    ? defaultI18n.t('title.shownOfTotal', {shown: `${defaultI18n.number(state.total)}${state.totalIsLowerBound ? defaultI18n.t('next.browse.native.countPlus') : ''}`,
      total: defaultI18n.t(total.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: total.total, pivot: source.tab.pivot ?? 'other'})})
    : defaultI18n.t(state.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: state.total, pivot: source.tab.pivot ?? 'other'});
  const genres = source.tab.shows === 'genres', square = source.library.kind === 'music' || source.library.kind === 'audiobook';
  const width = list ? Math.max(0, paneWidth - (tabletGeometry ? 2 * tabletGeometry.gutter : 32)) : theme.isTV ? genres ? 320 : 200 : tabletGeometry ? tabletGeometry.itemWidth : Math.floor((paneWidth - 62) / 3);
  const artHeight = genres ? theme.isTV ? 180 : 84 : width * (square ? 1 : 1.5);
  const captionLines = theme.spec.card.titleMargin[theme.platform] + 2 * (theme.type.cardTitle.lineHeight ?? 28 * theme.textScale)
    + theme.spec.card.subtitleMargin[theme.platform] + (theme.type.caption.lineHeight ?? 24 * theme.textScale);
  const captionSpace = square ? captionLines : theme.isTV ? 72 : 43;
  // LibraryCard allows two title lines. Keep the authored default, but reserve
  // both lines plus metadata when text grows and long titles wrap. The theme's
  // line heights already include the TV preference; apply only the OS factor.
  const itemHeight = genres ? artHeight : artHeight + (theme.fontScale > 1 || theme.textScale > 1 ? Math.max(captionSpace, captionLines) * theme.fontScale : captionSpace);
  const gap = list ? 0 : theme.isTV ? 32 : tabletGeometry ? tabletGeometry.gap : 10;
  const rowGap = list ? 0 : theme.isTV ? 40 : tabletGeometry ? tabletGeometry.rowGap : 22;
  const gutter = theme.isTV ? theme.gutter : tabletGeometry ? tabletGeometry.gutter : 16;
  const rightGutter = list ? gutter : theme.isTV ? 72 : tabletGeometry ? tabletGeometry.rightGutter : 26;
  const [measuredColumns, setMeasuredColumns] = useState(1);
  const onGridGeometry = useCallback((columns: number) => setMeasuredColumns(columns), []);
  const rowGutter = tabletGeometry?.gutter ?? theme.gutter;
  return <View style={{flex: 1}}>
    {selection && selected?.active ? selection.render(path => {const art = path ? runtime.artwork(path) : undefined;
      return art ? <Artwork source={art} width={48} height={48}/> : undefined;}) : <FocusRegion id={ids.libraryToolbar(source.library.id)} entry={theme.isTV ? 'sort' : 'filters'} orientation="horizontal" enabled={active.visible} edges={{left: ids.rail, up: ids.libraryTabs(source.library.id), ...(theme.isTV ? {down: applied.length ? appliedRegion : gridRegion} : {})}}>
      <LibraryToolbar focusScope={ids.libraryToolbar(source.library.id)} model={toolbar} labels={labels} filterCount={source.predicates.length} filtersOpen={filtersOpen} docked={dockedFilters} gutter={tabletGeometry?.gutter}
        showSelect={!expanded}
        trailing={theme.isTV || theme.platform === 'phone' ? undefined : <>{count && !dockedFilters ? <Text style={[browseType(theme, 14.5, 600), {color: theme.colors.textSecondary, marginRight: 6}]}>{count}</Text> : null}<ViewToggle value={view.mode} onValueChange={changeView}/></>}
        canSave={nativeLibraryCanSave(source, {predicates: source.predicates, sort: source.sort!})}
        onFilters={onFilters}
        onSort={sort => destination.setQuery(source.predicates, sort)} onReshuffle={() => destination.setQuery(source.predicates, {...source.sort!, seed: Math.floor(Math.random() * 4294967296)})}
        onQuick={destination.setQuick} onSave={() => actions.saveView(destination, {predicates: source.predicates, sort: source.sort!})}
        onJump={() => setJumpOpen(true)} jumpEnabled={!!state.positionIndex && !!Object.keys(state.positionIndex).length} jumpAnchor={jumpAnchor}
        onSelect={selection?.start} selecting={selected?.active ?? false}/>
    </FocusRegion>}
    {!theme.isTV && theme.platform !== 'phone' && !dockedFilters && toolbar.quick.length > 0 && <FocusRegion id={`${ids.libraryToolbar(source.library.id)}.quick`} entry={`quick:${toolbar.quick[0].id}`} orientation="horizontal" enabled={active.visible}
      edges={{left: ids.rail, up: ids.libraryToolbar(source.library.id)}} style={{paddingHorizontal: rowGutter, paddingTop: 12, flexDirection: 'row', gap: 8}}>
      {toolbar.quick.map(preset => <FocusItem key={preset.id} focusId={`quick:${preset.id}`}><QuickToggle checked={preset.active} onCheckedChange={enabled => destination.setQuick(preset.id, enabled)}>{preset.label?.text ?? labels.quick(preset)}</QuickToggle></FocusItem>)}
    </FocusRegion>}
    {!!applied.length ? <FocusRegion id={appliedRegion} entry={applied[0].id} orientation="horizontal" enabled={active.visible}
      edges={{left: ids.rail, up: ids.libraryToolbar(source.library.id), ...(firstKey ? {down: theme.isTV ? gridRegion : ids.libraryCard(source.library.id, firstKey)} : {})}}
      style={{paddingLeft: theme.isTV ? theme.gutter : rowGutter, paddingRight: theme.isTV ? theme.layout.gutter.tvRight : rowGutter, paddingTop: theme.isTV ? 24 : tablet ? 12 : 10, paddingBottom: theme.isTV ? 14 : theme.platform === 'phone' ? 0 : 10, flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 12 : 8, flexWrap: 'wrap'}}>
      {applied.map(filter => <Focusable key={filter.id} focusId={filter.id} disabled={!active.visible} accessibilityLabel={defaultI18n.t('next.browse.native.removeFilter', {field: filter.fieldName})}
        onPress={() => destination.setQuery(source.predicates.filter(node => JSON.stringify(node) !== filter.id))}
        style={{height: theme.isTV ? 52 : tablet ? 44 : 30, borderRadius: theme.isTV ? 14 : tablet ? 20 : theme.platform === 'phone' ? 9 : 17, paddingLeft: theme.isTV ? 20 : tablet ? 14 : 11, paddingRight: theme.isTV ? 20 : tablet ? 6 : theme.platform === 'phone' ? 8 : 7, flexDirection: 'row', alignItems: 'center', gap: 7, backgroundColor: theme.isTV ? alpha(theme.colors.accent, .18) : theme.platform === 'phone' ? theme.colors.raisedHigh : theme.colors.raisedBright,
          borderWidth: theme.isTV ? 1.5 : 0, borderColor: alpha(theme.colors.accentStrong, .5)}}>
        <Text numberOfLines={1} style={[browseType(theme, theme.isTV ? 20 : tablet ? 14 : 13, tablet ? 700 : 600), {color: theme.colors.text}]}>{filter.label}</Text><Icon name="i-close" size={theme.isTV ? 22 : tablet ? 16 : 14} color={theme.colors.textSecondary}/>
      </Focusable>)}
      <Focusable focusId="clear" disabled={!active.visible} onPress={() => destination.setQuery(base as readonly BrowseNode[])} style={{height: theme.isTV ? 52 : tablet ? 44 : 30, justifyContent: 'center', paddingHorizontal: 8}}>
        <Text style={[browseType(theme, theme.isTV ? 20 : tablet ? 14 : 13, tablet ? 700 : 600), {color: theme.colors.textTertiary}]}>{defaultI18n.t('next.browse.native.clearAll')}</Text>
      </Focusable>
      {/* tv4 .active-f .count sits 18 after Clear all; phone and tablet keep the right edge. */}
      {count && theme.platform !== 'phone' && (theme.isTV || dockedFilters) && <Text style={[browseType(theme, theme.isTV ? 22 : 13, 600), {color: theme.colors.textTertiary, marginLeft: theme.isTV ? 18 : 'auto'}]}>{count}</Text>}
    </FocusRegion> : count && theme.platform !== 'phone' && (theme.isTV || dockedFilters) && <Text style={[browseType(theme, theme.isTV ? 22 : 13, 600), {color: theme.colors.textTertiary, marginHorizontal: theme.isTV ? theme.gutter : rowGutter, marginTop: theme.isTV ? 24 : 10, marginBottom: theme.isTV ? 40 : 10}]}>{count}</Text>}
    {count && theme.platform === 'phone' && <LibraryResultCount phone controls={<ViewToggle value={view.mode} onValueChange={changeView}/>}>{count}</LibraryResultCount>}
    {state.status === 'error' && <StateView {...runtime.error(state.error, destination.retry, 'library')}/>}
    <LibraryGridRegion id={gridRegion} enabled={active.visible} entry={firstAddress?.childId ?? firstKey} grid={theme.isTV ? {columns: measuredColumns, count: state.total} : undefined}
      edges={theme.isTV ? {left: ids.rail, up: applied.length ? appliedRegion : ids.libraryToolbar(source.library.id)} : undefined}
      screenStart={{regionId: ids.libraryToolbar(source.library.id), childId: theme.isTV ? 'sort' : 'filters'}} style={{flex: 1}}>
    <WindowedGrid key={view.generation} ref={grid} testID={list ? "library-list" : "library-grid"} collection={collection} getKey={nativeLibraryEntryKey} itemWidth={width} itemHeight={list ? 70 : itemHeight} captionHeight={list ? 70 : 0}
      gap={gap} rowGap={rowGap} gutter={gutter} rightGutter={rightGutter} selection={selected}
      onVisibleRange={changedRange} onScrollOffset={changedOffset} refreshControl={pull} onGeometry={theme.isTV ? onGridGeometry : undefined} initialIndex={view.generation ? view.anchor : initialIndex} paddingBottom={28} style={{flex: 1}}
      renderPlaceholder={() => <View style={{width, height: list ? 70 : artHeight, borderRadius: theme.isTV ? 14 : 8, backgroundColor: theme.colors.raised}}/>}
      renderNativeSlot={theme.isTV ? (entry, context) => <FocusItem focusId={context.focusId} entityId={context.entityId} waiting={context.waiting}
        cell={{index: context.index, pending: context.waiting, generation: context.source}}>
        <LibraryCard entry={entry} width={width} placeholderHeight={artHeight} runtime={runtime} actions={actions} disabled={!active.visible}
          selected={context.selection} focusId={context.focusId} onFocus={() => {
            if (entry && resourceGrid && active.visible) {
              let memory = resourceFocusMemory.get(runtime);
              if (!memory) {memory = new Map(); resourceFocusMemory.set(runtime, memory);}
              memory.delete(memoryKey); memory.set(memoryKey, {key: nativeLibraryEntryKey(entry), index: context.index});
              if (memory.size > 64) memory.delete(memory.keys().next().value!);
            }
            context.onFocus();
          }} onBlur={context.onBlur} onSelect={context.selection?.onEnter ?? selection?.start}/>
      </FocusItem> : undefined}
      renderItem={(entry, context) => {
        const key = nativeLibraryEntryKey(entry);
        const focused = () => {
          if (resourceGrid && theme.isTV && active.visible) {
            let memory = resourceFocusMemory.get(runtime);
            if (!memory) {memory = new Map(); resourceFocusMemory.set(runtime, memory);}
            memory.delete(memoryKey); memory.set(memoryKey, {key, index: context.index});
            if (memory.size > 64) memory.delete(memory.keys().next().value!);
          }
          context.onFocus();
        };
        const card = list ? <LibraryListRow entry={entry} width={width} runtime={runtime} actions={actions} disabled={!active.visible} selected={context.selection} focusId={key} onFocus={focused} onBlur={context.onBlur} onSelect={context.selection?.onEnter ?? selection?.start}/> : <LibraryCard entry={entry} width={width} runtime={runtime} actions={actions} disabled={!active.visible} selected={context.selection} focusId={key} onFocus={focused} onBlur={context.onBlur} onSelect={context.selection?.onEnter ?? selection?.start}/>;
        return theme.isTV ? <FocusItem focusId={key} cell={{index: context.index}}>{card}</FocusItem>
          : <FocusRegion id={ids.libraryCard(source.library.id, key)} entry={key} entryPolicy="entry" order={[key]} enabled={active.visible}>{card}</FocusRegion>;
      }}/>
    </LibraryGridRegion>
    {alphabet && state.positionIndex !== undefined && state.total !== undefined && <AlphabetRail positionIndex={state.positionIndex}
      total={state.total} firstVisible={visible.first} lastVisible={visible.last} testID="library-alphabet" onJump={index => grid.current?.scrollToIndex(index)}
      height={theme.isTV ? 514 : 480} style={{position: 'absolute', right: theme.isTV ? 40 : 2, top: theme.isTV ? 174 : 110}}/>}
    <LibraryJumpSheet open={jumpOpen} panelId={`${ids.libraryToolbar(source.library.id)}.jump`} letters={jumpLetters} selected={jumpLetter}
      onSelect={key => {setJumpOpen(false); grid.current?.scrollToLetter(key);}} onClose={() => setJumpOpen(false)}/>
  </View>;
}

function LibraryCard({entry, width, placeholderHeight, runtime, actions, selected, disabled, focusId, onFocus, onBlur, onSelect}: {entry?: ContentEntry; width: number; placeholderHeight?: number; runtime: NativeShellRuntime; actions: NativeLibraryActions; selected?: CardSelection; disabled?: boolean; focusId?: string; onFocus?: () => void; onBlur?: () => void; onSelect?: () => void}) {
  const menuAnchorRef = useRef<View>(null);
  const theme = useTheme();
  const adapter = entry ? runtime.playback : undefined;
  const playing = useSyncExternalStore(adapter?.subscribe ?? (() => () => {}), adapter?.getSnapshot ?? (() => undefined));
  const renderCard = <P extends CardProps & React.Attributes,>(Component: React.ComponentType<P>, props: P) => theme.isTV
    ? <TVSparseCardFrame {...props}><Component {...props} frame={TVCardPaintFrame}/></TVSparseCardFrame> : <Component {...props}/>;
  if (!entry) return <TVSparseCardFrame title="" width={width} disabled={disabled} focusId={focusId} onFocus={onFocus} onBlur={onBlur} menuAnchorRef={menuAnchorRef}>
    <TVPendingCardPaint height={placeholderHeight ?? width * 1.5}/>
  </TVSparseCardFrame>;
  // All live variants delegate only paint; the one outer native CardFrame survives a fill/kind change.
  if (entry.kind === 'radio' && entry.radioSeed) return renderCard(SquareCard, {focusId, disabled,
    title: defaultI18n.t('music.radioNamed', {name: entry.title}), width, titleLines: 2,
    caption: [{id: 'radio', role: 'value' as const, text: defaultI18n.t('music.radio')}],
    artwork: runtime.artwork(entry.posterUrl), onNavigate: () => actions.play(entry), onFocus, onBlur});
  if (entry.kind === 'playlist' || entry.kind === 'category' && entry.navigation?.browse?.pivot === 'albums') {
    const covers = (entry.artworkPaths ?? []).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];});
    const caption = entry.count === undefined ? [] : [{id: 'songs', role: 'value' as const,
      text: defaultI18n.t(entry.countIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: entry.count, pivot: 'songs'})}];
    const resource = {focusId, disabled, width, title: entry.title, caption, covers, onNavigate: () => actions.open(entry), onFocus, onBlur};
    return entry.kind === 'playlist' ? renderCard(PlaylistMosaic, {...resource, titleLines: 2})
      : renderCard(GenreTile, {...resource, variant: 'music' as const, height: theme.isTV ? 180 : 84});
  }
  const text = homeCardText(entry, false, runtime.text);
  const playingAlbum = entry.kind === 'album' && playing?.entry?.album?.id === entry.id;
  const props = {focusId, disabled, title: entry.displayTitle || entry.title, width, titleLines: 2 as const, caption: text.caption, artwork: runtime.artwork(entry.posterUrl), watched: entry.watched, playing: playingAlbum,
    onNavigate: () => actions.open(entry), onPlay: entry.playback ? () => actions.play(entry) : undefined, onMore: () => actions.menu(entry, menuAnchorRef, onSelect), selection: selected,
    menuAnchorRef, moreExpanded: actions.isMenuOpen?.(entry), onFocus, onBlur};
  const artistCovers = (entry.artworkPaths ?? []).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];});
  return entry.kind === 'artist' ? renderCard(CircleCard, {...props, art: {covers: artistCovers.length ? artistCovers : props.artwork ? [props.artwork] : [], alt: entry.title}})
    : ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind) ? renderCard(SquareCard, props) : renderCard(PosterCard, props);
}
