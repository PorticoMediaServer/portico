import React,{useEffect,useLayoutEffect,useMemo,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import type {LibraryChannel,LibraryChannelTemplate} from '../../../../../../../packages/contracts/src/next/api';
import {channelMonograms} from '../../../../../../../packages/client-core/src/presentation/kit-model/channels';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {ChannelMark} from '../../../../kit/artwork';
import {Button,Focusable} from '../../../../kit/controls';
import {BottomSheet,MenuRow} from '../../../../kit/overlays';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import type {NativeSettingsLivePorts} from '../../ports';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {createNativeChannels,type NativeChannelsGroup} from './model';
import {NativeChannelEditor} from './ChannelEditor';
import {NativeBlockPresets} from './BlockPresets';
import {NativeChannelLogo} from './ChannelLogo';
const t=defaultI18n.t;
export function NativeLibraryChannelsPage({ports,runtime,initialChannelId}:{ports:Pick<NativeSettingsLivePorts,'context'|'requestId'>;runtime:NativeShellRuntime;initialChannelId?:string}){
 const context=ports.context,[group,setGroup]=useState<NativeChannelsGroup>();
 useLayoutEffect(()=>{const next=createNativeChannels(context,runtime.reportError);setGroup(next);return next.dispose;},[context,runtime.reportError]);
 return group?.context===context?<Channels group={group} runtime={runtime} initialChannelId={initialChannelId}/>:null;
}
export function Channels({group,runtime,initialChannelId}:{group:NativeChannelsGroup;runtime:NativeShellRuntime;initialChannelId?:string}){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),theme=useTheme(),[menu,setMenu]=useState<{row:LibraryChannel;token:object}>(),[templates,setTemplates]=useState<object>(),[remove,setRemove]=useState<{row:LibraryChannel;token:object}>();
 const openings=useMemo(()=>({active:group.current(),menu:undefined as typeof menu,templates:undefined as object|undefined,remove:undefined as typeof remove}),[group]);
 useLayoutEffect(()=>{openings.active=group.current();return()=>{openings.active=false;openings.menu=undefined;openings.templates=undefined;openings.remove=undefined;};},[group,openings]);
 if(openings.menu&&!state.channels.includes(openings.menu.row))openings.menu=undefined;
 if(openings.remove&&!state.channels.includes(openings.remove.row))openings.remove=undefined;
 const closeMenu=()=>{if(openings.active&&openings.menu===menu){openings.menu=undefined;setMenu(undefined);}};
 const closeTemplates=()=>{if(openings.active&&openings.templates===templates){openings.templates=undefined;setTemplates(undefined);}};
 const closeRemove=()=>{if(openings.active&&openings.remove===remove){openings.remove=undefined;setRemove(undefined);}};
 useEffect(()=>{void source.load().then(()=>{if(initialChannelId&&group.current())void source.openId(initialChannelId);});},[group,source,initialChannelId]);
 const blocked=!!(state.loading||state.busy||state.uncertain||state.editor||state.presetEditor||state.pendingCreate||state.pendingPreset),held=menu&&openings.menu===menu&&state.channels.includes(menu.row)&&group.current()?menu:undefined,removal=remove&&openings.remove===remove&&state.channels.includes(remove.row)&&group.current()?remove:undefined;
 const marks=channelMonograms({kind:'complete-authorized',channels:state.channels.map(row=>({sourceId:row.id,id:row.id,name:row.name}))});
 const open=(template?:LibraryChannelTemplate)=>{if(openings.active&&templates&&openings.templates===templates&&!blocked&&group.current()&&(!template||state.choices?.items.includes(template)&&template.available)){source.blank(template);closeTemplates();}};
 const action=(run:(row:LibraryChannel)=>void)=>{if(openings.active&&held&&openings.menu===held&&!blocked&&group.current()&&source.getSnapshot().channels.includes(held.row)){closeMenu();run(held.row);}};
 return <View style={{marginTop:14,gap:12}} testID="server-library-channels"><Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('settings.server.libraryChannels')}</Text><View style={{flexDirection:'row',gap:8}}><Button testID="channels-new" disabled={blocked||!state.choices} onPress={()=>{if(!blocked&&group.current()){const token={};openings.templates=token;setTemplates(token);}}}>{t('builder.newTitle')}</Button><Button size="compact" variant="glass" disabled={blocked} onPress={()=>{if(group.current())void source.load();}}>{t('web.selection.refresh')}</Button></View>
  {state.loading&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}
  {!!state.error&&<><Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(state.pendingCreate?'error.unconfirmedChange':'error.unavailable')}</Text><Button size="compact" onPress={()=>{if(state.pendingCreate)source.resume();else void source.load();}}>{t('action.tryAgain')}</Button></>}
  {!state.loading&&!state.channels.length&&<><Text style={[shellType(17,700),{color:theme.colors.text}]}>{t('builder.empty.title')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('builder.empty.body')}</Text></>}
  {state.channels.map(row=>{
   const word=!row.on?'web.libraryChannels.offState':row.state==='onAir'?'web.libraryChannels.onAir':row.state==='building'?'web.libraryChannels.healthBeingMade':row.state==='needsSchedule'?'web.ownerChannels.needsSchedule':'web.ownerChannels.needsAttention';
   const ink=!row.on?theme.colors.textTertiary:row.state==='onAir'?theme.colors.textSecondary:row.state==='building'?theme.colors.accentStrong:row.state==='needsSchedule'?theme.colors.amber:theme.colors.record;
   return <View key={row.id} style={{padding:12,gap:8,borderRadius:12,backgroundColor:theme.colors.raised}}><Focusable testID={`channel-${row.id}`} accessibilityRole="button" accessibilityLabel={row.name} disabled={blocked} onPress={()=>{if(!blocked&&group.current()&&source.getSnapshot().channels.includes(row)){const next={row,token:{}};openings.menu=next;setMenu(next);}}} style={{gap:8}}>
    <View style={{flexDirection:'row',alignItems:'center',gap:12}}><ChannelMark mark={marks.get(row.id)!.get(row.id)!} source={row.logo?runtime.artwork(row.logo):undefined} size={44} sized/><Text style={[shellType(15,700),{color:theme.colors.text,flex:1}]}>{row.name}</Text></View>
    <View style={{flexDirection:'row',flexWrap:'wrap',alignItems:'center',gap:12}}><Text style={[shellType(13),{color:theme.colors.textSecondary}]}>{t('server.channels.titles',{count:row.titles})}</Text><View style={{flexDirection:'row',alignItems:'center',gap:7}}><View style={{width:7,height:7,borderRadius:3.5,backgroundColor:row.state==='onAir'?theme.colors.healthy:ink}}/><Text style={[shellType(13,700),{color:ink}]}>{t(word)}</Text></View></View>
    {row.scheduledThrough&&<Text style={{color:theme.colors.textTertiary}}>{t('server.channels.through',{when:defaultI18n.dateTime(row.scheduledThrough)})}</Text>}{row.summary&&<Text style={{color:theme.colors.textSecondary}}>{row.summary}</Text>}
   </Focusable></View>;
  })}
  {state.cursor&&<Button size="compact" disabled={blocked} onPress={()=>{void source.load(true);}}>{t('action.showMore')}</Button>}
  <BottomSheet open={!!templates&&openings.templates===templates&&group.current()} onOpenChange={v=>{if(!v)closeTemplates();}} title={t('builder.newTitle')}>
   <View style={{borderRadius:14,backgroundColor:theme.colors.raised,overflow:'hidden'}}>{state.choices?.items.map(template=><MenuRow key={template.key} variant="grouped" icon="i-tv" label={template.name} description={!template.available?t('server.channels.notEnough'):undefined} disabled={blocked||!template.available} onPress={()=>open(template)}/>)}<MenuRow variant="grouped" icon="i-plus" label={t('web.libraryChannels.startFromScratch')} disabled={blocked} onPress={()=>open()}/></View>
  </BottomSheet>
  <BottomSheet open={!!held} onOpenChange={v=>{if(!v)closeMenu();}} title={held?.row.name}>
   {held&&<View style={{borderRadius:14,backgroundColor:theme.colors.raised,overflow:'hidden'}}><MenuRow variant="grouped" icon="i-edit" label={t('web.selection.edit')} disabled={blocked} onPress={()=>action(row=>{void source.open(row);})}/><MenuRow variant="grouped" icon="i-pulse" label={t('web.libraryChannels.checkHealth')} disabled={blocked} onPress={()=>action(row=>{void source.health(row);})}/>
    <NativeChannelLogo group={group} channel={held.row}/>
    <MenuRow variant="grouped" icon={held.row.on?'i-pause':'i-play'} label={t(held.row.on?'web.libraryChannels.turnOff':'web.libraryChannels.turnOn')} disabled={blocked} onPress={()=>action(row=>{void source.action(row,'toggle');})}/><MenuRow variant="grouped" icon="i-refresh" label={t('web.libraryChannels.regenSchedule')} disabled={blocked} onPress={()=>action(row=>{void source.action(row,'regenerate');})}/>
    <MenuRow variant="grouped" icon="i-up" label={t('web.profile.moveUp')} disabled={blocked||state.channels.indexOf(held.row)===0} onPress={()=>action(row=>{void source.action(row,'up');})}/><MenuRow variant="grouped" icon="i-down" label={t('web.profile.moveDown')} disabled={blocked||state.channels.indexOf(held.row)===state.channels.length-1&&!state.cursor} onPress={()=>action(row=>{void source.action(row,'down');})}/>
    <MenuRow variant="grouped" icon="i-trash" label={t('web.libraryChannels.delete')} destructive disabled={blocked} onPress={()=>action(row=>{const next={row,token:{}};openings.remove=next;setRemove(next);})}/>
   </View>}
  </BottomSheet>
  <BottomSheet open={!!removal} onOpenChange={v=>{if(!v&&!state.busy)closeRemove();}} title={removal?.row.name} description={t('web.libraryChannels.deleteBody')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" disabled={state.busy} onPress={closeRemove}>{t('action.cancel')}</Button><Button variant="danger" disabled={state.busy} onPress={()=>{if(openings.active&&removal&&openings.remove===removal&&group.current())void source.action(removal.row,'remove').then(()=>{if(openings.active&&openings.remove===removal&&group.current())closeRemove();});}}>{t('action.delete')}</Button></View>}><View/></BottomSheet>
  <BottomSheet open={!!state.health} onOpenChange={v=>{if(!v)source.closeHealth();}} title={state.health?.name} description={t('web.libraryChannels.healthLede')}>
   {state.health&&<View style={{gap:14}}><Text style={{color:theme.colors.text}}>{state.health.health?.message??state.health.problem??t(state.health.health?.onAir?'web.libraryChannels.onAir':'web.libraryChannels.offAir')}</Text>{state.health.health&&<><Text style={{color:theme.colors.textSecondary}}>{t('web.libraryChannels.healthPrograms')}: {defaultI18n.number(state.health.health.programmes)}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.libraryChannels.healthCandidates')}: {defaultI18n.number(state.health.health.titles)}</Text><Text style={{color:theme.colors.textSecondary}}>{t('web.libraryChannels.healthUnresolved')}: {defaultI18n.number(state.health.health.unplaceable)}</Text><Text style={{color:theme.colors.textSecondary}}>{state.health.health.reaches?defaultI18n.dateTime(state.health.health.reaches):t('web.libraryChannels.healthNotGenerated')}</Text></>}<Button onPress={()=>{if(state.health)void source.health(state.health);}}>{t('server.remote.checkAgain')}</Button></View>}
  </BottomSheet>
  <NativeBlockPresets group={group}/>
  {state.editor&&<NativeChannelEditor key={state.editor.key} group={group} runtime={runtime} editor={state.editor}/>}
 </View>;
}
