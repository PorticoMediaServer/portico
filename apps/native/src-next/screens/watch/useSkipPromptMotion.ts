import {useEffect, useLayoutEffect, useRef} from 'react';
import {cancelAnimation, useAnimatedStyle, useSharedValue, withSpring, withTiming} from 'react-native-reanimated';
import {useMotion} from '../../kit/useMotion';
import {usePresence} from '../../kit/overlays/surface';

/** One current prompt, one inert exit; command admission stays with WatchChrome. */
export function useSkipPromptMotion<T extends {id: string}>(wanted: T | undefined, scope: unknown, lifted: boolean, liftDistance: number) {
  const motion = useMotion(), open = !!wanted && scope !== undefined;
  const last = useRef<{value: T; scope: unknown} | undefined>(undefined);
  useLayoutEffect(() => {if (wanted && scope !== undefined) last.current = {value: wanted, scope};}, [wanted, scope]);
  const present = usePresence(open, motion.duration('quick'));
  const opacity = useSharedValue(0), rise = useSharedValue(12), lift = useSharedValue(0);
  const identity = useRef<{scope: unknown; key: string} | undefined>(undefined);
  const key = wanted?.id;
  useEffect(() => {
    if (scope === undefined) {opacity.value = 0; return;}
    if (open && key && (identity.current?.scope !== scope || identity.current?.key !== key)) {identity.current = {scope, key}; opacity.value = 0;}
    if (open && opacity.value === 0) rise.value = motion.transforms ? 12 : 0;
    opacity.value = withTiming(open ? 1 : 0, motion.timing(open ? 'standard' : 'quick'));
    if (open) rise.value = motion.transforms ? withTiming(0, motion.timing('standard')) : 0;
    return () => {cancelAnimation(opacity); cancelAnimation(rise);};
  }, [key, motion, open, opacity, rise, scope]);
  useEffect(() => {
    const spring = motion.spring('snap');
    lift.value = spring ? withSpring(lifted ? liftDistance : 0, spring) : 0;
    return () => cancelAnimation(lift);
  }, [lift, liftDistance, lifted, motion]);
  const bodyStyle = useAnimatedStyle(() => ({opacity: opacity.value, transform: motion.transforms ? [{translateY: rise.value}] : []}));
  const positionStyle = useAnimatedStyle(() => ({transform: motion.transforms ? [{translateY: -lift.value}] : []}));
  return {value: scope === undefined ? undefined : wanted ?? (present && scope !== undefined && last.current?.scope === scope ? last.current.value : undefined), interactive: open, bodyStyle, positionStyle};
}
