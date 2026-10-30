import type {QuietRefreshOutcome} from './freshness-actions.ts';
import {expectedRemoteSource,type ExpectedRemoteSource} from './remote-source-selection.ts';
import {parseLibrarySummary} from './library-summary.ts';
import type {LibrarySummary} from './library-summary.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';
/** Owner-only library administration. Source paths stay in scoped memory; commands are never auto-replayed. */
import type {ContentScope,LibraryContentApi} from './library-content.ts';
export type LibraryJobStatus='queued'|'running'|'paused'|'complete'|'complete_with_warnings'|'failed'|'cancelled';
export type LibraryAdminJob=Readonly<{id:string;libraryId:string;status:LibraryJobStatus;processed:number;sourceId?:string;phase?:string;analyzed?:number;warnings?:number;pauseReason?:string;createdAt:string;updatedAt:string|null;finishedAt:string|null;error?:string;errorCode?:'source_unavailable';actions:readonly ('pause'|'resume'|'cancel'|'retry')[]}>;
export type ManagedLibrary=Readonly<{id:string;name:string;position?:number;kind:'movie'|'tv'|'anime'|'music'|'audiobook'|'book'|'comic';summary?:LibrarySummary;audience?:Readonly<{everyone:boolean;accounts:number}>;filesSkipped?:number;revision:number;source:Readonly<{path:string;availability:'unknown'|'available'|'unavailable';basis:'not_checked'|'probe';observedAt?:string;message?:string}>;lastScan:LibraryAdminJob|null;actions:readonly ('rename'|'scan'|'remove')[];
 /** Files the scanner couldn't place (episode naming issues): `atLeast` when there are more than the server counts (1,000). Absent when none. */
 attention?:Readonly<{files:number;atLeast:boolean}>}>;
