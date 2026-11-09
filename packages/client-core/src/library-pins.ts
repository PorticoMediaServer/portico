import {routePath,encodedRoutePath} from '../../contracts/src/route-paths.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable} from './tolerant.ts';
/** Pinned libraries and pinned-resource order. The arrangement is server state:
 * clients read it, send a replacement with the revision they saw, and re-read.
 * Nothing here re-orders locally or remembers a pin the server did not confirm. */
export type PinnedResourceKind='collection'|'view'|'playlist';
export type PinOrderEntry=Readonly<{kind:PinnedResourceKind;id:string}>;
/** A pinned collection, saved view or playlist as navigation shows it: its name, and the pin
 * revision an unpin sends back. The read lists every pin the viewer can still open, arranged ones
 * first; a save answers with the arrangement alone (no names), so a client reads again after it. */
export type PinnedResource=Readonly<PinOrderEntry&{name:string;pinRevision:number}>;
export type PinOrder=Readonly<{serverId:string;viewerFence:string;revision:number;order:readonly PinnedResource[]}>;
export type LibraryNavigationPins=Readonly<{pinnedLibraryIds:readonly string[];revision:number}>;
export type PinnedLibrary=Readonly<{id:string;name:string;kind:string;defaultView:string;pinned:boolean}>;

const obj=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const str=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>str(v,256)&&v.length>0&&!/[\r\n]/.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const kinds:readonly string[]=['collection','view','playlist'];
function bad(message=unreadableServerResponse):never{throw Object.assign(new Error(message),{code:'invalid_pins'});}
function rows(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)bad();return v;}
function freeze<T>(v:T):T{if(v&&typeof v==='object'){for(const child of Object.values(v))freeze(child);Object.freeze(v);}return v;}

export function parsePinOrder(raw:unknown,serverId:string):PinOrder{
 if(!obj(raw)||raw.serverId!==serverId||!id(raw.viewerFence)||!count(raw.revision))bad();
 const order=rows(raw.order,200).map(v=>{
  if(!obj(v)||!id(v.id)||!str(v.kind,32)||!kinds.includes(v.kind))bad();
  if(v.name!==undefined&&!str(v.name,512)||v.pinRevision!==undefined&&!count(v.pinRevision))bad();
  return {kind:v.kind as PinnedResourceKind,id:v.id,name:typeof v.name==='string'?v.name:'',pinRevision:typeof v.pinRevision==='number'?v.pinRevision:0};
 });
 if(new Set(order.map(entry=>entry.kind+':'+entry.id)).size!==order.length)bad();
 return freeze({serverId,viewerFence:raw.viewerFence,revision:raw.revision,order});
}

export function parseLibraryNavigationPins(raw:unknown):LibraryNavigationPins{
 if(!obj(raw)||!count(raw.revision))bad();
 const pinnedLibraryIds=rows(raw.pinnedLibraryIds,100).map(v=>{if(!id(v))bad();return v;});
 if(new Set(pinnedLibraryIds).size!==pinnedLibraryIds.length)bad();
 return freeze({pinnedLibraryIds,revision:raw.revision});
}

/** The listing is already in the viewer's order; a client that re-sorts it will
 * disagree with every other surface, so this only checks that it is. A library this app cannot
 * read is left out alone and a repeated one keeps its first row. The two arrangements above are
 * sent back whole when the viewer changes them, so they stay exact. */
export function parsePinnedLibraries(raw:unknown):readonly PinnedLibrary[]{
 if(!obj(raw))bad();
 const items=firstOfEach('library-pins','items',keepReadable('library-pins','items',rows(raw.items,2048),v=>{
  if(!obj(v)||!id(v.id)||!str(v.name,512)||!id(v.kind)||!str(v.defaultView,64)||typeof v.pinned!=='boolean')bad();
  return {id:v.id,name:v.name,kind:v.kind,defaultView:v.defaultView,pinned:v.pinned};
 }),item=>item.id);
 if(items.some((item,index)=>item.pinned&&index>0&&!items[index-1].pinned))bad();
 return freeze(items);
}

/** Builds the PUT body for a replacement arrangement. */
export function pinOrderRequest(expectedRevision:number,order:readonly PinOrderEntry[]):Readonly<{expectedRevision:number;order:readonly PinOrderEntry[]}>{
 if(!count(expectedRevision)||order.length>200)bad('That pin arrangement cannot be saved.');
 const seen=new Set<string>();
 for(const entry of order){
  if(!kinds.includes(entry.kind)||!id(entry.id)||seen.has(entry.kind+':'+entry.id))bad('That pin arrangement cannot be saved.');
  seen.add(entry.kind+':'+entry.id);
 }
 return freeze({expectedRevision,order:[...order]});
}

