import type {ClientAudioCodec, ClientTransport, ClientVideoCodec, DynamicRange, ProfileEvidence} from '../../../../../packages/client-core/src/client-profile';

/**
 * What the mpv engine can play on this device: the second profile of the capability document,
 * beside the platform player's (`engines.mpv`, next to `engines.native`). The codec, transport and
 * evidence shapes are the ones the app's capability probe already uses (`client-profile.ts`).
 *
 * mpv brings its own demuxers and decoders (FFmpeg), so what it can *open* is the same on every
 * device. What differs by device is what it can decode *in time*:
 *
 * - Codecs the chip decodes in hardware (VideoToolbox on Apple: H.264, HEVC, and on newer chips
 *   VP9 and AV1; MediaCodec on Android: whatever `MediaCodecList` has a hardware decoder for) play
 *   at the chip's limits. On Android those limits are the decoder's own, as the system reports them.
 * - Everything else is decoded in software, within limits set by the device's class. The class is
 *   taken from memory and processor count, which every platform can report the same way.
 *
 * The picture is drawn by mpv's own renderer, which tone-maps HDR and Dolby Vision (profiles 5, 7
 * and 8) to whatever the screen shows, so no HDR title needs the server's tone mapping for this
 * engine. Sound is decoded on the device to PCM in as many channels as the output takes; nothing
 * is passed through as a bitstream, and object audio (Atmos, DTS:X) plays as its channel bed.
 *
 * Android differs in two ways, each reported by its module and none assumed here:
 * - Dolby Vision profiles 7 and 8 play as their HDR10 layer; profile 5 (no such layer) is drawn
 *   by mpv's gpu-next renderer, which reshapes it.
 * - An HDR title on an HDR screen is shown in HDR (the decoder draws straight onto the surface),
 *   unless the chosen subtitles are ones mpv must draw, when it is tone-mapped like any other.
 */
export type MpvDeviceFacts = Readonly<{
  available?: boolean;
  memoryBytes?: number;
  processorCount?: number;
  hardwareDecode?: Readonly<{h264?: boolean; hevc?: boolean; vp9?: boolean; av1?: boolean}>;
  /** The screen can show HDR drawn by this engine (an iPhone or iPad with an HDR panel; an Android
   * device with an HDR screen and a hardware decoder for a 10-bit codec). */
  hdrOutput?: boolean;
  screen?: Readonly<{width: number; height: number; maxFrameRate?: number}>;
  audio?: Readonly<{maxOutputChannels?: number}>;
  /** Android: each hardware decoder's own limits, by codec (`MediaCodecList`). A codec listed here
   * is decoded by the chip; `tenBit` says whether that includes 10-bit. Absent (Apple): a
   * hardware codec plays at the chip's fixed limits. */
  hardwareLimits?: Readonly<Record<string, Readonly<{maxWidth?: number; maxHeight?: number; maxFrameRate?: number; tenBit?: boolean; limits?: readonly Readonly<{maxWidth: number; maxHeight: number; maxFrameRate: number}>[]}> | undefined>>;
  /** The HDR kinds the screen shows from this engine. Absent: HDR10 and HLG when `hdrOutput`. */
  hdrOutputRanges?: readonly string[];
  /** The Dolby Vision profiles this engine shows in the right colours. Absent: 5, 7 and 8. */
  dolbyVisionProfiles?: readonly number[];
  /** False when this build of mpv cannot deinterlace. Absent: it can. */
  softwareDeinterlace?: boolean;
  /** This build's mpv plays live channels (placed by the server's clock epochs). Absent: it does not. */
  liveChannels?: boolean;
}>;

export type SubtitleRenderingKind = 'text' | 'ass' | 'pgs' | 'vobsub' | 'dvb';

/** One engine of the capability document (the server's `EngineCapabilities`). */
export type EngineCapabilities = Readonly<{
  name: string;
  version?: string;
  evidence: ProfileEvidence;
  video: readonly ClientVideoCodec[];
  audio: readonly ClientAudioCodec[];
  audioPassthrough: readonly Readonly<{codec: string; maxChannels?: number; objectAudio?: readonly string[]; evidence?: ProfileEvidence}>[];
  transports: readonly ClientTransport[];
  subtitleRendering: readonly SubtitleRenderingKind[];
  toneMapsHdr: boolean;
  embeddedAudioSwitching: boolean;
  liveChannels?: boolean;
}>;

/** How much software decoding the device can keep up with. */
export type MpvDeviceClass = 'light' | 'standard' | 'strong';

const GiB = 1024 ** 3;

