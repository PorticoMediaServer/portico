import type {ExpectedRemoteSource} from './remote-source-selection.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
/** Local/mounted inventory administration. No provider credentials, persistent
 * path cache, automatic command replay, or client-side identity heuristics. */
import type {ContentScope, LibraryContentApi} from './library-content.ts';
import {firstOfEach,keepReadable,noteSkipped} from './tolerant.ts';
import {durationIntent,wireDurations} from './wire-units.ts';
import type {ServerEvent} from './playback-v1/types.ts';

export type ScanTier='file_list_only'|'basic'|'complete'|'custom';
export const scanOperations=['probe','local_metadata','subtitles','checksum','chapter_images','trickplay','waveform','loudness','fingerprint'] as const;
export type ScanOperation=typeof scanOperations[number];
export type ScanPolicy=Readonly<{tier:ScanTier;revision:number;operations:readonly ScanOperation[]}>;
export type InventorySource=Readonly<{id:string;libraryId:string;kind:string;name:string;path:string;resolvedPath:string;classification:'local'|'network';generation:number;health:string;enabled:boolean;followSymlinks:boolean;identityConfirmed:boolean;intervalSeconds:number;nextScanAt:string;lastCompleteAt:string;lastProgressAt:string}>;
export type SourceSettings=Readonly<{expectedRemoteSource?:ExpectedRemoteSource;name:string;path:string;classification:'local'|'network';followSymlinks:boolean;intervalSeconds:number;expectedRevision:number;acceptReplacement:boolean;/** false: attach without the automatic scan. */scanAfter?:boolean}>;
export type InventoryConfig=Readonly<{scope:Readonly<{serverId:string;viewerFence:string}>;libraryId:string;revision:number;sources:readonly InventorySource[];policy:ScanPolicy}>;
export type InventoryObject=Readonly<{id:string;sourceId:string;assetId:string;itemId:string;title:string;relativePath:string;revision:string;state:string;size:number;modifiedNs:number;missingSince:string;analysisState:string;analysisError:string;unsupportedReason:string}>;
export type InventoryFilter=Readonly<{sourceId?:string;state?:''|'available'|'missing'|'unsupported'}>;
export type InventoryObjects=Readonly<{scope:Readonly<{serverId:string;viewerFence:string}>;libraryId:string;sourceId:string;revision:number;items:readonly InventoryObject[];nextCursor:string}>;
export type InventoryError=Readonly<{code:string;message:string}>;
export type LibraryInventorySnapshot=Readonly<{config:InventoryConfig|null;objects:InventoryObjects|null;loading:boolean;error:InventoryError|null;filter:InventoryFilter;cursor:string;canPrevious:boolean;mutation:'idle'|'pending'|'complete'|'error'|'ambiguous'}>;
const record=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=8192):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&!!v;
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const list=(v:unknown,max:number):unknown[]=>{if(!Array.isArray(v)||v.length>max)throw new Error('Invalid inventory response.');return v;};
function assert(condition:unknown):asserts condition {if(!condition)throw new Error('Invalid inventory response.');}
function scope(raw:unknown,expected:ContentScope){assert(record(raw)&&raw.serverId===expected.serverId&&id(raw.viewerFence));return Object.freeze({serverId:expected.serverId,viewerFence:raw.viewerFence});}
function errorOf(e:unknown):InventoryError {return Object.freeze({code:record(e)&&id(e.code)?e.code:'request_failed',message:e instanceof Error?e.message:'Inventory request failed.'});}
export function parseInventorySource(wire:unknown,libraryId:string):InventorySource{const v:unknown=wireDurations(wire,{intervalSeconds:'intervalMs'});assert(record(v)&&id(v.id)&&v.libraryId===libraryId&&text(v.kind,64)&&text(v.name,512)&&text(v.path)&&text(v.resolvedPath)&&['local','network'].includes(String(v.classification))&&integer(v.generation)&&text(v.health,128)&&typeof v.enabled==='boolean'&&typeof v.followSymlinks==='boolean'&&typeof v.identityConfirmed==='boolean'&&integer(v.intervalSeconds)&&text(v.nextScanAt,128)&&text(v.lastCompleteAt,128)&&text(v.lastProgressAt,128));return Object.freeze({...v}) as InventorySource;}
function parseConfig(raw:unknown,bound:ContentScope,libraryId:string):InventoryConfig {
 assert(record(raw)&&raw.libraryId===libraryId&&integer(raw.revision)&&raw.revision>0&&record(raw.policy));
 const policy=raw.policy;assert(['file_list_only','basic','complete','custom'].includes(String(policy.tier))&&integer(policy.revision));
 // The scan policy is sent back when the owner changes it, so its tier stays exact; a step this app does not know is left out of what it shows, each known step once.
 const given=list(policy.operations,64),operations=[...new Set(given.filter(v=>scanOperations.includes(v as ScanOperation)))];if(operations.length!==given.length)noteSkipped('library-inventory','policy.operations',given.length-operations.length);
 // A folder that cannot be read is left out alone, and a repeated one keeps its first row.
 const sources=firstOfEach('library-inventory','sources',keepReadable('library-inventory','sources',list(raw.sources,64),wire=>parseInventorySource(wire,libraryId)),s=>s.id);
 return Object.freeze({scope:scope(raw.scope,bound),libraryId,revision:raw.revision,sources:Object.freeze(sources),policy:Object.freeze({tier:policy.tier as ScanTier,revision:policy.revision,operations:Object.freeze(operations as ScanOperation[])})});
}
function parseObjects(raw:unknown,bound:ContentScope,libraryId:string,filter:InventoryFilter):InventoryObjects {
 assert(record(raw)&&raw.libraryId===libraryId&&raw.sourceId===(filter.sourceId??'')&&integer(raw.revision)&&text(raw.nextCursor,4096));
 // A file that cannot be read is left out alone, and a repeated one keeps its first row.
 const items=firstOfEach('library-inventory','items',keepReadable('library-inventory','items',list(raw.items,40),v=>{assert(record(v)&&id(v.id)&&id(v.sourceId)&&id(v.assetId)&&text(v.itemId,256)&&text(v.title,4096)&&text(v.relativePath)&&id(v.revision)&&text(v.state,128)&&integer(v.size)&&typeof v.modifiedNs==='number'&&Number.isFinite(v.modifiedNs)&&text(v.missingSince,128)&&text(v.analysisState,128)&&text(v.analysisError,512)&&text(v.unsupportedReason,512));if(filter.sourceId)assert(v.sourceId===filter.sourceId);return Object.freeze({...v}) as InventoryObject;}),i=>i.id);
 return Object.freeze({scope:scope(raw.scope,bound),libraryId,sourceId:raw.sourceId as string,revision:raw.revision,items:Object.freeze(items),nextCursor:raw.nextCursor});
}

