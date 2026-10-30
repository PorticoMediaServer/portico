import React, {useState} from 'react';
import {Text, View} from 'react-native';
import type {RuntimeSettings} from '../../../../../../../packages/client-core/src/console';
import type {LogSettings} from '../../../../../../../packages/client-core/src/server-administration';
import type {LogsDraft, MaintenanceDocument, MaintenanceSettings} from '../../../../../../../packages/client-core/src/server-admin/server-forms';
import {applySettingsBindingPatch, exactPresetValue, presetPatch, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex, settingsSpec, type SettingsRow as SpecRow} from '../../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n, type MessageId} from '../../../../../../../packages/i18n/src';
import {Button, Select, TextField} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {NativeSettingControl} from '../SettingControl';
import {settingChoice, settingNumber} from '../settings-values';
import {nativeStorageOwner} from './space-source';
import {useFormState, useStorageSet, type StorageForms} from './forms';

type Translate = typeof defaultI18n.t;
type NumberEditor = Readonly<{label: string; value: string; min: number; max: number; done: (value: number) => void}>;
const bounds: Readonly<Record<string, readonly [number, number]>> = {
  'storage.analysisSpace': [1, 16384], 'storage.analysisStage': [1, 4096], 'storage.subtitleTracks': [32, 4096], 'storage.subtitleFile': [32, 512],
  'storage.keepNotifications': [1, 180], 'storage.backupKeepExact': [1, 365],
};

