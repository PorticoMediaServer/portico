import {apiRoot} from "../../contracts/src/routes.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOf,oneOfOr} from './tolerant.ts';
import {parseScreenMetadata,screenTarget,type ScreenMetadataContext,type ScreenPolicyInput,type ScreenIdentity,type ScreenOrder,type ScreenProvider} from './screen-metadata.ts';
import {parseMusicPolicy,parseMusicObservation,parseMusicDetails,type MusicPolicy,type MusicPolicyInput,type MusicMatchObservation,type MusicEvidenceDetails} from "./music-metadata.ts";
/** Owner-only provider evidence review. The server owns candidates, ordering and matching. */
export type MetadataReviewScope = Readonly<{serverId:string; viewerId:string}>;
export type MetadataReviewTarget = Readonly<{kind:'show'|'album'|'song'|'movie'; libraryId:string; entityId:string; provider?:'screen'}>;
export interface MetadataReviewApi { request<T>(path:string, method?:string, body?:unknown, signal?:AbortSignal):Promise<T> }
export type TVDBOrder = 'official'|'dvd'|'absolute'|'default'|'alternate'|'regional';
export type MetadataReviewAction = 'select'|'retry'|'search'|'policy'|'map_season';
export type MetadataReviewStatus = 'searching'|'pending_children'|'unmatched'|'matched_work'|'needs_consent'|'source_unavailable'|'provider_disabled'|'needs_parent_match'|'needs_season_mapping'|'identity_conflict'|'manual_preserved'|'delegated_tvdb'|'pending_search'|'pending_episodes'|'pending_apply'|'needs_order'|'complete'|'pending'|'needs_selection'|'matched'|'unresolved'|'unavailable';
export type MetadataReviewCandidate = Readonly<{screen?:Readonly<{identity:ScreenIdentity;orders:readonly ScreenOrder[];contradiction:boolean;strongSignals:number;sourceKind:string;sourceUrl:string;attribution:string}>;id:string; title:string; subtitle?:string; overview?:string; confidence?:number; reasons?:readonly string[]; decision?:'candidate'|'accepted'|'rejected'|'superseded'; observedAt:string; provenance:Readonly<{year?:number; artist?:string; edition?:string}>}>;
export type MetadataPublication = Readonly<{releaseId:string; releaseGroupId:string; recordingId:string; trackId:string; title:string; artist:string; date:string; country:string; observedAt:string; trackTitle:string; trackStatus:string; totalTracks:number; matchedTracks:number; pendingTracks:number; reviewTracks:number; reconciliationPending:boolean;details?:MusicEvidenceDetails}>;
export type MetadataReviewData = Readonly<{scope:Readonly<MetadataReviewScope & {libraryId:string; entityId:string; viewerFence:string}>; provider:'tvdb'|'musicbrainz'|'screen'; revision:number; status:MetadataReviewStatus; selectedId:string; order:string; orders:readonly string[]; screen?:ScreenMetadataContext; actions:readonly MetadataReviewAction[]; candidates:readonly MetadataReviewCandidate[]; attempts:number; nextAttempt:string; providerError:string; attribution:string; manual?:boolean; page?:number; published?:MetadataPublication;policy?:MusicPolicy;observation?:MusicMatchObservation}>;
export type MetadataReviewError = Readonly<{code:string; message:string; retryable:boolean}>;
export type MetadataReviewSnapshot = Readonly<{generation:number; scope:MetadataReviewScope; target:MetadataReviewTarget|null; phase:'idle'|'loading'|'ready'|'error'; data:MetadataReviewData|null; error:MetadataReviewError|null; pending:MetadataReviewAction|null; mutationError:MetadataReviewError|null}>;
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&v.length>0&&!/[\r\n]/.test(v);
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const orders:readonly string[]=['official','dvd','absolute','default','alternate','regional'];
const tvStatuses=['pending_search','pending_episodes','pending_apply','needs_order','complete','needs_selection','unresolved','unavailable'];
const mbStatuses=['pending','needs_selection','matched','unresolved','unavailable'];
const mbId=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
class Failure extends Error { code:string; retryable:boolean; constructor(code:string,message:string,retryable=false){super(message);this.code=code;this.retryable=retryable;} }
function invalid():never {throw new Failure('invalid_metadata_review',unreadableServerResponse);}
function list(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
/** An action or an order this app does not know is not offered, and a repeated one is offered once. The list stays short. */
function offered<T extends string>(part:string,v:unknown,known:readonly string[]):T[]{const values=list(v,16),kept=[...new Set(values.filter(a=>known.includes(a as string)))] as T[];if(kept.length!==values.length)noteSkipped('metadata-review',part,values.length-kept.length);return kept;}
function scope(v:MetadataReviewScope):MetadataReviewScope {if(!object(v)||!id(v.serverId)||!text(v.viewerId,1024)||!v.viewerId)throw new Error('A bound server and viewer scope is required.');return Object.freeze({...v});}
function target(v:MetadataReviewTarget):MetadataReviewTarget {if(!object(v)||!['show','album','song','movie'].includes(v.kind)||!id(v.libraryId)||!id(v.entityId))throw new Error('Invalid provider review target.');return Object.freeze({...v});}
function info(e:unknown):MetadataReviewError {const v=e as {code?:unknown;retryable?:unknown};return Object.freeze({code:id(v?.code)?v.code:'request_failed',message:e instanceof Error?e.message:'Provider review request failed.',retryable:v?.retryable===true});}
function parse(raw:unknown,s:MetadataReviewScope,t:MetadataReviewTarget):MetadataReviewData {
 if(screenTarget(t))return parseScreenMetadata(raw,s,t);
 if(!object(raw)||raw.serverId!==s.serverId||raw.libraryId!==t.libraryId||!id(raw.viewerFence)||!integer(raw.revision)||raw.revision<1||!integer(raw.attempts)||!text(raw.nextAttempt,128)||!text(raw.error)||!text(raw.attribution))invalid();
 const tv=t.kind==='show';
 if(tv?raw.showId!==t.entityId:raw.entityId!==t.entityId||raw.kind!==t.kind)invalid();
 if(typeof raw.status!=='string')invalid();
 // A status this app does not know reads as unresolved; the candidates and the offered actions still come from the server.
 const status=oneOfOr('metadata-review','status',raw.status,(tv?tvStatuses:mbStatuses) as readonly MetadataReviewStatus[],'unresolved');
 let actions=offered<MetadataReviewAction>('actions',raw.actions,tv?['select','retry']:['select','retry','search','policy']);
 const choices=tv?offered<TVDBOrder>('orders',raw.orders,orders):[];
 let selectedId:string,order:TVDBOrder|''='';
 if(tv){if(!integer(raw.providerId)||!integer(raw.page)||typeof raw.order!=='string'||raw.order!==''&&!orders.includes(raw.order))invalid();selectedId=raw.providerId?String(raw.providerId):'';order=raw.order as TVDBOrder|'';}
 else {if(typeof raw.manual!=='boolean'||typeof raw.selectedId!=='string'||raw.selectedId!==''&&!mbId.test(raw.selectedId))invalid();selectedId=raw.selectedId;}
 // A candidate that cannot be read is left out alone and a repeated id keeps its first row; an over-long list of reasons still refuses the answer.
 const listed=list(raw.candidates,25);if(!tv&&listed.some(c=>object(c)&&Array.isArray(c.reasons)&&c.reasons.length>16))invalid();
 const candidates=firstOfEach('metadata-review','candidates',keepReadable('metadata-review','candidates',listed,(c):MetadataReviewCandidate=>{
  if(!object(c)||!text(c.observedAt,128))invalid();
  if(tv){if(!integer(c.id)||c.id<1||!integer(c.year)||!text(c.name,2048)||!text(c.overview,65536))invalid();return Object.freeze({id:String(c.id),title:c.name,subtitle:c.year?String(c.year):undefined,overview:c.overview,observedAt:c.observedAt,provenance:Object.freeze({year:c.year})});}
  if(typeof c.id!=='string'||!mbId.test(c.id)||!text(c.title,4096)||!text(c.artist,4096)||!text(c.edition,8192))invalid();
  if(typeof c.confidence!=='number'||!Number.isFinite(c.confidence)||c.confidence<0||c.confidence>1)invalid();
  // A reason that cannot be read is left out and a repeated one is listed once; a decision this app does not know is left off the candidate.
  const given=list(c.reasons,16),reasons=[...new Set(given.filter(id))];if(!reasons.length)invalid();if(reasons.length!==given.length)noteSkipped('metadata-review','candidate.reasons',given.length-reasons.length);
  const decision=oneOf(c.decision,['candidate','accepted','rejected','superseded'] as const);if(decision===undefined&&c.decision!==undefined)noteSkipped('metadata-review','candidate.decision');
  return Object.freeze({id:c.id,title:c.title,subtitle:c.artist,confidence:c.confidence,reasons:Object.freeze(reasons),...(decision?{decision}:{}),observedAt:c.observedAt,provenance:Object.freeze({artist:c.artist,edition:c.edition})});
 }),c=>c.id);
 // With no readable candidate there is nothing to select, so Select is not offered.
 if(!candidates.length)actions=actions.filter(a=>a!=='select');
 let published:MetadataPublication|undefined;
 if(!tv&&raw.published!==undefined){
  // Published evidence that cannot be read is left out, and so are its details alone; the candidates and the review state are kept.
  published=keepReadable('metadata-review','published',[raw.published],(p):MetadataPublication=>{if(!object(p)||typeof p.reconciliationPending!=='boolean')invalid();
  for(const key of ['releaseId','releaseGroupId','recordingId','trackId'])if(typeof p[key]!=='string'||p[key]!==''&&!mbId.test(p[key] as string))invalid();
  for(const key of ['title','artist','date','country','observedAt','trackTitle','trackStatus'])if(!text(p[key],key==='title'||key==='trackTitle'?4096:2048))invalid();
  for(const key of ['totalTracks','matchedTracks','pendingTracks','reviewTracks'])if(!integer(p[key]))invalid();
  if((p.matchedTracks as number)+(p.pendingTracks as number)+(p.reviewTracks as number)!==p.totalTracks||t.kind==='album'&&(!p.releaseId||!p.releaseGroupId))invalid();
  const rest:Record<string,unknown>={...p};delete rest.details;const details=p.details===undefined?[]:keepReadable('metadata-review','published.details',[p.details],parseMusicDetails);
  return Object.freeze({...rest,...(details.length?{details:details[0]}:{})}) as MetadataPublication;})[0];
 }
 // Fingerprint evidence that cannot be read is left out. The music policy stays exact: its revision is sent back with a change.
 const observation=tv||raw.observation===undefined?[]:keepReadable('metadata-review','observation',[raw.observation],parseMusicObservation);
 return Object.freeze({scope:Object.freeze({...s,libraryId:t.libraryId,entityId:t.entityId,viewerFence:raw.viewerFence}),provider:tv?'tvdb':'musicbrainz',revision:raw.revision,status,selectedId,order,orders:Object.freeze(choices),actions:Object.freeze(actions),candidates:Object.freeze(candidates),attempts:raw.attempts,nextAttempt:raw.nextAttempt,providerError:raw.error,attribution:raw.attribution,...(tv?{page:raw.page as number}:{manual:raw.manual as boolean,...(published?{published}:{}),...(raw.policy!==undefined?{policy:parseMusicPolicy(raw.policy)}:{}),...(observation.length?{observation:observation[0]}:{})})});
}
function path(t:MetadataReviewTarget):string {return apiRoot+(t.kind==='show'?'shows':t.kind==='album'?'albums':'items')+'/'+encodeURIComponent(t.entityId)+'/metadata/'+(screenTarget(t)?'screen':t.kind==='show'?'tvdb':'musicbrainz');}
export class MetadataReviewService {
 private api:MetadataReviewApi; private bound:MetadataReviewScope; private timeout:number; private generation=0; private read=0; private stopped=false; private controllers=new Set<AbortController>(); private readController?:AbortController; private listeners=new Set<()=>void>(); private state:MetadataReviewSnapshot; private command?:{key:string;promise:Promise<void>};
 constructor(o:{api:MetadataReviewApi;scope:MetadataReviewScope;timeoutMs?:number}){this.api=o.api;this.bound=scope(o.scope);this.timeout=o.timeoutMs??15000;if(!Number.isFinite(this.timeout)||this.timeout<=0||this.timeout>120000)throw new Error('Invalid provider review deadline.');this.state=this.empty();}
 private empty():MetadataReviewSnapshot{return Object.freeze({generation:this.generation,scope:this.bound,target:null,phase:'idle',data:null,error:null,pending:null,mutationError:null});}
 getSnapshot=():MetadataReviewSnapshot=>this.state;
 subscribe=(fn:()=>void):(()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(p:Partial<MetadataReviewSnapshot>):void {if(this.stopped)return;this.state=Object.freeze({...this.state,...p});for(const fn of this.listeners)fn();}
 private active():void {if(this.stopped)throw new Error('Provider review service is disposed.');}
 private fence():void {this.generation++;this.read++;for(const c of this.controllers)c.abort();this.controllers.clear();this.command=undefined;}
 select(input:MetadataReviewTarget):Promise<void>{this.active();const t=target(input);this.fence();this.publish({...this.empty(),target:t});return this.refresh();}
 setScope(s:MetadataReviewScope,api:MetadataReviewApi):void {this.active();const checked=scope(s);this.fence();this.bound=checked;this.api=api;this.publish(this.empty());}
 cancel():void {this.active();this.fence();this.publish(this.empty());}
 dispose():void {this.fence();this.stopped=true;this.listeners.clear();}
 refresh():Promise<void>{this.active();if(this.command)return this.command.promise;return this.load();}
 /** Reconciliation is read-only. A lost response never automatically repeats a selection. */
 async reconcile():Promise<void>{const generation=this.generation;await this.refresh();if(!this.stopped&&generation===this.generation&&this.state.phase==='ready')this.publish({mutationError:null});}
 private async load(previous:MetadataReviewData|null=this.state.data):Promise<void>{
  const t=this.state.target;if(!t)return;this.readController?.abort();const c=new AbortController();this.readController=c;const gen=this.generation,read=++this.read,api=this.api,s=this.bound;this.publish({phase:'loading',data:null,error:null});
  try{const raw=await this.request(api,path(t),'GET',undefined,c);if(this.stopped||gen!==this.generation||read!==this.read)return;const data=parse(raw,s,t);if(previous&&(data.scope.viewerFence!==previous.scope.viewerFence||data.revision<previous.revision))throw new Failure('metadata_scope_changed','Provider state changed scope or returned an older revision. Refresh required.',true);this.publish({phase:'ready',data,error:null});}
  catch(e){if(!this.stopped&&gen===this.generation&&read===this.read)this.publish({phase:'error',data:null,error:info(e)});}
 }
 choose(candidateId:string,order?:string):Promise<void>{return this.run('select',candidateId,order);}
 retryProvider():Promise<void>{return this.run('retry');}
 searchAlternatives():Promise<void>{return this.run('search');}
 configureMusic(policy:MusicPolicyInput):Promise<void>{return this.run('policy',undefined,undefined,{...policy});}
 searchScreen(provider:ScreenProvider,query=''):Promise<void>{return this.run('search',undefined,undefined,{provider,query});}
 updateScreenPolicy(input:ScreenPolicyInput):Promise<void>{return this.run('policy',undefined,undefined,{...input});}
 mapAnimeSeason(season:number,anilistId:string):Promise<void>{return this.run('map_season',undefined,undefined,{season,anilistId});}
 private run(action:MetadataReviewAction,candidateId?:string,order?:string,options?:Record<string,unknown>):Promise<void>{
  this.active();const key=JSON.stringify([action,candidateId,order,options]);if(this.command){if(this.command.key===key)return this.command.promise;throw new Error('Wait for the current provider command to finish.');}
  const data=this.state.data,t=this.state.target;if(this.state.phase!=='ready'||!data||!t||!data.actions.includes(action))throw new Error('This provider action is not currently available.');
  const screen=screenTarget(t),candidate=data.candidates.find(c=>c.id===candidateId);
  if(action==='select'&&(!candidate||t.kind==='show'&&(!order||!(screen?candidate.screen?.orders.map(o=>o.id)??[]:data.orders).includes(order))||t.kind!=='show'&&order!==undefined))throw new Error('Select an observed candidate and an explicit supported TV order.');
  let endpoint=path(t)+(action==='select'?'':'/'+action),method=action==='select'?'PUT':'POST';
  let body:Record<string,unknown>={expectedRevision:data.revision};
  if(screen){
   if(!data.screen)throw new Error('Screen metadata context is missing.');
   if(action==='select')body={...body,candidateKey:candidateId,order:order??''};
   if(action==='search'){if(!options||!data.screen.policy.providers.includes(options.provider as ScreenProvider))throw new Error('Choose an enabled provider to search.');body={...options,expectedRevision:data.revision};}
   if(action==='policy'){if(!options)throw new Error('Metadata settings are required.');body={...options,expectedRevision:data.screen.policy.revision,expectedConsentRevision:data.screen.policy.consentRevision};endpoint=encodedRoutePath("get_libraries_id_metadata_screen", {"id": encodeURIComponent(t.libraryId)});method='PUT';}
   if(action==='map_season'){body={...options,expectedRevision:data.revision};endpoint=path(t)+'/season';method='PUT';}
  }else if(action==='select'){body.providerId=t.kind==='show'?Number(candidateId):candidateId;if(t.kind==='show')body.order=order;}
  if(!screen&&action==='policy'){if((t.kind!=='song'&&t.kind!=='album')||!data.policy||!options||!['off','supplement','prefer'].includes(options.localMode as string)||typeof options.musicBrainzEnabled!=='boolean'||typeof options.acoustidEnabled!=='boolean')throw new Error('Choose a valid music metadata policy.');Object.assign(body,options,{expectedPolicyRevision:data.policy.revision});}
  this.readController?.abort();this.read++;const gen=this.generation,api=this.api,c=new AbortController();
  let resolve!:()=>void;const promise=new Promise<void>(r=>{resolve=r;});this.command={key,promise};this.publish({pending:action,mutationError:null});
  void (async()=>{try{
   const raw=await this.request(api,endpoint,method,body,c);
   if(gen!==this.generation||this.stopped)return;
   if(!object(raw)||(screen?(action==='policy'?raw.libraryId!==t.libraryId:raw.entityId!==t.entityId):(t.kind==='show'?raw.showId!==t.entityId:raw.entityId!==t.entityId)))invalid();
   // The acknowledgement must name the same entity; its status is not relied on, because the read that follows is the authority.
   if(raw.status!==(screen?'pending':t.kind==='show'?(action==='select'?'pending_episodes':'queued'):'pending'))noteSkipped('metadata-review','command.status');
   await this.load();
  }catch(e){if(gen!==this.generation||this.stopped)return;const error=info(e);this.publish({mutationError:error,data:null});if(['unauthorized','forbidden','not_found'].includes(error.code)){this.publish({phase:'error',error});}else await this.load(data);}
  finally{if(gen===this.generation&&!this.stopped){this.command=undefined;this.publish({pending:null});}resolve();}})();return promise;
 }
 private async request(api:MetadataReviewApi,p:string,m:string,b:unknown,c:AbortController):Promise<unknown>{
  this.controllers.add(c);let timer:ReturnType<typeof setTimeout>|undefined;let abort:(()=>void)|undefined;
  try{return await Promise.race([api.request(p,m,b,c.signal),new Promise<never>((_,reject)=>{abort=()=>reject(new Failure('cancelled','Provider request cancelled.'));c.signal.addEventListener('abort',abort,{once:true});if(c.signal.aborted)abort();timer=setTimeout(()=>{reject(new Failure('timeout','Provider request timed out. Refresh to reconcile before selecting again.',true));c.abort();},this.timeout);})]);}
  finally{if(timer)clearTimeout(timer);if(abort)c.signal.removeEventListener('abort',abort);this.controllers.delete(c);}
 }
}
