import {validSelectionBasis} from './selection-basis.ts';
import {routePath} from "../../contracts/src/route-paths.ts";
import {randomId} from './random-id.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOf} from './tolerant.ts';
/**
 * Bulk jobs (`server/api/portico.openapi.json`):
 * one `POST /v1/jobs` for a whole personal-state, playlist-add, collection-add,
 * metadata-edit, refresh or trash action, then `GET /v1/jobs/{id}` (or a
 * `job.updated` event on `/v1/events`) for progress and
 * `GET /v1/jobs/{id}/failures?cursor=` for the currently visible failures.
 *
 * Parsers plus thin request helpers, in the style of `downloads.ts`. Nothing
 * here enumerates membership: selectors name a container, a query or at most
 * 200 explicit item ids, and the server captures membership asynchronously
 * (`totalKnown:false` until capture finishes).
 */

export type JobContainerKind='show'|'season'|'album'|'artist'|'book'|'collection'|'playlist'|'library';
export type JobSelector=
 | Readonly<{container:Readonly<{kind:JobContainerKind;id:string}>}>
 | Readonly<{items:Readonly<{ids:readonly string[]}>}>
 | Readonly<{query:Readonly<{libraryId:string;pivot:string;selectionBasis?:string;filter?:unknown;sort?:readonly {field:string;direction:'asc'|'desc'}[]}>}>;
export type JobCommand='personal-state'|'playlist-add'|'collection-add'|'metadata-edit'|'refresh';
export type JobPlacement='end'|'next'|Readonly<{after:string}>;
export type JobArgs=Readonly<{
 watched?:boolean;favorite?:boolean;watchlist?:boolean;rating?:number|null;
 playlistId?:string;expectedRevision?:number;placement?:JobPlacement;
 collectionId?:string;
 fields?:Readonly<Record<string,unknown>>;lists?:Readonly<Record<string,unknown>>;genres?:unknown;lockEdited?:boolean;
}>;
export type JobRequest=Readonly<{
 operationId:string;command:JobCommand;selector:JobSelector;args:JobArgs;
 expected?:Readonly<{catalogRevision?:string}>;
}>;
export type JobState='queued'|'running'|'complete'|'partial'|'failed';
export type JobErrorCode='selection_changed'|'selection_too_large'|'authority_revoked'|'execution_failed'|'revision_mismatch'|'destination_not_found';
export type Job=Readonly<{
 jobId:string;command:JobCommand;state:JobState;
 total:number;totalKnown:boolean;done:number;failed:number;
 errorCode?:JobErrorCode;failuresCursor?:string;
}>;
export type JobFailure=Readonly<{itemId:string;code:string}>;
export type JobFailurePage=Readonly<{items:readonly JobFailure[];nextCursor:string}>;
export type JobUpdatedEvent=Readonly<{
 jobId:string;command:JobCommand;state:JobState;
 total:number;totalKnown:boolean;done:number;failed:number;errorCode?:JobErrorCode;
}>;
export interface BulkJobsApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

export const JOB_PATH=routePath("create_job");
/** Terminal states: the client stops polling and reads the failures page. */
export const jobTerminalStates:readonly JobState[]=Object.freeze(['complete','partial','failed'] as const);
export const isJobTerminal=(state:JobState):boolean=>state==='complete'||state==='partial'||state==='failed';
/**
 * Shared error-code correction: `idempotency_key_reused` is HTTP 422,
 * `revision_mismatch` is HTTP 409. Earlier Phase 1 text saying 409 for a reused
 * key was incorrect.
 */
export const IDEMPOTENCY_REUSED='idempotency_key_reused';
export const REVISION_MISMATCH='revision_mismatch';

const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=256&&!/[\x00-\x1f\x7f]/.test(v);
const operationId=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const commands:readonly string[]=Object.freeze(['personal-state','playlist-add','collection-add','metadata-edit','refresh']);
const states:readonly string[]=Object.freeze(['queued','running','complete','partial','failed']);
const errorCodes:readonly string[]=Object.freeze(['selection_changed','selection_too_large','authority_revoked','execution_failed','revision_mismatch','destination_not_found']);
const containerKinds:readonly string[]=Object.freeze(['show','season','album','artist','book','collection','playlist','library']);

