import {groupPort, subscribeGroupPort} from '../settings-live/group-port';
import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Dimensions, StatusBar, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Animated, {cancelAnimation, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import {resolvePlayerPresentation, playerControlsRevealDeadline, playerPostureCrossFadeMs, type PlayerPresentation, type PlayerPresentationInput, type PlayerRect} from '../../platform/layout/player-presentation';
import {usePlatformPosture} from '../../platform/platformPosture';
import {useMotion} from '../../kit/useMotion';
import {useInitialPlayerPaint} from '../../kit/dock/PlayerMorphBridge';
import {useOptionalFocusRegistry} from '../../kit/focus';
import {useScreenReader} from '../../kit/useScreenReader';
import {TVPanelTransition, TVPanelSlide, TVPanelScrim, useTVPanelPresence} from '../../kit/overlays/TVPanelMotion';
import type {NativePlaybackAdapter, NativePlaybackReadLifetime, NativePlaybackRuntimeSnapshot} from '../../platform/playback/types';
import type {NativeWatchNotice} from './playback-info';
import type {NativePlaybackFailure} from './PlaybackFailed';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeWatchOutletPort} from '../../shell/watch-outlet';
import {NativeWatchChrome, type NativeWatchChromeProps} from './WatchChrome';
import {NativeWatchPicture} from './picture-slot';
import {NativeHandheldMiniGuide} from './HandheldMiniGuide';
import {emptyLiveGuide, noLiveGuideSubscription, type NativeTVLiveGuide} from './live-guide';
import {nativeWatchTVPresentation} from './tv-presentation';
import type {NativeWatchControls, NativeWatchControlContext, NativeWatchControlState, NativeWatchPanel} from './controls';

export type NativeWatchHostContext = Readonly<{
  source: NativePlaybackRuntimeSnapshot;
  fence: PlaybackFence;
  layout: PlayerPresentation;
  state: NativeWatchControlState;
  runtime: NativeShellRuntime;
  /** Drawn UI coordinates. Native surface measurements always use layout.surface. */
  renderer: Readonly<{width: number; height: number; scale: number; panel?: PlayerRect}>;
  closePanel: () => void;
  backPanel: () => void;
  openPanel: (panel: NativeWatchPanel) => void;
  rememberPanel: (offset: number) => void;
  panelCurrent: () => boolean;
  panelExited: () => void;
}>;
/** Genuine producer labels/settings and the owned content/panel renderers. No
 * default track, invented quality, engine, route or copied presentation store. */
export type NativeWatchHostBindings = Readonly<{
  chrome: (source: NativePlaybackRuntimeSnapshot, runtime: NativeShellRuntime) => Omit<NativeWatchChromeProps, 'source' | 'controls' | 'playback' | 'text' | 'width' | 'height' | 'compact' | 'fullWindowRequest' | 'onPanel' | 'edgeInsets' | 'tvPanelRight' | 'tablet' | 'tabletop' | 'bar'>;
  /** Touch Guide uses the same measured panel geometry as player options. */
  liveGuide?: NativeTVLiveGuide;
  content?: (context: NativeWatchHostContext) => React.ReactNode;
  details?: (context: NativeWatchHostContext) => React.ReactNode;
  /** foldable5: the episodes that follow, beside the tabletop's controls column. */
  episodes?: (context: NativeWatchHostContext) => React.ReactNode;
  panel: (context: NativeWatchHostContext) => React.ReactNode;
  /** A title that ended with nothing queued after it: its own end screen. */
  postPlay?: (context: NativeWatchHostContext) => React.ReactNode;
  /** states 11: the failed-playback state, its own resume port and (where sanctioned) owner facts. */
  failed?: (context: NativeWatchHostContext, failure: NativePlaybackFailure, chromeVisible: boolean) => React.ReactNode;
  /** True when the failure renderer actually offers a retry, details or version control. */
  failureAction?: (context: NativeWatchHostContext, failure: NativePlaybackFailure) => boolean;
  /** Shell owns status/navigation application; Watch supplies canonical facts. */
  presentation?: (layout: PlayerPresentation | undefined) => void;
  scrubbing?: boolean;
  focusHeld?: boolean;
  /** tv-player/phone-player 5 and 13: the committed plan's own loss note, read once per playback. */
  noticePreferences?: Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => boolean}>;
  notice?: (fence: PlaybackFence, lifetime: NativePlaybackReadLifetime) => Promise<NativeWatchNotice | undefined>;
}>;
type Props = Readonly<{
  authority: ShellAuthority;
  current: () => ShellAuthority;
  playback: NativePlaybackAdapter;
  outlet: NativeWatchOutletPort;
  runtime: NativeShellRuntime;
  controls: NativeWatchControls;
  bindings: NativeWatchHostBindings;
  isTV?: boolean;
  setPresentation: (context: Omit<NativeWatchControlContext, 'expanded' | 'paused'>) => void;
}>;
const noNoticeSubscription = () => () => {};
const noticesOn = () => true;
const groupPlayerVisible = () => groupPort()?.playerVisible?.() !== false;
const positioned = (pane: PlayerRect) => ({position: 'absolute' as const, left: pane.x, top: pane.y, width: pane.width, height: pane.height});
const rectKey = (pane: PlayerRect | undefined) => pane ? `${pane.x},${pane.y},${pane.width},${pane.height}` : '';

