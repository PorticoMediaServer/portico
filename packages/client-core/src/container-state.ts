import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {noteSkipped} from './tolerant.ts';
/**
 * Container personal state (`server/api/portico.openapi.json`): an atomic watched mark for a show, season, album or book,
 * and the title's own Watchlist and Favorite flags (a show can be on the Watchlist; a show, album,
 * book or artist can be a Favorite), so a show's action row matches a movie's.
 *
 * `GET /v1/containers/{kind}/{id}/personal-state` reads the revision the
 * approved PUT contract requires, plus the viewer's visible-member counts.
 * `PUT` writes one profile/container watermark with `{expectedRevision,
 * watched}`; a 412 `revision_mismatch` means reread before authoring another
 * intent. A mark clears member progress and overrides older item intent. Newer
 * item intent overrides the default; the latest
 * applicable container watermark wins (season wins a timestamp tie); items
 * added after the watermark appear unwatched.
 */

export type ContainerKind='show'|'season'|'album'|'book'|'artist';
/** The saved flags a title carries besides watched. */
export type ContainerFlag='watchlisted'|'favorite';
export type ContainerState=Readonly<{
 undoToken?:string;undoUntil?:string;
 revision:number;watched:boolean;watermark:string;watchlisted:boolean;favorite:boolean;
 /** Present on GET: the viewer's visible members only, never library-wide summaries. */
 watchedCount?:number;unwatchedCount?:number;
 /** Present on GET: the library the title lives in, so a link with only its id can open its page. */
 libraryId?:string;
}>;
export interface ContainerStateApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

export const containerKinds:readonly ContainerKind[]=Object.freeze(['show','season','album','book','artist'] as const);
/** Which kinds carry a flag: My List (`watchlisted`) is for shows and books; Favorite for shows, albums, books and artists. */
export function containerCarries(kind:string|undefined,flag:ContainerFlag|'watched'):boolean{
 if(flag==='watched')return kind==='show'||kind==='season'||kind==='album'||kind==='book';
 if(flag==='watchlisted')return kind==='show'||kind==='book';
 return kind==='show'||kind==='album'||kind==='book'||kind==='artist';
}
/** Shared error-code correction: a stale revision is HTTP 412 (see `bulk-jobs.ts`). */
export const CONTAINER_REVISION_MISMATCH='revision_mismatch';

const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=256&&!/[\x00-\x1f\x7f]/.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;

function invalid():never{throw new Error(unreadableServerResponse);}

function checkKind(kind:string):ContainerKind{
 if(!containerKinds.includes(kind as ContainerKind))throw new Error('Container state covers shows, seasons, albums, books and artists.');
 return kind as ContainerKind;
}

/**
 * A container's personal state. The revision, the watched default and its watermark are exact:
 * the next change is sent against them. Everything else is only shown: a field this app does not
 * know is ignored, a flag that is missing or unreadable reads as off, a count that cannot be read
 * as 0, and a library id that cannot be read is left out.
 */
function parse(raw:unknown,withCounts:boolean):ContainerState{
 if(!object(raw))invalid();
 if(!count(raw.revision)||typeof raw.watched!=='boolean'||typeof raw.watermark!=='string'||raw.watermark.length>128)invalid();
 if(raw.undoToken!==undefined&&(!id(raw.undoToken)||!id(raw.undoUntil)))invalid();
 const undo=raw.undoToken===undefined?{}:{undoToken:raw.undoToken as string,undoUntil:raw.undoUntil as string};
 const flags={watchlisted:raw.watchlisted===true,favorite:raw.favorite===true};
 const shown=(part:string,v:unknown):number|undefined=>{if(count(v))return v;if(v!==undefined&&v!==null)noteSkipped('container-state',part);return undefined;};
 const watchedCount=shown('watchedCount',raw.watchedCount),unwatchedCount=shown('unwatchedCount',raw.unwatchedCount);
 if(withCounts){
  return Object.freeze({revision:raw.revision as number,watched:raw.watched as boolean,watermark:raw.watermark as string,...flags,...undo,watchedCount:watchedCount??0,unwatchedCount:unwatchedCount??0,...(id(raw.libraryId)?{libraryId:raw.libraryId}:{})});
 }
 return Object.freeze({
  revision:raw.revision as number,watched:raw.watched as boolean,watermark:raw.watermark as string,...flags,...undo,
  ...(watchedCount===undefined?{}:{watchedCount}),
  ...(unwatchedCount===undefined?{}:{unwatchedCount}),
 });
}

