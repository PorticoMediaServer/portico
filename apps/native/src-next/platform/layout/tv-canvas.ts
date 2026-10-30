/** The page and every separate TV overlay share the authored 1920 × 1080 canvas. */
export function tvCanvasFrame(width: number, height: number) {
  const scale = Math.min(width / 1920, height / 1080);
  return {width: 1920, height: 1080, scale, left: (width - 1920 * scale) / 2, top: (height - 1080 * scale) / 2};
}