/** One file the scanner couldn't place into a show, season and episode (`GET /v1/libraries/{id}/episode-issues`). */
export type EpisodeIssue=Readonly<{assetId:string;sourceName:string;code:string}>;
export type EpisodeIssuePage=Readonly<{issues:readonly EpisodeIssue[];nextCursor:string}>;
export type LibraryManagementError=Readonly<{code:string;message:string;retryable:boolean}>;
export type AdminPage<T>=Readonly<{scope:Readonly<{serverId:string;viewerFence:string}>;revision:number;items:readonly T[];nextCursor:string}>;
export type AdminRead<T>=Readonly<{phase:'idle'|'loading'|'ready'|'error'|'refresh-required';data:T|null;error:LibraryManagementError|null;cursor:string|null;canPrevious:boolean}>;
export type LibraryJobFilter=Readonly<{libraryId?:string;status?:LibraryJobStatus}>;
export type LibraryManagementSnapshot=Readonly<{generation:number;scope:ContentScope;selectedLibraryId:string|null;directory:AdminRead<AdminPage<ManagedLibrary>>;selected:AdminRead<Readonly<{scope:Readonly<{serverId:string;viewerFence:string}>;library:ManagedLibrary}>>;jobs:AdminRead<AdminPage<LibraryAdminJob>>;jobFilter:LibraryJobFilter;mutation:Readonly<{phase:'idle'|'pending'|'complete'|'error'|'ambiguous';kind:'create'|'rename'|'scan'|'cancel'|null;error:LibraryManagementError|null}>}>;
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&v.length>0;
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const statuses:readonly string[]=['queued','running','paused','complete','complete_with_warnings','failed','cancelled'];
class Failure extends Error{code:string;retryable:boolean;constructor(code:string,message:string,retryable=false){super(message);this.code=code;this.retryable=retryable;}}
function invalid():never{throw new Failure('invalid_management',unreadableServerResponse);}
function rows(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
/** An action this app does not know is not offered, and a repeated one is offered once. The list stays short. */
function actions<T extends string>(v:unknown,allowed:readonly T[]):readonly T[]{const values=rows(v,16),known=[...new Set(values.filter((a):a is T=>allowed.includes(a as T)))];if(known.length!==values.length)noteSkipped('library-management','actions',values.length-known.length);return Object.freeze(known);}
function parseJob(v:unknown,libraryId?:string):LibraryAdminJob{if(!object(v)||!id(v.id)||!id(v.libraryId)||libraryId!==undefined&&v.libraryId!==libraryId||!statuses.includes(v.status as string)||!integer(v.processed)||!text(v.createdAt,128)||v.updatedAt!==null&&!text(v.updatedAt,128)||v.finishedAt!==null&&!text(v.finishedAt,128)||v.error!==undefined&&!text(v.error))invalid();
 // A failure code this app does not know is left off, and a finished scan is never offered Cancel.
 const errorCode=v.errorCode==='source_unavailable'&&v.status==='failed'?v.errorCode:undefined;if(v.errorCode!==undefined&&errorCode===undefined)noteSkipped('library-management','job.errorCode');
 const offered=actions(v.actions,['pause','resume','cancel','retry'] as const),a=['queued','running','paused'].includes(v.status as string)?offered:Object.freeze(offered.filter(action=>action!=='cancel'));return Object.freeze({id:v.id,libraryId:v.libraryId,status:v.status as LibraryJobStatus,processed:v.processed,...(id(v.sourceId)?{sourceId:v.sourceId}:{}),...(text(v.phase,128)?{phase:v.phase}:{}),...(integer(v.analyzed)?{analyzed:v.analyzed}:{}),...(integer(v.warnings)?{warnings:v.warnings}:{}),...(text(v.pauseReason,128)?{pauseReason:v.pauseReason}:{}),createdAt:v.createdAt,updatedAt:v.updatedAt as string|null,finishedAt:v.finishedAt as string|null,...(v.error===undefined?{}:{error:v.error as string}),...(errorCode===undefined?{}:{errorCode}),actions:a});}
function parseLibrary(v:unknown):ManagedLibrary{if(!object(v)||!id(v.id)||!text(v.name,512)||!['movie','tv','anime','music','audiobook','book','comic'].includes(v.kind as string)||!integer(v.revision)||v.revision<1||!object(v.source))invalid();const s=v.source;if(!text(s.path,8192)||s.observedAt!==undefined&&!text(s.observedAt,128)||s.message!==undefined&&!text(s.message))invalid();
 // A folder state this app does not know reads as not yet known, and a last scan it cannot read is left off the library.
 const lastScan=v.lastScan===null?null:keepReadable('library-management','library.lastScan',[v.lastScan],job=>parseJob(job,v.id as string))[0]??null;
 return Object.freeze({id:v.id,name:v.name,kind:v.kind as ManagedLibrary['kind'],revision:v.revision,...(integer(v.position)?{position:v.position}:{}),source:Object.freeze({path:s.path,availability:oneOfOr('library-management','library.source.availability',s.availability,['unknown','available','unavailable'] as const,'unknown'),basis:oneOfOr('library-management','library.source.basis',s.basis,['not_checked','probe'] as const,'not_checked'),...(s.observedAt===undefined?{}:{observedAt:s.observedAt as string}),...(s.message===undefined?{}:{message:s.message as string})}),lastScan,actions:actions(v.actions,['rename','scan','remove'] as const),...attention(v.attention),...(v.summary===undefined?{}:{summary:parseLibrarySummary(v.summary)}),...(object(v.audience)&&typeof v.audience.everyone==='boolean'&&integer(v.audience.accounts)?{audience:Object.freeze({everyone:v.audience.everyone,accounts:v.audience.accounts})}:{}),...(integer(v.filesSkipped)?{filesSkipped:v.filesSkipped}:{})});}
/** Tolerant: an unreadable attention block is left out rather than failing the library. */
function attention(v:unknown):{attention?:ManagedLibrary['attention']}{if(!object(v)||!integer(v.files)||v.files<1)return {};return {attention:Object.freeze({files:v.files,atLeast:v.atLeast===true})};}
/** A page of the files that need attention in one library, owner only. Unreadable rows are skipped. */
export async function fetchEpisodeIssues(api:LibraryContentApi,libraryId:string,cursor='',signal?:AbortSignal):Promise<EpisodeIssuePage>{
 const q=new URLSearchParams({limit:'40'});if(cursor)q.set('cursor',cursor);
 const raw=await api.request<RouteResponse<"get_libraries_id_episode_issues">>(encodedRoutePath("get_libraries_id_episode_issues", {"id": encodeURIComponent(libraryId)}) + "?" + (q),'GET',undefined,signal);
 if(!object(raw)||!Array.isArray(raw.issues)||raw.issues.length>100||typeof raw.nextCursor!=='string')invalid();
 const issues=raw.issues.filter((x:unknown):x is Record<string,unknown>=>object(x)&&id(x.assetId)&&text(x.sourceName,8192)&&text(x.code,256)).map(x=>Object.freeze({assetId:x.assetId as string,sourceName:x.sourceName as string,code:x.code as string}));
 return Object.freeze({issues:Object.freeze(issues),nextCursor:raw.nextCursor});
}
function parseScope(v:unknown,s:ContentScope){if(!object(v)||v.serverId!==s.serverId||!id(v.viewerFence))invalid();return Object.freeze({serverId:s.serverId,viewerFence:v.viewerFence});}
function errorInfo(e:unknown):LibraryManagementError{const v=e as {code?:unknown;retryable?:unknown};return Object.freeze({code:id(v?.code)?v.code:'request_failed',message:e instanceof Error?e.message:'Library management request failed.',retryable:v?.retryable===true});}
/** The server says the same request may simply be repeated: `retry: same_request`, or a 5xx/429 that says nothing else. */
function sameRequest(e:unknown):boolean{const v=e as {status?:unknown;retry?:unknown;retryable?:unknown;code?:unknown}|null;if(!v||v.code==='timeout'||v.code==='cancelled')return false;if(typeof v.retry==='string')return v.retry==='same_request';const status=typeof v.status==='number'?v.status:0;return status===429||status>=500&&v.retryable!==false;}
function blank<T=never>():AdminRead<T>{return Object.freeze({phase:'idle',data:null,error:null,cursor:null,canPrevious:false});}
function checkScope(s:ContentScope):ContentScope{if(!object(s)||!id(s.serverId)||!text(s.viewerId,1024)||!s.viewerId)throw new Error('Library management requires a bound owner API and viewer.');return Object.freeze({...s});}
type Lane='directory'|'selected'|'jobs';type PageOperation={cursor:string|null;history:readonly (string|null)[];/** Zero-based logical page, independent of the bounded Back history. */pageIndex?:number;filter?:LibraryJobFilter;fence?:{revision:number;viewerFence:string}};
export class LibraryManagementService{
 private readRetries:number;private retryBaseMs:number;private api:LibraryContentApi;private scope:ContentScope;private pageSize:number;private timeoutMs:number;private epoch=0;private generation=0;private disposed=false;private listeners=new Set<()=>void>();private controls:Partial<Record<Lane,AbortController>>={};private sequences:Record<Lane,number>={directory:0,selected:0,jobs:0};private operations:Partial<Record<'directory'|'jobs',PageOperation>>={};private mutationController?:AbortController;private mutationPromise?:Promise<void>;private mutationKey='';private viewerFence?:string;private state:LibraryManagementSnapshot;
 constructor(o:{api:LibraryContentApi;scope:ContentScope;pageSize?:number;timeoutMs?:number;readRetries?:number;retryBaseMs?:number}){this.readRetries=o.readRetries??3;this.retryBaseMs=o.retryBaseMs??1000;this.api=o.api;this.scope=checkScope(o.scope);this.pageSize=o.pageSize??40;this.timeoutMs=o.timeoutMs??15000;if(!Number.isInteger(this.pageSize)||this.pageSize<1||this.pageSize>40||!Number.isFinite(this.timeoutMs)||this.timeoutMs<1||this.timeoutMs>60000)throw new Error('Invalid management request bounds.');this.state=this.empty();}
 private quietDirectory?:AbortController;
 /** Cancels only the mounted quiet directory read; ordinary paging and command owners stay intact. */
 invalidateQuietDirectory=():void=>{const c=this.quietDirectory;this.quietDirectory=undefined;c?.abort();if(c&&this.controls.directory===c)delete this.controls.directory;};
 getSnapshot=()=>this.state;subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>this.listeners.delete(listener);};
 private empty():LibraryManagementSnapshot{return Object.freeze({generation:this.generation,scope:this.scope,selectedLibraryId:null,directory:blank(),selected:blank(),jobs:blank(),jobFilter:Object.freeze({}),mutation:Object.freeze({phase:'idle',kind:null,error:null})});}
 private active(){if(this.disposed)throw new Error('Library management service is disposed.');}
 private publish(p:Partial<LibraryManagementSnapshot>){this.state=Object.freeze({...this.state,...p,generation:++this.generation});for(const fn of this.listeners)fn();}
 private fence(value:string){if(this.viewerFence&&this.viewerFence!==value)throw new Failure('permission_changed','The owner session changed. Reopen library management.');this.viewerFence=value;}
 private denied(e:unknown){const v=e as {status?:unknown;code?:unknown};return v?.status===401||v?.status===403||['unauthorized','forbidden','permission_changed','session_revoked'].includes(String(v?.code));}
 private stop(){this.epoch++;for(const c of Object.values(this.controls))c?.abort();this.mutationController?.abort();this.mutationPromise=undefined;this.mutationKey='';this.operations={};this.viewerFence=undefined;}
 cancel(){this.active();this.stop();this.publish(this.empty());}
 setScope(scope:ContentScope,api:LibraryContentApi){this.active();const next=checkScope(scope);this.stop();this.scope=next;this.api=api;this.publish(this.empty());}
 dispose(){this.stop();this.disposed=true;this.listeners.clear();}
 loadLibraries():Promise<void>{this.active();return this.page('directory',{cursor:null,history:[],pageIndex:0});}
 nextLibraries(){return this.turn('directory',true);}previousLibraries(){return this.turn('directory',false);}
 loadJobs(filter:LibraryJobFilter={}):Promise<void>{this.active();if(!object(filter)||filter.libraryId!==undefined&&!id(filter.libraryId)||filter.status!==undefined&&!statuses.includes(filter.status))throw new Error('Invalid job filter.');const checked=Object.freeze({...filter});this.publish({jobFilter:checked});return this.page('jobs',{cursor:null,history:[],filter:checked});}
 nextJobs(){return this.turn('jobs',true);}previousJobs(){return this.turn('jobs',false);}
 async selectLibrary(libraryId:string){this.active();if(!id(libraryId))throw new Error('Invalid library ID.');this.controls.selected?.abort();this.controls.jobs?.abort();this.mutationController?.abort();this.mutationController=undefined;this.mutationPromise=undefined;this.mutationKey='';this.publish({selectedLibraryId:libraryId,selected:blank(),jobs:blank(),mutation:Object.freeze({phase:'idle',kind:null,error:null})});await Promise.all([this.resource(libraryId),this.loadJobs({libraryId})]);}
 private turn(lane:'directory'|'jobs',next:boolean):Promise<void>{this.active();const read=this.state[lane],op=this.operations[lane];if(read.phase!=='ready'||!read.data||!op)return Promise.resolve();const history=[...op.history];let cursor:string|null;if(next){if(!read.data.nextCursor)return Promise.resolve();cursor=read.data.nextCursor;history.push(op.cursor);if(history.length>64)history.shift();}else{if(!history.length)return Promise.resolve();cursor=history.pop()!;}return this.page(lane,{...op,cursor,history,pageIndex:(op.pageIndex??0)+(next?1:-1),fence:{revision:read.data.revision,viewerFence:read.data.scope.viewerFence}});}
 private async read<T>(lane:Lane,path:string,parse:(raw:unknown)=>T,position:{cursor:string|null;canPrevious:boolean}={cursor:null,canPrevious:false}){this.controls[lane]?.abort();const c=new AbortController();this.controls[lane]=c;const seq=++this.sequences[lane],epoch=this.epoch,api=this.api;this.publish({[lane]:Object.freeze({...blank(),...position,phase:'loading'})});try{const raw=await this.readRetrying(api,path,c);if(this.disposed||epoch!==this.epoch||seq!==this.sequences[lane])return;const data=parse(raw);this.publish({[lane]:Object.freeze({...position,phase:'ready',data,error:null})});}catch(e){if(this.disposed||epoch!==this.epoch||seq!==this.sequences[lane])return;const error=errorInfo(e);if(this.denied(e)){this.stop();this.publish({...this.empty(),[lane]:Object.freeze({...blank(),phase:'error',error})});return;}this.publish({[lane]:Object.freeze({...position,data:null,phase:['stale_continuation','invalid_cursor'].includes(error.code)?'refresh-required':'error',error})});}}
 private pagePath(lane:'directory'|'jobs',op:PageOperation):string{const params=new URLSearchParams({limit:String(this.pageSize)});if(op.cursor)params.set('cursor',op.cursor);if(op.filter?.libraryId)params.set('libraryId',op.filter.libraryId);if(op.filter?.status)params.set('status',op.filter.status);return routePath(lane==='directory'?'get_admin_libraries':'get_admin_jobs')+'?'+params;}
 private parsePage(lane:'directory'|'jobs',op:PageOperation,raw:unknown){if(!object(raw)||!integer(raw.revision)||!text(raw.nextCursor))invalid();const scope=parseScope(raw.scope,this.scope);this.fence(scope.viewerFence);if(op.fence&&(scope.viewerFence!==op.fence.viewerFence||raw.revision!==op.fence.revision))throw new Failure('stale_continuation','Library management changed. Refresh this list.',true);if(raw.nextCursor&&(raw.nextCursor===op.cursor||op.history.includes(raw.nextCursor)))invalid();/* A library or a scan this app cannot read, or a scan outside the filter that was asked for, is left out alone; a repeated id keeps its first row. */const items=firstOfEach('library-management',lane,keepReadable('library-management',lane,rows(raw.items,this.pageSize),v=>{if(lane==='directory')return parseLibrary(v);const job=parseJob(v,op.filter?.libraryId);if(op.filter?.status&&job.status!==op.filter.status)invalid();return job;}),i=>i.id);return Object.freeze({scope,revision:raw.revision,items:Object.freeze(items),nextCursor:raw.nextCursor});}
 private page(lane:'directory'|'jobs',op:PageOperation):Promise<void>{this.operations[lane]=op;return this.read(lane,this.pagePath(lane,op),raw=>this.parsePage(lane,op,raw),{cursor:op.cursor,canPrevious:op.history.length>0});}
 /** Rebuilds expired revision-bound cursors to the same logical page, privately and at most 64 pages. */
 private async reanchorDirectory(target:PageOperation,originalError:unknown,api:LibraryContentApi,c:AbortController,owns:()=>boolean):Promise<{op:PageOperation;data:AdminPage<ManagedLibrary>}|undefined>{
  let op:PageOperation={cursor:null,history:[],pageIndex:0};
  for(let index=0;index<64;index++){
   if(!owns())return;
   const raw=await this.readRetrying(api,this.pagePath('directory',op),c);
   if(!owns())return;
   const data=this.parsePage('directory',op,raw) as AdminPage<ManagedLibrary>;
   if(index===(target.pageIndex??target.history.length))return {op,data};
   // A shorter directory cannot prove a replacement for the old page. Keep it owed.
   if(!data.nextCursor)throw originalError;
   op={cursor:data.nextCursor,history:[...op.history,op.cursor],pageIndex:index+1,fence:{revision:data.revision,viewerFence:data.scope.viewerFence}};
  }
  throw originalError;
 }
 /** Revalidates only the visible directory window, without replacing other lanes or receipts. */
 async refreshDirectory():Promise<QuietRefreshOutcome>{
  if(this.disposed||this.state.directory.phase==='loading')return {kind:'deferred'};
  const original=this.operations.directory??{cursor:null,history:[],pageIndex:0},before=this.state.directory;
  const epoch=this.epoch,c=new AbortController(),seq=++this.sequences.directory,api=this.api;
  this.controls.directory?.abort();this.controls.directory=c;this.quietDirectory=c;
  const owns=()=>!this.disposed&&epoch===this.epoch&&seq===this.sequences.directory&&!c.signal.aborted&&this.state.directory===before;
  try{
   let result:{op:PageOperation;data:AdminPage<ManagedLibrary>}|undefined;
   try{
    const raw=await this.readRetrying(api,this.pagePath('directory',original),c);
    if(!owns())return {kind:'deferred'};
    result={op:original,data:this.parsePage('directory',original,raw) as AdminPage<ManagedLibrary>};
   }catch(error){
    if(!owns())return {kind:'deferred'};
    const code=(error as {code?:unknown}|null)?.code;
    if(!original.cursor||code!=='stale_continuation'&&code!=='invalid_cursor')throw error;
    result=await this.reanchorDirectory(original,error,api,c,owns);
   }
   if(!owns()||!result)return {kind:'deferred'};
   const {op,data}=result;
   this.operations.directory=op;
   this.publish({directory:Object.freeze({phase:'ready',data,error:null,cursor:op.cursor,canPrevious:op.history.length>0})});
   return {kind:'adopted'};
  }catch(error){
   if(!owns())return {kind:'deferred'};
   if(this.denied(error)){this.stop();this.publish({...this.empty(),directory:Object.freeze({...blank(),phase:'error',error:errorInfo(error)})});}
   return {kind:'unreadable',error};
  }finally{if(this.controls.directory===c)delete this.controls.directory;if(this.quietDirectory===c)this.quietDirectory=undefined;}
 }

 private resource(libraryId:string):Promise<void>{return this.read('selected',encodedRoutePath("get_admin_libraries_id", {"id": encodeURIComponent(libraryId)}),raw=>{if(!object(raw))invalid();const scope=parseScope(raw.scope,this.scope);this.fence(scope.viewerFence);const library=parseLibrary(raw.library);if(library.id!==libraryId)invalid();return Object.freeze({scope,library});});}
 /** Read reconciliation only. Never retries a scan, cancellation or CAS command. */
 async reconcile(){this.active();const id=this.state.selectedLibraryId,filter=this.state.jobFilter;await Promise.all([this.loadLibraries(),this.loadJobs(filter),...(id?[this.resource(id)]:[])]);}
 /** `metadataAgent`: the library's metadata source, `online` (the server's default) or `local` (local metadata only), applied before the first scan. */
  createLibrary(input:{expectedRemoteSource?:ExpectedRemoteSource;name:string;kind:ManagedLibrary['kind'];path?:string;folders?:readonly string[];metadataAgent?:'online'|'local';metadataLanguage?:string;/** false: create without the automatic first scan. */scanAfter?:boolean}):Promise<void>{this.active();/* No directory precondition: a library can be created before the owner screen has loaded; the server validates, and the create reconciles the directory. */if(!object(input)||!text(input.name,100)||!input.name.trim()||new TextEncoder().encode(input.name.trim()).length>100||!['movie','tv','anime','music','audiobook','book','comic'].includes(input.kind)||(!(Array.isArray(input.folders)&&input.folders.length>=1&&input.folders.length<=64&&input.folders.every(path=>text(path,8192)&&path))&&(!text(input.path,8192)||!input.path)))throw new Error('Check the library name, kind and server source path.');if(input.metadataAgent!==undefined&&input.metadataAgent!=='online'&&input.metadataAgent!=='local')throw new Error('Choose a metadata source for the library.');if(input.metadataLanguage!==undefined&&!/^[a-z]{2}$/.test(input.metadataLanguage))throw new Error('Choose a metadata language this library offers.');if(input.scanAfter!==undefined&&typeof input.scanAfter!=='boolean')throw new Error('Choose whether to scan the new library.');const capturedRemote=input.expectedRemoteSource===undefined?undefined:expectedRemoteSource(input.expectedRemoteSource);return this.mutate('create','', 'POST',routePath("post_libraries"),{name:input.name.trim(),kind:input.kind,...(capturedRemote?{expectedRemoteSource:capturedRemote}:{}),...(input.folders?{folders:input.folders}:{path:input.path}),...(input.metadataAgent?{metadataAgent:input.metadataAgent}:{}),...(input.scanAfter===false?{scanAfter:false}:{}),...(input.metadataLanguage?{metadataLanguage:input.metadataLanguage}:{})});}
 rename(name:string,expectedRevision:number):Promise<void>{this.active();const selected=this.state.selected.data;if(!selected||this.state.selected.phase!=='ready'||!selected.library.actions.includes('rename'))throw new Error('Select a library that allows renaming.');const normalized=name.trim();if(!normalized||new TextEncoder().encode(normalized).length>100||!text(normalized,100)||!integer(expectedRevision)||expectedRevision!==selected.library.revision)throw new Error('The library name or expected revision is invalid.');return this.mutate('rename',selected.library.id,'PATCH',encodedRoutePath("patch_admin_libraries_id", {"id": encodeURIComponent(selected.library.id)}),{name:normalized,expectedRevision});}
 /** `full` re-reads every file, changed or not ("Scan all files"); the default lets the server choose (first import, weekly integrity or incremental). */
 scan(mode?:'full'):Promise<void>{this.active();const selected=this.state.selected.data;if(!selected||this.state.selected.phase!=='ready'||!selected.library.actions.includes('scan'))throw new Error('Select a library that allows scanning.');return this.mutate('scan',selected.library.id,'POST',encodedRoutePath("post_libraries_id_scans", {"id": encodeURIComponent(selected.library.id)})+(mode?'?mode='+mode:''));}
 cancelJob(jobId:string):Promise<void>{this.active();const job=this.state.jobs.data?.items.find(j=>j.id===jobId)??this.state.selected.data?.library.lastScan;if(!job||job.id!==jobId||!job.actions.includes('cancel'))throw new Error('Select a job that allows cancellation.');return this.mutate('cancel',job.libraryId,'DELETE',encodedRoutePath("delete_admin_ingestion_jobs_id", {"id": encodeURIComponent(jobId)}));}
 controlJob(jobId:string,action:'pause'|'resume'|'retry'):Promise<void>{this.active();const job=this.state.jobs.data?.items.find(j=>j.id===jobId);if(!job||!job.actions.includes(action))throw new Error('This scan does not allow that action.');return this.mutate('scan',job.libraryId,'POST',encodedRoutePath("post_admin_ingestion_jobs_id_action", {"id": encodeURIComponent(jobId), "action": action}));}
 private mutate(kind:'create'|'rename'|'scan'|'cancel',libraryId:string,method:string,path:string,body?:unknown):Promise<void>{const key=JSON.stringify([kind,libraryId,body,path]);if(this.mutationPromise){if(this.mutationKey===key)return this.mutationPromise;throw new Error('Wait for the current library command to finish.');}const epoch=this.epoch,c=new AbortController();let selected=this.state.selectedLibraryId;this.mutationController=c;this.mutationKey=key;this.publish({mutation:Object.freeze({phase:'pending',kind,error:null})});const promise=(async()=>{try{const raw=await this.request(this.api,path,method,body,c);if(this.disposed||epoch!==this.epoch||c.signal.aborted||selected!==this.state.selectedLibraryId)return;if(kind==='create'){const submitted=body as {name:string;kind:string};if(!object(raw)||!id(raw.id)||raw.name!==submitted.name||raw.kind!==submitted.kind)invalid();selected=raw.id;this.publish({selectedLibraryId:selected,selected:blank(),jobs:blank(),jobFilter:Object.freeze({libraryId:selected})});}else if(kind==='rename'){if(!object(raw))invalid();const scope=parseScope(raw.scope,this.scope);this.fence(scope.viewerFence);const library=parseLibrary(raw.library);if(library.id!==libraryId||library.name!==(body as {name:string}).name||library.revision<(body as {expectedRevision:number}).expectedRevision)invalid();}else if(kind==='scan'&&!path.endsWith('/pause')&&!path.endsWith('/resume')&&(!object(raw)||!id(raw.id)||!integer(raw.processed)))invalid();await this.reconcile();if(!this.disposed&&epoch===this.epoch&&!c.signal.aborted)this.publish({mutation:Object.freeze({phase:'complete',kind,error:null})});}catch(e){if(this.disposed||epoch!==this.epoch||c.signal.aborted&&this.mutationController!==c||selected!==this.state.selectedLibraryId)return;const error=errorInfo(e);if(this.denied(e)){this.stop();this.publish({...this.empty(),mutation:Object.freeze({phase:'error',kind,error})});return;}const definite=['source_changed','library_configuration_conflict','source_already_configured','invalid_request','invalid_metadata_language','not_found','forbidden','unauthorized'].includes(error.code);this.publish({mutation:Object.freeze({phase:definite?'error':'ambiguous',kind,error})});}finally{if(this.mutationController===c){this.mutationPromise=undefined;this.mutationKey='';}}})();this.mutationPromise=promise;return promise;}
 /** A read the server says may be repeated (`retry: same_request`, a 5xx or 429) is tried again
  * with backoff, honouring Retry-After, before the page shows its error with Try again. */
 private async readRetrying(api:LibraryContentApi,path:string,c:AbortController):Promise<unknown>{
  for(let attempt=0;;attempt++){
   try{return await this.request(api,path,'GET',undefined,c);}
   catch(e){
    if(attempt>=this.readRetries||c.signal.aborted||this.denied(e)||!sameRequest(e))throw e;
    const after=(e as {retryAfterSeconds?:unknown}).retryAfterSeconds;
    const floor=typeof after==='number'&&Number.isFinite(after)&&after>0?after*1000:0;
    // Never shorten a server floor into the platform's maximum timer delay.
    if(!Number.isFinite(floor)||floor>2147483647)throw e;
    const wait=Math.ceil(Math.max(floor,Math.min(8000,this.retryBaseMs*2**attempt)));
    await new Promise<void>(resolve=>{
     const t=setTimeout(done,wait);
     function done(){clearTimeout(t);c.signal.removeEventListener('abort',done);resolve();}
     c.signal.addEventListener('abort',done,{once:true});
    });
    if(c.signal.aborted)throw e;
   }
  }
 }
 private async request(api:LibraryContentApi,path:string,method:string,body:unknown,c:AbortController):Promise<unknown>{let timer:ReturnType<typeof setTimeout>|undefined;let abort=()=>{};try{return await Promise.race([api.request(path,method,body,c.signal),new Promise<never>((_,reject)=>{abort=()=>reject(new Failure('cancelled','Library request cancelled.'));c.signal.addEventListener('abort',abort,{once:true});timer=setTimeout(()=>{reject(new Failure('timeout','The server took too long. Refresh to check the current state.',true));c.abort();},this.timeoutMs);})]);}finally{if(timer)clearTimeout(timer);c.signal.removeEventListener('abort',abort);}}
}

