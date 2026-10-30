import {settingsRowIndex, type SettingsRow} from '@core/presentation/settings-spec.ts';

/**
 * A preset row is offered only when every row it sets has a real engine, the
 * same rule the shared reader applies to one row. A missing target, an empty
 * preset or a cycle refuses it, so a preset can never vouch for a row with no
 * consumer. Returns undefined when the row is not a preset at all.
 */
export function presetEnginesAvailable(row: SettingsRow, engine: (row: SettingsRow) => boolean): boolean | undefined {
  const presets = row.control.options?.filter(option => option.sets);
  if (!presets?.length) return undefined;
  const targets = [...new Set(presets.flatMap(option => Object.keys(option.sets ?? {})))];
  if (!targets.length) return false;
  return targets.every(id => {
    const target = settingsRowIndex.get(id)?.row;
    if (!target) return false;
    return presetEnginesAvailable(target, engine) ?? engine(target);
  });
}
