export type NativePinRowBounds = Readonly<{id: string; y: number; height: number}>;

/** All geometry comes from this group's actual native row layouts. */
export function nativePinDrop(rows: readonly NativePinRowBounds[], pickedId: string, translationY: number): Readonly<{beforeId?: string; lineY: number}> | undefined {
  const picked = rows.find(row => row.id === pickedId);
  if (!picked || !Number.isFinite(translationY) || rows.some(row => !Number.isFinite(row.y) || !Number.isFinite(row.height) || row.height <= 0)) return;
  const middle = picked.y + picked.height / 2 + translationY;
  const remaining = rows.filter(row => row.id !== pickedId), before = remaining.find(row => middle < row.y + row.height / 2);
  return before ? {beforeId: before.id, lineY: before.y} : {lineY: remaining.length ? remaining[remaining.length - 1].y + remaining[remaining.length - 1].height : picked.y};
}
