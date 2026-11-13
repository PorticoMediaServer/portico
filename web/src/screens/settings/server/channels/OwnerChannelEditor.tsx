import {useEffect,useRef,useState} from 'react';
import {defaultI18n,type MessageId} from '@i18n';
import type {Library} from '@core/index.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button,Select,Switch,TextField} from '../../../../kit/controls';
import {Dialog} from '../../../../kit/overlays';
import {Artwork} from '../../../../kit/artwork/Artwork';
import type {ArtworkSource} from '../../../../kit/artwork/types';
import {OwnerSelection} from './OwnerSelection';
import {OwnerHours} from './OwnerHours';
import type {ChannelLookup} from './OwnerConditions';
import type {OwnerChannelSource,Editor} from './owner-channel-source';
import {validBuilder} from './owner-channel-draft';
import styles from './channels.module.css';
const t=defaultI18n.t;
export function OwnerChannelEditor({source,editor,lookup,libraries,manage,sampleArt}:{source:OwnerChannelSource;editor:Editor;lookup:ChannelLookup;libraries:readonly Library[];manage:()=>void;sampleArt?:(id:string)=>ArtworkSource|undefined}){
 const state=source.getSnapshot(),draft=editor.draft,builder=draft.builder,[advanced,setAdvanced]=useState(false),[tried,setTried]=useState(false),form=useRef<HTMLFieldSetElement>(null);
 const locked=state.busy||state.uncertain||state.pendingCreate||state.pendingPreset;let zoneValid=false;try{if(draft.timeZone){new Intl.DateTimeFormat(undefined,{timeZone:draft.timeZone});zoneValid=true;}}catch{/* Keep the owner's invalid text editable. */}
 const valid=!!draft.name.trim()&&zoneValid&&validBuilder(builder),change=(patch:Partial<typeof draft>)=>source.edit(editor,value=>({...value,...patch}));
 useEffect(()=>{if(!valid||locked||editor.conflict)return;const timer=setTimeout(()=>{if(source.getSnapshot().editor===editor&&lookup.current())void source.preview(editor);},600);return()=>clearTimeout(timer);},[source,editor,valid,locked,lookup]);
 const preview=state.previewFor===draft?state.preview:undefined;
 return <Dialog footerDismisses responsive={false} className={styles.editorDialog} headerClassName={styles.editorHeader} footerClassName={styles.editorFooter} bodyClassName={styles.editorBody} open title={editor.templateName?t('builder.reviewTemplate',{name:editor.templateName}):t(editor.document?'builder.editTitle':'builder.newTitle')} width={960} onOpenChange={open=>{if(!open)source.close(editor);}} dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy}
  footer={<>{preview&&<span className={styles.footerCount}>{t('builder.matches',{count:preview.titles})}</span>}<Button size="small" disabled={state.busy} onClick={()=>source.close(editor)}>{t('action.cancel')}</Button><Button size="small" variant="primary" disabled={state.busy||state.uncertain||editor.conflict} onClick={()=>{setTried(true);if(valid&&[...(form.current?.querySelectorAll<HTMLInputElement>('input')??[])].every(input=>input.reportValidity()))void source.save(editor);}}>{state.pendingCreate?t('action.tryAgain'):t(editor.document?'builder.save':'builder.create')}</Button></>}>
  <div className={styles.editorGrid}><fieldset ref={form} className={styles.editorFields} disabled={!!locked}>
   <TextField label={t('builder.name')} value={draft.name} required maxLength={120} error={tried&&!draft.name.trim()?t('builder.error.name'):undefined} onChange={event=>change({name:event.currentTarget.value})}/><TextField label={t('builder.description')} value={draft.description} maxLength={2048} onChange={event=>change({description:event.currentTarget.value})}/>
   <OwnerSelection lookup={lookup} libraries={libraries} value={builder} locked={!!locked} change={next=>change({builder:{...builder,...next}})}/>
   {tried&&!builder.libraries.length&&<p role="alert" className={styles.warning}>{t('builder.error.libraries')}</p>}{tried&&!builder.kinds.length&&<p role="alert" className={styles.warning}>{t('builder.error.kinds')}</p>}{tried&&!validBuilder(builder)&&<p role="alert" className={styles.warning}>{t('builder.error.conditions')}</p>}
   <label className={styles.control}><span>{t('builder.on')}</span><Switch checked={draft.on} aria-label={t('builder.on')} onCheckedChange={on=>change({on})}/></label>
   <OwnerHours source={source} lookup={lookup} libraries={libraries} value={builder} locked={!!locked} change={builder=>change({builder})} manage={manage}/><p className={styles.help}>{t('builder.audience')}</p>
   <Button size="small" variant="text" onClick={()=>setAdvanced(v=>!v)}>{t(advanced?'builder.hideAdvanced':'builder.advanced')}</Button>
   {advanced&&<section className={styles.editorSection}><TextField label={t('builder.avoidRepeats')} description={t('builder.avoidRepeatsHelp')} type="number" min={0} max={1000} step={1} value={builder.avoidRepeats??5} onChange={event=>{const n=event.currentTarget.valueAsNumber;if(Number.isInteger(n)&&n>=0&&n<=1000)change({builder:{...builder,avoidRepeats:n}});}}/>
    <Select label={t('builder.quality')} value={builder.quality.mode||'auto'} options={['auto','original','limited'].map(v=>({value:v,label:t(`builder.quality.${v==='auto'?'automatic':v}` as MessageId)}))} onValueChange={mode=>change({builder:{...builder,quality:{...builder.quality,mode,maxKbps:mode==='limited'?builder.quality.maxKbps||5000:builder.quality.maxKbps}}})}/>
    {builder.quality.mode==='limited'&&<TextField label={t('web.ownerChannels.maxKbps')} required type="number" min={500} max={200000} step={1} value={builder.quality.maxKbps??5000} onChange={event=>{const maxKbps=event.currentTarget.valueAsNumber;if(Number.isInteger(maxKbps))change({builder:{...builder,quality:{...builder.quality,maxKbps}}});}}/>}
    <TextField label={t('builder.timeZone')} value={draft.timeZone} required error={tried&&!zoneValid?t('builder.error.timeZone'):undefined} onChange={event=>change({timeZone:event.currentTarget.value})}/>
    <label><input type="checkbox" checked={!!builder.logoOnScreen.on} disabled={!editor.document?.logo} onChange={event=>change({builder:{...builder,logoOnScreen:{...builder.logoOnScreen,on:event.target.checked}}})}/>{t('web.ownerChannels.overlay')}</label>{!editor.document?.logo&&<p className={styles.help}>{t('web.ownerChannels.needsLogo')}</p>}
    {builder.logoOnScreen.on&&<><Select label={t('web.ownerChannels.corner')} value={builder.logoOnScreen.corner||'topRight'} options={['topRight','topLeft','bottomRight','bottomLeft'].map(v=>({value:v,label:t(`web.ownerChannels.${v}` as MessageId)}))} onValueChange={corner=>change({builder:{...builder,logoOnScreen:{...builder.logoOnScreen,corner}}})}/><Select label={t('web.ownerChannels.size')} value={builder.logoOnScreen.size||'medium'} options={['small','medium','large'].map(v=>({value:v,label:t(`web.ownerChannels.${v}` as MessageId)}))} onValueChange={size=>change({builder:{...builder,logoOnScreen:{...builder.logoOnScreen,size}}})}/></>}
   </section>}
  </fieldset><aside className={styles.preview} aria-busy={state.previewLoading}><h3>{t('builder.preview')}</h3>{state.previewLoading&&<p role="status">{t('builder.previewing')}</p>}{!preview&&!state.previewLoading&&<p>{t('builder.previewEmpty')}</p>}
   {preview&&<><b>{t('builder.matches',{count:preview.titles})}</b><p>{t('builder.hours',{hours:Number(preview.hours.toFixed(1))})}</p>{preview.unplaceable>0&&<p>{t('builder.unresolved',{count:preview.unplaceable})}</p>}{!preview.titles&&<p>{t('builder.previewNoMatches')}</p>}<h4>{t('builder.samples')}</h4><ul className={styles.sampleList}>{preview.sample.map(row=><li key={row.id}><Artwork source={sampleArt?.(row.id)} alt="" width={28} height={42}/><span>{[row.title,row.episodeTitle,row.year].filter(Boolean).join(' · ')}</span></li>)}</ul><h4>{t('builder.firstDay')}</h4><ul>{preview.today.map((row,i)=><li key={i}>{defaultI18n.formatDate(row.startsAt,{hour:'numeric',minute:'2-digit',timeZone:draft.timeZone})} · {row.title?.title??t('builder.nothingScheduled')}</li>)}</ul></>}
  </aside></div>
  {!!state.error&&<p role="alert" className={styles.warning}>{state.pendingCreate?t('error.unconfirmedChange'):errorMessage(state.error)}</p>}
  {editor.conflict&&<div role="alert" className={styles.warning}><p>{t('web.ownerChannels.changed')}</p><Button size="small" disabled={state.busy} onClick={()=>source.acceptLocal(editor)}>{t('web.ownerChannels.keepEdits')}</Button></div>}
  {editor.document&&<Button size="small" disabled={state.busy} onClick={()=>void source.reloadEditor(editor)}>{t('action.refresh')}</Button>}
 </Dialog>;
}
