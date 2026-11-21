import React,{useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import {validConditions,clockMinute,week} from '../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import type {LibraryChannelBlockPreset,LibraryChannelBlockPresetSave} from '../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {defaultI18n,type MessageId} from '../../../../../../packages/i18n/src';
import {Button,Select,TextField} from '../../controls';
import {BottomSheet} from '../../overlays';
import {useTheme} from '../../theme';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import {NativeChannelConditions} from './ChannelConditions';
import {NativeChannelDays} from './ChannelHours';
const t=defaultI18n.t;
export function NativeBlockPresets({group}:{group:NativeChannelsGroup}){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),theme=useTheme(),[open,setOpen]=useState(false),[remove,setRemove]=useState<LibraryChannelBlockPreset>(),[tried,setTried]=useState(false),editor=state.presetEditor,draft=editor?.draft;
 const locked=!!(state.busy||state.uncertain||state.pendingPreset||state.pendingCreate),edit=(patch:LibraryChannelBlockPresetSave)=>{if(editor)source.presetEdit(editor,{...editor.draft,...patch});};
 const valid=!!draft?.name.trim()&&!!draft.library&&validConditions(draft.recipe.clauses)&&draft.recipe.minutes>=5&&draft.recipe.minutes<=1440&&Number.isInteger(draft.recipe.minutes)&&(!draft.recipe.startsAt||clockMinute(draft.recipe.startsAt)!==undefined);
 return <><Button size="compact" variant="glass" disabled={!!state.editor||state.busy} onPress={()=>{setOpen(true);void source.presets();}}>{t('builder.preset.title')}</Button>
  <BottomSheet open={open} onOpenChange={v=>{if(!state.busy){setOpen(v);if(!v&&editor)source.presetClose(editor);}}} title={t('builder.preset.title')}>
   <View style={{gap:14}}>{state.pendingPreset&&<Button disabled={state.busy} onPress={()=>source.presetResume()}>{t('action.tryAgain')}</Button>}
    {!editor&&<>{state.presets.map(row=><View key={row.id} style={{gap:8}}><Button size="compact" variant="glass" disabled={locked} onPress={()=>{setTried(false);source.presetOpen(row);}}>{row.name}</Button><Button size="compact" variant="text" disabled={locked} onPress={()=>setRemove(row)}>{t('builder.remove')}</Button></View>)}{state.presetCursor&&<Button disabled={locked} onPress={()=>{void source.presets(true);}}>{t('action.showMore')}</Button>}<Button size="compact" disabled={locked} onPress={()=>{setTried(false);source.presetOpen();}}>{t('builder.preset.new')}</Button></>}
    {editor&&draft&&<><TextField label={t('builder.name')} value={draft.name} maxLength={120} editable={!locked} onChangeText={name=>edit({name})}/><TextField label={t('builder.description')} value={draft.description} maxLength={2048} editable={!locked} onChangeText={description=>edit({description})}/>
     <Select label={t('builder.preset.library')} value={draft.library} disabled={locked} options={[{value:'',label:t('builder.error.libraries')},...group.libraries.map(v=>({value:v.id,label:v.name}))]} onValueChange={library=>edit({library})}/>
     <NativeChannelConditions lookup={group.lookup} libraries={[draft.library].filter(Boolean)} rows={draft.recipe.clauses} groups={false} preset locked={locked} onChange={clauses=>edit({recipe:{...draft.recipe,clauses}})}/>
     <Select label={t('builder.preset.order')} value={draft.recipe.order} disabled={locked} options={[['shuffle','builder.order.shuffle'],['inOrder','builder.order.inOrder'],['recent','builder.sort.recent'],['leastPlayed','builder.preset.leastPlayed']].map(([value,label])=>({value:value!,label:t(label as MessageId)}))} onValueChange={order=>edit({recipe:{...draft.recipe,order}})}/>
     <TextField label={t('builder.preset.length')} description={t('builder.preset.minutes')} value={String(draft.recipe.minutes)} editable={!locked} keyboardType="numeric" onChangeText={text=>edit({recipe:{...draft.recipe,minutes:Number(text)}})}/>
     <TextField label={t('builder.preset.starts')} value={draft.recipe.startsAt??''} editable={!locked} maxLength={5} onChangeText={startsAt=>edit({recipe:{...draft.recipe,startsAt:startsAt||undefined}})}/>
     <NativeChannelDays days={draft.recipe.days?.length?draft.recipe.days:[...week]} locked={locked} change={days=>edit({recipe:{...draft.recipe,days}})}/>
     <View style={{flexDirection:'row',gap:8}}><Button disabled={state.busy} variant="glass" onPress={()=>source.presetClose(editor)}>{t('action.cancel')}</Button><Button disabled={state.busy||state.uncertain||editor.conflict} onPress={()=>{setTried(true);if(valid)void source.presetSave(editor);}}>{t(state.pendingPreset?'action.tryAgain':'builder.preset.save')}</Button></View>
     {tried&&!valid&&<Text style={{color:theme.colors.record}}>{t('builder.error.rejected')}</Text>}
     {editor.conflict&&<><Text style={{color:theme.colors.record}}>{t('web.ownerChannels.changed')}</Text><Button disabled={state.busy} onPress={()=>source.presetAccept(editor)}>{t('web.ownerChannels.keepEdits')}</Button></>}
     {editor.document&&<Button disabled={state.busy} onPress={()=>{void source.presetReload(editor);}}>{t('action.refresh')}</Button>}
    </>}
    {!!state.error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(state.pendingPreset?'error.unconfirmedChange':'error.unavailable')}</Text>}
   </View>
  </BottomSheet>
  <BottomSheet open={!!remove} onOpenChange={v=>{if(!v&&!state.busy)setRemove(undefined);}} title={t('action.delete')} description={t('builder.preset.help')} footer={<View style={{flexDirection:'row',gap:8}}><Button disabled={state.busy} variant="glass" onPress={()=>setRemove(undefined)}>{t('action.cancel')}</Button><Button disabled={state.busy} onPress={()=>{if(remove)void source.presetRemove(remove).then(()=>{if(group.current())setRemove(undefined);});}}>{t('action.delete')}</Button></View>}><Text style={{color:theme.colors.text}}>{remove?.name}</Text></BottomSheet>
 </>;
}
