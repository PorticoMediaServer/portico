import {useEffect,useState} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {channelFieldLabel,channelValueLabel} from '@core/server-admin/channel-catalogue.ts';
import {channelConditionNames,channelConditionValues,readChannelLibrary,type LibraryChannelCondition as Condition} from '@core/server-admin/owner-library-channels.ts';
import type {Filter} from '../../../../../../packages/contracts/src/next/api.ts';
import {Button,Select,TextField} from '../../../../kit/controls';
import {errorMessage} from '@core/presentation/errors.ts';
import {terminal} from '../../../live/owner/lifetime';
import styles from './channels.module.css';
const t=defaultI18n.t;
export type ChannelLookup={api:LibraryContentApi;current:()=>boolean;report:(error:unknown)=>void;moodLabels?:Readonly<Record<string,string>>};
const personal=new Set(['watched','inProgress','favorite','inList','userRating','lastPlayed','playCount']);
export const presetFields:Filter[]=[{id:'genre',type:'tag'},{id:'tag',type:'tag'},{id:'studio',type:'tag'},{id:'network',type:'tag'},{id:'contentRating',type:'text'},{id:'decade',type:'number'},{id:'year',type:'year'},{id:'audioLanguage',type:'text'},{id:'country',type:'tag'},{id:'collection',type:'collection'},{id:'mood',type:'tag'}];
function label(id:string){return id==='country'?t('web.ownerChannels.country'):id==='mood'?t('web.ownerChannels.mood'):id==='released'?t('filter.field.releaseDate'):id==='available'?t('filter.field.availability'):channelFieldLabel({id},t);}
export function OwnerConditions({lookup,libraries,rows,match='all',onChange,locked,fields:fixed,groups=true}:{lookup:ChannelLookup;libraries:readonly string[];rows:Condition[];match?:string;onChange:(rows:Condition[],match:string)=>void;locked:boolean;fields?:Filter[];groups?:boolean}){
 const [fields,setFields]=useState<Filter[]>(fixed??[]),[error,setError]=useState<unknown>();const key=JSON.stringify(libraries);
 useEffect(()=>{if(fixed){setFields(fixed);return;}const read=new AbortController();setFields([]);setError(undefined);
  void (async()=>{const all:Filter[]=[];for(const id of JSON.parse(key) as string[]){if(read.signal.aborted||!lookup.current())return;const library=await readChannelLibrary(lookup.api,id,read.signal);for(const field of library.filters??[])if(!personal.has(field.id)&&field.id!=='mood'&&label(field.id)&&!all.some(row=>row.id===field.id))all.push(field);}if(!read.signal.aborted&&lookup.current())setFields(all);})().catch(e=>{if(!read.signal.aborted&&lookup.current()){setError(e);if(terminal(e))lookup.report(e);}});return()=>read.abort();
 },[lookup,key,fixed]);
 const change=(next:Condition[],mode=match)=>{if(!locked&&lookup.current())onChange(next,mode);};
 const count=rows.reduce((sum,row)=>sum+1+(row.conditions?.length??0),0),add=(group=false)=>{if(!fields[0]||count>=40)return;change([...rows,group?{match:'all',conditions:[{field:fields[0].id,values:[]}]}:{field:fields[0].id,values:[]}]);};
 return <section className={styles.editorSection}><h3>{t('builder.conditions')}</h3>{groups&&<Select label={t('builder.conditions')} value={match||'all'} disabled={locked} options={['all','any'].map(value=>({value,label:t(value==='all'?'builder.matchAll':'builder.matchAny')}))} onValueChange={mode=>change(rows,mode)}/>}
  {rows.map((row,index)=><div key={index} className={row.conditions?styles.conditionGroup:styles.condition}>
   {row.conditions&&groups?<><Select label={t('builder.conditions')} value={row.match||'all'} disabled={locked} options={['all','any'].map(value=>({value,label:t(value==='all'?'builder.matchAll':'builder.matchAny')}))} onValueChange={mode=>change(rows.map((v,i)=>i===index?{...v,match:mode}:v))}/><label><input type="checkbox" disabled={locked} checked={!!row.exclude} onChange={event=>change(rows.map((v,i)=>i===index?{...v,exclude:event.target.checked}:v))}/>{t('builder.exclude')}</label>
    {row.conditions.map((child,j)=><ConditionRow key={j} lookup={lookup} library={libraries[0]??''} row={child} fields={fields} locked={locked} update={next=>change(rows.map((v,i)=>i===index?{...v,conditions:v.conditions!.map((item,k)=>k===j?next:item)}:v))} remove={()=>change(rows.map((v,i)=>i===index?{...v,conditions:v.conditions!.filter((_,k)=>k!==j)}:v))}/>)}
    <Button size="small" disabled={locked||count>=40||!fields.length} onClick={()=>change(rows.map((v,i)=>i===index?{...v,conditions:[...v.conditions!,{field:fields[0]!.id,values:[]}]}:v))}>{t('builder.addCondition')}</Button>
   </>:<ConditionRow lookup={lookup} library={libraries[0]??''} row={row} fields={fields} locked={locked} update={next=>change(rows.map((v,i)=>i===index?next:v))}/>}
   <Button size="small" variant="text" disabled={locked} onClick={()=>change(rows.filter((_,i)=>i!==index))}>{t('builder.remove')}</Button>
  </div>)}
  {!rows.length&&<p className={styles.help}>{t('builder.noConditions')}</p>}
  <div className={styles.choices}><Button size="small" disabled={locked||count>=40||!fields.length} onClick={()=>add()}>{t('builder.addCondition')}</Button>{groups&&<Button size="small" disabled={locked||count>=39||!fields.length} onClick={()=>add(true)}>{t('builder.addGroup')}</Button>}</div>
  {!!error&&<p role="alert">{errorMessage(error)}</p>}
 </section>;
}
function ConditionRow({lookup,library,row,fields,locked,update,remove}:{lookup:ChannelLookup;library:string;row:Condition;fields:Filter[];locked:boolean;update:(row:Condition)=>void;remove?:()=>void}){
 const field=fields.find(v=>v.id===row.field),[names,setNames]=useState<Record<string,string>>({}),[choices,setChoices]=useState<{id:string;name:string}[]>([]),[cursor,setCursor]=useState<string>(),[error,setError]=useState<unknown>(),[loading,setLoading]=useState(false),[page,setPage]=useState<{field:string;library:string;cursor:string;identity:string}>();
 const identity=JSON.stringify([row.field,row.values,library]);const provider=field&&['tag','person','collection'].includes(field.type);
 useEffect(()=>{const read=new AbortController();setNames({});setChoices([]);setCursor(undefined);setError(undefined);setPage(undefined);if(!provider)return()=>read.abort();setLoading(true);
  const [fieldId,values,selectedLibrary]=JSON.parse(identity) as [string,string[]|undefined,string];
  void Promise.all([channelConditionNames(lookup.api,fieldId,values??[],read.signal,lookup.moodLabels),channelConditionValues(lookup.api,fieldId,selectedLibrary,undefined,read.signal,lookup.moodLabels)]).then(([labels,found])=>{if(!read.signal.aborted&&lookup.current()){setNames(labels);setChoices(found.items);setCursor(found.nextCursor);}},e=>{if(!read.signal.aborted&&lookup.current()){setError(e);if(terminal(e))lookup.report(e);}}).finally(()=>{if(!read.signal.aborted&&lookup.current())setLoading(false);});return()=>read.abort();
 },[lookup,identity,provider]);
 useEffect(()=>{if(!page||page.identity!==identity)return;const read=new AbortController();setLoading(true);void channelConditionValues(lookup.api,page.field,page.library,page.cursor,read.signal,lookup.moodLabels).then(found=>{if(!read.signal.aborted&&lookup.current()){setChoices(old=>[...old,...found.items.filter(item=>!old.some(v=>v.id===item.id))]);setCursor(found.nextCursor);}},e=>{if(!read.signal.aborted&&lookup.current()){setError(e);if(terminal(e))lookup.report(e);}}).finally(()=>{if(!read.signal.aborted&&lookup.current())setLoading(false);});return()=>read.abort();},[lookup,page,identity]);
 const put=(values:string[])=>{if(!locked&&lookup.current())update({...row,values});};
 if(!field||row.conditions)return <><p className={styles.warning}>{t('builder.opaqueConditions')}</p>{remove&&<Button size="small" disabled={locked} onClick={remove}>{t('builder.remove')}</Button>}</>;
 return <div className={styles.predicate}><Select label={t('web.ownerChannels.field')} value={row.field!} disabled={locked} options={fields.map(v=>({value:v.id,label:label(v.id)!}))} onValueChange={id=>{if(!locked&&lookup.current())update({field:id,values:[],exclude:row.exclude});}}/>
  <Select label={t('web.ownerChannels.values')} value={row.mode||'any'} disabled={locked} options={['any','all','none'].map(value=>({value,label:t(value==='all'?'next.kit.filter.containsAll':value==='none'?'next.kit.filter.notIn':'next.kit.filter.containsAny')}))} onValueChange={mode=>update({...row,mode})}/>
  <label><input type="checkbox" checked={!!row.exclude} disabled={locked} onChange={event=>update({...row,exclude:event.target.checked})}/>{t('builder.exclude')}</label>
  {(row.values??[]).map((value,i)=><div key={i} className={styles.choices}>{provider?<span>{names[value]||choices.find(v=>v.id===value)?.name||t('error.unavailable')}</span>:<ScalarValue key={row.field} field={field} value={value} locked={locked} put={next=>put(row.values!.map((v,j)=>j===i?next:v))}/>}
   <Button size="small" variant="text" disabled={locked} onClick={()=>put(row.values!.filter((_,j)=>j!==i))}>{t('builder.remove')}</Button></div>)}
  {provider?<><Select label={label(field.id)!} value="" disabled={locked||loading||(row.values?.length??0)>=100} options={[{value:'',label:t('player.opt.add')},...choices.filter(v=>!row.values?.includes(v.id)).map(v=>({value:v.id,label:v.name}))]} onValueChange={value=>{if(choices.some(v=>v.id===value))put([...(row.values??[]),value]);}}/>{cursor&&<Button size="small" disabled={locked||loading} onClick={()=>{if(cursor)setPage({field:row.field!,library,cursor,identity});}}>{t('action.showMore')}</Button>}</>:<Button size="small" disabled={locked||(row.values?.length??0)>=100} onClick={()=>put([...(row.values??[]),field.type==='boolean'?'true':''])}>{t('player.opt.add')}</Button>}
  {!!error&&<p role="alert">{errorMessage(error)}</p>}{remove&&<Button size="small" variant="text" disabled={locked} onClick={remove}>{t('builder.remove')}</Button>}
 </div>;
}
function ScalarValue({field,value,locked,put}:{field:Filter;value:string;locked:boolean;put:(value:string)=>void}){
 const [selectedMode,setSelectedMode]=useState<string>();
 const range=['number','year','day'].includes(field.type)&&field.id!=='decade',split=value.split('..'),mode=selectedMode??(value.includes('..')?split[0]&&split[1]?'between':split[0]?'min':'max':'equal');
 const factor=field.id==='durationMs'?60000:1,toHuman=(v:string)=>factor!==1&&v?String(Number(v)/factor):v,fromHuman=(v:string)=>factor!==1&&v?String(Number(v)*factor):v;
 if(field.type==='boolean'||field.values||field.id==='dynamicRange'){const values=field.type==='boolean'?['true','false']:field.values??['sdr','hdr10','hdr10plus','hlg','dolby_vision'];return <Select label={label(field.id)!} value={value} disabled={locked} options={[{value:'',label:t('web.ownerChannels.chooseValue')},...values.map(v=>({value:v,label:channelValueLabel(v,t)}))]} onValueChange={put}/>;}
 return <>{range&&<Select label={t('web.ownerChannels.operator')} value={mode} disabled={locked} options={['equal','min','max','between'].map(v=>({value:v,label:t(v==='equal'?'next.kit.filter.equals':v==='min'?'next.kit.filter.atLeast':v==='max'?'next.kit.filter.atMost':'next.kit.filter.between')}))} onValueChange={m=>{setSelectedMode(m);const v=split.find(Boolean)||'';put(m==='equal'?v:m==='min'?v+'..':m==='max'?'..'+v:v+'..'+v);}}/>}
  {(mode==='between'?[0,1]:[mode==='max'?1:0]).map(i=><TextField key={i} label={field.id==='durationMs'?t('web.ownerChannels.durationMinutes'):label(field.id)!} aria-label={mode==='between'?t(i===0?'web.ownerChannels.from':'web.ownerChannels.to'):label(field.id)} type={field.type==='day'?'date':range||field.id==='decade'?'number':'text'} min={field.type==='year'?1:0} max={field.type==='year'?9999:undefined} step={field.id==='decade'?10:field.id==='durationMs'?'any':1} maxLength={500} disabled={locked} value={toHuman(split[i]??'')} onChange={event=>{const next=fromHuman(event.currentTarget.value);if(mode==='equal')put(next);else{const parts=[split[0]??'',split[1]??''];parts[i]=next;put(parts.join('..'));}}}/>)}</>;
}
