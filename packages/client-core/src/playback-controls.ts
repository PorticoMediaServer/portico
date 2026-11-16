import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {serverPath} from './query/server-api.ts';
/** `durationMs`: the length of what is playing, when the server knows it (never for a channel). */
export type SessionControls=Readonly<{sessionId:string;generation?:number;revision:string;state:string;positionMs:number;durationMs?:number;actions:readonly string[]}>;
/** One of the account's streams, on any of its devices. No `item` when the profile may not see the title (07 rule 38). */
export type RemotePlaybackControls=SessionControls&Readonly<{device:Readonly<{id:string;name:string}>;item?:Readonly<{id:string;title:string}>;kind:string}>;
/** Actual finite own-account policy/count from the same admitted read snapshot,
 * never inferred from the visible (possibly redacted) controls rows. */
export type AccountStreamLimit=Readonly<{scope:'account';limit:number;playing:number}>;
export type RemoteControlsDocument=Readonly<{items:readonly RemotePlaybackControls[];nextCursor:string;streamLimit?:AccountStreamLimit}>;
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f]/.test(v);
function invalid():never{throw new Error(unreadableServerResponse);}
export function parseAccountStreamLimit(value:unknown):AccountStreamLimit{
 if(!obj(value)||value.scope!=='account'||!Number.isSafeInteger(value.limit)||Number(value.limit)<=0||!Number.isSafeInteger(value.playing)||Number(value.playing)<0)invalid();
 return Object.freeze({scope:'account',limit:value.limit as number,playing:value.playing as number});
}
export function parseSessionControls(v:unknown):SessionControls{
 if(!obj(v)||!text(v.sessionId)||!v.sessionId||!text(v.revision,32)||!/^\d+$/.test(v.revision)||!text(v.state,32)||!Number.isSafeInteger(v.positionMs)||Number(v.positionMs)<0||!Array.isArray(v.actions)||v.actions.length>16)invalid();
 if(v.generation!==undefined&&(!Number.isSafeInteger(v.generation)||Number(v.generation)<1||Number(v.generation)>2147483647))invalid();
 const duration=Number.isSafeInteger(v.durationMs)&&Number(v.durationMs)>0?{durationMs:v.durationMs as number}:{};
 return Object.freeze({sessionId:v.sessionId,...(v.generation===undefined?{}:{generation:v.generation as number}),revision:v.revision,state:v.state,positionMs:v.positionMs as number,...duration,actions:Object.freeze(v.actions.filter((a):a is string=>['pause','resume','seek','stop'].includes(String(a))))});
}
/** What `GET /v1/playback/sessions` answers: the account's streams on every device and, when the owner set
 * one, the account's limit. Every listed stream counts against that limit. */
export function parseRemotePlaybackControls(v:unknown):RemoteControlsDocument{
 if(!obj(v)||!Array.isArray(v.items)||v.items.length>500)invalid();
 const items=v.items.map(e=>{
  if(!obj(e)||!text(e.id)||!e.id||!text(e.deviceName,512)||!text(e.kind,32)||!text(e.state,32)||!Number.isSafeInteger(e.positionMs)||Number(e.positionMs)<0)invalid();
  const durationMs=Number.isSafeInteger(e.durationMs)&&Number(e.durationMs)>0?e.durationMs as number:undefined;
  // Any of the account's devices may pause, resume and stop the others; seeking needs a known length.
  const actions=[e.state==='paused'?'resume':'pause',...((e.kind==='vod'||e.kind==='audio')&&durationMs?['seek']:[]),'stop'];
  const item=text(e.itemId)&&e.itemId&&text(e.title,2048)?Object.freeze({id:e.itemId,title:e.title}):undefined;
  return Object.freeze({sessionId:e.id,revision:'0',state:e.state,positionMs:e.positionMs as number,...(durationMs?{durationMs}:{}),actions:Object.freeze(actions),
   device:Object.freeze({id:text(e.deviceId)?e.deviceId:'',name:e.deviceName}),kind:e.kind,...(item?{item}:{})});
 });
 if(v.limit!==undefined&&(!Number.isSafeInteger(v.limit)||Number(v.limit)<=0))invalid();
 return Object.freeze({items:Object.freeze(items),nextCursor:'',...(v.limit===undefined?{}:{streamLimit:Object.freeze({scope:'account' as const,limit:v.limit as number,playing:items.length})})});
}
export function playbackControlsService(request:<T>(path:string,method?:string,body?:unknown)=>Promise<T>){
 const path=(id:string)=>encodedRoutePath("get_admin_session_controls", {"id": encodeURIComponent(id)});
 return Object.freeze({
 /** The whole list in one answer; the paging arguments are kept for callers that page. */
 async remote(_cursor='',_limit=50){return parseRemotePlaybackControls(await request<unknown>(serverPath('GET /v1/playback/sessions')));},
 /** Ends one of the account's streams on whichever device plays it. */
 async stopAccount(selected:Pick<RemotePlaybackControls,'sessionId'>){await request<unknown>(serverPath('DELETE /v1/playback/sessions/{id}',{id:selected.sessionId}),'DELETE',{});},
 async readAdmin(id:string){return parseSessionControls(await request<unknown>(path(id)));},
 async controlAdmin(current:SessionControls,action:'pause'|'resume'|'seek',positionMs?:number){return parseSessionControls(await request<unknown>(path(current.sessionId),'POST',{action,expectedRevision:current.revision,...(positionMs===undefined?{}:{positionMs})}));},
 });
}
