/**
 * The canvas over the video that drawn subtitles (styled and image) appear on.
 *
 * It keeps three things true whatever the renderer:
 * - **Where**: the canvas sits exactly on the picture (or on the whole player, for a renderer
 *   that may draw in the black bars), at the screen's pixel density, and follows the window, full
 *   screen, the fit mode and a change of the video's own size.
 * - **When**: the renderer is told the subtitle's time on every video frame while playing and
 *   whenever playback pauses, waits, seeks or changes speed. Subtitle time is the element's time
 *   less where the stream sits on the element's clock (told by the media player port, as for text
 *   subtitles) and less the viewer's timing correction. Until the port has said where the stream
 *   sits, nothing is shown.
 * - **Whether**: one renderer at a time. Presenting another subtitle, or none, releases the one
 *   before, including its worker.
 */
import type {DrawnSubtitleRenderer, DrawnSubtitleSource, OverlayGeometry, ReadSubtitleFile} from './types.ts';

type Box = {x: number; y: number; width: number; height: number};

/** Where a video of `video` size is shown inside an element of `element` size (CSS pixels). */
export function pictureBox(element: {width: number; height: number}, video: {width: number; height: number}, fit: 'contain' | 'cover'): Box {
  if (!(element.width > 0) || !(element.height > 0) || !(video.width > 0) || !(video.height > 0)) return {x: 0, y: 0, width: 0, height: 0};
  const across = element.width / video.width, down = element.height / video.height;
  const scale = fit === 'cover' ? Math.max(across, down) : Math.min(across, down);
  const width = video.width * scale, height = video.height * scale;
  return {x: (element.width - width) / 2, y: (element.height - height) / 2, width, height};
}

/** The most pixels a subtitle canvas is given: a 4K frame. */
const maxPixels = 3840 * 2160;

/** The canvas's place and pixel size for a renderer, from the element's box and the picture inside it. */
export function overlayGeometry(element: {width: number; height: number}, picture: Box, area: 'picture' | 'frame', density: number): {css: Box; geometry: OverlayGeometry} {
  const css = area === 'picture' ? picture : {x: 0, y: 0, width: element.width, height: element.height};
  let ratio = density > 0 ? density : 1;
  if (css.width * css.height * ratio * ratio > maxPixels) ratio = Math.sqrt(maxPixels / (css.width * css.height));
  const px = (n: number) => Math.round(n * ratio);
  return {css, geometry: {width: Math.max(1, px(css.width)), height: Math.max(1, px(css.height)), picture: {x: px(picture.x - css.x), y: px(picture.y - css.y), width: Math.max(1, px(picture.width)), height: Math.max(1, px(picture.height))}}};
}

/** The parts of a video element the overlay reads. */
export type OverlayVideo = {
  readonly currentTime: number; readonly paused: boolean; readonly seeking: boolean; readonly readyState: number; readonly playbackRate: number;
  readonly videoWidth: number; readonly videoHeight: number;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
  getBoundingClientRect(): {left: number; top: number; width: number; height: number};
  requestVideoFrameCallback?(callback: () => void): number;
  cancelVideoFrameCallback?(handle: number): void;
};
export type OverlayCanvas = HTMLCanvasElement;
export type OverlayEnvironment = {
  /** Calls `changed` whenever the picture may have moved or changed size; returns how to stop. */
  watchLayout(video: OverlayVideo, changed: () => void): () => void;
  fit(video: OverlayVideo): 'contain' | 'cover';
  density(): number;
  /** The corner of the box the canvas is positioned in. */
  origin(canvas: OverlayCanvas): {left: number; top: number};
  frame(callback: () => void): number;
  cancelFrame(handle: number): void;
};
export type RendererFactory = (kind: DrawnSubtitleSource['kind']) => Promise<DrawnSubtitleRenderer>;

/* The renderers are separate chunks: neither is fetched until a subtitle of its kind is chosen. */
const loadRenderer: RendererFactory = async kind => kind === 'ass' ? new (await import('./ass-renderer.ts')).StyledSubtitleRenderer() : new (await import('./image-renderer.ts')).ImageSubtitleRenderer();

