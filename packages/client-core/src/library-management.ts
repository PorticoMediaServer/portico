import type {QuietRefreshOutcome} from './freshness-actions.ts';
import {expectedRemoteSource,type ExpectedRemoteSource} from './remote-source-selection.ts';
import type {LibrarySummary} from './library-summary.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {callServer, serverPath} from './query/server-api.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable} from './tolerant.ts';
import {queryKeys} from './query/keys.ts';
import {until} from './query/until.ts';
import {libraryAdminDependencies} from './library-inventory.ts';
import {dependsOn} from './freshness-hub.ts';
import type {ViewerQueries} from './query/viewer-queries.ts';
import type {ViewerQueryOptions} from './query/react.ts';
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
/** An owner's view of one library: the document under `['admin','library',id,'management']`. */
export type LibraryDocument=Readonly<{scope:Readonly<{serverId:string;viewerFence:string}>;library:ManagedLibrary}>;
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
/** A page of the files that need attention in one library, owner only. Unreadable rows are skipped. */
export async function fetchEpisodeIssues(api:LibraryContentApi,libraryId:string,cursor='',signal?:AbortSignal):Promise<EpisodeIssuePage>{
 const q=new URLSearchParams({limit:'40'});if(cursor)q.set('cursor',cursor);
 const raw=await api.request<RouteResponse<"get_libraries_id_episode_issues">>(encodedRoutePath("get_libraries_id_episode_issues", {"id": encodeURIComponent(libraryId)}) + "?" + (q),'GET',undefined,signal);
 if(!object(raw)||!Array.isArray(raw.issues)||raw.issues.length>100||typeof raw.nextCursor!=='string')invalid();
 const issues=raw.issues.filter((x:unknown):x is Record<string,unknown>=>object(x)&&id(x.assetId)&&text(x.sourceName,8192)&&text(x.code,256)).map(x=>Object.freeze({assetId:x.assetId as string,sourceName:x.sourceName as string,code:x.code as string}));
 return Object.freeze({issues:Object.freeze(issues),nextCursor:raw.nextCursor});
}
function errorInfo(e:unknown):LibraryManagementError{const v=e as {code?:unknown;retryable?:unknown};return Object.freeze({code:id(v?.code)?v.code:'request_failed',message:e instanceof Error?e.message:'Library management request failed.',retryable:v?.retryable===true});}
/** The server says the same request may simply be repeated: `retry: same_request`, or a 5xx/429 that says nothing else. */
function sameRequest(e:unknown):boolean{const v=e as {status?:unknown;retry?:unknown;retryable?:unknown;code?:unknown}|null;if(!v||v.code==='timeout'||v.code==='cancelled')return false;if(typeof v.retry==='string')return v.retry==='same_request';const status=typeof v.status==='number'?v.status:0;return status===429||status>=500&&v.retryable!==false;}
function blank<T=never>():AdminRead<T>{return Object.freeze({phase:'idle',data:null,error:null,cursor:null,canPrevious:false});}
function checkScope(s:ContentScope):ContentScope{if(!object(s)||!id(s.serverId)||!text(s.viewerId,1024)||!s.viewerId)throw new Error('Library management requires a bound owner API and viewer.');return Object.freeze({...s});}
type Lane='directory'|'selected'|'jobs';type PageOperation={cursor:string|null;history:readonly (string|null)[];/** Zero-based logical page, independent of the bounded Back history. */pageIndex?:number;filter?:LibraryJobFilter;/** The revision of the list the cursor came from: a cursor is only continued while the list is still at it. The viewer's fence is the server's to check: it binds every cursor. */anchor?:{revision:number}};
export class LibraryManagementService{
 private readRetries:number;private retryBaseMs:number;private api:LibraryContentApi;private scope:ContentScope;private pageSize:number;private timeoutMs:number;private epoch=0;private generation=0;private disposed=false;private listeners=new Set<()=>void>();private controls:Partial<Record<Lane,AbortController>>={};private sequences:Record<Lane,number>={directory:0,selected:0,jobs:0};private operations:Partial<Record<'directory'|'jobs',PageOperation>>={};private mutationController?:AbortController;private mutationPromise?:Promise<void>;private mutationKey='';private queries?:ViewerQueries;private state:LibraryManagementSnapshot;
 /** `queries`: the viewer's cache; a library's own document (`selectLibrary`) is read through it, shared with every other view of it. */
 constructor(o:{api:LibraryContentApi;scope:ContentScope;pageSize?:number;timeoutMs?:number;readRetries?:number;retryBaseMs?:number;queries?:ViewerQueries;scanRecheckMs?:number;hidden?:()=>boolean}){this.scanRecheckMs=o.scanRecheckMs??5000;if(o.hidden)this.hidden=o.hidden;this.readRetries=o.readRetries??3;this.retryBaseMs=o.retryBaseMs??1000;this.api=o.api;this.scope=checkScope(o.scope);if(o.queries&&o.queries.scope.serverId!==this.scope.serverId)throw new Error('Library management is read from its own server\'s cache.');this.queries=o.queries;this.pageSize=o.pageSize??40;this.timeoutMs=o.timeoutMs??15000;if(!Number.isInteger(this.pageSize)||this.pageSize<1||this.pageSize>40||!Number.isFinite(this.timeoutMs)||this.timeoutMs<1||this.timeoutMs>60000)throw new Error('Invalid management request bounds.');this.state=this.empty();}
 private quietDirectory?:AbortController;
 /** Cancels only the mounted quiet directory read; ordinary paging and command owners stay intact. */
 invalidateQuietDirectory=():void=>{const c=this.quietDirectory;this.quietDirectory=undefined;c?.abort();if(c&&this.controls.directory===c)delete this.controls.directory;};
 getSnapshot=()=>this.state;subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>this.listeners.delete(listener);};
 private empty():LibraryManagementSnapshot{return Object.freeze({generation:this.generation,scope:this.scope,selectedLibraryId:null,directory:blank(),selected:blank(),jobs:blank(),jobFilter:Object.freeze({}),mutation:Object.freeze({phase:'idle',kind:null,error:null})});}
 private active(){if(this.disposed)throw new Error('Library management service is disposed.');}
 private publish(p:Partial<LibraryManagementSnapshot>){this.state=Object.freeze({...this.state,...p,generation:++this.generation});for(const fn of this.listeners)fn();this.followScan();}
 /** How often a running scan is looked at again. */
 private scanRecheckMs:number;private scanTimer:ReturnType<typeof setTimeout>|undefined;
 /** A tab out of sight reads nothing while a scan runs; it reads again within one wait of being shown. A phone app has no document and reads on. */
 private hidden:()=>boolean=()=>typeof document!=='undefined'&&document.visibilityState==='hidden';
 /** The rebuilt server says nothing on the change feed when a scan ends, so while the list or the selected library
  * shows a scan running, both are read again every few seconds, and not once none does. */
 private followScan(){
  const running=(l:ManagedLibrary|undefined)=>l?.lastScan?.status==='running';
  const any=!!this.state.directory.data?.items.some(running)||running(this.state.selected.data?.library);
  if(!any||this.scanTimer||this.disposed)return;
  this.scanTimer=setTimeout(()=>{this.scanTimer=undefined;if(this.disposed)return;if(this.hidden()){this.followScan();return;}
   void this.refreshDirectory();const id=this.state.selectedLibraryId;if(id&&running(this.state.selected.data?.library))void this.resource(id,true).catch(()=>{});},this.scanRecheckMs);
 }
 private denied(e:unknown){const v=e as {status?:unknown;code?:unknown};return v?.status===401||v?.status===403||['unauthorized','forbidden','permission_changed','session_revoked'].includes(String(v?.code));}
 /** The server refused the owner: what was read for them is kept nowhere, in this service or in the viewer's cache. */
 private forgetCached(){this.queries?.client.removeQueries({queryKey:queryKeys.adminLibraries()});}
 private stop(){if(this.scanTimer){clearTimeout(this.scanTimer);this.scanTimer=undefined;}this.epoch++;for(const c of Object.values(this.controls))c?.abort();this.mutationController?.abort();this.mutationPromise=undefined;this.mutationKey='';this.operations={};}
 cancel(){this.active();this.stop();this.publish(this.empty());}
 setScope(scope:ContentScope,api:LibraryContentApi){this.active();const next=checkScope(scope);if(this.queries&&this.queries.scope.serverId!==next.serverId)throw new Error('Library management is read from its own server\'s cache: build a new one for another server.');this.stop();this.scope=next;this.api=api;this.publish(this.empty());}
 dispose(){this.stop();this.disposed=true;this.listeners.clear();}
 loadLibraries():Promise<void>{this.active();return this.page('directory',{cursor:null,history:[],pageIndex:0});}
 nextLibraries(){return this.turn('directory',true);}previousLibraries(){return this.turn('directory',false);}
 loadJobs(filter:LibraryJobFilter={}):Promise<void>{this.active();if(!object(filter)||filter.libraryId!==undefined&&!id(filter.libraryId)||filter.status!==undefined&&!statuses.includes(filter.status))throw new Error('Invalid job filter.');const checked=Object.freeze({...filter});
  // The rebuilt server reads a library's scan with the library itself; no screen lists this lane, so it holds an empty page and asks nothing.
  this.publish({jobFilter:checked,jobs:Object.freeze({...blank<AdminPage<LibraryAdminJob>>(),phase:'ready',data:Object.freeze({scope:this.serverScope(),revision:1,items:Object.freeze([]),nextCursor:''})})});return Promise.resolve();}
 nextJobs(){return this.turn('jobs',true);}previousJobs(){return this.turn('jobs',false);}
 async selectLibrary(libraryId:string){this.active();if(!id(libraryId))throw new Error('Invalid library ID.');this.controls.selected?.abort();this.controls.jobs?.abort();this.mutationController?.abort();this.mutationController=undefined;this.mutationPromise=undefined;this.mutationKey='';this.publish({selectedLibraryId:libraryId,selected:blank(),jobs:blank(),mutation:Object.freeze({phase:'idle',kind:null,error:null})});await Promise.all([this.resource(libraryId,false),this.loadJobs({libraryId})]);}
 private turn(lane:'directory'|'jobs',next:boolean):Promise<void>{this.active();const read=this.state[lane],op=this.operations[lane];if(read.phase!=='ready'||!read.data||!op)return Promise.resolve();const history=[...op.history];let cursor:string|null;if(next){if(!read.data.nextCursor)return Promise.resolve();cursor=read.data.nextCursor;history.push(op.cursor);if(history.length>64)history.shift();}else{if(!history.length)return Promise.resolve();cursor=history.pop()!;}return this.page(lane,{...op,cursor,history,pageIndex:(op.pageIndex??0)+(next?1:-1),anchor:{revision:read.data.revision}});}
 private async read<T>(lane:Lane,load:(c:AbortController,api:LibraryContentApi)=>Promise<T>,position:{cursor:string|null;canPrevious:boolean}={cursor:null,canPrevious:false}){this.controls[lane]?.abort();const c=new AbortController();this.controls[lane]=c;const seq=++this.sequences[lane],epoch=this.epoch,api=this.api;this.publish({[lane]:Object.freeze({...blank(),...position,phase:'loading'})});try{const data=await load(c,api);if(this.disposed||epoch!==this.epoch||seq!==this.sequences[lane])return;this.publish({[lane]:Object.freeze({...position,phase:'ready',data,error:null})});}catch(e){if(this.disposed||epoch!==this.epoch||seq!==this.sequences[lane])return;const error=errorInfo(e);if(this.denied(e)){this.stop();this.forgetCached();this.publish({...this.empty(),[lane]:Object.freeze({...blank(),phase:'error',error})});return;}this.publish({[lane]:Object.freeze({...position,data:null,phase:['stale_continuation','invalid_cursor'].includes(error.code)?'refresh-required':'error',error})});}}
 /** The rebuilt server's library list, `GET /v1/admin/libraries` (owner), a page at a time. */
 private pagePath(_lane:'directory'|'jobs',op:PageOperation):string{return serverPath('GET /v1/admin/libraries',{query:{cursor:op.cursor??undefined}});}
 /** The rebuilt server answers no viewer fence: this service's own scope stands for it. */
 private serverScope(){return Object.freeze({serverId:this.scope.serverId,viewerFence:'server'});}
 private parsePage(lane:'directory'|'jobs',op:PageOperation,raw:unknown){if(lane!=='directory'||!object(raw)||!Array.isArray(raw.items)||raw.items.length>this.pageSize*2||raw.nextCursor!==undefined&&!text(raw.nextCursor))invalid();const nextCursor=(raw.nextCursor as string|undefined)??'';if(nextCursor&&(nextCursor===op.cursor||op.history.includes(nextCursor)))invalid();/* A library this app cannot read is left out alone; a repeated id keeps its first row. */const items=firstOfEach('library-management',lane,keepReadable('library-management',lane,raw.items,fromServerLibrary),i=>i.id);return Object.freeze({scope:this.serverScope(),revision:1,items:Object.freeze(items),nextCursor});}
 private page(lane:'directory'|'jobs',op:PageOperation):Promise<void>{this.operations[lane]=op;const path=this.pagePath(lane,op);return this.read(lane,async(c,api)=>this.parsePage(lane,op,await this.readRetrying(api,path,c)),{cursor:op.cursor,canPrevious:op.history.length>0});}
 /** Revalidates only the visible directory window, without replacing other lanes or receipts. */
 async refreshDirectory():Promise<QuietRefreshOutcome>{
  if(this.disposed||this.state.directory.phase==='loading')return {kind:'deferred'};
  const original=this.operations.directory??{cursor:null,history:[],pageIndex:0},before=this.state.directory;
  const epoch=this.epoch,c=new AbortController(),seq=++this.sequences.directory,api=this.api;
  this.controls.directory?.abort();this.controls.directory=c;this.quietDirectory=c;
  const owns=()=>!this.disposed&&epoch===this.epoch&&seq===this.sequences.directory&&!c.signal.aborted&&this.state.directory===before;
  try{
   const raw=await this.readRetrying(api,this.pagePath('directory',original),c);
   if(!owns())return {kind:'deferred'};
   const op=original,data=this.parsePage('directory',original,raw) as AdminPage<ManagedLibrary>;
   this.operations.directory=op;
   this.publish({directory:Object.freeze({phase:'ready',data,error:null,cursor:op.cursor,canPrevious:op.history.length>0})});
   return {kind:'adopted'};
  }catch(error){
   if(!owns())return {kind:'deferred'};
   if(this.denied(error)){this.stop();this.forgetCached();this.publish({...this.empty(),directory:Object.freeze({...blank(),phase:'error',error:errorInfo(error)})});}
   return {kind:'unreadable',error};
  }finally{if(this.controls.directory===c)delete this.controls.directory;if(this.quietDirectory===c)this.quietDirectory=undefined;}
 }

 /** An owner's view of one library, in the viewer's cache (every view of it shares one read). It is read
  * with the cache's own requester, never this view's: closing the view that started it must not end it for the others. */
 private libraryQuery(queries:ViewerQueries,libraryId:string):ViewerQueryOptions<LibraryDocument>{
  const api:LibraryContentApi=queries.api;
  return {queryKey:queryKeys.adminLibraryManagement(libraryId),
   queryFn:async({signal})=>{const c=new AbortController(),stop=()=>c.abort();if(signal.aborted)stop();else signal.addEventListener('abort',stop,{once:true});
    try{return this.parseLibraryDocument(await this.readRetrying(api,encodedRoutePath("get_admin_libraries_id", {"id": encodeURIComponent(libraryId)}),c),libraryId);}finally{signal.removeEventListener('abort',stop);}},
   meta:{dependencies:libraryAdminDependencies(libraryId)}};
 }
 private parseLibraryDocument(raw:unknown,libraryId:string):LibraryDocument{const library=fromServerLibrary(raw);if(library.id!==libraryId)invalid();return Object.freeze({scope:this.serverScope(),library});}
 /** The cached document of a library, when nothing it was built from has moved since it was read. */
 private cachedLibrary(libraryId:string):LibraryDocument|undefined{
  const queries=this.queries;if(!queries)return undefined;
  const key=queryKeys.adminLibraryManagement(libraryId),state=queries.client.getQueryState<LibraryDocument>(key);
  return state&&state.data!==undefined&&state.status==='success'&&!state.isInvalidated&&queries.documentFor(key)?.getState().stale!==true?state.data:undefined;
 }
 /** `fresh`: something moved that a cached document may not include (a write here, a reconcile). */
 private resource(libraryId:string,fresh:boolean):Promise<void>{
  const held=fresh?undefined:this.cachedLibrary(libraryId);
  // A library the cache holds and nothing has moved since: shown at once, with no request and no loading state.
  if(held){this.controls.selected?.abort();++this.sequences.selected;this.publish({selected:Object.freeze({...blank(),phase:'ready',data:held})});return Promise.resolve();}
  return this.read('selected',async(c,api)=>{
   const queries=this.queries;
   if(!queries)return this.parseLibraryDocument(await this.readRetrying(api,encodedRoutePath("get_admin_libraries_id", {"id": encodeURIComponent(libraryId)}),c),libraryId);
   const options=this.libraryQuery(queries,libraryId);return until(queries.client.fetchQuery(fresh?{...options,staleTime:0}:options),c.signal);
  });
 }
 /** Read reconciliation only. Never retries a scan, cancellation or CAS command. */
 async reconcile(){this.active();const id=this.state.selectedLibraryId,filter=this.state.jobFilter;await Promise.all([this.loadLibraries(),this.loadJobs(filter),...(id?[this.resource(id,true)]:[])]);}
 /** `metadataAgent`: the library's metadata source, `online` (the server's default) or `local` (local metadata only), applied before the first scan. */
  /** Creates a library on the rebuilt server (`POST /v1/admin/libraries`), which starts its first scan itself.
  * Home and every library list read their libraries again. `metadataAgent` and `metadataLanguage` are not on the server's create yet and are not sent. */
 createLibrary(input:{expectedRemoteSource?:ExpectedRemoteSource;name:string;kind:ManagedLibrary['kind'];path?:string;folders?:readonly string[];metadataAgent?:'online'|'local';metadataLanguage?:string;/** Not on the rebuilt server: a new library is always scanned. */scanAfter?:boolean}):Promise<void>{
  this.active();
  const folders=(input.folders?.length?[...input.folders]:input.path?[typedServerPath(input.path)]:[]).map(cleanServerPath).filter(Boolean);
  if(!object(input)||!text(input.name,100)||!input.name.trim()||new TextEncoder().encode(input.name.trim()).length>100||!(input.kind in serverLibraryKinds)||!folders.length||folders.length>64||!folders.every(path=>text(path,8192)&&path))throw new Error('Check the library name, kind and server source path.');
  if(this.mutationPromise)throw new Error('Wait for the current library command to finish.');
  const epoch=this.epoch,c=new AbortController();this.mutationController=c;this.mutationKey='create';
  this.publish({mutation:Object.freeze({phase:'pending',kind:'create',error:null})});
  const api={request:<T,>(path:string,method?:string,body?:unknown)=>this.api.request<T>(path,method,body,c.signal)};
  const promise=(async()=>{
   try{
    const made=await callServer(api,'POST /v1/admin/libraries',{body:{name:input.name.trim(),kind:serverLibraryKinds[input.kind],folders}});
    if(!object(made)||!id(made.id))invalid();
    if(this.disposed||epoch!==this.epoch||c.signal.aborted)return;
    this.queries?.moved([dependsOn.libraries]);
    // The owner's library directory is still read from the old server's routes; a failure there does not undo the create.
    await this.reconcile().catch(()=>{});
    if(!this.disposed&&epoch===this.epoch&&!c.signal.aborted)this.publish({mutation:Object.freeze({phase:'complete',kind:'create',error:null})});
   }catch(e){
    if(this.disposed||epoch!==this.epoch||c.signal.aborted)return;
    const error=errorInfo(e);this.publish({mutation:Object.freeze({phase:'error',kind:'create',error})});
   }finally{if(this.mutationController===c){this.mutationPromise=undefined;this.mutationKey='';}}
  })();
  this.mutationPromise=promise;return promise;
 }
 rename(name:string,expectedRevision:number):Promise<void>{this.active();const selected=this.state.selected.data;if(!selected||this.state.selected.phase!=='ready'||!selected.library.actions.includes('rename'))throw new Error('Select a library that allows renaming.');const normalized=name.trim();if(!normalized||new TextEncoder().encode(normalized).length>100||!text(normalized,100)||!integer(expectedRevision)||expectedRevision!==selected.library.revision)throw new Error('The library name or expected revision is invalid.');return this.mutate('rename',selected.library.id,'PATCH',serverPath('PATCH /v1/admin/libraries/{id}',{id:selected.library.id}),{name:normalized});}
 /** `full` re-reads every file, changed or not ("Scan all files"); the default lets the server choose (first import, weekly integrity or incremental). */
 scan(mode?:'full'):Promise<void>{this.active();const selected=this.state.selected.data;if(!selected||this.state.selected.phase!=='ready'||!selected.library.actions.includes('scan'))throw new Error('Select a library that allows scanning.');return this.mutate('scan',selected.library.id,'POST',serverPath('POST /v1/admin/libraries/{id}/scan',{id:selected.library.id}),mode==='full'?{allFiles:true}:{});}
 cancelJob(jobId:string):Promise<void>{this.active();const job=this.state.jobs.data?.items.find(j=>j.id===jobId)??this.state.selected.data?.library.lastScan;if(!job||job.id!==jobId||!job.actions.includes('cancel'))throw new Error('Select a job that allows cancellation.');return this.mutate('cancel',job.libraryId,'DELETE',serverPath('DELETE /v1/admin/libraries/{id}/scan',{id:job.libraryId}));}
 /** The rebuilt server offers no pause or resume of a scan; a failed one is scanned again. */
 controlJob(jobId:string,action:'pause'|'resume'|'retry'):Promise<void>{this.active();const job=this.state.jobs.data?.items.find(j=>j.id===jobId)??this.state.selected.data?.library.lastScan;if(!job||job.id!==jobId||!job.actions.includes(action)||action!=='retry')throw new Error('This scan does not allow that action.');return this.mutate('scan',job.libraryId,'POST',serverPath('POST /v1/admin/libraries/{id}/scan',{id:job.libraryId}),{});}
 private mutate(kind:'create'|'rename'|'scan'|'cancel',libraryId:string,method:string,path:string,body?:unknown):Promise<void>{const key=JSON.stringify([kind,libraryId,body,path]);if(this.mutationPromise){if(this.mutationKey===key)return this.mutationPromise;throw new Error('Wait for the current library command to finish.');}const epoch=this.epoch,c=new AbortController();let selected=this.state.selectedLibraryId;this.mutationController=c;this.mutationKey=key;this.publish({mutation:Object.freeze({phase:'pending',kind,error:null})});const promise=(async()=>{try{const raw=await this.request(this.api,path,method,body,c);if(this.disposed||epoch!==this.epoch||c.signal.aborted||selected!==this.state.selectedLibraryId)return;if(kind==='create'){const submitted=body as {name:string;kind:string};if(!object(raw)||!id(raw.id)||raw.name!==submitted.name||raw.kind!==submitted.kind)invalid();selected=raw.id;this.publish({selectedLibraryId:selected,selected:blank(),jobs:blank(),jobFilter:Object.freeze({libraryId:selected})});}else if(kind==='rename'){const library=fromServerLibrary(raw);if(library.id!==libraryId||library.name!==(body as {name:string}).name)invalid();}else if(kind==='scan'&&(!object(raw)||!id(raw.id)))invalid();/* Every cached owner document of this library (a new library: the list) is read again when next asked for. */this.queries?.moved(kind==='create'?[dependsOn.libraries]:libraryAdminDependencies(libraryId));await this.reconcile();if(!this.disposed&&epoch===this.epoch&&!c.signal.aborted)this.publish({mutation:Object.freeze({phase:'complete',kind,error:null})});}catch(e){if(this.disposed||epoch!==this.epoch||c.signal.aborted&&this.mutationController!==c||selected!==this.state.selectedLibraryId)return;const error=errorInfo(e);if(this.denied(e)){this.stop();this.forgetCached();this.publish({...this.empty(),mutation:Object.freeze({phase:'error',kind,error})});return;}const definite=['source_changed','library_configuration_conflict','source_already_configured','invalid_request','invalid_metadata_language','not_found','forbidden','unauthorized'].includes(error.code);this.publish({mutation:Object.freeze({phase:definite?'error':'ambiguous',kind,error})});}finally{if(this.mutationController===c){this.mutationPromise=undefined;this.mutationKey='';}}})();this.mutationPromise=promise;return promise;}
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
/** One folder of the server's machine for the folder explorer (`GET /v1/admin/filesystem`, 200 folders at a time), in the shape the
 * picker draws. The top (`path` empty) lists the places to start from as roots. `showHidden` asks for `hidden`, `checkWritable` for
 * `writable`; each folder says whether it is already a library's (`library`) or cannot be one (`cannotBeChosen`). Only folders are
 * listed (`includeFiles` is ignored); `prefix` narrows the folders shown here. */
