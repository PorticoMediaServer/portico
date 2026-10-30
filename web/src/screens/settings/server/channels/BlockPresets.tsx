import {channelFieldLabel, channelFieldMessage} from '@core/server-admin/channel-catalogue.ts';
import {useCallback, useEffect, useLayoutEffect,useRef, useMemo, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {Library} from '@core/index.ts';
import type {ChannelApi} from '@core/channel-guide.ts';
import {Button, RichSelect, TextField} from '../../../../kit/controls';
import {Dialog} from '../../../../kit/overlays';
import {ChannelFieldsSource} from './channel-fields-source';
import type {BlockPreset, BlockPresetBody, BlockPresetsSource} from './block-presets-source';
import styles from './channels.module.css';
const t=defaultI18n.t;
const days=['monday','tuesday','wednesday','thursday','friday','saturday','sunday'];
const dayLabel=(index:number)=>new Intl.DateTimeFormat(undefined,{weekday:'short',timeZone:'UTC'}).format(Date.UTC(2023,0,2+index));
const minuteText=(value:number)=>`${String(Math.floor(value/60)).padStart(2,'0')}:${String(value%60).padStart(2,'0')}`;
type Props=Readonly<{source:BlockPresetsSource;api:ChannelApi;libraries:readonly Library[];isCurrent:()=>boolean;reportError:(error:unknown)=>void;close:()=>void}>;
type Opening=Readonly<{id:number;original?:BlockPreset}>;
let sequence=0;
export function BlockPresets(props:Props){
  const {source,isCurrent,close}=props,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot);
  const [form,setForm]=useState<Opening>(),[removing,setRemoving]=useState<BlockPreset>();
  const scope=useMemo(()=>({active:true,source,form:undefined as Opening|undefined,removing:undefined as BlockPreset|undefined}),[source]);
  useLayoutEffect(()=>{scope.active=true;return()=>{scope.active=false;scope.form=undefined;scope.removing=undefined;};},[scope]);
  const current=useCallback(()=>scope.active&&scope.source===source&&isCurrent(),[scope,source,isCurrent]);
  const formCurrent=useCallback(()=>current()&&!!form&&scope.form===form,[current,form,scope]);
  useEffect(()=>{if(!source.getSnapshot().loaded&&!source.getSnapshot().pending)void source.load();},[source]);
  const open=(original?:BlockPreset)=>{if(!current()||source.getSnapshot().busy||source.getSnapshot().pending||source.getSnapshot().reconcile||original&&!source.getSnapshot().items.includes(original))return;const next={id:++sequence,original};scope.form=next;setForm(next);};
  const closeForm=()=>{if(!formCurrent()||source.getSnapshot().busy)return;scope.form=undefined;setForm(undefined);};
  const finish=()=>{if(!current()||source.getSnapshot().busy)return;scope.active=false;scope.form=undefined;scope.removing=undefined;close();};
  const closeRemoval=()=>{if(!current()||scope.removing!==removing||source.getSnapshot().busy)return;scope.removing=undefined;setRemoving(undefined);};
  const remove=async()=>{const record=removing;if(!record||!current()||scope.removing!==record||!source.getSnapshot().items.includes(record))return;const done=await source.remove(record);if(done&&current()&&scope.removing===record)closeRemoval();};
  const blocked=state.busy||!!state.pending||state.reconcile;
  return <><Dialog open={current()} width={960} title={t('builder.preset.title')} description={t('builder.preset.help')} onOpenChange={value=>{if(!value)finish();}} dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy} footer={<Button size="compact" variant="primary" disabled={state.busy} onClick={finish}>{t('action.done')}</Button>}>
    <div className={styles.presetGrid}><section className={styles.editorFields}>
      {state.items.map(record=><div className={styles.condition} key={record.id}><b>{record.name}</b><p className={styles.help}>{record.body.description}</p><p className={styles.help}>{props.libraries.find(library=>library.id===record.body.libraryId)?.name??t('error.unavailable')} · {t('duration.long.minutes',{minutes:record.body.durationMinutes})}</p><div className={styles.choices}><Button size="small" variant="glass" disabled={blocked} onClick={()=>open(record)}>{t('web.selection.edit')}</Button><Button size="small" variant="text" className={styles.danger} disabled={blocked} onClick={()=>{if(!current()||!source.getSnapshot().items.includes(record)||source.getSnapshot().busy)return;scope.removing=record;setRemoving(record);}}>{t('action.delete')}</Button></div></div>)}
      {state.cursor&&<Button size="small" disabled={blocked} onClick={()=>{if(current())void source.load(true);}}>{t('action.showMore')}</Button>}
      <Button size="small" variant="glass" disabled={blocked||!state.loaded} onClick={()=>open()}>{t('builder.preset.new')}</Button>
      {state.busy&&<p role="status" className={styles.help}>{t('next.settings.loading')}</p>}
      {state.error&&<p role="alert" className={styles.warning}>{t(state.pending||state.reconcile?'error.unconfirmedChange':'error.unavailable')}</p>}
      {(state.pending||state.reconcile||!state.loaded&&state.error)&&<Button size="small" disabled={state.busy} onClick={()=>{if(current())void(state.pending?source.retry():source.load());}}>{t('action.tryAgain')}</Button>}
    </section>{form&&<PresetForm key={form.id} {...props} original={form.original} isCurrent={formCurrent} close={closeForm}/>}</div>
  </Dialog><Dialog open={!!removing&&current()&&scope.removing===removing} title={t('action.delete')} description={t('builder.preset.help')} onOpenChange={value=>{if(!value)closeRemoval();}} dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy} footer={<><Button size="compact" variant="glass" disabled={state.busy} onClick={closeRemoval}>{t('action.cancel')}</Button><Button size="compact" variant="glass" className={styles.danger} disabled={blocked} onClick={()=>{void remove();}}>{t('action.delete')}</Button></>}>{removing?.name}</Dialog></>;
}
function PresetForm({source,api,libraries,isCurrent,reportError,close,original}:Props&{original?:BlockPreset}){
  const state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot);
  const [draft,setDraft]=useState(()=>({name:original?.name??'',body:original?structuredClone(original.body):{libraryId:'',criteria:[],order:'shuffle',durationMinutes:240,startMinute:-1,days:[],description:''} as BlockPresetBody}));
  const [authority]=useState(()=>({draft})),[fields,setFields]=useState<ChannelFieldsSource>(),[error,setError]=useState(false);
  const libraryScope=JSON.stringify(libraries.map(library=>[library.id,library.kind]));
  const setupLibraries=useRef(libraries);useLayoutEffect(()=>{setupLibraries.current=libraries;});
  useLayoutEffect(()=>{const next=new ChannelFieldsSource(api,setupLibraries.current,isCurrent,reportError);setFields(next);return next.dispose;},[api,libraryScope,isCurrent,reportError]);
  useEffect(()=>{if(fields)void fields.select(draft.body.libraryId?[draft.body.libraryId]:[]);},[fields,draft.body.libraryId]);
  const locked=state.busy||!!state.pending||state.reconcile;
  const change=(next:typeof draft)=>{if(!isCurrent()||locked||source.getSnapshot().busy||source.getSnapshot().pending||source.getSnapshot().reconcile||authority.draft!==draft)return;authority.draft=next;setDraft(next);setError(false);};
  const body=(patch:Partial<BlockPresetBody>)=>change({...draft,body:{...draft.body,...patch}});
  const save=async()=>{if(!isCurrent()||locked||authority.draft!==draft||!draft.name.trim()||!libraries.some(library=>library.id===draft.body.libraryId)){setError(true);return;}const captured=draft;const done=await source.save(original,draft.name,draft.body);if(!isCurrent()||authority.draft!==captured)return;if(done)close();else setError(true);};
  return <section className={styles.editorFields}><h3>{t(original?'web.selection.edit':'builder.preset.newTitle')}</h3>
    <TextField label={t('builder.name')} value={draft.name} maxLength={120} disabled={locked} onChange={event=>change({...draft,name:event.target.value})}/>
    <TextField label={t('builder.description')} value={draft.body.description} maxLength={400} disabled={locked} onChange={event=>body({description:event.target.value})}/>
    <RichSelect aria-label={t('builder.preset.library')} value={draft.body.libraryId} disabled={locked} options={libraries.filter(library=>['movie','tv','anime'].includes(library.kind)).map(library=>({value:library.id,label:library.name}))} onValueChange={libraryId=>{if(libraries.some(library=>library.id===libraryId))body({libraryId});}}/>
    {fields&&<PresetCriteria source={fields} criteria={draft.body.criteria} locked={locked} onChange={criteria=>body({criteria})}/>}
    <RichSelect aria-label={t('builder.preset.order')} value={draft.body.order} disabled={locked} options={[{value:'shuffle',label:t('builder.order.shuffle')},{value:'sequential',label:t('builder.order.inOrder')},{value:'recent',label:t('builder.sort.recent')},{value:'least-played',label:t('builder.preset.leastPlayed')}]} onValueChange={order=>{if(['shuffle','sequential','recent','least-played'].includes(order))body({order});}}/>
    <TextField label={t('builder.preset.length')} type="number" min={5} max={1440} value={draft.body.durationMinutes} disabled={locked} onChange={event=>body({durationMinutes:Number(event.target.value)})}/><p className={styles.help}>{t('builder.preset.minutes')}</p>
    <label className={styles.help}><input type="checkbox" checked={draft.body.startMinute===-1} disabled={locked} onChange={event=>body({startMinute:event.target.checked?-1:0})}/>{t('builder.preset.anywhere')}</label>
    {draft.body.startMinute!==-1&&<TextField label={t('builder.preset.starts')} type="time" value={minuteText(draft.body.startMinute)} disabled={locked} onChange={event=>{const value=event.target.value;if(/^([01]\d|2[0-3]):[0-5]\d$/.test(value))body({startMinute:Number(value.slice(0,2))*60+Number(value.slice(3))});}}/>}
    <div className={styles.choices}>{days.map((day,index)=><label key={day}><input type="checkbox" disabled={locked} checked={draft.body.days.includes(day)} onChange={event=>body({days:event.target.checked?[...draft.body.days,day]:draft.body.days.filter(value=>value!==day)})}/>{dayLabel(index)}</label>)}</div>
    {error&&<p role="alert" className={styles.warning}>{t('builder.error.rejected')}</p>}
    <div className={styles.choices}><Button size="small" variant="glass" disabled={state.busy} onClick={close}>{t('builder.cancel')}</Button><Button size="small" variant="primary" disabled={locked} onClick={()=>{void save();}}>{t('builder.preset.save')}</Button></div>
  </section>;
}
function PresetCriteria({source,criteria,locked,onChange}:Readonly<{source:ChannelFieldsSource;criteria:BlockPresetBody['criteria'];locked:boolean;onChange:(criteria:BlockPresetBody['criteria'])=>void}>){
  const state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),fields=state.fields.filter(entry=>channelFieldMessage(entry.field) !== undefined);
  const replace=(index:number,patch:Partial<BlockPresetBody['criteria'][number]>)=>onChange(criteria.map((row,i)=>i===index?{...row,...patch}:row));
  return <section className={styles.editorSection}><h3>{t('builder.preset.selection')}</h3>{criteria.map((clause,index)=><div key={index} className={styles.condition}>
    <RichSelect aria-label={t('builder.preset.selection')} value={clause.field} disabled={locked} options={fields.map(entry=>({value:entry.field.id,label:channelFieldLabel(entry.field, t) ?? t('builder.opaqueConditions')}))} onValueChange={field=>{if(fields.some(entry=>entry.field.id===field))replace(index,{field});}}/>
    <RichSelect aria-label={t('builder.preset.includes')} value={clause.operator} disabled={locked} options={[{value:'includes',label:t('builder.preset.includes')},{value:'excludes',label:t('builder.preset.excludes')}]} onValueChange={operator=>{if(operator==='includes'||operator==='excludes')replace(index,{operator});}}/>
    {clause.values.map((value,i)=><div className={styles.choices} key={i}><TextField aria-label={t('builder.preset.selection')} value={value} maxLength={200} disabled={locked} onChange={event=>replace(index,{values:clause.values.map((v,n)=>n===i?event.target.value:v)})}/><Button size="small" variant="text" disabled={locked} onClick={()=>replace(index,{values:clause.values.filter((_,n)=>n!==i)})}>{t('builder.remove')}</Button></div>)}
    <Button size="small" variant="text" disabled={locked||clause.values.length>=200} onClick={()=>replace(index,{values:[...clause.values,'']})}>{t('player.opt.add')}</Button>
    <Button size="small" variant="text" disabled={locked} onClick={()=>onChange(criteria.filter((_,i)=>i!==index))}>{t('builder.remove')}</Button>
  </div>)}<Button size="small" variant="glass" disabled={locked||!fields.length||criteria.length>=16} onClick={()=>{const field=fields[0];if(field)onChange([...criteria,{field:field.field.id,operator:'includes',values:['']}]);}}>{t('builder.preset.clause')}</Button></section>;
}
