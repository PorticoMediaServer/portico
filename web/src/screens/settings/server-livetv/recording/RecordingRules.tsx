import {useEffect,useRef,useState,useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {GeneratedArt} from '../../../../kit/artwork';
import ruleStyles from './recording-rules.module.css';
import {MetadataFacts} from '../../../../kit/MetadataFacts';
import {Button,Select,Switch,TextField} from '../../../../kit/controls';
import {Dialog,Menu,useRetained} from '../../../../kit/overlays';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import type {RecordingRulesController,OwnerRule,RuleDraft} from './rules-controller';
import type {RecordingPermissionsController} from './permissions-controller';
import styles from '../../../live/owner/owner.module.css';
import fields from '../source-settings.module.css';
const t=defaultI18n.t;
export function RecordingRules({controller,permissions,sources}:{controller:RecordingRulesController;permissions:RecordingPermissionsController;sources:readonly {id:string;name:string}[]}){
 const state=useSyncExternalStore(controller.subscribe,controller.getSnapshot,controller.getSnapshot),policy=useSyncExternalStore(permissions.subscribe,permissions.getSnapshot,permissions.getSnapshot);
 const [remove,setRemove]=useState<OwnerRule>(),[cancelBookings,setCancelBookings]=useState(false),[menu,setMenu]=useState<string>();const editor=useRef<HTMLDivElement>(null);
 useEffect(()=>{void controller.load();},[controller]);
 const draft=state.draft,shown=useRetained(draft),shownRemove=useRetained(remove);
 if(!controller.isActive())return null;
 const busy=state.saving||state.uncertain,owners=policy.owners.filter(row=>row.canRecord),chosen=state.programmes.find(row=>row.id===draft?.programme);
 const valid=!!draft&&(!!draft.base||!!chosen&&draft.byTitle!==undefined&&owners.some(row=>row.profileId===draft.profileId))&&!state.guideLoading&&!state.previewLoading&&!busy;
 const edit=(patch:Partial<RuleDraft>)=>{if(draft)controller.edit(draft,patch);};
 const keep=shown?.keepNewest&&shown.keepDays?'custom':shown?.keepNewest?'newest':shown?.keepDays?'days':'all';
 return <section className={styles.section}><div className={styles.heading}><h3>{t('web.recordingRules.title')}</h3><Button size="small" disabled={state.loading||!!busy||!!draft} onClick={()=>controller.open()}>{t('web.recordingRules.newRule')}</Button></div>
  <p className={styles.lede}>{t('web.recordingRules.lede')}</p>
  {!!state.error&&<p role="alert" className={styles.error}>{errorMessage(state.error)}<Button size="small" disabled={state.loading||state.saving} onClick={()=>void controller.load()}>{t('action.tryAgain')}</Button></p>}
  {state.loading&&<p role="status">{t('status.loading')}</p>}
  {!state.loading&&!state.rules.length&&<p>{t('web.recordingRules.noRules')}</p>}
  {state.rules.map(rule=><SettingsRow key={rule.id} label={<span className={ruleStyles.label}><span className={ruleStyles.art}><GeneratedArt glyph={Array.from(rule.title.trim())[0]?.toLocaleUpperCase()??''}/></span><span>{rule.title}</span><span className={styles.badge} data-tone={rule.paused?undefined:'healthy'} data-paused={rule.paused||undefined}>{t(rule.paused?'web.recordingRules.pausedRule':'web.recordingRules.onRule')}</span></span>} help={<MetadataFacts parts={[rule.profileName,sources.find(source=>source.id===rule.source)?.name,rule.channelName,rule.byTitle?t('web.recordingRules.titleMatch'):undefined,rule.newOnly?t('guide.newOnly'):undefined,rule.paused?t('web.recordingRules.pausedRule'):undefined]}/>}>
   <Button size="small" disabled={!!busy||!!draft} onClick={()=>controller.open(rule)}>{t('web.recordingRules.editRule')}</Button>
   <Menu open={menu===rule.id} onOpenChange={open=>setMenu(open?rule.id:undefined)} title={rule.title} trigger={<Button size="small" variant="icon" icon="i-more" iconOnly disabled={!!busy||!!draft} aria-label={t('server.row.actions',{name:rule.title})}/>} items={[
    {id:'pause',label:t(rule.paused?'web.recordingRules.resumeRule':'web.recordingRules.pauseRule'),onSelect:()=>void controller.toggle(rule)},
    {id:'remove',label:t('web.recordingRules.remove'),destructive:true,onSelect:()=>{setCancelBookings(false);setRemove(rule);}},
   ]}/>
  </SettingsRow>)}
  <Button size="small" variant="text" disabled={state.loading||state.saving||!!draft&&!state.uncertain} onClick={()=>void controller.load()}>{t('action.refresh')}</Button>
  {shown&&<Dialog footerDismisses open={!!draft} title={t(shown.base?'web.recordingRules.editTitle':'web.recordingRules.newTitle')} width={560} onOpenChange={open=>{if(!open)controller.close(shown);}} footer={<><Button size="small" variant="text" disabled={state.saving} onClick={()=>controller.close(shown)}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={state.saving} disabled={!valid} onClick={()=>{if(editor.current&&[...editor.current.querySelectorAll<HTMLInputElement>('input')].every(input=>input.reportValidity()))void controller.save(shown);}}>{t('action.save')}</Button></>}>
   <div className={fields.fields} ref={editor}>
    {!!state.error&&<p role="alert" className={styles.error}>{errorMessage(state.error)}</p>}
    <Select label={t('web.recordingRules.recordFor')} value={shown.profileId} disabled={!!busy||!!shown.base||policy.loading} options={[{value:'',label:t('web.recordingRules.chooseProfile')},...owners.map(row=>({value:row.profileId,label:`${row.accountName} · ${row.profileName}`})),...(shown.base&&!owners.some(row=>row.profileId===shown.profileId)?[{value:shown.profileId,label:shown.base.profileName}]:[])]} onValueChange={profileId=>edit({profileId})}/>
    {!shown.base?<>
     <Select label={t('live.guideSource')} value={state.sourceId} disabled={!!busy} options={[{value:'',label:t('web.live.allSources')},...sources.map(row=>({value:row.id,label:row.name}))]} onValueChange={source=>void controller.guide(shown,state.day,source)}/>
     <TextField type="date" label={t('web.recordingRules.guideDate')} value={state.day} disabled={!!busy} onChange={event=>void controller.guide(shown,event.currentTarget.value,state.sourceId)}/>
     <Button size="small" variant="text" loading={state.guideLoading} disabled={!!busy} onClick={()=>void controller.guide(shown,state.day,state.sourceId)}>{t('web.recordingRules.firstChannels')}</Button>
     <Select label={t('web.recordingRules.seriesFromGuide')} value={shown.programme??''} disabled={!!busy||!shown.profileId||state.guideLoading} options={[{value:'',label:t('web.recordingRules.chooseProgram')},...state.programmes.map(row=>({value:row.id,label:`${row.title} · ${row.channelName} · ${defaultI18n.dateTime(row.startsAt)}`}))]} onValueChange={id=>{const row=state.programmes.find(row=>row.id===id);if(row)controller.choose(shown,row);}}/>
     {state.guideCursor&&<Button size="small" disabled={!!busy||state.guideLoading} onClick={()=>void controller.guide(shown,state.day,state.sourceId,state.guideCursor)}>{t('web.live.empty.moreTitle')}</Button>}
    </>:<p>{shown.title}</p>}
    {shown.byTitle&&<p>{t('web.recordingRules.titleMatch')}</p>}
    <SettingsRow label={t('web.recordingRules.anyChannel')}><Switch checked={shown.anyChannel} disabled={!!busy||shown.byTitle!==false} aria-label={t('web.recordingRules.anyChannel')} onCheckedChange={anyChannel=>edit({anyChannel})}/></SettingsRow>
    {shown.base&&!shown.anyChannel&&<><Select label={t('web.recordingRules.channel')} value={shown.channel} disabled={!!busy} options={[{value:shown.base.channel??'',label:shown.base.channelName??t('web.recordingRules.channel')},...state.channels.filter(row=>row.id!==shown.base!.channel).map(row=>({value:row.id,label:row.name}))]} onValueChange={channel=>edit({channel})}/><Button size="small" variant="text" disabled={!!busy} onClick={()=>void controller.channels(shown,state.channelCursor)}>{t('web.live.empty.moreTitle')}</Button></>}
    <SettingsRow label={t('guide.newOnly')}><Switch checked={shown.newOnly} disabled={!!busy||!shown.newOnly&&!state.newOnlyAvailable} aria-label={t('guide.newOnly')} onCheckedChange={newOnly=>edit({newOnly})}/></SettingsRow>
    {(['padBeforeMs','padAfterMs'] as const).map(key=><TextField key={key} label={t(key==='padBeforeMs'?'web.recordingRules.startEarlySeconds':'web.recordingRules.endLateSeconds')} type="number" min={0} max={key==='padBeforeMs'?1800:7200} step={1} value={shown[key]/1000} disabled={!!busy} onChange={event=>{const value=event.currentTarget.valueAsNumber;if(Number.isInteger(value)&&value>=0&&value<=(key==='padBeforeMs'?1800:7200))edit({[key]:value*1000});}}/>)}
    <Select label={t('web.recordingRules.priority')} value={String(shown.priority)} disabled={!!busy} options={[-1,0,1].map(value=>({value:String(value),label:t(value===1?'web.recordingRules.high':value===-1?'web.recordingRules.low':'web.recordingRules.normal')}))} onValueChange={value=>edit({priority:Number(value)})}/>
    <Select label={t('web.dvr.keepLabel')} value={keep} disabled={!!busy} options={[{value:'all',label:t('web.logs.everything')},{value:'newest',label:t('web.recordingRules.latestFew')},{value:'days',label:t('web.dvrSettings.keepDays')},{value:'custom',label:t('next.settingsSpec.server.live.groups.7.rows.1.control.options.4.label'),disabled:true}]} onValueChange={value=>edit({keepNewest:value==='newest'?shown.keepNewest||5:0,keepDays:value==='days'?shown.keepDays||30:0})}/>
    {(keep==='newest'||keep==='custom')&&<TextField label={t('web.dvrSettings.howMany')} type="number" min={1} max={100000} value={shown.keepNewest} disabled={!!busy} onChange={event=>{const value=event.currentTarget.valueAsNumber;if(Number.isInteger(value)&&value>0)edit({keepNewest:value});}}/>}
    {(keep==='days'||keep==='custom')&&<TextField label={t('web.maintenance.days')} type="number" min={1} max={3650} value={shown.keepDays} disabled={!!busy} onChange={event=>{const value=event.currentTarget.valueAsNumber;if(Number.isInteger(value)&&value>0)edit({keepDays:value});}}/>}
    <SettingsRow label={t('web.recordingRules.pausedRule')}><Switch checked={shown.paused} disabled={!!busy} aria-label={t('web.recordingRules.pausedRule')} onCheckedChange={paused=>edit({paused})}/></SettingsRow>
    <Button size="small" variant="text" loading={state.previewLoading} disabled={!!busy||!shown.base&&!shown.programme} onClick={()=>void controller.preview(shown)}>{t('web.recordingRules.preview')}</Button>
    {state.preview&&<><p>{t('web.recordingRules.previewCount',{count:state.preview.matches})}</p>{state.preview.airings.map((airing,index)=><p key={index}>{airing.title} · {airing.channel} · {defaultI18n.dateTime(airing.startsAt)}</p>)}</>}
   </div>
  </Dialog>}
  {shownRemove&&<Dialog footerDismisses open={!!remove} title={t('web.recordingRules.removeTitle',{name:shownRemove.title})} description={t('web.recordingRules.removeBody')} onOpenChange={open=>{if(!open&&!state.saving)setRemove(undefined);}} footer={<><Button size="small" variant="text" disabled={state.saving} onClick={()=>setRemove(undefined)}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={state.saving} onClick={()=>void controller.remove(shownRemove,cancelBookings).then(ok=>{if(ok&&controller.isActive())setRemove(undefined);})}>{t('web.recordingRules.remove')}</Button></>}><SettingsRow label={t('web.recordingRules.cancelBookings')}><Switch checked={cancelBookings} disabled={state.saving} aria-label={t('web.recordingRules.cancelBookings')} onCheckedChange={setCancelBookings}/></SettingsRow></Dialog>}
 </section>;
}
