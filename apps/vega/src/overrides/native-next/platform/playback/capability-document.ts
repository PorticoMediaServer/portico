import type {CapabilityDocument, CapabilityOverrides} from '../../../../../../../packages/client-core/src/client-profile';
import type {NativeFacts} from '../../../../../../native/src-next/platform/playback/device-profile';
import {vegaCapabilityDocument, probedSettings} from '../../../../bridge/capability-profile';

export type {CapabilityDocument};
export type CapabilityDocumentInput = Readonly<{
  overrides?: CapabilityOverrides; native: NativeFacts | null; appVersion: string;
  device: Readonly<{television: boolean; pad: boolean; osVersion?: string}>;
}>;

/** Shared publishing lifecycle, actual Vega decoder/output producer, one W3C engine. */
export function deviceCapabilityDocument(input: CapabilityDocumentInput): CapabilityDocument {
  const facts = input.native as (NativeFacts & {vegaProfile?: CapabilityDocument | null}) | null;
  const document = vegaCapabilityDocument({...facts, appVersion: input.appVersion}, null, input.overrides, facts?.vegaProfile ?? null);
  return {...document, audioDecode: []};
}
export function nativeEngineCapabilities(document: CapabilityDocument) {return document.engines.native;}
export const probedDevice = probedSettings;