/** Reads the revision and the current visible-member detail counts. */
export async function getContainerState(api:ContainerStateApi,kind:ContainerKind,containerId:string,signal?:AbortSignal):Promise<ContainerState>{
 const checked=checkKind(kind);
 if(!id(containerId))throw new Error('A container id is required.');
 return parse(await api.request<RouteResponse<"get_container_personal_state">>(encodedRoutePath("get_container_personal_state", {"kind": checked, "id": encodeURIComponent(containerId)}),'GET',undefined,signal),true);
}

/**
 * Writes the container watched default. On a 412 `revision_mismatch`, reread
 * with `getContainerState` before authoring another intent.
 */
export async function setContainerState(api:ContainerStateApi,kind:ContainerKind,containerId:string,input:Readonly<{expectedRevision:number;watched:boolean}>,signal?:AbortSignal):Promise<ContainerState>{
 const checked=checkKind(kind);
 if(!id(containerId))throw new Error('A container id is required.');
 if(!count(input.expectedRevision)||typeof input.watched!=='boolean')throw new Error('Container watched needs the current revision and the watched value.');
 try{
  return parse(await api.request<RouteResponse<"set_container_personal_state">>(
   encodedRoutePath("set_container_personal_state", {"kind": checked, "id": encodeURIComponent(containerId)}),'PUT',
   routeBody("set_container_personal_state", {expectedRevision:input.expectedRevision,watched:input.watched}),signal),false);
 }catch(e){
  const v=e as {status?:unknown;code?:unknown};
  const code=typeof v?.code==='string'&&v.code?v.code:(v?.status===409||v?.status===412)?CONTAINER_REVISION_MISMATCH:'container_state_not_saved';
  throw Object.assign(new Error(e instanceof Error?e.message:'The watched state could not be saved.'),{code});
 }
}

/** Sets the title's Watchlist or Favorite flag. Plain values: the last write wins, so there is no revision to reread. */
export async function setContainerFlag(api:ContainerStateApi,kind:ContainerKind,containerId:string,flag:ContainerFlag,value:boolean,signal?:AbortSignal):Promise<ContainerState>{
 const checked=checkKind(kind);
 if(!id(containerId))throw new Error('A container id is required.');
 if(!containerCarries(checked,flag)||typeof value!=='boolean')throw new Error('This title does not carry that flag.');
 try{
  return parse(await api.request<RouteResponse<"set_container_personal_state">>(encodedRoutePath("set_container_personal_state", {"kind": checked, "id": encodeURIComponent(containerId)}),'PUT',routeBody("set_container_personal_state", {expectedRevision:0,[flag]:value}),signal),false);
 }catch(e){
  const v=e as {code?:unknown};
  throw Object.assign(new Error(e instanceof Error?e.message:'The change could not be saved.'),{code:typeof v?.code==='string'&&v.code?v.code:'container_state_not_saved'});
 }
}

/** Detail copy for the visible watched/unwatched counts (no list/grid badges). */
export function containerCountLabel(watchedCount:number,unwatchedCount:number):{watched:number;unwatched:number}{
 if(!count(watchedCount)||!count(unwatchedCount))throw new Error('Invalid container counts.');
 return {watched:watchedCount,unwatched:unwatchedCount};
}

/** Restore the precise watched/resume mix captured by a mark within 30 seconds. */
export async function undoContainerState(api:ContainerStateApi,kind:ContainerKind,containerId:string,input:Readonly<{expectedRevision:number;undoToken:string}>,signal?:AbortSignal):Promise<ContainerState>{
 const checked=checkKind(kind);
 if(!id(containerId)||!count(input.expectedRevision)||input.expectedRevision<1||!id(input.undoToken))throw new Error('A recent container mark is required.');
 return parse(await api.request<RouteResponse<"undo_container_personal_state">>(encodedRoutePath("undo_container_personal_state", {"kind": checked, "id": encodeURIComponent(containerId)}),'POST',input,signal),false);
}
