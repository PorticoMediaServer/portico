/**
 * The audio render plan, version 2 (spec §18.1, lead 23 Sep; Plan — Client Playback Migration §9.0
 * and §9.6): the client decodes the audio itself, as Plexamp does. The server says how to fetch the
 * file (`direct`: the original bytes with Range; `converted`: FLAC or Ogg Opus when this device
 * can't decode the source) and supplies what a decoder can't know on its own: the exact gapless
 * trim and the loudness gains. Version 1 (server-decoded PCM windows) is withdrawn.
 *
 * Platform-neutral: the engines (web `WebAudioRender`, Apple `PorticoAudioRender`) consume this;
 * the §18.8 fixtures (`fixtures/audio-gapless/`) are their conformance tests.
 */

export type AudioRenderMode = 'direct' | 'converted' | 'unavailable';

/** Trim, relative to the codec's raw decoded output (every frame of every packet, before any
 * container- or codec-signaled skip): the track is raw frames [startFrames, startFrames + durationFrames). */
export type AudioTrim = Readonly<{startFrames: number; endFrames: number; source: string}>;

/** ReplayGain 2.0 reference (−18 LUFS). A value is absent when unknown. */
export type AudioGain = Readonly<{trackDb?: number; albumDb?: number; trackPeak?: number; albumPeak?: number; source: string}>;

export type AudioRenderV2 = Readonly<{
  version: 2;
 sourceBitrateBps?:number;conversionGainDb?:number;loudnessAppliedDb?:number;
 loudnessEnvelope?:Readonly<{intervalMs:100;startLufs:readonly number[];endLufs:readonly number[]}>;
  mode: AudioRenderMode;
  /** This presentation's media (opaque; not the session id or the presentation generation). */
  id: string;
  /** Diagnostic only: never display the server's reason. */
  reason?: string;
  /** Present unless `unavailable`. */
  url?: string;
  container?: string;
  codec?: string;
  /** Base64 codec configuration for packet decoders (AudioSpecificConfig, STREAMINFO, OpusHead, ALAC cookie). */
  decoderConfig?: string;
  sampleRate?: number;
  channels?: number;
  bitDepth?: number;
  /** `direct`: the file's size. */
  bytes?: number;
  /** Before commit (a prepared presentation) the grant serves at most this many bytes in total. */
  prefetchBytes?: number;
  /** Exact frames the track plays, at `sampleRate`, after trim. */
  durationFrames?: number;
  downmixed?: boolean;
  trim?: AudioTrim;
  gain?: AudioGain;
  /** `direct` in a container with no seek index of its own (MP3, ADTS, Ogg): the server's
   * (`parseSeekIndex`), so a part-way start reads the file from the nearest entry with a Range
   * request and decodes to the exact frame. Nothing is converted. */
  seekIndex?: Readonly<{url: string}>;
}>;

/** A file's seek index: `offsets[i]` is a packet (MP3, ADTS) or page (Ogg) boundary and
 * `frames[i]` the raw frame decoding from there starts at (from the first audio packet, as the
 * plan's trim counts; a Vorbis entry is the page granule before it). Both rise. */
export type SeekIndex = Readonly<{offsets: readonly number[]; frames: readonly number[]}>;

/** Reads `GET …/audio-index`. Undefined when it is not a well-formed index (the start then reads
 * the file from its beginning, as it would without one). */
export function parseSeekIndex(v: unknown): SeekIndex | undefined {
  if (!isObj(v) || v.version !== 1 || !Array.isArray(v.offsets) || !Array.isArray(v.frames)) return undefined;
  const offsets = v.offsets, frames = v.frames;
  if (!offsets.length || offsets.length > 4096 || offsets.length !== frames.length) return undefined;
  for (let i = 0; i < offsets.length; i++) {
    if (count(offsets[i]) === undefined || count(frames[i]) === undefined) return undefined;
    if (i > 0 && (offsets[i] <= offsets[i - 1] || frames[i] < frames[i - 1])) return undefined;
  }
  return Object.freeze({offsets: Object.freeze([...offsets] as number[]), frames: Object.freeze([...frames] as number[])});
}

