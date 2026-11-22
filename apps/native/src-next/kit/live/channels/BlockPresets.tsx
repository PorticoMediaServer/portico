import React from 'react';
import {Text,View} from 'react-native';
import {week} from '../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import {defaultI18n,type MessageId} from '../../../../../../packages/i18n/src';
import {Button,Select,TextField} from '../../controls';
import {BottomSheet} from '../../overlays';
import {useTheme} from '../../theme';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import {NativeChannelConditions} from './ChannelConditions';
import {NativeChannelDays} from './ChannelHours';
import {useBlockPresets} from '../../../pages/shared/live/channels/useBlockPresets';
const t=defaultI18n.t;
/** Block presets: the button, the sheet with the list or one preset's form, and the delete confirmation. */
export function NativeBlockPresets({group}:{group:NativeChannelsGroup}){
 const theme=useTheme(),view=useBlockPresets(group),{source,state,open,remove,setRemove,tried,editor,draft,locked,edit,valid}=view;
 return <><Button size="compact" variant="glass" disabled={!!state.editor||state.busy} onPress={view.show}>{t('builder.preset.title')}</Button>
  <BottomSheet open={open} onOpenChange={view.openChange} title={t('builder.preset.title')}>
   <View style={{paddingHorizontal:theme.isTV?0:20,gap:14}}>{state.pendingPreset&&<Button disabled={state.busy} onPress={()=>source.presetResume()}>{t('action.tryAgain')}</Button>}
    {!editor&&<>{state.presets.map(row=><View key={row.id} style={{gap:8}}><Button size="compact" variant="glass" disabled={locked} onPress={()=>view.openPreset(row)}>{row.name}</Button><Button size="compact" variant="text" disabled={locked} onPress={()=>setRemove(row)}>{t('builder.remove')}</Button></View>)}{state.presetCursor&&<Button disabled={locked} onPress={()=>{void source.presets(true);}}>{t('action.showMore')}</Button>}<Button size="compact" disabled={locked} onPress={()=>view.openPreset()}>{t('builder.preset.new')}</Button></>}
    {editor&&draft&&<><TextField label={t('builder.name')} value={draft.name} maxLength={120} editable={!locked} onChangeText={name=>edit({name})}/><TextField label={t('builder.description')} value={draft.description} maxLength={2048} editable={!locked} onChangeText={description=>edit({description})}/>
     <Select label={t('builder.preset.library')} value={draft.library} disabled={locked} options={[{value:'',label:t('builder.error.libraries')},...group.libraries.map(v=>({value:v.id,label:v.name}))]} onValueChange={library=>edit({library})}/>
     <NativeChannelConditions lookup={group.lookup} libraries={[draft.library].filter(Boolean)} rows={draft.recipe.clauses} groups={false} preset locked={locked} onChange={clauses=>edit({recipe:{...draft.recipe,clauses}})}/>
     <Select label={t('builder.preset.order')} value={draft.recipe.order} disabled={locked} options={[['shuffle','builder.order.shuffle'],['inOrder','builder.order.inOrder'],['recent','builder.sort.recent'],['leastPlayed','builder.preset.leastPlayed']].map(([value,label])=>({value:value!,label:t(label as MessageId)}))} onValueChange={order=>edit({recipe:{...draft.recipe,order}})}/>
     <TextField label={t('builder.preset.length')} description={t('builder.preset.minutes')} value={String(draft.recipe.minutes)} editable={!locked} keyboardType="numeric" onChangeText={text=>edit({recipe:{...draft.recipe,minutes:Number(text)}})}/>
     <TextField label={t('builder.preset.starts')} value={draft.recipe.startsAt??''} editable={!locked} maxLength={5} onChangeText={startsAt=>edit({recipe:{...draft.recipe,startsAt:startsAt||undefined}})}/>
     <NativeChannelDays days={draft.recipe.days?.length?draft.recipe.days:[...week]} locked={locked} change={days=>edit({recipe:{...draft.recipe,days}})}/>
     <View style={{flexDirection:'row',gap:8}}><Button disabled={state.busy} variant="glass" onPress={()=>source.presetClose(editor)}>{t('action.cancel')}</Button><Button disabled={state.busy||state.uncertain||editor.conflict} onPress={view.save}>{t(state.pendingPreset?'action.tryAgain':'builder.preset.save')}</Button></View>
     {tried&&!valid&&<Text style={{color:theme.colors.record}}>{t('builder.error.rejected')}</Text>}
     {editor.conflict&&<><Text style={{color:theme.colors.record}}>{t('web.ownerChannels.changed')}</Text><Button disabled={state.busy} onPress={()=>source.presetAccept(editor)}>{t('web.ownerChannels.keepEdits')}</Button></>}
     {editor.document&&<Button disabled={state.busy} onPress={()=>{void source.presetReload(editor);}}>{t('action.refresh')}</Button>}
    </>}
    {!!state.error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(state.pendingPreset?'error.unconfirmedChange':'error.unavailable')}</Text>}
   </View>
  </BottomSheet>
  <BottomSheet open={!!remove} onOpenChange={v=>{if(!v&&!state.busy)setRemove(undefined);}} title={t('action.delete')} description={t('builder.preset.help')} footer={<View style={{flexDirection:'row',gap:8}}><Button disabled={state.busy} variant="glass" onPress={()=>setRemove(undefined)}>{t('action.cancel')}</Button><Button disabled={state.busy} onPress={view.confirmRemove}>{t('action.delete')}</Button></View>}><View style={{paddingHorizontal:theme.isTV?0:20}}><Text style={{color:theme.colors.text}}>{remove?.name}</Text></View></BottomSheet>
 </>;
}
