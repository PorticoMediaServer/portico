import {useEffect,useRef,useState,useSyncExternalStore} from 'react';
import type {LibraryChannel} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {uploadNativeOwnerChannelLogo,removeOwnerChannelLogo} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import type {NativeChannelsGroup} from '../../../../screens/settings-live/server/channels/model';
import {pickNativeChannelLogo,type NativeLogoImage} from '../../../../screens/settings-live/server/channels/logo-upload';

/** A channel's logo: pick an image, upload it or remove the current one; a pick or save for a channel that has gone
 * is dropped. Moved from the old sheet as it was. */
export function useChannelLogo({group,channel}:{group:NativeChannelsGroup;channel:LibraryChannel}){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),[open,setOpen]=useState(false),[image,setImage]=useState<NativeLogoImage>(),[picking,setPicking]=useState(false),[error,setError]=useState(false),attempt=useRef<AbortController|undefined>(undefined);
 useEffect(()=>()=>attempt.current?.abort(),[channel]);
 const current=()=>group.current()&&source.getSnapshot().channels.includes(channel),locked=!!(state.busy||state.editor||state.presetEditor||state.uncertain||state.pendingCreate||state.pendingPreset);
 const close=()=>{if(!state.busy){attempt.current?.abort();attempt.current=undefined;setOpen(false);setImage(undefined);setPicking(false);}};
 const pick=async()=>{if(!current()||locked||picking)return;const read=new AbortController();attempt.current?.abort();attempt.current=read;setPicking(true);setError(false);const owns=()=>attempt.current===read&&!read.signal.aborted&&current();try{const found=await pickNativeChannelLogo(owns);if(owns())setImage(found);}catch{if(owns())setError(true);}finally{if(owns())setPicking(false);}};
 return {state,open:open&&current(),image,picking,error:error||!!state.error,locked,close,
  show:()=>{if(current()&&!locked){setOpen(true);setError(false);}},
  pick:()=>{void pick();},
  save:()=>{if(image&&!locked&&current())void source.logo(channel,signal=>uploadNativeOwnerChannelLogo(group.lookup.api,channel.id,image.base64,signal)).then(done=>{if(done&&group.current())close();});},
  remove:()=>{if(!locked&&current())void source.logo(channel,signal=>removeOwnerChannelLogo(group.lookup.api,channel.id,signal)).then(done=>{if(done&&group.current())close();});}};
}
