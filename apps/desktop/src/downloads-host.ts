import type {SavedDownloadsBinder,SavedDownloadsBinding} from '../../../web/src/screens/saved/native-binding.ts';
import type {WebPlaybackEnvironment,WebLocalQueueController} from '../../../web/src/platform/playback/types.ts';
import {localPlaybackScope,type LocalPlaybackScope} from '../../../packages/player-core/src/index.ts';
import {DesktopDownloadStore,nativeDownloadsBridge,downloadFailure,type NativeDownloadsBridge,type LocalSelection} from './downloads.ts';
import {DesktopLocalPlaybackBroker} from './local-playback.ts';
import {createDesktopSavedDownloadsPort} from './saved-downloads.ts';
import {createDesktopOfflineSession} from './offline-session.ts';
import {DesktopNetworkMonitor} from './network.ts';

const key=(scope:LocalPlaybackScope)=>JSON.stringify([scope.authority,scope.serverId,scope.accountId,scope.profileId,scope.sessionFamilyId]);
const aborted=()=>new DOMException('Aborted','AbortError');
type Attachment=Readonly<{controller:WebLocalQueueController;life:AbortController;retire:()=>void}>;
/** One app-owned broker. Saved retains its captured lease; the actual playback
 * runtime alone attaches its controller. No observer, engine or viewer is minted. */