/** The Storage page owns the forms and one Save, including its main backup settings. */
export function LimitsTab({group, t, customBackupKeep = false, onOpenPage}: {group: StorageForms; t: Translate; customBackupKeep?: boolean; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const theme = useTheme(), {forms} = group, state = useStorageSet(group);
  const runtimeState = useFormState<RuntimeSettings, unknown>(forms.runtime);
  const maintenanceState = useFormState<MaintenanceSettings, MaintenanceDocument>(forms.maintenance);
  const logsState = useFormState<LogsDraft, LogSettings>(forms.logs);
  const [editing, setEditing] = useState<NumberEditor>(), [value, setValue] = useState(''), [customFiles, setCustomFiles] = useState(false);
  const runtimeDraft = runtimeState.draft, maintenanceDraft = maintenanceState.draft, logsDraft = logsState.draft;
  const sources = {forms: {runtime: runtimeDraft, maintenance: maintenanceDraft}};
  const read = (id: string) => {const binding = settingsBinding(id); return binding ? readSettingsBinding(binding, sources, id)?.current : undefined;};
  const change = (row: SpecRow, next: unknown) => {
    if (state.saving || !nativeStorageOwner(group.context)) return;
    const binding = settingsBinding(row.id); if (!binding || !['runtime', 'maintenance'].includes(binding.form)) return;
    const patch = settingsBindingPatch(binding, next, sources);
    forms[patch.form as keyof typeof forms].set(current => applySettingsBindingPatch(current, patch));
  };
  const open = (label: string, current: number, min: number, max: number, done: (value: number) => void) => {
    if (state.saving || !nativeStorageOwner(group.context)) return;
    setEditing({label, value: String(current), min, max, done}); setValue(String(current));
  };
  const filesRow = settingsRowIndex.get('storage.keepFiles')!.row;
  const fileOptions = filesRow.control.options ?? [];
  const filePreset = exactPresetValue(fileOptions, read), showFileChildren = customFiles || filePreset === 'custom';
  const keepRow = settingsRowIndex.get('storage.backupKeep')!.row;
  const renderRow = (row: SpecRow): React.ReactNode => {
    if (row.id === 'storage.keepHistory' || row.id === 'storage.keepLogsLink') return <Button key={row.id} variant="text" onPress={() => {if (group.context.current()) onOpenPage(row.id === 'storage.keepHistory' ? 'history' : 'troubleshooting');}}>{t(row.control.label ?? row.label)}</Button>;
    if (row.id === 'storage.keepFiles') {
      if (!maintenanceDraft || !fileOptions.some(option => option.sets && Object.keys(option.sets).every(id => read(id) !== undefined))) return null;
      return <View key={row.id} style={{gap: 10}}><NativeSettingControl row={row} value={customFiles ? 'custom' : filePreset} t={t} disabled={state.saving} onCustom={() => setCustomFiles(true)}
        onChange={choice => {
          if (state.saving || !nativeStorageOwner(group.context)) return;
          const values = presetPatch(fileOptions, choice, read);
          forms.maintenance.set((draft: MaintenanceSettings) => Object.entries(values).reduce((current, [id, next]) => applySettingsBindingPatch(current, settingsBindingPatch(settingsBinding(id)!, next, {forms: {maintenance: current}})), draft));
          setCustomFiles(false);
        }}/>{showFileChildren && row.children?.map(renderRow)}</View>;
    }
    if (row.id === 'storage.backupKeepExact' && !(customBackupKeep || settingChoice(keepRow, read('storage.backupKeep')) === 'custom')) return null;
    const binding = settingsBinding(row.id), current = read(row.id);
    if (!binding || current === undefined || !['runtime', 'maintenance'].includes(binding.form)) return null;
    if (row.id === 'storage.backupDays' || row.id === 'storage.backupWindowLength') return null;
    if (row.control.type === 'select' && row.control.options && binding.codec === 'number-choice') {
      const shown = settingChoice(row, current), limits = bounds[row.id] ?? [1, maintenanceState.status?.retentionMaxima[binding.key.replace(/^retention\./, '')] ?? 3650];
      const options = row.control.options.filter(option => option.value !== 'custom').map(option => ({value: String(option.value), label: t(option.label)}));
      if (shown === 'custom') options.push({value: 'custom', label: t('next.settings.customValue', {value: String(current)})});
      options.push({value: 'custom-edit', label: t('next.settingsSpec.server.general.groups.2.rows.0.control.options.2.label')});
      return <Select key={row.id} testID={row.id} rowLabel={t(row.label)} description={row.help ? t(row.help) : undefined} value={shown} options={options} disabled={state.saving}
        onValueChange={next => next === 'custom-edit' ? open(t(row.label), Number(current), limits[0], limits[1], number => change(row, String(number))) : change(row, next)}/>;
    }
    if (row.control.type === 'number') return <View key={row.id} style={{gap: 6}}><Text style={{color: theme.colors.text}}>{t(row.label)}</Text><Button testID={row.id} variant="glass" disabled={state.saving} onPress={() => open(t(row.label), Number(current), row.control.min ?? 0, row.control.max ?? Number.MAX_SAFE_INTEGER, number => change(row, number))}>{String(current)}</Button></View>;
    return <NativeSettingControl key={row.id} row={row} value={current} t={t} disabled={state.saving} onChange={next => change(row, next)}/>;
  };
  const retention = (label: string, current: number, maximum: number, done: (value: number) => void, minimum = 1) => {
    const choices = [0, 1, 7, 14, 30, 60, 90, 180, 365, 1095].filter(number => number >= minimum && number <= maximum);
    const options = choices.map(number => ({value: String(number), label: number === 0 ? t('pref.value.playback.introSkip.off') : `${defaultI18n.number(number)} ${t('settings.unit.days')}`}));
    if (!choices.includes(current)) options.push({value: 'custom', label: t('next.settings.customValue', {value: current})});
    options.push({value: 'custom-edit', label: t('next.settingsSpec.server.general.groups.2.rows.0.control.options.2.label')});
    return <Select rowLabel={label} value={choices.includes(current) ? String(current) : 'custom'} options={options} disabled={state.saving}
      onValueChange={next => {if (state.saving || !nativeStorageOwner(group.context)) return;if (next === 'custom-edit') open(label, current, minimum, maximum, done);else if (choices.includes(Number(next))) done(Number(next));}}/>;
  };
  const section = settingsSpec.find(section => section.scope === 'server' && section.id === 'storage');
  const customNumber = editing ? settingNumber(value, {min: editing.min, max: editing.max, step: 1}) : {valid: false};
  const expertRetention = <>
    {runtimeDraft && retention(t('web.logs.retentionTasks'), runtimeDraft.jobDays, 30, jobDays => forms.runtime.set((draft: RuntimeSettings) => ({...draft, jobDays})))}
    {maintenanceDraft && maintenanceState.status?.storageCategories.filter(category => !['conversions', 'prepared', 'downloads', 'subtitles', 'trickplay'].includes(category.retentionKey)).map(category => <View key={category.id}>
      {retention(category.name, maintenanceDraft.retention[category.retentionKey] ?? 0, maintenanceState.status?.retentionMaxima[category.retentionKey] ?? 3650, days => forms.maintenance.set((draft: MaintenanceSettings) => ({...draft, retention: {...draft.retention, [category.retentionKey]: days}})), 0)}
    </View>)}
  </>;
  return <View style={{gap: 18}} testID="server-storage-limits">
    {section?.advanced?.groups?.map((expertGroup, index) => {
      const rows = expertGroup.rows.map(renderRow).filter(Boolean);
      return rows.length ? <View key={index} style={{gap: 8}}>{expertGroup.title && <Text style={[shellType(16, 700), {color: theme.colors.textSecondary}]}>{t(expertGroup.title)}</Text>}<View style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 14, gap: 12}}>{rows}{index === 0 && expertRetention}</View></View> : null;
    })}
    {logsDraft && <View style={{gap: 10}}><Text style={[shellType(16, 700), {color: theme.colors.textSecondary}]}>{t('server.logsByKind')}</Text><View style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 14, gap: 12}}>
      {logsDraft.retention.map(entry => <View key={entry.category}>{retention(t('settings.server.retention.logs', {category: t(`server.logCategory.${entry.category}` as MessageId)}), entry.days, 90, days => forms.logs.set((draft: LogsDraft) => ({...draft, retention: draft.retention.map(row => row.category === entry.category ? {...row, days} : row)})))}</View>)}
    </View></View>}
    <BottomSheet open={Boolean(editing)} onOpenChange={next => {if (!next) setEditing(undefined);}} testID="server-storage-number" keyboardAvoiding title={editing?.label}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" onPress={() => setEditing(undefined)}>{t('action.cancel')}</Button>
        <Button disabled={state.saving || !customNumber.valid} onPress={() => {if (editing && customNumber.valid && typeof customNumber.value === 'number' && nativeStorageOwner(group.context)) {editing.done(customNumber.value);setEditing(undefined);}}}>{t('action.save')}</Button></View>}>
      <TextField label={editing?.label ?? ''} value={value} onChangeText={setValue} keyboardType="number-pad" error={!customNumber.valid ? t('error.invalidInput') : undefined} autoFocus/>
    </BottomSheet>
  </View>;
}
