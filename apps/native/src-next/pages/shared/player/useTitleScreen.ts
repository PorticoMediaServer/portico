import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {ScrollView, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {TextEnvironment} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {EpisodeTileProps} from '../../../kit/cards';
import {useOptionalFocusRegistry} from '../../../kit/focus';
import {tvFocusIds} from '../../../shell/tv-focus';
import type {ShellAuthority} from '../../../shell/model';
import {tvTitleRegionMap, tvTitleFocusSection} from '../../../screens/watch/tv-title-focus';
import {useMotion} from '../../../kit/useMotion';
import {useTheme} from '../../../kit/theme';
import type {NativeTitleController, NativeTitlePageLease} from '../../../screens/watch/title-controller';
import type {NativeTitleTab} from '../../../screens/watch/title-memory';
import type {NativePlaybackAdapter, NativePlaybackRuntimeSnapshot} from '../../../platform/playback/types';
import {usePlatformPosture} from '../../../platform/platformPosture';
import {mediumRailMetrics} from '../../../shell/model';
import {usePageInset} from '../../../kit/layout/page-inset';
import type {NativePlayOn} from '../../../platform/play-on/controller';

export type NativeTitleScreenProps = Readonly<{
  controller: NativeTitleController;
  /** The route occurrence this page is drawn for (TV names its season and episode focus regions by it). */
  visit?: string;
  authority?: ShellAuthority;
  text: TextEnvironment;
  artwork: (path: string | undefined) => ArtworkSource | undefined;
  ambient: (path: string | undefined) => AmbientArtSource | undefined;
  back: () => void;
  navigate: (entry: ContentEntry) => void;
  more: (entry: ContentEntry, anchorRef?: React.RefObject<View | null>) => void;
  reportError: (error: unknown) => void;
  renderError: (error: unknown, retry: () => void) => ReactNode;
  /** The title's whole-page load failure (W-2): backdrop, clear of the status bar and Back. Falls back to renderError. */
  renderPageError?: (error: unknown, retry: () => void) => ReactNode;
  /** Exact approved About label from the canonical catalogue owner. */
  aboutLabel: string;
  /** The saved server display for a named gone-title state; never a synthesized name. */
  serverName?: string;
  /** One ordinary Search route seeded with the captured title; absent when no producer exists. */
  search?: (query: string) => void;
  /** The Home entry's captured Remove from Continue Watching command, when it offered one. */
  removeContinue?: (signal: AbortSignal) => Promise<boolean>;
  /** A real native download action, absent on a server/device where it is not offered. */
  download?: (entry: ContentEntry) => void;
  /** tablet6/8: the real Play on control. Present only while this device has a
   * local playback the shell can hand off; the screen never invents one. */
  onPlayOn?: (anchorRef?: React.RefObject<View | null>) => void;
  /** The one Play on controller, for the top bar's shared button and its chosen-device state. */
  playOn?: NativePlayOn;
  /** A registered person route, where the shell has one. */
  person?: (personId: string) => void;
  /** The Search genre page for one of the title's genres (its tag id is the genre's id). */
  genre?: (genre: Readonly<{id: string; name: string}>) => void;
  /** The signed-in viewer owns this server; Show settings is drawn for owners. */
  owner?: boolean;
  /** A real catalogue collection named by the title's detail projection. */
  openCollection?: (collection: Readonly<{id: string; name: string; libraryId: string; memberCount: number; posterUrls: readonly string[]}>) => void;
  /** Current producer state and command, never a fabricated default grant. */
  myList?: Readonly<{selected: boolean; change: (selected: boolean) => Promise<void>}>;
  /** Live captured entry from the actual menu owner, not local open state. */
  menu?: Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => ContentEntry | undefined}>;
  /** Keep the actual selection port even while inactive for accessibility. */
  selection?: (entry: ContentEntry) => EpisodeTileProps['selection'];
  /** The page's own selection bar while a card is selected (DESIGN: Select in every card's hold-OK menu). */
  selectionBar?: ReactNode;
  /** The upright Watch page reuses the retained real title body. Its Play/Pause
   * controls the exact existing session; it never starts another queue. */
  watch?: Readonly<{source: NativePlaybackRuntimeSnapshot; width: number; playback: Pick<NativePlaybackAdapter, 'getSnapshot' | 'toggle'>;
    /** foldable3g: the book posture's left page draws the title block alone and its
     * right page the tabs and episodes; the ordinary watch page keeps drawing both. */
    sections?: 'all' | 'title' | 'episodes'}>;
}>;
const noMenuSubscribe = () => () => {};
const noMenuSnapshot = () => undefined;
const noFocusSnapshot = () => 0;
/** TV: while browsing, the focused row's top sits here, under the compact title and tabs (tv.html frame 6). Rows can always
 * reach it (the body leaves room under the last one), and what scrolls above it is hidden behind the header's ink band. */
export const tvBrowseTop = 252;

/** The title route's state: the controller's snapshot, the measured layout and art, the TV focus sections and alignment,
 * the Continue removal and the seasons sheet. Moved from screens/watch/TitleScreen as it was; `TitleScreenView` draws
 * what this returns. */
