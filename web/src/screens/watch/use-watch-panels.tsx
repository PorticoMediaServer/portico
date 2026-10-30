import {subscribePresentation} from '../../shell/fact-hooks';
import {useShellFacts} from '../../shell/fact-hooks';
import {useEffect,useLayoutEffect,useMemo,useRef,useState,useSyncExternalStore} from 'react';
import {router} from '../../router';
import {defaultI18n} from '@i18n';
import {shellLanguage} from '../../shell/settings';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import type {WebPlaybackRuntimeSnapshot,WebQueueListPort} from '../../platform/playback';
import type {WebShellPlaybackRuntime} from '../../platform/playback';
import type {WebQueueListLifetime} from '../../platform/playback/queue-list';
import {fenceKey} from '../../shell/playback-model';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import {playbackFailure} from '@core/presentation/playback-failure.ts';
import {WatchStreamLimitSessions} from './StreamLimitSessions';
import type {WatchBindings,WatchSurfaces,WatchPanel} from './types';
import {watchPanelModel} from './panel-model';
import {WatchOptions} from './WatchOptions';
import {WatchRoomPill} from './WatchRoomPill';
import {WatchStartRoom} from './StartRoom';
import {useTogetherBindings} from '../together/owner';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {castDisplayName} from '../../platform/cast';
import {WatchInfoPanel} from './WatchInfoPanel';
import {watchLossNotice} from './loss-copy';
import {settingsPlatform} from '../../platform/settings-platform';
const noPreferences=()=>null;
// The persistent media host is a sibling of RouterProvider, including while
// signed out. Subscribe to the existing router directly; context hooks here
// would throw before the credential screen can render.
const subscribeQueueRoute=(changed:()=>void)=>router.subscribe('onResolved',changed);
const queueRouteSnapshot=()=>router.state.location;
/** Selecting a queue row replaces the output and its drawer, but not the
 * original route/authority. Keep that command on this committed route lifetime
 * so drawer teardown cannot cancel its own accepted playback. */
