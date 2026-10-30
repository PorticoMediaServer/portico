import {decodingInfo} from '@amazon-devices/react-native-w3cmedia';
import type {CapabilityDocument, ClientAudioCodec, DynamicRange} from '../../../../packages/client-core/src/client-profile';

/** A hung device query is bounded, and completed queries leave no timeout behind. */
export async function boundedVegaProbe<T>(question: Promise<T>, ms = 1500): Promise<T | null> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try { return await Promise.race([question.catch(() => null), new Promise<null>(resolve => { timer = setTimeout(() => resolve(null), ms); })]); }
  finally { if (timer) clearTimeout(timer); }
}

type OutputConfiguration = Omit<Parameters<typeof decodingInfo>[0], 'audio' | 'video'> & Partial<Pick<Parameters<typeof decodingInfo>[0], 'audio' | 'video'>>;
type OutputProbe = (configuration: OutputConfiguration) => Promise<Pick<Awaited<ReturnType<typeof decodingInfo>>, 'supported'>>;

/** Keep the device's exact output answers on the document; never turn codec support into an
 * invented HDMI route, bit-perfect output, or Atmos support. Exported for controlled SDK tests. */
export async function probeVegaOutputs(document: CapabilityDocument, ask: OutputProbe = configuration => decodingInfo(configuration as Parameters<typeof decodingInfo>[0])): Promise<CapabilityDocument> {
  const native = document.engines.native;
  const supported = async (configuration: OutputConfiguration) => {
    try { return (await boundedVegaProbe(ask(configuration)))?.supported === true; } catch { return false; }
  };
  const video = await Promise.all((native.video ?? []).map(async codec => {
    const contentType = ({hevc: 'video/mp4; codecs="hvc1.2.4.L153.B0"', av1: 'video/mp4; codecs="av01.0.12M.10.0.110.09.16.09.0"', vp9: 'video/webm; codecs="vp09.02.51.10"'} as Record<string, string>)[codec.codec];
    const dynamicRanges: DynamicRange[] = ['sdr'];
    const profiles: number[] = [];
    const question = {width: Math.min(codec.maxWidth ?? 1920, 3840), height: Math.min(codec.maxHeight ?? 1080, 2160), bitrate: 25_000_000, framerate: 30, hasAlphaChannel: false};
    if (contentType && codec.bitDepths?.includes(10)) {
      for (const [range, transferFunction] of [['hdr10', 'pq'], ['hlg', 'hlg']] as const) {
        if (await supported({type: 'media-source', video: {...question, contentType, transferFunction, colorGamut: 'rec2020'}})) dynamicRanges.push(range);
      }
    }
    if (codec.codec === 'hevc') {
      for (const profile of [5, 7, 8]) {
        if (await supported({type: 'media-source', video: {...question, contentType: `video/mp4; codecs="dvh1.${String(profile).padStart(2, '0')}.07"`}})) profiles.push(profile);
      }
      if (profiles.length) dynamicRanges.push('dolby_vision');
    }
    return {...codec, dynamicRanges, dolbyVisionProfiles: profiles};
  }));
  // The SDK uses `samplerate` (lowercase) and a string channel count. Test the channel/rate
  // combination together: a stereo hi-res answer must not raise a surround format's ceiling.
  const types: Record<string, string> = {aac: 'mp4a.40.2', mp3: 'mp4a.69', ac3: 'ac-3', eac3: 'ec-3', flac: 'fLaC', opus: 'Opus', vorbis: 'vorbis', alac: 'alac', pcm: 'lpcm'};
  const audio = await Promise.all((native.audio ?? []).map(async codec => {
    const contentType = codec.codec === 'vorbis' ? 'audio/webm; codecs="vorbis"' : `audio/mp4; codecs="${types[codec.codec]}"`;
    const query = (channels: number, samplerate: number) => supported({type: 'media-source', audio: {contentType, channels: String(channels), samplerate, bitrate: codec.codec === 'flac' || codec.codec === 'pcm' ? 8_000_000 : 768_000, spatialRendering: false}});
    let maxChannels = 2, maxSampleRate: number | undefined;
    let answered = false;
    for (const channels of [8, 6, 2]) { if (types[codec.codec] && await query(channels, 48000)) { maxChannels = channels; answered = true; break; } }
    for (const rate of [192000, 176400, 96000, 88200, 48000, 44100]) { if (types[codec.codec] && await query(maxChannels, rate)) { maxSampleRate = rate; break; } }
    const {objectAudio: _unverifiedObjects, ...plain} = codec;
    return {...plain, maxChannels, ...(maxSampleRate ? {maxSampleRate} : {}), evidence: answered ? 'probed' as const : 'declared' as const};
  }));
  // The player accepts Dolby through the SDK; its answer does not distinguish decode from
  // passthrough. Declare playable audio, without asserting an unobserved receiver route.
  return {...document, display: {...document.display, dynamicRanges: [...new Set(video.flatMap(codec => codec.dynamicRanges))]}, audioOutput: {maxChannels: Math.max(2, ...audio.map(codec => codec.maxChannels ?? 2))}, engines: {native: {...native, video, audio: audio as ClientAudioCodec[], audioPassthrough: []}}};
}
