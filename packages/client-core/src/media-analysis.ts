import {routeBody} from "../../contracts/src/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import type {LibraryContentApi, ContentScope} from './library-content.ts';
import type {PlaybackSnapshot} from './index.ts';
import {firstOfEach,keepReadable,oneOfOr} from './tolerant.ts';
import {wireDurations} from './wire-units.ts';

export const analysisOperations = ['checksum','chapter_images','trickplay','waveform','loudness','fingerprint'] as const;
export type AnalysisOperation = typeof analysisOperations[number];
export type AnalysisTarget = Readonly<{libraryId:string;itemId:string;sourceId?:string;sessionId?:string;generation?:number}>;
export type AnalysisScope = Readonly<{serverId:string;libraryId:string;itemId:string;viewerFence:string;sessionId:string;sessionGeneration:number}>;
export type AnalysisSource = Readonly<{id:string;revision:string;mappingRevision:string;durationUS:string;needsProbe:boolean}>;
export type AnalysisMarker = Readonly<{id:string;kind:'intro'|'recap'|'credits'|'commercial'|'outro'|'chapter';title:string;startUS:string;endUS:string;confidence:number;provenance:string;approved:boolean;edited:boolean}>;
export type AnalysisPreview = Readonly<{id:string;kind:'trickplay'|'chapter_images';startUS:string;endUS:string;width:number;height:number}>;
export type AnalysisResult = Readonly<{stage:AnalysisOperation;algorithm:string;originAssurance:string;createdMS:number;summary:Readonly<Record<string,unknown>>;artifactId?:string}>;
export type AnalysisJob = Readonly<{jobId:string;jobStatus:string;stage:string;state:string;attempt:number;processedBytes:string;errorCode:string;updatedMS:number}>;
/** Owner-configured trickplay production geometry. Zero means the server default. */
export type AnalysisTrickplayPolicy = Readonly<{intervalSeconds:number;tileWidth:number;maxTiles:number}>;
export type AnalysisView = Readonly<{
 scope:AnalysisScope;source:AnalysisSource;sources:readonly {id:string;label:string}[];canManage:boolean;
 policy:Readonly<{tier:'file_list_only'|'basic'|'complete'|'custom';operations:readonly string[];revision:number;trickplay:AnalysisTrickplayPolicy}>;
 budgets:Readonly<Record<string,unknown>>|null;results:readonly AnalysisResult[];markers:readonly AnalysisMarker[];markerRevision:number;previews:readonly AnalysisPreview[];jobs:readonly AnalysisJob[];
}>;
export type WaveformPoint = Readonly<{startUS:string;endUS:string;peak:number;rms:number}>;
export type AnalysisState = Readonly<{data:AnalysisView|null;busy:boolean;imageBusy:boolean;image:Readonly<{id:string;uri:string}>|null;waveform:readonly WaveformPoint[]|null;error:string;notice:string;ambiguous:boolean}>;
export type MarkerEdit = Readonly<{action:'create'|'edit'|'approve'|'dismiss';markerId?:string;kind?:AnalysisMarker['kind'];title?:string;startUS?:string;endUS?:string}>;
const object=(x:unknown):x is Record<string,unknown>=>!!x&&typeof x==='object'&&!Array.isArray(x);
function assert(x:unknown):asserts x {if(!x)throw new Error('Analysis response does not match this source and viewer. Refresh the item.');}
function text(x:unknown,max=512):x is string{return typeof x==='string'&&x.length<=max;}
function opaque(x:unknown):x is string{return typeof x==='string'&&/^[a-f0-9]{64}$/.test(x);}
function integer(x:unknown):x is number{return typeof x==='number'&&Number.isSafeInteger(x)&&x>=0;}
function list(x:unknown,max:number):unknown[]{assert(Array.isArray(x)&&x.length<=max);return x;}
export function analysisMicroseconds(x:unknown):x is string{return typeof x==='string'&&/^(0|[1-9][0-9]{0,18})$/.test(x)&&Number.isSafeInteger(Number(x));}
export function analysisSeconds(us:string):number {if(!analysisMicroseconds(us))throw new Error('Position is outside the supported playback clock.');return Number(us)/1e6;}
export function analysisClock(us:string):string {const s=Math.floor(analysisSeconds(us));return s>=3600?`${Math.floor(s/3600)}:${String(Math.floor(s/60)%60).padStart(2,'0')}:${String(s%60).padStart(2,'0')}`:`${Math.floor(s/60)}:${String(s%60).padStart(2,'0')}`;}
function checkedScope(raw:unknown,bound:ContentScope,t:AnalysisTarget):AnalysisScope{
 assert(object(raw)&&raw.serverId===bound.serverId&&raw.libraryId===t.libraryId&&raw.itemId===t.itemId&&text(raw.viewerFence,256)&&raw.viewerFence&&raw.sessionId===(t.sessionId??'')&&raw.sessionGeneration===(t.generation??0));return Object.freeze({...raw}) as AnalysisScope;
}
function checkedSource(raw:unknown,t:AnalysisTarget):AnalysisSource {assert(object(raw)&&text(raw.id,256)&&raw.id&&(!t.sourceId||raw.id===t.sourceId)&&text(raw.revision,256)&&raw.revision&&opaque(raw.mappingRevision)&&analysisMicroseconds(raw.durationUS)&&typeof raw.needsProbe==='boolean');return Object.freeze({...raw}) as AnalysisSource;}
/** The scope, the source and its revisions, the marker and policy revisions and every list's bound are exact.
 * Inside a list, a row that cannot be read (a stage, kind or assurance this app does not know, a time outside
 * the source) or that repeats is left out and the rest are kept. */