/**
 * - light: 2.5 GB of memory or less, or two processor cores (Apple TV HD, phones and tablets of
 *   that age). Software decoding of the heavy codecs is left to the server.
 * - standard: up to 5 GB (Apple TV 4K, most phones). Heavy codecs in software up to 1080p.
 * - strong: more than 5 GB and six or more cores (recent Pro phones, iPad Pro and Air).
 *   Heavy codecs in software up to 4K at film frame rates.
 * Unknown (the module did not answer): light, with no heavy software decoding claimed.
 */
export function mpvDeviceClass(facts: MpvDeviceFacts | null): MpvDeviceClass {
  const memory = facts?.memoryBytes, cores = facts?.processorCount;
  if (!memory || !Number.isFinite(memory) || memory <= 0) return 'light';
  if (memory <= 2.5 * GiB || (!!cores && cores <= 2)) return 'light';
  if (memory > 5 * GiB && (!cores || cores >= 6)) return 'strong';
  return 'standard';
}

type Limit = Readonly<{maxWidth: number; maxHeight: number; maxFrameRate: number}>;
const uhd60: Limit = {maxWidth: 4096, maxHeight: 2304, maxFrameRate: 60};
const hd60: Limit = {maxWidth: 1920, maxHeight: 1080, maxFrameRate: 60};
const hd30: Limit = {maxWidth: 1920, maxHeight: 1080, maxFrameRate: 30};
const uhd30: Limit = {maxWidth: 4096, maxHeight: 2304, maxFrameRate: 30};

/** Software limits for the codecs that cost the most to decode (HEVC, VP9, AV1, 10-bit H.264).
 * ESTIMATES by device class, not measurements. */
const heavySoftware: Record<MpvDeviceClass, Limit | null> = {light: null, standard: hd30, strong: uhd30};
/** Software limits for the older, cheap codecs (MPEG-2, MPEG-4 part 2, VC-1, VP8). ESTIMATES. */
const lightSoftware: Record<MpvDeviceClass, Limit> = {light: hd30, standard: hd60, strong: hd60};

const heavyCodecs = new Set(['hevc', 'vp9', 'av1']);

/**
 * How large a picture a device of this class decodes in software, in time, for a codec; null when
 * it does not keep up at all. The same estimates serve any engine that decodes in software: mpv,
 * and an Android device whose system has only a software decoder for a codec.
 */
export function softwareLimit(codec: string, tier: MpvDeviceClass): Limit | null {
  if (heavyCodecs.has(codec)) return heavySoftware[tier];
  if (codec === 'h264') return tier === 'strong' ? uhd30 : tier === 'standard' ? hd60 : hd30;
  return lightSoftware[tier];
}

/** The limit that fits inside both. */
const within = (a: Limit, b: Limit): Limit => ({maxWidth: Math.min(a.maxWidth, b.maxWidth), maxHeight: Math.min(a.maxHeight, b.maxHeight), maxFrameRate: Math.min(a.maxFrameRate, b.maxFrameRate)});

const whole = (value: unknown): number | undefined => (typeof value === 'number' && Number.isFinite(value) && value >= 1 ? Math.round(value) : undefined);

const hdrRanges: readonly DynamicRange[] = ['sdr', 'hdr10', 'hdr10plus', 'hlg'];

const audio: readonly ClientAudioCodec[] = ([
  ['aac', 8], ['mp3', 2], ['mp2', 2], ['ac3', 6], ['eac3', 8], ['dts', 8], ['truehd', 8], ['mlp', 8],
  ['flac', 8], ['alac', 8], ['opus', 8], ['vorbis', 8], ['pcm', 8], ['wmav2', 2], ['wmapro', 8],
] as const).map<ClientAudioCodec>(([codec, maxChannels]) => Object.freeze({codec, maxChannels,
  ...(codec === 'dts' ? {variants: ['dts_core', 'dts_hd_hra', 'dts_hd_ma'] as ClientAudioCodec['variants']} : {}), evidence: 'declared' as const}));

const fileContainers = ['mkv', 'webm', 'mp4', 'm4v', 'mov', 'avi', 'ts', 'm2ts', 'mts', 'mpg', 'mpeg', 'vob', 'wmv', 'asf', 'flv', 'ogv', 'ogm', '3gp', 'divx'];

/**
 * The mpv engine's capabilities on this device. `facts` is what `PorticoMpvPlaybackModule.facts()`
 * reports; null (it could not be asked) gives a conservative 1080p H.264 baseline. A decoder
 * availability probe does not establish its maximum dimensions or software decoding throughput.
 */
