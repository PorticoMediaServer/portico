import {useEffect,useState} from 'react';
import {findChannelTitles,readChannelTitle} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import type {NativeChannelsGroup} from '../../../../screens/settings-live/server/channels/model';
import {terminal} from '../../../../screens/settings-live/server/channels/source';

export type ChannelPinsProps={group:NativeChannelsGroup;libraryIds:readonly string[];ids:readonly string[];locked:boolean;label:string;onChange:(ids:string[])=>void;limit?:number;weight?:(id:string)=>number;setWeight?:(id:string,value:number)=>void};

/** A channel's always/never/weighted titles: the chosen titles' names (read four at a time), a search in one chosen library
 * (anime as films or shows), paging, and the limit. Moved from the old field as it was. */
export function useChannelPins({group,libraryIds,ids,locked,onChange,limit=500}:ChannelPinsProps){
 const {lookup,libraries}=group,[library,setLibrary]=useState(libraryIds[0]??''),[anime,setAnime]=useState<'movie'|'show'>('show'),[query,setQuery]=useState(''),[names,setNames]=useState<Record<string,{title:string;kind:string}>>({}),[found,setFound]=useState<{id:string;title:string;kind:string}[]>([]),[cursor,setCursor]=useState<string>(),[page,setPage]=useState<{key:string;cursor:string}>(),[shown,setShown]=useState(20),[error,setError]=useState(false),[loading,setLoading]=useState(false);
 const selected=libraryIds.includes(library)?library:libraryIds[0]??'',libraryKind=libraries.find(v=>v.id===selected)?.kind,kind=libraryKind==='movie'?'movie':libraryKind==='anime'?anime:'show',key=JSON.stringify([query,selected,kind]),labelKey=JSON.stringify(ids.slice(0,shown));
 useEffect(()=>{
  const abort=new AbortController(),loaded:typeof names={};let index=0;
  const values=(JSON.parse(labelKey) as string[]).filter(id=>!names[id]);
  void Promise.all(Array.from({length:Math.min(4,values.length)},async()=>{
   for(;;){
    const id=values[index++];if(!id||abort.signal.aborted||!lookup.current())return;
    try{loaded[id]=await readChannelTitle(lookup.api,id,abort.signal);}
    catch(e){if(!abort.signal.aborted&&lookup.current()&&(e as {status?:number}).status!==404){setError(true);if(terminal(e))lookup.report(e);}}
   }
  })).then(()=>{if(!abort.signal.aborted&&lookup.current()&&Object.keys(loaded).length)setNames(old=>({...old,...loaded}));});
  return()=>abort.abort();
 },[lookup,labelKey,names]);
 useEffect(()=>{const abort=new AbortController();setFound([]);setCursor(undefined);setPage(undefined);setError(false);setLoading(false);const [q,id,k]=JSON.parse(key) as [string,string,'movie'|'show'];if(!id||q.trim().length<2)return()=>abort.abort();setLoading(true);const timer=setTimeout(()=>{void findChannelTitles(lookup.api,id,q.trim(),k,undefined,abort.signal).then(result=>{if(!abort.signal.aborted&&lookup.current()){setFound(result.items);setCursor(result.nextCursor);}},e=>{if(!abort.signal.aborted&&lookup.current()){setError(true);if(terminal(e))lookup.report(e);}}).finally(()=>{if(!abort.signal.aborted&&lookup.current())setLoading(false);});},250);return()=>{abort.abort();clearTimeout(timer);};},[lookup,key]);
 useEffect(()=>{if(!page||page.key!==key)return;const abort=new AbortController();setLoading(true);void findChannelTitles(lookup.api,selected,query.trim(),kind,page.cursor,abort.signal).then(result=>{if(!abort.signal.aborted&&lookup.current()){setFound(old=>[...old,...result.items]);setCursor(result.nextCursor);}},()=>{if(!abort.signal.aborted&&lookup.current())setError(true);}).finally(()=>{if(!abort.signal.aborted&&lookup.current())setLoading(false);});return()=>abort.abort();},[lookup,page,key,selected,query,kind]);
 const change=(next:string[])=>{if(!locked&&lookup.current())onChange(next);};
 return {libraries,ids,selected,libraryKind,anime,setAnime,setLibrary,query,setQuery,names,found:found.filter(v=>!ids.includes(v.id)),cursor,loading,error,shown,setShown,change,
  more:()=>{if(cursor)setPage({key,cursor});},
  add:(row:{id:string;title:string;kind:string})=>{if(!locked&&lookup.current()&&ids.length<limit){change([...ids,row.id]);setNames(old=>({...old,[row.id]:row}));setQuery('');}},
  full:ids.length>=limit,searchable:!locked&&ids.length<limit&&!!selected,noneFound:query.trim().length>=2&&!loading&&!error&&!found.length,
  weigh:(setWeight:((id:string,value:number)=>void)|undefined,id:string,text:string)=>{const n=Number(text);if(!locked&&lookup.current()&&n>=.25&&n<=10)setWeight?.(id,n);}};
}
