import {capabilityDocumentProblem, probeWebProfile, tizenDeclaredProfile, webosDeclaredProfile, withViewerSettings,
  type CapabilityDocument, type CapabilityOverrides, type DecodingAnswer, type EngineCapabilities, type VideoDecodingQuestion} from '../../../../packages/client-core/src/client-profile';
import {tvPlatform} from '../platform/device';

/**
 * The set's capability document: the one description of what it plays, which the server plans
 * every title from. One engine, the browser's media element fed by the Fire TV app's HLS reader
 * (`apps/vega/src/bridge/hls.ts`); no mpv.
 *
 * - client-core's declared table for the platform (`webosDeclaredProfile`, `tizenDeclaredProfile`)
 *   is the floor, and the screen and sound output always come from it: SDR and stereo, with Dolby
 *   Digital (Plus) handed on. A browser's `decodingInfo` speaks for its decoder, not for the
 *   screen or the receiver, so no HDR display or surround route is inferred from it.
 * - What the browser answers about its decoders (`probeTVPlayer`) replaces the table's codecs and
 *   transports when it is in hand.
 * - True of this app's player whatever the set answers: text subtitles only (the app draws the
 *   server's cue document; the server burns in styled and image subtitles), no switching between
 *   the audio tracks inside an original file, and no direct files (every byte passes through the
 *   HLS reader, which is what keeps credentials to their own server).
 */
export type TVFacts = Readonly<{model?: string; osVersion?: string; appVersion?: string}>;

function declared(facts: TVFacts | null): CapabilityDocument {
  const model = facts?.model?.trim();
  const options = {osVersion: facts?.osVersion, model: model && model.length <= 64 && model.toLowerCase() !== 'unknown' ? model : undefined};
  const document = tvPlatform === 'tizen' ? tizenDeclaredProfile(options) : webosDeclaredProfile(options);
  if (facts?.appVersion) document.client.appVersion = facts.appVersion;
  return document;
}

const ownPlayer = (engine: EngineCapabilities): EngineCapabilities => ({
  ...engine,
  transports: (engine.transports ?? []).filter(transport => transport.transport !== 'direct'),
  subtitleRendering: ['text'],
  toneMapsHdr: false,
  embeddedAudioSwitching: false,
});

export function tvCapabilityDocument(facts: TVFacts | null, overrides: CapabilityOverrides = {}, probed: CapabilityDocument | null = null): CapabilityDocument {
  const table = declared(facts);
  const asked = probed?.engines.native;
  const settings = {engine: 'automatic' as const, overrides};
  const stamped = withViewerSettings({...table, engines: {native: ownPlayer(asked ? {...asked, name: table.engines.native.name, evidence: 'probed'} : table.engines.native)}}, settings);
  if (!capabilityDocumentProblem(stamped)) return stamped;
  // A document the server would refuse is never sent: what the browser answered is left out.
  return withViewerSettings({...table, engines: {native: ownPlayer(table.engines.native)}}, settings);
}

const H264 = 'video/mp4; codecs="avc1.640028"';
const supports = (type: string): boolean => {
  try { return globalThis.MediaSource?.isTypeSupported(type) === true; } catch { return false; }
};

async function decoding(question: VideoDecodingQuestion): Promise<DecodingAnswer> {
  const {contentType, width, height, bitrate, framerate, transferFunction, colorGamut, hdrMetadataType} = question;
  const answer = await navigator.mediaCapabilities.decodingInfo({type: 'media-source',
    video: {contentType, width, height, bitrate, framerate, ...(transferFunction ? {transferFunction} : {}), ...(colorGamut ? {colorGamut} : {}), ...(hdrMetadataType ? {hdrMetadataType} : {})}} as MediaDecodingConfiguration);
  return {supported: answer.supported === true, smooth: answer.smooth === true, powerEfficient: answer.powerEfficient === true};
}

let asked: Promise<CapabilityDocument | null> | undefined;
/** What the browser's decoders take, or null when it could not be asked (the table stands). */
export function probeTVPlayer(refresh = false): Promise<CapabilityDocument | null> {
  if (refresh) asked = undefined;
  asked ??= (async () => {
    try {
      if (!supports(H264) || !navigator.mediaCapabilities) return null;
      const document = await probeWebProfile({
        canPlayType: type => (type === 'application/vnd.apple.mpegurl' ? '' : supports(type) ? 'probably' : ''),
        mediaSourceSupports: supports,
        decodingInfo: decoding,
        identity: {engine: 'html5'},
      });
      return document.engines.native.evidence === 'probed' ? document : null;
    } catch {
      return null;
    }
  })();
  return asked;
}
