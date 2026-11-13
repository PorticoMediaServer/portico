import React, {useState} from 'react';
import {View} from 'react-native';
import {settingsBinding, readSettingsBinding, settingsBindingPatch} from '../../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex} from '../../../../../../../packages/client-core/src/presentation/settings-spec';
import type {BackupSettings} from '../../../../../../../packages/client-core/src/server-admin/server-backups';
import type {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button} from '../../../../kit/controls';
import {NativeSettingControl} from '../SettingControl';

/** One draft for the real days/hour/keep fields, saved through the page's existing backup source. */
export function NativeBackupSchedule({settings, saving, t, onSave}: {settings: BackupSettings; saving: boolean; t: typeof defaultI18n.t; onSave: (change: Pick<BackupSettings, 'days' | 'hour' | 'keep'>) => void}) {
 const [draft, setDraft] = useState(settings), [chosen, setChosen] = useState(false), [valid, setValid] = useState(true);
 const sources = {forms: {backups: draft}};
 const read = (id: string) => readSettingsBinding(settingsBinding(id)!, sources, id)?.current;
 const mode = chosen ? 'custom' : read('storage.backupSchedule');
 const change = (id: string, value: unknown) => {
  if (saving) return;
  const patch = settingsBindingPatch(settingsBinding(id)!, value, sources);
  setDraft(current => ({...current, ...patch.values}));
  if (id === 'storage.backupSchedule') setChosen(value === 'custom');
 };
 const row = (id: string) => <NativeSettingControl key={id} row={settingsRowIndex.get(id)!.row} value={id === 'storage.backupSchedule' ? mode : read(id)} disabled={saving} t={t}
  onCustom={id === 'storage.backupSchedule' ? () => change(id, 'custom') : undefined} onChange={value => change(id, value)} onValidity={id === 'storage.backupKeep' ? setValid : undefined}/>;
 const dirty = JSON.stringify([draft.days, draft.hour, draft.keep]) !== JSON.stringify([settings.days, settings.hour, settings.keep]);
 return <View style={{gap:12}}>
  {row('storage.backupKeep')}{row('storage.backupSchedule')}
  {mode !== 'never' && row('storage.backupTime')}
  {mode === 'custom' && row('storage.backupDays')}
  {dirty && <View style={{flexDirection:'row',gap:8}}><Button variant="glass" disabled={saving} onPress={() => {setDraft(settings);setChosen(false);setValid(true);}}>{t('action.discard')}</Button>
   <Button loading={saving} disabled={!valid} onPress={() => {if (!saving && valid) onSave({days:draft.days,hour:draft.hour,keep:draft.keep});}}>{t('action.save')}</Button></View>}
 </View>;
}