/** A plan an engine can play: direct or converted, with everything gapless needs. */
export type PlayableAudioRender = AudioRenderV2 & Readonly<{mode: 'direct' | 'converted'; url: string; container: string; codec: string; sampleRate: number; channels: number; durationFrames: number; trim: AudioTrim}>;

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const count = (v: unknown, max = Number.MAX_SAFE_INTEGER): number | undefined => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 && v <= max ? v : undefined;
const finite = (v: unknown, lo: number, hi: number): number | undefined => typeof v === 'number' && Number.isFinite(v) && v >= lo && v <= hi ? v : undefined;
const text = (v: unknown, max = 4096): string | undefined => typeof v === 'string' && v.length <= max ? v : undefined;

/**
 * Reads `presentation.audioRender`. Undefined when absent, not version 2, or malformed: the client
 * then plays the presentation ordinarily. A `direct`/`converted` plan missing what gapless needs
 * reads as `unavailable` (never guessed), keeping any reason.
 */
export function parseAudioRenderV2(v: unknown): AudioRenderV2 | undefined {
  if (!isObj(v) || v.version !== 2 || typeof v.id !== 'string' || v.id === '' || v.id.length > 256) return undefined;
  const mode = v.mode === 'direct' || v.mode === 'converted' || v.mode === 'unavailable' ? v.mode : undefined;
  if (!mode) return undefined;
  const reason = text(v.reason);
  if (mode === 'unavailable') return Object.freeze({version: 2, mode, id: v.id, ...(reason ? {reason} : {})});
  const trim = isObj(v.trim) ? v.trim : undefined;
  const startFrames = count(trim?.startFrames), endFrames = count(trim?.endFrames);
  const gain = isObj(v.gain) ? v.gain : undefined;
  const seekIndex = mode === 'direct' && isObj(v.seekIndex) && text(v.seekIndex.url) ? Object.freeze({url: v.seekIndex.url as string}) : undefined;
  let loudnessEnvelope:AudioRenderV2['loudnessEnvelope'];
 if(v.loudnessEnvelope!==undefined){const e=v.loudnessEnvelope;if(!isObj(e)||e.intervalMs!==100||!Array.isArray(e.startLufs)||!Array.isArray(e.endLufs)||e.startLufs.length>30||e.endLufs.length>30||[...e.startLufs,...e.endLufs].some(x=>finite(x,-120,10)===undefined))return undefined;loudnessEnvelope=Object.freeze({intervalMs:100,startLufs:Object.freeze([...e.startLufs]),endLufs:Object.freeze([...e.endLufs])});}
 // A baked stream cannot also instruct the device to apply source gain.
 // Refuse contradictory metadata instead of relying on every renderer to
 // notice that the gain has already been applied.
 if(v.loudnessAppliedDb!==undefined&&(finite(v.loudnessAppliedDb,-60,30)===undefined||isObj(v.gain)))return undefined;
 if(v.conversionGainDb!==undefined&&finite(v.conversionGainDb,-60,30)===undefined)return undefined;
 const plan = {
 ...(loudnessEnvelope?{loudnessEnvelope}:{}),
 ...(count(v.sourceBitrateBps)!==undefined?{sourceBitrateBps:v.sourceBitrateBps as number}:{}),
 ...(v.conversionGainDb!==undefined?{conversionGainDb:v.conversionGainDb as number}:{}),
 ...(v.loudnessAppliedDb!==undefined?{loudnessAppliedDb:v.loudnessAppliedDb as number}:{}),
    version: 2 as const, mode, id: v.id,
    ...(reason ? {reason} : {}),
    url: text(v.url), container: text(v.container, 32), codec: text(v.codec, 32),
    ...(text(v.decoderConfig, 1 << 16) ? {decoderConfig: v.decoderConfig as string} : {}),
    sampleRate: count(v.sampleRate, 768_000), channels: count(v.channels, 32),
    ...(count(v.bitDepth, 64) ? {bitDepth: v.bitDepth as number} : {}),
    ...(count(v.bytes) !== undefined ? {bytes: v.bytes as number} : {}),
    ...(count(v.prefetchBytes) !== undefined ? {prefetchBytes: v.prefetchBytes as number} : {}),
    durationFrames: count(v.durationFrames),
    ...(v.downmixed === true ? {downmixed: true} : {}),
    trim: startFrames !== undefined && endFrames !== undefined ? Object.freeze({startFrames, endFrames, source: text(trim?.source, 32) ?? ''}) : undefined,
    ...(gain ? {gain: Object.freeze({
      ...(finite(gain.trackDb, -60, 30) !== undefined ? {trackDb: gain.trackDb as number} : {}),
      ...(finite(gain.albumDb, -60, 30) !== undefined ? {albumDb: gain.albumDb as number} : {}),
      ...(finite(gain.trackPeak, 1e-6, 64) !== undefined ? {trackPeak: gain.trackPeak as number} : {}),
      ...(finite(gain.albumPeak, 1e-6, 64) !== undefined ? {albumPeak: gain.albumPeak as number} : {}),
      source: text(gain.source, 32) ?? '',
    })} : {}),
    ...(seekIndex ? {seekIndex} : {}),
  };
  if (!plan.url || !plan.container || !plan.codec || !plan.sampleRate || !plan.channels || !plan.durationFrames || !plan.trim) {
    return Object.freeze({version: 2, mode: 'unavailable', id: v.id, ...(reason ? {reason} : {})});
  }
  return Object.freeze(plan) as AudioRenderV2;
}