function useQueueSelectionLife(context:ShellAssemblyContext|undefined,runtime:WebShellPlaybackRuntime|undefined):WebQueueListLifetime|undefined {
 const location=useSyncExternalStore(subscribeQueueRoute,queueRouteSnapshot,queueRouteSnapshot);
 const [held,publish]=useState<Readonly<{context:ShellAssemblyContext;runtime:WebShellPlaybackRuntime;location:typeof location;life:WebQueueListLifetime}>>();
 useLayoutEffect(()=>{
  if(!context?.isCurrent()||!runtime)return;
  const controller=new AbortController();
  const life:WebQueueListLifetime={signal:controller.signal,isCurrent:()=>!controller.signal.aborted&&context.isCurrent()
   &&router.state.location===location&&!runtime.getSnapshot().disposed};
  publish({context,runtime,location,life});
  return()=>controller.abort();
 },[context,runtime,location]);
 return held&&held.context===context&&held.runtime===runtime&&held.location===location&&held.life.isCurrent()?held.life:undefined;
}
/** One adapter in the persistent parent; components borrow the existing runtime and stores. */
export function useWatchPanels(binding:WatchBindings|undefined,snapshot:WebPlaybackRuntimeSnapshot|undefined,context:ShellAssemblyContext|undefined):WatchSurfaces|undefined {
 useShellFacts(context);
 const preferences=useSyncExternalStore((listener)=>subscribePresentation(context,listener),()=>context?.preferences?.getSnapshot('web')??context?.facts.preferences??null,noPreferences);
 const i18n=useMemo(()=>preferences?shellLanguage(preferences):defaultI18n,[preferences]);
 // This device's own setting, read from the app's device document in the same render.
 const deviceDocument=settingsPlatform()?.device?.getSnapshot().document;
 const together=useTogetherBindings(context),model=useMemo(()=>binding&&snapshot?watchPanelModel(binding.runtime,snapshot,i18n):null,[binding,snapshot,i18n]);
 const [startRoom,setStartRoom]=useState(false),room=together?.getSnapshot().room;
 // f1q: one port per captured queue run, so a replacement fence never inherits an old list.
 const selectionLife=useQueueSelectionLife(context,binding?.runtime);
 const ports=useRef<{key?:string;life?:WebQueueListLifetime;port?:WebQueueListPort}>({});
 const fence=snapshot?.fence,key=fence?fenceKey(fence):undefined;
 if(!key||key!==ports.current.key||selectionLife!==ports.current.life||!ports.current.port?.isCurrent())ports.current={key,life:selectionLife,port:fence&&binding&&typeof binding.runtime.queueList==='function'?binding.runtime.queueList(fence,selectionLife):undefined};
 const queue=ports.current.port?.isCurrent()?ports.current.port:undefined;
 // states f9: an owner who can manage this server gets the drawn way to the real setting. The
 // action belongs to the exact failed attempt it was drawn for; the click re-checks that attempt
 // and the same owner/main-profile/manage-server gate before it goes anywhere.
 const identity=context?.facts.identity,viewer=identity?.viewer,ownerContext=context?.isCurrent()?context:undefined;
 const runtime=binding?.runtime,failedFence=snapshot?.fence,failedKey=failedFence?fenceKey(failedFence):'',failedRun=snapshot?.playback.playbackRun,failedIntent=snapshot?.playback.intentId;
 const failureBody=snapshot?.playback.phase==='error'?playbackFailure(snapshot.playback.error).body:undefined;
 const canManage=viewer?.role==='owner'&&identity?.primaryProfile===true&&identity?.capabilities?.manageServer===true;
 const convertingOwnerAction=!!snapshot&&!!runtime&&failureBody==='web.player.error.conversionOff'&&canManage&&ownerContext
  ?()=>{
   if(!ownerContext.isCurrent()||viewer?.role!=='owner'||identity?.primaryProfile!==true||identity?.capabilities?.manageServer!==true)return;
   const live=runtime.getSnapshot();
   if(live.disposed||live.playback.phase!=='error'||live.playback.playbackRun!==failedRun||live.playback.intentId!==failedIntent
    ||!live.fence||!failedFence||live.fence.kind!=='queue'||!sameFence(live.fence,failedFence))return;
   void router.navigate({to:'/settings',search:()=>({scope:'server' as const,section:'streaming'})});
  }:undefined;
 // states f9: the stream-limit list belongs to this exact failed attempt, never to a later one.
 const failedCurrent=useMemo(()=>()=>{
  if(!binding||!context?.isCurrent()||!failedKey)return false;
  const live=binding.runtime.getSnapshot();
  // The same live stream-limit failure, not merely the same fence: a later unrelated error
  // on this run must not list sessions under this card. Keys, never a fence object, so this
  // predicate is stable while the failure stands.
  return !live.disposed&&live.playback.phase==='error'&&playbackFailure(live.playback.error).body==='web.player.error.streamLimit'
   &&!!live.fence&&live.fence.kind==='queue'&&failedKey===fenceKey(live.fence)
   &&live.playback.intentId===failedIntent&&live.playback.playbackRun===failedRun;
 },[binding,context,failedKey,failedIntent,failedRun]);
 // desktop-player f1: Watch Together is listed only when this playback has no room yet.
 const canStartRoom=!!together&&!!snapshot&&!snapshot.disposed&&snapshot.fence?.kind==='queue'&&snapshot.presentation?.kind==='video'
  &&!(room?.group&&(room.phase==='live'||room.phase==='reconnecting'));
 useEffect(()=>{if(!canStartRoom)setStartRoom(false);},[canStartRoom]);
 if(!binding||!snapshot||!context?.isCurrent())return undefined;
 const available:WatchPanel[]=[];
 if(model?.audio.length||model?.subtitles.length||model?.subtitleAdditions.length)available.push('subtitles-audio');
 if(model?.audio.length)available.push('audio');if(model?.subtitles.length||model?.subtitleAdditions.length)available.push('subtitles');
 if(model?.quality.some(row=>row.enabled))available.push('quality');if(model?.speed.length)available.push('speed');
 // The drawn options panel holds the media controls; the queue list is its own surface, and it
 // appears only when something really follows this entry (an unknown count never claims empty).
 const controls=available.length>0,remaining=snapshot.queueView?.remainingCount;
 if(queue&&(remaining===undefined||remaining>0))available.push('queue');
 if(controls)available.push('options');
 // Playback info f1: offered only when the server committed its own comparison for
 // this presentation; the panel draws those facts and this browser's observations.
 const infoReady=!!snapshot&&!snapshot.disposed&&snapshot.fence?.kind==='queue'&&!!snapshot.session?.playbackInfo&&!!snapshot.subject;
 if(infoReady)available.push('info');
 // Frame 6: one note per playback for Dolby Vision, HDR or Atmos lost here, unless
 // this device's own "Picture and sound notices" setting is off.
 const loss=snapshot?.session?.playbackInfo?watchLossNotice(i18n,snapshot.session.playbackInfo,snapshot.session.observedPlayback,String(snapshot.fence?.playback??snapshot.session.id)):undefined;
 return {available,render:panel=>{
   const live=panel.snapshot;
   if(panel.panel==='info'&&live.fence?.kind==='queue'&&live.session?.playbackInfo&&live.subject)
    return <WatchInfoPanel runtime={binding.runtime} snapshot={live} fence={live.fence} title={playbackIdentity(live.subject,'full',i18n).title}
      deviceName={castDisplayName()} i18n={i18n} layout={panel.layout} phoneTop={panel.phoneTop} returnFocusRef={panel.returnFocusRef}
      close={panel.close} reportError={binding.reportError}/>;
   return model&&panel.panel&&available.includes(panel.panel)&&model.isCurrent()?<WatchOptions model={model} context={panel} i18n={i18n} reportError={binding.reportError} info={infoReady} openSettings={()=>{if(!context.isCurrent()||!model.isCurrent())throw new DOMException('Playback source retired','AbortError');return router.navigate({to:'/settings'});}}/>:null;
  },
  quality:model?.qualityLabel&&model.qualityAccessibleLabel&&available.includes('quality')?{fence:model.fence,automatic:model.automatic,label:model.qualityLabel,accessibleLabel:model.qualityAccessibleLabel}:undefined,
  tracks:model?{fence:model.fence,audio:model.audioLabel,subtitles:model.subtitleLabel}:undefined,
  ...(queue?{queue}:{}),
  ...(convertingOwnerAction?{convertingOwnerAction}:{}),
  ...(failureBody==='web.player.error.streamLimit'
    ?{streamLimit:<WatchStreamLimitSessions api={context.api} isCurrent={failedCurrent} excludeSessionId={snapshot.session?.id} reportError={binding.reportError}/>}:{}),
  ...(loss?{loss:{notice:loss,enabled:deviceDocument?.values['lossNotices']!==false}}:{}),
  together:<WatchRoomPill binding={together} runtime={binding.runtime} snapshot={snapshot} reportError={binding.reportError}/>,
  more:canStartRoom&&together?[{id:'watch-together',label:i18n.t('web.settings.together'),action:()=>setStartRoom(true)}]:[],
  prompts:canStartRoom&&together?<WatchStartRoom binding={together} open={startRoom} onOpenChange={setStartRoom}/>:undefined,
 };
}
