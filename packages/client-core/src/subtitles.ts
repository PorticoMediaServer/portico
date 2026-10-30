import {routePattern} from "../../contracts/src/routes.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {enUS,type MessageId} from '../../i18n/src/index.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {keepReadable} from './tolerant.ts';
import {serviceI18n, serviceText} from './presentation/service-text.ts';
import type {LibraryContentApi} from './library-content.ts';

export type SubtitleFormat='srt'|'vtt'|'ass'|'ssa'|'pgs'|'sup'|'vobsub'|'idx'|'dvb'|'smi'|'sami'|'ttml'|'dfxp';
export type SubtitleRendering='text'|'ass'|'pgs'|'vobsub'|'dvb';
/** `linesUrl` (image subtitles): how many of the track's lines are forced (`parseSubtitleLines`). */
export type SubtitleDelivery=Readonly<{format:SubtitleRendering;url?:string;companionUrl?:string;fontsUrl?:string;linesUrl?:string}>;
export type SubtitleFont=Readonly<{name:string;url:string}>;
export type SubtitlePresentation=Readonly<{sessionId:string;generation:number;streamUrl:string;mode:'direct'|'hls';positionUs:string}>;
export type SubtitleResource = Readonly<{
 manifestName?:string;id:string; sourceId:string; revision:number; scope:'personal'|'shared'; language:string; title:string;
 format:SubtitleFormat; origin:'upload'|'sidecar'|'embedded'|'provider'; provider?:string; attribution?:string;
 rights:string; offsetUs:string; canManage:boolean; enabled:boolean; reason?:string; pinned?:boolean;
 retired?:boolean; discoveryId?:string;renderer:'external_text'|'burn_in'|'client';default:boolean;forced:boolean;hearingImpaired?:boolean;deliveries?:readonly SubtitleDelivery[];
}>;
export type SubtitleDiscovery = Readonly<{
 id:string;sourceId:string;revision:number;origin:'sidecar'|'embedded';format:string;language:string;title:string;
 default:boolean;forced:boolean;enabled:boolean;reason?:string;hearingImpaired?:boolean;
}>;
export type SubtitlePlan = Readonly<{
 version:1;sessionId:string;generation:number;sourceId:string;revision:number;catalogRevision:number;
 renderer:'external_text'|'burn_in'|'client';presentation?:SubtitlePresentation;mode:'off'|'track';offAvailable:true;offsetUs:string;selected:SubtitleResource|null;
 documentUrl?:string;resources:readonly SubtitleResource[];discovered:readonly SubtitleDiscovery[];appliedRevision?:number;
}>;
export type SubtitleCatalog = Readonly<{
 version:1;itemId:string;revision:number;canShare:boolean;
 sources:readonly Readonly<{id:string;durationUs:string;inventoryRevision:number;available:boolean;timingKnown:boolean}>[];
 resources:readonly SubtitleResource[];discovered:readonly SubtitleDiscovery[];
 provider:Readonly<{id:string;enabled:boolean;reason?:string;resetsAt?:string;retryAt?:string}>;
}>;
/** `italic` and `top` are the two presentation facts every player can honour:
 * a cue that is wholly italic, and a cue authored at the top of the picture. */
