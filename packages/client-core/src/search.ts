import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** Authorized server search projections. No local matching, grouping, ranking or query persistence.
 * Groups are independent: one group may fail while the response as a whole succeeds, and the
 * client renders that group's own failure rather than discarding the results that arrived. */
import type {ContentScope,ContentHeading,LibraryContentApi} from './library-content.ts';
import {validateHomeEntry,type HomeEntry} from './home.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOf,oneOfOr} from './tolerant.ts';
/** How many results each group shows when every group is searched at once (the server's `SearchPreviewLimit`). */
export const SEARCH_PREVIEW_LIMIT=12;
export type SearchGroup='movies'|'shows'|'episodes'|'artists'|'albums'|'songs'|'books'|'reading'|'collections'|'authors'|'people'|'live-tv';
export type SearchSort='relevance'|'title'|'releaseYear'|'dateAdded';
export type SearchDirection='asc'|'desc';
export type SearchGroupStatus='success'|'error';
export type SearchGroupErrorCode=''|'search_group_timeout'|'search_group_unavailable';
export type SearchQuery=Readonly<{q:string;group?:SearchGroup;groups?:readonly SearchGroup[];sort?:SearchSort;direction?:SearchDirection;libraryIds?:readonly string[];record?:boolean}>;
export type SearchEntry=HomeEntry|Readonly<{id:string;kind:'person'|'channel';title:string;subtitle?:string;posterUrl?:string;count?:number;navigation:Readonly<{view:'person'|'channel';entityId:string;sourceId?:string;channelId?:string;provenance?:'live-source'}>}>;
export type SearchGroupResult=Readonly<{id:SearchGroup;labelKey?:string;title:string;entityKind:string;status:SearchGroupStatus;errorCode:SearchGroupErrorCode;items:readonly SearchEntry[];totalCount:number;totalCountExact:boolean;hasMore:boolean;nextCursor:string}>;
export type SearchGroupCapability=Readonly<{id:SearchGroup;labelKey?:string;title:string;entityKind:string;available:boolean;reason:string;sorts:readonly SearchSort[]}>;
export type SearchCapabilities=Readonly<{groups:readonly SearchGroupCapability[];sorts:readonly SearchSort[];directions:readonly SearchDirection[];maxLimit:number;groupBudgetMs:number}>;
export type SearchData=Readonly<{scope:Readonly<{serverId:string;libraryId:'';libraryKind:'mixed';view:'search';entityId:'';viewerFence:string}>;revision:Readonly<{catalog:number;viewer:number}>;heading:ContentHeading;query:Readonly<{q:string;sort:SearchSort;direction:SearchDirection;group:SearchGroup|'';groups:readonly SearchGroup[];libraryIds:readonly string[];limit:number;searchMode:'normalized_prefix';recorded:boolean}>;groups:readonly SearchGroupResult[];capabilities:SearchCapabilities;empty?:ContentHeading}>;
export type SearchSnapshot=Readonly<{generation:number;scope:ContentScope;query:SearchQuery|null;phase:'idle'|'loading'|'ready'|'error'|'refresh-required';data:SearchData|null;groups:readonly SearchGroupResult[];pagination:Readonly<{cursor:string|null;canPrevious:boolean}>;error:Readonly<{code:string;message:string;retryable:boolean}>|null}>;
export type SearchHistoryEntry=Readonly<{q:string;updatedAt:string;uses:number}>;
export type SearchHistoryPage=Readonly<{serverId:string;/** The viewer's own fence, present only when the server's page names one. */viewerFence?:string;/** The server's own remembering switch; absent on older pages. False exposes no entries. */remembered?:boolean;entries:readonly SearchHistoryEntry[];maxEntries:number}>;
const groups:readonly SearchGroup[]=['movies','shows','episodes','artists','albums','songs','books','reading','collections','authors','people','live-tv'];
const sorts:readonly SearchSort[]=['relevance','title','releaseYear','dateAdded'];
const directions:readonly SearchDirection[]=['asc','desc'];
const statuses:readonly SearchGroupStatus[]=['success','error'];
const errorCodes:readonly SearchGroupErrorCode[]=['','search_group_timeout','search_group_unavailable'];
const kinds={movies:'movie',shows:'show',episodes:'episode',artists:'artist',albums:'album',songs:'song',books:'book',reading:'reading',collections:'collection',authors:'author',people:'person','live-tv':'channel'} as const;
const targets={movies:'item',shows:'show',episodes:'item',artists:'artist',albums:'album',songs:'item',books:'book',reading:'item',collections:'collection',authors:'author',people:'person','live-tv':'channel'} as const;
const entityGroups:readonly SearchGroup[]=['people','live-tv'];
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&v.length>0;
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
class Failure extends Error{code:string;retryable:boolean;constructor(code:string,message:string,retryable=false){super(message);this.code=code;this.retryable=retryable;}}
function invalid():never{throw new Failure('invalid_search',unreadableServerResponse);}
function array(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
function heading(v:unknown):ContentHeading{if(!object(v)||!id(v.key)||!text(v.fallback,512))invalid();return Object.freeze({key:v.key,fallback:v.fallback});}
function bound(s:ContentScope):ContentScope{if(!object(s)||!id(s.serverId)||!text(s.viewerId,1024)||!s.viewerId)throw new Error('Search requires a bound server and viewer.');return Object.freeze({...s});}
function names(v:unknown,max:number,allowed?:readonly string[]):readonly string[]{return Object.freeze(array(v,max).map(x=>{if(!id(x)||allowed&&!allowed.includes(x))invalid();return x;}));}
/** How many groups, sorts or directions one answer may name. It leaves room for ones a newer server adds, which are left out one by one, and still refuses an unbounded list. */
const MAX_PUBLISHED=64;
/** The names in a published list that this app knows, each once; one it does not know is left out. The list's bound is exact, and a list the server leaves out is empty. */
function known<T extends string>(part:string,v:unknown,max:number,allowed:readonly T[]):readonly T[]{return Object.freeze([...new Set(keepReadable('search',part,array(v??[],max),x=>oneOf(x,allowed)))]);}
/** A heading that cannot be read is blank, so the results are still shown. */
function headingOr(v:unknown,key:string):ContentHeading{try{return heading(v);}catch{if(v!==undefined&&v!==null)noteSkipped('search','heading');return Object.freeze({key,fallback:''});}}
/** Why a typed query can't be searched (the server's NormalizeSearch rules, CD-30), so a client can
 * say so instead of sending it: `short` (no letters or numbers),
 * `words` (more than 8 words), `long` (over 128 code points). Undefined when it can be searched, or
 * when it is empty (idle). */
export function searchQueryProblem(q:string):'short'|'words'|'long'|undefined{
 const normalized=q.split(/\p{White_Space}+/u).filter(Boolean).join(' ');
 if(normalized==='')return undefined;
 const length=[...normalized].length;
 if(length>128)return 'long';
 if(length<1)return 'short';
 const tokens=normalized.replace(/[^\p{L}\p{N}\p{White_Space}]/gu,'').split(/\p{White_Space}+/u).filter(Boolean);
 if(tokens.length>8)return 'words';
 if(!tokens.length)return 'short';
 return undefined;
}
function query(q:string,options:Omit<SearchQuery,'q'>={}):SearchQuery{
 const {group,groups:restrict,sort,direction,libraryIds,record}=options;
 if(typeof q!=='string'||q.length>2048)throw new Error('Invalid search query.');
 if(group!==undefined&&!groups.includes(group))throw new Error('Unknown search group.');
 if(restrict!==undefined&&(group!==undefined||!restrict.length||restrict.length>groups.length||restrict.some(g=>!groups.includes(g))||new Set(restrict).size!==restrict.length))throw new Error('Invalid search group restriction.');
 if(sort!==undefined&&!sorts.includes(sort))throw new Error('Unknown search sort.');
 if(direction!==undefined&&!directions.includes(direction))throw new Error('Unknown search direction.');
 if(libraryIds!==undefined&&(!libraryIds.length||libraryIds.length>64||libraryIds.some(v=>!id(v))))throw new Error('Invalid library restriction.');
 const normalized=q.split(/\p{White_Space}+/u).filter(Boolean).join(' ');
 // CD-30: match server catalog.NormalizeSearch before dispatch. Empty stays idle
 // (the caller clears without requesting); anything else must be 1–128 code
 // points and 1–8 normalized letter/number words.
 if(normalized==='')return Object.freeze({q:normalized,...(group?{group}:{}),...(restrict?{groups:Object.freeze([...restrict])}:{}),...(sort?{sort}:{}),...(direction?{direction}:{}),...(libraryIds?{libraryIds:Object.freeze([...libraryIds])}:{}),...(record?{record:true as const}:{})});
 const problem=searchQueryProblem(normalized);
 if(problem==='long'||!text(normalized,512))throw new Error('Search supports 1 to 128 characters.');
 if(problem)throw new Error('Invalid search query.');
 return Object.freeze({q:normalized,...(group?{group}:{}),...(restrict?{groups:Object.freeze([...restrict])}:{}),...(sort?{sort}:{}),...(direction?{direction}:{}),...(libraryIds?{libraryIds:Object.freeze([...libraryIds])}:{}),...(record?{record:true as const}:{})});
}
/** A person or channel is an entity row, not a media item: whatever else the server sends with it, the row carries no library, playback or progress. An optional caption, picture or count that cannot be read is left out. */
function entityEntry(v:unknown,group:SearchGroup):SearchEntry{
 if(!object(v)||!id(v.id)||v.kind!==kinds[group]||!text(v.title??'',2048))invalid();
 if(!object(v.navigation)||v.navigation.view!==targets[group]||v.navigation.entityId!==v.id)invalid();
 let anchor:Readonly<{sourceId:string;channelId:string;provenance:'live-source'}>|undefined;
 if(group==='live-tv'&&(v.navigation.sourceId!==undefined||v.navigation.channelId!==undefined||v.navigation.provenance!==undefined)){
  if(!id(v.navigation.sourceId)||v.navigation.channelId!==v.id||v.navigation.provenance!=='live-source')invalid();
  anchor={sourceId:v.navigation.sourceId,channelId:v.id,provenance:'live-source'};
 }
 return Object.freeze({id:v.id,kind:kinds[group] as 'person'|'channel',title:(v.title??'') as string,...(text(v.subtitle,4096)?{subtitle:v.subtitle}:{}),...(text(v.posterUrl,4096)&&v.posterUrl?{posterUrl:v.posterUrl}:{}),...(count(v.count)?{count:v.count}:{}),navigation:Object.freeze({view:targets[group] as 'person'|'channel',entityId:v.id,...anchor})});
}
/** What search offers. Its limits are exact; a group, sort or direction this app does not know is left out of what it offers. */
function capabilities(v:unknown):SearchCapabilities{
 if(!object(v)||!count(v.maxLimit)||!count(v.groupBudgetMs)||!v.maxLimit||!v.groupBudgetMs)invalid();
 const published=firstOfEach('search','capabilities.groups',keepReadable('search','capabilities.groups',array(v.groups,MAX_PUBLISHED),g=>{
  if(!object(g)||!groups.includes(g.id as SearchGroup)||!text(g.title??'',512)||!text(g.reason??'',1024))invalid();
  // A group that does not say it is available is not; one that does not say why reads without a reason.
  return Object.freeze({id:g.id as SearchGroup,title:(g.title??'') as string,...(text(g.labelKey,512)?{labelKey:g.labelKey}:{}),entityKind:kinds[g.id as SearchGroup] as string,available:g.available===true,reason:(g.reason??'') as string,sorts:known('capabilities.group.sorts',g.sorts,MAX_PUBLISHED,sorts)});
 }),g=>g.id);
 return Object.freeze({groups:Object.freeze(published),sorts:known('capabilities.sorts',v.sorts,MAX_PUBLISHED,sorts),directions:known('capabilities.directions',v.directions,MAX_PUBLISHED,directions),maxLimit:v.maxLimit,groupBudgetMs:v.groupBudgetMs});
}
/**
 * One search answer. Its scope, revisions, the query it echoes (text, group, limit, and the sort
 * and direction the client asked for) and its bounds are exact. A group this app does not know,
 * did not ask for, or cannot read is left out alone, and so is a result that cannot be read or is
 * not of its group's kind. A failed group shows only its failure; a status or failure code this
 * app does not know reads as success or as unavailable.
 */
function parse(raw:unknown,op:Operation,s:ContentScope,limit:number):SearchData{
 if(!object(raw)||!object(raw.scope)||!object(raw.revision)||!object(raw.query))invalid();const scope=raw.scope,r=raw.revision,q=raw.query;
 if(scope.serverId!==s.serverId||scope.libraryId!==''||scope.libraryKind!=='mixed'||scope.view!=='search'||scope.entityId!==''||!id(scope.viewerFence)||!count(r.catalog)||!count(r.viewer))invalid();
 if(!text(q.q,512)||!q.q||q.group!==(op.query.group??'')||q.limit!==limit)invalid();
 if(q.q!==op.query.q)invalid();
 // The order the client asked for must be the order answered; an order it left to the server and does not know reads as relevance, best first.
 if(op.query.sort&&q.sort!==op.query.sort||op.query.direction&&q.direction!==op.query.direction)invalid();
 // Recording a search in the viewer's history is the viewer's choice: an answer that says it recorded
 // a search nobody asked it to is refused. One that declined to record (the profile has since turned
 // history off, perhaps on another device) is an ordinary answer.
 if(q.recorded===true&&op.query.record!==true)invalid();
 const sort=oneOfOr('search','query.sort',q.sort,sorts,'relevance'),direction=oneOfOr('search','query.direction',q.direction,directions,'desc');
 const echoedGroups=known('query.groups',q.groups,MAX_PUBLISHED,groups);
 const requested=op.query.group?[op.query.group]:op.query.groups;
 if(requested&&(echoedGroups.length!==requested.length||requested.some(g=>!echoedGroups.includes(g))))invalid();
 const libraryIds=names(q.libraryIds??[],64);
 const published=capabilities(raw.capabilities);
 const results=firstOfEach('search','groups',keepReadable('search','groups',array(raw.groups,MAX_PUBLISHED),value=>{
  if(!object(value)||!groups.includes(value.id as SearchGroup)||!text(value.title??'',512)||!text(value.nextCursor??''))invalid();
  const group=value.id as SearchGroup;
  if(value.entityKind!==kinds[group])invalid();
  if(!echoedGroups.includes(group))invalid();
  const code=value.errorCode===undefined||value.errorCode===null?'':value.errorCode;
  const status:SearchGroupStatus=oneOf(value.status,statuses)??(code?'error':'success');
  // A failed group carries no partial page, and a successful one never carries a failure code.
  if(status==='error')return Object.freeze({id:group,title:(value.title??'') as string,...(text(value.labelKey,512)?{labelKey:value.labelKey}:{}),entityKind:value.entityKind as string,status,errorCode:oneOf(code,errorCodes)||'search_group_unavailable' as const,items:Object.freeze([]),totalCount:0,totalCountExact:false,hasMore:false,nextCursor:''});
  const items=firstOfEach('search','items',keepReadable('search','items',array(value.items,op.query.group?limit:Math.min(limit,SEARCH_PREVIEW_LIMIT)),v=>{
   if(entityGroups.includes(group))return entityEntry(v,group);
   const media=validateHomeEntry(v);
   if(!id(media.libraryId)||(group==='reading'?media.kind!=='ebook'&&media.kind!=='comic':media.kind!==kinds[group])||media.navigation?.view!==targets[group]||media.navigation.entityId!==media.id)invalid();
   if(media.playback&&(!['movies','episodes','songs'].includes(group)||media.playback.itemId!==media.id||media.available===false))invalid();
   return media;
  }),e=>e.id);
  const nextCursor=(value.nextCursor??'') as string;
  // The count is at least the results kept, and more are offered exactly when there is a cursor to ask with.
  return Object.freeze({id:group,title:(value.title??'') as string,...(text(value.labelKey,512)?{labelKey:value.labelKey}:{}),entityKind:value.entityKind as string,status,errorCode:'' as const,items:Object.freeze(items),totalCount:Math.max(count(value.totalCount)?value.totalCount:0,items.length),totalCountExact:value.totalCountExact===true&&count(value.totalCount)&&value.totalCount>=items.length,hasMore:!!nextCursor,nextCursor});
 }),g=>g.id);
 // An answer none of whose groups could be read is refused rather than shown as no results.
 if(!results.length&&(raw.groups as unknown[]).length)invalid();
 let empty:ContentHeading|undefined;if(raw.empty!==undefined&&raw.empty!==null){try{empty=heading(raw.empty);}catch{noteSkipped('search','empty');}}
 return Object.freeze({scope:Object.freeze({serverId:s.serverId,libraryId:'' as const,libraryKind:'mixed' as const,view:'search' as const,entityId:'' as const,viewerFence:scope.viewerFence}),revision:Object.freeze({catalog:r.catalog,viewer:r.viewer}),heading:headingOr(raw.heading,'search'),query:Object.freeze({q:q.q,sort,direction,group:q.group as SearchGroup|'',groups:echoedGroups,libraryIds,limit,searchMode:'normalized_prefix' as const,recorded:q.recorded===true}),groups:Object.freeze(results),capabilities:published,...(empty?{empty}:{})});
}
/** Recent searches are the viewer's own, bounded and separately erasable. The page is
 * the server's own: it names each retained query and when it was last used; a page that
 * names another server is still refused. Older pages that also carry the viewer fence,
 * a depth and `q` remain readable. */
export function parseSearchHistory(raw:unknown,scope:ContentScope):SearchHistoryPage{
 if(!object(raw)||!Array.isArray(raw.entries))invalid();
 if(raw.serverId!==undefined&&raw.serverId!==scope.serverId)invalid();
 if(raw.remembered!==undefined&&typeof raw.remembered!=='boolean')invalid();
 const viewerFence=raw.viewerFence===undefined?undefined:id(raw.viewerFence)?raw.viewerFence:invalid();
 const maxEntries=raw.maxEntries===undefined?20:count(raw.maxEntries)&&raw.maxEntries?raw.maxEntries:invalid();
 // A page whose remembering switch is off exposes no entry at all, whatever it carries.
 const readable=raw.remembered===false?[]:raw.entries;
 // An entry that cannot be read is left out alone; a repeated search keeps its first appearance.
 const entries=firstOfEach('search-history','entries',keepReadable('search-history','entries',array(readable,maxEntries),v=>{
  if(!object(v))invalid();
  const q=text(v.query,512)?String(v.query):text(v.q,512)?String(v.q):'';
  if(!q||!text(v.updatedAt??'',128))invalid();
  return Object.freeze({q,updatedAt:(v.updatedAt??'') as string,uses:count(v.uses)&&v.uses?v.uses:1});
 }),e=>e.q);
 return Object.freeze({serverId:scope.serverId,...(viewerFence?{viewerFence}:{}),...(typeof raw.remembered==='boolean'?{remembered:raw.remembered}:{}),entries:Object.freeze(entries),maxEntries});
}
/** The profile's recent searches, newest first, as the server keeps them (it keeps none while the profile has history turned off). */
export async function readSearchHistory(api:LibraryContentApi,scope:ContentScope,signal?:AbortSignal):Promise<SearchHistoryPage>{
 return parseSearchHistory(await api.request<RouteResponse<"get_search_history">>(routePath("get_search_history"),'GET',undefined,signal),bound(scope));
}
/** Forgets every search the server kept for this profile. */
export async function clearSearchHistory(api:LibraryContentApi,signal?:AbortSignal):Promise<void>{
 await api.request<RouteResponse<"delete_search_history">>(routePath("delete_search_history"),'DELETE',undefined,signal);
}
/** Removes the one retained query the viewer named, at the server's own canonical identity,
 * and answers with the page the server now holds. Nothing is inferred locally. */
export async function deleteSearchHistoryEntry(api:LibraryContentApi,scope:ContentScope,query:string,signal?:AbortSignal):Promise<SearchHistoryPage>{
 const named=query.trim();
 if(!named)throw new Error('invalid_search_history_query');
 const namedQuery=new URLSearchParams({query:named}).toString();
 return parseSearchHistory(await api.request<RouteResponse<"delete_search_history">>(routePath("delete_search_history")+'?'+namedQuery,'DELETE',undefined,signal),bound(scope));
}
export function searchPath(q:SearchQuery,limit:number,cursor?:string|null):string{
 const params=new URLSearchParams({q:q.q,limit:String(limit)});
 if(q.group)params.set('group',q.group);
 if(q.groups)params.set('groups',q.groups.join(','));
 if(q.sort)params.set('sort',q.sort);
 if(q.direction)params.set('direction',q.direction);
 if(q.libraryIds)params.set('libraryIds',q.libraryIds.join(','));
 if(q.record)params.set('record','1');
 if(cursor)params.set('cursor',cursor);
 return routePath("search") + "?" + (params);
}
type Page=Readonly<{query:SearchQuery;cursor:string|null}>;
type Operation=Page&{history:readonly Page[];fence?:SearchData};
const sameReadQuery=(left:SearchQuery|null,right:SearchQuery)=>!!left&&JSON.stringify({...left,record:undefined})===JSON.stringify({...right,record:undefined});
export class SearchService{
 private api:LibraryContentApi;private scope:ContentScope;private readonly pageSize:number;private readonly timeoutMs:number;private generation=0;private disposed=false;private controller?:AbortController;private pending?:Promise<void>;private key='';private operation?:Operation;private displayedOperation?:Operation;private revalidations=new Set<AbortController>();private listeners=new Set<()=>void>();private state:SearchSnapshot;
 constructor(options:{api:LibraryContentApi;scope:ContentScope;pageSize?:number;timeoutMs?:number}){this.api=options.api;this.scope=bound(options.scope);this.pageSize=options.pageSize??40;this.timeoutMs=options.timeoutMs??15000;if(!Number.isInteger(this.pageSize)||this.pageSize<1||this.pageSize>40||!Number.isFinite(this.timeoutMs)||this.timeoutMs<1||this.timeoutMs>60000)throw new Error('Invalid search bounds.');this.state=this.empty();}
 getSnapshot=():SearchSnapshot=>this.state;
 subscribe=(listener:()=>void):(()=>void)=>{this.listeners.add(listener);return()=>this.listeners.delete(listener);};
 private empty():SearchSnapshot{return Object.freeze({generation:this.generation,scope:this.scope,query:null,phase:'idle',data:null,groups:Object.freeze([]),pagination:Object.freeze({cursor:null,canPrevious:false}),error:null});}
 private active(){if(this.disposed)throw new Error('Search service is disposed.');}
 private publish(patch:Partial<SearchSnapshot>){this.state=Object.freeze({...this.state,...patch});for(const listener of this.listeners)listener();}
 select(q:string,options:Omit<SearchQuery,'q'>|SearchGroup={}):Promise<void>{this.active();const checked=query(q,typeof options==='string'?{group:options}:options);if(!checked.q){this.cancel();return Promise.resolve();}return this.load({query:checked,cursor:null,history:[]});}
 next(group:SearchGroup):Promise<void>{this.active();const data=this.state.data,result=data?.groups.find(g=>g.id===group);if(this.state.phase!=='ready'||!result?.nextCursor||!this.operation)return Promise.resolve();return this.load({query:query(data!.query.q,{...this.operation.query,group,groups:undefined}),cursor:result.nextCursor,history:[...this.operation.history,{query:this.operation.query,cursor:this.operation.cursor}].slice(-64),fence:data!});}
 /** An independent continuation lifetime for a genuinely offered group. The seed is the
  * already validated displayed read; callers cannot supply a cursor or substitute a fence.
  * The parent projection stays unchanged while the child uses ordinary next/retry/abort. */
 forkContinuation(group:SearchGroup):SearchService|undefined{
  this.active();
  const data=this.state.data,result=data?.groups.find(value=>value.id===group);
  if(this.state.phase!=='ready'||!result?.nextCursor||!this.operation||!this.displayedOperation)return;
  const child=new SearchService({api:this.api,scope:this.scope,pageSize:this.pageSize,timeoutMs:this.timeoutMs});
  // Continuing an offered read is never another explicit history-recording intent.
  const seed={...this.displayedOperation,query:{...this.displayedOperation.query,record:false}};
  child.state=this.state;child.operation=seed;child.displayedOperation=seed;
  return child;
 }
 previous():Promise<void>{this.active();const op=this.operation;if(this.state.phase!=='ready'||!op?.history.length||!this.state.data)return Promise.resolve();return this.load({...op.history.at(-1)!,history:op.history.slice(0,-1),fence:this.state.data});}
 refresh():Promise<void>{this.active();const q=this.state.query;return q?this.load({query:q,cursor:null,history:[]}):Promise.resolve();}
 retry():Promise<void>{this.active();if(this.state.phase==='refresh-required')return this.refresh();return this.operation?this.load(this.operation):Promise.resolve();}
 /**
  * Run the search that is showing again without touching `phase`, and show the answer in place.
  * A failed read rejects and changes nothing.
  */
 async revalidate(context?:{signal?:AbortSignal;settle?:()=>Promise<void>}):Promise<void>{
  this.active();const op=this.operation;
  if(!op||this.state.phase!=='ready'||!this.state.data||context?.signal?.aborted)return;
  const generation=this.generation,controller=new AbortController(),stop=()=>controller.abort(),scope=this.scope;let timer:ReturnType<typeof setTimeout>|undefined;
  this.revalidations.add(controller);
  context?.signal?.addEventListener('abort',stop,{once:true});
  try{
   const raw=await Promise.race([this.api.request<unknown>(searchPath(op.query,this.pageSize,op.cursor),'GET',undefined,controller.signal),new Promise<never>((_,reject)=>{timer=setTimeout(()=>{reject(new Failure('timeout','Search took too long. Try again.',true));controller.abort();},this.timeoutMs);})]);
   if(this.disposed||generation!==this.generation)return;
   const data=parse(raw,op,scope,this.pageSize);
   await context?.settle?.();
   if(this.disposed||generation!==this.generation||controller.signal.aborted||this.operation!==op||this.state.phase!=='ready')return;
   if(JSON.stringify(data)===JSON.stringify(this.state.data))return;
   this.publish({data,groups:data.groups});
  }catch(error){
   if(this.disposed||generation!==this.generation||controller.signal.aborted)return;
   const e=error as {code?:unknown;status?:number};
   if(e?.status===401||e?.status===403||['stale_continuation','invalid_cursor','invalid_search'].includes(String(e?.code))){this.invalidateRead();await this.refresh();return;}
   throw error;
  }finally{if(timer)clearTimeout(timer);context?.signal?.removeEventListener('abort',stop);this.revalidations.delete(controller);}
 }
 private invalidateRead(){this.displayedOperation=undefined;this.publish({data:null,groups:Object.freeze([])});}
 /** Stop reads when a retained Search view is hidden. Only a page that actually
  * arrived may remain ready, with that page's own query/cursor/history. A pending
  * replacement never supplies facts, and the caller still owns route authority. */
 pause(){this.active();const data=this.state.data,op=this.displayedOperation,phase=this.state.phase==='error'||this.state.phase==='refresh-required'?this.state.phase:'ready',error=phase==='ready'?null:this.state.error;this.stop();
  if(!data||!op||!sameReadQuery(this.state.query,op.query)){this.operation=undefined;this.displayedOperation=undefined;this.publish(this.empty());return;}
  this.operation=op;this.publish({generation:this.generation,query:op.query,phase,data,groups:data.groups,error,pagination:Object.freeze({cursor:op.cursor,canPrevious:op.history.length>0})});
 }
 cancel(){this.active();this.stop();this.operation=undefined;this.displayedOperation=undefined;this.publish(this.empty());}
 setScope(scope:ContentScope,api:LibraryContentApi){this.active();const checked=bound(scope);this.stop();this.scope=checked;this.api=api;this.operation=undefined;this.displayedOperation=undefined;this.publish(this.empty());}
 dispose(){if(this.disposed)return;this.stop();this.disposed=true;this.operation=undefined;this.displayedOperation=undefined;try{this.publish(this.empty());}finally{this.listeners.clear();}}
 private stop(){this.generation++;this.controller?.abort();this.controller=undefined;for(const controller of this.revalidations)controller.abort();this.revalidations.clear();this.pending=undefined;this.key='';}
 private load(op:Operation):Promise<void>{const key=JSON.stringify([op.query,op.cursor]);if(this.pending&&key===this.key)return this.pending;this.stop();this.key=key;this.operation=op;const generation=this.generation,controller=new AbortController();this.controller=controller;const retain=this.state.query?.q===op.query.q&&this.state.query?.group===op.query.group;if(!retain)this.displayedOperation=undefined;this.publish({generation,query:op.query,phase:'loading',...(retain?{}:{data:null,groups:Object.freeze([])}),error:null,pagination:Object.freeze({cursor:op.cursor,canPrevious:op.history.length>0})});if(this.disposed||generation!==this.generation||controller.signal.aborted)return Promise.resolve();const task=this.fetch(op,this.api,this.scope,controller,generation);if(generation===this.generation)this.pending=task;return task;}
 private async fetch(op:Operation,api:LibraryContentApi,scope:ContentScope,controller:AbortController,generation:number){let timer:ReturnType<typeof setTimeout>|undefined;let abort:()=>void=()=>{};
  try{const raw=await Promise.race([api.request<unknown>(searchPath(op.query,this.pageSize,op.cursor),'GET',undefined,controller.signal),new Promise<never>((_,reject)=>{abort=()=>reject(new Failure('cancelled','Search cancelled.'));controller.signal.addEventListener('abort',abort,{once:true});timer=setTimeout(()=>{reject(new Failure('timeout','Search took too long. Try again.',true));controller.abort();},this.timeoutMs);})]);if(this.disposed||generation!==this.generation)return;const data=parse(raw,op,scope,this.pageSize);
   // A later page read after the library moved is still a valid page; only another viewer's answer is refused.
   if(op.fence&&data.scope.viewerFence!==op.fence.scope.viewerFence)throw new Failure('stale_continuation','Search results changed. Refresh to continue.',true);
   // A cursor that comes back again would page for ever: that group has reached its end.
   const repeats=(c:string)=>c===op.cursor||op.history.some(p=>p.cursor===c);
   const groups=data.groups.some(g=>g.nextCursor&&repeats(g.nextCursor))?Object.freeze(data.groups.map(g=>g.nextCursor&&repeats(g.nextCursor)?Object.freeze({...g,nextCursor:''}):g)):data.groups;
   this.displayedOperation=op;this.publish({query:op.query,phase:'ready',data:groups===data.groups?data:Object.freeze({...data,groups}),groups,pagination:Object.freeze({cursor:op.cursor,canPrevious:op.history.length>0})});
  }catch(error){if(this.disposed||generation!==this.generation)return;const e=error as {code?:unknown;retryable?:unknown;status?:number};const code=e?.status===401?'unauthorized':e?.status===403?'forbidden':id(e?.code)?e.code:'request_failed';
   // A continuation the server no longer honours: run the search again from its first page, keeping what is shown until it arrives.
   if(['stale_continuation','invalid_cursor'].includes(code)&&op.cursor){this.invalidateRead();await this.load({query:op.query,cursor:null,history:[]});return;}const transient=!['unauthorized','forbidden','invalid_search','stale_continuation','invalid_cursor'].includes(code)&&(error instanceof TypeError||e?.retryable===true||code==='timeout'||!!e?.status&&(e.status>=500||[408,429].includes(e.status)));if(!transient)this.displayedOperation=undefined;this.publish({phase:['stale_continuation','invalid_cursor'].includes(code)?'refresh-required':'error',...(transient?{}:{data:null,groups:Object.freeze([])}),error:Object.freeze({code,message:['unauthorized','forbidden'].includes(code)?'Your access changed. Sign in again to search.':['stale_continuation','invalid_cursor'].includes(code)?'The library changed. Refresh these results.':'Search could not connect to this server. Try again.',retryable:transient})});}
  finally{if(timer)clearTimeout(timer);controller.signal.removeEventListener('abort',abort);if(generation===this.generation){this.pending=undefined;this.key='';}}
 }
}