export function useTitleScreen(props: NativeTitleScreenProps) {
  const {controller, text} = props, snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const menuEntry = useSyncExternalStore(props.menu?.subscribe ?? noMenuSubscribe, props.menu?.getSnapshot ?? noMenuSnapshot);
  const openEpisode = useSyncExternalStore(controller.episode.subscribe, controller.episode.getSnapshot);
  const menuIsOpen = (entry: ContentEntry) => !!menuEntry && menuEntry.id === entry.id && menuEntry.kind === entry.kind && menuEntry.libraryId === entry.libraryId;
  const theme = useTheme(), viewport = useWindowDimensions(), insets = useSafeAreaInsets(), t = defaultI18n.t, motion = useMotion();
  const titleArt = useRef<View>(null), readyArt = useRef<ArtworkSource | undefined>(undefined), artScrollRevision = useRef(0);
  const drawnArt = useRef<{controller: NativeTitleController; key: string} | undefined>(undefined);
  const artListeners = useRef(new Set<() => void>()), artMounted = useRef(false);
  useLayoutEffect(() => {for (const listener of [...artListeners.current]) listener();});
  useLayoutEffect(() => {
    const ownedArtListeners = artListeners.current;
    artMounted.current = true;
    return () => {artMounted.current = false;for (const listener of [...ownedArtListeners]) listener();};
  }, [controller]);
  const posture = usePlatformPosture(), registry = useOptionalFocusRegistry();
  useSyncExternalStore(registry?.subscribe ?? noMenuSubscribe, registry?.getSnapshot ?? noFocusSnapshot, registry?.getSnapshot ?? noFocusSnapshot);
  const activeFocus = registry?.capture();
  const fold = posture.status === 'available' && posture.window?.width === viewport.width && posture.window.height === viewport.height
    ? posture.folds.find(value => value.orientation === 'vertical' && value.bounds.left > 0 && value.bounds.left < viewport.width) : undefined;
  // N2-3: the page is drawn beside the rail on a tablet; its panes are measured from where it starts.
  const inset = usePageInset(), rail = inset || mediumRailMetrics.width, pageWidth = Math.max(0, viewport.width - inset);
  const divider = fold ? Math.min(viewport.width - 280, Math.max(rail + 280, fold.bounds.left)) : viewport.width;
  const twoPane = !props.watch && theme.platform === 'tablet' && viewport.width > viewport.height && !!fold;
  // tablet-play3: an upright tablet draws the phone's watch page with larger
  // targets, the title block and its buttons on one row, and 224 pt episode stills.
  const tabletWatch = !!props.watch && props.watch.width >= 600;
  // tablet6: the expanded film page is the desktop one at touch size, with every
  // hover-only explanation written out beside its control.
  const wide = !theme.isTV && theme.platform !== 'phone' && viewport.width >= 1024;
  // tablet8: upright, the show page keeps the phone's arrangement centred in the
  // width beside the rail.
  const centered = !theme.isTV && theme.platform === 'tablet' && !props.watch;
  const phoneTitle = !theme.isTV && !wide && !centered && !props.watch;
  const headerHeight = 44 + insets.top;
  // foldable3g splits the watch page's own sections across the two book pages.
  const titleOnly = props.watch?.sections === 'title', episodesOnly = props.watch?.sections === 'episodes';
  const leftWidth = twoPane ? divider - inset : pageWidth, rightWidth = twoPane ? viewport.width - divider : pageWidth;
  const bodyWidth = props.watch?.width ?? (twoPane ? rightWidth : pageWidth);
  const scroll = useRef<ScrollView>(null), titleMore = useRef<View>(null), titlePlay = useRef<View>(null), bodyTop = useRef(0), cells = useRef(new Map<string, {y: number; height: number}>());
  const [tvBrowsing, setTVBrowsing] = useState(false), [tvTab, setTVTab] = useState<NativeTitleTab>(snapshot.place.tab);
  const tvAnchor = useRef<Readonly<{browsing: boolean; anchor?: string}> | undefined>(undefined), tvAligned = useRef('');
  const [collapsed, setCollapsed] = useState(false), [elapsed, setElapsed] = useState(0), [pinnedWidth, setPinnedWidth] = useState(0);
  // Each sheet owns the exact page and host it opened on, and the settings sheet
  // also owns its ticket. A callback only ever acts for the sheet it was rendered
  // with, so a superseded save can never close or borrow a successor's sheet.
  const [seasonsSheet, setSeasonsSheet] = useState<NativeTitlePageLease>();
  const continueController = useRef<AbortController | undefined>(undefined);
  const [continueBusy, setContinueBusy] = useState(false), [continueRemoved, setContinueRemoved] = useState(false);
  useEffect(() => () => continueController.current?.abort(), []);
  const pendingAnchor = useRef<ReturnType<NativeTitleController['memory']['getSnapshot']>['anchor'] | undefined>(controller.memory.getSnapshot().anchor);
  const detail = snapshot.detail?.data, workspace = snapshot.workspace?.data;
  const focusScope = JSON.stringify([props.authority, snapshot.entry.kind, snapshot.entry.libraryId, snapshot.entry.id]);
  // A title's related rows; a show or season page's are the show's recommendations (N4-8).
  const relatedRows = [...(detail?.related?.rows ?? []), ...(snapshot.recommendationRows ?? [])].filter(row => row.entries.length > 0);
  const hasPeople = !!props.person && ((snapshot.credits?.credits ?? detail?.metadata.credits ?? []).some(credit => !!credit.personId) || !!snapshot.credits?.hasMore);
  const regions = tvTitleRegionMap(focusScope, props.authority ? tvFocusIds(props.authority).rail : undefined,
    ['show', 'season', 'episode'].includes(snapshot.entry.kind) && !!workspace, relatedRows.map(row => row.id), !!detail?.collections?.[0], hasPeople);
  const tvSection = theme.isTV ? tvTitleFocusSection(regions, activeFocus?.regionId) : undefined;
  const alignTV = () => {
    if (!theme.isTV) return;
    const target = tvAnchor.current;
    if (!target) return;
    const cell = target.anchor ? cells.current.get(target.anchor) : undefined;
    if (target.browsing && (!bodyTop.current || !cell)) return;
    const y = target.browsing ? Math.max(0, bodyTop.current + cell!.y - tvBrowseTop) : 0;
    const key = `${target.anchor ?? 'hero'}:${y}`;
    if (tvAligned.current === key) return;
    tvAligned.current = key; scroll.current?.scrollTo({y, animated: !motion.reduce});
  };
  useEffect(() => {
    if (!tvSection) return;
    tvAnchor.current = tvSection; setTVBrowsing(tvSection.browsing); if (tvSection.tab) setTVTab(tvSection.tab); alignTV();
    // A row's focus identity, not each card or season in it, aligns the title body.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [controller, tvSection?.browsing, tvSection?.anchor, motion.reduce]);
  const show = ['show', 'season', 'episode'].includes(snapshot.entry.kind);
  // W-8: a show, season or episode page wears the show's hero, so its My List is the show's own
  // (phone-detail frames 4-5, as on web); a movie keeps its own item action.
  const myList = props.myList ?? (show
    ? snapshot.showState ? {selected: snapshot.showState.watchlisted, change: (value: boolean) => controller.setShowMyList(value)} : undefined
    : detail?.actions.some(action => action.id === 'watchlist' && action.enabled)
      ? {selected: detail.personal.watchlisted, change: (value: boolean) => controller.personal({action: 'watchlist', value})} : undefined);
  const loading = show ? snapshot.workspace?.phase === 'loading' : snapshot.detail?.phase === 'loading';
  useEffect(() => {void controller.open().catch(props.reportError);}, [controller, props.reportError]);
  useEffect(() => {
    if (!loading) {setElapsed(0); return;}
    const begin = Date.now(), timers = [setTimeout(() => setElapsed(Math.max(300, Date.now() - begin)), 300), setTimeout(() => setElapsed(Date.now() - begin), 8000)];
    return () => {for (const timer of timers) clearTimeout(timer);};
  }, [loading]);
  useEffect(() => {
    if (openEpisode.phase !== 'idle') controller.episode.close();
    // An open episode belongs to its own season page; leaving it closes the plate.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [controller, snapshot.place.tab, snapshot.place.seasonId, snapshot.place.group]);
  const restore = () => {
    const anchor = pendingAnchor.current;
    if (!anchor) return;
    const cell = anchor.itemId ? cells.current.get(anchor.itemId) : undefined;
    if (cell || !anchor.itemId) {
      scroll.current?.scrollTo({y: cell ? Math.max(0, bodyTop.current + cell.y + anchor.offset) : anchor.offset, animated: false});
      pendingAnchor.current = undefined;
    }
  };
  const restoreTab = theme.isTV ? undefined : snapshot.place.tab;
  // A new season (or tab, group, size) lays the body out again: the TV alignment is redone too, or a restore under it leaves the browsed row low.
  useLayoutEffect(() => {pendingAnchor.current = controller.memory.getSnapshot().anchor; cells.current.clear(); tvAligned.current = '';}, [controller, restoreTab, snapshot.place.seasonId, snapshot.place.group, viewport.width, viewport.height, bodyWidth]);
  return {props, controller, text, snapshot, openEpisode, menuIsOpen, theme, viewport, insets, t, titleArt, readyArt, artScrollRevision, drawnArt, artListeners, artMounted, registry, twoPane, tabletWatch, wide, centered, phoneTitle, headerHeight, titleOnly, episodesOnly, leftWidth, rightWidth, bodyWidth, scroll, titleMore, titlePlay, bodyTop, cells, tvBrowsing, tvTab, collapsed, setCollapsed, elapsed, pinnedWidth, setPinnedWidth, seasonsSheet, setSeasonsSheet, continueController, continueBusy, setContinueBusy, continueRemoved, setContinueRemoved, detail, workspace, relatedRows, regions, tvSection, alignTV, show, myList, loading, restore};
}
