import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {ActivityIndicator, Image, Text, View, type LayoutChangeEvent} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import Animated, {cancelAnimation, runOnJS, useAnimatedReaction, useAnimatedStyle, useSharedValue, withSpring, withTiming} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {Artwork} from '../artwork';
import {Button, Focusable} from '../controls';
import {Popover} from '../overlays';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {ListeningCoverLocalPaint, useListeningCoverSlot} from './ListeningCoverBridge';
import {BlurView} from '../../platform/native-blur';
import {selectionHaptic} from '../../platform/haptics';
import {DockHandover, DockPaintRow} from './DockHandover';
import type {DockVideoExitPaint} from './video-exit-paint';
import type {PlaybackFence} from '../../../../../packages/player-core/src';
import {sameDockFence, commandAllowed, dockCommand, expandedDockMetrics, mediumDockMetrics, playbackPosition, progressOf, releaseAside, remoteSubtitle, seekBounds, seekTo, validRect} from './model';
import {alpha, DockFacts, dockStyles as s, dockType} from './visual';
import type {DockAction, DockRect, DockRenderProps, DockSlotAdmission, PlayerMorphController} from './types';

const dockPaintTypes = [DockPaintRow, View, Text, Image, React.Fragment, Artwork, Button, Focusable, Icon, DockFacts, ActivityIndicator, ListeningCoverLocalPaint];

export type MediaBarProps = DockRenderProps & {
  onVideoSlot?: (rect: DockRect, sourceCurrent?: () => boolean) => void;
  /** Host closes this while its ancestor moves; standalone bars have no presence transform. */
  slotAdmission?: DockSlotAdmission | null;
  onMenuOpenChange?: (open: boolean) => void;
  /** Live core ownership, including a replacement before React rerenders. */
  isCurrent?: () => boolean;
  morph?: PlayerMorphController;
  menuOpen?: boolean;
  takeVideoExit?: (fence: PlaybackFence) => DockVideoExitPaint | undefined;
};