export type SubtitleCue=Readonly<{startUs:string;endUs:string;text:string;italic?:boolean;top?:boolean}>;
export type SubtitleDocument=Readonly<{version:1;timeDomain:'source-relative';cues:readonly SubtitleCue[];skippedCues?:number}>;
export type SubtitleCandidate=Readonly<{id:string;language:string;title:string;attribution:string}>;
export type SubtitleReceipt=Readonly<{operationId:string;resourceId:string;revision:number;catalogRevision:number;deleted?:boolean}>;
const record=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,128)&&/^[A-Za-z0-9_-]+$/.test(v);
const integer=(v:unknown,min=1):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=min;
const decimal=(v:unknown,max=86400000000,signed=false):v is string=>typeof v==='string'&&/^(?:0|[1-9]\d*|-[1-9]\d*)$/.test(v)&&Number.isSafeInteger(Number(v))&&Number(v)>=(signed?-max:0)&&Number(v)<=max;
function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_subtitles'});}
function array(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
export function validateSubtitleDelivery(v:unknown):SubtitleDelivery{
 if(!record(v)||!['text','ass','pgs','vobsub','dvb'].includes(String(v.format)))invalid();
 for(const key of ['url','companionUrl','fontsUrl','linesUrl']){const path=v[key];if(path!==undefined&&(!text(path,1024)||!/^\/v1\/media\/subtitles\/[A-Za-z0-9_-]+\/[A-Za-z0-9_-]+\/[1-9]\d*(?:\.vtt|\/original\.(?:ass|ssa|sup|idx|sub|mks)|\/fonts|\/lines)?$/.test(path)))invalid();}
 return Object.freeze({...v}) as SubtitleDelivery;
}
function resource(v:unknown):SubtitleResource{
 if(!record(v)||!id(v.id)||!id(v.sourceId)||!integer(v.revision)||!['personal','shared'].includes(String(v.scope))||!text(v.language,64)||!text(v.title,160)||!['srt','vtt','ass','ssa','pgs','sup','vobsub','idx','dvb','smi','sami','ttml','dfxp'].includes(String(v.format))||!['upload','sidecar','embedded','provider'].includes(String(v.origin))||!text(v.rights,500)||!decimal(v.offsetUs,600000000,true)||typeof v.canManage!=='boolean'||typeof v.enabled!=='boolean')invalid();
 for(const k of ['provider','attribution','reason','manifestName'])if(v[k]!==undefined&&!text(v[k],512))invalid();
 if(v.renderer!==undefined&&!['external_text','burn_in','client'].includes(String(v.renderer)))invalid();
 for(const k of ['default','forced','pinned','retired','hearingImpaired'])if(v[k]!==undefined&&typeof v[k]!=='boolean')invalid();
 if(v.discoveryId!==undefined&&!id(v.discoveryId))invalid();
  const renderer=v.renderer??'external_text';if(renderer==='external_text'&&!['srt','vtt','ass','ssa','smi','sami','ttml','dfxp'].includes(String(v.format)))invalid();
 return Object.freeze({...v,...(v.deliveries===undefined?{}:{deliveries:Object.freeze(array(v.deliveries,5).map(validateSubtitleDelivery))}),renderer,default:v.default??false,forced:v.forced??false}) as SubtitleResource;
}
function discovery(v:unknown):SubtitleDiscovery{
 if(!record(v)||!id(v.id)||!id(v.sourceId)||!integer(v.revision)||!['sidecar','embedded'].includes(String(v.origin))||!text(v.format,64)||!text(v.language,64)||!text(v.title,160)||typeof v.default!=='boolean'||typeof v.forced!=='boolean'||typeof v.enabled!=='boolean'||v.reason!==undefined&&!text(v.reason,128))invalid();
 if(v.hearingImpaired!==undefined&&typeof v.hearingImpaired!=='boolean')invalid();
 return Object.freeze({...v}) as SubtitleDiscovery;
}
export function validateSubtitlePlan(v:unknown):SubtitlePlan{
 if(!record(v)||v.version!==1||!id(v.sessionId)||!integer(v.generation)||!id(v.sourceId)||!integer(v.revision)||!integer(v.catalogRevision)||!['external_text','burn_in','client'].includes(String(v.renderer))||!['off','track'].includes(String(v.mode))||v.offAvailable!==true||!decimal(v.offsetUs,600000000,true))invalid();
 // A row this app cannot read (a format or origin newer than it) is left out and the rest offered; the selected track stays exact.
 const resources=keepReadable('subtitle-plan','resources',array(v.resources,128),resource),discovered=keepReadable('subtitle-plan','discovered',array(v.discovered,1024),discovery),selected=v.selected===null?null:resource(v.selected);
 if(resources.some(r=>r.sourceId!==v.sourceId)||discovered.some(r=>r.sourceId!==v.sourceId)||selected&&selected.sourceId!==v.sourceId)invalid();
 if(new Set(resources.map(r=>r.id)).size!==resources.length||new Set(discovered.map(r=>r.id)).size!==discovered.length)invalid();
 if((v.mode==='off')!==(selected===null)||v.mode==='off'&&(v.offsetUs!=='0'||v.documentUrl!==undefined))invalid();
 if(v.documentUrl!==undefined&&(!selected||!text(v.documentUrl,512)||!new RegExp('^'+routePattern('get_v1_media_subtitles_grant_resource_revision').replace('{grant}','[A-Za-z0-9_-]{1,128}').replace('{resource}',selected.id).replace('{revision}',String(selected.revision))+'$').test(v.documentUrl)))invalid();
 if(selected&&(selected.pinned!==true||selected.renderer!==v.renderer))invalid();if(v.mode==='off'&&v.renderer!=='external_text')invalid();if(selected?.enabled&&v.renderer==='external_text'&&!v.documentUrl)invalid();if(v.renderer==='burn_in'&&v.documentUrl!==undefined)invalid();
 if(v.presentation!==undefined){const p=v.presentation;if(!record(p)||p.sessionId!==v.sessionId||p.generation!==v.generation||!text(p.streamUrl,1024)||!/^\/v1\/media\/[A-Za-z0-9_-]+(?:\/master\.m3u8)?$/.test(p.streamUrl)||!['direct','hls'].includes(String(p.mode))||!decimal(p.positionUs))invalid();}
 if(v.appliedRevision!==undefined&&(!integer(v.appliedRevision)||v.appliedRevision>v.revision))invalid();
 return Object.freeze({...v,selected,resources:Object.freeze(resources),discovered:Object.freeze(discovered)}) as SubtitlePlan;
}
export function validateSubtitleCatalog(v:unknown):SubtitleCatalog{
 if(!record(v)||v.version!==1||!id(v.itemId)||!integer(v.revision)||typeof v.canShare!=='boolean'||!record(v.provider)||!text(v.provider.id,64)||typeof v.provider.enabled!=='boolean'||v.provider.reason!==undefined&&!text(v.provider.reason,128)||v.provider.resetsAt!==undefined&&(!text(v.provider.resetsAt,64)||!Number.isFinite(Date.parse(v.provider.resetsAt as string)))||v.provider.retryAt!==undefined&&(!text(v.provider.retryAt,64)||!Number.isFinite(Date.parse(v.provider.retryAt as string))))invalid();
 const sources=array(v.sources,32).map(s=>{if(!record(s)||!id(s.id)||!decimal(s.durationUs)||!integer(s.inventoryRevision,0)||typeof s.available!=='boolean'||typeof s.timingKnown!=='boolean')invalid();return Object.freeze({...s});});
 const resources=keepReadable('subtitle-catalog','resources',array(v.resources,128),resource),discovered=keepReadable('subtitle-catalog','discovered',array(v.discovered,1024),discovery);
 if(resources.some(r=>!sources.some(s=>s.id===r.sourceId))||discovered.some(r=>!sources.some(s=>s.id===r.sourceId)))invalid();
 return Object.freeze({...v,sources:Object.freeze(sources),resources:Object.freeze(resources),discovered:Object.freeze(discovered),provider:Object.freeze({...v.provider})}) as SubtitleCatalog;
}
/** Strict about the envelope, forgiving about the cues.
 *
 * A document that is not a version 1 source-relative cue list is refused: the
 * caller shows no subtitles and asks again. Inside a valid envelope one bad cue
 * costs only itself. A cue with unreadable times, an end before its start or no
 * words is dropped; control characters and any markup that slipped through are
 * removed (angle brackets are words: the server sends plain text and players escape it); an over-long cue is cut; cues out of order are put in order; and the
 * whole is bounded (20 000 cues, 4 MiB of text). A document whose every cue was
 * bad is refused like a bad envelope, so "this track is empty" is never shown for
 * what is really a broken response. */
export function validateSubtitleDocument(v:unknown):SubtitleDocument{
 if(!record(v)||v.version!==1||v.timeDomain!=='source-relative'||!Array.isArray(v.cues)||v.cues.length>20000)invalid();
 if(v.skippedCues!==undefined&&(!integer(v.skippedCues,0)||v.skippedCues>4194304))invalid();
 const day=86_400_000_000;let total=0,ordered=true,previous=-1;
 const cues:{startUs:string;endUs:string;text:string;italic?:boolean;top?:boolean;at:number}[]=[];
 for(const c of v.cues as unknown[]){
  if(!record(c)||!decimal(c.startUs)||!decimal(c.endUs)||typeof c.text!=='string')continue;
  const start=Number(c.startUs),end=Number(c.endUs);
  if(!(end>start)||end>day)continue;
  let words=c.text.replace(/\{\\[^{}]*\}/g,'').replace(/[\x00-\x08\x0b-\x1f\x7f]/g,'').replace(/[ \t]+\n/g,'\n').trim();
  if(!words)continue;
  if(words.length>8192)words=words.slice(0,8192);
  total+=words.length;if(total>4194304)break;
  if(start<previous)ordered=false;previous=start;
  cues.push({startUs:c.startUs,endUs:c.endUs,text:words,...(c.italic===true?{italic:true}:{}),...(c.top===true?{top:true}:{}),at:start});
 }
 if(!cues.length&&v.cues.length)invalid();
 if(!ordered)cues.sort((a,b)=>a.at-b.at);
 return Object.freeze({version:1,timeDomain:'source-relative',cues:Object.freeze(cues.map(({at:_,...cue})=>Object.freeze(cue))),...(v.skippedCues===undefined?{}:{skippedCues:v.skippedCues as number})});
}
/** One authorised read of the active subtitle document.
 *
 * Players reload the document every few seconds purely to stay authorised. The
 * server runs its full check every time and answers 304 with no body when the
 * validator sent here still names the document, so the reload costs a header
 * instead of the whole track. The browser cache is never involved: the request
 * is `no-store` and the validator is set by hand, so a revoked viewer can only
 * ever be answered by the server. */
export type SubtitleDocumentRead=Readonly<{unchanged:true}>|Readonly<{unchanged:false;document:SubtitleDocument;etag:string}>;
export async function fetchSubtitleDocument(fetcher:typeof fetch,url:string,etag:string,signal:AbortSignal):Promise<SubtitleDocumentRead>{
 const validator=/^"[A-Za-z0-9_.-]{1,200}"$/.test(etag)?etag:'';
 const response=await fetcher(url,{signal,credentials:'omit',cache:'no-store',redirect:'error',...(validator?{headers:{'If-None-Match':validator}}:{})});
 if(response.status===304&&validator){void response.body?.cancel().catch(()=>{});return Object.freeze({unchanged:true as const});}
 const document=await readSubtitleDocument(response,signal);
 const next=response.headers.get('ETag')??'';
 return Object.freeze({unchanged:false as const,document,etag:/^"[A-Za-z0-9_.-]{1,200}"$/.test(next)?next:''});
}
export async function readSubtitleDocument(response:Response,signal:AbortSignal):Promise<SubtitleDocument>{
 if(!response.ok){void response.body?.cancel().catch(()=>{});throw Object.assign(new Error('Subtitle access is no longer available.'),{status:response.status});}
 if(!response.headers.get('Content-Type')?.toLowerCase().startsWith('application/json'))invalid();
 const max=8388608,declared=response.headers.get('Content-Length');if(declared!==null&&(!/^\d+$/.test(declared)||Number(declared)>max))invalid();
 if(!response.body)invalid();const reader=response.body.getReader();const chunks:Uint8Array[]=[];let total=0;
 const abort=()=>{void reader.cancel().catch(()=>{});};signal.addEventListener('abort',abort,{once:true});
 try{while(!signal.aborted){const {done,value}=await reader.read();if(done)break;total+=value.length;if(total>max)invalid();chunks.push(value);}if(signal.aborted)throw new Error('Subtitle request cancelled.');const bytes=new Uint8Array(total);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}return validateSubtitleDocument(JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes)));}
 finally{signal.removeEventListener('abort',abort);void reader.cancel().catch(()=>{});reader.releaseLock();}
}
const providerMessages:Readonly<Record<string,MessageId>>={subtitle_provider_not_configured:'subtitles.error.providerNotConfigured',subtitle_provider_key_rejected:'subtitles.error.providerKeyRejected',subtitle_provider_sign_in_failed:'subtitles.error.providerSignInFailed',subtitle_provider_sign_in_required:'subtitles.error.providerSignInRequired',subtitle_provider_busy:'subtitles.error.providerBusy'};
/** OpenSubtitles' refusals, by the server's code: who can fix it, and for a used-up quota, when it resets.
 * Only a pause OpenSubtitles asked for is worth retrying from the player. */
