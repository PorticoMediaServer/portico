/**
 * Pure paint geometry for the Web music Now playing expansion (motion 2.1–2.5).
 *
 * This module owns no media, session, controller, subscription, queue, seek,
 * notice or clock. It takes the rectangles the owner actually measured — the dock
 * slot and the player cover — and returns paint geometry and progress for one
 * frame. When the owner has no admitted rectangle, it returns nothing and the
 * caller keeps its existing fallback: no source rectangle is invented here.
 *
 * Authored motion (`design/ui-v1/notes/motion.md`):
 * - 2.5 "Music bar to Now playing": cover on player; contents in three steps,
 *   60 ms apart, 180 each.
 * - Reduce motion: a 160 ms cross-fade, movement removed.
 */
export type PaintRect = Readonly<{left: number; top: number; right: number; bottom: number}>;

export type ExpansionFrame = Readonly<{
  /** Interpolated cover rectangle, only when both owner rectangles were admitted. */
  rect: PaintRect;
  /** 0..1, the same progress the owner passed. */
  progress: number;
  /**
   * The cover hands off from the dock artwork: it is already opaque as soon as the
   * owner's progress is past zero. It is never faded in proportion to progress.
   */
  coverOpacity: 0 | 1;
}>;

const CONTENT_STEPS = 3;
const STEP_GAP_MS = 60;
const STEP_DURATION_MS = 180;
const REDUCED_DURATION_MS = 160;

const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);

function admitted(rect: PaintRect | null | undefined): rect is PaintRect {
  if (!rect) return false;
  const {left, top, right, bottom} = rect;
  if (![left, top, right, bottom].every(finite)) return false;
  return right > left && bottom > top;
}

const interpolate = (from: number, to: number, progress: number) => from + (to - from) * progress;

/** One paint frame between the owner's own dock and player rectangles. */
export function expansionFrame(source: PaintRect | null | undefined, target: PaintRect | null | undefined,
  progress: number): ExpansionFrame | undefined {
  if (!admitted(source) || !admitted(target) || !finite(progress)) return undefined;
  const clamped = Math.min(1, Math.max(0, progress));
  return {
    rect: {
      left: interpolate(source.left, target.left, clamped),
      top: interpolate(source.top, target.top, clamped),
      right: interpolate(source.right, target.right, clamped),
      bottom: interpolate(source.bottom, target.bottom, clamped),
    },
    progress: clamped,
    coverOpacity: clamped > 0.001 ? 1 : 0,
  };
}

/** The three authored content steps: index 0..2, 60 ms apart, each 180 ms. */
export function contentStep(index: number, durationMs = STEP_DURATION_MS, gapMs = STEP_GAP_MS) {
  if (!Number.isInteger(index) || index < 0 || index >= CONTENT_STEPS) return undefined;
  if (!finite(durationMs) || durationMs <= 0 || !finite(gapMs) || gapMs < 0) return undefined;
  return Object.freeze({index, delayMs: index * gapMs, durationMs});
}

/** Reduce motion content cross-fade: opacity only, 160 ms, no geometry and no stagger. */
export function reducedFrame(progress: number): Readonly<{opacity: number; durationMs: number}> | undefined {
  if (!finite(progress)) return undefined;
  return {opacity: Math.min(1, Math.max(0, progress)), durationMs: REDUCED_DURATION_MS};
}