function invalid():never{throw new Error(unreadableServerResponse);}

function selector(v:unknown):JobSelector{
 if(!object(v))invalid();
 const keys=Object.keys(v);
 if(keys.length!==1)invalid();
 if(v.container!==undefined){
  const c=v.container;
  if(!object(c)||!containerKinds.includes(String(c.kind))||!id(c.id)||Object.keys(c).some(k=>k!=='kind'&&k!=='id'))invalid();
  return Object.freeze({container:Object.freeze({kind:c.kind as JobContainerKind,id:c.id as string})});
 }
 if(v.items!==undefined){
  const items=v.items;
  if(!object(items)||!Array.isArray(items.ids)||items.ids.length<1||items.ids.length>200||Object.keys(items).some(k=>k!=='ids'))invalid();
  for(const entryId of items.ids)if(!id(entryId))invalid();
  if(new Set(items.ids as string[]).size!==(items.ids as string[]).length)invalid();
  return Object.freeze({items:Object.freeze({ids:Object.freeze([...items.ids as string[]])})});
 }
 if(v.query!==undefined){
  const q=v.query;
  if(!object(q)||!id(q.libraryId)||!id(q.pivot)||Object.keys(q).some(k=>k!=='libraryId'&&k!=='pivot'&&k!=='filter'&&k!=='sort'&&k!=='selectionBasis'))invalid();
  if(q.selectionBasis!==undefined&&!validSelectionBasis(q.selectionBasis))invalid();
  let sort:readonly {field:string;direction:'asc'|'desc'}[]|undefined;
  if(q.sort!==undefined){
   if(!Array.isArray(q.sort))invalid();
   sort=Object.freeze((q.sort as unknown[]).map(entry=>{
    if(!object(entry)||!id(entry.field)||entry.direction!=='asc'&&entry.direction!=='desc'||Object.keys(entry).some(k=>k!=='field'&&k!=='direction'))invalid();
    return Object.freeze({field:entry.field as string,direction:entry.direction as 'asc'|'desc'});
   }));
  }
  return Object.freeze({query:Object.freeze({libraryId:q.libraryId as string,pivot:q.pivot as string,...(q.selectionBasis===undefined?{}:{selectionBasis:q.selectionBasis as string}),...(q.filter===undefined?{}:{filter:q.filter}),...(sort===undefined?{}:{sort})})});
 }
 invalid();
}

function placement(v:unknown):JobPlacement{
 if(v==='end'||v==='next')return v;
 if(object(v)&&Object.keys(v).length===1&&id(v.after))return Object.freeze({after:v.after as string});
 invalid();
}

function args(command:JobCommand,v:unknown):JobArgs{
 if(!object(v))invalid();
 if(command==='personal-state'){
  const keys=Object.keys(v);
  if(!keys.length||keys.some(k=>k!=='watched'&&k!=='favorite'&&k!=='watchlist'&&k!=='rating'))invalid();
  if(v.watched!==undefined&&typeof v.watched!=='boolean'||v.favorite!==undefined&&typeof v.favorite!=='boolean'||v.watchlist!==undefined&&typeof v.watchlist!=='boolean')invalid();
  if(v.rating!==undefined&&!(v.rating===null||typeof v.rating==='number'&&v.rating>=0&&v.rating<=10&&Number.isInteger(v.rating)))invalid();
  return Object.freeze({...v} as JobArgs);
 }
 if(command==='playlist-add'){
  if(!id(v.playlistId)||!count(v.expectedRevision)||(v.expectedRevision as number)<1||v.placement===undefined||Object.keys(v).some(k=>k!=='playlistId'&&k!=='expectedRevision'&&k!=='placement'))invalid();
  return Object.freeze({playlistId:v.playlistId as string,expectedRevision:v.expectedRevision as number,placement:placement(v.placement)});
 }
 if(command==='collection-add'){
  if(!id(v.collectionId)||!count(v.expectedRevision)||(v.expectedRevision as number)<1||Object.keys(v).some(k=>k!=='collectionId'&&k!=='expectedRevision'))invalid();
  return Object.freeze({collectionId:v.collectionId as string,expectedRevision:v.expectedRevision as number});
 }
 if(command==='metadata-edit'){
  const keys=Object.keys(v);
  if(!keys.length||keys.some(k=>k!=='fields'&&k!=='lists'&&k!=='genres'&&k!=='lockEdited'))invalid();
  if(v.lockEdited!==undefined&&typeof v.lockEdited!=='boolean')invalid();
  return Object.freeze({...v} as JobArgs);
 }
 if(!object(v)||Object.keys(v).length)invalid();
 return Object.freeze({});
}

