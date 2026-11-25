import {NativeModules, Platform} from 'react-native';
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

/** Hardware facts evaluated to decide whether a device defaults to the lite effects tier. */
export type LiteEffectsFacts = Readonly<{
  os?: string;
  isTV?: boolean;
  manufacturer?: string;
  memoryBytes?: number;
  processorCount?: number;
}>;

const GiB = 1024 ** 3;
const LOW_RAM_BYTES = 3 * GiB;
const LOW_CORE_COUNT = 4;

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

/**
 * Pure hardware-tier heuristic: Fire TV and low-RAM (<= 3 GiB) or low-core (<= 4) Android
 * devices default to lite effects. Apple platforms (iOS / iPadOS / tvOS) stay on full effects.
 */
export function shouldUseLiteEffects(facts: LiteEffectsFacts | null | undefined): boolean {
  if (!facts || facts.os !== 'android') return false;
  const manufacturer = typeof facts.manufacturer === 'string' ? facts.manufacturer.trim().toLowerCase() : '';
  if (facts.isTV === true && manufacturer === 'amazon') return true;
  const memory = facts.memoryBytes;
  if (typeof memory === 'number' && Number.isFinite(memory) && memory > 0 && memory <= LOW_RAM_BYTES) return true;
  const cores = facts.processorCount;
  if (typeof cores === 'number' && Number.isFinite(cores) && cores > 0 && cores <= LOW_CORE_COUNT) return true;
  return false;
}

type GlobalEffectsTier = {__PORTICO_EFFECTS_TIER__?: string};
type RawHardwareFacts = Readonly<{manufacturer?: unknown; memoryBytes?: unknown; processorCount?: unknown}>;
type FactsModule = Readonly<{facts?: () => Promise<RawHardwareFacts | null | undefined> | RawHardwareFacts | null | undefined}>;

function explicitGlobalTier(): boolean | undefined {
  const tier = (globalThis as GlobalEffectsTier).__PORTICO_EFFECTS_TIER__;
  if (tier === 'lite') return true;
  if (tier === 'full') return false;
  return undefined;
}

function readPlatformManufacturer(): string | undefined {
  const constants = (Platform as {constants?: {Manufacturer?: unknown; Brand?: unknown}} | undefined)?.constants;
  if (typeof constants?.Manufacturer === 'string' && constants.Manufacturer.trim()) return constants.Manufacturer;
  if (typeof constants?.Brand === 'string' && constants.Brand.trim()) return constants.Brand;
  return undefined;
}

async function readFacts(module: FactsModule | undefined): Promise<RawHardwareFacts | null> {
  if (!module || typeof module.facts !== 'function') return null;
  try {
    const result = await module.facts();
    return result && typeof result === 'object' ? result : null;
  } catch {
    return null;
  }
}

const finitePositive = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : undefined;

const nonEmptyString = (value: unknown): string | undefined =>
  typeof value === 'string' && value.trim() ? value : undefined;

// The lite switch. Manual overrides (`globalThis.__PORTICO_EFFECTS_TIER__` or `setLiteEffects()`)
// take permanent precedence over automatic hardware detection.
const initialOverride = explicitGlobalTier();
let liteEffects = initialOverride ?? false;
let manualOverride = initialOverride !== undefined;
let detectionPromise: Promise<boolean> | undefined;
const liteListeners = new Set<() => void>();

function applyLiteEffects(on: boolean): void {
  if (on === liteEffects) return;
  liteEffects = on;
  liteListeners.forEach(listener => listener());
}

export function getLiteEffects(): boolean {return liteEffects;}
export function setLiteEffects(on: boolean): void {
  manualOverride = true;
  applyLiteEffects(on);
}
export function subscribeLiteEffects(listener: () => void): () => void {
  liteListeners.add(listener);
  return () => {liteListeners.delete(listener);};
}

/**
 * Probes device hardware facts once on startup and enables lite effects on low-RAM / low-core
 * Android and Fire TV devices. Never clobbers an explicit `globalThis.__PORTICO_EFFECTS_TIER__`
 * or `setLiteEffects()` override. Safe when `NativeModules` is mocked or empty.
 */
export function detectLiteEffects(): Promise<boolean> {
  const globalOverride = explicitGlobalTier();
  if (manualOverride || globalOverride !== undefined) {
    if (!manualOverride && globalOverride !== undefined) applyLiteEffects(globalOverride);
    return Promise.resolve(liteEffects);
  }
  if (detectionPromise) return detectionPromise;

  const os = typeof Platform?.OS === 'string' ? Platform.OS : undefined;
  const isTV = Platform?.isTV === true;
  const manufacturer = readPlatformManufacturer();

  // Synchronous fast path: Fire TV is identifiable from Platform constants before native probes
  // resolve, and non-Android platforms (iOS / iPadOS / tvOS) always stay on full effects.
  if (shouldUseLiteEffects({os, isTV, manufacturer})) {
    applyLiteEffects(true);
    detectionPromise = Promise.resolve(liteEffects);
    return detectionPromise;
  }
  if (os !== 'android') {
    detectionPromise = Promise.resolve(liteEffects);
    return detectionPromise;
  }

  detectionPromise = (async () => {
    const modules = NativeModules as {PorticoMpvPlaybackModule?: FactsModule; PorticoCapabilities?: FactsModule} | undefined;
    const mpvFacts = await readFacts(modules?.PorticoMpvPlaybackModule);
    let memoryBytes = finitePositive(mpvFacts?.memoryBytes);
    let processorCount = finitePositive(mpvFacts?.processorCount);
    let resolvedManufacturer = manufacturer ?? nonEmptyString(mpvFacts?.manufacturer);

    if (memoryBytes === undefined || processorCount === undefined || (isTV && !resolvedManufacturer)) {
      const capFacts = await readFacts(modules?.PorticoCapabilities);
      memoryBytes ??= finitePositive(capFacts?.memoryBytes);
      processorCount ??= finitePositive(capFacts?.processorCount);
      resolvedManufacturer ??= nonEmptyString(capFacts?.manufacturer);
    }

    if (manualOverride || explicitGlobalTier() !== undefined) return liteEffects;

    if (shouldUseLiteEffects({os, isTV, manufacturer: resolvedManufacturer, memoryBytes, processorCount})) {
      applyLiteEffects(true);
    }
    return liteEffects;
  })();
  return detectionPromise;
}
