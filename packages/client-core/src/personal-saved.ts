import {validateHomeEntry} from './home.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {wireDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';
/** Online personal resources. The server owns membership, query evaluation and permissions.
 * Existing library navigation owns destinations/Back; this module owns no queue or device store. */
import {validateContentEntry,type ContentEntry} from './library-content.ts';
import {parseBrowseNode,parseBrowseResult,type BrowseNode,type BrowseResult,type BrowseSortSelection} from './browse.ts';
import type {SavedApi,SavedScope,SavedRoute,SavedActor,SavedShapeCounts} from './saved.ts';
import {parseSavedShapeCounts,parseResourceOrderWitness,resourceContentManual,resourceContentParams,resourceContentQuery,type ResourceContentQuery,type ResourceOrderWitness} from './saved.ts';
export type PersonalResourceKind='collection'|'view';
/** A saved view stores the whole browse request: the pivot, the expression tree
 * and the sort. The server validates it against the published capabilities; this
 * parser only refuses what it could never have produced. */
export type SavedDefinition=Readonly<{libraryId:string;pivot:string;query:BrowseNode|null;sort:readonly BrowseSortSelection[];presentation:'grid'|'list'}>;
export type PersonalVisibility='private'|'server';
export type PersonalResource=Readonly<{totalIsLowerBound?:boolean;shape?:'posters'|'squares';artworkPaths?:readonly string[];samples?:readonly ContentEntry[];onHome?:boolean;serverId:string;viewerFence:string;id:string;kind:PersonalResourceKind;name:string;summary:string;visibility:PersonalVisibility;revision:number;role:'owner'|'editor'|'viewer';entryCount:number;actions:readonly string[];definition?:SavedDefinition;status:'ready'|'needs-review';invalidComponents:readonly string[];pinned:boolean;pinRevision:number;shares:readonly Readonly<SavedActor&{role:'viewer'|'editor';displayName:string}>[]}>;
export type PersonalResourceEntry=Readonly<{id:string;hidden:boolean;media?:ContentEntry;updatedAt?:string;positionSeconds?:number;completed?:boolean}>;
export type PersonalSavedIntent=Readonly<
 {action:'create';kind:PersonalResourceKind;name:string;summary?:string;visibility?:PersonalVisibility;definition?:SavedDefinition}|
 {action:'update';name?:string;summary?:string;visibility?:PersonalVisibility;definition?:SavedDefinition}|
 /** `undoToken` alone: put back the entries a removal took out (its result's `undo.token`). */
 {action:'entries';addItemIds?:readonly string[];removeEntryIds?:readonly string[];undoToken?:string}|
 /** A collection's own order: these entries go just after `afterEntryId` ("" the front), in the
  * order given; without it they swap among their places. Fenced by the collection's revision. */
 {action:'reorder';entryIds:readonly string[];afterEntryId?:string}|
 {action:'delete'}|{action:'pin';pinned:boolean}|
 {action:'share';actor:SavedActor;role:'viewer'|'editor'}|{action:'unshare';actor:SavedActor}|
 {action:'clear-history'|'reset-viewing-activity'}>;
export type PersonalSavedError=Readonly<{code:string;message:string;conflict:boolean}>;
/** A collection page's own query echo, offered orders and filters, matching count, the index
 * of its first entry in the whole query and, in the collection's own order, its witnesses. */
