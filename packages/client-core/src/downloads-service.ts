import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {decodeGetContainerDownloadOptionsResponse} from "../../contracts/src/decoders.ts";
import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {randomId} from './random-id.ts';
import {mergeRevalidatedList} from './freshness-actions.ts';
/** The server side of downloads, as one framework-free service shared by web and Apple: what
 * the server is preparing for this profile, what may be asked for, and the actions on each.
 *
 * Forgiving by design: a failed refresh keeps the list that is on screen. While anything is
 * still being prepared it polls gently; once everything has settled it stops. */
import {serviceProblem} from './presentation/service-text.ts';
import {downloadReasonMessage,parseDownloadProfileRevocation,parseDownloadBatch,parseDownloadBundle,parseDownloadGrant,parseDownloadOptions,parseDownloadPage,parseDownloadPreparation,parseDownloadSettings,parseDownloadUsage,type DownloadAction,type DownloadBatch,type DownloadBundle,type DownloadGrant,type DownloadOptionsView,type DownloadPreparation,type DownloadQualityChoice,type DownloadUsage,originalQuality} from './downloads.ts';
import {createDownloadRequest,getDownloadRequest,isDownloadRequestTerminal,listDownloadRequests,type DownloadEpisodes,type DownloadRequest,type DownloadRequestKind} from './download-requests.ts';

