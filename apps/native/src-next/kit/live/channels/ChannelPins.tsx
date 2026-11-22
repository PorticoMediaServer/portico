import React from 'react';
import {Text,View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button,Chip,Select,TextField} from '../../controls';
import {useTheme} from '../../theme';
import {useChannelPins,type ChannelPinsProps} from '../../../pages/shared/live/channels/useChannelPins';
const t=defaultI18n.t;

/** Always include / Never include / weighted titles: chips for the chosen titles, then a library, a title search and its results. */
export function NativeChannelPins(props:ChannelPinsProps){
 const theme=useTheme(),view=useChannelPins(props),{ids,locked,label,weight,setWeight}={...props,...view};
 return <View style={{gap:8}}><Text style={{color:theme.colors.text}}>{label}</Text>{ids.slice(0,view.shown).map(id=><View key={id} style={{gap:8}}><Chip variant="applied" trailingIcon="i-close" disabled={locked} onPress={()=>view.change(ids.filter(v=>v!==id))}>{view.names[id]?.title??t('error.unavailable')}{view.names[id]?.kind==='show'?` · ${t('builder.pinnedShow')}`:''}</Chip>{weight&&<TextField label={t('web.ownerChannels.frequency')} value={String(weight(id))} keyboardType="numeric" editable={!locked} onChangeText={text=>view.weigh(setWeight,id,text)}/>}</View>)}
  {view.shown<ids.length&&<Button size="compact" onPress={()=>view.setShown(v=>v+20)}>{t('action.showMore')}</Button>}
  <Select label={t('builder.preset.library')} value={view.selected} disabled={locked} options={view.libraries.filter(v=>props.libraryIds.includes(v.id)).map(v=>({value:v.id,label:v.name}))} onValueChange={view.setLibrary}/>
  {view.libraryKind==='anime'&&<Select label={t('builder.kinds')} value={view.anime} disabled={locked} options={[{value:'movie',label:t('builder.movies')},{value:'show',label:t('builder.shows')}]} onValueChange={v=>{if(v==='movie'||v==='show')view.setAnime(v);}}/>}
  <TextField label={t('builder.findTitle')} value={view.query} maxLength={128} editable={view.searchable} onChangeText={view.setQuery}/>
  {view.found.map(row=><Button key={row.id} size="compact" variant="glass" disabled={locked||view.full} onPress={()=>view.add(row)}>{row.title}{row.kind==='show'?` · ${t('builder.pinnedShow')}`:''}</Button>)}
  {view.cursor&&<Button size="compact" disabled={locked||view.loading} onPress={view.more}>{t('action.showMore')}</Button>}{view.noneFound&&<Text style={{color:theme.colors.textSecondary}}>{t('builder.noTitles')}</Text>}{view.error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}
 </View>;
}
