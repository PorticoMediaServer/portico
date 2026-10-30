export type SelectionIntent = {intent: 'toggle' | 'enter' | 'range'; shiftKey?: boolean};

/** select.html phone .pick/.cbm and TV .pk; the caller owns placement on artwork. */
export function selectionCircleGeometry(isTV: boolean, size?: number) {
  const diameter = size ?? (isTV ? 44 : 24);
  return {diameter, border: isTV ? 3 : 2, check: diameter * 2 / 3};
}

export function selectionIntent(selected: boolean, selecting: boolean, shiftKey?: boolean): SelectionIntent {
  return {intent: shiftKey ? 'range' : selecting || selected ? 'toggle' : 'enter', ...(shiftKey === undefined ? {} : {shiftKey})};
}

/** Null means no measurement; it never starts a spinner or a synthetic countdown. */
export function holdFraction(progress: number | null): number | null {
  return progress === null || !Number.isFinite(progress) ? null : Math.max(0, Math.min(1, progress));
}
