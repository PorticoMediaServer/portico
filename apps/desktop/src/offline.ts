import {routePath} from '../../../packages/contracts/src/routes.ts';
import {parseDownloadPreparation,parseDeferredProgressReceipt,parseReceiptKeys,parseReceiptOutcomes,parseRevocationPage,type DownloadArtifact,type DownloadGrant,type DownloadPreparation,type DownloadBundle} from '../../../packages/client-core/src/downloads.ts';
import type {DownloadsApi,DownloadsService} from '../../../packages/client-core/src/downloads-service.ts';
import {downloadFailure,type DesktopDownloadStore,type LocalDownload} from './downloads.ts';

/** App-owned captured transport. Auth stays in the parent, never in the Saved UI port. */
export type DesktopOfflineTransport=Readonly<{
  store:DesktopDownloadStore;
  downloads:Pick<DownloadsService,'grant'|'bundle'>;
  api:DownloadsApi;
  resolvePublishedPath:(path:string)=>string;
  serverOrigin:string;
  /** Actual captured RouteConnection admission, never a hostname or discovery guess. */
  isPublishedOrigin?:(origin:string)=>boolean;
  fetcher:typeof fetch;
  /** Actual OS observation; unknown must not be treated as unmetered. */
  networkCost:()=> 'unmetered'|'metered'|'unknown';
  /** Actual native-cost producer; no preparation observer lives here. */
  subscribeNetworkCost?:(listener:()=>void)=>()=>void;
}>;
const chunkLimit=262144,artworkLimit=4194304;
function failed(code='download_verification_failed'):never{throw new Error(code);}
function sameArtifact(a:DownloadArtifact,b:DownloadArtifact):boolean{
  return !a.estimated&&!b.estimated&&a.sha256===b.sha256&&a.bytes===b.bytes&&a.kind===b.kind&&a.ref===b.ref;
}
function checkedGrant(grant:DownloadGrant,preparation:DownloadPreparation):DownloadGrant{
  if(grant.preparationId!==preparation.id||grant.itemId!==preparation.itemId||!sameArtifact(grant.artifact,preparation.artifact))failed();
  return grant;
}
function checkedBundle(bundle:DownloadBundle,preparation:DownloadPreparation):DownloadBundle{
  if(bundle.preparationId!==preparation.id||bundle.itemId!==preparation.itemId||bundle.quality!==preparation.quality)failed();
  return bundle;
}
function length(response:Response,expected:number):void{
  const value=response.headers.get('content-length');
  if(value!==null&&(!/^\d+$/.test(value)||Number(value)!==expected))failed();
}
function mediaResponse(response:Response,entry:LocalDownload):void{
  const etag=response.headers.get('etag');
  if(etag!==null&&etag!==`"sha256-${entry.sha256}"`)failed();
  if(response.headers.get('content-encoding')&&!/^identity$/i.test(response.headers.get('content-encoding')!))failed();
  if(entry.bytesDone>0||response.status===206){
    if(response.status!==206||response.headers.get('content-range')!==`bytes ${entry.bytesDone}-${entry.bytes-1}/${entry.bytes}`)failed();
  }else if(response.status!==200)failed('download_not_ready');
  length(response,entry.bytes-entry.bytesDone);
}

/** Transfers bytes into the one native store; it never owns a preparation list or UI state. */
export class DesktopOfflineCoordinator{
  private readonly lifetime=new AbortController();
  private readonly active=new Map<string,Readonly<{artifact:string;task:Promise<void>}>>();
  private pumping=false;
  private readonly origin:string;
  private readonly transport:DesktopOfflineTransport;
  constructor(transport:DesktopOfflineTransport){
    this.transport=transport;
    const origin=new URL(transport.serverOrigin);
    if(!['http:','https:'].includes(origin.protocol)||origin.username||origin.password||origin.search||origin.hash||origin.pathname!=='/')failed('invalid_command');
    this.origin=origin.origin;
  }
  private guard(signal:AbortSignal):void{
    if(this.lifetime.signal.aborted||signal.aborted)throw new DOMException('Aborted','AbortError');
    this.transport.store.assertCurrent(signal);
  }
  private address(path:string):string{
    const resolved=new URL(this.transport.resolvePublishedPath(path));
    if((resolved.origin!==this.origin&&this.transport.isPublishedOrigin?.(resolved.origin)!==true)||!['http:','https:'].includes(resolved.protocol)||resolved.username||resolved.password||resolved.hash)failed('invalid_command');
    return resolved.href;
  }
  private allowed(unmeteredOnly:boolean):boolean{return !unmeteredOnly||this.transport.networkCost()==='unmetered';}

