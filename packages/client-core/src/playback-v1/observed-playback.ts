/** Renderer measurements for one presentation. Source metadata, device capability
 * and requested settings never supply these values. Omission makes no assertion;
 * an empty snapshot explicitly clears this presentation's previous observations. */
export type ObservedDynamicRange = 'sdr' | 'hdr10' | 'hdr10plus' | 'hlg' | 'dolby_vision';
export type ObservedPlayback = Readonly<{
  video?: Readonly<{width?: number; height?: number; frameRateHz?: number; bitDepth?: 8 | 10 | 12 | 16; dynamicRange?: ObservedDynamicRange}>;
  screen?: Readonly<{dynamicRange?: ObservedDynamicRange; refreshRateHz?: number}>;
  audio?: Readonly<{
    handling?: 'passthrough' | 'decoded' | 'pcm' | 'mixed';
    codec?: 'aac' | 'ac3' | 'eac3' | 'truehd' | 'dts' | 'dts_hd' | 'flac' | 'alac' | 'pcm' | 'opus' | 'mp3' | 'mp2' | 'vorbis' | 'ac4';
    objectAudio?: 'atmos' | 'dtsx';
    channels?: number; layout?: 'mono' | 'stereo' | '2.0' | '5.1' | '5.1(side)' | '7.1' | '7.1(wide)' | '7.1(wide-side)';
    sampleRate?: number; bitDepth?: 16 | 20 | 24 | 32;
  }>;
  subtitles?: Readonly<{rendering: 'drawnByDevice' | 'externalText' | 'burnedIn' | 'off'}>;
  deliveredBitrateKbps?: number;
}>;

const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const ranges = ['sdr', 'hdr10', 'hdr10plus', 'hlg', 'dolby_vision'] as const;
function fail(): never { throw new Error('Invalid playback observation.'); }
/** Copy only bounded scalar output facts before they enter a timeline request.
 * No URL, token, file path, receiver or hardware model is carried from a bridge. */
export function parseObservedPlayback(value: unknown): ObservedPlayback | undefined {
  if (value === undefined) return undefined;
  if (!record(value)) fail();
  const out: Record<string, unknown> = {};
  const number = (from: Record<string, unknown>, into: Record<string, unknown>, key: string, max: number, integer = true) => {
    const n = from[key];
    if (n === undefined) return;
    if (typeof n !== 'number' || !Number.isFinite(n) || n < 0 || n > max || integer && !Number.isSafeInteger(n)) fail();
    if (n > 0) into[key] = n;
  };
  const choice = (from: Record<string, unknown>, into: Record<string, unknown>, key: string, allowed: readonly (string | number)[]) => {
    const v = from[key];
    if (v === undefined || v === '' || v === 0) return;
    if (typeof v !== 'string' && typeof v !== 'number' || !allowed.includes(v as string | number)) fail();
    into[key] = v;
  };
  for (const key of ['video', 'screen', 'audio', 'subtitles'] as const) {
    if (value[key] === undefined) continue;
    const block = value[key];
    if (!record(block)) fail();
    const fields: Record<string, unknown> = {};
    if (key === 'video') {
      number(block, fields, 'width', 32768); number(block, fields, 'height', 32768);
      number(block, fields, 'frameRateHz', 1000, false);
      choice(block, fields, 'bitDepth', [8, 10, 12, 16]); choice(block, fields, 'dynamicRange', ranges);
    } else if (key === 'screen') {
      number(block, fields, 'refreshRateHz', 1000, false); choice(block, fields, 'dynamicRange', ranges);
    } else if (key === 'audio') {
      choice(block, fields, 'handling', ['passthrough', 'decoded', 'pcm', 'mixed']);
      choice(block, fields, 'codec', ['aac', 'ac3', 'eac3', 'truehd', 'dts', 'dts_hd', 'flac', 'alac', 'pcm', 'opus', 'mp3', 'mp2', 'vorbis', 'ac4']);
      choice(block, fields, 'objectAudio', ['atmos', 'dtsx']);
      choice(block, fields, 'layout', ['mono', 'stereo', '2.0', '5.1', '5.1(side)', '7.1', '7.1(wide)', '7.1(wide-side)']);
      choice(block, fields, 'bitDepth', [16, 20, 24, 32]);
      number(block, fields, 'channels', 64); number(block, fields, 'sampleRate', 1536000);
    } else {
      choice(block, fields, 'rendering', ['drawnByDevice', 'externalText', 'burnedIn', 'off']);
      if (fields.rendering === undefined) fail();
    }
    out[key] = Object.freeze(fields);
  }
  number(value, out, 'deliveredBitrateKbps', 1000000);
  return Object.freeze(out) as ObservedPlayback;
}