function browserEnvironment(): OverlayEnvironment {
  return {
    watchLayout(video, changed) {
      const element = video as unknown as HTMLVideoElement, stops: (() => void)[] = [];
      if (typeof ResizeObserver === 'function') { const observer = new ResizeObserver(changed); observer.observe(element); stops.push(() => observer.disconnect()); }
      // The fit mode is a class on the element; it changes the picture without changing the box.
      if (typeof MutationObserver === 'function') { const observer = new MutationObserver(changed); observer.observe(element, {attributes: true, attributeFilter: ['class', 'style']}); stops.push(() => observer.disconnect()); }
      const later = () => { changed(); setTimeout(changed, 150); };
      for (const type of ['resize', 'orientationchange']) { window.addEventListener(type, later); stops.push(() => window.removeEventListener(type, later)); }
      for (const type of ['fullscreenchange', 'webkitfullscreenchange']) { document.addEventListener(type, later); stops.push(() => document.removeEventListener(type, later)); }
      // A window moved to a screen of another density.
      let density: MediaQueryList | undefined;
      const rewatch = () => { density?.removeEventListener('change', rewatch); try { density = matchMedia(`(resolution: ${devicePixelRatio}dppx)`); density.addEventListener('change', rewatch); } catch { density = undefined; } changed(); };
      rewatch();
      stops.push(() => density?.removeEventListener('change', rewatch));
      return () => { for (const stop of stops) stop(); };
    },
    fit: video => { try { return getComputedStyle(video as unknown as Element).objectFit === 'cover' ? 'cover' : 'contain'; } catch { return 'contain'; } },
    density: () => (typeof devicePixelRatio === 'number' && devicePixelRatio > 0 ? devicePixelRatio : 1),
    origin: canvas => { const parent = (canvas.offsetParent ?? canvas.parentElement)?.getBoundingClientRect(); return {left: parent?.left ?? 0, top: parent?.top ?? 0}; },
    frame: callback => requestAnimationFrame(callback),
    cancelFrame: handle => cancelAnimationFrame(handle),
  };
}

const clockEvents = ['timeupdate', 'seeking', 'seeked', 'play', 'playing', 'pause', 'waiting', 'ratechange', 'emptied', 'loadeddata'] as const;
const layoutEvents = ['loadedmetadata', 'resize'] as const;

export type OverlayAdjustment = Readonly<{scale: number; raise: number}>;

export class SubtitleOverlay {
  private readonly video: OverlayVideo;
  private readonly canvas: OverlayCanvas;
  private readonly environment: OverlayEnvironment;
  private readonly make: RendererFactory;
  private renderer: DrawnSubtitleRenderer | null = null;
  private loading: AbortController | null = null;
  private offset: number | null = 0;
  private delay = 0;
  private adjustment: OverlayAdjustment = {scale: 1, raise: 0};
  private forcedOnly = false;
  private geometry: OverlayGeometry | null = null;
  private stopLayout: (() => void) | null = null;
  private loop: {video?: number; frame?: number} = {};
  private disposed = false;
  private readonly tick = () => this.sync();
  private readonly moved = () => this.layout();

  constructor(video: OverlayVideo, canvas: OverlayCanvas, options: {environment?: OverlayEnvironment; renderers?: RendererFactory} = {}) {
    this.video = video;
    this.canvas = canvas;
    this.environment = options.environment ?? browserEnvironment();
    this.make = options.renderers ?? loadRenderer;
    canvas.style.display = 'none';
  }

  /** Whether this browser can put a canvas over the video at all. */
  static supported(): boolean {
    try { return typeof document !== 'undefined' && !!document.createElement('canvas').getContext && typeof Worker === 'function'; } catch { return false; }
  }

  /**
   * Shows this subtitle, replacing whatever was shown. Resolves once it is ready to draw; rejects
   * when it cannot be drawn here (the caller falls back to another delivery). `failed` is called
   * if it stops working later.
   */
  async present(source: DrawnSubtitleSource, read: ReadSubtitleFile, failed: (reason: unknown) => void = () => {}): Promise<void> {
    this.clear();
    if (this.disposed) throw new Error('The player has closed.');
    const loading = this.loading = new AbortController();
    let renderer: DrawnSubtitleRenderer | null = null;
    try {
      renderer = await this.make(source.kind);
      if (loading.signal.aborted) throw new Error('Subtitle loading was cancelled.');
      await renderer.load(source, {canvas: this.canvas, read, signal: loading.signal, failed: reason => { if (this.renderer === renderer) { this.clear(); failed(reason); } }});
      if (loading.signal.aborted) throw new Error('Subtitle loading was cancelled.');
    } catch (error) {
      renderer?.dispose();
      if (this.loading === loading) this.loading = null;
      throw error;
    }
    this.loading = null;
    this.renderer = renderer;
    renderer.setForcedOnly?.(this.forcedOnly);
    this.geometry = null;
    for (const type of clockEvents) this.video.addEventListener(type, this.tick);
    for (const type of layoutEvents) this.video.addEventListener(type, this.moved);
    this.stopLayout = this.environment.watchLayout(this.video, this.moved);
    this.layout();
    this.sync();
  }

  /** Takes the drawn subtitle off the picture and releases its renderer. */
  clear(): void {
    this.loading?.abort();
    this.loading = null;
    const renderer = this.renderer;
    this.renderer = null;
    if (renderer) {
      for (const type of clockEvents) this.video.removeEventListener(type, this.tick);
      for (const type of layoutEvents) this.video.removeEventListener(type, this.moved);
      this.stopLayout?.();
      this.stopLayout = null;
      renderer.dispose();
    }
    this.stopLoop();
    this.canvas.style.display = 'none';
  }

