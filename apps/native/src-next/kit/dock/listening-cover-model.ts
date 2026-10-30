import type {DockRect} from './types';

/** FX-064 cover paint: transform-only placement of one persistent cover between a
 * measured full frame and a measured mini frame.
 *
 * Everything here comes from the caller's own measurements. A missing or
 * non-finite rectangle returns undefined instead of guessing a dock or window
 * coordinate, and progress is clamped to its two endpoints the way the existing
 * player bounds are. Nothing in this file owns a controller, a fence or a
 * playback fact. */

export type ListeningCoverTransform = Readonly<{translateX: number; translateY: number; scaleX: number; scaleY: number}>;

const usable = (rect: DockRect | undefined): rect is DockRect => {
  'worklet';
  return !!rect
    && Number.isFinite(rect.x) && Number.isFinite(rect.y) && Number.isFinite(rect.width) && Number.isFinite(rect.height)
    && rect.width > 0 && rect.height > 0;
};

const centreX = (rect: DockRect): number => {
  'worklet';
  return rect.x + rect.width / 2;
};
const centreY = (rect: DockRect): number => {
  'worklet';
  return rect.y + rect.height / 2;
};

/** Both rectangles are real measured frames; callers draw nothing otherwise. */
export function listeningCoverDrawable(full: DockRect | undefined, mini: DockRect | undefined): boolean {
  'worklet';
  return usable(full) && usable(mini);
}

/** Clamp to the drawn endpoints the way the existing player bounds do: NaN sits at
 * the full end, and the infinite values clamp to the nearest endpoint. */
export function listeningCoverProgress(progress: number): number {
  'worklet';
  if (progress !== progress) return 0;
  return progress < 0 ? 0 : progress > 1 ? 1 : progress;
}

/** The transform for a fixed-size box drawn at the full frame: translate first so
 * the movement stays in screen pixels, then scale about the box centre. */
export function listeningCoverTransform(full: DockRect | undefined, mini: DockRect | undefined, progress: number): ListeningCoverTransform | undefined {
  'worklet';
  if (!usable(full) || !usable(mini)) return undefined;
  const t = listeningCoverProgress(progress);
  const width = full.width + (mini.width - full.width) * t;
  const height = full.height + (mini.height - full.height) * t;
  const x = centreX(full) + (centreX(mini) - centreX(full)) * t;
  const y = centreY(full) + (centreY(mini) - centreY(full)) * t;
  return {
    translateX: x - centreX(full),
    translateY: y - centreY(full),
    scaleX: width / full.width,
    scaleY: height / full.height,
  };
}
