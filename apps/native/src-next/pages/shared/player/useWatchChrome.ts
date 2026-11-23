import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {AccessibilityInfo, View} from 'react-native';
import {Gesture} from 'react-native-gesture-handler';
import {cancelAnimation, Easing, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {followedByScene, sameFence, UNDO_SECONDS, type PlaybackFence, type SegmentMarker} from '../../../../../../packages/player-core/src';
import type {TextEnvironment} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativePlaybackAdapter, NativePlaybackRuntimeSnapshot} from '../../../platform/playback/types';
import {useTVKeyEvent} from '../../../platform/tv-keys';
import {admitTVPlayPause, tvPlayPauseTogglesPlayback} from '../../../platform/tv-play-pause';
import {useTVPlayPauseOwner} from '../../../platform/tv-play-pause-owner';
import {useFocusRegistry} from '../../../kit/focus';
import {useTheme} from '../../../kit/theme';
import {useMotion} from '../../../kit/useMotion';
import {useScreenReader} from '../../../kit/useScreenReader';
import {NativeWatchControls, type NativeWatchPanel} from '../../../screens/watch/controls';
import {createLandingStore} from '../../../screens/watch/landing-store';
import {useSkipPromptMotion} from '../../../screens/watch/useSkipPromptMotion';
import {useInitialPlayerPaint, usePublishedPlayerMorph} from '../../../kit/dock/PlayerMorphBridge';
import type {ArtworkSource} from '../../../kit/artwork';
import type {NativeWatchNotice} from '../../../screens/watch/playback-info';
import {emptyLiveGuide, noLiveGuideSubscription, type NativeTVLiveGuide} from '../../../screens/watch/live-guide';
import {emptyLiveActions, noLiveActionsSubscription, type NativeTVLiveActions} from '../../../screens/watch/live-actions';

export type NativeWatchChromeProps = Readonly<{
  source: NativePlaybackRuntimeSnapshot;
  controls: NativeWatchControls;
  playback: Pick<NativePlaybackAdapter, 'getSnapshot' | 'toggle' | 'skip' | 'seek' | 'trickplay' | 'previous' | 'next' | 'goLive' | 'replay' | 'stop' | 'policyDismiss' | 'policySkip' | 'policyUndo' | 'policyAdvance' | 'policyCancelAutoplay'>;
  text: TextEnvironment;
  /** Delivered by the current-fence producers, never the first/default offer or a policy ceiling. */
  quality: string;
  audio?: string;
  subtitle?: string;
  /** Actual physical picture pane; centre transport is centred inside it, including book mode. */
  width: number;
  height: number;
  compact: boolean;
  /** The picture's waiting ring replaces its centre transport, including before the line's delay. */
  waiting?: boolean;
  /** Measured picture footer, including its bottom inset, kept clear by the waiting column. */
  onPictureControlsLayout?: (height: number) => void;
  /** Live, committed eligibility: the watch page only, never the book or tabletop pages that
   * `compact` also covers. A function so a retained chip handler re-checks the host at dispatch
   * instead of trusting this render's snapshot. */
  fullWindowRequest?: () => boolean;
  /** An expanded-size-class device (iPad): tablet-play3 draws an 84 pt centre button. */
  tablet?: boolean;
  /** foldable4/5: the controls column under a horizontal hinge's top surface. */
  tabletop?: 'column' | 'stacked';
  /** tablet-play1: an expanded window draws the desktop player's two bars. */
  bar?: boolean;
  /** Book-mode edge chrome avoids the padded crease; centre transport does not move. */
  edgeInsets?: Readonly<{left?: number; right?: number; /** The upright player's picture starts at the screen's top edge, under the status bar. */ top?: number}>;
  skipBackSeconds: number;
  skipForwardSeconds: number;
  onMinimize: (fence: PlaybackFence) => void;
  /** Live TV opens the mini guide for this presented session. */
  onGuide?: () => void;
  liveGuide?: NativeTVLiveGuide;
  liveActions?: NativeTVLiveActions;
  /** Channels only: return the same session to the live edge. */
  goLive?: (fence: PlaybackFence) => void;
  /** The current authority's artwork resolver, for the next episode's still. */
  artwork?: (path?: string) => ArtworkSource | undefined;
  onPanel?: (panel: NativeWatchPanel, fence: PlaybackFence) => void;
  /** TV4 keeps only identity/seek dimmed behind the authored overlay. */
  tvPanelRight?: number;
  reportError: (error: unknown) => void;
  onPictureInPicture?: (fence: PlaybackFence) => void;
  onCast?: (fence: PlaybackFence, anchorRef?: React.RefObject<View | null>) => void;
  /** states 11: the failure block drew a real action region this render, so Up can reach it. */
  failureAction?: boolean;
  /** tv-player/phone-player 5 and 13: the committed plan's one loss note, once per playback. */
  notice?: Readonly<{key: string; value: NativeWatchNotice; shownAt: number}>;
}>;