export {LibraryInventoryService,fetchInventoryStatus,inventoryStatusMessage,scanProgress} from './library-inventory.ts';
export type {ScanProgress,ViewerInventoryStatus} from './library-inventory.ts';

/** Read-only, folders-only server explorer. A writable check touches only the
 * selected directory and is requested explicitly by single-folder callers. */
/** The server's own classification of a chosen folder: the longest published
 * root it lives under, using the server's drive or mount facts. A longer root
 * with no classification wins over a shorter classified ancestor, so an
 * unclassified nested mount stays unknown. No path guess is made. */
export function classifyServerPath(path:string,roots:readonly {path:string;classification?:'local'|'network'}[],separator:string):'local'|'network'|undefined{
 const trim=(value:string)=>value.length>1&&value.endsWith(separator)?value.slice(0,-1):value;
 const target=trim(path);let best=-1,classification:'local'|'network'|undefined;
 for(const root of roots){const base=trim(root.path);
  const under=base===separator?target.startsWith(separator):target===base||target.startsWith(base+separator);
  if(under&&base.length>best){best=base.length;classification=root.classification;}}
 return best<0?undefined:classification;
}
export async function browseServerFilesystem(api:LibraryContentApi, options:Readonly<{path?:string;cursor?:string;showHidden?:boolean;prefix?:string;checkWritable?:boolean;includeFiles?:boolean;limit?:number}>={}, signal?:AbortSignal):Promise<import('./administration.ts').FilesystemPage>{
 const limit=options.limit??200;
 if(!Number.isInteger(limit)||limit<1||limit>200||options.prefix!==undefined&&(!text(options.prefix,255)||/[\\/]/.test(options.prefix)))throw new Error('Invalid folder explorer query.');
 const q=new URLSearchParams({limit:String(limit)});
 for(const key of ['path','cursor','prefix'] as const){const value=options[key];if(value!==undefined){if(!text(value,8192))throw new Error('Invalid folder explorer path.');q.set(key,value);}}
 for(const key of ['showHidden','checkWritable','includeFiles'] as const)if(options[key]!==undefined)q.set(key,options[key]?'1':'0');
 const {parseFilesystemPage}=await import('./administration.ts');
 const raw=await api.request<RouteResponse<'get_admin_filesystem'>>(routePath('get_admin_filesystem')+'?'+q,'GET',undefined,signal);
 if(!object(raw))invalid();return parseFilesystemPage(raw.result);
}