export class LibraryInventoryService {
 private state:LibraryInventorySnapshot=Object.freeze({config:null,objects:null,loading:false,error:null,filter:Object.freeze({}),cursor:'',canPrevious:false,mutation:'idle'});
 private listeners=new Set<()=>void>();private disposed=false;private epoch=0;private reads=0;private controllers=new Set<AbortController>();private history:string[]=[];private fence?:string;private commandBusy=false;
 private readonly api:LibraryContentApi;private readonly bound:ContentScope;readonly libraryId:string;
 constructor(input:{api:LibraryContentApi;scope:ContentScope;libraryId:string}){assert(id(input.libraryId)&&id(input.scope.serverId)&&text(input.scope.viewerId,1024)&&input.scope.viewerId);this.api=input.api;this.bound=Object.freeze({...input.scope});this.libraryId=input.libraryId;}
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>this.listeners.delete(listener);};getSnapshot=()=>this.state;
 private publish(update:Partial<LibraryInventorySnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...update});for(const listener of this.listeners)listener();}
 dispose(){this.epoch++;this.disposed=true;for(const c of this.controllers)c.abort();this.controllers.clear();this.listeners.clear();this.state=Object.freeze({...this.state,config:null,objects:null});}
 private base(){return encodedRoutePath("get_admin_libraries_id", {"id": encodeURIComponent(this.libraryId)});}
 private async request(path:string,method='GET',body?:unknown){if(this.disposed)throw new Error('Inventory view is closed.');const c=new AbortController();this.controllers.add(c);let timer:ReturnType<typeof setTimeout>|undefined;
  try{return await Promise.race([this.api.request<unknown>(path,method,body,c.signal),new Promise<never>((_,reject)=>{timer=setTimeout(()=>{c.abort();reject(new Error('Inventory response interrupted. Check current state before issuing the command again.'));},15000);})]);}finally{if(timer)clearTimeout(timer);this.controllers.delete(c);}
 }
 private acceptFence(value:string){if(this.fence&&this.fence!==value){this.publish({config:null,objects:null});throw new Error('Owner session changed. Reopen library management.');}this.fence=value;}
 async refresh(){if(this.disposed)return;const epoch=this.epoch,seq=++this.reads;this.publish({loading:true,error:null});const filter=this.state.filter,cursor=this.state.cursor;
  try {const params=new URLSearchParams({limit:'40'});if(cursor)params.set('cursor',cursor);if(filter.sourceId)params.set('sourceId',filter.sourceId);if(filter.state)params.set('state',filter.state);
   const [a,b]=await Promise.all([this.request(this.base()+'/inventory-config'),this.request(this.base()+'/inventory?'+params)]);
   if(this.disposed||epoch!==this.epoch||seq!==this.reads)return;
   const config=parseConfig(a,this.bound,this.libraryId),objects=parseObjects(b,this.bound,this.libraryId,filter);assert(config.scope.viewerFence===objects.scope.viewerFence&&config.revision===objects.revision);this.acceptFence(config.scope.viewerFence);
   if(objects.nextCursor&&(objects.nextCursor===cursor||this.history.includes(objects.nextCursor)))throw new Error('Invalid inventory continuation.');
   this.publish({config,objects,loading:false,error:null,mutation:this.commandBusy?'pending':'idle'});
  }catch(e){if(this.disposed||epoch!==this.epoch||seq!==this.reads)return;const error=errorOf(e);if(['unauthorized','forbidden'].includes(error.code)){this.epoch++;for(const c of this.controllers)c.abort();this.publish({config:null,objects:null});}this.publish({loading:false,error});}
 }
 async loadObjects(filter:InventoryFilter={}){assert(!filter.sourceId||id(filter.sourceId));assert(!filter.state||['available','missing','unsupported'].includes(filter.state));this.history=[];this.publish({filter:Object.freeze({...filter}),cursor:'',canPrevious:false,objects:null});await this.refresh();}
 async next(){if(this.state.loading||!this.state.objects?.nextCursor)return;this.history.push(this.state.cursor);if(this.history.length>64)this.history.shift();this.publish({cursor:this.state.objects.nextCursor,canPrevious:true});await this.refresh();}
 async previous(){if(this.state.loading||!this.history.length)return;this.publish({cursor:this.history.pop()!,canPrevious:this.history.length>0});await this.refresh();}
 async reconcile(){this.history=[];this.publish({cursor:'',canPrevious:false});await this.refresh();}
 private config(){if(!this.state.config||this.state.loading)throw new Error('Load current library configuration first.');return this.state.config;}
 private async command(path:string,method:string,body?:unknown):Promise<boolean>{this.config();if(this.commandBusy||this.state.mutation==='ambiguous')throw new Error('Check the current state before issuing another command.');this.commandBusy=true;const epoch=this.epoch;this.publish({mutation:'pending',error:null});
  try{await this.request(this.base()+path,method,body);if(this.disposed||epoch!==this.epoch)return false;await this.reconcile();if(this.disposed||epoch!==this.epoch)return false;this.publish({mutation:'complete'});return true;
  }catch(e){if(this.disposed||epoch!==this.epoch)return false;const error=errorOf(e);const definite=['invalid_request','source_busy','source_changed','source_already_configured','library_configuration_conflict','stale_continuation','not_found','forbidden','unauthorized'].includes(error.code);if(['unauthorized','forbidden'].includes(error.code)){this.epoch++;for(const c of this.controllers)c.abort();this.publish({config:null,objects:null,loading:false});}this.publish({error,mutation:definite?'error':'ambiguous'});return false;}finally{this.commandBusy=false;}
 }
 saveSource(sourceId:string|undefined,input:SourceSettings){assert(!sourceId||id(sourceId));assert(text(input.name,100)&&text(input.path)&&input.path&&integer(input.expectedRevision)&&input.expectedRevision>0&&integer(input.intervalSeconds));return this.command('/sources'+(sourceId?'/'+encodeURIComponent(sourceId):''),sourceId?'PATCH':'POST',durationIntent(input,{intervalSeconds:'intervalMs'}));}
 removeSource(sourceId:string,expectedRevision:number){assert(id(sourceId)&&integer(expectedRevision));return this.command('/sources/'+encodeURIComponent(sourceId),'DELETE',{expectedRevision});}
 scanSource(sourceId:string){assert(id(sourceId));return this.command('/sources/'+encodeURIComponent(sourceId)+'/scans','POST');}
 checkSource(sourceId:string){assert(id(sourceId));return this.command('/sources/'+encodeURIComponent(sourceId)+'/check','POST');}
 setPolicy(policy:{tier:ScanTier;operations:readonly ScanOperation[];expectedRevision:number}){assert(integer(policy.expectedRevision)&&['file_list_only','basic','complete','custom'].includes(policy.tier));return this.command('/scan-policy','PUT',policy);}
 /** `associate`: this file is another version of `itemId`. `separate`: this file is not a version of the title it was grouped with; it becomes a title of its own. Either decision is remembered across scans. */
 objectAction(object:InventoryObject,action:'associate'|'separate',itemId?:string){assert(this.state.objects?.items.some(v=>v.id===object.id&&v.revision===object.revision));assert(action!=='associate'||id(itemId));return this.command('/inventory/'+encodeURIComponent(object.id)+'/'+action,'POST',{revision:object.revision,...(itemId?{itemId}:{})});}
}

