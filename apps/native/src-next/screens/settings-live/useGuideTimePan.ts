import {useCallback, useEffect, useLayoutEffect, useMemo, useRef} from 'react';
import {Gesture} from 'react-native-gesture-handler';
import {cancelAnimation, runOnJS, useAnimatedReaction, useSharedValue, withDecay} from 'react-native-reanimated';
import type {GuideSourceSummary} from '../../../../../packages/client-core/src/guide/directory';
import {useReducedMotion} from '../../kit/useMotion';
import {nativeGuidePanRange, nativeGuidePanTarget} from './guide-pan-model';

type Props = Readonly<{
  enabled: boolean; revision: number; start: number; end: number; pxPerMs: number;
  channelWidth: number; sources: readonly GuideSourceSummary[];
  commit: (start: number) => void; current: () => boolean;
}>;

/** The native UI thread moves only visible lanes. Half-span commits keep the existing
 * bounded GuideWindowStore ahead; no lineup, schedule or persistence store is created. */
export function useGuideTimePan({enabled, revision, start, end, pxPerMs, channelWidth, sources, commit, current}: Props) {
  const span = Math.round(end - start), reduceMotion = useReducedMotion();
  const range = useMemo(() => nativeGuidePanRange(sources, span), [sources, span]);
  const active = enabled && !!range && Number.isFinite(pxPerMs) && pxPerMs > 0;
  const position = useSharedValue(start * pxPerMs), origin = useSharedValue(start * pxPerMs);
  const lastPublished = useSharedValue(start * pxPerMs), moving = useSharedValue(false), engaged = useSharedValue(false);
  const gestureGeneration = useRef(0), nativeGeneration = useSharedValue(0), startedGeneration = useSharedValue(0);
  const mounted = useRef(true), pending = useRef(new Set<number>()), previous = useRef({active, revision, pxPerMs, first: range?.first, last: range?.last});
  const publish = useCallback((pixels: number, generation: number) => {
    if (!mounted.current || generation !== gestureGeneration.current || !current()) return;
    const target = nativeGuidePanTarget(pixels / pxPerMs, span, range);
    if (target === undefined) return;
    pending.current.add(target);
    while (pending.current.size > 4) pending.current.delete(pending.current.values().next().value!);
    commit(target);
  }, [commit, current, pxPerMs, range, span]);
  useLayoutEffect(() => {
    const prior = previous.current;
    const internal = pending.current.has(start) && prior.active === active && prior.revision === revision && prior.pxPerMs === pxPerMs && prior.first === range?.first && prior.last === range?.last;
    pending.current.clear();
    if (!internal) {
      nativeGeneration.value = ++gestureGeneration.current;
      moving.value = false; cancelAnimation(position); position.value = start * pxPerMs; lastPublished.value = position.value;
    }
    previous.current = {active, revision, pxPerMs, first: range?.first, last: range?.last};
  }, [active, start, revision, pxPerMs, range?.first, range?.last, position, lastPublished, moving, nativeGeneration]);
  useEffect(() => {mounted.current = true; return () => {mounted.current = false; moving.value = false; cancelAnimation(position);};}, [position, moving]);
  useAnimatedReaction(() => position.value, pixels => {
    if (moving.value && Math.abs(pixels - lastPublished.value) >= span * pxPerMs / 2) {
      lastPublished.value = pixels; runOnJS(publish)(pixels, startedGeneration.value);
    }
  }, [span, pxPerMs, publish]);
  const first = (range?.first ?? start) * pxPerMs, last = (range?.last ?? start) * pxPerMs;
  const gesture = useMemo(() => Gesture.Pan().enabled(active).maxPointers(1).activeOffsetX([-10, 10]).failOffsetY([-12, 12])
    .onTouchesDown((event, manager) => {
      // A real touch interrupts momentum before the next gesture activates, including
      // a vertical scroll or channel-column tap that never becomes a horizontal pan.
      if (moving.value) {cancelAnimation(position); moving.value = false; runOnJS(publish)(position.value, startedGeneration.value);}
      if (event.changedTouches.some(touch => touch.x < channelWidth)) manager.fail();
    })
    .onStart(() => {cancelAnimation(position); origin.value = position.value; startedGeneration.value = nativeGeneration.value; moving.value = true; engaged.value = true;})
    .onUpdate(event => {if (startedGeneration.value === nativeGeneration.value) position.value = Math.max(first, Math.min(last, origin.value - event.translationX));})
    .onEnd(event => {
      if (startedGeneration.value !== nativeGeneration.value) {moving.value = false; cancelAnimation(position); return;}
      // phone-guide.md: reduced motion stops exactly on lift; ordinary panning decays
      // from genuine native velocity and can be interrupted by the next touch.
      if (reduceMotion) {moving.value = false; runOnJS(publish)(position.value, startedGeneration.value);}
      else position.value = withDecay({velocity: -event.velocityX, clamp: [first, last]}, finished => {
        if (finished) {moving.value = false; runOnJS(publish)(position.value, startedGeneration.value);}
      });
    }).onFinalize((_event, success) => {
      if (!success && engaged.value) {moving.value = false; cancelAnimation(position); runOnJS(publish)(position.value, startedGeneration.value);}
      engaged.value = false;
    }), [active, channelWidth, position, origin, moving, engaged, first, last, reduceMotion, publish, nativeGeneration, startedGeneration]);
  const adjust = useCallback((delta: number) => {
    if (!current()) return;
    const target = nativeGuidePanTarget(start + delta, span, range);
    if (target !== undefined) commit(target);
  }, [commit, current, range, start, span]);
  return {gesture, position, active, adjust};
}
