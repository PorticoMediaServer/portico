import {useEffect,useRef,useSyncExternalStore} from 'react';
import {useRouterState} from '@tanstack/react-router';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {defineGalleryFamily} from '../gallery-registry';
import type {WebShellPlaybackRuntime} from '../../platform/playback';
const noSubscribe=()=>()=>{},noSnapshot=()=>undefined;
/** DEV inspector for the real already-mounted Watch host. Sign in and start an actual video
 * before opening this case. There is no fixture runtime, second media element or fallback DTO. */
function WatchDevEntry(){
 const captured=useProtectedWebScreenContext(),runtime=captured?.binding.playback?.runtime;
 const snapshot=useSyncExternalStore(runtime?.subscribe??noSubscribe,runtime?.getSnapshot??noSnapshot,noSnapshot);
 const status=useRouterState({select:state=>state.status}),opened=useRef<{runtime:WebShellPlaybackRuntime;run:number}|null>(null);
 useEffect(()=>{
  const fence=snapshot?.fence;
  if(status!=='idle'||!runtime||!captured?.context.isCurrent()||snapshot?.subject?.kind!=='video'||!fence)return;
  if(opened.current?.runtime===runtime&&opened.current.run===fence.playback.run)return;
  opened.current={runtime,run:fence.playback.run};runtime.open(fence);
 },[captured,runtime,snapshot,status]);
 return null;
}
export default defineGalleryFamily({id:'watch',labelKey:'next.dashboard.player',cases:[
 {id:'desktop-player-f1',labelKey:'next.dashboard.player',width:1440,height:810,render:WatchDevEntry},
]});
