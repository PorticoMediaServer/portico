export type SelectionIntent = Readonly<{intent: 'toggle' | 'enter' | 'range'; shiftKey?: boolean}>;

/** UI intent only; ids, range membership and whole-view targets belong to SelectionModel. */
export function circleIntent(selecting: boolean, shiftKey: boolean): SelectionIntent {
  return {intent: shiftKey ? 'range' : selecting ? 'toggle' : 'enter', shiftKey};
}

/** Invalid or absent progress never becomes invented progress or a looping animation. */
export function holdProgress(progress: number | null): number | null {
  return progress === null || !Number.isFinite(progress) ? null : Math.max(0, Math.min(1, progress));
}
