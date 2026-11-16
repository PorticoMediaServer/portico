import {enUS,type MessageId} from '../../i18n/src/index.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {serviceI18n, serviceText} from './presentation/service-text.ts';
import {callServer,type ServerRequester} from './query/server-api.ts';
import {serverNow} from './server-clock.ts';
import {parseWebVtt} from './playback-v1/webvtt.ts';

export type SubtitleFormat='srt'|'vtt'|'ass'|'ssa'|'pgs'|'sup'|'vobsub'|'idx'|'dvb'|'smi'|'sami'|'ttml'|'dfxp';
export type SubtitleRendering='text'|'ass'|'pgs'|'vobsub'|'dvb';
/** `linesUrl` (image subtitles): how many of the track's lines are forced (`parseSubtitleLines`). */
export type SubtitleDelivery=Readonly<{format:SubtitleRendering;url?:string;companionUrl?:string;fontsUrl?:string;linesUrl?:string}>;
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
/** `italic` and `top` are the two presentation facts every player can honour:
 * a cue that is wholly italic, and a cue authored at the top of the picture. */
export type SubtitleCue=Readonly<{startUs:string;endUs:string;text:string;italic?:boolean;top?:boolean}>;
export type SubtitleDocument=Readonly<{version:1;timeDomain:'source-relative';cues:readonly SubtitleCue[];skippedCues?:number}>;
export type SubtitleCandidate=Readonly<{id:string;language:string;title:string;attribution:string}>;
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
 const type=response.headers.get('Content-Type')?.toLowerCase()??'',vtt=type.startsWith('text/vtt');
 if(!vtt&&!type.startsWith('application/json'))invalid();
 const max=8388608,declared=response.headers.get('Content-Length');if(declared!==null&&(!/^\d+$/.test(declared)||Number(declared)>max))invalid();
 if(!response.body)invalid();const reader=response.body.getReader();const chunks:Uint8Array[]=[];let total=0;
 const abort=()=>{void reader.cancel().catch(()=>{});};signal.addEventListener('abort',abort,{once:true});
 try{while(!signal.aborted){const {done,value}=await reader.read();if(done)break;total+=value.length;if(total>max)invalid();chunks.push(value);}if(signal.aborted)throw new Error('Subtitle request cancelled.');const bytes=new Uint8Array(total);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}const decoded=new TextDecoder('utf-8',{fatal:true}).decode(bytes);return vtt?parseWebVtt(decoded):validateSubtitleDocument(JSON.parse(decoded));}
 finally{signal.removeEventListener('abort',abort);void reader.cancel().catch(()=>{});reader.releaseLock();}
}
const providerMessages:Readonly<Record<string,MessageId>>={subtitle_search_not_set_up:'subtitles.error.providerNotConfigured',subtitle_account_not_set_up:'subtitles.error.providerAccountRequired',subtitle_key_not_set_up:'subtitles.error.providerKeyRequired',subtitle_key_refused:'subtitles.error.providerKeyRejected',subtitle_sign_in_refused:'subtitles.error.providerSignInFailed',provider_paused:'subtitles.error.providerBusy',subtitle_provider_not_configured:'subtitles.error.providerNotConfigured',subtitle_provider_key_rejected:'subtitles.error.providerKeyRejected',subtitle_provider_sign_in_failed:'subtitles.error.providerSignInFailed',subtitle_provider_sign_in_required:'subtitles.error.providerSignInRequired',subtitle_provider_busy:'subtitles.error.providerBusy'};
/** OpenSubtitles' refusals, by the server's code: who can fix it, and for a used-up quota, when it resets.
 * Only a pause OpenSubtitles asked for is worth retrying from the player. */
export function providerProblem(e:unknown,now=serverNow()):Readonly<{text:string;canRetry:boolean}>|undefined{
 const problem=e as {code?:unknown;retryAfterSeconds?:unknown}|null;const code=typeof problem?.code==='string'?problem.code:'';
 if(code==='subtitle_provider_quota'||code==='subtitle_allowance_used'){
  const wait=typeof problem?.retryAfterSeconds==='number'&&problem.retryAfterSeconds>0&&problem.retryAfterSeconds<=172800?problem.retryAfterSeconds:0;
  if(!wait)return Object.freeze({text:serviceText('subtitles.error.providerQuotaLater'),canRetry:false});
  const at=now+wait*1000,i18n=serviceI18n();
  return Object.freeze({text:serviceText('subtitles.error.providerQuota',{time:wait<20*3600?i18n.time(at):i18n.dateTime(at)}),canRetry:false});
 }
 const id=providerMessages[code];
 return id?Object.freeze({text:serviceText(id),canRetry:code==='subtitle_provider_busy'||code==='provider_paused'}):undefined;
}
/** The setup prerequisite the server found missing; shared by the web player and SubtitleService. */
export function subtitleSetupReason(code:unknown):string|undefined{
 return code==='subtitle_search_not_set_up'?'provider_not_configured':code==='subtitle_account_not_set_up'?'provider_account_not_configured':code==='subtitle_key_not_set_up'?'provider_key_not_configured':undefined;
}
export function subtitleReason(reason?:string,translate:(id:MessageId)=>string=id=>enUS[id]):string{
 const key:Record<string,MessageId>={provider_not_configured:'player.subtitleReason.provider',provider_account_not_configured:'subtitles.error.providerAccountRequired',provider_key_not_configured:'subtitles.error.providerKeyRequired',provider_sign_in_failed:'player.subtitleReason.signIn',unsupported_format:'player.subtitleReason.format',unsupported_text_features:'player.subtitleReason.text',extraction_unavailable:'player.subtitleReason.extraction',source_changed:'player.subtitleReason.changed',source_unavailable:'player.subtitleReason.source',ambiguous_association:'player.subtitleReason.association',unsupported_timing:'player.subtitleReason.timing',capacity:'player.subtitleReason.capacity'};
 return translate(key[reason??'']??'player.subtitleReason.unavailable');
}

