import {callServer} from './query/server-api.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {keepReadable} from './tolerant.ts';
/**
 * Remembered playback problems on the rebuilt server (console rule 72): `GET /v1/admin/playback/failures`
 * lists each way of playing a file that failed and is avoided until it expires, and
 * `DELETE /v1/admin/playback/failures/{id}` forgets one. `sourceId` is that failure's own id, so each one is
 * a row with its own Forget, naming the device it failed on (07-playback rule 75). `item`: the title the file belongs
 * to, absent once the file has left the library.
 */
export type PlaybackFailure = Readonly<{sourceId:string;device:string;engine:'native'|'mpv';route:'original'|'repackaged'|'converted';code:string;detail:string;reportedAtMs:number;expiresAtMs:number;item?:Readonly<{id:string;title:string}>}>;
export type PlaybackFailureMemory = Readonly<{items:readonly PlaybackFailure[];more:boolean}>;
type Api = Readonly<{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}>;
const obj=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,n:number):v is string=>typeof v==='string'&&v.length<=n&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const time=(v:unknown):number|undefined=>typeof v==='string'&&Number.isFinite(Date.parse(v))?Date.parse(v):undefined;
const id=(v:unknown):v is string=>text(v,128)&&/^[A-Za-z0-9_-]+$/.test(v);
export function parsePlaybackFailureMemory(raw:unknown):PlaybackFailureMemory {
 if(!obj(raw)||!Array.isArray(raw.items)||raw.nextCursor!==undefined&&typeof raw.nextCursor!=='string')throw new Error(unreadableServerResponse);
 const items=keepReadable('playback-failure-memory','items',raw.items,(v):PlaybackFailure=>{
  const failedAt=obj(v)?time(v.failedAt):undefined,expiresAt=obj(v)?time(v.expiresAt):undefined;
  if(!obj(v)||!id(v.id)||!text(v.deviceName,256)||!['native','mpv'].includes(v.player as string)||!['original','repackaged','converted'].includes(v.route as string)||!text(v.code,48)||v.detail!==undefined&&!text(v.detail,512)||failedAt===undefined||expiresAt===undefined)throw new Error(unreadableServerResponse);
  // The title is a nicety: a malformed one leaves the row as it was rather than dropping it.
  const item=text(v.itemId,128)&&v.itemId&&text(v.title,2048)&&v.title?Object.freeze({id:v.itemId,title:v.title}):undefined;
  return Object.freeze({sourceId:v.id,device:v.deviceName as string,engine:v.player as PlaybackFailure['engine'],route:v.route as PlaybackFailure['route'],code:v.code,detail:(v.detail as string|undefined)??'',reportedAtMs:failedAt,expiresAtMs:expiresAt,...(item?{item}:{})});
 });
 return Object.freeze({items:Object.freeze(items),more:!!raw.nextCursor});
}
export async function readPlaybackFailureMemory(api:Api,signal?:AbortSignal):Promise<PlaybackFailureMemory>{return parsePlaybackFailureMemory(await callServer(api,'GET /v1/admin/playback/failures',{signal}));}
export async function clearPlaybackFailureMemory(api:Api,sourceId:string,signal?:AbortSignal):Promise<void>{if(!id(sourceId))throw new Error(unreadableServerResponse);await callServer(api,'DELETE /v1/admin/playback/failures/{id}',{id:sourceId,signal});}

/** One file's remembered failures: every way of playing it that is being avoided, newest first, and when the last of them is forgotten. */
export type PlaybackFailureGroup = Readonly<{sourceId:string;failures:readonly PlaybackFailure[];reportedAtMs:number;expiresAtMs:number;item?:PlaybackFailure['item']}>;
/** The failures file by file (one Forget covers a file), the most recently reported file first. */
export function groupPlaybackFailures(items:readonly PlaybackFailure[]):readonly PlaybackFailureGroup[] {
 const by=new Map<string,PlaybackFailure[]>();
 for(const f of items){const list=by.get(f.sourceId);if(list)list.push(f);else by.set(f.sourceId,[f]);}
 return [...by.entries()].map(([sourceId,list])=>{const failures=[...list].sort((a,b)=>b.reportedAtMs-a.reportedAtMs);const item=failures.find(f=>f.item)?.item;return Object.freeze({sourceId,failures:Object.freeze(failures),reportedAtMs:failures[0]!.reportedAtMs,expiresAtMs:Math.max(...failures.map(f=>f.expiresAtMs)),...(item?{item}:{})});}).sort((a,b)=>b.reportedAtMs-a.reportedAtMs);
}
