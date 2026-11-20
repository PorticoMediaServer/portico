import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {changedFromDefault, readingSettingsProperty} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {readingPreferencesPatch} from '../../../../../../packages/client-core/src/preference-effects';
import {Button, Segmented, Select, Switch} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {DefaultHint, SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {nativeReadingSettingsSection, type NativeReadingSettingsPreferences} from '../../../screens/settings/reading-preferences';
import {useReadingSettings} from '../../shared/settings/useReadingSettings';

export function ReadingSettingsPage({source, runtime, current, onBack}: {
  source: NativeReadingSettingsPreferences; runtime: NativeShellRuntime; current: () => boolean; onBack: () => void;
}) {
  const t = defaultI18n.t;
  const {state, values, advanced, setAdvanced, change, groups} = useReadingSettings({source, runtime, current});
  return <SettingsPageFrame testID="native-reading-settings" title={t(nativeReadingSettingsSection.title)}
    back={{label: t('action.back'), onPress: onBack, testID: 'reading-settings-back'}}>
    {state.error !== undefined && <StateView {...runtime.error(state.error, () => {void source.refresh();})}/>}
    {groups.map(group => <SectionCard key={group.title} label={group.title ? t(group.title) : undefined}>
      {group.rows.map((row, index) => {
        const property = readingSettingsProperty(row.id);
        if (!property || !row.key) throw new Error('native_reading_settings_binding_missing');
        const value = values[property], field = state.preferences.registry.fields.find(field => field.key === row.key);
        if (!field) throw new Error('native_reading_preferences_unavailable');
        const disabled = state.busy || !source.rowEnabled(row);
        const options = row.control.options?.map(option => {
          let allowed = !option.locked;
          try {readingPreferencesPatch(state.preferences, {[property]: option.value});} catch {allowed = false;}
          return {value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined, disabled: !allowed};
        });
        const choose = (next: string) => {const option = row.control.options?.find(option => String(option.value) === next); if (option) change(row, option.value);};
        const control = row.control.type === 'toggle' ? <Switch testID={row.id} checked={value === true} disabled={disabled} accessibilityLabel={t(row.label)} onCheckedChange={next => change(row, next)}/>
          : row.control.type === 'segmented' ? <Segmented testID={row.id} accessibilityLabel={t(row.label)} disabled={disabled} value={String(value)} items={options ?? []} onValueChange={choose} variant="phone"/>
            : <Select testID={row.id} accessibilityLabel={t(row.label)} disabled={disabled} value={String(value)} options={options ?? []} onValueChange={choose}/>;
        const defaultOption = row.control.options?.find(option => option.value === field.default);
        const defaultText = defaultOption ? t(defaultOption.label) : typeof field.default === 'boolean' ? t(field.default ? 'next.settings.on' : 'next.settings.off') : String(field.default);
        return <React.Fragment key={row.id}>
          <SettingsRow testID={`reading-settings-row-${row.id}`} label={t(row.label)} description={row.help ? t(row.help) : undefined} control={control} first={index === 0}/>
          <DefaultHint inset testID={`reading-settings-default-${row.id}`} changed={changedFromDefault(value, field.default)} value={defaultText} onReset={() => change(row, null)}/>
        </React.Fragment>;
      })}
    </SectionCard>)}
    <SectionCard><Button testID="reading-settings-advanced" variant="glass" trailingIcon={advanced ? 'i-up' : 'i-down'} onPress={() => setAdvanced(value => !value)}>{t('settings.section.advanced')}</Button></SectionCard>
  </SettingsPageFrame>;
}