export function parseAnalysis(raw:unknown,bound:ContentScope,t:AnalysisTarget):AnalysisView{
 assert(object(raw));const scope=checkedScope(raw.scope,bound,t),source=checkedSource(raw.source,t);
 assert(typeof raw.canManage==='boolean'&&integer(raw.markerRevision)&&raw.markerRevision>0&&object(raw.policy));const p=raw.policy;
 assert(text(p.tier,64)&&p.tier&&integer(p.revision)&&p.revision>0);const operations=list(p.operations,32);assert(operations.every(x=>text(x,64)));
 const trickplay:unknown=p.trickplay===undefined?{intervalSeconds:0,tileWidth:0,maxTiles:0}:wireDurations(p.trickplay,{intervalSeconds:'intervalMs'});
 assert(object(trickplay)&&integer(trickplay.intervalSeconds)&&integer(trickplay.tileWidth)&&integer(trickplay.maxTiles));
 assert(trickplay.intervalSeconds<=600&&trickplay.tileWidth<=640&&trickplay.maxTiles<=20000);
 // A tier this app does not know reads as custom: the operations the server lists are what it shows.
 const tier=oneOfOr('analysis','policy.tier',p.tier,['file_list_only','basic','complete','custom'] as const,'custom');
 const results=firstOfEach('analysis','results',keepReadable('analysis','results',list(raw.results,16),r=>{assert(object(r)&&analysisOperations.includes(r.stage as AnalysisOperation)&&text(r.algorithm,128)&&['observed_acquisition','strong_version'].includes(String(r.originAssurance))&&integer(r.createdMS)&&object(r.summary)&&(!r.artifactId||opaque(r.artifactId)));return Object.freeze({...r,summary:Object.freeze({...r.summary})}) as AnalysisResult;}),r=>r.stage);
 const markers=firstOfEach('analysis','markers',keepReadable('analysis','markers',list(raw.markers,512),m=>{assert(object(m)&&opaque(m.id)&&['intro','recap','credits','commercial','outro','chapter'].includes(String(m.kind))&&text(m.title,512)&&text(m.provenance,512)&&analysisMicroseconds(m.startUS)&&analysisMicroseconds(m.endUS)&&Number(m.startUS)<Number(m.endUS)&&Number(m.endUS)<=Number(source.durationUS)&&typeof m.confidence==='number'&&Number.isFinite(m.confidence)&&m.confidence>=0&&m.confidence<=1&&typeof m.approved==='boolean'&&typeof m.edited==='boolean');return Object.freeze({...m}) as AnalysisMarker;}),m=>m.id);
 const previews=keepReadable('analysis','previews',list(raw.previews,12),p=>{assert(object(p)&&opaque(p.id)&&['trickplay','chapter_images'].includes(String(p.kind))&&analysisMicroseconds(p.startUS)&&analysisMicroseconds(p.endUS)&&Number(p.startUS)<Number(p.endUS)&&Number(p.endUS)<=Number(source.durationUS)&&integer(p.width)&&p.width>0&&p.width<=4096&&integer(p.height)&&p.height>0&&p.height<=4096);return Object.freeze({...p}) as AnalysisPreview;});
 const jobs=keepReadable('analysis','jobs',list(raw.jobs,33),j=>{assert(object(j)&&text(j.jobId,256)&&j.jobId&&text(j.jobStatus,64)&&text(j.stage,64)&&text(j.state,64)&&integer(j.attempt)&&analysisMicroseconds(j.processedBytes)&&text(j.errorCode,128)&&integer(j.updatedMS));return Object.freeze({...j}) as AnalysisJob;});
 const sources=keepReadable('analysis','sources',list(raw.sources,32),v=>{assert(object(v)&&text(v.id,256)&&v.id&&text(v.label,128));return Object.freeze({id:v.id,label:v.label});});
 assert(sources.some(v=>v.id===source.id));if(source.needsProbe)assert(!results.length&&!markers.length&&!previews.length);assert(raw.budgets===null||object(raw.budgets));
 return Object.freeze({scope,source,sources:Object.freeze(sources),canManage:raw.canManage,policy:Object.freeze({tier,revision:p.revision,operations:Object.freeze(operations),trickplay:Object.freeze({intervalSeconds:trickplay.intervalSeconds,tileWidth:trickplay.tileWidth,maxTiles:trickplay.maxTiles})}) as AnalysisView['policy'],budgets:raw.budgets as AnalysisView['budgets'],results:Object.freeze(results),markers:Object.freeze(markers),markerRevision:raw.markerRevision,previews:Object.freeze(previews),jobs:Object.freeze(jobs)});
}
function message(error:unknown):string{return error instanceof Error?error.message:'Analysis request failed. Refresh to check its current state.';}
let requestSequence=0;
function requestID():string{return globalThis.crypto?.randomUUID?.()??`analysis-${Date.now()}-${++requestSequence}-${Math.random().toString(36).slice(2)}`;}

