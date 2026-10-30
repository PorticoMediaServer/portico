import React,{useEffect,useLayoutEffect,useMemo,useState} from 'react';
import {Text,View} from 'react-native';
import type {ChannelFieldsSource,PinTitle} from '../../../../../../../packages/client-core/src/server-admin/channel-fields-source';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button,Chip,TextField} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
export function NativeChannelPins({source,libraryIds,ids,excluded=false,locked,isCurrent,t,onChange}:{source:ChannelFieldsSource;libraryIds:readonly string[];ids:readonly string[];excluded?:boolean;locked:boolean;isCurrent:()=>boolean;t:typeof defaultI18n.t;onChange:(ids:string[])=>void}) {
  const theme=useTheme(),[query,setQuery]=useState(''),[found,setFound]=useState<readonly PinTitle[]>(),[failed,setFailed]=useState(false),[shown,setShown]=useState(20),[,redraw]=useState(0),visible=useMemo(()=>ids.slice(0,shown),[ids,shown]);
  const scope=useMemo(()=>({source,active:true,search:undefined as AbortController|undefined,labels:undefined as AbortController|undefined,results:undefined as readonly PinTitle[]|undefined}),[source]);
  useLayoutEffect(()=>{scope.active=true;return()=>{scope.active=false;scope.search?.abort();scope.labels?.abort();scope.results=undefined;source.cancelSearch();};},[scope,source]);
  const current=()=>scope.active&&scope.source===source&&isCurrent()&&!locked;
  useEffect(()=>{
    const abort=new AbortController();scope.labels?.abort();scope.labels=abort;
    const owns=()=>scope.active&&scope.labels===abort&&!abort.signal.aborted&&isCurrent();
    if(!visible.length||!owns())return()=>abort.abort();
    void source.select(libraryIds).then(async()=>{
      // The shared source accepts one page of at most twenty IDs. Previously
      // sending the entire visible prefix meant Show more only retried page one.
      for(let offset=0;offset<visible.length;offset+=20){if(!owns())return;await source.labels(visible.slice(offset,offset+20),abort.signal);}
      if(owns())redraw(value=>value+1);
    }).catch(()=>{if(owns())setFailed(true);});
    return()=>abort.abort();
  },[source,libraryIds,visible,isCurrent,scope]);
  useEffect(()=>{const abort=new AbortController();scope.search?.abort();scope.search=abort;scope.results=undefined;setFound(undefined);if(query.trim().length<2)return()=>abort.abort();const timer=setTimeout(()=>{if(!scope.active||scope.search!==abort||abort.signal.aborted||!isCurrent())return;void(async()=>{await source.select(libraryIds);if(!scope.active||scope.search!==abort||abort.signal.aborted||!isCurrent())return;const result=await source.search(query,abort.signal);if(scope.active&&scope.search===abort&&!abort.signal.aborted&&isCurrent()){scope.results=result;setFound(result);setFailed(false);}})().catch(()=>{if(scope.active&&scope.search===abort&&!abort.signal.aborted&&isCurrent())setFailed(true);});},200);return()=>{abort.abort();clearTimeout(timer);};},[source,libraryIds,query,isCurrent,scope]);
  const search=(value:string)=>{if(!current())return;scope.search?.abort();source.cancelSearch();scope.results=undefined;setFound(undefined);setFailed(false);setQuery(value);};
  const choose=(title:PinTitle)=>{if(!current()||ids.length>=500||!found||scope.results!==found||!found.includes(title)||!source.isSearchResult(title))return;scope.search?.abort();source.cancelSearch();scope.results=undefined;setFound(undefined);setQuery('');onChange([...ids.filter(id=>id!==title.id),title.id]);};
  return <View style={{gap:8}}><Text style={[shellType(17,800),{color:theme.colors.text}]}>{t(excluded?'builder.neverInclude':'builder.alwaysInclude')}</Text><View style={{flexDirection:'row',flexWrap:'wrap',gap:8}}>{visible.map(id=><Chip key={id} variant="applied" trailingIcon="i-close" disabled={locked} accessibilityLabel={`${source.title(id)?.title??t('error.unavailable')}, ${t('builder.remove')}`} onPress={()=>{if(current()&&ids.includes(id))onChange(ids.filter(value=>value!==id));}}>{source.title(id)?.title??t('error.unavailable')}</Chip>)}</View>{shown<ids.length&&<Button size="compact" variant="text" onPress={()=>{if(current())setShown(value=>Math.min(ids.length,value+20));}}>{t('action.showMore')}</Button>}<TextField label={t('builder.findTitle')} value={query} maxLength={200} editable={!locked&&ids.length<500&&!!libraryIds.length} onChangeText={search}/>{failed&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}{found&&(found.length?found.map(title=><Button key={title.id} size="compact" variant="glass" disabled={locked} onPress={()=>choose(title)}>{title.title}{title.kind==='show'?` · ${t('builder.pinnedShow')}`:''}</Button>):<Text style={{color:theme.colors.textSecondary}}>{t('builder.noTitles')}</Text>)}</View>;
}
