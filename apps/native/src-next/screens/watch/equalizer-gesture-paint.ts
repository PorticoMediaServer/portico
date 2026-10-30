/** UI-thread math for the equalizer's finger preview (FX-067 / RV-MOB-006).
 *
 * These are worklets so a gesture can move the thumb, the fill and the tooltip
 * from the real pointer displacement without waiting for a React render. They
 * read nothing but their arguments: the authored -12..12 dB bounds, the measured
 * track length and the orientation the caller already draws. They never decide a
 * gain that is written anywhere — the caller's own commit callback still owns
 * validation, ordering and cadence. */

export const EQUALIZER_MIN_DB = -12;
export const EQUALIZER_MAX_DB = 12;
export const EQUALIZER_RANGE_DB = EQUALIZER_MAX_DB - EQUALIZER_MIN_DB;

/** A normalized position clamped to the drawn track. */
export function equalizerFraction(value: number): number {
  'worklet';
  return value < 0 ? 0 : value > 1 ? 1 : value;
}

/** The drawn fraction of an authored gain, clamped to the same -12..12 bounds. */
export function equalizerFractionFromGain(gain: number): number {
  'worklet';
  const bounded = gain < EQUALIZER_MIN_DB ? EQUALIZER_MIN_DB : gain > EQUALIZER_MAX_DB ? EQUALIZER_MAX_DB : gain;
  return (EQUALIZER_MAX_DB - bounded) / EQUALIZER_RANGE_DB;
}

/** The gain a fraction names, for the caller's existing commit callback. */
export function equalizerGainFromFraction(fraction: number): number {
  'worklet';
  return EQUALIZER_MAX_DB - equalizerFraction(fraction) * EQUALIZER_RANGE_DB;
}

/** The pointer's fraction from an origin frozen at begin plus the TOTAL measured
 * displacement since then, so two identical events cannot move it twice. Zero or
 * unmeasured travel yields undefined: the caller then commits nothing, exactly as
 * the previous unmeasured track did. */
export function equalizerDragFraction(origin: number, translation: number, travel: number, horizontal: boolean): number | undefined {
  'worklet';
  if (!(travel > 0)) return undefined;
  return equalizerFraction(horizontal ? origin - translation / travel : origin + translation / travel);
}

/** The previewed gain, or undefined when the track is not measured. */
export function equalizerDragGain(origin: number, translation: number, travel: number, horizontal: boolean): number | undefined {
  'worklet';
  const fraction = equalizerDragFraction(origin, translation, travel, horizontal);
  return fraction === undefined ? undefined : equalizerGainFromFraction(fraction);
}

/** The drawn fill as a fixed full-size view scaled about its middle and moved to
 * the drawn span. The horizontal track reads its position as 1 - fraction
 * (fraction = (12 - gain) / 24), the vertical one as the fraction itself, so the
 * two offsets carry opposite signs: +6 dB on a 200 pt track fills 100..150
 * horizontally (centre 125) and 50..100 vertically (centre 75). */
export function equalizerFillTransform(fraction: number, travel: number, horizontal: boolean): {length: number; offset: number} {
  'worklet';
  const f = equalizerFraction(fraction), position = horizontal ? 1 - f : f;
  return {length: Math.abs(0.5 - position), offset: ((0.5 + position) / 2 - 0.5) * travel};
}

/** The thumb moved by translation from its own static origin, never re-laid out. */
export function equalizerThumbTranslate(fraction: number, travel: number, horizontal: boolean): number {
  'worklet';
  const f = equalizerFraction(fraction);
  return horizontal ? (1 - f) * travel : f * travel;
}

/** The dragging tooltip's offset from its static top, 60 above the thumb. */
export function equalizerTooltipTranslate(fraction: number, travel: number): number {
  'worklet';
  return equalizerFraction(fraction) * travel - 60;
}
