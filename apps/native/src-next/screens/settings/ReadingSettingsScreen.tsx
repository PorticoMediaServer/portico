import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {changedFromDefault, readingSettingsProperty} from '../../../../../packages/client-core/src/presentation/settings-bindings';
import {readingPreferencesPatch} from '../../../../../packages/client-core/src/preference-effects';
import type {SettingsRow as ReadingRow} from '../../../../../packages/client-core/src/presentation/settings-spec';
import type {PreferenceValue} from '../../../../../packages/client-core/src/preferences';
import {Button, Segmented, Select, Switch} from '../../kit/controls';
import {DefaultHint, SettingsRow} from '../../kit/shell';
import {StateView} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import {shellType} from '../../kit/shell/visual';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeReadingSettingsSection, type NativeReadingSettingsPreferences} from './reading-preferences';
import {StandardBackdrop} from '../../kit/artwork';

export function NativeReadingSettingsScreen({source, runtime, current, onBack}: {
  source: NativeReadingSettingsPreferences; runtime: NativeShellRuntime; current: () => boolean; onBack: () => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const state = useSyncExternalStore(source.subscribe, source.getState), values = source.getSnapshot();
  const [advanced, setAdvanced] = useState(false);
  const currentRoute = useRef(current); currentRoute.current = current;
  useEffect(() => {if (currentRoute.current()) void source.refresh();}, [source]);
  const change = (row: ReadingRow, value: PreferenceValue | null) => {
    if (!current() || state.busy) return;
    void source.setRow(row.id, value).catch(error => {if (source.current() && source.getState().error === undefined) runtime.reportError(error);});
  };
  const groups = [...(nativeReadingSettingsSection.groups ?? []), ...(advanced ? nativeReadingSettingsSection.advanced?.groups ?? [] : [])];
  return <View testID="native-reading-settings" style={{flex: 1, paddingTop: insets.top + 8}}><StandardBackdrop/>
    <View style={{paddingHorizontal: 20, paddingBottom: 20, gap: 12}}>
      <Button testID="reading-settings-back" icon="i-left" variant="text" size="small" onPress={onBack} style={{alignSelf: 'flex-start'}}>{t('action.back')}</Button>
      <Text accessibilityRole="header" style={[shellType(32, 800, 'normal', -.035), {color: theme.colors.text}]}>{t(nativeReadingSettingsSection.title)}</Text>
    </View>
    <ScrollView contentContainerStyle={{paddingHorizontal: 20, paddingBottom: 84 + insets.bottom, gap: 24}}>
      {state.error !== undefined && <StateView {...runtime.error(state.error, () => {void source.refresh();})}/>}
      {groups.map(group => <View key={group.title} style={{gap: 10}}>
        {group.title && <Text style={[shellType(18, 700), {color: theme.colors.textTertiary}]}>{t(group.title)}</Text>}
        <View style={{borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>{group.rows.map((row, index) => {
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
          return <View key={row.id}>
            <SettingsRow testID={`reading-settings-row-${row.id}`} label={t(row.label)} description={row.help ? t(row.help) : undefined} control={control} first={index === 0}/>
            <View style={{paddingHorizontal: 14}}><DefaultHint testID={`reading-settings-default-${row.id}`} changed={changedFromDefault(value, field.default)} value={defaultText} onReset={() => change(row, null)}/></View>
          </View>;
        })}</View>
      </View>)}
      <Button testID="reading-settings-advanced" variant="glass" trailingIcon={advanced ? 'i-up' : 'i-down'} onPress={() => setAdvanced(value => !value)}>{t('settings.section.advanced')}</Button>
    </ScrollView>
  </View>;
}
