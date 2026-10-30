import React,{useLayoutEffect,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import type {LibraryChannel} from '../../../../../../../packages/client-core/src/library-channels';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button} from '../../../../kit/controls';
import {BottomSheet, MenuRow} from '../../../../kit/overlays';
import {useTheme} from '../../../../kit/theme';
import type {NativeChannelsGroup} from './model';
import {pickNativeChannelLogo,type NativeLogoImage} from './logo-upload';
type Dialog=Readonly<{token:object;channel:LibraryChannel;image?:NativeLogoImage}>;
type Scope={active:boolean;dialog?:Dialog;picker?:object};
export function NativeChannelLogo({group,channel,isCurrent,claim,release,menuRow = false}:{menuRow?: boolean;group:NativeChannelsGroup;channel:LibraryChannel;isCurrent:()=>boolean;claim:(token:object)=>boolean;release:(token:object)=>void}){
  const source=group.logos,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot),theme=useTheme(),t=defaultI18n.t;
  const [scope,setScope]=useState<Scope>(),[dialog,setDialog]=useState<Dialog>(),[picking,setPicking]=useState(false),[error,setError]=useState(false);
  useLayoutEffect(()=>{const next:Scope={active:true};setScope(next);return()=>{next.active=false;if(next.dialog)release(next.dialog.token);next.dialog=undefined;next.picker=undefined;};},[source,channel,release]);
  const current=()=>!!scope?.active&&group.current()&&isCurrent()&&!!group.reader.getSnapshot().channels?.includes(channel);
  const held=dialog&&scope?.dialog===dialog&&dialog.channel===channel&&current()?dialog:undefined;
  const writable=()=>current()&&!source.getSnapshot().busy&&!source.getSnapshot().pending;
  const close=()=>{if(!scope||scope.dialog!==held)return;if(scope.dialog)release(scope.dialog.token);scope.dialog=undefined;scope.picker=undefined;setDialog(undefined);setPicking(false);setError(false);};
  const open=()=>{if(!scope||!writable()||scope.dialog)return;const next={channel,token:{}};if(!claim(next.token))return;scope.dialog=next;setDialog(next);setError(false);};
  const pick=async()=>{const captured=held;if(!scope||!captured||!writable()||scope.picker)return;const attempt={};scope.picker=attempt;setPicking(true);setError(false);
    const owns=()=>writable()&&scope.dialog===captured&&scope.picker===attempt;
    try{const image=await pickNativeChannelLogo(owns);if(!image||!owns())return;const next={...captured,image};scope.dialog=next;setDialog(next);}catch{if(owns())setError(true);}finally{if(scope.picker===attempt){scope.picker=undefined;if(scope.active)setPicking(false);}}
  };
  const upload=async()=>{const captured=held;if(!scope||!captured?.image||!writable()||scope.picker||scope.dialog!==captured)return;const done=await source.choose(captured.channel,captured.image);if(done&&scope.active&&group.current()&&isCurrent()&&scope.dialog===captured){if(scope.dialog)release(scope.dialog.token);scope.dialog=undefined;scope.picker=undefined;setDialog(undefined);setPicking(false);}};
  return <>{menuRow ? <MenuRow variant="grouped" icon="i-image" label={t('web.libraryChannels.uploadLogo')} disabled={state.busy||!!state.pending} onPress={open}/> : <Button size="compact" variant="glass" disabled={state.busy||!!state.pending} onPress={open}>{t('web.libraryChannels.uploadLogo')}</Button>}<BottomSheet open={!!held} onOpenChange={value=>{if(!value)close();}} title={t('web.libraryChannels.uploadLogo')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button><Button disabled={!held?.image||picking||state.busy||!!state.pending} loading={state.busy} onPress={()=>{void upload();}}>{t('web.libraryChannels.uploadLogo')}</Button></View>}><View style={{gap:12}}><Text style={{color:theme.colors.text}}>{held?.channel.config.name}</Text><Button variant="glass" disabled={picking||state.busy||!!state.pending} loading={picking} onPress={()=>{void pick();}}>{t('web.libraryChannels.uploadLogo')}</Button>{held?.image&&<Text style={{color:theme.colors.textSecondary}}>{held.image.name}</Text>}{(error||state.error)&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}</View></BottomSheet></>;
}
