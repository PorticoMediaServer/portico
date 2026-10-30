import {readSettingsBinding, type SettingsBinding, type SettingsBindingSources, type SettingsBindingValue} from '@core/presentation/settings-bindings.ts';
import type {SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingsDevicePort} from '../../platform/settings-platform';

export function canWriteDeviceSettings(port: SettingsDevicePort | undefined): boolean {
  return !!port?.apply && !!port.getSnapshot().document;
}

/** Reads the injected app/device facade through the canonical binding, including its genuine defaults. */
export function readDeviceSettingsBinding(binding: SettingsBinding, port: SettingsDevicePort | undefined, sources: SettingsBindingSources, rowId: string): SettingsBindingValue | undefined {
  if (binding.form !== 'device') return undefined;
  const document = port?.getSnapshot().document;
  if (!document) return undefined;
  const value = readSettingsBinding(binding, {...sources, configuration: undefined, device: document.values}, rowId);
  if (!value) return undefined;
  const defaults = readSettingsBinding(binding, {...sources, configuration: undefined, device: document.defaults}, rowId);
  return {...value, default: defaults?.current};
}

/** No optimistic local mirror. The facade owns CAS, persistence and snapshot publication. */
export async function writeDeviceSettingsBinding(port: SettingsDevicePort | undefined, values: Readonly<Record<string, SettingsValue>>, signal: AbortSignal, isCurrent: () => boolean): Promise<void> {
  const current = () => !signal.aborted && isCurrent();
  const document = current() ? port?.getSnapshot().document : undefined;
  if (!port?.apply || !document || !current()) throw new DOMException('Settings device scope closed', 'AbortError');
  await port.apply(document.revision, values, signal);
  if (!current()) throw new DOMException('Settings device scope closed', 'AbortError');
}