export function createDesktopDownloadsHost(options:Readonly<{
  bridge?:NativeDownloadsBridge;network?:()=>DesktopNetworkMonitor;reportFailure:(error:Error)=>void;
}>) {
  const broker=new DesktopLocalPlaybackBroker(),life=new AbortController();
  const attachments=new Map<string,Attachment>(),listeners=new Set<()=>void>(),bindings=new Set<()=>void>();
  const notify=()=>{for(const listener of [...listeners])listener();};
  const attachLocalQueue:NonNullable<WebPlaybackEnvironment['attachLocalQueue']>=(input,controller)=>{
    if(life.signal.aborted)throw aborted();
    const scope=localPlaybackScope(input),id=key(scope),previous=attachments.get(id);
    previous?.retire();
    const attachmentLife=new AbortController(),off=controller.subscribe(notify);let retired=false;
    const attachment={controller,life:attachmentLife,retire(){if(retired)return;retired=true;attachmentLife.abort();off();}};
    attachments.set(id,attachment);notify();
    return()=>{attachment.retire();if(attachments.get(id)===attachment){attachments.delete(id);notify();}};
  };
  const waitForController=(scope:LocalPlaybackScope,signal:AbortSignal,current:()=>boolean):Promise<void>=>new Promise((resolve,reject)=>{
    const done=()=>{listeners.delete(check);signal.removeEventListener('abort',check);};
    const check=()=>{
      if(signal.aborted||!current()){done();reject(aborted());return;}
      if(attachments.get(key(scope))?.controller.canPlayLocalQueue()){done();resolve();}
    };
    listeners.add(check);signal.addEventListener('abort',check,{once:true});check();
  });
  const bindSaved:SavedDownloadsBinder=async input=>{
    const scope=localPlaybackScope(input.runtime.scope),captured=new AbortController();
    const signal=AbortSignal.any([input.signal,life.signal,captured.signal]);
    const current=()=>!signal.aborted&&input.isCurrent();
    // Never install a Play surrogate while the actual same-scope runtime lacks
    // its native port. Its real mount/subscription wakes this binding, without polling.
    await waitForController(scope,signal,current);
    const store=await DesktopDownloadStore.bind(scope,current,options.bridge??nativeDownloadsBridge,signal);
    let port:ReturnType<typeof createDesktopSavedDownloadsPort>|undefined;
    let offline:ReturnType<typeof createDesktopOfflineSession>|undefined,disposed=false;
    const dispose=()=>{
      if(disposed)return;disposed=true;captured.abort();bindings.delete(dispose);
      signal.removeEventListener('abort',dispose);port?.dispose();offline?.dispose();store.dispose();
    };
    bindings.add(dispose);signal.addEventListener('abort',dispose,{once:true});
    try{
      if(!current())throw aborted();
      const routes=input.api.getRouteConnection();
      offline=createDesktopOfflineSession({store,downloads:input.runtime.downloads,api:input.api,
        resolvePublishedPath:path=>input.api.mediaUrl(path),serverOrigin:input.api.baseUrl,fetcher:input.api.routeFetch,
        // Only the captured canonical route connection may admit its proved
        // physical origin. A replacement connection cannot widen this transfer.
        isPublishedOrigin:origin=>input.api.getRouteConnection()===routes&&routes?.acceptsOrigin(origin)===true,
      },signal,options.reportFailure,options.network?.());
      const play=async(selection:LocalSelection,actionSignal:AbortSignal)=>{
        const attachment=attachments.get(key(scope));
        if(!current()||!attachment||!attachment.controller.canPlayLocalQueue())throw new Error('download_not_ready');
        const active=AbortSignal.any([signal,actionSignal,attachment.life.signal]);
        const isCurrent=()=>current()&&attachments.get(key(scope))===attachment&&!active.aborted;
        const resource=await broker.prepare(store,selection,{signal:active,isCurrent});
        try{await attachment.controller.playLocalQueue(resource,{signal:active,isCurrent});}
        catch(error){await resource.release().catch(error=>options.reportFailure(downloadFailure(error)));throw error;}
      };
      port=createDesktopSavedDownloadsPort({store,signal,wake:offline.wake,
        folder:{read:signal=>store.folderLocation(signal),choose:signal=>store.chooseFolder(signal),open:signal=>store.openFolder(signal)},
        playback:{play,playGroup:play},
      });
      const session=offline;
      const binding:SavedDownloadsBinding=Object.freeze({localDownloads:port,saveReadyPreparation:async(preparation,actionSignal)=>{
        if(!current())throw aborted();await session.saveReadyPreparation(preparation,AbortSignal.any([signal,actionSignal]));if(!current())throw aborted();
      },dispose});
      if(!current())throw aborted();return binding;
    }catch(error){dispose();throw error;}
  };
  const offlineStores=new Set<DesktopDownloadStore>();
  /** A launch-time view of this device's completed downloads, bound to the persisted
   * scope under the same authority rules. It mounts no player and touches no server:
   * an expired or unreadable scope simply yields no port at the call site. */
  const bindOfflineDownloads=async(scope:LocalPlaybackScope,signal:AbortSignal)=>{
    if(life.signal.aborted)throw aborted();
    const active=AbortSignal.any([signal,life.signal]);
    const store=await DesktopDownloadStore.bind(localPlaybackScope(scope),()=>!active.aborted,options.bridge??nativeDownloadsBridge,active);
    offlineStores.add(store);
    // Only the rows this launch page last offered may be played; a row's exact revision
    // is rechecked by the native store when its preparation is committed.
    const playable=new Map<string,Readonly<{revision:number}>>();
    const done=()=>{playable.clear();offlineStores.delete(store);store.dispose();};
    return Object.freeze({
      /** Completed downloads that play without a check-in; a row needing one is left out. */
      async list(actionSignal:AbortSignal){
        playable.clear();
        const combined=AbortSignal.any([active,actionSignal]);
        const page=await store.list(null,combined);
        const rows=page.items.filter(row=>row.state==='ready'&&row.offlineAccess==='valid').map(row=>Object.freeze({
          id:row.id,itemId:row.itemId,revision:row.revision,quality:row.quality,bytes:row.bytes,
          title:row.title.title,artist:row.title.artist,kind:row.title.kind,onServer:row.title.onServer,
        }));
        for(const row of rows)playable.set(row.id,{revision:row.revision});
        return Object.freeze(rows);
      },
      /** Prepare exactly the offered row through the one app-owned broker. Ownership of
       * the returned resource passes to the caller, including on a later failure. */
      async prepare(id:string,actionSignal:AbortSignal,isCurrent:()=>boolean){
        const combined=AbortSignal.any([active,actionSignal]),offered=playable.get(id);
        if(!offered||combined.aborted||!isCurrent())throw new Error('download_not_ready');
        return broker.prepare(store,{kind:'entry',entryId:id,expectedRevision:offered.revision},{signal:combined,isCurrent:()=>!combined.aborted&&isCurrent()});
      },
      openFolder:(actionSignal:AbortSignal)=>store.openFolder(AbortSignal.any([active,actionSignal])),
      dispose:done,
    });
  };
  return Object.freeze({broker,attachLocalQueue,bindSaved,bindOfflineDownloads,async dispose(){
    life.abort();for(const dispose of [...bindings])dispose();
    for(const store of [...offlineStores])store.dispose();
    offlineStores.clear();
    for(const attachment of attachments.values())attachment.retire();attachments.clear();notify();listeners.clear();
    await broker.dispose();
  }});
}
