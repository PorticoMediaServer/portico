import {applySettingsBindingPatch, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import type {MaintenanceSettings} from '../../../../../../packages/client-core/src/server-admin/server-forms';

export type HeavyWorkRow = 'general.heavyWork' | 'general.heavyStart' | 'general.heavyLength' | 'general.heavyDays';

/** Use the canonical row codecs: the heavy window is selected by its tasks, never its position. */
export function readHeavyWork(draft: MaintenanceSettings, row: HeavyWorkRow): unknown {
  return readSettingsBinding(settingsBinding(row)!, {forms: {maintenance: draft}}, row)?.current;
}

export function patchHeavyWork(draft: MaintenanceSettings, row: HeavyWorkRow, value: unknown): MaintenanceSettings {
  return applySettingsBindingPatch(draft, settingsBindingPatch(settingsBinding(row)!, value, {forms: {maintenance: draft}}));
}
