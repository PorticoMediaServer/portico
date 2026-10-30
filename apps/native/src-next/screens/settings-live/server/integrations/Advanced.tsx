import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import type {RuntimeSettings} from '../../../../../../../packages/client-core/src/console';
import {readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex} from '../../../../../../../packages/client-core/src/presentation/settings-spec';
import {Button,Switch, TextField} from '../../../../kit/controls';
import {SettingsRow} from '../../../../kit/shell';
import {useTheme} from '../../../../kit/theme';
import {StateView} from '../../../../kit/states';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeAdvancedSection} from '../AdvancedSection';
import {settingNumber, settingsSummary} from '../settings-values';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from '../storage/forms';

const IDS = ['runtime'] as const;
const row = settingsRowIndex.get('integrations.requestsPerMinute')!.row;
type Field = Readonly<{text: string}>;
type Opening = {live: boolean; field: Field};
export function NativeIntegrationAdvanced({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <Advanced key={group.mount} group={group} runtime={runtime}/> : null;
}
function Advanced({group, runtime}: {group: StorageForms; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t, form = group.forms.runtime;
  const state = useFormState<RuntimeSettings, unknown>(form), save = useStorageSet(group);
  const binding = settingsBinding('integrations.requestsPerMinute')!;
  const fact = state.draft ? readSettingsBinding(binding, {forms: {runtime: state.draft}}, row.id) : undefined;
  const value = fact?.effective;
  const [field, setField] = useState<Field>({text: ''}), [opening, setOpening] = useState<Opening>();
  const active = useRef<Opening | undefined>(undefined);
  useLayoutEffect(() => {
    const next: Opening = {live: true, field}; active.current = next; setOpening(next);
    return () => {next.live = false; if (active.current === next) active.current = undefined;};
    // A new committed form owns the field; edits do not create another opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form]);
  const live = () => !!opening?.live && active.current === opening;
  const current = () => live() && opening!.field === field && form.getSnapshot().draft === state.draft && !form.getSnapshot().saving;
  const input = field.text, parsed = settingNumber(input, row.control), valid = parsed.valid;
  const replaceInput = (text: string) => {const next = {text}; if (opening) opening.field = next; setField(next);};
  useEffect(() => {
    if (live() && typeof value === 'number') replaceInput(String(value));
    // Only a new opening or producer value replaces a partly entered number.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opening, value]);
  const commit = () => {if (current() && settingNumber(opening!.field.text, row.control).valid && !group.set.getSnapshot().problem) void group.set.save();};
  const discard = () => {
    if (!current()) return;
    group.set.discard(); const saved = form.getSnapshot().draft as RuntimeSettings | undefined;
    replaceInput(saved ? String(saved.apiRequestsPerMinute) : '');
  };
  return <><View style={{marginTop:16}}>{state.draft&&<Switch checked={state.draft['integrations.compatibilityEnabled']===true} disabled={save.saving||!live()} onCheckedChange={value=>{if(live()&&!form.getSnapshot().saving)form.set((draft:RuntimeSettings)=>({...draft,'integrations.compatibilityEnabled':value}));}}>{t('integrations.compatibility.title')}</Switch>}<Text style={{color:theme.colors.textSecondary,marginTop:4}}>{t('integrations.compatibility.help')}</Text></View><NativeAdvancedSection testID="integrations-advanced" summary={fact ? settingsSummary([row], () => fact, t) : t('next.settings.loading')}>
    {state.loadError !== undefined && <><Text accessibilityRole="alert" style={{color: theme.colors.record}}>{t('error.unavailable')}</Text><Button size="compact" variant="glass" onPress={() => {if (current()) void form.load();}}>{t('action.tryAgain')}</Button></>}
    {state.draft && <SettingsRow first label={t('next.settingsSpec.server.integrations.advanced.groups.1.rows.0.label')} description={t('next.settingsSpec.server.integrations.advanced.groups.1.rows.0.help')}
      control={<TextField accessibilityLabel={t('next.settingsSpec.server.integrations.advanced.groups.1.rows.0.label')} value={input} keyboardType="number-pad" editable={live() && !save.saving} error={input && !valid ? t('error.invalidInput') : undefined} onChangeText={text => {
        if (!live() || form.getSnapshot().saving) return;
        replaceInput(text);
        const next = settingNumber(text, row.control); if (!next.valid || typeof next.value !== 'number') return;
        const patch = settingsBindingPatch(binding, next.value);
        form.set((draft: RuntimeSettings) => ({...draft, ...patch.values}));
      }}/>}/>} 

  </NativeAdvancedSection>    {save.saveError !== undefined && <StateView variant="inline" {...runtime.error(save.saveError, commit)}/>}{save.dirty&&<View style={{flexDirection:'row',gap:8,marginTop:8}}><Button variant="glass" disabled={save.saving||!live()} onPress={discard}>{t('action.discard')}</Button><Button disabled={save.saving||!live()||!valid||!!save.problem} loading={save.saving} onPress={commit}>{t('action.save')}</Button></View>}</>;
}
