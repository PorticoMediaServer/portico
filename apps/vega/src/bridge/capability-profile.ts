import {capabilityDocumentProblem, vegaDeclaredProfile, withViewerSettings, type CapabilityDocument, type CapabilityOverrides, type EngineCapabilities} from '../../../../packages/client-core/src/client-profile';
import type {AudioDecodeCapability} from '../../../../packages/client-core/src/playback-v1/audio-render';
// Pure output declarations; loading a profile must not start the retired native UI's stores.
export type DeclaredOutputs = Readonly<{hdr: boolean; surround: boolean}>;
const noDeclaredOutputs: DeclaredOutputs = Object.freeze({hdr: false, surround: false});

/** What `PorticoCapabilities.facts()` answers on Vega. */
export type VegaFacts = Readonly<{model?: string; osVersion?: string; deviceName?: string; appVersion?: string}>;

/**
 * What the app's own audio engine decodes (spec §3 `audioDecode`, §18.1). Music and audiobooks
 * play through the media element here, not through an engine of the app's that decodes samples,
 * so nothing is declared: the server plans listening as ordinary playback.
 */
export const vegaAudioDecode: readonly AudioDecodeCapability[] = Object.freeze([]);

/**
 * The device's capability document: the one description of what this device plays, which the
 * server plans every title from. It has one player engine, the platform's media element
 * (`engines.native`); there is no mpv here, so there is no second profile and the Player setting
 * is always Automatic.
 *
 * Where it comes from:
 *
 * - client-core's declared table for Vega (`vegaDeclaredProfile`) is the floor: what this
 *   platform's player is known to take.
 * - `probed` is what this very device answered when asked (`capability-probe.ts`): which codecs
 *   its decoders take, at what size, in which containers. When it is in hand it replaces the
 *   table's codecs and transports; when the device could not be asked, the table stands.
 *
 * And what is true of this app's player whatever the device answers, stated on top:
 *
 * - **Subtitles: plain text only.** The app draws the server's cue document and nothing else, so
 *   the server burns styled (ASS) and image (PGS, VobSub, DVB) subtitles into the picture for this
 *   device. With "Styled subtitles" off, a styled track arrives as its plain words instead.
 * - **It does not switch between the audio tracks inside an original file.** The app switches
 *   between the named tracks of a stream, and asks the server for the title again for anything else.
 * - **Output facts come from the SDK.** Exact HDR/Dolby Vision and channel/rate probes include
 *   the connected display and AVR. Missing answers keep the declared SDR/stereo floor. No
 *   HDMI route, passthrough, Atmos or bit-perfect output is inferred from a codec-only answer.
 * - **Media passes through the app's HLS reader.** The platform media element does not expose
 *   the final address of a directly opened file. HLS lets the app check every response and
 *   contain grant exposure; the server can use a plain remux without converting the picture.
 */
export function vegaCapabilityDocument(facts: VegaFacts | null, origin: string | null = null, overrides: CapabilityOverrides = {}, probed: CapabilityDocument | null = null, declared: DeclaredOutputs = noDeclaredOutputs): CapabilityDocument {
  return withDeclaredOutputs(vegaDocument(facts, origin, overrides, probed), declared);
}

