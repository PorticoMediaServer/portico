import type {VideoFrame} from './video-gesture-geometry';

/** Private presentation port. It creates no media, source, playback state or command. */
export type VideoPointer = Readonly<{pointerId: number; clientX: number; clientY: number; timeStamp: number; pointerType: string; button: number}>;
export type VideoGesturePort = Readonly<{
  begin: (event: VideoPointer, root: HTMLElement, start: 0 | 1) => boolean;
  move: (event: VideoPointer) => boolean;
  release: (event: VideoPointer, cancelled?: boolean) => boolean;
  cancel: () => void;
}>;
export type VideoSurfacePaint = Readonly<{
  element: HTMLDivElement;
  /** Snapshot native paint, cancel only this surface's current geometry job. */
  freeze: () => VideoFrame;
  resume: () => void;
}>;
