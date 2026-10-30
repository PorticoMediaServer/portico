import {useMemo, useSyncExternalStore} from 'react';
import {AccessibilityInfo} from 'react-native';
import {Easing, ReduceMotion, type WithTimingConfig, type WithSpringConfig} from 'react-native-reanimated';
import {effectsPolicy, getLiteEffects, motionPolicy, subscribeLiteEffects, type EffectsPolicy, type MotionCurve, type MotionDuration, type MotionSpring} from '../platform/motion-policy';
import {useTheme} from './theme';
import {easings} from '@portico/design';

// One app-wide answer, read synchronously by every later mount. Per-component
// async reads let each new surface play its first animation at full motion.
let systemReduce = false, subscribed = false;
const systemListeners = new Set<() => void>();
function setSystemReduce(value: boolean) {
  if (value === systemReduce) return;
  systemReduce = value;
  systemListeners.forEach(listener => listener());
}
function subscribeSystemReduce(listener: () => void) {
  systemListeners.add(listener);
  if (!subscribed && typeof AccessibilityInfo?.addEventListener === 'function') {
    subscribed = true;
    let changed = false;
    AccessibilityInfo.addEventListener('reduceMotionChanged', value => {changed = true; setSystemReduce(value);});
    if (typeof AccessibilityInfo.isReduceMotionEnabled === 'function') void AccessibilityInfo.isReduceMotionEnabled().then(value => {if (!changed) setSystemReduce(value);}, () => {});
  }
  return () => {systemListeners.delete(listener);};
}
const systemSnapshot = () => systemReduce;

export function useReducedMotion(): boolean {
  const {reduceMotion} = useTheme();
  const system = useSyncExternalStore(subscribeSystemReduce, systemSnapshot, systemSnapshot);
  return reduceMotion || system;
}

/** The performance tier (dev setting today). */
export function useLiteEffects(): boolean {
  return useSyncExternalStore(subscribeLiteEffects, getLiteEffects, getLiteEffects);
}

/** Blur, glow, shadows, drift, arrival fades and the backdrop's detail for the current tier. */
export function useEffectsPolicy(): EffectsPolicy {
  return effectsPolicy(useReducedMotion(), useLiteEffects());
}

const curves = Object.fromEntries(Object.entries(easings).map(([name, value]) => {
  const points = value.match(/-?\d*\.?\d+/g)?.map(Number) as [number, number, number, number] | undefined;
  return [name, points ? Easing.bezier(...points) : Easing.linear];
})) as Record<MotionCurve, ReturnType<typeof Easing.bezier>>;

/** Retarget shared values directly. Reduced movement uses timing() on opacity, never a transform spring. */
export function useMotion() {
  const reduce = useReducedMotion(), lite = useLiteEffects();
  return useMemo(() => {
    const policy = motionPolicy(reduce, lite);
    return {
      ...policy,
      timing: (name: MotionDuration = 'standard', curve: MotionCurve = 'out'): WithTimingConfig => ({duration: policy.duration(name), easing: curves[reduce ? 'linear' : curve], reduceMotion: ReduceMotion.Never}),
      spring: (name: MotionSpring = 'settle', velocity = 0): WithSpringConfig | undefined => {
        const config = policy.spring(name, velocity);
        return config ? {...config, reduceMotion: ReduceMotion.Never} : undefined;
      },
    };
  }, [reduce, lite]);
}
