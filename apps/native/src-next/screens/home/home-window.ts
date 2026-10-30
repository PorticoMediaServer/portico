export type HomeRowBox = Readonly<{id: string; y: number; height: number}>;

/** Sorted row geometry is also used on the UI thread. JS is notified only when
 * the intersecting range changes, not once per scrolling frame. */
export function homeVisibleRange(boxes: readonly HomeRowBox[], offset: number, height: number): readonly [number, number] {
  'worklet';
  let low = 0, high = boxes.length;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (boxes[middle].y + boxes[middle].height <= offset) low = middle + 1;
    else high = middle;
  }
  const first = low;
  high = boxes.length;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (boxes[middle].y < offset + Math.max(0, height)) low = middle + 1;
    else high = middle;
  }
  return [first, Math.max(first - 1, low - 1)];
}

/** Keep neighbors available for native TV Up/Down and keep the original return
 * target resident even while an overlay owns focus. Other rows become spacers. */
export function homeMountedRows(count: number, first: number, last: number, pinned: readonly number[], overscan = 2): ReadonlySet<number> {
  const mounted = new Set<number>();
  const add = (from: number, to: number) => {for (let index = Math.max(0, from); index <= Math.min(count - 1, to); index++) mounted.add(index);};
  add(first - overscan, Math.max(first, last) + overscan);
  for (const index of pinned) if (index >= 0 && index < count) add(index - 1, index + 1);
  return mounted;
}
