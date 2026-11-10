import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** One server-owned show workspace; episode details and playback have separate scoped owners. */
import {validateContentEntry,validateContentProjection,type ContentEntry,type ContentProjection,type ContentScope,type LibraryContentApi} from './library-content.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';
import {QueryObserver} from '@tanstack/query-core';
import type {ViewerQueries} from './query/viewer-queries.ts';
import type {ViewerQueryOptions} from './query/react.ts';
import {queryKeys} from './query/keys.ts';
import {entriesFromItems,entryFromItem,readAllItems,readItem,readItems} from './items.ts';
import {childrenQuery} from './library-browse.ts';
import type {ServerRequester} from './query/server-api.ts';
import {dependsOn, titlesWithPersonalState} from './freshness-hub.ts';
import {linkAbortSignals} from './abort-lifetime.ts';
export type ShowWorkspaceRoute=Readonly<{libraryId:string;episodeDirection?:'asc'|'desc';playState?:'all'|'played'|'unplayed'|'in-progress';showId?:string;seasonId?:string;episodeId?:string;selectedSeasonId?:string;group?:string}>;
/** How the owner asked a show to be presented (Spec — Title Pages §4). A flat show (seasons hidden, or ranges) lists its episodes under groups, not seasons. */
export type ShowSettings=Readonly<{hideSeasons:boolean;absoluteNumbering:boolean;ranges:boolean;newestFirst:boolean;revision:number}>;
export const defaultShowSettings:ShowSettings=Object.freeze({hideSeasons:false,absoluteNumbering:false,ranges:false,newestFirst:false,revision:0});
/** The episode groups a workspace can name: unassigned absolute episodes, a flat show's whole list, its specials, or a range of a hundred. */
const SHOW_GROUP=/^(unassigned_absolute|all|specials|range:\d{1,7})$/;
const group=(v:unknown):v is string=>typeof v==='string'&&SHOW_GROUP.test(v);
/** One of a show's people (cast first, then creators, writers and directors), from the server's show credits. */
export type WorkspaceShowCredit=Readonly<{id?:string;name:string;role?:string;department?:string;ordinal:number;portraitUrl?:string}>;
export type WorkspaceShow=Readonly<{episodeCount?:number;addedAt?:string;originalLanguage?:string;status?:string;firstAired?:string;lastAired?:string;id:string;libraryId:string;title:string;credits?:readonly WorkspaceShowCredit[];year?:number;overview?:string;posterUrl?:string;backdropUrl?:string;logoUrl?:string;network?:string;studio?:string;contentRating?:string;originalTitle?:string;country?:string;genres?:readonly string[];providerSourceUrl?:string;providerObservedAt?:string;libraryKind:'tv'|'anime';providerMatchStatus:string}>;
export type WorkspaceSeason=Readonly<{episodeCount?:number;watchedCount?:number;unwatchedCount?:number;firstAired?:string;lastAired?:string;overview?:string;id:string;showId:string;number:number;title:string}>;
export type ShowWorkspaceData=Readonly<{scope:Readonly<{serverId:string;libraryId:string}>;show:WorkspaceShow;seasons:readonly WorkspaceSeason[];seasonTotalCount:number;nextSeasonCursor:string;groups:readonly Readonly<{id:string;title:string;count:number}>[];selected:Readonly<{seasonId:string|null;group?:string;episodeId?:string}>;episodes:ContentProjection;nextUp?:ContentEntry;settings:ShowSettings}>;
export type ShowWorkspaceAnchor=Readonly<{expandedEpisodeId:string|null;focusedEpisodeId:string|null;scrollOffset:number}>;
export type ShowWorkspaceRestore=Readonly<{scope:ContentScope;route:ShowWorkspaceRoute;cursor:string|null;history:readonly (string|null)[];seasonCursor:string|null;seasonHistory:readonly (string|null)[];anchor:ShowWorkspaceAnchor;pageAnchorEpisodeId:string|null;pageAnchors:readonly string[];resolvedSelection:Readonly<{showId:string;seasonId:string|null;group?:string}>}>;
export type ShowWorkspaceError=Readonly<{code:string;message:string;retryable:boolean}>;
export type ShowWorkspaceSnapshot=Readonly<{generation:number;scope:ContentScope;route:ShowWorkspaceRoute|null;phase:'idle'|'loading'|'ready'|'error'|'refresh-required';data:ShowWorkspaceData|null;pagination:Readonly<{cursor:string|null;history:readonly (string|null)[];nextCursor:string|null;canPrevious:boolean}>;seasonPagination:Readonly<{cursor:string|null;history:readonly (string|null)[];nextCursor:string|null;canPrevious:boolean}>;anchor:ShowWorkspaceAnchor;error:ShowWorkspaceError|null}>;
const record=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const str=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>str(v,256)&&!!v;
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
class Failure extends Error{code:string;retryable:boolean;constructor(code:string,message:string,retryable=false){super(message);this.code=code;this.retryable=retryable;}}
function invalid():never{throw new Failure('invalid_show_workspace',unreadableServerResponse);}
function arr(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
const blankAnchor=():ShowWorkspaceAnchor=>Object.freeze({expandedEpisodeId:null,focusedEpisodeId:null,scrollOffset:0});
function bound(v:ContentScope):ContentScope{if(!record(v)||!id(v.serverId)||!str(v.viewerId,1024)||!v.viewerId)throw new Error('A server and composed authority/account/profile scope is required.');return Object.freeze({serverId:v.serverId,viewerId:v.viewerId});}
export function validateShowWorkspaceRoute(v:ShowWorkspaceRoute):ShowWorkspaceRoute{if(!record(v)||!id(v.libraryId)||!v.showId&&!v.seasonId&&!v.episodeId||v.group!==undefined&&!group(v.group)||v.group!==undefined&&v.selectedSeasonId!==undefined)throw new Error('Invalid show workspace target.');for(const k of Object.keys(v))if(!['libraryId','episodeDirection','playState','showId','seasonId','episodeId','selectedSeasonId','group'].includes(k))throw new Error('Unknown show workspace route field.');const result:{-readonly[K in keyof ShowWorkspaceRoute]:ShowWorkspaceRoute[K]}={libraryId:v.libraryId};for(const k of ['showId','seasonId','episodeId','selectedSeasonId'] as const)if(v[k]!==undefined){if(!id(v[k]))throw new Error('Invalid show workspace identifier.');result[k]=v[k];}if(v.episodeDirection!==undefined){if(!['asc','desc'].includes(v.episodeDirection))throw new Error('Invalid episode direction.');result.episodeDirection=v.episodeDirection;}if(v.playState!==undefined){if(!['all','played','unplayed','in-progress'].includes(v.playState))throw new Error('Invalid episode playState.');result.playState=v.playState;}if(v.group)result.group=v.group;return Object.freeze(result);}
function anchor(v:ShowWorkspaceAnchor):ShowWorkspaceAnchor{if(!record(v)||v.expandedEpisodeId!==null&&!id(v.expandedEpisodeId)||v.focusedEpisodeId!==null&&!id(v.focusedEpisodeId)||typeof v.scrollOffset!=='number'||!Number.isFinite(v.scrollOffset)||v.scrollOffset<0||v.scrollOffset>10000000)throw new Error('Invalid show workspace anchor.');return Object.freeze({...v});}
function cursor(v:unknown):string|null{if(v===null)return null;if(!str(v,4096)||!v)throw new Error('Invalid show workspace continuation.');return v;}
const present=<T extends Record<string,unknown>>(value:T):T=>{for(const key of Object.keys(value))if(value[key]===undefined||value[key]===null||value[key]==='')delete value[key];return value;};
/** A show's page from the server's items: the show, its seasons, and one page of the chosen season's episodes in the order asked for. */
async function readShow(api:ServerRequester,r:ShowWorkspaceRoute,s:ContentScope,pageSize:number,cursor:string|null,signal:AbortSignal):Promise<ShowWorkspaceData>{
 // A link to a season or an episode opens its show on that season.
 const opened=r.showId?undefined:await readItem(api,(r.episodeId??r.seasonId)!,signal);
 const showId=r.showId??opened?.rootId??opened?.parentId;if(!showId)invalid();
 const [show,seasonItems]=await Promise.all([readItem(api,showId,signal),readAllItems(api,childrenQuery(showId,'season'),signal)]);
 if(show.kind!=='show'||show.libraryId!==r.libraryId)throw new Failure('not_found','This show is not in this library.');
 const seasons=seasonItems.filter(season=>season.kind==='season').map(season=>Object.freeze(present({id:season.id,showId,number:season.index??0,title:season.title,episodeCount:season.counts?.leaves,
  watchedCount:season.counts?season.counts.leaves-season.counts.unwatchedLeaves:undefined,unwatchedCount:season.counts?.unwatchedLeaves}) as WorkspaceSeason));
 // Specials are their own season and are not where a show opens.
 const wanted=r.selectedSeasonId??r.seasonId??(opened?.kind==='episode'?opened.parentId:opened?.kind==='season'?opened.id:undefined);
 const selected=seasons.find(season=>season.id===wanted)??seasons.find(season=>season.number>0)??seasons[0];
 const direction=r.episodeDirection??'asc';
 const page=selected?await readItems(api,{...childrenQuery(selected.id,'episode'),order:direction,...(r.playState==='played'?{watched:true}:r.playState==='unplayed'?{watched:false}:{})},{cursor:cursor??undefined,limit:pageSize,total:true},signal):{items:[]};
 let entries=entriesFromItems(page.items,r.libraryId).filter(entry=>entry.kind==='episode');
 if(r.playState==='in-progress')entries=entries.filter(entry=>!!entry.progressSeconds&&!entry.watched);
 const heading=Object.freeze({key:'library.episodes',fallback:'Episodes'});
 const episodes:ContentProjection=Object.freeze({heading,scope:Object.freeze({serverId:s.serverId,libraryId:r.libraryId,libraryKind:'tv',view:selected?'season':'show',entityId:selected?.id??showId}),navigation:Object.freeze([]),
  query:Object.freeze({sort:'episode',direction,category:'',q:'',limit:pageSize,searchMode:'none'}),sorts:Object.freeze([]),filters:Object.freeze([]),
  sections:Object.freeze(selected?[Object.freeze({id:'episodes',type:'list' as const,heading,entries:Object.freeze(entries),totalCount:page.total??entries.length,nextCursor:page.nextCursor??''})]:[])});
 const tag=(kind:string)=>show.tags?.find(each=>each.kind===kind)?.name,card=entryFromItem(show,r.libraryId);
 const credits=(show.credits??[]).map((credit,ordinal)=>Object.freeze(present({id:credit.personId,name:credit.name,role:credit.character||credit.role,department:['actor','guest'].includes(credit.role)?'cast':credit.role,ordinal,portraitUrl:credit.image}) as WorkspaceShowCredit));
 const genres=(show.tags??[]).filter(each=>each.kind==='genre').map(each=>each.name);
 const parsedShow=Object.freeze(present({id:show.id,libraryId:r.libraryId,title:show.title,libraryKind:'tv' as const,providerMatchStatus:'',year:show.year,overview:show.summary,posterUrl:card?.posterUrl,backdropUrl:card?.backdropUrl,logoUrl:show.images?.logo,
  network:tag('network'),studio:tag('studio'),contentRating:show.contentRating,originalTitle:show.originalTitle,addedAt:show.addedAt,episodeCount:show.counts?.leaves,
  ...(genres.length?{genres:Object.freeze(genres)}:{}),...(credits.length?{credits:Object.freeze(credits)}:{})}) as WorkspaceShow);
 // The episode the main action plays: the one in progress, else the first not yet watched, in the show's order.
 const nextUp=entries.find(entry=>!!entry.progressSeconds&&!entry.watched)??entries.find(entry=>!entry.watched);
 return Object.freeze({scope:Object.freeze({serverId:s.serverId,libraryId:r.libraryId}),show:parsedShow,seasons:Object.freeze(seasons),seasonTotalCount:seasons.length,nextSeasonCursor:'',groups:Object.freeze([]),
  selected:Object.freeze({seasonId:selected?.id??null,...(r.episodeId?{episodeId:r.episodeId}:{})}),episodes,...(nextUp?{nextUp}:{}),settings:defaultShowSettings});
}
const SHOW_DEADLINE_MS=15000;
/** One page of a show (its route, episode page and season page): a document in the viewer's cache, read once and kept current by the hub. */
export function showWorkspaceQuery(queries:ViewerQueries,r:ShowWorkspaceRoute,cursor:string|null,seasonCursor:string|null,pageSize=40,seasonSize=128):ViewerQueryOptions<ShowWorkspaceData>{
 const params=new URLSearchParams();for(const [k,v]of Object.entries(r))if(k!=='libraryId'&&v!==undefined)params.set(k,v);
 params.set('limit',String(pageSize));params.set('seasonLimit',String(seasonSize));if(cursor)params.set('cursor',cursor);if(seasonCursor)params.set('seasonCursor',seasonCursor);
 return {queryKey:queryKeys.showWorkspace(r.libraryId,params.toString()),
  queryFn:async({signal})=>{
   const deadline=linkAbortSignals([signal],SHOW_DEADLINE_MS);
   try{return await readShow(queries.api,r,queries.scope,pageSize,cursor,deadline.signal);}
   catch(error){if(deadline.signal.aborted&&!signal.aborted)throw new Failure('timeout','The server took too long to respond.',true);throw error;}
   finally{deadline.dispose();}
  },
  meta:{dependencies:[dependsOn.libraries,...titlesWithPersonalState(r.libraryId),dependsOn.event('stream.resync')]}};
}
type Operation={fresh?:boolean;route:ShowWorkspaceRoute;cursor:string|null;history:(string|null)[];seasonCursor:string|null;seasonHistory:(string|null)[];anchor:ShowWorkspaceAnchor;pageAnchors:string[];returning:{afterPlayback?:boolean;afterMetadata?:boolean}|null};
export class ShowWorkspaceService{
 private readonly queries:ViewerQueries;private scope:ContentScope;private pageSize:number;private seasonSize:number;private timeout:number;private generation=0;private disposed=false;private controller?:AbortController;private pending?:Promise<void>;private key='';private operation?:Operation;private state:ShowWorkspaceSnapshot;private listeners=new Set<()=>void>();
 /** Keeps the page on screen current: the hub reads it again in place when something it shows moved. */
 private shown?:QueryObserver<ShowWorkspaceData,Error>;private stopShown?:()=>void;
 constructor(o:{queries:ViewerQueries;scope:ContentScope;pageSize?:number;seasonPageSize?:number;timeoutMs?:number}){this.queries=o.queries;this.scope=bound(o.scope);if(this.scope.serverId!==this.queries.scope.serverId)throw new Error('A show is read from its own server\'s cache.');this.pageSize=o.pageSize??40;this.seasonSize=o.seasonPageSize??128;this.timeout=o.timeoutMs??15000;if(!count(this.pageSize)||this.pageSize<1||this.pageSize>100||!count(this.seasonSize)||this.seasonSize<1||this.seasonSize>512||!Number.isFinite(this.timeout)||this.timeout<=0||this.timeout>120000)throw new Error('Invalid show request bounds.');this.state=this.empty();}
 private empty():ShowWorkspaceSnapshot{return Object.freeze({generation:this.generation,scope:this.scope,route:null,phase:'idle',data:null,pagination:Object.freeze({cursor:null,history:Object.freeze([]),nextCursor:null,canPrevious:false}),seasonPagination:Object.freeze({cursor:null,history:Object.freeze([]),nextCursor:null,canPrevious:false}),anchor:blankAnchor(),error:null});}
 getSnapshot=():ShowWorkspaceSnapshot=>this.state;subscribe=(fn:()=>void):(()=>void)=>{if(this.disposed)return()=>{};this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private active(){if(this.disposed)throw new Error('Show workspace is disposed.');}
 private publish(p:Partial<ShowWorkspaceSnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...p});for(const f of this.listeners)f();}
 private stop(){this.generation++;this.controller?.abort();this.pending=undefined;this.key='';this.stopShown?.();this.stopShown=undefined;this.shown=undefined;}
 cancel(){this.active();this.stop();this.operation=undefined;this.publish(this.empty());}
 dispose(){this.stop();this.disposed=true;this.listeners.clear();}
 select(input:ShowWorkspaceRoute,restore?:ShowWorkspaceRestore):Promise<void>{this.active();const route=validateShowWorkspaceRoute(input);let op:Operation={route,cursor:null,history:[],seasonCursor:null,seasonHistory:[],anchor:blankAnchor(),pageAnchors:[],returning:null};if(restore){if(!record(restore)||restore.scope?.serverId!==this.scope.serverId||restore.scope?.viewerId!==this.scope.viewerId||JSON.stringify(validateShowWorkspaceRoute(restore.route))!==JSON.stringify(route))throw new Error('Show restoration belongs to another scope or route.');op={route,cursor:cursor(restore.cursor),history:arr(restore.history,64).map(cursor),seasonCursor:cursor(restore.seasonCursor),seasonHistory:arr(restore.seasonHistory,64).map(cursor),anchor:anchor(restore.anchor),pageAnchors:arr(restore.pageAnchors,64).map(v=>{if(!id(v))invalid();return v;}),returning:{}};}return this.load(op);}
 /** Workspace-only order; reset both cursor histories without changing playback or owner settings. */
 selectEpisodeDirection(direction:'asc'|'desc'):Promise<void>{this.active();const d=this.state.data,r=this.state.route;if(!d||!r||this.state.phase!=='ready')return Promise.resolve();return this.select({libraryId:d.scope.libraryId,showId:d.show.id,...(d.selected.seasonId?{selectedSeasonId:d.selected.seasonId}:d.selected.group?{group:d.selected.group}:{}),playState:r.playState,episodeDirection:direction});}
 selectSeason(id:string):Promise<void>{this.active();const d=this.state.data;if(!d||!d.seasons.some(v=>v.id===id)&&d.selected.seasonId!==id)throw new Error('Choose a server-provided season.');return this.select({libraryId:d.scope.libraryId,showId:d.show.id,selectedSeasonId:id,playState:this.state.route?.playState,episodeDirection:this.state.route?.episodeDirection});}
 selectGroup(group:string):Promise<void>{this.active();const d=this.state.data;if(!d?.groups.some(g=>g.id===group))throw new Error('Choose a server-provided episode group.');return this.select({libraryId:d.scope.libraryId,showId:d.show.id,group,playState:this.state.route?.playState,episodeDirection:this.state.route?.episodeDirection});}
 updateAnchor(input:ShowWorkspaceAnchor){this.active();const a=anchor(input),entries=this.state.data?.episodes.sections[0]?.entries??[];if((a.expandedEpisodeId&&!entries.some(e=>e.id===a.expandedEpisodeId))||(a.focusedEpisodeId&&!entries.some(e=>e.id===a.focusedEpisodeId)))throw new Error('Episode anchor must belong to the current server page.');if(this.operation)this.operation.anchor=a;this.publish({anchor:a});}
 captureRestore():ShowWorkspaceRestore|null{const op=this.operation,d=this.state.data;if(!op||!d||this.state.phase!=='ready')return null;return Object.freeze({scope:this.scope,route:op.route,cursor:op.cursor,history:Object.freeze([...op.history]),seasonCursor:op.seasonCursor,seasonHistory:Object.freeze([...op.seasonHistory]),anchor:this.state.anchor,pageAnchorEpisodeId:d.episodes.sections[0]?.entries[0]?.id??null,pageAnchors:Object.freeze([...op.pageAnchors]),resolvedSelection:Object.freeze({showId:d.show.id,seasonId:d.selected.seasonId,...(d.selected.group?{group:d.selected.group}:{})})});}
 /** Own-playback return reads a fresh server target page; catalog/permission fences stay exact. No stale cursor is reused. */
 restoreAfterPlayback(restore:ShowWorkspaceRestore):Promise<void>{return this.restoreFresh(restore,false);}
 /** Explicit owner metadata publication refresh; never reuses old revision-bound cursors. */
 refreshAfterMetadata(restore:ShowWorkspaceRestore):Promise<void>{return this.restoreFresh(restore,true);}
 private restoreFresh(restore:ShowWorkspaceRestore,metadata:boolean):Promise<void>{
  this.active();if(!record(restore)||restore.scope?.serverId!==this.scope.serverId||restore.scope?.viewerId!==this.scope.viewerId||!record(restore.resolvedSelection)||!id(restore.resolvedSelection.showId)||restore.resolvedSelection.seasonId!==null&&!id(restore.resolvedSelection.seasonId)||restore.resolvedSelection.group!==undefined&&!group(restore.resolvedSelection.group))throw new Error('Invalid playback restoration scope.');
  const original=validateShowWorkspaceRoute(restore.route),selected=restore.resolvedSelection,first=restore.pageAnchorEpisodeId;if(first!==null&&!id(first))throw new Error('Invalid playback page anchor.');
  const route=validateShowWorkspaceRoute({libraryId:original.libraryId,showId:selected.showId,playState:original.playState,episodeDirection:original.episodeDirection,...(selected.seasonId?{selectedSeasonId:selected.seasonId}:selected.group?{group:selected.group}:{}),...(first?{episodeId:first}:{})});
  const pageAnchors=arr(restore.pageAnchors,64).map(v=>{if(!id(v))invalid();return v;});
  return this.load({route,cursor:null,history:[],seasonCursor:null,seasonHistory:[],anchor:anchor(restore.anchor),pageAnchors,returning:{afterPlayback:true,afterMetadata:metadata}});
 }
 nextEpisodes():Promise<void>{return this.page('episodes',true);}previousEpisodes():Promise<void>{return this.page('episodes',false);}nextSeasons():Promise<void>{return this.page('seasons',true);}previousSeasons():Promise<void>{return this.page('seasons',false);}
 private page(kind:'episodes'|'seasons',next:boolean):Promise<void>{this.active();const d=this.state.data,op=this.operation;if(!d||!op||this.state.phase!=='ready')return Promise.resolve();const history=[...(kind==='episodes'?op.history:op.seasonHistory)],current=kind==='episodes'?op.cursor:op.seasonCursor;const pageAnchors=[...op.pageAnchors];if(kind==='episodes'&&!next&&!history.length&&pageAnchors.length){const first=pageAnchors.pop()!;return this.load({...op,route:validateShowWorkspaceRoute({...op.route,episodeId:first}),cursor:null,history:[],pageAnchors,anchor:blankAnchor(),returning:{afterPlayback:true}});}let value:string|null;if(next){const offered=kind==='episodes'?d.episodes.sections[0]?.nextCursor:d.nextSeasonCursor;if(!offered)return Promise.resolve();value=offered;if(history.includes(value)||value===current)throw new Error('Repeated server continuation.');history.push(current);if(history.length>64)history.shift();if(kind==='episodes'){const first=d.episodes.sections[0]?.entries[0]?.id;if(first)pageAnchors.push(first);if(pageAnchors.length>64)pageAnchors.shift();}}else{if(!history.length)return Promise.resolve();value=history.pop()!;if(kind==='episodes')pageAnchors.pop();}return this.load({...op,pageAnchors,...(kind==='episodes'?{cursor:value,history,anchor:blankAnchor()}:{seasonCursor:value,seasonHistory:history}),returning:{}});}
 /** Explicit refresh restarts both bounded pages while preserving the requested show/ancestry. */
 refresh():Promise<void>{this.active();if(!this.operation)return Promise.resolve();const route=this.operation.route;return this.load({fresh:true,route,cursor:null,history:[],seasonCursor:null,seasonHistory:[],anchor:blankAnchor(),pageAnchors:[],returning:null});}
 retry():Promise<void>{this.active();if(!this.operation)return Promise.resolve();return this.state.phase==='refresh-required'?this.refresh():this.load({...this.operation,fresh:true});}
  private load(op:Operation):Promise<void>{
  const key=JSON.stringify(op);if(this.pending&&this.key===key)return this.pending;
  this.stop();this.key=key;this.operation=op;const gen=this.generation,c=new AbortController();this.controller=c;
  const previous=this.state.data;
  const retained=previous&&previous.scope.libraryId===op.route.libraryId&&(op.route.showId===previous.show.id||!!op.route.episodeId&&this.state.route?.episodeId===op.route.episodeId)?previous:null;
  const options=showWorkspaceQuery(this.queries,op.route,op.cursor,op.seasonCursor,this.pageSize,this.seasonSize);
  // A place this viewer has had, with nothing owed, is shown at once: Back reads nothing. A return after playback or an owner's edit always reads the server.
  const held=op.returning||op.fresh?undefined:this.queries.client.getQueryData<ShowWorkspaceData>(options.queryKey);
  if(held&&this.queries.documentFor(options.queryKey)?.getState().stale!==true){
   this.publish({generation:gen,route:op.route,error:null,anchor:op.anchor});
   try{this.apply(op,held);this.watch(op,options);}catch(e){this.failed(op,gen,e,retained);}
   this.key='';return Promise.resolve();
  }
  this.publish({generation:gen,route:op.route,phase:'loading',data:retained,error:null,anchor:op.anchor});
  const promise=(async()=>{try{
   // A read this page abandons (another season, another show) is stopped too, unless another screen shows the same page.
   c.signal.addEventListener('abort',()=>{const query=this.queries.client.getQueryCache().find({queryKey:options.queryKey,exact:true});if(query&&query.getObserversCount()===0)void query.cancel({revert:true});},{once:true});
   const data=await this.deadline(this.queries.client.fetchQuery(op.returning||op.fresh?{...options,staleTime:0}:options),c);
   if(this.disposed||gen!==this.generation)return;
   this.apply(op,data);this.watch(op,options);
  }catch(e){if(this.disposed||gen!==this.generation)return;await this.failed(op,gen,e,retained);
  }finally{if(gen===this.generation){this.pending=undefined;this.key='';}}})();
  this.pending=promise;return promise;
 }
 /** The page's answer for this operation, checked against what it was opened for, published as ready. */
 private apply(op:Operation,data:ShowWorkspaceData):void{
  if(op.returning?.afterMetadata&&[op.anchor.expandedEpisodeId,op.anchor.focusedEpisodeId].some(id=>id&&!data.episodes.sections[0]?.entries.some(e=>e.id===id)))throw new Failure('stale_continuation','The selected episode changed. Refresh this show.');
  const entries=data.episodes.sections[0]?.entries??[];let a=op.anchor;
  if((a.expandedEpisodeId&&!entries.some(e=>e.id===a.expandedEpisodeId))||(a.focusedEpisodeId&&!entries.some(e=>e.id===a.focusedEpisodeId)))throw new Failure('stale_continuation','The requested episode anchor is no longer on this page.',true);
  if(op.route.episodeId&&!op.cursor&&!a.expandedEpisodeId)a=Object.freeze({...a,expandedEpisodeId:op.route.episodeId,focusedEpisodeId:op.route.episodeId});
  op.anchor=a;
  this.publish({phase:'ready',data,anchor:a,pagination:Object.freeze({cursor:op.cursor,history:Object.freeze([...op.history]),nextCursor:data.episodes.sections[0]?.nextCursor||null,canPrevious:!!op.history.length||!!op.pageAnchors.length}),seasonPagination:Object.freeze({cursor:op.seasonCursor,history:Object.freeze([...op.seasonHistory]),nextCursor:data.nextSeasonCursor||null,canPrevious:!!op.seasonHistory.length})});
 }
 /** While this page is shown, a newer answer for it (the hub read it again) is shown in place; an open episode no longer on it is let go. */
 private watch(op:Operation,options:ViewerQueryOptions<ShowWorkspaceData>):void{
  const gen=this.generation;
  this.stopShown?.();this.shown=new QueryObserver<ShowWorkspaceData,Error>(this.queries.client,options);
  this.stopShown=this.shown.subscribe(result=>{
   if(this.disposed||gen!==this.generation||this.operation!==op||this.state.phase!=='ready')return;
   const e=result.error as {code?:unknown;status?:unknown}|null;
   // The place in the show is no longer one the server knows, or the show is gone: the page's own load says which.
   if(e&&(['stale_continuation','cursor_expired','invalid_cursor','invalid_request','not_found','forbidden','unauthorized'].includes(String(e.code))||e.status===401||e.status===403||e.status===404)){void this.refresh().catch(()=>{});return;}
   const data=result.data;if(!data||data===this.state.data)return;
   const entries=data.episodes.sections[0]?.entries??[],held=this.state.anchor;
   const a=(held.expandedEpisodeId&&!entries.some(x=>x.id===held.expandedEpisodeId))||(held.focusedEpisodeId&&!entries.some(x=>x.id===held.focusedEpisodeId))?blankAnchor():held;
   op.anchor=a;
   this.publish({data,anchor:a,pagination:Object.freeze({...this.state.pagination,nextCursor:data.episodes.sections[0]?.nextCursor||null}),seasonPagination:Object.freeze({...this.state.seasonPagination,nextCursor:data.nextSeasonCursor||null})});
  });
 }
 private async failed(op:Operation,gen:number,e:unknown,retained:ShowWorkspaceData|null):Promise<void>{
  const x=e as {code?:unknown;retryable?:unknown};let code=id(x?.code)?x.code:'request_failed';if(op.returning?.afterPlayback&&['invalid_request','not_found'].includes(code))code='stale_continuation';
  // A place in the show the server no longer honours is not the person's problem: open the show again from its first page, keeping what is shown until it arrives.
  if(['stale_continuation','cursor_expired','invalid_cursor'].includes(code)&&(op.cursor||op.seasonCursor||op.returning||op.history.length||op.pageAnchors.length||op.anchor.expandedEpisodeId||op.anchor.focusedEpisodeId)){if(gen===this.generation){this.pending=undefined;this.key='';}await this.load({fresh:true,route:op.route,cursor:null,history:[],seasonCursor:null,seasonHistory:[],anchor:blankAnchor(),pageAnchors:[],returning:null});return;}
  this.publish({phase:['stale_continuation','cursor_expired','invalid_cursor'].includes(code)?'refresh-required':'error',data:['request_failed','timeout','network_error','server_unavailable','service_unavailable'].includes(code)?retained:null,anchor:blankAnchor(),error:Object.freeze({code,message:e instanceof Error?e.message:'Could not load this show.',retryable:x?.retryable===true})});
 }
 /** How long this page waits for the shared read; the read itself goes on for the cache. */
 private async deadline<T>(p:Promise<T>,c:AbortController):Promise<T>{let timer:ReturnType<typeof setTimeout>|undefined,abort:(()=>void)|undefined;try{return await Promise.race([p,new Promise<never>((_,reject)=>{abort=()=>reject(new Failure('cancelled','Show request cancelled.'));c.signal.addEventListener('abort',abort,{once:true});timer=setTimeout(()=>reject(new Failure('timeout','The server took too long to respond.',true)),this.timeout);})]);}finally{if(timer)clearTimeout(timer);if(abort)c.signal.removeEventListener('abort',abort);}}
}

/** Stores the owner's settings for one show; the answer is the settings with their new revision. */
export async function saveShowSettings(api:Pick<LibraryContentApi,'request'>,libraryId:string,showId:string,settings:ShowSettings):Promise<ShowSettings>{
 const raw=await api.request<RouteResponse<"put_admin_libraries_id_shows_show_settings">>(encodedRoutePath("put_admin_libraries_id_shows_show_settings", {"id": encodeURIComponent(libraryId), "show": encodeURIComponent(showId)}),'PUT',settings);
 if(!record(raw)||!count(raw.revision))invalid();
 return Object.freeze({hideSeasons:raw.hideSeasons===true,absoluteNumbering:raw.absoluteNumbering===true,ranges:raw.ranges===true,newestFirst:raw.newestFirst===true,revision:raw.revision as number});
}
