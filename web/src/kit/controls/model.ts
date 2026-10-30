/** Fractions are presentation inputs, never inferred from a display label. */
export function progressFraction(value: number): number {
  return Number.isFinite(value) ? Math.min(1, Math.max(0, value)) : 0;
}

export function rangeBounds(min: number, max: number, step: number) {
  const low = Number.isFinite(min) ? min : 0;
  const high = Number.isFinite(max) ? Math.max(low, max) : Math.max(low, 100);
  const increment = Number.isFinite(step) && step > 0 ? step : 1;
  return {low, high, increment};
}

/** Keep range values finite, bounded and aligned with the caller's step. */
export function rangeValue(value: number, min: number, max: number, step: number): number {
  const {low, high, increment} = rangeBounds(min, max, step);
  const bounded = Math.min(high, Math.max(low, Number.isFinite(value) ? value : low));
  const result = low + Math.round((bounded - low) / increment) * increment;
  return Math.min(high, Math.max(low, Number(result.toPrecision(12))));
}

type NavigableOption = Readonly<{label: string; disabled?: boolean}>;
/** A picker keeps disabled choices visible while skipping them during keyboard navigation. */
export function moveOption(options: readonly NavigableOption[], current: number, direction: number): number {
  if (!options.length) return -1;
  for (let offset = 1; offset <= options.length; offset++) {
    const index = ((current + offset * (direction < 0 ? -1 : 1)) % options.length + options.length) % options.length;
    if (!options[index].disabled) return index;
  }
  return -1;
}

export function matchingOption(options: readonly NavigableOption[], query: string, current: number): number {
  const text = query.toLocaleLowerCase('en-US');
  if (!text) return current;
  const repeated = [...text].every(letter => letter === text[0]);
  const prefix = repeated ? text[0] : text;
  const start = repeated ? current + 1 : Math.max(0, current);
  for (let offset = 0; offset < options.length; offset++) {
    const index = (start + offset + options.length) % options.length;
    if (!options[index].disabled && options[index].label.toLocaleLowerCase('en-US').startsWith(prefix)) return index;
  }
  return current;
}