export function libraryNavigationRequest(expectedRevision:number,pinnedLibraryIds:readonly string[]):Readonly<{expectedRevision:number;pinnedLibraryIds:readonly string[]}>{
 if(!count(expectedRevision)||pinnedLibraryIds.length>100||new Set(pinnedLibraryIds).size!==pinnedLibraryIds.length||pinnedLibraryIds.some(entry=>!id(entry)))bad('Those library pins cannot be saved.');
 return freeze({expectedRevision,pinnedLibraryIds:[...pinnedLibraryIds]});
}

export interface PinsApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

/** The viewer's pinned collections, saved views and playlists, in their arrangement. */
export async function readPinOrder(api:PinsApi,serverId:string,signal?:AbortSignal):Promise<PinOrder>{
 return parsePinOrder(await api.request<unknown>(routePath("get_saved_pins_order"),'GET',undefined,signal),serverId);
}
/** Saves a new arrangement of the pins; the caller reads the pins again for their names. */
export async function savePinOrder(api:PinsApi,serverId:string,expectedRevision:number,order:readonly PinOrderEntry[]):Promise<PinOrder>{
 return parsePinOrder(await api.request<unknown>(routePath("put_saved_pins_order"),'PUT',pinOrderRequest(expectedRevision,order.map(entry=>({kind:entry.kind,id:entry.id})))),serverId);
}
/** Pins or unpins one collection, saved view or playlist. `pinRevision` is the one last read. */
export async function setResourcePin(api:PinsApi,kind:PinnedResourceKind,resourceId:string,pinned:boolean,pinRevision:number,operationId:string):Promise<Readonly<{pinned:boolean;pinRevision:number}>>{
 if(!kinds.includes(kind)||!id(resourceId)||!count(pinRevision)||!/^[A-Za-z0-9_-]{1,128}$/.test(operationId))bad('That pin cannot be saved.');
 const raw=await api.request<unknown>(encodedRoutePath("put_saved_pins_kind_id",{"kind":kind,"id":encodeURIComponent(resourceId)}),'PUT',{operationId,expectedRevision:pinRevision,pinned});
 if(!obj(raw)||typeof raw.pinned!=='boolean'||!count(raw.pinRevision))bad();
 return freeze({pinned:raw.pinned,pinRevision:raw.pinRevision});
}
export async function readLibraryNavigation(api:PinsApi,signal?:AbortSignal):Promise<LibraryNavigationPins>{
 return parseLibraryNavigationPins(await api.request<unknown>(routePath("get_me_library_navigation"),'GET',undefined,signal));
}
export async function saveLibraryNavigation(api:PinsApi,expectedRevision:number,pinnedLibraryIds:readonly string[]):Promise<LibraryNavigationPins>{
 return parseLibraryNavigationPins(await api.request<unknown>(routePath("put_me_library_navigation"),'PUT',libraryNavigationRequest(expectedRevision,pinnedLibraryIds)));
}

/** The pinned libraries after pinning (at the end) or unpinning one. */
export function withLibraryPinned(pinned:readonly string[],libraryId:string,pin:boolean):readonly string[]{
 const rest=pinned.filter(entry=>entry!==libraryId);
 return freeze(pin?[...rest,libraryId]:rest);
}
/** An arrangement with one entry moved by `by` places (clamped); unchanged when it cannot move. */
export function movedPin<T>(order:readonly T[],index:number,by:number):readonly T[]{
 const to=Math.max(0,Math.min(order.length-1,index+by));
 if(index<0||index>=order.length||to===index)return order;
 const next=[...order];
 const [entry]=next.splice(index,1);
 next.splice(to,0,entry!);
 return Object.freeze(next);
}
/** Run a pin change, and when another device changed the pins meanwhile, read them again and
 * apply the same change once more (a person never sees that conflict unless their pin really
 * changed). `apply` receives the fresh state and returns the request to send. */
export async function withPinRetry<S,R>(read:()=>Promise<S>,apply:(state:S)=>Promise<R>,state?:S):Promise<R>{
 try{return await apply(state??await read());}
 catch(error){
  const code=(error as {code?:unknown}|null)?.code;
  if(code!=='pin_order_conflict'&&code!=='revision_mismatch'&&code!=='revision_conflict'&&code!=='conflict'&&(error as {status?:unknown}|null)?.status!==409)throw error;
  return apply(await read());
 }
}
