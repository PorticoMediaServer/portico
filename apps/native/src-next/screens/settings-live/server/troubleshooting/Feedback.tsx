import {NativeBroadcastSheet} from '../BroadcastSheet';
import React,{useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import {SettingsFeedbackSource,feedbackSentFrom,feedbackTriageCategory,type FeedbackReport,type FeedbackSnapshot} from '../../../../../../../packages/client-core/src/server-admin/feedback-source';
import type {FeedbackStatus} from '../../../../../../../packages/client-core/src/console';
import {feedbackCategoryName,feedbackStatusName} from '../../../../../../../packages/client-core/src/server-admin/console-catalogue';
import {sentence,when} from '../../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
import {Button,Select,TextField} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
const STATUSES:readonly FeedbackStatus[]=['open','in-progress','resolved','closed'];
const EMPTY:FeedbackSnapshot=Object.freeze({phase:'idle',reports:[]});
const noopSubscribe=()=>()=>{};
const emptySnapshot=()=>EMPTY;
function createFeedback(context:NativeShellBootstrapContext,reportError:NativeShellRuntime['reportError']){
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  const source=new SettingsFeedbackSource(api,lifetime.authority.serverId,lifetime.authority.profileId,current,error=>{const owned=current();source.dispose();lifetime.dispose();if(owned)reportError(error);});
  return {context,source,current,dispose:()=>{source.dispose();lifetime.dispose();}};
}
type Group=ReturnType<typeof createFeedback>;
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
export function NativeFeedback({group,ports,runtime}:{group:Group;ports:NativeSettingsLivePorts;runtime:NativeShellRuntime}){
  const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),theme=useTheme(),t=defaultI18n.t;
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
  return <View style={{marginTop:14,gap:12}}><Button variant="glass" size="compact" icon="i-mail" onPress={()=>{if(current())setMessageOpen(true);}}>{t('server.messageEveryone')}</Button><NativeBroadcastSheet ports={ports} runtime={runtime} open={messageOpen} onOpenChange={setMessageOpen}/>{state.phase==='error'&&<><Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text><Button variant="glass" size="compact" onPress={()=>{if(current()&&!scope?.dialog&&!scope?.saving)void source.load();}}>{t('action.tryAgain')}</Button></>}{state.phase==='loading'&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}{state.phase==='ready'&&!state.reports.length&&<><Text style={[shellType(15,700),{color:theme.colors.text}]}>{t('web.feedback.inboxEmpty')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.feedback.inboxEmptyBody')}</Text></>}{state.reports.map(report=><Button key={report.id} variant="glass" size="compact" onPress={()=>{void open(report);}}><View style={{gap:4}}><Text numberOfLines={1} style={[shellType(15,700),{color:theme.colors.text}]}>{report.message.split('\n')[0]}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.feedback.rowSubtitle',{category:feedbackCategoryName(t,defaultI18n.has,feedbackTriageCategory(report)),when:when(report.createdAt),about:report.itemId?'yes':'no'})}</Text><Text style={{color:theme.colors.accentStrong}}>{feedbackStatusName(t,defaultI18n.has,report.status)}</Text></View></Button>)}{state.next&&<Button size="compact" variant="glass" disabled={state.phase==='loading'} onPress={()=>{if(current()&&!scope?.dialog&&!scope?.saving&&source.getSnapshot()===state)void source.more();}}>{t('action.showMore')}</Button>}
    <BottomSheet open={!!held} onOpenChange={value=>{if(!value)close();}} title={shown?feedbackCategoryName(t,defaultI18n.has,feedbackTriageCategory(shown.report)):undefined} description={shown?t('web.feedback.reportedMeta',{when:when(shown.report.createdAt),status:feedbackStatusName(t,defaultI18n.has,shown.report.status)}):undefined} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button><Button disabled={!shown||loading||busy} loading={busy} onPress={()=>{void save();}}>{t('web.feedback.updateReport')}</Button></View>}><View style={{gap:12}}>{loading&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}{shown&&<><Text style={{color:theme.colors.text}}>{shown.report.message}</Text>{shown.report.client&&<Text style={{color:theme.colors.textSecondary}}>{t('server.feedback.sentFrom',{...feedbackSentFrom(shown.report)!,platform:sentence(shown.report.client.platform)})}</Text>}<Select label={t('web.feedback.status')} value={shown.status} disabled={busy} options={STATUSES.map(status=>({value:status,label:feedbackStatusName(t,defaultI18n.has,status)}))} onValueChange={value=>{const status=STATUSES.find(status=>status===value);if(status)change({...shown,status});}}/><TextField label={t('web.feedback.replyLabel')} description={t('web.feedback.replyHelp')} value={shown.reply} maxLength={2000} editable={!busy} multiline onChangeText={reply=>change({...shown,reply})}/></>}{error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}</View></BottomSheet>
  </View>;
}
