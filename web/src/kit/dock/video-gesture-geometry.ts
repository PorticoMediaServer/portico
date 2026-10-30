import {GestureRelease} from './motion';

export type VideoFrame = Readonly<{left: number; top: number; width: number; height: number}>;
export type VideoGestureGeometry = Readonly<{dock: VideoFrame; full: VideoFrame; travel: number}>;

function validFrame(frame: VideoFrame | null | undefined): frame is VideoFrame {
  return !!frame && Number.isFinite(frame.left) && Number.isFinite(frame.top)
    && Number.isFinite(frame.width) && frame.width > 0 && Number.isFinite(frame.height) && frame.height > 0;
}

/** Capture real measured slots; downward motion must have a finite, positive travel. */
export function videoGestureGeometry(dock: VideoFrame, full: VideoFrame): VideoGestureGeometry | undefined {
  if (!validFrame(dock) || !validFrame(full)) return undefined;
  const travel = dock.top - full.top;
  if (!Number.isFinite(travel) || travel <= 0) return undefined;
  return {dock: {left: dock.left, top: dock.top, width: dock.width, height: dock.height},
    full: {left: full.left, top: full.top, width: full.width, height: full.height}, travel};
}

function validGeometry(geometry: VideoGestureGeometry | null | undefined): geometry is VideoGestureGeometry {
  return !!geometry && validFrame(geometry.dock) && validFrame(geometry.full)
    && Number.isFinite(geometry.travel) && geometry.travel > 0 && geometry.travel === geometry.dock.top - geometry.full.top;
}

/** Canonical position 0 is the full slot, and 1 is the dock slot. */
export function videoGestureFrame(geometry: VideoGestureGeometry | undefined, position: number): VideoFrame | undefined {
  if (!validGeometry(geometry) || !Number.isFinite(position)) return undefined;
  const p = Math.max(0, Math.min(1, position));
  const interpolate = (a: number, b: number) => a * (1 - p) + b * p;
  const frame = {left: interpolate(geometry.full.left, geometry.dock.left), top: interpolate(geometry.full.top, geometry.dock.top),
    width: interpolate(geometry.full.width, geometry.dock.width), height: interpolate(geometry.full.height, geometry.dock.height)};
  return validFrame(frame) ? frame : undefined;
}

export function videoGesturePosition(start: 0 | 1, dy: number, geometry: VideoGestureGeometry | undefined): number | undefined {
  if (!validGeometry(geometry) || (start !== 0 && start !== 1) || !Number.isFinite(dy)) return undefined;
  return Math.max(0, Math.min(1, start + dy / geometry.travel));
}

/** Delegate the decision to the existing player rule, expressed from the starting end. */
export function videoGestureRelease(start: 0 | 1, position: number, velocityPixels: number, geometry: VideoGestureGeometry | undefined): Readonly<{target: 0 | 1; velocity: number}> | undefined {
  if (!validGeometry(geometry) || (start !== 0 && start !== 1) || !Number.isFinite(position) || !Number.isFinite(velocityPixels)) return undefined;
  const velocity = velocityPixels / geometry.travel;
  if (!Number.isFinite(velocity)) return undefined;
  const p = Math.max(0, Math.min(1, position));
  const decision = GestureRelease.player(start === 0 ? p : 1 - p, start === 0 ? velocityPixels : -velocityPixels, geometry.travel);
  return {target: start === 0 ? decision.target : decision.target === 1 ? 0 : 1, velocity};
}