/** Validates a job request before it is sent: one selector, command-specific args. */
export function jobRequestBody(request:JobRequest):Readonly<{operationId:string;command:JobCommand;selector:JobSelector;args:JobArgs;expected?:{catalogRevision:string}}> {
 if(!object(request)||!operationId(request.operationId)||!commands.includes(request.command))throw new Error('A job needs a stable operationId and a known command.');
 const checkedSelector=selector(request.selector);
 const checkedArgs=args(request.command,request.args);
 if(request.expected!==undefined){
  if(!object(request.expected)||Object.keys(request.expected).some(k=>k!=='catalogRevision')||request.expected.catalogRevision!==undefined&&!id(request.expected.catalogRevision))throw new Error('Invalid job revision fence.');
  if(request.expected.catalogRevision===undefined)throw new Error('Invalid job revision fence.');
 }
 return Object.freeze({
  operationId:request.operationId,command:request.command,selector:checkedSelector,args:checkedArgs,
  ...(request.expected===undefined?{}:{expected:{catalogRevision:request.expected.catalogRevision as string}}),
 });
}

function progress(v:unknown):Job{
 if(!object(v)||!id(v.jobId)||!commands.includes(String(v.command))||!states.includes(String(v.state)))invalid();
 if(!count(v.total)||typeof v.totalKnown!=='boolean'||!count(v.done)||!count(v.failed))invalid();
 // A finished count above a known total reads as the total: progress never claims more than the job holds.
 const done=v.totalKnown?Math.min(v.done as number,v.total as number):v.done as number;
 if(done!==v.done)noteSkipped('bulk-jobs','job.done');
 // A failure code this app does not know, or one on a job that has not failed, is left out; the caller has its own general wording.
 const errorCode=v.state==='failed'?oneOf(v.errorCode,errorCodes as readonly JobErrorCode[]):undefined;
 if(v.errorCode!==undefined&&errorCode===undefined)noteSkipped('bulk-jobs','job.errorCode');
 if(v.failuresCursor!==undefined&&!id(v.failuresCursor))invalid();
 return Object.freeze({
  jobId:v.jobId as string,command:v.command as JobCommand,state:v.state as JobState,
  total:v.total as number,totalKnown:v.totalKnown as boolean,done,failed:v.failed as number,
  ...(errorCode===undefined?{}:{errorCode}),
  ...(v.failuresCursor===undefined?{}:{failuresCursor:v.failuresCursor as string}),
 });
}

/** A 202 receipt or a 200 poll read: same shape. */
export function parseJob(raw:unknown):Job{return progress(raw);}

export function parseJobFailures(raw:unknown):JobFailurePage{
 if(!object(raw)||!Array.isArray(raw.items)||raw.items.length>100)invalid();
 // A failure that cannot be read is left out alone, and an item named twice keeps its first row.
 const items=Object.freeze(firstOfEach('bulk-jobs','failures',keepReadable('bulk-jobs','failures',raw.items,entry=>{
  if(!object(entry)||!id(entry.itemId)||!id(entry.code))invalid();
  return Object.freeze({itemId:entry.itemId as string,code:entry.code as string});
 }),i=>i.itemId));
 if(raw.nextCursor!==undefined&&(typeof raw.nextCursor!=='string'||raw.nextCursor.length>4096))invalid();
 return Object.freeze({items,nextCursor:String(raw.nextCursor??'')});
}

