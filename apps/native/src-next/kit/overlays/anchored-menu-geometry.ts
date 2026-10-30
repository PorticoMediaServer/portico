type Rect = Readonly<{x: number; y: number; width: number; height: number}>;
type Insets = Readonly<{top: number; bottom: number; left: number; right: number}>;

/** Window-space opener; authored size/gaps share the caller's logical canvas scale. */
export function rightEndMenuRect(anchor: Rect, viewport: {width: number; height: number}, size: {width: number; height: number}, insets: Insets, offset = 6, alignOffset = 0, scale = 1): Rect {
  const margin = 8 * scale;
  const left = insets.left + margin, top = insets.top + margin;
  const right = Math.max(left, viewport.width - insets.right - margin);
  const bottom = Math.max(top, viewport.height - insets.bottom - margin);
  const width = Math.min(size.width * scale, right - left), height = Math.min(size.height * scale, bottom - top);
  return {x: Math.max(left, Math.min(right - width, anchor.x + anchor.width + offset * scale)),
    y: Math.max(top, Math.min(bottom - height, anchor.y + anchor.height - height + alignOffset * scale)), width, height};
}
