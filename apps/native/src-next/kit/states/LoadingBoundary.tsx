import React, {useEffect, useRef, useState} from 'react';
import {Animated, Easing, View, type StyleProp, type ViewStyle} from 'react-native';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import {useMotion} from '../useMotion';
import {cachedAfterLoadingCycle, contentFadeDuration, contentFadeEasing, nextLoadingPhase, type LoadingPhase} from './loading-boundary-model';

export type LoadingBoundaryProps = Readonly<{
  /** The real content is ready to render. */
  ready: boolean;
  /** The exact shape of the content that is loading. Shapes own their own arrival. */
  skeleton: React.ReactNode;
  children?: React.ReactNode;
  /** Content that was already cached when the boundary mounted: no quiet window, no fade. */
  immediate?: boolean;
  quietMs?: number;
  contentFadeMs?: number;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;

/** A caller that still holds only a bare skeleton gets the drawn loading order:
 * stay quiet for 300 ms, then show the exact shape (its own arrival fade), and
 * fade real content over 180 ms — 160 ms linear under the shared reduced-motion
 * policy, otherwise the design system's out curve. Every fresh loading cycle
 * starts its own quiet window, and a cached mount only skips the very first
 * fade. Presentation only: callers keep their own read, error and admission state. */
export function LoadingBoundary({ready, skeleton, children, immediate = false, quietMs = 300, contentFadeMs = 180, style, testID}: LoadingBoundaryProps) {
  const motion = useMotion(), paint = useOverlayPaintMode();
  const cached = useRef(immediate && ready);
  const phaseRef = useRef<LoadingPhase>(nextLoadingPhase('quiet', {type: 'mounted', ready, cached: cached.current}));
  const [phase, setPhase] = useState<LoadingPhase>(phaseRef.current);
  const opacity = useRef(new Animated.Value(cached.current ? 1 : 0)).current;
  const animation = useRef<Animated.CompositeAnimation | undefined>(undefined);
  const entered = useRef(cached.current);
  const stop = () => {animation.current?.stop(); animation.current = undefined;};
  useEffect(() => stop, []);
  // Quiet window and phase only: a policy or duration change must never restart the wait.
  useEffect(() => {
    if (!paint.interactive) return;
    if (!ready) {
      cached.current = cachedAfterLoadingCycle(cached.current, false);
      const quiet = nextLoadingPhase(phaseRef.current, {type: 'readyChanged', ready: false});
      if (quiet !== phaseRef.current) {phaseRef.current = quiet; setPhase(quiet);}
      let live = true;
      const timer = setTimeout(() => {
        if (!live) return;
        const next = nextLoadingPhase(phaseRef.current, {type: 'quietElapsed'});
        if (next !== phaseRef.current) {phaseRef.current = next; setPhase(next);}
      }, quietMs);
      return () => {live = false; clearTimeout(timer);};
    }
    const next = nextLoadingPhase(phaseRef.current, {type: 'readyChanged', ready: true});
    if (next !== phaseRef.current) {phaseRef.current = next; setPhase(next);}
  }, [ready, quietMs, paint.interactive]);
  // Content fade only: retargets from the current opacity when the policy changes.
  useEffect(() => {
    if (!paint.interactive) {stop(); return;}
    if (phase !== 'content') {entered.current = false; return;}
    stop();
    if (cached.current) {opacity.setValue(1); entered.current = true; return;}
    if (!entered.current) {opacity.setValue(0); entered.current = true;}
    const easing = motion.reduce ? Easing.linear : Easing.bezier(...contentFadeEasing.out);
    const fade = Animated.timing(opacity, {toValue: 1, duration: contentFadeDuration(motion.reduce, contentFadeMs, motion.duration('standard')), easing, useNativeDriver: true});
    animation.current = fade;
    fade.start(() => {if (animation.current === fade) animation.current = undefined;});
  }, [phase, motion, contentFadeMs, opacity, paint.interactive]);
  return <Animated.View testID={testID} style={[style, phase === 'content' ? {opacity} : null]}>{phase === 'content' ? children
    : <View key={phase} pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={phase === 'quiet' ? {opacity: 0} : undefined}>{skeleton}</View>}</Animated.View>;
}
