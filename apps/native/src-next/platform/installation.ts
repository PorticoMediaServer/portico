import {Platform} from 'react-native';
import type {InstallationIdentity} from '../../../../packages/client-core/src/installation';
import {createNativeInstallationHost} from './installation-host';

const invalid = () => Object.assign(new Error('invalid_installation_metadata'), {code: 'invalid_installation_metadata'});
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);

/** Same installation classification as the pure native device-platform bridge. Vega has its own host. */
function installationPlatform(): string {
  if (Platform.OS === 'android') {
    const amazon = String((Platform.constants as {Manufacturer?: string} | undefined)?.Manufacturer ?? '').toLowerCase() === 'amazon';
    return Platform.isTV ? amazon ? 'firetv' : 'androidtv' : 'android';
  }
  if (Platform.OS === 'ios') return Platform.isTV ? 'tvos' : (Platform as {isPad?: boolean}).isPad === true ? 'ipados' : 'ios';
  throw invalid();
}

// Pure device metadata from setup-codes, without its auth/UI dependencies or invented fallbacks.
const models: readonly [RegExp, string][] = [[/^AppleTV14,/, 'Apple TV 4K'], [/^AppleTV11,/, 'Apple TV 4K'], [/^AppleTV6,/, 'Apple TV 4K'], [/^AppleTV5,/, 'Apple TV HD'], [/^iPad/, 'iPad'], [/^iPhone/, 'iPhone']];
function description(raw: unknown): Pick<InstallationIdentity, 'name' | 'appVersion'> {
  if (!object(raw) || typeof raw.deviceName !== 'string' || typeof raw.appVersion !== 'string') throw invalid();
  const version = raw.appVersion.trim(), name = raw.deviceName.trim();
  // The shipped bundle's version must fit the canonical header unchanged. Never substitute a JS version.
  if (!version || version.length > 64 || !/^[\x21-\x7e]+$/.test(raw.appVersion) || /[\x00-\x1f\x7f]/.test(raw.deviceName)) throw invalid();
  const model = typeof raw.model === 'string' ? raw.model.trim() : '';
  const deviceName = !name || /^(apple tv|iphone|ipad)$/i.test(name)
    ? models.find(([pattern]) => pattern.test(model))?.[1] || model || name
    : name;
  if (!deviceName || /[\r\n\x00-\x1f\x7f]/.test(deviceName)) throw invalid();
  return {name: deviceName, appVersion: version};
}

export const beginNativeInstallation = createNativeInstallationHost({platform: installationPlatform, description});
