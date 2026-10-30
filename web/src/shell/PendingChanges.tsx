import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useNavigate} from '@tanstack/react-router';
import {defaultI18n, type MessageId} from '@i18n';
import type {UserMutationRecoveryModel, UserMutationRecoveryRow} from '@core/user-mutation-recovery.ts';
import {Button, TextField} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import styles from './pending-changes.module.css';

export type PendingChangesBinding = Readonly<{scopeKey:string;model:UserMutationRecoveryModel;reportError:(error:unknown)=>void}>;
const categoryLabels:Record<UserMutationRecoveryRow['category'],MessageId>={saved:'next.home.shell.saved',settings:'next.home.shell.settings',playback:'server.feedback.category.playback'};
const statusLabels:Record<UserMutationRecoveryRow['status'],MessageId>={waiting:'recovery.status.waiting',review:'recovery.status.review',done:'recovery.status.done'};
const t=defaultI18n.t;

/** This mounted form alone owns input. Closing, navigation or a new shell scope
 * destroys it; retained commands stay with the coordinator. */
export function PendingChanges({model,reportError}:PendingChangesBinding) {
 const state=useSyncExternalStore(model.subscribe,model.snapshot,model.snapshot),navigate=useNavigate();
 const [open,setOpen]=useState(false),[editing,setEditing]=useState<string>(),[values,setValues]=useState<Record<string,string>>({}),[busy,setBusy]=useState(false);
 const openingOwner=useRef<object|undefined>(undefined),formOwner=useRef<object|undefined>(undefined);
 const opening=openingOwner.current,form=formOwner.current;
 const request=useRef<AbortController|undefined>(undefined),mounted=useRef(true);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;openingOwner.current=undefined;formOwner.current=undefined;request.current?.abort();};},[model]);
 const current=()=>mounted.current&&!!opening&&openingOwner.current===opening;
 const clear=()=>{formOwner.current=undefined;request.current?.abort();request.current=undefined;setEditing(undefined);setValues({});setBusy(false);};
 const close=()=>{if(!current())return;openingOwner.current=undefined;clear();setOpen(false);};
 const begin=()=>{if(!mounted.current)return;clear();openingOwner.current={};setOpen(true);};
 const run=async(action:(signal:AbortSignal)=>Promise<void>)=>{
  if(!current()||request.current)return;
  const controller=request.current=new AbortController();setBusy(true);
  try {await action(controller.signal);}catch(error){if(!controller.signal.aborted&&mounted.current)reportError(error);}
  finally {if(request.current===controller){request.current=undefined;if(mounted.current)setBusy(false);}}
 };
 const review=(row:UserMutationRecoveryRow)=>run(async signal=>{
  const destination=await model.review(row.key,signal);
  if(signal.aborted||!current())return;
  close();
  if(destination.kind==='saved')await navigate({to:'/saved'});
  else if(destination.kind==='settings')await navigate({to:'/settings',search:{...(destination.server?{scope:'server'}:{}),...(destination.section?{section:destination.section}:{})}});
  else await navigate({to:'/player'});
 });
 const selected=state.rows.find(row=>row.key===editing&&row.secretFields.length>0);
 // An entry changing disposition also destroys an obsolete form before it can submit.
 useEffect(()=>{if(editing&&!selected){formOwner.current=undefined;request.current?.abort();request.current=undefined;setEditing(undefined);setValues({});setBusy(false);}},[editing,selected]);
 if(!state.rows.length)return null;
 return <>
  <Button className={styles.trigger} size="small" variant="text" onClick={begin} aria-haspopup="dialog">{t('recovery.title')}</Button>
  <Dialog open={open} onOpenChange={value=>{if(value)begin();else close();}} title={t('recovery.title')} footer={<Button size="small" onClick={close}>{t('next.overlays.close')}</Button>}>
   <p className={styles.note}>{t(selected?'recovery.reentryNote':'recovery.reviewNote')}</p>
   {selected?<form className={styles.form} onSubmit={event=>{event.preventDefault();if(!current()||!form||formOwner.current!==form)return;void run(async signal=>{const input=values;formOwner.current=undefined;setValues({});try{await model.reenter(selected.key,input,signal);if(!signal.aborted&&current()){setEditing(undefined);setValues({});}}finally{if(!signal.aborted&&current())formOwner.current={};}});}}>
    {selected.secretFields.map(field=><TextField key={field.path} label={t(field.labelKey)} type="password" autoComplete="off" spellCheck={false} maxLength={field.maxLength} disabled={busy} value={values[field.path]??''} onChange={event=>{if(!current()||!form||formOwner.current!==form||request.current)return;const value=event.currentTarget.value;setValues(current=>({...current,[field.path]:value}));}}/>)}
    <div className={styles.actions}><Button size="small" disabled={busy} onClick={()=>{if(!current()||!form||formOwner.current!==form)return;formOwner.current=undefined;setEditing(undefined);setValues({});}}>{t('next.settings.cancel')}</Button><Button size="small" type="submit" loading={busy} disabled={selected.secretFields.some(field=>!values[field.path])}>{t('next.shell.save')}</Button></div>
   </form>:<ul className={styles.rows}>{state.rows.map(row=><li key={row.key} className={styles.row}>
    <div className={styles.summary}><strong>{t(categoryLabels[row.category])}</strong><span>{t(statusLabels[row.status])}</span><time dateTime={new Date(row.createdMs).toISOString()}>{defaultI18n.dateTime(row.createdMs)}</time></div>
    <div className={styles.actions}>
     <Button size="small" variant="text" disabled={busy} onClick={()=>{void run(()=>model.checkAgain(row.key));}}>{t('recovery.checkAgain')}</Button>
     <Button size="small" variant="text" disabled={busy} onClick={()=>{void review(row);}}>{t('recovery.review')}</Button>
     {row.secretFields.length>0&&<Button size="small" disabled={busy} onClick={()=>{if(!current())return;formOwner.current={};setEditing(row.key);setValues({});}}>{t('recovery.reenter')}</Button>}
     {row.canKeep&&<Button size="small" disabled={busy} onClick={()=>{void run(()=>model.keepAsIs(row.key));}}>{t('recovery.keepAsIs')}</Button>}
    </div>
   </li>)}</ul>}
  </Dialog>
 </>;
}
