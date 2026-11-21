import {useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {SettingsFeedbackSource,type FeedbackReport,type FeedbackSnapshot} from '../../../../../../packages/client-core/src/server-admin/feedback-source';
import type {FeedbackStatus} from '../../../../../../packages/client-core/src/console';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';

const EMPTY:FeedbackSnapshot=Object.freeze({phase:'idle',reports:[]});
const noopSubscribe=()=>()=>{};
const emptySnapshot=()=>EMPTY;
function createFeedback(context:NativeShellBootstrapContext,reportError:NativeShellRuntime['reportError']){
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  const source=new SettingsFeedbackSource(api,lifetime.authority.serverId,lifetime.authority.profileId,current,error=>{const owned=current();source.dispose();lifetime.dispose();if(owned)reportError(error);});
  return {context,source,current,dispose:()=>{source.dispose();lifetime.dispose();}};
}
export type Group=ReturnType<typeof createFeedback>;
export function useNativeFeedback(ports:NativeSettingsLivePorts,runtime:NativeShellRuntime){
  const context=ports.context,reportError=runtime.reportError,[group,setGroup]=useState<Group>();
  useLayoutEffect(()=>{const next=createFeedback(context,reportError);setGroup(next);return next.dispose;},[context,reportError]);
  useEffect(()=>{if(group?.context===context)void group.source.load();},[group,context]);
  const actual=group?.context===context?group:undefined;
  const state=useSyncExternalStore(actual?.source.subscribe??noopSubscribe,actual?.source.getSnapshot??emptySnapshot,actual?.source.getSnapshot??emptySnapshot);
  return {group:actual,state};
}
type Dialog=Readonly<{abort:AbortController}>;
type Draft=Readonly<{report:FeedbackReport;status:FeedbackStatus;reply:string}>;
type Scope={active:boolean;dialog?:Dialog;draft?:Draft;saving?:object};
export function useFeedbackControls({group}:{group:Group}){
  const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),t=defaultI18n.t;
  const [messageOpen,setMessageOpen]=useState(false);
  const [scope,setScope]=useState<Scope>(),[dialog,setDialog]=useState<Dialog>(),[draft,setDraft]=useState<Draft>(),[busy,setBusy]=useState(false),[loading,setLoading]=useState(false),[error,setError]=useState(false);
  useLayoutEffect(()=>{const next:Scope={active:true};setScope(next);return()=>{next.active=false;next.dialog?.abort.abort();next.dialog=undefined;next.draft=undefined;};},[source]);
  const current=()=>!!scope?.active&&group.current();
  const held=dialog&&scope?.dialog===dialog&&!dialog.abort.signal.aborted&&current()?dialog:undefined;
  const close=()=>{if(!scope||scope.dialog!==held)return;scope.dialog=undefined;scope.draft=undefined;held?.abort.abort();setDialog(undefined);setDraft(undefined);setLoading(false);setError(false);};
  const open=async(report:FeedbackReport)=>{
    if(!scope||!current()||scope.dialog||scope.saving||!source.getSnapshot().reports.includes(report))return;
    const captured:Dialog={abort:new AbortController()};scope.dialog=captured;scope.draft=undefined;setDialog(captured);setDraft(undefined);setLoading(true);setError(false);setBusy(false);
    const owns=()=>current()&&scope.dialog===captured&&!captured.abort.signal.aborted;
    try{if(!owns())return;const value=await source.read(report.id,captured.abort.signal);if(!owns()||!source.getSnapshot().reports.includes(value))return;const next:Draft={report:value,status:value.status==='open'?'in-progress':value.status,reply:''};scope.draft=next;setDraft(next);}catch{if(owns())setError(true);}finally{if(owns())setLoading(false);}
  };
  const editable=()=>!!held&&!!draft&&scope?.draft===draft&&source.getSnapshot().reports.includes(draft.report)&&!scope.saving;
  const change=(next:Draft)=>{if(!scope||!editable())return;scope.draft=next;setDraft(next);setError(false);};
  const save=async()=>{
    const captured=held,original=draft;if(!scope||!captured||!original||!editable())return;
    const flight={};scope.saving=flight;setBusy(true);setError(false);
    const owns=()=>current()&&scope.dialog===captured&&scope.draft===original&&scope.saving===flight&&!captured.abort.signal.aborted;
    try{if(!owns())return;await source.triage(original.report,original.status,original.reply,captured.abort.signal);if(owns())close();}catch{if(owns())setError(true);}finally{if(scope.saving===flight){scope.saving=undefined;if(current()&&scope.dialog===captured)setBusy(false);}}
  };
  const shown=draft&&scope?.draft===draft?draft:undefined;
  const openMessage = () => {if (current()) setMessageOpen(true);};
  const reload = () => {if (current() && !scope?.dialog && !scope?.saving) void source.load();};
  const more = () => {
    if (current() && !scope?.dialog && !scope?.saving && source.getSnapshot() === state) void source.more();
  };
  return {state, t, messageOpen, setMessageOpen, openMessage, reload, more, open, held, close, shown, loading, busy, save, change, error};
}