/** Persistent chrome sibling above Accounts' single Surface. A transparent
 * measured surface pane is the only native-slot producer, including letterbox.
 * Posture changes never navigate, key a player, or recreate title memory. */
export function NativeWatchHost({authority, current, playback, outlet, runtime, controls, bindings, setPresentation, isTV = false}: Props) {
  const noticesEnabled = useSyncExternalStore(bindings.noticePreferences?.subscribe ?? noNoticeSubscription, bindings.noticePreferences?.getSnapshot ?? noticesOn);
  const source = useSyncExternalStore(playback.subscribe, playback.getSnapshot);
  const groupVisible = useSyncExternalStore(subscribeGroupPort, groupPlayerVisible, noticesOn);
  const state = useSyncExternalStore(controls.subscribe, controls.getSnapshot);
  const liveGuide = useSyncExternalStore(bindings.liveGuide?.subscribe ?? noLiveGuideSubscription, bindings.liveGuide?.getSnapshot ?? emptyLiveGuide);
  const window = useWindowDimensions(), posture = usePlatformPosture(), safeTop = useSafeAreaInsets().top;
  const local = source.dock.local, fence = source.fence;
  const {scrubbing, focusHeld, presentation} = bindings;
  const screenReader = useScreenReader(), registry = useOptionalFocusRegistry();
  const expanded = (!isTV || groupVisible) && !source.retired && sameAuthority(authority, current()) && sameAuthority(authority, source.authority)
    && !!fence && !!local && sameFence(local.fence, fence) && local.expanded && (local.kind === 'video' || local.kind === 'live');
  const guideOpen = !isTV && expanded && sameFence(liveGuide.fence, fence) && liveGuide.open && !state.panel;
  const panelOpen = guideOpen || expanded && sameFence(state.fence, fence) && !!state.panel;
  const [lossNotice, setLossNotice] = useState<{key: string; value: NativeWatchNotice}>();
  const noticeLive = useRef({read: bindings.notice, runtime, playback, fence, authority: source.authority, disposed: source.retired, expanded});
  noticeLive.current = {read: bindings.notice, runtime, playback, fence, authority: source.authority, disposed: source.retired, expanded};
  const noticeKey = fence ? JSON.stringify(fence) : undefined;
  useEffect(() => {
    const live = noticeLive.current;
    if (!live.read || !live.fence || !live.expanded || live.disposed || !noticeKey || lossNotice?.key === noticeKey) return;
    const readFence = live.fence;
    const abort = new AbortController();
    const isLive = () => !abort.signal.aborted && noticeLive.current.runtime === live.runtime && noticeLive.current.playback === live.playback
      && !!noticeLive.current.fence && sameFence(noticeLive.current.fence, readFence) && noticeLive.current.authority === live.authority
      && !noticeLive.current.disposed;
    void live.read(readFence, {signal: abort.signal, isCurrent: isLive}).then(value => {
      if (value && isLive()) setLossNotice({key: noticeKey, value});
    }).catch(() => {});
    return () => abort.abort();
  }, [noticeKey, expanded, lossNotice?.key]);
  const input = useMemo<PlayerPresentationInput>(() => ({window: {width: window.width, height: window.height}, posture, docked: !expanded, panelOpen: !isTV && panelOpen,
    upNext: !isTV && !!fence && fence.kind === 'queue' && source.entry?.kind === 'episode' && !!source.entry.show?.id}),
    [window.width, window.height, posture, expanded, panelOpen, isTV, fence, source.entry]);
  const television = useMemo(() => isTV ? nativeWatchTVPresentation(input.window, !expanded, panelOpen ? state.panel : undefined) : undefined, [isTV, input.window, expanded, panelOpen, state.panel]);
  const layout = useMemo(() => isTV ? television?.layout : resolvePlayerPresentation(input), [input, isTV, television]);
  // The full-window player has no system status bar. This host owns its own stack entry and
  // pops only that entry, so a later owner's entry is never overridden by this cleanup.
  const fullWindow = layout?.mode === 'full-window';
  const statusEntry = useRef<ReturnType<typeof StatusBar.pushStackEntry> | undefined>(undefined);
  useLayoutEffect(() => {
    if (isTV || !fullWindow) return;
    const entry = StatusBar.pushStackEntry({hidden: true, animated: true});
    statusEntry.current = entry;
    return () => {
      if (statusEntry.current !== entry) return;
      statusEntry.current = undefined;
      StatusBar.popStackEntry(entry);
    };
  }, [fullWindow, isTV]);
  // The request dies with the host that made it; the native slot stays owner-fenced.
  useLayoutEffect(() => () => controls.releaseFullWindow(), [controls]);
  // Only the watch page may ask. Eligibility is published in a layout turn, never during render,
  // and an unmounted host answers false, so a retained chip cannot act for an abandoned render.
  const mounted = useRef(false), eligible = useRef(false);
  const [watchOnly, setWatchOnly] = useState(false);
  const fullWindowRequest = useCallback(() => watchOnly && mounted.current && eligible.current, [watchOnly]);
  useLayoutEffect(() => {
    // Replay-safe: every setup restores the witness, every cleanup clears it synchronously.
    mounted.current = true;
    return () => {mounted.current = false; eligible.current = false;};
  }, []);
  useLayoutEffect(() => {
    const next = !isTV && layout?.mode === 'watch';
    eligible.current = next;
    // One render when the committed eligibility changes, not on the next playback tick.
    setWatchOnly(previous => previous === next ? previous : next);
    return () => {eligible.current = false;};
  }, [isTV, layout?.mode]);
  // The accepted full-window shape keeps its own request; a committed half-fold does not.
  useLayoutEffect(() => {
    if (layout?.mode !== 'book' && layout?.mode !== 'tabletop') return;
    controls.releaseFullWindow();
  }, [layout?.mode, controls]);
  const previous = useRef<PlayerPresentationInput | undefined>(undefined), deadline = useRef(0);
  useLayoutEffect(() => {
    // Apply suppression/pinning first. Repeated progress and panel close alone
    // cannot issue another fold reveal or extend its three-second deadline.
    // states 11 keeps the retained bar up while nothing can play, so it never auto-hides on TV.
    setPresentation({autoHide: isTV && source.playback.phase === 'error' ? false : layout?.controlsAutoHide ?? false, hiddenByPanel: layout?.controlsHidden ?? false,
      scrubbing: scrubbing === true, focusHeld: focusHeld === true || screenReader});
    const next = isTV && panelOpen ? 0 : playerControlsRevealDeadline(previous.current, input, Date.now(), deadline.current);
    if (fence && next !== deadline.current && next > 0) controls.revealAfterLayout(fence, next);
    deadline.current = next; previous.current = input;
    presentation?.(layout);
  }, [input, layout, fence, controls, setPresentation, scrubbing, focusHeld, screenReader, presentation, isTV, panelOpen, source.playback.phase]);
  const measuredCurrent = useCallback(() => ({authority: current(), fence: playback.getSnapshot().fence, window: Dimensions.get('window')}), [current, playback]);
  // foldable9: a fold, unfold, turn or panel change re-lays out in one pass and
  // fades the chrome and pages once for 250ms (none with reduced motion). The
  // picture is the persistent surface below, never part of this fade, and a
  // television keeps its own presentation untouched.
  const motion = useMotion(), paneFade = useSharedValue(1), lastLayout = useRef<string | undefined>(undefined);
  const layoutKey = layout ? [layout.mode, rectKey(layout.surface), rectKey(layout.controls), rectKey(layout.content), rectKey(layout.details), rectKey(layout.panel)].join('|') : undefined;
  useLayoutEffect(() => {
    if (isTV || !layoutKey) return;
    // Reduced motion finishes the running fade at once, even though the layout
    // itself has not changed, and never starts another.
    if (motion.reduce) {cancelAnimation(paneFade); paneFade.value = 1; lastLayout.current = layoutKey; return;}
    if (lastLayout.current === layoutKey) return;
    if (lastLayout.current === undefined) {lastLayout.current = layoutKey; return;}
    lastLayout.current = layoutKey;
    cancelAnimation(paneFade);
    paneFade.value = 0;
    paneFade.value = withTiming(1, {...motion.timing(), duration: playerPostureCrossFadeMs});
  }, [layoutKey, motion, paneFade, isTV]);
  useEffect(() => () => {cancelAnimation(paneFade);}, [paneFade]);
  const initialPlayer = useInitialPlayerPaint(isTV ? undefined : fence), initialPage = initialPlayer?.page, reduced = motion.reduce;
  // The measured picture slot remains fixed; only the surrounding watch page
  // rises. Moving the slot would change the very target the artwork is using.
  const paneFadeStyle = useAnimatedStyle(() => initialPage ? {opacity: paneFade.value * initialPage.value,
    transform: [{translateY: reduced ? 0 : 24 * (1 - initialPage.value)}]} : {opacity: paneFade.value});
  // states 11: one failure names one exact fence, session and place. The hook is
  // declared above every conditional return; it only ever captures with a valid fence.
  const failureRef = useRef<{key: string; failure: NativePlaybackFailure} | undefined>(undefined);
  if (!expanded || !fence || !layout?.surface) return null;
  const context: NativeWatchHostContext = {source, fence, layout, state, runtime,
    renderer: {width: television?.canvas.width ?? window.width, height: television?.canvas.height ?? window.height, scale: television?.canvas.scale ?? 1,
      panel: television?.panel ?? (layout.mode === 'watch' ? layout.content : layout.panel)},
    closePanel: () => {controls.closePanel(fence);}, backPanel: () => {controls.back(fence);}, openPanel: panel => {controls.open(panel, fence, registry?.capture());}, rememberPanel: offset => controls.rememberPanel(fence, offset),
    panelCurrent: () => mounted.current && !!state.panel && sameFence(controls.getSnapshot().fence, fence) && controls.getSnapshot().panel === state.panel,
    panelExited: () => {if (state.panelExitRevision !== undefined) controls.panelExited(fence, state.panelExitRevision);}};
  // Tabletop controls live below the actual top surface. In book mode edge
  // chrome avoids the crease while transport remains centred in its full pane.
  const chromePane = layout.mode === 'tabletop' ? layout.controls : layout.surface;
  // A title with nothing queued ends on its own card, on the TV and in the phone's
  // full-window player (phone-player 6); the upright watch page keeps its own page.
  const postPlay = bindings.postPlay && !panelOpen && (isTV || layout.mode === 'full-window') && source.playback.phase === 'ended'
    && !(source.queue.view?.next.available === true) ? bindings.postPlay(context) : undefined;
  // Capture only when a valid fence names this failure; a render without one leaves
  // the last capture alone rather than inventing a fence or changing hook count.
  const failureKey = fence && source.playback.phase === 'error'
    ? JSON.stringify([fence, source.playback.intentId, source.playback.session?.id, source.playback.positionSeconds]) : undefined;
  if (failureKey === undefined) failureRef.current = undefined;
  else if (failureRef.current?.key !== failureKey) failureRef.current = {key: failureKey,
    failure: Object.freeze({fence, sessionId: source.playback.session?.id, positionSeconds: source.playback.positionSeconds, itemId: source.playback.itemId})};
  // states 11 is the ten-foot state: it is drawn on TV only, and phone keeps its own surface.
  // The same predicates the module uses to draw a recovery control. The bar points Up
  // to that region only when the control really exists this render.
  const failureAction = isTV && !panelOpen && !!failureRef.current && source.playback.phase === 'error'
    && (bindings.failureAction?.(context, failureRef.current.failure)
      ?? (source.capabilities.retry === true && !!playback.retry && !!fence && sameFence(source.fence, failureRef.current.failure.fence)));
  const failed = bindings.failed && !panelOpen && failureRef.current
    ? bindings.failed(context, failureRef.current.failure, state.visible) : undefined;
  const content = <Animated.View testID="watch-panes" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted}
    importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{position: 'absolute', inset: 0}, paneFadeStyle]}>
    {postPlay ? <View testID="watch-post-play-pane" style={{position: 'absolute', inset: 0}}>{postPlay}</View> : <>
    {layout.content && <View testID="watch-content-pane" style={positioned(layout.content)} pointerEvents="box-none">{bindings.content?.(context)}</View>}
    {layout.details && <View testID="watch-details-pane" style={positioned(layout.details)} pointerEvents="box-none">{bindings.details?.(context)}</View>}
    {layout.episodes && <View testID="watch-episodes-pane" style={positioned(layout.episodes)} pointerEvents="box-none">{bindings.episodes?.(context)}</View>}
    {chromePane && <View testID="watch-controls-pane" pointerEvents="box-none" style={positioned(television ? {x: 0, y: 0, width: television.canvas.width, height: television.canvas.height} : chromePane)}>
      <NativeWatchChrome {...bindings.chrome(source, runtime)} notice={noticesEnabled && expanded && lossNotice && lossNotice.key === noticeKey && layout.mode !== 'watch' && layout.mode !== 'book' ? {key: lossNotice.key, value: lossNotice.value, shownAt: 0} : undefined} source={source} controls={controls} playback={playback} text={runtime.text}
        width={television?.canvas.width ?? chromePane.width} height={television?.canvas.height ?? chromePane.height} compact={layout.mode === 'watch' || layout.mode === 'book'} fullWindowRequest={fullWindowRequest} tablet={!isTV && Math.min(window.width, window.height) >= 600}
        tabletop={!isTV && layout.mode === 'tabletop' && layout.episodes && layout.controls ? (layout.controls.width < window.width ? 'column' : 'stacked') : undefined}
        bar={!isTV && layout.mode === 'full-window' && window.width >= 1024}
        tvPanelRight={television?.backdropRight} failureAction={failureAction}
        edgeInsets={layout.mode === 'book' && layout.controls ? {right: layout.surface.width - layout.controls.width} : layout.mode === 'watch' && !isTV && safeTop > 0 ? {top: safeTop} : undefined}/>
    </View>}
    {isTV ? <TVWatchPanel context={context} render={bindings.panel}/> : panelOpen && <View testID="watch-panel-pane" pointerEvents="box-none" style={context.renderer.panel ? positioned(context.renderer.panel) : {position: 'absolute', inset: 0}}>
      {guideOpen && bindings.liveGuide ? <NativeHandheldMiniGuide key={JSON.stringify(fence)} context={context} guide={bindings.liveGuide} snapshot={liveGuide}/> : bindings.panel(context)}
    </View>}
    {failed && <View testID="watch-failed-pane" pointerEvents="box-none" style={isTV ? {position: 'absolute', inset: 0} : positioned(layout.surface)}>{failed}</View>}
    </>}
  </Animated.View>;
  return <View testID="watch-host" pointerEvents="box-none" style={{position: 'absolute', inset: 0}}>
    <NativeWatchPicture authority={authority} fence={fence} outlet={outlet} current={measuredCurrent} style={positioned(layout.surface)}/>
    {television ? <View testID="watch-tv-canvas" pointerEvents="box-none" style={{position: 'absolute', left: television.canvas.left, top: television.canvas.top,
      width: television.canvas.width, height: television.canvas.height, transformOrigin: 'top left', transform: [{scale: television.canvas.scale}]}}>{content}</View> : content}
  </View>;
}

/** The live controls state closes immediately; only inert outgoing paint is retained. */
function TVWatchPanel({context, render}: {context: NativeWatchHostContext; render: NativeWatchHostBindings['panel']}) {
  const presence = useTVPanelPresence(context.state.panel ? context : undefined, JSON.stringify(context.fence));
  const shown = presence.value;
  if (!shown) return null;
  const pane = shown.renderer.panel;
  return <TVPanelTransition open={presence.open} onExited={() => {presence.exited(); context.panelExited();}}>
    <TVPanelScrim/>
    <View testID="watch-panel-pane" pointerEvents="box-none" style={pane ? positioned(pane) : {position: 'absolute', inset: 0}}>
      <TVPanelSlide width={pane?.width ?? shown.renderer.width}>{render(shown)}</TVPanelSlide>
    </View>
  </TVPanelTransition>;
}
