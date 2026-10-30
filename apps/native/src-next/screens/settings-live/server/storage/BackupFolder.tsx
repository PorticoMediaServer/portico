import React, {useLayoutEffect, useMemo, useState} from 'react';
import {Text, View} from 'react-native';
import type {BackupSettings, BackupTransport} from '../../../../../../../packages/client-core/src/backups';
import type {SettingsBackupSource} from '../../../../../../../packages/client-core/src/server-admin/backup-source';
import {routePath} from '../../../../../../../packages/contracts/src/routes';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {FolderExplorer} from '../../owner-libraries/FolderExplorer';
import {screenScopeEnded} from '../../lifetime';
import {Button} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';

type Picker=Readonly<{abort:AbortController;settings:BackupSettings;api:BackupTransport;chosen:readonly Readonly<{path:string;name:string}>[]}>;
export function NativeBackupFolder({source,settings,api,current:ownerCurrent,failure,t}:{source:SettingsBackupSource;settings:BackupSettings;api:BackupTransport;current:()=>boolean;failure:(error:unknown)=>void;t:typeof defaultI18n.t}) {
  const theme=useTheme(),[picker,setPicker]=useState<Picker>(),[busy,setBusy]=useState(false),[uncertain,setUncertain]=useState(false);
  const view=useMemo(()=>({source,active:true,picker:undefined as Picker|undefined,flight:undefined as object|undefined,uncertain:false}),[source]);
  useLayoutEffect(()=>{view.active=true;return()=>{view.active=false;view.picker?.abort.abort();view.picker=undefined;view.flight=undefined;};},[view]);
  useLayoutEffect(()=>{if(view.picker&&view.picker.settings!==settings){view.picker.abort.abort();view.picker=undefined;setPicker(undefined);}},[view,settings]);
  const current=()=>view.active&&view.source===source&&ownerCurrent();
  const held=picker&&view.picker===picker&&!picker.abort.signal.aborted&&current()?picker:undefined;
  const close=()=>{if(!held||!current()||view.picker!==held)return;view.picker=undefined;held.abort.abort();setPicker(undefined);};
  const open=()=>{
    if(!current()||view.flight||view.uncertain||source.getSnapshot().busy||source.getSnapshot().settings!==settings)return;
    const abort=new AbortController();
    const next:Picker={abort,settings,chosen:Object.freeze(settings.backupFolder?[Object.freeze({path:settings.backupFolder,name:settings.backupFolder})]:[]),api:{request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>{
      if(!current()||view.picker!==next||abort.signal.aborted||path.split('?')[0]!==routePath('get_admin_filesystem')||method&&method!=='GET')return Promise.reject(screenScopeEnded());
      const request=new AbortController(),stop=()=>request.abort();abort.signal.addEventListener('abort',stop,{once:true});caller?.addEventListener('abort',stop,{once:true});if(caller?.aborted)request.abort();
      return api.request<T>(path,'GET',undefined,request.signal).then(value=>{if(!current()||view.picker!==next||abort.signal.aborted||request.signal.aborted)throw screenScopeEnded();return value;}).finally(()=>{abort.signal.removeEventListener('abort',stop);caller?.removeEventListener('abort',stop);});
    }}};
    view.picker?.abort.abort();view.picker=next;setPicker(next);
  };
  const chooseFolder=async(folders:readonly Readonly<{path:string;name:string}>[])=>{
    const captured=held;if(!captured||!current()||view.picker!==captured||view.flight||view.uncertain||source.getSnapshot().settings!==captured.settings||folders.length!==1||!folders[0]?.path)return;
    const flight={},patch=Object.freeze({backupFolder:folders[0].path});view.flight=flight;view.picker=undefined;captured.abort.abort();setPicker(undefined);setBusy(true);
    // Choosing the folder commits to the page-owned save; closing its explorer only retires browsing.
    try{const done=await source.save(patch);if(!done&&current()){view.uncertain=true;setUncertain(true);}}catch(error){failure(error);if(current()){view.uncertain=true;setUncertain(true);}}finally{if(view.flight===flight){view.flight=undefined;if(current())setBusy(false);}}
  };
  const reconcile=async()=>{if(!current()||view.flight)return;const flight={};view.flight=flight;setBusy(true);try{await source.load();if(current()){view.uncertain=false;setUncertain(false);}}catch(error){failure(error);}finally{if(view.flight===flight){view.flight=undefined;if(current())setBusy(false);}}};
  return <View style={{gap:8}}><Text style={[shellType(15,600),{color:theme.colors.text}]}>{t('next.settingsSpec.server.storage.groups.1.rows.0.label')}</Text><Text selectable style={{color:theme.colors.textSecondary}}>{settings.backupFolder}</Text><Text style={{color:theme.colors.textSecondary}}>{t('next.settingsSpec.server.storage.groups.1.rows.0.help')}</Text><Button size="compact" variant="glass" disabled={busy||uncertain||source.getSnapshot().busy} onPress={open}>{t('server.chooseFolder')}</Button>{uncertain&&<><Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unconfirmedChange')}</Text><Button size="compact" variant="glass" disabled={busy} onPress={()=>{void reconcile();}}>{t('action.tryAgain')}</Button></>}{held&&<FolderExplorer open onOpenChange={value=>{if(!value)close();}} api={held.api} title={t('next.settingsSpec.server.storage.groups.1.rows.0.label')} chosen={held.chosen} multiple={false} onUse={folders=>{void chooseFolder(folders);}}/>}</View>;
}