/** A `job.updated` event payload from `/v1/events` carries profile-scoped progress, no item titles. */
export function parseJobUpdatedEvent(raw:unknown):JobUpdatedEvent{
 const parsed=progress(raw);
 return parsed as JobUpdatedEvent;
}

/** Parse one SSE `data:` payload for a `job.updated` frame; null when it is another event. */
export function parseJobUpdatedFrame(event:string,data:string):JobUpdatedEvent|null{
 if(event!=='job.updated')return null;
 let raw:unknown;
 try{raw=JSON.parse(data);}catch{throw new Error(unreadableServerResponse);}
 // An update in a state or for a command this app does not know is passed over like any other event; the next read of the job says where it stands.
 if(object(raw)&&id(raw.jobId)&&(typeof raw.state==='string'&&!states.includes(raw.state)||typeof raw.command==='string'&&!commands.includes(raw.command))){noteSkipped('bulk-jobs','event');return null;}
 return parseJobUpdatedEvent(raw);
}

function failureCode(e:unknown,fallback:string):string{
 const v=e as {status?:unknown;code?:unknown};
 if(typeof v?.code==='string'&&v.code)return v.code;
 // Contract correction: a reused key is 422, a stale revision is 409.
 if(v?.status===422)return IDEMPOTENCY_REUSED;
 if(v?.status===409)return REVISION_MISMATCH;
 return fallback;
}

/** Submits one bulk action. Repeat the same operationId and body on a transport retry. */
export async function createJob(api:BulkJobsApi,request:JobRequest,signal?:AbortSignal):Promise<Job>{
 const body=jobRequestBody(request);
 try{
  return parseJob(await api.request<unknown>(JOB_PATH,'POST',body,signal));
 }catch(e){throw Object.assign(new Error(e instanceof Error?e.message:'The bulk action could not start.'),{code:failureCode(e,'job_not_created')});}
}

/** Reads this profile's durable progress for one job. */
export async function getJob(api:BulkJobsApi,jobId:string,signal?:AbortSignal):Promise<Job>{
 if(!id(jobId))throw new Error('A job id is required.');
 return parseJob(await api.request<unknown>(JOB_PATH+'/'+encodeURIComponent(jobId),'GET',undefined,signal));
}

/** Pages the failures still visible to this viewer (up to 100 per page). */
export async function getJobFailures(api:BulkJobsApi,jobId:string,cursor?:string,signal?:AbortSignal):Promise<JobFailurePage>{
 if(!id(jobId))throw new Error('A job id is required.');
 const query=cursor?'?cursor='+encodeURIComponent(cursor):'';
 return parseJobFailures(await api.request<unknown>(JOB_PATH+'/'+encodeURIComponent(jobId)+'/failures'+query,'GET',undefined,signal));
}

/** Collects every visible failure page (bounded: at most 50 pages of 100). */
export async function getAllJobFailures(api:BulkJobsApi,jobId:string,signal?:AbortSignal):Promise<readonly JobFailure[]>{
 const out:JobFailure[]=[];
 let cursor='';
 for(let page=0;page<50;page++){
  const result=await getJobFailures(api,jobId,cursor||undefined,signal);
  out.push(...result.items);
  if(!result.nextCursor)break;
  if(result.nextCursor===cursor)throw new Error(unreadableServerResponse);
  cursor=result.nextCursor;
 }
 return Object.freeze(out);
}

/**
 * Polls until the job reaches a terminal state. Shows an indeterminate
 * "preparing" state while `totalKnown` is false: the worker is still capturing
 * membership in durable keyset pages. `selection_changed` fails before any
 * mutation: refresh and submit a new operationId.
 */