export type InventorySourceStatus=Readonly<{id:string;health:string;lastCompleteAt:string;jobId:string;status:string;phase:string;discovered:number;analyzed:number;warnings:number;pauseReason:string;deferredChanges?:boolean;deferredScanScope?:'folders'|'source'}>;
export type ViewerInventoryStatus=Readonly<{libraryId:string;revision:number;sources:readonly InventorySourceStatus[]}>;
export type ScanProgress=Readonly<{scanning:boolean;found:number}>;
/** Header/sidebar indicator input from the viewer-safe inventory status: active while a source is queued or running, with `found` summing discovered files. */
export function scanProgress(status:ViewerInventoryStatus|null|undefined):ScanProgress{
 const active=status?.sources.filter(s=>s.status==='queued'||s.status==='running')??[];
 if(!active.length)return Object.freeze({scanning:false,found:0});
 return Object.freeze({scanning:true,found:active.reduce((n,s)=>n+s.discovered,0)});
}
/** Callers own polling lifetime and cancel on server/viewer/library transitions. */
export async function fetchInventoryStatus(api:LibraryContentApi,serverId:string,libraryId:string,signal:AbortSignal):Promise<ViewerInventoryStatus>{
 const raw=await api.request<RouteResponse<"get_libraries_id_inventory_status">>(encodedRoutePath("get_libraries_id_inventory_status", {"id": encodeURIComponent(libraryId)}),'GET',undefined,signal);
 assert(!signal.aborted&&record(raw)&&raw.serverId===serverId&&record(raw.inventory));const v=raw.inventory;assert(v.libraryId===libraryId&&integer(v.revision));
 const sources=list(v.sources,64).map(s=>{assert(record(s)&&id(s.id)&&text(s.health,128)&&text(s.lastCompleteAt,128)&&text(s.jobId,256)&&text(s.status,128)&&text(s.phase,128)&&integer(s.discovered)&&integer(s.analyzed)&&integer(s.warnings)&&text(s.pauseReason,128)&&(s.deferredChanges===undefined||typeof s.deferredChanges==='boolean')&&(s.deferredScanScope===undefined||s.deferredScanScope==='folders'||s.deferredScanScope==='source')&&(s.deferredScanScope===undefined||s.deferredChanges===true));return Object.freeze({...(s.deferredChanges?{deferredChanges:true,...(s.deferredScanScope?{deferredScanScope:s.deferredScanScope as 'folders'|'source'}:{})}:{}),id:s.id,health:s.health,lastCompleteAt:s.lastCompleteAt,jobId:s.jobId,status:s.status,phase:s.phase,discovered:s.discovered,analyzed:s.analyzed,warnings:s.warnings,pauseReason:s.pauseReason});});
 return Object.freeze({libraryId,revision:v.revision,sources:Object.freeze(sources)});
}
// TODO(contracts): use the generated LibraryScanEventData once packages/contracts reaches remediation/frontend
export type LibraryScanEvent=Readonly<{libraryId:string;revision:number;state:'started'|'progress'|'finished';found:number}>;
/** Tolerant scan-event reader (W5): unknown types, states and malformed envelopes are skipped (undefined), never thrown. */
export function parseLibraryScanEvent(event:ServerEvent):LibraryScanEvent|undefined{
 try{
  if(!event||event.type!=='library.scan.updated')return undefined;
  const resource=event.resource;
  if(!resource||resource.kind!=='library'||typeof resource.id!=='string'||!resource.id)return undefined;
  if(typeof event.revision!=='string'||!/^[0-9]+$/.test(event.revision))return undefined;
  const revision=Number(event.revision);
  if(!Number.isSafeInteger(revision)||revision<0)return undefined;
  const data=event.data;
  if(!data||typeof data!=='object'||Array.isArray(data))return undefined;
  const state=(data as Record<string,unknown>).state;
  if(state!=='started'&&state!=='progress'&&state!=='finished')return undefined;
  const found=(data as Record<string,unknown>).found;
  if(typeof found!=='number'||!Number.isSafeInteger(found)||found<0)return undefined;
  return Object.freeze({libraryId:resource.id,revision,state,found});
 }catch{return undefined;}
}
/**
 * The one place both apps keep scan state (MU2): pure, no timers, no event
 * client. `read` is `fetchInventoryStatus` + `scanProgress`, exactly as the
 * polling hooks did. Reads run one at a time (never a burst for a long
 * sidebar); listeners fire only when a library's visible state changes.
 */
