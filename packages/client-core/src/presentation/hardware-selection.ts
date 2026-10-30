import type {HardwareDeviceFact} from '../telemetry.ts';
import {settingsRowIndex} from './settings-spec.ts';

export type HardwareSelectionChoice = Readonly<{value: string; label: string; hint?: string; locked?: boolean}>;

/** `streaming.hardware`: Automatic and Processor only from the spec, then each adapter the
 * hardware report found, by its opaque id and driver name. A saved adapter that is no longer
 * found stays visible as the current choice, never offered; the server converts on the
 * processor until the owner picks again. */
export function hardwareSelectionChoices(devices: readonly HardwareDeviceFact[], current: unknown, t: (id: never) => string): readonly HardwareSelectionChoice[] {
  const translate = t as unknown as (id: string) => string;
  const row = settingsRowIndex.get('streaming.hardware')?.row;
  const fixed = (row?.control.options ?? []).map(option => ({value: String(option.value), label: translate(option.label), ...(option.hint ? {hint: translate(option.hint)} : {})}));
  const found = devices.filter(device => device.id && device.name).map(device => ({value: device.id, label: device.name}));
  const choices: HardwareSelectionChoice[] = [...fixed, ...found];
  if (typeof current === 'string' && current && !choices.some(choice => choice.value === current)) choices.push({value: current, label: translate('next.settings.hardware.deviceGone'), locked: true});
  return Object.freeze(choices);
}
