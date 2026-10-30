import type {AudioDecodeCapability} from '../../../../../packages/client-core/src/playback-v1/audio-render';
import type {SubtitleRendering} from '../../../../../packages/client-core/src/subtitles';

const record = (value: unknown): Record<string, unknown> | undefined => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
const integer = (value: unknown, maximum: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value > 0 && value <= maximum;
const pairs: Readonly<Record<string, readonly string[]>> = {flac: ['flac'], alac: ['mp4'], aac: ['mp4', 'adts'], mp3: ['mp3'], pcm: ['wav', 'aiff'], opus: ['ogg']};

/** Real native decoder creation at each rate, narrowed to the file engine's implemented pairs.
 * These are decoding facts; they never establish a DAC's supported rates or bit-perfect output. */
export function appleAudioDecodeFor(facts: unknown): readonly AudioDecodeCapability[] {
  const rows = record(facts)?.audioDecode;
  if (!Array.isArray(rows) || rows.length > 32) return [];
  const result: AudioDecodeCapability[] = [];
  for (const value of rows) {
    const row = record(value);
    if (!row || typeof row.codec !== 'string' || !Object.hasOwn(pairs, row.codec) || result.some(item => item.codec === row.codec)) continue;
    const codec = row.codec, containers = row.containers, rates = row.sampleRates;
    if (!Array.isArray(containers) || !containers.length || containers.length > 16 || !containers.every((item): item is string => typeof item === 'string' && pairs[codec].includes(item))) continue;
    if (!Array.isArray(rates) || !rates.length || rates.length > 32 || !rates.every((item): item is number => integer(item, 768000))) continue;
    if (!integer(row.maxSampleRate, 768000) || row.maxSampleRate !== Math.max(...rates) || !integer(row.maxChannels, 32) || !integer(row.maxBitDepth, 32)) continue;
    if (typeof row.via !== 'string' || !row.via.length || row.via.length > 32 || /[\x00-\x1f\x7f]/.test(row.via)) continue;
    result.push({codec, containers: [...containers], sampleRates: [...new Set(rates)].sort((a, b) => a - b), maxSampleRate: row.maxSampleRate, maxChannels: row.maxChannels, maxBitDepth: row.maxBitDepth, via: row.via});
  }
  return result;
}

/** Only a build that reports its linked overlay implementation advertises rich subtitles. */
export function appleSubtitleRendering(facts: unknown): SubtitleRendering[] {
  const values = record(facts)?.subtitleRendering;
  const supported: readonly SubtitleRendering[] = ['text', 'ass', 'pgs', 'vobsub', 'dvb'];
  if (!Array.isArray(values) || values.length > supported.length || !values.every(value => supported.includes(value))) return ['text'];
  return [...new Set<SubtitleRendering>(['text', ...values])];
}
