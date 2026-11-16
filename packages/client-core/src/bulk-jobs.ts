import {validSelectionBasis} from './selection-basis.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {noteSkipped,oneOf} from './tolerant.ts';
import type {Job as ServerJob, JobStart} from '../../contracts/src/next/api.ts';
import type {BrowseNode} from './browse.ts';
import {libraryItemQuery} from './library-browse.ts';
import {serverPath} from './query/server-api.ts';
/**
 * Bulk actions on a selection, in this app's terms (a command, a selector, its args) and done on the rebuilt server:
 * see `createJob` below for which route each takes. Nothing here enumerates a view: a whole view is the server's
 * job over the view's query.
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
 /** A selection of titles is done here, one title at a time: its job ends as it starts, with what failed and why. */
 failures?:readonly JobFailure[];
}>;
export type JobFailure=Readonly<{itemId:string;code:string}>;
export type JobFailurePage=Readonly<{items:readonly JobFailure[];nextCursor:string}>;
export interface BulkJobsApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

export const JOB_PATH=serverPath('POST /v1/jobs');
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

function failureCode(e:unknown,fallback:string):string{
 const v=e as {status?:unknown;code?:unknown};
 if(typeof v?.code==='string'&&v.code)return v.code;
 // Contract correction: a reused key is 422, a stale revision is 409.
 if(v?.status===422)return IDEMPOTENCY_REUSED;
 if(v?.status===409)return REVISION_MISMATCH;
 return fallback;
}

/**
 * The rebuilt server (definition 06, rules 39 to 42). A whole view is one of its jobs (`POST /v1/jobs`): `set_state`
 * or `add_view_to_list`, the view sent as the query of `GET /v1/items` that lists it. Chosen titles are done one at a
 * time with the title's own route (`PUT /v1/items/{id}/state`, `POST /v1/lists/{id}/entries`), so the answer says
 * what failed title by title and the rest is done; Refresh metadata on them is the owner's `refresh_items` job.
 */
const serverKinds:Readonly<Partial<Record<JobCommand,string>>>={'personal-state':'set_state','playlist-add':'add_view_to_list','collection-add':'add_view_to_list',refresh:'refresh_items'};
const commandOfKind:Readonly<Record<string,JobCommand>>={set_state:'personal-state',add_view_to_list:'playlist-add',refresh_items:'refresh',edit_items:'metadata-edit'};
const unavailable=()=>Object.assign(new Error('This action is not available on this server for this selection.'),{code:'job_kind_unavailable'});
/** A job this app ran title by title: its id is local, and it never needs reading again. */
const localJob=(id:string)=>id.startsWith('titles-');

