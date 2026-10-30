/**
 * Subtitles the player draws itself, on a canvas over the picture, instead of having the server
 * burn them into the video: styled subtitles (ASS, SSA) through libass, and image subtitles
 * (Blu-ray PGS, DVD VobSub, DVB) through Portico's own decoders.
 *
 * A renderer is given a subtitle as bytes or as an address to read them from, a canvas, and the
 * playback clock. Nothing else in the player knows which kind is on screen.
 */

/** A file: its bytes, or where to read them. */
export type SubtitleInput = Uint8Array | string;

export type DrawnSubtitleKind = 'ass' | 'pgs' | 'vobsub' | 'dvb';
export type DrawnSubtitleSource =
  /** `charset` names the script's encoding when it is not UTF-8; `fonts` are the fonts attached to the file. */
  | Readonly<{kind: 'ass'; script: SubtitleInput; charset?: string; fonts?: readonly string[]; /** The server's CJK font, for a line in Chinese, Japanese or Korean the attached fonts lack. */ cjkFont?: string}>
  | Readonly<{kind: 'pgs'; data: SubtitleInput}>
  | Readonly<{kind: 'vobsub'; index: SubtitleInput; data: SubtitleInput}>
  | Readonly<{kind: 'dvb'; data: SubtitleInput}>;

/** A bounded read of one file. Rejects with `{status}` when the server refuses. */
export type ReadSubtitleFile = (url: string, maxBytes: number, signal: AbortSignal) => Promise<Readonly<{bytes: Uint8Array; charset: string}>>;

/** The subtitle's own time (title time, seconds), with the viewer's timing correction applied. */
export type OverlayClock = Readonly<{timeSeconds: number; paused: boolean; rate: number}>;

/**
 * The canvas in device pixels and where the picture sits in it. A renderer that draws for the
 * picture alone (`area: 'picture'`) gets a canvas that is exactly the picture.
 */
export type OverlayGeometry = Readonly<{width: number; height: number; picture: Readonly<{x: number; y: number; width: number; height: number}>}>;

/** `failed` is for a renderer that stops working after it loaded (its worker died). */
export type RendererContext = Readonly<{canvas: HTMLCanvasElement; read: ReadSubtitleFile; signal: AbortSignal; failed: (reason: unknown) => void}>;

export interface DrawnSubtitleRenderer {
  /** `picture`: the canvas covers the picture only. `frame`: it covers the whole player, bars included. */
  readonly area: 'picture' | 'frame';
  /** Reads the subtitle and gets ready to draw. Rejects when this subtitle cannot be drawn here. */
  load(source: DrawnSubtitleSource, context: RendererContext): Promise<void>;
  resize(geometry: OverlayGeometry): void;
  /** Called on every frame while playing, and whenever playback pauses, seeks or changes speed. */
  sync(clock: OverlayClock): void;
  /** Stops and releases everything; the canvas is left clear. */
  dispose(): void;
  /** Image subtitles: how many cues are marked forced, of how many, and showing only those. */
  readonly cues?: {forced: number; total: number};
  setForcedOnly?(on: boolean): void;
}

export const subtitleFileLimits = Object.freeze({script: 16 * 1024 * 1024, image: 96 * 1024 * 1024, index: 8 * 1024 * 1024, fonts: 32});

/** Bytes of an input, read through `read` when it is an address. */
export async function inputBytes(input: SubtitleInput, read: ReadSubtitleFile, maxBytes: number, signal: AbortSignal): Promise<{bytes: Uint8Array; charset: string}> {
  if (typeof input !== 'string') {
    if (input.byteLength > maxBytes) throw new Error('Subtitle file is too large.');
    return {bytes: input, charset: ''};
  }
  return read(input, maxBytes, signal);
}

/** Text of a subtitle file in the encoding the server named, falling back to UTF-8; a byte-order mark is dropped. */
export function decodeSubtitleText(bytes: Uint8Array, charset: string): string {
  let label = charset.trim().toLowerCase();
  if (bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) label = 'utf-8';
  else if (bytes[0] === 0xff && bytes[1] === 0xfe) label = 'utf-16le';
  else if (bytes[0] === 0xfe && bytes[1] === 0xff) label = 'utf-16be';
  let decoder: TextDecoder;
  try { decoder = new TextDecoder(label || 'utf-8'); } catch { decoder = new TextDecoder('utf-8'); }
  return decoder.decode(bytes);
}
