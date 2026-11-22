import {useEffect,useState} from 'react';
import {channelFieldLabel} from '../../../../../../../packages/client-core/src/server-admin/channel-catalogue';
import {channelConditionNames,channelConditionValues,readChannelLibrary,type LibraryChannelCondition as Condition} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import type {Filter} from '../../../../../../../packages/contracts/src/next/api';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {NativeChannelsGroup} from '../../../../screens/settings-live/server/channels/model';
import {terminal} from '../../../../screens/settings-live/server/channels/source';
const t=defaultI18n.t;
const personal=new Set(['watched','inProgress','favorite','inList','userRating','lastPlayed','playCount']);
export const conditionLabel=(id:string)=>id==='country'?t('web.ownerChannels.country'):id==='mood'?t('web.ownerChannels.mood'):id==='released'?t('filter.field.releaseDate'):id==='available'?t('filter.field.availability'):channelFieldLabel({id},t);
const label=conditionLabel;

/** The fields a channel's conditions can use: a preset's fixed list, or every chosen library's own filters (personal
 * fields and mood left out). Moved from the old conditions field as it was. */
export function useConditionFields({lookup,libraries,preset}:{lookup:NativeChannelsGroup['lookup'];libraries:readonly string[];preset:boolean}){
 const [fields,setFields]=useState<Filter[]>([]),[error,setError]=useState(false),key=JSON.stringify(libraries);
 useEffect(()=>{const abort=new AbortController();setFields([]);setError(false);void(async()=>{
  const found:Filter[]=preset?[{id:'genre',type:'tag'},{id:'tag',type:'tag'},{id:'studio',type:'tag'},{id:'network',type:'tag'},{id:'contentRating',type:'text'},{id:'decade',type:'number'},{id:'year',type:'year'},{id:'audioLanguage',type:'text'},{id:'country',type:'tag'},{id:'collection',type:'collection'},...(lookup.moodAvailable?[{id:'mood',type:'tag'} as Filter]:[])]:[];if(!preset)for(const id of JSON.parse(key) as string[]){const library=await readChannelLibrary(lookup.api,id,abort.signal);for(const field of library.filters??[])if(!personal.has(field.id)&&field.id!=='mood'&&label(field.id)&&!found.some(v=>v.id===field.id))found.push(field);}
  if(!abort.signal.aborted&&lookup.current())setFields(found);
 })().catch(e=>{if(!abort.signal.aborted&&lookup.current()){setError(true);if(terminal(e))lookup.report(e);}});return()=>abort.abort();},[lookup,key,preset]);
 return {fields,error};
}

/** One condition's values: their names and the field's choices (tags, people, collections) with paging. */
export function useConditionChoices({lookup,library,row,fields}:{lookup:NativeChannelsGroup['lookup'];library:string;row:Condition;fields:Filter[]}){
 const field=fields.find(v=>v.id===row.field),[names,setNames]=useState<Record<string,string>>({}),[choices,setChoices]=useState<{id:string;name:string}[]>([]),[cursor,setCursor]=useState<string>(),[error,setError]=useState(false),[loading,setLoading]=useState(false),[page,setPage]=useState<{cursor:string;identity:string}>();
 const identity=JSON.stringify([row.field,row.values,library]),provider=field&&['tag','person','collection'].includes(field.type);
 useEffect(()=>{const abort=new AbortController();setNames({});setChoices([]);setCursor(undefined);setError(false);setPage(undefined);if(!provider)return()=>abort.abort();setLoading(true);
  const [id,values,selectedLibrary]=JSON.parse(identity) as [string,string[]|undefined,string];
  void Promise.all([channelConditionNames(lookup.api,id,values??[],abort.signal,lookup.moodLabels),channelConditionValues(lookup.api,id,selectedLibrary,undefined,abort.signal,lookup.moodLabels)]).then(([labels,found])=>{if(!abort.signal.aborted&&lookup.current()){setNames(labels);setChoices(found.items);setCursor(found.nextCursor);}},e=>{if(!abort.signal.aborted&&lookup.current()){setError(true);if(terminal(e))lookup.report(e);}}).finally(()=>{if(!abort.signal.aborted&&lookup.current())setLoading(false);});return()=>abort.abort();
 },[lookup,identity,provider]);
 useEffect(()=>{if(!page||page.identity!==identity)return;const abort=new AbortController();setLoading(true);void channelConditionValues(lookup.api,row.field!,library,page.cursor,abort.signal,lookup.moodLabels).then(found=>{if(!abort.signal.aborted&&lookup.current()){setChoices(old=>[...old,...found.items.filter(item=>!old.some(v=>v.id===item.id))]);setCursor(found.nextCursor);}},()=>{if(!abort.signal.aborted&&lookup.current())setError(true);}).finally(()=>{if(!abort.signal.aborted&&lookup.current())setLoading(false);});return()=>abort.abort();},[lookup,page,identity,row.field,library]);
 return {field,names,choices,cursor,error,loading,provider,more:()=>{if(cursor)setPage({cursor,identity});}};
}
