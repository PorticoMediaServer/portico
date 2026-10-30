import {MediaSource, decodingInfo} from '@amazon-devices/react-native-w3cmedia';
import {probeWebProfile, type CapabilityDocument, type DecodingAnswer, type VideoDecodingQuestion} from '../../../../packages/client-core/src/client-profile';
import {trace} from '../debug';
import {boundedVegaProbe, probeVegaOutputs} from './capability-output';

/** The SDK's decodingInfo includes the connected screen/AVR, unlike a browser's decoder-only
 * answer. Ask exact transfer functions, Dolby Vision profiles and audio configurations. Failed
 * or missing probes contribute no output facts. SDK 2.3.2 ignores spatialRendering and drops
 * hdrMetadataType internally, so neither Atmos nor HDR10+ can be inferred from those switches.
 * https://developer.amazon.com/docs/vega/0.24/media-capabilities
 */
const H264 = 'video/mp4; codecs="avc1.640028"';

function supports(type: string): boolean {
  try { return MediaSource.isTypeSupported(type) === true; } catch { return false; }
}

/**
 * Whether a picture of this size and frame rate decodes. The SDK asks the device's decoders for
 * exactly the codec, profile, level, size and frame rate named, so `supported` already means "at
 * this size, at this rate". Its own `smooth` is never filled in for a picture (SDK 0.24 leaves it
 * false whatever the decoders answer), so a supported picture is taken to play smoothly: the
 * decoders on these devices are hardware ones, and one that lists a size plays it.
 */
async function decoding(question: VideoDecodingQuestion): Promise<DecodingAnswer> {
  const {contentType, width, height, bitrate, framerate, transferFunction, colorGamut, hdrMetadataType} = question;
  const answer = await decodingInfo({
    type: 'media-source',
    video: {contentType, width, height, bitrate, framerate, hasAlphaChannel: false, ...(transferFunction ? {transferFunction} : {}), ...(colorGamut ? {colorGamut} : {}), ...(hdrMetadataType ? {hdrMetadataType} : {})},
  } as never);
  const supported = answer?.supported === true;
  return {supported, smooth: supported, powerEfficient: answer?.powerEfficient === true};
}

let asked: Promise<CapabilityDocument | null> | undefined;

export function probeVegaPlayer(refresh = false): Promise<CapabilityDocument | null> {
  if (refresh) asked = undefined;
  asked ??= (async () => {
    try {
      if (!supports(H264)) { trace('capability probe: the device did not answer; using the declared table'); return null; }
      const document = await probeWebProfile({
        canPlayType: type => (type === 'application/vnd.apple.mpegurl' ? '' : supports(type) ? 'probably' : ''),
        mediaSourceSupports: supports,
        decodingInfo: decoding,
        identity: {engine: 'w3cmedia'},
      });
      const native = document.engines.native;
      // The shared question about 4K names H.264 at level 4.0, which no decoder owes a 4K
      // picture: a device that answers by level says no. Asked again at the level 4K needs.
      const h264 = native.video?.find(codec => codec.codec === 'h264');
      if (native.evidence === 'probed' && h264 && (h264.maxHeight ?? 0) < 2160) {
        const uhd = await boundedVegaProbe(decoding({contentType: 'video/mp4; codecs="avc1.640033"', width: 3840, height: 2160, bitrate: 40000000, framerate: 30}));
        if (uhd?.supported) { h264.maxWidth = 4096; h264.maxHeight = 2160; }
      }
      const outputs = await probeVegaOutputs(document);
      trace(`capability probe: video ${native.video?.map(codec => `${codec.codec} ${codec.maxHeight ?? '?'}p ${codec.bitDepths?.join('/') ?? ''}`).join(', ')}; audio ${native.audio?.map(codec => codec.codec).join(', ')}`);
      return native.evidence === 'probed' ? outputs : null;
    } catch (error) {
      trace(`capability probe failed: ${String((error as Error)?.message ?? error)}`, true);
      return null;
    }
  })();
  return asked;
}