/** Owns only reads and explicit analysis commands, never playback intent. Every
 * async result is fenced by server/viewer/item/source/mapping/session generation. */
export class MediaAnalysisService {
 private state:AnalysisState=Object.freeze({data:null,busy:false,imageBusy:false,image:null,waveform:null,error:'',notice:'',ambiguous:false});
 private listeners=new Set<()=>void>();private epoch=0;private disposed=false;private fence?:string;private controllers=new Set<AbortController>();private imageSequence=0;private pendingMarker?:Record<string,unknown>;private command=false;private selectedSource?:string;private readonly timeoutMs:number;
 private readonly api:LibraryContentApi;private readonly scope:ContentScope;readonly target:AnalysisTarget;
 constructor(api:LibraryContentApi,scope:ContentScope,target:AnalysisTarget,timeoutMs=20000){assert(Number.isInteger(timeoutMs)&&timeoutMs>0&&timeoutMs<=20000);this.timeoutMs=timeoutMs;assert(scope.serverId&&scope.viewerId&&target.libraryId&&target.itemId);this.api=api;this.scope=Object.freeze({...scope});this.target=Object.freeze({...target});}
 subscribe=(f:()=>void)=>{this.listeners.add(f);return()=>this.listeners.delete(f);};getSnapshot=()=>this.state;
 private publish(update:Partial<AnalysisState>){if(this.disposed)return;this.state=Object.freeze({...this.state,...update});for(const f of this.listeners)f();}
 dispose(){this.disposed=true;this.epoch++;for(const c of this.controllers)c.abort();this.controllers.clear();this.listeners.clear();this.pendingMarker=undefined;this.state=Object.freeze({...this.state,data:null,image:null,waveform:null});}
 private base(){return routePath("get_items_id_analysis", {id: this.target.itemId});}
 private body(data:AnalysisView){return {sourceId:data.source.id,sourceRevision:data.source.revision,mappingRevision:data.source.mappingRevision,sessionId:this.target.sessionId??'',generation:this.target.generation??0};}
 private query(data?:AnalysisView,positionUS?:string){const q=new URLSearchParams();const target=data?this.body(data):{sourceId:this.target.sourceId??this.selectedSource??'',sessionId:this.target.sessionId??'',generation:this.target.generation??0};for(const [k,v]of Object.entries(target)){if(v!==''&&v!==0)q.set(k,String(v));}if(positionUS!==undefined)q.set('positionUS',positionUS);return '?'+q;}
 private async request<T=unknown>(path:string,method='GET',body?:unknown):Promise<T>{
  const c=new AbortController();this.controllers.add(c);let abort!:()=>void;
  const cancelled=new Promise<never>((_,reject)=>{abort=()=>reject(new Error('Analysis request timed out or was cancelled.'));c.signal.addEventListener('abort',abort,{once:true});});
  const timer=setTimeout(()=>c.abort(),this.timeoutMs);
  try{return await Promise.race([this.api.request<T>(path,method,body,c.signal),cancelled]);}
  finally{clearTimeout(timer);c.signal.removeEventListener('abort',abort);this.controllers.delete(c);}
 }
 async refresh(positionUS='0'):Promise<void>{if(this.disposed||this.command)return;assert(analysisMicroseconds(positionUS));const epoch=++this.epoch;for(const c of this.controllers)c.abort();this.publish({busy:true,imageBusy:false,image:null,waveform:null,error:''});
  try{const raw=await this.request<RouteResponse<"get_items_id_analysis">>(this.base()+this.query(undefined,positionUS));if(this.disposed||epoch!==this.epoch)return;const data=parseAnalysis(raw,this.scope,{...this.target,sourceId:this.target.sourceId??this.selectedSource});this.selectedSource=data.source.id;if(this.fence&&data.scope.viewerFence!==this.fence)throw new Error('Viewer permissions changed. Reopen the item.');this.fence=data.scope.viewerFence;this.publish({data,busy:false});}
  catch(e){if(this.disposed||epoch!==this.epoch)return;this.publish({data:null,busy:false,image:null,waveform:null,error:message(e)});}
 }
 private async artifact(id:string,data:AnalysisView):Promise<Record<string,unknown>>{const raw=await this.request<RouteResponse<"get_items_id_analysis_artifacts_artifact">>(routePath("get_items_id_analysis_artifacts_artifact",{id:this.target.itemId,artifact:id})+this.query(data));assert(object(raw)&&raw.id===id);const scope=checkedScope(raw.scope,this.scope,this.target),source=checkedSource(raw.source,this.target);assert(scope.viewerFence===data.scope.viewerFence&&source.id===data.source.id&&source.revision===data.source.revision&&source.mappingRevision===data.source.mappingRevision);return raw;}
 async image(id:string):Promise<void>{const data=this.state.data,epoch=this.epoch,seq=++this.imageSequence;if(!data||this.state.busy||!data.previews.some(p=>p.id===id))return;this.publish({imageBusy:true,image:null,error:''});
  try{const r=await this.artifact(id,data);if(this.disposed||epoch!==this.epoch||seq!==this.imageSequence||this.state.data!==data)return;assert(r.mime==='image/jpeg'&&text(r.data,3<<20)&&/^[A-Za-z0-9+/]+={0,2}$/.test(r.data));this.publish({imageBusy:false,image:Object.freeze({id,uri:'data:image/jpeg;base64,'+r.data})});}
  catch(e){if(!this.disposed&&epoch===this.epoch&&seq===this.imageSequence)this.publish({imageBusy:false,image:null,error:message(e)});}
 }
 async waveform():Promise<void>{const data=this.state.data,epoch=this.epoch;const id=data?.results.find(r=>r.stage==='waveform')?.artifactId;if(!id||!data||this.state.busy)return;
  try{const r=await this.artifact(id,data);if(this.disposed||epoch!==this.epoch||this.state.data!==data)return;assert(r.kind==='waveform'&&r.mime==='application/json'&&object(r.document));const points=keepReadable('analysis','waveform.points',list(r.document.points,2048),p=>{assert(object(p)&&analysisMicroseconds(p.startUS)&&analysisMicroseconds(p.endUS)&&Number(p.endUS)>Number(p.startUS)&&Number(p.endUS)<=Number(data.source.durationUS)&&typeof p.peak==='number'&&Number.isFinite(p.peak)&&p.peak>=0&&p.peak<=1&&typeof p.rms==='number'&&Number.isFinite(p.rms)&&p.rms>=0&&p.rms<=1);return Object.freeze({...p}) as WaveformPoint;});this.publish({waveform:Object.freeze(points)});}
  catch(e){if(!this.disposed&&epoch===this.epoch)this.publish({waveform:null,error:message(e)});}
 }
 private current():AnalysisView{const d=this.state.data;if(!d||!d.canManage||this.state.busy||this.command||this.state.ambiguous)throw new Error('Refresh current analysis before changing it.');return d;}
 private async execute(path:string,method:string,body:unknown,marker=false){if(this.disposed||this.command)return;this.command=true;const epoch=++this.epoch;for(const c of this.controllers)c.abort();this.publish({busy:true,error:'',notice:'',imageBusy:false});
  try{await this.request(path,method,body);if(this.disposed||epoch!==this.epoch)return;this.pendingMarker=undefined;this.publish({busy:false,ambiguous:false,notice:marker?'Marker change saved.':'Request saved. The library source job uses the saved policy and yields to playback.'});this.command=false;await this.refresh();}
  catch(e){if(this.disposed||epoch!==this.epoch)return;const code=object(e)&&typeof e.code==='string'?e.code:'';const definite=['analysis_clock_unavailable','analysis_conflict','invalid_analysis','analysis_budget_exceeded','unauthorized','forbidden','not_found','invalid_request','library_configuration_conflict'].includes(code);if(definite)this.pendingMarker=undefined;this.publish({busy:false,data:null,image:null,waveform:null,error:message(e),ambiguous:marker&&!definite});}
  finally{this.command=false;}
 }
 mutate(edit:MarkerEdit):Promise<void>{const d=this.current();assert(!d.source.needsProbe);if(edit.action!=='create')assert(d.markers.some(m=>m.id===edit.markerId));const body={...this.body(d),...edit,requestId:requestID(),expectedRevision:d.markerRevision};this.pendingMarker=body;return this.execute(routePath("post_items_id_analysis_markers",{id:this.target.itemId}),'POST',routeBody("post_items_id_analysis_markers",body),true);}
 retryMarker():Promise<void>{if(!this.pendingMarker)return Promise.resolve();return this.execute(routePath("post_items_id_analysis_markers",{id:this.target.itemId}),'POST',this.pendingMarker,true);}
 queue():Promise<void>{const d=this.current();return this.execute(routePath("post_items_id_analysis_jobs",{id:this.target.itemId}),'POST',routeBody("post_items_id_analysis_jobs",this.body(d)));}
 control(jobId:string,action:'pause'|'resume'|'cancel'|'retry'):Promise<void>{const d=this.current();assert(d.jobs.some(j=>j.jobId===jobId));return this.execute(routePath("post_items_id_analysis_jobs_job_control",{id:this.target.itemId,job:jobId}),'POST',routeBody("post_items_id_analysis_jobs_job_control",{...this.body(d),action}));}
 policy(tier:AnalysisView['policy']['tier'],operations:readonly string[]):Promise<void>{const d=this.current();return this.execute(routePath("put_items_id_analysis_policy",{id:this.target.itemId}),'PUT',routeBody("put_items_id_analysis_policy",{...this.body(d),tier,operations,expectedRevision:d.policy.revision}));}
}