export type PersonalResourcePage=Readonly<{sort:string;direction:'asc'|'desc';filter:string;sorts:readonly Readonly<{id:string;labelKey:string;directions:readonly ('asc'|'desc')[]}>[];filters:readonly Readonly<{id:string;labelKey:string;options:readonly Readonly<{id:string;label:string;count:number}>[]}>[];totalCount:number;start:number;order?:ResourceOrderWitness}>;
export type PersonalSavedSnapshot=Readonly<{page:PersonalResourcePage|null;shapeCounts?:SavedShapeCounts;route:SavedRoute|null;loading:boolean;error:PersonalSavedError|null;mutationError:PersonalSavedError|null;pending:boolean;retryPending:boolean;resources:readonly PersonalResource[];resource:PersonalResource|null;entries:readonly PersonalResourceEntry[];browse:BrowseResult|null;revision:number;viewerFence:string;cursor:string|null;history:readonly (string|null)[];nextCursor:string;result:Readonly<{resourceId?:string;deleted:boolean;entries?:EntryOutcomes;/** A removal can be undone until `until` with this token. */undo?:Readonly<{token:string;until:string}>}>|null;candidates:readonly Readonly<SavedActor&{id:string;displayName:string}>[];candidateCursor:string;libraries:readonly Readonly<{id:string;name:string;kind:string}>[]}>;
const obj=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const str=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>str(v,256)&&v.length>0&&!/[\r\n]/.test(v);
const num=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
function bad():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_saved'});}
function rows(v:unknown,max=100):unknown[]{if(!Array.isArray(v)||v.length>max)bad();return v;}
/** A repeated id keeps its first row. */
function distinct<T extends {id:string}>(a:T[],part='rows'):T[]{return firstOfEach('personal-saved',part,a,v=>v.id);}
function actor(v:unknown):SavedActor{if(!obj(v)||!['local','hosted'].includes(v.authority as string)||!id(v.accountId)||!id(v.profileId))bad();return {authority:v.authority as SavedActor['authority'],accountId:v.accountId,profileId:v.profileId};}
function definition(v:unknown):SavedDefinition{
 if(!obj(v)||!id(v.libraryId)||!id(v.pivot)||!['grid','list'].includes(v.presentation as string))bad();
 const sort=rows(v.sort,3).map(s=>{if(!obj(s)||!id(s.field)||!['asc','desc'].includes(s.direction as string))bad();return {field:s.field,direction:s.direction as 'asc'|'desc'};});
 if(sort.length<1)bad();
 let query:BrowseNode|null=null;
 if(v.query!==undefined&&v.query!==null){try{query=parseBrowseNode(v.query);}catch{bad();}}
 return {libraryId:v.libraryId,pivot:v.pivot,query,sort,presentation:v.presentation as 'grid'|'list'};
}
function resource(v:unknown,scope:SavedScope):PersonalResource{
 if(!obj(v)||v.serverId!==scope.serverId||!id(v.viewerFence)||!id(v.id)||!['collection','view'].includes(v.kind as string)||!str(v.name,400)||!v.name.trim()||!str(v.summary,8000)||!['private','server'].includes(v.visibility as string)||!num(v.revision)||!num(v.entryCount)||!['owner','editor','viewer'].includes(v.role as string)||!['ready','needs-review'].includes(v.status as string)||typeof v.pinned!=='boolean'||!num(v.pinRevision))bad();
 // An action this app does not know is not offered. The role and what a role may hold stay exact.
 const offered=rows(v.actions,8),actions=[...new Set(offered.filter((a):a is string=>typeof a==='string'&&['pin','update','delete','entries','reorder','share'].includes(a)))];
 if(actions.length!==offered.length)noteSkipped('personal-saved','resource.actions',offered.length-actions.length);
 if(v.role==='viewer'&&actions.some(a=>a!=='pin')||v.role==='editor'&&actions.some(a=>a!=='pin'&&a!=='entries'&&a!=='reorder'))bad();
 const shares=keepReadable('personal-saved','resource.shares',rows(v.shares??[],100),s=>{if(!obj(s)||typeof s.role!=='string'||!str(s.displayName,512))bad();/* Someone a collection or view is shared with is never hidden for holding a role this app does not know: they are listed, as a viewer, and can still be removed. */return {...actor(s),role:oneOfOr('personal-saved','share.role',s.role,['viewer','editor'] as const,'viewer'),displayName:s.displayName};});
 const invalidComponents=keepReadable('personal-saved','resource.invalidComponents',rows(v.invalidComponents,32),x=>{if(!str(x,256))bad();return x;});
 // Needs-review definitions are deliberately not executed or silently normalized.
 const d=v.kind==='view'&&v.status==='ready'?definition(v.definition):undefined;
 const card=v.shape===undefined?{}:{...(v.totalIsLowerBound===true?{totalIsLowerBound:true}:{}),shape:v.shape as 'posters'|'squares',artworkPaths:rows(v.artworkPaths,4).map(x=>{if(!str(x,4096))bad();return x;}),samples:rows(v.samples,4).map(validateContentEntry),onHome:v.onHome===true};
 if(v.shape!==undefined&&!['posters','squares'].includes(v.shape as string))bad();
 return {...card,serverId:scope.serverId,viewerFence:v.viewerFence,id:v.id,kind:v.kind as PersonalResourceKind,name:v.name,summary:v.summary,visibility:v.visibility as PersonalVisibility,revision:v.revision,role:v.role as PersonalResource['role'],entryCount:v.entryCount,actions,...(d?{definition:d}:{}),status:v.status as PersonalResource['status'],invalidComponents,pinned:v.pinned,pinRevision:v.pinRevision,shares};
}
/** Visibility is a published server policy, not a client-side sharing rule. */
function visibility(v:unknown):PersonalVisibility{if(v!=='private'&&v!=='server')throw new Error('Collection visibility must be private or server.');return v;}
export type EntryOutcomes=Readonly<{added:readonly string[];removed:readonly string[];unchanged:readonly string[];failed:readonly Readonly<{itemId:string;code:string}>[]}>;
/** Membership batches report every item. A row the server refused is an outcome the
 * viewer can see and act on, never a silently dropped change. */
export function parseEntryOutcomes(v:unknown,limit=200):EntryOutcomes{
 if(!obj(v))bad();
 const list=(x:unknown)=>Object.freeze(rows(x,limit).map(entry=>{if(!id(entry))bad();return entry;}));
 const failed=rows(v.failed,limit).map(entry=>{if(!obj(entry)||!id(entry.itemId)||!id(entry.code))bad();return Object.freeze({itemId:entry.itemId,code:entry.code});});
 const outcomes=Object.freeze({added:list(v.added),removed:list(v.removed),unchanged:list(v.unchanged),failed:Object.freeze(failed)});
 const all=[...outcomes.added,...outcomes.removed,...outcomes.unchanged,...outcomes.failed.map(f=>f.itemId)];
 if(new Set(all).size!==all.length)bad();
 return outcomes;
}
/** One already-named resource, without executing its saved query or scanning a
 * directory. The same canonical metadata parser/actor scope is used as Saved. */
