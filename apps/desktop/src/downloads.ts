import {invoke} from '@tauri-apps/api/core';
import {sameSavedDownloadsScope,type SavedLocalDownloadGroup,type SavedLocalGroupSelector,type SavedOtherDownloadsConfirmation} from '../../../web/src/screens/saved/downloads-port.ts';
import {parseDownloadTitle, type DownloadTitle, type DownloadReceipt} from '../../../packages/client-core/src/downloads.ts';

// App-owned transport. UI owners consume the canonical web port after its MAIN landing.
export type DownloadScope = Readonly<{authority:'local'|'hosted';serverId:string;accountId:string;profileId:string;sessionFamilyId:string}>;
export type LocalDownload = Readonly<{id:string;revision:number;preparationId:string;itemId:string;quality:string;sha256:string;bytes:number;bytesDone:number;state:'queued'|'downloading'|'paused'|'failed'|'ready';held:boolean;addedMs:number;receiptExpiresMs:number|null;offlineAccess:'valid'|'needsCheckIn';title:DownloadTitle}>;
export type DownloadRules = Readonly<{removeWatched:boolean;nextEpisode:boolean;unmeteredOnly:boolean;limitBytes:number}>;
export type RulesSnapshot = Readonly<{revision:number;rules:DownloadRules}>;
export type DownloadCursor = Readonly<{addedMs:number;id:string}>;
export type DownloadPage = Readonly<{items:readonly LocalDownload[];nextCursor:DownloadCursor|null}>;
export type Capacity = Readonly<{totalBytes:number;freeBytes:number;usedBytes:number;otherDownloadsBytes:number}>;
export type PendingPlay = Readonly<{playId:string;itemId:string;positionMs:number;ended:boolean;observedMs:number}>;
export type LocalSelection = Readonly<{kind:'entry';entryId:string;expectedRevision:number}> | Readonly<{kind:'group';selector:SavedLocalGroupSelector;expectedRevision:string}>;
export type PreparedLocalQueue = Readonly<{
  preparationId:string;scope:DownloadScope;selectionRevision:string;
  items:readonly Readonly<{id:string;revision:number;itemId:string;title:DownloadTitle;durationMs:number;localSourceId:string}>[];
}>;
/** App-private bridge acknowledgement. This generation is native, never the public
 * player generation. The MediaPort adapter remaps it only for its exact live load. */
