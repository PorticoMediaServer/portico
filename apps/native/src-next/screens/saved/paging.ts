/** A grid prefetches about ten cards before its currently loaded end. */
export function savedNearEnd(offset: number, viewport: number, extent: number, itemStride: number, columns = 1): boolean {
  return [offset, viewport, extent, itemStride, columns].every(Number.isFinite) && viewport > 0 && extent > 0 && itemStride > 0 && columns > 0
    && Math.max(0, offset) + viewport >= extent - Math.ceil(10 / columns) * itemStride;
}

/** A drag names an existing occurrence and an existing insertion neighbor. */
export function savedReorderTarget(ids: readonly string[], id: string, translation: number, rowHeight: number): Readonly<{index: number; afterEntryId: string}> | undefined {
  const from = ids.indexOf(id);
  if (from < 0 || !Number.isFinite(translation) || !Number.isFinite(rowHeight) || rowHeight <= 0) return;
  const index = Math.max(0, Math.min(ids.length - 1, from + Math.round(translation / rowHeight)));
  if (index === from) return;
  const rest = ids.filter(value => value !== id);
  return {index, afterEntryId: index === 0 ? '' : rest[index - 1]!};
}

/** The whole row and its grip can recognize the same touch. Only the recognizer
 * that successfully acquired this pickup may move or finalize it. In particular,
 * the unstarted long-press recognizer must not cancel an active grip drag. */
export function createSavedReorderGesture(callbacks: {
  start: (id: string) => boolean; move: (translation: number) => void; drop: (success: boolean) => void;
}) {
  let owner: 'row' | 'handle' | undefined;
  return {
    begin: (recognizer: 'row' | 'handle', id: string): boolean => {
      if (owner || !callbacks.start(id)) return false;
      owner = recognizer; return true;
    },
    move: (recognizer: 'row' | 'handle', translation: number) => {if (owner === recognizer) callbacks.move(translation);},
    finish: (recognizer: 'row' | 'handle', success: boolean) => {
      if (owner !== recognizer) return;
      owner = undefined; callbacks.drop(success);
    },
  };
}