/** The view a query selector names, as the query of `GET /v1/items` (its sort is no part of the view). */
export function jobViewFilter(query:Extract<JobSelector,{query:unknown}>['query']):string{
 const list=libraryItemQuery({id:query.libraryId},query.pivot,query.filter===undefined||query.filter===null?[]:[query.filter as BrowseNode],[]);
 const out=new URLSearchParams();
 for(const [name,value] of Object.entries(list))if(value!==undefined&&value!=='')out.set(name,String(value));
 return out.toString();
}
function stateParams(a:JobArgs){
 return {...(a.watched===undefined?{}:{watched:a.watched}),...(a.favorite===undefined?{}:{favorite:a.favorite}),
  ...(a.watchlist===undefined?{}:{inList:a.watchlist}),...(a.rating===undefined?{}:{rating:a.rating})};
}
const listOf=(a:JobArgs)=>a.playlistId??a.collectionId;
function serverStart(body:ReturnType<typeof jobRequestBody>):JobStart{
 const kind=serverKinds[body.command];
 if(!kind)throw unavailable();
 if(body.command==='refresh'){
  if(!('items' in body.selector||'container' in body.selector))throw unavailable();
  return {kind,target:{items:'items' in body.selector?[...body.selector.items.ids]:[body.selector.container.id]}};
 }
 if(!('query' in body.selector))throw unavailable();
 return {kind,target:{filter:jobViewFilter(body.selector.query)},params:body.command==='personal-state'?stateParams(body.args):{list:listOf(body.args)}};
}
/** The rebuilt server's job in this app's terms. It counts what it has done; a job that failed says so as a whole. */
function serverJob(raw:ServerJob,fallback:JobCommand):Job{
 if(!object(raw)||!id(raw.id)||typeof raw.state!=='string')invalid();
 const state:JobState=raw.state==='done'?'complete':raw.state==='failed'||raw.state==='cancelled'?'failed':raw.state==='running'?'running':'queued';
 const total=count(raw.progress?.total)?raw.progress.total:undefined,done=count(raw.progress?.done)?raw.progress.done:0;
 return Object.freeze({jobId:raw.id,command:commandOfKind[String(raw.kind)]??fallback,state,total:total??0,totalKnown:total!==undefined,
  done:total===undefined?done:Math.min(done,total),failed:0,...(state==='failed'?{errorCode:'execution_failed' as const}:{})});
}
/** One title at a time: each title's own answer, its failure kept with the server's reason. */
async function titleByTitle(api:BulkJobsApi,body:ReturnType<typeof jobRequestBody>,signal?:AbortSignal):Promise<Job>{
 const ids='items' in body.selector?body.selector.items.ids:'container' in body.selector?[body.selector.container.id]:[];
 const list=listOf(body.args),failures:JobFailure[]=[];
 for(const itemId of ids){
  if(signal?.aborted)throw Object.assign(new Error('The bulk action was cancelled.'),{code:'cancelled'});
  try{
   if(body.command==='personal-state')await api.request(serverPath('PUT /v1/items/{id}/state',{id:itemId}),'PUT',stateParams(body.args),signal);
   else await api.request(serverPath('POST /v1/lists/{id}/entries',{id:list!}),'POST',{items:[itemId]},signal);
  }catch(e){
   if(signal?.aborted)throw e;
   failures.push(Object.freeze({itemId,code:failureCode(e,'failed')}));
  }
 }
 const failed=failures.length;
 return Object.freeze({jobId:'titles-'+body.operationId,command:body.command,state:failed===ids.length?'failed':failed?'partial':'complete',
  total:ids.length,totalKnown:true,done:ids.length-failed,failed,failures:Object.freeze(failures)});
}

/** Submits one bulk action. Repeat the same operationId and body on a transport retry. */
export async function createJob(api:BulkJobsApi,request:JobRequest,signal?:AbortSignal):Promise<Job>{
 const body=jobRequestBody(request);
 const perTitle=(body.command==='personal-state'||body.command==='playlist-add'||body.command==='collection-add')&&!('query' in body.selector);
 if(perTitle)return titleByTitle(api,body,signal);
 try{
  return serverJob(await api.request<ServerJob>(serverPath('POST /v1/jobs'),'POST',serverStart(body),signal),body.command);
 }catch(e){
  if((e as {code?:unknown})?.code==='job_kind_unavailable')throw e;
  throw Object.assign(new Error(e instanceof Error?e.message:'The bulk action could not start.'),{code:failureCode(e,'job_not_created')});
 }
}

/** Reads this profile's progress for one job. */
export async function getJob(api:BulkJobsApi,jobId:string,signal?:AbortSignal):Promise<Job>{
 if(!id(jobId)||localJob(jobId))throw new Error('A job id is required.');
 return serverJob(await api.request<ServerJob>(serverPath('GET /v1/jobs/{id}',{id:jobId}),'GET',undefined,signal),'personal-state');
}

/** The failures of a job this app ran title by title are on the job itself; the rebuilt server's jobs list none
 * (a failed job fails as a whole), so there is no page to read. */
export async function getJobFailures(_api:BulkJobsApi,jobId:string,_cursor?:string,_signal?:AbortSignal):Promise<JobFailurePage>{
 if(!id(jobId))throw new Error('A job id is required.');
 return Object.freeze({items:Object.freeze([]),nextCursor:''});
}
