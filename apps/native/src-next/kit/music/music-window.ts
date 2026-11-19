export function musicWindow(count: number, columns: number, stride: number, top: number, offset: number, height: number, pin?: number): readonly number[] {
  if (!count || columns < 1 || stride <= 0) return [];
  const rows = Math.ceil(count / columns), first = Math.max(0, Math.floor((offset - top) / stride) - 2);
  const last = Math.min(rows - 1, Math.ceil((offset + Math.max(0, height) - top) / stride) + 2);
  const indices: number[] = [];
  for (let index = first * columns; index < Math.min(count, (last + 1) * columns); index++) indices.push(index);
  if (pin !== undefined && pin >= 0 && pin < count && !indices.includes(pin)) indices.push(pin);
  return indices;
}

/** Preserve the actual first visible item when the bounded cursor pager drops
 * an older chunk. Index zero is relative to this retained window, not the album. */
export function musicWindowShift(previous: readonly string[], next: readonly string[], columns: number, stride: number, localOffset: number): number {
  if (localOffset < 0 || !previous.length || !next.length) return 0;
  const index = Math.min(previous.length - 1, Math.max(0, Math.floor(localOffset / stride) * columns));
  const retained = next.indexOf(previous[index]!);
  return retained < 0 ? 0 : (Math.floor(retained / columns) - Math.floor(index / columns)) * stride;
}
