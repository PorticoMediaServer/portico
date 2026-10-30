import {limitPassthroughFormats, type PassthroughFormats} from './passthrough-formats';
import {androidProfile, appleProfile, capabilityDocumentProblem, withViewerSettings, type AndroidNativeFacts, type AppleNativeFacts, type CapabilityDocument, type CapabilityOverrides, type EngineCapabilities as DocumentEngine, type EngineSetting} from '../../../../../packages/client-core/src/client-profile';
import {androidAudioDecodeFor, appleAudioDecode} from './audio-decode';
import type {NativeFacts} from './device-profile';
import {mpvDeviceClass, mpvDisplayRanges, mpvEngineCapabilities, softwareLimit, type EngineCapabilities, type MpvDeviceClass, type MpvDeviceFacts} from './mpv-profile';

export type {CapabilityDocument};

/**
 * The device's capability document (`PUT /v1/me/devices/current/capabilities`): the one
 * description of what this device can play. It carries one profile per player engine
 * (`engines.native`, and `engines.mpv` where the build has mpv), the viewer's Player setting and
 * overrides, the screen and the audio output. The server plans every title from it and names the
 * engine in the plan.
 *
 * The platform player's profile is the shared one for the platform (the declared table narrowed by
 * what the device reports), with what is true of this app's own engines stated on top:
 *
 * - It draws text subtitles only: the app draws the server's plain cue document itself, whatever
 *   the system's player could draw on its own.
 * - It does not switch between the audio tracks inside an original file: the app switches between
 *   the named tracks of a stream, and asks the server for the title again for anything else.
 * - Android: a codec the system has only a software decoder for is held to what software keeps
 *   up with on this class of device, or left out where it does not keep up at all.
 */
export function nativeEngineCapabilities(document: CapabilityDocument, android?: Readonly<{facts: AndroidNativeFacts | null; deviceClass: MpvDeviceClass}>): DocumentEngine {
  const base = document.engines.native;
  let video = base.video ?? [];
  let transports = base.transports ?? [];
  const software = new Set((android?.facts?.decoders ?? []).filter(d => d.hardware === false).map(d => d.codec));
  if (android && software.size) {
    video = video.flatMap(v => {
      if (!software.has(v.codec)) return [v];
      const limit = softwareLimit(v.codec, android.deviceClass);
      if (!limit) return [];
      // The software estimate replaces the decoder's own operating points.
      const narrowed = {...v};
      delete narrowed.limits;
      return [{...narrowed, maxWidth: Math.min(v.maxWidth ?? limit.maxWidth, limit.maxWidth), maxHeight: Math.min(v.maxHeight ?? limit.maxHeight, limit.maxHeight), maxFrameRate: Math.min(v.maxFrameRate ?? limit.maxFrameRate, limit.maxFrameRate), evidence: 'declared' as const}];
    });
    const names = new Set(video.map(v => v.codec));
    transports = transports.map(t => (t.video ? {...t, video: t.video.filter(name => names.has(name))} : t));
  }
  return {...base, video, transports, subtitleRendering: ['text'], embeddedAudioSwitching: false};
}

/** The mpv profile in the document's own (plain) shape. */
function documentEngine(engine: EngineCapabilities): DocumentEngine {
  return {
    name: engine.name, ...(engine.version ? {version: engine.version} : {}), evidence: engine.evidence,
    video: engine.video.map(v => ({...v})), audio: engine.audio.map(a => ({...a})), audioPassthrough: [],
    transports: engine.transports.map(t => ({...t})), subtitleRendering: [...engine.subtitleRendering],
    toneMapsHdr: engine.toneMapsHdr, embeddedAudioSwitching: engine.embeddedAudioSwitching,
    ...(engine.liveChannels ? {liveChannels: true} : {}),
  };
}

