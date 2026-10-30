import {useLayoutEffect,useRef,useState,useSyncExternalStore} from 'react';
import {defaultI18n,type MessageId} from '@i18n';
import type {ApiAccessTransport,ApiScope} from '@core/api-access.ts';
import type {IntegrationAppPassword} from '@core/integration-access.ts';
import {SettingsIntegrationsSource} from '@core/server-admin/integrations-source.ts';
import {Button,Select,TextField} from '../../../../kit/controls';
import {Dialog} from '../../../../kit/overlays/Dialog';
import type {SettingsApiKeysSource} from './api-keys-source';
import styles from './integrations.module.css';
const t=defaultI18n.t;
let groupSequence=0;
type Group=Readonly<{id:number;owner:SettingsApiKeysSource;source:SettingsIntegrationsSource;current:()=>boolean}>;
type Props=Readonly<{api:ApiAccessTransport;owner:SettingsApiKeysSource;isCurrent:()=>boolean;reportError:(e:unknown)=>void}>;
export function AppPasswordsPanel({api,owner,isCurrent,reportError}:Props){
 const [group,setGroup]=useState<Group>();
 useLayoutEffect(()=>{let live=true;const current=()=>live&&isCurrent()&&owner.getSnapshot().phase!=='idle';const source=new SettingsIntegrationsSource(api,current,reportError);setGroup({id:++groupSequence,owner,source,current});void source.load();return()=>{live=false;source.dispose();};},[api,owner,isCurrent,reportError]);
 return group?.owner===owner&&group.current()?<AppPasswords key={group.id} group={group}/>:null;
}
function AppPasswords({group}:Readonly<{group:Group}>){
 const source=group.source,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),generation=useRef<object>({});
 const [creating,setCreating]=useState(false),[name,setName]=useState(''),[target,setTarget]=useState(''),[scopes,setScopes]=useState<readonly ApiScope[]>(['read']);
 const [card,setCard]=useState<Readonly<{secret:string;name:string}>>(),[copied,setCopied]=useState(false),[revoke,setRevoke]=useState<IntegrationAppPassword>();
 useLayoutEffect(()=>()=>{generation.current={};},[source]);
 const current=()=>group.current()&&source.getSnapshot().phase!=='idle';
 const close=()=>{generation.current={};setCard(undefined);setCopied(false);};
 const reveal=async(app:IntegrationAppPassword)=>{if(!current())return;close();const ticket={};generation.current=ticket;const secret=await source.reveal(app);if(secret&&current()&&generation.current===ticket)setCard({secret,name:app.name});};
 const create=async()=>{if(!current())return;const user=state.users?.items.find(u=>u.profileId===target&&!u.disabled);if(!state.pendingCreate&&!user)return;const ticket={};generation.current=ticket;const result=await(state.pendingCreate?source.retryCreate():source.create({name,scopes,accountId:user!.accountId,profileId:user!.profileId}));if(!result||!current()||generation.current!==ticket)return;setCreating(false);if(result.secret)setCard({secret:result.secret,name:result.appPassword.name});};
 const users=state.users?.items.filter(u=>!u.disabled)??[],busy=state.busy||state.phase==='loading';
 return <section className={styles.page} aria-label={t('integrations.appPasswords.title')}>
  <div className={styles.heading}><h3>{t('integrations.appPasswords.title')}</h3><Button size="xs" variant="glass" disabled={busy||!state.options} onClick={()=>{if(!current())return;setCreating(true);if(!state.pendingCreate){setName('');setScopes(['read']);setTarget(users[0]?.profileId??'');}}}>{t('integrations.appPasswords.create')}</Button></div>
  <p className={styles.lede}>{t('integrations.appPasswords.help')}</p>
  {state.error&&<p className={styles.failure} role="alert">{t(state.pendingCreate?'error.unconfirmedChange':'error.unavailable')}</p>}
  <ul className={styles.keys}>{state.apps?.items.map(app=><li key={app.id} className={styles.keyRow}><div className={styles.keyText}><b>{app.name}</b><div className={styles.facts}><span>{app.username}</span><span>{app.scopes.map(scope=>t(`api.scope.${scope}` as MessageId)).join(', ')}</span><span>{t(`api.credential.state.${app.state}` as MessageId)}</span></div></div>{app.state==='active'&&<div className={styles.actions}><Button size="xs" variant="text" disabled={busy} onClick={()=>{void reveal(app);}}>{t('integrations.credential.reveal')}</Button><Button size="xs" variant="text" disabled={busy} onClick={()=>{if(current())setRevoke(app);}}>{t('web.access.revoke')}</Button></div>}</li>)}</ul>
  {state.apps?.items.length===0&&<p className={styles.note}>{t('integrations.appPasswords.empty')}</p>}
  <div className={styles.actions}><Button size="xs" variant="glass" disabled={busy} onClick={()=>{if(current())void source.load();}}>{t('action.refresh')}</Button>{state.apps?.nextCursor&&<Button size="xs" variant="glass" disabled={busy} onClick={()=>{if(current())void source.load(state.apps?.nextCursor);}}>{t('action.showMore')}</Button>}</div>
  <Dialog open={current()&&creating} onOpenChange={open=>{if(!open&&!state.busy){generation.current={};setCreating(false);}}} title={t('integrations.appPasswords.create')} width={500} dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} footer={<><Button variant="glass" size="compact" disabled={state.busy} onClick={()=>{generation.current={};setCreating(false);}}>{t('action.cancel')}</Button><Button size="compact" disabled={busy||!state.pendingCreate&&(!name.trim()||!target||!scopes.length)} onClick={()=>{void create();}}>{t(state.pendingCreate?'action.tryAgain':'web.saved.create')}</Button></>}>
   <div className={styles.form}>{state.error&&<p className={styles.failure} role="alert">{t(state.pendingCreate?'error.unconfirmedChange':'error.unavailable')}</p>}<TextField label={t('profile.name')} value={name} maxLength={80} disabled={busy||state.pendingCreate} onChange={e=>setName(e.target.value)}/><Select label={t('integrations.appPasswords.target')} value={target} disabled={busy||state.pendingCreate} options={users.map(user=>({value:user.profileId,label:`${user.username} · ${user.profileName}`}))} onValueChange={setTarget}/>{state.users?.nextCursor&&<Button size="xs" variant="text" disabled={busy||state.pendingCreate} onClick={()=>{setTarget('');if(current())void source.load('',state.users?.nextCursor);}}>{t('integrations.appPasswords.nextUsers')}</Button>}
   <fieldset className={styles.scopes} disabled={busy||state.pendingCreate}><legend>{t('web.access.scopeLabel')}</legend>{state.options?.scopes.map(scope=><label key={scope} className={styles.scope}><input type="checkbox" checked={scopes.includes(scope)} disabled={scopes.includes('admin')&&scope!=='admin'} onChange={e=>setScopes(old=>scope==='admin'?(e.target.checked?['admin']:[]):e.target.checked?[...old.filter(s=>s!=='admin'),scope]:old.filter(s=>s!==scope))}/><span><b>{t(`api.scope.${scope}` as MessageId)}</b><small>{t(`api.scope.${scope}.help` as MessageId)}</small></span></label>)}</fieldset></div>
  </Dialog>
  <Dialog open={current()&&!!card} onOpenChange={open=>{if(!open)close();}} title={card?.name} width={500} footer={<Button size="compact" onClick={close}>{t('action.done')}</Button>}><div className={styles.form}><p>{t('integrations.credential.recoverable')}</p><label className={styles.secretLabel}>{t('integrations.credential.secret')}<textarea className={styles.secret} readOnly rows={3} value={card?.secret??''}/></label><Button size="xs" variant="glass" onClick={()=>{const held=card,ticket=generation.current;if(!held||!current())return;void navigator.clipboard.writeText(held.secret).then(()=>{if(current()&&ticket===generation.current)setCopied(true);}).catch(()=>{if(current()&&ticket===generation.current)setCopied(false);});}}>{t('web.twoStep.copy')}</Button>{copied&&<p role="status">{t('api.credential.copied')}</p>}</div></Dialog>
  <Dialog open={current()&&!!revoke} onOpenChange={open=>{if(!open&&!busy)setRevoke(undefined);}} title={revoke?.name} description={t('integrations.appPasswords.revoke')} footer={<><Button variant="glass" disabled={busy} onClick={()=>setRevoke(undefined)}>{t('action.cancel')}</Button><Button disabled={busy} onClick={()=>{const held=revoke;if(!held||!current())return;void source.revoke(held).then(()=>{if(current()&&!source.getSnapshot().error)setRevoke(undefined);});}}>{t('web.access.revoke')}</Button></>}><div/></Dialog>
 </section>;
}