export async function readPersonalResource(api:SavedApi,scope:SavedScope,resourceId:string,signal?:AbortSignal):Promise<PersonalResource>{
 const original=Object.freeze({serverId:scope.serverId,viewerId:scope.viewerId});
 if(!id(original.serverId)||!str(original.viewerId,1024)||!original.viewerId||!id(resourceId))bad();
 const current=()=>{if(signal?.aborted)throw Object.assign(new Error('Saved resource read retired'),{name:'AbortError'});};
 current();
 try{
  const raw=await api.request<unknown>(encodedRoutePath('get_saved_resources_id',{id:encodeURIComponent(resourceId)}),'GET',undefined,signal);
  current();const value=resource(raw,original);if(value.id!==resourceId)bad();current();return freeze(value);
 }catch(error){current();throw error;}
}

export const MAX_PERSONAL_BATCH=200;
/** `notInterested` (recommendation feedback) is the one field a show, album or book id may carry. */
export type PersonalBatchItem=Readonly<{itemId:string;expectedRevision?:number;watched?:boolean;favorite?:boolean;watchlisted?:boolean;notInterested?:boolean}>;
export type PersonalBatchResult=Readonly<{itemId:string;ok:boolean;code:string;message:string;personal:unknown}>;
export type PersonalBatchReceipt=Readonly<{serverId:string;viewerFence:string;operationId:string;results:readonly PersonalBatchResult[];updated:number;failed:number}>;
/** One request per bulk action. The client never fans out a row at a time. */
export function personalBatchBody(operationId:string,items:readonly PersonalBatchItem[]):Readonly<{operationId:string;items:readonly PersonalBatchItem[]}>{
 if(!id(operationId)||!/^[A-Za-z0-9_-]{1,128}$/.test(operationId))throw new Error('A batch needs a single stable operationId.');
 if(!items.length||items.length>MAX_PERSONAL_BATCH)throw new Error('A batch carries 1 to 200 items.');
 const seen=new Set<string>();
 const body=items.map(item=>{
  if(!id(item.itemId)||seen.has(item.itemId))throw new Error('Each batch item must name a distinct itemId.');
  seen.add(item.itemId);
  if(item.watched===undefined&&item.favorite===undefined&&item.watchlisted===undefined&&item.notInterested===undefined)throw new Error('Each batch item must set watched, favorite, watchlisted or notInterested.');
  if(item.expectedRevision!==undefined&&!num(item.expectedRevision))throw new Error('Invalid expectedRevision.');
  return Object.freeze({itemId:item.itemId,...(item.expectedRevision===undefined?{}:{expectedRevision:item.expectedRevision}),...(item.watched===undefined?{}:{watched:item.watched}),...(item.favorite===undefined?{}:{favorite:item.favorite}),...(item.watchlisted===undefined?{}:{watchlisted:item.watchlisted}),...(item.notInterested===undefined?{}:{notInterested:item.notInterested})});
 });
 return Object.freeze({operationId,items:Object.freeze(body)});
}
export function parsePersonalBatchReceipt(v:unknown,serverId:string,requested:readonly string[]):PersonalBatchReceipt{
 if(!obj(v)||v.serverId!==serverId||!id(v.viewerFence)||!id(v.operationId)||!num(v.updated)||!num(v.failed))bad();
 const results=rows(v.results,MAX_PERSONAL_BATCH).map(r=>{
  if(!obj(r)||!id(r.itemId)||typeof r.ok!=='boolean')bad();
  if(r.code!==undefined&&!id(r.code))bad();
  if(r.message!==undefined&&!str(r.message,4096))bad();
  if(r.ok&&r.code)bad();
  if(!r.ok&&!r.code)bad();
  return Object.freeze({itemId:r.itemId,ok:r.ok,code:(r.code as string)??'',message:(r.message as string)??'',personal:r.personal??null});
 });
 // Every item the client sent is answered exactly once, in the order it sent them.
 if(results.length!==requested.length||results.some((r,i)=>r.itemId!==requested[i]))bad();
 if(results.filter(r=>r.ok).length!==v.updated||results.filter(r=>!r.ok).length!==v.failed)bad();
 return Object.freeze({serverId,viewerFence:v.viewerFence,operationId:v.operationId,results:Object.freeze(results),updated:v.updated,failed:v.failed});
}
export const PERSONAL_BATCH_PATH=routePath("put_items_personal_state_batch");
function info(e:unknown):PersonalSavedError{const x=e as {code?:unknown};const code=id(x?.code)?x.code:'request_failed';return {code,message:e instanceof Error?e.message:'The request failed.',conflict:['resource_conflict','revision_mismatch','personal_state_conflict','stale_continuation','operation_expired'].includes(code)};}
function freeze<T>(v:T):T{if(v&&typeof v==='object'){for(const c of Object.values(v))freeze(c);Object.freeze(v);}return v;}
/** Entries a page holds, and how far a quiet re-read reaches: DEEP_PAGES pages of PAGE. */
const PAGE=40,DEEP_PAGES=25;
function shownOf(route:SavedRoute,p:{resources?:readonly PersonalResource[];entries?:readonly PersonalResourceEntry[]}):readonly {id:string}[]{return (route.view==='collections'||route.view==='views'?p.resources:p.entries)??[];}
/** A collection page's query echo must be the query asked for (defaults filled in). */
function resourcePage(raw:Record<string,unknown>,asked:ResourceContentQuery):PersonalResourcePage{
 const q=raw.query;if(!obj(q)||q.sort!==(asked.sort??'position')||q.category!==(asked.filter??'all')||q.direction!=='asc'&&q.direction!=='desc'||asked.direction!==undefined&&q.direction!==asked.direction||!num(raw.totalCount)||raw.start!==undefined&&!num(raw.start))bad();
 const sorts=distinct(keepReadable('personal-saved','sorts',rows(raw.sorts,16),v=>{if(!obj(v)||!id(v.id)||!id(v.labelKey))bad();return {id:v.id,labelKey:v.labelKey,directions:[...new Set(rows(v.directions,2).filter((d):d is 'asc'|'desc'=>d==='asc'||d==='desc'))]};}),'sorts');
 const filters=distinct(keepReadable('personal-saved','filters',rows(raw.filters,4),v=>{if(!obj(v)||!id(v.id)||!id(v.labelKey))bad();return {id:v.id,labelKey:v.labelKey,options:distinct(keepReadable('personal-saved','filter.options',rows(v.options,8),o=>{if(!obj(o)||!id(o.id)||!str(o.label,120)||!num(o.count))bad();return {id:o.id,label:o.label,count:o.count};}),'filter.options')};}),'filters');
 const order=resourceContentManual(asked)?parseResourceOrderWitness(raw.order):undefined;
 return freeze({sort:q.sort as string,direction:q.direction as 'asc'|'desc',filter:q.category as string,sorts,filters,totalCount:raw.totalCount as number,start:(raw.start as number|undefined)??0,...(order?{order}:{})});
}
export function isPersonalSavedRoute(route:SavedRoute|null):boolean{return !!route&&['collections','views','history','resource'].includes(route.view);}
/** The definition names an existing authorized library and canonical browse semantics. */
export function makeSavedDefinition(libraryId:string,titlePrefix='',presentation:'grid'|'list'='grid',sort:string='title',direction:'asc'|'desc'='asc',categoryId='',pivot='movies'):SavedDefinition{
 const predicates:BrowseNode[]=[];
 if(titlePrefix.trim())predicates.push({field:'title',operator:'starts-with',value:titlePrefix});
 if(categoryId){
  const [field,value]=categoryId.split(':');
  if(!field||!value)bad();
  predicates.push(field==='decade'||field==='year'?{field,operator:'equals',value:Number(value)}:{field,operator:'contains',value});
 }
 const query=predicates.length===0?null:predicates.length===1?predicates[0]:{all:predicates};
 return definition({libraryId,pivot,query,sort:[{field:sort,direction}],presentation});
}
/** Reads back the two flat controls the simple saved-view editor writes. A saved
 * view can hold a far richer expression; these only recognise what that editor
 * produced and return '' for anything else, rather than lossily flattening it. */
