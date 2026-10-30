/** What the page and the image-subtitle worker say to each other. */
export type ImageWorkerRequest =
  | {type: 'load'; kind: 'pgs' | 'vobsub' | 'dvb'; data: ArrayBuffer; index?: string}
  | {type: 'paint'; index: number};
export type FrameRect = {x: number; y: number; width: number; height: number; bitmap?: ImageBitmap; rgba?: ArrayBuffer};
export type ImageWorkerReply =
  | {type: 'ready'; width: number; height: number; starts: Float64Array; ends: Float64Array; forced: Uint8Array; skipped: number}
  | {type: 'failed'; code: 'no_pictures' | 'unreadable'}
  | {type: 'frame'; index: number; rects: FrameRect[]};
