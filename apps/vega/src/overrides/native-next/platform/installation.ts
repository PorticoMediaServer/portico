import {platformId, deviceKindName} from '../../../platform';
import type {InstallationIdentity} from '../../../../../../packages/client-core/src/installation';
import {createNativeInstallationHost} from '../../../../../native/src-next/platform/installation-host';

const invalid = () => Object.assign(new Error('invalid_installation_metadata'), {code: 'invalid_installation_metadata'});
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);

/** Actual Vega classification; never impersonate an Apple or Android installation. */
function installationPlatform(): string {return platformId;}

// The actual device name/model, with the sanctioned product noun only when unnamed.
function description(raw: unknown): Pick<InstallationIdentity, 'name' | 'appVersion'> {
  if (!object(raw) || typeof raw.deviceName !== 'string' || typeof raw.appVersion !== 'string') throw invalid();
  const version = raw.appVersion.trim(), name = raw.deviceName.trim();
  // The shipped bundle's version must fit the canonical header unchanged. Never substitute a JS version.
  if (!version || version.length > 64 || !/^[\x21-\x7e]+$/.test(raw.appVersion) || /[\x00-\x1f\x7f]/.test(raw.deviceName)) throw invalid();
  const model = typeof raw.model === 'string' ? raw.model.trim() : '';
  const deviceName = name || model || deviceKindName;
  if (!deviceName || /[\r\n\x00-\x1f\x7f]/.test(deviceName)) throw invalid();
  return {name: deviceName, appVersion: version};
}

export const beginNativeInstallation = createNativeInstallationHost({platform: installationPlatform, description});
