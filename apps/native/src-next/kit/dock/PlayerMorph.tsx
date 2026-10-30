import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {Platform, View} from 'react-native';
import {Gesture} from 'react-native-gesture-handler';
import Animated, {cancelAnimation, runOnJS, runOnUI, useAnimatedReaction, useAnimatedStyle, useSharedValue, withSpring, withTiming, type SharedValue} from 'react-native-reanimated';
import {type PlaybackFence} from '../../../../../packages/player-core/src/index';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {playerPostureCrossFadeMs} from '../../platform/layout/player-presentation';
import {currentMorphFence} from './morph-fence';
import {releaseDock, validRect} from './model';
import {alpha, dockStyles} from './visual';
import type {DockRect, PlayerMorphController} from './types';

export type PlayerMorphOptions = {
  docked: boolean;
  travel: number;
  fence?: PlaybackFence;
  /** Caller supplies the single animated ancestor around picture and chrome. */
  dockMotionHosted?: boolean;
  onRelease: (docked: boolean, fence: PlaybackFence, fromDocked: boolean) => void;
};

/** One continuous value is retargeted, including during a reversal. It is never reset to an endpoint. */
export function usePlayerMorph({docked, travel, fence, dockMotionHosted = false, onRelease}: PlayerMorphOptions): PlayerMorphController {
  const motion = useMotion();
  const progress = useSharedValue(docked ? 1 : 0), velocity = useSharedValue(0), dragging = useSharedValue(false), releaseVelocity = useSharedValue(0);
  const presentationGoal = useSharedValue(docked ? 1 : 0);
  const dockX = useSharedValue(0), dockFade = useSharedValue(1), dockTravelFade = useSharedValue(1);
  const origin = useSharedValue(0), span = useSharedValue(travel), lastTime = useSharedValue(0);
  const intent = useRef({fence, docked, epoch: 1});
  if (intent.current.docked !== docked || (intent.current.fence !== fence && !currentMorphFence(intent.current.fence, fence))) {
    intent.current = {fence, docked, epoch: intent.current.epoch + 1};
  }
  const source = intent.current;
  const epoch = useSharedValue(source.epoch), run = useSharedValue(0), started = useSharedValue(0);
  const animationRun = useSharedValue(0);
  const completed = useSharedValue<{epoch: number; run: number; animation: number; goal: number} | null>(null);
  const callback = useRef(onRelease), mounted = useRef(false), consumed = useRef(0);
  callback.current = onRelease;
  const released = useCallback((target: boolean, original: PlaybackFence, version: number, gestureRun: number) => {
    const current = intent.current;
    if (!mounted.current || current.epoch !== version || epoch.value !== version || run.value !== gestureRun
      || consumed.current === gestureRun || !currentMorphFence(current.fence, original)) return;
    consumed.current = gestureRun;
    callback.current(target, original, current.docked);
  }, [epoch, run]);
  useEffect(() => {span.value = Math.max(1, travel);}, [travel, span]);
  const spring = useMemo(() => motion.spring('player'), [motion]);
  const timing = useMemo(() => motion.timing(), [motion]);
  const completionFor = useCallback((goal: number) => {
    'worklet';
    const completion = {epoch: epoch.value, run: run.value, animation: ++animationRun.value, goal};
    completed.value = null;
    return (finished?: boolean) => {
      'worklet';
      if (finished && epoch.value === completion.epoch && run.value === completion.run
        && animationRun.value === completion.animation && !dragging.value) completed.value = completion;
    };
  }, [epoch, run, animationRun, completed, dragging]);
  const request = useCallback((target: boolean, speed?: number) => {
    // Capture the ticket on UI after preceding source/goal retirement writes.
    runOnUI((goal: number, requestedSpeed?: number) => {
      'worklet';
      const inherited = requestedSpeed ?? velocity.value;
      presentationGoal.value = goal;
      releaseVelocity.value = inherited;
      const finish = completionFor(goal);
      // Reanimated inherits the running spring's instantaneous velocity on retarget.
      progress.value = spring ? withSpring(goal, {...spring, velocity: inherited}, finish) : withTiming(goal, timing, finish);
    })(target ? 1 : 0, speed);
  }, [progress, velocity, releaseVelocity, spring, timing, completionFor, presentationGoal]);
  useEffect(() => {
    epoch.value = source.epoch; run.value++; dragging.value = false;
    cancelAnimation(progress); cancelAnimation(dockX); cancelAnimation(dockFade);
    dockX.value = 0; dockFade.value = 1; dockTravelFade.value = 1;
    request(source.docked);
  }, [source, request, epoch, run, dragging, progress, dockX, dockFade, dockTravelFade]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false; epoch.value++; run.value++; dragging.value = false;
      cancelAnimation(progress); cancelAnimation(dockX); cancelAnimation(dockFade);
    };
  }, [epoch, run, dragging, progress, dockX, dockFade]);
  useAnimatedReaction(() => ({p: progress.value, completed: completed.value}), (value, previous) => {
    const now = performance.now(), elapsed = now - lastTime.value;
    const changed = previous === null || value.p !== previous.p, completion = value.completed;
    // A later completion corrects the last derivative; an earlier completion
    // is consumed by the endpoint reaction itself. Neither leaves stale speed.
    if (completion && completion.epoch === epoch.value && completion.run === run.value
      && completion.animation === animationRun.value && !dragging.value && value.p === completion.goal) velocity.value = 0;
    else if (!dragging.value && previous !== null && changed && elapsed > 0 && elapsed < 100) velocity.value = (value.p - previous.p) * 1000 / elapsed;
    if (changed) lastTime.value = now;
  });
  const gestures = useMemo(() => {
    // RNGH mutates handler identity on attachment. Each physical detector gets
    // its own instance, even while the outgoing dock chrome remains mounted.
    const pan = (forDock: boolean, current?: SharedValue<boolean>, paintCurrent?: () => boolean) => {
      const releaseFromTarget = (target: boolean, original: PlaybackFence, version: number, gestureRun: number) => {
        let allowed = false;
        try {allowed = (!current || current.value) && (!paintCurrent || paintCurrent());} catch { /* Expired UI paint refuses only this gesture. */ }
        if (allowed) {released(target, original, version, gestureRun); return;}
        // A queued lift can outlive this exact page/paint target while the same
        // playback stays expanded. Cancel only its original UI run, never a new
        // source/gesture and never by writing a business transition.
        if (!mounted.current || intent.current !== source) return;
        runOnUI((originalEpoch: number, originalRun: number, goal: number) => {
          'worklet';
          if (epoch.value !== originalEpoch || run.value !== originalRun) return;
          run.value++; dragging.value = false; cancelAnimation(progress);
          presentationGoal.value = goal;
          const finish = completionFor(goal);
          progress.value = spring ? withSpring(goal, {...spring, velocity: velocity.value}, finish) : withTiming(goal, timing, finish);
        })(version, gestureRun, source.docked ? 1 : 0);
      };
      return Gesture.Pan().enabled(!!source.fence && source.docked === forDock).activeOffsetY([-10, 10]).failOffsetX([-12, 12])
      .onBegin(() => {
        if (current && !current.value || epoch.value !== source.epoch || source.docked !== forDock) return;
        run.value++; started.value = run.value;
        cancelAnimation(progress); origin.value = progress.value; dragging.value = true;
      })
      .onUpdate(event => {
        if (current && !current.value || !dragging.value || epoch.value !== source.epoch || started.value !== run.value) return;
        const value = origin.value + event.translationY / span.value;
        progress.value = value < 0 ? value * .18 : value > 1 ? 1 + (value - 1) * .18 : value;
        velocity.value = event.velocityY / span.value;
      })
      .onEnd((event, success) => {
        // RNGH calls onEnd(false) BEFORE onFinalize(false) for ACTIVE cancellation.
        if (!success || current && !current.value || !dragging.value || epoch.value !== source.epoch || started.value !== run.value || !source.fence) return;
        const target = releaseDock(progress.value, event.velocityY);
        presentationGoal.value = target;
        dragging.value = false;
        releaseVelocity.value = velocity.value;
        const finish = completionFor(target);
        progress.value = spring ? withSpring(target, {...spring, velocity: velocity.value}, finish) : withTiming(target, timing, finish);
        runOnJS(releaseFromTarget)(target === 1, source.fence, source.epoch, started.value);
      })
      .onFinalize((_event, success) => {
        if ((!success || current && !current.value) && dragging.value && epoch.value === source.epoch && started.value === run.value) {
          run.value++; dragging.value = false; cancelAnimation(progress);
          presentationGoal.value = source.docked ? 1 : 0;
          const finish = completionFor(source.docked ? 1 : 0);
          progress.value = spring ? withSpring(source.docked ? 1 : 0, {...spring, velocity: velocity.value}, finish) : withTiming(source.docked ? 1 : 0, timing, finish);
        }
      });
    };
    return {gesture: pan(false), dockGesture: pan(true), chromeGesture: (enabled: boolean, current?: SharedValue<boolean>, paintCurrent?: () => boolean) => pan(false, current, paintCurrent).enabled(enabled && !!source.fence && !source.docked)};
  }, [progress, origin, dragging, span, velocity, releaseVelocity, spring, timing, released, source, epoch, run, started, completionFor, presentationGoal]);
  return useMemo(() => ({progress, velocity, dragging, releaseVelocity, presentationGoal, request, dockX, dockFade, dockTravelFade, dockMotionHosted, ...gestures}), [progress, velocity, dragging, releaseVelocity, presentationGoal, request, dockX, dockFade, dockTravelFade, dockMotionHosted, gestures]);
}

