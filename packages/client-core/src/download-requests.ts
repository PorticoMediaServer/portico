import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {parseDownloadPreparation,type DownloadPreparation} from './downloads.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOf} from './tolerant.ts';
/**
 * Whole-container download requests (`create_download_request`,
 * `get_download_request` and `list_download_requests` in
 * `server/api/portico.openapi.json`):
 * one `POST /v1/downloads/requests` for a show, season, album, book or
 * playlist — never TV — with a policy choosing which episodes to take, then
 * `GET /v1/downloads/requests/{id}?limit=&cursor=` for request progress, and
 * `GET /v1/downloads/requests?deviceId=` to find a device's requests again.
 *
 * Admission returns 202 with `totalKnown:false` while the worker captures
 * visible membership; the client shows an indeterminate capture state until
 * `totalKnown` is true. A library that changes while it is being read does not
 * fail the request: the server reads it again. `ready` means server
 * preparation completed: the client still transfers and verifies the artifact
 * through the per-preparation grant, verification and offline receipt flow.
 */

export type DownloadRequestKind='show'|'season'|'album'|'book'|'playlist';
export type DownloadEpisodes='all'|'unwatched'|'next';
export type DownloadRequestPolicy=Readonly<{episodes:DownloadEpisodes;keepNext?:number}>;
export type DownloadRequestState='capturing'|'admitting'|'preparing'|'complete'|'failed';
export type DownloadRequestErrorCode='authority_revoked'|'target_not_found';
/** One request as a device lists it to find the ones it started. */
export type DownloadRequestSummary=Readonly<{requestId:string;state:DownloadRequestState;target:Readonly<{kind:DownloadRequestKind;id:string}>;title:string;quality:string;createdAt:string}>;
/** One page of a device's requests, newest first; follow `nextCursor` until it is empty. */
export type DownloadRequestPage=Readonly<{requests:readonly DownloadRequestSummary[];nextCursor:string}>;
/** The most requests one discovery page holds. */
export const DOWNLOAD_REQUEST_PAGE=50;
export type DownloadRequestItem=Readonly<{itemId:string;reason?:string;preparation?:DownloadPreparation}>;
export type DownloadRequest=Readonly<{
 requestId:string;total:number;totalKnown:boolean;state:DownloadRequestState;
 errorCode?:DownloadRequestErrorCode;
 ready:number;preparing:number;queued:number;failed:number;paused:number;
 items:readonly DownloadRequestItem[];nextCursor:string;
}>;
export interface DownloadRequestsApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

export const DOWNLOAD_REQUESTS_PATH=routePath("list_download_requests");
export const downloadRequestStates:readonly DownloadRequestState[]=Object.freeze(['capturing','admitting','preparing','complete','failed'] as const);
/** Complete means no queued/running/paused members remain (individual failures may remain). */
export const isDownloadRequestTerminal=(state:DownloadRequestState):boolean=>state==='complete'||state==='failed';

const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,160}$/.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const requestErrorCodes:readonly DownloadRequestErrorCode[]=Object.freeze(['authority_revoked','target_not_found'] as const);
const requestKinds:readonly string[]=Object.freeze(['show','season','album','book','playlist']);

function invalid():never{throw new Error(unreadableServerResponse);}

function policy(v:unknown):DownloadRequestPolicy{
 if(!object(v)||v.episodes!=='all'&&v.episodes!=='unwatched'&&v.episodes!=='next')invalid();
 if(Object.keys(v).some(k=>k!=='episodes'&&k!=='keepNext'))invalid();
 if(v.keepNext!==undefined){
  if(!count(v.keepNext)||(v.keepNext as number)<1||(v.keepNext as number)>10000)invalid();
  if(v.episodes!=='next')invalid();
 }
 return Object.freeze({episodes:v.episodes as DownloadEpisodes,...(v.keepNext===undefined?{}:{keepNext:v.keepNext as number})});
}

