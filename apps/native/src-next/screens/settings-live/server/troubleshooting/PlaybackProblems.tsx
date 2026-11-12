import React,{useEffect,useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {PlaybackFailure} from '../../../../../../../packages/client-core/src/playback-failure-memory';
import type {NativeSettingsLivePorts} from '../../ports';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {Button} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {NativePlaybackProblemsSource} from './playback-problems-source';
const ROUTES={original:'server.routes.route.original',repackaged:'server.routes.route.repackaged',converted:'server.routes.route.converted'} as const;
export function NativePlaybackProblems({ports,runtime}:{ports:NativeSettingsLivePorts;runtime:NativeShellRuntime}){
  const context=ports.context,reportError=runtime.reportError,[held,setHeld]=useState<{context:typeof context;source:NativePlaybackProblemsSource}>();
  useLayoutEffect(()=>{const source=new NativePlaybackProblemsSource(context,reportError);setHeld({context,source});return source.dispose;},[context,reportError]);
  return held?.context===context?<Problems source={held.source}/>:null;
}
function Problems({source}:{source:NativePlaybackProblemsSource}){
  const state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),theme=useTheme(),t=defaultI18n.t;
  useEffect(()=>{void source.load();},[source]);
  if(state.phase==='idle'||!state.groups.length&&state.phase!=='error')return null;
  const line=(failure:PlaybackFailure)=>t('server.routes.line',{route:t(ROUTES[failure.route]),device:failure.device,when:defaultI18n.date(failure.reportedAtMs,'dayMonth')});
  return <View style={{marginTop:16,gap:12}}><Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('server.routes.title')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('server.routes.lede')}</Text>{state.phase==='error'&&<><Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('next.states.pageFailedBody')}</Text><Button size="compact" variant="glass" onPress={()=>{void source.load();}}>{t('next.shell.tryAgain')}</Button></>}{state.groups.map(group=><View key={group.sourceId} style={{padding:12,gap:8,borderRadius:12,backgroundColor:theme.colors.raised}}><Text style={[shellType(15,700),{color:theme.colors.text}]}>{group.item?.title||t('server.routes.unknownTitle')}</Text>{group.failures.map((failure,index)=><Text key={index} style={{color:theme.colors.textSecondary}}>{line(failure)}</Text>)}<Text style={{color:theme.colors.textSecondary}}>{t('server.routes.when',{until:defaultI18n.date(group.expiresAtMs,'dayMonth')})}</Text><Button size="compact" variant="glass" disabled={!!state.busy} onPress={()=>{void source.forget(group);}}>{t('server.routes.forget')}</Button></View>)}{state.more&&<Text style={{color:theme.colors.textSecondary}}>{t('server.routes.more')}</Text>}</View>;
}