export function playableAudio(plan: AudioRenderV2 | undefined): plan is PlayableAudioRender {
  return !!plan && plan.mode !== 'unavailable' && !!plan.url && !!plan.trim && !!plan.durationFrames && !!plan.sampleRate;
}

/** The track's length in seconds (exact frames over the file's rate). */
export function audioSeconds(plan: PlayableAudioRender): number { return plan.durationFrames / plan.sampleRate; }

export type Normalization = 'off' | 'track' | 'album';

/**
 * The linear gain an engine applies (§9.6 rule 7): `10^((gainDb + preampDb)/20)`, limited so the
 * peak stays at or under full scale when the peak is known. Album mode falls back to track values.
 * 1 when normalization is off or the gain is unknown.
 */
export function audioGainLinear(plan: AudioRenderV2 | undefined, mode: Normalization, preampDb = 0, neverBoost = false): number {
 if(plan?.loudnessAppliedDb!==undefined)return 1;
  const g = plan?.gain;
  if (!g || mode === 'off') return 1;
  const album = mode === 'album' && g.albumDb !== undefined;
  const db = album ? g.albumDb : g.trackDb;
  if (db === undefined) return 1;
  const peak = album ? (g.albumPeak ?? g.trackPeak) : g.trackPeak;
  let linear = Math.pow(10, (db + preampDb) / 20);
  if (peak !== undefined && peak * linear > 1) linear = 1 / peak;
  // A gain without a measured peak may attenuate, but cannot safely amplify.
  return Math.max(0, Math.min(linear, neverBoost || peak === undefined ? 1 : 32));
}

/**
 * Which raw decoded frames are the track, given how many frames the platform decoder already
 * dropped at the start (0 for a decoder that emits every packet's frames; `trim.startFrames` for one
 * that honors the container's skip). The engine outputs raw frames [from, to).
 */
export function audioTrimWindow(plan: PlayableAudioRender, alreadySkipped: number): Readonly<{from: number; to: number}> {
  const from = Math.max(0, plan.trim.startFrames - Math.max(0, alreadySkipped));
  return Object.freeze({from, to: from + plan.durationFrames});
}

/** What an engine declares in the capability profile's `audioDecode` (spec §3). */
export type AudioDecodeCapability = Readonly<{codec: string; containers: readonly string[]; maxSampleRate: number; maxChannels: number; maxBitDepth: number;
  /** When present, the exact rates that decode (overrides maxSampleRate). */
  sampleRates?: readonly number[];
  /** Client-side detail (the server ignores it): which decoder path passed the fixtures. */
  via?: string}>;
