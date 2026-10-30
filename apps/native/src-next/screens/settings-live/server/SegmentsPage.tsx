import React, {useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import type {RuntimeSettings} from '../../../../../../packages/client-core/src/console';
import {applySettingsBindingPatch, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex, settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {StateView} from '../../../kit/states';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeSettingControl} from './SettingControl';
import {settingEnabled, settingsSummary} from './settings-values';
import {nativeStorageOwner} from './storage/space-source';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';

const IDS = ['runtime'] as const;

/** Server settings rows use revisioned forms, never the account preference renderer. */
export function NativeServerSegments({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <Segments key={group.mount} group={group} ports={ports} runtime={runtime}/> : null;
}
function Segments({ports, runtime, group}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; group: StorageForms}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const {forms, set} = group, state = useStorageSet(group);
  const settings = useFormState<RuntimeSettings, unknown>(forms.runtime);
  const [invalid, setInvalid] = useState<ReadonlySet<string>>(new Set()), [draftKey, setDraftKey] = useState(0);
  const sources = {forms: {runtime: settings.draft}};
  const read = (row: SpecRow) => {const binding = settingsBinding(row.id);return binding?.form === 'runtime' ? readSettingsBinding(binding, sources, row.id) : undefined;};
  const current = () => nativeStorageOwner(group.context) && !set.getSnapshot().saving;
  const renderRow = (row: SpecRow) => {
    const value = read(row)?.current;
    if (value === undefined) return null;
    return <NativeSettingControl key={`${row.id}:${draftKey}`} row={row} value={value} t={t}
      disabled={state.saving || !settingEnabled(row, id => {const other = settingsRowIndex.get(id)?.row;return other ? read(other)?.current : undefined;})}
      onValidity={valid => setInvalid(previous => {const next = new Set(previous);if (valid) next.delete(row.id);else next.add(row.id);return next;})}
      onChange={next => {if (!current()) return;const patch = settingsBindingPatch(settingsBinding(row.id)!, next, sources);forms.runtime.set((draft: RuntimeSettings) => applySettingsBindingPatch(draft, patch));}}/>;
  };
  const section = settingsSpec.find(section => section.scope === 'server' && section.id === 'segments');
  const save = () => {if (current() && !invalid.size) void set.save();};
  if (settings.loadError !== undefined && !settings.draft) return <StateView {...runtime.error(settings.loadError, () => {void forms.runtime.load();}, 'server-console')}/>;
  return <ScrollView style={{flex: 1}} contentContainerStyle={{paddingHorizontal: 20, paddingBottom: 32}}>
    {settings.draft && renderRow(settingsRowIndex.get('segments.mode')!.row)}
    {ports.renderSettingsPanel('server.segments', runtime)}
    <NativeAdvancedSection testID="server-segments-advanced" summary={settingsSummary(section?.advanced?.groups?.flatMap(group => group.rows) ?? [], read, t)}>
      {section?.advanced?.groups?.map((group, index) => {
        const rows = group.rows.map(renderRow).filter(Boolean);
        return rows.length ? <View key={index} style={{gap: 10}}>{group.title && <Text style={[shellType(16, 700), {color: theme.colors.textSecondary}]}>{t(group.title)}</Text>}{rows}</View> : null;
      })}
    </NativeAdvancedSection>
    {state.dirty && <View style={{flexDirection: 'row', gap: 8, marginTop: 12}}>
      <Button variant="glass" disabled={state.saving} onPress={() => {if (current()) {set.discard();setInvalid(new Set());setDraftKey(key => key + 1);}}}>{t('action.discard')}</Button>
      <Button loading={state.saving} disabled={Boolean(state.problem) || invalid.size > 0} onPress={save}>{t('action.save')}</Button>
    </View>}
    {state.problem && <StateView title={t(state.problem as MessageId)} tone="error"/>}
    {settings.saveError !== undefined && <StateView {...runtime.error(settings.saveError, save, 'server-console')}/>}
  </ScrollView>;
}