export function parseDownloadRequest(raw:unknown):DownloadRequest{
 if(!object(raw)||!id(raw.requestId))invalid();
 if(!count(raw.total)||typeof raw.totalKnown!=='boolean')invalid();
 if(!downloadRequestStates.includes(String(raw.state) as DownloadRequestState))invalid();
 // An error code this app does not know, or one on a request that has not failed, is left out.
 const errorCode=raw.state==='failed'?oneOf(raw.errorCode,requestErrorCodes):undefined;
 if(raw.errorCode!==undefined&&errorCode===undefined)noteSkipped('download-request','errorCode');
 for(const key of ['ready','preparing','queued','failed','paused'] as const)if(!count(raw[key]))invalid();
 if(!Array.isArray(raw.items)||raw.items.length>100)invalid();
 // One member that cannot be read, or is listed twice, is left out and the rest are kept. A reason
 // or a preparation is only shown: one that cannot be read is left out and its member is kept.
 const items=Object.freeze(firstOfEach('download-request','items',keepReadable('download-request','items',raw.items,entry=>{
  if(!object(entry)||!id(entry.itemId))invalid();
  if(entry.reason!==undefined&&typeof entry.reason!=='string')noteSkipped('download-request','item.reason');
  return Object.freeze({
   itemId:entry.itemId as string,
   ...(typeof entry.reason==='string'?{reason:entry.reason}:{}),
   ...(entry.preparation===undefined?{}:keepReadable('download-request','item.preparation',[entry.preparation],v=>({preparation:parseDownloadPreparation(v)}))[0]),
  });
 }),i=>i.itemId));
 if(raw.nextCursor!==undefined&&typeof raw.nextCursor!=='string')invalid();
 return Object.freeze({
  requestId:raw.requestId as string,total:raw.total as number,totalKnown:raw.totalKnown as boolean,
  state:raw.state as DownloadRequestState,
  ...(errorCode===undefined?{}:{errorCode}),
  ready:raw.ready as number,preparing:raw.preparing as number,queued:raw.queued as number,
  failed:raw.failed as number,paused:raw.paused as number,
  items,nextCursor:String(raw.nextCursor??''),
 });
}

export type DownloadRequestInput=Readonly<{
 operationId:string;
 target:Readonly<{kind:DownloadRequestKind;id:string}>;
 deviceId:string;quality?:string;policy:DownloadRequestPolicy;
}>;

/**
 * Submits one whole-container download intent. Send one operationId per intent
 * and retain it for retries; different bodies under that id return 422
 * `idempotency_key_reused`.
 */
export async function createDownloadRequest(api:DownloadRequestsApi,input:DownloadRequestInput,signal?:AbortSignal):Promise<DownloadRequest>{
 if(!object(input)||!/^[A-Za-z0-9_-]{1,160}$/.test(String(input.operationId)))throw new Error('A download request needs a stable operationId.');
 const target=input.target;
 if(!object(target)||target.kind!=='show'&&target.kind!=='season'&&target.kind!=='album'&&target.kind!=='book'&&target.kind!=='playlist'||!id(target.id))throw new Error('Downloads cover shows, seasons, albums, books and playlists.');
 if(!id(input.deviceId))throw new Error('A download request needs the authenticated device id.');
 const quality=input.quality===undefined?'original':input.quality;
 if(typeof quality!=='string'||!canonicalQualityLadder.some(q=>q.id===quality))throw new Error('Choose a download quality.');
 const checkedPolicy=policy(input.policy);
 try{
  return parseDownloadRequest(await api.request<unknown>(DOWNLOAD_REQUESTS_PATH,'POST',{
   operationId:input.operationId,
   target:{kind:target.kind,id:target.id},
   deviceId:input.deviceId,quality,policy:{...checkedPolicy},
  },signal));
 }catch(e){
  const v=e as {status?:unknown;code?:unknown};
  const code=typeof v?.code==='string'&&v.code?v.code:v?.status===422?'idempotency_key_reused':'download_request_not_created';
  throw Object.assign(new Error(e instanceof Error?e.message:'The download could not start.'),{code});
 }
}

