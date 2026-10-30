/**
 * Image subtitles (PGS, VobSub, DVB) on the overlay canvas. A worker reads the file and paints
 * each cue's pictures; this side only decides which cue is due and draws its pictures, scaled
 * from the subtitle's own canvas onto the picture.
 *
 * A subtitle authored for the full frame of a film that has since been cropped (a 1920×1080
 * subtitle on a 1920×800 picture) keeps its scale and is centred, so lines that sat in the black
 * bars still sit there: the canvas covers the whole player for this reason.
 */
import {defaultI18n} from '@i18n';
import type {FrameRect, ImageWorkerReply, ImageWorkerRequest} from './image-protocol.ts';
import {inputBytes, decodeSubtitleText, subtitleFileLimits, type DrawnSubtitleRenderer, type DrawnSubtitleSource, type OverlayClock, type OverlayGeometry, type RendererContext} from './types.ts';

export type ImageWorkerLike = {postMessage(message: ImageWorkerRequest, transfer?: Transferable[]): void; terminate(): void; onmessage: ((event: {data: ImageWorkerReply}) => void) | null; onerror: ((event: unknown) => void) | null};
type Drawable = {x: number; y: number; width: number; height: number; image: CanvasImageSource; close?: () => void};
type Context2D = Pick<CanvasRenderingContext2D, 'clearRect' | 'drawImage'> & {imageSmoothingEnabled: boolean; imageSmoothingQuality?: string};

export type ImageRendererOptions = {
  spawn?: () => ImageWorkerLike;
  /** Turns raw pixels into something a canvas can draw (browsers whose workers cannot make bitmaps). */
  fromPixels?: (rgba: ArrayBuffer, width: number, height: number) => CanvasImageSource;
  readyTimeoutMs?: number;
};

const spawnWorker = (): ImageWorkerLike => new Worker(new URL('./image-worker.ts', import.meta.url), {type: 'module'}) as unknown as ImageWorkerLike;
function pixelsToCanvas(rgba: ArrayBuffer, width: number, height: number): CanvasImageSource {
  const canvas = document.createElement('canvas');
  canvas.width = width; canvas.height = height;
  canvas.getContext('2d')?.putImageData(new ImageData(new Uint8ClampedArray(rgba), width, height), 0, 0);
  return canvas;
}

/** Index of the cue on screen at `timeMs` in parallel start and end lists, or -1. */
export function cueIndexAt(starts: ArrayLike<number>, ends: ArrayLike<number>, timeMs: number): number {
  let low = 0, high = starts.length - 1;
  while (low <= high) {
    const middle = (low + high) >> 1;
    if (timeMs < starts[middle]!) high = middle - 1;
    else if (timeMs >= ends[middle]!) low = middle + 1;
    else return middle;
  }
  return -1;
}

export class ImageSubtitleRenderer implements DrawnSubtitleRenderer {
  readonly area = 'frame' as const;
  private readonly options: ImageRendererOptions;
  private worker: ImageWorkerLike | null = null;
  private canvas: HTMLCanvasElement | null = null;
  private context: Context2D | null = null;
  private geometry: OverlayGeometry | null = null;
  private track: {width: number; height: number; starts: Float64Array; ends: Float64Array; forced: Uint8Array} | null = null;
  private forcedOnly = false;
  private current = -1;
  private drawn = -2;
  private readonly frames = new Map<number, Drawable[]>();
  private readonly asked = new Set<number>();
  private disposed = false;

  constructor(options: ImageRendererOptions = {}) { this.options = options; }