  readonly saveReadyPreparation=async(preparation:DownloadPreparation,signal:AbortSignal):Promise<void>=>{
    this.guard(signal);
    if(preparation.profileId!==this.transport.store.scope.profileId||preparation.state!=='ready'||preparation.artifact.estimated||!preparation.artifact.bytes||!/^[a-f0-9]{64}$/.test(preparation.artifact.sha256))failed('download_not_ready');
    const artifact=JSON.stringify([preparation.artifact.kind,preparation.artifact.ref,preparation.artifact.sha256,preparation.artifact.bytes]);
    const previous=this.active.get(preparation.id);
    if(previous){
      if(previous.artifact!==artifact)failed('downloads_conflict');
      // A later observer shares the genuine operation without gaining cancellation
      // authority over the first owner's request. Viewer retirement aborts both.
      await this.wait(previous.task,signal);this.guard(signal);return;
    }
    const task=this.transfer(preparation,signal);
    this.active.set(preparation.id,{artifact,task});
    try{await task;}finally{if(this.active.get(preparation.id)?.task===task)this.active.delete(preparation.id);}
  };
  private wait(task:Promise<void>,signal:AbortSignal):Promise<void>{
    return new Promise((resolve,reject)=>{
      const abort=()=>{signal.removeEventListener('abort',abort);reject(new DOMException('Aborted','AbortError'));};
      signal.addEventListener('abort',abort,{once:true});
      if(signal.aborted){abort();return;}
      task.then(()=>{signal.removeEventListener('abort',abort);resolve();},error=>{signal.removeEventListener('abort',abort);reject(error);});
    });
  }
  /** Wake only already-persisted work. The Saved owner alone observes newly READY
   * server rows. Complete native pagination never turns into a second preparation list. */
  async resumeQueued(signal:AbortSignal):Promise<void>{
    this.guard(signal);if(this.pumping)return;this.pumping=true;
    try{
      const rules=await this.transport.store.rules(signal);this.guard(signal);
      if(!this.allowed(rules.rules.unmeteredOnly))return;
      let cursor:Awaited<ReturnType<DesktopDownloadStore['list']>>['nextCursor']=null;
      do{
        const page=await this.transport.store.list(cursor,signal);this.guard(signal);
        for(const entry of page.items){
          if(entry.held||this.active.has(entry.preparationId)||!(['queued','downloading'].includes(entry.state)||entry.state==='ready'&&entry.receiptExpiresMs===null))continue;
          if(!this.allowed(rules.rules.unmeteredOnly))return;
          const preparation=parseDownloadPreparation(await this.transport.api.request(routePath('get_download_preparation',{id:entry.preparationId}),'GET',undefined,signal));this.guard(signal);
          if(preparation.id!==entry.preparationId||preparation.profileId!==this.transport.store.scope.profileId||preparation.itemId!==entry.itemId||preparation.quality!==entry.quality)failed('download_verification_failed');
          if(preparation.state!=='ready')continue;
          if(preparation.artifact.sha256!==entry.sha256||preparation.artifact.bytes!==entry.bytes)failed('download_verification_failed');
          await this.saveReadyPreparation(preparation,signal);
        }
        cursor=page.nextCursor;
      }while(cursor!==null);
    }finally{this.pumping=false;}
  }
  private async transfer(preparation:DownloadPreparation,signal:AbortSignal):Promise<void>{
    this.guard(signal);
    const controller=new AbortController();
    const abort=()=>controller.abort();
    signal.addEventListener('abort',abort,{once:true});this.lifetime.signal.addEventListener('abort',abort,{once:true});
    const current=controller.signal;
    let costPaused=false,offCost:(()=>void)|undefined;
    let entry:LocalDownload|undefined,renewTimer:ReturnType<typeof setTimeout>|undefined,renewal:Promise<void>|undefined,renewError:unknown;
    try{
      const bundle=checkedBundle(await this.transport.downloads.bundle(preparation,current),preparation);this.guard(current);
      entry=await this.transport.store.enroll({preparationId:preparation.id,itemId:preparation.itemId,quality:preparation.quality,sha256:preparation.artifact.sha256,bytes:preparation.artifact.bytes,title:bundle.title},current);
      if(entry.held||entry.state==='failed')failed('downloads_conflict');
      const rules=await this.transport.store.rules(current);
      // A queued copy is genuine persisted work. No bytes start on an unknown/metered route.
      if(!this.allowed(rules.rules.unmeteredOnly))return;
      if(rules.rules.unmeteredOnly&&this.transport.subscribeNetworkCost){
        offCost=this.transport.subscribeNetworkCost(()=>{if(!this.allowed(true)){costPaused=true;controller.abort();}});
        if(!this.allowed(true))return;
      }
      let grant=checkedGrant(await this.transport.downloads.grant(preparation),preparation);this.guard(current);
      let renewing=true;
      const schedule=()=>{
        if(!renewing||current.aborted)return;
        renewTimer=setTimeout(()=>{
          renewal=(async()=>{
            const next=checkedGrant(await this.transport.downloads.grant(preparation,grant.token),preparation);this.guard(current);
            if(next.token!==grant.token||next.url!==grant.url||!next.renewed)failed();
            grant=next;schedule();
          })().catch(error=>{renewError=error;controller.abort();});
        },Math.min(grant.renewAfterSeconds*1000,2147483647));
      };
      schedule();
      try{
        if(entry.state!=='ready'){
          if(entry.bytesDone<entry.bytes){
            const response=await this.transport.fetcher(this.address(grant.url),{signal:current,credentials:'omit',redirect:'error',cache:'no-store',headers:entry.bytesDone?{Range:`bytes=${entry.bytesDone}-`,'If-Range':`"sha256-${entry.sha256}"`}:undefined});
            try{
              this.guard(current);mediaResponse(response,entry);
              if(!response.body)failed();
              const reader=response.body!.getReader();
              try{
                while(true){
                  this.guard(current);
                  if(!this.allowed(rules.rules.unmeteredOnly)){costPaused=true;controller.abort();throw new DOMException('Aborted','AbortError');}
                  const {done,value}=await reader.read();this.guard(current);if(done)break;
                  if(value.byteLength>entry.bytes-entry.bytesDone)failed();
                  for(let offset=0;offset<value.byteLength;offset+=chunkLimit){
                    this.guard(current);
                    entry=await this.transport.store.append(entry,value.subarray(offset,offset+chunkLimit),current);
                  }
                }
              }finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
            }catch(error){await response.body?.cancel().catch(()=>{});throw error;}
          }
          if(entry.bytesDone!==entry.bytes)failed();
          entry=await this.transport.store.seal(entry,current);
        }
        entry=await this.artwork(entry,bundle,grant,current);
      }finally{renewing=false;if(renewTimer)clearTimeout(renewTimer);}
      if(renewal)await renewal;
      if(renewError)throw renewError;
      this.guard(current);
      const keys=parseReceiptKeys(await this.transport.api.request(routePath('list_download_receipt_keys'),'GET',undefined,current));this.guard(current);
      for(const key of keys)await this.transport.store.installReceiptKey(key.id,key.publicKey,current);
      const outcomes=parseReceiptOutcomes(await this.transport.api.request(routePath('issue_download_receipts'),'POST',{operationId:crypto.randomUUID(),preparationIds:[preparation.id]},current));this.guard(current);
      if(outcomes.length!==1||outcomes[0].preparationId!==preparation.id||!outcomes[0].receipt)failed('invalid_receipt');
      await this.transport.store.saveReceipt(entry.id,outcomes[0].receipt,current);
      if(outcomes[0].titleOnServer!==null)await this.transport.store.serverPresence(entry,outcomes[0].titleOnServer,current);
    }catch(error){
      if(entry&&(costPaused||entry.state==='downloading')){
        // A native append may commit before cancellation discards its reply. Re-read
        // the actual revision; never acknowledge our stale pre-append row as queued.
        try{
          this.transport.store.assertCurrent();
          const actual=await this.transport.store.entry(entry.id);
          if(actual.state==='downloading'&&!actual.held)entry=await this.transport.store.hold(actual,false);
        }catch{
          if(costPaused){const actual=await this.transport.store.entry(entry.id,signal);if(actual.state==='downloading'&&!actual.held)failed('downloads_conflict');}
        }
      }
      if(costPaused){this.guard(signal);return;}
      if(renewError)throw downloadFailure(renewError);
      this.guard(current);throw downloadFailure(error);
    }finally{
      controller.abort();offCost?.();if(renewTimer)clearTimeout(renewTimer);
      signal.removeEventListener('abort',abort);this.lifetime.signal.removeEventListener('abort',abort);
    }
  }
  private async artwork(entry:LocalDownload,bundle:DownloadBundle,grant:DownloadGrant,signal:AbortSignal):Promise<LocalDownload>{
    const supported=bundle.artwork.filter(v=>['image/jpeg','image/png','image/webp'].includes(v.contentType));
    const picture=supported.find(v=>v.role==='poster')??supported[0];
    if(!picture)return entry;
    const response=await this.transport.fetcher(this.address(`${grant.url}/${picture.path}`),{signal,credentials:'omit',redirect:'error',cache:'no-store'});
    try{
      this.guard(signal);if(response.status===404)return entry;
      if(response.status!==200||!response.body||response.headers.get('content-type')?.split(';')[0].trim()!==picture.contentType)failed();
      const declared=response.headers.get('content-length');
      if(declared!==null&&(!/^\d+$/.test(declared)||Number(declared)>artworkLimit))failed();
      const bytes=new Uint8Array(artworkLimit),reader=response.body!.getReader();let used=0;
      try{while(true){const {done,value}=await reader.read();this.guard(signal);if(done)break;if(value.length>artworkLimit-used)failed();bytes.set(value,used);used+=value.length;}}
      finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
      if(!used||declared!==null&&Number(declared)!==used)failed();
      return await this.transport.store.saveArtwork(entry,picture.contentType,bytes.subarray(0,used),signal);
    }finally{await response.body?.cancel().catch(()=>{});}
  }
  async syncProgress(signal:AbortSignal):Promise<void>{
    const current=AbortSignal.any([signal,this.lifetime.signal]);this.guard(current);
    const plays=await this.transport.store.pendingPlays(current);if(!plays.length)return;
    const receipt=parseDeferredProgressReceipt(await this.transport.api.request(routePath('sync_download_progress'),'POST',{operationId:crypto.randomUUID(),entries:plays.map(v=>({playId:v.playId,itemId:v.itemId,positionMs:v.positionMs,ended:v.ended,observedAt:new Date(v.observedMs).toISOString()}))},current));this.guard(current);
    for(const result of receipt.entries){
      const original=plays.find(v=>v.playId===result.playId&&v.itemId===result.itemId&&v.observedMs===Date.parse(result.observedAt));
      if(original){try{await this.transport.store.acknowledgePlay(original,current);}catch(error){if(!(error instanceof Error)||error.message!=='downloads_conflict')throw error;}}
    }
  }
  async refreshRevocations(signal:AbortSignal):Promise<void>{
    const current=AbortSignal.any([signal,this.lifetime.signal]);this.guard(current);
    let cursor='';const seen=new Set<string>();
    do{
      const page=parseRevocationPage(await this.transport.api.request(`${routePath('list_download_revocations')}${cursor?`?cursor=${encodeURIComponent(cursor)}`:''}`,'GET',undefined,current));this.guard(current);
      for(const revoked of page.items)if(revoked.profileId===this.transport.store.scope.profileId)await this.transport.store.revoke(revoked.receiptId,current);
      if(page.nextCursor&&seen.has(page.nextCursor))failed();
      cursor=page.nextCursor;seen.add(cursor);
    }while(cursor);
  }
  dispose():void{this.lifetime.abort();}
}
