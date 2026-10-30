type Rect = Readonly<{x: number; y: number; width: number; height: number}>;
type Insets = Readonly<{top: number; bottom: number; left: number; right: number}>;

/** tablet9: beside the item, left when necessary, upward in the bottom rows. */
export function touchMenuRect(anchor: Rect, viewport: {width: number; height: number}, contentHeight: number, insets: Insets) {
  const margin = 8, gap = 12, width = 300;
  const left = insets.left + margin, right = viewport.width - insets.right - margin;
  const top = insets.top + margin, bottom = viewport.height - insets.bottom - margin;
  const height = Math.min(contentHeight, bottom - top);
  const side = anchor.x + anchor.width + gap + width <= right ? 'right' : 'left';
  const x = Math.max(left, Math.min(right - width, side === 'right' ? anchor.x + anchor.width + gap : anchor.x - gap - width));
  const y = Math.max(top, Math.min(bottom - height, anchor.y + height <= bottom ? anchor.y : anchor.y + anchor.height - height));
  return {x, y, width, height, side, arrowY: Math.max(16, Math.min(height - 16, anchor.y + anchor.height / 2 - y))};
}
