/**
 * Styled subtitles (ASS, SSA) drawn by libass, compiled to WebAssembly and run in a worker
 * (`third_party/libass-wasm`). The subtitle keeps the look its author gave it: positions,
 * typefaces, colours, karaoke, signs.
 *
 * The engine's files are requested the first time a styled subtitle is shown, never before. The
 * script is read and decoded here (it may be in an old encoding libass would misread) and handed
 * over as text; fonts are handed over as addresses the worker fetches: the fonts attached to the
 * file, then fonts for any script in the text that the default font lacks.
 *
 * The worker keeps its own clock between messages. It is told the time when playback starts,
 * stops, seeks or changes speed, and otherwise only when it has drifted or half a second has gone
 * by, so a playing subtitle costs two small messages a second rather than one per frame.
 */
import {fallbackFontsFor} from './fallback-fonts.ts';
import {decodeSubtitleText, inputBytes, subtitleFileLimits, type DrawnSubtitleRenderer, type DrawnSubtitleSource, type OverlayClock, type OverlayGeometry, type RendererContext} from './types.ts';
import {libassFiles} from './vendor.ts';

/** The parts of the vendored wrapper (`subtitles-octopus.js`) this renderer uses. */
export type LibassInstance = {resize(width: number, height: number): void; setCurrentTime(seconds: number): void; setIsPaused(paused: boolean, seconds: number): void; setRate(rate: number): void; dispose(): void};
export type LibassOptions = {
  canvas: HTMLCanvasElement; subContent: string; fonts: string[]; fallbackFont: string; workerUrl: string;
  renderMode: 'wasm-blend'; targetFps: number; libassMemoryLimit: number; libassGlyphLimit: number;
  onReady: () => void; onError: (error: unknown) => void;
};
export type LibassConstructor = new (options: LibassOptions) => LibassInstance;

let engine: Promise<LibassConstructor> | undefined;
/** Fetches the engine's main-thread script once. A failed attempt can be made again. */
export function loadLibass(): Promise<LibassConstructor> {
  engine ??= new Promise<LibassConstructor>((resolve, reject) => {
    const scope = window as unknown as {SubtitlesOctopus?: LibassConstructor};
    if (scope.SubtitlesOctopus) { resolve(scope.SubtitlesOctopus); return; }
    const script = document.createElement('script');
    script.src = libassFiles.main();
    script.async = true;
    script.onload = () => { if (scope.SubtitlesOctopus) resolve(scope.SubtitlesOctopus); else reject(new Error('The subtitle renderer did not start.')); };
    script.onerror = () => { script.remove(); reject(new Error('The subtitle renderer could not be loaded.')); };
    document.head.append(script);
  });
  engine.catch(() => { engine = undefined; });
  return engine;
}

/** Whether this browser can run the engine at all. */
export function libassSupported(): boolean {
  try { return typeof WebAssembly === 'object' && typeof WebAssembly.instantiate === 'function' && typeof Worker === 'function'; } catch { return false; }
}

/** The tallest canvas libass is asked to fill; a larger picture is scaled up from it. */
export const styledMaxHeight = 1440;

export type StyledRendererOptions = {load?: () => Promise<LibassConstructor>; now?: () => number; fallbackFonts?: (text: string) => string[]; readyTimeoutMs?: number};

export class StyledSubtitleRenderer implements DrawnSubtitleRenderer {
  readonly area = 'picture' as const;
  private readonly options: StyledRendererOptions;
  private instance: LibassInstance | null = null;
  private canvas: HTMLCanvasElement | null = null;
  private size: {width: number; height: number} | null = null;
  private clock: OverlayClock | null = null;
  private told: {time: number; at: number; paused: boolean; rate: number} | null = null;
  private ready = false;
  private disposed = false;

  constructor(options: StyledRendererOptions = {}) { this.options = options; }