export async function browseServerFilesystem(api:LibraryContentApi, options:Readonly<{path?:string;cursor?:string;showHidden?:boolean;prefix?:string;checkWritable?:boolean;includeFiles?:boolean;limit?:number}>={}, signal?:AbortSignal):Promise<import('./administration.ts').FilesystemPage>{
 for(const value of [options.path,options.cursor])if(value!==undefined&&!text(value,8192))throw new Error('Invalid folder explorer path.');
 const raw=await callServer(api,'GET /v1/admin/filesystem',{query:{path:options.path===undefined?undefined:cleanServerPath(options.path),cursor:options.cursor,hidden:options.showHidden?'true':undefined,writable:options.checkWritable?'true':undefined},signal});
 if(!object(raw)||!Array.isArray(raw.folders))invalid();
 const path=typeof raw.path==='string'?raw.path:'',separator=path.includes('\\')||/^[A-Za-z]:/.test(path)?'\\':'/';
 const prefix=options.prefix?.toLocaleLowerCase();
 const folders=raw.folders.filter(folder=>typeof folder?.name==='string'&&typeof folder.path==='string'&&(!prefix||folder.name.toLocaleLowerCase().startsWith(prefix)));
 const top=path==='';
 return Object.freeze({path,parent:raw.parent??'',separator,platform:separator==='/'?'linux':'windows',writable:false,writableChecked:false,suggestions:Object.freeze([]),truncated:raw.limited===true,
  roots:Object.freeze(top?folders.map(folder=>Object.freeze({path:folder.path,name:folder.name,kind:'folder',description:''})):[]),
  entries:Object.freeze(top?[]:folders.map(raw=>{
   const folder=raw as typeof raw&{library?:unknown;cannotBeChosen?:unknown;writable?:unknown};
   return Object.freeze({name:folder.name,path:folder.path,kind:'directory' as const,readable:true,symlink:false,bytes:0,hidden:folder.name.startsWith('.'),
    ...(typeof folder.library==='string'&&folder.library?{library:folder.library}:{}),...(typeof folder.cannotBeChosen==='string'&&folder.cannotBeChosen?{cannotBeChosen:folder.cannotBeChosen}:{}),
    ...(typeof folder.writable==='boolean'?{writable:folder.writable}:{})});
  })),
  nextCursor:raw.nextCursor??''});
}
/** A folder path in the form the server takes: no trailing slash or backslash (but `/` and `C:\\` stay), so
 * `/srv/media/` reads as `/srv/media` rather than being refused as not a full path. Nothing else changes: a folder's
 * name may end in a space, so padding is trimmed only where a person types (`typedServerPath`). */
