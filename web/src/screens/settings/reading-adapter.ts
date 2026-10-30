import {readingPreferences, readingPreferencesPatch, type ReadingPreferencesChange} from '@core/preference-effects.ts';
import {readingSettingsProperty} from '@core/presentation/settings-bindings.ts';
import type {ReadingSettingsAdapter} from './controller';

/** The canonical fifteen-property projection and patch helper own every key. */
export const readingSettingsAdapter: ReadingSettingsAdapter = {
  read(rowId, snapshot) {
    const property = readingSettingsProperty(rowId);
    if (!property) throw new Error('unknown_reading_setting');
    const current = readingPreferences(snapshot)[property];
    const defaults = readingPreferences({...snapshot, effective: Object.fromEntries(snapshot.registry.fields.map(field => [field.key, field.default]))});
    const key = Object.keys(readingPreferencesPatch(snapshot, {[property]: current}))[0];
    return {current, effective: current, default: defaults[property], source: snapshot.effectiveSource[key], configOnly: false};
  },
  patch(rowId, value, snapshot) {
    const property = readingSettingsProperty(rowId);
    if (!property) throw new Error('unknown_reading_setting');
    const change: ReadingPreferencesChange = {[property]: value};
    return readingPreferencesPatch(snapshot, change);
  },
};