export function mpvEngineCapabilities(facts: MpvDeviceFacts | null): EngineCapabilities {
  const tier = mpvDeviceClass(facts);
  const hardware = {h264: facts?.hardwareDecode?.h264 !== false, hevc: facts?.hardwareDecode?.hevc === true, vp9: facts?.hardwareDecode?.vp9 === true, av1: facts?.hardwareDecode?.av1 === true};
  const probed: ProfileEvidence = facts ? 'mixed' : 'declared';
  const heavy = heavySoftware[tier], light = lightSoftware[tier];
  // A hardware codec's limits: the decoder's own where the device reports them (Android), the
  // chip's fixed limits otherwise (Apple).
  const chip = (codec: string): Limit => {
    const own = facts?.hardwareLimits?.[codec];
    const width = whole(own?.maxWidth), height = whole(own?.maxHeight);
    if (!width || !height) return uhd60;
    return {maxWidth: Math.max(width, height), maxHeight: Math.min(width, height), maxFrameRate: Math.min(240, whole(own?.maxFrameRate) ?? 60)};
  };
  // The chip decoder's own operating points ("4K at 30, 1080p at 60") where the device reports
  // them. Clip dimensions only: the rate at the largest size is not the rate at smaller sizes.
  const chipPoints = (codec: string, overall: Limit): Limit[] => {
    const points = (facts?.hardwareLimits?.[codec]?.limits ?? []).map(p => ({maxWidth: whole(p.maxWidth), maxHeight: whole(p.maxHeight), maxFrameRate: whole(p.maxFrameRate)}))
      .filter((p): p is Limit => !!p.maxWidth && !!p.maxHeight && !!p.maxFrameRate)
      .map(p => ({maxWidth: Math.min(p.maxWidth, overall.maxWidth), maxHeight: Math.min(p.maxHeight, overall.maxHeight), maxFrameRate: Math.min(240, p.maxFrameRate)}));
    return points.length ? points.slice(0, 8) : [overall];
  };
  // Operating points by depth: the chip's for the depths it decodes, software's for a depth only
  // mpv's own decoder takes. Absent when one limit says it all.
  const points = (chipDepths: number[], chipLimit: Limit[], softDepths: number[], soft: Limit | null): ClientVideoCodec['limits'] => {
    const out = [...chipLimit.map(l => ({bitDepths: chipDepths, ...l})), ...(soft && softDepths.length ? [{bitDepths: softDepths, ...soft}] : [])];
    return out.length > 1 ? out : undefined;
  };
  const largest = (limits: Limit[]): Limit => limits.reduce((a, b) => ({maxWidth: Math.max(a.maxWidth, b.maxWidth), maxHeight: Math.max(a.maxHeight, b.maxHeight), maxFrameRate: Math.max(a.maxFrameRate, b.maxFrameRate)}));
  // Whether the chip takes 10-bit for a codec. Where it does not, 10-bit is software's.
  const deep = (codec: string): boolean => facts?.hardwareLimits?.[codec]?.tenBit !== false;
  const dolbyVision = (facts?.dolbyVisionProfiles ?? [5, 7, 8]).filter(p => Number.isInteger(p) && p >= 0 && p <= 20);
  const deinterlaces = facts?.softwareDeinterlace !== false;
  const video: ClientVideoCodec[] = [];
  // H.264: 8-bit in hardware. 10-bit (Hi10P) has no hardware decoder anywhere, so it is offered
  // only where software keeps up, within the software limit: the document's operating points let
  // the two depths have different limits. Availability alone cannot give an A8-based Apple TV HD a
  // 4K decoder; an Android device reports its H.264 decoder's own limits.
  const h264Hardware = facts?.hardwareLimits?.h264 ? chip('h264') : tier === 'light' ? hd60 : uhd60;
  if (hardware.h264) {
    const eight = chipPoints('h264', h264Hardware);
    const soft = heavy ? within(heavy, h264Hardware) : null;
    const limits = points([8], eight, heavy ? [10] : [], soft);
    video.push({codec: 'h264', bitDepths: heavy ? [8, 10] : [8], ...largest([...eight, ...(soft ? [soft] : [])]), ...(limits ? {limits} : {}), dynamicRanges: ['sdr'], evidence: probed});
  } else {
    video.push({codec: 'h264', bitDepths: heavy ? [8, 10] : [8], ...(heavy ?? hd30), dynamicRanges: ['sdr'], evidence: probed});
  }
  // HEVC, VP9 and AV1. A chip that decodes only 8-bit (an older Android device) leaves 10-bit,
  // and with it HDR, to software where software keeps up; both depths then share the limit both
  // fit, as for H.264.
  const modern = (codec: 'hevc' | 'vp9' | 'av1'): void => {
    if (!hardware[codec] && !heavy) return;
    const chipTenBit = hardware[codec] && deep(codec);
    const tenBit = chipTenBit || !!heavy;
    const own = hardware[codec] ? chipPoints(codec, chip(codec)) : [];
    // A chip that decodes only 8-bit keeps its own limits for 8-bit; 10-bit is software's.
    const soft = hardware[codec] && !chipTenBit && heavy ? within(chip(codec), heavy) : null;
    const limits = hardware[codec] ? points(chipTenBit ? [8, 10] : [8], own, soft ? [10] : [], soft) : undefined;
    const limit = !hardware[codec] ? heavy! : largest([...own, ...(soft ? [soft] : [])]);
    const ranges: DynamicRange[] = tenBit ? [...hdrRanges, ...(codec === 'hevc' && dolbyVision.length ? ['dolby_vision' as const] : [])] : ['sdr'];
    video.push({codec, bitDepths: tenBit ? [8, 10] : [8], ...limit, ...(limits ? {limits} : {}), dynamicRanges: ranges, ...(codec === 'hevc' && tenBit && dolbyVision.length ? {dolbyVisionProfiles: dolbyVision} : {}), evidence: probed});
  };
  modern('hevc'); modern('vp9'); modern('av1');
  // The older codecs: the chip's decoder where the device has one (Android), software otherwise.
  // Interlaced video is offered only where mpv can deinterlace it.
  const older = (codec: string, interlaced: boolean): void => {
    const own = facts?.hardwareLimits?.[codec];
    video.push({codec, bitDepths: [8], ...(own ? chip(codec) : light), dynamicRanges: ['sdr'], ...(interlaced && deinterlaces ? {interlaced: true} : {}), evidence: own ? probed : 'declared'});
  };
  for (const codec of ['mpeg2video', 'vc1']) older(codec, true);
  for (const codec of ['mpeg4', 'msmpeg4v3', 'wmv3', 'vp8', 'mpeg1video']) older(codec, false);
  const videoNames = video.map(v => v.codec), audioNames = audio.map(a => a.codec);
  const only = (names: string[]) => names.filter(n => videoNames.includes(n));
  const transports: ClientTransport[] = [
    {transport: 'direct', containers: fileContainers, video: videoNames, audio: audioNames},
    {transport: 'hls_ts', containers: ['mpegts'], video: only(['h264', 'hevc', 'mpeg2video']), audio: ['aac', 'mp3', 'mp2', 'ac3', 'eac3']},
    {transport: 'hls_fmp4', containers: ['fmp4'], video: only(['h264', 'hevc', 'vp9', 'av1']), audio: ['aac', 'mp3', 'ac3', 'eac3', 'flac', 'alac', 'opus']},
  ];
  return Object.freeze({
    name: 'mpv',
    evidence: facts ? 'mixed' : 'declared',
    video: Object.freeze(video.map(v => Object.freeze(v))),
    audio,
    // Nothing is handed to a receiver undecoded: the system has no way for this engine to do it.
    audioPassthrough: Object.freeze([]),
    transports: Object.freeze(transports.map(t => Object.freeze(t))),
    subtitleRendering: Object.freeze(['text', 'ass', 'pgs', 'vobsub', 'dvb'] as const),
    toneMapsHdr: true,
    embeddedAudioSwitching: true,
    ...(facts?.liveChannels === true ? {liveChannels: true} : {}),
  });
}

/**
 * What the screen shows from this engine. An iPhone or iPad with an HDR panel shows HDR10 and HLG
 * (Dolby Vision is drawn as HDR10). An Apple TV shows this engine's picture in standard range:
 * the system gives HDR output to its own video layers only, so HDR titles are tone-mapped on the
 * device there. The server should know: an HDR title on an HDR television loses its HDR in mpv.
 * An Android device with an HDR screen shows HDR10 and HLG titles in HDR from this engine, except
 * while subtitles that mpv itself must draw are on.
 */
export function mpvDisplayRanges(facts: MpvDeviceFacts | null): readonly DynamicRange[] {
  if (facts?.hdrOutput !== true) return ['sdr'];
  // Android names the kinds its screen shows; an HDR iPhone or iPad shows both.
  const named = facts.hdrOutputRanges?.filter((r): r is 'hdr10' | 'hlg' => r === 'hdr10' || r === 'hlg');
  return ['sdr', ...(named ?? (['hdr10', 'hlg'] as const))];
}