/** The player's controls over the shell's one retained surface: the live guide and actions, the prompts (skip, undo,
 * Up next, Still watching), the focus and remote keys, and the morph. Moved from screens/watch/WatchChrome as it was;
 * `WatchChromeView` draws what this returns. */
export function useWatchChrome(props: NativeWatchChromeProps) {
  const {source, controls, playback: originalPlayback, width, height, compact} = props;
  const t = defaultI18n.t;
  const wallClock = (atMs: number) => new Intl.DateTimeFormat(props.text.locale, {hour: 'numeric', minute: '2-digit'}).format(new Date(atMs));
  const theme = useTheme(), motion = useMotion(), state = useSyncExternalStore(controls.subscribe, controls.getSnapshot);
  const screenReader = useScreenReader(true);
  // Even a retained timeline/transport callback must consult the actual modal
  // owner at dispatch, rather than its pre-modal render's enabled boolean.
  const playback = useMemo<NativeWatchChromeProps['playback']>(() => {
    const guide = props.liveGuide;
    if (!guide) return originalPlayback;
    const allowed = (fence: PlaybackFence) => {const live = guide.getSnapshot(), actions = props.liveActions?.getSnapshot();
      return !(live.open && sameFence(live.fence, fence) || actions?.sheet && sameFence(actions.fence, fence));};
    return {...originalPlayback,
      toggle: async fence => {if (allowed(fence)) await originalPlayback.toggle(fence);},
      seek: async (fence, seconds) => {if (allowed(fence)) await originalPlayback.seek(fence, seconds);},
      skip: async (fence, seconds) => {if (allowed(fence)) await originalPlayback.skip(fence, seconds);},
      previous: async fence => {if (allowed(fence)) await originalPlayback.previous(fence);},
      next: async fence => {if (allowed(fence)) await originalPlayback.next(fence);},
      ...(originalPlayback.goLive ? {goLive: async (fence: PlaybackFence) => {if (allowed(fence)) await originalPlayback.goLive!(fence);}} : {}),
    };
  }, [originalPlayback, props.liveGuide, props.liveActions]);
  const registry = useFocusRegistry();
  useSyncExternalStore(registry.subscribe, registry.getSnapshot, registry.getSnapshot);
  const focus = registry.capture();
  const opacity = useSharedValue(0), toggle = useRef<View>(null), picture = useRef<View>(null), guideButton = useRef<View>(null), castButton = useRef<View>(null);
  const liveGuide = useSyncExternalStore(props.liveGuide?.subscribe ?? noLiveGuideSubscription, props.liveGuide?.getSnapshot ?? emptyLiveGuide);
  const miniRestoreProof = useMemo(() => props.liveGuide?.restoreProof(liveGuide) ?? (() => false), [props.liveGuide, liveGuide]);
  const liveActions = useSyncExternalStore(props.liveActions?.subscribe ?? noLiveActionsSubscription, props.liveActions?.getSnapshot ?? emptyLiveActions);
  const actionsRestoreProof = useMemo(() => props.liveActions?.restoreProof(liveActions) ?? (() => false), [props.liveActions, liveActions]);
  const miniReturn = useRef<{fence: PlaybackFence; current: () => boolean; target: 'guide' | 'more'} | undefined>(undefined);
  // A held scrub moves its landing time many times a second. The chrome keeps only whether a
  // scrub is under way; the time lives in a store read by the one hint that prints it (P2-71-PERF).
  const landingStore = useMemo(createLandingStore, []), [scrubbing, setScrubbing] = useState(false);
  const setLanding = useCallback((seconds: number | undefined) => {landingStore.set(seconds); setScrubbing(seconds !== undefined);}, [landingStore]);
  const [returnAt, setReturnAt] = useState(0), [now, setNow] = useState(() => Date.now());
  const promptRevealToToggle = useRef(false);
  const fence = source.fence;
  const current = useCallback(() => !!fence && !source.retired && sameFence(playback.getSnapshot().fence, fence) && sameFence(state.fence, fence), [fence, source.retired, playback, state.fence]);
  const visible = current() && state.visible;
  const liveCurrent = fence?.kind === 'channel' && sameFence(liveGuide.fence, fence);
  const miniOpen = liveCurrent && liveGuide.open;
  const actionsCurrent = fence?.kind === 'channel' && sameFence(liveActions.fence, fence);
  const actionsOpen = actionsCurrent && !!liveActions.sheet;
  // Live is the normal player with a channel in it (tv-player 11, tablet-play 8, desktop-live 7). Where the mini guide is
  // drawn over the picture (TV, and the full-window player's side panel) it holds the controls. The upright watch page
  // draws it below the picture, so the picture keeps its controls, minimise included (W4 F13: no way to minimise).
  const liveOverlayOpen = (theme.isTV || !compact) && miniOpen || actionsOpen;
  const miniScope = fence ? JSON.stringify(fence) : undefined;
  const [miniPresenceScope, setMiniPresenceScope] = useState<string>();
  const miniPaintPresent = theme.isTV && (miniOpen || !!miniScope && miniPresenceScope === miniScope);
  useLayoutEffect(() => {if (theme.isTV && miniOpen) setMiniPresenceScope(miniScope);}, [miniOpen, miniScope, theme.isTV]);
  const miniExited = useCallback(() => {setMiniPresenceScope(value => value === miniScope ? undefined : value);}, [miniScope]);
  const controlsEnabled = visible && !liveOverlayOpen && !miniPaintPresent && !state.panelReturning;
  const controlsCurrent = useCallback(() => {
    const owner = playback.getSnapshot(), live = props.liveGuide?.getSnapshot(), actions = props.liveActions?.getSnapshot();
    return current() && !owner.retired && owner.dock.local?.expanded === true && !controls.getSnapshot().panel && !controls.getSnapshot().panelReturning && !miniPaintPresent && !(fence && ((theme.isTV || !compact) && live?.open && sameFence(live.fence, fence) || actions?.sheet && sameFence(actions.fence, fence)));
  }, [current, playback, fence, props.liveGuide, props.liveActions, controls, miniPaintPresent, theme.isTV, compact]);
  // Moving between the controls keeps them up. Apple TV's swipes move focus without a key event reaching useTVKeyEvent,
  // so the hide timer ran from the first reveal and the controls went while the viewer was still on them.
  useEffect(() => {
    if (!theme.isTV || !fence || !visible) return;
    const at = (value = registry.capture()) => value && value.regionId.startsWith('watch-') && value.regionId !== 'watch-surface' ? `${value.regionId}\n${value.childId}` : undefined;
    let last = at();
    return registry.subscribe(() => {
      const next = at();
      if (!next || next === last) return;
      last = next;
      if (controlsCurrent()) controls.reveal(fence);
    });
  }, [theme.isTV, fence, visible, registry, controls, controlsCurrent]);
  useLayoutEffect(() => {
    if (!liveOverlayOpen || !fence) return;
    miniReturn.current = {fence, current: miniOpen ? miniRestoreProof : actionsRestoreProof, target: miniOpen ? 'guide' : 'more'};
    controls.holdOverlay(fence, true);
    return () => {controls.holdOverlay(fence, false);};
  }, [liveOverlayOpen, miniOpen, fence, controls, miniRestoreProof, actionsRestoreProof]);
  useEffect(() => {if (miniOpen && (theme.isTV && !visible || state.panel)) props.liveGuide?.close(liveGuide);}, [miniOpen, visible, state.panel, props.liveGuide, liveGuide, theme.isTV]);
  useEffect(() => {if (actionsOpen && (theme.isTV && !visible || state.panel)) props.liveActions?.close(liveActions.sheet);}, [actionsOpen, visible, state.panel, props.liveActions, liveActions.sheet, theme.isTV]);
  // Chrome and the native picture follow the same progress and release guard;
  // each physical detector still owns a distinct native RNGH instance.
  const pictureMorph = usePublishedPlayerMorph(fence);
  const initialPlayer = useInitialPlayerPaint(fence), initialControls = initialPlayer?.controls;
  const initialEntering = initialPlayer?.entering === true;
  const revealedInitial = useRef<object | undefined>(undefined);
  useEffect(() => {
    if (theme.isTV || !fence || !initialPlayer?.landed || revealedInitial.current === initialPlayer.key
      || !sameFence(playback.getSnapshot().fence, fence)) return;
    revealedInitial.current = initialPlayer.key;
    controls.reveal(fence, Date.now() + motion.duration('standard') + 4000);
  }, [initialPlayer, fence, theme.isTV, playback, controls, motion]);
  const minimizeSwipe = useMemo(() => pictureMorph?.chromeGesture(!theme.isTV && !state.panel && !liveOverlayOpen)
    ?? Gesture.Pan().enabled(false), [pictureMorph, theme.isTV, state.panel, liveOverlayOpen]);
  const pictureProgress = pictureMorph?.progress;
  const chromeMorphStyle = useAnimatedStyle(() => ({opacity: (pictureProgress ? 1 - Math.max(0, Math.min(1, pictureProgress.value)) : 1) * (initialControls?.value ?? 1)}));
  const seekFocused = theme.isTV && visible && !state.panel && focus?.regionId === 'watch-seek-region' && focus.childId === 'watch-seek';
  const [groupPillPresent, setGroupPillPresent] = useState(false);
  const groupPillHost = useCallback((host: number | undefined) => setGroupPillPresent(host !== undefined), []);
  const groupSheetChange = useCallback((open: boolean) => {if (fence) controls.holdOverlay(fence, open);}, [controls, fence]);
  // The retained playback owner supplies the shared policy's prompts, Undo and
  // countdown. Chrome paints those facts; it never infers a skip from a seek or
  // advances a separate wall clock while playback is paused.
  const policy = fence?.kind === 'queue' ? source.policy : undefined;
  const segments = policy?.markers ?? [];
  const promptScope = JSON.stringify(fence);
  const promptEnabled = current() && source.dock.local?.expanded === true && !state.panel && !liveOverlayOpen;
  const [dismissedUndo, setDismissedUndo] = useState<string>();
  // JUSTIN-ANSWERS #29: with a screen reader the Undo note stays, announced, until
  // used or dismissed. After the policy's own window it returns to the marker start.
  const [heldUndo, setHeldUndo] = useState<Readonly<{scope: string; marker: SegmentMarker}>>();
  const liveUndo = promptEnabled ? policy?.undo : undefined;
  useEffect(() => {if (screenReader && liveUndo) setHeldUndo({scope: promptScope, marker: liveUndo.marker});}, [screenReader, liveUndo, promptScope]);
  const held = screenReader && promptEnabled && heldUndo?.scope === promptScope ? heldUndo : undefined;
  const undo = liveUndo ?? (held ? {marker: held.marker, remainingSeconds: 0} : undefined);
  const undoKey = undo ? `${promptScope}:${undo.marker.id}` : undefined;
  const notice = undoKey !== dismissedUndo ? undo : undefined;
  const undoLine = useSharedValue(1);
  useEffect(() => {
    if (!notice) return;
    if (screenReader) {undoLine.value = 1; return;}
    undoLine.value = Math.max(0, Math.min(1, notice.remainingSeconds / UNDO_SECONDS));
    undoLine.value = withTiming(0, {duration: Math.max(0, notice.remainingSeconds) * 1000, easing: Easing.linear});
    return () => {cancelAnimation(undoLine);};
  }, [undoKey, notice, undoLine, screenReader]);
  const undoLineStyle = useAnimatedStyle(() => ({transform: [{scaleX: Math.max(0, Math.min(1, undoLine.value))}]}));
  const view = source.queue.view;
  const nextItem = view?.next.entryId ? view.items.find(item => item.entryId === view.next.entryId) : undefined;
  const policyPrompt = promptEnabled ? policy?.prompt : undefined;
  const endCard = promptEnabled && policy?.finished === true && !!nextItem && view?.next.available === true && (!policy.attention || policy.attention === 'still-watching');
  const prompt = !notice && policyPrompt?.kind === 'skip' ? policyPrompt.marker : undefined;
  const visibleOffer = !notice && nextItem && policyPrompt?.kind === 'up-next' ? policyPrompt.offer : undefined;
  const countdown = endCard ? policy?.endCountdown ?? null : policyPrompt?.kind === 'up-next' ? policyPrompt.countdown : null;
  const sceneAfter = policyPrompt?.kind === 'up-next' ? policyPrompt.sceneAfterCredits : !!prompt && prompt.kind === 'credits' && followedByScene(prompt, source.playback.duration);
  const credits = visibleOffer?.marker ?? segments.find(segment => segment.kind === 'credits');
  const promptMotion = useSkipPromptMotion(!endCard && !visibleOffer && !notice ? prompt : undefined, current() && source.dock.local?.expanded === true ? JSON.stringify(fence) : undefined, visible, !theme.isTV && !props.bar && !props.tabletop ? 90 : 0);
  const promptEntry = !promptEnabled ? undefined : endCard || visibleOffer ? 'watch-up-next-play' : notice ? 'watch-skip-undo' : prompt ? 'watch-skip-action' : undefined;
  const skipNoticeLabel = notice ? t('player.skipped', {kind: notice.marker.kind}) : undefined;
  useEffect(() => {
    if (promptEnabled && skipNoticeLabel) AccessibilityInfo.announceForAccessibility(`${skipNoticeLabel}, ${t('action.undo')}`);
  }, [promptEnabled, undoKey, skipNoticeLabel, t]);
  const promptLive = useRef<{entry: string | undefined; dismiss: () => boolean}>({entry: promptEntry, dismiss: () => false});
  promptLive.current = {entry: promptEntry, dismiss: () => {
    if (registry.capture()?.regionId !== 'watch-skip' || !promptLive.current.entry || !fence || !controlsCurrent()) return false;
    // Back can put away the note without executing Undo. Its actual lifetime and
    // return point remain owned by PlaybackPolicy for this same playback.
    if (endCard) {
      if (!playback.getSnapshot().policy?.endCountdown || !playback.policyCancelAutoplay) return false;
      playback.policyCancelAutoplay(fence);
    } else if (notice && undoKey) dismissUndo();
    else if (!playback.policyDismiss?.(fence)) return false;
    promptLive.current.entry = undefined; return true;
  }};
  useEffect(() => {
    if (!theme.isTV || !fence) return;
    return controls.registerPromptDismiss(fence, () => promptLive.current.dismiss());
  }, [theme.isTV, fence, controls]);
  const previousPrompt = useRef<string | undefined>(undefined), promptOwned = useRef(false);
  if (focus?.regionId === 'watch-skip') promptOwned.current = true;
  else if (focus) promptOwned.current = false;
  useEffect(() => {
    if (!theme.isTV || !fence) return;
    const previous = previousPrompt.current; previousPrompt.current = promptEntry;
    if (promptEntry && !visible) registry.focus({regionId: 'watch-skip', childId: promptEntry});
    else if (!promptEntry && previous && promptOwned.current) {
      controls.hide(fence); picture.current?.requestTVFocus?.();
    }
  }, [theme.isTV, fence, controls, registry, promptEntry, visible]);
  const wasSeekFocused = useRef(false);
  useEffect(() => {
    if (seekFocused && !wasSeekFocused.current) setReturnAt(source.playback.positionSeconds);
    if (!seekFocused) setLanding(undefined);
    wasSeekFocused.current = seekFocused;
  }, [seekFocused, setLanding, source.playback.positionSeconds]); // The return point is where the viewer was when the bar took focus.
  useEffect(() => {
    if (!theme.isTV || !visible) return;
    // The clock and "Ends" time are right the moment the controls rise, then tick on the minute.
    setNow(Date.now());
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {setNow(Date.now()); timer = setTimeout(tick, 60_000 - Date.now() % 60_000);};
    timer = setTimeout(tick, 60_000 - Date.now() % 60_000);
    return () => clearTimeout(timer);
  }, [theme.isTV, visible]);
  useEffect(() => {
    // motion.html 2.6/4.5: controls enter standard, timer exit slow; no geometry animation here.
    // Only the entrance suppresses a duplicate visibility fade. An ordinary
    // hide/toggle always retains its own opacity clock, even while firstFrame
    // is slow and the original poster is still present.
    opacity.value = visible && initialEntering ? 1 : withTiming(visible ? 1 : 0, motion.timing(visible || state.hideReason !== 'timer' ? 'standard' : 'slow'));
    return () => cancelAnimation(opacity);
  }, [visible, motion, opacity, state.hideReason, initialEntering]);
  const fade = useAnimatedStyle(() => ({opacity: opacity.value}));
  const returnedPanelTarget = useRef<typeof state.returnTarget>(undefined), lastChromeVisible = useRef(visible);
  useEffect(() => {
    const raised = visible && !lastChromeVisible.current;
    lastChromeVisible.current = visible;
    if (!theme.isTV || !fence || !current() || state.panel || state.panelReturning || liveOverlayOpen || miniPaintPresent) return;
    const returning = miniReturn.current;miniReturn.current = undefined;
    if (returning) {
      if (visible && sameFence(returning.fence, fence) && returning.current()) {
        if (returning.target === 'guide') guideButton.current?.requestTVFocus?.();
        else registry.focus({regionId: 'watch-live-actions', childId: 'watch-live-more'});
      }
      return;
    }
    if (visible && state.returnTarget && state.returnTarget !== returnedPanelTarget.current) {
      if (registry.focus(state.returnTarget)) returnedPanelTarget.current = state.returnTarget;
      else toggle.current?.requestTVFocus?.();
      return;
    }
    // F-PLAYER-60: a direction key on a prompt with the controls hidden raises them on Play/Pause.
    if (visible && promptRevealToToggle.current) {promptRevealToToggle.current = false; toggle.current?.requestTVFocus?.(); return;}
    const active = registry.capture();
    if (visible && !raised && active && ['watch-controls', 'watch-transport', 'watch-seek-region', 'watch-group', 'watch-live-actions', 'watch-live-edge', 'watch-skip'].includes(active.regionId)) return;
    if (!visible && promptLive.current.entry) {registry.focus({regionId: 'watch-skip', childId: promptLive.current.entry}); return;}
    const node = visible ? toggle.current : picture.current;
    if (visible && !raised) {node?.requestTVFocus?.(); return;}
    // Wait for the new target to become focusable before Android chooses the nearest control.
    const frame = requestAnimationFrame(() => node?.requestTVFocus?.());
    return () => cancelAnimationFrame(frame);
  }, [visible, fence, theme.isTV, current, state.panel, state.panelReturning, state.returnTarget, liveOverlayOpen, miniPaintPresent, registry]); // The native focus engine owns movement inside the region.
  // The full player gives Play/Pause its meaning while it is up and nothing over it does (a PIN pad).
  const playerKeys = () => theme.isTV && !!fence && current() && source.dock.local?.expanded === true && !state.panel && !state.panelReturning && !liveOverlayOpen && !miniPaintPresent;
  const ownsPlayPause = useTVPlayPauseOwner('surface', playerKeys);
  useTVKeyEvent(event => {
    if (!playerKeys() || !fence || event.phase === 'long-end') return;
    const onPrompt = registry.capture()?.regionId === 'watch-skip';
    if (onPrompt && event.key === 'select') return;
    if (event.key === 'playPause') {
      // F-PLAYER-1: with the controls hidden the remote's Play/Pause raises them on
      // Play/Pause and does nothing else; with them up it is the transport key.
      if (event.phase !== 'press' || !ownsPlayPause() || !admitTVPlayPause('key')) return;
      if (!controls.getSnapshot().visible) {
        if (onPrompt) promptRevealToToggle.current = true;
        controls.key(fence);
        return;
      }
      // The same admission as the on-screen Play/Pause button. Android's media
      // session already toggles, so only Apple TV toggles here.
      const owner = playback.getSnapshot();
      if (!tvPlayPauseTogglesPlayback() || !controlsCurrent() || !owner.capabilities.play && !owner.capabilities.pause) return;
      controls.reveal(fence); void playback.toggle(fence).catch(props.reportError);
      return;
    }
    // The remote's channel keys step a live channel exactly as the on-screen channel buttons do, controls up or not.
    if (event.key === 'channelUp' || event.key === 'channelDown') {
      const target = liveCurrent ? event.key === 'channelDown' ? liveGuide.previous : liveGuide.next : undefined;
      if (event.phase === 'press' && target && !liveGuide.busy && controlsCurrent()) void props.liveGuide?.tune(target, liveGuide, props.reportError);
      return;
    }
    if (['up', 'down', 'left', 'right', 'select'].includes(event.key)) {
      if (onPrompt && !visible) promptRevealToToggle.current = true;
      if (!controls.key(fence)) controls.reveal(fence);
    }
  });
  const dismissUndo = () => {if (undoKey) setDismissedUndo(undoKey); setHeldUndo(undefined);};
  return {props, dismissUndo, source, controls, width, height, compact, t, wallClock, theme, motion, state, playback, registry, focus, toggle, picture, guideButton, castButton, liveGuide, liveActions, landingStore, scrubbing, setLanding, returnAt, now, fence, current, visible, liveCurrent, miniOpen, actionsCurrent, liveOverlayOpen, miniExited, controlsEnabled, controlsCurrent, initialPlayer, minimizeSwipe, chromeMorphStyle, seekFocused, groupPillPresent, groupPillHost, groupSheetChange, policy, segments, setHeldUndo, held, undoKey, notice, undoLineStyle, view, nextItem, endCard, prompt, visibleOffer, countdown, sceneAfter, credits, promptMotion, promptEntry, skipNoticeLabel, fade};
}
