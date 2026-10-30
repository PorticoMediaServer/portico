import {settingsRowIndex, type SettingsOption} from '@core/presentation/settings-spec.ts';
import {settingsRowVisible, type SettingsEnvironment} from '@core/presentation/settings-visibility.ts';

/** OS variants are structure, not aliases or extra preference scopes. Keep exact canonical sets. */
export function settingsPresetOptions(options: readonly SettingsOption[], environment: SettingsEnvironment): readonly SettingsOption[] {
  return options.map(option => {
    if (!option.sets) return option;
    const sets = Object.fromEntries(Object.entries(option.sets).filter(([id]) => {
      const row = settingsRowIndex.get(id)?.row;
      return row === undefined || settingsRowVisible(row, environment);
    }));
    return {...option, sets: Object.keys(sets).length ? sets : undefined};
  });
}