export class LibraryScanTracker{
 private readonly read:(libraryId:string,signal:AbortSignal)=>Promise<ScanProgress>;
 private watchedOrder:string[]=[];private watchedSet=new Set<string>();
 private states=new Map<string,ScanProgress>();private revisions=new Map<string,number>();
 private finishedCounts=new Map<string,number>();private epochs=new Map<string,number>();
 private controllers=new Map<string,AbortController>();
 private listeners=new Set<()=>void>();private version=0;
 private queue:Promise<void>=Promise.resolve();private disposed=false;
 constructor(o:{read:(libraryId:string,signal:AbortSignal)=>Promise<ScanProgress>}){this.read=o.read;}
 /** The libraries on screen now; reads each NEW one once (sequentially), forgets the rest (aborting their read). */
 watch(libraryIds:readonly string[]):void{
  if(this.disposed)return;
  const next=[...new Set(libraryIds.filter(id=>typeof id==='string'&&id))];
  const nextSet=new Set(next);
  if(nextSet.size===this.watchedSet.size&&next.every(id=>this.watchedSet.has(id)))return;
  const removed=[...this.watchedSet].filter(id=>!nextSet.has(id));
  const added=next.filter(id=>!this.watchedSet.has(id));
  this.watchedOrder=next;this.watchedSet=nextSet;
  for(const id of removed){
   this.controllers.get(id)?.abort();this.controllers.delete(id);
   this.states.delete(id);this.revisions.delete(id);this.finishedCounts.delete(id);this.epochs.delete(id);
  }
  for(const id of added)this.readOne(id);
 }
 /** Ignored for unwatched libraries; a lower revision than the last applied one is ignored (equal applies). */
 apply(event:LibraryScanEvent):void{
  if(this.disposed)return;
  const id=event.libraryId;
  if(!this.watchedSet.has(id))return;
  const last=this.revisions.get(id);
  if(last!==undefined&&event.revision<last)return;
  this.revisions.set(id,event.revision);
  this.epochs.set(id,(this.epochs.get(id)??0)+1);
  if(event.state==='finished'){
   this.finishedCounts.set(id,(this.finishedCounts.get(id)??0)+1);
   this.states.set(id,Object.freeze({scanning:false,found:0}));
   this.bump();
   return;
  }
  const next=Object.freeze({scanning:true,found:event.found});
  const prev=this.states.get(id);
  if(prev&&prev.scanning===true&&prev.found===next.found)return;
  this.states.set(id,next);
  this.bump();
 }
 /** Re-read every watched library once (sequentially). */
 resync():void{
  if(this.disposed)return;
  for(const id of this.watchedOrder)this.readOne(id);
 }
 get(libraryId:string):ScanProgress{
  return this.states.get(libraryId)??Object.freeze({scanning:false,found:0});
 }
 /** Increments on each applied 'finished' event for that library (screens refresh when it changes). */
 finished(libraryId:string):number{
  return this.finishedCounts.get(libraryId)??0;
 }
 getSnapshot=():number=>this.version;
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 dispose():void{
  this.disposed=true;
  for(const c of this.controllers.values())c.abort();
  this.controllers.clear();this.listeners.clear();
 }
 private bump(){this.version++;for(const l of [...this.listeners])l();}
 private readOne(libraryId:string):void{
  const run=async()=>{
   if(this.disposed||!this.watchedSet.has(libraryId))return;
   const controller=new AbortController();
   this.controllers.set(libraryId,controller);
   const epoch=this.epochs.get(libraryId)??0;
   try{
    const next=await this.read(libraryId,controller.signal);
    if(this.disposed||controller.signal.aborted||!this.watchedSet.has(libraryId))return;
    if((this.epochs.get(libraryId)??0)!==epoch)return;
    const frozen=Object.freeze({scanning:next.scanning,found:next.found});
    const prev=this.states.get(libraryId);
    if(prev&&prev.scanning===frozen.scanning&&prev.found===frozen.found)return;
    this.states.set(libraryId,frozen);
    this.bump();
   }catch{/* A failed read leaves the state unchanged (silent, as before). */}
   finally{if(this.controllers.get(libraryId)===controller)this.controllers.delete(libraryId);}
  };
  this.queue=this.queue.then(run,run);
 }
}
export function inventoryStatusMessage(value:ViewerInventoryStatus):string {
 const offline=value.sources.some(s=>['offline','root_changed'].includes(s.health));
 const active=value.sources.filter(s=>['queued','running'].includes(s.status));
 if(active.length){const discovered=active.reduce((n,s)=>n+s.discovered,0),analyzed=active.reduce((n,s)=>n+s.analyzed,0);return `${offline?'Some sources are unavailable. ':''}Library scan: ${discovered} discovered, ${analyzed} analyzed.${active.some(s=>s.pauseReason==='playback')?' Analysis is waiting for playback.':''}`;}
 if(offline)return 'Some library sources are unavailable. Available versions can still play.';
 if(value.sources.some(s=>s.status==='paused'))return 'Library scan paused. Already discovered media remains available.';
 if(value.sources.some(s=>s.status==='failed'))return 'A scan could not finish. Existing library entries were kept.';
 if(value.sources.some(s=>s.status==='complete_with_warnings'))return 'Scan completed with warnings. Some media may have limited details.';
 if(value.sources.every(s=>s.status==='not_scanned'))return 'This library has not been scanned yet.';
 return '';
}

/** Settings needs the bounded source/configuration snapshot, without inventory rows. */
export async function readLibraryInventoryConfig(api:LibraryContentApi,bound:ContentScope,libraryId:string,signal?:AbortSignal):Promise<InventoryConfig>{
 assert(id(libraryId));
 return parseConfig(await api.request<unknown>(encodedRoutePath('get_admin_libraries_id_inventory_config',{id:encodeURIComponent(libraryId)}),'GET',undefined,signal),bound,libraryId);
}
