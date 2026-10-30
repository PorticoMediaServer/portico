import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ActivityIndicator, View} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedReaction, useAnimatedStyle, useSharedValue, withDelay, withSpring, withTiming} from 'react-native-reanimated';
import type {NativeStartPresentation} from '../../platform/playback/start-presentation';
import {Artwork} from '../artwork';
import {useMotion} from '../useMotion';
import {useTheme} from '../theme';
import {useInitialPlayerPublication} from './PlayerMorphBridge';
import {initialPictureInHost, initialPictureMix, initialPictureRect, initialPictureTransform, initialPictureRetired, retireInitialPicture, sameInitialPictureRect, initialPictureSettled} from './initial-picture-model';
import type {DockRect, PlayerMorphController} from './types';

const current = (offer: NativeStartPresentation) => {try {return !offer.signal.aborted && offer.paintCurrent();} catch {return false;}};
type Props = Readonly<{offer: NativeStartPresentation; host: React.RefObject<View | null>; full: DockRect; dock?: DockRect; docked: boolean; firstFrame: boolean; controller: PlayerMorphController; travel: number}>;

/** Pixels and visual clocks only. The one native Surface keeps producing below
 * the opaque UI shutter: disabling surfaceActive would detach Android/mpv and
 * could prevent the very firstFrame fact this presenter must wait for. */
