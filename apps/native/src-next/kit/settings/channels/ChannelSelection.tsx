import React from 'react';
import {Text,View} from 'react-native';
import type {LibraryChannelSelection} from '../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {defaultI18n,type MessageId} from '../../../../../../packages/i18n/src';
import {Button,Select,Switch,TextField} from '../../controls';
import {useTheme} from '../../theme';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import {NativeChannelConditions} from './ChannelConditions';
import {NativeChannelPins} from './ChannelPins';
const t=defaultI18n.t;
export function NativeChannelSelection({group,value,locked,onChange}:{group:NativeChannelsGroup;value:LibraryChannelSelection;locked:boolean;onChange:(value:LibraryChannelSelection)=>void}){
 const theme=useTheme(),patch=(next:Partial<LibraryChannelSelection>)=>{if(!locked&&group.current())onChange({...value,...next});};
 return <View style={{gap:14}}><Text style={{color:theme.colors.text}}>{t('builder.libraries')}</Text>{!group.libraries.length&&<Text style={{color:theme.colors.textSecondary}}>{t('builder.noVideoLibraries')}</Text>}{group.libraries.map(library=><Switch key={library.id} checked={value.libraries.includes(library.id)} disabled={locked} onCheckedChange={checked=>patch({libraries:checked?[...value.libraries,library.id]:value.libraries.filter(id=>id!==library.id)})}>{library.name}</Switch>)}
  {value.libraries.filter(id=>!group.libraries.some(v=>v.id===id)).map(id=><Button key={id} size="compact" disabled={locked} onPress={()=>patch({libraries:value.libraries.filter(v=>v!==id)})}>{t('error.unavailable')} · {t('builder.remove')}</Button>)}
  {['movie','show'].map(kind=><Switch key={kind} checked={value.kinds.includes(kind)} disabled={locked} onCheckedChange={checked=>patch({kinds:checked?[...value.kinds,kind]:value.kinds.filter(v=>v!==kind)})}>{t(kind==='movie'?'builder.movies':'builder.shows')}</Switch>)}
  <NativeChannelConditions lookup={group.lookup} libraries={value.libraries} rows={value.conditions??[]} match={value.match} locked={locked} onChange={(conditions,match)=>patch({conditions,match})}/>
  <TextField label={t('builder.mentions')} description={t('builder.mentionsHint')} value={value.mentions??''} maxLength={200} editable={!locked} onChangeText={mentions=>patch({mentions})}/>
  <Select label={t('builder.recent')} value={String(value.addedWithinDays??0)} disabled={locked} options={[...new Set([0,7,30,90,365,value.addedWithinDays??0])].map(v=>({value:String(v),label:v?t('builder.recent.days',{count:v}):t('builder.recent.any')}))} onValueChange={v=>patch({addedWithinDays:Number(v)})}/>
  <NativeChannelPins group={group} libraryIds={value.libraries} ids={value.always??[]} locked={locked} label={t('builder.alwaysInclude')} onChange={always=>patch({always})}/><Switch checked={!!value.onlyAlways} disabled={locked} onCheckedChange={onlyAlways=>patch({onlyAlways})}>{t('web.ownerChannels.onlyPins')}</Switch>
  <NativeChannelPins group={group} libraryIds={value.libraries} ids={value.never??[]} locked={locked} label={t('builder.neverInclude')} onChange={never=>patch({never})}/>
  <Select label={t('builder.order')} value={value.order??'shuffle'} disabled={locked} options={[['shuffle','builder.order.shuffle'],['random','builder.order.random'],['inOrder','builder.order.inOrder'],['inOrderThenShuffle','builder.order.thenShuffle'],...(value.order==='leastPlayed'?[['leastPlayed','builder.preset.leastPlayed']]:[])].map(([value,label])=>({value:value!,label:t(label as MessageId)}))} onValueChange={order=>patch({order})}/>
  {['inOrder','inOrderThenShuffle'].includes(value.order??'')&&<Select label={t('builder.sortBy')} value={value.sort??'title'} disabled={locked} options={['title','release','year','recent','firstAdded','rating','episode'].map(value=>({value,label:t(`builder.sort.${value==='firstAdded'?'oldest':value}` as MessageId)}))} onValueChange={sort=>patch({sort})}/>}
  {value.kinds.includes('show')&&<><Select label={t('builder.episodes')} value={value.episodes??'showOrder'} disabled={locked} options={[['mixed','none'],['showOrder','inOrder'],['marathon','marathon'],['rotate','rotate'],['random','randomized']].map(([value,label])=>({value:value!,label:t(`builder.episodes.${label}` as MessageId)}))} onValueChange={episodes=>patch({episodes})}/>{value.episodes==='marathon'&&<TextField label={t('web.ownerChannels.marathon')} value={String(value.marathon??12)} keyboardType="numeric" editable={!locked} onChangeText={text=>{const n=Number(text);if(Number.isInteger(n)&&n>=2&&n<=100)patch({marathon:n});}}/>}</>}
  {(!value.order||['shuffle','inOrder'].includes(value.order))&&<Select label={t('builder.whenDone')} value={value.whenDone??'again'} disabled={locked} options={[{value:'again',label:t('builder.whenDone.loop')},{value:'slate',label:t('builder.whenDone.slate')}]} onValueChange={whenDone=>patch({whenDone})}/>}
  {value.order==='random'&&<NativeChannelPins group={group} libraryIds={value.libraries} ids={(value.weights??[]).map(v=>v.id)} limit={100} locked={locked} label={t('web.ownerChannels.weights')} onChange={ids=>patch({weights:ids.map(id=>value.weights?.find(v=>v.id===id)??{id,weight:1})})} weight={id=>value.weights?.find(v=>v.id===id)?.weight??1} setWeight={(id,weight)=>patch({weights:value.weights!.map(v=>v.id===id?{id,weight}:v)})}/>}
 </View>;
}
