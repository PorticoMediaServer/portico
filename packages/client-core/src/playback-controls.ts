import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {parseEntry, type QueueEntry} from './playback-v1/types.ts';
import {unreadableServerResponse} from './server-messages.ts';
/** `durationMs`: the length of what is playing, when the server knows it (never for a channel). */
export type SessionControls=Readonly<{sessionId:string;generation?:number;revision:string;state:string;positionMs:number;durationMs?:number;actions:readonly string[]}>;
export type RemotePlaybackControls=SessionControls&Readonly<{generation:number;device:Readonly<{id:string;name:string}>;item?:Readonly<{id:string;title:string}>;kind:string;next?:QueueEntry}>;
/** Actual finite own-account policy/count from the same admitted read snapshot,
 * never inferred from the visible (possibly redacted) controls rows. */
export type AccountStreamLimit=Readonly<{scope:'account';limit:number;playing:number}>;
export type RemoteControlsDocument=Readonly<{items:readonly RemotePlaybackControls[];nextCursor:string;streamLimit?:AccountStreamLimit}>;
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f]/.test(v);
function invalid():never{throw new Error(unreadableServerResponse);}
export function parseAccountStreamLimit(value:unknown):AccountStreamLimit{
 if(!obj(value)||Object.keys(value).length!==3||Object.keys(value).some(key=>!['scope','limit','playing'].includes(key))||value.scope!=='account'||!Number.isSafeInteger(value.limit)||Number(value.limit)<=0||!Number.isSafeInteger(value.playing)||Number(value.playing)<0)invalid();
 return Object.freeze({scope:'account',limit:value.limit as number,playing:value.playing as number});
}
export function parseSessionControls(v:unknown):SessionControls{
 if(!obj(v)||!text(v.sessionId)||!v.sessionId||!text(v.revision,32)||!/^\d+$/.test(v.revision)||!text(v.state,32)||!Number.isSafeInteger(v.positionMs)||Number(v.positionMs)<0||!Array.isArray(v.actions)||v.actions.length>16)invalid();
 if(v.generation!==undefined&&(!Number.isSafeInteger(v.generation)||Number(v.generation)<1||Number(v.generation)>2147483647))invalid();
 const duration=Number.isSafeInteger(v.durationMs)&&Number(v.durationMs)>0?{durationMs:v.durationMs as number}:{};
 return Object.freeze({sessionId:v.sessionId,...(v.generation===undefined?{}:{generation:v.generation as number}),revision:v.revision,state:v.state,positionMs:v.positionMs as number,...duration,actions:Object.freeze(v.actions.filter((a):a is string=>['pause','resume','seek','stop'].includes(String(a))))});
}
export function parseRemotePlaybackControls(v:unknown):RemoteControlsDocument{
 if(!obj(v)||!Array.isArray(v.items)||v.items.length>100||!text(v.nextCursor))invalid();
 const items=v.items.map(e=>{const c=parseSessionControls(e);if(c.generation===undefined)invalid();if(!obj(e)||!obj(e.device)||!text(e.device.id)||!text(e.device.name,512)||!text(e.kind,32))invalid();let item:Readonly<{id:string;title:string}>|undefined;if(e.item!==undefined){if(!obj(e.item)||!text(e.item.id)||!text(e.item.title,2048))invalid();item=Object.freeze({id:e.item.id,title:e.item.title});}return Object.freeze({...c,generation:c.generation,device:Object.freeze({id:e.device.id,name:e.device.name}),kind:e.kind,...(e.next!==undefined?{next:parseEntry(e.next)}:{}),...(item?{item}:{})});});
 return Object.freeze({items:Object.freeze(items),nextCursor:v.nextCursor,...(v.streamLimit===undefined?{}:{streamLimit:parseAccountStreamLimit(v.streamLimit)})});
}
export function playbackControlsService(request:<T>(path:string,method?:string,body?:unknown)=>Promise<T>){
 const path=(id:string)=>encodedRoutePath("get_admin_session_controls", {"id": encodeURIComponent(id)});
 return Object.freeze({
 async remote(cursor='',limit=50){return parseRemotePlaybackControls(await request<unknown>(routePath("get_remote_playback_controls") + "?cursor=" + (encodeURIComponent(cursor)) + "&limit=" + (limit)));},
 async stopAccount(selected:Pick<RemotePlaybackControls,'sessionId'|'generation'>){const {sessionId,generation}=selected;if(!Number.isSafeInteger(generation)||generation<1||generation>2147483647)invalid();await request<unknown>(encodedRoutePath("stop_account_playback",{id:encodeURIComponent(sessionId)}),'POST',{generation});},
 async readAdmin(id:string){return parseSessionControls(await request<unknown>(path(id)));},
 async controlAdmin(current:SessionControls,action:'pause'|'resume'|'seek',positionMs?:number){return parseSessionControls(await request<unknown>(path(current.sessionId),'POST',{action,expectedRevision:current.revision,...(positionMs===undefined?{}:{positionMs})}));},
 });
}
