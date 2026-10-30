/** The motion and effects tier, named as native's policy (P2-70): `reduced` is the accessibility
 * setting, `lite` the performance tier for weak devices. Nothing detects lite yet; a build or host
 * opts in with `globalThis.__PORTICO_EFFECTS_TIER__ = 'lite'`, as on native. The root carries the
 * tier as `data-motion-tier`. Web's lite paths currently do what reduced does, but each consumer
 * keeps its own lite hook so a build (TV web, say) can keep motion and drop only blur. */
export type MotionTier = 'full' | 'reduced' | 'lite';

export function liteEffectsRequested(scope: object = globalThis): boolean {
  return (scope as {__PORTICO_EFFECTS_TIER__?: unknown}).__PORTICO_EFFECTS_TIER__ === 'lite';
}
export function motionTier(reduce: boolean, lite = liteEffectsRequested()): MotionTier {
  return reduce ? 'reduced' : lite ? 'lite' : 'full';
}
