import {MetadataFacts} from '../../../../kit/MetadataFacts';
import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {LibraryChannel, LibraryChannelTemplate} from '@core/library-channels.ts';
import {channelMonograms} from '@core/presentation/kit-model/channels.ts';
import {Button} from '../../../../kit/controls';
import {Icon} from '../../../../kit/Icon';
import {ChannelMark} from '../../../../kit/artwork/ChannelMark';
import type {ArtworkSource} from '../../../../kit/artwork/types';
import {Menu, type MenuEntry} from '../../../../kit/overlays/Popover';
import {ChannelHealth} from './ChannelHealth';
import type {SettingsLibraryChannelsSource} from './channels-source';
import styles from './channels.module.css';
const t=defaultI18n.t;
export type ChannelAction='edit'|'logo'|'toggle'|'regenerate'|'up'|'down'|'delete';
type Props=Readonly<{source:SettingsLibraryChannelsSource;isCurrent:()=>boolean;busy:boolean;logo:(channel:LibraryChannel)=>ArtworkSource|undefined;create:(template?:LibraryChannelTemplate)=>void;action:(channel:LibraryChannel,action:ChannelAction)=>void;reorder:(channel:LibraryChannel,target:number)=>void}>;

/** A complete library channel is its own guide source (the server uses its
 * config ID for both). Marks use that exact source identity and the shared kit. */
