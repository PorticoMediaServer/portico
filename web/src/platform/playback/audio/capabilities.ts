import type {AudioDecodeCapability} from '@core/playback-v1/audio-render.ts';
import {qualifyBrowserDecoders, qualifiedConfigs} from './qualification';
import {WEBCODECS_SKIP, browserEngine, type BrowserEngine} from './track';

/**
 * What this browser's music engine decodes (§9.6 rule 10, spec §3 `audioDecode`): only the
 * (container, codec) pairs and limits that passed the §18.8 fixtures on this engine
 * (`WEBCODECS_SKIP`), confirmed by `AudioDecoder.isConfigSupported` at startup. The server plays
 * those files as they are and converts the rest to FLAC or Opus.
 */
export type ConfigCheck = ((config: {codec: string; sampleRate: number; numberOfChannels: number; description?: Uint8Array}) => Promise<boolean>) & {ready?: Promise<void>};

const aacConfig = (rate: number, channels: number) => {
  const rates = [96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000];
  const index = rates.indexOf(rate);
  return new Uint8Array([(2 << 3) | (index >> 1), ((index & 1) << 7) | (channels << 3)]);
};
const opusHead = (channels: number) => new Uint8Array([0x4f, 0x70, 0x75, 0x73, 0x48, 0x65, 0x61, 0x64, 1, channels, 0x38, 0x01, 0x80, 0xbb, 0, 0, 0, 0, 0]);
const flacInfo = (rate: number, channels: number, bits: number) => {
  const d = new Uint8Array(42);
  d.set([0x66, 0x4c, 0x61, 0x43, 0x80, 0, 0, 34, 0x10, 0, 0x10, 0]);
  d[18] = rate >> 12; d[19] = (rate >> 4) & 0xff; d[20] = ((rate & 0xf) << 4) | ((channels - 1) << 1) | ((bits - 1) >> 4); d[21] = ((bits - 1) & 0xf) << 4;
  return d;
};

/** The highest of `options` that passes `check`, or 0. */
async function highest(options: readonly number[], check: (v: number) => Promise<boolean>): Promise<number> {
  for (const v of [...options].sort((a, b) => b - a)) if (await check(v)) return v;
  return 0;
}

export async function probeAudioDecode(check: ConfigCheck | undefined, engine: BrowserEngine = browserEngine(), outputChannels = 2): Promise<AudioDecodeCapability[]> {
  await check?.ready;
  const known = WEBCODECS_SKIP[engine];
  const out: AudioDecodeCapability[] = [];
  const ok = async (c: Parameters<ConfigCheck>[0]) => { try { return !!check && await check(c); } catch { return false; } };
  // PCM needs no decoder: WAV and AIFF are converted here.
  out.push({codec: 'pcm', containers: ['wav', 'aiff'], maxSampleRate: 384000, maxChannels: 8, maxBitDepth: 32, via: 'pcm'});
  // FLAC: WebCodecs where it passed here, else the JavaScript decoder (exact everywhere).
  const flacWebCodecs = known.flac !== undefined && await ok({codec: 'flac', sampleRate: 96000, numberOfChannels: 2, description: flacInfo(96000, 2, 24)});
  out.push({codec: 'flac', containers: ['flac', 'mp4'], maxSampleRate: 384000, maxChannels: 8, maxBitDepth: 24, via: flacWebCodecs ? 'webcodecs' : 'js-flac'});
  if (known.mp3 !== undefined && await ok({codec: 'mp3', sampleRate: 44100, numberOfChannels: 2})) {
    const rate = await highest([48000, 44100, 32000], r => ok({codec: 'mp3', sampleRate: r, numberOfChannels: 2}));
    out.push({codec: 'mp3', containers: ['mp3'], maxSampleRate: rate, maxChannels: 2, maxBitDepth: 32, via: 'webcodecs'});
  }
  if (known.aac !== undefined && await ok({codec: 'mp4a.40.2', sampleRate: 44100, numberOfChannels: 2, description: aacConfig(44100, 2)})) {
    const rate = await highest([96000, 48000, 44100], r => ok({codec: 'mp4a.40.2', sampleRate: r, numberOfChannels: 2, description: aacConfig(r, 2)}));
    const channels = await highest([6, 2], n => ok({codec: 'mp4a.40.2', sampleRate: 48000, numberOfChannels: n, description: aacConfig(48000, n)}));
    out.push({codec: 'aac', containers: ['mp4', 'adts'], maxSampleRate: rate, maxChannels: channels, maxBitDepth: 32, via: 'webcodecs'});
  }
  if (known.opus !== undefined && await ok({codec: 'opus', sampleRate: 48000, numberOfChannels: 2, description: opusHead(2)})) {
    out.push({codec: 'opus', containers: ['ogg', 'mp4'], maxSampleRate: 48000, maxChannels: 2, maxBitDepth: 32, via: 'webcodecs'});
  }
  const vorbis = qualifiedConfigs.vorbis;
  if (known.vorbis !== undefined && vorbis && await ok(vorbis)) {
    out.push({codec: 'vorbis', containers: ['ogg'], maxSampleRate: vorbis.sampleRate, sampleRates: [vorbis.sampleRate], maxChannels: vorbis.numberOfChannels, maxBitDepth: 32, via: 'webcodecs'});
  }
  const ceiling = Number.isSafeInteger(outputChannels) && outputChannels > 0 ? Math.min(8, outputChannels) : 2;
  return out.map(capability => ({...capability, maxChannels: Math.min(capability.maxChannels ?? ceiling, ceiling)}));
}

/** The real browser's check. */
export function browserConfigCheck(): ConfigCheck | undefined {
  const Decoder = (globalThis as unknown as {AudioDecoder?: {isConfigSupported(c: unknown): Promise<{supported?: boolean}>}}).AudioDecoder;
  if (!Decoder) return undefined;
  return Object.assign(async (c: Parameters<ConfigCheck>[0]) => (await Decoder.isConfigSupported(c)).supported === true,
    {ready: qualifyBrowserDecoders()});
}