export type CapabilityDocumentInput = Readonly<{
  setting: EngineSetting;
  /** The viewer's overrides (force stereo, no passthrough, no HDR). Absent: none. */
  overrides?: CapabilityOverrides;
  /** `PorticoCapabilities.facts()`, or null when it could not be asked. */
  native: NativeFacts | null;
  /** `PorticoMpvPlaybackModule.facts()`; null when this build has no mpv (no `engines.mpv`). */
  mpv: MpvDeviceFacts | null;
  mpvAvailable: boolean;
  appVersion: string;
  /** What kind of device this is. `family` is set on Android (and absent on Apple). */
  device: Readonly<{television: boolean; pad: boolean; family?: 'android' | 'androidtv' | 'firetv'; osVersion?: string}>;
}>;

export function deviceCapabilityDocument(input: CapabilityDocumentInput & {passthroughFormats?: PassthroughFormats}): CapabilityDocument {
  const document = baseDeviceCapabilityDocument(input);
  return input.passthroughFormats ? limitPassthroughFormats(document, input.passthroughFormats) : document;
}

function baseDeviceCapabilityDocument(input: CapabilityDocumentInput): CapabilityDocument {
  const device = input.device;
  const osVersion = input.native?.osVersion ?? device.osVersion;
  const apple = !device.family;
  const shared = apple
    ? appleProfile({television: device.television, phone: !device.pad, osVersion, model: input.native?.model, appVersion: input.appVersion}, input.native as AppleNativeFacts | null)
    : androidProfile({family: device.family, television: device.television, tablet: device.pad, osVersion, model: input.native?.model}, input.native as AndroidNativeFacts | null);
  const native = nativeEngineCapabilities(shared, apple ? undefined : {facts: input.native as AndroidNativeFacts | null, deviceClass: mpvDeviceClass(input.mpv)});
  const settings = {engine: input.setting, overrides: input.overrides ?? {}};
  // The screen is one screen: what it shows is said once, for the platform player. Where mpv
  // shows less (an Apple TV shows mpv's picture in standard range) its own tone mapping covers
  // the difference, and `mpvDisplayRanges` is what a planner that weighs it would read.
  const base: CapabilityDocument = {
    ...shared,
    client: {...shared.client, ...(input.appVersion ? {appVersion: input.appVersion} : {})},
    engines: {native},
    audioDecode: (apple ? appleAudioDecode : androidAudioDecodeFor(input.native as AndroidNativeFacts | null)).map(({sampleRates, ...d}) => ({...d, containers: [...d.containers], ...(sampleRates ? {sampleRates: [...sampleRates]} : {})})),
  };
  const withoutMpv = withViewerSettings(base, settings);
  if (!input.mpvAvailable) return capabilityDocumentProblem(withoutMpv) ? withViewerSettings(shared, settings) : withoutMpv;
  const both = withViewerSettings({...base, engines: {native, mpv: documentEngine(mpvEngineCapabilities(input.mpv))}}, settings);
  // A document the server would refuse is never sent: the title would then be planned against
  // nothing at all. The mpv profile is the newest part, so it is the part that is left out.
  return capabilityDocumentProblem(both) ? (capabilityDocumentProblem(withoutMpv) ? withViewerSettings(shared, settings) : withoutMpv) : both;
}

/** What Settings needs to know of the document: whether there is a receiver to pass sound through
 * to, and whether this device shows or decodes HDR at all. */
export function probedDevice(document: CapabilityDocument): Readonly<{audioPassthrough: boolean; hdr: boolean}> {
  const engines = [document.engines.native, ...(document.engines.mpv ? [document.engines.mpv] : [])];
  const hdr = (ranges: readonly string[] | undefined) => !!ranges?.some(r => r !== 'sdr');
  return {
    audioPassthrough: engines.some(e => !!e.audioPassthrough?.length),
    hdr: hdr(document.display?.dynamicRanges) || engines.some(e => !!e.video?.some(v => hdr(v.dynamicRanges))),
  };
}

/** What the screen shows from the mpv engine, for diagnostics and for a planner that weighs it. */
export {mpvDisplayRanges};
