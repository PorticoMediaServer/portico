import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {cardText, episodeLabel, playbackIdentity, titleFactParts, type TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {AmbientArt, Artwork, type AmbientArtSource, type ArtworkSource, StandardBackdrop} from '../../kit/artwork';
import {EpisodeTile, PosterCard, type EpisodeTileProps, type PosterCardProps} from '../../kit/cards';
import {Button, Focusable, ResumeButton, Switch, Tabs} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {TVShelf, tvItemKey} from '../../tv/layout/TVShelf';
import {FocusRegion, useOptionalFocusRegistry, type FocusRegionProps} from '../../kit/focus';
import {tvFocusIds} from '../../shell/tv-focus';
import type {ShellAuthority} from '../../shell/model';
import {tvTitleRegionMap, tvTitleFocusSection} from './tv-title-focus';
import {TVTitleBackdrop, TVTitleCompactHeader, TVTitleHeroCopy} from './TVTitleChrome';
import {useMotion} from '../../kit/useMotion';
import {HeroMetadata} from '../../kit/layout/Hero';
import {SkeletonBlock, StateLayout, TitleSkeleton} from '../../kit/states';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {nativeType, useTheme} from '../../kit/theme';
import type {ShowSettings} from '../../../../../packages/client-core/src/show-workspace';
import type {NativeTitleController, NativeTitlePageLease} from './title-controller';
import type {NativeTitleTab} from './title-memory';
import {TitlePeople} from './TitlePeople';
import {NativeTitleEpisodePlate} from './TitleEpisodePlate';
import {NativeSeasonsSheet} from './SeasonsSheet';
import {TVEpisodeBrowser} from './TVEpisodeBrowser';
import {NativeShowSettingsSheet} from './ShowSettingsSheet';
import type {NativePlaybackAdapter, NativePlaybackRuntimeSnapshot} from '../../platform/playback/types';
import type {NativeStartArtworkOffer} from '../../platform/playback/start-presentation';
import {sameFence} from '../../../../../packages/player-core/src';
import {durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {usePlatformPosture} from '../../platform/platformPosture';
import {mediumRailMetrics} from '../../shell/model';
import {BlurView} from '../../platform/native-blur';

export type NativeTitleScreenProps = Readonly<{
  controller: NativeTitleController;
  authority?: ShellAuthority;
  text: TextEnvironment;
  artwork: (path: string | undefined) => ArtworkSource | undefined;
  ambient: (path: string | undefined) => AmbientArtSource | undefined;
  back: () => void;
  navigate: (entry: ContentEntry) => void;
  more: (entry: ContentEntry, anchorRef?: React.RefObject<View | null>) => void;
  reportError: (error: unknown) => void;
  renderError: (error: unknown, retry: () => void) => ReactNode;
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
  /** A registered person route, where the shell has one. */
  person?: (personId: string) => void;
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
/** An episode's own numbering, the same shape the episode rows and the player label use. */
const episodeNumbering = (entry: ContentEntry) => entry.absoluteNumber !== undefined ? {mode: 'absolute' as const, number: entry.absoluteNumber}
  : entry.dateBased && entry.airDate ? {mode: 'date' as const, day: entry.airDate}
    : entry.seasonNumber === 0 ? {mode: 'special' as const} : {mode: 'seasonal' as const, season: entry.seasonNumber, episode: entry.episodeNumber};

/** The retained title route. Technical facts exist only in its separate File info surface. */
export function NativeTitleScreen(props: NativeTitleScreenProps) {
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
  const rail = mediumRailMetrics.width;
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
  const headerHeight = (phoneTitle ? 48 : 50) + insets.top;
  // foldable3g splits the watch page's own sections across the two book pages.
  const titleOnly = props.watch?.sections === 'title', episodesOnly = props.watch?.sections === 'episodes';
  const leftWidth = twoPane ? divider - rail : viewport.width, rightWidth = twoPane ? viewport.width - divider : viewport.width;
  const bodyWidth = props.watch?.width ?? (twoPane ? rightWidth : viewport.width);
  const scroll = useRef<ScrollView>(null), titleMore = useRef<View>(null), titlePlay = useRef<View>(null), titlePlayOn = useRef<View>(null), bodyTop = useRef(0), cells = useRef(new Map<string, {y: number; height: number}>());
  const [tvBrowsing, setTVBrowsing] = useState(false), [tvTab, setTVTab] = useState<NativeTitleTab>(snapshot.place.tab);
  const tvAnchor = useRef<Readonly<{browsing: boolean; anchor?: string}> | undefined>(undefined), tvAligned = useRef('');
  const [collapsed, setCollapsed] = useState(false), [elapsed, setElapsed] = useState(0), [pinnedWidth, setPinnedWidth] = useState(0);
  // Each sheet owns the exact page and host it opened on, and the settings sheet
  // also owns its ticket. A callback only ever acts for the sheet it was rendered
  // with, so a superseded save can never close or borrow a successor's sheet.
  const [seasonsSheet, setSeasonsSheet] = useState<NativeTitlePageLease>();
  const [settingsSheet, setSettingsSheet] = useState<Readonly<{lease: NativeTitlePageLease; ticket: number; busy: boolean}>>();
  const sheetTicket = useRef(0);
  const continueController = useRef<AbortController | undefined>(undefined);
  const [continueBusy, setContinueBusy] = useState(false), [continueRemoved, setContinueRemoved] = useState(false);
  useEffect(() => () => continueController.current?.abort(), []);
  const pendingAnchor = useRef<ReturnType<NativeTitleController['memory']['getSnapshot']>['anchor'] | undefined>(controller.memory.getSnapshot().anchor);
  const detail = snapshot.detail?.data, workspace = snapshot.workspace?.data;
  const focusScope = JSON.stringify([props.authority, snapshot.entry.kind, snapshot.entry.libraryId, snapshot.entry.id]);
  const relatedRows = (detail?.related?.rows ?? []).filter(row => row.entries.length > 0);
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
    const y = target.browsing ? Math.max(0, bodyTop.current + cell!.y - 252) : 0;
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
  const myList = props.myList ?? (detail?.actions.some(action => action.id === 'watchlist' && action.enabled)
    ? {selected: detail.personal.watchlisted, change: (value: boolean) => controller.personal({action: 'watchlist', value})} : undefined);
  const show = ['show', 'season', 'episode'].includes(snapshot.entry.kind);
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
  useLayoutEffect(() => {pendingAnchor.current = controller.memory.getSnapshot().anchor; cells.current.clear();}, [controller, restoreTab, snapshot.place.seasonId, snapshot.place.group, viewport.width, viewport.height, bodyWidth]);
  if (snapshot.retired) return null;
  const title = workspace?.show.title ?? snapshot.entry.displayTitle ?? detail?.item.title ?? snapshot.entry.title;
  const backdrop = workspace?.show.backdropUrl ?? detail?.item.backdropUrl ?? snapshot.entry.backdropUrl;
  const source = props.ambient(backdrop), foreground = props.artwork(backdrop);
  const artKey = JSON.stringify([snapshot.entry.libraryId, snapshot.entry.kind, snapshot.entry.id,
    foreground?.cacheKey, foreground?.uri, viewport.width, viewport.height]);
  drawnArt.current = {controller, key: artKey};
  const selectedTab = snapshot.place.tab;
  const tabItems = [...(show ? [{value: 'episodes', label: t('title.episodes')}] : []), {value: 'related', label: t('next.controls.moreLikeThis')}, {value: 'details', label: t('title.details')}];
  const focusedSection = tvSection?.tab ?? tvTab;
  const tabs = <Tabs testID="watch-title-tabs" passive={theme.isTV} value={theme.isTV ? focusedSection : selectedTab}
    onValueChange={theme.isTV ? undefined : value => controller.tab(value as NativeTitleTab)} items={tabItems}
    contentContainerStyle={{paddingHorizontal: theme.isTV ? 0 : 20, justifyContent: centered ? 'center' : undefined}}/>;
  const originalArt = (): NativeStartArtworkOffer | undefined => {
    const node = titleArt.current, decoded = readyArt.current;
    if (theme.isTV || props.watch || !artMounted.current || !node || !foreground || decoded?.cacheKey !== foreground.cacheKey || decoded.uri !== foreground.uri) return undefined;
    // Start the real measurement on this press, before controller/HTTP awaits.
    // The callback can supply only this mounted art; it never measures a later
    // source or falls back to the player's dock/window rectangle.
    const scrollRevision = artScrollRevision.current;
    const paintCurrent = () => artMounted.current && titleArt.current === node && drawnArt.current?.controller === controller && drawnArt.current.key === artKey
      && artScrollRevision.current === scrollRevision && readyArt.current?.cacheKey === foreground.cacheKey && readyArt.current?.uri === foreground.uri;
    const bounds = new Promise<Readonly<{x: number; y: number; width: number; height: number}> | undefined>(resolve => {
      try {node.measureInWindow((x, y, width, height) => {
        if (!paintCurrent()
          || ![x, y, width, height].every(Number.isFinite) || width <= 0 || height <= 0
          || x >= viewport.width || y >= viewport.height || x + width <= 0 || y + height <= 0) {resolve(undefined);return;}
        resolve(Object.freeze({x, y, width, height}));
      });} catch {resolve(undefined);}
    });
    const subscribe = (listener: () => void) => {
      artListeners.current.add(listener);if (!paintCurrent()) listener();
      return () => {artListeners.current.delete(listener);};
    };
    return Object.freeze({artwork: foreground, bounds, paintCurrent, subscribe});
  };
  const play = () => {void controller.play(undefined, false, originalArt()).catch(props.reportError);};
  const toggle = () => {const watch = props.watch, fence = watch?.source.fence;
    if (watch && fence && !watch.source.retired && sameFence(watch.playback.getSnapshot().fence, fence)) void watch.playback.toggle(fence).catch(props.reportError);
  };
  const chromeButton = (testID: string, icon: 'i-left' | 'i-cast', label: string, onPress: () => void, anchorRef?: React.RefObject<View | null>) => phoneTitle
    ? <Focusable ref={anchorRef} testID={testID} accessibilityRole="button" accessibilityLabel={label} onPress={onPress}
        hitSlop={3} focusRadius={19} style={{width: 38, height: 38, borderRadius: 19}}>
        <View pointerEvents="none" style={{flex: 1, borderRadius: 19, overflow: 'hidden'}}>
          <BlurView blurType="dark" blurAmount={18} overlayColor="transparent" reducedTransparencyFallbackColor={theme.colors.ink} style={{flex: 1}}>
            <View collapsable={false} style={{flex: 1, alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(7,16,24,.45)'}}>
              <Icon name={icon} size={22} color={theme.colors.text}/>
            </View>
          </BlurView>
        </View>
      </Focusable>
    : <Button ref={anchorRef} testID={testID} variant={icon === 'i-left' ? 'icon' : 'glass'} icon={icon} iconOnly accessibilityLabel={label} onPress={onPress}/>;
  const headerFor = (showTitle: boolean) => !theme.isTV && !props.watch && <View testID="watch-title-header" style={{position: 'absolute', top: 0, left: 0, right: 0, height: headerHeight, paddingTop: insets.top, flexDirection: 'row', alignItems: 'center', paddingHorizontal: phoneTitle ? 12 : 10, zIndex: 10, backgroundColor: collapsed ? theme.colors.ink : 'transparent'}}>
    {chromeButton('watch-title-back', 'i-left', t('action.back'), props.back)}
    {collapsed && showTitle && <Text numberOfLines={1} style={[theme.type.bodyStrong, {flex: 1, textAlign: 'center', color: theme.colors.text}]}>{title}</Text>}
    {props.onPlayOn ? <View style={{marginLeft: 'auto'}}>{chromeButton('watch-title-play-on', 'i-cast', t('next.dock.native.playOnMenu'), () => props.onPlayOn?.(titlePlayOn), titlePlayOn)}</View> : null}
  </View>;
  const header = headerFor(true);
  const removeContinue = () => {
    if (!props.removeContinue || continueBusy) return;
    continueController.current?.abort();
    const controller = new AbortController(); continueController.current = controller;
    setContinueBusy(true);
    void props.removeContinue(controller.signal).then(removed => {if (removed && !controller.signal.aborted) setContinueRemoved(true);})
      .catch(error => {if (!controller.signal.aborted && (error as {name?: string}).name !== 'AbortError') props.reportError(error);})
      .finally(() => {if (continueController.current === controller) {continueController.current = undefined; setContinueBusy(false);}});
  };
  if (loading && !detail && !workspace) return <TitleSkeleton elapsedMs={elapsed} pressed={{title, ...(source ? {art: {source, foreground}} : {})}}
    shell={header} tabs={tabs} playAction={props.watch ? {id: 'toggle', label: t(props.watch.source.playback.intent === 'playing' ? 'action.pause' : 'action.play'), onPress: toggle}
      : snapshot.entry.playback ? {id: 'play', label: t('action.play'), onPress: play} : undefined}/>;
  const error = show ? snapshot.workspace?.error : snapshot.detail?.error;
  if (error && !detail && !workspace) {
    const presented = presentError(error, 'detail');
    // states13: a gone title is named from the captured route entry and the saved server display,
    // never from a fresh read; a permission refusal reveals neither that name nor its art. Search
    // for title and Remove Continue are not drawn: the shell has no search-query route and the
    // title route carries no such offered command.
    const captured = (snapshot.entry.displayTitle || snapshot.entry.title).trim();
    if (!theme.isTV && presented.category === 'permission') {
      return <View style={{flex: 1}}>
        <StateLayout testID="watch-title-restricted" icon="i-lock" contentTop={250} backdropHeight={420}
          title={presented.title} body={presented.body}/>
        {headerFor(false)}
      </View>;
    }
    if (!theme.isTV && presented.category === 'not-found' && captured && props.serverName) {
      return <View style={{flex: 1}}>
        <StateLayout testID="watch-title-removed" icon="i-not" contentTop={250} backdropHeight={420}
          title={t('next.states.removed', {title: captured, server: props.serverName})}
          body={t('next.states.removedBody')}
          primaryAction={props.search ? {id: 'search', label: t('next.states.searchTitle', {title: captured}), icon: 'i-search', onPress: () => props.search?.(captured)} : undefined}
          secondaryAction={props.removeContinue && !continueRemoved ? {id: 'remove-continue', label: t('next.states.removeContinue'), disabled: continueBusy, onPress: removeContinue} : undefined}/>
        {header}
      </View>;
    }
    return <View style={{flex: 1}}>{props.renderError(error, () => {void (show ? controller.workspace.retry() : controller.detail.refresh()).catch(props.reportError);})}{header}</View>;
  }
  const primary = detail?.actions.find(action => action.enabled && action.playback && action.id !== 'start_over');
  const playback = primary?.playback ?? workspace?.nextUp?.playback ?? snapshot.entry.playback;
  // tv.html frame 3 names the episode the button starts: "Play S1 E1".
  const primaryEpisode = theme.isTV && show && workspace?.nextUp && playback?.itemId === workspace.nextUp.id ? workspace.nextUp : undefined;
  const primaryCode = primaryEpisode ? episodeLabel(episodeNumbering(primaryEpisode), 'player', text) : undefined;
  const progress = detail?.personal.progressSeconds ?? snapshot.entry.progressSeconds;
  const resuming = !!playback?.startSeconds && (progress ?? 0) > 0;
  const duration = detail?.item.duration ?? snapshot.entry.duration;
  const startOver = detail?.actions.some(action => action.id === 'start_over' && action.enabled && action.playback);
  const remainingTitle = duration !== undefined && progress !== undefined ? durationText(Math.max(0, duration - progress), text.t) : undefined;
  // tablet7: once the hero is scrolled away, one solid bar carries Back, the title,
  // the pivots, Resume and the round My List. It draws over the page's own sticky
  // pivot strip (which stays in place and is hidden from assistive tech meanwhile),
  // so the scroll composition, anchors and every lease above are untouched.
  const pinned = collapsed && !theme.isTV && !props.watch && (wide || centered);
  const pinnedBar = pinned ? <View testID="watch-title-pinned-bar" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 56 + insets.top, paddingTop: insets.top, zIndex: 11,
    flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 10, backgroundColor: theme.colors.ink, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}
    onLayout={event => setPinnedWidth(event.nativeEvent.layout.width)}>
    <Button testID="watch-title-back" variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={props.back}/>
    <Text numberOfLines={1} style={[theme.type.bodyStrong, {fontSize: 18, fontWeight: '800', color: theme.colors.text, minWidth: 0, flexShrink: 1,
      maxWidth: pinnedWidth ? Math.max(96, Math.min(260, Math.round(pinnedWidth * 0.24))) : 180}]}>{title}</Text>
    <View style={{width: 1, height: 24, backgroundColor: theme.colors.line}}/>
    <View style={{flexShrink: 1, flexGrow: 1, minWidth: 0}}><Tabs testID="watch-title-bar-tabs" value={selectedTab} onValueChange={value => controller.tab(value as NativeTitleTab)} items={tabItems}
      style={{flexShrink: 1, flexGrow: 1, minWidth: 0}}
      contentContainerStyle={{paddingHorizontal: 0, justifyContent: 'flex-start', borderBottomWidth: 0}}/></View>
    <View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 8, flexShrink: 0}}>
      {playback && <Button testID="watch-title-bar-play" variant="primary" size="compact" icon="i-play" onPress={play} style={{height: 40}}>
        {primaryCode ? t('title.resumeEpisode', {code: primaryCode.text}) : t(resuming ? 'entry.resume' : 'entry.play')}</Button>}
      {myList && <Button testID="watch-title-bar-my-list" variant="glass" icon={myList.selected ? 'i-check' : 'i-plus'} iconOnly
        accessibilityLabel={t(myList.selected ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist')} onPress={() => {void myList.change(!myList.selected).catch(props.reportError);}}/>}
    </View>
  </View> : undefined;

  const playMenu = theme.isTV ? () => props.more(snapshot.entry, titlePlay) : undefined;
  const contentRating = workspace?.show.contentRating ?? detail?.facts?.contentRating ?? snapshot.entry.contentRating;
  // tv.html frame 3 and tv-detail frame 1 draw one comma genre group, and the
  // same model context supplies a film's length or a show's real season total.
  const factsContext = theme.isTV ? show && !props.watch ? 'home-show' as const : 'home-movie' as const : 'home-movie' as const;
  const factSource = titleFactParts({year: workspace?.show.year ?? detail?.item.year ?? snapshot.entry.year, durationSeconds: show ? undefined : duration,
    genres: workspace?.show.genres ?? detail?.metadata.genres.map(genre => genre.name) ?? snapshot.entry.genres, contentRating,
    seasonCount: show && !props.watch ? workspace?.seasonTotalCount : undefined}, text, factsContext);
  // The drawn title page keeps the rating in its own tag after the plain facts.
  const ratingFact = contentRating ? {id: 'content-rating' as const, text: contentRating, role: 'supplement' as const} : undefined;
  const facts = theme.isTV
    ? [...factSource, ...(ratingFact && !factSource.some(fact => fact.id === 'content-rating') ? [ratingFact] : [])]
    : [...factSource.filter(fact => fact.id !== 'content-rating'), ...(ratingFact ? [ratingFact] : [])];
  const heroHeight = props.watch ? 0 : theme.isTV ? 660 : phoneTitle ? 396 : Math.min(660, viewport.height * .69), overview = workspace?.show.overview ?? detail?.item.overview ?? snapshot.entry.overview;
  const watching = props.watch?.source, watchIdentity = watching?.subject ? playbackIdentity(watching.subject, 'full', text) : undefined;
  const remaining = watching && Number.isFinite(watching.playback.positionSeconds) && watching.playback.positionSeconds >= 0
    ? durationText(Math.max(0, watching.playback.duration - watching.playback.positionSeconds), text.t) : undefined;
  const shownFacts = props.watch ? [...facts.filter(fact => fact.id !== 'duration'), ...(remaining ? [{id: 'remaining', text: text.t('next.kit.remaining', {time: remaining}), role: 'supplement' as const}] : [])] : facts;
  const shownRating = props.watch ? undefined : shownFacts.find(fact => fact.id === 'content-rating');
  // tv.html frame 3 draws the show's season total as a tag at the end of the facts row.
  const seasonFact = theme.isTV && !props.watch ? shownFacts.find(fact => fact.id === 'season-count') : undefined;
  const metadataFacts = shownFacts.filter(fact => fact.id !== 'content-rating' && !(seasonFact && fact.id === 'season-count'));
  const studio = theme.isTV && !props.watch && show ? workspace?.show.studio : undefined;
  const episodes = snapshot.episodes;
  const selectedSeason = (snapshot.seasons ?? []).find(season => season.id === workspace?.selected.seasonId);
  const currentSeasonLabel = workspace?.selected.group ? workspace.groups.find(group => group.id === workspace.selected.group)?.title ?? t('title.seasons')
    : selectedSeason ? selectedSeason.title || t('title.season', {number: selectedSeason.number}) : t('title.seasons');
  const seasonProgress = selectedSeason?.episodeCount !== undefined ? t('title.seasonProgress', {watched: selectedSeason.watchedCount ?? 0, total: selectedSeason.episodeCount}) : undefined;
  const remember = (y: number) => {
    setCollapsed(old => (y > heroHeight - headerHeight) === old ? old : y > heroHeight - headerHeight);
    if (y < bodyTop.current) {controller.remember({itemId: null, offset: y}); return;}
    const local = Math.max(0, y - bodyTop.current);
    const first = [...cells.current].filter(([, cell]) => cell.y + cell.height > local).sort((a, b) => a[1].y - b[1].y)[0];
    if (first) controller.remember({itemId: first[0], offset: Math.max(0, local - first[1].y)});
  };
  const cell = (id: string, children: ReactNode) => <View key={id} onLayout={event => {cells.current.set(id, event.nativeEvent.layout); restore(); alignTV();}}>{children}</View>;
  const detailRows = [
    {id: 'released', label: t('title.releaseDate'), value: detail?.facts?.releaseDate ? new Intl.DateTimeFormat(text.locale ?? 'en-US', {month: 'long', day: 'numeric', year: 'numeric', timeZone: 'UTC'}).format(new Date(detail.facts.releaseDate)) : undefined},
    {label: t('title.genres'), value: detail?.metadata.genres.map(genre => genre.name).join(', ')},
    {label: t('title.ratings'), value: detail?.metadata.ratings.map(rating => `${rating.provider} ${rating.value}`).join(', ')},
    {label: t('title.studio'), value: workspace?.show.studio ?? detail?.facts?.studio},
    {label: t('title.added'), value: detail?.item.addedAt ? new Intl.DateTimeFormat(text.locale ?? 'en-US', {month: 'long', day: 'numeric', year: 'numeric'}).format(new Date(detail.item.addedAt)) : undefined},
  ].filter(row => row.value);
  // A show's own workspace carries its credits; a movie's detail read carries them.
  const credits = theme.isTV && snapshot.credits ? snapshot.credits.credits : detail?.metadata.credits ?? (workspace?.show.credits ?? []).map(credit => ({
    id: credit.id ?? `show-credit-${credit.ordinal}`, name: credit.name, role: credit.role ?? '', department: credit.department ?? '',
    provider: '', ...(credit.portraitUrl ? {portraitUrl: credit.portraitUrl} : {})})), creditNames = (departments: readonly string[]) =>
    [...new Set(credits.filter(credit => departments.includes(credit.department.toLowerCase())).map(credit => credit.name))];
  const directors = creditNames(['directing', 'director', 'direction']), writers = creditNames(['writing', 'writer', 'writers']);
  const creditPage = theme.isTV ? snapshot.credits : undefined;
  const creditPaging = creditPage ? {hasMore: creditPage.hasMore, loading: creditPage.loading, failed: !!creditPage.error,
    waitingId: creditPage.waitingId, waitingItem: {id: creditPage.waitingId, name: '', role: '', department: '', provider: ''},
    fulfilled: creditPage.fulfilled, requestNext: () => {void controller.moreCredits(creditPage).catch(props.reportError);}} : undefined;
  const aboutRows = [...detailRows.filter(row => row.id === 'released'),
    ...(directors.length ? [{label: t('title.directedBy', {names: ''}).trim(), value: defaultI18n.list(directors)}] : []),
    ...(writers.length ? [{label: t('title.writtenBy', {names: ''}).trim(), value: defaultI18n.list(writers)}] : []),
    ...detailRows.filter(row => row.id !== 'released')];

  const castNames = [...new Set(credits.filter(credit => ['acting', 'cast'].includes(credit.department.toLowerCase())).map(credit => credit.name))];
  // The drawn synopsis is 24/1.45 in 800 and ends in 30; the credits line follows it and ends in 30.
  const copyBlock = props.watch && !titleOnly ? null : <>
    {overview ? <Text numberOfLines={2} style={[theme.isTV ? theme.type.synopsis : phoneTitle ? nativeType({size: 14, weight: 400, tracking: 0, lineHeight: 21}, theme.textScale) : theme.type.body, {color: theme.colors.textSecondary,
      maxWidth: theme.isTV ? 800 : centered ? 560 : undefined, alignSelf: centered ? 'center' : undefined, textAlign: centered ? 'center' : undefined,
      marginTop: theme.isTV ? 0 : phoneTitle ? 16 : 18, marginBottom: theme.isTV ? 30 : 0}]}>{overview}</Text> : null}
    {(theme.isTV || wide) && (directors.length > 0 || castNames.length > 0) ? <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.isTV ? 24 : 16, rowGap: 6, marginTop: theme.isTV ? -10 : 8, marginBottom: theme.isTV ? 30 : 0}}>
      {directors.length > 0 && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('title.directedBy', {names: defaultI18n.list(directors)})}</Text>}
      {castNames.length > 0 && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('title.starring', {names: defaultI18n.list(castNames.slice(0, 3))})}</Text>}
    </View> : null}
  </>;
  // phone-detail and tablet draw these icon buttons as rounded squares (10 and 12), not circles.
  const squareRadius = theme.platform === 'tablet' ? 12 : 10;
  // A show with no loadable episodes (or absolute-numbered ones) has no Play. The page must still
  // open on a control, so the first remaining action takes the TV's preferred focus.
  const tvNoPlay = theme.isTV && !props.watch && !playback;
  const actions = <TitleRegion {...regions.actions} style={{flexDirection: 'row', gap: theme.isTV ? 18 : 10, justifyContent: centered ? 'center' : undefined, marginTop: props.watch ? (tabletWatch ? 0 : 16) : theme.isTV || phoneTitle ? 0 : 22, flexShrink: tabletWatch ? 0 : undefined}}>
    {props.watch ? <Button testID="watch-body-toggle" focusId="watch-body-toggle" style={tabletWatch ? undefined : {flex: 1}} disabled={!watching?.fence || watching.retired || !watching.capabilities.play && !watching.capabilities.pause}
      icon={watching?.playback.intent === 'playing' ? 'i-pause' : 'i-play'} onPress={toggle}>{t(watching?.playback.intent === 'playing' ? 'action.pause' : 'action.play')}</Button>
      : playback && (theme.isTV && resuming && duration && progress !== undefined
        ? <ResumeButton ref={titlePlay} testID="watch-title-play" focusId="watch-title-play" hasTVPreferredFocus icon="i-play" onPress={play} onLongPressAction={playMenu}
            progress={progress / duration} remaining={remainingTitle ? text.t('next.kit.remaining', {time: remainingTitle}) : undefined}>{primaryCode ? t('title.resumeEpisode', {code: primaryCode.text}) : t('entry.resume')}</ResumeButton>
        : <Button ref={titlePlay} testID="watch-title-play" focusId="watch-title-play" hasTVPreferredFocus={theme.isTV} icon="i-play" style={phoneTitle ? {flex: 1} : centered ? {minWidth: 300} : undefined} onPress={play} onLongPressAction={playMenu} >{primaryCode ? t(resuming ? 'title.resumeEpisode' : 'title.playEpisode', {code: primaryCode.text}) : t(resuming ? 'entry.resume' : 'entry.play')}</Button>)}
    {!props.watch && theme.isTV && resuming && startOver && <Button focusId="watch-title-start-over" variant="glass" icon="i-replay" onPress={() => {void controller.play(snapshot.entry, true).catch(props.reportError);}}>{t('action.startOver')}</Button>}
    {myList && <Button focusId="watch-title-my-list" hasTVPreferredFocus={tvNoPlay} style={!theme.isTV && !wide ? {borderRadius: squareRadius} : undefined} variant="glass" icon={myList.selected ? 'i-check' : 'i-plus'} iconOnly={!theme.isTV && !wide} accessibilityLabel={t(myList.selected ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist')} onPress={() => {void myList.change(!myList.selected).catch(props.reportError);}}>{t('next.controls.myList')}</Button>}
    {!props.watch && (theme.isTV || wide || centered) && detail?.actions.some(action => action.id === 'watched' && action.enabled) && <Button focusId="watch-title-watched" variant="glass" icon="i-check" iconOnly={!theme.isTV && !wide}
      accessibilityLabel={t(detail.personal.watched ? 'entry.markUnwatched' : 'entry.markWatched')} accessibilityState={{selected: detail.personal.watched}}
      onPress={() => {void controller.personal({action: 'watched', value: !detail.personal.watched}).catch(props.reportError);}}>{t('next.detail.watched')}</Button>}
    {props.download && !theme.isTV && <Button style={!wide ? {borderRadius: squareRadius} : undefined} variant="glass" icon="i-dl" iconOnly={!wide} accessibilityLabel={t('next.cards.download')} onPress={() => props.download!(snapshot.entry)}>{t('next.cards.download')}</Button>}
    <Button ref={titleMore} focusId="watch-title-more" hasTVPreferredFocus={tvNoPlay && !myList} style={!theme.isTV ? {borderRadius: squareRadius} : undefined} variant="glass" icon="i-more" iconOnly accessibilityLabel={t('card.more', {title})} accessibilityState={{expanded: menuIsOpen(snapshot.entry)}} onPress={() => props.more(snapshot.entry, titleMore)}/>
  </TitleRegion>;
  const titleProgress = !props.watch && !theme.isTV && resuming && duration && progress !== undefined && <View style={{marginTop: 16, gap: 8}}>
    <View style={{height: 4, backgroundColor: '#edf5fa26'}}><View style={{height: 4, width: `${Math.min(1, progress / duration) * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>
    <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 12}}>
      {remainingTitle ? <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{text.t('next.kit.remaining', {time: remainingTitle})}</Text> : <View/>}
      {startOver && <Button variant="text" size="small" icon="i-replay" onPress={() => {void controller.play(snapshot.entry, true).catch(props.reportError);}}>{t('action.startOver')}</Button>}
    </View>
  </View>;
  const heroArt = <AmbientArt source={source} foreground={foreground} treatment="title" height={props.watch ? 520 : heroHeight + 120}
    onReady={!theme.isTV && !props.watch ? value => {readyArt.current = value;} : undefined}
    onError={!theme.isTV && !props.watch ? value => {if (readyArt.current === value) readyArt.current = undefined;} : undefined}/>;
  const hero = (
      <View testID="watch-title-hero" style={props.watch ? {paddingTop: 18, paddingBottom: 18} : {height: heroHeight}}>
        {!theme.isTV && !props.watch ? <View ref={titleArt} collapsable={false} pointerEvents="none"
          style={{position: 'absolute', top: 0, left: 0, right: 0, height: heroHeight + 120}}>{heroArt}</View> : theme.isTV ? null : heroArt}
        <TitleHeroCopy browsing={tvBrowsing} style={props.watch ? {paddingHorizontal: 20} : {position: 'absolute', left: theme.isTV ? 200 : 20, right: theme.isTV ? 200 : 20, bottom: theme.isTV ? 60 : phoneTitle ? 4 : 24}}>
          {(theme.isTV || wide) && !props.watch && detail?.collections?.[0] ? <TitleRegion {...regions.collection}><Focusable focusId="watch-title-collection" testID="watch-title-collection" focusKind="row" focusRadius={8}
            onPress={() => props.openCollection?.({...detail.collections![0]!, libraryId: detail.scope.libraryId})}
            style={{alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 10 : 8, marginBottom: theme.isTV ? 12 : 6}}>
            <Icon name="i-layers" size={theme.isTV ? 24 : 18} color={theme.colors.accentStrong}/>
            <Text style={[nativeType({size: theme.isTV ? 24 : 14.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.accentStrong}]}>{detail.collections[0].name}</Text>
            <Text style={[nativeType({size: theme.isTV ? 22 : 14, weight: 500, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('next.browse.filmCount', {count: detail.collections[0].memberCount})}</Text>
          </Focusable></TitleRegion> : null}
          <View style={tabletWatch ? {flexDirection: 'row', alignItems: 'flex-end', gap: 24} : undefined}>
          <View style={tabletWatch ? {flex: 1, minWidth: 0} : undefined}>
          <Text accessibilityRole="header" numberOfLines={theme.isTV && !props.watch ? undefined : 2} style={[props.watch ? nativeType({size: 28, weight: 800, tracking: -.035, lineHeight: 30.8}, theme.textScale)
            : theme.isTV ? theme.type.lockup : wide ? nativeType({size: 64, weight: 800, tracking: -.045, lineHeight: 64}, theme.textScale)
            : nativeType({size: phoneTitle ? 46 : 40, weight: 800, tracking: phoneTitle ? -.045 : -.04, lineHeight: phoneTitle ? 43.24 : 43}, theme.textScale), {color: theme.colors.text, textAlign: centered || phoneTitle ? 'center' : undefined, marginBottom: props.watch ? 5 : theme.isTV ? 22 : phoneTitle ? 10 : 12}]}>{title}</Text>
          {props.watch && watchIdentity?.subtitle.length ? <Text numberOfLines={1} style={[nativeType({size: 15, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, marginBottom: 6}]}>{watchIdentity.subtitle.map(part => part.text).join(' ')}</Text> : null}
          <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: centered || phoneTitle ? 'center' : undefined, columnGap: theme.isTV ? 26 : phoneTitle ? 12 : 10, rowGap: theme.isTV ? 10 : phoneTitle ? 6 : 10, marginBottom: theme.isTV ? 18 : 0}}>
            {phoneTitle ? metadataFacts.map(fact => <Text key={fact.id} style={[nativeType({size: 13, weight: fact.id === 'year' || fact.role === 'identity' ? 700 : 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {includeFontPadding: false, color: fact.id === 'year' || fact.role === 'identity' ? theme.colors.text : theme.colors.textSecondary}]}>{fact.text}</Text>) : <HeroMetadata facts={metadataFacts}/>}
            {studio ? <Text style={[theme.type.meta, {color: theme.colors.textSecondary}]}>{studio}</Text> : null}
            {seasonFact ? <View style={{borderWidth: 1.5, borderColor: '#edf5fa47', borderRadius: 8, paddingHorizontal: 12, paddingVertical: 3}}>
              <Text style={[theme.type.tag, {color: theme.colors.text}]}>{seasonFact.text}</Text></View> : null}
            {shownRating ? <View style={theme.isTV ? {borderWidth: 1.5, borderColor: '#edf5fa47', borderRadius: 8, paddingHorizontal: 12, paddingVertical: 3}
              : {borderWidth: 1, borderColor: theme.colors.lineStrong, borderRadius: 4, paddingHorizontal: 7, paddingVertical: 1}}>
              <Text style={theme.isTV ? [theme.type.tag, {color: theme.colors.text}] : [phoneTitle ? nativeType({size: 13, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale) : theme.type.meta, {color: theme.colors.textSecondary}]}>{shownRating.text}</Text></View> : null}
          </View>
          {wide && !props.watch && detail?.facts?.tagline ? <Text numberOfLines={2} style={[theme.type.body, {color: theme.colors.textSecondary, marginTop: 14}]}>{detail.facts.tagline}</Text> : null}
          {/* tv.html frame 3 and tv-detail frame 1 draw the synopsis and credits above the buttons. */}
          {theme.isTV || wide ? copyBlock : null}
          </View>
          {!phoneTitle && actions}
          </View>
          {!phoneTitle && titleProgress}
          {theme.isTV || wide || phoneTitle ? null : copyBlock}
        </TitleHeroCopy>
      </View>
  );
  const heroNode = phoneTitle ? <View>{hero}<View style={{paddingTop: 18, paddingHorizontal: 20}}>{actions}{titleProgress}{copyBlock}</View></View> : hero;
  const playingEpisodeId = props.watch?.source.entry?.id, nextEpisodeId = workspace?.nextUp?.id;
  // phone-detail 6 previews the label the owner's own settings will produce, from
  // the page the sheet opened on, not from whatever replaced it since.
  const settingsPage = settingsSheet?.lease.page;
  const settingsEpisode = (settingsPage ?? workspace)?.nextUp ?? (props.watch?.source.entry?.kind === 'episode' ? props.watch.source.entry : undefined) ?? snapshot.episodes[0];
  // The controls the body draws belong to the page this render shows.
  const bodyLease = controller.pageLease();
  // Openers use the lease this render already holds: a stale opener can never
  // recapture a later page or host at press time.
  const closeSeasons = () => setSeasonsSheet(undefined);
  const openSeasons = () => {if (bodyLease) setSeasonsSheet(bodyLease);};
  const closeSettings = () => setSettingsSheet(undefined);
  const openSettings = () => {if (bodyLease) setSettingsSheet({lease: bodyLease, ticket: ++sheetTicket.current, busy: false});};
  const saveSettings = (value: ShowSettings) => {
    const sheet = settingsSheet;
    if (!sheet) return;
    setSettingsSheet(current => current?.ticket === sheet.ticket ? {...current, busy: true} : current);
    void controller.saveSettings(sheet.lease, value).then(() => {
      // This finalizer owns this ticket: a successor sheet stays open.
      setSettingsSheet(current => current?.ticket === sheet.ticket ? undefined : current);
    }, error => {
      setSettingsSheet(current => current?.ticket === sheet.ticket ? undefined : current);
      props.reportError(error);
    });
  };
  const bodyNode = (
      <View onLayout={event => {bodyTop.current = event.nativeEvent.layout.y; restore(); alignTV();}} style={{paddingLeft: theme.isTV ? 200 : 20, paddingRight: theme.isTV ? 96 : 20, paddingTop: 20, paddingBottom: theme.isTV ? Math.max(60, viewport.height - 600) : 60, gap: 20}}>
        {(theme.isTV || selectedTab === 'episodes') && workspace && (theme.isTV ? cell('episodes', <TVEpisodeBrowser focus={regions.episodes} requestedSeasonId={snapshot.workspace?.phase === 'loading' ? snapshot.workspace.route?.selectedSeasonId : undefined} seasons={snapshot.seasons} selectedSeasonId={workspace.selected.seasonId ?? null}
          episodes={episodes} nextUp={workspace.nextUp} show={{title: workspace.show.title, overview: workspace.show.overview, backdropUrl: workspace.show.backdropUrl}}
          open={openEpisode} text={text} artwork={props.artwork} more={props.more}
          onSeasonFocus={() => controller.episode.close()} onEpisodeFocus={entry => controller.episode.open(entry)}
          onSelectSeason={id => {void controller.season(id).catch(props.reportError);}}
          onPlayEpisode={entry => {void controller.play(entry).catch(props.reportError);}}
          onPlaySeason={id => {void controller.playSeason(id).catch(props.reportError);}}
          onMarkSeason={(id, watched) => {void controller.markSeason(id, watched).catch(props.reportError);}}
          onMoreSeasons={() => {void controller.moreSeasons().catch(props.reportError);}}
          onMoreEpisodes={() => {void controller.moreEpisodes().catch(props.reportError);}}
          onRetryEpisode={() => controller.episode.retry()} renderError={props.renderError}/>)
          : <>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
            <Button testID="watch-seasons-open" variant="glass" size="small" trailingIcon="i-chdown" style={{flexShrink: 0}}
              onPress={openSeasons}>{currentSeasonLabel}</Button>
            {seasonProgress ? <Text numberOfLines={1} style={[theme.type.caption, {color: theme.colors.textTertiary, flexShrink: 1}]}>{seasonProgress}</Text> : null}
            <View style={{flex: 1}}/>
            {!theme.isTV && <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, flexShrink: 0}}>
              <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.textSecondary}]}>{t('next.controls.hideWatched')}</Text>
              <Switch testID="watch-hide-watched" variant={theme.platform === 'phone' ? 'episode' : 'selection'} checked={snapshot.place.hideWatched}
                accessibilityLabel={t('next.controls.hideWatched')} onCheckedChange={value => {if (bodyLease) void controller.hideWatched(value, bodyLease).catch(props.reportError);}}/>
            </View>}
            {props.owner && <Button testID="watch-show-settings-open" variant="icon" icon="i-more" iconOnly accessibilityLabel={t('card.more', {title})} onPress={openSettings}/>}
          </View>
          {(() => {
            const model = (entry: ContentEntry) => cardText({id: entry.id, kind: entry.kind, title: entry.displayTitle ?? entry.title,
              episode: episodeNumbering(entry), durationSeconds: entry.duration, positionSeconds: entry.progressSeconds}, 'in-show', text);
            const card = (entry: ContentEntry, layout: 'row' | 'tile', width: number) => {
              const copy = model(entry);
              return <TitleEpisode entry={entry} more={props.more} moreExpanded={menuIsOpen(entry)} selection={props.selection?.(entry)}
                title={entry.displayTitle ?? entry.title} label={copy.caption} caption={layout === 'row' ? copy.detail : undefined}
                width={width} layout={layout} artWidth={layout === 'row' ? (tabletWatch ? 224 : theme.platform === 'tablet' ? 210 : undefined) : undefined}
                watched={entry.watched} progress={copy.progress} open={layout === 'tile' && openEpisode.entry?.id === entry.id}
                length={layout === 'tile' && entry.duration ? durationText(entry.duration, t) : undefined}
                status={entry.id === playingEpisodeId ? t('player.miniPlaying') : entry.id === nextEpisodeId ? t('player.queue') : undefined}
                artwork={props.artwork(entry.stillUrl ?? workspace.show.backdropUrl)} backdropFallback={!entry.stillUrl} synopsis={layout === 'row' ? entry.overview : undefined}
                {...(theme.isTV && entry.playback ? {primaryAction: 'play' as const, onPlay: () => {void controller.play(entry).catch(props.reportError);}} : {primaryAction: 'navigate' as const})}
                onNavigate={() => theme.isTV ? props.navigate(entry) : controller.episode.open(entry)} onDownload={props.download ? () => props.download!(entry) : undefined}/>;
            };
            const plate = (entry: ContentEntry) => <NativeTitleEpisodePlate entry={entry} code={episodeLabel(episodeNumbering(entry), 'player', text)} width={bodyWidth - 40}
              still={props.artwork(entry.stillUrl)} backdrop={props.artwork(entry.stillUrl ?? workspace.show.backdropUrl)} snapshot={openEpisode}
              more={props.more} download={props.download} onClose={() => controller.episode.close()} onPlay={() => {void controller.episode.play().catch(props.reportError);}}
              onStartOver={() => {void controller.episode.play(true).catch(props.reportError);}}
              onPersonal={intent => {void controller.episode.personal(intent).catch(props.reportError);}}
              onNeighbour={neighbour => props.navigate({id: neighbour.id, kind: 'episode', title: neighbour.title, libraryId: workspace.show.libraryId, show: {id: workspace.show.id, name: workspace.show.title}})}
              onRetry={() => controller.episode.retry()} renderError={props.renderError} locale={text.locale ?? 'en-US'}/>;
            const open = openEpisode.entry && openEpisode.entry.kind === 'episode' ? openEpisode.entry : undefined;
            if (theme.isTV) return episodes.map(entry => cell(entry.id, card(entry, 'tile', 356)));
            // tablet7: the expanded page draws the episodes as one sideways tile row,
            // three and a bit visible, with the open episode's panel beneath the row.
            if (wide) {
              const tile = Math.min(300, Math.max(200, Math.floor((bodyWidth - 40 - 28) / 3.3)));
              return <>
                <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap: 14, paddingRight: 20}}>
                  {episodes.map(entry => <View key={entry.id}>{card(entry, 'tile', tile)}</View>)}
                </ScrollView>
                {open ? plate(open) : null}
              </>;
            }
            return episodes.map(entry => cell(entry.id, open?.id === entry.id ? plate(entry) : card(entry, 'row', bodyWidth - 40)));
          })()}
        </>)}
        {(theme.isTV || selectedTab === 'related') && relatedRows.map(row => {
          // MOB-054/TV-019: each related row continues to its end through the title's row route.
          const pages = snapshot.related?.[row.id], entries = pages?.entries ?? row.entries;
          const paging = pages ? {hasMore: pages.hasMore, loading: pages.loading, failed: pages.failed, waitingId: pages.waitingId, fulfilled: pages.fulfilled,
            waitingItem: relatedWaiting(pages.waitingId, row.entries[0]), requestNext: () => controller.moreRelated(row.id, pages)} : undefined;
          return cell(row.id, <View><Text style={[theme.isTV ? theme.type.rowHeading : theme.type.heading, {color: theme.colors.text, marginBottom: theme.isTV ? 22 : 14}]}>{row.heading}</Text>
          {theme.isTV ? <TVShelf items={entries} getKey={tvItemKey} getFocusId={tvItemKey} focus={regions.related.get(row.id)} itemWidth={214} itemHeight={420} gap={28} paging={paging}
            renderItem={(entry, slot) => slot.waiting ? <Focusable focusId={slot.focusId} testID="watch-related-pending" focusTreatment="none" accessibilityLabel={defaultI18n.t('next.states.loading')}
              onFocus={() => {slot.onFocus(); paging?.requestNext();}} onBlur={slot.onBlur} style={{width: 214}}><SkeletonBlock width={214} height={321} radius={12} shine={!paging?.failed}/></Focusable>
            : <TitleRelated entry={entry} focusId={slot.focusId} more={props.more} moreExpanded={menuIsOpen(entry)} selection={props.selection?.(entry)} title={entry.displayTitle ?? entry.title} artwork={props.artwork(entry.posterUrl)} width={214}
              caption={entry.year !== undefined ? [{id: 'year', text: defaultI18n.number(entry.year, {useGrouping: false}), role: 'value'}] : undefined}
              watched={entry.watched} progress={entry.duration && Number.isFinite(entry.duration) && entry.duration > 0 && entry.progressSeconds !== undefined && Number.isFinite(entry.progressSeconds) ? Math.max(0, Math.min(1, entry.progressSeconds / entry.duration)) : undefined}
              onFocus={slot.onFocus} onBlur={slot.onBlur} onNavigate={() => props.navigate(entry)}/>}/>
            : <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap: 10}} scrollEventThrottle={100}
              onScroll={({nativeEvent: {contentOffset, layoutMeasurement, contentSize}}) => {if (paging?.hasMore && contentOffset.x + layoutMeasurement.width >= contentSize.width - layoutMeasurement.width) paging.requestNext();}}>
              {entries.map(entry => <TitleRelated key={entry.id} entry={entry} more={props.more} moreExpanded={menuIsOpen(entry)} selection={props.selection?.(entry)} title={entry.displayTitle ?? entry.title} artwork={props.artwork(entry.posterUrl)} width={(bodyWidth - 60) / 3} caption={entry.year !== undefined ? [{id: 'year', text: String(entry.year), role: 'supplement'}] : undefined} onNavigate={() => props.navigate(entry)}/>)}</ScrollView>}</View>);
        })}
        {(theme.isTV || selectedTab === 'details') && cell('details', <View style={{gap: 16}}>
          {theme.isTV ? <View testID="watch-title-about" style={{flexDirection: 'row', alignItems: 'flex-start', gap: 88}}>
            <View style={{width: 860}}>
              <Text style={[nativeType({size: 32, weight: 700, tracking: -.019, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, marginBottom: 14}]}>{props.aboutLabel}</Text>
              {overview ? <Text style={[nativeType({size: 24, weight: 400, tracking: 0, lineHeight: 34.8}, theme.textScale), {color: theme.colors.textSecondary}]}>{overview}</Text> : null}
              {detail?.facts?.tagline ? <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 31.9}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 16}]}>{detail.facts.tagline}</Text> : null}
            </View>
            <View style={{flex: 1, minWidth: 0, gap: 12, marginTop: 4}}>{aboutRows.map(row => <View key={row.label} style={{flexDirection: 'row', gap: 24}}>
              <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 28.6}, theme.textScale), {width: 170, color: theme.colors.textTertiary}]}>{row.label}</Text>
              <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 28.6}, theme.textScale), {flex: 1, color: theme.colors.text}]}>{row.value}</Text>
            </View>)}</View>
          </View> : <>
            <Text style={[theme.type.heading, {color: theme.colors.text}]}>{props.aboutLabel}</Text>
            {overview && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{overview}</Text>}
            <View style={{backgroundColor: theme.colors.raisedHigh, borderRadius: 14, paddingHorizontal: 14}}>{detailRows.map(row => <View key={row.label} style={{flexDirection: 'row', gap: 14, paddingVertical: 12, borderBottomWidth: 1, borderColor: theme.colors.line}}><Text style={[theme.type.caption, {width: 90, color: theme.colors.textTertiary}]}>{row.label}</Text><Text style={[theme.type.body, {flex: 1, color: theme.colors.text}]}>{row.value}</Text></View>)}</View>
          </>}
          {!theme.isTV && detail?.collections?.[0] ? <Focusable testID="title-collection-card" focusKind="row" focusRadius={14}
            onPress={() => props.openCollection?.({...detail.collections![0]!, libraryId: detail.scope.libraryId})}
            style={{flexDirection: 'row', alignItems: 'center', gap: 14, paddingHorizontal: 12, paddingVertical: 10, borderRadius: 14, backgroundColor: theme.colors.raised}}>
            <View style={{width: 58, height: 54}}>
              {detail.collections[0].posterUrls.slice(0, 2).map((poster, index) => <Artwork key={poster} source={props.artwork(poster)} width={36} height={54}
                style={{position: 'absolute', top: 0, left: index * 22, borderRadius: 5, transform: [{rotate: index ? '6deg' : '-7deg'}]}}/>)}
            </View>
            <View style={{flex: 1, minWidth: 0}}>
              <Text style={[theme.type.captionStrong, {color: theme.colors.accentStrong}]}>{t('next.home.collection')}</Text>
              <Text numberOfLines={1} style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{detail.collections[0].name}</Text>
              <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.collection.filmsOnServer', {count: detail.collections[0].memberCount})}</Text>
            </View>
            <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
          </Focusable> : null}
          {credits.length || creditPaging?.hasMore ? <View style={{gap: theme.isTV ? 20 : 10}}>
            <Text style={[theme.isTV ? theme.type.rowHeading : theme.type.heading, {color: theme.colors.text}]}>{t('web.metadata.tabPeople')}</Text>
            <TitlePeople credits={credits} artwork={props.artwork} onPerson={props.person} focus={regions.people} paging={creditPaging}/>
            {creditPage?.error ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{presentError(creditPage.error, 'detail').body}</Text> : null}
            {!theme.isTV && (directors.length > 0 || writers.length > 0) && <View style={{gap: 2}}>
              {directors.length ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('title.directedBy', {names: defaultI18n.list(directors)})}</Text> : null}
              {writers.length ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('title.writtenBy', {names: defaultI18n.list(writers)})}</Text> : null}
            </View>}
          </View> : null}
        </View>)}
      </View>
  );
  return <View testID="watch-title-screen" style={{flex: 1, backgroundColor: theme.isTV ? theme.colors.ink : undefined}}>{!theme.isTV && <StandardBackdrop/>}
    {theme.isTV && <TVTitleBackdrop source={source} foreground={foreground} browsing={tvBrowsing}/>}
    {twoPane ? <View style={{flex: 1, flexDirection: 'row'}}>
      <ScrollView style={{width: leftWidth, alignSelf: 'stretch'}} scrollEventThrottle={32} showsVerticalScrollIndicator={false}>
        {heroNode}
        <View style={{paddingHorizontal: 20, paddingTop: 18, paddingBottom: 48, gap: 10}}>
          {credits.length ? <><Text style={[theme.isTV ? theme.type.rowHeading : theme.type.heading, {color: theme.colors.text}]}>{t('web.metadata.tabPeople')}</Text>
            <TitlePeople credits={credits} artwork={props.artwork} onPerson={props.person}/></> : null}
          {directors.length ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('title.directedBy', {names: defaultI18n.list(directors)})}</Text> : null}
          {writers.length ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('title.writtenBy', {names: defaultI18n.list(writers)})}</Text> : null}
        </View>
      </ScrollView>
      <View style={{width: rightWidth}}>
        <View style={{backgroundColor: theme.colors.ink}}>{tabs}</View>
        <ScrollView ref={scroll} onScroll={event => {
          artScrollRevision.current++;
          for (const listener of [...artListeners.current]) listener();
          const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
          if ((theme.isTV || selectedTab === 'episodes') && contentSize.height - contentOffset.y - layoutMeasurement.height < layoutMeasurement.height) void controller.moreEpisodes().catch(props.reportError);
        }} scrollEventThrottle={32}>{bodyNode}</ScrollView>
      </View>
    </View> : titleOnly ? <View style={{flex: 1, overflow: 'hidden'}}>{heroNode}</View> : <ScrollView ref={scroll} stickyHeaderIndices={theme.isTV ? undefined : [episodesOnly ? 0 : 1]} onScroll={event => {
      artScrollRevision.current++;
      for (const listener of [...artListeners.current]) listener();
      remember(event.nativeEvent.contentOffset.y);
      const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
      if ((theme.isTV || selectedTab === 'episodes') && contentSize.height - contentOffset.y - layoutMeasurement.height < layoutMeasurement.height) void controller.moreEpisodes().catch(props.reportError);
    }} scrollEventThrottle={32}>
      {!episodesOnly && heroNode}
      <View accessibilityElementsHidden={pinned || theme.isTV && tvBrowsing} importantForAccessibility={pinned || theme.isTV && tvBrowsing ? 'no-hide-descendants' : 'auto'}
        style={{backgroundColor: theme.isTV ? 'transparent' : theme.colors.ink, opacity: theme.isTV && tvBrowsing ? 0 : 1, paddingLeft: theme.isTV ? 200 : 0, paddingRight: theme.isTV ? 96 : 0}}>{tabs}</View>
      {bodyNode}
    </ScrollView>}{pinnedBar ?? header}
    {theme.isTV && <TVTitleCompactHeader title={title} browsing={tvBrowsing}>{tabs}</TVTitleCompactHeader>}
    {!theme.isTV && <NativeSeasonsSheet open={!!seasonsSheet} onOpenChange={value => {if (value) openSeasons(); else closeSeasons();}}
      seasons={snapshot.seasons} total={workspace?.seasonTotalCount ?? snapshot.seasons.length} selectedId={workspace?.selected.seasonId ?? null}
      onSelect={id => {const lease = seasonsSheet; closeSeasons(); if (lease) void controller.season(id, lease).catch(props.reportError);}}
      onMore={() => {void controller.moreSeasons().catch(props.reportError);}}
      hideWatched={snapshot.place.hideWatched} onHideWatched={value => {if (seasonsSheet) void controller.hideWatched(value, seasonsSheet).catch(props.reportError);}}/>}
    {!theme.isTV && props.owner && settingsSheet && <NativeShowSettingsSheet open onOpenChange={value => {if (!value) closeSettings();}} showTitle={title}
      {...(settingsEpisode ? {episode: settingsEpisode} : {})} settings={settingsSheet.lease.page.settings} busy={settingsSheet.busy} onSave={saveSettings}/>}
  </View>;
}

/** The actual card owns the measured native ref. Its callback stays argument-free
 * for touch, keyboard and TV; capture authority still comes from the real entry. */
export function TitleEpisode({entry, more, ...props}: EpisodeTileProps & {entry: ContentEntry; more: NativeTitleScreenProps['more']}) {
  const anchor = useRef<View>(null);
  return <EpisodeTile {...props} menuAnchorRef={anchor} onMore={() => more(entry, anchor)}/>;
}
/** The waiting host of a related row's next page: a cursor, never a title. */
function relatedWaiting(waitingId: string, like: ContentEntry | undefined): ContentEntry {
  return {id: waitingId, kind: like?.kind ?? 'movie', title: '', libraryId: like?.libraryId} as ContentEntry;
}

export function TitleRelated({entry, more, ...props}: PosterCardProps & {entry: ContentEntry; more: NativeTitleScreenProps['more']}) {
  const anchor = useRef<View>(null);
  return <PosterCard {...props} menuAnchorRef={anchor} onMore={() => more(entry, anchor)}/>;
}

/** Touch keeps its original View ancestry; TV opts into the shared native guide policy. */
function TitleRegion({children, style, ...config}: Partial<FocusRegionProps>) {
  const {isTV} = useTheme();
  return isTV && config.id ? <FocusRegion {...config} id={config.id} style={style}>{children}</FocusRegion> : <View style={style}>{children}</View>;
}

/** The retained touch title keeps its original ancestry; TV animates its hero copy. */
function TitleHeroCopy({browsing, style, children}: React.ComponentProps<typeof TVTitleHeroCopy>) {
  const {isTV} = useTheme();
  return isTV ? <TVTitleHeroCopy browsing={browsing} style={style}>{children}</TVTitleHeroCopy> : <View style={style}>{children}</View>;
}
