import React from 'react';
import {Text, View} from 'react-native';
import {settingsBinding, readSettingsBinding, settingsBindingPatch} from '../../../../../../../packages/client-core/src/presentation/settings-bindings';
import type {MaintenanceSettings} from '../../../../../../../packages/client-core/src/server-admin/server-forms';
import {defaultI18n, type MessageId} from '../../../../../../../packages/i18n/src';
import {Select} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {nativeStorageOwner} from './space-source';
import type {StorageForms} from './forms';
const DAYS=['monday','tuesday','wednesday','thursday','friday','saturday','sunday'] as const;
const MODES=['nightly','weekdays','weekends','weekly','custom','never'] as const;
/** Canonical adapters alter the real backup-task window, preserving all other tasks. */
export function NativeBackupSchedule({group,draft,saving,t,placement='all',customDraft,onCustomDraftChange,onAdvanced}:{group:StorageForms;draft:MaintenanceSettings;saving:boolean;t:typeof defaultI18n.t;placement?:'all'|'main'|'advanced';customDraft?:MaintenanceSettings;onCustomDraftChange?:(draft:MaintenanceSettings|undefined)=>void;onAdvanced?:()=>void}) {
  const theme=useTheme(),sources={forms:{maintenance:{...draft}}},window=draft.windows.find(value=>value.tasks.includes('backup'));
  const read=(id:string)=>{const binding=settingsBinding(id);return binding?readSettingsBinding(binding,sources,id)?.current:undefined;};
  const write=(id:string,value:unknown)=>{if(saving||!nativeStorageOwner(group.context)||group.forms.maintenance.getSnapshot().draft!==draft)return;const binding=settingsBinding(id);if(!binding)return;const patch=settingsBindingPatch(binding,value,sources);if(patch.form!=='maintenance')return;group.forms.maintenance.set((old:MaintenanceSettings)=>({...old,...patch.values}));onCustomDraftChange?.(id==='storage.backupSchedule'?value==='custom'?group.forms.maintenance.getSnapshot().draft:undefined:customDraft===draft?group.forms.maintenance.getSnapshot().draft:undefined);};
  const mode=customDraft===draft?'custom':read('storage.backupSchedule'),minute=read('storage.backupTime'),hours=read('storage.backupWindowLength'),days=read('storage.backupDays') as Readonly<Record<string,string>>|undefined;
  if(!window||typeof mode!=='string')return null;
  const time=typeof minute==='string'?Number(minute):minute, duration=typeof hours==='string'?Number(hours):hours;
  const timeOptions=Array.from({length:24},(_,hour)=>({value:String(hour*60),label:t(`next.settingsSpec.server.storage.groups.1.rows.2.children.0.control.options.${hour}.label` as MessageId)}));
  if(typeof time==='number'&&!timeOptions.some(option=>option.value===String(time)))timeOptions.push({value:String(time),label:`${String(Math.floor(time/60)).padStart(2,'0')}:${String(time%60).padStart(2,'0')}`});
  const durationOptions=[1,2,4,8].map((value,index)=>({value:String(value),label:t(`next.settingsSpec.server.storage.advanced.groups.3.rows.3.control.options.${index}.label` as MessageId)}));
  if(typeof duration==='number'&&!durationOptions.some(option=>option.value===String(duration)))durationOptions.push({value:String(duration),label:`${defaultI18n.number(duration)} ${t('server.windows.hours')}`});
  return <View style={{gap:12}}>
    {placement!=='advanced'&&<Select testID="storage.backupSchedule" label={t('next.settingsSpec.server.storage.groups.1.rows.2.label')} value={mode} disabled={saving} options={MODES.map((value,index)=>({value,label:t(`next.settingsSpec.server.storage.groups.1.rows.2.control.options.${index}.label` as MessageId)}))} onValueChange={value=>{if(MODES.some(mode=>mode===value)){write('storage.backupSchedule',value);if(value==='custom')onAdvanced?.();}}}/>}
    {placement!=='advanced'&&mode!=='never'&&typeof time==='number'&&<Select testID="storage.backupTime" label={t('next.settingsSpec.server.storage.groups.1.rows.2.children.0.label')} value={String(time)} options={timeOptions} disabled={saving} onValueChange={value=>{if(timeOptions.some(option=>option.value===value))write('storage.backupTime',value);}}/>}
    {placement!=='main'&&mode!=='never'&&typeof duration==='number'&&<Select testID="storage.backupWindowLength" label={t('next.settingsSpec.server.storage.advanced.groups.3.rows.3.label')} description={t('next.settingsSpec.server.storage.advanced.groups.3.rows.3.help')} value={String(duration)} options={durationOptions} disabled={saving} onValueChange={value=>{if(durationOptions.some(option=>option.value===value))write('storage.backupWindowLength',value);}}/>}
    {placement!=='main'&&mode==='custom'&&days&&<View style={{gap:8}}><Text style={[shellType(15,600),{color:theme.colors.text}]}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.4.label')}</Text>{DAYS.map((day,index)=><Select key={day} testID={`storage.backupDays.${day}`} rowLabel={new Intl.DateTimeFormat(defaultI18n.locale,{weekday:'long',timeZone:'UTC'}).format(new Date(Date.UTC(2024,0,index+1)))} value={days[day]} disabled={saving} options={[{value:'runs',label:t('next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.columns.0.label')},{value:'skips',label:t('next.settingsSpec.server.storage.advanced.groups.3.rows.4.control.columns.1.label')}]} onValueChange={value=>write('storage.backupDays',{...days,[day]:value})}/>)}</View>}
  </View>;
}