export type DownloadsApi={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
/** Items for one preparation batch. A whole container (show, season, album, book, playlist) is
 * never a batch target: it goes through `requestContainer`, the durable container request. */
export type DownloadTarget=Readonly<{versionId?:string;mediaId?:string;mediaIds?:readonly string[];nextAfterMediaId?:string}>;
/** A whole-container request (show, season, album, book or playlist — never TV). */
export type ContainerDownloadTarget=Readonly<{kind:DownloadRequestKind;id:string;title?:string}>;
export type ContainerDownloadPolicy=Readonly<{episodes:DownloadEpisodes;keepNext?:number}>;
export type TrackedDownloadRequest=Readonly<{request:DownloadRequest;title:string;error?:string}>;
export type DownloadsSnapshot=Readonly<{phase:'idle'|'loading'|'ready'|'error';items:readonly DownloadPreparation[];usage?:DownloadUsage;busy:readonly string[];error?:string;/** The failure behind `error`, for `presentError`/`say` (category, code, retry). */failure?:unknown;unavailable?:boolean;/** The next page cursor (`''` when the list is complete). */nextCursor:string;/** A further page is being read. */loadingMore:boolean;/** Whole-container requests started on this device, newest first. */requests:readonly TrackedDownloadRequest[]}>;

const ACTIVE=new Set(['queued','running']);
/** One full-list page (the server allows up to 200; 100 keeps a poll page small). */
const PAGE_LIMIT=100;
/** Discovery reads at most this many pages of a device's requests per pass. */
const DISCOVERY_PAGES=10;
const progressMark=(r:DownloadRequest)=>`${r.state}/${r.totalKnown}/${r.total}/${r.ready}/${r.preparing}/${r.queued}/${r.failed}/${r.paused}`;
// X-04: the words come from the catalogue; `failure` keeps the error for screens that present it themselves.
const say=(e:unknown,fallback:'downloads.error.load'|'downloads.error.action')=>serviceProblem(e,'downloads',fallback,fallback==='downloads.error.load'?'load':'action');

export class DownloadsService{
 private api:DownloadsApi;private operationId:()=>string;private pollMs:number;
 private state:DownloadsSnapshot=Object.freeze<DownloadsSnapshot>({phase:'idle',items:[],busy:[],nextCursor:'',loadingMore:false,requests:[]});
 private listeners=new Set<()=>void>();private timer?:ReturnType<typeof setTimeout>;private generation=0;private disposed=false;private watching=false;
 /** Failed reads in a row. The list is tried again by itself, a little later each time. */
 private failures=0;
 /** Rotate bounded probes through claims outside the first active pages. */
 private probeOffset=0;
 /** One bounded, fair poller follows all container requests. */
 private requestTimer?:ReturnType<typeof setTimeout>;
 private requestPoll?:Promise<void>;
 private requestRefresh?:Promise<void>;
 /** A failed discovery is read again quietly; the list's own error stays the list's. */
 private rediscover?:ReturnType<typeof setTimeout>;
 private pendingRequests=new Set<string>();
 private lifetime=new AbortController();
 /** Whole-container requests followed on this device, by request id. */
 private tracked=new Map<string,TrackedDownloadRequest>();
 /** Each followed request's progress when its first member page was last read. */
 private memberMarks=new Map<string,string>();
 /** The device whose whole-container requests are found again on the server (`followDevice`). */
 private deviceId='';
 constructor(api:DownloadsApi,operationId:()=>string=()=>randomId(),pollMs=5000){this.api=api;this.operationId=operationId;this.pollMs=pollMs;}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(patch:Partial<DownloadsSnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...patch});this.listeners.forEach(f=>f());}
 dispose(){this.disposed=true;this.generation++;clearTimeout(this.timer);clearTimeout(this.requestTimer);clearTimeout(this.rediscover);this.lifetime.abort();this.pendingRequests.clear();this.listeners.clear();}

 /** Starts following the list. Safe to call again; `stop` ends the polling without forgetting. */
 start(){this.watching=true;void this.refresh();void this.refreshRequests();}
 stop(){this.watching=false;clearTimeout(this.timer);}
 private pagePath(cursor:string,state?:string){return routePath("list_download_preparations") + "?limit=" + (PAGE_LIMIT) + (cursor?'&cursor='+encodeURIComponent(cursor):'') + (state?'&state='+encodeURIComponent(state):'');}
 /** PERF-S12: the UI pages the list — one page per call, newest first — instead of re-reading up
  * to 10 × 100 every 5 s. Every page is reachable; nothing is silently truncated. */
 async refresh():Promise<void>{
  const generation=++this.generation;clearTimeout(this.timer);
  if(this.state.phase==='idle')this.publish({phase:'loading'});
  try{
   const result=parseDownloadPage(await this.api.request<unknown>(this.pagePath('')));
   if(generation!==this.generation)return;
   this.failures=0;
   this.publish({phase:'ready',items:Object.freeze(result.items.filter(p=>p.state!=='cancelled'||p.actions.length>0)),nextCursor:result.nextCursor,loadingMore:false,error:undefined,failure:undefined,unavailable:false});
    void this.api.request<RouteResponse<"get_download_usage">>(routePath("get_download_usage")).then(raw=>{try{if(generation===this.generation)this.publish({usage:parseDownloadUsage(raw)});}catch{}},()=>{});
  }catch(e){
   if(generation!==this.generation)return;
   const status=(e as {status?:number}|null)?.status, code=(e as {code?:string}|null)?.code;
   // Feature absence is only the unsupported route or the server's explicit canonical code.
   // A generic 503 (server_busy/timeout) is a transient failure and keeps its retry path.
   if(status===404||code==='downloads_unavailable'){this.failures=0;this.publish({phase:'ready',unavailable:true,error:undefined,failure:undefined});}
   else{this.failures++;this.publish({phase:this.state.items.length?'ready':'error',error:say(e,'downloads.error.load'),failure:e,unavailable:false});}
  }
  this.schedule(generation);
 }
 /**
  * Reads the list again behind what is shown (a download asked for on another device, one the
  * server finished, failed or let go). The first page is merged in: rows already loaded below it
  * stay, unchanged rows are the same rows. Nothing is said if the read fails: what is shown stays
  * and the call rejects, for the caller to try again later.
  */
 async revalidate():Promise<void>{
  if(this.disposed||this.state.phase!=='ready'||this.state.unavailable||this.state.loadingMore)return;
  const generation=this.generation;
  const result=parseDownloadPage(await this.api.request<unknown>(this.pagePath('')));
  if(generation!==this.generation||this.disposed||this.state.loadingMore)return;
  const fresh=result.items.filter(p=>p.state!=='cancelled'||p.actions.length>0);
  const items=mergeRevalidatedList(this.state.items,fresh,p=>p.id,{same:(a,b)=>JSON.stringify(a)===JSON.stringify(b),complete:!result.nextCursor});
  if(items!==this.state.items)this.publish({items:Object.freeze([...items]),...(this.state.nextCursor?{}:{nextCursor:result.nextCursor})});
  void this.api.request<RouteResponse<"get_download_usage">>(routePath("get_download_usage")).then(raw=>{try{if(generation===this.generation)this.publish({usage:parseDownloadUsage(raw)});}catch{}},()=>{});
  // Something that started being prepared elsewhere is followed at the ordinary pace from here.
  clearTimeout(this.timer);this.schedule(generation);
 }
 /** Reads the next page and appends it (deduplicated by id). A no-op without a cursor. */
 async loadMore():Promise<void>{
  const cursor=this.state.nextCursor;
  if(!cursor||this.state.loadingMore||this.disposed)return;
  const generation=this.generation;
  this.publish({loadingMore:true});
  try{
   const result=parseDownloadPage(await this.api.request<unknown>(this.pagePath(cursor)));
   if(generation!==this.generation||this.disposed){this.publish({loadingMore:false});return;}
   const seen=new Set(this.state.items.map(p=>p.id));
   const items=[...this.state.items,...result.items.filter(p=>(p.state!=='cancelled'||p.actions.length>0)&&!seen.has(p.id))];
   this.publish({items:Object.freeze(items),nextCursor:result.nextCursor,loadingMore:false});
  }catch(e){
   if(generation!==this.generation||this.disposed)return;
   this.publish({loadingMore:false,error:say(e,'downloads.error.load'),failure:e});
  }
 }
 /** Reads exact preparations without walking or extending the on-screen list. Callers recover
  * their durable preparation ids in batches of at most 200, within their own owner lifetime. */
 async lookupPreparations(ids:readonly string[],signal?:AbortSignal):Promise<readonly DownloadPreparation[]>{
  if(ids.length>200||ids.some(id=>!/^[A-Za-z0-9_-]{1,160}$/.test(id)))throw new Error('Read up to 200 preparation ids.');
  if(this.disposed||signal?.aborted)throw Object.assign(new Error('Download lookup cancelled.'),{name:'AbortError'});
  const wanted=new Set(ids);
  if(!wanted.size)return Object.freeze([]);
  const controller=new AbortController(),abort=()=>controller.abort();
  signal?.addEventListener('abort',abort,{once:true});this.lifetime.signal.addEventListener('abort',abort,{once:true});
  try{
   const query=new URLSearchParams({limit:'200',ids:[...wanted].join(',')});
   const result=parseDownloadPage(await this.api.request<unknown>(routePath("list_download_preparations")+'?'+query,'GET',undefined,controller.signal));
   if(controller.signal.aborted)throw Object.assign(new Error('Download lookup cancelled.'),{name:'AbortError'});
   if(result.nextCursor||result.items.some(p=>!wanted.has(p.id)))throw new Error(unreadableServerResponse);
   return Object.freeze(result.items);
  }finally{signal?.removeEventListener('abort',abort);this.lifetime.signal.removeEventListener('abort',abort);}
 }
 private schedule(generation:number){
  if(!this.watching||generation!==this.generation||this.disposed)return;
  // A list that could not be read is tried again quietly, three polls later at first and then
  // less and less often (five minutes at most), so a server that is away is not asked forever
  // at full pace. While something is being prepared it is polled at the ordinary pace.
  if(this.failures>0)this.timer=setTimeout(()=>void this.refresh(),Math.min(this.pollMs*3*2**Math.min(this.failures-1,6),300000));
  else if(this.state.items.some(p=>ACTIVE.has(p.state)))this.timer=setTimeout(()=>void this.poll(),this.pollMs);
 }
 /** PERF-S12: the 5 s poll reads only active preparations — one page each of `queued` and
  * `running`. Queued claims are not capped, so a third sparse page reads up to 200 loaded
  * claims absent from those pages, rotating through older rows. Finished preparations
  * settle without a full reload or individual requests. Stops when nothing is active. */
 private async poll():Promise<void>{
  const generation=this.generation;clearTimeout(this.timer);
  try{
   const [queued,running]=await Promise.all([
    parseDownloadPage(await this.api.request<unknown>(this.pagePath('', 'queued'))),
    parseDownloadPage(await this.api.request<unknown>(this.pagePath('', 'running'))),
   ]);
   if(generation!==this.generation||this.disposed)return;
   const active=[...queued.items,...running.items];
   const seen=new Set(active.map(p=>p.id));
   const byId=new Map(this.state.items.map(p=>[p.id,p]));
   for(const p of active)byId.set(p.id,p);
   // A sparse page reads older loaded claims together, including transitions
   // out of active states. One bounded request replaces a request per claim.
   const missing=this.state.items.filter(p=>ACTIVE.has(p.state)&&!seen.has(p.id));
   const start=missing.length?this.probeOffset%missing.length:0;
   const probes=Array.from({length:Math.min(200,missing.length)},(_,i)=>missing[(start+i)%missing.length]);
   this.probeOffset=start+probes.length;
   if(probes.length){
    const page=parseDownloadPage(await this.api.request<RouteResponse<"list_download_preparations">>(routePath("list_download_preparations") + "?" + (new URLSearchParams({limit:'200',ids:probes.map(p=>p.id).join(',')}))));
    const returned=new Set(page.items.map(p=>p.id));
    for(const p of page.items)byId.set(p.id,p);
    for(const p of probes)if(!returned.has(p.id))byId.delete(p.id);
   }
   if(generation!==this.generation||this.disposed)return;
   // Page order first, then actives from beyond the loaded pages.
   const paged:DownloadPreparation[]=[];
   for(const p of this.state.items){const current=byId.get(p.id);if(current)paged.push(current);}
   for(const p of active)if(!paged.some(q=>q.id===p.id))paged.push(p);
   this.failures=0;
   this.publish({phase:'ready',items:Object.freeze(paged.filter(p=>p.state!=='cancelled'||p.actions.length>0)),error:undefined,failure:undefined});
  }catch(e){
   if(generation!==this.generation||this.disposed)return;
   this.failures++;
   this.publish({phase:this.state.items.length?'ready':'error',error:say(e,'downloads.error.load'),failure:e});
  }
  this.schedule(generation);
 }
 /** What may be downloaded for one title, with sizes; the server decides, this shows it. */
 async options(itemId:string,signal?:AbortSignal):Promise<DownloadOptionsView>{
  return parseDownloadOptions(await this.api.request<RouteResponse<"get_download_options">>(encodedRoutePath("get_download_options", {"itemId": encodeURIComponent(itemId)}),'GET',undefined,signal),itemId);
 }
 /** Combined sizes for the same visible selection a container request admits. */
 async containerOptions(target:ContainerDownloadTarget,deviceId:string,policy:ContainerDownloadPolicy,signal?:AbortSignal){
  const result=decodeGetContainerDownloadOptionsResponse(await this.api.request<unknown>(routePath("get_container_download_options"),'POST',routeBody("get_container_download_options",{target:{kind:target.kind,id:target.id},deviceId,policy:{...policy}}),signal));
  if(result.target.id!==target.id||result.target.kind!==target.kind)throw new Error(unreadableServerResponse);
  return result;
 }

 /** The qualities a whole show, season, album, book or playlist may be asked for: the original,
  * and each smaller quality this server can make. The server decides; nothing is assumed here. */
 async revokeProfile(profileKey:string){return parseDownloadProfileRevocation(await this.api.request<RouteResponse<"revoke_profile_downloads">>(routePath("revoke_profile_downloads"),'POST',routeBody("revoke_profile_downloads", {profileKey,operationId:this.operationId()})));}

 async serverUsage(cursor='',limit=256){return parseDownloadUsage(await this.api.request<RouteResponse<"get_download_usage">>(routePath("get_download_usage") + "?scope=server&cursor=" + (encodeURIComponent(cursor)) + "&limit=" + (limit)));}

 async qualities(signal?:AbortSignal):Promise<readonly DownloadQualityChoice[]>{
  return parseDownloadSettings(await this.api.request<RouteResponse<"get_download_settings">>(routePath("get_download_settings"),'GET',undefined,signal)).qualities;
 }
 /** Asks the server to prepare one title, several, a whole season or album, or the next one.
  * A batch is partial by design: what could not be accepted is reported, the rest proceeds. */
 async request(target:DownloadTarget,quality:string=originalQuality):Promise<DownloadBatch>{
  if(!canonicalQualityLadder.some(q=>q.id===quality))throw new Error('Choose a download quality.');
  // Only the item fields are sent, whatever else the caller's object carries (containers are durable requests).
  const body={operationId:this.operationId(),...(target.versionId?{versionId:target.versionId}:{}),...(target.mediaId?{mediaId:target.mediaId}:{}),...(target.mediaIds?{mediaIds:[...target.mediaIds]}:{}),...(target.nextAfterMediaId?{nextAfterMediaId:target.nextAfterMediaId}:{}),quality};
  const batch=parseDownloadBatch(await this.api.request<RouteResponse<"create_download_preparations">>(routePath("create_download_preparations"),'POST',body));
  void this.refresh();return batch;
 }
 /**
  * Starts one whole-container download (show, season, album, book or playlist)
  * and follows its request until it completes or fails. The caller keeps its
  * own operationId per intent for safe retries. Capture shows as an
  * indeterminate state until `totalKnown` is true.
  */
 async requestContainer(input:Readonly<{target:ContainerDownloadTarget;deviceId:string;quality?:string;policy:ContainerDownloadPolicy;operationId?:string}>):Promise<DownloadRequest>{
  if(!input.target||input.target.kind!=='show'&&input.target.kind!=='season'&&input.target.kind!=='album'&&input.target.kind!=='book'&&input.target.kind!=='playlist'||!input.target.id)throw new Error('Whole downloads cover shows, seasons, albums, books and playlists.');
  const title=input.target.title??'';
  const request=await createDownloadRequest(this.api,{operationId:input.operationId??this.operationId(),target:{kind:input.target.kind,id:input.target.id},deviceId:input.deviceId,quality:input.quality,policy:{...input.policy}});
  this.tracked.set(request.requestId,Object.freeze({request,title}));
  this.publish({requests:Object.freeze([...this.tracked.values()])});
  if(!isDownloadRequestTerminal(request.state))this.pendingRequests.add(request.requestId);
  this.scheduleRequests();
  return request;
 }
 /** Names the device this service runs on. The whole-show, season, album, book and playlist
  * downloads it started are then found again on the server whenever the list is followed, so
  * closing the app, or signing in again, doesn't lose track of one that is still being prepared:
  * the server keeps the request, and this is how its remaining members reach the device. */
 followDevice(deviceId:string){
  if(this.deviceId===deviceId)return;
  this.deviceId=deviceId;
  if(this.watching)void this.refreshRequests();
 }
 /** Discovers requests and queues every followed id. Each turn reads at most 20, two at a time;
  * unfinished ids return to the tail so requests after the first 20 cannot starve. */
 refreshRequests():Promise<void>{
  if(this.disposed)return Promise.resolve();
  if(this.requestRefresh)return this.requestRefresh;
  const work=(async()=>{
   let deviceId:string;
   do{
    deviceId=this.deviceId;
    if(deviceId){
     try{
      // Every page, up to a bound: a device can have more requests at work than one page holds.
      let cursor='';
      for(let pages=0;pages<DISCOVERY_PAGES;pages++){
       const listed=await listDownloadRequests(this.api,deviceId,{cursor,signal:this.lifetime.signal});
       if(this.disposed||deviceId!==this.deviceId)break;
       for(const summary of listed.requests)if(!this.tracked.has(summary.requestId)){
        this.tracked.set(summary.requestId,Object.freeze({title:summary.title,request:Object.freeze({requestId:summary.requestId,total:0,totalKnown:false,state:summary.state,ready:0,preparing:0,queued:0,failed:0,paused:0,items:Object.freeze([]),nextCursor:''})}));
       }
       if(!listed.nextCursor||listed.nextCursor===cursor)break;
       cursor=listed.nextCursor;
      }
      if(this.disposed)return;
      if(deviceId!==this.deviceId)continue;
     }catch{
      // Requests already followed are still read below; discovery is tried again later.
      if(this.disposed)return;
      if(deviceId===this.deviceId&&this.rediscover===undefined)this.rediscover=setTimeout(()=>{this.rediscover=undefined;if(this.watching)void this.refreshRequests();},this.pollMs*3);
     }
    }
    if(deviceId!==this.deviceId)continue;
    for(const id of this.tracked.keys())this.pendingRequests.add(id);
    await this.pollRequests();
    // Native starts the list before supplying its device. Coalescing must not lose
    // that discovery, or a device change made while the preceding list was pending.
   }while(!this.disposed&&deviceId!==this.deviceId);
  })();
  const tracked=work.finally(()=>{if(this.requestRefresh===tracked)this.requestRefresh=undefined;});
  this.requestRefresh=tracked;return tracked;
 }
 private scheduleRequests(){
  if(this.disposed||this.requestTimer!==undefined||this.requestPoll||!this.pendingRequests.size)return;
  this.requestTimer=setTimeout(()=>{this.requestTimer=undefined;void this.pollRequests();},this.pollMs);
 }
 private pollRequests():Promise<void>{
  if(this.disposed)return Promise.resolve();
  if(this.requestPoll)return this.requestPoll;
  clearTimeout(this.requestTimer);this.requestTimer=undefined;
  const ids=[...this.pendingRequests].slice(0,20);
  for(const id of ids)this.pendingRequests.delete(id);
  let next=0,completed=false;
  const worker=async()=>{
   while(next<ids.length&&!this.disposed){
    const id=ids[next++],prior=this.tracked.get(id);
    if(!prior)continue;
    try{
     // Counts alone while nothing has moved; the first member page only when progress changed
     // since it was last read (it carries the preparations a device enrols).
     const status=await getDownloadRequest(this.api,id,{limit:0,signal:this.lifetime.signal});
     if(this.disposed)return;
     const mark=progressMark(status),unchanged=this.memberMarks.get(id)===mark;
     const request=unchanged?Object.freeze({...status,items:prior.request.items,nextCursor:prior.request.nextCursor}):await getDownloadRequest(this.api,id,{signal:this.lifetime.signal});
     if(this.disposed)return;
     if(!unchanged)this.memberMarks.set(id,progressMark(request));
     const current=this.tracked.get(id);
     if(!current)continue;
     this.tracked.set(id,Object.freeze({...current,request,error:undefined}));
     if(!isDownloadRequestTerminal(request.state))this.pendingRequests.add(id);
     else completed=completed||!isDownloadRequestTerminal(prior.request.state);
    }catch(e){
     if(this.disposed)return;
     if((e as {status?:number}|null)?.status===404){this.tracked.delete(id);this.memberMarks.delete(id);}
     else{const current=this.tracked.get(id);if(current){this.tracked.set(id,Object.freeze({...current,error:say(e,'downloads.error.load')}));this.pendingRequests.add(id);}}
    }
   }
  };
  const work=Promise.all([worker(),worker()]).then(()=>{
   if(this.disposed)return;
   this.publish({requests:Object.freeze([...this.tracked.values()])});
   if(completed)void this.refresh();
  });
  const tracked=work.finally(()=>{if(this.requestPoll===tracked)this.requestPoll=undefined;this.scheduleRequests();});
  this.requestPoll=tracked;return tracked;
 }
 /** Applies one action. The server fences an action on the row's revision; when the row moved
  * between the screen reading it and the person pressing the button, the row is read again and
  * the action sent once more if it still applies, so a person only hears about a conflict when
  * what they acted on has really become something else. */
 async act(preparation:DownloadPreparation,action:DownloadAction):Promise<void>{
  if(this.state.busy.includes(preparation.id))return;
  this.publish({busy:[...this.state.busy,preparation.id],error:undefined,failure:undefined});
  const path=encodedRoutePath("get_download_preparation", {"id": encodeURIComponent(preparation.id)});
  const send=async(revision:number)=>parseDownloadPreparation(await this.api.request<unknown>(path+'/actions','POST',{operationId:this.operationId(),action,expectedRevision:revision}));
  const show=(next:DownloadPreparation)=>this.publish({items:action==='remove'?this.state.items.filter(p=>p.id!==next.id):this.state.items.map(p=>p.id===next.id?next:p)});
  try{
   show(await send(preparation.revision));
  }catch(e){
   let failure:unknown=e;
   if((e as {status?:number}|null)?.status===409){
    try{
     const current=parseDownloadPreparation(await this.api.request<unknown>(path));
     if(current.actions.includes(action)){show(await send(current.revision));failure=undefined;}
     else{
      // It became something this action no longer applies to (it finished, or was removed
      // elsewhere). Showing it as it is now is the answer; there is nothing to report.
      this.publish({items:this.state.items.map(p=>p.id===current.id?current:p)});failure=undefined;
     }
    }catch(again){failure=again;}
   }
   if(failure!==undefined)this.publish({error:say(failure,'downloads.error.action'),failure});
  }finally{this.publish({busy:this.state.busy.filter(id=>id!==preparation.id)});void this.refresh();}
 }
 /** The address the prepared file is fetched from. Pass the token of the address this device
  * already holds for the download (`renew`) to keep that same address alive; a transfer the
  * device's own downloader is holding then carries on under it. */
 async grant(preparation:Pick<DownloadPreparation,'id'>,renew?:string):Promise<DownloadGrant>{
  return parseDownloadGrant(await this.api.request<RouteResponse<"create_download_grant">>(encodedRoutePath("create_download_grant", {"id": encodeURIComponent(preparation.id)}),'POST',routeBody("create_download_grant", {operationId:this.operationId(),...(renew?{renew}:{})})));
 }
 /** Everything that travels with a ready download: title facts, artwork, chapters, markers,
  * audio and subtitle tracks. The files it names are fetched under the grant's address. */
 async bundle(preparation:Pick<DownloadPreparation,'id'>,signal?:AbortSignal):Promise<DownloadBundle>{
  return parseDownloadBundle(await this.api.request<RouteResponse<"get_download_bundle">>(encodedRoutePath("get_download_bundle", {"id": encodeURIComponent(preparation.id)}),'GET',undefined,signal),preparation.id);
 }
 reason(preparation:DownloadPreparation):string{return preparation.reason?downloadReasonMessage(preparation.reason):'';}
}