export function cleanServerPath(path:string):string{
 if(/^[A-Za-z]:[\\/]*$/.test(path))return path.slice(0,2)+'\\';
 const cleaned=path.replace(/[\\/]+$/,'');
 return cleaned||(path.startsWith('/')?'/':path.startsWith('\\')?'\\':'');
}
/** A path a person typed: the spaces around it are not part of it. */
export const typedServerPath=(typed:string):string=>typed.trim();
/** The server's word for each library kind the screens offer. */
const serverLibraryKinds:Readonly<Record<string,string>>={movie:'movies',tv:'shows',anime:'anime',music:'music',audiobook:'audiobooks',book:'books',comic:'comics'};
/** The screens' word for each of the server's library kinds. */
const pageLibraryKinds:Readonly<Record<string,ManagedLibrary['kind']>>={movies:'movie',shows:'tv',anime:'anime',music:'music',audiobooks:'audiobook',books:'book',comics:'comic'};
/**
 * One library of the rebuilt server (`AdminLibrary`) in the shape the Libraries page draws. Its folders are
 * found or not by the last scan; a scan running now is its last scan, which Cancel stops; the owner may rename,
 * scan and remove it.
 */
function fromServerLibrary(v:unknown):ManagedLibrary{
 if(!object(v)||!id(v.id)||!text(v.name,512)||typeof v.kind!=='string'||!pageLibraryKinds[v.kind]||!text(v.revision,64)||!Array.isArray(v.folders)||!v.folders.length||typeof v.scanning!=='boolean')invalid();
 const folders=v.folders.filter(object),first=folders[0];
 if(!first||!text(first.path,8192))invalid();
 const libraryId=v.id,started=text(v.lastScanStartedAt,128)?v.lastScanStartedAt:undefined,finished=text(v.lastScanFinishedAt,128)?v.lastScanFinishedAt:undefined;
 const scan=(status:LibraryJobStatus,actions:LibraryAdminJob['actions']):LibraryAdminJob=>Object.freeze({id:'scan:'+libraryId,libraryId,status,processed:0,
  createdAt:started??finished??'',updatedAt:null,finishedAt:status==='running'?null:finished??null,actions:Object.freeze(actions)});
 const revision=Number.parseInt(v.revision,10);
 return Object.freeze({id:libraryId,name:v.name,kind:pageLibraryKinds[v.kind],revision:Number.isSafeInteger(revision)&&revision>0?revision:1,
  source:Object.freeze({path:first.path as string,availability:folders.every(folder=>folder.available===true)?'available' as const:'unavailable' as const,basis:'probe' as const,
   ...(text(first.lastScanAt,128)?{observedAt:first.lastScanAt}:{}),...(text(first.problem)&&first.problem?{message:first.problem}:{})}),
  lastScan:v.scanning?scan('running',['cancel']):finished?scan('complete',[]):null,
  actions:Object.freeze(['rename','scan','remove'] as const)});
}
