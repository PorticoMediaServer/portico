import {callServer} from './query/server-api.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {firstOfEach,keepReadable} from './tolerant.ts';
/** All four pin kinds live in the profile's `pins` preference, in order, at most 200 together.
 * Saves change that preference only. Compatibility revision fields are zero: preferences keep
 * the later save, without the retired routes' revision exchange. */
export type PinnedResourceKind='collection'|'view'|'playlist';
export type PinOrderEntry=Readonly<{kind:PinnedResourceKind;id:string}>;
/** A pinned collection, saved view or playlist as navigation shows it, named by a list read
 * the current viewer may make. A save answers with the arrangement alone (no names), so an
 * editor reads again after it. pinRevision remains zero for the existing editor shape. */
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
 const pinnedLibraryIds=rows(raw.pinnedLibraryIds,200).map(v=>{if(!id(v))bad();return v;});
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

/** Checks the arrangement supplied by the existing pin editors. */
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
 if(!count(expectedRevision)||pinnedLibraryIds.length>200||new Set(pinnedLibraryIds).size!==pinnedLibraryIds.length||pinnedLibraryIds.some(entry=>!id(entry)))bad('Those library pins cannot be saved.');
 return freeze({expectedRevision,pinnedLibraryIds:[...pinnedLibraryIds]});
}

export interface PinsApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

/** Pins have profile-server scope only, so the device class does not change their value. */
function pinsOf(raw:unknown):readonly string[]{
 if(!obj(raw)||!obj(raw.effective))bad();
 const pins=rows(raw.effective.pins,200);
 if(!pins.every(pin=>id(pin)&&pin.length<=128)||new Set(pins).size!==pins.length)bad();
 return Object.freeze([...pins] as string[]);
}
const readPins=async(api:PinsApi,signal?:AbortSignal)=>pinsOf(await callServer(api,'GET /v1/preferences',{query:{deviceClass:'web'},signal}));
async function savePins(api:PinsApi,pins:readonly string[]):Promise<readonly string[]>{
 if(pins.length>200)bad('That pin arrangement cannot be saved.');
 return pinsOf(await callServer(api,'PATCH /v1/preferences',{body:{scope:'profile-server',deviceClass:'web',values:{pins:[...pins]}}}));
}
function resourceEntries(pins:readonly string[]):PinOrderEntry[]{
 return pins.flatMap(pin=>{
  const colon=pin.indexOf(':'),kind=pin.slice(0,colon),resourceId=pin.slice(colon+1);
  return kinds.includes(kind)&&id(resourceId)?[{kind:kind as PinnedResourceKind,id:resourceId}]:[];
 });
}
/** Replace one part of the arrangement without shifting the other kinds out of their slots. */
function replacePins(pins:readonly string[],matches:(pin:string)=>boolean,next:readonly string[]):readonly string[]{
 let at=0;
 const out=pins.flatMap(pin=>matches(pin)?at<next.length?[next[at++]!]:[]:[pin]);
 return [...out,...next.slice(at)];
}
const savedPin=(pin:string)=>kinds.some(kind=>pin.startsWith(kind+':'));

/** Resolve each pinned id directly: a pin never disappears just because its list is beyond page one.
 * Deleted or no-longer-visible lists are absent; a failed read still fails the read, never an unpin. */
export async function readPinOrder(api:PinsApi,serverId:string,signal?:AbortSignal):Promise<PinOrder>{
 const entries=resourceEntries(await readPins(api,signal));
 const listed=await Promise.all(entries.map(async entry=>{
  let list:unknown;
  try{list=await callServer(api,'GET /v1/lists/{id}',{id:entry.id,signal});}
  catch(error){if((error as {status?:unknown}|null)?.status===404)return undefined;throw error;}
  return keepReadable('library-pins','lists',[list],row=>{
   if(!obj(row)||row.id!==entry.id||row.kind!==(entry.kind==='view'?'saved_view':entry.kind)||!str(row.name,512))bad();
   return {...entry,name:row.name,pinRevision:0};
  })[0];
 }));
 return freeze({serverId,viewerFence:'viewer',revision:0,order:listed.filter((entry):entry is PinnedResource=>entry!==undefined)});
}
/** Saves an arrangement, leaving pins not named here (including unavailable lists) pinned. */
export async function savePinOrder(api:PinsApi,serverId:string,expectedRevision:number,order:readonly PinOrderEntry[]):Promise<PinOrder>{
 const checked=pinOrderRequest(expectedRevision,order),pins=await readPins(api);
 const next=checked.order.map(entry=>entry.kind+':'+entry.id);
 if(next.some(pin=>!pins.includes(pin)))bad('That pin arrangement cannot be saved.');
 const saved=await savePins(api,replacePins(pins,savedPin,[...next,...pins.filter(pin=>savedPin(pin)&&!next.includes(pin))]));
 return parsePinOrder({serverId,viewerFence:'viewer',revision:0,order:resourceEntries(saved)},serverId);
}
/** Pins or unpins one list in the profile's preference. Existing editor revision/id arguments
 * remain accepted; the preference write has neither a resource revision nor an operation id. */
export async function setResourcePin(api:PinsApi,kind:PinnedResourceKind,resourceId:string,pinned:boolean,pinRevision:number,operationId:string):Promise<Readonly<{pinned:boolean;pinRevision:number}>>{
 if(!kinds.includes(kind)||!id(resourceId)||!count(pinRevision)||!/^[A-Za-z0-9_-]{1,128}$/.test(operationId))bad('That pin cannot be saved.');
 const pin=kind+':'+resourceId,pins=await readPins(api);
 const next=pinned?pins.includes(pin)?pins:[...pins,pin]:pins.filter(value=>value!==pin);
 const saved=await savePins(api,next);
 return freeze({pinned:saved.includes(pin),pinRevision:0});
}
export async function readLibraryNavigation(api:PinsApi,signal?:AbortSignal):Promise<LibraryNavigationPins>{
 const pins=await readPins(api,signal);
 return freeze({pinnedLibraryIds:pins.filter(pin=>pin.startsWith('library:')).map(pin=>pin.slice(8)),revision:0});
}
export async function saveLibraryNavigation(api:PinsApi,expectedRevision:number,pinnedLibraryIds:readonly string[]):Promise<LibraryNavigationPins>{
 const checked=libraryNavigationRequest(expectedRevision,pinnedLibraryIds),pins=await readPins(api);
 const saved=await savePins(api,replacePins(pins,pin=>pin.startsWith('library:'),checked.pinnedLibraryIds.map(id=>'library:'+id)));
 return parseLibraryNavigationPins({pinnedLibraryIds:saved.filter(pin=>pin.startsWith('library:')).map(pin=>pin.slice(8)),revision:0});
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
