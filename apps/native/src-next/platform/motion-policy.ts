import {easings, motion, motionRules, reducedMotion, springs} from '@portico/design';

export type MotionDuration = keyof typeof motion;
export type MotionCurve = keyof typeof easings;
export type MotionSpring = keyof typeof springs;
/** `reduced` is the accessibility setting; `lite` is the performance tier for weak devices. */
export type MotionTier = 'full' | 'reduced' | 'lite';
/** Every expensive effect asks here. Lite simplifies; it never drops a screen to flat ink. */
export type EffectsPolicy = Readonly<{
  /** Native backdrop blur; off means the drawn translucent fill. */
  blur: boolean;
  /** Ready-made glow layers around focus, cards and panels. */
  glow: boolean;
  /** Soft drop shadows (iOS shadow*, Android elevation, boxShadow). */
  shadows: boolean;
  /** Ambient drift and the hero's own rotation clock. */
  drift: boolean;
  /** Fades for content that arrives or is inserted (cards, rows). Navigation feedback stays. */
  enterFades: boolean;
}>;

const effectsByTier: Readonly<Record<MotionTier, EffectsPolicy>> = {
  full: {blur: true, glow: true, shadows: true, drift: true, enterFades: true},
  reduced: {blur: true, glow: true, shadows: true, drift: false, enterFades: true},
  lite: {blur: false, glow: false, shadows: false, drift: false, enterFades: false},
};

/** Effects for the combined state: reduced removes motion, lite removes cost; both apply together. */
export function effectsPolicy(reduce: boolean, lite: boolean): EffectsPolicy {
  if (!lite) return effectsByTier[reduce ? 'reduced' : 'full'];
  return effectsByTier.lite;
}

/** Reduced motion fades settled states; a live drag continues to follow its finger. */
export function motionPolicy(reduce: boolean, lite = false) {
  return {
    reduce,
    lite,
    tier: (reduce ? 'reduced' : lite ? 'lite' : 'full') as MotionTier,
    effects: effectsPolicy(reduce, lite),
    transforms: !reduce,
    fingerTracking: reducedMotion.fingerTracking,
    rules: motionRules,
    duration: (name: MotionDuration) => reduce ? reducedMotion.durationMs : motion[name],
    curve: (name: MotionCurve) => reduce ? reducedMotion.easing : easings[name],
    spring: (name: MotionSpring, velocity = 0) => reduce ? undefined : {...springs[name], velocity},
  };
}

// The lite switch. No device detection yet: a dev setting (or a later heuristic) flips it.
let liteEffects = (globalThis as {__PORTICO_EFFECTS_TIER__?: string}).__PORTICO_EFFECTS_TIER__ === 'lite';
const liteListeners = new Set<() => void>();
export function getLiteEffects(): boolean {return liteEffects;}
export function setLiteEffects(on: boolean): void {
  if (on === liteEffects) return;
  liteEffects = on;
  liteListeners.forEach(listener => listener());
}
export function subscribeLiteEffects(listener: () => void): () => void {
  liteListeners.add(listener);
  return () => {liteListeners.delete(listener);};
}
