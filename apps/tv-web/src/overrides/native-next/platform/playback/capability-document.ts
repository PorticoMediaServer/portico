import type {CapabilityDocument, CapabilityOverrides} from '../../../../../../../packages/client-core/src/client-profile';
import type {NativeFacts} from '../../../../../../native/src-next/platform/playback/device-profile';
import {probedSettings} from '../../../../../../vega/src/bridge/capability-profile';
import {tvCapabilityDocument} from '../../../../capabilities/document';

export type {CapabilityDocument};
export type CapabilityDocumentInput = Readonly<{
  overrides?: CapabilityOverrides; native: NativeFacts | null; appVersion: string;
  device: Readonly<{television: boolean; pad: boolean; osVersion?: string}>;
}>;

/** The shared publishing lifecycle with this app's document (`src/capabilities`), one engine.
 * The probed document arrives under the name the Fire TV bridge gives it (`vegaProfile`). */
export function deviceCapabilityDocument(input: CapabilityDocumentInput): CapabilityDocument {
  const facts = input.native as (NativeFacts & {model?: string; osVersion?: string; vegaProfile?: CapabilityDocument | null}) | null;
  const document = tvCapabilityDocument({model: facts?.model, osVersion: facts?.osVersion ?? input.device.osVersion, appVersion: input.appVersion}, input.overrides, facts?.vegaProfile ?? null);
  return {...document, audioDecode: []};
}
export function nativeEngineCapabilities(document: CapabilityDocument) {return document.engines.native;}
export const probedDevice = probedSettings;
