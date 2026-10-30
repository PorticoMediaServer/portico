import {routePath} from "../../contracts/src/route-paths.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {keepReadable} from './tolerant.ts';
/** `item`: the title the file belongs to (its id, title and kind), absent once the file has left the catalogue. */
export type PlaybackFailure = Readonly<{sourceId:string;engine:'native'|'mpv';route:'original'|'copy_remux'|'audio_conversion'|'video_conversion';code:string;detail:string;reportedAtMs:number;expiresAtMs:number;item?:Readonly<{id:string;title:string;kind:string}>}>;
export type PlaybackFailureMemory = Readonly<{items:readonly PlaybackFailure[];more:boolean}>;
type Api = Readonly<{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}>;
const obj=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,n:number):v is string=>typeof v==='string'&&v.length<=n&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const stamp=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
export function parsePlaybackFailureMemory(raw:unknown):PlaybackFailureMemory {
 if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>100||typeof raw.more!=='boolean')throw new Error(unreadableServerResponse);
 const items=keepReadable('playback-failure-memory','items',raw.items,(v):PlaybackFailure=>{
  if(!obj(v)||!text(v.sourceId,128)||!/^[A-Za-z0-9_-]+$/.test(v.sourceId)||!['native','mpv'].includes(v.engine as string)||!['original','copy_remux','audio_conversion','video_conversion'].includes(v.route as string)||!text(v.code,48)||!text(v.detail,512)||!stamp(v.reportedAtMs)||!stamp(v.expiresAtMs))throw new Error(unreadableServerResponse);
  // The title is a nicety: a malformed one leaves the row as it was rather than dropping it.
  const item=text(v.itemId,128)&&v.itemId&&text(v.title,2048)?Object.freeze({id:v.itemId,title:v.title,kind:text(v.kind,32)?v.kind:''}):undefined;
  return Object.freeze({sourceId:v.sourceId,engine:v.engine as PlaybackFailure['engine'],route:v.route as PlaybackFailure['route'],code:v.code,detail:v.detail,reportedAtMs:v.reportedAtMs,expiresAtMs:v.expiresAtMs,...(item?{item}:{})});
 });
 return Object.freeze({items:Object.freeze(items),more:raw.more});
}
export async function readPlaybackFailureMemory(api:Api,signal?:AbortSignal):Promise<PlaybackFailureMemory>{return parsePlaybackFailureMemory(await api.request<RouteResponse<'list_playback_failure_memory'>>(routePath('list_playback_failure_memory'),'GET',undefined,signal));}
export async function clearPlaybackFailureMemory(api:Api,sourceId:string,signal?:AbortSignal):Promise<void>{if(!/^[A-Za-z0-9_-]{1,128}$/.test(sourceId))throw new Error(unreadableServerResponse);await api.request(routePath('clear_playback_failure_memory',{sourceId}),'DELETE',undefined,signal);}

/** One file's remembered failures: every way of playing it that is being avoided, newest first, and when the last of them is forgotten. */
export type PlaybackFailureGroup = Readonly<{sourceId:string;failures:readonly PlaybackFailure[];reportedAtMs:number;expiresAtMs:number;item?:PlaybackFailure['item']}>;
/** The failures file by file (one Forget covers a file), the most recently reported file first. */
export function groupPlaybackFailures(items:readonly PlaybackFailure[]):readonly PlaybackFailureGroup[] {
 const by=new Map<string,PlaybackFailure[]>();
 for(const f of items){const list=by.get(f.sourceId);if(list)list.push(f);else by.set(f.sourceId,[f]);}
 return [...by.entries()].map(([sourceId,list])=>{const failures=[...list].sort((a,b)=>b.reportedAtMs-a.reportedAtMs);const item=failures.find(f=>f.item)?.item;return Object.freeze({sourceId,failures:Object.freeze(failures),reportedAtMs:failures[0]!.reportedAtMs,expiresAtMs:Math.max(...failures.map(f=>f.expiresAtMs)),...(item?{item}:{})});}).sort((a,b)=>b.reportedAtMs-a.reportedAtMs);
}
