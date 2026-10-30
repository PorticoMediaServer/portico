import {linkNativeAbortSignals} from '../../../../platform/abort-signals';
import React,{useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {ScrollView,Text,View} from 'react-native';
import {SettingsReportsSource} from '../../../../../../../packages/client-core/src/server-admin/reports-source';
import type {ClientReport} from '../../../../../../../packages/client-core/src/client-reports';
import {when,sentence} from '../../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
import {Button,Select} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {shellType} from '../../../../kit/shell/visual';
import {diagnosticsSavePort} from '../../../../platform/diagnostics-save';
import {useTheme} from '../../../../kit/theme';
const LANES=[{value:'runtime',label:'web.logs.laneServer'},{value:'client',label:'web.logs.laneClient'},{value:'audit',label:'web.logs.laneAudit'}] as const;
function createReports(context:NativeShellBootstrapContext,reportError:NativeShellRuntime['reportError']){
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  const source=new SettingsReportsSource(api,lifetime.authority.serverId,lifetime.authority.profileId,current,error=>{const owned=current();source.dispose();lifetime.dispose();if(owned)reportError(error);});
  return {context,source,current,signal:lifetime.signal,dispose:()=>{source.dispose();lifetime.dispose();}};
}
type Group=ReturnType<typeof createReports>;
export function useNativeReports(ports:NativeSettingsLivePorts,runtime:NativeShellRuntime){
  const context=ports.context,reportError=runtime.reportError,[group,setGroup]=useState<Group>();
  useLayoutEffect(()=>{const next=createReports(context,reportError);setGroup(next);return next.dispose;},[context,reportError]);
  useEffect(()=>{if(group?.context===context)void group.source.load();},[group,context]);
  return group?.context===context?group:undefined;
}
type Dialog=Readonly<{report:ClientReport;abort:AbortController}>;
type Confirmation=Readonly<{dialog:Dialog;report:ClientReport}>;
type Scope={active:boolean;dialog?:Dialog;confirmation?:Confirmation;flight?:object};
export function NativeReports({group}:{group:Group}){
  const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),theme=useTheme(),t=defaultI18n.t;
  const [savePort]=useState(()=>diagnosticsSavePort());
  const [scope,setScope]=useState<Scope>(),[dialog,setDialog]=useState<Dialog>(),[confirmation,setConfirmation]=useState<Confirmation>(),[body,setBody]=useState<string>(),[busy,setBusy]=useState(false),[error,setError]=useState(false);
  useLayoutEffect(()=>{const next:Scope={active:true};setScope(next);return()=>{next.active=false;next.dialog?.abort.abort();next.confirmation?.dialog.abort.abort();next.dialog=undefined;next.confirmation=undefined;};},[source]);
  useLayoutEffect(()=>{const selected=scope?.dialog;if(scope&&selected&&!source.listed(selected.report)){scope.dialog=undefined;scope.confirmation=undefined;selected.abort.abort();setDialog(undefined);setConfirmation(undefined);setBody(undefined);}},[scope,state.reports,source]);
  const current=()=>!!scope?.active&&group.current();
  const held=dialog&&scope?.dialog===dialog&&current()&&source.listed(dialog.report)?dialog:undefined;
  const heldConfirmation=confirmation&&scope?.confirmation===confirmation&&current()&&source.listed(confirmation.report)?confirmation:undefined;
  const close=()=>{if(!scope||scope.dialog!==held)return;scope.dialog=undefined;scope.confirmation=undefined;held?.abort.abort();setDialog(undefined);setConfirmation(undefined);setBody(undefined);setError(false);};
  const closeConfirmation=()=>{if(!scope||scope.confirmation!==heldConfirmation)return;if(scope.flight){close();return;}scope.confirmation=undefined;setConfirmation(undefined);};
  const open=async(report:ClientReport)=>{
    if(!scope||!current()||scope.dialog||scope.flight||state.phase!=='ready'||source.getSnapshot()!==state||!source.listed(report))return;
    const captured:Dialog={report,abort:new AbortController()};scope.dialog=captured;setDialog(captured);setBody(undefined);setError(false);setBusy(true);
    const owns=()=>current()&&scope.dialog===captured&&!captured.abort.signal.aborted&&source.listed(report);
    try{if(!owns())return;const value=await source.read(report.id,captured.abort.signal,report);if(owns())setBody(value.body??'');}catch{if(owns())setError(true);}finally{if(owns())setBusy(false);}
  };
  const ask=()=>{if(!scope||!held||scope.dialog!==held||scope.flight||source.getSnapshot().phase!=='ready')return;const next={dialog:held,report:held.report};scope.confirmation=next;setConfirmation(next);};
  const remove=async()=>{
    const captured=heldConfirmation;if(!scope||!captured||scope.confirmation!==captured||scope.flight||!current()||!source.listed(captured.report)||source.getSnapshot().phase!=='ready')return;
    const flight={};scope.flight=flight;setBusy(true);setError(false);
    const owns=()=>current()&&scope.flight===flight&&scope.confirmation===captured&&scope.dialog===captured.dialog&&!captured.dialog.abort.signal.aborted;
    try{if(!owns())return;const done=await source.remove(captured.report.id,captured.report,{signal:captured.dialog.abort.signal,isCurrent:()=>owns()&&source.listed(captured.report)});if(owns()){if(done){scope.dialog=undefined;scope.confirmation=undefined;captured.dialog.abort.abort();setDialog(undefined);setConfirmation(undefined);setBody(undefined);}else{scope.confirmation=undefined;setConfirmation(undefined);setError(true);}}}catch{if(owns())setError(true);}finally{if(scope.flight===flight){scope.flight=undefined;if(current())setBusy(false);}}
  };
  const save=async()=>{
    const captured=held;if(!scope||!captured||!savePort||!current()||scope.dialog!==captured||scope.flight||!source.listed(captured.report))return;
    const flight={};scope.flight=flight;setBusy(true);setError(false);
    const read=new AbortController(),linked=linkNativeAbortSignals([captured.abort.signal,group.signal,read.signal]),signal=linked.signal;
    const owns=()=>current()&&scope.dialog===captured&&scope.flight===flight&&!signal.aborted&&source.listed(captured.report);
    const off=source.subscribe(()=>{if(!owns())read.abort();});
    try{if(!owns())return;const report=await source.read(captured.report.id,signal,captured.report);if(!owns()||report.id!==captured.report.id||typeof report.body!=='string')return;const result=await savePort('report',report,signal,owns);if(owns()&&result==='saved')close();}catch{if(owns())setError(true);}finally{linked.dispose();off();read.abort();if(scope.flight===flight){scope.flight=undefined;if(current())setBusy(false);}}
  };
  const refresh=()=>{if(current()&&!scope?.dialog&&!scope?.flight)void source.load();};
  const records=state.records;
  return <View style={{marginTop:14,gap:12}}><Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.diagnostics.clientReports')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('server.reports.lede')}</Text>{state.phase==='error'&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}<Button size="compact" variant="glass" disabled={state.phase==='loading'} onPress={refresh}>{t('web.selection.refresh')}</Button>{state.phase==='loading'&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}{state.phase==='ready'&&!state.reports.length&&<Text style={{color:theme.colors.textSecondary}}>{t('server.reports.none')}</Text>}{state.reports.map(report=><Button key={report.id} variant="glass" size="compact" disabled={state.phase!=='ready'} onPress={()=>{void open(report);}}><View style={{gap:4}}><View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[report.platform||t('server.reports.unknownSender'), report.deviceClass].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(15,700),{color:theme.colors.text}]}>{part}</Text>)}</View><View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[report.appVersion, when(Date.parse(report.receivedAt)), defaultI18n.bytes(report.bytes)].filter(Boolean).map((part, index) => <Text key={index} style={{color:theme.colors.textSecondary}}>{part}</Text>)}</View></View></Button>)}{state.next&&<Button size="compact" disabled={state.phase!=='ready'} onPress={()=>{if(current()&&!scope?.dialog&&!scope?.flight&&source.getSnapshot()===state)void source.more();}}>{t('action.showMore')}</Button>}
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('server.setAside.title')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('server.setAside.lede')}</Text>{state.setAside?.failures.map((failure,index)=><View key={index} style={{gap:4}}><Text style={{color:theme.colors.text}}>{failure.title||t('server.setAside.untitled')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('server.setAside.tried',{count:failure.attempts,when:when(failure.failedAt)})}</Text><Text style={{color:theme.colors.textSecondary}}>{failure.reason}</Text></View>)}<Button size="compact" variant="glass" onPress={()=>{if(current())void source.checkSetAside();}}>{t('web.selection.refresh')}</Button>
    {records&&<><Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('settings.server.records')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('next.settings.recordsHelp')}</Text><Select label={t('settings.server.records')} value={records.lane} options={LANES.map(lane=>({value:lane.value,label:t(lane.label)}))} onValueChange={value=>{const lane=LANES.find(lane=>lane.value===value);if(current()&&lane&&source.getSnapshot().records===records)void source.selectLane(lane.value);}}/>{records.items.map(record=><View key={record.sequence} style={{gap:4}}><Text style={{color:theme.colors.text}}>{when(record.at)} · {sentence(record.code??record.action??'')}</Text>{record.component&&<Text style={{color:theme.colors.textSecondary}}>{record.component}</Text>}</View>)}{!records.items.length&&<Text style={{color:theme.colors.textSecondary}}>{t('settings.server.records.none')}</Text>}<View style={{flexDirection:'row',gap:8}}><Button size="compact" variant="glass" onPress={()=>{if(current()&&source.getSnapshot().records===records)void source.latestRecords();}}>{t('next.settings.recordsLatest')}</Button>{records.next&&<Button size="compact" variant="glass" onPress={()=>{if(current()&&source.getSnapshot().records===records)void source.moreRecords();}}>{t('next.settings.recordsOlder')}</Button>}</View></>}
    <BottomSheet open={!!held} onOpenChange={value=>{if(!value)close();}} title={held?`${held.report.platform||t('server.reports.unknownSender')} · ${held.report.deviceClass}`:undefined} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.close')}</Button>{savePort&&<Button variant="glass" disabled={busy||state.phase!=='ready'} onPress={()=>{void save();}}>{t('action.save')}</Button>}<Button variant="glass" disabled={busy||state.phase!=='ready'} onPress={ask}>{t('action.delete')}</Button></View>}><ScrollView style={{maxHeight:320}}>{busy&&body===undefined&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}{body!==undefined&&<Text selectable style={{color:theme.colors.textSecondary,fontFamily:'monospace'}}>{body||t('server.reports.empty')}</Text>}{error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}</ScrollView></BottomSheet>
    <BottomSheet open={!!heldConfirmation} onOpenChange={value=>{if(!value)closeConfirmation();}} title={t('server.reports.deleteTitle')} description={t('server.reports.deleteBody')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={closeConfirmation}>{t('action.cancel')}</Button><Button variant="glass" disabled={busy||state.phase!=='ready'} loading={busy} onPress={()=>{void remove();}}>{t('action.delete')}</Button></View>}><View/></BottomSheet>
  </View>;
}
