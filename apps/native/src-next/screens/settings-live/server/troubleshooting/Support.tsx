import {linkNativeAbortSignals} from '../../../../platform/abort-signals';
import type {SupportExport} from '../../../../../../../packages/client-core/src/console';
import {encodedRoutePath} from '../../../../../../../packages/contracts/src/routes';
import {diagnosticsSavePort} from '../../../../platform/diagnostics-save';
import React,{useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import {SettingsSupportSource,SUPPORT_COMPONENTS,type SupportComponent,type SupportSnapshot} from '../../../../../../../packages/client-core/src/server-admin/support-source';
import {CAPABILITY_ROWS,CAPABILITY_BEHIND_PRESENCE,capabilityReasonId,type ServerCapability} from '../../../../../../../packages/client-core/src/server-admin/capabilities';
import {skippedParts,resetSkippedParts,skippedVersion,subscribeSkipped} from '../../../../../../../packages/client-core/src/tolerant';
import {sentence,when} from '../../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n,type MessageId} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
import {nativeChannelOperationId} from '../channels/model';
import {Button,Select,Switch} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
const WINDOWS=[{value:'1h',ms:3600000,label:'web.logs.window1h'},{value:'6h',ms:21600000,label:'web.logs.window6h'},{value:'24h',ms:86400000,label:'web.logs.window24h'},{value:'3d',ms:259200000,label:'web.logs.window3d'}] as const;
const PARTS:Readonly<Record<SupportComponent,MessageId>>={runtime:'web.logs.includeServer',client:'web.logs.includeClient',audit:'web.logs.includeAudit',snapshot:'web.logs.includeSnapshot'};
const LABELS:Readonly<Record<typeof CAPABILITY_ROWS[number],MessageId>>={live_tv:'web.capabilities.live_tv',recording:'web.capabilities.recording',prepared_media:'web.capabilities.prepared_media',hardware_encoding:'web.capabilities.hardware_encoding',decoder_sandbox:'web.capabilities.decoder_sandbox',dolby_vision_conversion:'web.capabilities.dolby_vision_conversion'};
const REASONS=['web.capabilities.reason.platform','web.capabilities.reason.decoderSandboxUnavailable','web.capabilities.reason.decoderSandboxOff','web.capabilities.reason.dolbyVisionApproximate','web.capabilities.reason.tools','web.capabilities.reason.dependencies','web.capabilities.reason.delivery','web.capabilities.reason.hardware','web.capabilities.reason.other'] as const;
function terminal(error:unknown){return !!error&&typeof error==='object'&&('status' in error&&(error.status===401||error.status===403)||'code' in error&&(error.code==='unauthorized'||error.code==='forbidden'||error.code==='not_permitted'));}
function createSupport(context:NativeShellBootstrapContext,reportError:NativeShellRuntime['reportError']){
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context);
  type Download=Readonly<{ready:SupportExport;signal:AbortSignal;isCurrent:()=>boolean}>;
  let downloading:Download|undefined;
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>{
    const captured=downloading,selected=captured&&path.split('?')[0]===encodedRoutePath('get_admin_console_exports_id',{id:encodeURIComponent(captured.ready.id)})?captured:undefined;
    const valid=()=>current()&&!caller?.aborted&&(!selected||!selected.signal.aborted&&selected.isCurrent()&&source.getSnapshot().export===selected.ready&&source.canDownload(selected.ready.id));
    if(!valid()||selected&&method&&method!=='GET')return Promise.reject(screenScopeEnded());
    const linked=selected?linkNativeAbortSignals([selected.signal,caller]):undefined,combined=linked?.signal??caller;
    return lifetime.request(async signal=>{if(!valid())throw screenScopeEnded();const value=await context.api.request<T>(path,method,body,signal);if(!valid())throw screenScopeEnded();return value;},combined).finally(()=>linked?.dispose());
  }};
  const failure=(error:unknown)=>{if(!terminal(error)||!current())return;const owned=current();source.dispose();lifetime.dispose();if(owned)reportError(error);};
  const source:SettingsSupportSource=new SettingsSupportSource(api,lifetime.authority.serverId,lifetime.authority.profileId,current,failure,nativeChannelOperationId);
  const download=async(ready:SupportExport,signal:AbortSignal,isCurrent:()=>boolean)=>{
    if(!current()||downloading||signal.aborted||!isCurrent()||source.getSnapshot().export!==ready||!source.canDownload(ready.id))throw screenScopeEnded();
    const captured:Download={ready,signal,isCurrent};downloading=captured;
    try{const bundle=await source.download();if(downloading!==captured||!current()||signal.aborted||!isCurrent()||source.getSnapshot().export!==ready||!source.canDownload(ready.id))throw screenScopeEnded();return bundle;}finally{if(downloading===captured)downloading=undefined;}
  };
  return {context,source,current,failure,download,signal:lifetime.signal,dispose:()=>{source.dispose();lifetime.dispose();}};
}
type Group=ReturnType<typeof createSupport>;
export function useNativeSupport(ports:NativeSettingsLivePorts,runtime:NativeShellRuntime){
  const context=ports.context,reportError=runtime.reportError,[group,setGroup]=useState<Group>();
  useLayoutEffect(()=>{const next=createSupport(context,reportError);setGroup(next);return next.dispose;},[context,reportError]);
  useEffect(()=>{if(group?.context===context)void group.source.load();},[group,context]);
  return group?.context===context?group:undefined;
}
type Draft=Readonly<{window:string;parts:readonly SupportComponent[];paths:boolean}>;
type Scope={readonly source:SettingsSupportSource;active:boolean;draft:Draft;flight?:object;download?:AbortController};
export function NativeSupport({group}:{group:Group}){
  const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),theme=useTheme(),t=defaultI18n.t;
  useSyncExternalStore(subscribeSkipped,skippedVersion,skippedVersion);
  const [savePort]=useState(()=>diagnosticsSavePort());
  const [scope,setScope]=useState<Scope>(),[draft,setDraft]=useState<Draft>({window:'24h',parts:[...SUPPORT_COMPONENTS],paths:false}),[busy,setBusy]=useState(false),[error,setError]=useState(false),[technical,setTechnical]=useState<ServerCapability>();
  useLayoutEffect(()=>{const next:Scope={source,active:true,draft:{window:'24h',parts:[...SUPPORT_COMPONENTS],paths:false}};setScope(next);setDraft(next.draft);return()=>{next.active=false;next.download?.abort();next.download=undefined;};},[source]);
  const current=()=>!!scope?.active&&scope.source===source&&group.current();
  const frozen=state.preparing||!!state.pendingExport||busy;
  const writable=()=>!!scope&&current()&&scope.draft===draft&&!scope.flight&&!source.getSnapshot().preparing&&!source.getSnapshot().pendingExport;
  const change=(next:Draft)=>{if(!scope||!writable())return;scope.draft=next;setDraft(next);setError(false);};
  const prepare=async()=>{
    if(!scope||!current()||scope.flight||scope.draft!==draft||source.getSnapshot().preparing)return;
    const captured=draft,window=WINDOWS.find(value=>value.value===captured.window);if(!window||!captured.parts.length)return;
    const flight={};scope.flight=flight;
    // Consume the per-export opt-in before source publication; uncertain retry uses only its retained intent.
    const next={...captured,paths:false};scope.draft=next;setDraft(next);setBusy(true);setError(false);
    try{if(!current()||scope.flight!==flight)return;await source.prepare(window.ms,captured.parts,captured.paths&&captured.parts.includes('snapshot')&&source.getSnapshot().supportsPaths===true);}catch(error){group.failure(error);if(current()&&scope.flight===flight)setError(true);}finally{if(scope.flight===flight){scope.flight=undefined;if(current())setBusy(false);}}
  };
  const fix=async()=>{if(!scope||!current()||scope.flight||!source.getSnapshot().permissions)return;const flight={};scope.flight=flight;setBusy(true);setError(false);try{if(current()&&scope.flight===flight)await source.fixPermissions();}catch(error){group.failure(error);if(current()&&scope.flight===flight)setError(true);}finally{if(scope.flight===flight){scope.flight=undefined;if(current())setBusy(false);}}};
  const save=async(ready:SupportExport,snapshot:SupportSnapshot,rendered:Scope)=>{
    if(!scope||scope!==rendered||rendered.source!==source||snapshot!==state||snapshot.export!==ready||source.getSnapshot()!==snapshot||!savePort||!current()||scope.flight||!source.canDownload(ready.id))return;
    const flight={},read=new AbortController();scope.flight=flight;scope.download=read;setBusy(true);setError(false);
    const linked=linkNativeAbortSignals([read.signal,group.signal]),signal=linked.signal;
    const owns=()=>current()&&scope.flight===flight&&scope.download===read&&!signal.aborted&&source.getSnapshot().export===ready&&source.canDownload(ready.id);
    const off=source.subscribe(()=>{if(!owns())read.abort();});
    const expiry=setTimeout(()=>read.abort(),Math.min(2147483647,Math.max(0,ready.expiresAt-Date.now())));
    try{if(!owns())return;const bundle=await group.download(ready,signal,owns);if(!owns())return;await savePort('support',bundle,signal,owns);}catch(error){group.failure(error);if(owns())setError(true);}finally{linked.dispose();off();clearTimeout(expiry);read.abort();if(scope.download===read)scope.download=undefined;if(scope.flight===flight){scope.flight=undefined;if(current())setBusy(false);}}
  };
  const pending=state.pendingExport,parts=pending?.components??draft.parts,paths=pending?.includePaths??draft.paths,window=pending?WINDOWS.find(value=>value.ms===pending.windowMs)?.value??'':draft.window,ready=state.export,skipped=skippedParts();
  const known=new Set<string>(CAPABILITY_ROWS),rows=[...CAPABILITY_ROWS.flatMap(id=>{const found=state.capabilities.find(value=>value.capability===id);if(!found&&CAPABILITY_BEHIND_PRESENCE.some(value=>value===id))return [];return [{id,label:t(LABELS[id]),found}];}),...state.capabilities.filter(value=>!known.has(value.capability)).map(found=>({id:found.capability,label:t('web.capabilities.otherLabel',{id:found.capability}),found}))];
  return <View style={{marginTop:14,gap:12}}>{(state.phase==='error'||error)&&<><Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text><Button size="compact" variant="glass" disabled={busy} onPress={()=>{if(current()&&!scope?.flight)void source.retry();}}>{t('action.tryAgain')}</Button></>}{state.phase==='loading'&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}
    {state.permissions&&<View style={{gap:8}}><Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.maintenance.permissionsTitle')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.maintenance.permissionsBody')}</Text><Button size="compact" variant="glass" disabled={busy} onPress={()=>{void fix();}}>{t('web.maintenance.fixPermissions')}</Button></View>}{state.fixed&&<Text style={{color:theme.colors.textSecondary}}>{t('web.maintenance.permissionsFixed')}</Text>}
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.capabilities.title')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.capabilities.help')}</Text>{rows.map(row=><View key={row.id} style={{padding:12,gap:8,borderRadius:12,backgroundColor:theme.colors.raised}}><Text style={[shellType(15,700),{color:theme.colors.text}]}>{row.label}</Text><Text style={{color:theme.colors.textSecondary}}>{t(row.found?row.found.available?'web.capabilities.available':'web.capabilities.unavailable':'web.capabilities.unknown')}</Text>{row.found&&!row.found.available&&<Text style={{color:theme.colors.textSecondary}}>{t(REASONS.find(key=>key===capabilityReasonId(row.found?.code))??'web.capabilities.reason.other')}</Text>}{row.found&&<Button size="compact" variant="text" onPress={()=>{const selected=row.found;if(current()&&selected&&source.getSnapshot().capabilities.includes(selected))setTechnical(technical===selected?undefined:selected);}}>{t('web.capabilities.technical')}</Button>}{technical&&technical===row.found&&source.getSnapshot().capabilities.includes(technical)&&<Text selectable style={{color:theme.colors.textSecondary}}>{[technical.code,technical.detail].filter(Boolean).join(': ')}</Text>}</View>)}
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('settings.server.skippedParts')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('settings.server.skippedPartsLede')}</Text>{skipped.length?skipped.map(part=><View key={part.document+':'+part.part}><Text style={{color:theme.colors.text}}>{part.document}</Text><Text style={{color:theme.colors.textSecondary}}>{part.part} · {t('settings.server.skippedPartsCount',{count:part.count})}</Text></View>):<Text style={{color:theme.colors.textSecondary}}>{t('settings.server.skippedPartsNone')}</Text>}{skipped.length>0&&<Button size="compact" variant="glass" onPress={()=>{if(current())resetSkippedParts();}}>{t('action.clearAll')}</Button>}
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.logs.exportTitle')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.logs.exportLede')}</Text><Select label={t('web.logs.timeWindow')} value={window} disabled={frozen} options={WINDOWS.map(value=>({value:value.value,label:t(value.label)}))} onValueChange={window=>{if(WINDOWS.some(value=>value.value===window))change({...draft,window});}}/>{SUPPORT_COMPONENTS.map(component=><Switch key={component} checked={parts.includes(component)} disabled={frozen} onCheckedChange={checked=>change({...draft,parts:checked?[...draft.parts.filter(value=>value!==component),component]:draft.parts.filter(value=>value!==component),paths:component==='snapshot'&&!checked?false:draft.paths})}>{t(PARTS[component])}</Switch>)}{(state.supportsPaths||pending?.includePaths)&&<><Switch checked={paths} disabled={frozen||!draft.parts.includes('snapshot')||state.supportsPaths!==true} onCheckedChange={paths=>change({...draft,paths})}>{t('next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.label')}</Switch><Text style={{color:theme.colors.textSecondary}}>{t('web.logs.pathsHelp')}</Text></>}{state.prepareError&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unconfirmedChange')}</Text>}<Button size="compact" disabled={busy||state.preparing||!parts.length||state.phase!=='ready'} loading={state.preparing} onPress={()=>{void prepare();}}>{t(pending?'action.tryAgain':'web.logs.prepare')}</Button>{ready&&<>{savePort&&scope&&<Button size="compact" variant="glass" disabled={busy||!source.canDownload(ready.id)} onPress={()=>{void save(ready,state,scope);}}>{t('action.save')}</Button>}<Text style={{color:theme.colors.textSecondary}}>{t('web.logs.readyHelp',{included:ready.manifest.included.map(sentence).join(', ')||t('web.logs.exportNothing'),excluded:ready.manifest.excluded.map(sentence).join(', ')||t('web.logs.exportNothing'),redaction:sentence(ready.manifest.redaction),expires:when(ready.expiresAt)})}</Text>{ready.manifest.includePaths&&<Text style={{color:theme.colors.textSecondary}}>{t('web.logs.pathsRequested')}</Text>}</>}
  </View>;
}