export function InitialPicturePresentation({offer, host, full, dock, docked, firstFrame, controller, travel}: Props) {
  const motion = useMotion(), theme = useTheme(), reduce = motion.reduce;
  const subscribe = useCallback((listener: () => void) => {
    const off = offer.subscribe(listener); offer.signal.addEventListener('abort', listener);
    return () => {off(); offer.signal.removeEventListener('abort', listener);};
  }, [offer]);
  const valid = useSyncExternalStore(subscribe, useCallback(() => current(offer), [offer]));
  const [placement, setPlacement] = useState<{node: View; host: DockRect; source: DockRect}>();
  const [retired, setRetired] = useState(() => initialPictureRetired(offer.key)), [ready, setReady] = useState(false), [finished, setFinished] = useState(false);
  const [landing, setLanding] = useState<{version: number; goal: number} | undefined>(undefined), [entered, setEntered] = useState(false);
  const landed = landing !== undefined;
  const mounted = useRef(false), revision = useRef(0);
  const base = useSharedValue<DockRect>({x: 0, y: 0, width: 1, height: 1});
  const frame = useSharedValue<DockRect>({x: 0, y: 0, width: 1, height: 1}), target = useSharedValue(full);
  const dockFrame = useSharedValue<DockRect | undefined>(dock), wantedDock = useSharedValue<DockRect | undefined>(dock);
  const fallbackGoal = useSharedValue(docked ? 1 : 0), goal = controller.presentationGoal ?? fallbackGoal;
  const manual = useSharedValue(false), dragOrigin = useSharedValue(frame.value), dragProgress = useSharedValue(0), travelSpan = useSharedValue(travel);
  const ticket = useSharedValue(0), fadeTicket = useSharedValue(0), controlsTicket = useSharedValue(0), arrived = useSharedValue(false);
  const revealed = useRef(false), previousReduction = useRef(reduce);
  const controlsPolicy = useRef<typeof timing | undefined>(undefined);
  const title = useSharedValue(1), page = useSharedValue(0), controls = useSharedValue(0), poster = useSharedValue(motion.reduce ? 0 : 1);
  const progress = controller.progress, dragging = controller.dragging, velocity = controller.velocity;
  const spring = useMemo(() => motion.spring('player'), [motion]);
  const timing = useMemo(() => motion.timing('standard'), [motion]);
  const drawnFrame = useCallback(() => {
    'worklet';
    if (manual.value) return dragging.value ? {...dragOrigin.value, y: dragOrigin.value.y + (progress.value - dragProgress.value) * travelSpan.value} : frame.value;
    if (initialPictureRect(dockFrame.value)) return initialPictureMix(frame.value, dockFrame.value, progress.value);
    // Before a genuine mini slot exists, follow only actual gesture travel. A
    // late target is sprung from these current pixels, never snapped or guessed.
    return {...frame.value, y: frame.value.y + progress.value * travelSpan.value};
  }, [manual, dragging, dragOrigin, progress, dragProgress, travelSpan, frame, dockFrame]);
  const captured = valid && !retired && !finished && !!placement;
  const admitted = captured && ready;
  useLayoutEffect(() => {
    if (reduce && !previousReduction.current && !revealed.current && !dragging.value) {
      poster.value = 0;
      if (admitted) poster.value = withTiming(1, timing);
    }
    previousReduction.current = reduce;
  }, [reduce, admitted, dragging, poster, timing]);
  const markLanded = useCallback((expected: number) => {
    if (mounted.current && ticket.value === expected && current(offer)
      && initialPictureSettled(goal.value, progress.value, velocity.value, dragging.value, arrived.value, manual.value, initialPictureRect(dockFrame.value))) {
      const actualGoal = goal.value;
      setLanding(previous => previous?.version === expected && previous.goal === actualGoal ? previous : {version: expected, goal: actualGoal});
    }
  }, [ticket, offer, goal, progress, velocity, dragging, arrived, manual, dockFrame]);
  const markPending = useCallback((expected: number) => {
    if (mounted.current && ticket.value === expected && current(offer)) setLanding(undefined);
  }, [ticket, offer]);
  const controlsEntered = useCallback((expected: number) => {
    if (mounted.current && controlsTicket.value === expected && current(offer)) setEntered(true);
  }, [controlsTicket, offer]);
  const finish = useCallback((expected: number) => {
    if (!mounted.current || fadeTicket.value !== expected || !current(offer)
      || !initialPictureSettled(goal.value, progress.value, velocity.value, dragging.value, arrived.value, manual.value, initialPictureRect(dockFrame.value))) return;
    retireInitialPicture(offer.key); setFinished(true);
  }, [fadeTicket, offer, goal, progress, velocity, dragging, arrived, manual, dockFrame]);
  useLayoutEffect(() => {
    const measurements = revision;
    mounted.current = true;
    return () => {mounted.current = false; measurements.current++; retireInitialPicture(offer.key); ticket.value++; fadeTicket.value++; controlsTicket.value++; cancelAnimation(frame); cancelAnimation(title); cancelAnimation(page); cancelAnimation(controls); cancelAnimation(poster);};
  }, [offer, ticket, fadeTicket, controlsTicket, frame, title, page, controls, poster]);
  useEffect(() => {
    if (!valid || retired || finished) return;
    let live = true, pending: number | undefined;
    const check = () => {if (!live) return; if (!current(offer)) {setRetired(true); return;} pending = requestAnimationFrame(check);};
    check();
    return () => {live = false; if (pending !== undefined) cancelAnimationFrame(pending);};
  }, [offer, valid, retired, finished]);
  useLayoutEffect(() => {
    if (!valid || retired) return;
    const node = host.current, expected = ++revision.current;
    if (!node) return;
    node.measureInWindow((x, y, width, height) => {
      if (!mounted.current || revision.current !== expected || host.current !== node || !current(offer)) return;
      const hostRect = {x, y, width, height}, source = initialPictureInHost(offer.bounds, hostRect);
      if (!source) {setRetired(true); return;}
      if (placement) {
        // A changed host origin/size retires this particular measured offer;
        // never reinterpret its original window rect as a successor measurement.
        if (placement.node !== node || Object.keys(hostRect).some(key => hostRect[key as keyof DockRect] !== placement.host[key as keyof DockRect])) setRetired(true);
      } else {base.value = source; frame.value = source; setPlacement({node, host: hostRect, source});}
    });
  }, [host, offer, valid, retired, placement, full, base, frame]);
  useEffect(() => {target.value = full; wantedDock.value = dock; fallbackGoal.value = docked ? 1 : 0; travelSpan.value = travel;}, [full, dock, docked, travel, target, wantedDock, fallbackGoal, travelSpan]);
  const settleFrame = useCallback((toDock: number) => {
    'worklet';
    const destination = toDock === 1 ? dockFrame.value : target.value;
    if (!initialPictureRect(destination)) return;
    const expected = ++ticket.value;
    arrived.value = false;
    // Reduction changes the layout immediately and cross-fades its paint. Keep
    // no hidden spatial animation that could jump backwards if policy changes.
    if (!spring) {frame.value = destination; arrived.value = true; return;}
    const finish = (finished?: boolean) => {
      'worklet';
      if (finished && ticket.value === expected && !dragging.value && goal.value === toDock) arrived.value = true;
    };
    frame.value = withSpring(destination, spring, finish);
  }, [ticket, arrived, dragging, goal, frame, target, dockFrame, spring]);
  useEffect(() => {
    if (!admitted) return;
    title.value = withTiming(docked ? 1 : 0, motion.timing('quick', docked ? 'out' : 'in'));
    page.value = docked ? withTiming(0, motion.timing('quick', 'in')) : motion.reduce ? withTiming(1, timing) : withDelay(180, withTiming(1, {...motion.timing('standard'), duration: 280}));
    return () => {cancelAnimation(title); cancelAnimation(page);};
  }, [admitted, docked, motion, timing, title, page]);
  useEffect(() => {
    if (!admitted) return;
    setLanding(undefined);
    if (docked && !manual.value) {ticket.value++; cancelAnimation(frame);}
    else if (!dragging.value) settleFrame(docked ? 1 : 0);
  }, [admitted, docked, full, motion.reduce, settleFrame, dragging, ticket, frame, manual]);
  useAnimatedReaction(() => ({drag: dragging.value, p: progress.value, v: velocity.value, goal: goal.value, arrived: arrived.value, dock: wantedDock.value, version: ticket.value}), (now, before) => {
    if (!admitted) return;
    const changedGoal = before && now.goal !== before.goal;
    const released = !now.drag && before?.drag;
    // Invalidate the previous pose BEFORE starting this release's settlement.
    // Otherwise a 1→0 release would cancel its own newly installed spring.
    if (changedGoal) {
      fadeTicket.value++; cancelAnimation(poster);
      ticket.value++; arrived.value = false; cancelAnimation(frame); runOnJS(markPending)(ticket.value);
    }
    if (now.drag && !before?.drag) {
      if (manual.value) {dragOrigin.value = frame.value; dragProgress.value = now.p;}
      ticket.value++; arrived.value = false; cancelAnimation(frame);
      fadeTicket.value++; cancelAnimation(poster);
      runOnJS(markPending)(ticket.value);
    } else if (!now.drag && before?.drag) {
      if (manual.value) frame.value = {...dragOrigin.value, y: dragOrigin.value.y + (now.p - dragProgress.value) * travelSpan.value};
      if (manual.value || now.goal === 0) settleFrame(now.goal);
      if (reduce) {poster.value = 0; poster.value = withTiming(1, timing);}
    }
    if (!now.drag && initialPictureRect(now.dock) && !sameInitialPictureRect(now.dock, dockFrame.value)) {
      if (Math.abs(now.p) < .002 && !manual.value) dockFrame.value = now.dock;
      else {
        const actual = drawnFrame();
        ticket.value++; cancelAnimation(frame); frame.value = actual;
        dockFrame.value = now.dock; manual.value = true; settleFrame(now.goal);
        return;
      }
    } else if (!now.drag && !released && changedGoal && (manual.value || now.goal === 0)) {settleFrame(now.goal); return;}
    if (now.goal === 0 && now.arrived && !now.drag && Math.abs(now.p) < .002 && Math.abs(now.v) < .02
      && !(before?.version === now.version && before.goal === 0 && before.arrived && !before.drag && Math.abs(before.p) < .002 && Math.abs(before.v) < .02)) {manual.value = false; runOnJS(markLanded)(now.version);}
    if (now.goal === 1 && initialPictureRect(dockFrame.value) && (!manual.value || now.arrived) && !now.drag && Math.abs(now.p - 1) < .002 && Math.abs(now.v) < .02
      && !(before?.version === now.version && before.goal === 1 && before.arrived === now.arrived && !before.drag && Math.abs(before.p - 1) < .002 && Math.abs(before.v) < .02)) runOnJS(markLanded)(now.version);
  }, [admitted, settleFrame, markLanded, markPending, drawnFrame, reduce, timing]);
  useEffect(() => {
    if (!admitted || entered || !landing && !controlsPolicy.current || controlsPolicy.current === timing) return;
    controlsPolicy.current = timing;
    const expected = ++controlsTicket.value;
    controls.value = withTiming(1, timing, complete => {if (complete && controlsTicket.value === expected) runOnJS(controlsEntered)(expected);});
  }, [admitted, landing, entered, controls, controlsTicket, controlsEntered, timing]);
  useEffect(() => {
    cancelAnimation(poster);
    const expected = ++fadeTicket.value;
    if (admitted && landed && firstFrame) {
      revealed.current = true;
      poster.value = withTiming(0, timing, completed => {if (completed && fadeTicket.value === expected) runOnJS(finish)(expected);});
    }
    else if (admitted && (!firstFrame || !revealed.current)) poster.value = motion.reduce || revealed.current ? withTiming(1, timing) : 1;
    else if (!admitted) poster.value = motion.reduce ? 0 : 1;
    return () => {fadeTicket.value++; cancelAnimation(poster);};
  }, [admitted, landed, landing, firstFrame, poster, timing, motion.reduce, fadeTicket, finish]);
  const controlsAdmitted = landed || controlsPolicy.current !== undefined;
  const publication = useMemo(() => captured ? {key: offer.key, fence: offer.fence, title, page, controls, landed, entering: !entered, controlsAdmitted} : undefined,
    [captured, offer, title, page, controls, landed, entered, controlsAdmitted]);
  useInitialPlayerPublication(publication);
  const clipStyle = useAnimatedStyle(() => {
    const actual = reduce && !dragging.value ? goal.value === 1 && initialPictureRect(dockFrame.value) ? dockFrame.value : target.value : drawnFrame();
    const transform = initialPictureTransform(base.value, actual);
    return {opacity: poster.value, transform: [{translateX: transform.translateX}, {translateY: transform.translateY}, {scaleX: transform.scaleX}, {scaleY: transform.scaleY}]};
  });
  const imageStyle = useAnimatedStyle(() => {
    const actual = reduce && !dragging.value ? goal.value === 1 && initialPictureRect(dockFrame.value) ? dockFrame.value : target.value : drawnFrame();
    const transform = initialPictureTransform(base.value, actual);
    return {transform: [{scaleX: transform.imageScaleX}, {scaleY: transform.imageScaleY}]};
  });
  const spinnerStyle = useAnimatedStyle(() => {
    const actual = reduce && !dragging.value ? goal.value === 1 && initialPictureRect(dockFrame.value) ? dockFrame.value : target.value : drawnFrame();
    const transform = initialPictureTransform(base.value, actual);
    return {transform: [{scaleX: 1 / transform.scaleX}, {scaleY: 1 / transform.scaleY}]};
  });
  if (!valid || retired || finished || !placement) return null;
  return <View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', inset: 0}}>
    {/* Opaque until a genuine current first frame can replace the landed poster.
        This never changes the native producer's lease, host or readiness. */}
    {(!landed || !firstFrame) && <View style={{position: 'absolute', left: full.x, top: full.y, width: full.width, height: full.height, backgroundColor: theme.colors.ink}}/>}
    <Animated.View testID="initial-player-artwork" style={[{position: 'absolute', left: placement.source.x, top: placement.source.y, width: placement.source.width, height: placement.source.height, overflow: 'hidden', backgroundColor: theme.colors.ink}, clipStyle]}>
      <Animated.View style={[{width: placement.source.width, height: placement.source.height}, imageStyle]}>
        <Artwork source={offer.artwork} width={placement.source.width} height={placement.source.height}
          onReady={source => {if (mounted.current && current(offer) && source === offer.artwork) setReady(true);}}
          onError={() => {if (mounted.current && current(offer)) setRetired(true);}}/>
      </Animated.View>
      {landed && !firstFrame && <Animated.View style={[{position: 'absolute', left: '50%', top: '50%', width: 0, height: 0, alignItems: 'center', justifyContent: 'center'}, spinnerStyle]}><ActivityIndicator color={theme.colors.text}/></Animated.View>}
    </Animated.View>
  </View>;
}
