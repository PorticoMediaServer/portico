import React, {useEffect, useRef, useState} from 'react';
import {Animated, Easing, type StyleProp, type ViewStyle} from 'react-native';
import {useMotion} from '../useMotion';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import {contentFadeDuration, contentFadeEasing, paintSkeletonVisible} from './loading-boundary-model';

export type LoadingPaintProps = Readonly<{
  /** Real content is ready: only its paint changes, never its ancestry. */
  ready: boolean;
  /** The exact static shape to paint over the content while it is loading. */
  skeleton: React.ReactNode;
  children?: React.ReactNode;
  /** Content that was already ready when this mounted: no quiet window, no fade. */
  immediate?: boolean;
  quietMs?: number;
  contentFadeMs?: number;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;

/** A non-gating paint presenter for callers whose real child owns admission and
 * lifetime (the reader host's WebView). Children are always rendered in the same
 * native ancestry; ready only controls opacity. A quiet 300 ms window then paints
 * the supplied static shape over them, never taking layout, focus or input, and
 * the content fades over 180 ms — 160 ms linear under reduced motion. */
export function LoadingPaint({ready, skeleton, children, immediate = false, quietMs = 300, contentFadeMs = 180, style, testID}: LoadingPaintProps) {
  const motion = useMotion(), paint = useOverlayPaintMode();
  const cached = useRef(immediate && ready);
  const [cycle, setCycle] = useState(0);
  const [doneCycle, setDoneCycle] = useState(cached.current ? 0 : -1);
  const opacity = useRef(new Animated.Value(cached.current ? 1 : 0)).current;
  const animation = useRef<Animated.CompositeAnimation | undefined>(undefined);
  const entered = useRef(cached.current);
  const stop = () => {animation.current?.stop(); animation.current = undefined;};
  useEffect(() => stop, []);
  // Derived during render, so a new loading cycle never paints with the previous
  // cycle's completion even for one frame. Only the first cached mount is immediate.
  const [prevReady, setPrevReady] = useState(ready);
  if (prevReady !== ready) {
    setPrevReady(ready);
    if (!ready) {cached.current = false; setDoneCycle(-1); setCycle(value => value + 1);}
  }
  // Quiet window: owned by ready, quietMs and the derived cycle, never by policy.
  useEffect(() => {
    if (!paint.interactive) return;
    if (ready) {setDoneCycle(cycle); return;}
    let live = true;
    const timer = setTimeout(() => {if (live) setDoneCycle(cycle);}, quietMs);
    return () => {live = false; clearTimeout(timer);};
  }, [ready, quietMs, cycle, paint.interactive]);
  // Paint only: fresh entries start transparent, later policy changes time from
  // the current opacity instead of jumping.
  useEffect(() => {
    if (!paint.interactive) {stop(); return;}
    if (!ready) {stop(); opacity.setValue(0); entered.current = false; return;}
    stop();
    if (cached.current) {opacity.setValue(1); entered.current = true; return;}
    if (!entered.current) {opacity.setValue(0); entered.current = true;}
    const easing = motion.reduce ? Easing.linear : Easing.bezier(...contentFadeEasing.out);
    const fade = Animated.timing(opacity, {toValue: 1, duration: contentFadeDuration(motion.reduce, contentFadeMs, motion.duration('standard')), easing, useNativeDriver: true});
    animation.current = fade;
    fade.start(() => {if (animation.current === fade) animation.current = undefined;});
  }, [ready, motion, contentFadeMs, opacity, paint.interactive]);
  return <Animated.View testID={testID} style={[{flex: 1}, style]}>
    <Animated.View style={{flex: 1, opacity}}>{children}</Animated.View>
    {paintSkeletonVisible(ready, doneCycle, cycle) && <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
      style={{position: 'absolute', inset: 0}}>{skeleton}</Animated.View>}
  </Animated.View>;
}
