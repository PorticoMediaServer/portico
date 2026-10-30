/**
 * Image subtitles, decoded: what PGS, VobSub and DVB all come down to.
 *
 * A track is a list of cues on the subtitle's own canvas (the video size the subtitle was
 * authored for). A cue's pictures are decoded only when it is about to be shown, so a film's
 * worth of subtitles costs its compressed size in memory, not its painted size.
 *
 * These decoders read files a stranger may have made. Every count, size and offset is checked
 * against the bounds below; a part that breaks them is skipped and the rest is kept.
 */

/** One picture, placed on the subtitle's canvas. `rgba` is straight (not premultiplied) alpha. */
export type BitmapRect = Readonly<{x: number; y: number; width: number; height: number; rgba: Uint8ClampedArray; forced: boolean}>;
/** What is on screen from `startMs` until `endMs` (title time). */
export type BitmapCue = Readonly<{startMs: number; endMs: number; forced: boolean; paint(): BitmapRect[]}>;
export type BitmapTrack = Readonly<{kind: 'pgs' | 'vobsub' | 'dvb'; width: number; height: number; cues: readonly BitmapCue[]; skipped: number}>;

export const bitmapLimits = Object.freeze({
  /** The largest file a decoder is handed (the server's own bound is smaller). */
  maxBytes: 96 * 1024 * 1024,
  maxCues: 60_000,
  /** The largest canvas and the largest single picture. */
  maxWidth: 8192,
  maxHeight: 4320,
  maxPicturePixels: 4096 * 2304,
  /** Pictures on screen at once. */
  maxPictures: 64,
  /** A cue nothing ever ends is taken off after this long. */
  orphanMs: 10_000,
});

export class SubtitleDataError extends Error {
  constructor(message: string) { super(message); this.name = 'SubtitleDataError'; }
}

export function checkedSize(width: number, height: number): boolean {
  return Number.isInteger(width) && Number.isInteger(height) && width > 0 && height > 0 && width <= bitmapLimits.maxWidth && height <= bitmapLimits.maxHeight && width * height <= bitmapLimits.maxPicturePixels;
}

/** Limited-range Y'CbCr to RGB, BT.709 for high-definition canvases and BT.601 below. */
export function yuvToRgb(y: number, cb: number, cr: number, hd: boolean): [number, number, number] {
  const l = 1.164383 * (y - 16), u = cb - 128, v = cr - 128;
  const r = hd ? l + 1.792741 * v : l + 1.596027 * v;
  const g = hd ? l - 0.213249 * u - 0.532909 * v : l - 0.391762 * u - 0.812968 * v;
  const b = hd ? l + 2.112402 * u : l + 2.017232 * u;
  const clamp = (n: number) => n < 0 ? 0 : n > 255 ? 255 : Math.round(n);
  return [clamp(r), clamp(g), clamp(b)];
}

/** Ends each cue where the next begins when nothing ended it, bounds the list and puts it in order. */
export function finishCues<T extends {startMs: number; endMs: number}>(cues: T[]): T[] {
  const ordered = cues.filter(c => Number.isFinite(c.startMs) && c.startMs >= 0).sort((a, b) => a.startMs - b.startMs).slice(0, bitmapLimits.maxCues);
  for (let i = 0; i < ordered.length; i++) {
    const cue = ordered[i]!, next = ordered[i + 1];
    let end = cue.endMs;
    if (!(end > cue.startMs)) end = next ? next.startMs : cue.startMs + bitmapLimits.orphanMs;
    if (next && end > next.startMs && next.startMs > cue.startMs) end = next.startMs;
    (cue as {endMs: number}).endMs = end;
  }
  return ordered.filter(c => c.endMs > c.startMs);
}

/** The index of the cue on screen at `timeMs`, or -1. Cues are in order and do not overlap. */
export function cueAt(cues: readonly Readonly<{startMs: number; endMs: number}>[], timeMs: number): number {
  let low = 0, high = cues.length - 1;
  while (low <= high) {
    const middle = (low + high) >> 1, cue = cues[middle]!;
    if (timeMs < cue.startMs) high = middle - 1;
    else if (timeMs >= cue.endMs) low = middle + 1;
    else return middle;
  }
  return -1;
}

/** Combined limits apply even when each individual picture is valid. Tests may only lower them. */
const combinedLimits = Object.freeze({maxStatePixels: 16 * 1024 * 1024, maxFramePixels: 16 * 1024 * 1024, maxRetainedBytes: 96 * 1024 * 1024, maxWorkPixels: 512 * 1024 * 1024});
export type BitmapDecodeLimits = Partial<typeof combinedLimits>;
export function bitmapDecodeLimits(lower: BitmapDecodeLimits = {}): typeof combinedLimits {
  const out = {...combinedLimits};
  for (const key of Object.keys(out) as (keyof typeof out)[]) {
    const value = lower[key];
    if (value !== undefined && Number.isSafeInteger(value) && value >= 0) out[key] = Math.min(out[key], value);
  }
  return out;
}