export async function pollJob(api:BulkJobsApi,jobId:string,options:Readonly<{intervalMs?:number;timeoutMs?:number;signal?:AbortSignal}>={}):Promise<Job>{
 const intervalMs=options.intervalMs??1500,timeoutMs=options.timeoutMs??300000;
 if(!Number.isFinite(intervalMs)||intervalMs<250||intervalMs>15000)throw new Error('Invalid job poll interval.');
 if(!Number.isFinite(timeoutMs)||timeoutMs<1000||timeoutMs>1800000)throw new Error('Invalid job poll timeout.');
 const started=Date.now();
 for(;;){
  if(options.signal?.aborted)throw Object.assign(new Error('The bulk action was cancelled.'),{code:'cancelled'});
  const job=await getJob(api,jobId,options.signal);
  if(isJobTerminal(job.state))return job;
  if(Date.now()-started>timeoutMs)throw Object.assign(new Error('The bulk action is still running. Reopen it to check the result.'),{code:'job_poll_timeout',job});
  await new Promise<void>((resolve,reject)=>{
   let timer:ReturnType<typeof setTimeout>|undefined;const signal=options.signal;
   const cancel=()=>{if(timer!==undefined)clearTimeout(timer);signal?.removeEventListener('abort',cancel);reject(Object.assign(new Error('The bulk action was cancelled.'),{code:'cancelled'}));};
   timer=setTimeout(()=>{signal?.removeEventListener('abort',cancel);resolve();},intervalMs);
   signal?.addEventListener('abort',cancel,{once:true});
  });
 }
}

/** Builds an items selector of at most 200 distinct ids (the server's cap). */
export function itemsSelector(ids:readonly string[]):JobSelector{
 const distinct=[...new Set(ids)];
 if(!distinct.length||distinct.length>200)throw new Error('A job carries 1 to 200 explicit items.');
 for(const entryId of distinct)if(!id(entryId))throw new Error('Invalid item id.');
 return Object.freeze({items:Object.freeze({ids:Object.freeze(distinct)})});
}

/** Splits a larger explicit selection into one 200-item job request each. */
export function itemsJobRequests(operationIds:readonly string[],command:JobCommand,args:JobArgs,ids:readonly string[]):readonly JobRequest[]{
 const distinct=[...new Set(ids)];
 if(!distinct.length)throw new Error('A job needs at least one item.');
 const chunks:string[][]=[];
 for(let i=0;i<distinct.length;i+=200)chunks.push(distinct.slice(i,i+200));
 if(operationIds.length<chunks.length)throw new Error('Each bulk job needs its own operationId.');
 return Object.freeze(chunks.map((chunkIds,chunk)=>Object.freeze({
  operationId:operationIds[chunk]!,
  command,selector:itemsSelector(chunkIds),args,
 }) as JobRequest));
}

/** One job submission: the server selector plus the ids the caller counts as requested. */
export type BulkRequest=Readonly<{selector:JobSelector;operationId:string;ids:readonly string[]}>;
export type BulkProgress=Readonly<{
 /** preparing: membership still capturing (`totalKnown:false`); working: determinate. */
 phase:'preparing'|'working';
 /** Completed jobs' totals plus the running job's progress. */
 done:number;total:number;totalKnown:boolean;
}>;
export type BulkOutcome=Readonly<{ok:number;failed:readonly JobFailure[];jobs:number}>;

const BULK_POLL_MS=1500;
const BULK_TIMEOUT_MS=300000;

const sleep=(ms:number,signal?:AbortSignal)=>new Promise<void>((resolve,reject)=>{
 let timer:ReturnType<typeof setTimeout>|undefined;
 const cancel=()=>{if(timer!==undefined)clearTimeout(timer);signal?.removeEventListener('abort',cancel);reject(Object.assign(new Error('The bulk action was cancelled.'),{code:'cancelled'}));};
 timer=setTimeout(()=>{signal?.removeEventListener('abort',cancel);resolve();},ms);
 signal?.addEventListener('abort',cancel,{once:true});
});

/**
 * Runs explicit job requests for one bulk action, reporting progress across
 * every job (M25-4: shared by web and Apple; web `screens/shared/bulk-job.ts`
 * re-exports this). While `totalKnown` is false the worker is still capturing
 * membership: the caller shows an indeterminate "Preparing selection" state. A
 * `selection_changed` failure happens before any mutation: the caller tells
 * the viewer to refresh and submit again with a new operationId.
 */
