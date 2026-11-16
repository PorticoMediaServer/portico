import {supportZipSavePort} from '../../../../platform/diagnostics-save';
import React,{useCallback,useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import {SUPPORT_COMPONENTS,type SupportComponent} from '../../../../../../../packages/client-core/src/server-admin/support-source';
import {fixOwnerSupportPermissions,OWNER_SUPPORT_DEFAULT_COMPONENTS,OWNER_SUPPORT_WINDOWS,prepareOwnerSupportExport,readOwnerSupport,SUPPORT_CAPABILITY_STATES,supportCapabilityName,type OwnerSupportFacts,type OwnerSupportOptions} from '../../../../../../../packages/client-core/src/server-admin/support-next';
import {skippedParts,resetSkippedParts,skippedVersion,subscribeSkipped} from '../../../../../../../packages/client-core/src/tolerant';
import {defaultI18n,type MessageId} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
import {Button,Select,Switch} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';

const PARTS:Readonly<Record<SupportComponent,MessageId>>={runtime:'web.logs.includeServer',client:'web.logs.includeClient',audit:'web.logs.includeAudit',snapshot:'web.logs.includeSnapshot'};
/** The export's time windows (1 hour to 7 days) in the spec's own words, in the same order. */
const WINDOWS=OWNER_SUPPORT_WINDOWS.map((minutes,index)=>({minutes,label:`next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.${index}.label` as MessageId}));
type Facts=Readonly<{phase:'loading'|'ready'|'error';facts?:OwnerSupportFacts}>;

/** The owner's Support reads and the export, through the page's own lifetime (rebuilt routes in client-core support-next.ts). */
function createSupport(context:NativeShellBootstrapContext){
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  const exportZip=async(options:OwnerSupportOptions)=>{
    if(!current())throw screenScopeEnded();
    const ready=await prepareOwnerSupportExport(context.api,options,lifetime.signal);
    if(!current())throw screenScopeEnded();
    return ready.blob;
  };
  return {context,api,current,exportZip,signal:lifetime.signal,dispose:()=>lifetime.dispose()};
}
type Group=ReturnType<typeof createSupport>;
export function useNativeSupport(ports:NativeSettingsLivePorts,_runtime:NativeShellRuntime){
  const context=ports.context,[group,setGroup]=useState<Group>();
  useLayoutEffect(()=>{const next=createSupport(context);setGroup(next);return next.dispose;},[context]);
  return group?.context===context?group:undefined;
}

/** Settings › Server › Troubleshooting › Support on the phone: the data folder's permissions, what this server can do
 * (each check with the server's own sentence and path, wrapped), and the support export, a ZIP saved through the
 * system's save sheet. Definition 12 rules 74 and 76. */
export function NativeSupport({group}:{group:Group}){
  const theme=useTheme(),t=defaultI18n.t;
  useSyncExternalStore(subscribeSkipped,skippedVersion,skippedVersion);
  const [savePort]=useState(()=>supportZipSavePort());
  const [state,setState]=useState<Facts>({phase:'loading'}),[fixing,setFixing]=useState(false),[fixed,setFixed]=useState(false);
  const [window,setWindow]=useState<number>(1440),[parts,setParts]=useState<readonly SupportComponent[]>(OWNER_SUPPORT_DEFAULT_COMPONENTS),[paths,setPaths]=useState(false);
  const [exporting,setExporting]=useState(false),[failed,setFailed]=useState(false);
  const load=useCallback(()=>{
    setState(before=>({...before,phase:'loading'}));
    void readOwnerSupport(group.api,group.signal).then(facts=>{if(group.current())setState({phase:'ready',facts});},()=>{if(group.current())setState(before=>({...before,phase:'error'}));});
  },[group]);
  useEffect(load,[load]);
  const fix=async()=>{
    if(fixing||!group.current())return;setFixing(true);
    try{await fixOwnerSupportPermissions(group.api,group.signal);if(group.current()){setFixed(true);load();}}
    catch{if(group.current())setState(before=>({...before,phase:'error'}));}
    finally{if(group.current())setFixing(false);}
  };
  // One action: the server makes the ZIP at once and nothing is kept there (rule 76); the save sheet takes it.
  const exportNow=async()=>{
    if(exporting||!savePort||!parts.length||!group.current())return;
    setExporting(true);setFailed(false);
    try{
      const zip=await group.exportZip({windowMinutes:window as OwnerSupportOptions['windowMinutes'],components:[...parts],includePaths:paths&&parts.includes('snapshot')});
      await savePort(zip,group.signal,group.current);
    }catch{if(group.current())setFailed(true);}
    finally{if(group.current()){setExporting(false);setPaths(false);}}
  };
  const permissions=state.facts?.permissions,skipped=skippedParts();
  return <View style={{marginTop:14,gap:12}}>
    {state.phase==='error'&&<><Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text><Button size="compact" variant="glass" onPress={load}>{t('action.tryAgain')}</Button></>}
    {state.phase==='loading'&&!state.facts&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}
    {permissions?.readableByOthers&&<View style={{gap:8}}>
      <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.maintenance.permissionsTitle')}</Text>
      <Text style={{color:theme.colors.textSecondary}}>{permissions.fixable?t('web.maintenance.permissionsBody'):permissions.message??t('web.maintenance.permissionsBody')}</Text>
      {permissions.fixable&&<Button size="compact" variant="glass" loading={fixing} disabled={fixing} onPress={()=>{void fix();}}>{t('web.maintenance.fixPermissions')}</Button>}
    </View>}
    {fixed&&!permissions?.readableByOthers&&<Text style={{color:theme.colors.textSecondary}}>{t('web.maintenance.permissionsFixed')}</Text>}
    {state.facts&&<>
      <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.capabilities.title')}</Text>
      <Text style={{color:theme.colors.textSecondary}}>{t('web.capabilities.help')}</Text>
      {state.facts.capabilities.map(capability=>{const word=SUPPORT_CAPABILITY_STATES[capability.state];return <View key={`${capability.area}/${capability.name}/${capability.path??''}`} style={{padding:12,gap:4,borderRadius:12,backgroundColor:theme.colors.raised}}>
        <View style={{flexDirection:'row',flexWrap:'wrap',justifyContent:'space-between',columnGap:10,rowGap:4}}>
          <Text style={[shellType(15,700),{color:theme.colors.text,flexShrink:1}]}>{supportCapabilityName(capability,t)}</Text>
          {word&&<Text style={[shellType(13,600),{color:word.tone==='ok'?theme.colors.healthy:word.tone==='warn'?theme.colors.amber:theme.colors.textTertiary}]}>{t(word.label)}</Text>}
        </View>
        {[capability.reason,capability.detail].filter(Boolean).map((line,index)=><Text key={index} style={{color:theme.colors.textSecondary}}>{line}</Text>)}
        {capability.path&&<Text selectable style={{color:theme.colors.textTertiary,fontFamily:'monospace'}}>{capability.path}</Text>}
      </View>;})}
    </>}
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('settings.server.skippedParts')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('settings.server.skippedPartsLede')}</Text>
    {skipped.length?skipped.map(part=><View key={part.document+':'+part.part}><Text style={{color:theme.colors.text}}>{part.document}</Text><Text style={{color:theme.colors.textSecondary}}>{part.part} · {t('settings.server.skippedPartsCount',{count:part.count})}</Text></View>):<Text style={{color:theme.colors.textSecondary}}>{t('settings.server.skippedPartsNone')}</Text>}
    {skipped.length>0&&<Button size="compact" variant="glass" onPress={()=>{if(group.current())resetSkippedParts();}}>{t('action.clearAll')}</Button>}
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.logs.exportTitle')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.logs.exportLede')}</Text>
    <Select label={t('web.logs.timeWindow')} value={String(window)} disabled={exporting} options={WINDOWS.map(entry=>({value:String(entry.minutes),label:t(entry.label)}))} onValueChange={value=>{const found=WINDOWS.find(entry=>String(entry.minutes)===value);if(found)setWindow(found.minutes);}}/>
    {SUPPORT_COMPONENTS.map(component=><Switch key={component} checked={parts.includes(component)} disabled={exporting} onCheckedChange={checked=>{setParts(current=>checked?[...new Set([...current,component])]:current.filter(part=>part!==component));if(component==='snapshot'&&!checked)setPaths(false);}}>{t(PARTS[component])}</Switch>)}
    <Switch checked={paths} disabled={exporting||!parts.includes('snapshot')} onCheckedChange={setPaths}>{t('next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.label')}</Switch>
    <Text style={{color:theme.colors.textSecondary}}>{t('web.logs.pathsHelp')}</Text>
    {savePort?<Button testID="server-support-export" size="compact" disabled={exporting||!parts.length} loading={exporting} onPress={()=>{void exportNow();}}>{t('web.logs.prepare')}</Button>
      :<Text style={{color:theme.colors.textSecondary}}>{t('error.unavailable')}</Text>}
    {failed&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}
  </View>;
}
