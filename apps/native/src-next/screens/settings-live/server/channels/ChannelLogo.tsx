import React,{useEffect,useRef,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import type {LibraryChannel} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {uploadNativeOwnerChannelLogo,removeOwnerChannelLogo} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button} from '../../../../kit/controls';
import {BottomSheet,MenuRow} from '../../../../kit/overlays';
import {useTheme} from '../../../../kit/theme';
import type {NativeChannelsGroup} from './model';
import {pickNativeChannelLogo,type NativeLogoImage} from './logo-upload';
const t=defaultI18n.t;
export function NativeChannelLogo({group,channel}:{group:NativeChannelsGroup;channel:LibraryChannel}){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),theme=useTheme(),[open,setOpen]=useState(false),[image,setImage]=useState<NativeLogoImage>(),[picking,setPicking]=useState(false),[error,setError]=useState(false),attempt=useRef<AbortController|undefined>(undefined);
 useEffect(()=>()=>attempt.current?.abort(),[channel]);
 const current=()=>group.current()&&source.getSnapshot().channels.includes(channel),locked=!!(state.busy||state.editor||state.presetEditor||state.uncertain||state.pendingCreate||state.pendingPreset);
 const close=()=>{if(!state.busy){attempt.current?.abort();attempt.current=undefined;setOpen(false);setImage(undefined);setPicking(false);}};
 const pick=async()=>{if(!current()||locked||picking)return;const read=new AbortController();attempt.current?.abort();attempt.current=read;setPicking(true);setError(false);const owns=()=>attempt.current===read&&!read.signal.aborted&&current();try{const found=await pickNativeChannelLogo(owns);if(owns())setImage(found);}catch{if(owns())setError(true);}finally{if(owns())setPicking(false);}};
 return <><MenuRow variant="grouped" icon="i-image" label={t('web.libraryChannels.uploadLogo')} disabled={locked} onPress={()=>{if(current()&&!locked){setOpen(true);setError(false);}}}/>
  <BottomSheet open={open&&current()} onOpenChange={v=>{if(!v)close();}} title={t('web.libraryChannels.uploadLogo')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" disabled={state.busy} onPress={close}>{t('action.cancel')}</Button><Button disabled={locked||picking||!image} loading={state.busy} onPress={()=>{if(image&&!locked&&current())void source.logo(channel,signal=>uploadNativeOwnerChannelLogo(group.lookup.api,channel.id,image.base64,signal)).then(done=>{if(done&&group.current())close();});}}>{t('action.save')}</Button></View>}>
   <View style={{gap:12}}><Text style={{color:theme.colors.text}}>{channel.name}</Text><Button disabled={locked||picking} loading={picking} onPress={()=>{void pick();}}>{t('web.libraryChannels.uploadLogo')}</Button>{image&&<Text style={{color:theme.colors.textSecondary}}>{image.name}</Text>}{channel.logo&&<Button disabled={locked} onPress={()=>{if(!locked&&current())void source.logo(channel,signal=>removeOwnerChannelLogo(group.lookup.api,channel.id,signal)).then(done=>{if(done&&group.current())close();});}}>{t('builder.remove')}</Button>}{(error||!!state.error)&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}</View>
  </BottomSheet>
 </>;
}