function topLevel(d?:SavedDefinition):readonly BrowseNode[]{
 if(!d?.query)return [];
 return 'all' in d.query?d.query.all:[d.query];
}
export function savedTitlePrefix(d?:SavedDefinition):string{
 for(const node of topLevel(d))if('field' in node&&node.field==='title'&&node.operator==='starts-with'&&typeof node.value==='string')return node.value;
 return '';
}
export function savedCategoryId(d?:SavedDefinition):string{
 for(const node of topLevel(d)){
  if(!('field' in node)||node.field==='title')continue;
  if((node.operator==='equals'||node.operator==='contains')&&(typeof node.value==='string'||typeof node.value==='number'))return node.field+':'+String(node.value);
 }
 return '';
}
export class PersonalSavedService{
 private state:PersonalSavedSnapshot;private listeners=new Set<()=>void>();private generation=0;private readTicket=0;private candidateTicket=0;private disposed=false;private controllers=new Set<AbortController>();private seen=0;
 private command?:{intent:PersonalSavedIntent;path:string;method:string;body:Record<string,unknown>;fence:string;rebased?:boolean};
 private api:SavedApi;readonly scope:SavedScope;private requestId:()=>Promise<string>;
 constructor(api:SavedApi,scope:SavedScope,requestId:()=>Promise<string>){this.api=api;this.scope=Object.freeze({...scope});this.requestId=requestId;if(!id(scope.serverId)||!str(scope.viewerId,1024)||!scope.viewerId)throw new Error('A complete viewer scope is required.');this.state=this.empty();}
 private empty():PersonalSavedSnapshot{return freeze({page:null,route:null,loading:false,error:null,mutationError:null,pending:false,retryPending:false,resources:[],resource:null,entries:[],browse:null,revision:0,viewerFence:'',cursor:null,history:[],nextCursor:'',result:null,candidates:[],candidateCursor:'',libraries:[]});}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(patch:Partial<PersonalSavedSnapshot>){if(this.disposed)return;this.state=freeze({...this.state,...patch});for(const fn of this.listeners)fn();}
 private alive(g:number){return !this.disposed&&g===this.generation;}
 private async bounded<T>(operation:(signal:AbortSignal)=>Promise<T>,signal?:AbortSignal):Promise<T>{
  const cancelled=()=>Object.assign(new Error('The request was cancelled.'),{code:'cancelled',name:'AbortError'});
  if(this.disposed||signal?.aborted)throw cancelled();
  const c=new AbortController(),stop=()=>c.abort();this.controllers.add(c);let timer:ReturnType<typeof setTimeout>|undefined;let abort:(()=>void)|undefined;
  signal?.addEventListener('abort',stop,{once:true});
  try{
   const interrupted=new Promise<never>((_,reject)=>{abort=()=>reject(cancelled());c.signal.addEventListener('abort',abort,{once:true});timer=setTimeout(()=>{reject(Object.assign(new Error('The server took too long. Retry the same operation to reconcile it.'),{code:'timeout'}));c.abort();},15000);});
   return await Promise.race([interrupted,operation(c.signal)]);
  }finally{if(timer)clearTimeout(timer);if(abort)c.signal.removeEventListener('abort',abort);signal?.removeEventListener('abort',stop);this.controllers.delete(c);}
 }
 private request<T>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>{return this.bounded(combined=>this.api.request<T>(path,method,body,combined),signal);}
 select(route:SavedRoute,position:{cursor?:string|null;history?:readonly (string|null)[]}={}):Promise<void>{
  if(this.disposed)throw new Error('Saved service is disposed.');if(!isPersonalSavedRoute(route)||route.view==='resource'&&!id(route.resourceId))throw new Error('Invalid personal resource destination.');
  if((position.history?.length??0)>64||position.cursor!=null&&!str(position.cursor,4096)||position.history?.some(c=>c!==null&&!str(c,4096)))throw new Error('Invalid Saved position.');
  this.generation++;for(const c of this.controllers)c.abort();this.command=undefined;this.publish({...this.empty(),route:freeze({...route}),cursor:position.cursor??null,history:[...(position.history??[])]});return this.load(position.cursor??null,position.history??[]);
 }
 refresh=()=>this.load(null,[]);
 /**
  * Read the page that is showing again without a loading state, and show the answer in place.
  * A failed read rejects and changes nothing. Nothing is read while a change made here is pending.
  */
 revalidate=async(context?:{signal?:AbortSignal;settle?:()=>Promise<void>}):Promise<void>=>{
  if(this.disposed||context?.signal?.aborted||!this.state.route||this.state.loading||this.state.pending||this.command||this.state.error||!this.state.viewerFence)return;
  // Paged past the first page (or shown as one long first page since): every page on screen is read again, not only the last one.
  if(this.state.history.length||this.seen>PAGE){const g=this.generation;try{if(await this.readThrough(context??{}))return;}catch(e){if(!this.alive(g)||context?.signal?.aborted)return;if(['unauthorized','forbidden','not_found','scope_changed','invalid_saved'].includes(info(e).code))return this.load(null,[]);throw e;}}
  await this.load(this.state.cursor,this.state.history,context??{});
 };
 /** Try a failed read of the page again. */
 retryRead=()=>this.load(this.state.cursor,this.state.history);
 next=()=>this.state.nextCursor&&this.state.history.length<64?this.load(this.state.nextCursor,[...this.state.history,this.state.cursor]):Promise.resolve();
 previous=()=>this.state.history.length?this.load(this.state.history.at(-1)!,this.state.history.slice(0,-1)):Promise.resolve();
 private async load(cursor:string|null,history:readonly (string|null)[],quiet?:{signal?:AbortSignal;settle?:()=>Promise<void>}):Promise<void>{
  const route=this.state.route,g=this.generation,ticket=++this.readTicket;if(!route||this.disposed||quiet?.signal?.aborted)return;if(!quiet)this.publish({loading:true,error:null});
  try{
   const patch=await this.page(route,cursor,history,quiet?.signal);if(!this.alive(g)||ticket!==this.readTicket||quiet?.signal?.aborted)return;
   if(this.state.viewerFence&&this.state.viewerFence!==patch.viewerFence)throw Object.assign(new Error('Your access changed. Reopen Saved to continue.'),{code:'scope_changed'});
   if(quiet){await quiet.settle?.();if(!this.alive(g)||ticket!==this.readTicket||quiet.signal?.aborted||this.state.pending||this.command)return;}
   // What the viewer has on screen: the first page, and every page appended after it.
   const count=shownOf(route,patch).length;this.seen=cursor===null?count:cursor===this.state.nextCursor&&history.length>this.state.history.length?this.seen+count:this.seen;
   this.publish({...patch,loading:false});
  }catch(e){if(!this.alive(g)||ticket!==this.readTicket)return;const error=info(e);
   // A quiet read that failed leaves the page as it was; one the server answered with "gone" or "not yours" becomes the page's own state through an ordinary load.
   if(quiet){if(quiet.signal?.aborted)return;if(['unauthorized','forbidden','not_found','scope_changed','invalid_saved','invalid_cursor','stale_continuation'].includes(error.code))return this.load(null,[]);throw e;}this.publish({loading:false,error,...(['unauthorized','forbidden','not_found','scope_changed','invalid_saved'].includes(error.code)?{resources:[],resource:null,entries:[],browse:null,viewerFence:''}:{})});}
 }
 /**
  * A list paged past its first page, read again quietly from the first page through everything on
  * screen (at most DEEP_PAGES pages) and shown in place as one first page: a change anywhere in
  * view shows, nothing below the viewer disappears, and the next page continues from a fresh
  * cursor. False when the list on screen is longer than that bound or the pages read caught the
  * list mid-change; the caller then does what it did before (reads the page in view, or reloads).
  */
 private async readThrough(quiet:{signal?:AbortSignal;settle?:()=>Promise<void>}):Promise<boolean>{
  const route=this.state.route,want=this.seen,g=this.generation,ticket=++this.readTicket;
  if(!route||this.disposed||want>DEEP_PAGES*PAGE)return false;
  const current=()=>this.alive(g)&&ticket===this.readTicket&&!quiet.signal?.aborted;
  if(!current())return true;
  const first=await this.page(route,null,[],quiet.signal);if(!current())return true;
  const all=[...shownOf(route,first)] as {id:string}[],seen=new Set(all.map(x=>x.id));let next=first.nextCursor,last=first;
  for(let pages=1;next&&all.length<want;pages++){
   if(!current())return true;
   if(pages>=DEEP_PAGES)return false;
   let page:typeof first;try{page=await this.page(route,next,[],quiet.signal);}catch(e){if(!current())return true;if(['stale_continuation','invalid_cursor'].includes(info(e).code))return false;throw e;}
   if(!current())return true;
   if(page.viewerFence!==first.viewerFence||page.revision!==first.revision)return false;
   for(const x of shownOf(route,page) as {id:string}[])if(!seen.has(x.id)){seen.add(x.id);all.push(x);}
   next=page.nextCursor===next?'':page.nextCursor;last=page;
  }
  if(this.state.viewerFence&&this.state.viewerFence!==first.viewerFence)throw Object.assign(new Error('Your access changed. Reopen Saved to continue.'),{code:'scope_changed'});
  await quiet.settle?.();if(!this.alive(g)||ticket!==this.readTicket||quiet.signal?.aborted||this.state.pending||this.command)return true;
  const combined=route.view==='collections'||route.view==='views'?{resources:all as unknown as PersonalResource[]}:{entries:all as unknown as PersonalResourceEntry[],...(first.browse&&last.browse?{browse:freeze({...first.browse,entries:(all as unknown as PersonalResourceEntry[]).flatMap(e=>e.media?[e.media]:[]),pageInfo:last.browse.pageInfo})}:{})};
  // Read from the first page, so nothing precedes it: the last page read names what follows.
  let page=first.page;if(page&&last.page){const {order:_,...rest}=page;page=freeze({...rest,...(last.page.order?{order:last.page.order}:{})});}
  this.seen=all.length;this.publish({...first,...combined,page,nextCursor:next,cursor:null,history:[],loading:false});
  return true;
 }
 /** One page of the route, read and checked. */
 private async page(route:SavedRoute,cursor:string|null,history:readonly (string|null)[],signal?:AbortSignal){
   const query=new URLSearchParams({limit:String(PAGE),...(cursor?{cursor}:{})});
   if(route.view==='resource')resourceContentParams(query,resourceContentQuery(route));
   const path=route.view==='resource'?encodedRoutePath("get_saved_resources_id_content", {"id": encodeURIComponent(route.resourceId!)}):route.view==='history'?routePath("get_personal_history"):routePath("get_saved_resources");
   if(route.view==='collections'||route.view==='views'){query.set('kind',route.view==='collections'?'collection':'view');if('shape'in route&&route.shape)query.set('shape',route.shape);}
   if(route.view==='history'&&'period'in route&&route.period)query.set('period',route.period);
   const raw=await this.request<unknown>(path+'?'+query,'GET',undefined,signal);
   if(!obj(raw)||!str(raw.nextCursor,4096))bad();let patch:{-readonly [K in keyof PersonalSavedSnapshot]?:PersonalSavedSnapshot[K]}&{viewerFence:string;revision:number;nextCursor:string}={page:null,resource:null,resources:[],entries:[],browse:null,nextCursor:raw.nextCursor,cursor,history:[...history],viewerFence:'',revision:0};
   if(route.view==='resource'){
    const r=resource(raw.resource,this.scope);if(r.id!==route.resourceId)bad();patch.resource=r;patch.viewerFence=r.viewerFence;patch.revision=r.revision;
    if(r.kind==='view'&&r.status==='ready'){
     // The saved query runs on the server's browse engine; the client re-parses
     // the page it returns rather than re-deriving membership from the query.
     const page=parseBrowseResult(raw.browse);
     if(page.pivot!==r.definition!.pivot)bad();
     if(!obj(raw.browse)||!Array.isArray(raw.browse.entries))bad();
     const offered=raw.browse.entries;
     patch.browse=page;patch.entries=page.entries.map(media=>{
      const original=offered.find(value=>obj(value)&&value.id===media.id&&value.kind===media.kind&&value.libraryId===media.libraryId);
      if(!obj(original))bad();
      return {id:media.id,hidden:false,media:validateHomeEntry({...media,homeActions:original.homeActions})};
     });patch.nextCursor=page.pageInfo.nextCursor;
    }else{patch.entries=distinct(keepReadable('personal-saved','entries',rows(raw.entries),e=>{if(!obj(e)||!id(e.id)||typeof e.hidden!=='boolean')bad();return {id:e.id,hidden:e.hidden,...(!e.hidden?{media:validateHomeEntry(e.media)}:{})};}),'entries');
     if(r.kind==='collection')patch.page=resourcePage(raw,resourceContentQuery(route));}
   }else{
    if(raw.serverId!==this.scope.serverId||!id(raw.viewerFence)||!num(raw.revision))bad();patch.viewerFence=raw.viewerFence;patch.revision=raw.revision;
    if(route.view==='history')patch.entries=distinct(keepReadable('personal-saved','history',rows(raw.entries),e=>{e=wireDurations(e,{positionSeconds:'positionMs'});if(!obj(e)||!id(e.id)||!str(e.updatedAt,128)||typeof e.positionSeconds!=='number'||!Number.isFinite(e.positionSeconds)||e.positionSeconds<0||typeof e.completed!=='boolean')bad();return {id:e.id,hidden:false,media:validateContentEntry(e.media),updatedAt:e.updatedAt,positionSeconds:e.positionSeconds,completed:e.completed};}),'history');
    // A row from another server or another viewer fence refuses the whole directory; any other row this app cannot read is left out alone.
    else{if(raw.shapeCounts!==undefined)patch.shapeCounts=parseSavedShapeCounts(raw.shapeCounts);const listed=rows(raw.resources);for(const v of listed)if(obj(v)&&(v.serverId!==this.scope.serverId||v.viewerFence!==raw.viewerFence))bad();patch.resources=distinct(keepReadable('personal-saved','resources',listed,v=>{const r=resource(v,this.scope);if(r.kind!==(route.view==='collections'?'collection':'view'))bad();return r;}),'resources');}
   }
   return patch;
 }
 async loadLibraries():Promise<void>{const g=this.generation;try{const raw=await this.request<RouteResponse<"get_libraries">>(routePath("get_libraries"));if(!this.alive(g))return;if(!obj(raw))bad();const libraries=distinct(keepReadable('personal-saved','libraries',rows(raw.items,2048),v=>{if(!obj(v)||!id(v.id)||!str(v.name,512)||!id(v.kind))bad();return{id:v.id,name:v.name,kind:v.kind};}),'libraries');this.publish({libraries});}catch(e){if(this.alive(g))this.publish({error:info(e)});}}
 async loadCandidates(cursor=''):Promise<void>{const r=this.state.resource,g=this.generation,ticket=++this.candidateTicket;if(!r?.actions.includes('share'))return;
  try{const raw=await this.request<RouteResponse<"get_saved_resources_id_share_candidates">>(encodedRoutePath("get_saved_resources_id_share_candidates", {"id": encodeURIComponent(r.id)}) + "?" + (new URLSearchParams({limit:'40',...(cursor?{cursor}:{})})));if(!this.alive(g)||ticket!==this.candidateTicket)return;
   if(!obj(raw)||raw.serverId!==this.scope.serverId||raw.viewerFence!==r.viewerFence||raw.resourceId!==r.id||raw.revision!==r.revision||!str(raw.nextCursor,4096))bad();const candidates=distinct(keepReadable('personal-saved','candidates',rows(raw.candidates),c=>{if(!obj(c)||!id(c.id)||!str(c.displayName,512))bad();return {...actor(c),id:c.id,displayName:c.displayName};}),'candidates');this.publish({candidates,candidateCursor:raw.nextCursor});
  }catch(e){if(this.alive(g)&&ticket===this.candidateTicket)this.publish({error:info(e)});}}
 async mutate(intent:PersonalSavedIntent,rebased=false):Promise<boolean>{
  if(this.disposed||this.state.loading||this.state.pending||this.command)throw new Error('Finish or dismiss the outstanding change before starting another.');
  const r=this.state.resource,g=this.generation,route=this.state.route;if(!route||!this.state.viewerFence)throw new Error('Reload Saved before changing it.');
  let path: string=routePath("post_saved_resources"),method='POST',body:Record<string,unknown>={expectedRevision:r?.revision??0};
  if(intent.action==='create'){if(route.view!=='collections'&&route.view!=='views')throw new Error('Open the matching resource directory.');Object.assign(body,{kind:intent.kind,name:intent.name,summary:intent.summary??'',...(intent.visibility?{visibility:visibility(intent.visibility)}:{}),...(intent.definition?{definition:definition(intent.definition)}:{})});}
  else if(intent.action==='clear-history'||intent.action==='reset-viewing-activity'){if(route.view!=='history')throw new Error('Open viewing history.');path=routePath("post_personal_history_actions");body={expectedRevision:this.state.revision,action:intent.action};}
  else{
   const action=intent.action==='unshare'?'share':intent.action;if(!r||!r.actions.includes(action))throw new Error('This action is not currently available to your profile.');const resourceId=encodeURIComponent(r.id);
   switch(intent.action){
    case 'update':path=encodedRoutePath('patch_saved_resources_id',{id:resourceId});method='PATCH';Object.assign(body,{...(intent.name!==undefined?{name:intent.name}:{}),...(intent.summary!==undefined?{summary:intent.summary}:{}),...(intent.visibility!==undefined?{visibility:visibility(intent.visibility)}:{}),...(intent.definition?{definition:definition(intent.definition)}:{})});break;
    case 'delete':path=encodedRoutePath('delete_saved_resources_id',{id:resourceId});method='DELETE';break;
    case 'entries':method='PUT';path=encodedRoutePath('put_saved_resources_id_entries',{id:resourceId});if(intent.undoToken!==undefined){if(!id(intent.undoToken)||intent.addItemIds?.length||intent.removeEntryIds?.length)throw new Error('Invalid collection entries.');body.undoToken=intent.undoToken;break;}if((intent.addItemIds?.length??0)>100||(intent.removeEntryIds?.length??0)>100||intent.addItemIds?.some(x=>!id(x))||intent.removeEntryIds?.some(x=>!id(x)))throw new Error('Invalid collection entries.');Object.assign(body,{addItemIds:intent.addItemIds??[],removeEntryIds:intent.removeEntryIds??[]});break;
    case 'reorder':if(route.view!=='resource'||!resourceContentManual(resourceContentQuery(route)))throw new Error('Moves are made in the collection’s own order.');method='PUT';path=encodedRoutePath('reorder_saved_collection',{id:resourceId});if(!intent.entryIds.length||intent.entryIds.length>200||intent.entryIds.some(x=>!id(x))||new Set(intent.entryIds).size!==intent.entryIds.length||intent.afterEntryId!==undefined&&intent.afterEntryId!==''&&(!id(intent.afterEntryId)||intent.entryIds.includes(intent.afterEntryId)))throw new Error('Invalid collection order.');Object.assign(body,{entryIds:[...intent.entryIds],...(intent.afterEntryId!==undefined?{afterEntryId:intent.afterEntryId}:{})});break;
    case 'share':case 'unshare':path=encodedRoutePath(intent.action==='share'?'put_saved_resources_id_shares':'delete_saved_resources_id_shares',{id:resourceId});method=intent.action==='share'?'PUT':'DELETE';Object.assign(body,actor(intent.actor));if(intent.action==='share')body.role=intent.role;break;
    case 'pin':path=encodedRoutePath("put_saved_pins_kind_id", {"kind": r.kind, "id": encodeURIComponent(r.id)});method='PUT';body={expectedRevision:r.pinRevision,pinned:intent.pinned};break;
   }
  }
  this.publish({pending:true,mutationError:null,result:null});
  try{const operationId=await this.bounded(()=>this.requestId());if(!this.alive(g))return false;if(!uuid.test(operationId)&&!/^p251_[0-9]{13}_[a-f0-9]{32}$/.test(operationId))throw new Error('A UUIDv4 operation ID is required.');body.operationId=operationId;this.command={intent:freeze({...intent}),path,method,body:freeze(body),fence:this.state.viewerFence,...(rebased?{rebased}:{})};return await this.send();}
  catch(e){if(this.alive(g))this.publish({pending:false,mutationError:info(e),retryPending:!!this.command});return false;}
 }
 private async send():Promise<boolean>{const c=this.command,g=this.generation;if(!c||this.disposed)return false;this.publish({pending:true,mutationError:null});
  try{const raw=await this.request<unknown>(c.path,c.method,c.body);if(!this.alive(g))return false;if(obj(raw)&&obj(raw.mutationReceipt)&&['applied','deleted','removed'].includes(String(raw.mutationReceipt.status))){await this.refresh();if(!this.alive(g))return false;this.command=undefined;this.publish({pending:false,retryPending:false,mutationError:null,result:null});await this.api.acknowledgeUserMutation?.(String(c.body.operationId));return true;}if(!obj(raw)||raw.serverId!==this.scope.serverId||raw.viewerFence!==c.fence)bad();const receipt=c.intent.action==='pin'?raw:raw.receipt;if(!obj(receipt)||receipt.operationId!==c.body.operationId||!num(receipt.revision))bad();const activity=c.intent.action==='clear-history'||c.intent.action==='reset-viewing-activity';if(!activity&&(!id(receipt.resourceId)||typeof receipt.deleted!=='boolean'))bad();
   if(c.intent.action!=='create'&&!activity&&receipt.resourceId!==this.state.resource?.id)bad();
   const deleted=obj(raw.current)&&raw.current.resourceId===receipt.resourceId&&typeof raw.current.deleted==='boolean'?raw.current.deleted:receipt.deleted===true;
   const entries=c.intent.action==='entries'&&receipt.entries!==undefined?parseEntryOutcomes(receipt.entries):undefined;
   const undo=c.intent.action==='entries'&&id(receipt.undoToken)&&str(receipt.undoUntil,64)?{undo:{token:receipt.undoToken,until:receipt.undoUntil}}:{};
   if(entries?.failed.length){this.publish({pending:false,retryPending:true,mutationError:null,result:{entries,...undo,...(typeof receipt.resourceId==='string'?{resourceId:receipt.resourceId}:{}),deleted}});await this.refresh();return false;}
   this.command=undefined;this.publish({pending:false,retryPending:false,mutationError:null,result:{...(entries?{entries}:{}),...undo,...(typeof receipt.resourceId==='string'?{resourceId:receipt.resourceId}:{}),deleted},candidates:[],candidateCursor:''});
   // A list paged further than its first page stays where the viewer is (a move, a removal deep in a collection).
   if(!deleted&&!((this.state.history.length||this.seen>PAGE)&&await this.readThrough({}).catch(()=>false)))await this.refresh();await this.api.acknowledgeUserMutation?.(String(c.body.operationId));return this.alive(g);
  }catch(e){if(!this.alive(g))return false;const error=info(e);this.publish({pending:false,retryPending:true,mutationError:error});if(error.conflict)await this.refresh();
   return false;}
 }
 /** Network retry is byte-identical. Rebase is a separate, explicit reviewed intent. */
 retry=()=>this.state.pending?Promise.resolve(false):this.send();
 async rebase():Promise<boolean>{const c=this.command;if(!c||this.state.pending||!this.state.mutationError?.conflict)return false;await this.api.dismissReviewedUserMutation?.(String(c.body.operationId));this.command=undefined;return this.mutate(c.intent);}
 dismiss():void{if(this.state.pending)return;const command=this.command;const finish=()=>{if(this.command===command){this.command=undefined;this.publish({retryPending:false,mutationError:null});}};if(command&&this.api.dismissReviewedUserMutation)void this.api.dismissReviewedUserMutation(String(command.body.operationId)).then(finish,error=>this.publish({mutationError:info(error)}));else finish();}
 dispose():void{this.disposed=true;this.generation++;for(const c of this.controllers)c.abort();this.controllers.clear();this.command=undefined;this.listeners.clear();}
}
