import {useContext,useEffect,useLayoutEffect,useMemo,useRef,useState,useSyncExternalStore} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import type {Library} from '@core/index.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {dependsOn} from '@core/freshness-hub.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {removeOwnerChannelLogo} from '@core/server-admin/owner-library-channels.ts';
import {useFreshness} from '../../../../platform/freshness';
import {mountQuietInput,SettingsOpenContext} from '../../quiet-refresh';
import {settingsRoute} from '../../../../kit/settings/search';
import {Button} from '../../../../kit/controls';
import {Dialog,Menu} from '../../../../kit/overlays';
import type {MenuEntry} from '../../../../kit/overlays/Popover';
import {Icon} from '../../../../kit/Icon';
import {ChannelMark} from '../../../../kit/artwork/ChannelMark';
import {channelMonograms} from '@core/presentation/kit-model/channels.ts';
import type {ArtworkSource} from '../../../../kit/artwork/types';
import {OwnerChannelSource,type Channel} from './owner-channel-source';
import {OwnerChannelEditor} from './OwnerChannelEditor';
import {OwnerPresets} from './OwnerHours';
import styles from './channels.module.css';
const t=defaultI18n.t;
export type OwnerChannelPorts={api:LibraryContentApi;serverId:string;isCurrent:()=>boolean;reportError:(error:unknown)=>void;libraries:readonly Library[];subscribe?:(listener:()=>void)=>()=>void;logo:(channel:Channel)=>ArtworkSource|undefined;sampleArt?:(id:string)=>ArtworkSource|undefined;uploadLogo:(id:string,file:File,signal:AbortSignal)=>Promise<Channel>};
export function LibraryChannelsPage(input:OwnerChannelPorts|{ports:OwnerChannelPorts}){
 const ports='ports' in input?input.ports:input,[source,setSource]=useState<OwnerChannelSource>(),[,redraw]=useState(0),location=useLocation();
 useEffect(()=>ports.subscribe?.(()=>redraw(v=>v+1)),[ports]);
 useLayoutEffect(()=>{const next=new OwnerChannelSource(ports.api,ports.isCurrent,ports.reportError);setSource(next);return()=>next.dispose();},[ports.api,ports.serverId,ports.isCurrent,ports.reportError]);
 const channelId=settingsRoute(Object.fromEntries(new URLSearchParams(location.searchStr)),()=>true).channelId;
 return source&&source.isActive()?<ChannelBody key={ports.serverId} ports={ports} source={source} channelId={channelId}/>:null;
}
function stateLabel(row:Channel){return t(!row.on?'web.libraryChannels.offState':row.state==='onAir'?'web.libraryChannels.onAir':row.state==='building'?'web.libraryChannels.healthBeingMade':row.state==='needsSchedule'?'web.ownerChannels.needsSchedule':'web.ownerChannels.needsAttention');}
function ChannelBody({ports,source,channelId}:{ports:OwnerChannelPorts;source:OwnerChannelSource;channelId?:string}){
 const state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),{hub}=useFreshness(),visibility=useContext(SettingsOpenContext),opened=useRef<string|undefined>(undefined),list=useRef<HTMLUListElement>(null),drag=useRef<{id:string;rows:readonly Channel[];to:number;pointer:number}|undefined>(undefined);
 const [newMenu,setNewMenu]=useState(false),[rowMenu,setRowMenu]=useState<string>(),[modal,setModal]=useState<{row:Channel;kind:'remove'|'logo'|'regenerate'}>(),[presets,setPresets]=useState(false),[drop,setDrop]=useState<string>();
 useEffect(()=>{void source.load();},[source]);
 useEffect(()=>{const interest=mountQuietInput(hub,'settings/channels',[dependsOn.list('libraryChannels'),dependsOn.settings('server'),dependsOn.libraries],{id:'owner-directory',refresh:source.refreshQuietly,cancel:source.invalidateQuiet,subscribe:source.subscribe,blocked:()=>source.quietBlocked()||!!modal||presets},visibility);return()=>interest.dispose();},[hub,source,visibility,modal,presets]);
 useEffect(()=>{if(channelId&&opened.current!==channelId&&state.choices&&!state.busy&&!state.loading){opened.current=channelId;void source.openId(channelId);}},[source,channelId,state.choices,state.busy,state.loading]);
 const lookup=useMemo(()=>({api:ports.api,current:source.isActive,report:ports.reportError,moodLabels:state.choices?.moodAvailable?state.choices.moodLabels:undefined}),[source,ports.api,ports.reportError,state.choices]);
 if(!source.isActive())return null;
 const locked=state.busy||state.loading||!!state.editor||!!state.presetEditor||state.uncertain||state.pendingCreate||state.pendingPreset,busy=!!locked,health=state.health;
 const video=ports.libraries.filter(row=>['movie','tv','anime'].includes(row.kind));
 // Each Library Channel is its own guide source: this row is its complete collision domain.
 const marks=channelMonograms({kind:'complete-authorized',channels:state.channels.map(row=>({sourceId:row.id,id:row.id,name:row.name}))});
 const close=()=>{if(!state.busy)setModal(undefined);},run=()=>{if(!modal||busy||!source.getSnapshot().channels.includes(modal.row))return;void source.action(modal.row,modal.kind==='remove'?'remove':'regenerate').then(()=>{if(source.isActive())setModal(undefined);});};
 const move=(row:Channel,to:number)=>{if(!busy&&source.isActive())void source.moveTo(row,to);};
 const templateItems:MenuEntry[]=[{id:'templates',label:t('server.channels.templates'),section:true},...(state.choices?.items??[]).map(row=>({id:row.key,label:row.name,description:row.available?undefined:t('server.channels.notEnough'),disabled:!row.available,onSelect:()=>source.blank(row)})),{id:'blank',label:t('web.libraryChannels.startFromScratch'),onSelect:()=>source.blank()}];
 return <div className={styles.page}><div className={styles.heading}><h3>{t('channels.custom.name')}</h3><Menu open={newMenu} onOpenChange={setNewMenu} title={t('builder.newTitle')} items={templateItems} trigger={<Button size="small" variant="primary" disabled={busy||!state.choices}>{t('builder.newTitle')}</Button>}/></div>
  {!!state.error&&<p role="alert" className={styles.warning}>{errorMessage(state.error)} <Button size="small" disabled={state.busy||state.loading} onClick={()=>void source.load()}>{t('action.refresh')}</Button></p>}
  {state.loading&&<p role="status">{t('status.loading')}</p>}{state.pendingCreate&&!state.editor&&<Button size="small" disabled={state.busy} onClick={()=>source.resume()}>{t('action.tryAgain')}</Button>}
  {!state.loading&&!state.channels.length&&<div className={styles.empty}><h3>{t('builder.empty.title')}</h3><p>{t('builder.empty.body')}</p><Button size="small" disabled={busy||!state.choices} onClick={()=>source.blank()}>{t('builder.empty.create')}</Button></div>}
  <ul ref={list} className={styles.list}>{state.channels.map((row,index)=><li className={styles.row} key={row.id} data-channel-row={row.id} data-drop-target={drop===row.id||undefined}>
   <button type="button" className={styles.grip} disabled={busy} aria-label={t('next.settings.reorder',{name:row.name})} onKeyDown={event=>{if(busy||!['ArrowUp','ArrowDown','Home','End'].includes(event.key))return;event.preventDefault();move(row,event.key==='Home'?0:event.key==='End'?state.channels.length-1:index+(event.key==='ArrowUp'?-1:1));}}
    onPointerDown={event=>{if(busy||event.button!==0)return;event.preventDefault();event.currentTarget.focus();event.currentTarget.setPointerCapture(event.pointerId);drag.current={id:row.id,rows:state.channels,to:index,pointer:event.pointerId};}}
    onPointerMove={event=>{const held=drag.current;if(!held||held.pointer!==event.pointerId)return;if(busy||held.rows!==state.channels||!source.isActive()){drag.current=undefined;setDrop(undefined);return;}const rows=[...(list.current?.querySelectorAll<HTMLElement>('[data-channel-row]')??[])],to=rows.findIndex(v=>{const box=v.getBoundingClientRect();return event.clientY>=box.top&&event.clientY<box.bottom;});if(to>=0){held.to=to;setDrop(state.channels[to]!.id);}}}
    onPointerUp={event=>{const held=drag.current;drag.current=undefined;setDrop(undefined);if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId);if(held&&held.pointer===event.pointerId&&held.rows===state.channels&&held.id===row.id)move(row,held.to);}} onPointerCancel={()=>{drag.current=undefined;setDrop(undefined);}} onLostPointerCapture={()=>{drag.current=undefined;setDrop(undefined);}}><Icon name="i-grip" size={18}/></button>
   <ChannelMark mark={marks.get(row.id)!.get(row.id)!} source={ports.logo(row)} size={44}/><div className={styles.channelText}><b>{row.name}</b><p>{[row.summary,t('server.channels.titles',{count:row.titles}),row.scheduledThrough?t('server.channels.through',{when:defaultI18n.dateTime(row.scheduledThrough)}):undefined].filter(Boolean).join(' · ')}</p></div><span className={styles.badge} data-state={row.state==='onAir'?'ready':row.on?'degraded':'off'}>{stateLabel(row)}</span>
   <Menu open={rowMenu===row.id} onOpenChange={open=>setRowMenu(open?row.id:undefined)} title={row.name} trigger={<Button size="small" variant="icon" icon="i-more" iconOnly disabled={busy} aria-label={t('server.row.actions',{name:row.name})}/>} items={[
    {id:'edit',label:t('web.selection.edit'),onSelect:()=>void source.open(row)},{id:'health',label:t('web.libraryChannels.checkHealth'),onSelect:()=>void source.health(row)},
    {id:'logo',label:t('web.libraryChannels.uploadLogo'),onSelect:()=>setModal({row,kind:'logo'})},{id:'toggle',label:t(row.on?'web.libraryChannels.turnOff':'web.libraryChannels.turnOn'),onSelect:()=>void source.action(row,'toggle')},
    {id:'regenerate',label:t('web.libraryChannels.regenSchedule'),onSelect:()=>setModal({row,kind:'regenerate'})},{id:'up',label:t('web.profile.moveUp'),disabled:index===0,onSelect:()=>move(row,index-1)},
    {id:'down',label:t('web.profile.moveDown'),disabled:index===state.channels.length-1&&!state.cursor,onSelect:()=>move(row,index+1)},{id:'remove',label:t('web.libraryChannels.delete'),destructive:true,onSelect:()=>setModal({row,kind:'remove'})}]}/>
  </li>)}</ul>
  {state.cursor&&<Button size="small" disabled={busy} onClick={()=>void source.load(true)}>{t('action.showMore')}</Button>}<Button size="small" disabled={busy} onClick={()=>{setPresets(true);void source.presets();}}>{t('builder.preset.title')}</Button>
  {state.editor&&<OwnerChannelEditor key={state.editor.key} source={source} editor={state.editor} lookup={lookup} libraries={video} manage={()=>{setPresets(true);void source.presets();}} sampleArt={ports.sampleArt}/>}
  {presets&&<OwnerPresets source={source} lookup={lookup} libraries={video} close={()=>{if(!state.busy)setPresets(false);}}/>}
  {health&&<Dialog footerDismisses open title={health.name} description={t('web.libraryChannels.healthLede')} onOpenChange={open=>{if(!open)source.closeHealth();}} footer={<Button size="small" onClick={()=>source.closeHealth()}>{t('action.done')}</Button>}><div className={styles.health}><p className={health.health?.onAir?styles.healthy:styles.warning}>{health.health?.message||stateLabel(health)}</p><dl className={styles.facts}>{[
   ['web.libraryChannels.healthSchedule',health.health?.reaches?defaultI18n.dateTime(health.health.reaches):t('web.libraryChannels.healthNotGenerated')],['web.libraryChannels.healthPrograms',health.health?.programmes??0],['web.libraryChannels.healthCandidates',health.health?.titles??0],['web.libraryChannels.healthUnresolved',health.health?.unplaceable??0],['web.libraryChannels.healthNewSchedule',t(health.health?.building?'web.libraryChannels.healthBeingMade':'web.libraryChannels.healthUpToDate')]
  ].map(([label,value])=><div key={label}><dt>{t(label as Parameters<typeof t>[0])}</dt><dd>{value}</dd></div>)}</dl></div></Dialog>}
  {modal&&<Dialog footerDismisses open title={t(modal.kind==='logo'?'web.libraryChannels.uploadLogo':modal.kind==='remove'?'web.libraryChannels.deleteTitle':'web.libraryChannels.regenSchedule',{name:modal.row.name})} description={modal.kind==='remove'?t('web.libraryChannels.deleteBody'):modal.kind==='regenerate'?t('web.ownerChannels.regenerateBody'):undefined} onOpenChange={open=>{if(!open)close();}} footer={<><Button size="small" disabled={state.busy} onClick={close}>{t('action.cancel')}</Button>{modal.kind!=='logo'&&<Button size="small" variant="primary" disabled={busy} onClick={run}>{t(modal.kind==='remove'?'web.libraryChannels.delete':'web.libraryChannels.regenSchedule')}</Button>}</>}>
   {modal.kind==='logo'&&<><input type="file" accept="image/jpeg,image/png,image/webp,image/gif" aria-label={t('web.libraryChannels.uploadLogo')} disabled={busy} onChange={event=>{const file=event.currentTarget.files?.[0];event.currentTarget.value='';if(file&&!busy)void source.logo(modal.row,signal=>ports.uploadLogo(modal.row.id,file,signal)).then(()=>{if(source.isActive())setModal(undefined);});}}/>{modal.row.logo&&<Button size="small" disabled={busy} onClick={()=>void source.logo(modal.row,async signal=>{await removeOwnerChannelLogo(ports.api,modal.row.id,signal);return modal.row;}).then(()=>{if(source.isActive())setModal(undefined);})}>{t('builder.remove')}</Button>}</>}
  </Dialog>}
 </div>;
}
