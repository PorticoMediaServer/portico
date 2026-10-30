/** A logical-frame knot in the original gain automation. */
export type GainKnot = Readonly<{frame: number; value: number}>;

/**
 * Rebuild this renderer-owned automation while suspended. The current value is interpolated from
 * the original knots, not sampled from AudioParam after the context has overshot the frontier.
 * Future original knots retain their original positions; a sliced curve is never stretched.
 */
export function rebuildGain(param: AudioParam, held: number, initial: number, knots: readonly GainKnot[], contextSeconds: (logicalFrame: number) => number): void {
  if (!Number.isFinite(held) || !Number.isFinite(initial)) throw new Error('invalid gain timeline');
  let lastFrame = -Infinity;
  for (const knot of knots) {
    if (!Number.isFinite(knot.frame) || !Number.isFinite(knot.value) || knot.frame <= lastFrame) throw new Error('invalid gain knot');
    lastFrame = knot.frame;
  }
  const value = gainAt(held, initial, knots);
  let previous: GainKnot | undefined;
  for (const knot of knots) { if (knot.frame > held) break; previous = knot; }
  param.cancelScheduledValues(0);
  param.setValueAtTime(value, contextSeconds(held));
  let hasPriorKnot = !!previous;
  for (const knot of knots) {
    if (knot.frame <= held) continue;
    const time = contextSeconds(knot.frame);
    if (hasPriorKnot) param.linearRampToValueAtTime(knot.value, time);
    else param.setValueAtTime(knot.value, time);
    hasPriorKnot = true;
  }
}

/** Sample the same piecewise-linear timeline at a logical frame. */
export function gainAt(frame: number, initial: number, knots: readonly GainKnot[]): number {
  let value = initial, previous: GainKnot | undefined;
  for (const knot of knots) {
    if (knot.frame > frame) {
      if (previous) return previous.value + (knot.value - previous.value) * (frame - previous.frame) / (knot.frame - previous.frame);
      break;
    }
    value = knot.value; previous = knot;
  }
  return value;
}
