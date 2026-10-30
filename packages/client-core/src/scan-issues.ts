import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
/** Owner-only scan diagnostics. Paths stay scoped to this response and are not
 * added to viewer library status or persistent client caches. */
export type ScanIssue=Readonly<{sequence:number;line:number;path:string;reason:string;category?:'damaged'|'unsupported'|'not-media';messageKey:string;warning:boolean}>;
export type ScanIssues=Readonly<{jobId:string;items:readonly ScanIssue[];nextSequence:number;hasMore:boolean;count?:number}>;
export type InventoryChanges=Readonly<{sourceId:string;items:readonly Readonly<{sequence:number;sourceId:string;jobId:string;objectId:string;assetId:string;revision:string;kind:string;createdAt:string}>[];nextSequence:number;hasMore:boolean;resetRequired:boolean;oldestSequence:number}>;
type Transport={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
const record=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=16384):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
function assert(v:unknown):asserts v {if(!v)throw new Error('Invalid scan diagnostics response.');}
export function parseScanIssues(raw:unknown,jobId:string):ScanIssues{
 assert(record(raw)&&raw.jobId===jobId&&integer(raw.nextSequence)&&typeof raw.hasMore==='boolean'&&Array.isArray(raw.items)&&raw.items.length<=128);
 let previous=0;const items=raw.items.map(v=>{assert(record(v)&&integer(v.sequence)&&v.sequence>previous&&integer(v.line)&&text(v.path)&&text(v.reason,128)&&text(v.messageKey,256)&&v.messageKey.startsWith('scan.issue.')&&typeof v.warning==='boolean');previous=v.sequence;return Object.freeze({sequence:v.sequence,line:v.line,path:v.path,reason:v.reason,...(v.category==='damaged'||v.category==='unsupported'||v.category==='not-media'?{category:v.category}:{}),messageKey:v.messageKey,warning:v.warning});});
 assert(!items.length||raw.nextSequence===previous);
 return Object.freeze({jobId,items:Object.freeze(items),nextSequence:raw.nextSequence,hasMore:raw.hasMore,...(integer(raw.count)?{count:raw.count}:{})});
}
export async function readScanIssues(api:Transport,libraryId:string,jobId:string,after=0,signal?:AbortSignal):Promise<ScanIssues>{
 assert(text(libraryId,256)&&libraryId&&text(jobId,256)&&jobId&&integer(after));
 const raw=await api.request<RouteResponse<"get_scan_issues">>(encodedRoutePath("get_scan_issues", {"id": encodeURIComponent(libraryId), "job": encodeURIComponent(jobId)}) + "?after=" + (after) + "&limit=128",'GET',undefined,signal);
 return parseScanIssues(raw,jobId);
}
export function parseInventoryChanges(raw:unknown,sourceId:string):InventoryChanges{
 assert(record(raw)&&raw.sourceId===sourceId&&integer(raw.nextSequence)&&integer(raw.oldestSequence)&&typeof raw.hasMore==='boolean'&&typeof raw.resetRequired==='boolean'&&Array.isArray(raw.items)&&raw.items.length<=128);
 let previous=0;const items=raw.items.map(v=>{assert(record(v)&&integer(v.sequence)&&v.sequence>previous&&v.sourceId===sourceId&&text(v.jobId,256)&&text(v.objectId,256)&&text(v.assetId,256)&&text(v.revision,512)&&text(v.kind,128)&&text(v.createdAt,128));previous=v.sequence;return Object.freeze({sequence:v.sequence,sourceId,jobId:v.jobId,objectId:v.objectId,assetId:v.assetId,revision:v.revision,kind:v.kind,createdAt:v.createdAt});});
 return Object.freeze({sourceId,items:Object.freeze(items),nextSequence:raw.nextSequence,oldestSequence:raw.oldestSequence,hasMore:raw.hasMore,resetRequired:raw.resetRequired});
}
export async function readInventoryChanges(api:Transport,libraryId:string,sourceId:string,after=0,signal?:AbortSignal):Promise<InventoryChanges>{
 assert(text(libraryId,256)&&libraryId&&text(sourceId,256)&&sourceId&&integer(after));
 return parseInventoryChanges(await api.request<RouteResponse<"get_inventory_changes">>(encodedRoutePath("get_inventory_changes", {"id": encodeURIComponent(libraryId), "source": encodeURIComponent(sourceId)}) + "?after=" + (after),'GET',undefined,signal),sourceId);
}
