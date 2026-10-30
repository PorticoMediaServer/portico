import {settingsBinding, readSettingsBinding, settingsBindingPatch, type SettingsBindingValue} from '../../../../../packages/client-core/src/presentation/settings-bindings';
import {nativeDeviceSettings, type NativeDeviceSettings} from '../../platform/device-settings';

const unavailable = () => Object.assign(new Error('native_device_setting_unsupported'), {code: 'unsupported'});

/** A Settings device row resolves only through the installed device source. */
export function readNativeDevicePreference(rowId: string, source: NativeDeviceSettings): SettingsBindingValue | undefined {
  const binding = settingsBinding(rowId);
  if (!binding || binding.form !== 'device') return undefined;
  if (binding.fields) {
    // The TV output row composes one choice from two device keys. A key that was
    // never overridden reads as off for that choice only; the observed capability
    // is shown separately and is never written here.
    if (binding.codec !== 'tv-support') return undefined;
    const device: Record<string, unknown> = {};
    for (const key of Object.values(binding.fields)) {
      if (!source.supports(key)) return undefined;
      device[key] = source.read(key) === true;
    }
    return readSettingsBinding(binding, {device}, rowId);
  }
  const key = binding.key;
  if (!source.supports(key)) return undefined;
  const value = source.read(key);
  if (value === undefined) return undefined;
  return readSettingsBinding(binding, {device: {[key]: value}}, rowId);
}

/** The canonical row patch must land as one supported device key; the result is
 * the source's committed record, never the asked value or a local echo. */
export async function writeNativeDevicePreference(rowId: string, value: unknown, source: NativeDeviceSettings): Promise<SettingsBindingValue> {
  const binding = settingsBinding(rowId);
  if (!binding || binding.form !== 'device') throw unavailable();
  if (binding.fields) {
    if (binding.codec !== 'tv-support') throw unavailable();
    const patch = settingsBindingPatch(binding, value);
    const keys = Object.keys(patch.values);
    if (patch.form !== 'device' || keys.length !== Object.keys(binding.fields).length) throw unavailable();
    for (const key of keys) {
      if (!source.supports(key) || typeof patch.values[key] !== 'boolean') throw unavailable();
      await source.write(key, patch.values[key] as boolean);
    }
    const composed = readNativeDevicePreference(rowId, source);
    if (!composed) throw new Error('native_device_preference_unreadable');
    return composed;
  }
  const key = binding.key;
  if (!source.supports(key)) throw unavailable();
  const patch = settingsBindingPatch(binding, value);
  const keys = Object.keys(patch.values);
  if (patch.form !== 'device' || keys.length !== 1 || keys[0] !== key) throw unavailable();
  const written = patch.values[key];
  if (typeof written !== 'string' && typeof written !== 'boolean') throw unavailable();
  await source.write(key, written);
  const confirmed = readNativeDevicePreference(rowId, source);
  if (!confirmed) throw new Error('native_device_preference_unreadable');
  return confirmed;
}

export type NativeDevicePreferenceOwner = Readonly<{current: () => boolean; subscribePreferences: (listener: () => void) => () => void}>;

/** Borrows the one bootstrap-bound device source. No second store, source
 * cache, cloned owner, controller or dispose port is created here. */
export function bindNativeDevicePreferences(owner: NativeDevicePreferenceOwner) {
  const source = nativeDeviceSettings(owner);
  return Object.freeze({
    read: (rowId: string) => readNativeDevicePreference(rowId, source),
    write: (rowId: string, value: unknown) => writeNativeDevicePreference(rowId, value, source),
    subscribe: (listener: () => void): (() => void) => source.subscribe(listener),
  });
}
