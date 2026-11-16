import type {InstallationIdentity} from '../../../../../../packages/client-core/src/installation';
import {createNativeInstallationHost} from '../../../../../native/src-next/platform/installation-host';
import {deviceKindName, platformId} from '../../../platform/identity';

const invalid = () => Object.assign(new Error('invalid_installation_metadata'), {code: 'invalid_installation_metadata'});
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);

/** The set's own name or model, with the product noun ("LG TV") only when it has neither, as on Fire TV. */
function description(raw: unknown): Pick<InstallationIdentity, 'name' | 'appVersion'> {
  if (!object(raw) || typeof raw.deviceName !== 'string' || typeof raw.appVersion !== 'string') throw invalid();
  const version = raw.appVersion.trim(), name = raw.deviceName.trim();
  if (!version || version.length > 64 || !/^[\x21-\x7e]+$/.test(raw.appVersion) || /[\x00-\x1f\x7f]/.test(raw.deviceName)) throw invalid();
  const model = typeof raw.model === 'string' ? raw.model.trim() : '';
  const deviceName = name || model || deviceKindName;
  if (!deviceName || /[\r\n\x00-\x1f\x7f]/.test(deviceName)) throw invalid();
  return {name: deviceName, appVersion: version};
}

export const beginNativeInstallation = createNativeInstallationHost({platform: () => platformId, description});
