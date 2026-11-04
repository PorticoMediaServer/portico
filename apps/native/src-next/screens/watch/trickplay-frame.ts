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

/** phone-player 1b: the scrub card's size. */
export const SCRUB_CARD = Object.freeze({width: 178, height: 100});
/** Where the scrub card's centre sits on a timeline `width` wide: over the knob, kept whole inside
 * the timeline at either end (a 390-wide phone timeline included). */
export function scrubCardCentre(fraction: number, width: number): number {
  if (!(width > 0)) return 0;
  const half = SCRUB_CARD.width / 2, x = Math.max(0, Math.min(1, Number.isFinite(fraction) ? fraction : 0)) * width;
  return width <= SCRUB_CARD.width ? width / 2 : Math.max(half, Math.min(width - half, x));
}