export function providerProblem(e:unknown,now=Date.now()):Readonly<{text:string;canRetry:boolean}>|undefined{
 const problem=e as {code?:unknown;retryAfterSeconds?:unknown}|null;const code=typeof problem?.code==='string'?problem.code:'';
 if(code==='subtitle_provider_quota'){
  const wait=typeof problem?.retryAfterSeconds==='number'&&problem.retryAfterSeconds>0&&problem.retryAfterSeconds<=172800?problem.retryAfterSeconds:0;
  if(!wait)return Object.freeze({text:serviceText('subtitles.error.providerQuotaLater'),canRetry:false});
  const at=now+wait*1000,i18n=serviceI18n();
  return Object.freeze({text:serviceText('subtitles.error.providerQuota',{time:wait<20*3600?i18n.time(at):i18n.dateTime(at)}),canRetry:false});
 }
 const id=providerMessages[code];
 return id?Object.freeze({text:serviceText(id),canRetry:code==='subtitle_provider_busy'}):undefined;
}
export function subtitleReason(reason?:string,translate:(id:MessageId)=>string=id=>enUS[id]):string{
 const key:Record<string,MessageId>={provider_not_configured:'player.subtitleReason.provider',provider_sign_in_failed:'player.subtitleReason.signIn',unsupported_format:'player.subtitleReason.format',unsupported_text_features:'player.subtitleReason.text',extraction_unavailable:'player.subtitleReason.extraction',source_changed:'player.subtitleReason.changed',source_unavailable:'player.subtitleReason.source',ambiguous_association:'player.subtitleReason.association',unsupported_timing:'player.subtitleReason.timing',capacity:'player.subtitleReason.capacity'};
 return translate(key[reason??'']??'player.subtitleReason.unavailable');
}