/** A user gesture may inspect any candidate, but only owner-approved markers
 * offer skip actions. No detector confidence threshold enables auto-skip. */
export function analysisSeekPosition(data:AnalysisView,us:string,player:PlaybackSnapshot):number|null{
 if(player.session?.preparedVersionId||data.source.needsProbe||player.phase!=='ready'||player.pendingSeek||player.itemId!==data.scope.itemId||player.session?.id!==data.scope.sessionId||player.session?.generation!==data.scope.sessionGeneration||!analysisMicroseconds(us)||Number(us)>Number(data.source.durationUS))return null;
 return analysisSeconds(us);
}
export function markerSkipPosition(data:AnalysisView,marker:AnalysisMarker,player:PlaybackSnapshot):number|null{
 if(!marker.approved||marker.kind==='chapter'||!data.markers.includes(marker))return null;
 const start=analysisSeconds(marker.startUS),end=analysisSeconds(marker.endUS);
 if(player.positionSeconds<start||player.positionSeconds>=end)return null;
 return analysisSeekPosition(data,marker.endUS,player);
}

export function analysisInputUS(seconds:string):string {
 const match=/^(0|[1-9][0-9]*)(?:\.([0-9]{1,6}))?$/.exec(seconds.trim());
 if(!match)throw new Error('Enter nonnegative seconds with at most six decimal places.');
 const value=(match[1]+(match[2]??'').padEnd(6,'0')).replace(/^0+(?=\d)/,'');
 if(!analysisMicroseconds(value))throw new Error('Time is outside the supported range.');return value;
}
export function analysisInputSeconds(us:string):string {if(!analysisMicroseconds(us))throw new Error('Invalid time.');const value=us.padStart(7,'0');return `${value.slice(0,-6)}.${value.slice(-6)}`;}
export const analysisLabels:Record<string,string>={probe:'Technical probe',local_metadata:'Local metadata',subtitles:'Local subtitles',checksum:'SHA-256 checksum',chapter_images:'Chapter images',trickplay:'Seek previews',waveform:'Waveform',loudness:'Loudness (measurement only)',fingerprint:'Audio fingerprint'};
export const analysisGroups:readonly {label:string;operations:readonly string[]}[]=[
 {label:'Bounded metadata reads',operations:['probe','local_metadata','subtitles']},
 {label:'Preview images · repeated or full video reads',operations:['chapter_images','trickplay']},
 {label:'Deep analysis · full media reads',operations:['checksum','waveform','loudness','fingerprint']},
];
