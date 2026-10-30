import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {TVControlLabel} from './TVControlLabel';
import {AccessibilityInfo, Text, View} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import Animated, {cancelAnimation, Easing, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import Svg, {Defs, LinearGradient, Path, Rect, Stop, Text as SvgText} from 'react-native-svg';
import {followedByScene, sameFence, UNDO_SECONDS, type PlaybackFence, type SegmentMarker} from '../../../../../packages/player-core/src';
import {channelSeconds} from '../../../../../packages/client-core/src/playback/linear-protocol';
import {channelTint, playbackIdentity, type TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import {clockText, durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativePlaybackAdapter, NativePlaybackRuntimeSnapshot} from '../../platform/playback/types';
import {useTVKeyEvent} from '../../platform/tv-keys';
import {Button, Focusable} from '../../kit/controls';
import {FocusRegion, useFocusRegistry} from '../../kit/focus';
import {Icon, type IconName} from '../../kit/Icon';
import {NativeTVGroupPill} from '../settings-live/GroupPill';
import {nativeType, useTheme} from '../../kit/theme';
import {useMotion, useReducedMotion} from '../../kit/useMotion';
import {useScreenReader} from '../../kit/useScreenReader';
import {NativeWatchControls, type NativeWatchPanel} from './controls';
import {NativeWatchTimeline} from './WatchTimeline';
import {createLandingStore, type LandingStore} from './landing-store';
import {useSkipPromptMotion} from './useSkipPromptMotion';
import {useInitialPlayerPaint, usePublishedPlayerMorph} from '../../kit/dock/PlayerMorphBridge';
import {NativeUpNextCard} from './UpNextCard';
import {NativeStillWatchingCard} from './StillWatchingCard';
import {Artwork, type ArtworkSource} from '../../kit/artwork';
import type {NativeWatchNotice} from './playback-info';
import {emptyLiveGuide, noLiveGuideSubscription, type NativeTVLiveGuide} from './live-guide';
import {nowNext} from '../../../../../packages/client-core/src/guide/now-next';
import {playbackUserMessage} from '../../../../../packages/client-core/src/playback-messages';
import {NativeMiniGuide} from './TVMiniGuide';
import {TVLiveActions} from './TVLiveActions';
import {emptyLiveActions, noLiveActionsSubscription, type NativeTVLiveActions} from './live-actions';

export type NativeWatchChromeProps = Readonly<{
  source: NativePlaybackRuntimeSnapshot;
  controls: NativeWatchControls;
  playback: Pick<NativePlaybackAdapter, 'getSnapshot' | 'toggle' | 'skip' | 'seek' | 'previous' | 'next' | 'goLive' | 'replay' | 'stop' | 'policyDismiss' | 'policySkip' | 'policyUndo' | 'policyAdvance' | 'policyCancelAutoplay'>;
  text: TextEnvironment;
  /** Delivered by the current-fence producers, never the first/default offer or a policy ceiling. */
  quality: string;
  audio?: string;
  subtitle?: string;
  /** Actual physical picture pane; centre transport is centred inside it, including book mode. */
  width: number;
  height: number;
  compact: boolean;
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

const NOTICE_SECONDS = 6;
/** tv-player/phone-player 5 and 13: one quiet line, once per playback, that never
 * takes focus and clears the top row. The plan's own words, not a code. */
function LossNotice({notice, top, gutter, available}: {notice: NonNullable<NativeWatchChromeProps['notice']>; top: number; gutter: number; available: number}) {
  const theme = useTheme(), reduced = useReducedMotion(), line = useSharedValue(1);
  const [expired, setExpired] = useState<string | undefined>(undefined);
  useEffect(() => {
    setExpired(undefined);
    line.value = 1;
    line.value = withTiming(0, {duration: reduced ? 0 : NOTICE_SECONDS * 1000, easing: Easing.linear});
    const timer = setTimeout(() => setExpired(notice.key), NOTICE_SECONDS * 1000);
    return () => {clearTimeout(timer); cancelAnimation(line);};
  }, [notice.key, line, reduced]);
  const drain = useAnimatedStyle(() => ({transform: [{scaleX: Math.max(0, Math.min(1, line.value))}]}));
  if (expired === notice.key) return null;
  const phone = !theme.isTV;
  const icon = notice.value.icon === 'picture' ? 'i-film' : 'i-audio';
  // playback-info 13: the phone's note is one 44 pt line inside a 28 pt glass
  // circle, inset 54 pt from the picture's right edge, with the remaining-time
  // line as a 2 pt foot (the lane's own override of the 3 pt base). It grows
  // only when a longer message needs a second line.
  return <View pointerEvents="none" accessibilityLiveRegion="polite" accessibilityRole="alert" testID="watch-loss-notice"
    style={{position: 'absolute', top, right: gutter, maxWidth: phone ? Math.max(0, available - gutter * 2) : 560, borderRadius: phone ? 12 : 20, overflow: 'hidden',
      paddingLeft: phone ? 8 : 20, paddingRight: phone ? 16 : 26, paddingTop: phone ? 0 : 14, paddingBottom: phone ? 0 : 18,
      ...(phone ? {minHeight: 44, justifyContent: 'center' as const, backgroundColor: 'rgba(12,25,35,.84)'} : {backgroundColor: 'rgba(9,19,27,.82)'}),
      boxShadow: phone ? 'inset 0 0 0 1px rgba(237,245,250,.1), 0 18px 36px -14px rgba(0,0,0,1)'
        : 'inset 0 0 0 1px rgba(237,245,250,.1), 0 18px 40px -18px rgba(0,0,0,.85)'}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 10 : 14}}>
      {phone
        ? <View style={{width: 28, height: 28, borderRadius: 14, alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(237,245,250,.1)'}}>
          <Icon name={icon} size={16} color={theme.colors.text}/>
        </View>
        : <Icon name={icon} size={24} color={theme.colors.textSecondary}/>}
      <Text style={[nativeType({size: phone ? 13.5 : 24, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {flexShrink: 1, color: theme.colors.textSecondary}]}>
        <Text style={[nativeType({size: phone ? 13.5 : 24, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{notice.value.lead}</Text>
        {` ${notice.value.detail}`}
      </Text>
    </View>
    {!reduced && (phone
      ? <Animated.View style={[{position: 'absolute', left: 0, bottom: 0, width: '100%', transformOrigin: 'left center', height: 2, backgroundColor: 'rgba(237,245,250,.38)'}, drain]}/>
      : <View style={{marginTop: 10, height: 2, borderRadius: 1, overflow: 'hidden', backgroundColor: 'rgba(237,245,250,.18)'}}>
        <Animated.View style={[{width: '100%', transformOrigin: 'left center', height: 2, backgroundColor: theme.colors.accentStrong}, drain]}/>
      </View>)}
  </View>;
}

function SkipIcon({seconds, forward, size, color}: {seconds: number; forward?: boolean; size: number; color: string}) {
  return <Svg width={size} height={size} color={color} viewBox="0 0 32 32" accessible={false}>
    <Path d={forward ? 'M25 10a11 11 0 1 1-8-4M25 3v7h-7' : 'M7 10a11 11 0 1 0 8-4M7 3v7h7'} stroke="currentColor" strokeWidth={2.3} fill="none" strokeLinecap="round" strokeLinejoin="round"/>
    <SvgText x={16} y={23} textAnchor="middle" fontFamily="Manrope-Bold" fontSize={11} fill="currentColor">{seconds}</SvgText>
  </Svg>;
}

/** Portico controls over the shell's one retained surface. This component owns no video view. */
export function NativeWatchChrome(props: NativeWatchChromeProps) {
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
  const liveOverlayOpen = miniOpen || actionsOpen;
  const miniScope = fence ? JSON.stringify(fence) : undefined;
  const [miniPresenceScope, setMiniPresenceScope] = useState<string>();
  const miniPaintPresent = theme.isTV && (miniOpen || !!miniScope && miniPresenceScope === miniScope);
  useLayoutEffect(() => {if (theme.isTV && miniOpen) setMiniPresenceScope(miniScope);}, [miniOpen, miniScope, theme.isTV]);
  const miniExited = useCallback(() => {setMiniPresenceScope(value => value === miniScope ? undefined : value);}, [miniScope]);
  const controlsEnabled = visible && !liveOverlayOpen && !miniPaintPresent && !state.panelReturning;
  const controlsCurrent = useCallback(() => {
    const owner = playback.getSnapshot(), live = props.liveGuide?.getSnapshot(), actions = props.liveActions?.getSnapshot();
    return current() && !owner.retired && owner.dock.local?.expanded === true && !controls.getSnapshot().panel && !controls.getSnapshot().panelReturning && !miniPaintPresent && !(fence && (live?.open && sameFence(live.fence, fence) || actions?.sheet && sameFence(actions.fence, fence)));
  }, [current, playback, fence, props.liveGuide, props.liveActions, controls, miniPaintPresent]);
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
  const returnedPanelTarget = useRef<typeof state.returnTarget>(undefined);
  useEffect(() => {
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
    if (visible && active && ['watch-controls', 'watch-transport', 'watch-seek-region', 'watch-group', 'watch-live-actions', 'watch-live-edge', 'watch-skip'].includes(active.regionId)) return;
    if (!visible && promptLive.current.entry) {registry.focus({regionId: 'watch-skip', childId: promptLive.current.entry}); return;}
    const node = visible ? toggle.current : picture.current;
    node?.requestTVFocus?.();
  }, [visible, fence, theme.isTV, current, state.panel, state.panelReturning, state.returnTarget, liveOverlayOpen, miniPaintPresent, registry]); // The native focus engine owns movement inside the region.
  useTVKeyEvent(event => {
    if (!theme.isTV || !fence || !current() || source.dock.local?.expanded !== true || state.panel || state.panelReturning || liveOverlayOpen || miniPaintPresent || event.phase === 'long-end') return;
    const onPrompt = registry.capture()?.regionId === 'watch-skip';
    if (onPrompt && event.key === 'select') return;
    if (['up', 'down', 'left', 'right', 'select'].includes(event.key)) {
      if (onPrompt && !visible) promptRevealToToggle.current = true;
      if (!controls.key(fence)) controls.reveal(fence);
    }
  });
  if (!fence || !current() || source.dock.local?.expanded !== true) return null;
  const act = (command: (expected: PlaybackFence) => Promise<void>) => {if (controlsCurrent()) {controls.reveal(fence); void command(fence).catch(props.reportError);}};
  const skipSegment = (marker: Pick<SegmentMarker, 'id'>) => {if (!controlsCurrent() || !playback.policySkip) return; if (theme.isTV) controls.hide(fence); if (controlsCurrent()) void playback.policySkip(fence, marker.id).catch(props.reportError);};
  const panel = (value: NativeWatchPanel) => {if (controlsCurrent() && controls.open(value, fence, registry.capture())) props.onPanel?.(value, fence);};
  const identity = source.subject ? playbackIdentity(source.subject, 'full', props.text) : undefined;
  const queue = fence.kind === 'queue' && (source.queue.view?.items.length ?? 0) > 1;
  // tv-player 11: a channel is the normal player with the guide's programme in it.
  const linear = fence?.kind === 'channel' ? source.playback.linear : undefined;
  const liveOffer = actionsCurrent ? liveActions.offer : undefined;
  const liveRow = liveOffer?.channel ?? source.channelGuide?.row ?? liveGuide.rows?.find(row => row.step === 0)?.channel;
  const channelNumber = liveRow?.number;
  const recordState = liveActions.recording?.state ?? liveOffer?.programme.recording?.state;
  const recordingLabel = (value?: string): string => {
    switch (value) {
      case 'scheduled': return t('guide.recordingScheduled');
      case 'conflicted': return t('web.dvr.state.conflicted');
      case 'waiting-source': return t('web.dvr.state.waitingSource');
      case 'waiting-guide': return t('web.dvr.state.waitingGuide');
      case 'preparing': return t('web.dvr.state.preparing');
      case 'recording': return t('player.live.recording');
      case 'finalizing': return t('web.dvr.state.finalizing');
      case 'completed': return t('web.dvr.state.completed');
      case 'incomplete-playable': return t('web.dvr.state.incomplete');
      case 'failed': return t('web.dvr.state.failed');
      case 'cancelled': return t('web.dvr.state.cancelled');
      case 'pending-delete': return t('web.dvr.state.pendingDelete');
      case 'deleted': return t('web.dvr.state.deleted');
      default: return t('player.live.record');
    }
  };
  const liveConfirmation = liveActions.confirmed === 'series' ? t('guide.seriesSet') : liveActions.recording && liveOffer
    ? ['scheduled', 'recording', 'preparing'].includes(liveActions.recording.state)
      ? t('player.live.scheduled', {title: liveActions.recording.programme.title}) : `${recordingLabel(liveActions.recording.state)}: ${liveActions.recording.programme.title}` : undefined;
  const liveEdge = linear ? channelSeconds(linear.media.liveEdgeUs) : undefined;
  const behindLive = liveEdge === undefined ? 0 : Math.max(0, liveEdge - source.playback.positionSeconds);
  const liveEdgeAction = theme.isTV && !!linear && behindLive > 5 && !!props.goLive && !scrubbing;
  const audioTracks = source.fence?.kind === 'local' ? source.localTracks?.audio.length ?? 0
    : source.options?.versions.find(version => version.id === source.session?.versionId)?.audio.length ?? source.options?.versions[0]?.audio.length ?? 0;
  const initials = (name: string) => name.split(/\s+/).filter(Boolean).slice(0, 3).map(word => word[0]!.toUpperCase()).join('');
  const channelMark = linear ? (linear.media.logoUrl
    ? <Artwork source={props.artwork?.(linear.media.logoUrl)} width={64} height={64} style={{borderRadius: 15, overflow: 'hidden', marginRight: 24}}/>
    : <View style={{width: 64, height: 64, borderRadius: 15, alignItems: 'center', justifyContent: 'center', marginRight: 24, backgroundColor: theme.colors.accentDeep}}>
        <Text style={[nativeType({size: 21, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: '#fff'}]}>{initials(linear.name)}</Text></View>) : null;
  if (theme.isTV && state.panel && props.tvPanelRight !== undefined) return <View testID="watch-tv-panel-backdrop" pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{width, height}}>
    {identity && <View style={{position: 'absolute', left: 96, right: props.tvPanelRight, top: 80}}>
      <Text numberOfLines={1} style={[nativeType({size: 44, weight: 800, tracking: -.015, lineHeight: 52}, theme.textScale), {color: theme.colors.text}]}>{identity.title}</Text>
      <Text numberOfLines={1} style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 32}, theme.textScale), {color: theme.colors.textSecondary}]}>{identity.subtitle.map(part => part.text).join(' ')}</Text>
    </View>}
    <View style={{position: 'absolute', left: 96, right: props.tvPanelRight, bottom: 118}}>
      <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={false} enabled={false} tv reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
    </View>
    <View style={{position: 'absolute', left: 96, right: props.tvPanelRight, bottom: 80, flexDirection: 'row', justifyContent: 'space-between'}}>
      <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 32}, theme.textScale), {color: theme.colors.textSecondary}]}>{clockText(source.playback.positionSeconds)}</Text>
      <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 32}, theme.textScale), {color: theme.colors.textSecondary}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>
    </View>
    <View style={{position: 'absolute', inset: 0, backgroundColor: '#04090d80'}}/>
  </View>;
  // The retained view answers whether it can start PiP; an admitted `unavailable`
  // (a television, an unsupported engine) hides the tool it cannot honour.
  const pipOffered = !!props.onPictureInPicture && !!source.pictureInPicture && source.pictureInPicture.state !== 'unavailable';
  const tablet = props.tablet === true, tabletop = props.tabletop, bar = props.bar === true && !tabletop;
  // foldable5: the 508-wide column is the frame's 46/56/84 transport with space
  // between its rows; foldable4 stacks the same rows with the frame's 12/12/9/10
  // steps over an 80 centre and keeps the Episodes pill above the two rows.
  const stacked = tabletop === 'stacked';
  const side = tabletop ? 22 : undefined;
  const big = theme.isTV ? 88 : bar ? 62 : tabletop ? (stacked ? 80 : 84) : compact ? (tablet ? 84 : 58) : 74;
  const small = theme.isTV ? 64 : bar ? 48 : tabletop ? 56 : compact ? 46 : 58;
  const channelStep = (direction: -1 | 1) => {
    const target = liveCurrent ? direction < 0 ? liveGuide.previous : liveGuide.next : undefined;
    const size = theme.isTV ? small : bar ? 48 : 46;
    if (!target) return <View style={{width: size, height: size}}/>;
    const id = direction < 0 ? 'watch-channel-down' : 'watch-channel-up';
    return <TVControlLabel focused={controlsEnabled && focus?.childId === id} label={t(direction < 0 ? 'player.channelDown' : 'player.channelUp')} detail={[target.channel.number, target.channel.name].filter(Boolean).join(' ')}>
      <Focusable focusId={id} testID={id} accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled || liveGuide.busy}
        accessibilityLabel={t(direction < 0 ? 'next.live.channelDown' : 'next.live.channelUp', {channel: target.channel.name, number: target.channel.number})}
        onPress={() => {if (controlsCurrent()) void props.liveGuide?.tune(target, liveGuide, props.reportError);}}
        style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center'}}>
        {theme.isTV && direction < 0 && !!target.channel.number && <Text style={[nativeType({size: 20, weight: 800, tracking: 0, lineHeight: 20}, theme.textScale), {color: theme.colors.textSecondary}]}>{target.channel.number}</Text>}
        <Icon name={direction < 0 ? 'i-chdown' : 'i-chup'} size={theme.isTV || bar ? 24 : 30} color={theme.colors.text}/>
        {!theme.isTV && bar && !!target.channel.number && <Text style={[theme.type.captionStrong, {fontSize: 11, color: theme.colors.textSecondary}]}>{target.channel.number}</Text>}
        {theme.isTV && direction > 0 && !!target.channel.number && <Text style={[nativeType({size: 20, weight: 800, tracking: 0, lineHeight: 20}, theme.textScale), {color: theme.colors.textSecondary}]}>{target.channel.number}</Text>}
      </Focusable>
    </TVControlLabel>;
  };
  // E-mobile-14b: Minimize is presentation only. It needs this screen current and not retired,
  // never the playback-control gates (a stopped or failed session still folds away).
  const minimizeCurrent = () => !source.retired && !playback.getSnapshot().retired;
  const icon = (id: string, name: IconName, label: string, onPress: () => void, primary = false, disabled = false, size?: number, gate: () => boolean = controlsCurrent) => <TVControlLabel focused={controlsEnabled && focus?.childId === `watch-${id}`} label={label} primary={primary} align={id === 'options' || id === 'pip' ? 'end' : 'center'}><Button ref={id === 'cast' ? castButton : undefined} focusId={`watch-${id}`} testID={`watch-${id}`} variant={primary ? 'primary' : theme.isTV ? 'icon' : 'text'} iconOnly icon={name} accessibilityLabel={label}
    focusable={controlsEnabled} disabled={disabled || !controlsEnabled} onPress={() => {if (gate()) onPress();}} style={{width: size ?? (primary ? big : small), height: size ?? (primary ? big : small), borderRadius: (size ?? (primary ? big : small)) / 2}}
    iconSize={size ? Math.round(size * .6) : primary ? theme.isTV ? 40 : 28 : theme.isTV ? 26 : 22}/></TVControlLabel>;
  const liveButtons = (size?: number, compact = true) => linear && liveOffer && props.liveActions ? <TVLiveActions enabled={controlsEnabled} size={size} compact={compact}
    record={liveOffer.channel.kind === 'live' && liveOffer.channel.recordAvailable ? {label: recordingLabel(recordState), recording: recordState === 'recording',
      busy: liveActions.busy, disabled: liveActions.busy || !!liveActions.recording || !!liveOffer.programme.recording,
      onPress: () => {if (controlsCurrent()) void props.liveActions?.record(liveActions, props.reportError);}} : undefined}
    more={{disabled: liveActions.busy, onPress: () => {if (controlsCurrent()) props.liveActions?.openMore(liveActions);}}}/> : null;
  const skip = (forward: boolean) => <TVControlLabel focused={controlsEnabled && focus?.childId === (forward ? 'watch-skip-forward' : 'watch-skip-back')} label={t(forward ? 'player.skipForwardSeconds' : 'player.skipBackSeconds', {seconds: forward ? props.skipForwardSeconds : props.skipBackSeconds})}><Focusable focusId={forward ? 'watch-skip-forward' : 'watch-skip-back'} testID={forward ? 'watch-skip-forward' : 'watch-skip-back'} focusable={controlsEnabled} disabled={!controlsEnabled || !source.capabilities.seek} accessibilityRole="button"
    accessibilityLabel={t(forward ? 'player.skipForwardSeconds' : 'player.skipBackSeconds', {seconds: forward ? props.skipForwardSeconds : props.skipBackSeconds})}
    onPress={() => act(expected => playback.skip(expected, forward ? props.skipForwardSeconds : -props.skipBackSeconds))}
    style={{width: small, height: small, alignItems: 'center', justifyContent: 'center', opacity: source.capabilities.seek ? 1 : .4}}>
    <SkipIcon color={theme.colors.text} forward={forward} seconds={forward ? props.skipForwardSeconds : props.skipBackSeconds} size={theme.isTV ? 36 : compact ? 30 : 38}/>
  </Focusable></TVControlLabel>;
  const option = (name: NativeWatchPanel, label: string, value?: string) => <Focusable focusId={`watch-option-${name}`} testID={`watch-option-${name}`} accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled} onPress={() => panel(name)}
    style={({focused}) => theme.isTV
      ? {height: 64, minWidth: 92, maxWidth: 200, flexShrink: name === 'subtitles' ? 4 : name === 'quality' ? 0 : 1, paddingHorizontal: 18, justifyContent: 'center', borderRadius: 14, backgroundColor: state.panel === name ? 'rgba(237,245,250,.24)' : focused ? 'rgba(237,245,250,.28)' : 'rgba(237,245,250,.14)'}
      : {minHeight: 36, paddingHorizontal: 12, paddingVertical: 6, justifyContent: 'center', borderRadius: 8, backgroundColor: theme.colors.raisedHigh}}>
    {({focused}) => theme.isTV ? <><Text numberOfLines={1} style={[nativeType({size: 20, weight: 700, tracking: 0, lineHeight: 24}, theme.textScale), {color: theme.colors.text}]}>{label}</Text>{value && <Text numberOfLines={1} ellipsizeMode="tail" style={[nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 24}, theme.textScale), {color: focused ? theme.colors.white : theme.colors.textSecondary}]}>{value}</Text>}</> : <Text style={[theme.type.caption, {color: theme.colors.text}]}>{value ? label ? `${label} ${value}` : value : label}</Text>}
  </Focusable>;
  // phone-player 1: the pill row — Episodes, Subtitles, Audio and the Quality chip that
  // shows the current choice, last on the right.
  const chip = (name: NativeWatchPanel, icon: IconName, label: string, value: string | undefined, onPress?: () => void, tall = false) => <Focusable key={`${name}:${label}`}
    focusId={onPress ? `watch-chip-${name}` : `watch-option-${name}`} testID={onPress ? `watch-chip-${name}` : `watch-option-${name}`} accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
    onPress={() => {if (!controlsCurrent()) return;if (onPress) onPress(); else panel(name);}} style={{height: tall ? 44 : 36, paddingLeft: tall ? 14 : 11, paddingRight: tall ? 18 : 14, borderRadius: tall ? 22 : 18, flexDirection: 'row', alignItems: 'center', gap: 7,
      backgroundColor: 'rgba(237,245,250,.14)'}}>
    <Icon name={icon} size={18} color={theme.colors.textSecondary}/>
    {!!label && <Text style={{fontFamily: 'Manrope-Bold', fontSize: 13.5, fontWeight: '700', color: theme.colors.text}}>{label}</Text>}
    {!!value && <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: 13.5, fontWeight: '600', marginLeft: label ? 3 : 0, color: label ? theme.colors.textTertiary : theme.colors.text}}>{value}</Text>}
  </Focusable>;
  const qualityChip = (small = false) => <Focusable focusId="watch-option-quality" testID="watch-option-quality" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
    onPress={() => panel('quality')} style={small ? {height: 28, paddingLeft: 8, paddingRight: 10, borderRadius: 14, flexDirection: 'row', alignItems: 'center', gap: 5, backgroundColor: 'rgba(237,245,250,.16)'}
      : {height: 36, paddingLeft: 11, paddingRight: 14, borderRadius: 18, flexDirection: 'row', alignItems: 'center', gap: 6, backgroundColor: 'rgba(237,245,250,.14)'}}>
    <Icon name="i-quality" size={small ? 16 : 18} color={theme.colors.textSecondary}/>
    <Text style={{fontFamily: 'Manrope-Bold', fontSize: small ? 12.5 : 13.5, fontWeight: '700', color: theme.colors.text, fontVariant: ['tabular-nums']}}>{props.quality || t('player.opt.quality')}</Text>
  </Focusable>;
  const keycap = (label: string, icons?: IconName[]) => <View style={{minWidth: 44, height: 36, paddingHorizontal: 10, borderRadius: 9, borderWidth: 1.5, borderColor: 'rgba(237,245,250,.3)', flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 2}}>
    {icons ? icons.map(name => <Icon key={name} name={name} size={20} color={theme.colors.textSecondary}/>) : <Text style={[nativeType({size: 17, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{label}</Text>}
  </View>;
  const hint = (index: number, cap: React.ReactNode, text: React.ReactNode, key = String(text)) => <View key={key} style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginLeft: index ? 26 : 0}}>{cap}
    <Text style={[nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{text}</Text></View>;
  // states 11: while the failure owns the screen the bar's entry is its first real
  // bottom control, and Up reaches the failure block only when it drew one.
  const barEntry = theme.isTV && source.playback.phase === 'error' && fence
    ? fence.kind === 'queue' && fence.showId ? 'watch-option-episodes' : 'watch-option-quality' : 'watch-toggle';
  const barUp = theme.isTV && source.playback.phase === 'error' && props.failureAction === true;
  // states 11: only the centred transport gives way while nothing can play.
  const transport = source.playback.phase === 'error' && theme.isTV ? null : <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 22 : bar ? 14 : tabletop ? 36 : compact ? 12 : 38}}>
    {fence.kind === 'channel' ? channelStep(-1) : queue && icon('previous', 'i-prev', t('player.previous'), () => act(playback.previous), false, !source.capabilities.previous, bar ? 48 : theme.isTV || compact ? undefined : 46)}
    {skip(false)}
    <TVControlLabel focused={controlsEnabled && focus?.childId === 'watch-toggle'} label={t(source.playback.intent === 'playing' ? 'action.pause' : 'action.play')} primary><Button ref={toggle} testID="watch-toggle" focusId="watch-toggle" variant="primary" iconOnly icon={source.playback.intent === 'playing' ? 'i-pause' : 'i-play'}
      accessibilityLabel={t(source.playback.intent === 'playing' ? 'action.pause' : 'action.play')} hasTVPreferredFocus={false}
      focusable={controlsEnabled} disabled={!controlsEnabled || !source.capabilities.play && !source.capabilities.pause} onPress={() => act(playback.toggle)}
      style={{width: big, height: big, borderRadius: big / 2}} iconSize={theme.isTV ? 40 : compact && tablet ? 34 : 28}/></TVControlLabel>
    {skip(true)}
    {fence.kind === 'channel' ? channelStep(1) : queue && icon('next', 'i-next', t('player.next'), () => act(playback.next), false, !source.capabilities.next, bar ? 48 : theme.isTV || compact ? undefined : 46)}
  </View>;
  // tv-player 3 / phone-player 2: the same prompt, notice and offer on a phone,
  // where the controls may be down and the note's line rides its bottom edge.
  const phone = !theme.isTV;
  const endCurrent = () => {const live = playback.getSnapshot();return controlsCurrent() && live.policy?.finished === true && live.policy.attention === policy?.attention && live.queue.view?.next.entryId === nextItem?.entryId;};
  // phone-player 6e: on handhelds, Still watching? is its own centred question over a dimmed picture.
  const stillWatching = phone && endCard && policy?.attention === 'still-watching' && !!nextItem;
  const stillWatchingNode = stillWatching && nextItem ? <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, alignItems: 'center', justifyContent: 'center'}}>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, backgroundColor: 'rgba(3,8,12,.42)'}}/>
    <NativeStillWatchingCard item={nextItem}
      onConfirm={() => {
        const live = playback.getSnapshot();
        if (!endCurrent() || live.policy?.attention !== 'still-watching' || !playback.policyAdvance || live.queue.view?.next.entryId !== nextItem.entryId) return;
        void playback.policyAdvance(fence, 'still-watching').catch(props.reportError);
      }}
      onReplay={() => {if (endCurrent()) void playback.replay(fence).catch(props.reportError);}}
      onClose={() => {if (endCurrent()) void playback.stop(fence).catch(props.reportError);}}/>
  </View> : undefined;
  const skipOfferNode = !stillWatching && (endCard || visibleOffer) && nextItem ? <NativeUpNextCard compact={phone} item={nextItem} artwork={props.artwork?.(nextItem.stillUrl ?? nextItem.backdropUrl)} offer={visibleOffer}
    countdown={countdown} countdownTotal={view?.postPlay.countdownSeconds ?? 10} sceneAfter={!endCard && sceneAfter} onPlayNow={() => {
      const live = playback.getSnapshot(), mode = endCard && policy?.attention === 'still-watching' ? 'still-watching' : 'manual';
      if (!controlsCurrent() || !playback.policyAdvance || !live.queue.view?.next.available || live.queue.view.next.entryId !== nextItem.entryId
        || endCard && (!live.policy?.finished || (live.policy.attention === 'still-watching') !== (mode === 'still-watching'))) return;
      if (theme.isTV) controls.hide(fence);
      void playback.policyAdvance(fence, mode).catch(props.reportError);
    }}
    onWatchCredits={() => {if (controlsCurrent() && playback.policyDismiss?.(fence) && theme.isTV) controls.hide(fence);}} onSkipCredits={() => {if (credits) skipSegment(credits);}}
    postPlay={endCard ? {
      onCancel: () => {if (endCurrent() && playback.getSnapshot().policy?.endCountdown) playback.policyCancelAutoplay?.(fence);},
      onReplay: () => {if (endCurrent()) void playback.replay(fence).catch(props.reportError);},
      onClose: () => {if (endCurrent()) void playback.stop(fence).catch(props.reportError);},
    } : undefined}/> : undefined;
  const dismissUndo = () => {if (undoKey) setDismissedUndo(undoKey); setHeldUndo(undefined);};
  const undoSkip = () => {
    if (!notice || !controlsCurrent()) return;
    const current = playback.getSnapshot().policy?.undo;
    if (theme.isTV) controls.hide(fence);
    if (current?.marker.id === notice.marker.id && playback.policyUndo) {setHeldUndo(undefined); void playback.policyUndo(fence).catch(props.reportError); return;}
    // A held screen-reader note outlived the policy's window: return to where the segment began.
    if (held && held.marker.id === notice.marker.id) {dismissUndo(); void playback.seek(fence, held.marker.startSeconds).catch(props.reportError);}
  };
  const skipNoticeNode = notice ? <View accessible accessibilityLabel={skipNoticeLabel}
    accessibilityActions={[{name: 'activate', label: t('action.undo')}, {name: 'dismiss', label: t('next.overlays.native.dismissToast')}]}
    onAccessibilityAction={event => {if (event.nativeEvent.actionName === 'activate') undoSkip(); else if (event.nativeEvent.actionName === 'dismiss') dismissUndo();}}
    style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 14 : 28, paddingLeft: phone ? 16 : 30, paddingRight: 6, paddingVertical: phone ? 6 : 16,
    borderRadius: phone ? 12 : 22, overflow: 'hidden', backgroundColor: phone ? 'rgba(7,16,24,.76)' : 'rgba(9,19,27,.8)',
    boxShadow: `inset 0 0 0 1px rgba(237,245,250,.1), 0 14px 30px -12px rgba(0,0,0,.8)`}}>
    <View style={{gap: 8}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 8 : 12}}>
        {!phone && <Icon name="i-check" size={26} color={theme.colors.accentStrong}/>}
        <Text style={phone ? {fontFamily: 'Manrope-Bold', fontSize: 14.5, fontWeight: '600', color: theme.colors.textSecondary} : [nativeType({size: 24, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('player.skipped', {kind: notice.marker.kind})}</Text>
      </View>
      {!phone && <View style={{height: 3, width: 240, borderRadius: 2, backgroundColor: 'rgba(237,245,250,.18)'}}>
        <Animated.View style={[{width: '100%', transformOrigin: 'left center', height: 3, borderRadius: 2, backgroundColor: theme.colors.accentStrong}, undoLineStyle]}/>
      </View>}
    </View>
    <Button focusId="watch-skip-undo" testID="watch-skip-undo" variant={phone ? 'glass' : 'glass'} size={phone ? 'compact' : 'hero'} hasTVPreferredFocus={false} icon="i-back" iconSize={phone ? 18 : undefined}
      style={phone ? {height: 36, paddingHorizontal: 14, borderRadius: 8} : undefined} textStyle={phone ? {fontSize: 14.5, fontWeight: '700'} : undefined}
      onPress={undoSkip}>{t('action.undo')}</Button>
    {phone && <Animated.View style={[{position: 'absolute', left: 0, bottom: 0, width: '100%', transformOrigin: 'left center', height: 2, backgroundColor: 'rgba(237,245,250,.45)'}, undoLineStyle]}/>}
  </View> : undefined;
  const paintedPrompt = promptMotion.value;
  const skipPromptNode = paintedPrompt ? <Animated.View pointerEvents={promptMotion.interactive ? 'box-none' : 'none'} accessibilityElementsHidden={!promptMotion.interactive} importantForAccessibility={promptMotion.interactive ? 'auto' : 'no-hide-descendants'} style={[{flexDirection: 'row', alignItems: 'center', gap: 22}, promptMotion.bodyStyle]}>{paintedPrompt.kind === 'credits' && sceneAfter && !phone && <View style={{paddingHorizontal: 18, paddingVertical: 12, borderRadius: 12, backgroundColor: 'rgba(9,19,27,.78)'}}>
      <Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('player.post.sceneAfter')}</Text>
    </View>}
    <Button focusId="watch-skip-action" testID="watch-skip-action" disabled={!promptMotion.interactive} focusable={promptMotion.interactive} pressDurationMs={90} pressScale={.96} variant="primary" size={phone ? 'compact' : 'hero'} hasTVPreferredFocus={false} icon="i-next" iconSize={phone ? 18 : undefined}
      style={phone ? {height: 44, paddingHorizontal: 16, borderRadius: 10, opacity: 1} : {opacity: 1}} textStyle={phone ? {fontSize: 15, fontWeight: '700'} : undefined}
      onPress={promptMotion.interactive && prompt ? () => skipSegment(prompt) : undefined}>{t('player.skip', {kind: paintedPrompt.kind})}</Button></Animated.View> : undefined;
  const skipNodes = skipOfferNode ?? skipNoticeNode ?? skipPromptNode;
  // tablet-play1: an expanded window draws the desktop player's two bars — a top
  // bar with the way out, the title and the tools, and a bottom bar with the seek,
  // the times and one row holding the pills either side of a centred transport.
  if (bar) return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted} importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{width, height}, chromeMorphStyle]}>
    <Animated.View pointerEvents={visible ? 'box-none' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{position: 'absolute', inset: 0}, fade]}>
      <FocusRegion id="watch-controls" orientation="horizontal" entry="watch-toggle" style={{position: 'absolute', inset: 0}}>
        <View style={{flex: 1}}>
          <View style={{height: 115, paddingTop: 26, paddingHorizontal: 32, flexDirection: 'row', alignItems: 'flex-start', gap: 16}}>
            {icon('minimize', 'i-down', t('player.minimize'), () => props.onMinimize(fence), false, false, 48, minimizeCurrent)}
            {linear && <View testID="watch-live-identity" style={{flexDirection: 'row', alignItems: 'flex-start', gap: 14, flexShrink: 1, minWidth: 0, paddingTop: 2}}>
              <View style={{width: 48, height: 48, borderRadius: 11, overflow: 'hidden', alignItems: 'center', justifyContent: 'center', backgroundColor: liveRow?.mark ? channelTint(liveRow.mark.seed) : '#edf5fa14'}}>
                <Text numberOfLines={1} style={[nativeType({size: 13, weight: 800, tracking: -.01, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{liveRow?.mark?.text ?? channelNumber}</Text>
                {linear.media.logoUrl && <Artwork source={props.artwork?.(linear.media.logoUrl)} width={48} height={48} style={{position: 'absolute', inset: 0}}/>}
              </View>
              <View style={{flexShrink: 1, minWidth: 0}}>
                <Text numberOfLines={1} style={[nativeType({size: 26, weight: 800, tracking: -.03, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{linear.programme?.title ?? linear.name}</Text>
                <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 1}}>
                  <Text numberOfLines={1} style={[nativeType({size: 15, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary, flexShrink: 1}]}>{linear.name}</Text>
                  {!!channelNumber && <Text style={[theme.type.caption, {fontSize: 15, color: theme.colors.textSecondary}]}>{channelNumber}</Text>}
                  <View style={{flexDirection: 'row', alignItems: 'center', gap: 6}}><View style={{width: 7, height: 7, borderRadius: 3.5, backgroundColor: theme.colors.record}}/>
                    <Text style={[theme.type.captionStrong, {fontSize: 13.5, color: theme.colors.record}]}>{t('next.kit.live')}</Text></View>
                </View>
                {linear.programme && <Text numberOfLines={1} style={[theme.type.caption, {fontSize: 15, marginTop: 4, color: theme.colors.textSecondary}]}>{t('next.kit.range', {start: wallClock(linear.programme.startMs), end: wallClock(linear.programme.endMs)})}</Text>}
              </View>
            </View>}
            {!linear && identity && <View style={{flexShrink: 1, minWidth: 0, paddingTop: 2}}>
              <Text numberOfLines={1} style={{fontFamily: 'Manrope-ExtraBold', fontSize: 26, fontWeight: '800', letterSpacing: -.03, color: theme.colors.text}}>{identity.title}</Text>
              <Text numberOfLines={1} style={{fontFamily: 'Manrope-SemiBold', fontSize: 15, fontWeight: '600', marginTop: 1, color: theme.colors.textSecondary}}>{identity.subtitle.map(part => part.text).join('  ')}</Text>
            </View>}
            <View style={{flex: 1}}/>
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
              {props.onCast && !linear && icon('cast', 'i-cast', t('player.playOn'), () => props.onCast!(fence, castButton), false, false, 48)}
              {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence), false, false, 48)}
              {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'), false, false, 48)}
              {liveButtons(48, false)}
            </View>
          </View>
          <View style={{marginTop: 'auto', height: 164}}>
            <View style={{height: 44, marginHorizontal: 32}}>
              <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={false} enabled={controlsEnabled} liveEdge={liveEdge} onPreview={setLanding}
                reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
            </View>
            <View style={{height: 18, marginHorizontal: 32, flexDirection: 'row', alignItems: 'center'}}>
              <Text style={[theme.type.bodyStrong, {fontSize: 13, color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{clockText(source.playback.positionSeconds)}</Text>
              <View style={{flex: 1}}/>
              {linear && behindLive > 5 && props.goLive
                ? <Focusable focusId="watch-go-live" testID="watch-go-live" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
                    onPress={() => {if (controlsCurrent() && props.goLive) {controls.reveal(fence); props.goLive(fence);}}}
                    style={{height: 18, paddingHorizontal: 8, borderRadius: 9, flexDirection: 'row', alignItems: 'center', gap: 6, backgroundColor: 'rgba(237,245,250,.14)'}}>
                    <View style={{width: 6, height: 6, borderRadius: 3, backgroundColor: theme.colors.record}}/>
                    <Text style={{fontFamily: 'Manrope-Bold', fontSize: 12, fontWeight: '700', color: theme.colors.text}}>{t('player.live.goLive')}</Text>
                  </Focusable>
                : <Text style={[theme.type.bodyStrong, {fontSize: 13, color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>}
            </View>
            <View style={{height: 62, marginTop: 8}}>
              <View pointerEvents="box-none" style={{position: 'absolute', left: 32, right: 32, top: 0, height: 62, flexDirection: 'row', alignItems: 'center'}}>
                {linear
                  ? (props.onGuide ? chip('episodes', 'i-list', t('player.live.guide'), undefined, () => {if (controlsCurrent() && props.onGuide) props.onGuide();}, true) : null)
                  : fence.kind === 'queue' && fence.showId ? chip('episodes', 'i-stack', t('title.episodes'), undefined, undefined, true) : null}
                <View style={{flex: 1}}/>
                {source.capabilities.subtitleTrack && chip('subtitles', 'i-cc', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined), undefined, true)}
                {source.capabilities.audioTrack && audioTracks > 1 && chip('audio', 'i-audio', t('title.audio'), props.audio, undefined, true)}
                {chip('quality', 'i-quality', t('player.opt.quality'), props.quality, undefined, true)}
              </View>
              <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, top: 0, height: 62, alignItems: 'center', justifyContent: 'center'}}>{transport}</View>
            </View>
          </View>
        </View>
      </FocusRegion>
    </Animated.View>
    {stillWatchingNode}
    {skipNodes && <View pointerEvents="box-none" style={{position: 'absolute', right: 32, bottom: 180}}>
      <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>{skipNodes}</View>
    </View>}
    {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={115} gutter={32} available={width}/>}
  </Animated.View></GestureDetector>;
  if (tabletop) return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted} importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{width, height}, chromeMorphStyle]}>
    <Animated.View pointerEvents={visible ? 'box-none' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{position: 'absolute', inset: 0}, fade]}>
      <FocusRegion id="watch-controls" orientation="horizontal" entry="watch-toggle" style={{position: 'absolute', inset: 0}}>
        <View style={{flex: 1, flexDirection: 'column', justifyContent: stacked ? 'flex-start' : 'space-between', paddingTop: stacked ? 12 : 0}}>
          <View style={{height: 44, flexDirection: 'row', alignItems: 'center', gap: 6}}>
            {icon('minimize', 'i-down', t('player.minimize'), () => props.onMinimize(fence), false, false, 44, minimizeCurrent)}
            {!linear && identity && <View style={{flexShrink: 1, minWidth: 0, marginLeft: 8}}>
              <Text numberOfLines={1} style={{fontFamily: 'Manrope-ExtraBold', fontSize: 20, fontWeight: '800', letterSpacing: -.025, color: theme.colors.text}}>{identity.title}</Text>
              <Text numberOfLines={1} style={{fontFamily: 'Manrope-SemiBold', fontSize: 13.5, fontWeight: '600', marginTop: 1, color: theme.colors.textSecondary}}>{identity.subtitle.map(part => part.text).join('  ')}</Text>
            </View>}
            <View style={{flex: 1}}/>
            {props.onCast && !linear && icon('cast', 'i-cast', t('player.playOn'), () => props.onCast!(fence, castButton), false, false, 44)}
            {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence), false, false, 44)}
            {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'), false, false, 44)}
            {liveButtons(44)}
          </View>
          <View style={{height: 44, flexDirection: 'row', alignItems: 'center', gap: 14, marginTop: stacked ? 12 : 0}}>
            <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{clockText(source.playback.positionSeconds)}</Text>
            <View style={{flex: 1}}>
              <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={false} enabled={controlsEnabled} liveEdge={liveEdge} onPreview={setLanding}
                reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
            </View>
            <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>
          </View>
          <View style={{height: stacked ? 80 : 84, alignItems: 'center', justifyContent: 'center', marginTop: stacked ? 9 : 0}}>{transport}</View>
          <View style={{height: 44, flexDirection: 'row', alignItems: 'center', gap: 8, marginTop: stacked ? 10 : 0}}>
            {(stacked || linear) && (linear
              ? (props.onGuide ? chip('episodes', 'i-list', t('player.live.guide'), undefined, () => {if (controlsCurrent() && props.onGuide) props.onGuide();}, true) : null)
              : fence.kind === 'queue' && fence.showId ? chip('episodes', 'i-stack', t('title.episodes'), undefined, undefined, true) : null)}
            <View style={{flex: 1}}/>
            {source.capabilities.subtitleTrack && chip('subtitles', 'i-cc', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined), undefined, true)}
            {source.capabilities.audioTrack && audioTracks > 1 && chip('audio', 'i-audio', t('title.audio'), props.audio, undefined, true)}
            {chip('quality', 'i-quality', t('player.opt.quality'), props.quality, undefined, true)}
          </View>
        </View>
      </FocusRegion>
    </Animated.View>
    {stillWatchingNode}
    {skipNodes && <View pointerEvents="box-none" style={{position: 'absolute', left: 22, right: 22, bottom: 52}}>
      <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>{skipNodes}</View>
    </View>}
    {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={4} gutter={22} available={width}/>}
  </Animated.View></GestureDetector>;
  return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted} importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{width, height}, chromeMorphStyle]}>
    <FocusRegion id="watch-surface" entry="watch-picture" style={{position: 'absolute', inset: 0}}><Focusable ref={picture} focusId="watch-picture" testID="watch-picture" focusTreatment="none" accessibilityRole="button" accessibilityLabel={identity?.title ?? t('player.opt.menu')}
      hasTVPreferredFocus={theme.isTV && !visible && !liveOverlayOpen && !promptEntry} focusable={theme.isTV && !visible && !state.panel && !liveOverlayOpen} onPress={() => {if (!controlsCurrent()) return;if (theme.isTV) controls.key(fence); else controls.toggleVisibility(fence);}} style={{position: 'absolute', inset: 0}}/></FocusRegion>
    <Animated.View pointerEvents={visible ? 'box-none' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{position: 'absolute', inset: 0}, fade]}>
      {theme.isTV ? <>
        <Svg pointerEvents="none" width="100%" height={400} style={{position: 'absolute', top: 0, left: 0, right: 0}}><Defs>
          <LinearGradient id="watch-top" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor="#04090d" stopOpacity={.88}/><Stop offset="0.38" stopColor="#04090d" stopOpacity={.6}/><Stop offset="0.72" stopColor="#04090d" stopOpacity={.2}/><Stop offset="1" stopColor="#04090d" stopOpacity={0}/></LinearGradient>
        </Defs><Rect width="100%" height="100%" fill="url(#watch-top)"/></Svg>
        <Svg pointerEvents="none" width="100%" height={600} style={{position: 'absolute', bottom: 0, left: 0, right: 0}}><Defs>
          <LinearGradient id="watch-bottom" x1="0" y1="1" x2="0" y2="0"><Stop offset="0" stopColor="#04090d" stopOpacity={.96}/><Stop offset="0.3" stopColor="#04090d" stopOpacity={.84}/><Stop offset="0.6" stopColor="#04090d" stopOpacity={.46}/><Stop offset="1" stopColor="#04090d" stopOpacity={0}/></LinearGradient>
        </Defs><Rect width="100%" height="100%" fill="url(#watch-bottom)"/></Svg>
      </> : <Svg pointerEvents="none" width="100%" height="100%" style={{position: 'absolute', inset: 0}}><Defs>
        <LinearGradient id="watch-top" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor="#03080c" stopOpacity={.82}/><Stop offset="0.36" stopColor="#03080c" stopOpacity={0}/></LinearGradient>
        <LinearGradient id="watch-bottom" x1="0" y1="1" x2="0" y2="0"><Stop offset="0" stopColor="#03080c" stopOpacity={.92}/><Stop offset="0.5" stopColor="#03080c" stopOpacity={0}/></LinearGradient>
      </Defs><Rect width="100%" height="100%" fill="url(#watch-top)"/><Rect width="100%" height="100%" fill="url(#watch-bottom)"/></Svg>}
      <FocusRegion id="watch-controls" orientation={theme.isTV ? "free" : "horizontal"} entry={theme.isTV ? undefined : barEntry} entryRegion={theme.isTV ? "watch-transport" : undefined} style={{position: 'absolute', inset: 0}}>
        <View style={{position: 'absolute', top: (compact ? 4 : theme.isTV ? 80 : 12) + (props.edgeInsets?.top ?? 0), left: (side ?? (compact ? 6 : theme.isTV ? 96 : 54)) + (props.edgeInsets?.left ?? 0), right: (side ?? (compact ? 6 : theme.isTV ? 96 : 54)) + (props.edgeInsets?.right ?? 0), flexDirection: 'row', alignItems: theme.isTV ? 'flex-start' : 'center'}}>
          {!theme.isTV && icon('minimize', 'i-down', t('player.minimize'), () => props.onMinimize(fence), false, false, undefined, minimizeCurrent)}
          {theme.isTV && linear && <>{channelMark}<View style={{flexShrink: 1, minWidth: 0}}>
            <Text numberOfLines={1} style={[nativeType({size: 44, weight: 800, tracking: -.03, lineHeight: 52}, theme.textScale), {color: theme.colors.text}]}>{linear.programme?.title ?? linear.name}</Text>
            <View style={{flexDirection: 'row', alignItems: 'center', minWidth: 0}}>
              <Text numberOfLines={1} style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 32}, theme.textScale), { color: theme.colors.text, flexShrink: 0}]}>{linear.name}</Text>
              {!!channelNumber && <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 32}, theme.textScale), { marginLeft: 18, color: theme.colors.textSecondary}]}>{channelNumber}</Text>}
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, marginLeft: 18}}>
                <View style={{width: 10, height: 10, borderRadius: 5, backgroundColor: theme.colors.record}}/>
                <Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('next.kit.live')}</Text>
              </View>
            </View>
            <View style={{flexDirection: 'row', alignItems: 'baseline', minWidth: 0}}>
              {linear.programme && <Text numberOfLines={1} style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 32}, theme.textScale), { marginRight: 12, color: theme.colors.textSecondary}]}>{t('next.kit.range', {start: wallClock(linear.programme.startMs), end: wallClock(linear.programme.endMs)})}</Text>}
              {linear.next && <Text numberOfLines={1} ellipsizeMode="tail" style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 32}, theme.textScale), { color: theme.colors.textTertiary, flexShrink: 1}]}>{t('player.live.next', {title: linear.next.title})}</Text>}
            </View>
          </View></>}

          {!linear && !compact && theme.isTV && identity && <View style={{flexShrink: 1, minWidth: 0}}><Text numberOfLines={1} style={[nativeType({size: 44, weight: 800, tracking: -.03, lineHeight: 52}, theme.textScale), {color: theme.colors.text, textShadowColor: 'rgba(0,0,0,.45)', textShadowOffset: {width: 0, height: 2}, textShadowRadius: 24}]}>{identity.title}</Text>
            <View style={{flexDirection: 'row', alignItems: 'baseline', minWidth: 0, marginTop: 4}}>{identity.subtitle.map(part =>
              <Text key={part.id} numberOfLines={1} ellipsizeMode="tail" style={[nativeType({size: 24, weight: part.id === 'episode-title' ? 700 : 600, tracking: 0, lineHeight: 32}, theme.textScale),
                {color: part.id === 'episode-title' ? theme.colors.text : theme.colors.textSecondary, flexShrink: part.id === 'episode-title' ? 1 : 0, marginRight: 10}]}>{part.text}</Text>)}</View></View>}
          {!theme.isTV && linear && <View style={{flexShrink: 1, minWidth: 0}}>
            <Text numberOfLines={1} style={[nativeType({size: 16.5, weight: 800, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{linear.programme?.title ?? linear.name}</Text>
            <View style={{flexDirection: 'row', alignItems: 'center', minWidth: 0, marginTop: 1}}>
              <Text numberOfLines={1} style={[theme.type.bodyStrong, {fontSize: 13, color: theme.colors.textSecondary, flexShrink: 0}]}>{linear.name}</Text>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginLeft: 10}}>
                <View style={{width: 7, height: 7, borderRadius: 3.5, backgroundColor: theme.colors.record}}/>
                <Text style={{fontFamily: 'Manrope-SemiBold', fontSize: 12, fontWeight: '600', color: theme.colors.text}}>{t('next.kit.live')}</Text>
              </View>
            </View>
          </View>}
          {!theme.isTV && !linear && !compact && identity && <View style={{flexShrink: 1, minWidth: 0, marginLeft: 8}}>
            <Text numberOfLines={1} style={[nativeType({size: 16.5, weight: 800, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{identity.title}</Text>
            <Text numberOfLines={1} style={[theme.type.bodyStrong, {fontSize: 13, marginTop: 1, color: theme.colors.textSecondary}]}>{identity.subtitle.map(part => part.text).join('  ')}</Text>
          </View>}
          {theme.isTV && !compact && <FocusRegion id="watch-group" entry="player-group-pill" orientation="horizontal" enabled={controlsEnabled && groupPillPresent}
            edges={{down: 'watch-seek-region', right: linear && liveOffer && props.liveActions ? 'watch-live-actions' : undefined}} stops={{up: true}}
            style={{flexShrink: 0, alignSelf: 'center', marginLeft: groupPillPresent ? 28 : 0}}>
            <NativeTVGroupPill onHost={groupPillHost} onSheetChange={groupSheetChange}/>
          </FocusRegion>}
          <View style={{flex: 1}}/>
          {theme.isTV && linear && liveOffer && props.liveActions && <FocusRegion id="watch-live-actions" entry="watch-live-more" orientation="horizontal" enabled={controlsEnabled}
            edges={{down: 'watch-seek-region', left: groupPillPresent ? 'watch-group' : undefined}} stops={{up: true}} style={{flexShrink: 0, marginLeft: 14}}>{liveButtons()}</FocusRegion>}
          {theme.isTV && !linear && <View style={{marginLeft: 28, flexShrink: 0, alignItems: 'flex-end'}}>
            <Text style={[nativeType({size: 32, weight: 700, tracking: 0, lineHeight: 40}, theme.textScale), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{wallClock(now)}</Text>
            <Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 26}, theme.textScale), {marginTop: 2, color: theme.colors.textSecondary}]}>{t('next.watch.ends', {time: wallClock(now + Math.max(0, source.playback.duration - source.playback.positionSeconds) * 1000)})}</Text>
          </View>}
          {props.onCast && !theme.isTV && !linear && icon('cast', 'i-cast', t('player.playOn'), () => props.onCast!(fence, castButton))}
          {pipOffered && !theme.isTV && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence))}
          {!theme.isTV && icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'))}
          {!theme.isTV && liveButtons(compact ? 46 : 48)}
        </View>
        {!theme.isTV && <View style={{position: 'absolute', left: 0, right: 0, top: height / 2 - big / 2, alignItems: 'center'}}>{transport}</View>}
        {theme.isTV && <View style={{position: 'absolute', left: 96 + (props.edgeInsets?.left ?? 0), right: 96 + (props.edgeInsets?.right ?? 0), bottom: 80}}>
          <FocusRegion id="watch-seek-region" entry="watch-seek" stops={{left: true, right: true, up: !groupPillPresent && !(linear && liveOffer && props.liveActions)}} edges={{up: groupPillPresent ? 'watch-group' : linear && liveOffer && props.liveActions ? 'watch-live-actions' : undefined, down: liveEdgeAction ? 'watch-live-edge' : 'watch-transport'}}>
            <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={compact} enabled={controlsEnabled} tv focused={seekFocused} onPreview={setLanding} liveEdge={liveEdge} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
          </FocusRegion>
          {scrubbing && <View style={{height: 30, marginTop: 10}}>
            {[...new Map<string, SegmentMarker>(segments.filter(segment => segment.kind === 'intro' || segment.kind === 'recap' || segment.kind === 'credits')
              .map(segment => [segment.kind, segment] as const)).values()].map(segment => {
              const at = source.playback.duration > 0 ? Math.min(1, segment.startSeconds / source.playback.duration) : 0;
              const label = segment.kind === 'intro' ? t('player.marker.intro') : segment.kind === 'recap' ? t('player.marker.recap') : t('player.marker.credits');
              return <Text key={segment.kind} numberOfLines={1} style={[nativeType({size: 20, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {position: 'absolute', top: 2, color: theme.colors.textSecondary},
                at >= .9 ? {right: 0} : {left: `${at * 100}%`}]}>{label}</Text>;
            })}
          </View>}
          {!scrubbing && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, height: 30, marginTop: 10}}>
            <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 30}, theme.textScale), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{clockText(source.playback.positionSeconds)}</Text>
            {linear && behindLive > 30 && <Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {marginLeft: 12, color: theme.colors.textSecondary}]}>{t('player.live.behindLive', {duration: durationText(Math.max(60, Math.round(behindLive / 60) * 60), t)})}</Text>}
            <View style={{flex: 1}}/>
            {linear && behindLive > 5 && props.goLive
              ? <FocusRegion id="watch-live-edge" entry="watch-go-live" edges={{up: 'watch-seek-region', down: 'watch-transport'}} enabled={controlsEnabled}><Focusable focusId="watch-go-live" testID="watch-go-live" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
                  onPress={() => {if (controlsCurrent() && props.goLive) {controls.reveal(fence); props.goLive(fence);}}}
                  style={{height: 48, paddingHorizontal: 20, borderRadius: 12, flexDirection: 'row', alignItems: 'center', gap: 12, backgroundColor: 'rgba(237,245,250,.14)'}}>
                  <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: theme.colors.record}}/>
                  <Text style={[nativeType({size: 21, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('player.live.goLive')}</Text>
                </Focusable></FocusRegion>
              : <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 30}, theme.textScale), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>}
          </View>}
          <View style={{height: 88, marginTop: 20}}>
            {seekFocused && scrubbing && <View pointerEvents="none" style={{position: 'absolute', inset: 0, flexDirection: 'row', alignItems: 'center'}}>
              {hint(0, keycap('', ['i-left', 'i-right']), t('player.hint.move'))}
              {hint(1, keycap(t('player.hint.hold')), t('player.hint.faster'))}
              {hint(2, keycap(t('title.ok')), <LandingTime store={landingStore} fallback={returnAt}/>, 'play-from')}
              {hint(3, keycap(t('action.back')), t('player.hint.returnTo', {time: clockText(returnAt)}))}
            </View>}
            <FocusRegion id="watch-transport" orientation="horizontal" entry={barEntry} stops={{down: true}} edges={{up: barUp ? 'watch-failed' : liveEdgeAction ? 'watch-live-edge' : 'watch-seek-region', right: promptEntry ? 'watch-skip' : undefined}}
              accessibilityElementsHidden={seekFocused && scrubbing} importantForAccessibility={seekFocused && scrubbing ? 'no-hide-descendants' : 'auto'}
              style={{height: 88, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', opacity: seekFocused && scrubbing ? 0 : 1}}>
              <View style={{flex: 1}}>{linear
                ? (props.onGuide ? <Focusable ref={guideButton} focusId="watch-guide" testID="watch-guide" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled} onPress={() => {if (controlsCurrent() && props.onGuide) props.onGuide();}}
                    style={{height: 64, paddingHorizontal: 26, borderRadius: 32, flexDirection: 'row', alignItems: 'center', gap: 12, backgroundColor: 'rgba(237,245,250,.14)'}}>
                    <Icon name="i-list" size={28} color={theme.colors.text}/><Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('player.live.guide')}</Text>
                  </Focusable> : null)
                : fence.kind === 'queue' && fence.showId && option('episodes', t('title.episodes'))}</View>
              {transport}
              <View style={{flex: 1, flexDirection: 'row', justifyContent: 'flex-end', gap: 14}}>
                {source.capabilities.subtitleTrack && option('subtitles', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined))}
                {source.capabilities.audioTrack && audioTracks > 1 && option('audio', t('title.audio'), props.audio)}
                {option('quality', t('player.opt.quality'), props.quality)}
                {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'))}
                {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence))}
              </View>
            </FocusRegion>
          </View>
        </View>}
        {!theme.isTV && <View style={{position: 'absolute', left: (side ?? (compact ? 14 : 60)) + (props.edgeInsets?.left ?? 0), right: (side ?? (compact ? 6 : 60)) + (props.edgeInsets?.right ?? 0), bottom: compact ? 8 : 15}}>
          {!compact && <View style={{marginBottom: 8}}>
            <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={compact} enabled={controlsEnabled} liveEdge={liveEdge} onPreview={setLanding} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
          </View>}
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
            <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{clockText(source.playback.positionSeconds)}</Text>
            {linear && behindLive > 30 && <Text style={[theme.type.bodyStrong, {fontSize: 12, marginLeft: 8, color: theme.colors.textSecondary}]}>{t('player.live.behindLive', {duration: durationText(Math.max(60, Math.round(behindLive / 60) * 60), t)})}</Text>}
            {compact && <Text style={[theme.type.bodyStrong, {fontSize: 12, color: theme.colors.textSecondary}]}>{`/ ${clockText(source.playback.duration)}`}</Text>}
            <View style={{flex: 1}}/>
            {!compact && (linear && behindLive > 5 && props.goLive
              ? <Focusable focusId="watch-go-live" testID="watch-go-live" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
                  onPress={() => {if (controlsCurrent() && props.goLive) {controls.reveal(fence); props.goLive(fence);}}}
                  style={{height: 28, paddingHorizontal: 12, borderRadius: 14, flexDirection: 'row', alignItems: 'center', gap: 8, backgroundColor: 'rgba(237,245,250,.14)'}}>
                  <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.record}}/>
                  <Text style={{fontFamily: 'Manrope-Bold', fontSize: 12.5, fontWeight: '700', color: theme.colors.text}}>{t('player.live.goLive')}</Text>
                </Focusable>
              : <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>)}
            {compact && !linear && <>{qualityChip(true)}{props.fullWindowRequest?.() && controls.canFullWindow() && !controls.getSnapshot().fullWindow && icon('fullscreen', 'i-fs', t('next.states.shortcut.fullscreen'), () => {if (current() && props.fullWindowRequest?.()) controls.setFullWindow(fence, true);})}</>}
          </View>
          {compact && linear && <View testID="watch-live-pills" style={{flexDirection: 'row', alignItems: 'center', marginTop: 6, gap: 8}}>
            {props.onGuide && chip('episodes', 'i-list', t('player.live.guide'), undefined, props.onGuide)}
            <View style={{flex: 1}}/>{qualityChip(true)}
            {props.fullWindowRequest?.() && controls.canFullWindow() && !controls.getSnapshot().fullWindow && icon('fullscreen', 'i-fs', t('next.states.shortcut.fullscreen'), () => {if (current() && props.fullWindowRequest?.()) controls.setFullWindow(fence, true);})}
          </View>}
          {!compact && <View style={{flexDirection: 'row', alignItems: 'center', marginTop: 10, gap: 8}}>
            {linear
              ? (props.onGuide ? chip('episodes', 'i-list', t('player.live.guide'), undefined, () => {if (controlsCurrent() && props.onGuide) props.onGuide();}) : null)
              : fence.kind === 'queue' && fence.showId ? chip('episodes', 'i-stack', t('title.episodes'), undefined) : null}
            <View style={{flex: 1}}/>
            {source.capabilities.subtitleTrack && chip('subtitles', 'i-cc', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined))}
            {source.capabilities.audioTrack && audioTracks > 1 && chip('audio', 'i-audio', t('title.audio'), props.audio)}
            {qualityChip()}
          </View>}
        </View>}
        {!theme.isTV && compact && <View style={{position: 'absolute', left: (props.edgeInsets?.left ?? 0), right: (props.edgeInsets?.right ?? 0), bottom: -21}}>
          <NativeWatchTimeline source={source} controls={controls} playback={playback} compact enabled={controlsEnabled} onPreview={setLanding} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
        </View>}
      </FocusRegion>
    </Animated.View>
    {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={theme.isTV ? 168 : 64} gutter={theme.isTV ? 96 : 54} available={width}/>}
    {stillWatchingNode}
    {skipNodes && <Animated.View pointerEvents="box-none" style={[{position: 'absolute', right: theme.isTV ? 96 : 60,
      bottom: theme.isTV ? 284 : skipNodes === skipPromptNode && !motion.reduce ? 20 : visible ? 110 : 20}, skipNodes === skipPromptNode ? promptMotion.positionStyle : undefined]}>
      {theme.isTV
        ? <FocusRegion id="watch-skip" orientation="horizontal" entry={promptEntry} edges={visible ? {left: 'watch-transport'} : undefined} screenStart={{regionId: 'watch-surface', childId: 'watch-picture'}}>
            <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>{skipNodes}</View>
          </FocusRegion>
        : <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>{skipNodes}</View>}
    </Animated.View>}
    {theme.isTV && visible && actionsCurrent && liveActions.confirmed && liveConfirmation && !liveOverlayOpen && <View testID="watch-live-confirmation" pointerEvents="none"
      accessibilityRole="alert" accessibilityLiveRegion="polite" style={{position: 'absolute', right: 96, top: 168, maxWidth: width - 192, flexDirection: 'row', alignItems: 'center', gap: 14,
        paddingVertical: 16, paddingHorizontal: 24, borderRadius: 16, backgroundColor: 'rgba(16,33,45,.94)',
        boxShadow: '0 24px 60px -18px rgba(0,0,0,.85), inset 0 0 0 1px rgba(237,245,250,.1)'}}>
      <View style={{width: 14, height: 14, borderRadius: 7, backgroundColor: theme.colors.record}}/>
      <Text numberOfLines={1} style={[nativeType({size: 23, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, flexShrink: 1}]}>{liveConfirmation}</Text>
    </View>}
    {theme.isTV && props.liveGuide && liveGuide.timezone && <NativeMiniGuide key={JSON.stringify(fence)} open={miniOpen} onExited={miniExited} rows={liveGuide.rows.map(row => ({channel: row.channel, ...nowNext(row.programmes, now), watching: row.step === 0}))}
      now={now} timezone={liveGuide.timezone} loading={liveGuide.phase === 'loading'} busy={liveGuide.busy} artwork={props.artwork}
      onSelect={channel => {const row = liveGuide.rows.find(value => value.channel === channel);if (current() && row) void props.liveGuide?.tune(row, liveGuide);}}
      onClose={() => {if (current()) props.liveGuide?.close(liveGuide);}} onFullGuide={() => {if (current()) props.liveGuide?.fullGuide(liveGuide);}}
      error={liveGuide.error ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{playbackUserMessage(liveGuide.error)}</Text> : undefined}/>}
  </Animated.View></GestureDetector>;
}

/** The only node a held scrub re-renders in the chrome: "Play from 12:34". */
function LandingTime({store, fallback}: {store: LandingStore; fallback: number}) {
  const landing = useSyncExternalStore(store.subscribe, store.get, store.get);
  return <>{defaultI18n.t('player.hint.playFrom', {time: clockText(landing ?? fallback)})}</>;
}