export type PlayerMorphProps = {
  controller: PlayerMorphController;
  fence?: PlaybackFence;
  docked: boolean;
  fullFrame: DockRect;
  dockFrame: DockRect;
  /** The SAME child/native surface stays under this parent across every layout state. */
  children: React.ReactNode | ((context: PlayerSurfaceContext) => React.ReactNode);
  /** Real native last-frame capture, fenced to this source. Never a fabricated backdrop/video clone. */
  retainedSnapshot?: Readonly<{fence: PlaybackFence; image: React.ReactNode; ready?: boolean; failed?: boolean}>;
  /** Retain a real current-source image before geometry-only settlement. */
  prepareLayout?: (fence: PlaybackFence) => Promise<void>;
  onPresentationChange?: (state: Readonly<{fence: PlaybackFence; docked: boolean; moving: boolean; hideSubtitles: boolean}>) => void;
  testID?: string;
};
export type PlayerSurfaceContext = Readonly<{active: boolean; bounds: DockRect; docked: boolean; moving: boolean; subtitlesVisible: boolean; surfaceActive: boolean}>;

const sameRect = (a: DockRect, b: DockRect) => a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;

/** Only transforms/opacity move. Native SurfaceView bounds change once on settlement. */
export function PlayerMorph({controller, fence, docked, fullFrame, dockFrame, children, retainedSnapshot, prepareLayout, onPresentationChange, testID}: PlayerMorphProps) {
  const theme = useTheme(), motion = useMotion();
  const {progress, dragging, velocity} = controller;
  const snapshotPlatform = !theme.isTV && (Platform.OS === 'android' || Platform.OS === 'ios');
  const [frame, setFrame] = useState(docked && validRect(dockFrame) ? dockFrame : fullFrame);
  const [movingFor, setMovingFor] = useState<PlaybackFence | undefined>(undefined);
  const [fadingFor, setFadingFor] = useState<PlaybackFence | undefined>(undefined);
  const moving = currentMorphFence(movingFor, fence);
  const fading = currentMorphFence(fadingFor, fence);
  const snapshot = snapshotPlatform && retainedSnapshot && currentMorphFence(retainedSnapshot.fence, fence) ? retainedSnapshot.image : null;
  const snapshotReady = !!snapshot && retainedSnapshot?.ready === true;
  const currentSnapshot = useRef(snapshotReady); currentSnapshot.current = snapshotReady;
  const full = useSharedValue(fullFrame), dock = useSharedValue(dockFrame), base = useSharedValue(frame);
  const target = useSharedValue(docked ? 1 : 0), surfaceFade = useSharedValue(fence ? 1 : 0);
  const snapshotFade = useSharedValue(1), fadeTicket = useRef(0);
  const active = useSharedValue(fence !== undefined), dragRun = useSharedValue(0);
  const targetFrame = docked && validRect(dockFrame) ? dockFrame : fullFrame;
  const lifetime = useRef({fence, docked, frame: targetFrame});
  if (lifetime.current.docked !== docked || !sameRect(lifetime.current.frame, targetFrame)
    || (lifetime.current.fence !== fence && !currentMorphFence(lifetime.current.fence, fence))) lifetime.current = {fence, docked, frame: targetFrame};
  const transition = lifetime.current;
  const preparingLayout = useRef<typeof transition | undefined>(undefined), preparedLayout = useRef<typeof transition | undefined>(undefined);
  const [layoutReady, setLayoutReady] = useState<typeof transition | undefined>(undefined);
  const settledTransition = useRef(transition), settledDragRun = useRef(0), wasMoving = useRef(false), mounted = useRef(false);
  const cancelFades = useCallback(() => {fadeTicket.current++; cancelAnimation(surfaceFade); cancelAnimation(snapshotFade);}, [surfaceFade, snapshotFade]);
  useEffect(() => {mounted.current = true; return () => {mounted.current = false; cancelFades();};}, [cancelFades]);
  const currentFence = useRef(fence), previousFence = useRef<PlaybackFence | undefined>(undefined);
  currentFence.current = fence;
  useEffect(() => {
    fadeTicket.current++; cancelAnimation(snapshotFade); snapshotFade.value = 1; setFadingFor(undefined);
  }, [transition, snapshotFade]);
  useEffect(() => {if (validRect(fullFrame)) full.value = fullFrame; if (validRect(dockFrame)) dock.value = dockFrame;}, [fullFrame, dockFrame, full, dock]);
  useEffect(() => {target.value = docked ? 1 : 0;}, [docked, target]);
  useEffect(() => {
    if (fence === undefined && previousFence.current === undefined || currentMorphFence(previousFence.current, fence)) return;
    previousFence.current = fence;
    active.value = fence !== undefined;
    cancelAnimation(surfaceFade);
    cancelAnimation(snapshotFade); fadeTicket.current++; setFadingFor(undefined); snapshotFade.value = 1;
    setMovingFor(undefined); wasMoving.current = false; settledTransition.current = transition; settledDragRun.current = dragRun.value;
    surfaceFade.value = fence ? 1 : 0;
    // A new source starts at its own actual layout; an idle gap retains no previous fence.
    const nextFrame = docked && validRect(dockFrame) ? dockFrame : fullFrame;
    if (validRect(nextFrame)) {setFrame(nextFrame); base.value = nextFrame;}
  }, [fence, docked, dockFrame, fullFrame, active, surfaceFade, snapshotFade, base, transition, dragRun]);
  const finishFade = useCallback((ticket: number) => {
    if (mounted.current && fadeTicket.current === ticket && lifetime.current === transition && currentMorphFence(currentFence.current, fence)) setFadingFor(undefined);
  }, [transition, fence]);
  const settle = useCallback((value: number, observedDragRun: number) => {
    if (!mounted.current || lifetime.current !== transition || !currentMorphFence(currentFence.current, fence)
      || dragging.value || dragRun.value !== observedDragRun) return;
    const finalFrame = value === 1 ? dockFrame : fullFrame;
    if (!validRect(finalFrame)) return;
    const previous = settledTransition.current;
    const geometryOnly = previous.docked === transition.docked && currentMorphFence(previous.fence, transition.fence) && !sameRect(previous.frame, transition.frame);
    // A newly installed UI reaction can observe an already-settled progress
    // value. It must not bypass geometry capture/readiness on that first sample.
    if (geometryOnly && !motion.reduce && snapshotPlatform && prepareLayout
      && (preparedLayout.current !== transition || snapshot && !snapshotReady && !retainedSnapshot?.failed)) return;
    setFrame(previous => previous.x === finalFrame.x && previous.y === finalFrame.y && previous.width === finalFrame.width && previous.height === finalFrame.height ? previous : finalFrame);
    base.value = finalFrame;
    setMovingFor(undefined);
    // Reduced layout settles immediately, before its progress timing finishes.
    // That timing's intermediate samples must not re-arm its consumed fade;
    // a genuine new drag can still fade once on return to the same goal.
    if (settledTransition.current !== transition || (motion.reduce ? settledDragRun.current !== observedDragRun : wasMoving.current)) {
      const fade = geometryOnly ? {...motion.timing(), duration: playerPostureCrossFadeMs} : motion.timing();
      if (geometryOnly && motion.reduce) {
        cancelAnimation(surfaceFade); surfaceFade.value = 1;
        cancelAnimation(snapshotFade); snapshotFade.value = 1; fadeTicket.current++; setFadingFor(undefined);
      } else if (Platform.OS === 'android' || snapshotPlatform && !motion.reduce) {
        // Restore the same live producer at its settled bounds beneath the real
        // retained image, then fade that image away exactly once. SurfaceView
        // cannot cross-fade through an ancestor's opacity. Apple uses the same
        // decoded-cover readiness; without that cover its live layer stays up.
        if (currentSnapshot.current) {
          const ticket = ++fadeTicket.current;
          cancelAnimation(snapshotFade); snapshotFade.value = 1; setFadingFor(fence);
          snapshotFade.value = withTiming(0, fade, finished => {if (finished) runOnJS(finishFade)(ticket);});
        }
      } else {
        cancelAnimation(surfaceFade);
        surfaceFade.value = 0;
        surfaceFade.value = withTiming(1, fade);
      }
    }
    settledTransition.current = transition; settledDragRun.current = observedDragRun; wasMoving.current = false;
  }, [fence, fullFrame, dockFrame, base, surfaceFade, snapshotFade, finishFade, motion, transition, dragging, dragRun, prepareLayout, snapshot, snapshotReady, retainedSnapshot?.failed, snapshotPlatform]);
  const updateMoving = useCallback((value: boolean, observedDragRun: number) => {
    if (mounted.current && lifetime.current === transition && currentMorphFence(currentFence.current, fence) && dragRun.value === observedDragRun) {
      if (value) {fadeTicket.current++; cancelAnimation(snapshotFade); snapshotFade.value = 1; setFadingFor(undefined);}
      wasMoving.current = value; setMovingFor(value ? fence : undefined);
    }
  }, [fence, transition, dragRun, snapshotFade]);
  useEffect(() => {
    if (motion.reduce && !dragging.value) settle(docked ? 1 : 0, dragRun.value);
  }, [docked, motion.reduce, dragging, settle, dragRun, prepareLayout, frame, transition]);
  useEffect(() => {
    // A fold/rotation can change only geometry while progress is already at
    // its goal. It must settle the existing native view without a new source,
    // drag or dock transition. Active drags settle through the reaction below.
    const goal = docked ? 1 : 0;
    if (dragging.value || !motion.reduce && (Math.abs(progress.value - goal) >= .002 || Math.abs(velocity.value) >= .02)) return;
    if (fence && !motion.reduce && snapshotPlatform && prepareLayout && !sameRect(frame, transition.frame) && preparedLayout.current !== transition) {
      if (preparingLayout.current === transition) return;
      preparingLayout.current = transition; setMovingFor(fence);
      // A failed/unsupported capture still settles with live composition active.
      // Never blank the producer while waiting for a usable image.
      void Promise.resolve().then(() => prepareLayout(fence)).catch(() => {}).then(() => {
        if (!mounted.current || lifetime.current !== transition || !currentMorphFence(currentFence.current, fence)) return;
        preparedLayout.current = transition; preparingLayout.current = undefined; setLayoutReady(transition);
      });
      return;
    }
    // The mounted retained Image must finish decoding before it may occlude a
    // live producer. An image error settles with the live producer still visible.
    if (!motion.reduce && preparedLayout.current === transition && snapshot && !snapshotReady && !retainedSnapshot?.failed) return;
    settle(goal, dragRun.value);
  }, [transition, docked, fence, frame, dragging, progress, velocity, settle, dragRun, prepareLayout, layoutReady, motion.reduce, snapshot, snapshotReady, retainedSnapshot?.failed, snapshotPlatform]);
  useAnimatedReaction(() => ({active: active.value, p: progress.value, v: velocity.value, drag: dragging.value, goal: target.value}), (now, previous) => {
    if (!now.active) return;
    const beganDrag = now.drag && !previous?.drag;
    if (beganDrag) dragRun.value++;
    const settled = !now.drag && Math.abs(now.p - now.goal) < .002 && Math.abs(now.v) < .02;
    const wasSettled = previous && !previous.drag && Math.abs(previous.p - previous.goal) < .002 && Math.abs(previous.v) < .02;
    if (settled && !wasSettled) runOnJS(settle)(now.goal, dragRun.value);
    else if (!settled && (wasSettled || beganDrag)) runOnJS(updateMoving)(true, dragRun.value);
  }, [settle, updateMoving, dragRun]);
  useEffect(() => {if (fence && currentMorphFence(currentFence.current, fence)) onPresentationChange?.({fence, docked, moving: moving || fading, hideSubtitles: docked || moving || fading});}, [fence, docked, moving, fading, onPresentationChange]);
  const interpolateFrame = () => {
    'worklet';
    const p = progress.value;
    return {x: full.value.x + (dock.value.x - full.value.x) * p, y: full.value.y + (dock.value.y - full.value.y) * p,
      width: full.value.width + (dock.value.width - full.value.width) * p, height: full.value.height + (dock.value.height - full.value.height) * p};
  };
  const surfaceStyle = useAnimatedStyle(() => {
    const rect = interpolateFrame();
    const transform = (motion.transforms || dragging.value) && Platform.OS !== 'android';
    return {opacity: surfaceFade.value, transform: transform ? [{translateX: rect.x - base.value.x + (rect.width - base.value.width) / 2}, {translateY: rect.y - base.value.y + (rect.height - base.value.height) / 2}, {scaleX: rect.width / base.value.width}, {scaleY: rect.height / base.value.height}] : []};
  });
  const previewStyle = useAnimatedStyle(() => {
    const rect = interpolateFrame();
    return {opacity: moving ? 1 : snapshotFade.value, transform: motion.transforms || dragging.value ? [{translateX: rect.x - full.value.x + (rect.width - full.value.width) / 2}, {translateY: rect.y - full.value.y + (rect.height - full.value.height) / 2}, {scaleX: rect.width / full.value.width}, {scaleY: rect.height / full.value.height}] : []};
  });
  // Android SurfaceView punches a native composition hole even beneath an alpha-zero
  // ancestor. Hide the idle host from native layout while retaining the SAME child.
  return <View testID={testID} pointerEvents="none" accessibilityElementsHidden={!fence} importantForAccessibility={fence ? 'auto' : 'no-hide-descendants'} style={[dockStyles.fill, {zIndex: theme.layers.player + 1, display: fence ? 'flex' : 'none'}]}>
    <Animated.View style={[{position: 'absolute', left: frame.x, top: frame.y, width: frame.width, height: frame.height, overflow: 'hidden', borderRadius: docked && !moving ? 7 : 0, backgroundColor: theme.colors.shadow}, surfaceStyle, moving && snapshotReady ? {opacity: 0} : undefined]}>
      {typeof children === 'function' ? children({active: fence !== undefined, bounds: {x: 0, y: 0, width: frame.width, height: frame.height}, docked, moving: moving || fading,
        subtitlesVisible: fence !== undefined && !docked && !moving && !fading, surfaceActive: !(moving && snapshotReady)}) : children}
      {fence && docked && !moving && <View style={[dockStyles.expand, {backgroundColor: alpha(theme.colors.dim, .62)}]}><Icon name="i-full" size={11}/></View>}
    </Animated.View>
    {(moving || fading) && snapshot ? <Animated.View style={[{position: 'absolute', left: fullFrame.x, top: fullFrame.y, width: fullFrame.width, height: fullFrame.height, overflow: 'hidden', backgroundColor: theme.colors.shadow}, previewStyle]}>{snapshot}</Animated.View> : null}
  </View>;
}
