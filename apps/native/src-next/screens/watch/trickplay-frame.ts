import type {PlaybackOptions} from '../../../../../packages/client-core/src/playback-v1/types';

/** Where one frame of the sprite sheet sits, with the sheet kept at its native size.
 * The sheet image is laid out unscaled (so every tile of the strip asks for the same decode,
 * which Android decodes once and shares), offset so the wanted frame sits at the window's
 * corner; the window, one source frame in size, is then scaled to the display box on the
 * GPU. Nothing upscales the whole sheet in memory (P2-71-PERF). */
export function nativeTrickplayWindow(trickplay: PlaybackOptions['trickplay'], seconds: number, box: Readonly<{width: number; height: number}>) {
  if (!trickplay || ![trickplay.intervalMs, trickplay.tileWidth, trickplay.tileHeight, trickplay.columns, trickplay.rows, box.width, box.height]
    .every(value => Number.isFinite(value) && value > 0) || !Number.isFinite(seconds)) return undefined;
  const index = Math.max(0, Math.floor(seconds * 1000 / trickplay.intervalMs));
  const column = index % trickplay.columns, row = Math.floor(index / trickplay.columns) % trickplay.rows;
  return {
    window: {width: trickplay.tileWidth, height: trickplay.tileHeight},
    sheet: {width: trickplay.columns * trickplay.tileWidth, height: trickplay.rows * trickplay.tileHeight,
      left: -column * trickplay.tileWidth, top: -row * trickplay.tileHeight},
    scaleX: box.width / trickplay.tileWidth, scaleY: box.height / trickplay.tileHeight,
  };
}