export type NativeLocalLoadReceipt=Readonly<{
  kind:'local';scope:DownloadScope;preparationId:string;localSourceId:string;generation:number;
  sessionId:string;playId:string;itemId:string;entryId:string;artifact:Readonly<{sha256:string;bytes:number}>;
}>;
export type NativeLocalLoadRequest=Readonly<{preparationId:string;localSourceId:string;generation:number;startPositionMs:number;autoplay:boolean}>;
export type NativeDownloadsBridge = Readonly<{
  bind(scope:DownloadScope):Promise<string>;
  release(lease:string):Promise<void>;
  command(lease:string,operation:Readonly<Record<string,unknown>>):Promise<unknown>;
  loadLocal?(lease:string,source:NativeLocalLoadRequest):Promise<unknown>;
  retireLocal?(lease:string,generation:number):Promise<void>;
  folder?:Readonly<{
    location(lease:string):Promise<unknown>;begin(lease:string):Promise<unknown>;
    choose(token:string):Promise<void>;open(token:string):Promise<void>;cancel(token:string):Promise<void>;
  }>;
}>;
export const nativeDownloadsBridge:NativeDownloadsBridge = Object.freeze({
  bind:scope=>invoke<string>('desktop_downloads_bind',{scope}),
  release:lease=>invoke<void>('desktop_downloads_release',{lease}),
  command:(lease,operation)=>invoke<unknown>('desktop_downloads_command',{lease,operation}),
  loadLocal:(lease,source)=>invoke<unknown>('desktop_load_local',{lease,source}),
  retireLocal:(lease,generation)=>invoke<void>('desktop_retire_local',{lease,generation}),
  folder:{
    location:lease=>invoke<unknown>('desktop_download_folder_location',{lease}),
    begin:lease=>invoke<unknown>('desktop_download_folder_begin',{lease}),
    choose:token=>invoke<void>('desktop_download_folder_choose',{token}),
    open:token=>invoke<void>('desktop_download_folder_open',{token}),
    cancel:token=>invoke<void>('desktop_download_folder_cancel',{token}),
  },
});
const fixedCodes = new Set(['downloads_unavailable','downloads_conflict','download_not_ready','download_verification_failed','invalid_receipt','storage_full','invalid_command','forbidden_origin','unsupported']);
export function downloadFailure(error:unknown):Error {
  const code=typeof error==='string'?error:error instanceof Error?error.message:'';
  return new Error(fixedCodes.has(code)?code:'downloads_unavailable');
}
function invalid():never {throw new Error('downloads_unavailable');}
function object(value:unknown):Record<string,unknown>{if(!value||typeof value!=='object'||Array.isArray(value))return invalid();return value as Record<string,unknown>;}
function integer(value:unknown):number{if(typeof value!=='number'||!Number.isSafeInteger(value)||value<0)return invalid();return value;}
function text(value:unknown,max=1024):string{if(typeof value!=='string'||value.length>max||/[\x00-\x1f\x7f]/.test(value))return invalid();return value;}
function id(value:unknown):string{const v=text(value,160);if(!/^[A-Za-z0-9_-]+$/.test(v))return invalid();return v;}
function digest(value:unknown):string{const v=text(value,64);if(!/^[a-f0-9]{64}$/.test(v))return invalid();return v;}
function boolean(value:unknown):boolean{if(typeof value!=='boolean')return invalid();return value;}
export function parseLocalDownload(value:unknown):LocalDownload {
  const v=object(value),state=text(v.state);
  if(!['queued','downloading','paused','failed','ready'].includes(state)||!['valid','needsCheckIn'].includes(String(v.offlineAccess)))return invalid();
  const bytes=integer(v.bytes),bytesDone=integer(v.bytesDone);
  if(!bytes||bytesDone>bytes)return invalid();
  return Object.freeze({id:digest(v.id),revision:integer(v.revision),preparationId:id(v.preparationId),itemId:id(v.itemId),quality:id(v.quality),sha256:digest(v.sha256),bytes,bytesDone,state:state as LocalDownload['state'],held:boolean(v.held),addedMs:integer(v.addedMs),receiptExpiresMs:v.receiptExpiresMs===null?null:integer(v.receiptExpiresMs),offlineAccess:v.offlineAccess as LocalDownload['offlineAccess'],title:parseDownloadTitle(v.title)});
}
function rules(value:unknown):RulesSnapshot{
  const v=object(value),r=object(v.rules);
  return Object.freeze({revision:integer(v.revision),rules:Object.freeze({removeWatched:boolean(r.removeWatched),nextEpisode:boolean(r.nextEpisode),unmeteredOnly:boolean(r.unmeteredOnly),limitBytes:integer(r.limitBytes)})});
}
function page(value:unknown):DownloadPage{
  const v=object(value);
  if(!Array.isArray(v.items)||v.items.length>200)return invalid();
  const cursor=v.nextCursor===null?null:object(v.nextCursor);
  return Object.freeze({items:Object.freeze(v.items.map(parseLocalDownload)),nextCursor:cursor?Object.freeze({addedMs:integer(cursor.addedMs),id:digest(cursor.id)}):null});
}
function capacity(value:unknown):Capacity{
  const v=object(value),totalBytes=integer(v.totalBytes),freeBytes=integer(v.freeBytes);
  if(freeBytes>totalBytes)return invalid();
  return Object.freeze({totalBytes,freeBytes,usedBytes:integer(v.usedBytes),otherDownloadsBytes:integer(v.otherDownloadsBytes)});
}
function selector(value:SavedLocalGroupSelector):SavedLocalGroupSelector{
  if(!['songs','episodes'].includes(value.kind))return invalid();
  return Object.freeze({kind:value.kind,parentId:id(value.parentId),...(value.groupId!==undefined?{groupId:value.groupId===''?'':id(value.groupId)}:{})});
}
function groups(value:unknown):readonly SavedLocalDownloadGroup[]{
  if(!Array.isArray(value))return invalid();
  return Object.freeze(value.map(raw=>{
    const v=object(raw);if(!['songs','episodes'].includes(String(v.kind)))return invalid();
    const bytes=integer(v.bytes),bytesDone=integer(v.bytesDone),count=integer(v.count);
    if(!count||bytesDone>bytes)return invalid();
    const optional:{-readonly [K in keyof SavedLocalDownloadGroup]?:SavedLocalDownloadGroup[K]}={};
    if(v.parentTitle!==undefined){const title=text(v.parentTitle);if(!title.trim())return invalid();optional.parentTitle=title;}
    if(v.artist!==undefined){const artist=text(v.artist);if(!artist.trim())return invalid();optional.artist=artist;}
    if(v.quality!==undefined)optional.quality=id(v.quality);
    if(v.state!==undefined){if(!['queued','downloading','paused','failed','ready'].includes(String(v.state)))return invalid();optional.state=v.state as LocalDownload['state'];}
    if(v.held!==undefined)optional.held=boolean(v.held);
    if(v.artworkId!==undefined)optional.artworkId=digest(v.artworkId);
    if(v.offlineAccess!==undefined){if(!['valid','needsCheckIn'].includes(String(v.offlineAccess)))return invalid();optional.offlineAccess=v.offlineAccess as 'valid'|'needsCheckIn';}
    return Object.freeze({kind:v.kind as 'songs'|'episodes',parentId:id(v.parentId),groupId:v.groupId===''?'':id(v.groupId),count,bytes,bytesDone,revision:digest(v.revision),...optional});
  }));
}
function aborted():never{throw new DOMException('Aborted','AbortError');}
function checkScope(scope:DownloadScope):DownloadScope{
  if(!['local','hosted'].includes(scope.authority))return invalid();
  return Object.freeze({authority:scope.authority,serverId:id(scope.serverId),accountId:id(scope.accountId),profileId:id(scope.profileId),sessionFamilyId:id(scope.sessionFamilyId)});
}

