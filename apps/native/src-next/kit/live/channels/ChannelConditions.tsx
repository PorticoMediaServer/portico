import React,{useState} from 'react';
import {Text,View} from 'react-native';
import {channelValueLabel} from '../../../../../../packages/client-core/src/server-admin/channel-catalogue';
import type {LibraryChannelCondition as Condition} from '../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import type {Filter} from '../../../../../../packages/contracts/src/next/api';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button,Select,Switch,TextField} from '../../controls';
import {useTheme} from '../../theme';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import {conditionLabel,useConditionChoices,useConditionFields} from '../../../pages/shared/live/channels/useChannelConditions';
const t=defaultI18n.t;
const label=conditionLabel;
export function NativeChannelConditions({lookup,libraries,rows,match='all',onChange,locked,groups=true,preset=false}:{lookup:NativeChannelsGroup['lookup'];libraries:readonly string[];rows:Condition[];match?:string;onChange:(rows:Condition[],match:string)=>void;locked:boolean;groups?:boolean;preset?:boolean}){
 const theme=useTheme(),{fields,error}=useConditionFields({lookup,libraries,preset});
 const change=(next:Condition[],mode=match)=>{if(!locked&&lookup.current())onChange(next,mode);};
 const count=rows.reduce((sum,row)=>sum+1+(row.conditions?.length??0),0),fresh=()=>({field:fields[0]!.id,values:[]});
 const mode=(value:string,put:(value:string)=>void)=><Select label={t('builder.conditions')} value={value} disabled={locked} options={['all','any'].map(value=>({value,label:t(value==='all'?'builder.matchAll':'builder.matchAny')}))} onValueChange={put}/>;
 return <View style={{gap:10}}><Text style={{color:theme.colors.text}}>{t('builder.conditions')}</Text>{groups&&mode(match,value=>change(rows,value))}
  {rows.map((row,index)=><View key={index} style={{padding:12,gap:8,backgroundColor:theme.colors.raised,borderRadius:12}}>
   {row.conditions&&groups?<>{mode(row.match??'all',match=>change(rows.map((v,i)=>i===index?{...v,match}:v)))}<Switch checked={!!row.exclude} disabled={locked} onCheckedChange={exclude=>change(rows.map((v,i)=>i===index?{...v,exclude}:v))}>{t('builder.exclude')}</Switch>
    {row.conditions.map((child,j)=><ConditionRow key={j} lookup={lookup} library={libraries[0]??''} row={child} fields={fields} locked={locked} update={next=>change(rows.map((v,i)=>i===index?{...v,conditions:v.conditions!.map((item,k)=>k===j?next:item)}:v))} remove={()=>change(rows.map((v,i)=>i===index?{...v,conditions:v.conditions!.filter((_,k)=>k!==j)}:v))}/>)}
    <Button size="compact" disabled={locked||count>=40||!fields.length} onPress={()=>change(rows.map((v,i)=>i===index?{...v,conditions:[...v.conditions!,fresh()]}:v))}>{t('builder.addCondition')}</Button>
   </>:<ConditionRow lookup={lookup} library={libraries[0]??''} row={row} fields={fields} locked={locked} update={next=>change(rows.map((v,i)=>i===index?next:v))}/>}
   <Button size="compact" variant="text" disabled={locked} onPress={()=>change(rows.filter((_,i)=>i!==index))}>{t('builder.remove')}</Button>
  </View>)}
  {!rows.length&&<Text style={{color:theme.colors.textSecondary}}>{t('builder.noConditions')}</Text>}
  <Button size="compact" disabled={locked||count>=40||!fields.length} onPress={()=>change([...rows,fresh()])}>{t('builder.addCondition')}</Button>{groups&&<Button size="compact" disabled={locked||count>=39||!fields.length} onPress={()=>change([...rows,{match:'all',conditions:[fresh()]}])}>{t('builder.addGroup')}</Button>}
  {error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}
 </View>;
}
function ConditionRow({lookup,library,row,fields,locked,update,remove}:{lookup:NativeChannelsGroup['lookup'];library:string;row:Condition;fields:Filter[];locked:boolean;update:(row:Condition)=>void;remove?:()=>void}){
 const theme=useTheme(),{field,names,choices,cursor,error,loading,provider,more}=useConditionChoices({lookup,library,row,fields});
 const put=(values:string[])=>{if(!locked&&lookup.current())update({...row,values});};
 if(!field||row.conditions)return <Text style={{color:theme.colors.textSecondary}}>{t('builder.opaqueConditions')}</Text>;
 return <View style={{gap:8}}><Select label={t('web.ownerChannels.field')} value={row.field!} disabled={locked} options={fields.map(v=>({value:v.id,label:label(v.id)!}))} onValueChange={id=>{if(!locked&&lookup.current())update({field:id,values:[],exclude:row.exclude});}}/>
  <Select label={t('web.ownerChannels.values')} value={row.mode??'any'} disabled={locked} options={['any','all','none'].map(value=>({value,label:t(value==='all'?'next.kit.filter.containsAll':value==='none'?'next.kit.filter.notIn':'next.kit.filter.containsAny')}))} onValueChange={mode=>{if(!locked&&lookup.current())update({...row,mode});}}/>
  <Switch checked={!!row.exclude} disabled={locked} onCheckedChange={exclude=>{if(!locked&&lookup.current())update({...row,exclude});}}>{t('builder.exclude')}</Switch>
  {(row.values??[]).map((value,i)=><View key={i} style={{gap:8}}>{provider?<Text style={{color:theme.colors.text}}>{names[value]||choices.find(v=>v.id===value)?.name||t('error.unavailable')}</Text>:<Scalar field={field} value={value} locked={locked} put={next=>put(row.values!.map((v,j)=>j===i?next:v))}/>}
   <Button size="compact" variant="text" disabled={locked} onPress={()=>put(row.values!.filter((_,j)=>j!==i))}>{t('builder.remove')}</Button></View>)}
  {provider?<><Select label={label(field.id)!} value="" disabled={locked||loading||(row.values?.length??0)>=100} options={[{value:'',label:t('player.opt.add')},...choices.filter(v=>!row.values?.includes(v.id)).map(v=>({value:v.id,label:v.name}))]} onValueChange={value=>{if(choices.some(v=>v.id===value))put([...(row.values??[]),value]);}}/>{cursor&&<Button size="compact" disabled={locked||loading} onPress={more}>{t('action.showMore')}</Button>}</>:<Button size="compact" disabled={locked||(row.values?.length??0)>=100} onPress={()=>put([...(row.values??[]),field.type==='boolean'?'true':''])}>{t('player.opt.add')}</Button>}
  {error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}{remove&&<Button size="compact" disabled={locked} onPress={remove}>{t('builder.remove')}</Button>}
 </View>;
}
function Scalar({field,value,locked,put}:{field:Filter;value:string;locked:boolean;put:(value:string)=>void}){
 const [selected,setSelected]=useState<string>(),range=['number','year','day'].includes(field.type)&&field.id!=='decade',split=value.split('..'),mode=selected??(value.includes('..')?split[0]&&split[1]?'between':split[0]?'min':'max':'equal');
 const factor=field.id==='durationMs'?60000:1;
 if(field.type==='boolean'||field.values||field.id==='dynamicRange'){const values=field.type==='boolean'?['true','false']:field.values??['sdr','hdr10','hdr10plus','hlg','dolby_vision'];return <Select label={label(field.id)!} value={value} disabled={locked} options={[{value:'',label:t('web.ownerChannels.chooseValue')},...values.map(v=>({value:v,label:channelValueLabel(v,t)}))]} onValueChange={put}/>;}
 return <>{range&&<Select label={t('web.ownerChannels.operator')} value={mode} disabled={locked} options={['equal','min','max','between'].map(value=>({value,label:t(value==='equal'?'next.kit.filter.equals':value==='min'?'next.kit.filter.atLeast':value==='max'?'next.kit.filter.atMost':'next.kit.filter.between')}))} onValueChange={next=>{setSelected(next);const v=split.find(Boolean)||'';put(next==='equal'?v:next==='min'?v+'..':next==='max'?'..'+v:v+'..'+v);}}/>}
  {(mode==='between'?[0,1]:[mode==='max'?1:0]).map(i=><TextField key={i} label={field.id==='durationMs'?t('web.ownerChannels.durationMinutes'):label(field.id)!} value={factor!==1&&split[i]?String(Number(split[i])/factor):split[i]??''} editable={!locked} maxLength={500} keyboardType={range||field.id==='decade'?'numeric':'default'} onChangeText={text=>{const v=factor!==1&&text?String(Number(text)*factor):text;if(mode==='equal')put(v);else{const parts=[split[0]??'',split[1]??''];parts[i]=v;put(parts.join('..'));}}}/>)}</>;
}