export type SubtitleTarget=Readonly<{itemId:string;sessionId:string;generation:number;sourceId?:never}>|Readonly<{itemId:string;sourceId:string;sessionId?:never;generation?:never}>;
/** Optional caller lifetime for a file upload or replacement, including time in the queue. */
export type SubtitleSaveContext=Readonly<{signal?:AbortSignal;isCurrent?:()=>boolean}>;
/** A file for a multipart subtitle upload (IM04): a browser Blob/File, or a React Native file
 * reference the platform's networking streams from disk, so its bytes never cross the JS bridge. */
export type SubtitleUploadFile=Blob|Readonly<{uri:string;name:string;type:string}>;
/** The upload port: one multipart write under the captured session, never replayed. */
export type SubtitleUploadApi=LibraryContentApi&{requestSubtitleUpload?<T>(path:string,method:'POST'|'PUT',body:FormData,signal?:AbortSignal):Promise<T>};
function appendUploadFile(body:FormData,name:string,file:SubtitleUploadFile,fileName:string){
 if(typeof Blob!=='undefined'&&file instanceof Blob)body.append(name,file,fileName);
 // React Native's FormData sends a {uri,name,type} part as a file read from disk.
 else body.append(name,file as unknown as Blob);
}
function saveCurrent(context?:SubtitleSaveContext):boolean{try{return !context?.signal?.aborted&&context?.isCurrent?.()!==false;}catch{return false;}}
function saveRetired():DOMException{return new DOMException('Subtitle action retired.','AbortError');}
function checkSave(context?:SubtitleSaveContext):void{if(!saveCurrent(context))throw saveRetired();}
export type SubtitleSnapshot=Readonly<{catalog:SubtitleCatalog|null;plan:SubtitlePlan|null;busy:boolean;loading:boolean;suppressed:boolean;error:string|null;canRetry:boolean;candidates:readonly SubtitleCandidate[]}>;
/** What a playing session's subtitle reads need from the player: a presentation the server moved
 * the session to (a subtitle burned in or taken out again) is handed over here. */