/** A captured viewer/family owns one native lease. Late results are never republished. */
export class DesktopDownloadStore {
  readonly scope:DownloadScope;
  private lease='';
  private disposed=false;
  private readonly isCurrent:()=>boolean;
  private readonly bridge:NativeDownloadsBridge;
  private currentPreparation?:PreparedLocalQueue;
  private readonly localLeases=new Map<number,string>();
  private readonly localRetirements=new Map<number,Promise<void>>();
  private readonly localLoads=new Map<number,Promise<unknown>>();

  private readonly listeners=new Set<()=>void>();
  private readonly folderCancels=new Set<()=>void>();
  private readonly releasedPreparations=new WeakSet<PreparedLocalQueue>();
  private readonly preparations=new WeakMap<PreparedLocalQueue,{lease:string;retired:boolean}>();
  private constructor(scope:DownloadScope,isCurrent:()=>boolean,bridge:NativeDownloadsBridge){this.scope=checkScope(scope);this.isCurrent=isCurrent;this.bridge=bridge;}
  static async bind(scope:DownloadScope,isCurrent:()=>boolean,bridge:NativeDownloadsBridge=nativeDownloadsBridge,signal?:AbortSignal):Promise<DesktopDownloadStore>{
    const store=new DesktopDownloadStore(scope,isCurrent,bridge);store.guard(signal);
    try{store.lease=digest(await bridge.bind(store.scope));store.guard(signal);return store;}
    catch(error){store.dispose();if(signal?.aborted||!isCurrent())return aborted();throw downloadFailure(error);}
  }
  private guard(signal?:AbortSignal){if(this.disposed||!this.isCurrent()||signal?.aborted)aborted();}
  assertCurrent(signal?:AbortSignal):void{this.guard(signal);}
  subscribe(listener:()=>void):()=>void{this.guard();this.listeners.add(listener);return()=>{this.listeners.delete(listener);};}
  async folderLocation(signal:AbortSignal):Promise<Readonly<{displayPath:string}>>{
    this.guard(signal);const folder=this.bridge.folder;if(!folder)throw new Error('unsupported');
    try{
      const value=object(await folder.location(this.lease));this.guard(signal);const displayPath=text(value.displayPath,32768);
      if(!displayPath)return invalid();return Object.freeze({displayPath});
    }catch(error){this.guard(signal);throw downloadFailure(error);}
  }
  private async folderAction(action:'choose'|'open',signal:AbortSignal):Promise<void>{
    this.guard(signal);const folder=this.bridge.folder;if(!folder)throw new Error('unsupported');
    let token:string|undefined,cancelled=false;
    const cancel=()=>{cancelled=true;if(token)void folder.cancel(token).catch(()=>{});};
    this.folderCancels.add(cancel);signal.addEventListener('abort',cancel,{once:true});
    try{
      token=digest(await folder.begin(this.lease));this.guard(signal);if(cancelled)aborted();
      await folder[action](token);this.guard(signal);if(cancelled)aborted();
      if(action==='choose')for(const listener of [...this.listeners]){try{listener();}catch{/* Committed native pointer remains authoritative. */}}
      this.guard(signal);
    }catch(error){if(token)await folder.cancel(token).catch(()=>{});this.guard(signal);throw downloadFailure(error);}
    finally{signal.removeEventListener('abort',cancel);this.folderCancels.delete(cancel);}
  }
  chooseFolder(signal:AbortSignal){return this.folderAction('choose',signal);}
  openFolder(signal:AbortSignal){return this.folderAction('open',signal);}
  async prepareLocalQueue(input:LocalSelection,signal:AbortSignal):Promise<PreparedLocalQueue>{
    this.guard(signal);
    const selection=input.kind==='entry'?{kind:'entry',entryId:digest(input.entryId),expectedRevision:integer(input.expectedRevision)}:
      input.kind==='group'?{kind:'group',selector:selector(input.selector),expectedRevision:digest(input.expectedRevision)}:invalid();
    const lease=this.lease;
    let preparationId:string|undefined;
    try{
      const reply=object(await this.bridge.command(lease,{action:'prepareLocalQueue',selection}));
      if(reply.kind!=='localQueue')return invalid();
      const value=object(reply.value);preparationId=digest(value.preparationId);
      const scope=checkScope(object(value.scope) as unknown as DownloadScope);
      if(!sameSavedDownloadsScope(this.scope,scope))return invalid();
      const selectionRevision=text(value.selectionRevision);
      if(selectionRevision!==String(input.expectedRevision)||!Array.isArray(value.items)||!value.items.length)return invalid();
      const ids=new Set<string>(),sources=new Set<string>();
      const items=value.items.map(raw=>{
        const row=object(raw),entryId=digest(row.id),localSourceId=digest(row.localSourceId),durationMs=integer(row.durationMs),title=parseDownloadTitle(row.title);
        if(ids.has(entryId)||sources.has(localSourceId)||Math.round(title.durationSeconds*1000)!==durationMs)return invalid();
        ids.add(entryId);sources.add(localSourceId);
        if(input.kind==='entry'&&(value.items as unknown[]).length!==1)return invalid();
        if(input.kind==='entry'&&(entryId!==input.entryId||row.revision!==input.expectedRevision))return invalid();
        if(input.kind==='group'&&(title.parentId!==input.selector.parentId||input.selector.groupId!==undefined&&title.groupId!==input.selector.groupId||!(input.selector.kind==='songs'?['song','track']:['episode']).includes(title.kind)))return invalid();
        return Object.freeze({id:entryId,revision:integer(row.revision),itemId:id(row.itemId),title,durationMs,localSourceId});
      });
      const prepared=Object.freeze({preparationId,scope,selectionRevision,items:Object.freeze(items)});
      this.guard(signal);
      this.preparations.set(prepared,{lease,retired:false});this.currentPreparation=prepared;
      return prepared;
    }catch(error){
      // Cancellation can arrive after native committed the preparation. Retire that
      // exact opaque result using its original lease, even after this store disposed.
      if(preparationId)await this.bridge.command(lease,{action:'releaseLocalQueue',preparationId}).catch(()=>{});
      this.guard(signal);throw downloadFailure(error);
    }
  }
  async releaseLocalQueue(prepared:PreparedLocalQueue):Promise<void>{
    const owned=this.preparations.get(prepared);
    if(!owned){if(this.releasedPreparations.has(prepared))return;throw new Error('downloads_conflict');}
    owned.retired=true;
    if(this.currentPreparation===prepared)this.currentPreparation=undefined;
    try{
      const reply=object(await this.bridge.command(owned.lease,{action:'releaseLocalQueue',preparationId:prepared.preparationId}));
      if(reply.kind!=='done')return invalid();
      this.releasedPreparations.add(prepared);this.preparations.delete(prepared);
    }catch(error){throw downloadFailure(error);}
  }
  async loadLocal(prepared:PreparedLocalQueue,localSourceId:string,generation:number,startPositionMs:number,autoplay:boolean,signal:AbortSignal):Promise<NativeLocalLoadReceipt>{
    this.guard(signal);
    const owned=this.preparations.get(prepared),member=prepared.items.find(item=>item.localSourceId===localSourceId);
    if(!owned||owned.retired||this.currentPreparation!==prepared||!member||!sameSavedDownloadsScope(prepared.scope,this.scope))throw new Error('downloads_conflict');
    if(!this.bridge.loadLocal||!this.bridge.retireLocal)throw new Error('unsupported');
    if(integer(generation)===0||!Number.isFinite(startPositionMs)||startPositionMs<0||startPositionMs>1e12||typeof autoplay!=='boolean')throw new Error('invalid_command');
    if(this.localLeases.has(generation))throw new Error('downloads_conflict');
    this.localLeases.set(generation,owned.lease);
    try{
      const pending=this.bridge.loadLocal(owned.lease,{preparationId:prepared.preparationId,localSourceId,generation,startPositionMs,autoplay});
      this.localLoads.set(generation,pending);
      const raw=object(await pending);
      const scope=checkScope(object(raw.scope) as unknown as DownloadScope),artifact=object(raw.artifact),entryId=digest(raw.entryId),sessionId=text(raw.sessionId,256);
      const suffix=sessionId.startsWith(`local:${entryId}:`)?sessionId.slice(`local:${entryId}:`.length):'';
      if(raw.kind!=='local'||!sameSavedDownloadsScope(scope,this.scope)||raw.preparationId!==prepared.preparationId||raw.localSourceId!==localSourceId||raw.generation!==generation
        ||entryId!==member.id||raw.itemId!==member.itemId||!/^[1-9][0-9]{0,15}$/.test(suffix)||!Number.isSafeInteger(Number(suffix))||integer(artifact.bytes)===0)return invalid();
      const receipt=Object.freeze({kind:'local' as const,scope,preparationId:prepared.preparationId,localSourceId,generation,sessionId,playId:digest(raw.playId),itemId:member.itemId,entryId,artifact:Object.freeze({sha256:digest(artifact.sha256),bytes:integer(artifact.bytes)})});
      this.guard(signal);
      if(owned.retired||this.currentPreparation!==prepared)throw new Error('downloads_conflict');
      return receipt;
    }catch(error){
      // The native command may already have committed before cancellation or an
      // invalid/late reply. Retire only that exact private generation and lease.
      await this.retireLocal(generation).catch(()=>{});this.guard(signal);throw downloadFailure(error);
    }
  }
  async retireLocal(generation:number):Promise<void>{
    if(integer(generation)===0)throw new Error('invalid_command');
    const lease=this.localLeases.get(generation);if(!lease)return;
    const pending=this.localRetirements.get(generation);if(pending)return pending;
    if(!this.bridge.retireLocal)throw new Error('unsupported');
    // A failed acknowledgement retains this exact lease, even after dispose. Never
    // derive a cleanup target from the current viewer or an unknown generation.
    const task=(async()=>{
      try{
        // Stop must follow the actual load ACK/failure. An early stop ACK cannot
        // prove that a still-running native load will not commit afterwards.
        await this.localLoads.get(generation)?.catch(()=>{});
        await this.bridge.retireLocal!(lease,generation);
        this.localLeases.delete(generation);this.localLoads.delete(generation);
      }
      catch(error){throw downloadFailure(error);}
    })();
    this.localRetirements.set(generation,task);
    try{await task;}finally{this.localRetirements.delete(generation);}
  }
  async request<T>(operation:Readonly<Record<string,unknown>>,kind:string,parse:(value:unknown)=>T,signal?:AbortSignal):Promise<T>{
    this.guard(signal);
    try{
      const raw=await this.bridge.command(this.lease,operation);this.guard(signal);
      const reply=object(raw);if(reply.kind!==kind)return invalid();
      const value=parse(reply.value);
      if(['setRules','enroll','append','seal','hold','retry','remove','groupAction','removeOtherProfiles','receipt','revoke','serverPresence','saveArtwork'].includes(String(operation.action)))for(const listener of [...this.listeners]){try{listener();}catch{/* A view cannot invalidate a committed native acknowledgement. */}}
      this.guard(signal);
      return value;
    }catch(error){this.guard(signal);throw downloadFailure(error);}
  }
  list(cursor:DownloadCursor|null=null,signal?:AbortSignal){return this.request({action:'list',cursor,limit:100},'page',page,signal);}
  entry(entryId:string,signal?:AbortSignal){return this.request({action:'entry',id:digest(entryId)},'entry',parseLocalDownload,signal);}
  capacity(signal?:AbortSignal){return this.request({action:'capacity'},'capacity',capacity,signal);}
  groups(signal?:AbortSignal){return this.request({action:'groups'},'groups',groups,signal);}
  actionGroup(group:SavedLocalGroupSelector,expectedRevision:string,operation:'remove'|'pause'|'resume'|'retry',signal?:AbortSignal){
    if(!['remove','pause','resume','retry'].includes(operation))return Promise.reject(new Error('invalid_command'));
    return this.request({action:'groupAction',selector:selector(group),expectedRevision:digest(expectedRevision),operation},'done',()=>undefined,signal);
  }
  previewOtherProfiles(signal?:AbortSignal):Promise<SavedOtherDownloadsConfirmation>{
    return this.request({action:'previewOtherProfiles'},'otherProfiles',value=>{const v=object(value);return Object.freeze({scope:this.scope,snapshotToken:digest(v.snapshotToken),bytes:integer(v.bytes)});},signal);
  }
  removeOtherProfiles(confirmation:SavedOtherDownloadsConfirmation,signal?:AbortSignal){
    if(!sameSavedDownloadsScope(this.scope,confirmation.scope))return Promise.reject(new Error('downloads_conflict'));
    return this.request({action:'removeOtherProfiles',snapshotToken:digest(confirmation.snapshotToken)},'done',()=>undefined,signal);
  }
  rules(signal?:AbortSignal){return this.request({action:'rules'},'rules',rules,signal);}
  setRules(expectedRevision:number,value:DownloadRules,signal?:AbortSignal){return this.request({action:'setRules',expectedRevision:integer(expectedRevision),value},'rules',rules,signal);}
  enroll(input:Readonly<{preparationId:string;itemId:string;quality:string;sha256:string;bytes:number;title:DownloadTitle}>,signal?:AbortSignal){
    const {durationSeconds,...title}=input.title;
    return this.request({action:'enroll',...input,title:{...title,durationMs:Math.round(durationSeconds*1000)}},'entry',parseLocalDownload,signal);
  }
  append(entry:Pick<LocalDownload,'id'|'revision'|'bytesDone'>,bytes:Uint8Array,signal?:AbortSignal){
    if(!bytes.byteLength||bytes.byteLength>262144)return Promise.reject(new Error('invalid_command'));
    return this.request({action:'append',id:entry.id,expectedRevision:entry.revision,offset:entry.bytesDone,bytes:Array.from(bytes)},'entry',parseLocalDownload,signal);
  }
  seal(entry:Pick<LocalDownload,'id'|'revision'>,signal?:AbortSignal){return this.request({action:'seal',id:entry.id,expectedRevision:entry.revision},'entry',parseLocalDownload,signal);}
  hold(entry:Pick<LocalDownload,'id'|'revision'>,held:boolean,signal?:AbortSignal){return this.request({action:'hold',id:entry.id,expectedRevision:entry.revision,held},'entry',parseLocalDownload,signal);}
  retry(entry:Pick<LocalDownload,'id'|'revision'>,signal?:AbortSignal){return this.request({action:'retry',id:entry.id,expectedRevision:entry.revision},'entry',parseLocalDownload,signal);}
  remove(entry:Pick<LocalDownload,'id'|'revision'>,signal?:AbortSignal){return this.request({action:'remove',id:entry.id,expectedRevision:entry.revision},'done',()=>undefined,signal);}
  async getArtwork(entryId:string,signal?:AbortSignal):Promise<Blob|null>{
    return this.request({action:'artwork',id:digest(entryId)},'artwork',value=>{
      if(value===null)return null;const v=object(value),mime=text(v.mime,64);
      if(!['image/png','image/jpeg','image/webp'].includes(mime)||!Array.isArray(v.bytes)||v.bytes.length>4194304||!v.bytes.length||!v.bytes.every(b=>typeof b==='number'&&Number.isInteger(b)&&b>=0&&b<=255))return invalid();
      return new Blob([Uint8Array.from(v.bytes)],{type:mime});
    },signal);
  }
  saveArtwork(entry:Pick<LocalDownload,'id'|'revision'>,mime:string,bytes:Uint8Array,signal?:AbortSignal){
    if(bytes.byteLength>4194304)return Promise.reject(new Error('invalid_command'));
    return this.request({action:'saveArtwork',id:entry.id,expectedRevision:entry.revision,mime,bytes:Array.from(bytes)},'entry',parseLocalDownload,signal);
  }
  installReceiptKey(keyId:string,publicKey:string,signal?:AbortSignal){return this.request({action:'receiptKey',keyId,publicKey},'done',()=>undefined,signal);}
  saveReceipt(entryId:string,receipt:DownloadReceipt,signal?:AbortSignal){
    const {receiptId,keyId,algorithm,payload,signature,revision}=receipt;
    return this.request({action:'receipt',id:digest(entryId),envelope:{receiptId,keyId,algorithm,payload,signature,revision}},'done',()=>undefined,signal);
  }
  revoke(receiptId:string,signal?:AbortSignal){return this.request({action:'revoke',receiptId:id(receiptId)},'done',()=>undefined,signal);}
  serverPresence(entry:Pick<LocalDownload,'id'|'revision'>,onServer:boolean,signal?:AbortSignal){return this.request({action:'serverPresence',id:entry.id,expectedRevision:entry.revision,onServer},'entry',parseLocalDownload,signal);}
  pendingPlays(signal?:AbortSignal):Promise<readonly PendingPlay[]>{return this.request({action:'pendingPlays'},'pendingPlays',value=>{
    if(!Array.isArray(value)||value.length>100)return invalid();
    return Object.freeze(value.map(raw=>{const v=object(raw);return Object.freeze({playId:id(v.playId),itemId:id(v.itemId),positionMs:integer(v.positionMs),ended:boolean(v.ended),observedMs:integer(v.observedMs)});}));
  },signal);}
  acknowledgePlay(play:Pick<PendingPlay,'playId'|'observedMs'>,signal?:AbortSignal){return this.request({action:'acknowledgePlay',playId:id(play.playId),observedMs:integer(play.observedMs)},'done',()=>undefined,signal);}
  dispose(){if(this.disposed)return;this.disposed=true;for(const cancel of this.folderCancels)cancel();this.folderCancels.clear();this.listeners.clear();const lease=this.lease;this.lease='';
    if(!lease)return;
    const prepared=this.currentPreparation;
    const release=()=>prepared?this.releaseLocalQueue(prepared).catch(()=>{}).then(()=>this.bridge.release(lease)):this.bridge.release(lease);
    if(this.localLeases.size)void Promise.allSettled([...this.localLeases.keys()].map(generation=>this.retireLocal(generation))).then(release).catch(()=>{});
    else void release().catch(()=>{});
  }
}
