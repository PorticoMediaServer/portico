import {SubtitleOverlay, type OverlayVideo} from './overlay.ts';
import type {DrawnSubtitleSource, ReadSubtitleFile} from './types.ts';

/**
 * One drawn subtitle over the player's own video: a styled (ASS, SSA) or image (PGS, VobSub, DVB)
 * track on the overlay canvas this surface owns. Each kind's renderer is fetched the first time a
 * subtitle of that kind is shown.
 */
export type DrawnSubtitleSurface = Readonly<{
  present(source: DrawnSubtitleSource): Promise<void>;
  timeline(offsetSeconds: number | null): void;
  setDelay(seconds: number): void;
  setForcedOnly(on: boolean): void;
  /** The viewer's subtitle size, 1 as authored. */
  setScale(scale: number): void;
  /** The track really has some forced lines and some not, so the drawn switch changes something. */
  readonly forcedChoice: boolean;
  clear(): void;
  dispose(): void;
}>;

export function createDrawnSubtitleSurface(video: HTMLVideoElement, read: ReadSubtitleFile, failed: (reason: unknown) => void = () => {}): DrawnSubtitleSurface | undefined {
  if (!SubtitleOverlay.supported()) return undefined;
  const host = video.parentElement ?? video;
  if (!host) return undefined;
  const canvas = host.ownerDocument.createElement('canvas');
  canvas.setAttribute('data-drawn-subtitles', '');
  if (host.ownerDocument.defaultView?.getComputedStyle(host).position === 'static') host.style.position = 'relative';
  Object.assign(canvas.style, {position: 'absolute', inset: '0', width: '100%', height: '100%', display: 'none', pointerEvents: 'none', zIndex: '1'});
  host.append(canvas);
  // The default environment places the canvas by its real containing block and follows the
  // element's own fit/layout, which a fixed inset would shift on every relayout.
  const overlay = new SubtitleOverlay(video as unknown as OverlayVideo, canvas);
  let scaled = 1;
  return Object.freeze({
    present: (source: DrawnSubtitleSource) => overlay.present(source, read, failed),
    timeline: (offset: number | null) => overlay.timeline(offset),
    setDelay: (seconds: number) => overlay.setDelay(seconds),
    setForcedOnly: (on: boolean) => overlay.setForcedOnly(on),
    setScale: (scale: number) => { if (scale !== scaled) { scaled = scale; overlay.adjust({scale, raise: 0}); } },
    get forcedChoice() { return overlay.forcedChoice; },
    clear: () => overlay.clear(),
    dispose: () => { overlay.dispose(); canvas.remove(); },
  });
}
