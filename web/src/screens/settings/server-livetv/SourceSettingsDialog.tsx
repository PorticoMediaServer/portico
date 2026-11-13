import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import {dialogAction} from '../../live/owner/dialog-lifetime';
import type {SourceSettingsController, SourceSettingsDraft} from './source-settings-controller';
import type {SourceChoicesController} from './source-choices-controller';
import {SourceChoicePicker} from './SourceChoicePicker';
import styles from './source-settings.module.css';
const t=defaultI18n.t,K='next.settingsSpec.server.live.groups.2.rows',N='next.settingsSpec.server.live.groups.3.rows.0';
export function SourceSettingsDialog({controller,choices,name,guides,close,changed,sourceBusy=false}: {controller:SourceSettingsController;choices:{groups:SourceChoicesController;channels:SourceChoicesController};name:string;guides:readonly {id:string;name:string}[];acquisition?:'upload'|'remote';enabled?:boolean;setEnabled?:(enabled:boolean)=>void;sourceBusy?:boolean;close:()=>void;changed:()=>void}){
 const owner=useRef(controller);owner.current=controller;
 const [choice,setChoice]=useState<string>(),[guideAddress,setGuideAddress]=useState(false),[replace,setReplace]=useState(false);
 const state=useSyncExternalStore(controller.subscribe,controller.getSnapshot,controller.getSnapshot),{draft,document:doc}=state;
 useEffect(()=>{setChoice(undefined);setGuideAddress(false);setReplace(false);void controller.load();return()=>{controller.dispose();choices.groups.dispose();choices.channels.dispose();};},[controller,choices]);
 const act=(callback:()=>void)=>dialogAction(owner,controller,callback),busy=state.loading||state.saving||!!state.logoBusy||!!state.reading||sourceBusy;
 const locked=busy||!!state.uncertain;
 const closeCurrent=act(()=>{controller.dispose();if(owner.current===controller)close();});
 const cfg=draft?.config,guideOnly=doc?.source.kind==='guide',included=choice??(cfg?.channels?.length?'picked':cfg?.groups?.length?'groups':'all');
 // Compare just editable fields; source health/report changes do not make a draft dirty.
 const changedDraft=!!draft&&!!doc&&(JSON.stringify([draft.name,draft.audience,draft.paused,draft.tunerCount,draft.config,draft.playlist,draft.guide])!==JSON.stringify([doc.source.name,doc.source.audience,doc.source.paused,doc.source.tunerCount,doc.source.config,undefined,undefined])||!!draft.username||!!draft.password);
 const credentialPair=!(draft?.username||draft?.password)||!!draft?.username&&!!draft?.password;
 const edit=(patch:Partial<SourceSettingsDraft>)=>{if(owner.current===controller&&controller.isActive())controller.edit(patch);};
 const config=(patch:Partial<SourceSettingsDraft['config']>)=>{if(cfg)edit({config:{...cfg,...patch},...(patch.playlistUrl?{playlist:undefined}:{}),...(patch.guideUrl?{guide:undefined}:{})});};
 const file=(field:'playlist'|'guide',value?:File)=>{if(value&&owner.current===controller&&controller.isActive())void controller.upload(field,()=>{if(value.size>128*1024*1024)throw {status:413,code:'body_too_large'};return value.text();});};
 return <Dialog footerDismisses open width={620} title={name} onOpenChange={open=>{if(!open)closeCurrent();}}
  footer={<><Button size="small" variant="text" onClick={closeCurrent}>{t('action.close')}</Button><Button size="small" variant="primary" loading={state.saving} disabled={!changedDraft||locked||!credentialPair||!draft?.name.trim()} onClick={act(()=>void controller.save().then(saved=>{if(saved&&owner.current===controller&&controller.isActive())changed();}))}>{t('action.save')}</Button></>}>
  <div className={styles.fields}>
   {state.loading&&!draft&&<p role="status">{t('web.librarySettings.loading')}</p>}
   {!!state.error&&<p role="alert" className={styles.error}>{errorMessage(state.error,'server-console',{operation:'action'})}<Button size="small" disabled={busy} onClick={act(()=>void controller.load())}>{t('action.tryAgain')}</Button></p>}
   {state.saved&&<p role="status">{t('server.live.settingsSaved')}</p>}
   {draft&&doc&&cfg&&<>
    <TextField label={t('profile.name')} value={draft.name} maxLength={120} disabled={locked} onChange={event=>edit({name:event.currentTarget.value})}/>
    <SettingsRow label={t(`${K}.0.label`)}><Switch checked={!draft.paused} disabled={locked} aria-label={t(`${K}.0.label`)} onCheckedChange={enabled=>edit({paused:!enabled})}/></SettingsRow>
    <Select label={t(`${K}.1.label`)} value={draft.audience} disabled={locked} options={['everyone','owner'].map((value,index)=>({value,label:t(`${K}.1.control.options.${index}.label` as MessageId)}))} onValueChange={audience=>edit({audience})}/>
    {doc.source.kind==='playlist'&&<>
     <TextField label={t('web.liveTv.playlistUrl')} value={cfg.playlistUrl??''} disabled={locked} onChange={event=>config({playlistUrl:event.currentTarget.value,uploaded:false})}/>
     <TextField label={t('web.liveTv.playlistFile')} type="file" accept=".m3u,.m3u8,text/plain" disabled={locked} onChange={event=>file('playlist',event.currentTarget.files?.[0])}/>
    </>}
    {doc.source.kind==='xtream'&&<>
     <TextField label={t('web.liveTv.serverAddress')} value={cfg.serverUrl??''} disabled={locked} onChange={event=>config({serverUrl:event.currentTarget.value})}/>
     <TextField label={t('web.liveSource.replaceUsername')} type="password" autoComplete="new-password" value={draft.username} disabled={locked} onChange={event=>edit({username:event.currentTarget.value})}/>
     <TextField label={t('web.liveSource.replacePassword')} type="password" autoComplete="new-password" value={draft.password} disabled={locked} onChange={event=>edit({password:event.currentTarget.value})}/>
    </>}
    {doc.source.kind==='hdhomerun'&&<TextField label={t('web.liveTv.tunerAddress')} value={cfg.tunerUrl??''} disabled={locked} onChange={event=>config({tunerUrl:event.currentTarget.value})}/>}
    {!guideOnly&&<>
     {doc.source.kind==='hdhomerun'?<p>{t('server.live.tunerCount',{count:draft.tunerCount})}</p>:<TextField label={t('web.dvrSettings.tuners')} type="number" min={1} max={256} step={1} value={draft.tunerCount} disabled={locked} onChange={event=>{const n=event.currentTarget.valueAsNumber;if(Number.isInteger(n)&&n>=1&&n<=256)edit({tunerCount:n});}}/>}
     <Select label={t(`${K}.2.label`)} value={cfg.guideSource||(guideAddress||cfg.guideUrl?'url':'own')} disabled={locked} options={[{value:'own',label:t('player.quality.automatic')},...guides.filter(source=>source.id!==controller.sourceId).map(source=>({value:source.id,label:source.name})),{value:'url',label:t(`${K}.2.control.options.2.label`)}]} onValueChange={value=>{setGuideAddress(value==='url');config({guideSource:value==='own'||value==='url'?'':value});}}/>
    </>}
    {(guideOnly||guideAddress||cfg.guideUrl)&&<TextField label={t('web.liveTv.guideUrl')} description={t('server.live.guideUrlHelp')} value={cfg.guideUrl??''} disabled={locked} onChange={event=>config({guideUrl:event.currentTarget.value,guideUploaded:false})}/>}
    <TextField label={t('web.liveTv.guideFile')} type="file" accept=".xml,text/xml,application/xml" disabled={locked} onChange={event=>file('guide',event.currentTarget.files?.[0])}/>
    <TextField label={t(`${K}.3.label`)} description={t('web.liveSource.refreshHours')} type="number" min={1} max={168} step={1} value={cfg.refreshHours||6} disabled={locked} onChange={event=>{const n=event.currentTarget.valueAsNumber;if(Number.isInteger(n)&&n>=1&&n<=168)config({refreshHours:n});}}/>
    {!guideOnly&&<>
     <Select label={t(`${N}.label`)} value={included} disabled={locked} options={['all','groups','picked'].map((value,index)=>({value,label:t(`${N}.control.options.${index}.label` as MessageId)}))} onValueChange={value=>{setChoice(value);config({groups:value==='groups'?cfg.groups:[],channels:value==='picked'?cfg.channels:[]});}}/>
     {included==='groups'&&<SourceChoicePicker controller={choices.groups} value={cfg.groups??[]} change={groups=>config({groups:[...groups]})} disabled={locked}/>}
     {included==='picked'&&<SourceChoicePicker controller={choices.channels} value={cfg.channels??[]} change={channels=>config({channels:[...channels]})} disabled={locked}/>}
     <Select label={t('web.liveSource.channelNumbers')} value={cfg.numbering||'source'} disabled={locked} options={[{value:'source',label:t('web.liveSource.asSent')},{value:'sequential',label:t('web.liveSource.inOrder')}]} onValueChange={numbering=>config({numbering,...(numbering==='sequential'?{firstNumber:cfg.firstNumber||1}:{})})}/>
     {cfg.numbering==='sequential'&&<TextField label={t('web.liveSource.startAt')} type="number" min={1} max={99999} step={1} value={cfg.firstNumber||1} disabled={locked} onChange={event=>{const n=event.currentTarget.valueAsNumber;if(Number.isInteger(n)&&n>=1&&n<=99999)config({firstNumber:n});}}/>}
     <SettingsRow label={t('web.liveSource.replaceLogos')}><Switch checked={replace} disabled={locked||!doc.imagesEnabled} aria-label={t('web.liveSource.replaceLogos')} onCheckedChange={setReplace}/></SettingsRow>
     <Button size="small" loading={state.logoBusy} disabled={locked||changedDraft||!doc.imagesEnabled} onClick={act(()=>void controller.importLogos(replace))}>{t('web.liveSource.importLogos')}</Button>
     {!doc.imagesEnabled&&<p>{t('web.liveSource.imagesDisabled')}</p>}
     {state.logoResult&&<p role="status">{t('web.liveSource.logoResult',state.logoResult)}</p>}
    </>}
   </>}
  </div>
 </Dialog>;
}