export function parseDownloadRequestList(raw:unknown):DownloadRequestPage{
 if(!object(raw)||!Array.isArray(raw.requests)||raw.requests.length>DOWNLOAD_REQUEST_PAGE||raw.nextCursor!==undefined&&(typeof raw.nextCursor!=='string'||raw.nextCursor.length>256))invalid();
 const out=raw.requests.map(entry=>{
  if(!object(entry)||!id(entry.requestId)||!downloadRequestStates.includes(String(entry.state) as DownloadRequestState))invalid();
  const target=entry.target;
  if(!object(target)||!requestKinds.includes(String(target.kind))||!id(target.id))invalid();
  if(typeof entry.title!=='string'||entry.title.length>1024||!id(entry.quality)||typeof entry.createdAt!=='string'||entry.createdAt.length>64)invalid();
  return Object.freeze({requestId:entry.requestId as string,state:entry.state as DownloadRequestState,target:Object.freeze({kind:target.kind as DownloadRequestKind,id:target.id as string}),title:entry.title as string,quality:entry.quality as string,createdAt:entry.createdAt as string});
 });
 if(new Set(out.map(r=>r.requestId)).size!==out.length)invalid();
 return Object.freeze({requests:Object.freeze(out),nextCursor:(raw.nextCursor as string|undefined)??''});
}

/** One page of the whole-container requests this device started that are still at work (however
 * old), or finished in the last day. The server keeps them, so a device finds them again after the
 * app was closed. */
export async function listDownloadRequests(api:DownloadRequestsApi,deviceId:string,options:Readonly<{cursor?:string;signal?:AbortSignal}>={}):Promise<DownloadRequestPage>{
 if(!id(deviceId))throw new Error('A download request needs the authenticated device id.');
 const query=new URLSearchParams({deviceId,limit:String(DOWNLOAD_REQUEST_PAGE)});
 if(options.cursor)query.set('cursor',options.cursor);
 return parseDownloadRequestList(await api.request<unknown>(DOWNLOAD_REQUESTS_PATH+'?'+query,'GET',undefined,options.signal));
}

/** Reads live visible-member counts plus one keyset page of preparations. `limit: 0` reads the
 * counts alone (no members), for a poll that only follows progress. */
export async function getDownloadRequest(api:DownloadRequestsApi,requestId:string,options:Readonly<{cursor?:string;limit?:number;signal?:AbortSignal}>={}):Promise<DownloadRequest>{
 if(!id(requestId))throw new Error('A download request id is required.');
 const limit=options.limit??100;
 if(!Number.isInteger(limit)||limit<0||limit>100||limit===0&&options.cursor)throw new Error('Download request pages read 1 to 100 members, or 0 for counts alone.');
 const query=new URLSearchParams({limit:String(limit)});
 if(options.cursor)query.set('cursor',options.cursor);
 const raw=await api.request<unknown>(DOWNLOAD_REQUESTS_PATH+'/'+encodeURIComponent(requestId)+'?'+query,'GET',undefined,options.signal);
 const parsed=parseDownloadRequest(raw);
 if(parsed.requestId!==requestId)throw new Error(unreadableServerResponse);
 return parsed;
}

/** Polls until the request completes or fails. */
export async function pollDownloadRequest(api:DownloadRequestsApi,requestId:string,options:Readonly<{intervalMs?:number;timeoutMs?:number;signal?:AbortSignal}>={}):Promise<DownloadRequest>{
 const intervalMs=options.intervalMs??2000,timeoutMs=options.timeoutMs??1800000;
 if(!Number.isFinite(intervalMs)||intervalMs<500||intervalMs>15000)throw new Error('Invalid download poll interval.');
 if(!Number.isFinite(timeoutMs)||timeoutMs<1000||timeoutMs>7200000)throw new Error('Invalid download poll timeout.');
 const started=Date.now();
 for(;;){
  if(options.signal?.aborted)throw Object.assign(new Error('The download was cancelled.'),{code:'cancelled'});
  const request=await getDownloadRequest(api,requestId,{signal:options.signal});
  if(isDownloadRequestTerminal(request.state))return request;
  if(Date.now()-started>timeoutMs)throw Object.assign(new Error('The download is still preparing. Reopen Downloads to check it.'),{code:'download_poll_timeout',request});
  await new Promise<void>((resolve,reject)=>{
   let timer:ReturnType<typeof setTimeout>|undefined;const signal=options.signal;
   const cancel=()=>{if(timer!==undefined)clearTimeout(timer);signal?.removeEventListener('abort',cancel);reject(Object.assign(new Error('The download was cancelled.'),{code:'cancelled'}));};
   timer=setTimeout(()=>{signal?.removeEventListener('abort',cancel);resolve();},intervalMs);
   signal?.addEventListener('abort',cancel,{once:true});
  });
 }
}
