export type Rect = {x: number; y: number; width: number; height: number};
export type Insets = {top: number; bottom: number; left: number; right: number};
export type Placement = 'bottom-start' | 'bottom-center' | 'bottom-end' | 'top-start' | 'top-center' | 'top-end';

export function containsPoint(rect: Rect, x: number, y: number): boolean {
  return x >= rect.x && x <= rect.x + rect.width && y >= rect.y && y <= rect.y + rect.height;
}
export function popoverRect(anchor: Rect, viewport: {width: number; height: number}, size: {width: number; height: number}, insets: Insets, placement: Placement, offset = 6): Rect {
  const margin = 8;
  const width = Math.min(size.width, Math.max(0, viewport.width - insets.left - insets.right - margin * 2));
  const height = Math.min(size.height, Math.max(0, viewport.height - insets.top - insets.bottom - margin * 2));
  const start = insets.left + margin;
  const end = viewport.width - insets.right - margin - width;
  const desiredX = placement.endsWith('center') ? anchor.x + (anchor.width - width) / 2 : placement.endsWith('end') ? anchor.x + anchor.width - width : anchor.x;
  const bottom = anchor.y + anchor.height + offset;
  const top = anchor.y - offset - height;
  const topLimit = insets.top + margin;
  const bottomLimit = viewport.height - insets.bottom - margin;
  const wantsTop = placement.startsWith('top');
  const y = wantsTop ? top >= topLimit ? top : bottom : bottom + height <= bottomLimit ? bottom : top;
  return {x: Math.max(start, Math.min(end, desiredX)), y: Math.max(topLimit, Math.min(bottomLimit - height, y)), width, height};
}
export function sheetHeights(detents: readonly number[] | undefined, height: number, top: number): number[] {
  const max = Math.max(1, height - Math.max(top, 24));
  const valid = detents?.filter(value => Number.isFinite(value) && value > 0);
  const source = valid?.length ? valid : [height / 2, max];
  return [...new Set(source.map(value => Math.max(1, Math.min(max, value))))].sort((a, b) => a - b);
}
/** Height is positive upwards; gesture velocity is positive downwards, in points per second. */
export function releaseSheet(visibleHeight: number, velocityY: number, heights: readonly number[]): {closed: boolean; index: number} {
  'worklet';
  const projected = visibleHeight - velocityY * 0.2;
  if (velocityY > 0 && projected < heights[0] / 2) return {closed: true, index: 0};
  let index = 0;
  for (let i = 1; i < heights.length; i++) if (Math.abs(heights[i] - projected) < Math.abs(heights[index] - projected)) index = i;
  return {closed: false, index};
}
export function resistSheet(visibleHeight: number, max: number): number {
  'worklet';
  if (visibleHeight < 0) return visibleHeight * 0.18;
  return visibleHeight > max ? max + (visibleHeight - max) * 0.18 : visibleHeight;
}
export function dismissToastGesture(x: number, y: number, velocityX: number, velocityY: number): boolean {
  'worklet';
  return Math.abs(x) > 40 || y > 40 || Math.abs(velocityX) > 500 || velocityY > 500;
}