function vegaDocument(facts: VegaFacts | null, origin: string | null, overrides: CapabilityOverrides, probed: CapabilityDocument | null): CapabilityDocument {
  const model = typeof facts?.model === 'string' ? facts.model.trim() : '';
  // A missing platform answer is not a model name. Keep the actual system value only.
  const observedModel = model && model.length <= 64 && model.toLowerCase() !== 'unknown' ? model : undefined;
  const declared = vegaDeclaredProfile({osVersion: facts?.osVersion, model: observedModel});
  if (facts?.appVersion) declared.client.appVersion = facts.appVersion;
  const table = declared.engines.native, asked = probed?.engines.native;
  const base: EngineCapabilities = asked
    ? {...asked, name: table.name, evidence: 'probed'}
    : table;
  const native: EngineCapabilities = {
    ...base,
    transports: (base.transports ?? []).filter(transport => transport.transport !== 'direct'),
    subtitleRendering: ['text'],
    toneMapsHdr: false,
    embeddedAudioSwitching: false,
  };
  const settings = {engine: 'automatic' as const, overrides};
  const stamped = withViewerSettings({...declared, ...(asked ? {display: probed?.display, audioOutput: probed?.audioOutput} : {}), engines: {native}}, settings);
  if (!capabilityDocumentProblem(stamped)) return stamped;
  // A document the server would refuse is never sent. What the device answered is the newest
  // part, so it is the part left out; the table alone is always a valid document.
  const tabled = withViewerSettings({...declared, engines: {native: {...table, transports: (table.transports ?? []).filter(transport => transport.transport !== 'direct'), subtitleRendering: ['text'], toneMapsHdr: false, embeddedAudioSwitching: false}}}, settings);
  return capabilityDocumentProblem(tabled) ? withViewerSettings({...declared, engines: {native: {...table, transports: (table.transports ?? []).filter(transport => transport.transport !== 'direct')}}}, settings) : tabled;
}

const hdrRanges = ['sdr', 'hdr10', 'hlg'] as const;
const surroundChannels: Readonly<Record<string, number>> = {aac: 6, ac3: 6, eac3: 8};

/** What the viewer said the TV shows and plays, laid over the document (see above). */
export function withDeclaredOutputs(document: CapabilityDocument, declared: DeclaredOutputs): CapabilityDocument {
  if (!declared.hdr && !declared.surround) return document;
  const native = document.engines.native;
  const video = declared.hdr ? native.video?.map(codec => (codec.codec === 'hevc' && codec.bitDepths?.includes(10) ? {...codec, dynamicRanges: [...hdrRanges]} : codec)) : native.video;
  let audio = native.audio;
  if (declared.surround) {
    const listed = new Set((audio ?? []).map(codec => codec.codec));
    audio = [
      ...(audio ?? []).map(codec => (surroundChannels[codec.codec] ? {...codec, maxChannels: Math.max(codec.maxChannels ?? 2, surroundChannels[codec.codec])} : codec)),
      ...(['ac3', 'eac3'] as const).filter(codec => !listed.has(codec)).map(codec => ({codec, maxChannels: surroundChannels[codec], evidence: 'declared' as const})),
    ];
  }
  const next: CapabilityDocument = {
    ...document,
    ...(declared.hdr ? {display: {...document.display, dynamicRanges: [...hdrRanges]}} : {}),
    ...(declared.surround ? {audioOutput: {...document.audioOutput, maxChannels: 8}} : {}),
    engines: {...document.engines, native: {...native, ...(video ? {video} : {}), ...(audio ? {audio} : {})}},
  };
  // What the viewer said never makes the document one the server would refuse.
  return capabilityDocumentProblem(next) ? document : next;
}

const receiverRoutes: readonly string[] = ['hdmi', 'arc', 'earc', 'optical', 'spdif'];

/**
 * What Settings needs to know of the document, so that it offers only switches that do
 * something on this device: whether sound is being handed to a receiver undecoded ("Don't pass
 * sound through"), and whether the device shows or decodes HDR at all ("Turn off HDR").
 */
export function probedSettings(document: CapabilityDocument): Readonly<{audioPassthrough: boolean; hdr: boolean}> {
  const hdr = (ranges: readonly string[] | undefined) => !!ranges?.some(range => range !== 'sdr');
  const native = document.engines.native;
  return {
    audioPassthrough: !!native.audioPassthrough?.length && receiverRoutes.includes(document.audioOutput?.route ?? ''),
    hdr: hdr(document.display?.dynamicRanges) || !!native.video?.some(codec => hdr(codec.dynamicRanges)),
  };
}
