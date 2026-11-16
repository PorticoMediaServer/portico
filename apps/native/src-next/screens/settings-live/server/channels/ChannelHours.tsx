import React,{useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import {clonePortableDto} from '../../../../../../../packages/client-core/src';
import type {LibraryChannelBlock,LibraryChannelBuilder} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {week,clockMinute,copyBlockPreset,blockOverlap,validSelection} from '../../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import {newChannelOperation} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {defaultI18n,type MessageId} from '../../../../../../../packages/i18n/src';
import {Button,Select,Switch,TextField} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {useTheme} from '../../../../kit/theme';
import type {NativeChannelsGroup} from './model';
import {NativeChannelSelection} from './ChannelSelection';
const t=defaultI18n.t;
export function NativeChannelDays({days,locked,change}:{days:string[];locked:boolean;change:(days:string[])=>void}){
 return <>{week.map((day,index)=><Switch key={day} checked={days.includes(day)} disabled={locked} onCheckedChange={checked=>change(checked?[...days,day]:days.filter(v=>v!==day))}>{new Intl.DateTimeFormat(defaultI18n.locale,{weekday:'short',timeZone:'UTC'}).format(Date.UTC(2023,0,1+index))}</Switch>)}</>;
}
export function NativeChannelHours({group,value,locked,onChange}:{group:NativeChannelsGroup;value:LibraryChannelBuilder;locked:boolean;onChange:(value:LibraryChannelBuilder)=>void}){
 const theme=useTheme(),state=useSyncExternalStore(group.source.subscribe,group.source.getSnapshot),[opening,setOpening]=useState<{base:LibraryChannelBuilder;block:LibraryChannelBlock;original?:LibraryChannelBlock}>(),[tried,setTried]=useState(false);
 const held=opening?.base===value?opening:undefined,blocks=value.blocks??[],draft=held?.block;
 const change=(blocks:LibraryChannelBlock[])=>{if(!locked&&group.current())onChange({...value,libraries:[...new Set([...value.libraries,...blocks.flatMap(v=>v.selection?.libraries??[])])],blocks});};
 const edit=(patch:Partial<LibraryChannelBlock>)=>{if(held&&!locked&&group.current())setOpening({...held,block:{...held.block,...patch}});};
 const open=(block?:LibraryChannelBlock)=>{if(locked||!group.current())return;setTried(false);setOpening({base:value,original:block,block:clonePortableDto(block??{id:newChannelOperation(),name:'',days:[...week],from:'18:00',to:'20:00'})});void group.source.presets();};
 const close=()=>{if(!locked)setOpening(undefined);};
 const save=()=>{if(!held||locked||!group.current())return;setTried(true);const next=held.original?blocks.map(v=>v===held.original?held.block:v):[...blocks,held.block];if(!draft?.days.length||clockMinute(draft.from)===undefined||clockMinute(draft.to)===undefined||draft.selection&&!validSelection(draft.selection)||blockOverlap(next))return;change(next);setOpening(undefined);};
 return <View style={{gap:10}}><Text style={{color:theme.colors.text}}>{t('builder.schedule.hours')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('builder.schedule.help')}</Text>
  {blocks.map(block=><View key={block.id} style={{gap:8}}><Button variant="glass" size="compact" disabled={locked} onPress={()=>open(block)}>{block.name||`${block.from}–${block.to}`}</Button><Button variant="text" size="compact" disabled={locked} onPress={()=>change(blocks.filter(v=>v!==block))}>{t('builder.remove')}</Button></View>)}
  <Button size="compact" variant="glass" disabled={locked||blocks.length>=500} onPress={()=>open()}>{t('builder.schedule.add')}</Button>
  <BottomSheet open={!!held} onOpenChange={v=>{if(!v)close();}} title={t('builder.schedule.add')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button><Button disabled={locked} onPress={save}>{t('action.save')}</Button></View>}>
   {draft&&<View style={{gap:14}}><TextField label={t('builder.name')} value={draft.name??''} editable={!locked} onChangeText={name=>edit({name})}/>
    <Select label={t('web.ownerChannels.copyPreset')} value="" disabled={locked} options={[{value:'',label:t('builder.preset.title')},...state.presets.map(v=>({value:v.id,label:v.name}))]} onValueChange={id=>{const preset=state.presets.find(v=>v.id===id);if(preset&&held&&!locked){const kind=group.libraries.find(v=>v.id===preset.library)?.kind??'';setOpening({...held,block:copyBlockPreset(preset,kind,draft.id)});}}}/>
    <TextField label={t('web.ownerChannels.from')} value={draft.from} editable={!locked} maxLength={5} onChangeText={from=>edit({from})}/><TextField label={t('web.ownerChannels.to')} value={draft.to} editable={!locked} maxLength={5} onChangeText={to=>edit({to})}/>
    <NativeChannelDays days={draft.days} locked={locked} change={days=>edit({days})}/>
    <Switch checked={!!draft.selection} disabled={locked} onCheckedChange={own=>edit({selection:own?{libraries:[...value.libraries],kinds:[...value.kinds]}:undefined})}>{t('web.ownerChannels.ownSelection')}</Switch>
    {draft.selection&&<NativeChannelSelection group={group} value={draft.selection} locked={locked} onChange={selection=>edit({selection})}/>}
    <Select label={t('builder.block.overrun')} value={draft.overrun??'finish'} disabled={locked} options={['finish','cut','fit'].map(value=>({value,label:t(`builder.block.${value}` as MessageId)}))} onValueChange={overrun=>edit({overrun})}/>
    <Select label={t('builder.block.anchor')} value={draft.resume??'pickUp'} disabled={locked} options={[{value:'pickUp',label:t('builder.block.resume')},{value:'restart',label:t('builder.block.restart')}]} onValueChange={resume=>edit({resume})}/>
    <Select label={t('builder.block.fallback')} value={draft.ifEmpty??'channel'} disabled={locked} options={[{value:'channel',label:t('builder.block.default')},{value:'slate',label:t('builder.whenDone.slate')}]} onValueChange={ifEmpty=>edit({ifEmpty})}/>
    {tried&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('builder.error.rejected')}</Text>}
   </View>}
  </BottomSheet>
 </View>;
}