  async load(source: DrawnSubtitleSource, context: RendererContext): Promise<void> {
    if (source.kind === 'ass') throw new Error('Not an image subtitle.');
    this.canvas = context.canvas;
    this.context = context.canvas.getContext('2d') as Context2D | null;
    if (!this.context) throw new Error('This browser cannot draw subtitles.');
    const file = await inputBytes(source.data, context.read, subtitleFileLimits.image, context.signal);
    let index: string | undefined;
    if (source.kind === 'vobsub') { const read = await inputBytes(source.index, context.read, subtitleFileLimits.index, context.signal); index = decodeSubtitleText(read.bytes, read.charset); }
    if (context.signal.aborted || this.disposed) throw new Error('Subtitle loading was cancelled.');
    // The worker is given its own copy unless these bytes were read just for it.
    const whole = file.bytes.byteOffset === 0 && file.bytes.byteLength === file.bytes.buffer.byteLength && typeof source.data === 'string';
    const data = (whole ? file.bytes.buffer : file.bytes.slice().buffer) as ArrayBuffer;
    const worker = this.worker = (this.options.spawn ?? spawnWorker)();
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('The subtitle took too long to read.')), this.options.readyTimeoutMs ?? 60000);
      context.signal.addEventListener('abort', () => reject(new Error('Subtitle loading was cancelled.')), {once: true});
      worker.onerror = () => { clearTimeout(timer); if (this.track) { if (!this.disposed) context.failed(new Error('The subtitle reader stopped.')); } else reject(new Error('The subtitle reader could not start.')); };
      worker.onmessage = event => {
        const reply = event.data;
        if (reply.type === 'ready') { clearTimeout(timer); this.track = {width: reply.width, height: reply.height, starts: reply.starts, ends: reply.ends, forced: reply.forced ?? new Uint8Array(reply.starts.length)}; resolve(); }
        else if (reply.type === 'failed') { clearTimeout(timer); const text = reply.code === 'no_pictures' ? defaultI18n.t('next.subtitles.image.noPictures') : defaultI18n.t('next.subtitles.image.unreadable'); if (this.track) { if (!this.disposed) context.failed(new Error(text)); } else reject(new Error(text)); }
        else this.arrived(reply.index, reply.rects);
      };
      worker.postMessage({type: 'load', kind: source.kind, data, ...(index === undefined ? {} : {index})}, [data]);
    }).catch(error => { this.dispose(); throw error; });
  }

  resize(geometry: OverlayGeometry): void {
    this.geometry = geometry;
    const canvas = this.canvas;
    if (!canvas) return;
    if (canvas.width !== geometry.width) canvas.width = geometry.width;
    if (canvas.height !== geometry.height) canvas.height = geometry.height;
    this.drawn = -2;
    this.draw();
  }

  /** Cues the subtitle marks forced, and all its cues; known once it has loaded. */
  get cues(): {forced: number; total: number} {
    const track = this.track;
    if (!track) return {forced: 0, total: 0};
    let forced = 0;
    for (const flag of track.forced) forced += flag;
    return {forced, total: track.forced.length};
  }

  /** Shows only the cues marked forced (the viewer's "Forced only"), or every cue. */
  setForcedOnly(on: boolean): void {
    if (this.forcedOnly === on) return;
    this.forcedOnly = on;
    this.drawn = -2;
  }

  sync(clock: OverlayClock): void {
    const track = this.track;
    if (!track || this.disposed) return;
    const shown = (index: number) => index >= 0 && (!this.forcedOnly || track.forced[index] === 1);
    const at = cueIndexAt(track.starts, track.ends, clock.timeSeconds * 1000);
    this.current = shown(at) ? at : -1;
    if (this.current >= 0) this.ask(this.current);
    // The next cue is painted ahead so it is there when its time comes.
    const next = at >= 0 ? at + 1 : cueIndexAt(track.starts, track.ends, clock.timeSeconds * 1000 + 1500);
    if (!clock.paused && next >= 0 && next < track.starts.length && shown(next)) this.ask(next);
    this.draw();
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    try { this.worker?.terminate(); } catch { /* already gone */ }
    this.worker = null;
    for (const frame of this.frames.values()) for (const rect of frame) rect.close?.();
    this.frames.clear();
    const canvas = this.canvas;
    try { if (canvas) this.context?.clearRect(0, 0, canvas.width, canvas.height); } catch { /* no canvas */ }
  }

  private ask(index: number) {
    if (this.frames.has(index) || this.asked.has(index) || this.asked.size >= 2) return;
    this.asked.add(index);
    this.worker?.postMessage({type: 'paint', index});
  }

  private arrived(index: number, rects: FrameRect[]) {
    this.asked.delete(index);
    if (this.disposed) { for (const rect of rects) rect.bitmap?.close(); return; }
    const make = this.options.fromPixels ?? pixelsToCanvas;
    this.frames.set(index, rects.map(rect => rect.bitmap
      ? {x: rect.x, y: rect.y, width: rect.width, height: rect.height, image: rect.bitmap, close: () => rect.bitmap!.close()}
      : {x: rect.x, y: rect.y, width: rect.width, height: rect.height, image: make(rect.rgba ?? new ArrayBuffer(rect.width * rect.height * 4), rect.width, rect.height)}));
    // Keep what is on screen and what comes next; the rest can be painted again if it is wanted.
    for (const [kept, frame] of this.frames) if (kept !== index && kept !== this.current && kept !== this.current + 1) { for (const rect of frame) rect.close?.(); this.frames.delete(kept); }
    if (index === this.current) { this.drawn = -2; this.draw(); }
  }

  private draw() {
    const context = this.context, canvas = this.canvas, geometry = this.geometry, track = this.track;
    if (!context || !canvas || !geometry || !track) return;
    const frame = this.current >= 0 ? this.frames.get(this.current) : undefined;
    // A cue whose pictures have not arrived yet shows nothing rather than the cue before it.
    const showing = frame ? this.current : -1;
    if (showing === this.drawn) return;
    this.drawn = showing;
    context.clearRect(0, 0, canvas.width, canvas.height);
    if (!frame) return;
    const scale = geometry.picture.width / track.width;
    const left = geometry.picture.x, top = geometry.picture.y + (geometry.picture.height - track.height * scale) / 2;
    context.imageSmoothingEnabled = true;
    context.imageSmoothingQuality = 'high';
    for (const rect of frame) context.drawImage(rect.image, left + rect.x * scale, top + rect.y * scale, rect.width * scale, rect.height * scale);
  }
}
