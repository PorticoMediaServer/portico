/** A layer of reader chrome drawn over the surface, in layout (not painted) pixels. */
export type ChromeLayerBox = Readonly<{offsetTop: number; offsetHeight: number}>;

/** Space a fixed page must leave above and below it so the whole page sits between the
 * reader's top and bottom chrome. Layout offsets, not painted boxes: the chrome's 8px
 * entry/exit slide must not nudge the fit. The top is the bottom edge of the lowest top
 * layer; the bottom is the distance from the reader's bottom to the highest bottom layer
 * (the page strip sits above the progress footer). Layers that are not laid out report
 * nothing and reserve nothing. */
export function fixedPageClearance(height: number, top: readonly ChromeLayerBox[], bottom: readonly ChromeLayerBox[]): Readonly<{top: number; bottom: number}> {
  const bounded = (value: number) => Number.isFinite(value) ? Math.min(Math.max(0, value), Math.max(0, height)) : 0;
  const laid = (layer: ChromeLayerBox) => layer.offsetHeight > 0;
  return {
    top: bounded(Math.max(0, ...top.filter(laid).map(layer => layer.offsetTop + layer.offsetHeight))),
    bottom: bounded(Math.max(0, ...bottom.filter(laid).map(layer => height - layer.offsetTop))),
  };
}