/** This chrome has no video element, segment input, prompt slot, queue preview, or volume. */
export function MediaBar({occupant, onAction, visible = true, onVideoSlot, slotAdmission, onMenuOpenChange, isCurrent, morph, menuOpen, takeVideoExit, style, testID}: MediaBarProps) {
  const theme = useTheme(), motion = useMotion(), t = defaultI18n.t;
  const bar = useRef<View>(null), art = useRef<View>(null), openerRef = useRef<View>(null), firstMenu = useRef<View>(null);
  const castButton = useRef<View>(null);
  const [internalMenu, setMenu] = useState(false), [expandedWidth, setExpandedWidth] = useState(0);
  const menu = menuOpen ?? internalMenu;
  const width = useSharedValue(374), localX = useSharedValue(0), xOrigin = useSharedValue(0), held = useSharedValue(1), localFade = useSharedValue(1);
  const scrubWidth = useSharedValue(0), scrubAt = useSharedValue(-1);
  const {presentation: p, remote, playback} = occupant;
  const menuSource = useRef(playback.fence), menuRemote = useRef(remote?.id);
  const video = occupant.mode === 'video', wide = video || !!remote;
  const audioCover = useListeningCoverSlot(!remote && playback.fence.kind !== 'cast-view' && (occupant.mode === 'music' || occupant.mode === 'audiobook') ? playback.fence : undefined, 'mini', art, visible && slotAdmission !== null && slotAdmission?.isCurrent() !== false);
  // tablet-play 4a/13: an expanded window draws the dock at the content's foot.
  const expandedBar = !theme.isTV && theme.platform === 'web';
  const sharedPicture = video && morph?.dockMotionHosted === true, x = sharedPicture ? morph.dockX : localX, fade = sharedPicture ? morph.dockFade : localFade;
  const terminal = !!remote && ['stopped', 'replaced', 'error'].includes(remote.phase);
  const connecting = !!remote && ['connecting', 'pairing', 'waiting'].includes(remote.phase);
  const progress = progressOf(occupant);
  const act = useCallback((type: DockAction, anchorRef?: React.RefObject<View | null>) => {
    if (commandAllowed(occupant, type)) onAction({...dockCommand(occupant, type), ...(type === 'cast' ? {anchorRef: anchorRef ?? castButton} : {})});
  }, [occupant, onAction]);
  const seekBy = useCallback((seconds: number) => {
    const command = seekTo(occupant, playbackPosition(occupant) + seconds);
    if (command) onAction(command);
  }, [occupant, onAction]);
  const seekBack = useCallback(() => seekBy(-p.skipBackSeconds), [seekBy, p.skipBackSeconds]);
  const seekForward = useCallback(() => seekBy(p.skipForwardSeconds ?? 30), [seekBy, p.skipForwardSeconds]);
  const bounds = seekBounds(occupant), seekAllowed = commandAllowed(occupant, 'seek');
  const scrubStart = bounds?.start, scrubEnd = bounds?.end;
  const changeMenu = useCallback((open: boolean) => {setMenu(open); onMenuOpenChange?.(open);}, [onMenuOpenChange]);
  useEffect(() => () => onMenuOpenChange?.(false), [onMenuOpenChange]);
  useEffect(() => {
    if (!sameDockFence(menuSource.current, playback.fence) || menuRemote.current !== remote?.id) {changeMenu(false); x.value = 0; fade.value = 1;}
    menuSource.current = playback.fence; menuRemote.current = remote?.id;
  }, [playback.fence, remote?.id, changeMenu, x, fade]);
  const dismiss = useCallback(() => act(terminal ? 'dismiss-remote' : remote ? 'stop' : 'close'), [act, terminal, remote]);
  const spring = useMemo(() => motion.spring('settle'), [motion]);
  const latest = useRef({occupant, onAction, visible, isCurrent});
  latest.current = {occupant, onAction, visible, isCurrent};
  const gestureTarget = useRef({occupant, visible, epoch: 1});
  const old = gestureTarget.current;
  if (!sameDockFence(old.occupant.playback.fence, playback.fence) || old.occupant.remote?.id !== remote?.id
    || old.occupant.remote?.sequence !== remote?.sequence || old.visible !== visible || old.occupant.mode !== occupant.mode) {
    gestureTarget.current = {occupant, visible, epoch: old.epoch + 1};
  }
  const gestureSource = gestureTarget.current;
  const lifetime = useSharedValue(gestureSource.epoch), run = useSharedValue(0), active = useSharedValue(false);
  const mounted = useRef(false), consumed = useRef(0);
  const hold = useRef<{source: typeof gestureSource; used: boolean} | null>(null);
  useEffect(() => {
    lifetime.value = gestureSource.epoch; run.value++; active.value = false; hold.current = null;
    cancelAnimation(x); cancelAnimation(fade); cancelAnimation(held);
    x.value = 0; fade.value = 1; held.value = 1;
    return () => {
      lifetime.value = -1; run.value++; active.value = false; hold.current = null;
      cancelAnimation(x); cancelAnimation(fade); cancelAnimation(held);
    };
  }, [gestureSource, lifetime, run, active, x, fade, held]);
  useEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, []);
  const currentInteraction = useCallback(() => mounted.current && gestureTarget.current === gestureSource
    && latest.current.visible && latest.current.isCurrent?.() !== false, [gestureSource]);
  const dismissFromGesture = useCallback((version: number) => {
    if (!currentInteraction() || version !== run.value || consumed.current === version) return;
    consumed.current = version;
    const original = gestureSource.occupant;
    const terminalRemote = original.remote && ['stopped', 'replaced', 'error'].includes(original.remote.phase);
    const type = terminalRemote ? 'dismiss-remote' : original.remote ? 'stop' : 'close';
    changeMenu(false);
    if (commandAllowed(original, type)) latest.current.onAction(dockCommand(original, type));
  }, [changeMenu, gestureSource, currentInteraction, run]);
  // The expanded bar's 4 pt scrubber speaks the bar's own interaction language: it
  // starts only in the current interaction, tracks only while the source is
  // unchanged, commits once on a successful end, and cancels on failure, a new
  // interaction or unmount. Taps and the accessibility actions take the same path.
  // Plain, worklet-safe timing configs: the Reanimated worklets capture these
  // values instead of calling the JS motion helper at gesture time.
  const exitTiming = useMemo(() => motion.timing('standard', 'out'), [motion]);
  const outwardTiming = useMemo(() => motion.reduce ? exitTiming : {...exitTiming, duration: 200}, [motion, exitTiming]);
  const transforms = motion.transforms;
  const scrubEpoch = useSharedValue(-1), scrubVersion = useSharedValue(0), scrubActive = useSharedValue(0);
  const seekFraction = useCallback((fraction: number) => {
    if (!currentInteraction() || scrubStart === undefined || scrubEnd === undefined) return;
    const command = seekTo(occupant, scrubStart + Math.max(0, Math.min(1, fraction)) * (scrubEnd - scrubStart));
    if (command) onAction(command);
  }, [currentInteraction, occupant, onAction, scrubStart, scrubEnd]);
  const commitScrub = useCallback((fraction: number, epoch: number, version: number) => {
    if (lifetime.value !== epoch || run.value !== version) return;
    seekFraction(fraction);
  }, [lifetime, run, seekFraction]);
  const scrubGesture = useMemo(() => Gesture.Pan().enabled(seekAllowed && visible).minDistance(0).activeOffsetX([-2, 2]).failOffsetY([-14, 14])
    .onBegin(event => {
      scrubActive.value = 1;
      scrubEpoch.value = lifetime.value;
      scrubVersion.value = run.value;
      scrubAt.value = Math.max(0, Math.min(1, event.x / Math.max(1, scrubWidth.value)));
    })
    .onUpdate(event => {
      if (!scrubActive.value || scrubEpoch.value !== lifetime.value) {scrubAt.value = -1; return;}
      scrubAt.value = Math.max(0, Math.min(1, event.x / Math.max(1, scrubWidth.value)));
    })
    .onEnd((_event, success) => {
      const fraction = scrubAt.value, epoch = scrubEpoch.value, version = scrubVersion.value;
      scrubActive.value = 0; scrubAt.value = -1;
      if (!success || fraction < 0 || epoch !== lifetime.value) return;
      runOnJS(commitScrub)(fraction, epoch, version);
    })
    .onFinalize(() => {scrubActive.value = 0; scrubAt.value = -1;}),
    [seekAllowed, visible, scrubAt, scrubWidth, scrubActive, scrubEpoch, scrubVersion, lifetime, run, commitScrub]);
  // The fill is a fixed full-width layer scaled from its left edge, so a scrub
  // frame never re-lays-out the bar; the fraction and every seek callback are
  // unchanged.
  const scrubFill = useAnimatedStyle(() => {
    const fraction = Math.max(0, Math.min(1, scrubAt.value < 0 ? progress ?? 0 : scrubAt.value));
    const width = Math.max(1, scrubWidth.value);
    return {transform: [{translateX: -(width * (1 - fraction)) / 2}, {scaleX: fraction}]};
  }, [progress]);
  const aside = useMemo(() => Gesture.Pan().enabled(visible && !expandedBar).activeOffsetX([-10, 10]).failOffsetY([-12, 12])
    .onBegin(() => {
      if (!gestureSource.visible || lifetime.value !== gestureSource.epoch) return;
      run.value++; active.value = true;
      cancelAnimation(x); cancelAnimation(fade); fade.value = 1; xOrigin.value = x.value;
    })
    .onUpdate(event => {if (active.value && lifetime.value === gestureSource.epoch) x.value = xOrigin.value + event.translationX;})
    .onEnd((event, success) => {
      if (!success || !active.value || lifetime.value !== gestureSource.epoch) return;
      active.value = false;
      const direction = releaseAside(x.value, event.velocityX, width.value), version = run.value;
      if (direction) {
        const done = (finished?: boolean) => {if (finished && run.value === version && lifetime.value === gestureSource.epoch) runOnJS(dismissFromGesture)(version);};
        // The outward exit decelerates on the authored out curve; opacity is
        // derived from the distance actually travelled in the animated style, so
        // a drag and its release stay in step. Reduced motion keeps the bar in
        // place and fades it linearly; the return keeps the snap spring, and the
        // same successful completion gate decides the dismissal.
        if (spring === undefined) {
          x.value = 0;
          fade.value = withTiming(0, exitTiming, done);
        } else {
          x.value = withTiming(direction * width.value * 1.25, outwardTiming, done);
        }
      } else if (spring) x.value = withSpring(0, {...spring, velocity: event.velocityX});
      else {x.value = 0; fade.value = withTiming(1, exitTiming);}
    }).onFinalize((_event, success) => {
      if (!success && lifetime.value === gestureSource.epoch) {
        run.value++; active.value = false; cancelAnimation(x); cancelAnimation(fade);
        x.value = spring ? withSpring(0, spring) : 0; fade.value = 1;
      }
    }), [x, xOrigin, width, spring, fade, dismissFromGesture, visible, expandedBar, gestureSource, lifetime, run, active, outwardTiming, exitTiming]);
  const gestures = useMemo(() => morph ? Gesture.Race(aside, morph.dockGesture) : aside, [aside, morph]);
  // Local video uses the single ancestor shared with its native picture. Applying
  // x/fade here too would double the chrome's movement and opacity.
  const sharedTravelFade = morph?.dockTravelFade;
  useAnimatedReaction(() => sharedPicture && transforms
    ? Math.max(0, Math.min(1, 1 - Math.abs(x.value) / Math.max(1, width.value))) : 1,
  value => {if (sharedTravelFade) sharedTravelFade.value = value;}, [sharedPicture, transforms, sharedTravelFade]);
  const animated = useAnimatedStyle(() => {
    // Opacity follows the bar's actual travel while it can transform; reduced
    // motion has no displacement and uses its own linear fade.
    const travel = transforms && !sharedPicture ? Math.max(0, Math.min(1, 1 - Math.abs(x.value) / Math.max(1, width.value))) : 1;
    return {opacity: sharedPicture ? 1 : Math.min(travel, fade.value), transform: [{translateX: sharedPicture ? 0 : x.value}, {scale: held.value}]};
  }, [transforms, sharedPicture]);
  const beginHold = useCallback(() => {
    if (!currentInteraction()) return;
    hold.current = {source: gestureSource, used: false};
    held.value = motion.transforms ? withTiming(.98, motion.timing('quick')) : 1;
  }, [currentInteraction, gestureSource, held, motion]);
  const endHold = useCallback(() => {
    if (hold.current?.source !== gestureSource) return;
    hold.current = null;
    held.value = motion.transforms ? withSpring(1, motion.spring('snap')) : 1;
  }, [gestureSource, held, motion]);
  const heldMenu = useCallback(() => {
    const pending = hold.current;
    if (!currentInteraction() || remote || !pending || pending.source !== gestureSource || pending.used) return;
    pending.used = true;
    held.value = motion.transforms ? withSpring(1.02, motion.spring('snap')) : 1;
    void selectionHaptic();
    changeMenu(true);
  }, [currentInteraction, remote, gestureSource, held, motion, changeMenu]);
  const slotSource = useRef(playback.fence);
  if (!sameDockFence(slotSource.current, playback.fence)) slotSource.current = playback.fence;
  const measuredFence = slotSource.current;
  const measurement = useRef(0), measuring = useRef(false), artLayout = useRef(0);
  const slotNow = useRef({video, visible, onVideoSlot, slotAdmission, fence: playback.fence});
  slotNow.current = {video, visible, onVideoSlot, slotAdmission, fence: playback.fence};
  useEffect(() => {
    const request = measurement, live = measuring;
    live.current = true;
    return () => {live.current = false; request.current++;};
  }, []);
  const measureArt = useCallback(() => {
    const version = ++measurement.current, node = art.current, layout = artLayout.current;
    if (!measuring.current || active.value || x.value !== 0 || fade.value !== 1 || held.value !== 1 || !video || !visible || !onVideoSlot || !node || slotAdmission === null || slotAdmission?.isCurrent() === false) return;
    // Callback identity still guards delivery below. Once admitted, ordinary
    // position/playing rerenders do not retire the original native slot proof.
    const sourceCurrent = () => {
      const now = slotNow.current;
      return measuring.current && artLayout.current === layout && node === art.current && !active.value && x.value === 0 && fade.value === 1 && held.value === 1
        && now.video && now.visible && now.slotAdmission === slotAdmission
        && sameDockFence(now.fence, measuredFence) && slotAdmission?.isCurrent() !== false;
    };
    node.measureInWindow((ax, ay, w, h) => {
      const now = slotNow.current, rect = {x: ax, y: ay, width: w, height: h};
      if (measuring.current && !active.value && x.value === 0 && fade.value === 1 && held.value === 1 && version === measurement.current && node === art.current && now.video && now.visible &&
        now.onVideoSlot === onVideoSlot && now.slotAdmission === slotAdmission && sameDockFence(now.fence, measuredFence) &&
        slotAdmission?.isCurrent() !== false && sourceCurrent() && validRect(rect)) onVideoSlot(rect, sourceCurrent);
    });
  }, [video, visible, onVideoSlot, slotAdmission, measuredFence, active, x, fade, held]);
  // Ancestor transforms do not produce a child onLayout at animation completion.
  useEffect(() => {
    const request = measurement;
    measureArt();
    return () => {request.current++;};
  }, [measureArt]);
  const measureAudioCover = audioCover.measure;
  const onLayout = useCallback((event: LayoutChangeEvent) => {
    const measured = event.nativeEvent.layout.width;
    artLayout.current++;
    width.value = measured;
    setExpandedWidth(current => current === Math.round(measured) ? current : Math.round(measured));
    measureArt(); measureAudioCover();
  }, [width, measureArt, measureAudioCover]);
  const label = remote ? t('next.dock.native.openRemote', {title: p.identity.title, device: p.device ?? t('cast.theTV')}) : t('next.dock.native.openPlayerLabel', {title: p.identity.title});
  const button = (type: DockAction, icon: IconName, name: string, size = 22, close = false, skip?: 'back' | 'forward', primary = false) => {
    const enabled = commandAllowed(occupant, type);
    const seconds = skip === 'forward' ? p.skipForwardSeconds ?? 30 : p.skipBackSeconds;
    return <Focusable key={`${type}:${skip ?? ''}:${primary ? 'p' : ''}`} ref={type === 'cast' ? castButton : undefined} accessibilityRole="button" accessibilityLabel={name} disabled={!enabled} focusRadius={expandedBar ? 22 : 20}
      hitSlop={{top: 2, bottom: 2, left: 0, right: 0}}
      style={[expandedBar ? primary ? s.expandedPrimary : s.expandedButton : s.phoneButton, !expandedBar && theme.platform === 'tablet' && s.mediumButton, !expandedBar && close && s.close, {opacity: enabled ? 1 : .32}]}
      onPress={() => skip === 'back' ? seekBack() : skip === 'forward' ? seekForward() : act(type)}>
      <Icon name={icon} size={size} color={close ? theme.colors.textSecondary : theme.colors.text}/>
      {skip && <Text accessible={false} style={[dockType(theme, 8.5, 700), {position: 'absolute', color: theme.colors.text, top: expandedBar ? 19 : 15}]}>{seconds}</Text>}
    </Focusable>;
  };
  const playAction = p.finished ? 'replay' : playback.playing ? 'pause' : 'play';
  const playIcon = p.finished ? 'i-restart' : playback.playing ? 'i-pause' : 'i-play';
  const playLabel = p.finished ? t('player.post.replay') : t(playback.playing ? 'action.pause' : 'action.play');
  const subtitle = p.finished && !remote ? [{id: 'finished', text: t('player.post.finished'), role: 'status' as const}] : remoteSubtitle(occupant);
  const artStyle = expandedBar ? s.expandedCard : wide ? s.videoArt : s.art;
  const artWidth = expandedBar ? expandedDockMetrics.cardWidth : wide ? 80 : 44, artHeight = expandedBar ? expandedDockMetrics.cardHeight : wide ? 45 : 44;
  // The frame's 232 body and 400 scrubber are kept where they fit, and clamped to
  // the bar the shell actually gave us: the centred mid must clear the body on
  // the left and the tools on the right at any expanded width.
  const dock = expandedDockMetrics;
  const scrubSize = Math.round(Math.max(dock.minScrub, Math.min(dock.scrubWidth, expandedWidth - 2 * (dock.sideLeft + dock.edgeGap + dock.bodyWidth))));
  const bodySize = Math.round(Math.max(dock.minBody, Math.min(dock.bodyWidth, (expandedWidth - scrubSize) / 2 - dock.sideLeft - dock.edgeGap)));
  const titleSize = expandedBar ? 15 : 14.5, subtitleSize = expandedBar ? 13 : 12.5;
  const takeOriginalVideo = useCallback((original: PlaybackFence) => {
    const paint = takeVideoExit?.(original);
    if (paint && (active.value || localX.value !== 0 || localFade.value !== 1 || held.value !== 1 || morph?.dragging.value
      || morph && (morph.dockX.value !== 0 || morph.dockFade.value !== 1 || (morph.dockTravelFade?.value ?? 1) !== 1))) {
      void paint.release().catch(() => {}); return;
    }
    return paint;
  }, [takeVideoExit, active, localX, localFade, held, morph]);
  const menuRows = [
    {id: 'open', icon: 'i-up' as const, label: t('next.dock.native.openPlayer'), action: 'expand' as const},
    ...(p.capabilities.cast ? [{id: 'cast', icon: 'i-cast' as const, label: t('next.dock.native.playOnMenu'), action: 'cast' as const}] : []),
    {id: 'stop', icon: 'i-stop' as const, label: t('action.stop'), action: 'close' as const},
  ];
  const opener = <Focusable ref={openerRef} focusTreatment="none" style={expandedBar ? [s.expandedBody, {width: bodySize}] : [s.body, {gap: theme.platform === 'tablet' ? mediumDockMetrics.gap : wide ? 11 : 12}]} accessible={!connecting && !terminal} accessibilityRole={connecting || terminal ? undefined : 'button'} accessibilityLabel={connecting || terminal ? undefined : label}
    onPress={() => act(remote ? 'open-remote' : 'expand')} onLongPressAction={!remote ? heldMenu : undefined} delayLongPress={500}
    onPressIn={beginHold}
    onPressOut={endHold}
    accessibilityActions={[{name: 'activate', label: remote ? label : t('next.dock.native.openPlayer')}, {name: 'stop', label: t('action.stop')}]}
    onAccessibilityAction={event => {if (event.nativeEvent.actionName === 'stop') dismiss(); else if (event.nativeEvent.actionName === 'activate') act(remote ? 'open-remote' : 'expand');}}/>;
  const artPaint = <View ref={art} collapsable={false} onLayout={() => {artLayout.current++; measureArt(); audioCover.measure();}} pointerEvents="none" style={[artStyle, {backgroundColor: theme.colors.shadow}]}>
      <ListeningCoverLocalPaint fence={!video && !remote && playback.fence.kind !== 'cast-view' ? playback.fence : undefined} style={{width: artWidth, height: artHeight}}>
      {!video && <Artwork source={p.artwork} width={artWidth} height={artHeight} visible={visible} priority imageStyle={{opacity: connecting || terminal ? .55 : 1}}/>}
      {video && <View style={[s.expand, {backgroundColor: alpha(theme.colors.dim, .62)}]}><Icon name="i-full" size={11}/></View>}
      {remote && <View style={[s.fill, s.center, {backgroundColor: alpha(theme.colors.dim, .55)}]}>{connecting ? <ActivityIndicator size={20} color={theme.colors.text}/> : <Icon name="i-tvdev" size={18}/>}</View>}
      </ListeningCoverLocalPaint>
    </View>;
  const wordsPaint = <><Text numberOfLines={1} ellipsizeMode="tail" style={[dockType(theme, titleSize, 700), {color: theme.colors.text}]}>{p.identity.title}</Text>
      <DockFacts parts={subtitle} style={{...dockType(theme, subtitleSize, 400), color: theme.colors.textSecondary}} gap={12}/>
    </>;
  const canSkip = video || !!remote;
  const previous = button('previous', 'i-prev', p.previousLabel ?? t('player.previous'));
  const next = button('next', 'i-next', p.nextLabel ?? t('player.next'));
  const play = button(playAction, playIcon, playLabel, 24, false, undefined, expandedBar);
  const back = canSkip ? button('seek', 'i-rew', t('player.skipBackSeconds', {seconds: p.skipBackSeconds}), 27, false, 'back') : previous;
  const forward = canSkip ? button('seek', 'i-ffw', t('player.skipForwardSeconds', {seconds: p.skipForwardSeconds ?? 30}), 27, false, 'forward') : null;
  const transport = connecting ? button('stop', 'i-stop', t('next.dock.native.stopConnecting'), 20) : expandedBar
    ? video ? <>{previous}{back}{play}{forward}{next}</> : remote ? <>{back}{play}{forward}</> : <>{previous}{play}{next}</>
    : <>{back}{play}{remote ? button('stop', 'i-stop', t('next.dock.native.stopRemote', {device: p.device ?? t('cast.theTV')}), 20) : next}{!remote && button('close', 'i-x', t('next.dock.native.stopClose'), 18, true)}</>;
  const mid = <View style={expandedBar ? s.expandedMid : s.transports} pointerEvents="box-none">
    <View style={{flexDirection: 'row', alignItems: 'center'}}>{transport}</View>
    {expandedBar && !connecting && progress !== undefined && bounds && <View style={[s.expandedScrub, {width: scrubSize}]}>
      <Text style={[dockType(theme, 12, 600), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(playbackPosition(occupant))}</Text>
      <GestureDetector gesture={scrubGesture}>
        <Focusable accessibilityRole="adjustable" accessibilityLabel={t('player.position')} disabled={!seekAllowed}
          accessibilityValue={{min: Math.round(bounds.start), max: Math.round(bounds.end), now: Math.round(playbackPosition(occupant))}}
          accessibilityActions={[{name: 'increment'}, {name: 'decrement'}]}
          onAccessibilityAction={event => {
            if (scrubStart === undefined || scrubEnd === undefined) return;
            const step = event.nativeEvent.actionName === 'increment' ? p.skipForwardSeconds ?? 30 : -p.skipBackSeconds;
            seekFraction((playbackPosition(occupant) + step - scrubStart) / (scrubEnd - scrubStart));
          }}
          onPress={event => {
            const total = scrubWidth.value, at = event.nativeEvent.locationX;
            if (!seekAllowed || total <= 0) return;
            seekFraction(at / total);
          }}
          onLayout={event => {scrubWidth.value = event.nativeEvent.layout.width;}}
          style={{flex: 1, height: expandedDockMetrics.scrubZone, justifyContent: 'center', opacity: seekAllowed ? 1 : .4}}>
          <View pointerEvents="none" style={{height: expandedDockMetrics.scrubHeight, borderRadius: 2, backgroundColor: alpha(theme.colors.text, .18)}}>
            <Animated.View style={[{width: '100%', height: expandedDockMetrics.scrubHeight, borderRadius: 2, backgroundColor: theme.colors.text}, scrubFill]}/>
          </View>
        </Focusable>
      </GestureDetector>
      <Text style={[dockType(theme, 12, 600), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, bounds.end - playbackPosition(occupant)))}</Text>
    </View>}
  </View>;
  const terminalTools = <>
    {remote?.phase === 'stopped' && p.positionKnown !== false && <Button variant="primary" size="small" accessibilityLabel={t('web.cast.continueHere', {position: clockText(playbackPosition(occupant)) ?? ''})}
      onPress={() => act('continue-here')} style={{height: 34, paddingHorizontal: 11, borderRadius: 9}} textStyle={dockType(theme, 13, 700)}>{t('next.dock.native.continueHere')}</Button>}
    {button('dismiss-remote', 'i-x', t('action.dismiss'), 18, true)}
  </>;
  const tools = terminal ? terminalTools : expandedBar ? <View style={{flexDirection: 'row', alignItems: 'center'}}>
    {remote ? <>
      {button('stop', 'i-stop', t('next.dock.native.stopRemote', {device: p.device ?? t('cast.theTV')}), 20)}
      {button('open-remote', 'i-right', label, 20)}
    </> : <>
      {p.capabilities.cast && button('cast', 'i-cast', t('next.dock.native.playOnMenu'), 20)}
      {button('expand', 'i-full', t('next.dock.native.openPlayer'), 20)}
      {button('close', 'i-x', t('next.dock.native.stopClose'), 18, true)}
    </>}
  </View> : undefined;
  return <>
    <GestureDetector gesture={gestures}><Animated.View ref={bar} testID={testID} onLayout={onLayout} style={[{shadowColor: theme.colors.shadow, shadowOffset: {width: 0, height: menu ? 24 : 14}, shadowOpacity: .85, shadowRadius: 15}, animated, style]}>
      <View style={[expandedBar ? s.expanded : s.phone, !expandedBar && wide && s.video, !expandedBar && theme.platform === 'tablet' && s.medium, {paddingLeft: 0, paddingRight: 0, backgroundColor: alpha(theme.colors.raisedBright, .88), borderWidth: 1, borderColor: alpha(theme.colors.text, menu ? .12 : .07)}]}>
        <BlurView pointerEvents="none" style={s.fill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.raisedBright}/>
        <DockHandover occupant={occupant} allowedTypes={dockPaintTypes} takeVideoExit={takeOriginalVideo} row={<DockPaintRow opener={opener} art={artPaint} words={wordsPaint}
          controls={terminal ? undefined : mid} tools={tools} expanded={expandedBar} listeningFence={!video && !remote && playback.fence.kind !== 'cast-view' ? playback.fence : undefined} artWidth={artWidth}
          openerGap={expandedBar ? 12 : theme.platform === 'tablet' ? mediumDockMetrics.gap : wide ? 11 : 12}
          rowGap={expandedBar ? 0 : theme.platform === 'tablet' ? mediumDockMetrics.gap : 12} textStyle={s.text}
          containerStyle={[expandedBar ? s.expanded : s.phone, !expandedBar && wide && s.video, !expandedBar && theme.platform === 'tablet' && s.medium, {height: undefined, borderRadius: 0, overflow: 'visible'}]}
          line={!expandedBar && !connecting && !terminal && progress !== undefined ? <View pointerEvents="none" style={[s.line, {backgroundColor: alpha(theme.colors.text, .14)}]}><View style={{height: 2, width: '100%', transformOrigin: 'left center', transform: [{scaleX: progress}], backgroundColor: wide ? theme.colors.accentStrong : theme.colors.text}}/></View> : undefined}/>} />
      </View>
    </Animated.View></GestureDetector>
    <Popover open={menu && !remote} onOpenChange={changeMenu} anchorRef={bar} returnFocusRef={openerRef} initialFocusRef={firstMenu} title={p.identity.title} modal backdropOpacity={0} placement="top-start" width={250} offset={8}
      style={{backgroundColor: theme.colors.raisedBright, borderRadius: 14}} contentStyle={{paddingVertical: 6}}>
      {menuRows.map((row, index) => <Focusable key={row.id} ref={index === 0 ? firstMenu : undefined} accessibilityRole="menuitem" accessibilityLabel={row.label}
        disabled={!commandAllowed(occupant, row.action)}
        onPress={() => {changeMenu(false); act(row.action, bar);}} style={{height: 42, flexDirection: 'row', alignItems: 'center', gap: 14, paddingHorizontal: 14, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.text, .06)}}>
        <Icon name={row.icon} size={20} color={row.id === 'stop' ? theme.colors.record : theme.colors.textSecondary}/><Text style={[dockType(theme, 14.5, 600), {color: theme.colors.text}]}>{row.label}</Text>
      </Focusable>)}
    </Popover>
  </>;
}

export const DockBar = MediaBar;