  /** Whether a drawn subtitle is on (or loading). */
  get active(): boolean { return this.renderer !== null || this.loading !== null; }

  /** The subtitle on screen marks some lines forced and not others, so "Forced only" means something. */
  get forcedChoice(): boolean {
    const cues = this.renderer?.cues;
    return !!cues && cues.forced > 0 && cues.forced < cues.total;
  }

  /** Shows only the lines marked forced, for every image subtitle from now on, or every line. */
  setForcedOnly(on: boolean): void {
    this.forcedOnly = on;
    this.renderer?.setForcedOnly?.(on);
    this.sync();
  }


  /** The viewer's timing correction, in seconds (positive shows the subtitle later). */
  setDelay(seconds: number): void { this.delay = Number.isFinite(seconds) ? seconds : 0; this.sync(); }

  /** The viewer's overall size (1 is as authored) and how far up the picture the subtitle is moved (a fraction of its height). */
  adjust(adjustment: OverlayAdjustment): void {
    this.adjustment = {scale: Math.min(2, Math.max(0.5, adjustment.scale || 1)), raise: Math.min(0.5, Math.max(-0.2, adjustment.raise || 0))};
    this.place();
  }

  // The media player port: where the stream sits on the element's clock.
  reset(): void { this.offset = null; this.sync(); }
  timeline(offsetSeconds: number | null): void { if (this.offset === offsetSeconds) return; this.offset = offsetSeconds; this.sync(); }

  dispose(): void {
    this.clear();
    this.disposed = true;
  }

  private box: {left: number; top: number; width: number; height: number} | null = null;

  private layout() {
    const renderer = this.renderer;
    if (!renderer) return;
    const element = this.video.getBoundingClientRect();
    const picture = pictureBox(element, {width: this.video.videoWidth, height: this.video.videoHeight}, this.environment.fit(this.video));
    if (!picture.width || !picture.height) { this.box = null; this.show(); return; }
    const {css, geometry} = overlayGeometry(element, picture, renderer.area, this.environment.density());
    const origin = this.environment.origin(this.canvas);
    this.box = {left: element.left - origin.left + css.x, top: element.top - origin.top + css.y, width: css.width, height: css.height};
    this.place();
    const before = this.geometry;
    if (!before || before.width !== geometry.width || before.height !== geometry.height || before.picture.x !== geometry.picture.x || before.picture.y !== geometry.picture.y || before.picture.width !== geometry.picture.width || before.picture.height !== geometry.picture.height) {
      this.geometry = geometry;
      renderer.resize(geometry);
    }
    this.show();
  }

  private place() {
    const box = this.box, style = this.canvas.style;
    if (!box) return;
    style.left = `${box.left}px`; style.top = `${box.top}px`; style.width = `${box.width}px`; style.height = `${box.height}px`;
    const {scale, raise} = this.adjustment;
    // Grown or shrunk about the bottom centre, where dialogue sits, then moved up as a whole.
    style.transformOrigin = '50% 100%';
    style.transform = scale === 1 && raise === 0 ? '' : `translateY(${(-raise * 100).toFixed(2)}%) scale(${scale})`;
  }

  private show() {
    const visible = !!this.renderer && !!this.box && this.offset !== null && Number.isFinite(this.offset);
    this.canvas.style.display = visible ? 'block' : 'none';
  }

  private sync() {
    const renderer = this.renderer;
    if (!renderer) return;
    if (!this.geometry) this.layout();
    this.show();
    const video = this.video;
    if (this.offset === null || !Number.isFinite(this.offset)) { this.stopLoop(); return; }
    // Waiting for media counts as paused: the picture is not moving, so neither should the subtitle.
    const paused = video.paused || video.seeking || video.readyState < 3;
    renderer.sync({timeSeconds: video.currentTime - this.offset - this.delay, paused, rate: video.playbackRate || 1});
    if (paused) this.stopLoop(); else this.startLoop();
  }

  private startLoop() {
    if (this.loop.video !== undefined || this.loop.frame !== undefined) return;
    const again = () => { this.loop = {}; this.sync(); };
    // The video's own frame callback where there is one, so a subtitle changes with the frame it belongs to.
    if (this.video.requestVideoFrameCallback) this.loop.video = this.video.requestVideoFrameCallback(again);
    else this.loop.frame = this.environment.frame(again);
  }

  private stopLoop() {
    if (this.loop.video !== undefined) this.video.cancelVideoFrameCallback?.(this.loop.video);
    if (this.loop.frame !== undefined) this.environment.cancelFrame(this.loop.frame);
    this.loop = {};
  }
}