export function LibraryChannels({source,isCurrent,busy,logo,create,action,reorder}:Props){
  const state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),[menu,setMenu]=useState(false);
  const list=useRef<HTMLUListElement>(null),drag=useRef<{pointer:number;channel:LibraryChannel;to:number;rows:readonly LibraryChannel[];source:SettingsLibraryChannelsSource}|undefined>(undefined);
  const [dropTarget,setDropTarget]=useState<string>();
  const cancelDrag=()=>{drag.current=undefined;setDropTarget(undefined);};
  useEffect(()=>()=>{drag.current=undefined;},[source]);
  useEffect(()=>{void source.load();void source.templates();},[source]);
  const channels=state.channels ?? [],live=isCurrent(),blocked=busy||state.phase==='loading'||!live;
  const marks=channelMonograms({kind:'complete-authorized',channels:channels.map(channel=>({sourceId:channel.config.id,id:channel.config.id,name:channel.config.name}))});
  const start=(template?:LibraryChannelTemplate)=>{if(isCurrent()&&!busy&&(!template||template.applicable&&source.getSnapshot().templates?.includes(template)))create(template);};
  const run=(channel:LibraryChannel,kind:ChannelAction|'health')=>{if(!isCurrent()||busy||!source.getSnapshot().channels?.includes(channel))return;if(kind==='health')void source.health(channel);else action(channel,kind);};
  const templates:MenuEntry[]=[{id:'templates',label:t('server.channels.templates'),section:true},...(state.templates??[]).map(template=>({id:template.id,label:template.name,description:template.applicable?undefined:t('server.channels.notEnough'),disabled:!template.applicable,onSelect:()=>start(template)}))];
  if(state.templatesLoading)templates.push({id:'loading',label:t('next.settings.loading'),disabled:true,onSelect:()=>{}});
  if(state.templatesError)templates.push({id:'retry',label:t('action.tryAgain'),onSelect:()=>{if(isCurrent())void source.templates();}});
  templates.push({id:'custom',label:t('server.channels.custom'),section:true},{id:'blank',label:t('web.libraryChannels.startFromScratch'),icon:'i-plus',onSelect:()=>start()});
  return <div className={styles.page}>
    <div className={styles.heading}><h3>{t('channels.custom.name')}</h3><Menu open={menu} onOpenChange={setMenu} title={t('builder.newTitle')} width={280} trigger={<Button variant="primary" size="compact" icon="i-plus" disabled={blocked}>{t('builder.newTitle')}</Button>} items={templates}/></div>
    {state.templatesError&&<p role="alert" className={styles.warning}>{t('builder.templatesFailed')}</p>}
    {state.phase==='error'&&<p role="alert" className={styles.warning}>{t('error.unavailable')} <Button size="xs" onClick={()=>{if(isCurrent())void source.load();}}>{t('action.tryAgain')}</Button></p>}
    {!state.channels&&state.phase==='loading'&&<p role="status">{t('next.settings.loading')}</p>}
    {state.channels&&!channels.length&&<div className={styles.empty}><h3>{t('builder.empty.title')}</h3><p>{t('builder.empty.body')}</p><Button variant="primary" disabled={blocked} onClick={()=>start()}>{t('builder.empty.create')}</Button><div className={styles.templates}>{(state.templates??[]).map(template=><Button size="xs" key={template.id} disabled={blocked||!template.applicable} title={template.applicable?undefined:template.reason} onClick={()=>start(template)}>{template.name}</Button>)}</div></div>}
    {!!channels.length&&<ul ref={list} className={styles.list}>{channels.map((channel,index)=>{
      const key=`server.channels.state.${channel.state}`,word=!channel.config.enabled?t('web.libraryChannels.offState'):channel.healthCode||!defaultI18n.has(key)?t('server.channels.state.degraded'):t(key as MessageId);
      const mark=marks.get(channel.config.id)!.get(channel.config.id)!;
      return <li key={channel.config.id} className={styles.row} data-channel-row={channel.config.id} data-drop-target={dropTarget===channel.config.id||undefined}>
        <button type="button" className={styles.grip} disabled={blocked} aria-label={t('next.settings.reorder',{name:channel.config.name})}
          onKeyDown={event=>{if(blocked||!['ArrowUp','ArrowDown','Home','End'].includes(event.key))return;event.preventDefault();const target=event.key==='Home'?0:event.key==='End'?channels.length-1:index+(event.key==='ArrowUp'?-1:1);if(target>=0&&target<channels.length)reorder(channel,target);}}
          onPointerDown={event=>{if(event.button!==0||blocked||!isCurrent())return;event.preventDefault();event.currentTarget.focus();event.currentTarget.setPointerCapture(event.pointerId);drag.current={pointer:event.pointerId,channel,to:index,rows:channels,source};}}
          onPointerMove={event=>{const held=drag.current;if(!held||held.pointer!==event.pointerId)return;if(blocked||!isCurrent()||held.rows!==channels||held.source!==source){cancelDrag();return;}const rows=[...(list.current?.querySelectorAll<HTMLElement>('[data-channel-row]')??[])];const target=rows.findIndex(row=>{const box=row.getBoundingClientRect();return event.clientY>=box.top&&event.clientY<box.bottom;});if(target>=0){held.to=target;setDropTarget(channels[target]!.config.id);}}}
          onPointerUp={event=>{const held=drag.current;cancelDrag();if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId);if(!held||held.pointer!==event.pointerId||blocked||!isCurrent()||held.rows!==channels||held.source!==source)return;reorder(held.channel,held.to);}}
          onPointerCancel={cancelDrag} onLostPointerCapture={cancelDrag}><Icon name="i-grip" size={18}/></button>
        <ChannelMark mark={mark} source={logo(channel)} size={44}/><div className={styles.channelText}><b>{channel.config.name}</b><p><MetadataFacts parts={[channel.config.description||t('web.libraryChannels.ruleCount',{count:channel.config.rules.length}),t('server.channels.titles',{count:channel.candidateCount}),channel.generatedThrough?t('server.channels.through',{when:defaultI18n.dateTime(channel.generatedThrough)}):'']} /></p></div><span className={styles.badge} data-state={!channel.config.enabled?'disabled':channel.healthCode?'degraded':channel.state}>{word}</span><ChannelMenu name={channel.config.name} disabled={blocked} items={[
        {id:'edit',label:t('web.selection.edit'),icon:'i-edit',onSelect:()=>run(channel,'edit')},
        {id:'health',label:t('web.libraryChannels.checkHealth'),icon:'i-pulse',onSelect:()=>run(channel,'health')},
        {id:'logo',label:t('web.libraryChannels.uploadLogo'),icon:'i-upload',onSelect:()=>run(channel,'logo')},
        {id:'toggle',label:t(channel.config.enabled?'web.libraryChannels.turnOff':'web.libraryChannels.turnOn'),icon:channel.config.enabled?'i-pause':'i-play',onSelect:()=>run(channel,'toggle')},
        {id:'regenerate',label:t('web.libraryChannels.regenSchedule'),icon:'i-refresh',disabled:!channel.replacementBoundary,onSelect:()=>run(channel,'regenerate')},
        {id:'up',label:t('web.profile.moveUp'),icon:'i-up',disabled:index===0,onSelect:()=>run(channel,'up')},
        {id:'down',label:t('web.profile.moveDown'),icon:'i-down',disabled:index===channels.length-1,onSelect:()=>run(channel,'down')},
        {id:'separator',separator:true},{id:'delete',label:t('web.libraryChannels.delete'),icon:'i-trash',destructive:true,onSelect:()=>run(channel,'delete')},
      ]}/></li>;
    })}</ul>}
    <ChannelHealth source={source}/>
  </div>;
}
function ChannelMenu({name,disabled,items}:Readonly<{name:string;disabled:boolean;items:readonly MenuEntry[]}>){const[open,setOpen]=useState(false);return <Menu open={open} onOpenChange={setOpen} title={t('server.row.actions',{name})} width={232} items={items} trigger={<Button size="xs" variant="text" disabled={disabled} icon="i-more" aria-label={t('server.row.actions',{name})}/>}/>;}