export type SubtitleTransport=Readonly<{install:(presentation:SubtitlePresentation)=>boolean}>;
/** Bound to exactly one viewer/API and playback generation. No disk cache or
 * separate media clock. Management operations do not replace the active pin.
 *
 * It reads the catalogue and the session's plan and manages files. It never selects: a track, Off
 * or a timing correction is a change of the playing session (`PlaybackService.changePlayback`
 * with `subtitleTrackId`, `subtitleDelivery` and `subtitleOffsetMs`), which the server applies,
 * carries through later re-plans and on to the queue's next entry, for this playback only. */
export class SubtitleService {
 private state:SubtitleSnapshot={catalog:null,plan:null,busy:false,loading:true,suppressed:false,error:null,canRetry:false,candidates:[]};
 private listeners=new Set<()=>void>();private active=false;private life=0;private interval?:ReturnType<typeof setInterval>;
 private refreshing?:Promise<void>;private refreshRetry?:ReturnType<typeof setTimeout>;
 private controllers=new Set<AbortController>();private queue:Promise<void>=Promise.resolve();private queued=0;private retryOperation?:Readonly<{task:()=>Promise<void>;context?:SubtitleSaveContext}>;
 private api:SubtitleUploadApi; target:SubtitleTarget; private operationId:()=>string; private transport?:SubtitleTransport;
 constructor(api:SubtitleUploadApi,target:SubtitleTarget,operationId:()=>string,transport?:SubtitleTransport){this.api=api;this.target=target;this.operationId=operationId;this.transport=transport;if(!id(target.itemId)||(target.sourceId!==undefined?!id(target.sourceId):!id(target.sessionId)||!integer(target.generation)))throw new Error('Invalid subtitle target.');}
 getSnapshot=()=>this.state;subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(p:Partial<SubtitleSnapshot>){this.state=Object.freeze({...this.state,...p});this.listeners.forEach(f=>f());}
 private path(suffix=''){return encodedRoutePath("get_v1_items_itemId_subtitles", {"itemId": this.target.itemId}) + (suffix);}
 get sourceId(){return this.target.sourceId??this.state.plan?.sourceId;}
 private planPath(){if(this.target.sourceId!==undefined)throw new Error('Start playback to read the subtitle plan.');return encodedRoutePath("get_v1_items_itemId_playback_session_subtitles", {"itemId": this.target.itemId, "session": this.target.sessionId});}
 private polling=true;
 start(){if(this.active)return;this.active=true;this.life++;void this.refresh();this.arm();}
 private arm(){if(this.interval){clearInterval(this.interval);this.interval=undefined;}if(this.active&&this.polling&&this.target.sourceId===undefined)this.interval=setInterval(()=>void this.refresh(),25000);}
 /** PERF-24: re-reading the catalogue every 25 s is only worth it while subtitles are on or their
  * menu is open; a client turns it off otherwise (a later `setPolling(true)` reads at once). */
 setPolling(on:boolean){if(this.polling===on)return;this.polling=on;this.arm();if(on&&this.active)void this.refresh();}
 stop(){this.active=false;this.life++;if(this.interval)clearInterval(this.interval);if(this.refreshRetry!==undefined)clearTimeout(this.refreshRetry);this.refreshRetry=undefined;this.refreshing=undefined;for(const c of this.controllers)c.abort();this.controllers.clear();this.retryOperation=undefined;this.publish({catalog:null,plan:null,candidates:[],suppressed:true,loading:false});}
 private async request(path:string,method='GET',body?:unknown,context?:SubtitleSaveContext):Promise<unknown>{
  checkSave(context);
  const c=new AbortController(),life=this.life;this.controllers.add(c);const timer=setTimeout(()=>c.abort(),method==='GET'?15000:110000);
  const cancel=()=>c.abort();context?.signal?.addEventListener('abort',cancel,{once:true});
  try{checkSave(context);const result=await this.api.request<unknown>(path,method,body,c.signal);if(!this.active||life!==this.life)throw new Error('Subtitle scope retired.');checkSave(context);return result;}finally{context?.signal?.removeEventListener('abort',cancel);clearTimeout(timer);this.controllers.delete(c);}
 }
 private acceptPlan(raw:unknown){const p=validateSubtitlePlan(raw);if(this.target.generation===undefined||p.sessionId!==this.target.sessionId||p.generation<this.target.generation)invalid();
 if(p.presentation&&p.generation!==this.target.generation){if(!this.transport||!this.transport.install(p.presentation))throw new Error('Subtitle playback scope retired.');}
 if(p.generation!==this.target.generation){if(!p.presentation)invalid();this.target={itemId:this.target.itemId,sessionId:p.sessionId,generation:p.generation};}if(this.state.plan&&p.revision<this.state.plan.revision)return this.state.plan;this.publish({plan:p});return p;}
 private async plan(){return this.acceptPlan(await this.request(this.planPath()));}
 private fail(e:unknown){
  if(!this.active)return;const problem=e as {status?:number;code?:string};
  const denied=problem.status===401||problem.status===403,conflict=problem.status===409;
  const invalidInput=problem.status===400||problem.status===422,missing=problem.status===404;
  const invalidData=problem.code==='invalid_subtitles';
  const provider=providerProblem(e);
  if(provider){this.publish({error:provider.text,canRetry:provider.canRetry});if(!provider.canRetry)this.retryOperation=undefined;return;}
  const canRetry=!denied&&!conflict&&!invalidInput&&!missing&&!invalidData;
  this.publish({error:denied?serviceText('subtitles.error.denied'):conflict?serviceText('subtitles.error.changed'):invalidInput?serviceText('subtitles.error.invalidInput'):missing?serviceText('subtitles.error.missing'):invalidData?unreadableServerResponse:serviceText('subtitles.error.failed'),canRetry,suppressed:denied||invalidData||!this.state.plan,...(denied?{catalog:null,plan:null,candidates:[]}: {})});
  if(!canRetry)this.retryOperation=undefined;
 }
 /** One short recovery burst also runs with catalogue polling disabled. Route/foreground
  * recovery can call refresh again; it never reinstates the 25 s polling loop. */
 refresh():Promise<void>{return this.refreshAttempt(0);}
 private refreshAttempt(attempt:number):Promise<void>{
  if(!this.active||this.queued)return Promise.resolve();
  if(this.refreshing)return this.refreshing;
  if(this.refreshRetry!==undefined)clearTimeout(this.refreshRetry);this.refreshRetry=undefined;
  const life=this.life;
  const work=Promise.resolve().then(()=>this.readRefresh(life,attempt)).finally(()=>{if(this.refreshing===work)this.refreshing=undefined;});
  this.refreshing=work;return work;
 }
 private async readRefresh(life:number,attempt:number){
  if(!this.active||life!==this.life||this.queued)return;
  this.publish({loading:true});
  if(!this.active||life!==this.life||this.queued)return;
  try{
   const [raw]=await Promise.all([this.request(this.path()),this.target.sourceId===undefined?this.plan():Promise.resolve(null)]);
   if(!this.active||life!==this.life||this.queued)return;
   const catalog=validateSubtitleCatalog(raw);
   if(catalog.itemId!==this.target.itemId||this.target.sourceId!==undefined&&!catalog.sources.some(source=>source.id===this.target.sourceId))invalid();
   if(!this.state.catalog||catalog.revision>=this.state.catalog.revision)this.publish({catalog});
   this.publish({loading:false,error:null,suppressed:false,canRetry:false});
  }catch(e){
   if(!this.active||life!==this.life)return;
   this.publish({loading:false});this.fail(e);
   const problem=e as {retryable?:boolean;retry?:string;retryAfterMs?:number;retryAfterSeconds?:number}|null;
   const retryable=problem?.retryable!==false&&(problem?.retry===undefined||problem.retry==='same_request');
   if(this.active&&life===this.life&&this.state.canRetry&&retryable&&attempt<2&&!this.queued){
    const milliseconds=problem?.retryAfterMs,seconds=problem?.retryAfterSeconds;
    const delay=Math.max(500*2**attempt,typeof milliseconds==='number'&&Number.isFinite(milliseconds)&&milliseconds>0?milliseconds:0,typeof seconds==='number'&&Number.isFinite(seconds*1000)&&seconds>0?seconds*1000:0);
    const wait=(remaining:number)=>{this.refreshRetry=setTimeout(()=>{
     this.refreshRetry=undefined;if(!this.active||life!==this.life)return;
     if(remaining>2147483647)wait(remaining-2147483647);else void this.refreshAttempt(attempt+1);
    },Math.min(remaining,2147483647));};
    wait(delay);
   }
  }
 }
 private enqueue(task:()=>Promise<void>,context?:SubtitleSaveContext){
  if(context&&(!this.active||!saveCurrent(context))){this.retryOperation=undefined;if(this.active)this.publish({error:null,canRetry:false});return Promise.reject(saveRetired());}
  if(!this.active)return Promise.resolve();const life=this.life;this.queued++;this.publish({busy:true,error:null,canRetry:false});
  const run=async()=>{try{if(!this.active||life!==this.life){if(context)throw saveRetired();return;}checkSave(context);await task();checkSave(context);this.retryOperation=undefined;this.publish({error:null,canRetry:false});}catch(e){
   if(context&&(!this.active||life!==this.life||!saveCurrent(context))){if(life===this.life){this.retryOperation=undefined;if(this.active)this.publish({error:null,canRetry:false});}throw saveRetired();}
   this.retryOperation={task,context};this.fail(e);
  }};
  const result=this.queue.then(run,run).finally(()=>{this.queued--;if(this.active&&life===this.life){this.publish({busy:this.queued>0});if(!this.queued&&!this.state.error)void this.refresh();}});
  // A canceled caller receives its rejection; the serial queue itself stays usable.
  this.queue=result.then(()=>{},()=>{});return result;
 }
 refreshSources(){return this.enqueue(async()=>{const sourceId=this.sourceId;if(!sourceId)throw new Error('No active source.');const catalog=validateSubtitleCatalog(await this.request(this.path('/refresh'),'POST',{sourceId}));if(catalog.itemId!==this.target.itemId||!catalog.sources.some(source=>source.id===sourceId))invalid();this.publish({catalog});if(this.target.sourceId===undefined)await this.plan();});}
 remote(name:string,format:SubtitleFormat,language:string,title:string,rights:string,scope:'personal'|'shared') {const payload={operationId:this.operationId(),sourceId:this.sourceId,name,format,language,title,rights,scope};return this.enqueue(async()=>{this.receipt(await this.request(this.path('/remote'),'POST',payload));});}
 retry(){if(this.retryOperation)return this.enqueue(this.retryOperation.task,this.retryOperation.context);return this.refresh();}


