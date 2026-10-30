import React,{useLayoutEffect,useMemo,useState,useSyncExternalStore} from 'react';
import {Platform,Text,View} from 'react-native';
import {SettingsIntegrationsSource} from '../../../../../../../packages/client-core/src/server-admin/integrations-source';
import type {IntegrationAppPassword} from '../../../../../../../packages/client-core/src/integration-access';
import type {ApiScope} from '../../../../../../../packages/client-core/src/api-access';
import {defaultI18n,type MessageId} from '../../../../../../../packages/i18n/src';
import {Button,Select,Switch,TextField} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {useTheme} from '../../../../kit/theme';
import {shellType} from '../../../../kit/shell/visual';
import {copyText} from '../../../../platform/clipboard';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime,screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
let groupSequence=0;
type Group=Readonly<{id:number;source:SettingsIntegrationsSource;context:NativeSettingsLivePorts['context'];current:()=>boolean;dispose:()=>void}>;
export function NativeAppPasswords({ports,runtime}:Readonly<{ports:NativeSettingsLivePorts;runtime:NativeShellRuntime}>){
 const context=ports.context,reportError=runtime.reportError,[group,setGroup]=useState<Group>();
 useLayoutEffect(()=>{
  if(Platform.isTV||!nativeStorageOwner(context))return;
  const lifetime=new NativeScreenLifetime(context),current=()=>!Platform.isTV&&lifetime.current()&&nativeStorageOwner(context);
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal)=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject<T>(screenScopeEnded())};
  const source=new SettingsIntegrationsSource(api,current,error=>{lifetime.dispose();reportError(error);},ports.requestId);
  const next={id:++groupSequence,source,context,current,dispose:()=>{source.dispose();lifetime.dispose();}};setGroup(next);void source.load();return()=>next.dispose();
 },[context,ports.requestId,reportError]);
 return !Platform.isTV&&group?.context===context&&group.current()?<AppPasswords key={group.id} group={group} runtime={runtime}/>:null;
}
function AppPasswords({group,runtime}:Readonly<{group:Group;runtime:NativeShellRuntime}>){
 const theme=useTheme(),t=runtime.text.t as typeof defaultI18n.t,source=group.source,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot);
 const view=useMemo(()=>({group,active:true,ticket:{}}),[group]);useLayoutEffect(()=>{view.active=true;return()=>{view.active=false;view.ticket={};};},[view]);
 const current=()=>!Platform.isTV&&view.active&&group.current()&&source.getSnapshot().phase!=='idle';
 const [creating,setCreating]=useState(false),[name,setName]=useState(''),[target,setTarget]=useState(''),[scopes,setScopes]=useState<readonly ApiScope[]>(['read']),[card,setCard]=useState<Readonly<{name:string;secret:string}>>(),[copied,setCopied]=useState(false),[copyError,setCopyError]=useState(false),[revoke,setRevoke]=useState<IntegrationAppPassword>();
 const text={...shellType(13,400,18),color:theme.colors.textSecondary},users=state.users?.items.filter(user=>!user.disabled)??[],busy=state.busy||state.phase==='loading';
 const close=()=>{view.ticket={};setCard(undefined);setCopied(false);setCopyError(false);};
 const reveal=async(app:IntegrationAppPassword)=>{if(!current())return;close();const ticket={};view.ticket=ticket;const secret=await source.reveal(app);if(secret&&current()&&view.ticket===ticket)setCard({name:app.name,secret});};
 const create=async()=>{if(!current())return;const user=users.find(u=>u.profileId===target);if(!state.pendingCreate&&!user)return;const ticket={};view.ticket=ticket;const out=await(state.pendingCreate?source.retryCreate():source.create({name,scopes,accountId:user!.accountId,profileId:user!.profileId}));if(!out||!current()||view.ticket!==ticket)return;setCreating(false);if(out.secret)setCard({name:out.appPassword.name,secret:out.secret});};
 return <View testID="integration-app-passwords" style={{marginTop:14,gap:12}}>
  <Text style={text}>{t('integrations.appPasswords.help')}</Text>
  <Button size="compact" disabled={busy||!state.options} onPress={()=>{if(!current())return;setCreating(true);if(!state.pendingCreate){setName('');setTarget(users[0]?.profileId??'');setScopes(['read']);}}}>{t('integrations.appPasswords.create')}</Button>
  {state.error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(state.pendingCreate?'error.unconfirmedChange':'error.unavailable')}</Text>}
  {state.apps?.items.map(app=><View key={app.id} style={{padding:12,borderRadius:12,backgroundColor:theme.colors.raised,gap:6}}><Text style={{...shellType(15,700),color:theme.colors.text}}>{app.name}</Text><Text style={text}>{app.username} · {app.scopes.map(scope=>t(`api.scope.${scope}` as MessageId)).join(', ')}</Text>{app.state==='active'&&<View style={{flexDirection:'row',gap:8}}><Button size="compact" variant="glass" disabled={busy} onPress={()=>{void reveal(app);}}>{t('integrations.credential.reveal')}</Button><Button size="compact" variant="glass" disabled={busy} onPress={()=>{if(current())setRevoke(app);}}>{t('web.access.revoke')}</Button></View>}</View>)}
  {state.apps?.items.length===0&&<Text style={text}>{t('integrations.appPasswords.empty')}</Text>}
  <View style={{flexDirection:'row',gap:8}}><Button size="compact" variant="glass" disabled={busy} onPress={()=>{if(current())void source.load();}}>{t('action.refresh')}</Button>{state.apps?.nextCursor&&<Button size="compact" variant="glass" disabled={busy} onPress={()=>{if(current())void source.load(state.apps?.nextCursor);}}>{t('action.showMore')}</Button>}</View>
  <BottomSheet open={current()&&creating} title={t('integrations.appPasswords.create')} keyboardAvoiding dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy} onOpenChange={open=>{if(!open&&!state.busy){view.ticket={};setCreating(false);}}} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" disabled={state.busy} onPress={()=>{view.ticket={};setCreating(false);}}>{t('action.cancel')}</Button><Button disabled={busy||!state.pendingCreate&&(!name.trim()||!target||!scopes.length)} onPress={()=>{void create();}}>{t(state.pendingCreate?'action.tryAgain':'integrations.appPasswords.create')}</Button></View>}>
   <View style={{gap:12}}>{state.error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(state.pendingCreate?'error.unconfirmedChange':'error.unavailable')}</Text>}<TextField label={t('profile.name')} value={name} maxLength={80} editable={!busy&&!state.pendingCreate} onChangeText={setName}/><Select label={t('integrations.appPasswords.target')} value={target} disabled={busy||state.pendingCreate} options={users.map(user=>({value:user.profileId,label:`${user.username} · ${user.profileName}`}))} onValueChange={setTarget}/>{state.users?.nextCursor&&<Button size="compact" variant="glass" disabled={busy||state.pendingCreate} onPress={()=>{setTarget('');if(current())void source.load('',state.users?.nextCursor);}}>{t('integrations.appPasswords.nextUsers')}</Button>}
    {state.options?.scopes.map(scope=><Switch key={scope} checked={scopes.includes(scope)} disabled={busy||state.pendingCreate||scopes.includes('admin')&&scope!=='admin'} onCheckedChange={checked=>setScopes(old=>scope==='admin'?(checked?['admin']:[]):checked?[...old.filter(s=>s!=='admin'),scope]:old.filter(s=>s!==scope))}>{t(`api.scope.${scope}` as MessageId)}</Switch>)}
   </View>
  </BottomSheet>
  <BottomSheet open={current()&&!!card} title={card?.name} description={t('integrations.credential.recoverable')} onOpenChange={open=>{if(!open)close();}} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={()=>{const held=card,ticket=view.ticket;if(!held||!current())return;void copyText(held.secret).then(()=>{if(current()&&view.ticket===ticket){setCopied(true);setCopyError(false);}}).catch(()=>{if(current()&&view.ticket===ticket)setCopyError(true);});}}>{t('web.twoStep.copy')}</Button><Button onPress={close}>{t('action.done')}</Button></View>}>
   <Text selectable style={{...shellType(13,500,18),color:theme.colors.text}}>{card?.secret}</Text>{copied&&<Text style={text}>{t('api.credential.copied')}</Text>}{copyError&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}
  </BottomSheet>
  <BottomSheet open={current()&&!!revoke} title={revoke?.name} description={t('integrations.appPasswords.revoke')} dismissOnBack={!busy} dismissOnGesture={!busy} dismissOnOutsidePress={!busy} onOpenChange={open=>{if(!open&&!busy)setRevoke(undefined);}} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" disabled={busy} onPress={()=>setRevoke(undefined)}>{t('action.cancel')}</Button><Button disabled={busy} onPress={()=>{const held=revoke;if(!held||!current())return;void source.revoke(held).then(()=>{if(current()&&!source.getSnapshot().error)setRevoke(undefined);});}}>{t('web.access.revoke')}</Button></View>}><View/></BottomSheet>
 </View>;
}
