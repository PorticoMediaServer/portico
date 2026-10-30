import {appleProfile, capabilityDocumentProblem, withViewerSettings, type AppleNativeFacts, type CapabilityDocument, type CapabilityOverrides, type EngineCapabilities} from '../../../../../packages/client-core/src/client-profile';
import {appleAudioDecodeFor, appleSubtitleRendering} from './audio-decode.ios';
import type {NativeFacts} from './device-profile';

export type {CapabilityDocument};
export type CapabilityDocumentInput = Readonly<{
  overrides?: CapabilityOverrides;
  native: NativeFacts | null;
  appVersion: string;
  device: Readonly<{television: boolean; pad: boolean; osVersion?: string}>;
}>;

/** AVPlayer is the only Apple video engine. Its original-file audio is replanned server-side. */
export function nativeEngineCapabilities(document: CapabilityDocument, facts?: unknown): EngineCapabilities {
  return {...document.engines.native, subtitleRendering: appleSubtitleRendering(facts), embeddedAudioSwitching: false};
}

export function deviceCapabilityDocument(input: CapabilityDocumentInput): CapabilityDocument {
  const shared = appleProfile({television: input.device.television, phone: !input.device.pad,
    osVersion: input.native?.osVersion ?? input.device.osVersion, model: input.native?.model, appVersion: input.appVersion}, input.native as AppleNativeFacts | null);
  const settings = {engine: 'native' as const, overrides: input.overrides ?? {}};
  const document = withViewerSettings({...shared,
    client: {...shared.client, ...(input.appVersion ? {appVersion: input.appVersion} : {})},
    engines: {native: nativeEngineCapabilities(shared, input.native)},
    audioDecode: [...appleAudioDecodeFor(input.native)].map(value => ({...value, containers: [...value.containers], sampleRates: value.sampleRates ? [...value.sampleRates] : undefined})),
  }, settings);
  return capabilityDocumentProblem(document) ? withViewerSettings(shared, settings) : document;
}

export function probedDevice(document: CapabilityDocument): Readonly<{audioPassthrough: boolean; hdr: boolean}> {
  const native = document.engines.native;
  const hdr = (ranges: readonly string[] | undefined) => !!ranges?.some(range => range !== 'sdr');
  return {audioPassthrough: !!native.audioPassthrough?.length,
    hdr: hdr(document.display?.dynamicRanges) || !!native.video?.some(value => hdr(value.dynamicRanges))};
}