 private receipt(v:unknown):SubtitleReceipt{if(!record(v)||!id(v.operationId)||!id(v.resourceId)||!integer(v.revision)||!integer(v.catalogRevision)||v.deleted!==undefined&&typeof v.deleted!=='boolean')invalid();return Object.freeze({...v}) as SubtitleReceipt;}
 save(input:{content?:string;data?:string;companion?:string;default?:boolean;forced?:boolean;format:SubtitleFormat;language:string;title:string;rights:string;scope:'personal'|'shared';offsetUs:string},resource?:SubtitleResource,context?:SubtitleSaveContext){
  const payload={...input,operationId:this.operationId(),sourceId:resource?.sourceId??this.sourceId,expectedRevision:resource?.revision??0,...(resource?{resourceId:resource.id}: {})};
  return this.enqueue(async()=>{this.receipt(await this.request(this.path(resource?'/'+resource.id:''),resource?'PUT':'POST',payload,context));},context);
 }
 /** Whether this service can send a file as multipart (`upload`) rather than inside JSON. */
 get canUpload():boolean{return typeof this.api.requestSubtitleUpload==='function';}
 /** Saves a subtitle file as multipart/form-data (IM04): the bounded JSON metadata, then the raw
  * file and a VobSub pair's companion. Same operation, revision and retry rules as `save`. */
 upload(input:{file:SubtitleUploadFile;fileName:string;companion?:SubtitleUploadFile;companionName?:string;default?:boolean;forced?:boolean;format:SubtitleFormat;language:string;title:string;rights:string;scope:'personal'|'shared';offsetUs:string},resource?:SubtitleResource,context?:SubtitleSaveContext){
  const send=this.api.requestSubtitleUpload;
  if(typeof send!=='function')return Promise.reject(new Error('Subtitle upload is unavailable.'));
  const {file,fileName,companion,companionName,...fields}=input;
  if(companion!==undefined&&input.format!=='idx'&&input.format!=='vobsub')return Promise.reject(new Error('Only a VobSub pair has a companion file.'));
  const metadata={...fields,operationId:this.operationId(),sourceId:resource?.sourceId??this.sourceId,expectedRevision:resource?.revision??0,...(resource?{resourceId:resource.id}:{})};
  const path=this.path(resource?'/'+resource.id:''),method=resource?'PUT' as const:'POST' as const;
  return this.enqueue(async()=>{
   const body=new FormData();body.append('metadata',JSON.stringify(metadata));
   appendUploadFile(body,'file',file,fileName);if(companion!==undefined)appendUploadFile(body,'companion',companion,companionName??'subtitle.sub');
   this.receipt(await this.uploadRequest(path,method,body,send,context));
  },context);
 }
 private async uploadRequest(path:string,method:'POST'|'PUT',body:FormData,send:NonNullable<SubtitleUploadApi['requestSubtitleUpload']>,context?:SubtitleSaveContext):Promise<unknown>{
  checkSave(context);
  const c=new AbortController(),life=this.life;this.controllers.add(c);const timer=setTimeout(()=>c.abort(),110000);
  const cancel=()=>c.abort();context?.signal?.addEventListener('abort',cancel,{once:true});
  try{checkSave(context);const result=await send.call(this.api,path,method,body,c.signal);if(!this.active||life!==this.life)throw new Error('Subtitle scope retired.');checkSave(context);return result;}finally{context?.signal?.removeEventListener('abort',cancel);clearTimeout(timer);this.controllers.delete(c);}
 }
 remove(resource:SubtitleResource){const body={operationId:this.operationId(),expectedRevision:resource.revision};return this.enqueue(async()=>{this.receipt(await this.request(this.path('/'+resource.id),'DELETE',body));});}
 search(language='',query='',page=1){return this.enqueue(async()=>{const raw=await this.request(this.path('/search'),'POST',{sourceId:this.sourceId,language,query,page});if(!record(raw)||raw.version!==1||!integer(raw.page))invalid();const candidates=array(raw.candidates,40).map(c=>{if(!record(c)||!id(c.id)||!text(c.language,64)||!text(c.title,160)||!text(c.attribution,512))invalid();return Object.freeze({...c}) as SubtitleCandidate;});this.publish({candidates:Object.freeze(candidates)});});}
 apply(candidate:SubtitleCandidate,scope:'personal'|'shared',rights:string){const payload={operationId:this.operationId(),candidateId:candidate.id,scope,rights};return this.enqueue(async()=>{this.receipt(await this.request(this.path('/apply'),'POST',payload));this.publish({candidates:[]});});}
}

export async function fetchSubtitleFonts(fetcher:typeof fetch,url:string,signal:AbortSignal):Promise<readonly SubtitleFont[]>{
 if(!/^\/v1\/media\/subtitles\/[A-Za-z0-9_-]+\/[A-Za-z0-9_-]+\/[1-9]\d*\/fonts$/.test(url))invalid();
 const response=await fetcher(url,{signal,credentials:'omit',cache:'no-store',redirect:'error'});
 if(!response.ok)throw new Error(enUS['player.subtitleReason.fonts']);
 const raw=await response.text();if(raw.length>32768)invalid();const v:unknown=JSON.parse(raw);if(!record(v))invalid();
 return Object.freeze(array(v.fonts,16).map(f=>{if(!record(f)||!text(f.name,160)||!text(f.url,1024)||!f.url.startsWith(url+'/')||!/^\d+$/.test(f.url.slice(url.length+1)))invalid();return Object.freeze({name:f.name,url:f.url});}));
}