  async load(source: DrawnSubtitleSource, context: RendererContext): Promise<void> {
    if (source.kind !== 'ass') throw new Error('Not a styled subtitle.');
    this.canvas = context.canvas;
    const file = await inputBytes(source.script, context.read, subtitleFileLimits.script, context.signal);
    const text = decodeSubtitleText(file.bytes, source.charset || file.charset);
    if (!/^\s*\[events\]/im.test(text)) throw new Error('This subtitle has no lines to draw.');
    const Engine = await (this.options.load ?? loadLibass)();
    if (context.signal.aborted || this.disposed) throw new Error('Subtitle loading was cancelled.');
    const fonts = [...(source.fonts ?? []).slice(0, subtitleFileLimits.fonts), ...(this.options.fallbackFonts ?? (t => fallbackFontsFor(t, script => (script === 'cjk' ? source.cjkFont : undefined))))(text)];
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('The subtitle renderer took too long to start.')), this.options.readyTimeoutMs ?? 45000);
      context.signal.addEventListener('abort', () => reject(new Error('Subtitle loading was cancelled.')), {once: true});
      try {
        this.instance = new Engine({
          canvas: context.canvas, subContent: text, fonts, fallbackFont: libassFiles.defaultFont(), workerUrl: libassFiles.worker(),
          renderMode: 'wasm-blend', targetFps: 24, libassMemoryLimit: 40, libassGlyphLimit: 40,
          onReady: () => { clearTimeout(timer); resolve(); },
          // Before it is ready this fails the load; afterwards it tells the player the subtitle stopped.
          onError: error => { clearTimeout(timer); if (this.ready) { if (!this.disposed) context.failed(error); } else reject(error instanceof Error ? error : new Error('The subtitle renderer failed.')); },
        });
      } catch (error) { clearTimeout(timer); reject(error); }
    }).catch(error => { this.dispose(); throw error; });
    if (this.disposed) throw new Error('Subtitle loading was cancelled.');
    this.ready = true;
    if (this.size) this.instance?.resize(this.size.width, this.size.height);
    if (this.clock) this.sync(this.clock);
  }

  resize(geometry: OverlayGeometry): void {
    const scale = Math.min(1, styledMaxHeight / Math.max(1, geometry.height));
    const size = {width: Math.max(1, Math.round(geometry.width * scale)), height: Math.max(1, Math.round(geometry.height * scale))};
    if (this.size && this.size.width === size.width && this.size.height === size.height) return;
    this.size = size;
    if (this.ready) { this.instance?.resize(size.width, size.height); this.told = null; if (this.clock) this.sync(this.clock); }
  }

  sync(clock: OverlayClock): void {
    this.clock = clock;
    const instance = this.instance;
    if (!this.ready || !instance || this.disposed) return;
    const now = (this.options.now ?? (() => performance.now()))(), told = this.told;
    const say = () => { this.told = {time: clock.timeSeconds, at: now, paused: clock.paused, rate: clock.rate}; };
    if (!told || told.paused !== clock.paused) {
      if (!told || told.rate !== clock.rate) instance.setRate(clock.rate);
      instance.setIsPaused(clock.paused, clock.timeSeconds);
      say();
      return;
    }
    if (told.rate !== clock.rate) { instance.setRate(clock.rate); instance.setCurrentTime(clock.timeSeconds); say(); return; }
    const expected = told.paused ? told.time : told.time + ((now - told.at) / 1000) * told.rate;
    if (Math.abs(expected - clock.timeSeconds) > 0.04 || (!clock.paused && now - told.at >= 500)) { instance.setCurrentTime(clock.timeSeconds); say(); }
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    const instance = this.instance;
    this.instance = null;
    try { instance?.dispose(); } catch { /* the worker is already gone */ }
    const canvas = this.canvas;
    try { canvas?.getContext('2d')?.clearRect(0, 0, canvas.width, canvas.height); } catch { /* no canvas */ }
  }
}
