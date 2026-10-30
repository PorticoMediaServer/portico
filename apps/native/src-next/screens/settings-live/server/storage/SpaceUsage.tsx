import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {MessageId} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {Button} from '../../../../kit/controls/Button';
import {alpha, shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {nativeStorageOwner, NativeStorageSpaceSource} from './space-source';
const folderKeys:Readonly<Record<string,string>>={Database:'database',Metadata:'metadata',Cache:'cache',Backups:'backups',Logs:'logs',Keys:'keys',Temp:'temp'};
export function NativeStorageSpaceUsage({context,runtime}:Readonly<{context:NativeShellBootstrapContext;runtime:NativeShellRuntime}>){
  const [source,setSource]=useState<NativeStorageSpaceSource>();
  const reportError=runtime.reportError;
  useLayoutEffect(()=>{const next=new NativeStorageSpaceSource(context,reportError);setSource(next);return next.dispose;},[context,reportError]);
  return source&&nativeStorageOwner(context)?<Usage source={source} runtime={runtime}/>:null;
}
function Usage({source,runtime}:Readonly<{source:NativeStorageSpaceSource;runtime:NativeShellRuntime}>){
  const theme=useTheme(),t=runtime.text.t as typeof defaultI18n.t,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot);
  useEffect(()=>{void source.load();},[source]);
  if(state.phase==='error')return <View><Text accessibilityRole="alert" style={[shellType(13,400),{color:theme.colors.textSecondary}]}>{t('error.unavailable')}</Text><Button size="compact" variant="glass" onPress={()=>{void source.load();}}>{t('action.tryAgain')}</Button></View>;
  if(!state.usage)return <Text accessibilityRole="progressbar" style={[shellType(13,400),{color:theme.colors.textSecondary}]}>{t('next.settings.loading')}</Text>;
  const usage=state.usage;
  const rows=usage.folders.map(folder=>{const key=folderKeys[folder.name],label=key?`server.space.${key}`:undefined,help=key?`server.space.${key}Help`:undefined;return{id:folder.name,name:label&&defaultI18n.has(label)?t(label as MessageId):folder.name,bytes:folder.bytes,help:help&&defaultI18n.has(help)?t(help as MessageId):undefined};});
  if(usage.transcodeDirectory){const label='server.space.conversions',help='server.space.conversionsHelp';rows.push({id:'conversions-outside-data',name:defaultI18n.has(label)?t(label as MessageId):t('next.dashboard.conversions'),bytes:usage.transcodeBytes,help:defaultI18n.has(help)?t(help as MessageId,{path:usage.transcodeDirectory}):usage.transcodeDirectory});}
  const total=rows.reduce((sum,row)=>sum+row.bytes,0);
  return <View testID="server-storage-data-usage"><View style={{gap:4,marginBottom:18}}><Text style={[shellType(15,600),{color:theme.colors.text}]}>{t('next.settingsSpec.server.storage.groups.0.rows.0.label')}</Text><Text selectable style={[shellType(13,400,18),{color:theme.colors.textSecondary}]}>{usage.stateDirectory}</Text><Text style={[shellType(12.5,400,17),{color:theme.colors.textTertiary}]}>{t('next.settingsSpec.server.storage.groups.0.rows.0.help')}</Text></View>
    {usage.onNetwork===true&&<View style={{gap:4,marginBottom:18}}><Text style={[shellType(15,600),{color:theme.colors.amber}]}>{t('next.settingsSpec.server.storage.groups.0.rows.1.label')}</Text><Text style={[shellType(12.5,400,17),{color:theme.colors.textSecondary}]}>{t('next.settingsSpec.server.storage.groups.0.rows.1.help')}</Text></View>}
    {usage.notices.map((notice,index)=><Text key={`${notice.code}/${index}`} accessibilityRole="alert" style={[shellType(13,400,18),{color:theme.colors.textSecondary,marginBottom:8}]}>{notice.message}</Text>)}
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('next.settingsSpec.server.storage.groups.0.rows.2.label')}</Text>
    <View style={{marginTop:10,borderRadius:14,overflow:'hidden',backgroundColor:theme.colors.raised}}>
      <View style={{paddingHorizontal:14,paddingVertical:12}}><View style={{flexDirection:'row',justifyContent:'space-between'}}><Text style={[shellType(16,700),{color:theme.colors.text}]}>{t('web.maintenance.totalLabel')}</Text><Text style={[shellType(22,800,'normal',-.02),{color:theme.colors.text}]}>{defaultI18n.bytes(total)}</Text></View><Text style={[shellType(13,400,18),{color:theme.colors.textSecondary,marginTop:4}]}>{t('web.maintenance.storageLede')}</Text></View>
      {rows.map(row=><View key={row.id} style={{paddingHorizontal:14,paddingVertical:11,borderTopWidth:1,borderTopColor:theme.colors.lineSoft}}><View style={{flexDirection:'row',justifyContent:'space-between',gap:10}}><Text style={[shellType(15.5,600),{color:theme.colors.text,flex:1}]}>{row.name}</Text><Text style={[shellType(15,700),{color:theme.colors.textSecondary}]}>{defaultI18n.bytes(row.bytes)}</Text></View>{total>0&&<View accessible={false} style={{height:5,borderRadius:3,overflow:'hidden',marginTop:6,marginBottom:2,backgroundColor:alpha(theme.colors.text,.09)}}><View style={{height:'100%',width:`${Math.max(0,Math.min(1,row.bytes/total))*100}%`,borderRadius:3,backgroundColor:theme.colors.accent}}/></View>}{row.help&&<Text style={[shellType(12.5,400,17),{color:theme.colors.textTertiary,marginTop:2}]}>{row.help}</Text>}</View>)}
    </View>
  </View>;
}