export async function runBulkRequests(
 api:BulkJobsApi,
 command:JobCommand,
 args:JobArgs,
 requests:readonly BulkRequest[],
 onProgress?:(progress:BulkProgress)=>void,
 signal?:AbortSignal,
):Promise<BulkOutcome>{
 if(!requests.length)throw new Error('A bulk action needs at least one request.');
 let ok=0,jobs=0;
 let failed:JobFailure[]=[];
 const totals={done:0,total:0,known:true};
 for(const request of requests){
  const created=await createJob(api,{operationId:request.operationId,command,selector:request.selector,args},signal);
  const started=Date.now();
  let current=created;
  for(;;){
   if(signal?.aborted)throw Object.assign(new Error('The bulk action was cancelled.'),{code:'cancelled'});
   current=await getJob(api,created.jobId,signal);
   onProgress?.({phase:current.totalKnown&&totals.known?'working':'preparing',done:totals.done+current.done,total:totals.total+current.total,totalKnown:current.totalKnown&&totals.known});
   if(isJobTerminal(current.state))break;
   if(Date.now()-started>BULK_TIMEOUT_MS)throw Object.assign(new Error('The bulk action is still running. Reopen it to check the result.'),{code:'job_poll_timeout'});
   await sleep(BULK_POLL_MS,signal);
  }
  jobs++;
  totals.done+=current.totalKnown?current.done:0;
  totals.total+=current.totalKnown?current.total:0;
  totals.known=totals.known&&current.totalKnown;
  if(current.state==='failed'){
   throw Object.assign(new Error('The bulk action could not finish.'),{code:current.errorCode??'job_failed',job:current});
  }
  ok+=current.done;
  if(current.failed>0)failed=[...failed,...(await getAllJobFailures(api,created.jobId,signal))];
 }
 return {ok,failed:Object.freeze(failed),jobs};
}

/** Runs the chunked items-selector jobs for one bulk action over explicit ids. */
export function runBulkJobs(
 api:BulkJobsApi,
 command:JobCommand,
 args:JobArgs,
 ids:readonly string[],
 operationIds:readonly string[],
 onProgress?:(progress:BulkProgress)=>void,
 signal?:AbortSignal,
):Promise<BulkOutcome>{
 const chunked=itemsJobRequests(operationIds,command,args,ids);
 return runBulkRequests(api,command,args,chunked.map(request=>({selector:request.selector,operationId:request.operationId,ids:request.selector&&'items' in request.selector?request.selector.items.ids:[]})),onProgress,signal);
}

/** Failure codes the bulk UI names honestly instead of counting silently. */
export function bulkFailureCounts(failures:readonly JobFailure[]):Readonly<Record<string,number>>{
 const counts:Record<string,number>={};
 for(const failure of failures)counts[failure.code]=(counts[failure.code]??0)+1;
 return counts;
}

/**
 * Groups metadata targets into job selectors (M25-4: shared by web and Apple).
 * Item targets share one items selector per 200 ids; each container target
 * (show, season, album, artist, book) is its own container selector. The
 * caller supplies operation ids (web: `randomId()`; Apple: its own
 * id factory, since `crypto.randomUUID` is unavailable on Hermes).
 */
export function groupMetadataTargets(
 targets:readonly {kind:string;id:string}[],
 makeOperationId:()=>string,
):readonly BulkRequest[]{
 const items=targets.filter(t=>t.kind==='item');
 const containers=(targets.filter(t=>t.kind==='show'||t.kind==='season'||t.kind==='album'||t.kind==='artist'||t.kind==='book') as readonly {kind:'show'|'season'|'album'|'artist'|'book';id:string}[]);
 const out:BulkRequest[]=[];
 for(let i=0;i<items.length;i+=200){
  const slice=items.slice(i,i+200);
  out.push({selector:{items:{ids:slice.map(t=>t.id)}},operationId:makeOperationId(),ids:slice.map(t=>t.id)});
 }
 for(const t of containers)out.push({selector:{container:{kind:t.kind,id:t.id}},operationId:makeOperationId(),ids:[t.id]});
 return Object.freeze(out);
}