export type SubtitleSnapshot=Readonly<{plan:SubtitlePlan|null;provider:Readonly<{enabled:boolean;reason?:string}>;busy:boolean;loading:boolean;suppressed:boolean;error:string|null;canRetry:boolean;candidates:readonly SubtitleCandidate[]}>;

/** OpenSubtitles results for a title in the given languages (07 rules 65 to 68). Each carries the provider's credit. */
export async function searchItemSubtitles(api:ServerRequester,itemId:string,languages:readonly string[],signal?:AbortSignal):Promise<readonly SubtitleCandidate[]>{
 const page=await callServer(api,'GET /v1/items/{id}/subtitles/search',{id:itemId,query:{languages:languages.join(',')},signal});
 return Object.freeze((page.items??[]).map(r=>Object.freeze({id:r.ref,language:r.language,title:r.release||r.language.toUpperCase(),attribution:page.credit})));
}
/** Downloads one result of this profile's recent search for this title. It is then offered to everyone who may
 * play that version, as a track of the playback options; the answer is that track. */
export const downloadItemSubtitle=(api:ServerRequester,itemId:string,versionId:string|undefined,ref:string,signal?:AbortSignal)=>
 callServer(api,'POST /v1/items/{id}/subtitles',{id:itemId,body:{...(versionId?{versionId}:{}),ref},signal});

/**
 * One playing title's subtitles on a device. The plan is the session's own (what is on and its WebVTT), made from
 * the session and its playback options as `readPlayerOffers` makes it for web; nothing is read for it. Searching and
 * downloading go to OpenSubtitles through the server. A track, Off or a timing correction is a change of the playing
 * session (`changePlayback` with `subtitleTrackId` or `subtitleOffsetMs`), never made here.
 */
export class SubtitleService {
 private state:SubtitleSnapshot={plan:null,provider:Object.freeze({enabled:true}),busy:false,loading:true,suppressed:false,error:null,canRetry:false,candidates:[]};
 private listeners=new Set<()=>void>();private active=false;private controllers=new Set<AbortController>();
 private last?:()=>Promise<unknown>;
 private api:ServerRequester;private readPlan:()=>SubtitlePlan|null;readonly itemId:string;
 constructor(api:ServerRequester,itemId:string,readPlan:()=>SubtitlePlan|null){this.api=api;this.itemId=itemId;this.readPlan=readPlan;if(!id(itemId))throw new Error('Invalid subtitle target.');}
 getSnapshot=()=>this.state;subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(p:Partial<SubtitleSnapshot>){this.state=Object.freeze({...this.state,...p});this.listeners.forEach(f=>f());}
 start(){if(this.active)return;this.active=true;void this.refresh();}
 stop(){this.active=false;for(const c of this.controllers)c.abort();this.controllers.clear();this.last=undefined;this.publish({plan:null,candidates:[],suppressed:true,loading:false,busy:false});}
 /** Takes the plan again from the session and its options (after either arrives or changes). */
 refresh():Promise<void>{if(this.active)this.publish({plan:this.readPlan(),loading:false,suppressed:false});return Promise.resolve();}
 private async run<T>(task:(signal:AbortSignal)=>Promise<T>):Promise<T|undefined>{
  if(!this.active)return undefined;
  const c=new AbortController();this.controllers.add(c);const timer=setTimeout(()=>c.abort(),60000);
  this.last=()=>this.run(task);this.publish({busy:true,error:null,canRetry:false});
  try{const out=await task(c.signal);if(this.active){this.last=undefined;this.publish({busy:false});}return out;}
  catch(e){
   if(!this.active)return undefined;
   const code=(e as {code?:unknown})?.code,problem=providerProblem(e),setup=subtitleSetupReason(code);
   this.publish({busy:false,error:problem?.text??serviceText('subtitles.error.failed'),canRetry:problem?problem.canRetry:(e as {retryable?:unknown})?.retryable!==false,
    ...(setup?{provider:Object.freeze({enabled:false,reason:setup}),candidates:[]}:{})});
   return undefined;
  }finally{clearTimeout(timer);this.controllers.delete(c);}
 }
 search(language:string){return this.run(async signal=>{const candidates=await searchItemSubtitles(this.api,this.itemId,[language],signal);if(this.active)this.publish({candidates});}).then(()=>{});}
 /** Downloads a search result for the version playing; answers the new track's id, or undefined when it failed. */
 apply(candidate:SubtitleCandidate){const versionId=this.state.plan?.sourceId;return this.run(async signal=>{const added=await downloadItemSubtitle(this.api,this.itemId,versionId,candidate.id,signal);if(this.active)this.publish({candidates:[]});return added.id;});}
 retry():Promise<void>{const last=this.last;return last?last().then(()=>{}):this.refresh();}
}
