import {linkNativeAbortSignals} from '../../../platform/abort-signals';
import {useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {OwnerAppLogsSource,type AppReport} from '../../../../../../packages/client-core/src/server-admin/owner-app-logs';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';
import {diagnosticsSavePort} from '../../../platform/diagnostics-save';

function createReports(context:NativeShellBootstrapContext,reportError:NativeShellRuntime['reportError']){
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  // App problem reports and the server's records (rebuilt /v1/admin/reports, logs, audit). Files Portico could not
  // prepare have their own tab, so this source does not read them again.
  const source=new OwnerAppLogsSource({...api,readReportLog:context.api.readReportLog.bind(context.api)},lifetime.authority.serverId,lifetime.authority.profileId,current,error=>{const owned=current();source.dispose();lifetime.dispose();if(owned)reportError(error);},{reports:true,stuckFiles:false});
  return {context,source,current,signal:lifetime.signal,dispose:()=>{source.dispose();lifetime.dispose();}};
}
export type Group=ReturnType<typeof createReports>;
export function useNativeReports(ports:NativeSettingsLivePorts,runtime:NativeShellRuntime){
  const context=ports.context,reportError=runtime.reportError,[group,setGroup]=useState<Group>();
  useLayoutEffect(()=>{const next=createReports(context,reportError);setGroup(next);return next.dispose;},[context,reportError]);
  // Reports are the owner's alone; an administrator's page never asks for them.
  const owner=context.account.account.role==='owner';
  useEffect(()=>{if(owner&&group?.context===context)void group.source.load();},[group,context,owner]);
  return group?.context===context?group:undefined;
}
type Dialog=Readonly<{report:AppReport;abort:AbortController}>;
type Confirmation=Readonly<{dialog:Dialog;report:AppReport}>;
type Scope={active:boolean;dialog?:Dialog;confirmation?:Confirmation;flight?:object};
/** What a report says, with its attached app log when it has one. */
const reportText=(report:AppReport&{log?:string}):string=>[report.message,report.log].filter(Boolean).join('\n\n');
export function useReportsControls({group}:{group:Group}){
  const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),t=defaultI18n.t;
  const [savePort]=useState(()=>diagnosticsSavePort());
  const [scope,setScope]=useState<Scope>(),[dialog,setDialog]=useState<Dialog>(),[confirmation,setConfirmation]=useState<Confirmation>(),[body,setBody]=useState<string>(),[busy,setBusy]=useState(false),[error,setError]=useState(false);
  useLayoutEffect(()=>{const next:Scope={active:true};setScope(next);return()=>{next.active=false;next.dialog?.abort.abort();next.confirmation?.dialog.abort.abort();next.dialog=undefined;next.confirmation=undefined;};},[source]);
  useLayoutEffect(()=>{const selected=scope?.dialog;if(scope&&selected&&!source.listed(selected.report)){scope.dialog=undefined;scope.confirmation=undefined;selected.abort.abort();setDialog(undefined);setConfirmation(undefined);setBody(undefined);}},[scope,state.reports,source]);
  const current=()=>!!scope?.active&&group.current();
  const held=dialog&&scope?.dialog===dialog&&current()&&source.listed(dialog.report)?dialog:undefined;
  const heldConfirmation=confirmation&&scope?.confirmation===confirmation&&current()&&source.listed(confirmation.report)?confirmation:undefined;
  const close=()=>{if(!scope||scope.dialog!==held)return;scope.dialog=undefined;scope.confirmation=undefined;held?.abort.abort();setDialog(undefined);setConfirmation(undefined);setBody(undefined);setError(false);};
  const closeConfirmation=()=>{if(!scope||scope.confirmation!==heldConfirmation)return;if(scope.flight){close();return;}scope.confirmation=undefined;setConfirmation(undefined);};
  const open=async(report:AppReport)=>{
    if(!scope||!current()||scope.dialog||scope.flight||state.phase!=='ready'||source.getSnapshot()!==state||!source.listed(report))return;
    const captured:Dialog={report,abort:new AbortController()};scope.dialog=captured;setDialog(captured);setBody(undefined);setError(false);setBusy(true);
    const owns=()=>current()&&scope.dialog===captured&&!captured.abort.signal.aborted&&source.listed(report);
    try{if(!owns())return;const value=await source.read(report.id,captured.abort.signal,report);if(owns())setBody(reportText(value));}catch{if(owns())setError(true);}finally{if(owns())setBusy(false);}
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
    try{if(!owns())return;const report=await source.read(captured.report.id,signal,captured.report);if(!owns()||report.id!==captured.report.id)return;const result=await savePort('report',{...report,body:reportText(report)},signal,owns);if(owns()&&result==='saved')close();}catch{if(owns())setError(true);}finally{linked.dispose();off();read.abort();if(scope.flight===flight){scope.flight=undefined;if(current())setBusy(false);}}
  };
  const refresh=()=>{if(current()&&!scope?.dialog&&!scope?.flight)void source.load();};
  const records=state.records;
  const more = () => {
    if (current() && !scope?.dialog && !scope?.flight && source.getSnapshot() === state) void source.more();
  };
  return {state, t, records, source, current, refresh, more, open, held, heldConfirmation, close, closeConfirmation, save, savePort, ask, remove, busy, body, error};
}
