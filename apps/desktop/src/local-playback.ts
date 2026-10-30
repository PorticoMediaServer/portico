import {localPlayerSource,localPlayerLoadReceipt,type LocalPlayerSource,type LocalPlayerLoadContext,type LocalPlayerLoadReceipt} from '../../../packages/client-core/src/playback-v1/local-port.ts';
import {sameSavedDownloadsScope} from '../../../web/src/screens/saved/downloads-port.ts';
import type {DesktopDownloadStore,PreparedLocalQueue,LocalSelection} from './downloads.ts';

export interface DesktopLocalPlayback {
  load(source:LocalPlayerSource,nativeGeneration:number,context:LocalPlayerLoadContext):Promise<LocalPlayerLoadReceipt>;
  retire(nativeGeneration:number):Promise<void>;
}
type OwnedPreparation={store:DesktopDownloadStore;prepared:PreparedLocalQueue;retiring:boolean;nativeReleased:boolean;releaseTask?:Promise<void>};
export type DesktopPreparedLocalResource=Readonly<{preparation:PreparedLocalQueue;release():Promise<void>}>;
const cancelled=()=>new DOMException('Aborted','AbortError');

/** App-private custody of actual native preparations. This is not a second queue:
 * Web owns ordering and controls; native owns receipt validation and its journal. */
export class DesktopLocalPlaybackBroker implements DesktopLocalPlayback {
  private readonly preparations=new Map<string,OwnedPreparation>();
  private readonly loads=new Map<number,{store:DesktopDownloadStore;prepared:PreparedLocalQueue;retired:boolean}>();
  private disposed=false;
  retain(store:DesktopDownloadStore,prepared:PreparedLocalQueue):void {
    store.assertCurrent();
    if(this.disposed||!sameSavedDownloadsScope(store.scope,prepared.scope)||this.preparations.has(prepared.preparationId))throw new Error('downloads_conflict');
    this.preparations.set(prepared.preparationId,{store,prepared,retiring:false,nativeReleased:false});
  }
  async prepare(store:DesktopDownloadStore,selection:LocalSelection,context:LocalPlayerLoadContext):Promise<DesktopPreparedLocalResource>{
    const current=()=>!this.disposed&&!context.signal.aborted&&context.isCurrent();
    if(!current())throw cancelled();
    const prepared=await store.prepareLocalQueue(selection,context.signal);
    if(this.preparations.has(prepared.preparationId))throw new Error('downloads_conflict');
    // Keep custody before testing a late action fence so failed cleanup is retryable.
    this.preparations.set(prepared.preparationId,{store,prepared,retiring:false,nativeReleased:false});
    try{
      if(!current())throw cancelled();
      store.assertCurrent();
      // Web takes ownership of the returned resource, including retained bookmarks.
      // The initiating action signal must not retire it after a successful handoff.
      return Object.freeze({preparation:prepared,release:()=>this.release(prepared)});
    }catch(error){await this.release(prepared).catch(()=>{});throw error;}
  }
  async load(input:LocalPlayerSource,nativeGeneration:number,context:LocalPlayerLoadContext):Promise<LocalPlayerLoadReceipt>{
    const source=localPlayerSource(input),owned=this.preparations.get(source.preparationId);
    const member=owned?.prepared.items.find(item=>item.localSourceId===source.localSourceId);
    if(this.disposed||!owned||owned.retiring||!member||member.id!==source.entryId||member.itemId!==source.itemId||!sameSavedDownloadsScope(source.scope,owned.prepared.scope)||this.loads.has(nativeGeneration))throw new Error('downloads_conflict');
    const current=()=>!this.disposed&&!context.signal.aborted&&context.isCurrent()&&!owned.retiring&&this.preparations.get(source.preparationId)===owned;
    if(!current())throw cancelled();
    const load={...owned,retired:false};this.loads.set(nativeGeneration,load);
    try{
      const native=await owned.store.loadLocal(owned.prepared,source.localSourceId,nativeGeneration,source.startPositionMs,source.autoplay,context.signal);
      if(!current()||load.retired)throw cancelled();
      // Only the validated ACK for this exact private load can echo public generation.
      return localPlayerLoadReceipt({...native,generation:source.generation},source);
    }catch(error){
      await this.retire(nativeGeneration).catch(()=>{});
      throw error;
    }
  }
  async retire(nativeGeneration:number):Promise<void>{
    const load=this.loads.get(nativeGeneration);if(!load)return;
    load.retired=true;
    await load.store.retireLocal(nativeGeneration);
    if(this.loads.get(nativeGeneration)===load)this.loads.delete(nativeGeneration);
  }
  async release(prepared:PreparedLocalQueue):Promise<void>{
    const owned=this.preparations.get(prepared.preparationId);
    if(!owned||owned.prepared!==prepared)return;
    if(owned.releaseTask)return owned.releaseTask;
    owned.retiring=true;
    const task=(async()=>{
      const generations=[...this.loads].filter(([,load])=>load.prepared===prepared).map(([generation])=>generation);
      // Always retire the preparation too. A failed load retirement must neither
      // leak source handles nor discard the custody needed to retry that load.
      const results=await Promise.allSettled([
        ...generations.map(generation=>this.retire(generation)),
        ...(owned.nativeReleased?[]:[owned.store.releaseLocalQueue(prepared).then(()=>{owned.nativeReleased=true;})]),
      ]);
      if(results.some(result=>result.status==='rejected'))throw new Error('downloads_unavailable');
      if(this.preparations.get(prepared.preparationId)===owned)this.preparations.delete(prepared.preparationId);
    })();
    owned.releaseTask=task;
    try{await task;}finally{owned.releaseTask=undefined;}
  }
  async dispose():Promise<void>{
    this.disposed=true;
    const results=await Promise.allSettled([...this.preparations.values()].map(({prepared})=>this.release(prepared)));
    if(results.some(result=>result.status==='rejected'))throw new Error('downloads_unavailable');
  }
}
