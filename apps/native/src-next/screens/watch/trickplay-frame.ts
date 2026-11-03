import {trickplayFrameAt, type TrickplaySet} from '../../../../../packages/client-core/src/trickplay';

/** Where one preview frame sits in its sprite sheet, with the sheet kept at its native size.
 * The sheet image is laid out unscaled (so every tile of the strip on that sheet asks for the
 * same decode, which Android decodes once and shares), offset so the wanted frame sits at the
 * window's corner; the window, one source frame in size, is then scaled to the display box on
 * the GPU. Nothing upscales the whole sheet in memory (P2-71-PERF). The set's own geometry and
 * sheet URLs come from the server (PB-2); none is derived here. */
export function nativeTrickplayWindow(set: TrickplaySet | null | undefined, seconds: number, box: Readonly<{width: number; height: number}>) {
  if (!set || !(box.width > 0 && box.height > 0) || !Number.isFinite(seconds)) return undefined;
  const frame = trickplayFrameAt(set, Math.max(0, seconds));
  if (!frame) return undefined;
  return {
    tileUrl: frame.tileUrl,
    window: {width: frame.width, height: frame.height},
    sheet: {width: set.columns * set.tileWidth, height: set.rows * set.tileHeight, left: -frame.x, top: -frame.y},
    scaleX: box.width / frame.width, scaleY: box.height / frame.height,
  };
}
