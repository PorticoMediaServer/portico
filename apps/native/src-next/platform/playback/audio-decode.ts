import type {AndroidNativeFacts} from '../../../../../packages/client-core/src/client-profile';
import type {AudioDecodeCapability} from '../../../../../packages/client-core/src/playback-v1/audio-render';

/**
 * What PorticoAudioRender (the Apple engine for the §18.1 version-2 plan) decodes on the device, for
 * the capability document's `audioDecode` (spec §3). Only the (container, codec) pairs and limits
 * the §18.8 fixtures proved on iOS and tvOS Core Audio (exact frames, lag 0, the albums joined to
 * 0 frames); anything else the server converts to FLAC. Ogg Opus is left out: Core Audio has no
 * Ogg parser, so the server converts it. Channels stop at 2: past that the server downmixes.
 * AAC in ADTS is what the server converts to under a listening bitrate limit (B-10.11): Opus is
 * not available here, so AAC is. ADTS states no priming; the plan's trim (`aac-priming`) does.
 */
export const appleAudioDecode: readonly AudioDecodeCapability[] = Object.freeze([
  {codec: 'flac', containers: ['flac'], maxSampleRate: 96000, maxChannels: 2, maxBitDepth: 24, via: 'ExtAudioFile'},
  {codec: 'alac', containers: ['mp4'], maxSampleRate: 96000, maxChannels: 2, maxBitDepth: 24, via: 'ExtAudioFile'},
  {codec: 'aac', containers: ['mp4', 'adts'], maxSampleRate: 48000, maxChannels: 2, maxBitDepth: 16, via: 'ExtAudioFile'},
  {codec: 'mp3', containers: ['mp3'], maxSampleRate: 48000, maxChannels: 2, maxBitDepth: 16, via: 'ExtAudioFile'},
  {codec: 'pcm', containers: ['wav'], maxSampleRate: 96000, maxChannels: 2, maxBitDepth: 24, via: 'ExtAudioFile'},
].map(c => Object.freeze(c)));

/**
 * What the Android listening owner (ExoPlayer) decodes for the §18.1 plan, as (container, codec)
 * pairs `PorticoAudioPlan.kt` accepts. ALAC is absent: Android has no decoder for it, so the server
 * converts it to FLAC. Ogg Opus is present (Android has both the parser and the decoder), which
 * lets the server answer a bitrate limit with Opus. Each codec's maxChannels follows the device's
 * observed per-codec `maxInputChannelCount` when it is a positive safe integer in the producer's
 * 1..16 range; otherwise it keeps the conservative 2 (the server downmixes above it). PCM stays 2
 * and needs no MediaCodec.
 */
export const androidAudioDecode: readonly AudioDecodeCapability[] = Object.freeze([
  {codec: 'flac', containers: ['flac'], maxSampleRate: 96000, maxChannels: 2, maxBitDepth: 24, via: 'Media3'},
  {codec: 'aac', containers: ['mp4'], maxSampleRate: 48000, maxChannels: 2, maxBitDepth: 16, via: 'Media3'},
  {codec: 'mp3', containers: ['mp3'], maxSampleRate: 48000, maxChannels: 2, maxBitDepth: 16, via: 'Media3'},
  {codec: 'opus', containers: ['ogg'], maxSampleRate: 48000, maxChannels: 2, maxBitDepth: 16, via: 'Media3'},
  {codec: 'vorbis', containers: ['ogg'], maxSampleRate: 48000, maxChannels: 2, maxBitDepth: 16, via: 'Media3'},
  {codec: 'pcm', containers: ['wav'], maxSampleRate: 96000, maxChannels: 2, maxBitDepth: 24, via: 'Media3'},
].map(c => Object.freeze(c)));

/** The table narrowed to the decoders this device reports (`MediaCodecList`); PCM needs none.
 * A codec present in the facts gets its validated observed channel count (1..16), aggregated by
 * maximum across duplicate rows; an absent/invalid count keeps the conservative 2. The baseline
 * table and the input facts are never mutated. */
export function androidAudioDecodeFor(facts: AndroidNativeFacts | null): readonly AudioDecodeCapability[] {
  const decoders = facts?.audioDecoders;
  if (!decoders) return androidAudioDecode;
  const seen = new Set<string>(), observed = new Map<string, number>();
  for (const decoder of decoders) {
    seen.add(decoder.codec);
    const count = decoder.maxChannels;
    if (typeof count !== 'number' || !Number.isSafeInteger(count) || count < 1 || count > 16) continue;
    observed.set(decoder.codec, Math.max(observed.get(decoder.codec) ?? 0, count));
  }
  return Object.freeze(androidAudioDecode.flatMap(row => {
    if (row.codec === 'pcm') return [row];
    if (!seen.has(row.codec)) return [];
    return [Object.freeze({...row, maxChannels: observed.get(row.codec) ?? 2})];
  }));
}
